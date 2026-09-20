package postgres_test

import (
	"context"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// What a contributor says about themselves (ADR-0018), as opposed to what
// GitHub says. The properties worth holding are that a blank profile is a real
// state rather than an error, that a full replace really replaces, and that
// the first pull request is asked and then fixed.

func TestWorkPreferences(t *testing.T) {
	t.Run("a contributor who has never opened the form reads as blank", func(t *testing.T) {
		// Absence is not an error: everybody starts here. And the zero value is
		// the safe one — every flag false, so a profile nobody filled in
		// surfaces nobody (ADR-0018 §12).
		db, ctx := newDB(t), testContext(t)
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		got, err := db.Profiles().WorkPreferences(ctx, alice.ID)
		if err != nil {
			t.Fatalf("reading a blank profile should not fail: %v", err)
		}
		// Every preference false. Since ADR-0021 this no longer hides them
		// from hirers — the availability switch does that — but it does mean
		// their own view of what is open is unfiltered by preference, which
		// is the right default for somebody who has stated none.
		if got.OpenToRemote || got.OpenToInternships || got.OpenToOnsite ||
			got.OpenToContract || got.OpenToFreelance {
			t.Error("no preference may default to true")
		}
	})

	t.Run("a save replaces rather than merges", func(t *testing.T) {
		// The endpoint is a PUT and a form submits every field it shows. A
		// merge would leave a flag set that the person had just cleared.
		db, ctx := newDB(t), testContext(t)
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		yoe := 6

		save := func(w domain.WorkPreferences) {
			t.Helper()
			w.UserID = alice.ID
			if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				return db.Profiles().SaveWorkPreferences(ctx, tx, &w)
			}); err != nil {
				t.Fatalf("saving: %v", err)
			}
		}

		save(domain.WorkPreferences{
			OpenToRemote: true, OpenToContract: true, OpenToFreelance: true,
			CurrentCountry: "GB", OfficeYOE: &yoe,
		})
		got, err := db.Profiles().WorkPreferences(ctx, alice.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if !got.OpenToRemote || !got.OpenToContract || !got.OpenToFreelance ||
			got.CurrentCountry != "GB" {
			t.Fatalf("the first save did not land: %+v", got)
		}
		if got.OfficeYOE == nil || *got.OfficeYOE != 6 {
			t.Error("office years should round-trip")
		}

		// Now clear everything. A second row must not survive, and neither
		// must the flags from the first.
		save(domain.WorkPreferences{})
		got, err = db.Profiles().WorkPreferences(ctx, alice.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.OpenToRemote || got.OpenToContract || got.OpenToFreelance {
			t.Error("clearing every preference left one set — this was a merge, not a replace")
		}
		if got.CurrentCountry != "" || got.OfficeYOE != nil {
			t.Error("cleared fields should clear")
		}
		if n := count(t, db, `SELECT count(*) FROM user_work_preferences WHERE user_id = $1`,
			string(alice.ID)); n != 1 {
			t.Errorf("expected one row after two saves, found %d", n)
		}
	})
}

func TestCompensationRoundTrips(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

	hourly := int64(45_00)
	yearly := int64(95_000_00)
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Profiles().SaveCompensation(ctx, tx, &domain.Compensation{
			UserID: alice.ID, Currency: "GBP",
			HourlyRate: &hourly, YearlyAmount: &yearly,
		})
	}); err != nil {
		t.Fatalf("saving: %v", err)
	}

	got, err := db.Profiles().Compensation(ctx, alice.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	// Minor units, exactly. A float would have lost this by now.
	if got.HourlyRate == nil || *got.HourlyRate != 45_00 {
		t.Errorf("hourly rate = %v", got.HourlyRate)
	}
	if got.YearlyAmount == nil || *got.YearlyAmount != 95_000_00 {
		t.Errorf("yearly amount = %v", got.YearlyAmount)
	}
	if got.Currency != "GBP" {
		t.Errorf("currency = %q", got.Currency)
	}

	t.Run("nothing stated is not zero", func(t *testing.T) {
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
		blank, err := db.Profiles().Compensation(ctx, bob.ID)
		if err != nil {
			t.Fatalf("reading a blank row should not fail: %v", err)
		}
		if blank.HourlyRate != nil || blank.YearlyAmount != nil {
			t.Error("unstated expectations must be nil, not zero")
		}
	})
}

// The first pull request is ASKED and WRITE-ONCE (ADR-0018).
//
// Derived-from-claims was wrong: a first contribution is often years old in a
// repository nobody claimed here, and a claim is five PRs somebody chose as
// their BEST — not their earliest.
func TestFirstPullRequestIsWriteOnce(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

	save := func(first, latest string) {
		t.Helper()
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Profiles().SaveWorkPreferences(ctx, tx, &domain.WorkPreferences{
				UserID: alice.ID, FirstPRURL: first, LatestPRURL: latest,
			})
		}); err != nil {
			t.Fatalf("saving: %v", err)
		}
	}

	save("https://github.com/acme/platform/pull/1", "https://github.com/acme/platform/pull/9")

	// A second, different first PR must not land. The service refuses it with
	// a readable error; this asserts the column ALSO refuses it, so the
	// guarantee does not rest on anybody remembering the check.
	save("https://github.com/other/repo/pull/2", "https://github.com/acme/platform/pull/12")

	got, err := db.Profiles().WorkPreferences(ctx, alice.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if got.FirstPRURL != "https://github.com/acme/platform/pull/1" {
		t.Errorf("the first pull request was overwritten: %q", got.FirstPRURL)
	}
	// The latest one is editable, and did move.
	if got.LatestPRURL != "https://github.com/acme/platform/pull/12" {
		t.Errorf("the latest pull request should be editable, got %q", got.LatestPRURL)
	}
}

// "Never asked" and "asked, and the answer was no to everything" must not look
// alike (ADR-0018). One needs prompting; the other has already answered, and
// prompting them again is nagging somebody about a decision they made.
func TestStatedDistinguishesNeverAskedFromAnsweredNo(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

	before, err := db.Profiles().WorkPreferences(ctx, alice.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if before.Stated {
		t.Error("a contributor who has never saved must not read as having stated anything")
	}

	// Save a form with every flag false — a real answer, deliberately given.
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Profiles().SaveWorkPreferences(ctx, tx,
			&domain.WorkPreferences{UserID: alice.ID})
	}); err != nil {
		t.Fatalf("saving: %v", err)
	}

	after, err := db.Profiles().WorkPreferences(ctx, alice.ID)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if !after.Stated {
		t.Error("an answer of 'none of these' is still an answer")
	}
	// Neither has stated a preference. Only one should be prompted — which
	// is the whole of what Stated is for, now that "matchable" is gone
	// (ADR-0021 §4).
}
