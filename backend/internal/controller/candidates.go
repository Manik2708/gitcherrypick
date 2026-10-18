package controller

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// Candidates on a role, and the act that tells them (ADR-0023).
//
// These were /shortlists. There was never more than one round per role, so the
// round was a second name for the job — and a hirer had to keep two objects in
// their head to answer one question: who have we approached for this.

// stageRequest puts somebody on a role.
type stageRequest struct {
	UserID domain.UserID `json:"user_id"`

	// Note is private to the organisation. The contributor never sees it, which
	// is what makes it usable for the thing it is actually for: why this person
	// is worth the conversation.
	Note string `json:"note"`
}

// contactRequestBody is the promise the contact step makes.
type contactRoleRequest struct {
	// TentativeResultDate is told to everybody this reaches, and the overdue
	// ratio is measured against it (ADR-0005). Required: "we will let you know"
	// with no date is what this field exists to stop.
	TentativeResultDate Date `json:"tentative_result_date"`
}

// candidateBody is one person on a role.
type candidateBody struct {
	UserID      domain.UserID `json:"user_id"`
	DisplayName string        `json:"display_name"`
	GitHubLogin string        `json:"github_login,omitempty"`

	Note    string       `json:"note,omitempty"`
	AddedBy hirerRefBody `json:"added_by"`
	AddedAt time.Time    `json:"added_at"`

	// NotifiedAt is THE boundary removal turns on (ADR-0008 §3a): null means
	// still private and removable, set means the contributor was told and the
	// row is permanent.
	//
	// Carried explicitly rather than inferred from contact_status. The two
	// almost agree — a notified candidate has a request, a staged one does not
	// — and "almost" is how a client ends up offering a Remove button the
	// server refuses.
	NotifiedAt *time.Time `json:"notified_at"`

	// ContactStatus is empty while somebody is still staged.
	ContactStatus string `json:"contact_status"`

	// Accepted is the only state a hire may be recorded from.
	Accepted bool `json:"accepted"`

	// Email is absent unless the contributor released it by accepting.
	// Omitted rather than null, so a body that carries no key cannot be read
	// as "they have no address".
	Email string `json:"email,omitempty"`
}

func candidateBodyOf(c domain.RoleCandidate) candidateBody {
	return candidateBody{
		UserID: c.UserID, DisplayName: c.DisplayName, GitHubLogin: c.GitHubLogin,
		Note: c.Note, AddedBy: hirerRefBodyOf(c.AddedBy), AddedAt: c.AddedAt,
		NotifiedAt: c.NotifiedAt, ContactStatus: string(c.ContactStatus),
		Accepted: c.Accepted, Email: c.Email,
	}
}

// contactedBody is one contributor who has now been told.
//
// Carries the contact request the disclosure created: that request is the
// thing the contributor will answer, and it is what makes the notification
// auditable afterwards.
type contactedBody struct {
	UserID           domain.UserID    `json:"user_id"`
	DisplayName      string           `json:"display_name"`
	ContactRequestID domain.ContactID `json:"contact_request_id"`
	NotifiedAt       *time.Time       `json:"notified_at"`
}

// contactResultBody reports what one contact step actually sent.
//
// irreversible is stated rather than implied: this is the moment staging
// becomes disclosure, and a hirer who has just crossed it should be told so by
// the response rather than by discovering that remove no longer works.
//
// already_notified is present so a second press can be SEEN to have sent
// nothing — the operation is idempotent, and a bare success would leave a hirer
// wondering whether they had just emailed everyone twice.
type contactResultBody struct {
	RoleID           domain.RoleID   `json:"role_id"`
	Status           string          `json:"status"`
	Notified         int             `json:"notified"`
	AlreadyNotified  int             `json:"already_notified"`
	Irreversible     bool            `json:"irreversible"`
	FirstContactedAt *time.Time      `json:"first_contacted_at"`
	Contacted        []contactedBody `json:"contacted"`
}

