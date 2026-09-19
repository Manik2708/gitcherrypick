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

func TestShortlistRepositoryStaging(t *testing.T) {
	t.Run("a new round is a draft and discloses nothing", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team, Q3")
		if s.Status != domain.ShortlistDraft {
			t.Errorf("expected a draft, got %s", s.Status)
		}

		e := mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		if e.NotifiedAt != nil {
			t.Error("staging must not notify")
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE shortlist_id = $1`, string(s.ID)); n != 0 {
			t.Errorf("staging wrote %d contact request(s); it must write none", n)
		}
	})

	t.Run("a staged entry can be removed and the contributor never learns", func(t *testing.T) {
		// The mis-click remedy. Removal is what makes "once shortlisted, never
		// removed" tolerable as a rule.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)

		if err := db.Shortlists().RemoveEntry(ctx, s.ID, alice.ID); err != nil {
			t.Fatalf("removing: %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM shortlist_entries WHERE shortlist_id = $1`, string(s.ID)); n != 0 {
			t.Errorf("expected the entry gone, found %d", n)
		}
	})

	t.Run("a closed round takes no new entries", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Finished")

		if _, err := db.Shortlists().Close(ctx, s.ID); err != nil {
			t.Fatalf("closing: %v", err)
		}
		_, err := db.Shortlists().AddEntry(ctx, &domain.ShortlistEntry{
			ShortlistID: s.ID, UserID: alice.ID, AddedBy: domain.RefTo(hirer)})
		if err == nil {
			t.Error("staging someone for a finished search would only mislead them")
		}
	})

	t.Run("a duplicate entry conflicts", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)

		_, err := db.Shortlists().AddEntry(ctx, &domain.ShortlistEntry{
			ShortlistID: s.ID, UserID: alice.ID, AddedBy: domain.RefTo(hirer)})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})
}

func TestShortlistRepositoryConfirm(t *testing.T) {
	t.Run("confirm writes one request per staged entry and opens the round", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustAddEntry(ctx, t, db, s.ID, bob.ID, hirer)

		requests := mustConfirm(ctx, t, db, s.ID)
		if len(requests) != 2 {
			t.Fatalf("expected 2 contact requests, got %d", len(requests))
		}

		got, err := db.Shortlists().ByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if got.Status != domain.ShortlistOpen {
			t.Errorf("expected the round open, got %s", got.Status)
		}
		if got.FirstConfirmedAt == nil {
			t.Error("expected first_confirmed_at stamped")
		}
		for _, e := range got.Entries {
			if e.Removable() {
				t.Errorf("entry %s is still removable after confirm", e.UserID)
			}
		}
	})

	t.Run("a notified entry can never be removed", func(t *testing.T) {
		// The contributor was told. Deleting the entry would destroy the
		// record of a disclosure that already happened.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustConfirm(ctx, t, db, s.ID)

		err := db.Shortlists().RemoveEntry(ctx, s.ID, alice.ID)
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM shortlist_entries WHERE shortlist_id = $1`, string(s.ID)); n != 1 {
			t.Error("the entry was removed after notification")
		}
	})

	t.Run("a second confirm with nothing new notifies nobody", func(t *testing.T) {
		// A double click must not re-email anyone.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)

		mustConfirm(ctx, t, db, s.ID)
		second := mustConfirm(ctx, t, db, s.ID)

		if len(second) != 0 {
			t.Errorf("expected the second confirm to notify nobody, got %d", len(second))
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE shortlist_id = $1`, string(s.ID)); n != 1 {
			t.Errorf("expected 1 contact request, found %d", n)
		}
	})

	t.Run("a candidate added on day 3 gets an email and the others do not", func(t *testing.T) {
		// The round stays living rather than fragmenting across several lists
		// with several result dates (ADR-0008 §3a).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
		carol := mustCreateContributor(ctx, t, db, "Carol Diaz", 100003, "cdiaz")

		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustAddEntry(ctx, t, db, s.ID, bob.ID, hirer)
		if got := mustConfirm(ctx, t, db, s.ID); len(got) != 2 {
			t.Fatalf("expected 2 on the first confirm, got %d", len(got))
		}

		mustAddEntry(ctx, t, db, s.ID, carol.ID, hirer)
		second := mustConfirm(ctx, t, db, s.ID)

		if len(second) != 1 {
			t.Fatalf("expected exactly 1 new notification, got %d", len(second))
		}
		if second[0].UserID != carol.ID {
			t.Errorf("expected Carol notified, got %s", second[0].UserID)
		}
		if n := count(t, db, `SELECT count(*) FROM contact_requests WHERE shortlist_id = $1`, string(s.ID)); n != 3 {
			t.Errorf("expected 3 requests in total, found %d", n)
		}
	})

	t.Run("an entry added after confirm is removable until IT is confirmed", func(t *testing.T) {
		// One rule for every entry whenever it was added: removable until
		// confirmed, permanent after.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustConfirm(ctx, t, db, s.ID)

		mustAddEntry(ctx, t, db, s.ID, bob.ID, hirer)
		if err := db.Shortlists().RemoveEntry(ctx, s.ID, bob.ID); err != nil {
			t.Fatalf("an unnotified entry must still be removable: %v", err)
		}
		if err := db.Shortlists().RemoveEntry(ctx, s.ID, alice.ID); !errors.Is(err, port.ErrConflict) {
			t.Errorf("the notified entry must stay: %v", err)
		}
	})

	t.Run("the promised date is copied, so a later edit cannot rewrite it", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)

		requests := mustConfirm(ctx, t, db, s.ID)
		promised := requests[0].TentativeResultDate

		later := promised.AddDate(0, 3, 0)
		if _, err := db.Shortlists().Update(ctx, s.ID, nil, nil, &later); err != nil {
			t.Fatalf("editing: %v", err)
		}

		var stored time.Time
		scanRow(ctx, t, db, []any{&stored},
			`SELECT tentative_result_date FROM contact_requests WHERE shortlist_id = $1`, string(s.ID))
		if !stored.Equal(promised) {
			t.Errorf("the edit rewrote what the contributor was told: %s became %s", promised, stored)
		}
	})

	t.Run("a closed round cannot be confirmed", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		if _, err := db.Shortlists().Close(ctx, s.ID); err != nil {
			t.Fatalf("closing: %v", err)
		}

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Shortlists().Confirm(ctx, tx, s.ID)
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})
}

