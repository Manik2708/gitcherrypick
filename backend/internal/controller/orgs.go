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

// OrganizationController serves /orgs — seat invitations.
//
// An organization self-administers its seats with no admin involvement
// (ADR-0002): requiring approval for every recruiter at an already verified
// company would make the queue unworkable and buys nothing.
type OrganizationController struct {
	orgs port.OrganizationService
}

// NewOrganizationController wires seat management.
func NewOrganizationController(orgs port.OrganizationService) *OrganizationController {
	return &OrganizationController{orgs: orgs}
}

var _ port.Controller = (*OrganizationController)(nil)

// Routes mounts /orgs.
//
// Redemption does NOT live here: its callers hold no session, so it belongs
// with the other unauthenticated paths under /auth (ADR-0016 §7).
func (c *OrganizationController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/{orgID}/roster", c.listRoster)
	r.Post("/{orgID}/roster", c.addToRoster)
	r.Delete("/{orgID}/roster/{entryID}", c.removeFromRoster)
	r.Get("/{orgID}/seats", c.listSeats)

	return "/orgs", r
}

// --- shapes ------------------------------------------------------------------

// rosterRequest names an address, a username and a role.
//
// The username is the organization's choice, not the redeemer's: a redeemer
// who could name themselves could take a colleague's name, or claim an owner
// seat and roster further people from it (ADR-0016 §0).
type rosterRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// rosterEntryBody is one entry. No token, because a roster grants nothing by
// itself — being listed is an allowlist fact, not a credential.
type rosterEntryBody struct {
	ID       domain.RosterEntryID `json:"id"`
	Email    string               `json:"email"`
	Username string               `json:"username"`
	Role     string               `json:"role"`

	// AddedBy is the owner who listed this address, named rather than
	// referenced — ADR-0016 §9 is unconditional about where an author appears.
	AddedBy hirerRefBody `json:"added_by"`

	RedeemedAt *time.Time      `json:"redeemed_at"`
	RedeemedBy *domain.HirerID `json:"redeemed_by"`
	CreatedAt  time.Time       `json:"created_at"`
}

func rosterEntryBodyOf(e port.RosterEntry) rosterEntryBody {
	return rosterEntryBody{
		ID: e.ID, Email: e.Email, Username: e.Username, Role: string(e.Role),
		AddedBy: hirerRefBodyOf(e.AddedBy), RedeemedAt: e.RedeemedAt,
		RedeemedBy: e.RedeemedBy, CreatedAt: e.CreatedAt,
	}
}

// seatBody is one seat, live or revoked.
//
// `active` is the field an owner reads: a revoked seat stays listed precisely
// so the author of an old round can still be resolved (ADR-0016 §5a).
type seatBody struct {
	ID          domain.HirerID `json:"id"`
	Username    string         `json:"username"`
	DisplayName string         `json:"display_name"`
	Email       string         `json:"email"`
	Role        string         `json:"role"`
	Active      bool           `json:"active"`
	DisabledAt  *time.Time     `json:"disabled_at"`
}

func seatBodyOf(h domain.Hirer) seatBody {
	return seatBody{
		ID: h.ID, Username: h.Username, DisplayName: h.DisplayName,
		Email: h.Email, Role: string(h.OrgRole),
		Active: h.DisabledAt == nil, DisabledAt: h.DisabledAt,
	}
}

// --- handlers ----------------------------------------------------------------

// orgIDFrom reads and validates the organization in the path.
//
// A malformed id is 400 rather than 403. They are different mistakes: one is a
// typo, the other is reaching into someone else's organization, and answering
// 403 to both would tell a caller who fat-fingered a character that they are
// not a member of an organization that does not exist.
func orgIDFrom(w http.ResponseWriter, r *http.Request) (domain.OrganizationID, bool) {
	raw := chi.URLParam(r, "orgID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return "", false
	}
	return domain.OrganizationID(raw), true
}

// addToRoster names an address for a future seat.
//
// OWNERS ONLY. Capability is NOT required: an unverified organization may build
// its roster while it waits for review — redeeming an entry is what needs a
// verified org (ADR-0016 §2).
func (c *OrganizationController) addToRoster(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return
	}

	var body rosterRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	entry, err := c.orgs.AddToRoster(r.Context(), p, orgID, body.Email, body.Username,
		domain.OrgRole(body.Role))
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
			return
		}
		// A taken username is 409 and names nothing further: who holds it, and
		// where, is not this caller's business.
		if code := service.CodeOf(err); code == service.CodeUsernameTaken {
			writeCode(w, http.StatusConflict, code)
			return
		}
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, rosterEntryBodyOf(*entry))
}

// listRoster returns every entry, redeemed or not.
func (c *OrganizationController) listRoster(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return
	}

	entries, err := c.orgs.ListRoster(r.Context(), p, orgID)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
			return
		}
		writeError(w, err)
		return
	}

	out := make([]rosterEntryBody, 0, len(entries))
	for _, e := range entries {
		out = append(out, rosterEntryBodyOf(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// removeFromRoster withdraws the offer and revokes the seat it created.
//
// 204, deliberately: there is nothing to return. The seat row survives — it
// authored shortlists and the record of who was told (ADR-0016 §5) — so a body
// saying "deleted" would be a lie.
func (c *OrganizationController) removeFromRoster(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return
	}
	entryID := domain.RosterEntryID(chi.URLParam(r, "entryID"))

	if err := c.orgs.RemoveFromRoster(r.Context(), p, orgID, entryID); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSeats returns live and revoked seats.
//
// Any hirer in the organization, not owners only: every seat already sees every
// round, so naming their author exposes nothing new (ADR-0016 §5a).
func (c *OrganizationController) listSeats(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return
	}

	seats, err := c.orgs.ListSeats(r.Context(), p, orgID)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
			return
		}
		writeError(w, err)
		return
	}

	out := make([]seatBody, 0, len(seats))
	for _, h := range seats {
		out = append(out, seatBodyOf(h))
	}
	writeJSON(w, http.StatusOK, map[string]any{"seats": out})
}
