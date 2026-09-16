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
	profiles  port.ProfileService

	// roles backs GET /me/roles — the contributor's view of what is open to
	// them, and the one place their compensation expectation is used.
	roles port.RoleService
}

// NewMeController wires the self-service surface.
func NewMeController(
	auth port.AuthService,
	orgs port.OrganizationService,
	skills port.SkillService,
	discovery port.DiscoveryService,
	contacts port.ContactService,
	reeval port.ReevaluationService,
	profiles port.ProfileService,
	roles port.RoleService,
) *MeController {
	return &MeController{auth: auth, orgs: orgs, skills: skills,
		discovery: discovery, contacts: contacts, reeval: reeval,
		profiles: profiles, roles: roles}
}

var _ port.Controller = (*MeController)(nil)

// Routes mounts /me. Every route requires a principal; which KIND varies, and
// each handler states its own requirement.
func (c *MeController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/", c.me)
	r.Put("/availability", c.setAvailability)

	// The contributor profile (ADR-0018). Compensation is a SEPARATE pair
	// rather than fields on the profile, so a client updating what shape of
	// work it will take never sends a salary — and so no hirer-facing handler
	// is one struct field away from reading one.
	r.Get("/profile", c.profile)
	r.Put("/profile", c.saveProfile)
	r.Get("/compensation", c.compensation)
	r.Put("/compensation", c.saveCompensation)

	// What is open to this person (ADR-0019). Under /me rather than a public
	// roles list, because the filter reads their profile — including the pay
	// they expect, which is why the result can only be theirs.
	r.Get("/roles", myRoles(c.roles))
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

	// CanAppeal says whether submitting again is worth the hirer's time.
	//
	// A rejection with a reason is something they can answer; an approval is
	// not something to appeal. Reported rather than inferred, because "may I
	// try again" is the only question a rejected applicant actually has
	// (ADR-0002).
	CanAppeal bool `json:"can_appeal,omitempty"`
}

type shareLinkBody struct {
	ID        domain.ShareLinkID `json:"id"`
	Token     string             `json:"token"`
	URL       string             `json:"url"`
	CreatedAt time.Time          `json:"created_at"`
}

// mySkillsBody is a contributor's own standing.
//
// The scores are pointers because null is not zero: a contributor with no
// primary skill has not been measured (ADR-0007).
type mySkillsBody struct {
	Skills          []userSkillBody `json:"skills"`
	OverallScore    *float64        `json:"overall_score"`
	GeneralistScore *float64        `json:"generalist_score"`
	RubricVersion   string          `json:"rubric_version"`

	// Both are warnings, so both are omitted when there is nothing to warn
	// about. Stale means at least one skill was judged under an older rubric,
	// so these numbers are not comparable with a current leaderboard.
	Stale                  bool `json:"stale,omitempty"`
	ReevaluationInProgress bool `json:"reevaluation_in_progress,omitempty"`
}

type cooldownBody struct {
	RejectionCount int        `json:"rejection_count"`
	Tier           int        `json:"tier"`
	CooldownUntil  *time.Time `json:"cooldown_until"`

	// CooldownDays is the length of the CURRENT tier's wait, present only
	// while one is running. "28 days" is the number a contributor can act on;
	// a bare expiry date makes them do the subtraction.
	CooldownDays int `json:"cooldown_days,omitempty"`

	CanRequest bool `json:"can_request"`

	// BlockedBy and PendingRequestID are present only when something blocks.
	// Reporting `"blocked_by": null` beside `can_request: true` would invite
	// a client to branch on a field that never means anything.
	BlockedBy        string            `json:"blocked_by,omitempty"`
	PendingRequestID *domain.RequestID `json:"pending_request_id,omitempty"`

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

	standing, err := c.skills.MySkills(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mySkillsBody{
		Skills:                 userSkillBodies(standing.Skills, standing.ActiveRubricVersion),
		OverallScore:           standing.OverallScore,
		GeneralistScore:        standing.GeneralistScore,
		RubricVersion:          standing.RubricVersion,
		Stale:                  standing.Stale,
		ReevaluationInProgress: standing.ReevaluationInProgress,
	})
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

	out := verificationBody{
		Status:        status.Status,
		HirerVerified: status.HirerVerified,
		CanAppeal:     status.Status == "rejected",
	}
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

	standing, err := c.reeval.Status(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}

	blocker := standing.Blocker
	out := cooldownBody{
		RejectionCount:   standing.Cooldown.RejectionCount,
		Tier:             standing.Cooldown.Tier,
		CooldownUntil:    standing.Cooldown.CooldownUntil,
		CanRequest:       blocker == domain.NotBlocked,
		BlockedBy:        string(blocker),
		ClaimsEligible:   standing.ClaimsEligible,
		PendingRequestID: standing.PendingRequestID,
	}
	if blocker == domain.BlockedByCooldown {
		out.CooldownDays = standing.Cooldown.CooldownDays()
	}
	// The pending dispute is named only when it is what blocks. Reporting it
	// alongside a cooldown would point at the shorter of two waits.
	if blocker != domain.BlockedByPending {
		out.PendingRequestID = nil
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
		writeJSON(w, http.StatusOK, contactDecisionBody{
			ID:              request.ID,
			Status:          string(request.Status),
			RespondedAt:     request.RespondedAt,
			EmailReleasedAt: request.EmailReleasedAt,
			// Answering yes is a clearer statement of availability than the
			// button they forgot to click, so accepting refreshes the window
			// (ADR-0008 §1a). Said out loud, because it changes who can see
			// them and they did not ask for it.
			AvailabilityRefreshed: accept,
		})
	}
}