func TestShortlistRepositoryOverdue(t *testing.T) {
	t.Run("a draft never counts as overdue", func(t *testing.T) {
		// It promised nothing to anyone.
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

		s := mustCreateShortlist(ctx, t, db, hirer, "Abandoned idea")
		past := time.Now().AddDate(0, 0, -30)
		if _, err := db.Shortlists().Update(ctx, s.ID, nil, nil, &past); err != nil {
			t.Fatalf("backdating: %v", err)
		}

		got, err := db.Shortlists().OverdueRatios(ctx, time.Now())
		if err != nil {
			t.Fatalf("computing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("a draft was counted: %+v", got)
		}
	})

	t.Run("flags an organization past the threshold", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		// Two open rounds, both overdue: ratio 1.0. A contributor may appear
		// on several rounds, so Alice is reused rather than duplicated.
		for i, name := range []string{"Round A", "Round B"} {
			s := mustCreateShortlist(ctx, t, db, hirer, name)
			mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
			mustConfirm(ctx, t, db, s.ID)
			past := time.Now().AddDate(0, 0, -10-i)
			if _, err := db.Shortlists().Update(ctx, s.ID, nil, nil, &past); err != nil {
				t.Fatalf("backdating %s: %v", name, err)
			}
		}

		got, err := db.Shortlists().OverdueRatios(ctx, time.Now())
		if err != nil {
			t.Fatalf("computing: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 organization, got %d", len(got))
		}
		if got[0].Ratio < postgres.OverdueFlagRatio {
			t.Errorf("expected a ratio past the %.1f threshold, got %.2f", postgres.OverdueFlagRatio, got[0].Ratio)
		}
		if got[0].Overdue != 2 || got[0].OpenShortlists != 2 {
			t.Errorf("expected 2 of 2 overdue, got %d of %d", got[0].Overdue, got[0].OpenShortlists)
		}
	})

	t.Run("a closed round drops out of the ratio", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		s := mustCreateShortlist(ctx, t, db, hirer, "Round A")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustConfirm(ctx, t, db, s.ID)
		past := time.Now().AddDate(0, 0, -10)
		if _, err := db.Shortlists().Update(ctx, s.ID, nil, nil, &past); err != nil {
			t.Fatalf("backdating: %v", err)
		}
		if _, err := db.Shortlists().Close(ctx, s.ID); err != nil {
			t.Fatalf("closing: %v", err)
		}

		got, err := db.Shortlists().OverdueRatios(ctx, time.Now())
		if err != nil {
			t.Fatalf("computing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("a finished round cannot be late: %+v", got)
		}
	})
}

