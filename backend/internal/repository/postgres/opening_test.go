package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

// Published adverts (ADR-0020).
//
// Almost every case here is about the SAME rule from a different angle: a
// stated bar is not cleared by an absent score. These figures are evidence the
// platform produced, and somebody with no overall score has not been judged
// rather than scored low — showing them a role asking for 60 would promise a
// match that does not exist.
func TestOpeningsMatchOnlyWhatIsCleared(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	p := newSearchPopulation(ctx, t, db)

	// Alice: overall 64.2, go primary at 64.2, in GB, contributing since 2016.
	six := 6
	saveWorkPreferences(ctx, t, db, p.alice, domain.WorkPreferences{
		OpenToRemote: true, CurrentCountry: "GB", OfficeYOE: &six,
		FirstPRURL: "https://github.com/acme/platform/pull/1",
	})
	verifyFirstPR(ctx, t, db, p.alice, "2016-03-01")

	// Bob has scores too, but has never opened the profile form: no country.
	role := mustCreateRole(ctx, t, db, p.owner, nil)
	mustOpen(ctx, t, db, role.ID, p.owner)

	matched := func(user domain.UserID) *port.OpeningResults {
		t.Helper()
		out, err := db.Openings().Matching(ctx, port.OpeningMatch{UserID: user})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		return out
	}

	t.Run("an unpublished advert reaches nobody", func(t *testing.T) {
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{})
		got := matched(p.alice)
		if got.Matched != 0 || got.Missed != 0 {
			t.Errorf("a draft advert is not live: matched=%d missed=%d", got.Matched, got.Missed)
		}
	})

	t.Run("published with no bar reaches everybody", func(t *testing.T) {
		publishOpening(ctx, t, db, role.ID, p.hirer)
		got := matched(p.alice)
		if got.Matched != 1 {
			t.Fatalf("expected one, got %d (missed %d)", got.Matched, got.Missed)
		}
		if got.Openings[0].OrganizationName == "" {
			t.Error("a contributor sees a company name, not an id")
		}
	})

	t.Run("an overall bar above their score drops it, and it is COUNTED", func(t *testing.T) {
		// The count is the whole of what they are told about the rest: an
		// empty list alone cannot say whether nobody is hiring or whether they
		// clear nothing.
		high := 99.0
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{MinOverallScore: &high})
		got := matched(p.alice)
		if got.Matched != 0 {
			t.Errorf("64.2 should not clear 99")
		}
		if got.Missed != 1 {
			t.Errorf("the miss must be counted, got %d", got.Missed)
		}
	})

	t.Run("AN ABSENT SCORE CLEARS NOTHING", func(t *testing.T) {
		// Carol has no scored skills in this population and no overall score.
		// The rule that matters: unjudged is not low, and a filter that let
		// her through would promise a match that does not exist.
		low := 1.0
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{MinOverallScore: &low})

		unscored := mustCreateContributor(ctx, t, db, "Unjudged Person", 100009, "unjudged")
		got := matched(unscored.ID)
		if got.Matched != 0 {
			t.Error("somebody who has never been judged cleared a stated bar")
		}
		if got.Missed != 1 {
			t.Errorf("and the miss is counted, got %d", got.Missed)
		}
	})

	t.Run("a skill bar reads PRIMARY standing only", func(t *testing.T) {
		// Dave's Go is the highest score on the platform and is SECONDARY.
		// Five distinct merged pull requests is what makes a skill rankable,
		// and a bar cleared by a secondary skill would be cleared by evidence
		// the platform declines to rank (ADR-0005).
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{
			Skills: []domain.OpeningSkill{{SkillID: p.goSkill, MinScore: 60}},
		})

		if got := matched(p.alice); got.Matched != 1 {
			t.Errorf("alice is primary in go at 64.2 and should clear 60, got %d", got.Matched)
		}
		if got := matched(p.dave); got.Matched != 0 {
			t.Error("dave's go is secondary and must not clear a skill bar")
		}
	})

	t.Run("a skill nobody claimed fails on absence", func(t *testing.T) {
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{
			Skills: []domain.OpeningSkill{{SkillID: p.goSkill, MinScore: 60}},
		})
		if got := matched(p.carol); got.Matched != 0 {
			t.Error("carol has no go standing at all and must not clear a go bar")
		}
	})

	t.Run("unverified open-source years clear nothing", func(t *testing.T) {
		one := 1
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{MinOSSYOE: &one})
		if got := matched(p.alice); got.Matched != 1 {
			t.Error("alice's verified 2016 first pull request clears one year")
		}
		if got := matched(p.bob); got.Matched != 0 {
			t.Error("bob has no verified first pull request and must not clear it")
		}
	})

	t.Run("withdrawing takes it down without deleting it", func(t *testing.T) {
		saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{})
		publishOpening(ctx, t, db, role.ID, p.hirer)

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Openings().Withdraw(ctx, tx, role.ID, time.Now())
			return err
		}); err != nil {
			t.Fatalf("withdrawing: %v", err)
		}

		got := matched(p.alice)
		if got.Matched != 0 || got.Missed != 0 {
			t.Error("a withdrawn advert is not live, and is not a miss either")
		}
		// Still there: a contributor who saw it yesterday is better served by
		// a row that says where it went.
		back, err := db.Openings().ByRole(ctx, role.ID)
		if err != nil {
			t.Fatalf("the row should survive a withdrawal: %v", err)
		}
		if back.WithdrawnAt == nil || back.PublishedAt == nil {
			t.Error("a withdrawal keeps the publication date")
		}
	})
}

