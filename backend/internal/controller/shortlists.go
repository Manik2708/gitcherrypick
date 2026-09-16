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

// ShortlistController serves /shortlists — hiring rounds and the two-phase
// disclosure (ADR-0008 §3a).
type ShortlistController struct {
	shortlists port.ShortlistService
}

// NewShortlistController wires hiring rounds.
func NewShortlistController(shortlists port.ShortlistService) *ShortlistController {
	return &ShortlistController{shortlists: shortlists}
}

var _ port.Controller = (*ShortlistController)(nil)

// Routes mounts /shortlists. Hirer-only throughout.
func (c *ShortlistController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Post("/", c.create)
	r.Get("/", c.list)

	r.Route("/{shortlistID}", func(r chi.Router) {
		r.Get("/", c.get)
		r.Patch("/", c.update)
		r.Post("/close", c.close)
		r.Post("/confirm", c.confirm)

		r.Post("/entries", c.addEntry)
		r.Delete("/entries/{userID}", c.removeEntry)

		r.Get("/contact-requests", c.contactRequests)
	})

	return "/shortlists", r
}

// --- shapes ------------------------------------------------------------------

type createShortlistRequest struct {
	// RoleID is the job this round is for (ADR-0019 §3). Required: without it
	// a contact request can only say that somebody is interested, and the role
	// details have no route to the person being contacted.
	RoleID string `json:"role_id"`

	Name                string `json:"name"`
	Description         string `json:"description"`
	TentativeResultDate Date   `json:"tentative_result_date"`
}

// updateShortlistRequest edits presentation and the promised date.
//
// Every field is a pointer so absent means "leave alone", distinct from an
// empty string meaning "clear it".
type updateShortlistRequest struct {
	Name                *string `json:"name"`
	Description         *string `json:"description"`
	TentativeResultDate *Date   `json:"tentative_result_date"`
}

type addEntryRequest struct {
	UserID domain.UserID `json:"user_id"`
	Note   string        `json:"note"`
}

// entryContributorBody is the person an entry stages.
//
// Named rather than referenced: a hirer reading a shortlist is looking at
// people, and a column of uuids would send them to fetch each one.
type entryContributorBody struct {
	ID          domain.UserID `json:"id"`
	DisplayName string        `json:"display_name"`
	GitHubLogin string        `json:"github_login"`
}

// shortlistEntryBody is one staged contributor.
//
// No address anywhere. An entry is staged, not disclosed: notified_at stays
// null until the round is confirmed, and releasing an address is what accepting
// a CONTACT REQUEST does, not what being shortlisted does (ADR-0008 §3a).
type shortlistEntryBody struct {
	ShortlistID domain.ShortlistID   `json:"shortlist_id"`
	User        entryContributorBody `json:"user"`

	// Null when the hirer left none, rather than "": an absent note and an
	// empty one are the same thing, and null is the one that says so.
	Note *string `json:"note"`

	AddedBy    hirerRefBody `json:"added_by"`
	NotifiedAt *time.Time   `json:"notified_at"`
	AddedAt    time.Time    `json:"added_at"`
}

