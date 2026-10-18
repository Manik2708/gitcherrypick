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

func TestRoleStaging(t *testing.T) {
	t.Run("staging discloses nothing", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)

		if role.FirstContactedAt != nil {
			t.Error("a new role has told nobody, so it has no contact stamp")
		}

		c := mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		if c.NotifiedAt != nil {
			t.Error("staging must not notify")
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE role_id = $1`, string(role.ID)); n != 0 {
			t.Errorf("staging wrote %d contact request(s); it must write none", n)
		}
	})

	t.Run("a staged candidate can be removed and the contributor never learns", func(t *testing.T) {
		// The mis-click remedy. Removal is what makes "once contacted, never
		// removed" tolerable as a rule.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)

		if err := db.Roles().Unstage(ctx, role.ID, alice.ID); err != nil {
			t.Fatalf("unstaging: %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM role_candidates WHERE role_id = $1`, string(role.ID)); n != 0 {
			t.Errorf("expected the candidate gone, found %d", n)
		}
	})

	t.Run("a closed role takes nobody new", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustCloseRole(ctx, t, db, role.ID, hirer)

		_, err := db.Roles().Stage(ctx, &domain.RoleCandidate{
			UserID: alice.ID, AddedBy: domain.RefTo(hirer)}, role.ID)
		if err == nil {
			t.Error("staging someone for a withdrawn job would only mislead them")
		}
	})

	t.Run("a duplicate conflicts", func(t *testing.T) {
		// One row per person per job — the primary key, which is also what
		// makes the candidate list one line per human (ADR-0023).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)

		_, err := db.Roles().Stage(ctx, &domain.RoleCandidate{
			UserID: alice.ID, AddedBy: domain.RefTo(hirer)}, role.ID)
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})
}

