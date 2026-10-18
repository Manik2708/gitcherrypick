package controller_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// Controller tests for ADR-0024's IO: routes, status codes, payload shapes
// and the field a refusal names. The services are mocked, so nothing here
// asserts a business outcome — only how one is read and written.

const (
	addressPath  = "/orgs/" + orgID + "/addresses"
	officeID     = "01920000-0000-7000-8000-0000000e0001"
	addressID    = "01920000-0000-7000-8000-0000000ad002"
	officeDecide = "/admin/office-requests/" + officeID + "/decide"
)

func bengaluru() domain.Address {
	removed := fixedTime()
	return domain.Address{
		ID: "01920000-0000-7000-8000-0000000ad001", Country: "IN", City: "Bengaluru",
		PostalCode: "560001", Street1: "1 MG Road", Street2: "Floor 4",
		IsMainOffice: true, RemovedAt: &removed,
	}
}

func officeRequest(decided bool) *domain.OfficeRequest {
	r := &domain.OfficeRequest{
		ID: officeID, OrganizationID: orgID, OrganizationName: "Tiny Studio",
		Country: "PT", City: "Lisbon", PostalCode: "1100-053", Street1: "Rua Augusta 100",
		RequestedBy: domain.HirerRef{Username: "sam"}, CreatedAt: fixedTime(),
	}
	if decided {
		at := fixedTime()
		r.DecidedAt, r.Reason = &at, "Not the company's lease."
	}
	return r
}

func asMap(t *testing.T, r response) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &out), "body was %s", r.Body)
	return out
}

// object is v as a JSON object, failing the test when it is not one.
func object(t *testing.T, v any) map[string]any {
	t.Helper()
	out, ok := v.(map[string]any)
	require.True(t, ok, "not an object: %v", v)
	return out
}

// item is entry i of v as a JSON object, failing when v is not a list.
func item(t *testing.T, v any, i int) map[string]any {
	t.Helper()
	list, ok := v.([]any)
	require.True(t, ok, "not a list: %v", v)
	require.Greater(t, len(list), i)
	return object(t, list[i])
}

func TestAddressesEnvelopeShape(t *testing.T) {
	h := newHarness(t)
	h.signIn("tok", hirerPrincipal(true))

	h.workplace.EXPECT().Addresses(mock.Anything, mock.Anything, domain.OrganizationID(orgID)).Return(
		&port.WorkplaceView{
			Workplace:     domain.WorkplaceFullyRemote,
			Addresses:     []domain.Address{bengaluru()},
			OfficeRequest: officeRequest(true),
		}, nil).Once()

	body := asMap(t, h.do(t, http.MethodGet, addressPath, "tok", ""))
	require.Equal(t, "fully_remote", body["workplace"])

	address := item(t, body["addresses"], 0)
	require.Equal(t, true, address["removed"])
	require.Equal(t, true, address["is_main_office"])
	require.Len(t, address, 8, "the address object everywhere a hirer meets one")

	request := object(t, body["office_request"])
	require.Equal(t, "rejected", request["status"])
	require.Equal(t, "Not the company's lease.", request["reason"])
	require.Contains(t, request, "decided_at")
	require.Len(t, request["address"], 5)

	t.Run("a pending request carries neither a decision date nor a reason", func(t *testing.T) {
		h.workplace.EXPECT().Addresses(mock.Anything, mock.Anything, domain.OrganizationID(orgID)).Return(
			&port.WorkplaceView{Workplace: domain.WorkplaceFullyRemote, OfficeRequest: officeRequest(false)}, nil).Once()
		body := asMap(t, h.do(t, http.MethodGet, addressPath, "tok", ""))
		request := object(t, body["office_request"])
		require.Equal(t, "pending", request["status"])
		require.NotContains(t, request, "decided_at")
		require.NotContains(t, request, "reason")
		require.Equal(t, []any{}, body["addresses"])
	})

	t.Run("null when there is none", func(t *testing.T) {
		h.workplace.EXPECT().Addresses(mock.Anything, mock.Anything, domain.OrganizationID(orgID)).Return(
			&port.WorkplaceView{Workplace: domain.WorkplaceOffice}, nil).Once()
		body := asMap(t, h.do(t, http.MethodGet, addressPath, "tok", ""))
		require.Nil(t, body["office_request"])
	})

	t.Run("not a seat is refused as the roster refuses", func(t *testing.T) {
		h.workplace.EXPECT().Addresses(mock.Anything, mock.Anything, domain.OrganizationID(orgID)).Return(
			nil, service.ErrForbidden).Once()
		res := h.do(t, http.MethodGet, addressPath, "tok", "")
		require.Equal(t, http.StatusForbidden, res.Status)
		require.Equal(t, service.CodeNotAnOrgMember, res.errorCode(t))
	})

	t.Run("a malformed organisation id is 400", func(t *testing.T) {
		res := h.do(t, http.MethodGet, "/orgs/nope/addresses", "tok", "")
		require.Equal(t, http.StatusBadRequest, res.Status)
	})
}

