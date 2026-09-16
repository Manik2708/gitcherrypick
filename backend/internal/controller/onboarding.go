package controller

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// OrganizationsController serves /organizations — the public organisation
// surface (ADR-0017, ADR-0016 §3a).
//
// Every route here is UNAUTHENTICATED, and that is the design rather than an
// oversight: the person describing a company has no account, and onboarding is
// what eventually produces one. Nothing these routes can do creates anything —
// an administrator approving a submission is what does.
//
// Distinct from OrganizationController, which serves an owner acting on their
// own organisation at /orgs. Same noun, opposite audience, and keeping them
// apart is what stops an authenticated read leaking onto a public route by
// being one method away.
type OrganizationsController struct {
	redemption port.RedemptionService
	onboarding port.OnboardingService
}

// NewOrganizationsController wires the public organisation surface.
func NewOrganizationsController(redemption port.RedemptionService, onboarding port.OnboardingService) *OrganizationsController {
	return &OrganizationsController{redemption: redemption, onboarding: onboarding}
}

var _ port.Controller = (*OrganizationsController)(nil)

// Routes mounts /organizations.
func (c *OrganizationsController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/", c.list)
	r.Post("/", c.submit)
	r.Post("/verify", c.verify)
	r.Post("/revise", c.revise)

	return "/organizations", r
}

// --- shapes ------------------------------------------------------------------

// organizationBody is name and slug ONLY.
//
// The list already discloses who is approved to hire here, which ADR-0016 §3a
// accepts as the price of a redeemer finding their employer. It goes no
// further: no roster size, no seat count, no verification date, no payment
// status — nothing about how much an organisation is hiring or whether it can
// pay.
type organizationBody struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// addressInput is a main office, as submitted.
//
// Nested rather than flattened to country/city/street1, so the body says which
// fields describe a place and which describe a company.
type addressInput struct {
	Country    string `json:"country"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	Street1    string `json:"street1"`
	Street2    string `json:"street2"`
}

// onboardingRequest is a company describing itself.
//
// No owner fields. The person claiming the company chooses their username and
// password when they prove the address, not here — so a form submitted by
// anyone cannot name who will own the result (ADR-0017 §2).
type onboardingRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Email       string       `json:"email"`
	Phone       string       `json:"phone"`
	Headcount   string       `json:"headcount"`
	Address     addressInput `json:"address"`
}

func (b onboardingRequest) form() port.OnboardingForm {
	return port.OnboardingForm{
		Name: b.Name, Description: b.Description, Email: b.Email,
		Phone: b.Phone, Headcount: domain.HeadcountBand(b.Headcount),
		Country: b.Address.Country, City: b.Address.City,
		PostalCode: b.Address.PostalCode,
		Street1:    b.Address.Street1, Street2: b.Address.Street2,
	}
}

// onboardingVerifyRequest proves the address and names the owner.
type onboardingVerifyRequest struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// onboardingReviseRequest corrects a rejected submission.
//
// The token carries WHICH submission, so there is no id in the path. A
// revision keyed on an id alone would let anyone rewrite any refused company's
// answers; the code proves the caller is the one who received the rejection.
type onboardingReviseRequest struct {
	Token string `json:"token"`
	onboardingRequest
}

// --- handlers ----------------------------------------------------------------

// list backs the picker a redeemer chooses their employer from.
func (c *OrganizationsController) list(w http.ResponseWriter, r *http.Request) {
	orgs, err := c.redemption.ListOrganizations(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]organizationBody, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, organizationBody{Name: o.Name, Slug: o.Slug})
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": out})
}

// submit records a company and sends a code to the address it gave.
//
// 202 with an EMPTY BODY, always. Not 201, because nothing was created; not an
// id, because a caller holding one could poll for a decision about a company
// they may have nothing to do with. A name already submitted must not be
// distinguishable from one that is free, or this endpoint reports who is
// mid-onboarding before the public picker would ever list them.
func (c *OrganizationsController) submit(w http.ResponseWriter, r *http.Request) {
	var body onboardingRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	if err := c.onboarding.Submit(r.Context(), body.form()); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// verify spends the code and records how the owner will sign in.
//
// 204, and no token pair. There is no account to sign in as until an
// administrator approves, so a session here would be a credential for a seat
// that does not exist. This is the one place onboarding diverges from roster
// redemption, which does return a pair (ADR-0017 §6).
func (c *OrganizationsController) verify(w http.ResponseWriter, r *http.Request) {
	var body onboardingVerifyRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	if err := c.onboarding.Verify(r.Context(), body.Token, port.OnboardingOwnerInput{
		Username: body.Username, DisplayName: body.DisplayName, Password: body.Password,
	}); err != nil {
		writeRedemptionFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revise records a correction to a rejected submission.
func (c *OrganizationsController) revise(w http.ResponseWriter, r *http.Request) {
	var body onboardingReviseRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRegistration)
		return
	}

	if err := c.onboarding.Revise(r.Context(), body.Token, body.form()); err != nil {
		writeRedemptionFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
