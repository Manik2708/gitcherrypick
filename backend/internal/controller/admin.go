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

	// clock, because how long a request has waited is measured against the
	// platform's clock rather than the host's (ADR-0012).
	clock port.Clock
}

// NewAdminController wires the review queues.
func NewAdminController(admin port.AdminService, evaluation port.EvaluationService, clock port.Clock) *AdminController {
	return &AdminController{admin: admin, evaluation: evaluation, clock: clock}
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

	// Reason is client-facing and echoed back; Note is the admin's private
	// record and is not. Both reach the service the same way — the difference
	// is only whether the decision explains itself in the response.
	Reason string `json:"reason"`
	Note   string `json:"note"`

	// PaymentVerified is the admin's separate answer on payment capability.
	//
	// A pointer so "not stated" is distinguishable from "stated false" — the
	// admin who leaves it out has not decided it either way.
	PaymentVerified *bool `json:"payment_verified"`

	// Set only when approving a skill request: the admin names the catalogue
	// entry being created, which is not always what the contributor proposed.
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases"`
}

// justification is the text the service records, whichever key carried it.
func (d decisionRequest) justification() string {
	if d.Reason != "" {
		return d.Reason
	}
	return d.Note
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

// skillRequesterBody is the contributor who asked for a catalogue addition.
type skillRequesterBody struct {
	ID          domain.UserID `json:"id"`
	DisplayName string        `json:"display_name"`
}

// createdSkillBody is the catalogue entry an approval brought into existence.
//
// The admin names it, so the response says what was actually created rather
// than what the contributor proposed.
type createdSkillBody struct {
	ID       domain.SkillID `json:"id"`
	Slug     string         `json:"slug"`
	Name     string         `json:"name"`
	Category string         `json:"category"`
}

// verificationHirerBody is the seat awaiting review.
type verificationHirerBody struct {
	ID          domain.HirerID `json:"id"`
	DisplayName string         `json:"display_name"`
	Email       string         `json:"email"`
}

// verificationOrgBody is the company awaiting review.
type verificationOrgBody struct {
	ID   domain.OrganizationID `json:"id"`
	Name string                `json:"name"`
}

// verificationRequestBody is one queued review.
//
// The subject is resolved rather than referenced: an admin deciding whether a
// company is real needs its name and the seat's address, and a pair of uuids
// would send them looking elsewhere for both.
type verificationRequestBody struct {
	ID domain.RequestID `json:"id"`

	// Subject is who the decision is ABOUT, in one field.
	//
	// The hirer and the organization are both reported below, because an
	// admin deciding either needs the person and the company together. Subject
	// says which of the two the request names, so a queue can be read without
	// inferring it from which field happens to be null.
	Subject verificationSubjectBody `json:"subject"`

	Hirer        *verificationHirerBody `json:"hirer,omitempty"`
	Organization *verificationOrgBody   `json:"organization,omitempty"`
	Status       string                 `json:"status"`
	CreatedAt    time.Time              `json:"created_at"`

	// AgeHours is how long the request has waited.
	//
	// Computed here rather than left to the client: the queue is worked
	// oldest-first and the platform's own clock is the one that decides what
	// old means (ADR-0012).
	AgeHours float64 `json:"age_hours"`

	Proofs []verificationProofBody `json:"proofs"`
}

// verificationSubjectBody names who a request is about.
type verificationSubjectBody struct {
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

// verificationProofBody is one piece of submitted evidence.
//
// Value, notes and the attachment are each omitted when empty: the five proof
// kinds populate different ones, and reporting the others as "" would read as
// "submitted, and blank".
type verificationProofBody struct {
	Kind          string `json:"kind"`
	Value         string `json:"value,omitempty"`
	Notes         string `json:"notes,omitempty"`
	AttachmentURL string `json:"attachment_url,omitempty"`
}

type skillRequestBodyOut struct {
	ID domain.RequestID `json:"id"`

	// requested_by, not user_id: an admin reading the queue is looking at who
	// asked, and the queue holds requests from many people.
	RequestedBy skillRequesterBody `json:"requested_by"`

	ProposedName string    `json:"proposed_name"`
	Rationale    string    `json:"rationale"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`

	// Both absent while the request is pending: there is no decision yet, and
	// a null reason beside a pending status only invites the reader to wonder
	// whether it was decided without one.
	Reason     string     `json:"reason,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

// disputantBody is who raised a dispute.
//
// Name only, no id: the queue is a reading list, and an admin deciding whether
// an argument holds does not act on the person.
type disputantBody struct {
	DisplayName string `json:"display_name"`
}

// reevaluationBody is one queued dispute.
//
// No status: the queue is filtered to pending, so every row would carry the
// same value and it would say nothing.
type reevaluationBody struct {
	ID        domain.RequestID `json:"id"`
	User      disputantBody    `json:"user"`
	ClaimID   domain.ClaimID   `json:"claim_id"`
	Reason    string           `json:"reason"`
	CreatedAt time.Time        `json:"created_at"`

	// Both absent while pending: an undecided dispute has no note and no
	// review time, and reporting nulls for them beside a pending row would
	// invite a reader to wonder whether it was decided without either.
	DecisionNote string     `json:"decision_note,omitempty"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
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
		out = append(out, c.verificationBody(v))
	}
	writeJSON(w, http.StatusOK, verificationQueueBody{Total: len(out), Requests: out})
}

// verificationBody renders one queued review.
func (c *AdminController) verificationBody(v port.VerificationRequest) verificationRequestBody {
	body := verificationRequestBody{
		ID: v.ID, Status: v.Status, CreatedAt: v.CreatedAt,
		AgeHours: c.clock.Now().Sub(v.CreatedAt).Hours(),
		Proofs:   make([]verificationProofBody, 0, len(v.Proofs)),
	}

	if v.Hirer != nil {
		body.Hirer = &verificationHirerBody{
			ID: v.Hirer.ID, DisplayName: v.Hirer.DisplayName, Email: v.Hirer.Email,
		}
	}
	if v.Organization != nil {
		body.Organization = &verificationOrgBody{ID: v.Organization.ID, Name: v.Organization.Name}
	}

	// The person, when the request resolved to one. A company is a thing an
	// admin can look up; a seat is who they are answering.
	switch {
	case body.Hirer != nil:
		body.Subject = verificationSubjectBody{Kind: "hirer", DisplayName: body.Hirer.DisplayName}
	case body.Organization != nil:
		body.Subject = verificationSubjectBody{
			Kind: "organization", DisplayName: body.Organization.Name}
	}

	for _, proof := range v.Proofs {
		body.Proofs = append(body.Proofs, verificationProofBody{
			Kind: string(proof.Kind), Value: proof.Value,
			Notes: proof.Notes, AttachmentURL: proof.AttachmentURL,
		})
	}
	return body
}

// sweepRequest asks for a global re-run.
//
// rubric_version, not to_version: the field names what the corpus is moving
// TO, and it is the same name every response and every score carries.
type sweepRequest struct {
	RubricVersion string `json:"rubric_version"`
	Reason        string `json:"reason"`
}

// sweepBody is the sweep that was started.
//
// It reports both versions rather than only the target: an admin reading a
// sweep months later needs to know what it replaced, and the row is the only
// record of the platform's rubric history (RFC-0015).
type sweepBody struct {
	ID             domain.RequestID `json:"id"`
	From           string           `json:"from_rubric_version"`
	To             string           `json:"to_rubric_version"`
	Reason         string           `json:"reason"`
	ClaimsEnqueued int              `json:"claims_enqueued"`
	RequestedBy    domain.AdminID   `json:"requested_by"`
	CreatedAt      time.Time        `json:"created_at"`
}

// verificationQueueBody is the queue page.
type verificationQueueBody struct {
	Total    int                       `json:"total"`
	Requests []verificationRequestBody `json:"requests"`
}

// verificationDecisionBody is what deciding one returns.
type verificationDecisionBody struct {
	ID         domain.RequestID `json:"id"`
	Status     string           `json:"status"`
	ReviewedAt time.Time        `json:"reviewed_at"`
	ReviewedBy string           `json:"reviewed_by"`
}

func (c *AdminController) decideVerification(w http.ResponseWriter, r *http.Request) {
	p, id, decision, ok := c.decisionContext(w, r)
	if !ok {
		return
	}

	approve, _ := decision.approved()
	if err := c.admin.DecideVerification(r.Context(), p, id, domain.VerificationDecision{
		Approve:         approve,
		Reason:          decision.justification(),
		PaymentVerified: decision.PaymentVerified != nil && *decision.PaymentVerified,
	}); err != nil {
		writeError(w, err)
		return
	}
	// The reason is not echoed: it is written for the hirer, who reads it on
	// their own verification status, not for the admin who just typed it.
	writeJSON(w, http.StatusOK, verificationDecisionBody{
		ID:         id,
		Status:     decisionStatus(approve),
		ReviewedAt: c.clock.Now(),
		ReviewedBy: p.Subject(),
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
			ID: s.ID,
			RequestedBy: skillRequesterBody{
				ID: s.RequestedBy.ID, DisplayName: s.RequestedBy.DisplayName,
			},
			ProposedName: s.ProposedName,
			Rationale:    s.Rationale, Status: s.Status, CreatedAt: s.CreatedAt,
			Reason: s.Reason, ReviewedAt: s.ReviewedAt,
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

	created, err := c.admin.DecideSkillRequest(r.Context(), p, id, approve, skill, decision.justification())
	if err != nil {
		writeError(w, err)
		return
	}

	body := map[string]any{
		"id":          id,
		"status":      decisionStatus(approve),
		"reviewed_at": time.Now().UTC(),
		"reviewed_by": p.Subject(),
	}
	if created != nil {
		body["created_skill"] = createdSkillBody{
			ID: created.ID, Slug: created.Slug, Name: created.Name, Category: created.Category,
		}
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

	requests, err := c.admin.Reevaluations(r.Context(), p, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, err)
		return
	}

	out := make([]reevaluationBody, 0, len(requests))
	for _, v := range requests {
		out = append(out, reevaluationBody{
			ID:      v.ID,
			User:    disputantBody{DisplayName: v.DisplayName},
			ClaimID: v.ClaimID, Reason: v.Reason, CreatedAt: v.CreatedAt,
			DecisionNote: v.Decision, ReviewedAt: v.ReviewedAt,
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
	if !accept && decision.justification() == "" {
		// A rejection costs the contributor a cooldown tier, so it must say
		// why. An acceptance needs no justification.
		writeCode(w, http.StatusUnprocessableEntity, service.CodeReasonRequired)
		return
	}

	if err := c.admin.DecideReevaluation(r.Context(), p, id, accept, decision.justification()); err != nil {
		writeError(w, err)
		return
	}

	// claim_requeued is the consequence the contributor cares about: accepting
	// a dispute re-judges the claim, rejecting it leaves the score standing.
	body := map[string]any{
		"id":             id,
		"status":         disputeStatus(accept),
		"reviewed_at":    time.Now().UTC(),
		"claim_requeued": accept,
	}
	if decision.Reason != "" {
		body["reason"] = decision.Reason
	}
	writeJSON(w, http.StatusOK, body)
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

	var body sweepRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeUnknownRubricVersion)
		return
	}

	sweep, err := c.evaluation.Sweep(r.Context(), p, body.RubricVersion, body.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sweepBody{
		ID: sweep.ID, From: sweep.From, To: sweep.To, Reason: sweep.Reason,
		ClaimsEnqueued: sweep.ClaimsEnqueued, RequestedBy: sweep.RequestedBy,
		CreatedAt: sweep.CreatedAt,
	})
}

// --- helpers ---// --- helpers -----------------------------------------------------------------

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

// disputeStatus is the decision vocabulary for a re-evaluation.
//
// "accepted" rather than "approved": an admin agreeing with a dispute is
// conceding an argument, not admitting something to a catalogue, and the two
// reads differently enough in a queue that the fixtures keep them apart.
func disputeStatus(accepted bool) string {
	if accepted {
		return "accepted"
	}
	return "rejected"
}
