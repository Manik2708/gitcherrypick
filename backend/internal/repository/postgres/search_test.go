package postgres_test

import (
	"context"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestSearchRankingIsGlobal(t *testing.T) {
	t.Run("a hidden contributor leaves a GAP in the sequence", func(t *testing.T) {
		// The property the whole design turns on (ADR-0008 §1a). Rank is
		// computed over the unfiltered population and the view filters it, so
		// a list can legitimately start at 2.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Skills: []string{"postgres"}})

		if got.Total != 1 {
			t.Fatalf("expected 1 visible result, got %d", got.Total)
		}
		if got.InactiveHidden != 1 {
			t.Errorf("expected 1 hidden, got %d — the gap must be explained", got.InactiveHidden)
		}
		if got.Results[0].Rank != 2 {
			t.Errorf("expected the list to start at rank 2 with Carol hidden, got %d", got.Results[0].Rank)
		}
		if got.Results[0].DisplayName != "Bob Nakamura" {
			t.Errorf("expected Bob, got %q", got.Results[0].DisplayName)
		}
	})

	t.Run("the toggle returns her AT HER TRUE RANK", func(t *testing.T) {
		// The ranking was never recomputed, only filtered.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"postgres"}, IncludeInactive: true})

		if got.Total != 2 {
			t.Fatalf("expected 2 results, got %d", got.Total)
		}
		if got.InactiveHidden != 0 {
			t.Errorf("expected nothing hidden, got %d", got.InactiveHidden)
		}
		if got.Results[0].DisplayName != "Carol Diaz" || got.Results[0].Rank != 1 {
			t.Errorf("expected Carol at rank 1, got %q at %d",
				got.Results[0].DisplayName, got.Results[0].Rank)
		}
		if got.Results[0].Active {
			t.Error("Carol is lapsed and must be reported inactive")
		}
	})

	t.Run("an inactive row carries both staleness numbers", func(t *testing.T) {
		// They answer different questions and differ by the 15-day window.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"postgres"}, IncludeInactive: true})

		carol := got.Results[0]
		if carol.LastConfirmedAt == nil {
			t.Error("expected last_confirmed_at")
		}
		if carol.InactiveForDays == nil {
			t.Fatal("expected inactive_for_days")
		}
		if *carol.InactiveForDays < 0 {
			t.Errorf("expected a non-negative day count, got %d", *carol.InactiveForDays)
		}
	})
}

func TestSearchGates(t *testing.T) {
	t.Run("only PRIMARY standing matches a skill filter", func(t *testing.T) {
		// Dave's Go scores 81 — the highest on the platform — and is
		// unsearchable because it is secondary. Score and standing are
		// orthogonal.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"go"}, MinSkillScore: f(80)})

		if got.Total != 0 {
			t.Errorf("a secondary skill matched a skill filter: %+v", got.Results)
		}
		if got.InactiveHidden != 0 {
			t.Error("standing is a permanent gate, not a hidden-by-default filter")
		}
	})

	t.Run("the toggle never reveals a secondary skill", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{
			Skills: []string{"go"}, MinSkillScore: f(80), IncludeInactive: true})
		if got.Total != 0 {
			t.Errorf("include_inactive revealed a secondary skill: %+v", got.Results)
		}
	})

	t.Run("AssertNotSelf removes the caller and nobody else", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		// dave_hiring shares a GitHub identity with contributor Dave.
		asSelf := mustSearch(ctx, t, db, p.daveHiring, domain.SearchQuery{Skills: []string{"kubernetes"}})
		for _, res := range asSelf.Results {
			if res.DisplayName == "Dave Whitfield" {
				t.Error("a hirer saw their own contributor account")
			}
		}
		if asSelf.InactiveHidden != 0 {
			t.Error("self-exclusion must never be counted or explained")
		}

		asOther := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Skills: []string{"kubernetes"}})
		if len(asOther.Results) <= len(asSelf.Results) {
			t.Error("the exclusion must be identity-scoped, not a general hide")
		}
	})

	t.Run("not_looking is excluded and no toggle reveals it", func(t *testing.T) {
		// An explicit opt-out, unlike a lapse. It leaves the ranked population
		// entirely, so no board carries a gap nothing could fill.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		if _, err := db.Users().SetAvailability(ctx, p.bob, domain.NotLooking); err != nil {
			t.Fatalf("opting out: %v", err)
		}

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"postgres"}, IncludeInactive: true})
		for _, res := range got.Results {
			if res.DisplayName == "Bob Nakamura" {
				t.Error("include_inactive revealed an opted-out contributor")
			}
		}
	})

	t.Run("multiple skills means AND", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"go", "kubernetes"}})

		if got.Total != 1 {
			t.Fatalf("expected only the contributor holding both, got %d", got.Total)
		}
		if got.Results[0].DisplayName != "Bob Nakamura" {
			t.Errorf("expected Bob, got %q", got.Results[0].DisplayName)
		}
	})
}

