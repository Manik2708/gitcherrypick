package controller_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

const rolesPath = "/org-roles/" + orgID + "/roles"

func locatedRole() *domain.Role {
	main := bengaluru()
	main.RemovedAt = nil
	austin := domain.Address{ID: addressID, Country: "US", City: "Austin", Street1: "500 W 2nd St"}
	another := main
	another.ID = "01920000-0000-7000-8000-0000000ad003"
	return &domain.Role{
		ID: "01920000-0000-7000-8000-0000000a0001", OrgID: orgID, Title: "Field engineer",
		Engagement: domain.EngagementFullTime, Status: domain.RoleOpen,
		Locations: []domain.RoleLocation{
			{Mode: domain.ModeRemote, Country: "GB"},
			{Mode: domain.ModeOnsite, AddressID: &main.ID, Address: &main},
			// A second office in the same city reads the same to a contributor.
			{Mode: domain.ModeOnsite, AddressID: &another.ID, Address: &another},
			{Mode: domain.ModeOnsite, AddressID: &austin.ID, Address: &austin},
		},
	}
}

func TestRoleLocationsInAndOut(t *testing.T) {
	h := newHarness(t)
	h.signIn("tok", hirerPrincipal(true))

	t.Run("a hirer's entries reach the service as sent", func(t *testing.T) {
		h.roles.EXPECT().Create(mock.Anything, mock.Anything, domain.OrganizationID(orgID),
			mock.MatchedBy(func(r domain.Role) bool {
				return len(r.Locations) == 3 &&
					r.Locations[0].Mode == domain.ModeRemote && r.Locations[0].Country == "IN" &&
					r.Locations[1].AddressID != nil && *r.Locations[1].AddressID == addressID &&
					r.Locations[2].Mode == "hybrid"
			})).Return(locatedRole(), nil).Once()

		res := h.do(t, http.MethodPost, rolesPath, "tok", `{"title":"Field engineer","engagement":"full_time",
			"locations":[{"mode":"remote","country":"IN"},{"mode":"onsite","address_id":"`+addressID+`"},{"mode":"hybrid"}]}`)
		require.Equal(t, http.StatusCreated, res.Status)

		body := asMap(t, res)
		require.NotContains(t, body, "location")
		require.NotContains(t, body, "eligible_countries")
		require.Equal(t, map[string]any{"mode": "remote", "country": "GB", "address": nil}, item(t, body["locations"], 0))
		onsite := item(t, body["locations"], 1)
		require.Nil(t, onsite["country"])
		require.Equal(t, "1 MG Road", object(t, onsite["address"])["street1"],
			"a hirer reads the address in full")
	})

	for _, c := range []undecodableCase{
		{"the old location key", `{"title":"x","locations":[{"mode":"remote"}],"location":"remote"}`, "location"},
		{"the old address key", `{"title":"x","address_id":"a"}`, "address_id"},
		{"eligible countries", `{"title":"x","eligible_countries":["GB"]}`, "eligible_countries"},
		{"locations of the wrong type", `{"title":"x","locations":"remote"}`, "locations"},
		{"an entry's mode of the wrong type", `{"title":"x","locations":[{"mode":5}]}`, "locations"},
		{"a title of the wrong type", `{"title":7}`, "title"},
	} {
		t.Run("400 names "+c.field+": "+c.name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, rolesPath, "tok", c.body)
			require.Equal(t, http.StatusBadRequest, res.Status)
			require.Equal(t, map[string]any{"error": "invalid_role", "field": c.field}, asMap(t, res))
		})
	}

	t.Run("a body that does not parse names nothing", func(t *testing.T) {
		res := h.do(t, http.MethodPost, rolesPath, "tok", `{"title":`)
		require.Equal(t, http.StatusBadRequest, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_role"}, asMap(t, res))
	})

	t.Run("a location refusal carries its field and position", func(t *testing.T) {
		h.roles.EXPECT().Create(mock.Anything, mock.Anything, domain.OrganizationID(orgID), mock.Anything).Return(nil,
			service.Coded(service.ErrInvalid, service.CodeOnsiteNeedsAddress, "x").
				WithDetail(map[string]any{"field": "locations", "position": 2})).Once()
		res := h.do(t, http.MethodPost, rolesPath, "tok", `{"title":"x","locations":[{"mode":"onsite"}]}`)
		require.Equal(t, http.StatusUnprocessableEntity, res.Status)
		require.Equal(t, map[string]any{"error": "onsite_needs_address", "field": "locations", "position": float64(2)}, asMap(t, res))
	})

	t.Run("close and settings name an unknown key too", func(t *testing.T) {
		res := h.do(t, http.MethodPost, rolesPath+"/"+string(locatedRole().ID)+"/close", "tok", `{"why":"x"}`)
		require.Equal(t, map[string]any{"error": "invalid_role", "field": "why"}, asMap(t, res))
		res = h.do(t, http.MethodPut, "/org-roles/"+orgID+"/settings", "tok", `{"role_create_authority":1}`)
		require.Equal(t, map[string]any{"error": "invalid_role", "field": "role_create_authority"}, asMap(t, res))
	})
}

// undecodableCase is a body that does not decode, and the key it is refused by.
type undecodableCase struct {
	name  string
	body  string
	field string
}