func TestAddressWrites(t *testing.T) {
	h := newHarness(t)
	h.signIn("tok", hirerPrincipal(true))
	org := domain.OrganizationID(orgID)
	body := `{"country":"DE","city":"Berlin","postal_code":"10117","street1":"Unter den Linden 1","street2":"Haus B"}`

	t.Run("add: 201 with the address", func(t *testing.T) {
		h.workplace.EXPECT().AddAddress(mock.Anything, mock.Anything, org, domain.Address{
			Country: "DE", City: "Berlin", PostalCode: "10117", Street1: "Unter den Linden 1", Street2: "Haus B",
		}).Return(&domain.Address{ID: addressID, Country: "DE", City: "Berlin"}, nil).Once()
		res := h.do(t, http.MethodPost, addressPath, "tok", body)
		require.Equal(t, http.StatusCreated, res.Status)
		require.Equal(t, addressID, asMap(t, res)["id"])
	})

	t.Run("add: a refusal names its field", func(t *testing.T) {
		h.workplace.EXPECT().AddAddress(mock.Anything, mock.Anything, org, mock.Anything).Return(nil,
			service.Coded(service.ErrInvalid, service.CodeInvalidAddress, "x").
				WithDetail(map[string]any{"field": "city"})).Once()
		res := h.do(t, http.MethodPost, addressPath, "tok", `{"country":"DE"}`)
		require.Equal(t, http.StatusUnprocessableEntity, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_address", "field": "city"}, asMap(t, res))
	})

	t.Run("add: an unknown key is named", func(t *testing.T) {
		res := h.do(t, http.MethodPost, addressPath, "tok", `{"county":"Kent"}`)
		require.Equal(t, http.StatusBadRequest, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_address", "field": "county"}, asMap(t, res))
	})

	t.Run("add: a fully remote organisation is 409", func(t *testing.T) {
		h.workplace.EXPECT().AddAddress(mock.Anything, mock.Anything, org, mock.Anything).Return(nil,
			service.Coded(service.ErrConflict, service.CodeOrganizationFullyRemote, "x")).Once()
		res := h.do(t, http.MethodPost, addressPath, "tok", body)
		require.Equal(t, http.StatusConflict, res.Status)
		require.Equal(t, service.CodeOrganizationFullyRemote, res.errorCode(t))
	})

	t.Run("remove: 204", func(t *testing.T) {
		h.workplace.EXPECT().RemoveAddress(mock.Anything, mock.Anything, org, domain.AddressID(addressID)).Return(nil).Once()
		res := h.do(t, http.MethodDelete, addressPath+"/"+addressID, "tok", "")
		require.Equal(t, http.StatusNoContent, res.Status)
	})

	t.Run("remove: not found is 404", func(t *testing.T) {
		h.workplace.EXPECT().RemoveAddress(mock.Anything, mock.Anything, org, domain.AddressID(addressID)).Return(
			service.Coded(service.ErrNotFound, service.CodeAddressNotFound, "x")).Once()
		res := h.do(t, http.MethodDelete, addressPath+"/"+addressID, "tok", "")
		require.Equal(t, http.StatusNotFound, res.Status)
		require.Equal(t, service.CodeAddressNotFound, res.errorCode(t))
	})

	t.Run("remove: in use names the roles", func(t *testing.T) {
		h.workplace.EXPECT().RemoveAddress(mock.Anything, mock.Anything, org, domain.AddressID(addressID)).Return(
			service.Coded(service.ErrConflict, service.CodeAddressInUse, "x").WithDetail(map[string]any{
				"roles": []map[string]any{{"id": "r1", "title": "Austin lead", "status": "open"}},
			})).Once()
		res := h.do(t, http.MethodDelete, addressPath+"/"+addressID, "tok", "")
		require.Equal(t, http.StatusConflict, res.Status)
		require.Len(t, asMap(t, res)["roles"], 1)
	})

	t.Run("remove: a malformed address id is 400", func(t *testing.T) {
		res := h.do(t, http.MethodDelete, addressPath+"/nope", "tok", "")
		require.Equal(t, http.StatusBadRequest, res.Status)
		require.Equal(t, service.CodeInvalidID, res.errorCode(t))
	})

	t.Run("office request: 201 with the request", func(t *testing.T) {
		h.workplace.EXPECT().RequestOffice(mock.Anything, mock.Anything, org, mock.Anything).Return(officeRequest(false), nil).Once()
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/office-request", "tok", body)
		require.Equal(t, http.StatusCreated, res.Status)
		got := asMap(t, res)
		require.Equal(t, officeID, got["id"])
		require.Equal(t, "pending", got["status"])
		require.Contains(t, got, "requested_at")
	})

	t.Run("office request: undecodable", func(t *testing.T) {
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/office-request", "tok", `{`)
		require.Equal(t, http.StatusBadRequest, res.Status)
		require.Equal(t, map[string]any{"error": "invalid_address"}, asMap(t, res))
	})

	t.Run("office request: a member is refused", func(t *testing.T) {
		h.workplace.EXPECT().RequestOffice(mock.Anything, mock.Anything, org, mock.Anything).Return(nil, service.ErrForbidden).Once()
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/office-request", "tok", body)
		require.Equal(t, http.StatusForbidden, res.Status)
		require.Equal(t, service.CodeNotAnOrgMember, res.errorCode(t))
	})

	t.Run("fully remote: 200 with the envelope", func(t *testing.T) {
		h.workplace.EXPECT().GoFullyRemote(mock.Anything, mock.Anything, org).Return(
			&port.WorkplaceView{Workplace: domain.WorkplaceFullyRemote}, nil).Once()
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/fully-remote", "tok", "")
		require.Equal(t, http.StatusOK, res.Status)
		require.Equal(t, map[string]any{"workplace": "fully_remote", "addresses": []any{}, "office_request": nil}, asMap(t, res))
	})

	t.Run("fully remote: onsite roles are 409", func(t *testing.T) {
		h.workplace.EXPECT().GoFullyRemote(mock.Anything, mock.Anything, org).Return(nil,
			service.Coded(service.ErrConflict, service.CodeOnsiteRolesExist, "x")).Once()
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/fully-remote", "tok", "")
		require.Equal(t, http.StatusConflict, res.Status)
	})

	t.Run("a contributor reaches none of it", func(t *testing.T) {
		h.signIn("alice", contributorPrincipal())
		res := h.do(t, http.MethodPost, "/orgs/"+orgID+"/fully-remote", "alice", "")
		require.Equal(t, http.StatusForbidden, res.Status)
	})
}

func TestOfficeRequestAdminRoutes(t *testing.T) {
	h := newHarness(t)
	h.signIn("root", adminPrincipal())

	t.Run("the queue", func(t *testing.T) {
		second := officeRequest(false)
		previous := domain.OfficeRequestID("01920000-0000-7000-8000-0000000e0000")
		second.Supersedes = &previous
		h.workplace.EXPECT().OfficeRequests(mock.Anything, mock.Anything).Return(
			[]domain.OfficeRequest{*officeRequest(false), *second}, nil).Once()

		body := asMap(t, h.do(t, http.MethodGet, "/admin/office-requests", "root", ""))
		require.EqualValues(t, 2, body["total"])
		first := item(t, body["office_requests"], 0)
		require.Equal(t, "sam", first["requested_by"])
		require.Equal(t, map[string]any{"id": orgID, "name": "Tiny Studio"}, first["organization"])
		require.NotContains(t, first, "supersedes")
		require.Equal(t, string(previous), item(t, body["office_requests"], 1)["supersedes"])
	})

	t.Run("the queue fails loudly", func(t *testing.T) {
		h.workplace.EXPECT().OfficeRequests(mock.Anything, mock.Anything).Return(nil, service.ErrForbidden).Once()
		res := h.do(t, http.MethodGet, "/admin/office-requests", "root", "")
		require.Equal(t, http.StatusForbidden, res.Status)
	})

	t.Run("decide: the shared envelope and the shared body", func(t *testing.T) {
		h.workplace.EXPECT().DecideOfficeRequest(mock.Anything, mock.Anything,
			domain.OfficeRequestID(officeID), false, "Not the lease.").Return(&port.OfficeDecision{
			ID: officeID, Status: domain.OfficeRejected, ReviewedAt: fixedTime(), ReviewedBy: adminID,
		}, nil).Once()
		res := h.do(t, http.MethodPost, officeDecide, "root", `{"decision":"rejected","reason":"Not the lease."}`)
		require.Equal(t, http.StatusOK, res.Status)
		got := asMap(t, res)
		require.Equal(t, "rejected", got["status"])
		require.Equal(t, adminID, got["reviewed_by"])
		require.Equal(t, officeID, got["id"])
	})

	t.Run("decide: already decided names the standing decision", func(t *testing.T) {
		h.workplace.EXPECT().DecideOfficeRequest(mock.Anything, mock.Anything,
			domain.OfficeRequestID(officeID), true, "").Return(nil,
			service.Coded(service.ErrConflict, service.CodeRequestAlreadyDecided, "x").
				WithDetail(map[string]any{"status": "rejected"})).Once()
		res := h.do(t, http.MethodPost, officeDecide, "root", `{"decision":"approved"}`)
		require.Equal(t, http.StatusConflict, res.Status)
		require.Equal(t, map[string]any{"error": "request_already_decided", "status": "rejected"}, asMap(t, res))
	})

	for _, c := range []decideCase{
		{"a malformed id", "/admin/office-requests/nope/decide", `{"decision":"approved"}`, http.StatusBadRequest, service.CodeInvalidID},
		{"an undecodable body", officeDecide, `{"approve": true}`, http.StatusUnprocessableEntity, service.CodeInvalidState},
		{"an unknown decision", officeDecide, `{"decision":"maybe"}`, http.StatusUnprocessableEntity, service.CodeInvalidState},
	} {
		t.Run("decide: "+c.name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, c.path, "root", c.body)
			require.Equal(t, c.status, res.Status)
			require.Equal(t, c.code, res.errorCode(t))
		})
	}
}

// decideCase is one request the shared decide handler refuses before the
// service is reached.
type decideCase struct {
	name   string
	path   string
	body   string
	status int
	code   string
}