// shortlistBody is a round as a hirer sees it.
//
// Deliberately small on create: a round that was just made has no entries, no
// confirmations and an organization the caller already knows, because they are
// the one who owns it. The fuller picture — entries and their notification
// state — is what GET /shortlists/{id} is for.
type shortlistBody struct {
	ID domain.ShortlistID `json:"id"`

	// RoleID is the job this round is for (ADR-0019 §3). One round, one role.
	RoleID domain.RoleID `json:"role_id"`

	Name                string `json:"name"`
	Status              string `json:"status"`
	TentativeResultDate Date   `json:"tentative_result_date"`

	// Counts rather than the entries themselves on a LIST. Loading every
	// staged contributor for every round would be several joins per row to
	// produce two integers, and the round the hirer opens is the only one
	// whose entries they wanted.
	EntryCount      *int `json:"entry_count,omitempty"`
	UnnotifiedCount *int `json:"unnotified_count,omitempty"`

	Entries   []shortlistEntryBody `json:"entries,omitempty"`
	CreatedBy *hirerRefBody        `json:"created_by,omitempty"`
	ClosedAt  *time.Time           `json:"closed_at,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
}

// patchedShortlistBody is a round as an EDIT returns it.
//
// Fuller than either the create or the list: an edit is where a hirer confirms
// what the round now says, so it restates the description they may have just
// changed, the organization that owns it, and the counts that decide whether
// confirming would disclose anything.
type patchedShortlistBody struct {
	ID                  domain.ShortlistID `json:"id"`
	Name                string             `json:"name"`
	Description         string             `json:"description"`
	Status              string             `json:"status"`
	TentativeResultDate Date               `json:"tentative_result_date"`
	Organization        *orgRef            `json:"organization,omitempty"`
	CreatedBy           hirerRefBody       `json:"created_by"`
	EntryCount          int                `json:"entry_count"`
	UnnotifiedCount     int                `json:"unnotified_count"`
	FirstConfirmedAt    *time.Time         `json:"first_confirmed_at"`

	// Exactly one of these is set. An EDIT reports when it landed, because
	// that is the value the caller needs to detect their own write; a READ
	// reports when the round was opened, which is what dates it.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// patchedShortlistBodyOf renders the response to an edit.
func patchedShortlistBodyOf(p domain.Principal, s *domain.Shortlist) patchedShortlistBody {
	if s == nil {
		return patchedShortlistBody{}
	}

	unnotified := 0
	for _, e := range s.Entries {
		if e.Removable() {
			unnotified++
		}
	}
	return patchedShortlistBody{
		ID: s.ID, Name: s.Name, Description: s.Description,
		Status:              string(s.Status),
		TentativeResultDate: Date{Time: s.TentativeResultDate},
		Organization:        orgRefOf(p),
		CreatedBy:           hirerRefBodyOf(s.CreatedBy),
		EntryCount:          len(s.Entries),
		UnnotifiedCount:     unnotified,
		FirstConfirmedAt:    s.FirstConfirmedAt,
		UpdatedAt:           &s.UpdatedAt,
	}
}

// confirmResultBody reports what one confirm actually sent.
//
// AlreadyNotified is present so a second confirm can be SEEN to have sent
// nothing — the operation is idempotent, and a bare success would leave a hirer
// wondering whether they had just emailed everyone twice.
// notifiedEntryBody is one contributor who has now been told.
//
// Carries the contact request the disclosure created: that request is the
// thing the contributor will answer, and it is what makes the notification
// auditable afterwards.
type notifiedEntryBody struct {
	UserID           domain.UserID    `json:"user_id"`
	DisplayName      string           `json:"display_name"`
	ContactRequestID domain.ContactID `json:"contact_request_id"`
	NotifiedAt       *time.Time       `json:"notified_at"`
}

// confirmResultBody reports what one confirm actually sent.
//
// irreversible is stated rather than implied: this is the moment staging
// becomes disclosure, and a hirer who has just crossed it should be told so by
// the response rather than by discovering that remove no longer works.
type confirmResultBody struct {
	ID               domain.ShortlistID  `json:"id"`
	Status           string              `json:"status"`
	Notified         int                 `json:"notified"`
	AlreadyNotified  int                 `json:"already_notified"`
	Irreversible     bool                `json:"irreversible"`
	FirstConfirmedAt *time.Time          `json:"first_confirmed_at"`
	Entries          []notifiedEntryBody `json:"entries"`
}

// confirmResultBodyOf pairs each notified entry with the request it raised.
func confirmResultBodyOf(result *port.ConfirmResult) confirmResultBody {
	out := confirmResultBody{
		Notified:        result.Notified,
		AlreadyNotified: result.AlreadyNotified,
		Irreversible:    true,
		Entries:         []notifiedEntryBody{},
	}
	if result.Shortlist != nil {
		out.ID = result.Shortlist.ID
		out.Status = string(result.Shortlist.Status)
		out.FirstConfirmedAt = result.Shortlist.FirstConfirmedAt
	}

	// Keyed by contributor: one confirm raises at most one request each, so
	// the pairing is exact rather than positional.
	requests := make(map[domain.UserID]domain.ContactID, len(result.Requests))
	for _, req := range result.Requests {
		requests[req.UserID] = req.ID
	}
	if result.Shortlist == nil {
		return out
	}
	// Only what THIS confirm sent. A second confirm notifies nobody, and
	// listing everyone told by the first would read as having told them twice
	// — which for an irreversible disclosure is the worst thing to be unclear
	// about.
	for _, e := range result.Shortlist.Entries {
		id, notified := requests[e.UserID]
		if !notified || e.NotifiedAt == nil {
			continue
		}
		out.Entries = append(out.Entries, notifiedEntryBody{
			UserID: e.UserID, DisplayName: e.DisplayName,
			ContactRequestID: id, NotifiedAt: e.NotifiedAt,
		})
	}
	return out
}

// --- handlers ----------------------------------------------------------------

func (c *ShortlistController) create(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	var body createShortlistRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist)
		return
	}

	// The date is what a contributor is told and what the overdue ratio is
	// measured against (ADR-0005), so a round without one promises nothing and
	// a round already past its date is born overdue.
	if body.TentativeResultDate.IsZero() {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist,
			[]fieldError{{Field: "tentative_result_date", Reason: "required"}})
		return
	}

	// Checked here rather than left to the database, so a missing or
	// fat-fingered id is a named field error instead of a foreign key
	// violation the caller has to decode.
	if !isUUID(body.RoleID) {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist,
			[]fieldError{{Field: "role_id", Reason: "required"}})
		return
	}

	shortlist, err := c.shortlists.Create(r.Context(), p, domain.RoleID(body.RoleID),
		body.Name, body.Description, body.TentativeResultDate.Time)
	if err != nil {
		if service.CodeOf(err) == "" && errors.Is(err, service.ErrInvalid) {
			writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist,
				[]fieldError{{Field: "tentative_result_date", Reason: "must_be_future"}})
			return
		}
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusCreated, shortlistBodyOf(p, shortlist))
}

func (c *ShortlistController) list(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	var status *domain.ShortlistStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		parsed := domain.ShortlistStatus(raw)
		status = &parsed
	}

	shortlists, err := c.shortlists.List(r.Context(), p, status)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}

	out := make([]shortlistBody, 0, len(shortlists))
	for i := range shortlists {
		out = append(out, listedShortlistBodyOf(p, &shortlists[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "shortlists": out})
}

func (c *ShortlistController) get(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	shortlist, err := c.shortlists.Get(r.Context(), p, id)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, roundDetailBodyOf(shortlist))
}

func (c *ShortlistController) update(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	var body updateShortlistRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist)
		return
	}

	// Absent means "leave alone", which a zero time cannot express.
	var date *time.Time
	if body.TentativeResultDate != nil {
		date = &body.TentativeResultDate.Time
	}

	shortlist, err := c.shortlists.Update(r.Context(), p, id,
		body.Name, body.Description, date)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, patchedShortlistBodyOf(p, shortlist))
}

func (c *ShortlistController) close(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	shortlist, err := c.shortlists.Close(r.Context(), p, id)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, closedShortlistBody{
		ID: shortlist.ID, Name: shortlist.Name, Status: string(shortlist.Status),
		EntryCount: len(shortlist.Entries), ClosedAt: shortlist.ClosedAt,
	})
}

// addEntry stages a candidate and discloses NOTHING.
//
// The self-exclusion check fires here rather than at confirm: discovering at
// send time that a candidate was never eligible is too late (ADR-0008 §3a).
func (c *ShortlistController) addEntry(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	var body addEntryRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidShortlist)
		return
	}
	if !isUUID(string(body.UserID)) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	entry, err := c.shortlists.AddEntry(r.Context(), p, id, body.UserID, body.Note)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusCreated, entryBody(*entry))
}

func (c *ShortlistController) removeEntry(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	raw := chi.URLParam(r, "userID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	if err := c.shortlists.RemoveEntry(r.Context(), p, id, domain.UserID(raw)); err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// confirm notifies every unnotified entry. IRREVERSIBLE.
//
// This is the moment staging becomes disclosure: once a contributor has been
// told, the entry can never be withdrawn, which is why confirm is a separate
// call rather than a side effect of adding one.
func (c *ShortlistController) confirm(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	result, err := c.shortlists.Confirm(r.Context(), p, id)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, confirmResultBodyOf(result))
}

func (c *ShortlistController) contactRequests(w http.ResponseWriter, r *http.Request) {
	p, id, ok := c.shortlistContext(w, r)
	if !ok {
		return
	}

	requests, err := c.shortlists.ContactRequests(r.Context(), p, id)
	if err != nil {
		c.writeShortlistError(w, p, err)
		return
	}
	out := make([]hirerContactBody, 0, len(requests))
	for _, req := range requests {
		body := hirerContactBody{
			ID: req.ID,
			User: entryContributorBody{
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

// roundEntryBody is one contributor on a round, as the round detail shows them.
//
// contact_status and email are the two facts a hirer is waiting on, and both
// stay null until the contributor acts: staging discloses nothing, and the
// address is released only by an acceptance (ADR-0005).
type roundEntryBody struct {
	UserID        domain.UserID `json:"user_id"`
	DisplayName   string        `json:"display_name"`
	ContactStatus *string       `json:"contact_status"`
	Email         *string       `json:"email"`
}

// roundDetailBody is a single round: who is on it, and where each stands.
type roundDetailBody struct {
	ID domain.ShortlistID `json:"id"`

	// RoleID is the job this round is for (ADR-0019 §3).
	RoleID domain.RoleID `json:"role_id"`

	Name                string           `json:"name"`
	Status              string           `json:"status"`
	TentativeResultDate Date             `json:"tentative_result_date"`
	Entries             []roundEntryBody `json:"entries"`
}

func roundDetailBodyOf(s *domain.Shortlist) roundDetailBody {
	if s == nil {
		return roundDetailBody{Entries: []roundEntryBody{}}
	}
	out := roundDetailBody{
		ID: s.ID, RoleID: s.RoleID, Name: s.Name, Status: string(s.Status),
		TentativeResultDate: Date{Time: s.TentativeResultDate},
		Entries:             make([]roundEntryBody, 0, len(s.Entries)),
	}
	for _, e := range s.Entries {
		row := roundEntryBody{UserID: e.UserID, DisplayName: e.DisplayName}
		if e.ContactStatus != "" {
			status := e.ContactStatus
			row.ContactStatus = &status
		}
		if e.Email != "" {
			email := e.Email
			row.Email = &email
		}
		out.Entries = append(out.Entries, row)
	}
	return out
}

// closedShortlistBody is a round that has just ended.
//
// Deliberately terse: closing settles a question, so what it reports is that
// the round is closed, when, and how many people it reached. The promised date
// is gone because there is no longer a decision pending against it.
type closedShortlistBody struct {
	ID         domain.ShortlistID `json:"id"`
	Name       string             `json:"name"`
	Status     string             `json:"status"`
	EntryCount int                `json:"entry_count"`
	ClosedAt   *time.Time         `json:"closed_at"`
}

// hirerContactBody is a contact request as the ASKING side sees it.
//
// The mirror of contactRequestBody, and deliberately asymmetric: a contributor
// sees the company and the disclosure, a hirer sees a person and a status. The
// address appears only once it has been released, which is the moment the
// contributor consented to it (ADR-0005).
type hirerContactBody struct {
	ID   domain.ContactID     `json:"id"`
	User entryContributorBody `json:"user"`

	// RequestedBy names the colleague who asked, including one who has since
	// left (ADR-0016 §9). A hirer looking at an old approach needs to know who
	// on their side made it before making another.
	RequestedBy hirerRefBody `json:"requested_by"`

	Status      string     `json:"status"`
	Email       *string    `json:"email"`
	RespondedAt *time.Time `json:"responded_at"`
}

// --- helpers -----------------------------------------------------------------

func (c *ShortlistController) shortlistContext(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.ShortlistID, bool) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return domain.Principal{}, "", false
	}

	raw := chi.URLParam(r, "shortlistID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return domain.Principal{}, "", false
	}
	return p, domain.ShortlistID(raw), true
}

// writeShortlistError names the resource and preserves the capability detail.
//
// Another organization's round is NOT FOUND rather than forbidden: a competitor
// must not be able to confirm that a given id belongs to somebody.
func (c *ShortlistController) writeShortlistError(w http.ResponseWriter, p domain.Principal, err error) {
	if errorsIsCapability(err) {
		status, code := statusFor(err)
		body := map[string]any{"error": code}
		if message := messages[code]; message != "" {
			body["message"] = message
		}
		if p.Hirer != nil {
			body["hirer_verified"] = p.Hirer.VerifiedAt != nil
			body["organization_verified"] = p.Hirer.Organization.IsVerified()
		}
		writeDetail(w, status, body)
		return
	}

	if service.CodeOf(err) == "" && isNotFound(err) {
		writeCode(w, http.StatusNotFound, service.CodeShortlistNotFound)
		return
	}
	writeError(w, err)
}

// --- serialization -----------------------------------------------------------

// shortlistBodyOf serializes a round.
//
// The organization comes from the ACTING PRINCIPAL: a service refuses any hirer
// reaching outside their own, so by the time a response is built the two are
// necessarily the same.
func shortlistBodyOf(p domain.Principal, s *domain.Shortlist) shortlistBody {
	if s == nil {
		return shortlistBody{}
	}

	entries := make([]shortlistEntryBody, 0, len(s.Entries))
	unnotified := 0
	for _, e := range s.Entries {
		entries = append(entries, entryBody(e))
		if e.Removable() {
			unnotified++
		}
	}

	out := shortlistBody{
		ID: s.ID, RoleID: s.RoleID, Name: s.Name,
		Status: string(s.Status), TentativeResultDate: Date{Time: s.TentativeResultDate},
		ClosedAt: s.ClosedAt, CreatedAt: s.CreatedAt,
	}
	if len(entries) > 0 {
		out.Entries = entries
	}
	return out
}

// listedShortlistBodyOf renders a round in the LIST, where counts stand in for
// the entries and the owner is named so a colleague can see whose round it is.
func listedShortlistBodyOf(p domain.Principal, s *domain.Shortlist) shortlistBody {
	out := shortlistBodyOf(p, s)
	if s == nil {
		return out
	}

	entryCount, unnotified := len(s.Entries), 0
	for _, e := range s.Entries {
		if e.Removable() {
			unnotified++
		}
	}

	out.Entries = nil
	out.EntryCount, out.UnnotifiedCount = &entryCount, &unnotified
	createdBy := hirerRefBodyOf(s.CreatedBy)
	out.CreatedBy = &createdBy
	return out
}

func entryBody(e domain.ShortlistEntry) shortlistEntryBody {
	out := shortlistEntryBody{
		ShortlistID: e.ShortlistID,
		User: entryContributorBody{
			ID: e.UserID, DisplayName: e.DisplayName, GitHubLogin: e.GitHubLogin,
		},
		AddedBy: hirerRefBodyOf(e.AddedBy), NotifiedAt: e.NotifiedAt, AddedAt: e.AddedAt,
	}
	if e.Note != "" {
		out.Note = &e.Note
	}
	return out
}
