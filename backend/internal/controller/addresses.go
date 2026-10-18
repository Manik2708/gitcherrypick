package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// --- shapes ------------------------------------------------------------------

// addressRequest is an address an owner adds or asks for. The address is the
// whole body, so a refusal names a top-level field (ADR-0024 amendment 1 §1.8).
type addressRequest struct {
	Country    string `json:"country"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	Street1    string `json:"street1"`
	Street2    string `json:"street2"`
}

func (b addressRequest) address() domain.Address {
	return domain.Address{
		Country: b.Country, City: b.City, PostalCode: b.PostalCode,
		Street1: b.Street1, Street2: b.Street2,
	}
}

// requestedAddressBody is an address that is not a row yet: the five parts,
// and no id, no main-office flag, no removed flag.
type requestedAddressBody struct {
	Country    string `json:"country"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	Street1    string `json:"street1"`
	Street2    string `json:"street2"`
}

func requestedAddressOf(r domain.OfficeRequest) requestedAddressBody {
	return requestedAddressBody{
		Country: r.Country, City: r.City, PostalCode: r.PostalCode,
		Street1: r.Street1, Street2: r.Street2,
	}
}

// officeRequestBody is the organisation's own view of its request (amendment
// 1 §1.3). decided_at and reason only on a rejection: a pending request has
// neither, and an approved one is never shown — its address is the main office.
type officeRequestBody struct {
	ID          domain.OfficeRequestID `json:"id"`
	Status      string                 `json:"status"`
	Address     requestedAddressBody   `json:"address"`
	RequestedAt time.Time              `json:"requested_at"`
	DecidedAt   *time.Time             `json:"decided_at,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
}

func officeRequestBodyOf(r domain.OfficeRequest) officeRequestBody {
	out := officeRequestBody{
		ID: r.ID, Status: string(r.Status()), Address: requestedAddressOf(r),
		RequestedAt: r.CreatedAt,
	}
	if r.Status() == domain.OfficeRejected {
		out.DecidedAt, out.Reason = r.DecidedAt, r.Reason
	}
	return out
}

// workplaceBody is GET /orgs/{orgID}/addresses: where a client learns the
// organisation's kind, and so whether to offer Onsite at all.
type workplaceBody struct {
	Workplace     string             `json:"workplace"`
	Addresses     []addressBody      `json:"addresses"`
	OfficeRequest *officeRequestBody `json:"office_request"`
}

func workplaceBodyOf(v port.WorkplaceView) workplaceBody {
	out := workplaceBody{
		Workplace: string(v.Workplace),
		Addresses: make([]addressBody, 0, len(v.Addresses)),
	}
	for _, a := range v.Addresses {
		out.Addresses = append(out.Addresses, addressBodyOf(a))
	}
	if v.OfficeRequest != nil {
		r := officeRequestBodyOf(*v.OfficeRequest)
		out.OfficeRequest = &r
	}
	return out
}

// --- handlers ----------------------------------------------------------------

// workplaceContext resolves the hirer and the organisation in the path.
func workplaceContext(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.OrganizationID, bool) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return domain.Principal{}, "", false
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return domain.Principal{}, "", false
	}
	return p, orgID, true
}

// writeWorkplaceError answers a refusal. Not being a seat, and being a member
// where an owner is needed, are refused as the roster refuses them.
func writeWorkplaceError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrForbidden) && service.CodeOf(err) == "" {
		writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
		return
	}
	writeError(w, err)
}

// addresses is the envelope every seat reads.
func (c *OrganizationController) addresses(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := workplaceContext(w, r)
	if !ok {
		return
	}
	view, err := c.workplace.Addresses(r.Context(), p, orgID)
	if err != nil {
		writeWorkplaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workplaceBodyOf(*view))
}

// addAddress adds a further address, live at once. 201 with the address.
func (c *OrganizationController) addAddress(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := workplaceContext(w, r)
	if !ok {
		return
	}
	var body addressRequest
	if err := decode(w, r, &body); err != nil {
		writeUndecodable(w, service.CodeInvalidAddress, err)
		return
	}
	out, err := c.workplace.AddAddress(r.Context(), p, orgID, body.address())
	if err != nil {
		writeWorkplaceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, addressBodyOf(*out))
}

// removeAddress stamps an address removed. 204: the row survives, so there is
// nothing to return and "deleted" would be untrue.
func (c *OrganizationController) removeAddress(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := workplaceContext(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "addressID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}
	if err := c.workplace.RemoveAddress(r.Context(), p, orgID, domain.AddressID(raw)); err != nil {
		writeWorkplaceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requestOffice submits a fully remote organisation's first address for
// review. 201 with the request.
func (c *OrganizationController) requestOffice(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := workplaceContext(w, r)
	if !ok {
		return
	}
	var body addressRequest
	if err := decode(w, r, &body); err != nil {
		writeUndecodable(w, service.CodeInvalidAddress, err)
		return
	}
	out, err := c.workplace.RequestOffice(r.Context(), p, orgID, body.address())
	if err != nil {
		writeWorkplaceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, officeRequestBodyOf(*out))
}

// goFullyRemote switches the organisation. No body; 200 with the envelope, so
// a client disables Onsite from the same response.
func (c *OrganizationController) goFullyRemote(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := workplaceContext(w, r)
	if !ok {
		return
	}
	view, err := c.workplace.GoFullyRemote(r.Context(), p, orgID)
	if err != nil {
		writeWorkplaceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workplaceBodyOf(*view))
}
