package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OrganizationRepository owns invitations and seat grants.
//
// An organization self-administers its seats, with no admin involvement
// (ADR-0002). Requiring an admin to approve every recruiter at an already
// verified company would make the queue unworkable and buys nothing, since the
// org has already vouched for itself.
type OrganizationRepository struct{ db *DB }

// Organizations returns the seat-management repository.
func (db *DB) Organizations() *OrganizationRepository { return &OrganizationRepository{db: db} }

var _ port.OrganizationRepository = (*OrganizationRepository)(nil)

// InvitationWindow is how long an invitation stays acceptable.
const InvitationWindow = 14 * 24 * time.Hour

// CreateInvitation records a pending seat grant.
//
// Only the HASH is stored. The plaintext is shown once, at creation, and
// delivered by email — so a database read cannot yield a usable invitation,
// and neither can a backup.
func (r *OrganizationRepository) CreateInvitation(ctx context.Context, t port.Tx, orgID domain.OrganizationID, email string, role domain.OrgRole, invitedBy domain.HirerID, tokenHash []byte, expiresAt time.Time) (*port.Invitation, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating invitation id: %w", err)
	}
	if role == "" {
		role = domain.RoleMember
	}

	inv := port.Invitation{
		OrganizationID: orgID, Email: email, Role: role,
		InvitedBy: invitedBy, ExpiresAt: expiresAt,
	}
	if err := r.db.q(t).QueryRow(ctx, `
		INSERT INTO organization_invitations
		    (id, organization_id, email, role, invited_by, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		id.String(), string(orgID), email, string(role), string(invitedBy), tokenHash, expiresAt,
	).Scan(&inv.ID, &inv.CreatedAt); err != nil {
		return nil, translate(err, fmt.Sprintf("inviting %q", email))
	}
	return &inv, nil
}

// InvitationByTokenHash resolves a presented token.
//
// Locks the row FOR UPDATE. Acceptance is read-modify-write — check unused,
// check unexpired, then stamp — and without the lock two simultaneous
// acceptances of one invitation would both pass the check.
func (r *OrganizationRepository) InvitationByTokenHash(ctx context.Context, t port.Tx, hash []byte) (*port.Invitation, error) {
	var inv port.Invitation
	err := r.db.q(t).QueryRow(ctx, `
		SELECT id, organization_id, email, role, invited_by, accepted_at, expires_at
		FROM organization_invitations
		WHERE token_hash = $1
		FOR UPDATE`, hash,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.InvitedBy,
		&inv.AcceptedAt, &inv.ExpiresAt)
	if err != nil {
		// Deliberately not distinguishing "no such token" from anything else:
		// a distinguishable response would let an attacker probe for live
		// invitation tokens.
		return nil, translate(err, "resolving invitation")
	}
	return &inv, nil
}

// AcceptInvitation creates the seat, the membership, and marks the invitation
// used — in one transaction.
//
// The seat inherits the organization's verification rather than earning its
// own, which is the point of verifying an org rather than a person. Nothing
// here writes verified_at: capability is read through the organization.
func (r *OrganizationRepository) AcceptInvitation(ctx context.Context, t port.Tx, id domain.RequestID, h *domain.Hirer, passwordHash []byte, now time.Time) (*domain.Hirer, error) {
	q := r.db.q(t)

	// Single-use, enforced by the UPDATE rather than by the read above: the
	// predicate and the write are then the same statement, so nothing can
	// change between them.
	var (
		orgID string
		role  string
		email string
	)
	err := q.QueryRow(ctx, `
		UPDATE organization_invitations
		SET accepted_at = $2
		WHERE id = $1 AND accepted_at IS NULL AND expires_at > $2
		RETURNING organization_id, role, email`, string(id), now,
	).Scan(&orgID, &role, &email)
	if err != nil {
		if isNotFound(err) {
			// The UPDATE matched nothing, which the predicate makes ambiguous:
			// consumed, lapsed, or never there. The holder's remedy differs
			// for each, so the row is read to say which.
			return nil, r.explainRefusedInvitation(ctx, q, id, now)
		}
		return nil, translate(err, fmt.Sprintf("accepting invitation %s", id))
	}

	hirerID := h.ID
	if hirerID == "" {
		generated, gerr := uuid.NewV7()
		if gerr != nil {
			return nil, fmt.Errorf("generating hirer id: %w", gerr)
		}
		hirerID = domain.HirerID(generated.String())
	}

	var hash *[]byte
	if h.AuthProvider == domain.ProviderEmail {
		hash = &passwordHash
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO hirer_accounts
		    (id, organization_id, email, auth_provider, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		string(hirerID), orgID, email, string(h.AuthProvider), h.DisplayName, hash); err != nil {
		return nil, translate(err, "creating the invited hirer")
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO organization_members (organization_id, hirer_account_id, role)
		VALUES ($1, $2, $3)`, orgID, string(hirerID), role); err != nil {
		return nil, translate(err, "creating the membership")
	}

	created := *h
	created.ID = hirerID
	created.OrganizationID = domain.OrganizationID(orgID)
	created.OrgRole = domain.OrgRole(role)
	created.Email = email

	// The organization travels with the seat. Capability is a property of the
	// org, not the person (ADR-0002), so a seat reported without it cannot say
	// whether it may hire — which is the first thing the new member asks.
	org, err := r.db.Hirers().Organization(ctx, created.OrganizationID)
	if err != nil {
		return nil, err
	}
	created.Organization = org
	return &created, nil
}

