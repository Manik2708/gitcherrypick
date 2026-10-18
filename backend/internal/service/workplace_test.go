package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

// WorkplaceService (ADR-0024). Mocks the repositories: these assert the
// business rules — who may, in which kind of organisation, and in what order
// the refusals come — given what storage answers.

var (
	workplaceNow = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	mainOfficeID = domain.AddressID("01920000-0000-7000-8000-0000000ad001")
	austinID     = domain.AddressID("01920000-0000-7000-8000-0000000ad002")
	officeReqID  = domain.OfficeRequestID("01920000-0000-7000-8000-0000000e0001")
)

// workplaceFixture is a WorkplaceService over mocked repositories.
type workplaceFixture struct {
	orgs     *mocks.OrganizationRepository
	roles    *mocks.RoleRepository
	requests *mocks.OfficeRequestRepository
	hirers   *mocks.HirerRepository
	notifier *mocks.Notifier
	places   *mocks.PlaceService
	svc      *service.WorkplaceService
}

func newWorkplaceFixture(t *testing.T) *workplaceFixture {
	t.Helper()
	f := &workplaceFixture{
		orgs:     mocks.NewOrganizationRepository(t),
		roles:    mocks.NewRoleRepository(t),
		requests: mocks.NewOfficeRequestRepository(t),
		hirers:   mocks.NewHirerRepository(t),
		notifier: mocks.NewNotifier(t),
		places:   mocks.NewPlaceService(t),
	}
	clock := mocks.NewClock(t)
	clock.EXPECT().Now().Return(workplaceNow).Maybe()
	f.svc = service.NewWorkplaceService(f.orgs, f.roles, f.requests, f.hirers,
		f.notifier, f.places, runningTx(t), clock)
	return f
}

// runningTx is a TxManager that runs the closure, as a real one would.
func runningTx(t *testing.T) *mocks.TxManager {
	t.Helper()
	tx := mocks.NewTxManager(t)
	tx.EXPECT().InTx(mock.Anything, mock.Anything).
		RunAndReturn(func(c context.Context, fn func(context.Context, port.Tx) error) error {
			return fn(c, stubTx{})
		}).Maybe()
	return tx
}

func ownerPrincipal() domain.Principal {
	p := hirerPrincipal()
	p.Hirer.OrgRole = domain.RoleOwner
	return p
}

func memberPrincipal() domain.Principal {
	p := hirerPrincipal()
	p.Hirer.OrgRole = domain.RoleMember
	return p
}

func strangerPrincipal() domain.Principal {
	p := ownerPrincipal()
	p.Hirer.OrganizationID = "01920000-0000-7000-8000-00000000b002"
	return p
}

func lisbon() domain.Address {
	return domain.Address{Country: "pt", City: "Lisbon", PostalCode: "1100-053", Street1: "Rua Augusta 100"}
}

func pendingRequest() *domain.OfficeRequest {
	return &domain.OfficeRequest{
		ID: officeReqID, OrganizationID: orgID, OrganizationName: "Tiny Studio",
		Country: "PT", City: "Lisbon", Street1: "Rua Augusta 100",
		RequestedBy: domain.HirerRef{ID: hirerID, Username: "sam"},
		CreatedAt:   workplaceNow,
	}
}

func detailOf(t *testing.T, err error, key string) any {
	t.Helper()
	return service.DetailOf(err)[key]
}

