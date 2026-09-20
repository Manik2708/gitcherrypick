package postgres_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestContactRepositoryRespond(t *testing.T) {
	t.Run("accepting releases the email; declining does not", func(t *testing.T) {
		// The consent model. email_released_at records that release was
		// AUTHORISED and when — the audit fact, not the address.
		db, ctx := newDB(t), testContext(t)
		req := mustPendingContact(ctx, t, db)

		accepted := mustRespond(ctx, t, db, req.ID, true)
		if accepted.Status != domain.ContactAccepted {
			t.Errorf("expected accepted, got %s", accepted.Status)
		}
		if accepted.EmailReleasedAt == nil {
			t.Error("expected the email release stamped")
		}

		other := mustPendingContact(ctx, t, db)
		declined := mustRespond(ctx, t, db, other.ID, false)
		if declined.Status != domain.ContactDeclined {
			t.Errorf("expected declined, got %s", declined.Status)
		}
		if declined.EmailReleasedAt != nil {
			t.Error("declining must not release an email")
		}
		if declined.RespondedAt == nil {
			t.Error("expected a decline to be recorded")
		}
	})

	t.Run("answering twice conflicts", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		req := mustPendingContact(ctx, t, db)
		mustRespond(ctx, t, db, req.ID, false)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Contacts().Respond(ctx, tx, req.ID, true, time.Now())
			return err
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected a decided request to be unmatched, got %v", err)
		}
	})
}

func TestContactRepositoryVisibility(t *testing.T) {
	t.Run("a contributor sees only their own requests", func(t *testing.T) {
		// Nothing about shortlist membership is visible beyond the requests
		// addressed to them (ADR-0005).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		s := mustCreateShortlist(ctx, t, db, hirer, "Platform team")
		mustAddEntry(ctx, t, db, s.ID, alice.ID, hirer)
		mustAddEntry(ctx, t, db, s.ID, bob.ID, hirer)
		mustConfirm(ctx, t, db, s.ID)

		got, err := db.Contacts().ListForUser(ctx, alice.ID, nil)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected Alice to see 1 request, got %d", len(got))
		}
		if got[0].UserID != alice.ID {
			t.Error("Alice saw a request addressed to someone else")
		}
	})

	t.Run("filters by status", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		req := mustPendingContact(ctx, t, db)
		mustRespond(ctx, t, db, req.ID, true)

		pending := domain.ContactPending
		got, err := db.Contacts().ListForUser(ctx, req.UserID, &pending)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected no pending requests, got %d", len(got))
		}
	})
}