func TestSearchOrdering(t *testing.T) {
	t.Run("names the ordering it used", func(t *testing.T) {
		// A rank is meaningless without saying rank in WHAT (ADR-0008 §1).
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		for _, tt := range []struct {
			name  string
			query domain.SearchQuery
			want  domain.RankedBy
		}{
			{"one skill", domain.SearchQuery{Skills: []string{"go"}}, domain.RankedBySkill("go")},
			{"two skills", domain.SearchQuery{Skills: []string{"go", "kubernetes"}}, domain.RankedByOverall},
			{"generalist", domain.SearchQuery{MinGeneralistScore: f(1)}, domain.RankedByGeneralist},
			{"nothing", domain.SearchQuery{}, domain.RankedByOverall},
		} {
			got := mustSearch(ctx, t, db, p.hirer, tt.query)
			if got.RankedBy != tt.want {
				t.Errorf("%s: expected ranked_by %q, got %q", tt.name, tt.want, got.RankedBy)
			}
		}
	})

	t.Run("a multi-skill result ranks by overall, not by either skill", func(t *testing.T) {
		// Bob is 2nd in both skill boards and 3rd overall. The response says
		// which order produced the number.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer,
			domain.SearchQuery{Skills: []string{"go", "kubernetes"}})
		if got.RankedBy != domain.RankedByOverall {
			t.Fatalf("expected overall ordering, got %q", got.RankedBy)
		}
		if got.Results[0].Rank == 2 {
			t.Error("a multi-skill rank must not be a skill rank in disguise")
		}
	})
}

func TestSearchNameLookup(t *testing.T) {
	t.Run("a name search surfaces an inactive contributor", func(t *testing.T) {
		// ADR-0008 §1b. Browsing the market is a question about who is
		// available; typing a name is about a person you know exists.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		byName := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Query: "Carol"})
		if byName.Total != 1 {
			t.Fatalf("expected the name search to find Carol, got %d", byName.Total)
		}
		if byName.Results[0].Active {
			t.Error("expected her flagged inactive")
		}

		bySkill := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Skills: []string{"postgres"}})
		for _, res := range bySkill.Results {
			if res.DisplayName == "Carol Diaz" {
				t.Error("browsing must still hide her")
			}
		}
	})

	t.Run("it does not reveal an opted-out contributor", func(t *testing.T) {
		// The exception relaxes a default FILTER, never a gate.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)
		if _, err := db.Users().SetAvailability(ctx, p.bob, domain.NotLooking); err != nil {
			t.Fatalf("opting out: %v", err)
		}

		got := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Query: "Nakamura"})
		if got.Total != 0 {
			t.Errorf("a name search revealed an opted-out contributor: %+v", got.Results)
		}
	})
}

func TestSearchPaging(t *testing.T) {
	t.Run("rejects a page size past the maximum", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		_, err := db.Search().Search(ctx, p.hirer, domain.SearchQuery{PerPage: postgres.MaxPerPage + 1})
		if err == nil {
			t.Error("expected a page size past the cap to be refused")
		}
	})

	t.Run("an empty page still reports the totals", func(t *testing.T) {
		// Reporting zero would read as "nobody matched" when the truth is
		// "nobody on THIS page".
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got := mustSearch(ctx, t, db, p.hirer, domain.SearchQuery{Page: 99})
		if len(got.Results) != 0 {
			t.Fatalf("expected an empty page, got %d", len(got.Results))
		}
		if got.Total == 0 {
			t.Error("an empty page must still say how many matched")
		}
	})
}

