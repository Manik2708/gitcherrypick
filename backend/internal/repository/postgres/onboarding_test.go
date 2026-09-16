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

// Onboarding is the one place a company comes from (ADR-0017). The property
// every test here circles is that the form creates NOTHING — no organisation,
// no seat, no reserved slug — until an administrator approves it.

func mustSubmit(ctx context.Context, t *testing.T, db *postgres.DB, name, email string) *port.OnboardingSubmission {
	t.Helper()
	var out *port.OnboardingSubmission
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.Onboarding().Submit(ctx, tx, &port.OnboardingSubmission{
			Name: name, Email: email, Description: "We build things.",
			Phone: "+44 20 7946 0958", Headcount: domain.Headcount11To50,
			Country: "GB", City: "London", PostalCode: "EC1A 1BB",
			Street1: "12 Old Street",
		})
		return err
	})
	if err != nil {
		t.Fatalf("submitting %q: %v", name, err)
	}
	return out
}

func mustProve(ctx context.Context, t *testing.T, db *postgres.DB, id domain.OnboardingID, username string) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := db.Organizations().ClaimUsername(ctx, tx, username); err != nil {
			return err
		}
		return db.Onboarding().Prove(ctx, tx, id, port.OnboardingOwner{
			Username: username, DisplayName: "Jo Mensah",
			PasswordHash: []byte("argon2id-hash"),
		}, time.Now())
	}); err != nil {
		t.Fatalf("proving %s: %v", id, err)
	}
}

func TestOnboardingCreatesNothingUntilApproved(t *testing.T) {
	t.Run("submitting reserves no slug and creates no organisation", func(t *testing.T) {
		// The form is PUBLIC. If it wrote an organisation, anyone could take
		// "acme" — or a hundred names — by typing them in and walking away.
		db, ctx := newDB(t), testContext(t)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")

		if n := count(t, db, `SELECT count(*) FROM organizations`); n != 0 {
			t.Errorf("submitting created %d organisation(s)", n)
		}
		if n := count(t, db, `SELECT count(*) FROM hirer_accounts`); n != 0 {
			t.Errorf("submitting created %d seat(s)", n)
		}
		if sub.Proven() {
			t.Error("a fresh submission must not be proven")
		}
	})

	t.Run("two companies may submit the same name", func(t *testing.T) {
		// Nothing reserves a slug, so both are legal. Whichever an
		// administrator approves first gets it.
		db, ctx := newDB(t), testContext(t)
		mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustSubmit(ctx, t, db, "Acme Corp", "jobs@acme-two.example")

		if n := count(t, db, `SELECT count(*) FROM organization_onboarding`); n != 2 {
			t.Errorf("expected both submissions, got %d", n)
		}
	})

	t.Run("an unproven submission is invisible to the queue", func(t *testing.T) {
		// An administrator must never spend attention on a company nobody can
		// reach (ADR-0017 §4).
		db, ctx := newDB(t), testContext(t)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")

		queue, err := db.Onboarding().Queue(ctx)
		if err != nil {
			t.Fatalf("reading the queue: %v", err)
		}
		if len(queue) != 0 {
			t.Fatalf("an unproven submission reached the queue: %d", len(queue))
		}

		mustProve(ctx, t, db, sub.ID, "jo")

		queue, err = db.Onboarding().Queue(ctx)
		if err != nil {
			t.Fatalf("reading the queue: %v", err)
		}
		if len(queue) != 1 {
			t.Fatalf("a proven submission should be queued, got %d", len(queue))
		}
		if queue[0].OwnerUsername != "jo" {
			t.Errorf("owner username = %q", queue[0].OwnerUsername)
		}
	})

	t.Run("the schema refuses to decide an unproven submission", func(t *testing.T) {
		// ck_onboarding_decided_after_verified. The queue query and this
		// constraint say the same thing; the constraint is the one that holds
		// when somebody writes a second query.
		db, ctx := newDB(t), testContext(t)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")

		_, err := db.Pool().Exec(ctx,
			`UPDATE organization_onboarding SET decided_at = now() WHERE id = $1`,
			string(sub.ID))
		if err == nil {
			t.Fatal("an unproven submission must not be decidable")
		}
	})

	t.Run("proving is single-use", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, sub.ID, "jo")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Onboarding().Prove(ctx, tx, sub.ID, port.OnboardingOwner{
				Username: "impostor", DisplayName: "Someone Else",
				PasswordHash: []byte("hash"),
			}, time.Now())
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a replayed proof to be refused, got %v", err)
		}
	})
}

