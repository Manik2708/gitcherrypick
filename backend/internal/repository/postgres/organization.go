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
func (r *OrganizationRepository) CreateInvitation(ctx context.Context, t port.Tx, orgID domain.OrganizationID, email string, role domain.OrgRole, invitedBy domain.HirerID, tokenHash []byte, expiresAt time.Time) (domain.RequestID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating invitation id: %w", err)
	}
	if role == "" {
		role = domain.RoleMember
	}
	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO organization_invitations
		    (id, organization_id, email, role, invited_by, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id.String(), string(orgID), email, string(role), string(invitedBy), tokenHash, expiresAt); err != nil {
		return "", translate(err, fmt.Sprintf("inviting %q", email))
	}
	return domain.RequestID(id.String()), nil
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
func (r *OrganizationRepository) AcceptInvitation(ctx context.Context, t port.Tx, id domain.RequestID, h *domain.Hirer, passwordHash []byte) (*domain.Hirer, error) {
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
		SET accepted_at = now()
		WHERE id = $1 AND accepted_at IS NULL AND expires_at > now()
		RETURNING organization_id, role, email`, string(id),
	).Scan(&orgID, &role, &email)
	if err != nil {
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
	return &created, nil
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
