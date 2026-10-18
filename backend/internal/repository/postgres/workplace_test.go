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

// ADR-0024 at the storage layer: the mapping between the domain's addresses,
// location entries and office requests and the rows that hold them. The rules
// about WHEN each may be written live in the service; what is held here is
// that each write lands as the schema means it and reads back the same.

func TestApprovalIsKeyedOnTheStatedWorkplace(t *testing.T) {
	db, ctx := newDB(t), testContext(t)

	office := mustRegisterOffice(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	remote := mustRegister(ctx, t, db, "sam@studio.example", "Sam", "Studio", "studio")

	t.Run("an office organisation gets exactly its main office", func(t *testing.T) {
		if office.Organization.Workplace != domain.WorkplaceOffice {
			t.Errorf("workplace = %q", office.Organization.Workplace)
		}
		got, err := db.Organizations().Addresses(ctx, office.OrganizationID)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 1 || !got[0].IsMainOffice || got[0].City != "Bengaluru" ||
			got[0].PostalCode != "560001" || got[0].AddedBy != nil || got[0].Removed() {
			t.Errorf("addresses = %+v", got)
		}
	})

	t.Run("a fully remote one gets none", func(t *testing.T) {
		w, err := db.Organizations().Workplace(ctx, nil, remote.OrganizationID)
		if err != nil || w != domain.WorkplaceFullyRemote {
			t.Errorf("workplace = %q, %v", w, err)
		}
		got, err := db.Organizations().AllAddresses(ctx, nil, remote.OrganizationID)
		if err != nil || len(got) != 0 {
			t.Errorf("addresses = %+v, %v", got, err)
		}
	})

	t.Run("the submission round-trips its choice", func(t *testing.T) {
		queue, err := db.Onboarding().Queue(ctx)
		if err != nil {
			t.Fatalf("queue: %v", err)
		}
		if len(queue) != 0 {
			t.Errorf("both were decided, queue = %d", len(queue))
		}
	})
}

func TestAddressesAreAddedListedAndStamped(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	owner := mustRegisterOffice(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	org := owner.OrganizationID

	austin := mustAddAddress(ctx, t, db, owner, "US", "Austin")
	berlin := mustAddAddress(ctx, t, db, owner, "DE", "Berlin")

	if austin.IsMainOffice || austin.AddedBy == nil || *austin.AddedBy != owner.ID {
		t.Errorf("a later address is never the main office and names who added it: %+v", austin)
	}

	t.Run("live addresses, main office first, then in the order added", func(t *testing.T) {
		got, err := db.Organizations().Addresses(ctx, org)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 3 || !got[0].IsMainOffice || got[1].ID != austin.ID || got[2].ID != berlin.ID {
			t.Errorf("order = %+v", got)
		}
	})

	t.Run("removal is a stamp, not a delete", func(t *testing.T) {
		at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().RemoveAddress(ctx, tx, austin.ID, owner.ID, at)
		}); err != nil {
			t.Fatalf("removing: %v", err)
		}

		live, _ := db.Organizations().Addresses(ctx, org)
		if len(live) != 2 {
			t.Errorf("austin should have left the picker: %+v", live)
		}
		all, _ := db.Organizations().AllAddresses(ctx, nil, org)
		var found *domain.Address
		for i := range all {
			if all[i].ID == austin.ID {
				found = &all[i]
			}
		}
		if found == nil || !found.Removed() || found.RemovedBy == nil || *found.RemovedBy != owner.ID {
			t.Errorf("the removed row survives, stamped: %+v", found)
		}
	})

	t.Run("an address already removed is not removed twice", func(t *testing.T) {
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().RemoveAddress(ctx, tx, austin.ID, owner.ID, time.Now())
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Errorf("expected not found, got %v", err)
		}
	})
}

