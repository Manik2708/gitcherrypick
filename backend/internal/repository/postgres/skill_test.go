package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestSkillRepositorySearch(t *testing.T) {
	t.Run("an alias resolves to the canonical skill", func(t *testing.T) {
		// The alias is a lookup key, never a claimable thing. Returning the
		// canonical skill plus how it matched is what lets a controller say
		// "'golang' is an alias for 'go'" instead of silently substituting.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)

		got, err := db.Skills().Search(ctx, "golang")
		if err != nil {
			t.Fatalf("searching: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 match, got %d", len(got))
		}
		if got[0].Skill.Slug != "go" {
			t.Errorf("expected the canonical slug 'go', got %q", got[0].Skill.Slug)
		}
		if got[0].MatchedVia != "alias" || got[0].MatchedAlias != "golang" {
			t.Errorf("expected to be told it matched via alias 'golang', got via=%q alias=%q",
				got[0].MatchedVia, got[0].MatchedAlias)
		}
	})

	t.Run("an exact name outranks a near miss", func(t *testing.T) {
		// Ordering by similarity alone would put a close misspelling above the
		// thing actually named.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)

		got, err := db.Skills().Search(ctx, "Go")
		if err != nil {
			t.Fatalf("searching: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("expected at least one match")
		}
		if got[0].Skill.Slug != "go" {
			t.Errorf("expected 'go' first, got %q", got[0].Skill.Slug)
		}
		if got[0].MatchedVia != "name" {
			t.Errorf("expected via=name for an exact hit, got %q", got[0].MatchedVia)
		}
	})

	t.Run("an inactive skill is never returned", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		if _, err := db.Pool().Exec(ctx, `UPDATE skills SET is_active = false WHERE slug = 'go'`); err != nil {
			t.Fatalf("deactivating: %v", err)
		}

		got, err := db.Skills().Search(ctx, "golang")
		if err != nil {
			t.Fatalf("searching: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected no matches for a deactivated skill, got %d", len(got))
		}
	})
}

