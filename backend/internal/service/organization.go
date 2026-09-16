package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OrganizationService owns seat rosters.
//
// An organization self-administers its seats, with no admin involvement
// (ADR-0002): requiring approval for every recruiter at an already verified
// company would make the queue unworkable and buys nothing.
//
// A roster is an allowlist, not a credential. Being on one is a guessable
// fact, so it grants nothing by itself — redeeming an entry also requires
// proving control of the address, which RedemptionService owns.
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

// AddToRoster names an address, a username and a role for a future seat.
//
// OWNERS ONLY. An owner entry grants the power to grant further seats, so a
// member who could roster could promote themselves through one (ADR-0016 §7).
//
// Capability is NOT required: an unverified organization may build its roster
// while it waits for review. Redeeming an entry is what requires a verified
// org, because that is the moment an unknown person becomes a hirer inside it.
func (s *OrganizationService) AddToRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, email, username string, role domain.OrgRole) (*port.RosterEntry, error) {
	hirer, err := s.requireOwnerOf(p, orgID)
	if err != nil {
		return nil, err
	}
	if email == "" {
		return nil, fmt.Errorf("a roster entry needs an email: %w", ErrInvalid)
	}
	if username == "" {
		return nil, fmt.Errorf("a roster entry needs a username: %w", ErrInvalid)
	}
	if role == "" {
		role = domain.RoleMember
	}

	var entry *port.RosterEntry
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		// The name is claimed FIRST, out of the global namespace. Leaving it
		// to the roster's own unique constraint would miss a name a LIVE SEAT
		// already holds — that clash then surfaces at redemption, to the wrong
		// person, after the offer has already been made (ADR-0016 §Identity).
		if err := s.orgs.ClaimUsername(ctx, tx, username); err != nil {
			return err
		}

		var err error
		entry, err = s.orgs.AddRosterEntry(ctx, tx, &port.RosterEntry{
			OrganizationID: orgID, Email: email, Username: username,
			Role: role, AddedBy: domain.RefTo(hirer),
		})
		return err
	})
	if err != nil {
		// A username taken anywhere on the platform surfaces as a conflict
		// naming nothing: who holds it, and where, is not this caller's
		// business (ADR-0009's pattern).
		if errors.Is(err, port.ErrConflict) {
			return nil, Coded(ErrConflict, CodeUsernameTaken,
				"the username %q is not available", username)
		}
		return nil, fmt.Errorf("rostering %q: %w", email, err)
	}
	return entry, nil
}

// ListRoster returns every entry, redeemed or not. Owners only: the roster is
// the list of who MAY join, which is a hiring plan.
func (s *OrganizationService) ListRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID) ([]port.RosterEntry, error) {
	if _, err := s.requireOwnerOf(p, orgID); err != nil {
		return nil, err
	}
	return s.orgs.ListRoster(ctx, orgID)
}

// RemoveFromRoster deletes the entry and revokes the seat it created.
//
// It never deletes the seat. That row authored shortlists and the permanent
// record of which candidates were told an organization was interested
// (ADR-0008), and deleting it would orphan the history a contributor relies on
// to know who approached them (ADR-0016 §5).
//
// The password is NOT rotated and no credential is issued to anyone. A
// disabled seat cannot sign in whatever its password is, so rotation would add
// nothing; handing an owner a working credential for a seat they do not hold
// would attribute their actions to the departed person.
func (s *OrganizationService) RemoveFromRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, id domain.RosterEntryID) error {
	owner, err := s.requireOwnerOf(p, orgID)
	if err != nil {
		return err
	}

	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		entry, err := s.orgs.RosterEntryByID(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("reading roster entry %s: %w", id, err)
		}
		// An entry belonging to another organization is not this owner's to
		// remove, and saying so is not a disclosure: they supplied the org id.
		if entry.OrganizationID != orgID {
			return fmt.Errorf("that entry belongs to another organization: %w", ErrForbidden)
		}

		if err := s.orgs.RemoveRosterEntry(ctx, tx, id); err != nil {
			return err
		}

		// An unredeemed entry has no seat behind it, so there is nothing to
		// revoke — removing it simply withdraws the offer, and the name goes
		// back. Nobody ever signed in as it, so releasing it erases nothing,
		// and burning a name over a typo would make the roster a trap.
		//
		// A name a seat HAS held is never released. The foreign key from
		// hirer_accounts enforces that, rather than this branch having to.
		if entry.RedeemedBy == nil {
			return s.orgs.ReleaseUsername(ctx, tx, entry.Username)
		}

		seat := *entry.RedeemedBy
		if err := s.hirers.Disable(ctx, tx, seat, owner.ID, s.clock.Now()); err != nil {
			return err
		}
		// Every family, not just one: access ends on every device, bounded
		// only by the 15-minute access token still in flight.
		return s.sessions.RevokeAllForPrincipal(ctx, tx, string(seat))
	})
}

// ListSeats returns live and revoked seats.
//
// Readable by ANY hirer in the organization, not owners only. Shortlists are
// already org-scoped, so every seat already sees every round — naming their
// author exposes no row that was not already visible (ADR-0016 §5a).
func (s *OrganizationService) ListSeats(ctx context.Context, p domain.Principal, orgID domain.OrganizationID) ([]domain.Hirer, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if hirer.OrganizationID != orgID {
		return nil, fmt.Errorf("you are not a member of that organization: %w", ErrForbidden)
	}
	return s.hirers.ListSeats(ctx, orgID)
}

// requireOwnerOf resolves the caller as an owner of the named organization.
//
// The two refusals are deliberately different: not being in the org at all and
// being in it without authority are distinct facts, and the second is
// actionable — ask an owner.
func (s *OrganizationService) requireOwnerOf(p domain.Principal, orgID domain.OrganizationID) (*domain.Hirer, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if hirer.OrganizationID != orgID {
		return nil, fmt.Errorf("you are not a member of that organization: %w", ErrForbidden)
	}
	if hirer.OrgRole != domain.RoleOwner {
		return nil, fmt.Errorf("only an owner may change the roster: %w", ErrForbidden)
	}
	return hirer, nil
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

	// An INDEPENDENT hirer has no organisation (ADR-0017 §1), so there is
	// none to read. Reading one anyway is how this endpoint used to fail for
	// exactly the account that needs it most: someone who has just registered
	// and is waiting to hear whether they were approved.
	var org *domain.Organization
	if hirer.OrganizationID != "" {
		var err error
		if org, err = s.hirers.Organization(ctx, hirer.OrganizationID); err != nil {
			return nil, fmt.Errorf("reading the organization: %w", err)
		}
	}

	out := &port.VerificationStatus{
		HirerVerified: hirer.VerifiedAt != nil,
		Organization:  org,
	}

	request, err := s.orgs.VerificationFor(ctx, hirer.ID, hirer.OrganizationID)
	switch {
	case errors.Is(err, port.ErrNotFound):
		// No request was ever raised. An organisation reached verification
		// some other way — a seat inheriting it (ADR-0008 §3a), or onboarding,
		// where approving the submission IS the verification and no request
		// row is created (ADR-0017 §2) — so the status follows the org rather
		// than claiming a review that never happened.
		//
		// org is nil for an independent hirer, and IsVerified reports false on
		// a nil receiver, so their status stays "not_requested" until their own
		// review lands.
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