func TestLocationEntriesRoundTripWithTheirAddress(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	owner := mustRegisterOffice(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	org := owner.OrganizationID
	main := mustMainOffice(ctx, t, db, org)
	austin := mustAddAddress(ctx, t, db, owner, "US", "Austin")

	role := mustCreateRole(ctx, t, db, owner, func(r *domain.Role) {
		r.Locations = []domain.RoleLocation{
			{Mode: domain.ModeOnsite, AddressID: &austin.ID},
			{Mode: domain.ModeRemote, Country: "IN"},
			{Mode: domain.ModeOnsite, AddressID: &main.ID},
			{Mode: domain.ModeRemote, Country: "DE"},
		}
	})

	t.Run("remote first, then by country and city, each address embedded", func(t *testing.T) {
		got := role.Locations
		if len(got) != 4 {
			t.Fatalf("locations = %+v", got)
		}
		if got[0].Country != "DE" || got[1].Country != "IN" {
			t.Errorf("remote entries out of order: %+v", got[:2])
		}
		if got[2].Address == nil || got[2].Address.City != "Bengaluru" ||
			got[3].Address == nil || got[3].Address.City != "Austin" {
			t.Errorf("onsite entries out of order or unresolved: %+v", got[2:])
		}
		if got[2].Country != "" {
			t.Error("an onsite entry names no country of its own")
		}
	})

	t.Run("a draft and an open role block; a closed one does not", func(t *testing.T) {
		open := mustCreateRole(ctx, t, db, owner, func(r *domain.Role) {
			r.Title = "Austin lead"
			r.Locations = []domain.RoleLocation{{Mode: domain.ModeOnsite, AddressID: &austin.ID}}
		})
		mustOpen(ctx, t, db, open.ID, owner)

		blocking := mustActiveOnsite(ctx, t, db, org, &austin.ID)
		if len(blocking) != 2 || blocking[0].ID != role.ID || blocking[1].Status != domain.RoleOpen {
			t.Errorf("blocking = %+v", blocking)
		}

		mustClose(ctx, t, db, open.ID, owner, domain.CloseNotNeeded)
		if got := mustActiveOnsite(ctx, t, db, org, &austin.ID); len(got) != 1 {
			t.Errorf("a closed role blocks nothing: %+v", got)
		}
		if got := mustActiveOnsite(ctx, t, db, org, nil); len(got) != 1 || got[0].ID != role.ID {
			t.Errorf("any onsite entry, at any address: %+v", got)
		}
	})

	t.Run("a draft's update replaces the set", func(t *testing.T) {
		role.Locations = anywhere()
		updated := mustUpdateDraft(ctx, t, db, role)
		if len(updated.Locations) != 1 || !updated.Locations[0].Anywhere() {
			t.Errorf("locations = %+v", updated.Locations)
		}
	})

	t.Run("going fully remote stamps every address and flips the kind", func(t *testing.T) {
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			w, err := db.Organizations().LockWorkplace(ctx, tx, org)
			if err != nil || w != domain.WorkplaceOffice {
				t.Errorf("locked workplace = %q, %v", w, err)
			}
			return db.Organizations().GoFullyRemote(ctx, tx, org, owner.ID, time.Now())
		}); err != nil {
			t.Fatalf("switching: %v", err)
		}
		if live, _ := db.Organizations().Addresses(ctx, org); len(live) != 0 {
			t.Errorf("every address leaves the picker: %+v", live)
		}
		if w, _ := db.Organizations().Workplace(ctx, nil, org); w != domain.WorkplaceFullyRemote {
			t.Errorf("workplace = %q", w)
		}

		// The closed role still says where it was.
		closed, err := db.Roles().ByID(ctx, mustActiveOrClosed(ctx, t, db, org, "Austin lead"))
		if err != nil {
			t.Fatalf("reading the closed role: %v", err)
		}
		if len(closed.Locations) != 1 || closed.Locations[0].Address == nil || !closed.Locations[0].Address.Removed() {
			t.Errorf("a closed role renders its removed address: %+v", closed.Locations)
		}
	})

	t.Run("an unknown organisation is not found", func(t *testing.T) {
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().GoFullyRemote(ctx, tx, "01920000-0000-7000-8000-0000000fffff", owner.ID, time.Now())
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Errorf("expected not found, got %v", err)
		}
	})
}

