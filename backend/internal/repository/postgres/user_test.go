package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestUserRepositoryCreate(t *testing.T) {
	t.Run("writes the user and its github identity together", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)

		var created *domain.Contributor
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			created, err = db.Users().Create(ctx, tx, &domain.Contributor{
				DisplayName:  "Nina Ferrer",
				Email:        "nina@example.com",
				GitHubUserID: 100077,
				GitHubLogin:  "newcomer",
			})
			return err
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}

		if created.ID == "" {
			t.Error("expected a generated id")
		}
		if created.CreatedAt.IsZero() {
			t.Error("expected created_at to come back from the database")
		}
		if n := count(t, db, `SELECT count(*) FROM user_github_identities WHERE user_id = $1`, string(created.ID)); n != 1 {
			t.Errorf("expected 1 github identity, found %d", n)
		}
	})

	t.Run("writes no availability row", func(t *testing.T) {
		// ADR-0002: a new contributor is invisible to search until they choose
		// to be visible. Defaulting them to a status would be a statement they
		// never made.
		db, ctx := newDB(t), testContext(t)
		created := mustCreateContributor(ctx, t, db, "Nina Ferrer", 100077, "newcomer")

		if n := count(t, db, `SELECT count(*) FROM user_availability WHERE user_id = $1`, string(created.ID)); n != 0 {
			t.Errorf("expected no availability row, found %d", n)
		}
		got, err := db.Users().ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if got.Availability != nil {
			t.Errorf("expected nil availability, got %+v", got.Availability)
		}
	})

	t.Run("rolls back the user when the identity conflicts", func(t *testing.T) {
		// The property the shared Tx exists for. Two contributors cannot hold
		// one GitHub id, and the half-written user must not survive.
		//
		// This test is why RFC-0009 exists. RFC-0002 constrains one identity per
		// account and never one account per identity, so before RFC-0009's
		// partial unique index the second insert succeeded and ByGitHubUserID
		// was ambiguous — an account-takeover path on the sign-in lookup.
		db, ctx := newDB(t), testContext(t)
		mustCreateContributor(ctx, t, db, "First Claimant", 100077, "first")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Users().Create(ctx, tx, &domain.Contributor{
				DisplayName:  "Second Claimant",
				GitHubUserID: 100077,
				GitHubLogin:  "second",
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM users WHERE display_name = 'Second Claimant'`); n != 0 {
			t.Errorf("the rolled-back user survived: %d row(s)", n)
		}
	})
}

func TestUserRepositoryByGitHubUserID(t *testing.T) {
	t.Run("resolves on the numeric id, not the login", func(t *testing.T) {
		// A login can be changed or handed to someone else. Treating it as
		// identity would give one contributor's account to whoever claimed
		// their old name.
		db, ctx := newDB(t), testContext(t)
		created := mustCreateContributor(ctx, t, db, "Nina Ferrer", 100077, "newcomer")

		if _, err := db.Pool().Exec(ctx,
			`UPDATE user_github_identities SET github_login = 'renamed' WHERE user_id = $1`,
			string(created.ID)); err != nil {
			t.Fatalf("renaming: %v", err)
		}

		got, err := db.Users().ByGitHubUserID(ctx, 100077)
		if err != nil {
			t.Fatalf("resolving after a rename: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("expected %s, got %s", created.ID, got.ID)
		}
		if got.GitHubLogin != "renamed" {
			t.Errorf("expected the refreshed login, got %q", got.GitHubLogin)
		}
	})

	t.Run("reports not found for an unknown identity", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		_, err := db.Users().ByGitHubUserID(ctx, 999999)
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})
}

func TestUserRepositorySetAvailability(t *testing.T) {
	t.Run("sets a fifteen-day window", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		created := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		got, err := db.Users().SetAvailability(ctx, created.ID, domain.LookingForJob)
		if err != nil {
			t.Fatalf("setting availability: %v", err)
		}
		if got.ExpiresAt == nil {
			t.Fatal("expected an expiry")
		}

		// A minute of slack: the database sets the timestamp, not the test.
		want := time.Now().Add(15 * 24 * time.Hour)
		if delta := got.ExpiresAt.Sub(want); delta > time.Minute || delta < -time.Minute {
			t.Errorf("expected expiry near %s, got %s", want, got.ExpiresAt)
		}
	})

	t.Run("refreshing extends the window and clears the reminder", func(t *testing.T) {
		// Leaving reminded_at set would silence the warning for a contributor
		// who renewed once and then went quiet again.
		db, ctx := newDB(t), testContext(t)
		created := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		if _, err := db.Users().SetAvailability(ctx, created.ID, domain.LookingForJob); err != nil {
			t.Fatalf("first set: %v", err)
		}
		if err := db.Users().MarkReminded(ctx, []domain.UserID{created.ID}); err != nil {
			t.Fatalf("marking reminded: %v", err)
		}

		got, err := db.Users().SetAvailability(ctx, created.ID, domain.LookingForFreelance)
		if err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		if got.RemindedAt != nil {
			t.Errorf("expected reminded_at cleared, got %s", got.RemindedAt)
		}
		if got.Status != domain.LookingForFreelance {
			t.Errorf("expected the new status, got %s", got.Status)
		}
		if n := count(t, db, `SELECT count(*) FROM user_availability WHERE user_id = $1`, string(created.ID)); n != 1 {
			t.Errorf("expected the upsert to keep one row, found %d", n)
		}
	})

	t.Run("a lapsed row is preserved, never deleted", func(t *testing.T) {
		// ADR-0002 enforces expiry in the QUERY, not by mutating the row: a
		// returning contributor must find their setting intact.
		db, ctx := newDB(t), testContext(t)
		created := mustCreateContributor(ctx, t, db, "Carol Diaz", 100003, "cdiaz")

		if _, err := db.Users().SetAvailability(ctx, created.ID, domain.LookingForJob); err != nil {
			t.Fatalf("setting: %v", err)
		}
		if _, err := db.Pool().Exec(ctx,
			`UPDATE user_availability SET expires_at = now() - interval '1 day' WHERE user_id = $1`,
			string(created.ID)); err != nil {
			t.Fatalf("lapsing: %v", err)
		}

		got, err := db.Users().ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if got.Availability == nil {
			t.Fatal("the lapsed row was lost")
		}
		if got.Availability.Status != domain.LookingForJob {
			t.Errorf("expected the status preserved, got %s", got.Availability.Status)
		}
		if got.Availability.IsActive(time.Now()) {
			t.Error("a lapsed contributor must not read as active")
		}
	})
}

func TestUserRepositoryLapsingSoon(t *testing.T) {
	t.Run("finds only unreminded contributors inside the window", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)

		soon := mustCreateContributor(ctx, t, db, "Lapsing Soon", 200001, "soon")
		later := mustCreateContributor(ctx, t, db, "Plenty Of Time", 200002, "later")
		reminded := mustCreateContributor(ctx, t, db, "Already Told", 200003, "told")
		optedOut := mustCreateContributor(ctx, t, db, "Not Looking", 200004, "nope")
		lapsed := mustCreateContributor(ctx, t, db, "Already Gone", 200005, "gone")

		for _, id := range []domain.UserID{soon.ID, later.ID, reminded.ID, lapsed.ID} {
			if _, err := db.Users().SetAvailability(ctx, id, domain.LookingForJob); err != nil {
				t.Fatalf("setting availability: %v", err)
			}
		}
		if _, err := db.Users().SetAvailability(ctx, optedOut.ID, domain.NotLooking); err != nil {
			t.Fatalf("setting availability: %v", err)
		}

		setExpiry(ctx, t, db, soon.ID, "now() + interval '2 days'")
		setExpiry(ctx, t, db, reminded.ID, "now() + interval '2 days'")
		setExpiry(ctx, t, db, optedOut.ID, "now() + interval '2 days'")
		setExpiry(ctx, t, db, lapsed.ID, "now() - interval '1 day'")
		if err := db.Users().MarkReminded(ctx, []domain.UserID{reminded.ID}); err != nil {
			t.Fatalf("marking reminded: %v", err)
		}

		got, err := db.Users().LapsingSoon(ctx, 3*24*time.Hour, 100)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}

		if len(got) != 1 {
			t.Fatalf("expected exactly 1 contributor, got %d: %s", len(got), names(got))
		}
		if got[0].ID != soon.ID {
			t.Errorf("expected %s, got %s", soon.DisplayName, got[0].DisplayName)
		}
	})

	t.Run("a refreshed window makes a reminded contributor eligible again", func(t *testing.T) {
		// The reminder is per window, not once per lifetime.
		db, ctx := newDB(t), testContext(t)
		c := mustCreateContributor(ctx, t, db, "Renewer", 200006, "renew")

		if _, err := db.Users().SetAvailability(ctx, c.ID, domain.LookingForJob); err != nil {
			t.Fatalf("setting: %v", err)
		}
		setExpiry(ctx, t, db, c.ID, "now() + interval '2 days'")
		if err := db.Users().MarkReminded(ctx, []domain.UserID{c.ID}); err != nil {
			t.Fatalf("marking reminded: %v", err)
		}

		got, err := db.Users().LapsingSoon(ctx, 3*24*time.Hour, 100)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("a reminded contributor should not reappear, got %d", len(got))
		}

		if _, err := db.Users().SetAvailability(ctx, c.ID, domain.LookingForJob); err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		setExpiry(ctx, t, db, c.ID, "now() + interval '2 days'")

		got, err = db.Users().LapsingSoon(ctx, 3*24*time.Hour, 100)
		if err != nil {
			t.Fatalf("listing again: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected the renewed contributor to be eligible, got %d", len(got))
		}
	})
}

func TestUserRepositorySetUserScores(t *testing.T) {
	t.Run("writes both numbers", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		c := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		overall, generalist := 64.2, 122.7
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Users().SetUserScores(ctx, tx, c.ID, &overall, &generalist)
		})
		if err != nil {
			t.Fatalf("setting scores: %v", err)
		}

		got, err := db.Users().ByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if got.OverallScore == nil || *got.OverallScore != overall {
			t.Errorf("expected overall %v, got %v", overall, got.OverallScore)
		}
		if got.GeneralistScore == nil || *got.GeneralistScore != generalist {
			t.Errorf("expected generalist %v, got %v", generalist, got.GeneralistScore)
		}
	})

	t.Run("null is not zero", func(t *testing.T) {
		// ADR-0007: a contributor with no primary skill has no score. Zero
		// would claim we measured something.
		db, ctx := newDB(t), testContext(t)
		c := mustCreateContributor(ctx, t, db, "Fresh Start", 100088, "fresh")

		got, err := db.Users().ByID(ctx, c.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.OverallScore != nil {
			t.Errorf("expected nil overall, got %v", *got.OverallScore)
		}
		if got.GeneralistScore != nil {
			t.Errorf("expected nil generalist, got %v", *got.GeneralistScore)
		}
	})

	t.Run("reports not found for an unknown user", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		score := 50.0
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Users().SetUserScores(ctx, tx, "01920000-0000-7000-8000-00000000dead", &score, &score)
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})
}

func TestInTx(t *testing.T) {
	t.Run("rolls back on error", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		sentinel := errors.New("deliberate")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			if _, err := db.Users().Create(ctx, tx, &domain.Contributor{
				DisplayName: "Doomed", GitHubUserID: 300001, GitHubLogin: "doomed",
			}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected the sentinel back, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
			t.Errorf("expected the write rolled back, found %d user(s)", n)
		}
	})

	t.Run("rolls back on panic, and keeps panicking", func(t *testing.T) {
		// A transaction left open by a panic would wedge the connection for
		// everything that reused it.
		db, ctx := newDB(t), testContext(t)

		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected the panic to propagate")
				}
			}()
			_ = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				_, _ = db.Users().Create(ctx, tx, &domain.Contributor{
					DisplayName: "Doomed", GitHubUserID: 300002, GitHubLogin: "doomed",
				})
				panic("deliberate")
			})
		}()

		if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
			t.Errorf("expected the write rolled back, found %d user(s)", n)
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustCreateContributor(ctx context.Context, t *testing.T, db *postgres.DB, name string, githubID int64, login string) *domain.Contributor {
	t.Helper()
	var created *domain.Contributor
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		created, err = db.Users().Create(ctx, tx, &domain.Contributor{
			DisplayName:  name,
			GitHubUserID: githubID,
			GitHubLogin:  login,
		})
		return err
	})
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	return created
}

func setExpiry(ctx context.Context, t *testing.T, db *postgres.DB, id domain.UserID, expr string) {
	t.Helper()
	if _, err := db.Pool().Exec(ctx,
		`UPDATE user_availability SET expires_at = `+expr+` WHERE user_id = $1`, string(id)); err != nil {
		t.Fatalf("setting expiry: %v", err)
	}
}

func names(cs []domain.Contributor) string {
	out := ""
	for i, c := range cs {
		if i > 0 {
			out += ", "
		}
		out += c.DisplayName
	}
	return out
}
