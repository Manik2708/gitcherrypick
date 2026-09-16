package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// VerificationWindow is how long a proof of address stays redeemable.
//
// Long enough to survive a mail delay or an overnight gap; short enough that a
// forwarded or archived message stops working the next day. Defensible only
// because resend exists — an expiry with no resend is a dead end (ADR-0016 §8).
const VerificationWindow = 24 * time.Hour

// RedemptionService turns a roster entry into a seat, and proves addresses.
//
// Separate from OrganizationService because its callers hold no session: a
// person redeeming an entry has no account yet, so none of these methods take
// a principal and none of them may rely on one.
//
// The rule the whole file exists to enforce: a roster is an ALLOWLIST, not a
// credential. Being listed is a guessable fact, so being listed is never
// sufficient — control of the address is proven first (ADR-0016 §3).
type RedemptionService struct {
	orgs          port.OrganizationRepository
	hirers        port.HirerRepository
	verifications port.EmailVerificationRepository
	sessions      port.SessionRepository
	notifier      port.Notifier
	minter        port.TokenMinter
	tokens        port.TokenIssuer
	hasher        port.PasswordHasher
	tx            port.TxManager
	clock         port.Clock
}

// NewRedemptionService wires roster redemption and address proofs.
func NewRedemptionService(
	orgs port.OrganizationRepository,
	hirers port.HirerRepository,
	verifications port.EmailVerificationRepository,
	sessions port.SessionRepository,
	notifier port.Notifier,
	minter port.TokenMinter,
	tokens port.TokenIssuer,
	hasher port.PasswordHasher,
	tx port.TxManager,
	clock port.Clock,
) *RedemptionService {
	return &RedemptionService{orgs: orgs, hirers: hirers, verifications: verifications,
		sessions: sessions, notifier: notifier, minter: minter, tokens: tokens,
		hasher: hasher, tx: tx, clock: clock}
}

var _ port.RedemptionService = (*RedemptionService)(nil)

// StartRedemption sends a proof if the address is on the organization's roster.
//
// It returns nil whether or not anything was found. A roster miss, an unknown
// organization and an unverified one are the SAME answer on purpose:
// distinguishing them turns this endpoint into an oracle for who an
// organization is hiring, which is exactly what a roster must not become
// (ADR-0016 §3).
//
// The controller maps that silence to 401 without knowing which case it was.
func (s *RedemptionService) StartRedemption(ctx context.Context, orgSlug, email string) error {
	if orgSlug == "" || email == "" {
		return fmt.Errorf("a redemption needs an organization and an email: %w", ErrInvalid)
	}

	org, err := s.hirers.OrganizationBySlug(ctx, orgSlug)
	if err != nil {
		// Not found, or not verified. Neither is disclosed.
		//
		//nolint:nilerr // Swallowing this IS the rule (ADR-0016 §3). Returning
		// the error would let a caller tell an unknown organization from a real
		// one, which is the first half of the oracle this endpoint must not be.
		return nil
	}

	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return fmt.Errorf("minting the proof: %w", err)
	}
	expiresAt := s.clock.Now().Add(VerificationWindow)

	var sendTo string
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		entry, err := s.orgs.RosterEntryByEmail(ctx, tx, org.ID, email)
		if err != nil {
			// Not on the roster, or already redeemed. The second half of the
			// same rule: a miss must be indistinguishable from a hit.
			//
			//nolint:nilerr // Deliberate, per ADR-0016 §3.
			return nil
		}
		if _, err := s.verifications.Create(ctx, tx, &port.EmailVerification{
			Email:     entry.Email,
			Purpose:   port.VerifyRosterRedemption,
			RosterID:  &entry.ID,
			ExpiresAt: expiresAt,
		}, hash); err != nil {
			return err
		}
		sendTo = entry.Email
		return nil
	})
	if err != nil {
		return fmt.Errorf("starting redemption: %w", err)
	}
	if sendTo == "" {
		return nil
	}

	// Delivery failure is NOT swallowed here, unlike a contact-request
	// notification: the proof is the only way in, so a caller told "check your
	// email" when nothing was sent has no way to discover that.
	if err := s.notifier.Send(ctx, port.Notification{
		Kind: port.NotifyEmailVerification, Recipient: sendTo,
		Data: map[string]any{
			"token": plaintext, "expires_at": expiresAt,
			"organization": org.Name, "purpose": string(port.VerifyRosterRedemption),
		},
	}); err != nil {
		return fmt.Errorf("sending the proof: %w", err)
	}
	return nil
}