// --- helpers -----------------------------------------------------------------

// The contact requests a confirm produces carry the JOB (ADR-0019 §3).
//
// Pinned here because it is a SEAM bug and nothing above can see it: the
// service mocks this repository, so a Confirm that returned requests with a
// blank role id would pass every unit test and quietly send an invitation
// saying only that somebody is interested. Which is exactly what it did.
func TestConfirmCarriesTheRole(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

	round := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
	mustAddEntry(ctx, t, db, round.ID, alice.ID, hirer)

	var requests []domain.ContactRequest
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		out, err := db.Shortlists().Confirm(ctx, tx, round.ID)
		requests = out
		return err
	}); err != nil {
		t.Fatalf("confirming: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	if requests[0].RoleID == "" {
		t.Error("the request carries no role, so the invitation can only say somebody is interested")
	}
	if requests[0].RoleID != round.RoleID {
		t.Errorf("role = %q, want the round's %q", requests[0].RoleID, round.RoleID)
	}
}

func mustCreateShortlist(ctx context.Context, t *testing.T, db *postgres.DB, hirer *domain.Hirer, name string) *domain.Shortlist {
	t.Helper()

	// A round is FOR a job (ADR-0019 §3), so one has to exist before a round
	// can name it.
	role := mustCreateOpenRole(ctx, t, db, hirer)

	s, err := db.Shortlists().Create(ctx, &domain.Shortlist{
		OrganizationID:      hirer.OrganizationID,
		RoleID:              role.ID,
		Name:                name,
		TentativeResultDate: time.Now().AddDate(0, 1, 0),
		CreatedBy:           domain.RefTo(hirer),
	})
	if err != nil {
		t.Fatalf("creating shortlist %q: %v", name, err)
	}
	return s
}

// mustCreateShortlistFor opens a second round on an EXISTING role, which is
// what "one job worked over several rounds" looks like.
func mustCreateShortlistFor(ctx context.Context, t *testing.T, db *postgres.DB, hirer *domain.Hirer, role domain.RoleID, name string) *domain.Shortlist {
	t.Helper()
	s, err := db.Shortlists().Create(ctx, &domain.Shortlist{
		OrganizationID:      hirer.OrganizationID,
		RoleID:              role,
		Name:                name,
		TentativeResultDate: time.Now().AddDate(0, 1, 0),
		CreatedBy:           domain.RefTo(hirer),
	})
	if err != nil {
		t.Fatalf("creating shortlist %q: %v", name, err)
	}
	return s
}

func mustAddEntry(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ShortlistID, user domain.UserID, by *domain.Hirer) *domain.ShortlistEntry {
	t.Helper()
	e, err := db.Shortlists().AddEntry(ctx, &domain.ShortlistEntry{
		ShortlistID: id, UserID: user, AddedBy: domain.RefTo(by)})
	if err != nil {
		t.Fatalf("staging entry: %v", err)
	}
	return e
}

func mustConfirm(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ShortlistID) []domain.ContactRequest {
	t.Helper()
	var out []domain.ContactRequest
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.Shortlists().Confirm(ctx, tx, id)
		return err
	}); err != nil {
		t.Fatalf("confirming: %v", err)
	}
	return out
}

// mustCreateOpenRole makes an opening a round can be raised against.
//
// Opened, not drafted: a shortlist may only approach people for a job the
// organisation has actually committed to, so a draft here would fail the
// service check these repository tests sit beneath.
func mustCreateOpenRole(ctx context.Context, t *testing.T, db *postgres.DB, hirer *domain.Hirer) *domain.Role {
	t.Helper()

	var role *domain.Role
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		created, err := db.Roles().Create(ctx, tx, &domain.Role{
			OrgID:      hirer.OrganizationID,
			Title:      "Backend engineer",
			Engagement: domain.EngagementFullTime,
			Location:   domain.LocationRemote,
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