func TestLeaderboard(t *testing.T) {
	t.Run("shows everyone, inactive included", func(t *testing.T) {
		// The board is where a hirer finds who fills a gap in a search.
		db, ctx := newDB(t), testContext(t)
		newSearchPopulation(ctx, t, db)

		board, err := db.Search().Leaderboard(ctx, domain.BoardOverall, nil, 50)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		var sawInactive bool
		for _, e := range board.Entries {
			if !e.Active {
				sawInactive = true
			}
		}
		if !sawInactive {
			t.Error("the board must show inactive contributors")
		}
		if board.Entries[0].DisplayName != "Carol Diaz" {
			t.Errorf("expected Carol top on overall, got %q", board.Entries[0].DisplayName)
		}
	})

	t.Run("overall and generalist order the same people differently", func(t *testing.T) {
		// Both numbers are true and they rank differently, which is why there
		// are two (ADR-0007 §5).
		db, ctx := newDB(t), testContext(t)
		newSearchPopulation(ctx, t, db)

		overall, err := db.Search().Leaderboard(ctx, domain.BoardOverall, nil, 50)
		if err != nil {
			t.Fatalf("reading overall: %v", err)
		}
		generalist, err := db.Search().Leaderboard(ctx, domain.BoardGeneralist, nil, 50)
		if err != nil {
			t.Fatalf("reading generalist: %v", err)
		}
		if overall.Entries[0].DisplayName == generalist.Entries[0].DisplayName {
			t.Error("expected the two boards to disagree about who is first")
		}
		if generalist.Entries[0].DisplayName != "Bob Nakamura" {
			t.Errorf("expected Bob first on breadth, got %q", generalist.Entries[0].DisplayName)
		}
	})

	t.Run("a skill board shows only primary standings", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		board, err := db.Search().Leaderboard(ctx, domain.BoardSkill, &p.goSkill, 50)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		for _, e := range board.Entries {
			if e.DisplayName == "Dave Whitfield" {
				t.Error("a secondary standing appeared on a ranked board")
			}
		}
		if board.Skill == nil || board.Skill.Slug != "go" {
			t.Error("expected the board to name its skill")
		}
	})

	t.Run("kind=skill without a skill is refused", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		newSearchPopulation(ctx, t, db)

		if _, err := db.Search().Leaderboard(ctx, domain.BoardSkill, nil, 50); err == nil {
			t.Error("expected kind=skill to require a skill")
		}
	})
}

func TestScorecard(t *testing.T) {
	t.Run("shows secondary skills, unranked", func(t *testing.T) {
		// Standing gates SEARCHABILITY, not disclosure (ADR-0008 §2).
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		card, err := db.Search().Scorecard(ctx, p.hirer, p.dave)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}

		var secondary *domain.ScorecardSkill
		for i := range card.Skills {
			if card.Skills[i].Standing == domain.Secondary {
				secondary = &card.Skills[i]
			}
		}
		if secondary == nil {
			t.Fatal("expected the secondary skill to appear")
		}
		if secondary.Rank != nil {
			t.Error("an unranked skill must carry no rank")
		}
		if secondary.Score <= 0 {
			t.Error("expected the secondary skill to keep its score")
		}
	})

	t.Run("a lapsed contributor's scorecard is readable", func(t *testing.T) {
		// A hirer who found them through include_inactive must be able to read
		// the evidence.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		card, err := db.Search().Scorecard(ctx, p.hirer, p.carol)
		if err != nil {
			t.Fatalf("a lapsed scorecard must be readable: %v", err)
		}
		if card.User.Active {
			t.Error("expected her flagged inactive")
		}
	})

	t.Run("AssertNotSelf hides the caller's own account", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		if _, err := db.Search().Scorecard(ctx, p.daveHiring, p.dave); err == nil {
			t.Error("a hirer read their own contributor scorecard")
		}
		if _, err := db.Search().Scorecard(ctx, p.daveHiring, p.alice); err != nil {
			t.Errorf("everyone else must be unaffected: %v", err)
		}
	})

	t.Run("an opted-out contributor is not found", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)
		if _, err := db.Users().SetAvailability(ctx, p.bob, domain.NotLooking); err != nil {
			t.Fatalf("opting out: %v", err)
		}

		if _, err := db.Search().Scorecard(ctx, p.hirer, p.bob); err == nil {
			t.Error("expected an opted-out scorecard to be hidden")
		}
	})
}

func TestRank(t *testing.T) {
	t.Run("rank survives lapsing", func(t *testing.T) {
		// ADR-0008 §1a. Carol has the highest overall score and is hidden from
		// default search; both facts are reported.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got, err := db.Search().Rank(ctx, p.carol)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if !got.Ranked {
			t.Error("a lapsed contributor must keep their rank")
		}
		if got.Active {
			t.Error("expected active=false so she knows she is hidden")
		}
		if got.Overall.Rank == nil || *got.Overall.Rank != 1 {
			t.Errorf("expected rank 1, got %v", got.Overall.Rank)
		}
	})

	t.Run("opting out yields no rank", func(t *testing.T) {
		// A state the contributor chose.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)
		if _, err := db.Users().SetAvailability(ctx, p.bob, domain.NotLooking); err != nil {
			t.Fatalf("opting out: %v", err)
		}

		got, err := db.Search().Rank(ctx, p.bob)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.Ranked {
			t.Error("an opted-out contributor must not be ranked")
		}
		if got.UnrankedReason != "opted_out" {
			t.Errorf("expected opted_out, got %q", got.UnrankedReason)
		}
		if got.Overall.Score == nil {
			t.Error("their score survives; only the position goes")
		}
	})

	t.Run("carries no neighbours", func(t *testing.T) {
		// Positions and totals and nothing identifying anyone else.
		db, ctx := newDB(t), testContext(t)
		p := newSearchPopulation(ctx, t, db)

		got, err := db.Search().Rank(ctx, p.alice)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.Overall.OutOf < 2 {
			t.Errorf("expected a population size, got %d", got.Overall.OutOf)
		}
		for _, s := range got.Skills {
			if s.Standing == domain.Secondary && s.Rank != nil {
				t.Error("a secondary skill reported a position")
			}
		}
	})
}