func TestAContributorIsToldACityNeverAStreet(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice", contributorPrincipal())

	h.roles.EXPECT().Openings(mock.Anything, domain.UserID(aliceID), 25, 0).Return(&port.OpeningResults{
		Openings: []domain.Opening{{ID: "o1", Role: locatedRole(), OrganizationName: "Acme"}},
		Matched:  1,
	}, nil).Once()

	res := h.do(t, http.MethodGet, "/openings", "alice", "")
	require.Equal(t, http.StatusOK, res.Status)
	require.NotContains(t, string(res.Body), "MG Road")
	require.NotContains(t, string(res.Body), "560001")
	require.NotContains(t, string(res.Body), addressID)

	role := object(t, item(t, asMap(t, res)["openings"], 0)["role"])
	require.Equal(t, []any{
		map[string]any{"mode": "remote", "country": "GB"},
		map[string]any{"mode": "onsite", "country": "IN", "city": "Bengaluru"},
		map[string]any{"mode": "onsite", "country": "US", "city": "Austin"},
	}, role["locations"])

	t.Run("remote from anywhere is a null country", func(t *testing.T) {
		anywhere := locatedRole()
		anywhere.Locations = []domain.RoleLocation{{Mode: domain.ModeRemote}}
		h.roles.EXPECT().Openings(mock.Anything, domain.UserID(aliceID), 25, 0).Return(&port.OpeningResults{
			Openings: []domain.Opening{{ID: "o1", Role: anywhere}},
		}, nil).Once()
		role := object(t, item(t, asMap(t, h.do(t, http.MethodGet, "/openings", "alice", ""))["openings"], 0)["role"])
		require.Equal(t, []any{map[string]any{"mode": "remote", "country": nil}}, role["locations"])
	})
}

func TestOnboardingCarriesTheWorkplace(t *testing.T) {
	h := newHarness(t)

	t.Run("the stated choice and the address reach the service", func(t *testing.T) {
		h.onboarding.EXPECT().Submit(mock.Anything, port.OnboardingForm{
			Name: "Harbour", Email: "a@b.example", Workplace: domain.WorkplaceOffice,
			Country: "GB", City: "Cardiff", Street1: "3 Bute Crescent",
		}).Return(nil).Once()
		res := h.do(t, http.MethodPost, "/organizations", "", `{"name":"Harbour","email":"a@b.example",
			"workplace":"office","address":{"country":"GB","city":"Cardiff","street1":"3 Bute Crescent"}}`)
		require.Equal(t, http.StatusAccepted, res.Status)
	})

	t.Run("a null address is no address", func(t *testing.T) {
		h.onboarding.EXPECT().Submit(mock.Anything, port.OnboardingForm{
			Name: "Driftwood", Email: "d@e.example", Workplace: domain.WorkplaceFullyRemote,
		}).Return(nil).Once()
		res := h.do(t, http.MethodPost, "/organizations", "",
			`{"name":"Driftwood","email":"d@e.example","workplace":"fully_remote","address":null}`)
		require.Equal(t, http.StatusAccepted, res.Status)
	})

	t.Run("a refusal names its field", func(t *testing.T) {
		h.onboarding.EXPECT().Submit(mock.Anything, mock.Anything).Return(
			service.Coded(service.ErrInvalid, service.CodeInvalidOnboarding, "x").
				WithDetail(map[string]any{"field": "address.city"})).Once()
		res := h.do(t, http.MethodPost, "/organizations", "", `{"name":"x","workplace":"office"}`)
		require.Equal(t, http.StatusUnprocessableEntity, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_onboarding", "field": "address.city"}, asMap(t, res))
	})

	t.Run("and the same on revise", func(t *testing.T) {
		h.onboarding.EXPECT().Revise(mock.Anything, "code", mock.Anything).Return(
			service.Coded(service.ErrInvalid, service.CodeInvalidOnboarding, "x").
				WithDetail(map[string]any{"field": "workplace"})).Once()
		res := h.do(t, http.MethodPost, "/organizations/revise", "", `{"token":"code","name":"x"}`)
		require.Equal(t, http.StatusUnprocessableEntity, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_onboarding", "field": "workplace"}, asMap(t, res))
	})

	t.Run("the queue states the choice; a fully remote company's address is null", func(t *testing.T) {
		h.signIn("root", adminPrincipal())
		h.onboarding.EXPECT().Queue(mock.Anything, mock.Anything).Return([]port.OnboardingSubmission{
			{ID: "s1", Name: "Harbour", Workplace: domain.WorkplaceOffice, Country: "GB", City: "Cardiff"},
			{ID: "s2", Name: "Driftwood", Workplace: domain.WorkplaceFullyRemote},
		}, nil).Once()
		rows := asMap(t, h.do(t, http.MethodGet, "/admin/onboarding", "root", ""))["submissions"]
		office, remote := item(t, rows, 0), item(t, rows, 1)
		require.Equal(t, "office", office["workplace"])
		require.Equal(t, "Cardiff", object(t, office["address"])["city"])
		require.Equal(t, "fully_remote", remote["workplace"])
		require.Nil(t, remote["address"])
	})
}