func TestRoleContact(t *testing.T) {
	t.Run("one request per staged candidate, and the role is stamped", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustStage(ctx, t, db, role.ID, bob.ID, hirer)

		requests := mustContact(ctx, t, db, role.ID, hirer)
		if len(requests) != 2 {
			t.Fatalf("expected 2 contact requests, got %d", len(requests))
		}
		for _, cr := range requests {
			if cr.RoleID != role.ID {
				t.Errorf("a request carries role %q, want %q — the invitation can only say somebody is interested without it", cr.RoleID, role.ID)
			}
		}

		got, err := db.Roles().ByID(ctx, role.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if got.FirstContactedAt == nil {
			t.Error("expected first_contacted_at stamped")
		}

		people, err := db.Roles().Candidates(ctx, role.ID)
		if err != nil {
			t.Fatalf("listing candidates: %v", err)
		}
		for i := range people {
			if domain.Removable(&people[i]) {
				t.Errorf("%s is still removable after being contacted", people[i].DisplayName)
			}
		}
	})

	t.Run("a contacted candidate can never be removed", func(t *testing.T) {
		// The contributor was told. Deleting the row would destroy the record
		// of a disclosure that already happened.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustContact(ctx, t, db, role.ID, hirer)

		err := db.Roles().Unstage(ctx, role.ID, alice.ID)
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM role_candidates WHERE role_id = $1`, string(role.ID)); n != 1 {
			t.Error("the candidate was removed after being told")
		}
	})

	t.Run("a second press with nobody new tells nobody", func(t *testing.T) {
		// A double click must not re-email anyone.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)

		mustContact(ctx, t, db, role.ID, hirer)
		second := mustContact(ctx, t, db, role.ID, hirer)

		if len(second) != 0 {
			t.Errorf("expected the second press to tell nobody, got %d", len(second))
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE role_id = $1`, string(role.ID)); n != 1 {
			t.Errorf("expected 1 contact request, found %d", n)
		}
	})

	t.Run("somebody staged on day 3 gets an email and the others do not", func(t *testing.T) {
		// The job stays living rather than fragmenting into several lists with
		// several result dates (ADR-0008 §3a).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
		carol := mustCreateContributor(ctx, t, db, "Carol Diaz", 100003, "cdiaz")

		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustStage(ctx, t, db, role.ID, bob.ID, hirer)
		if got := mustContact(ctx, t, db, role.ID, hirer); len(got) != 2 {
			t.Fatalf("expected 2 on the first press, got %d", len(got))
		}

		mustStage(ctx, t, db, role.ID, carol.ID, hirer)
		second := mustContact(ctx, t, db, role.ID, hirer)

		if len(second) != 1 {
			t.Fatalf("expected exactly 1 new notification, got %d", len(second))
		}
		if second[0].UserID != carol.ID {
			t.Errorf("expected Carol told, got %s", second[0].UserID)
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE role_id = $1`, string(role.ID)); n != 3 {
			t.Errorf("expected 3 requests in total, found %d", n)
		}
	})

	t.Run("somebody staged after a contact is removable until THEY are told", func(t *testing.T) {
		// One rule for everybody whenever they were staged: removable until
		// contacted, permanent after.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustContact(ctx, t, db, role.ID, hirer)

		mustStage(ctx, t, db, role.ID, bob.ID, hirer)
		if err := db.Roles().Unstage(ctx, role.ID, bob.ID); err != nil {
			t.Fatalf("an untold candidate must still be removable: %v", err)
		}
		if err := db.Roles().Unstage(ctx, role.ID, alice.ID); !errors.Is(err, port.ErrConflict) {
			t.Errorf("the contacted candidate must stay: %v", err)
		}
	})

	t.Run("the promised date is what was sent, whatever happens afterwards", func(t *testing.T) {
		// Copied onto the request rather than read back through the role, so
		// nothing the company does later rewrites what somebody was told.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
		role := mustCreateOpenRole(ctx, t, db, hirer)

		first := time.Now().AddDate(0, 1, 0)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustContactBy(ctx, t, db, role.ID, hirer, first)

		// A later press promises a later date. Alice keeps hers.
		later := first.AddDate(0, 3, 0)
		mustStage(ctx, t, db, role.ID, bob.ID, hirer)
		mustContactBy(ctx, t, db, role.ID, hirer, later)

		var stored time.Time
		scanRow(ctx, t, db, []any{&stored},
			`SELECT tentative_result_date FROM contact_requests WHERE role_id = $1 AND user_id = $2`,
			string(role.ID), string(alice.ID))
		if stored.Format("2006-01-02") != first.Format("2006-01-02") {
			t.Errorf("a later contact rewrote what Alice was told: %s became %s",
				first.Format("2006-01-02"), stored.Format("2006-01-02"))
		}
	})

	t.Run("a closed role contacts nobody", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)
		mustCloseRole(ctx, t, db, role.ID, hirer)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Roles().Contact(ctx, tx, role.ID, hirer.ID, time.Now().AddDate(0, 1, 0))
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})
}

func TestRoleOverdue(t *testing.T) {
	t.Run("a role nobody was told about never counts as overdue", func(t *testing.T) {
		// It promised nothing to anyone. Keyed on first_contacted_at, which is
		// the announcement itself (ADR-0023).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		role := mustCreateOpenRole(ctx, t, db, hirer)
		mustStage(ctx, t, db, role.ID, alice.ID, hirer)

		got, err := db.Roles().OverdueRatios(ctx, time.Now())
		if err != nil {
			t.Fatalf("computing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("an unannounced role was counted: %+v", got)
		}
	})

	t.Run("flags an organization past the threshold", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		// Two contacted roles, both past their promised date: ratio 1.0. A
		// contributor may be on several jobs, so Alice is reused.
		for i := 0; i < 2; i++ {
			role := mustCreateOpenRole(ctx, t, db, hirer)
			mustStage(ctx, t, db, role.ID, alice.ID, hirer)
			mustContact(ctx, t, db, role.ID, hirer)
			// Backdated on the REQUEST, because that is what was promised.
			if _, err := db.Pool().Exec(ctx,
				`UPDATE contact_requests SET tentative_result_date = current_date - $2::int
				  WHERE role_id = $1`, string(role.ID), 10+i); err != nil {
				t.Fatalf("backdating: %v", err)
			}
		}

		got, err := db.Roles().OverdueRatios(ctx, time.Now())
		if err != nil {
			t.Fatalf("computing: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 organization, got %d", len(got))
		}
		if got[0].Ratio < postgres.OverdueFlagRatio {
			t.Errorf("expected a ratio past the %.1f threshold, got %.2f",
				postgres.OverdueFlagRatio, got[0].Ratio)
		}
		if got[0].OpenRoles != 2 {
			t.Errorf("expected 2 contacted roles counted, got %d", got[0].OpenRoles)
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustStage(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, user domain.UserID, by *domain.Hirer) *domain.RoleCandidate {
	t.Helper()
	c, err := db.Roles().Stage(ctx, &domain.RoleCandidate{
		UserID: user, AddedBy: domain.RefTo(by)}, role)
	if err != nil {
		t.Fatalf("staging a candidate: %v", err)
	}
	return c
}

func mustContact(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, by *domain.Hirer) []domain.ContactRequest {
	t.Helper()
	return mustContactBy(ctx, t, db, role, by, time.Now().AddDate(0, 1, 0))
}

func mustContactBy(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, by *domain.Hirer, resultBy time.Time) []domain.ContactRequest {
	t.Helper()
	var out []domain.ContactRequest
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.Roles().Contact(ctx, tx, role, by.ID, resultBy)
		return err
	}); err != nil {
		t.Fatalf("contacting: %v", err)
	}
	return out
}

func mustCloseRole(ctx context.Context, t *testing.T, db *postgres.DB, role domain.RoleID, by *domain.Hirer) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		_, err := db.Roles().Close(ctx, tx, role, port.RoleClosure{
			By: by.ID, At: time.Now(), Reason: domain.CloseNotNeeded,
		})
		return err
	}); err != nil {
		t.Fatalf("closing the role: %v", err)
	}
}

// mustCreateOpenRole makes an opening candidates can be staged onto.
//
// Opened, not drafted: a company may only approach people for a job it has
// actually committed to, so a draft here would fail the service check these
// repository tests sit beneath.
func mustCreateOpenRole(ctx context.Context, t *testing.T, db *postgres.DB, hirer *domain.Hirer) *domain.Role {
	t.Helper()

	var role *domain.Role
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		created, err := db.Roles().Create(ctx, tx, &domain.Role{
			OrgID:      hirer.OrganizationID,
			Title:      "Backend engineer",
			Engagement: domain.EngagementFullTime,
			Locations:  anywhere(),
			CreatedBy:  hirer.ID,
		})
		if err != nil {
			return err
		}
		role, err = db.Roles().Open(ctx, tx, created.ID, hirer.ID, time.Now())
		return err
	})
	if err != nil {
		t.Fatalf("creating a role: %v", err)
	}
	return role
}