func TestContactRepositoryExpireStale(t *testing.T) {
	t.Run("lapses pending requests only", func(t *testing.T) {
		// An accepted request whose expiry passes is not expired: the email
		// was released, and a clock cannot undo that.
		db, ctx := newDB(t), testContext(t)
		pending := mustPendingContact(ctx, t, db)
		accepted := mustPendingContact(ctx, t, db)
		mustRespond(ctx, t, db, accepted.ID, true)

		if _, err := db.Pool().Exec(ctx,
			`UPDATE contact_requests SET expires_at = now() - interval '1 day'`); err != nil {
			t.Fatalf("backdating: %v", err)
		}

		n, err := db.Contacts().ExpireStale(ctx, time.Now())
		if err != nil {
			t.Fatalf("expiring: %v", err)
		}
		if n != 1 {
			t.Errorf("expected exactly 1 expired, got %d", n)
		}

		got, err := db.Contacts().ByID(ctx, pending.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.Status != domain.ContactExpired {
			t.Errorf("expected the pending one expired, got %s", got.Status)
		}

		still, err := db.Contacts().ByID(ctx, accepted.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if still.Status != domain.ContactAccepted {
			t.Errorf("an accepted request was expired: %s", still.Status)
		}
	})
}

func TestSavedSearchRepository(t *testing.T) {
	t.Run("filters round-trip on the ADR-0008 parameter names", func(t *testing.T) {
		// The stored keys ARE the query parameters, so a saved search replays
		// as a query string with no translation layer (ADR-0008 §5).
		db, ctx := newDB(t), testContext(t)
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

		score := 50.0
		saved, err := db.SavedSearches().Create(ctx, &domain.SavedSearch{
			OrganizationID: hirer.OrganizationID,
			Name:           "Go + Kubernetes, available",
			CreatedBy:      domain.RefTo(hirer),
			Filters: domain.SearchQuery{
				Skills:        []string{"go", "kubernetes"},
				MinSkillScore: &score,
				OpenTo:        []string{"remote"},
			},
		})
		if err != nil {
			t.Fatalf("creating: %v", err)
		}

		got, err := db.SavedSearches().ByID(ctx, saved.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if len(got.Filters.Skills) != 2 || got.Filters.Skills[0] != "go" {
			t.Errorf("skills did not survive the round trip: %+v", got.Filters.Skills)
		}
		if got.Filters.MinSkillScore == nil || *got.Filters.MinSkillScore != score {
			t.Errorf("min_skill_score did not survive: %+v", got.Filters.MinSkillScore)
		}

		// The stored JSON must use parameter names, not Go field names.
		var raw string
		scanRow(ctx, t, db, []any{&raw},
			`SELECT filters::text FROM saved_searches WHERE id = $1`, string(saved.ID))
		for _, key := range []string{`"skills"`, `"min_skill_score"`, `"open_to"`} {
			if !strings.Contains(raw, key) {
				t.Errorf("expected %s in the stored filters, got %s", key, raw)
			}
		}
	})

	t.Run("is org-scoped", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		acme := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		studio := mustRegister(ctx, t, db, "sam@studio.example", "Sam", "Tiny Studio", "tiny-studio")

		if _, err := db.SavedSearches().Create(ctx, &domain.SavedSearch{
			OrganizationID: acme.OrganizationID, Name: "Acme's", CreatedBy: domain.RefTo(acme),
			Filters: domain.SearchQuery{Skills: []string{"go"}},
		}); err != nil {
			t.Fatalf("creating: %v", err)
		}

		got, err := db.SavedSearches().ListByOrganization(ctx, studio.OrganizationID)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("a competitor saw %d saved search(es)", len(got))
		}
	})

	t.Run("deleting an unknown search reports not found", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		err := db.SavedSearches().Delete(ctx, "01920000-0000-7000-8000-00000000dead")
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})
}

// --- helpers -----------------------------------------------------------------

// mustPendingContact stages and confirms one candidate, yielding a pending
// request. Each call uses a fresh contributor so requests do not collide on
// uq_contact_request.
var contactSeq int64

func mustPendingContact(ctx context.Context, t *testing.T, db *postgres.DB) *domain.ContactRequest {
	t.Helper()
	contactSeq++
	hirer := mustRegister(ctx, t, db,
		"hirer"+strconv.FormatInt(contactSeq, 10)+"@acme.example", "Hirer", "Org "+strconv.FormatInt(contactSeq, 10), "org-"+strconv.FormatInt(contactSeq, 10))
	user := mustCreateContributor(ctx, t, db,
		"Contributor "+strconv.FormatInt(contactSeq, 10), 400000+contactSeq, "c"+strconv.FormatInt(contactSeq, 10))

	s := mustCreateShortlist(ctx, t, db, hirer, "Round "+strconv.FormatInt(contactSeq, 10))
	mustAddEntry(ctx, t, db, s.ID, user.ID, hirer)
	requests := mustConfirm(ctx, t, db, s.ID)
	if len(requests) != 1 {
		t.Fatalf("expected 1 contact request, got %d", len(requests))
	}
	return &requests[0]
}

func mustRespond(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ContactID, accept bool) *domain.ContactRequest {
	t.Helper()
	var cr *domain.ContactRequest
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		cr, err = db.Contacts().Respond(ctx, tx, id, accept, time.Now())
		return err
	}); err != nil {
		t.Fatalf("responding: %v", err)
	}
	return cr
}