func TestSkillRepositoryCreate(t *testing.T) {
	t.Run("writes the skill and its aliases", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)

		var created *domain.Skill
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			created, err = db.Skills().Create(ctx, tx, &domain.Skill{
				Slug: "webassembly", Name: "WebAssembly",
				Description: "Compiling to wasm.", Category: "language",
				Aliases: []string{"wasm"},
			})
			return err
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		if created.ScoringMode != domain.ScoringStandard {
			t.Errorf("expected the default scoring mode, got %q", created.ScoringMode)
		}

		got, err := db.Skills().Search(ctx, "wasm")
		if err != nil {
			t.Fatalf("searching: %v", err)
		}
		if len(got) != 1 || got[0].Skill.Slug != "webassembly" {
			t.Errorf("expected the new alias to resolve, got %+v", got)
		}
	})

	t.Run("a duplicate slug conflicts", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Skills().Create(ctx, tx, &domain.Skill{
				Slug: "go", Name: "Go Again", Description: "x", Category: "language",
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})

	t.Run("an alias colliding with another skill's conflicts", func(t *testing.T) {
		// uq_skill_alias is global. 'golang' must not become claimable through
		// a second skill's back door.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Skills().Create(ctx, tx, &domain.Skill{
				Slug: "rust", Name: "Rust", Description: "x", Category: "language",
				Aliases: []string{"golang"},
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM skills WHERE slug = 'rust'`); n != 0 {
			t.Errorf("the rolled-back skill survived: %d row(s)", n)
		}
	})
}

func TestSkillRepositoryStanding(t *testing.T) {
	t.Run("promotes automatically at the fifth scored PR", func(t *testing.T) {
		// ADR-0003: promotion needs no resubmission. The evidence already
		// exists and has already been judged.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		goID := skillID(ctx, t, db, "go")

		for i := 1; i <= 4; i++ {
			linkScored(ctx, t, db, user.ID, goID, claim, i)
			us := mustRecompute(ctx, t, db, user.ID, goID)
			if us.Standing != domain.Secondary {
				t.Fatalf("at %d PRs expected secondary, got %s", i, us.Standing)
			}
			if us.PromotedAt != nil {
				t.Fatalf("at %d PRs expected no promotion stamp", i)
			}
		}

		linkScored(ctx, t, db, user.ID, goID, claim, 5)
		us := mustRecompute(ctx, t, db, user.ID, goID)
		if us.Standing != domain.Primary {
			t.Errorf("at 5 PRs expected primary, got %s", us.Standing)
		}
		if us.DistinctPRCount != 5 {
			t.Errorf("expected 5 distinct PRs, got %d", us.DistinctPRCount)
		}
		if us.PromotedAt == nil {
			t.Error("expected promoted_at to be stamped")
		}
	})

	t.Run("a rejected link does not count", func(t *testing.T) {
		// ADR-0007 §3: five PRs of which one was disqualified is still four,
		// and the skill stays secondary. This is the rule that makes
		// disqualification bite on STANDING rather than only on score.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		goID := skillID(ctx, t, db, "go")

		for i := 1; i <= 4; i++ {
			linkScored(ctx, t, db, user.ID, goID, claim, i)
		}
		// The fifth is judged and rejected.
		link := port.PRLink{UserID: user.ID, SkillID: goID, ClaimID: claim,
			RepoOwner: "acme", RepoName: "repo", PRNumber: 5}
		mustLink(ctx, t, db, link)
		mustSetStatus(ctx, t, db, link, domain.LinkRejected)

		us := mustRecompute(ctx, t, db, user.ID, goID)
		if us.Standing != domain.Secondary {
			t.Errorf("expected secondary with one rejected PR, got %s", us.Standing)
		}
		if us.DistinctPRCount != 4 {
			t.Errorf("expected 4 counted PRs, got %d", us.DistinctPRCount)
		}
	})

	t.Run("a pending link does not count either", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		goID := skillID(ctx, t, db, "go")

		for i := 1; i <= 5; i++ {
			mustLink(ctx, t, db, port.PRLink{UserID: user.ID, SkillID: goID, ClaimID: claim,
				RepoOwner: "acme", RepoName: "repo", PRNumber: i})
		}
		if us := mustRecompute(ctx, t, db, user.ID, goID); us != nil {
			t.Errorf("pending links are not evidence, so there is no standing: %+v", us)
		}
		if n := count(t, db,
			`SELECT count(*) FROM user_skills WHERE user_id = $1`, string(user.ID)); n != 0 {
			t.Errorf("expected no user_skills row, found %d", n)
		}
	})

	t.Run("demotion keeps the promotion stamp", func(t *testing.T) {
		// Withdrawal demotes, but it does not unmake the fact that a promotion
		// happened.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		goID := skillID(ctx, t, db, "go")

		for i := 1; i <= 5; i++ {
			linkScored(ctx, t, db, user.ID, goID, claim, i)
		}
		promoted := mustRecompute(ctx, t, db, user.ID, goID)
		if promoted.PromotedAt == nil {
			t.Fatal("expected a promotion stamp")
		}

		if _, err := db.Pool().Exec(ctx,
			`DELETE FROM user_skill_pr_links WHERE user_id = $1 AND pr_number = 5`, string(user.ID)); err != nil {
			t.Fatalf("withdrawing a PR: %v", err)
		}
		demoted := mustRecompute(ctx, t, db, user.ID, goID)

		if demoted.Standing != domain.Secondary {
			t.Errorf("expected demotion to secondary, got %s", demoted.Standing)
		}
		if demoted.PromotedAt == nil {
			t.Error("expected promoted_at to survive demotion")
		}
	})

	t.Run("the same PR cannot evidence the same skill twice", func(t *testing.T) {
		// The (user, skill, repo, pr) primary key, enforced BEFORE a model
		// call is spent — ADR-0007.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		goID := skillID(ctx, t, db, "go")

		link := port.PRLink{UserID: user.ID, SkillID: goID, ClaimID: claim,
			RepoOwner: "acme", RepoName: "repo", PRNumber: 1}
		mustLink(ctx, t, db, link)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Skills().LinkPairs(ctx, tx, []port.PRLink{link})
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})

	t.Run("the same PR may evidence a DIFFERENT skill", func(t *testing.T) {
		// One PR can demonstrate Go and Kubernetes at once. The uniqueness is
		// on the triple, not on the PR.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)

		for _, slug := range []string{"go", "kubernetes"} {
			mustLink(ctx, t, db, port.PRLink{
				UserID: user.ID, SkillID: skillID(ctx, t, db, slug), ClaimID: claim,
				RepoOwner: "acme", RepoName: "repo", PRNumber: 1,
			})
		}
		if n := count(t, db, `SELECT count(*) FROM user_skill_pr_links WHERE pr_number = 1`); n != 2 {
			t.Errorf("expected one PR to back two skills, found %d link(s)", n)
		}
	})
}

func TestSkillRepositoryRequests(t *testing.T) {
	t.Run("a request that matches an existing skill never reaches the queue", func(t *testing.T) {
		// Most noise is spelling variants, and no admin should spend attention
		// on them (ADR-0003).
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)

		// The dedupe itself lives in the service, which asks this question
		// before writing anything (ADR-0003). What the repository owes it is
		// the answer: "Golang" is the alias of a skill that already exists.
		matched, err := db.Skills().MatchSkill(ctx, "Golang")
		if err != nil {
			t.Fatalf("matching a known alias: %v", err)
		}
		if matched.Slug != "go" {
			t.Errorf("expected the alias to resolve to go, got %q", matched.Slug)
		}

		if _, err := db.Skills().MatchSkill(ctx, "Zigzagulator"); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected an unknown name to match nothing, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM skill_requests`); n != 0 {
			t.Errorf("matching wrote to the queue: %d row(s)", n)
		}
	})

	t.Run("a genuinely new skill queues", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		id, err := db.Skills().CreateRequest(ctx, user.ID, "WebAssembly", "Compiled three runtimes.")
		if err != nil {
			t.Fatalf("creating a request: %v", err)
		}

		pending, err := db.Skills().PendingRequests(ctx, "pending")
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(pending) != 1 || pending[0].ID != id {
			t.Fatalf("expected the request in the queue, got %+v", pending)
		}
	})

	t.Run("approving creates the skill and decides the request together", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		admin := mustCreateAdmin(ctx, t, db)

		id, err := db.Skills().CreateRequest(ctx, user.ID, "WebAssembly", "Compiled three runtimes.")
		if err != nil {
			t.Fatalf("creating a request: %v", err)
		}

		err = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			created, err := db.Skills().Create(ctx, tx, &domain.Skill{
				Slug: "webassembly", Name: "WebAssembly", Description: "wasm.",
				Category: "language", Aliases: []string{"wasm"},
			})
			if err != nil {
				return err
			}
			return db.Skills().DecideRequest(ctx, tx, id, admin, true, "", created)
		})
		if err != nil {
			t.Fatalf("approving: %v", err)
		}

		if n := count(t, db, `SELECT count(*) FROM skill_requests WHERE status = 'pending'`); n != 0 {
			t.Errorf("expected the queue drained, found %d", n)
		}
		if n := count(t, db,
			`SELECT count(*) FROM skill_requests WHERE created_skill_id IS NOT NULL`); n != 1 {
			t.Error("expected the request to name the skill it created")
		}
	})

	t.Run("a decision is final", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		admin := mustCreateAdmin(ctx, t, db)

		id, err := db.Skills().CreateRequest(ctx, user.ID, "Agile", "I run standups.")
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		reject := func() error {
			return db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				return db.Skills().DecideRequest(ctx, tx, id, admin, false, "Not evidenced by a diff.", nil)
			})
		}
		if err := reject(); err != nil {
			t.Fatalf("rejecting: %v", err)
		}
		if err := reject(); !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected re-deciding to conflict, got %v", err)
		}
	})
}

