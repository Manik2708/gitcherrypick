package controller

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// MeController serves /me — everything an account can see about itself.
type MeController struct {
	auth      port.AuthService
	orgs      port.OrganizationService
	skills    port.SkillService
	discovery port.DiscoveryService
	contacts  port.ContactService
	reeval    port.ReevaluationService
}

// NewMeController wires the self-service surface.
func NewMeController(
	auth port.AuthService,
	orgs port.OrganizationService,
	skills port.SkillService,
	discovery port.DiscoveryService,
	contacts port.ContactService,
	reeval port.ReevaluationService,
) *MeController {
	return &MeController{auth: auth, orgs: orgs, skills: skills,
		discovery: discovery, contacts: contacts, reeval: reeval}
}

var _ port.Controller = (*MeController)(nil)

// Routes mounts /me. Every route requires a principal; which KIND varies, and
// each handler states its own requirement.
func (c *MeController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/", c.me)
	r.Put("/availability", c.setAvailability)
	r.Get("/skills", c.mySkills)
	r.Get("/rank", c.myRank)
	r.Get("/verification", c.myVerification)
	r.Get("/reevaluation-status", c.reevaluationStatus)

	r.Post("/share-link", c.mintShareLink)
	r.Delete("/share-link/{linkID}", c.revokeShareLink)

	r.Get("/contact-requests", c.contactRequests)
	r.Post("/contact-requests/{contactID}/accept", c.respondContact(true))
	r.Post("/contact-requests/{contactID}/decline", c.respondContact(false))

	return "/me", r
}

// --- shapes ------------------------------------------------------------------

// availabilityBody is a contributor's discovery window.
type availabilityBody struct {
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// contributorMe is a contributor's view of themselves.
//
// The scores are pointers: null is not zero. A contributor with no primary
// skill has not been measured, and a zero would claim otherwise (ADR-0007).
type contributorMe struct {
	ID              domain.UserID     `json:"id"`
	DisplayName     string            `json:"display_name"`
	GitHubLogin     string            `json:"github_login"`
	Email           string            `json:"email"`
	PrincipalType   string            `json:"principal_type"`
	Availability    *availabilityBody `json:"availability"`
	OverallScore    *float64          `json:"overall_score,omitempty"`
	GeneralistScore *float64          `json:"generalist_score,omitempty"`
}

// hirerCapabilities is what this seat may currently do.
//
// All three follow the same gate — hirer verified AND organization verified
// (ADR-0002) — and are reported separately anyway, so the frontend disables
// individual affordances without re-deriving the rule.
type hirerCapabilities struct {
	CanSearch         bool `json:"can_search"`
	CanShortlist      bool `json:"can_shortlist"`
	CanViewScorecards bool `json:"can_view_scorecards"`
}

// hirerOrgBody is the organization as a seat sees it on its own profile.
type hirerOrgBody struct {
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
}

// hirerMe is a hirer's view of themselves.
//
// The key is `kind` here and `principal_type` on the contributor shape above.
// That inconsistency is in the approved stage-3 fixtures, and CLAUDE.md forbids
// editing one to make it pass — so it is reproduced rather than unified.
type hirerMe struct {
	ID           domain.HirerID    `json:"id"`
	Kind         string            `json:"kind"`
	DisplayName  string            `json:"display_name"`
	Verified     bool              `json:"verified"`
	Organization hirerOrgBody      `json:"organization"`
	Capabilities hirerCapabilities `json:"capabilities"`
}

type adminMe struct {
	ID            domain.AdminID `json:"id"`
	DisplayName   string         `json:"display_name"`
	Email         string         `json:"email"`
	PrincipalType string         `json:"principal_type"`
}

type setAvailabilityRequest struct {
	Status string `json:"status"`
}

type verificationBody struct {
	Status        string               `json:"status"`
	HirerVerified bool                 `json:"hirer_verified"`
	Organization  *organizationSummary `json:"organization"`
	SubmittedAt   *time.Time           `json:"submitted_at"`
	ReviewedAt    *time.Time           `json:"reviewed_at"`
	Reason        *string              `json:"reason"`
}

type shareLinkBody struct {
	ID        domain.ShareLinkID `json:"id"`
	Token     string             `json:"token"`
	URL       string             `json:"url"`
	CreatedAt time.Time          `json:"created_at"`
}

type cooldownBody struct {
	RejectionCount int              `json:"rejection_count"`
	Tier           int              `json:"tier"`
	CooldownUntil  *time.Time       `json:"cooldown_until"`
	CanRequest     bool             `json:"can_request"`
	ClaimsEligible []domain.ClaimID `json:"claims_eligible"`
}

// --- handlers ----------------------------------------------------------------

// me returns whichever shape matches the caller's account type.
func (c *MeController) me(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAny(w, r)
	if !ok {
		return
	}

	current, err := c.auth.Me(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}

	switch current.Kind {
	case domain.KindContributor:
		writeJSON(w, http.StatusOK, contributorProfile(current.Contributor))
	case domain.KindHirer:
		writeJSON(w, http.StatusOK, hirerProfile(current.Hirer))
	case domain.KindAdmin:
		writeJSON(w, http.StatusOK, adminMe{
			ID: current.Admin.ID, DisplayName: current.Admin.DisplayName,
			Email: current.Admin.Email, PrincipalType: string(domain.KindAdmin),
		})
	default:
		writeCode(w, http.StatusForbidden, service.CodeForbidden)
	}
}

func (c *MeController) setAvailability(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	var body setAvailabilityRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	availability, err := c.auth.SetAvailability(r.Context(), p.Contributor.ID,
		domain.AvailabilityStatus(body.Status))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, availabilityOf(availability))
}

