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

func TestAdminRepositoryAccounts(t *testing.T) {
	t.Run("a disabled admin is not found", func(t *testing.T) {
		// Disablement is enforced in the query, so it is not a rule every call
		// site has to remember.
		db, ctx := newDB(t), testContext(t)
		mustCreateAdmin(ctx, t, db)

		if _, err := db.Admins().ByEmail(ctx, "admin@example.test"); err != nil {
			t.Fatalf("reading an enabled admin: %v", err)
		}
		if _, err := db.Pool().Exec(ctx,
			`UPDATE admin_accounts SET disabled_at = now()`); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if _, err := db.Admins().ByEmail(ctx, "admin@example.test"); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected a disabled admin to be unfindable, got %v", err)
		}
	})

	t.Run("seeds only the first admin", func(t *testing.T) {
		// One statement, so two servers booting at once cannot both decide the
		// table is empty.
		db, ctx := newDB(t), testContext(t)

		created, err := db.Admins().SeedAdmin(ctx, "root@example.test", []byte("hash"))
		if err != nil {
			t.Fatalf("seeding: %v", err)
		}
		if !created {
			t.Error("expected the first seed to create an admin")
		}

		created, err = db.Admins().SeedAdmin(ctx, "second@example.test", []byte("hash"))
		if err != nil {
			t.Fatalf("second seed: %v", err)
		}
		if created {
			t.Error("a second admin was seeded")
		}
		if n := count(t, db, `SELECT count(*) FROM admin_accounts`); n != 1 {
			t.Errorf("expected exactly 1 admin, found %d", n)
		}
	})
}

func TestAdminRepositoryVerifications(t *testing.T) {
	t.Run("approving an org-scoped request verifies the organization", func(t *testing.T) {
		// ck_verification_single_subject guarantees exactly one subject, and
		// org-scoped is what makes approval lift every seat (ADR-0008 §3a).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)

		pending, err := db.Admins().PendingVerifications(ctx)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending request, got %d", len(pending))
		}
		if pending[0].OrganizationID == nil {
			t.Fatal("registration should queue an ORGANIZATION request")
		}

		mustDecideVerification(ctx, t, db, pending[0].ID, admin, true)

		org, err := db.Hirers().Organization(ctx, hirer.OrganizationID)
		if err != nil {
			t.Fatalf("reading the org: %v", err)
		}
		if !org.IsVerified() {
			t.Error("expected the organization verified")
		}
	})

	t.Run("rejecting verifies nothing", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)

		pending, _ := db.Admins().PendingVerifications(ctx)
		mustDecideVerification(ctx, t, db, pending[0].ID, admin, false)

		org, err := db.Hirers().Organization(ctx, hirer.OrganizationID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if org.IsVerified() {
			t.Error("a rejected request verified the organization")
		}
	})

	t.Run("a decision is final", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)

		pending, _ := db.Admins().PendingVerifications(ctx)
		mustDecideVerification(ctx, t, db, pending[0].ID, admin, true)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Admins().DecideVerification(ctx, tx, pending[0].ID, admin, false, "changed my mind")
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})

	t.Run("the queue holds only undecided requests", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)

		pending, _ := db.Admins().PendingVerifications(ctx)
		mustDecideVerification(ctx, t, db, pending[0].ID, admin, true)

		after, err := db.Admins().PendingVerifications(ctx)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(after) != 0 {
			t.Errorf("expected the queue drained, got %d", len(after))
		}
	})
}

