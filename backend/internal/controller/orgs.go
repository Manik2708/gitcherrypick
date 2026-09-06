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
// Accepting an invitation is UNAUTHENTICATED: the invitee has no account yet,
// and the token in the path is the only credential they hold.
func (c *OrganizationController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Post("/{orgID}/invitations", c.invite)
	r.Post("/invitations/{token}/accept", c.acceptInvitation)

	return "/orgs", r
}

// --- shapes ------------------------------------------------------------------

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// invitationBody returns the token ONCE.
//
// Only its hash is stored, so this response is the only place the token exists
// in readable form — a database read cannot yield a working invitation and
// neither can a backup.
type invitationBody struct {
	ID           domain.RequestID `json:"id"`
	Email        string           `json:"email"`
	Role         string           `json:"role"`
	Organization *orgRef          `json:"organization,omitempty"`
	InvitedBy    domain.HirerID   `json:"invited_by,omitempty"`
	Token        string           `json:"token"`
	ExpiresAt    time.Time        `json:"expires_at"`
	CreatedAt    time.Time        `json:"created_at"`
}

type acceptInvitationRequest struct {
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// --- handlers ----------------------------------------------------------------

// invite grants a seat.
//
// Capability is NOT required: an unverified organization may still fill its own
// seats, and those seats inherit the nothing the org has. Requiring
// verification here would stop a company assembling its team while it waits.
func (c *OrganizationController) invite(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	raw := chi.URLParam(r, "orgID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	var body inviteRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	role := domain.OrgRole(body.Role)
	if role == "" {
		role = domain.RoleMember
	}

	invitation, token, err := c.orgs.Invite(r.Context(), p, domain.OrganizationID(raw), body.Email, role)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			writeCode(w, http.StatusForbidden, service.CodeNotAnOrgMember)
			return
		}
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, invitationBody{
		ID: invitation.ID, Email: invitation.Email, Role: string(invitation.Role),
		Organization: orgRefOf(p), InvitedBy: invitation.InvitedBy,
		Token: token, ExpiresAt: invitation.ExpiresAt,
	})
}

// acceptInvitation creates the seat and signs it in.
//
// The seat inherits the organization's verification rather than earning its own
// — the whole point of verifying an org rather than a person (ADR-0008 §3a).
func (c *OrganizationController) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	var body acceptInvitationRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	hirer, pair, err := c.orgs.AcceptInvitation(r.Context(), token, body.DisplayName, body.Password)
	if err != nil {
		c.writeInvitationError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, hirerAuthResponse{
		Hirer: summarizeHirer(hirer), tokenPair: pairOf(pair),
	})
}

// writeInvitationError separates "never existed" from "no longer usable".
//
// An unknown token is 404 and indistinguishable from a revoked one, so an
// attacker cannot probe for live invitations. A token that WAS valid and has
// been spent or has expired is 410 Gone, because the holder is entitled to know
// which of the two happened to them.
func (c *OrganizationController) writeInvitationError(w http.ResponseWriter, err error) {
	switch code := service.CodeOf(err); code {
	case service.CodeInvitationExpired, service.CodeInvitationAccepted:
		writeCode(w, http.StatusGone, code)
		return
	}

	switch {
	case errors.Is(err, service.ErrNotFound):
		writeCode(w, http.StatusNotFound, service.CodeInvitationNotFound)
	case errors.Is(err, service.ErrConflict):
		// The service collapses spent and expired into one conflict. Both mean
		// the invitation is gone rather than merely contended.
		writeCode(w, http.StatusGone, service.CodeInvitationAccepted)
	default:
		writeError(w, err)
	}
}