func TestOfficeRequestsAreDecidedOnce(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	owner := mustRegister(ctx, t, db, "sam@studio.example", "Sam", "Studio", "studio")
	org := owner.OrganizationID
	admin := mustCreateAdmin(ctx, t, db)

	if _, err := db.OfficeRequests().Latest(ctx, org); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("no request yet, got %v", err)
	}

	first := mustRequestOffice(ctx, t, db, owner, "Lisbon")
	if first.Status() != domain.OfficePending || first.RequestedBy.Username == "" ||
		first.OrganizationName != "Studio" || first.PostalCode != "1100-053" {
		t.Errorf("first = %+v", first)
	}

	t.Run("one waiting at a time", func(t *testing.T) {
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.OfficeRequests().Submit(ctx, tx, officeRequestFor(owner, "Porto"))
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Errorf("expected a conflict, got %v", err)
		}
	})

	t.Run("a rejection stamps the request and writes no address", func(t *testing.T) {
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			locked, err := db.OfficeRequests().ByID(ctx, tx, first.ID)
			if err != nil || locked.ID != first.ID {
				t.Errorf("locked = %+v, %v", locked, err)
			}
			return db.OfficeRequests().Reject(ctx, tx, first.ID, admin, "Not the company's lease.", time.Now())
		}); err != nil {
			t.Fatalf("rejecting: %v", err)
		}
		latest, err := db.OfficeRequests().Latest(ctx, org)
		if err != nil || latest.Status() != domain.OfficeRejected || latest.Reason != "Not the company's lease." {
			t.Errorf("latest = %+v, %v", latest, err)
		}
		if all, _ := db.Organizations().AllAddresses(ctx, nil, org); len(all) != 0 {
			t.Errorf("a rejection writes nothing: %+v", all)
		}
		err = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.OfficeRequests().Reject(ctx, tx, first.ID, admin, "again", time.Now())
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Errorf("a decided request is not decided again, got %v", err)
		}
	})

	second := mustRequestOffice(ctx, t, db, owner, "Porto")

	t.Run("the queue is undecided, oldest first, and names what it supersedes", func(t *testing.T) {
		queue, err := db.OfficeRequests().Queue(ctx)
		if err != nil {
			t.Fatalf("queue: %v", err)
		}
		if len(queue) != 1 || queue[0].ID != second.ID {
			t.Fatalf("queue = %+v", queue)
		}
		if queue[0].Supersedes == nil || *queue[0].Supersedes != first.ID {
			t.Errorf("supersedes = %v, want %s", queue[0].Supersedes, first.ID)
		}
	})

	t.Run("approval writes the main office and flips the kind", func(t *testing.T) {
		var address *domain.Address
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			address, err = db.OfficeRequests().Approve(ctx, tx, second.ID, admin, time.Now())
			return err
		}); err != nil {
			t.Fatalf("approving: %v", err)
		}
		if !address.IsMainOffice || address.City != "Porto" || address.AddedBy == nil || *address.AddedBy != owner.ID {
			t.Errorf("address = %+v", address)
		}
		if w, _ := db.Organizations().Workplace(ctx, nil, org); w != domain.WorkplaceOffice {
			t.Errorf("workplace = %q", w)
		}
		latest, _ := db.OfficeRequests().Latest(ctx, org)
		if latest.Status() != domain.OfficeApproved || latest.DecidedBy == nil {
			t.Errorf("latest = %+v", latest)
		}

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.OfficeRequests().Approve(ctx, tx, second.ID, admin, time.Now())
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Errorf("approving twice should conflict, got %v", err)
		}
	})
}

