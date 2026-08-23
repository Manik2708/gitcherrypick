package controller

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// AdminController serves /admin — the queues only an admin may drain.
type AdminController struct {
	admin      port.AdminService
	evaluation port.EvaluationService
}

// NewAdminController wires the review queues.
func NewAdminController(admin port.AdminService, evaluation port.EvaluationService) *AdminController {
	return &AdminController{admin: admin, evaluation: evaluation}
}

var _ port.Controller = (*AdminController)(nil)

// Routes mounts /admin. Admin-only throughout — there is deliberately no OAuth
// path to an admin session (ADR-0002), because an admin decides hirer
// verification and an account mintable by whoever controls an identity provider
// would make that decision worth nothing.
func (c *AdminController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/verifications", c.verifications)
	r.Post("/verifications/{requestID}/decide", c.decideVerification)

	r.Get("/skill-requests", c.skillRequests)
	r.Post("/skill-requests/{requestID}/decide", c.decideSkillRequest)

	r.Get("/reevaluations", c.reevaluations)
	r.Post("/reevaluations/{requestID}/decide", c.decideReevaluation)

	r.Post("/evaluations/sweep", c.sweep)

	return "/admin", r
}

// --- shapes ------------------------------------------------------------------

// decisionRequest is the shared decision envelope.
//
// "approved" / "rejected" as a string rather than a boolean, because the wire
// format is what the fixtures pin and because a boolean named `approved` reads
// ambiguously at a call site.
type decisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`

	// Set only when approving a skill request: the admin names the catalogue
	// entry being created, which is not always what the contributor proposed.
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases"`
}

// approved reports the decision, and whether it was one at all.
func (d decisionRequest) approved() (bool, bool) {
	switch d.Decision {
	case "approved", "approve", "accepted", "accept":
		return true, true
	case "rejected", "reject", "declined", "decline":
		return false, true
	}
	return false, false
}

type verificationRequestBody struct {
	ID             domain.RequestID       `json:"id"`
	HirerID        *domain.HirerID        `json:"hirer_id,omitempty"`
	OrganizationID *domain.OrganizationID `json:"organization_id,omitempty"`
	Status         string                 `json:"status"`
	CreatedAt      time.Time              `json:"created_at"`
	ReviewedAt     *time.Time             `json:"reviewed_at"`
}

type skillRequestBodyOut struct {
	ID           domain.RequestID `json:"id"`
	UserID       domain.UserID    `json:"user_id"`
	ProposedName string           `json:"proposed_name"`
	Rationale    string           `json:"rationale"`
	Status       string           `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
}

type reevaluationBody struct {
	ID        domain.RequestID `json:"id"`
	ClaimID   domain.ClaimID   `json:"claim_id"`
	UserID    domain.UserID    `json:"user_id"`
	Reason    string           `json:"reason"`
	Status    string           `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
}

// --- handlers ----------------------------------------------------------------

func (c *AdminController) verifications(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindAdmin)
	if !ok {
		return
	}

	requests, err := c.admin.PendingVerifications(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]verificationRequestBody, 0, len(requests))
	for _, v := range requests {
		out = append(out, verificationRequestBody{
			ID: v.ID, HirerID: v.HirerID, OrganizationID: v.OrganizationID,
			Status: v.Status, CreatedAt: v.CreatedAt, ReviewedAt: v.ReviewedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "requests": out})
}

func (c *AdminController) decideVerification(w http.ResponseWriter, r *http.Request) {
	p, id, decision, ok := c.decisionContext(w, r)
	if !ok {
		return
	}

	approve, _ := decision.approved()
	if err := c.admin.DecideVerification(r.Context(), p, id, approve, decision.Reason); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          id,
		"status":      decisionStatus(approve),
		"reason":      decision.Reason,
		"reviewed_at": time.Now().UTC(),
	})
}

func (c *AdminController) skillRequests(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindAdmin)
	if !ok {
		return
	}

	requests, err := c.admin.PendingSkillRequests(r.Context(), p, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]skillRequestBodyOut, 0, len(requests))
	for _, s := range requests {
		out = append(out, skillRequestBodyOut{
			ID: s.ID, UserID: s.UserID, ProposedName: s.ProposedName,
			Rationale: s.Rationale, Status: s.Status, CreatedAt: s.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "requests": out})
}