// CompleteRedemption consumes the proof and creates the seat.
//
// The username and role come from the ENTRY, never from the caller: a redeemer
// who could name themselves could take a colleague's name, or claim an owner
// seat and roster further people from it (ADR-0016 §3).
func (s *RedemptionService) CompleteRedemption(ctx context.Context, token, displayName, password string) (*domain.Hirer, *domain.TokenPair, error) {
	if token == "" {
		return nil, nil, fmt.Errorf("a redemption needs its token: %w", ErrInvalid)
	}
	if displayName == "" {
		return nil, nil, fmt.Errorf("a seat needs a display name: %w", ErrInvalid)
	}

	passwordHash, err := s.hasher.Hash(password)
	if err != nil {
		return nil, nil, fmt.Errorf("hashing the password: %w", err)
	}

	var (
		created *domain.Hirer
		pair    *domain.TokenPair
	)
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		proof, err := s.verifications.ByTokenHash(ctx, tx, s.minter.Hash(token))
		if err != nil {
			return Coded(ErrInvalidCredentials, CodeVerificationRequired,
				"that verification link does not exist")
		}
		if proof.Purpose != port.VerifyRosterRedemption || proof.RosterID == nil {
			return Coded(ErrInvalidCredentials, CodeVerificationRequired,
				"that link does not redeem a seat")
		}

		// Spent and expired are told apart deliberately: the remedy differs.
		// A spent link means the seat already exists and the holder should
		// sign in; an expired one means ask for another.
		if proof.ConsumedAt != nil {
			return Coded(ErrInvalidCredentials, CodeVerificationSpent,
				"that verification link was already used at %s", proof.ConsumedAt)
		}
		if !proof.ExpiresAt.After(s.clock.Now()) {
			return Coded(ErrInvalidCredentials, CodeVerificationExpired,
				"that verification link expired at %s", proof.ExpiresAt)
		}

		if err := s.verifications.Consume(ctx, tx, proof.ID, s.clock.Now()); err != nil {
			return err
		}

		created, err = s.orgs.RedeemRosterEntry(ctx, tx, *proof.RosterID, &domain.Hirer{
			DisplayName:  displayName,
			AuthProvider: domain.ProviderEmail,
		}, passwordHash, s.clock.Now())
		if err != nil {
			return err
		}

		pair, err = s.startSession(ctx, tx, created)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, port.ErrConflict) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("completing redemption: %w", err)
	}
	return created, pair, nil
}

// ResendVerification re-sends an outstanding proof.
//
// It re-sends the EXISTING one rather than minting a second: two live tokens
// for one address means two ways in, and the first one delivered stays valid
// after the second is issued.
//
// Silent about whether anything was found, for the same reason
// StartRedemption is.
func (s *RedemptionService) ResendVerification(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("a resend needs an email: %w", ErrInvalid)
	}

	// The stored token is a hash, so the original plaintext is gone — it was
	// shown once, to the inbox. A resend therefore rotates: the outstanding
	// proof is consumed and a fresh one issued, which keeps the invariant that
	// exactly one token is live for an address.
	//
	// ANY purpose. Filtering to roster redemption would leave a company waiting
	// on an onboarding code with no way to ask for another, and that code is
	// the only way forward (ADR-0017 §2).
	outstanding, err := s.verifications.Outstanding(ctx, email)
	if err != nil {
		// No live proof for that address. Silent for the same reason
		// StartRedemption is: a resend that reported "nothing outstanding"
		// would say whether something is in flight, and therefore whether the
		// address is rostered or mid-onboarding.
		//
		//nolint:nilerr // Deliberate, per ADR-0016 §3.
		return nil
	}

	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return fmt.Errorf("minting the replacement proof: %w", err)
	}
	expiresAt := s.clock.Now().Add(VerificationWindow)

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.verifications.Consume(ctx, tx, outstanding.ID, s.clock.Now()); err != nil {
			return err
		}
		// The replacement carries the SAME subject. A rotated onboarding proof
		// that forgot its submission would be a code unlocking nothing, and
		// ck_verification_subject refuses it outright.
		_, err := s.verifications.Create(ctx, tx, &port.EmailVerification{
			Email:        outstanding.Email,
			Purpose:      outstanding.Purpose,
			RosterID:     outstanding.RosterID,
			OnboardingID: outstanding.OnboardingID,
			ExpiresAt:    expiresAt,
		}, hash)
		return err
	})
	if err != nil {
		return fmt.Errorf("resending the proof: %w", err)
	}

	if err := s.notifier.Send(ctx, port.Notification{
		Kind: resendKind(outstanding.Purpose), Recipient: outstanding.Email,
		Data: map[string]any{
			"token": plaintext, "expires_at": expiresAt,
			"purpose": string(outstanding.Purpose),
		},
	}); err != nil {
		return fmt.Errorf("sending the proof: %w", err)
	}
	return nil
}

// resendKind picks the message a rotated proof is sent as.
//
// The kind decides which screen the link points at (claimPath in the resend
// adapter), so sending an onboarding code as a roster message would hand the
// reader a valid code and the wrong door.
func resendKind(purpose port.EmailVerificationPurpose) port.NotificationKind {
	if purpose == port.VerifyOrganizationOnboarding {
		return port.NotifyOnboardingVerification
	}
	return port.NotifyEmailVerification
}

// startSession mints the pair a newly created seat signs in with.
func (s *RedemptionService) startSession(ctx context.Context, tx port.Tx, h *domain.Hirer) (*domain.TokenPair, error) {
	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return nil, fmt.Errorf("minting the refresh token: %w", err)
	}

	p := hirerPrincipalOf(h)
	session := &domain.Session{
		PrincipalID: p.Subject(),
		Kind:        p.Kind,
		ExpiresAt:   s.clock.Now().Add(RefreshTokenTTL),
	}
	if err := s.sessions.Create(ctx, tx, session, hash); err != nil {
		return nil, fmt.Errorf("creating the session: %w", err)
	}

	access, err := s.tokens.Issue(ctx, port.AccessClaims{
		Subject: p.Subject(), Kind: p.Kind, Family: session.FamilyID,
	}, AccessTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing the access token: %w", err)
	}
	return &domain.TokenPair{
		AccessToken: access, RefreshToken: plaintext,
		ExpiresIn: int(AccessTokenTTL.Seconds()),
	}, nil
}

// ListOrganizations returns the verified organisations a redeemer picks from.
//
// Unauthenticated, and therefore deliberately narrow: it is the repository's
// verified-only query, and the controller strips it to name and slug. An
// unverified organisation is absent — it cannot seat anyone, so listing it
// would only produce a redemption that silently fails (ADR-0016 §3a).
func (s *RedemptionService) ListOrganizations(ctx context.Context) ([]domain.Organization, error) {
	orgs, err := s.hirers.ListVerifiedOrganizations(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", err)
	}
	return orgs, nil
}
