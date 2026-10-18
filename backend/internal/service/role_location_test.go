package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

// RoleService and ADR-0024: where a role's work is, the order its refusals
// come in, and the field every invalid_role names.

// roleFixture is a RoleService over mocked repositories.
type roleFixture struct {
	roles    *mocks.RoleRepository
	settings *mocks.OrgSettingsRepository
	orgs     *mocks.OrganizationRepository
	users    *mocks.UserRepository
	hirers   *mocks.HirerRepository
	notifier *mocks.Notifier
	access   *mocks.AccessService
	places   *mocks.PlaceService
	svc      *service.RoleService
}

func newRoleFixture(t *testing.T) *roleFixture {
	t.Helper()
	f := &roleFixture{
		roles:    mocks.NewRoleRepository(t),
		settings: mocks.NewOrgSettingsRepository(t),
		orgs:     mocks.NewOrganizationRepository(t),
		users:    mocks.NewUserRepository(t),
		hirers:   mocks.NewHirerRepository(t),
		notifier: mocks.NewNotifier(t),
		access:   mocks.NewAccessService(t),
		places:   mocks.NewPlaceService(t),
	}
	defaults := domain.DefaultOrgSettings(orgID)
	f.settings.EXPECT().Settings(mock.Anything, orgID).Return(&defaults, nil).Maybe()

	clock := mocks.NewClock(t)
	clock.EXPECT().Now().Return(workplaceNow).Maybe()
	f.svc = service.NewRoleService(f.roles, mocks.NewOpeningRepository(t),
		mocks.NewSkillRepository(t), f.settings, mocks.NewContactRepository(t),
		mocks.NewProfileRepository(t), f.users, f.orgs,
		f.hirers, f.notifier, f.access, f.places, runningTx(t), clock)
	return f
}

// organisation makes the organisation's kind and addresses what the
// transaction reads.
func (f *roleFixture) organisation(w domain.Workplace, addresses ...domain.Address) {
	f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(w, nil).Maybe()
	f.orgs.EXPECT().AllAddresses(mock.Anything, mock.Anything, orgID).Return(addresses, nil).Maybe()
}

func roleWith(locs ...domain.RoleLocation) domain.Role {
	return domain.Role{Title: "Platform engineer", Engagement: domain.EngagementFullTime, Locations: locs}
}

func remote(country string) domain.RoleLocation {
	return domain.RoleLocation{Mode: domain.ModeRemote, Country: country}
}

func onsite(id domain.AddressID) domain.RoleLocation {
	return domain.RoleLocation{Mode: domain.ModeOnsite, AddressID: &id}
}

func TestLocationRefusalsInPrecedenceOrder(t *testing.T) {
	removedAt := workplaceNow
	live := domain.Address{ID: mainOfficeID, Country: "IN", City: "Bengaluru", IsMainOffice: true}
	gone := domain.Address{ID: austinID, Country: "US", City: "Austin", RemovedAt: &removedAt}
	nobody := domain.AddressID("01920000-0000-7000-8000-0000000adfff")
	hybrid := domain.RoleLocation{Mode: "hybrid"}
	remoteAt := remote("")
	remoteAt.AddressID = &live.ID
	onsiteIn := onsite(live.ID)
	onsiteIn.Country = "IN"

	for _, c := range []locationCase{
		{"none at all", domain.WorkplaceOffice, nil, service.CodeRoleLocationRequired, 0},
		{"a mode that is neither", domain.WorkplaceOffice, []domain.RoleLocation{remote("IN"), hybrid}, service.CodeRoleLocationInvalid, 2},
		{"remote at an address", domain.WorkplaceOffice, []domain.RoleLocation{remoteAt}, service.CodeRoleLocationInvalid, 1},
		{"onsite with a country", domain.WorkplaceOffice, []domain.RoleLocation{remote("GB"), onsiteIn}, service.CodeRoleLocationInvalid, 2},
		{"a country of three letters", domain.WorkplaceOffice, []domain.RoleLocation{remote("IND")}, service.CodeRoleLocationInvalid, 1},
		{"onsite in a fully remote organisation, whatever the address", domain.WorkplaceFullyRemote, []domain.RoleLocation{remote("PT"), onsite(nobody)}, service.CodeOrganizationFullyRemote, 2},
		{"onsite with no address", domain.WorkplaceOffice, []domain.RoleLocation{remote("GB"), {Mode: domain.ModeOnsite}}, service.CodeOnsiteNeedsAddress, 2},
		{"an address nobody holds", domain.WorkplaceOffice, []domain.RoleLocation{onsite(nobody)}, service.CodeAddressNotFound, 1},
		{"a removed address", domain.WorkplaceOffice, []domain.RoleLocation{remote("US"), onsite(gone.ID)}, service.CodeAddressRemoved, 2},
		{"anywhere, then a country: the later is named", domain.WorkplaceOffice, []domain.RoleLocation{remote(""), remote("IN")}, service.CodeRemoteAnywhereExclusive, 2},
		{"a country, a repeat, then anywhere", domain.WorkplaceOffice, []domain.RoleLocation{remote("IN"), remote("in"), remote("")}, service.CodeRemoteAnywhereExclusive, 3},
		{"an entry's own fault outranks exclusivity", domain.WorkplaceOffice, []domain.RoleLocation{remote(""), remote("IN"), onsite(nobody)}, service.CodeAddressNotFound, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRoleFixture(t)
			f.organisation(c.workplace, live, gone)

			_, err := f.svc.Create(ctx(t), ownerPrincipal(), orgID, roleWith(c.locations...))
			if service.CodeOf(err) != c.code || !errors.Is(err, service.ErrInvalid) {
				t.Fatalf("expected %s, got %v", c.code, err)
			}
			detail := service.DetailOf(err)
			if detail["field"] != "locations" {
				t.Errorf("field = %v", detail["field"])
			}
			if c.position == 0 {
				if _, has := detail["position"]; has {
					t.Errorf("an empty list names no entry, got %v", detail["position"])
				}
			} else if detail["position"] != c.position {
				t.Errorf("position = %v, want %d", detail["position"], c.position)
			}
		})
	}
}