func contactResultBodyOf(result *port.ContactResult, candidates []domain.RoleCandidate) contactResultBody {
	out := contactResultBody{
		Notified:        result.Notified,
		AlreadyNotified: result.AlreadyNotified,
		Irreversible:    true,
		Contacted:       []contactedBody{},
	}
	if result.Role != nil {
		out.RoleID = result.Role.ID
		out.Status = string(result.Role.Status)
		out.FirstContactedAt = result.Role.FirstContactedAt
	}

	// Keyed by contributor: one contact step raises at most one request each,
	// so the pairing is exact rather than positional.
	requests := make(map[domain.UserID]domain.ContactID, len(result.Requests))
	for _, req := range result.Requests {
		requests[req.UserID] = req.ID
	}

	// Only what THIS press sent. A second press tells nobody, and listing
	// everybody reached by the first would read as having told them twice —
	// which for an irreversible disclosure is the worst thing to be unclear
	// about.
	for _, c := range candidates {
		id, notified := requests[c.UserID]
		if !notified {
			continue
		}
		out.Contacted = append(out.Contacted, contactedBody{
			UserID: c.UserID, DisplayName: c.DisplayName,
			ContactRequestID: id, NotifiedAt: c.NotifiedAt,
		})
	}
	return out
}

// hirerContactBody is a contact request as the ASKING side sees it.
//
// The mirror of contactRequestBody, and deliberately asymmetric: a contributor
// sees the company and the disclosure, a hirer sees a person and a status. The
// address appears only once it has been released, which is the moment the
// contributor consented to it (ADR-0005).
type hirerContactBody struct {
	ID   domain.ContactID `json:"id"`
	User contributorRef   `json:"user"`

	// RequestedBy names the colleague who asked, including one who has since
	// left (ADR-0016 §9). A hirer looking at an old approach needs to know who
	// on their side made it before making another.
	RequestedBy hirerRefBody `json:"requested_by"`

	Status      string     `json:"status"`
	Email       *string    `json:"email"`
	RespondedAt *time.Time `json:"responded_at"`
}

// contributorRef is the person, named rather than numbered.
type contributorRef struct {
	ID          domain.UserID `json:"id"`
	DisplayName string        `json:"display_name"`
	GitHubLogin string        `json:"github_login,omitempty"`
}

// --- handlers ----------------------------------------------------------------

// stage puts somebody on a role and discloses NOTHING.
func (c *RoleController) stage(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body stageRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist)
		return
	}
	if !isUUID(string(body.UserID)) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	candidate, err := c.roles.Stage(r.Context(), p, orgID, roleID, body.UserID, body.Note)
	if err != nil {
		c.writeRoleErrorFor(w, p, err)
		return
	}
	writeJSON(w, http.StatusCreated, candidateBodyOf(*candidate))
}

// unstage takes somebody off, while they have been told nothing.
func (c *RoleController) unstage(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	raw := chi.URLParam(r, "userID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	if err := c.roles.Unstage(r.Context(), p, orgID, roleID, domain.UserID(raw)); err != nil {
		c.writeRoleErrorFor(w, p, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// contact tells every staged candidate. IRREVERSIBLE.
//
// This is the moment staging becomes disclosure: once a contributor has been
// told, they can never be taken off, which is why this is a separate call
// rather than a side effect of staging one.
func (c *RoleController) contact(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body contactRoleRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidRole)
		return
	}

	result, err := c.roles.Contact(r.Context(), p, orgID, roleID, body.TentativeResultDate.Time)
	if err != nil {
		c.writeRoleErrorFor(w, p, err)
		return
	}

	// Read back so each contacted person carries their name and the stamp the
	// transaction just wrote, rather than the state from before it ran.
	after, err := c.roles.Candidates(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, contactResultBodyOf(result, after))
}

// contactRequests is what each contacted person answered.
func (c *RoleController) contactRequests(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	requests, err := c.roles.ContactRequests(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleErrorFor(w, p, err)
		return
	}

	out := make([]hirerContactBody, 0, len(requests))
	for _, req := range requests {
		body := hirerContactBody{
			ID: req.ID,
			User: contributorRef{
				ID: req.UserID, DisplayName: req.DisplayName, GitHubLogin: req.GitHubLogin,
			},
			RequestedBy: hirerRefBodyOf(req.RequestedBy),
			Status:      string(req.Status),
			RespondedAt: req.RespondedAt,
		}
		if req.Email != "" {
			email := req.Email
			body.Email = &email
		}
		out = append(out, body)
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "requests": out})
}
