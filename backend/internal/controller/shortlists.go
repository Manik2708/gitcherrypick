package controller

import (
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

type shortlistEntryBody struct {
	UserID     domain.UserID `json:"user_id"`
	Note       string        `json:"note"`
	NotifiedAt *time.Time    `json:"notified_at"`
	AddedAt    time.Time     `json:"added_at"`

	// Removable is derived rather than stored: an entry stops being removable
	// the moment its contributor is told, because deleting it afterwards would
	// destroy the record of a disclosure that happened (ADR-0008 §3a).
	Removable bool `json:"removable"`
}

type shortlistBody struct {
	ID                  domain.ShortlistID `json:"id"`
	Name                string             `json:"name"`
	Description         string             `json:"description"`
	Status              string             `json:"status"`
	TentativeResultDate Date               `json:"tentative_result_date"`
	Organization        *orgRef            `json:"organization,omitempty"`
	CreatedBy           domain.HirerID     `json:"created_by,omitempty"`

	EntryCount int `json:"entry_count"`

	// UnnotifiedCount is how many entries confirm would still tell. It is the
	// number that matters before an irreversible step: a hirer about to
	// confirm needs to know how many people are about to hear from them
	// (ADR-0008 §3a).
	UnnotifiedCount int `json:"unnotified_count"`

	FirstConfirmedAt *time.Time           `json:"first_confirmed_at"`
	ClosedAt         *time.Time           `json:"closed_at,omitempty"`
	Entries          []shortlistEntryBody `json:"entries,omitempty"`
}

// confirmResultBody reports what one confirm actually sent.
//
// AlreadyNotified is present so a second confirm can be SEEN to have sent
// nothing — the operation is idempotent, and a bare success would leave a hirer
// wondering whether they had just emailed everyone twice.
type confirmResultBody struct {
	Shortlist       shortlistBody `json:"shortlist"`
	Notified        int           `json:"notified"`
	AlreadyNotified int           `json:"already_notified"`
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

	shortlist, err := c.shortlists.Create(r.Context(), p,
		body.Name, body.Description, body.TentativeResultDate.Time)
	if err != nil {
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
		out = append(out, shortlistBodyOf(p, &shortlists[i]))
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
	writeJSON(w, http.StatusOK, shortlistBodyOf(p, shortlist))
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
	writeJSON(w, http.StatusOK, shortlistBodyOf(p, shortlist))
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
	writeJSON(w, http.StatusOK, shortlistBodyOf(p, shortlist))
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
	writeJSON(w, http.StatusOK, confirmResultBody{
		Shortlist:       shortlistBodyOf(p, result.Shortlist),
		Notified:        result.Notified,
		AlreadyNotified: result.AlreadyNotified,
	})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"total": len(requests), "requests": contactBodies(requests),
	})
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
		ID: s.ID, Name: s.Name, Description: s.Description,
		Status: string(s.Status), TentativeResultDate: Date{Time: s.TentativeResultDate},
		Organization: orgRefOf(p), CreatedBy: s.CreatedBy,
		EntryCount: len(entries), UnnotifiedCount: unnotified,
		FirstConfirmedAt: s.FirstConfirmedAt, ClosedAt: s.ClosedAt,
	}
	if len(entries) > 0 {
		out.Entries = entries
	}
	return out
}

func entryBody(e domain.ShortlistEntry) shortlistEntryBody {
	return shortlistEntryBody{
		UserID: e.UserID, Note: e.Note, NotifiedAt: e.NotifiedAt,
		AddedAt: e.AddedAt, Removable: e.Removable(),
	}
}