// --- helpers -----------------------------------------------------------------

// seedCatalogue writes the four skills the fixtures use.
func seedCatalogue(ctx context.Context, t *testing.T, db *postgres.DB) {
	t.Helper()
	skills := []domain.Skill{
		{Slug: "go", Name: "Go", Description: "The Go language.", Category: "language", Aliases: []string{"golang"}},
		{Slug: "kubernetes", Name: "Kubernetes", Description: "Operating on Kubernetes.", Category: "infrastructure", Aliases: []string{"k8s"}},
		{Slug: "postgres", Name: "PostgreSQL", Description: "Schema and query work.", Category: "database", Aliases: []string{"psql"}},
		{Slug: "pr-review", Name: "PR Review", Description: "Reviewing other people's work.", Category: "practice", ScoringMode: domain.ScoringJudgedOnly},
	}
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		for i := range skills {
			if _, err := db.Skills().Create(ctx, tx, &skills[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the catalogue: %v", err)
	}
}

func skillID(ctx context.Context, t *testing.T, db *postgres.DB, slug string) domain.SkillID {
	t.Helper()
	s, err := db.Skills().BySlug(ctx, slug)
	if err != nil {
		t.Fatalf("reading skill %q: %v", slug, err)
	}
	return s.ID
}

// mustCreateClaim writes a bare claim row. The claim repository does not exist
// yet, and these tests need a foreign key to point at rather than its
// behaviour.
func mustCreateClaim(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID) domain.ClaimID {
	t.Helper()
	var id string
	if err := db.Pool().QueryRow(ctx,
		`INSERT INTO claims (id, user_id, status) VALUES (gen_random_uuid(), $1, 'evaluated') RETURNING id`,
		string(user)).Scan(&id); err != nil {
		t.Fatalf("creating a claim: %v", err)
	}
	return domain.ClaimID(id)
}

func mustCreateAdmin(ctx context.Context, t *testing.T, db *postgres.DB) domain.AdminID {
	t.Helper()
	var id string
	if err := db.Pool().QueryRow(ctx,
		`INSERT INTO admin_accounts (id, email, password_hash, display_name)
		 VALUES (gen_random_uuid(), 'admin@example.test', 'x', 'Root') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("creating an admin: %v", err)
	}
	return domain.AdminID(id)
}

func mustLink(ctx context.Context, t *testing.T, db *postgres.DB, l port.PRLink) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Skills().LinkPairs(ctx, tx, []port.PRLink{l})
	}); err != nil {
		t.Fatalf("linking: %v", err)
	}
}

func mustSetStatus(ctx context.Context, t *testing.T, db *postgres.DB, l port.PRLink, status domain.PRLinkStatus) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Skills().SetLinkStatus(ctx, tx, []port.PRLink{l}, status, nil)
	}); err != nil {
		t.Fatalf("setting link status: %v", err)
	}
}

func linkScored(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, skill domain.SkillID, claim domain.ClaimID, pr int) {
	t.Helper()
	l := port.PRLink{UserID: user, SkillID: skill, ClaimID: claim,
		RepoOwner: "acme", RepoName: "repo", PRNumber: pr}
	mustLink(ctx, t, db, l)
	mustSetStatus(ctx, t, db, l, domain.LinkScored)
}

func mustRecompute(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, skill domain.SkillID) *domain.UserSkill {
	t.Helper()
	var us *domain.UserSkill
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		us, err = db.Skills().RecomputeStanding(ctx, tx, user, skill)
		return err
	}); err != nil {
		t.Fatalf("recomputing standing: %v", err)
	}
	return us
}