// decideSkillRequest approves a catalogue addition, or refuses it.
//
// On approval the admin names the entry: a contributor proposing "WebAssembly"
// may get a catalogue skill slugged `wasm` with aliases, because the catalogue
// is curated rather than crowd-sourced (ADR-0003).
func (c *AdminController) decideSkillRequest(w http.ResponseWriter, r *http.Request) {
	p, id, decision, ok := c.decisionContext(w, r)
	if !ok {
		return
	}

	approve, _ := decision.approved()

	var skill *domain.Skill
	if approve {
		if decision.Slug == "" || decision.Name == "" {
			writeCode(w, http.StatusUnprocessableEntity, service.CodeUnknownSkill)
			return
		}
		skill = &domain.Skill{
			Slug:     decision.Slug,
			Name:     decision.Name,
			Category: decision.Category,
			Aliases:  decision.Aliases,
		}
	}

	created, err := c.admin.DecideSkillRequest(r.Context(), p, id, approve, skill, decision.Reason)
	if err != nil {
		writeError(w, err)
		return
	}

	body := map[string]any{
		"id":          id,
		"status":      decisionStatus(approve),
		"reviewed_at": time.Now().UTC(),
	}
	if created != nil {
		body["created_skill"] = map[string]any{"slug": created.Slug, "name": created.Name}
	}
	if decision.Reason != "" {
		body["reason"] = decision.Reason
	}
	writeJSON(w, http.StatusOK, body)
}

func (c *AdminController) reevaluations(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindAdmin)
	if !ok {
		return
	}

	requests, err := c.admin.PendingReevaluations(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]reevaluationBody, 0, len(requests))
	for _, v := range requests {
		out = append(out, reevaluationBody{
			ID: v.ID, ClaimID: v.ClaimID, UserID: v.UserID, Reason: v.Reason,
			Status: string(v.Status), CreatedAt: v.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "requests": out})
}

// decideReevaluation rules on a dispute.
//
// A REJECTION advances the escalating cooldown; an acceptance does not
// (ADR-0007 §6). That asymmetry is the whole mechanism — disputing is free
// until you are repeatedly wrong.
func (c *AdminController) decideReevaluation(w http.ResponseWriter, r *http.Request) {
	p, id, decision, ok := c.decisionContext(w, r)
	if !ok {
		return
	}

	accept, _ := decision.approved()
	if !accept && decision.Reason == "" {
		// A rejection costs the contributor a cooldown tier, so it must say
		// why. An acceptance needs no justification.
		writeCode(w, http.StatusUnprocessableEntity, service.CodeReasonRequired)
		return
	}

	if err := c.admin.DecideReevaluation(r.Context(), p, id, accept, decision.Reason); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          id,
		"status":      decisionStatus(accept),
		"reason":      decision.Reason,
		"reviewed_at": time.Now().UTC(),
	})
}

// sweep re-queues the corpus when the rubric changes.
//
// Enqueue-only, and 202: a half-swept corpus mixes rubric versions, and a
// leaderboard mixing them ranks people by which version happened to judge them
// (ADR-0004).
func (c *AdminController) sweep(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindAdmin)
	if !ok {
		return
	}

	var body struct {
		ToVersion string `json:"to_version"`
		Reason    string `json:"reason"`
	}
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeUnknownRubricVersion)
		return
	}

	enqueued, err := c.evaluation.Sweep(r.Context(), p, body.ToVersion, body.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"to_version": body.ToVersion,
		"enqueued":   enqueued,
	})
}

// --- helpers -----------------------------------------------------------------

// decisionContext extracts the admin, a well-formed request id, and a decision.
func (c *AdminController) decisionContext(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.RequestID, decisionRequest, bool) {
	p, ok := require(w, r, domain.KindAdmin)
	if !ok {
		return domain.Principal{}, "", decisionRequest{}, false
	}

	raw := chi.URLParam(r, "requestID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return domain.Principal{}, "", decisionRequest{}, false
	}

	var decision decisionRequest
	if err := decode(w, r, &decision); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidState)
		return domain.Principal{}, "", decisionRequest{}, false
	}

	// An unrecognised decision is refused rather than defaulted. Defaulting to
	// "rejected" would let a client typo cost a contributor a cooldown tier.
	if _, valid := decision.approved(); !valid {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidState)
		return domain.Principal{}, "", decisionRequest{}, false
	}
	return p, domain.RequestID(raw), decision, true
}

func decisionStatus(approved bool) string {
	if approved {
		return "approved"
	}
	return "rejected"
}