func (c *MeController) mySkills(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	skills, err := c.skills.MySkills(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": userSkillBodies(skills)})
}

// myRank returns positions and totals and nothing identifying anyone else.
//
// A contributor sees their own standing; the pool stays invisible (ADR-0005).
func (c *MeController) myRank(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	rank, err := c.discovery.MyRank(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rankBody(rank))
}

func (c *MeController) myVerification(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	status, err := c.orgs.Verification(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}

	out := verificationBody{Status: status.Status, HirerVerified: status.HirerVerified}
	if !status.SubmittedAt.IsZero() {
		out.SubmittedAt = &status.SubmittedAt
	}
	out.ReviewedAt = status.ReviewedAt
	if status.Reason != "" {
		out.Reason = &status.Reason
	}
	if status.Organization != nil {
		out.Organization = &organizationSummary{
			ID:              status.Organization.ID,
			Name:            status.Organization.Name,
			Verified:        status.Organization.IsVerified(),
			PaymentVerified: status.Organization.PaymentVerified(),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// reevaluationStatus makes the cooldown legible BEFORE a contributor spends a
// request on a 429 (ADR-0007 §6).
func (c *MeController) reevaluationStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	cooldown, err := c.reeval.Status(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}

	out := cooldownBody{CanRequest: true, ClaimsEligible: []domain.ClaimID{}}
	if cooldown != nil {
		out.RejectionCount = cooldown.RejectionCount
		out.Tier = cooldown.Tier
		out.CooldownUntil = cooldown.CooldownUntil
		out.CanRequest = cooldown.CanRequest(time.Now())
	}
	writeJSON(w, http.StatusOK, out)
}

// mintShareLink issues the one thing a contributor may publish about
// themselves (ADR-0002).
//
// The plaintext is shown ONCE. Only its hash is stored, so this response is the
// only place the token ever exists in readable form.
func (c *MeController) mintShareLink(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	token, linkID, err := c.auth.MintShareLink(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, shareLinkBody{
		ID: linkID, Token: token, URL: "/public/scorecard/" + token,
		CreatedAt: time.Now().UTC(),
	})
}

func (c *MeController) revokeShareLink(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	linkID := domain.ShareLinkID(chi.URLParam(r, "linkID"))
	if err := c.auth.RevokeShareLink(r.Context(), p.Contributor.ID, linkID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *MeController) contactRequests(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	var status *domain.ContactRequestStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		parsed := domain.ContactRequestStatus(raw)
		status = &parsed
	}

	requests, err := c.contacts.List(r.Context(), p.Contributor.ID, status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":    len(requests),
		"requests": contactBodies(requests),
	})
}

// respondContact builds the accept and decline handlers.
//
// One function because the only difference is a boolean, and two near-identical
// handlers would be two places for the consent rule to drift.
func (c *MeController) respondContact(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := require(w, r, domain.KindContributor)
		if !ok {
			return
		}

		contactID := domain.ContactID(chi.URLParam(r, "contactID"))
		request, err := c.contacts.Respond(r.Context(), p.Contributor.ID, contactID, accept)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, contactBody(*request))
	}
}

// --- serialization -----------------------------------------------------------

func contributorProfile(c *domain.Contributor) contributorMe {
	return contributorMe{
		ID:              c.ID,
		DisplayName:     c.DisplayName,
		GitHubLogin:     c.GitHubLogin,
		Email:           c.Email,
		PrincipalType:   string(domain.KindContributor),
		Availability:    availabilityOf(c.Availability),
		OverallScore:    c.OverallScore,
		GeneralistScore: c.GeneralistScore,
	}
}

func hirerProfile(h *domain.Hirer) hirerMe {
	// Both gates, evaluated once. ADR-0002 requires the hirer AND their
	// organization to be verified; reporting either alone would let a seat
	// believe they can search when they cannot.
	capable := h.VerifiedAt != nil && h.Organization.IsVerified()

	out := hirerMe{
		ID:          h.ID,
		Kind:        string(domain.KindHirer),
		DisplayName: h.DisplayName,
		Verified:    h.VerifiedAt != nil,
		Capabilities: hirerCapabilities{
			CanSearch: capable, CanShortlist: capable, CanViewScorecards: capable,
		},
	}
	if h.Organization != nil {
		out.Organization = hirerOrgBody{
			Name:     h.Organization.Name,
			Verified: h.Organization.IsVerified(),
		}
	}
	return out
}

func availabilityOf(a *domain.Availability) *availabilityBody {
	if a == nil {
		return nil
	}
	return &availabilityBody{Status: string(a.Status), ExpiresAt: a.ExpiresAt}
}