// locationCase is one way a set of entries is wrong, and what names it.
type locationCase struct {
	name      string
	workplace domain.Workplace
	locations []domain.RoleLocation
	code      string
	position  int
}

func TestLocationsAreNormalisedAndCollapsed(t *testing.T) {
	f := newRoleFixture(t)
	live := domain.Address{ID: mainOfficeID, Country: "IN", City: "Bengaluru"}
	f.organisation(domain.WorkplaceOffice, live)

	f.roles.EXPECT().Create(mock.Anything, mock.Anything, mock.MatchedBy(func(r *domain.Role) bool {
		// Six sent, three kept, remote first: 'in' and 'IN' are one country,
		// the office twice is one entry.
		return len(r.Locations) == 3 &&
			r.Locations[0].Country == "DE" && r.Locations[1].Country == "IN" &&
			r.Locations[2].Address != nil && r.Locations[2].Address.City == "Bengaluru"
	})).Return(&domain.Role{ID: "r1"}, nil)

	_, err := f.svc.Create(ctx(t), ownerPrincipal(), orgID, roleWith(
		onsite(live.ID), remote(" in "), remote("DE"), remote("IN"), onsite(live.ID)))
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
}

func TestLocationsAreCheckedOnEveryWrite(t *testing.T) {
	draft := &domain.Role{ID: "r1", OrgID: orgID, Status: domain.RoleDraft, Locations: []domain.RoleLocation{onsite(mainOfficeID)}}
	open := &domain.Role{ID: "r2", OrgID: orgID, Status: domain.RoleOpen}

	t.Run("a draft's update", func(t *testing.T) {
		f := newRoleFixture(t)
		f.organisation(domain.WorkplaceFullyRemote)
		f.roles.EXPECT().ByID(mock.Anything, draft.ID).Return(draft, nil)
		_, err := f.svc.Update(ctx(t), ownerPrincipal(), orgID, draft.ID, roleWith(onsite(mainOfficeID)))
		if service.CodeOf(err) != service.CodeOrganizationFullyRemote {
			t.Fatalf("expected organization_fully_remote, got %v", err)
		}
	})

	t.Run("a revision", func(t *testing.T) {
		f := newRoleFixture(t)
		f.organisation(domain.WorkplaceOffice)
		f.roles.EXPECT().ByID(mock.Anything, open.ID).Return(open, nil)
		_, err := f.svc.Revise(ctx(t), ownerPrincipal(), orgID, open.ID, roleWith())
		if service.CodeOf(err) != service.CodeRoleLocationRequired {
			t.Fatalf("expected role_location_required, got %v", err)
		}
	})

	t.Run("opening a draft whose address has since been removed", func(t *testing.T) {
		removedAt := workplaceNow
		f := newRoleFixture(t)
		f.organisation(domain.WorkplaceOffice, domain.Address{ID: mainOfficeID, RemovedAt: &removedAt})
		f.roles.EXPECT().ByID(mock.Anything, draft.ID).Return(draft, nil)
		_, err := f.svc.Open(ctx(t), ownerPrincipal(), orgID, draft.ID)
		if service.CodeOf(err) != service.CodeAddressRemoved {
			t.Fatalf("expected address_removed, got %v", err)
		}
	})

	t.Run("opening a draft that is still sound", func(t *testing.T) {
		f := newRoleFixture(t)
		f.organisation(domain.WorkplaceOffice, domain.Address{ID: mainOfficeID})
		f.roles.EXPECT().ByID(mock.Anything, draft.ID).Return(draft, nil)
		f.roles.EXPECT().Open(mock.Anything, mock.Anything, draft.ID, hirerID, workplaceNow).Return(draft, nil)
		if _, err := f.svc.Open(ctx(t), ownerPrincipal(), orgID, draft.ID); err != nil {
			t.Fatalf("unexpected %v", err)
		}
	})

	t.Run("a lock that cannot be taken is an error, not a pass", func(t *testing.T) {
		f := newRoleFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return("", errors.New("boom"))
		if _, err := f.svc.Create(ctx(t), ownerPrincipal(), orgID, roleWith(remote(""))); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("addresses that cannot be read are an error", func(t *testing.T) {
		f := newRoleFixture(t)
		f.orgs.EXPECT().LockWorkplace(mock.Anything, mock.Anything, orgID).Return(domain.WorkplaceOffice, nil)
		f.orgs.EXPECT().AllAddresses(mock.Anything, mock.Anything, orgID).Return(nil, errors.New("boom"))
		if _, err := f.svc.Create(ctx(t), ownerPrincipal(), orgID, roleWith(remote(""))); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestEveryInvalidRoleNamesItsField(t *testing.T) {
	zero, negative, over := int64(0), int64(-1), int64(200)
	ctc := int64(100)
	hours, noRounds, years := 169, 0, 81
	minusDays, minusYears := -1, -1

	for _, c := range []fieldCase{
		{"no title", func(r *domain.Role) { r.Title = "   " }, "title", 0},
		{"an engagement", func(r *domain.Role) { r.Engagement = "permanent" }, "engagement", 0},
		{"a currency", func(r *domain.Role) { r.YearlyCTC = &ctc; r.Currency = "pounds" }, "currency", 0},
		{"a package", func(r *domain.Role) { r.Currency = "GBP"; r.YearlyCTC = &zero }, "yearly_ctc", 0},
		{"a base", func(r *domain.Role) { r.Currency = "GBP"; r.YearlyCTC = &ctc; r.YearlyBase = &negative }, "yearly_base", 0},
		{"base above package", func(r *domain.Role) { r.Currency = "GBP"; r.YearlyCTC = &ctc; r.YearlyBase = &over }, "yearly_base", 0},
		{"an hourly rate", func(r *domain.Role) {
			r.Engagement = domain.EngagementFreelance
			r.Currency = "GBP"
			r.HourlyRate = &negative
		}, "hourly_rate", 0},
		{"hours", func(r *domain.Role) { r.Engagement = domain.EngagementFreelance; r.ExpectedHours = &hours }, "expected_hours", 0},
		{"office years", func(r *domain.Role) { r.MinOfficeYOE = &minusYears }, "min_office_yoe", 0},
		{"open-source years", func(r *domain.Role) { r.MinOSSYOE = &years }, "min_oss_yoe", 0},
		{"rounds", func(r *domain.Role) { r.MaxInterviewRounds = &noRounds }, "max_interview_rounds", 0},
		{"days", func(r *domain.Role) { r.AvgDaysToOffer = &minusDays }, "avg_days_to_offer", 0},
		{"a question, by position", func(r *domain.Role) {
			r.Questions = []domain.RoleQuestion{{Question: "Visa?"}, {Question: " "}}
		}, "questions", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRoleFixture(t)
			in := roleWith(remote(""))
			c.shape(&in)
			_, err := f.svc.Create(ctx(t), ownerPrincipal(), orgID, in)
			if service.CodeOf(err) != service.CodeInvalidRole {
				t.Fatalf("expected invalid_role, got %v", err)
			}
			detail := service.DetailOf(err)
			if detail["field"] != c.field {
				t.Errorf("field = %v, want %s", detail["field"], c.field)
			}
			if c.position > 0 && detail["position"] != c.position {
				t.Errorf("position = %v, want %d", detail["position"], c.position)
			}
		})
	}
}

// fieldCase is one wrong field on a role and the key it is named by.
type fieldCase struct {
	name     string
	shape    func(*domain.Role)
	field    string
	position int
}

func TestCloseAndSettingsNameTheirField(t *testing.T) {
	open := &domain.Role{ID: "r2", OrgID: orgID, Status: domain.RoleOpen}

	for _, c := range []closeCase{
		{"a reason not offered", port.CloseRequest{Reason: domain.CloseSuperseded}, "reason"},
		{"other, and nothing said", port.CloseRequest{Reason: domain.CloseOther, Note: " "}, "note"},
		{"hires under another reason", port.CloseRequest{Reason: domain.CloseHiredElsewhere, Hired: []domain.UserID{userID}}, "hired"},
		{"hired here, naming nobody", port.CloseRequest{Reason: domain.CloseHiredViaPlatform}, "hired"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRoleFixture(t)
			f.roles.EXPECT().ByID(mock.Anything, open.ID).Return(open, nil)
			_, err := f.svc.Close(ctx(t), ownerPrincipal(), orgID, open.ID, c.in)
			if service.CodeOf(err) != service.CodeInvalidRole || service.DetailOf(err)["field"] != c.field {
				t.Fatalf("expected invalid_role on %s, got %v %v", c.field, err, service.DetailOf(err))
			}
		})
	}

	t.Run("hired here, naming only blanks", func(t *testing.T) {
		f := newRoleFixture(t)
		f.roles.EXPECT().ByID(mock.Anything, open.ID).Return(open, nil)
		f.roles.EXPECT().Candidates(mock.Anything, open.ID).Return(nil, nil)
		_, err := f.svc.Close(ctx(t), ownerPrincipal(), orgID, open.ID,
			port.CloseRequest{Reason: domain.CloseHiredViaPlatform, Hired: []domain.UserID{""}})
		if service.DetailOf(err)["field"] != "hired" {
			t.Fatalf("expected hired, got %v", err)
		}
	})

	for _, c := range []settingsCase{
		{"create", domain.OrgSettings{RoleCreateAuthority: "anyone", RoleUpdateAuthority: domain.AuthorityAnyHirer, RoleCloseAuthority: domain.AuthorityAnyHirer}, "role_create_authority"},
		{"update", domain.OrgSettings{RoleCreateAuthority: domain.AuthorityAnyHirer, RoleUpdateAuthority: "x", RoleCloseAuthority: domain.AuthorityAnyHirer}, "role_update_authority"},
		{"close", domain.OrgSettings{RoleCreateAuthority: domain.AuthorityAnyHirer, RoleUpdateAuthority: domain.AuthorityAnyHirer, RoleCloseAuthority: "owner"}, "role_close_authority"},
	} {
		t.Run("settings: "+c.name, func(t *testing.T) {
			f := newRoleFixture(t)
			_, err := f.svc.SaveSettings(ctx(t), ownerPrincipal(), orgID, c.in)
			if service.DetailOf(err)["field"] != c.field {
				t.Fatalf("expected %s, got %v", c.field, err)
			}
		})
	}
}

// closeCase is one wrong closure and the key it is named by.
type closeCase struct {
	name  string
	in    port.CloseRequest
	field string
}

// settingsCase is one wrong authority and the key it is named by.
type settingsCase struct {
	name  string
	in    domain.OrgSettings
	field string
}

// The contact email says where the job is in one phrase, at the detail a
// contributor may see: a city and a country's NAME, never a street
// (ADR-0024 §Bodies).
func TestContactEmailNamesCitiesNotStreets(t *testing.T) {
	f := newRoleFixture(t)
	main := domain.Address{ID: mainOfficeID, Country: "IN", City: "Bengaluru", Street1: "1 MG Road"}
	role := &domain.Role{ID: "r1", OrgID: orgID, Status: domain.RoleOpen, Title: "Field engineer",
		Engagement: domain.EngagementFullTime,
		Locations: []domain.RoleLocation{
			{Mode: domain.ModeRemote, Country: "GB"},
			{Mode: domain.ModeOnsite, AddressID: &main.ID, Address: &main},
		}}
	request := domain.ContactRequest{ID: "c1", RoleID: role.ID, UserID: userID, OrganizationID: orgID}

	f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
	f.roles.EXPECT().ByID(mock.Anything, role.ID).Return(role, nil)
	f.roles.EXPECT().Candidates(mock.Anything, role.ID).Return(nil, nil)
	f.roles.EXPECT().Contact(mock.Anything, mock.Anything, role.ID, hirerID, mock.Anything).
		Return([]domain.ContactRequest{request}, nil)
	f.users.EXPECT().ByID(mock.Anything, userID).Return(&domain.Contributor{Email: "alice@example.com"}, nil)
	f.hirers.EXPECT().Organization(mock.Anything, orgID).Return(verifiedOrg(), nil)
	f.places.EXPECT().Countries(mock.Anything).Return([]domain.Country{{Code: "IN", Name: "India"}}, nil)
	f.notifier.EXPECT().Send(mock.Anything, mock.MatchedBy(func(n port.Notification) bool {
		return n.Data["role_location"] == "remote within GB, or onsite in Bengaluru, India"
	})).Return(nil)

	if _, err := f.svc.Contact(ctx(t), ownerPrincipal(), orgID, role.ID, workplaceNow.Add(72*time.Hour)); err != nil {
		t.Fatalf("unexpected %v", err)
	}
}