// explainRefusedInvitation says WHY an acceptance did not match.
func (r *OrganizationRepository) explainRefusedInvitation(ctx context.Context, q querier, id domain.RequestID, now time.Time) error {
	var (
		acceptedAt *time.Time
		expiresAt  time.Time
	)
	if err := q.QueryRow(ctx,
		`SELECT accepted_at, expires_at FROM organization_invitations WHERE id = $1`,
		string(id)).Scan(&acceptedAt, &expiresAt); err != nil {
		return translate(err, fmt.Sprintf("invitation %s", id))
	}

	if acceptedAt != nil {
		return fmt.Errorf("invitation %s was already accepted: %w", id, port.ErrInvitationAccepted)
	}
	if !expiresAt.After(now) {
		return fmt.Errorf("invitation %s expired at %s: %w",
			id, expiresAt.Format(time.RFC3339), port.ErrInvitationExpired)
	}
	return fmt.Errorf("invitation %s: %w", id, port.ErrNotFound)
}

// VerificationFor reads a seat's own verification request.
//
// Ordered most recent first: registration raises one against the organization,
// and a rejected org may raise another later. A hirer asking where they stand
// means the current attempt, not the one that was refused last year.
func (r *OrganizationRepository) VerificationFor(ctx context.Context, hirer domain.HirerID, org domain.OrganizationID) (*port.VerificationRequest, error) {
	var v port.VerificationRequest
	var reason, hirerID, orgID *string

	// No join here, unlike the admin queue: the caller already knows who they
	// are and is asking only where their request stands.
	err := r.db.pool.QueryRow(ctx, `
		SELECT id, hirer_account_id, organization_id, status, created_at, reviewed_at, decision_reason
		FROM verification_requests
		WHERE hirer_account_id = $1::uuid OR organization_id = $2::uuid
		ORDER BY created_at DESC
		LIMIT 1`, string(hirer), string(org),
	).Scan(&v.ID, &hirerID, &orgID, &v.Status, &v.CreatedAt, &v.ReviewedAt, &reason)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("verification for hirer %s", hirer))
	}
	if reason != nil {
		v.DecisionReason = *reason
	}
	if hirerID != nil {
		v.Hirer = &port.VerificationHirer{ID: domain.HirerID(*hirerID)}
	}
	if orgID != nil {
		v.Organization = &port.VerificationOrganization{ID: domain.OrganizationID(*orgID)}
	}
	return &v, nil
}

// VerifyOrganization approves an organization, lifting every seat at once.
//
// One UPDATE against organizations, and none against hirer_accounts. Stamping
// each seat would leave two sources of truth that could disagree, and would
// miss any seat invited afterwards — which is exactly the case ADR-0008 §3a
// depends on.
func (r *OrganizationRepository) VerifyOrganization(ctx context.Context, t port.Tx, id domain.OrganizationID, by domain.AdminID, reason string) error {
	q := r.db.q(t)

	tag, err := q.Exec(ctx, `
		UPDATE organizations
		SET verified_at = now(), verified_by = $2, updated_at = now()
		WHERE id = $1 AND verified_at IS NULL`, string(id), string(by))
	if err != nil {
		return translate(err, fmt.Sprintf("verifying organization %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("organization %s is unknown or already verified: %w", id, port.ErrConflict)
	}

	if _, err := q.Exec(ctx, `
		UPDATE verification_requests
		SET status = 'approved', reviewed_by = $2, reviewed_at = now(),
		    decision_reason = NULLIF($3, ''), updated_at = now()
		WHERE organization_id = $1 AND status = 'pending'`,
		string(id), string(by), reason); err != nil {
		return translate(err, "closing the verification request")
	}
	return nil
}