func TestOnboardingPromotion(t *testing.T) {
	t.Run("approving creates the organisation, the address, the seat and the membership", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		admin := mustCreateAdmin(ctx, t, db)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, sub.ID, "jo")

		var org *domain.Organization
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			org, err = db.Onboarding().Promote(ctx, tx, sub.ID, "acme-corp", admin, time.Now())
			return err
		}); err != nil {
			t.Fatalf("promoting: %v", err)
		}

		if org.Slug != "acme-corp" {
			t.Errorf("slug = %q", org.Slug)
		}
		if org.Email != "hiring@acme.example" {
			t.Errorf("the company email should carry over, got %q", org.Email)
		}
		if org.Headcount != domain.Headcount11To50 {
			t.Errorf("headcount = %q", org.Headcount)
		}
		// The approval IS the verification. An organisation created unverified
		// would exist and be unable to hire, waiting on a decision that had
		// already been made.
		if !org.IsVerified() {
			t.Error("approving should verify the organisation, not merely create it")
		}
		// The owner exists, signs in by the name they chose, and carries the
		// company address (ADR-0017 §6).
		owner, err := db.Hirers().ByUsername(ctx, "jo")
		if err != nil {
			t.Fatalf("the owner should be able to sign in: %v", err)
		}
		if owner.Email != "hiring@acme.example" {
			t.Errorf("owner email = %q, want the company address", owner.Email)
		}
		if owner.OrgRole != domain.RoleOwner {
			t.Errorf("owner role = %q", owner.OrgRole)
		}
		if n := count(t, db,
			`SELECT count(*) FROM organization_addresses
			  WHERE organization_id = $1 AND is_main_office`, string(org.ID)); n != 1 {
			t.Error("the main office should have been created")
		}
	})

	t.Run("the second submission to be approved cannot have the slug", func(t *testing.T) {
		// Approval is the first admin decision the DATA can refuse
		// (ADR-0017 §Approval can fail).
		db, ctx := newDB(t), testContext(t)
		admin := mustCreateAdmin(ctx, t, db)

		first := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		second := mustSubmit(ctx, t, db, "Acme Corp", "jobs@acme-two.example")
		mustProve(ctx, t, db, first.ID, "jo")
		mustProve(ctx, t, db, second.ID, "sam")

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Onboarding().Promote(ctx, tx, first.ID, "acme-corp", admin, time.Now())
			return err
		}); err != nil {
			t.Fatalf("first promotion: %v", err)
		}

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Onboarding().Promote(ctx, tx, second.ID, "acme-corp", admin, time.Now())
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected the taken slug to refuse approval, got %v", err)
		}
		// And nothing of the second was written.
		if n := count(t, db, `SELECT count(*) FROM hirer_accounts WHERE username = 'sam'`); n != 0 {
			t.Error("a refused promotion left a seat behind")
		}
	})

	t.Run("approving twice is refused", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		admin := mustCreateAdmin(ctx, t, db)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, sub.ID, "jo")

		promote := func(slug string) error {
			return db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				_, err := db.Onboarding().Promote(ctx, tx, sub.ID, slug, admin, time.Now())
				return err
			})
		}
		if err := promote("acme-corp"); err != nil {
			t.Fatalf("first: %v", err)
		}
		if err := promote("acme-corp-2"); !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a decided submission to be refused, got %v", err)
		}
	})

	t.Run("a rejection keeps the row and creates nothing", func(t *testing.T) {
		// The row is the record of what was claimed and why it was refused,
		// and a revision points at it (ADR-0017 §8).
		db, ctx := newDB(t), testContext(t)
		admin := mustCreateAdmin(ctx, t, db)
		sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, sub.ID, "jo")

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Onboarding().Reject(ctx, tx, sub.ID, admin,
				"The address does not match the website.", time.Now())
		}); err != nil {
			t.Fatalf("rejecting: %v", err)
		}

		if n := count(t, db, `SELECT count(*) FROM organizations`); n != 0 {
			t.Error("a rejection created an organisation")
		}
		read, err := readSubmission(ctx, db, sub.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if !read.Decided() {
			t.Error("the submission should be stamped decided")
		}
		if read.DecisionReason == "" {
			t.Error("the reason is the point of a rejection")
		}
	})

	t.Run("a revision points at what it corrects", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		admin := mustCreateAdmin(ctx, t, db)
		rejected := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, rejected.ID, "jo")
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Onboarding().Reject(ctx, tx, rejected.ID, admin, "Wrong address.", time.Now())
		}); err != nil {
			t.Fatalf("rejecting: %v", err)
		}

		var revision *port.OnboardingSubmission
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			revision, err = db.Onboarding().Submit(ctx, tx, &port.OnboardingSubmission{
				Name: "Acme Corp", Email: "hiring@acme.example",
				Country: "GB", City: "Leeds", Street1: "40 New Road",
				SupersedesID: &rejected.ID,
			})
			return err
		}); err != nil {
			t.Fatalf("revising: %v", err)
		}

		// Both rows survive. An administrator reading the second attempt can
		// see what changed, which is what tells a typo from a rewrite.
		if revision.SupersedesID == nil || *revision.SupersedesID != rejected.ID {
			t.Error("the revision should name what it corrects")
		}
		if n := count(t, db, `SELECT count(*) FROM organization_onboarding`); n != 2 {
			t.Errorf("expected both rows, got %d", n)
		}
	})
}