func TestReevaluationRepository(t *testing.T) {
	t.Run("three rejections trigger a 28-day cooldown", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		admin := mustCreateAdmin(ctx, t, db)

		for i := 1; i <= 3; i++ {
			req := mustDispute(ctx, t, db, user.ID, claim)
			mustDecideDispute(ctx, t, db, req.ID, admin, false)

			c, err := db.Reevaluations().Cooldown(ctx, user.ID)
			if err != nil {
				t.Fatalf("reading cooldown: %v", err)
			}
			if i < 3 {
				if c.CooldownUntil != nil {
					t.Fatalf("rejection %d should not trigger a cooldown", i)
				}
				if c.RejectionCount != i {
					t.Fatalf("expected count %d, got %d", i, c.RejectionCount)
				}
			}
		}

		c, err := db.Reevaluations().Cooldown(ctx, user.ID)
		if err != nil {
			t.Fatalf("reading cooldown: %v", err)
		}
		if c.Tier != 1 {
			t.Errorf("expected tier 1, got %d", c.Tier)
		}
		if c.CanRequest(time.Now()) {
			t.Error("expected requests refused during the cooldown")
		}
		// The counter resets so the next three earn a longer wait; the tier does not.
		if c.RejectionCount != 0 {
			t.Errorf("expected the counter reset, got %d", c.RejectionCount)
		}
	})

	t.Run("acceptance touches neither counter", func(t *testing.T) {
		// A contributor who is repeatedly right is never throttled — they are
		// the population whose disputes are worth most (ADR-0007 §6).
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		admin := mustCreateAdmin(ctx, t, db)

		for range 5 {
			req := mustDispute(ctx, t, db, user.ID, claim)
			mustDecideDispute(ctx, t, db, req.ID, admin, true)
		}

		c, err := db.Reevaluations().Cooldown(ctx, user.ID)
		if err != nil {
			t.Fatalf("reading cooldown: %v", err)
		}
		if c.RejectionCount != 0 || c.Tier != 0 || c.CooldownUntil != nil {
			t.Errorf("acceptance moved the counters: %+v", c)
		}
		if !c.CanRequest(time.Now()) {
			t.Error("a contributor who is repeatedly right must never be throttled")
		}
	})

	t.Run("a mixed history counts only the rejections", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		admin := mustCreateAdmin(ctx, t, db)

		for _, accept := range []bool{false, true, false, true, false} {
			req := mustDispute(ctx, t, db, user.ID, claim)
			mustDecideDispute(ctx, t, db, req.ID, admin, accept)
		}

		c, err := db.Reevaluations().Cooldown(ctx, user.ID)
		if err != nil {
			t.Fatalf("reading cooldown: %v", err)
		}
		if c.Tier != 1 {
			t.Errorf("expected three rejections to trigger tier 1, got %d", c.Tier)
		}
	})

	t.Run("a contributor who never disputed has a zero cooldown", func(t *testing.T) {
		// "Never disputed" and "disputed and cleared" are the same answer to
		// the only question a caller asks.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		c, err := db.Reevaluations().Cooldown(ctx, user.ID)
		if err != nil {
			t.Fatalf("expected a zero value rather than an error: %v", err)
		}
		if !c.CanRequest(time.Now()) {
			t.Error("expected an unthrottled contributor")
		}
	})

	t.Run("a decision is final", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		admin := mustCreateAdmin(ctx, t, db)

		req := mustDispute(ctx, t, db, user.ID, claim)
		mustDecideDispute(ctx, t, db, req.ID, admin, false)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Reevaluations().Decide(ctx, tx, req.ID, admin, true, "")
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})

	t.Run("the queue holds only undecided disputes", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateClaim(ctx, t, db, user.ID)
		admin := mustCreateAdmin(ctx, t, db)

		// uq_reeval_open_per_claim allows one OPEN dispute per claim, so the
		// second goes on a different claim.
		other := mustCreateClaim(ctx, t, db, user.ID)
		first := mustDispute(ctx, t, db, user.ID, claim)
		mustDispute(ctx, t, db, user.ID, other)
		mustDecideDispute(ctx, t, db, first.ID, admin, true)

		pending, err := db.Reevaluations().Pending(ctx)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(pending) != 1 {
			t.Errorf("expected 1 pending dispute, got %d", len(pending))
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustDecideVerification(ctx context.Context, t *testing.T, db *postgres.DB, id domain.RequestID, admin domain.AdminID, approve bool) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Admins().DecideVerification(ctx, tx, id, admin, approve, "Reviewed.")
	}); err != nil {
		t.Fatalf("deciding verification: %v", err)
	}
}

func mustDispute(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, claim domain.ClaimID) *domain.ReevaluationRequest {
	t.Helper()
	req, err := db.Reevaluations().Create(ctx, &domain.ReevaluationRequest{
		ClaimID: claim, UserID: user, Reason: "PR 102 was judged as a dependency bump."})
	if err != nil {
		t.Fatalf("creating a dispute: %v", err)
	}
	return req
}

func mustDecideDispute(ctx context.Context, t *testing.T, db *postgres.DB, id domain.RequestID, admin domain.AdminID, accept bool) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Reevaluations().Decide(ctx, tx, id, admin, accept, "Reviewed.")
	}); err != nil {
		t.Fatalf("deciding dispute: %v", err)
	}
}