// --- fixture -----------------------------------------------------------------

type searchPopulation struct {
	hirer      domain.HirerID
	daveHiring domain.HirerID
	alice      domain.UserID
	bob        domain.UserID
	carol      domain.UserID
	dave       domain.UserID
	goSkill    domain.SkillID
}

// newSearchPopulation mirrors the e2e fixtures' scored_population, so the two
// suites disagree loudly rather than quietly if the semantics drift.
//
//	Alice  go 64.2                       overall 64.2  generalist  64.2  active
//	Bob    go 55, k8s 52, postgres 48    overall 60.4  generalist 122.7  active
//	Carol  postgres 71.5                 overall 71.5  generalist  71.5  LAPSED
//	Dave   k8s 58, go 81 SECONDARY       overall 58.0  generalist  58.0  active
func newSearchPopulation(ctx context.Context, t *testing.T, db *postgres.DB) searchPopulation {
	t.Helper()
	seedCatalogue(ctx, t, db)

	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank Rivera", "Acme Corp", "acme")

	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
	bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
	carol := mustCreateContributor(ctx, t, db, "Carol Diaz", 100003, "cdiaz")
	dave := mustCreateContributor(ctx, t, db, "Dave Whitfield", 100004, "dwhit")

	// dave_hiring shares Dave's GitHub identity — the AssertNotSelf case.
	daveHiring := mustRegister(ctx, t, db, "dave.hiring@example.com", "Dave Whitfield", "Acme Two", "acme-two")
	giveHirerGitHubIdentity(ctx, t, db, daveHiring.ID, 100004, "dwhit")

	for _, u := range []domain.UserID{alice.ID, bob.ID, dave.ID} {
		if _, err := db.Users().SetAvailability(ctx, u, domain.LookingForJob); err != nil {
			t.Fatalf("setting availability: %v", err)
		}
	}
	if _, err := db.Users().SetAvailability(ctx, carol.ID, domain.LookingForJob); err != nil {
		t.Fatalf("setting availability: %v", err)
	}
	setExpiry(ctx, t, db, carol.ID, "now() - interval '1 day'")

	setUserSkill(ctx, t, db, alice.ID, "go", domain.Primary, 5, 64.2)
	setUserSkill(ctx, t, db, bob.ID, "go", domain.Primary, 5, 55.0)
	setUserSkill(ctx, t, db, bob.ID, "kubernetes", domain.Primary, 5, 52.0)
	setUserSkill(ctx, t, db, bob.ID, "postgres", domain.Primary, 5, 48.0)
	setUserSkill(ctx, t, db, carol.ID, "postgres", domain.Primary, 5, 71.5)
	setUserSkill(ctx, t, db, dave.ID, "kubernetes", domain.Primary, 5, 58.0)
	setUserSkill(ctx, t, db, dave.ID, "go", domain.Secondary, 4, 81.0)

	setUserScores(ctx, t, db, alice.ID, 64.2, 64.2)
	setUserScores(ctx, t, db, bob.ID, 60.4, 122.7)
	setUserScores(ctx, t, db, carol.ID, 71.5, 71.5)
	setUserScores(ctx, t, db, dave.ID, 58.0, 58.0)

	return searchPopulation{
		hirer: hirer.ID, daveHiring: daveHiring.ID,
		alice: alice.ID, bob: bob.ID, carol: carol.ID, dave: dave.ID,
		goSkill: skillID(ctx, t, db, "go"),
	}
}

func setUserSkill(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, slug string, standing domain.Standing, prs int, score float64) {
	t.Helper()
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO user_skills (user_id, skill_id, distinct_pr_count, standing, score, promoted_at)
		VALUES ($1, (SELECT id FROM skills WHERE slug = $2), $3, $4::skill_standing, $5,
		        CASE WHEN $4::skill_standing = 'primary' THEN now() END)`,
		string(user), slug, prs, string(standing), score); err != nil {
		t.Fatalf("setting %s/%s: %v", user, slug, err)
	}
}

func setUserScores(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, overall, generalist float64) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Users().SetUserScores(ctx, tx, user, &overall, &generalist)
	}); err != nil {
		t.Fatalf("setting scores: %v", err)
	}
}

func mustSearch(ctx context.Context, t *testing.T, db *postgres.DB, caller domain.HirerID, q domain.SearchQuery) *domain.SearchResults {
	t.Helper()
	got, err := db.Search().Search(ctx, caller, q)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	return got
}

func f(v float64) *float64 { return &v }