// contactDecisionBody is the answer a contributor just gave.
//
// Not the request as they browse it: what they need back is confirmation of
// what their answer DID — when it was recorded, whether their address went
// out, and that their availability was refreshed as a side effect.
type contactDecisionBody struct {
	ID          domain.ContactID `json:"id"`
	Status      string           `json:"status"`
	RespondedAt *time.Time       `json:"responded_at"`

	// Both omitted on a DECLINE: nothing was released and nothing refreshed,
	// and reporting "null, false" would invite the reader to wonder whether
	// something nearly happened.
	EmailReleasedAt       *time.Time `json:"email_released_at,omitempty"`
	AvailabilityRefreshed bool       `json:"availability_refreshed,omitempty"`
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
	// Capability mirrors AccessService.RequireHiringCapability exactly, because
	// two endpoints disagreeing about what an account may do is worse than
	// either answer alone — the client hides a button the server would honour,
	// or offers one it refuses.
	//
	// That gate reads the ORGANIZATION and nothing else for a seat: verifying
	// an org lifts every seat under it, and a seat's own column is not
	// consulted at all, because it is stale for anyone created before the org
	// was approved (ADR-0002, ADR-0008 §3a). A roster-redeemed seat and an
	// onboarded owner both have verified_at NULL for exactly that reason.
	//
	// An INDEPENDENT hirer has no organisation (ADR-0017 §1), so their own
	// approval is the only thing there is to read.
	//
	// This was `h.VerifiedAt != nil && h.Organization.IsVerified()`, which is
	// wrong in both directions: it refused an org-verified seat the server
	// allows, and it would have granted a seat whose org is unverified.
	capable := h.Organization.IsVerified()
	if h.Organization == nil {
		capable = h.VerifiedAt != nil
	}

	out := hirerMe{
		ID:          h.ID,
		Kind:        string(domain.KindHirer),
		DisplayName: h.DisplayName,

		// Verified follows the same rule, for the same reason summarizeHirer
		// does: a seat reporting its own unstamped column would read "not
		// verified" while being perfectly able to hire.
		Verified: capable,
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

// --- the contributor profile (ADR-0018) ---------------------------------------

// workPreferencesBody is what shape of work somebody will take.
//
// No open_to_freelance: availability already carries freelance, and two
// controls meaning one thing can disagree (ADR-0018 §6).
type workPreferencesBody struct {
	OpenToRemote      bool `json:"open_to_remote"`
	OpenToInternships bool `json:"open_to_internships"`
	OpenToOnsite      bool `json:"open_to_onsite"`
	OpenToContract    bool `json:"open_to_contract"`

	// CurrentCountry is ISO 3166-1 alpha-2, empty when unstated. Distinct from
	// the free-text location GitHub supplies.
	CurrentCountry string `json:"current_country"`

	// OfficeYOE is SELF-REPORTED, and a pointer so that "not said" stays
	// distinct from "zero years" — somebody contributing before their first
	// job is a real person with a real answer.
	OfficeYOE *int `json:"office_yoe"`

	// FirstPRURL is WRITE-ONCE: sending a second, different value is a 409
	// rather than a silent overwrite. A contributor's first contribution is
	// often years old in a repository they never claimed here, which is why it
	// is asked rather than read off a claim.
	FirstPRURL string `json:"first_pr_url"`

	// LatestPRURL is editable, because it goes stale by definition.
	LatestPRURL string `json:"latest_pr_url"`
}

// profileBody is the whole profile screen's answer.
type profileBody struct {
	Preferences workPreferencesBody `json:"preferences"`

	Availability *availabilityBody `json:"availability"`

	// NeedsAttention is whether to PROMPT. True only when this person has
	// never answered — not when they answered "none of these", which is a real
	// answer somebody may mean and must not be nagged about.
	NeedsAttention bool `json:"needs_attention"`

	// Matchable is false when the window is live and no shape is enabled — a
	// state reachable by accident and invisible from outside (ADR-0018). A
	// named field rather than something a client derives, because a client
	// that forgot would leave somebody wondering why nobody ever writes.
	Matchable bool `json:"matchable"`
}

// compensationBody is what somebody expects to be paid.
//
// A HIRER NEVER RECEIVES THIS SHAPE (ADR-0018 §2). It is returned by
// GET /me/compensation and by nothing else.
type compensationBody struct {
	// Currency is ISO 4217, empty when nothing is stated.
	Currency string `json:"currency"`

	// Minor units. Null means not stated, which is not zero.
	HourlyRate   *int64 `json:"hourly_rate"`
	YearlyAmount *int64 `json:"yearly_amount"`
}

func profileBodyOf(p *port.ContributorProfile) profileBody {
	if p == nil {
		return profileBody{}
	}
	return profileBody{
		Preferences: workPreferencesBody{
			OpenToRemote:      p.Preferences.OpenToRemote,
			OpenToInternships: p.Preferences.OpenToInternships,
			OpenToOnsite:      p.Preferences.OpenToOnsite,
			OpenToContract:    p.Preferences.OpenToContract,
			CurrentCountry:    p.Preferences.CurrentCountry,
			OfficeYOE:         p.Preferences.OfficeYOE,
			FirstPRURL:        p.Preferences.FirstPRURL,
			LatestPRURL:       p.Preferences.LatestPRURL,
		},
		Availability:   availabilityOf(p.Availability),
		NeedsAttention: p.NeedsAttention,
		Matchable:      p.Matchable,
	}
}

func compensationBodyOf(c *domain.Compensation) compensationBody {
	if c == nil {
		return compensationBody{}
	}
	return compensationBody{
		Currency: c.Currency, HourlyRate: c.HourlyRate, YearlyAmount: c.YearlyAmount,
	}
}

func (c *MeController) profile(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}
	out, err := c.profiles.Profile(r.Context(), domain.UserID(p.Subject()))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileBodyOf(out))
}