func TestWorkplaceAddressesEnvelope(t *testing.T) {
	t.Run("only a seat of the organisation reads it", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		_, err := f.svc.Addresses(ctx(t), strangerPrincipal(), orgID)
		if !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})

	t.Run("an office organisation never reads a request", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return(domain.WorkplaceOffice, nil)
		f.orgs.EXPECT().Addresses(mock.Anything, orgID).Return([]domain.Address{{ID: mainOfficeID}}, nil)

		got, err := f.svc.Addresses(ctx(t), memberPrincipal(), orgID)
		if err != nil || got.Workplace != domain.WorkplaceOffice || len(got.Addresses) != 1 || got.OfficeRequest != nil {
			t.Fatalf("got %+v, %v", got, err)
		}
	})

	for _, c := range []envelopeCase{
		{"never asked", nil, port.ErrNotFound, false},
		{"pending is shown", pendingRequest(), nil, true},
		{"rejected is shown, reason and all", rejected(pendingRequest()), nil, true},
		{"approved is not", approved(pendingRequest()), nil, false},
	} {
		t.Run("fully remote: "+c.name, func(t *testing.T) {
			f := newWorkplaceFixture(t)
			f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return(domain.WorkplaceFullyRemote, nil)
			f.orgs.EXPECT().Addresses(mock.Anything, orgID).Return([]domain.Address{}, nil)
			f.requests.EXPECT().Latest(mock.Anything, orgID).Return(c.latest, c.err)

			got, err := f.svc.Addresses(ctx(t), memberPrincipal(), orgID)
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if (got.OfficeRequest != nil) != c.shown {
				t.Errorf("office request shown = %v, want %v", got.OfficeRequest != nil, c.shown)
			}
		})
	}

	t.Run("storage failures are wrapped, not swallowed", func(t *testing.T) {
		boom := errors.New("boom")

		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return("", boom)
		if _, err := f.svc.Addresses(ctx(t), memberPrincipal(), orgID); !errors.Is(err, boom) {
			t.Errorf("workplace: %v", err)
		}

		f = newWorkplaceFixture(t)
		f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return(domain.WorkplaceOffice, nil)
		f.orgs.EXPECT().Addresses(mock.Anything, orgID).Return(nil, boom)
		if _, err := f.svc.Addresses(ctx(t), memberPrincipal(), orgID); !errors.Is(err, boom) {
			t.Errorf("addresses: %v", err)
		}

		f = newWorkplaceFixture(t)
		f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.orgs.EXPECT().Addresses(mock.Anything, orgID).Return(nil, nil)
		f.requests.EXPECT().Latest(mock.Anything, orgID).Return(nil, boom)
		if _, err := f.svc.Addresses(ctx(t), memberPrincipal(), orgID); !errors.Is(err, boom) {
			t.Errorf("latest: %v", err)
		}
	})
}

// envelopeCase is one state of a fully remote organisation's latest request.
type envelopeCase struct {
	name   string
	latest *domain.OfficeRequest
	err    error
	shown  bool
}

func rejected(r *domain.OfficeRequest) *domain.OfficeRequest {
	r.DecidedAt, r.Reason = &workplaceNow, "Not the company's lease."
	return r
}

func approved(r *domain.OfficeRequest) *domain.OfficeRequest {
	r.DecidedAt, r.AddressID = &workplaceNow, &mainOfficeID
	return r
}

func TestAddressBodyIsValidatedAndNamed(t *testing.T) {
	for _, c := range []addressCase{
		{"no country", domain.Address{City: "Berlin", Street1: "Unter den Linden 1"}, "country"},
		{"a country name, not a code", domain.Address{Country: "Germany", City: "Berlin", Street1: "x"}, "country"},
		{"no city", domain.Address{Country: "DE", Street1: "x"}, "city"},
		{"no street", domain.Address{Country: "DE", City: "Berlin", Street1: "  "}, "street1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newWorkplaceFixture(t)
			_, err := f.svc.AddAddress(ctx(t), ownerPrincipal(), orgID, c.in)
			if service.CodeOf(err) != service.CodeInvalidAddress || detailOf(t, err, "field") != c.field {
				t.Errorf("add: %v, detail %v", err, service.DetailOf(err))
			}
			_, err = f.svc.RequestOffice(ctx(t), ownerPrincipal(), orgID, c.in)
			if service.CodeOf(err) != service.CodeInvalidAddress || detailOf(t, err, "field") != c.field {
				t.Errorf("request: %v, detail %v", err, service.DetailOf(err))
			}
		})
	}
}

// addressCase is one malformed address body and the field it is refused for.
type addressCase struct {
	name  string
	in    domain.Address
	field string
}

