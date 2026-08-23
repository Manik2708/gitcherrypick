package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OrganizationService owns seat invitations.
//
// An organization self-administers its seats, with no admin involvement
// (ADR-0002): requiring approval for every recruiter at an already verified
// company would make the queue unworkable and buys nothing.
type OrganizationService struct {
	orgs     port.OrganizationRepository
	hirers   port.HirerRepository
	sessions port.SessionRepository
	notifier port.Notifier
	minter   port.TokenMinter
	tokens   port.TokenIssuer
	hasher   port.PasswordHasher
	tx       port.TxManager
	clock    port.Clock
}

// NewOrganizationService wires seat management.
func NewOrganizationService(
	orgs port.OrganizationRepository,
	hirers port.HirerRepository,
	sessions port.SessionRepository,
	notifier port.Notifier,
	minter port.TokenMinter,
	tokens port.TokenIssuer,
	hasher port.PasswordHasher,
	tx port.TxManager,
	clock port.Clock,
) *OrganizationService {
	return &OrganizationService{orgs: orgs, hirers: hirers, sessions: sessions,
		notifier: notifier, minter: minter, tokens: tokens, hasher: hasher,
		tx: tx, clock: clock}
}

var _ port.OrganizationService = (*OrganizationService)(nil)

// InvitationWindow is how long an invitation stays acceptable.
const InvitationWindow = 14 * 24 * time.Hour

// Invite grants a seat.
//
// Capability is NOT required: an unverified organization may still fill its own
// seats, and the seats simply inherit the nothing the org has. Requiring
// verification here would mean a company could not assemble its team while
// waiting for review.
func (s *OrganizationService) Invite(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, email string, role domain.OrgRole) (*port.Invitation, string, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, "", err
	}
	// A member of another organization cannot invite into this one.
	if hirer.OrganizationID != orgID {
		return nil, "", fmt.Errorf("you are not a member of that organization: %w", ErrForbidden)
	}
	if email == "" {
		return nil, "", fmt.Errorf("an invitation needs an email: %w", ErrInvalid)
	}

	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return nil, "", fmt.Errorf("minting the invitation: %w", err)
	}
	expiresAt := s.clock.Now().Add(InvitationWindow)

	var id domain.RequestID
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		id, err = s.orgs.CreateInvitation(ctx, tx, orgID, email, role, hirer.ID, hash, expiresAt)
		return err
	})
	if err != nil {
		return nil, "", fmt.Errorf("inviting: %w", err)
	}

	_ = s.notifier.Send(ctx, port.Notification{
		Kind: port.NotifyOrgInvitation, Recipient: email,
		Data: map[string]any{"token": plaintext, "expires_at": expiresAt},
	})

	// The plaintext is returned once and never stored.
	return &port.Invitation{
		ID: id, OrganizationID: orgID, Email: email, Role: role,
		InvitedBy: hirer.ID, ExpiresAt: expiresAt,
	}, plaintext, nil
}

// AcceptInvitation creates the seat and signs it in.
//
// The seat inherits the organization's verification rather than earning its
// own — the whole point of verifying an org rather than a person.
func (s *OrganizationService) AcceptInvitation(ctx context.Context, token, displayName, password string) (*domain.Hirer, *domain.TokenPair, error) {
	if displayName == "" || password == "" {
		return nil, nil, fmt.Errorf("accepting needs a name and a password: %w", ErrInvalid)
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, nil, fmt.Errorf("hashing the password: %w", err)
	}

	var (
		created *domain.Hirer
		pair    *domain.TokenPair
	)
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		invitation, err := s.orgs.InvitationByTokenHash(ctx, tx, s.minter.Hash(token))
		if err != nil {
			// A distinguishable response would let an attacker probe for live
			// invitation tokens.
			return ErrNotFound
		}

		created, err = s.orgs.AcceptInvitation(ctx, tx, invitation.ID,
			&domain.Hirer{DisplayName: displayName, AuthProvider: domain.ProviderEmail}, hash)
		if err != nil {
			return err
		}

		plaintext, refreshHash, err := s.minter.Mint()
		if err != nil {
			return err
		}
		session := &domain.Session{
			PrincipalID: string(created.ID), Kind: domain.KindHirer,
			ExpiresAt: s.clock.Now().Add(RefreshTokenTTL),
		}
		if err := s.sessions.Create(ctx, tx, session, refreshHash); err != nil {
			return err
		}
		access, err := s.tokens.Issue(ctx, port.AccessClaims{
			Subject: string(created.ID), Kind: domain.KindHirer,
		}, AccessTokenTTL)
		if err != nil {
			return err
		}
		pair = &domain.TokenPair{
			AccessToken: access, RefreshToken: plaintext,
			ExpiresIn: int(AccessTokenTTL.Seconds()),
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, nil, ErrNotFound
		case errors.Is(err, port.ErrNotFound):
			// Already accepted, or expired. Both are gone as far as the caller
			// is concerned.
			return nil, nil, fmt.Errorf("this invitation is no longer valid: %w", ErrConflict)
		case errors.Is(err, port.ErrConflict):
			return nil, nil, fmt.Errorf("that email already has an account: %w", ErrConflict)
		}
		return nil, nil, fmt.Errorf("accepting the invitation: %w", err)
	}
	return created, pair, nil
}

// Verification tells a hirer where their own review stands.
//
// Read fresh rather than inferred from the principal's VerifiedAt: a seat
// polling this while waiting is asking whether an admin has acted, and
// answering from a fifteen-minute-old token would tell them "still pending"
// after the decision landed.
func (s *OrganizationService) Verification(ctx context.Context, p domain.Principal) (*port.VerificationStatus, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}

	org, err := s.hirers.Organization(ctx, hirer.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("reading the organization: %w", err)
	}

	out := &port.VerificationStatus{
		HirerVerified: hirer.VerifiedAt != nil,
		Organization:  org,
	}

	request, err := s.orgs.VerificationFor(ctx, hirer.ID, hirer.OrganizationID)
	switch {
	case errors.Is(err, port.ErrNotFound):
		// No request was ever raised. A verified org reached that state some
		// other way — a seat inheriting it through an invitation (ADR-0008
		// §3a) — so the status follows the org rather than claiming a review
		// that never happened.
		out.Status = "not_requested"
		if org.IsVerified() {
			out.Status = "approved"
		}
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("reading the verification request: %w", err)
	}

	out.Status = request.Status
	out.SubmittedAt = request.CreatedAt
	out.ReviewedAt = request.ReviewedAt
	out.Reason = request.DecisionReason
	return out, nil
}