// saveProfile replaces the stated preferences.
//
// PUT, not PATCH: the form submits every field it shows, so a full replace
// says what happened. A merge would leave a flag set that somebody had just
// cleared, and they would have no way to tell.
func (c *MeController) saveProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	var body workPreferencesBody
	if err := decode(w, r, &body); err != nil {
		// 400, not 422: the body could not be READ — a syntax error, or a field
		// nothing here has. That is a malformed request, and the rest of this
		// codebase answers 400 for it. 422 is for a body that parsed and then
		// failed a rule, which is a different thing to tell a client.
		//
		// invalid_profile, not invalid_claim: "claim" is a specific noun here,
		// and a client branching on it would think its evidence was rejected.
		writeCode(w, http.StatusBadRequest, service.CodeInvalidProfile)
		return
	}

	out, err := c.profiles.SaveProfile(r.Context(), domain.UserID(p.Subject()),
		domain.WorkPreferences{
			OpenToRemote:      body.OpenToRemote,
			OpenToInternships: body.OpenToInternships,
			OpenToOnsite:      body.OpenToOnsite,
			OpenToContract:    body.OpenToContract,
			CurrentCountry:    body.CurrentCountry,
			OfficeYOE:         body.OfficeYOE,
			FirstPRURL:        body.FirstPRURL,
			LatestPRURL:       body.LatestPRURL,
		})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileBodyOf(out))
}

// compensation returns the figure to the person who typed it.
//
// Contributor-only, and there is deliberately no hirer-facing counterpart. A
// hirer who knows what you will accept offers exactly that (ADR-0018 §2).
func (c *MeController) compensation(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}
	out, err := c.profiles.Compensation(r.Context(), domain.UserID(p.Subject()))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, compensationBodyOf(out))
}

func (c *MeController) saveCompensation(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	var body compensationBody
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidProfile)
		return
	}

	out, err := c.profiles.SaveCompensation(r.Context(), domain.UserID(p.Subject()),
		domain.Compensation{
			Currency: body.Currency, HourlyRate: body.HourlyRate,
			YearlyAmount: body.YearlyAmount,
		})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, compensationBodyOf(out))
}