func TestOnboardingExpiry(t *testing.T) {
	t.Run("deletes the unproven and leaves everything else", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		abandoned := mustSubmit(ctx, t, db, "Never Finished", "nobody@nowhere.example")
		proven := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
		mustProve(ctx, t, db, proven.ID, "jo")

		// Age the abandoned one past the window.
		if _, err := db.Pool().Exec(ctx,
			`UPDATE organization_onboarding SET created_at = now() - interval '2 days'`); err != nil {
			t.Fatalf("ageing: %v", err)
		}

		n, err := db.Onboarding().ExpireUnproven(ctx, time.Now().Add(-24*time.Hour))
		if err != nil {
			t.Fatalf("expiring: %v", err)
		}
		if n != 1 {
			t.Fatalf("expired %d, want 1", n)
		}
		if _, err := readSubmission(ctx, db, abandoned.ID); err == nil {
			t.Error("the abandoned submission should be gone")
		}
		if _, err := readSubmission(ctx, db, proven.ID); err != nil {
			t.Error("a proven submission must survive the sweep")
		}
	})
}

func readSubmission(ctx context.Context, db *postgres.DB, id domain.OnboardingID) (*port.OnboardingSubmission, error) {
	var out *port.OnboardingSubmission
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.Onboarding().ByID(ctx, tx, id)
		return err
	})
	return out, err
}

// The proof that unlocks a submission must survive a round trip.
//
// This is the seam nothing else crossed. Service tests mock this repository and
// the tests above write submissions directly, so `onboarding_id` could be — and
// was — present in the struct, present in the schema, and absent from every
// query here. Every onboarding proof violated ck_verification_subject, and
// POST /organizations was a 500 for everybody while check, db-test and e2e all
// stayed green.
func TestOnboardingProofCarriesItsSubmission(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	sub := mustSubmit(ctx, t, db, "Acme Corp", "hiring@acme.example")
	hash := []byte("sha256-of-the-code")

	var created *port.EmailVerification
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		created, err = db.EmailVerifications().Create(ctx, tx, &port.EmailVerification{
			Email:        "hiring@acme.example",
			Purpose:      port.VerifyOrganizationOnboarding,
			OnboardingID: &sub.ID,
			ExpiresAt:    time.Now().Add(24 * time.Hour),
		}, hash)
		return err
	}); err != nil {
		t.Fatalf("creating the proof: %v", err)
	}

	// Written.
	if created.OnboardingID == nil || *created.OnboardingID != sub.ID {
		t.Fatalf("the returned proof lost its submission: %+v", created.OnboardingID)
	}

	// And read back, by both paths the service uses. A proof whose subject
	// came back nil would be refused as "that link does not onboard an
	// organisation" — a valid code, rejected.
	var byHash *port.EmailVerification
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		byHash, err = db.EmailVerifications().ByTokenHash(ctx, tx, hash)
		return err
	}); err != nil {
		t.Fatalf("resolving by hash: %v", err)
	}
	if byHash.OnboardingID == nil || *byHash.OnboardingID != sub.ID {
		t.Error("ByTokenHash lost the submission")
	}
	if byHash.RosterID != nil {
		t.Error("an onboarding proof names no roster entry")
	}

	outstanding, err := db.EmailVerifications().Outstanding(ctx, "hiring@acme.example")
	if err != nil {
		t.Fatalf("reading the outstanding proof: %v", err)
	}
	if outstanding.OnboardingID == nil || *outstanding.OnboardingID != sub.ID {
		t.Error("Outstanding lost the submission — a resend would rotate it into nothing")
	}
}