// Closing the role takes the advert with it — otherwise a withdrawn job leaves
// a posting behind and a contributor reads about work nobody is offering.
func TestClosingARoleTakesItsAdvertDown(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	p := newSearchPopulation(ctx, t, db)
	hirer := p.owner

	// She has to have said she would take the work: an advert reaches only
	// people whose stated shapes admit it (ADR-0018 §4).
	saveWorkPreferences(ctx, t, db, p.alice, domain.WorkPreferences{OpenToRemote: true})

	role := mustCreateRole(ctx, t, db, hirer, nil)
	mustOpen(ctx, t, db, role.ID, hirer)
	saveOpening(ctx, t, db, role.ID, p.hirer, domain.Opening{})
	publishOpening(ctx, t, db, role.ID, p.hirer)

	before, err := db.Openings().Matching(ctx, port.OpeningMatch{UserID: p.alice})
	if err != nil {
		t.Fatalf("matching: %v", err)
	}
	if before.Matched != 1 {
		t.Fatalf("expected the advert to be live, got %d", before.Matched)
	}

	mustClose(ctx, t, db, role.ID, hirer, domain.CloseNotNeeded)

	after, err := db.Openings().Matching(ctx, port.OpeningMatch{UserID: p.alice})
	if err != nil {
		t.Fatalf("matching: %v", err)
	}
	if after.Matched != 0 || after.Missed != 0 {
		t.Errorf("a closed role must take its advert down: matched=%d missed=%d",
			after.Matched, after.Missed)
	}
}

// --- helpers -----------------------------------------------------------------

func saveOpening(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, by domain.HirerID, in domain.Opening) {
	t.Helper()
	in.RoleID, in.CreatedBy = role, by
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		_, err := db.Openings().Save(ctx, tx, &in)
		return err
	}); err != nil {
		t.Fatalf("saving the opening: %v", err)
	}
}

func publishOpening(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, by domain.HirerID) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		_, err := db.Openings().Publish(ctx, tx, role, by, time.Now())
		return err
	}); err != nil {
		t.Fatalf("publishing the opening: %v", err)
	}
}

func saveWorkPreferences(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, w domain.WorkPreferences) {
	t.Helper()
	w.UserID = user
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Profiles().SaveWorkPreferences(ctx, tx, &w)
	}); err != nil {
		t.Fatalf("saving preferences: %v", err)
	}
}