func TestAddAddress(t *testing.T) {
	t.Run("a member cannot", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		if _, err := f.svc.AddAddress(ctx(t), memberPrincipal(), orgID, lisbon()); !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})

	t.Run("a fully remote organisation adds none", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		_, err := f.svc.AddAddress(ctx(t), ownerPrincipal(), orgID, lisbon())
		if service.CodeOf(err) != service.CodeOrganizationFullyRemote || !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected organization_fully_remote, got %v", err)
		}
	})

	t.Run("an owner adds one, never the main office, and is named", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		f.orgs.EXPECT().AddAddress(mock.Anything, mock.Anything, mock.MatchedBy(func(a *domain.Address) bool {
			return a.OrganizationID == orgID && !a.IsMainOffice && a.Country == "PT" &&
				a.AddedBy != nil && *a.AddedBy == hirerID
		})).Return(&domain.Address{ID: austinID}, nil)

		got, err := f.svc.AddAddress(ctx(t), ownerPrincipal(), orgID, lisbon())
		if err != nil || got.ID != austinID {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

func TestRemoveAddress(t *testing.T) {
	removedAt := workplaceNow
	all := []domain.Address{
		{ID: mainOfficeID, IsMainOffice: true},
		{ID: austinID},
		{ID: "01920000-0000-7000-8000-0000000ad009", RemovedAt: &removedAt},
	}
	prepare := func(t *testing.T) *workplaceFixture {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		f.orgs.EXPECT().AllAddresses(mock.Anything, mock.Anything, orgID).Return(all, nil)
		return f
	}

	t.Run("a member cannot", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		if err := f.svc.RemoveAddress(ctx(t), memberPrincipal(), orgID, austinID); !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})

	for _, id := range []domain.AddressID{"01920000-0000-7000-8000-0000000adfff", "01920000-0000-7000-8000-0000000ad009"} {
		t.Run("not this organisation's live address: "+string(id), func(t *testing.T) {
			f := prepare(t)
			err := f.svc.RemoveAddress(ctx(t), ownerPrincipal(), orgID, id)
			if service.CodeOf(err) != service.CodeAddressNotFound || !errors.Is(err, service.ErrNotFound) {
				t.Fatalf("expected address_not_found, got %v", err)
			}
		})
	}

	t.Run("never the main office", func(t *testing.T) {
		f := prepare(t)
		err := f.svc.RemoveAddress(ctx(t), ownerPrincipal(), orgID, mainOfficeID)
		if service.CodeOf(err) != service.CodeMainOfficeNotRemovable {
			t.Fatalf("expected main_office_not_removable, got %v", err)
		}
	})

	t.Run("in use, naming the roles", func(t *testing.T) {
		f := prepare(t)
		f.roles.EXPECT().ActiveOnsiteRoles(mock.Anything, mock.Anything, orgID, &austinID).Return(
			[]domain.RoleSummary{{ID: "r1", Title: "Austin lead", Status: domain.RoleOpen}}, nil)
		err := f.svc.RemoveAddress(ctx(t), ownerPrincipal(), orgID, austinID)
		if service.CodeOf(err) != service.CodeAddressInUse || !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected address_in_use, got %v", err)
		}
		roles, _ := detailOf(t, err, "roles").([]map[string]any)
		if len(roles) != 1 || roles[0]["title"] != "Austin lead" || roles[0]["status"] != "open" {
			t.Errorf("roles = %v", detailOf(t, err, "roles"))
		}
	})

	t.Run("otherwise stamped by the owner", func(t *testing.T) {
		f := prepare(t)
		f.roles.EXPECT().ActiveOnsiteRoles(mock.Anything, mock.Anything, orgID, &austinID).Return(nil, nil)
		f.orgs.EXPECT().RemoveAddress(mock.Anything, mock.Anything, austinID, hirerID, workplaceNow).Return(nil)
		if err := f.svc.RemoveAddress(ctx(t), ownerPrincipal(), orgID, austinID); err != nil {
			t.Fatalf("unexpected %v", err)
		}
	})
}