// TestOpeningsReadEachLocationEntry pins the per-entry gate of GET /openings
// (ADR-0024 decision 5): a full-time role onsite in Bengaluru and remote in
// India, read by contributors who differ in where they are and what they tick.
func TestOpeningsReadEachLocationEntry(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	owner := mustRegisterOffice(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	main := mustMainOffice(ctx, t, db, owner.OrganizationID)

	role := mustCreateRole(ctx, t, db, owner, func(r *domain.Role) {
		r.Locations = []domain.RoleLocation{
			{Mode: domain.ModeOnsite, AddressID: &main.ID},
			{Mode: domain.ModeRemote, Country: "IN"},
		}
	})
	mustOpen(ctx, t, db, role.ID, owner)
	saveOpening(ctx, t, db, role.ID, owner.ID, domain.Opening{})
	publishOpening(ctx, t, db, role.ID, owner.ID)

	person := mustCreateContributor(ctx, t, db, "Asha Rao", 100101, "asha")
	for _, row := range []openingRow{
		{"in India, remote only", "IN", true, false, 1},
		{"in Germany, remote only", "DE", true, false, 0},
		{"in Germany, onsite", "DE", false, true, 1},
		{"no country, onsite", "", false, true, 1},
		{"no country, remote only", "", true, false, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			saveWorkPreferences(ctx, t, db, person.ID, domain.WorkPreferences{
				OpenToRemote: row.remote, OpenToOnsite: row.onsite, CurrentCountry: row.country,
			})
			got, err := db.Openings().Matching(ctx, port.OpeningMatch{UserID: person.ID})
			if err != nil {
				t.Fatalf("matching: %v", err)
			}
			if got.Matched != row.matched || got.Matched+got.Missed != 1 {
				t.Errorf("matched = %d, missed = %d, want matched %d", got.Matched, got.Missed, row.matched)
			}
		})
	}
}

// openingRow is one contributor of the worked example in RFC-0024 §2.
type openingRow struct {
	name    string
	country string
	remote  bool
	onsite  bool
	matched int
}

// --- helpers -----------------------------------------------------------------

func mustAddAddress(ctx context.Context, t *testing.T, db *postgres.DB, owner *domain.Hirer, country, city string) *domain.Address {
	t.Helper()
	var out *domain.Address
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.Organizations().AddAddress(ctx, tx, &domain.Address{
			OrganizationID: owner.OrganizationID, Country: country, City: city,
			Street1: "1 High Street", AddedBy: &owner.ID,
		})
		return err
	}); err != nil {
		t.Fatalf("adding %s: %v", city, err)
	}
	return out
}

func mustMainOffice(ctx context.Context, t *testing.T, db *postgres.DB, org domain.OrganizationID) domain.Address {
	t.Helper()
	live, err := db.Organizations().Addresses(ctx, org)
	if err != nil || len(live) == 0 || !live[0].IsMainOffice {
		t.Fatalf("no main office: %+v, %v", live, err)
	}
	return live[0]
}

func mustActiveOnsite(ctx context.Context, t *testing.T, db *postgres.DB, org domain.OrganizationID, at *domain.AddressID) []domain.RoleSummary {
	t.Helper()
	out, err := db.Roles().ActiveOnsiteRoles(ctx, nil, org, at)
	if err != nil {
		t.Fatalf("listing onsite roles: %v", err)
	}
	return out
}

// mustActiveOrClosed finds a role of the organisation by title.
func mustActiveOrClosed(ctx context.Context, t *testing.T, db *postgres.DB, org domain.OrganizationID, title string) domain.RoleID {
	t.Helper()
	roles, err := db.Roles().ListByOrganization(ctx, org, nil)
	if err != nil {
		t.Fatalf("listing roles: %v", err)
	}
	for _, r := range roles {
		if r.Title == title {
			return r.ID
		}
	}
	t.Fatalf("no role titled %q", title)
	return ""
}

func officeRequestFor(owner *domain.Hirer, city string) *domain.OfficeRequest {
	return &domain.OfficeRequest{
		OrganizationID: owner.OrganizationID, Country: "PT", City: city,
		PostalCode: "1100-053", Street1: "Rua Augusta 100",
		RequestedBy: domain.RefTo(owner),
	}
}

func mustRequestOffice(ctx context.Context, t *testing.T, db *postgres.DB, owner *domain.Hirer, city string) *domain.OfficeRequest {
	t.Helper()
	var out *domain.OfficeRequest
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = db.OfficeRequests().Submit(ctx, tx, officeRequestFor(owner, city))
		return err
	}); err != nil {
		t.Fatalf("requesting an office in %s: %v", city, err)
	}
	return out
}