func TestRequestOffice(t *testing.T) {
	t.Run("a member cannot", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		if _, err := f.svc.RequestOffice(ctx(t), memberPrincipal(), orgID, lisbon()); !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})

	t.Run("an organisation with an office cannot ask", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		_, err := f.svc.RequestOffice(ctx(t), ownerPrincipal(), orgID, lisbon())
		if service.CodeOf(err) != service.CodeOrganizationHasOffice {
			t.Fatalf("expected organization_has_office, got %v", err)
		}
	})

	t.Run("one waiting at a time", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.requests.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(nil, port.ErrConflict)
		_, err := f.svc.RequestOffice(ctx(t), ownerPrincipal(), orgID, lisbon())
		if service.CodeOf(err) != service.CodeOfficeRequestPending || !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected office_request_pending, got %v", err)
		}
	})

	t.Run("an owner of a fully remote organisation asks", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.requests.EXPECT().Submit(mock.Anything, mock.Anything, mock.MatchedBy(func(r *domain.OfficeRequest) bool {
			return r.OrganizationID == orgID && r.Country == "PT" && r.RequestedBy.ID == hirerID
		})).Return(pendingRequest(), nil)
		got, err := f.svc.RequestOffice(ctx(t), ownerPrincipal(), orgID, lisbon())
		if err != nil || got.ID != officeReqID {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

func TestGoFullyRemote(t *testing.T) {
	t.Run("a member cannot", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		if _, err := f.svc.GoFullyRemote(ctx(t), memberPrincipal(), orgID); !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})

	t.Run("already fully remote", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		_, err := f.svc.GoFullyRemote(ctx(t), ownerPrincipal(), orgID)
		if service.CodeOf(err) != service.CodeOrganizationFullyRemote || !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected organization_fully_remote, got %v", err)
		}
	})

	t.Run("refused while a draft or open role is onsite, naming them", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		f.roles.EXPECT().ActiveOnsiteRoles(mock.Anything, mock.Anything, orgID, (*domain.AddressID)(nil)).Return(
			[]domain.RoleSummary{{ID: "r1", Status: domain.RoleDraft}, {ID: "r2", Status: domain.RoleOpen}}, nil)
		_, err := f.svc.GoFullyRemote(ctx(t), ownerPrincipal(), orgID)
		if service.CodeOf(err) != service.CodeOnsiteRolesExist {
			t.Fatalf("expected onsite_roles_exist, got %v", err)
		}
		if roles, _ := detailOf(t, err, "roles").([]map[string]any); len(roles) != 2 {
			t.Errorf("roles = %v", detailOf(t, err, "roles"))
		}
	})

	t.Run("otherwise every address goes, and the envelope comes back", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		f.roles.EXPECT().ActiveOnsiteRoles(mock.Anything, mock.Anything, orgID, (*domain.AddressID)(nil)).Return(nil, nil)
		f.orgs.EXPECT().GoFullyRemote(mock.Anything, mock.Anything, orgID, hirerID, workplaceNow).Return(nil)
		f.orgs.EXPECT().Workplace(mock.Anything, nil, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.orgs.EXPECT().Addresses(mock.Anything, orgID).Return([]domain.Address{}, nil)
		f.requests.EXPECT().Latest(mock.Anything, orgID).Return(nil, port.ErrNotFound)

		got, err := f.svc.GoFullyRemote(ctx(t), ownerPrincipal(), orgID)
		if err != nil || got.Workplace != domain.WorkplaceFullyRemote {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

func TestOfficeRequestQueue(t *testing.T) {
	f := newWorkplaceFixture(t)
	if _, err := f.svc.OfficeRequests(ctx(t), ownerPrincipal()); err == nil {
		t.Fatal("a hirer reads nothing of the queue")
	}

	f.requests.EXPECT().Queue(mock.Anything).Return([]domain.OfficeRequest{*pendingRequest()}, nil).Once()
	got, err := f.svc.OfficeRequests(ctx(t), adminPrincipal())
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}

	f.requests.EXPECT().Queue(mock.Anything).Return(nil, errors.New("boom")).Once()
	if _, err := f.svc.OfficeRequests(ctx(t), adminPrincipal()); err == nil {
		t.Fatal("a failure is reported")
	}
}

func TestDecideOfficeRequest(t *testing.T) {
	t.Run("an administrator only", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		if _, err := f.svc.DecideOfficeRequest(ctx(t), ownerPrincipal(), officeReqID, true, ""); err == nil {
			t.Fatal("a hirer decides nothing")
		}
	})

	t.Run("a rejection needs a reason", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		_, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, false, "  ")
		if service.CodeOf(err) != service.CodeReasonRequired {
			t.Fatalf("expected reason_required, got %v", err)
		}
	})

	t.Run("an unknown request", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(nil, port.ErrNotFound)
		_, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, true, "")
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	})

	t.Run("a decided request names its standing decision", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(rejected(pendingRequest()), nil)
		_, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, true, "")
		if service.CodeOf(err) != service.CodeRequestAlreadyDecided || detailOf(t, err, "status") != "rejected" {
			t.Fatalf("expected request_already_decided/rejected, got %v %v", err, service.DetailOf(err))
		}
	})

	t.Run("a rejection is stamped and the reason emailed", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(pendingRequest(), nil)
		f.requests.EXPECT().Reject(mock.Anything, mock.Anything, officeReqID, adminID, "Not the lease.", workplaceNow).Return(nil)
		f.hirers.EXPECT().ByID(mock.Anything, hirerID).Return(&domain.Hirer{Email: "sam@tinystudio.example"}, nil)
		f.notifier.EXPECT().Send(mock.Anything, mock.MatchedBy(func(n port.Notification) bool {
			return n.Kind == port.NotifyOfficeRequestRejected && n.Recipient == "sam@tinystudio.example" &&
				n.Data["reason"] == "Not the lease."
		})).Return(nil)

		got, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, false, " Not the lease. ")
		if err != nil || got.Status != domain.OfficeRejected || got.ReviewedBy != adminID {
			t.Fatalf("got %+v, %v", got, err)
		}
	})

	t.Run("an approval writes the office and emails the country's name", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(pendingRequest(), nil)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.requests.EXPECT().Approve(mock.Anything, mock.Anything, officeReqID, adminID, workplaceNow).Return(&domain.Address{}, nil)
		f.hirers.EXPECT().ByID(mock.Anything, hirerID).Return(&domain.Hirer{Email: "sam@tinystudio.example"}, nil)
		f.places.EXPECT().Countries(mock.Anything).Return([]domain.Country{{Code: "PT", Name: "Portugal"}}, nil)
		f.notifier.EXPECT().Send(mock.Anything, mock.MatchedBy(func(n port.Notification) bool {
			return n.Kind == port.NotifyOfficeRequestApproved && n.Data["city"] == "Lisbon" &&
				n.Data["country"] == "Portugal"
		})).Return(nil)

		got, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, true, "")
		if err != nil || got.Status != domain.OfficeApproved {
			t.Fatalf("got %+v, %v", got, err)
		}
	})

	t.Run("a place service that fails leaves the code", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(pendingRequest(), nil)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceFullyRemote, nil)
		f.requests.EXPECT().Approve(mock.Anything, mock.Anything, officeReqID, adminID, workplaceNow).Return(&domain.Address{}, nil)
		f.hirers.EXPECT().ByID(mock.Anything, hirerID).Return(&domain.Hirer{Email: "sam@tinystudio.example"}, nil)
		f.places.EXPECT().Countries(mock.Anything).Return(nil, errors.New("down"))
		f.notifier.EXPECT().Send(mock.Anything, mock.MatchedBy(func(n port.Notification) bool {
			return n.Data["country"] == "PT"
		})).Return(nil)

		if _, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, true, ""); err != nil {
			t.Fatalf("unexpected %v", err)
		}
	})

	t.Run("an organisation that somehow has an office is not given a second", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(pendingRequest(), nil)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		_, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, true, "")
		if service.CodeOf(err) != service.CodeOrganizationHasOffice {
			t.Fatalf("expected organization_has_office, got %v", err)
		}
	})

	t.Run("an unreachable requester is not emailed, and the decision stands", func(t *testing.T) {
		f := newWorkplaceFixture(t)
		f.requests.EXPECT().ByID(mock.Anything, mock.Anything, officeReqID).Return(pendingRequest(), nil)
		f.requests.EXPECT().Reject(mock.Anything, mock.Anything, officeReqID, adminID, "No.", workplaceNow).Return(nil)
		f.hirers.EXPECT().ByID(mock.Anything, hirerID).Return(nil, port.ErrNotFound)
		if _, err := f.svc.DecideOfficeRequest(ctx(t), adminPrincipal(), officeReqID, false, "No."); err != nil {
			t.Fatalf("unexpected %v", err)
		}
	})
}
