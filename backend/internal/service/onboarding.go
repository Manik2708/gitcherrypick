package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OnboardingWindow is how long a company has to confirm its address.
//
// The same 24 hours a roster proof gets (ADR-0016 §4), and for the same
// reason: long enough to survive a mail delay, short enough that a forwarded
// message stops working the next day.
const OnboardingWindow = 24 * time.Hour

// OnboardingService turns a submitted form into a company, via an
// administrator.
//
// The rule the whole file exists to enforce: **the form creates nothing**. It
// is public, so anything it could create directly, anyone could create — a
// hundred company names in a minute, each one blocking the real company from
// ever claiming it. An organisation, its address and its owner seat come into
// existence together, when a person approves them (ADR-0017 §2, §3).
type OnboardingService struct {
	onboarding    port.OnboardingRepository
	orgs          port.OrganizationRepository
	verifications port.EmailVerificationRepository
	notifier      port.Notifier
	minter        port.TokenMinter
	hasher        port.PasswordHasher
	tx            port.TxManager
	clock         port.Clock
}

// NewOnboardingService wires organisation onboarding.
func NewOnboardingService(
	onboarding port.OnboardingRepository,
	orgs port.OrganizationRepository,
	verifications port.EmailVerificationRepository,
	notifier port.Notifier,
	minter port.TokenMinter,
	hasher port.PasswordHasher,
	tx port.TxManager,
	clock port.Clock,
) *OnboardingService {
	return &OnboardingService{onboarding: onboarding, orgs: orgs,
		verifications: verifications, notifier: notifier, minter: minter,
		hasher: hasher, tx: tx, clock: clock}
}

var _ port.OnboardingService = (*OnboardingService)(nil)

// Submit records a form and emails a code.
//
// It returns nothing on success — not an id, not a status. A caller told "that
// name is already submitted" could enumerate the companies mid-onboarding, one
// guess at a time, before the public picker would ever list them.
func (s *OnboardingService) Submit(ctx context.Context, in port.OnboardingForm) error {
	if err := validateOnboarding(in); err != nil {
		return err
	}
	return s.submit(ctx, in, nil)
}

// Revise records a correction to a rejected submission.
//
// The code identifies WHICH submission is being corrected, so a rejection can
// only be revised by whoever received the rejection — the company address. A
// revision keyed on the submission id alone would let anyone rewrite any
// refused company's answers.
func (s *OnboardingService) Revise(ctx context.Context, token string, in port.OnboardingForm) error {
	if err := validateOnboarding(in); err != nil {
		return err
	}

	var supersedes *domain.OnboardingID
	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		proof, err := s.spend(ctx, tx, token)
		if err != nil {
			return err
		}
		previous, err := s.onboarding.ByID(ctx, tx, *proof.OnboardingID)
		if err != nil {
			return err
		}
		// Only a REFUSED submission is revisable. One awaiting review is not:
		// an administrator may be reading it, and one already approved is a
		// company, which is edited through /orgs and not through this.
		if !previous.Decided() || previous.OrganizationID != nil {
			return Coded(ErrConflict, CodeVerificationSpent,
				"that submission is not awaiting a correction")
		}
		supersedes = &previous.ID
		return nil
	})
	if err != nil {
		return err
	}
	return s.submit(ctx, in, supersedes)
}

// submit is the shared half of Submit and Revise.
func (s *OnboardingService) submit(ctx context.Context, in port.OnboardingForm, supersedes *domain.OnboardingID) error {
	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return fmt.Errorf("minting the onboarding code: %w", err)
	}
	expiresAt := s.clock.Now().Add(OnboardingWindow)

	var submission *port.OnboardingSubmission
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		submission, err = s.onboarding.Submit(ctx, tx, &port.OnboardingSubmission{
			Name: in.Name, Description: in.Description, Email: in.Email,
			Phone: in.Phone, Headcount: in.Headcount,
			Country: in.Country, City: in.City, PostalCode: in.PostalCode,
			Street1: in.Street1, Street2: in.Street2,
			SupersedesID: supersedes,
		})
		if err != nil {
			return err
		}
		_, err = s.verifications.Create(ctx, tx, &port.EmailVerification{
			Email:        in.Email,
			Purpose:      port.VerifyOrganizationOnboarding,
			OnboardingID: &submission.ID,
			ExpiresAt:    expiresAt,
		}, hash)
		return err
	})
	if err != nil {
		return fmt.Errorf("submitting %q: %w", in.Name, err)
	}

	// Delivery failure is NOT swallowed. The code is the only way forward, so
	// a submitter told "check your email" when nothing was sent has no way to
	// discover that and no way to proceed.
	if err := s.notifier.Send(ctx, port.Notification{
		Kind: port.NotifyOnboardingVerification, Recipient: in.Email,
		Data: map[string]any{
			"token": plaintext, "expires_at": expiresAt,
			"organization": in.Name,
		},
	}); err != nil {
		return fmt.Errorf("sending the onboarding code: %w", err)
	}
	return nil
}

// Verify spends the code and records how the owner will sign in.
//
// No session is returned. There is no account to sign in as — approval is what
// creates one — so a token here would be a credential for a seat that does not
// exist (ADR-0017 §6).
func (s *OnboardingService) Verify(ctx context.Context, token string, owner port.OnboardingOwnerInput) error {
	if owner.Username == "" {
		return fmt.Errorf("an owner needs a username: %w", ErrInvalid)
	}
	if owner.DisplayName == "" {
		return fmt.Errorf("an owner needs a display name: %w", ErrInvalid)
	}

	hash, err := s.hasher.Hash(owner.Password)
	if err != nil {
		return fmt.Errorf("hashing the password: %w", err)
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		proof, err := s.spend(ctx, tx, token)
		if err != nil {
			return err
		}

		// The name is claimed NOW, not at approval. Two submissions could
		// otherwise both hold "jo" and discover it only when the second is
		// approved — by which point a person has been told they own a company
		// they cannot sign in to (ADR-0017 §Identity).
		if err := s.orgs.ClaimUsername(ctx, tx, owner.Username); err != nil {
			if errors.Is(err, port.ErrConflict) {
				return Coded(ErrConflict, CodeUsernameTaken,
					"the username %q is not available", owner.Username)
			}
			return err
		}

		return s.onboarding.Prove(ctx, tx, *proof.OnboardingID, port.OnboardingOwner{
			Username: owner.Username, DisplayName: owner.DisplayName,
			PasswordHash: hash,
		}, s.clock.Now())
	})
	if err != nil {
		// A coded failure is already the answer a caller needs — which of the
		// three ways the code failed, or that the username is taken. Wrapping
		// it would bury the code the controller branches on.
		if CodeOf(err) != "" {
			return err
		}
		return fmt.Errorf("verifying the onboarding code: %w", err)
	}
	return nil
}

// spend resolves a code, checks it, and consumes it.
//
// Shared by Verify and Revise because both are "prove you received the mail we
// sent about this submission", and the three ways a code fails — never
// existed, already used, expired — mean different things to the holder.
func (s *OnboardingService) spend(ctx context.Context, tx port.Tx, token string) (*port.EmailVerification, error) {
	if token == "" {
		return nil, Coded(ErrInvalidCredentials, CodeVerificationRequired,
			"that link does not exist")
	}

	proof, err := s.verifications.ByTokenHash(ctx, tx, s.minter.Hash(token))
	if err != nil {
		return nil, Coded(ErrInvalidCredentials, CodeVerificationRequired,
			"that link does not exist")
	}
	if proof.Purpose != port.VerifyOrganizationOnboarding || proof.OnboardingID == nil {
		return nil, Coded(ErrInvalidCredentials, CodeVerificationRequired,
			"that link does not onboard an organisation")
	}
	if proof.ConsumedAt != nil {
		return nil, Coded(ErrInvalidCredentials, CodeVerificationSpent,
			"that link was already used at %s", proof.ConsumedAt)
	}
	if !proof.ExpiresAt.After(s.clock.Now()) {
		return nil, Coded(ErrInvalidCredentials, CodeVerificationExpired,
			"that link expired at %s", proof.ExpiresAt)
	}

	if err := s.verifications.Consume(ctx, tx, proof.ID, s.clock.Now()); err != nil {
		return nil, err
	}
	return proof, nil
}

// Queue is the administrator's view.
func (s *OnboardingService) Queue(ctx context.Context, p domain.Principal) ([]port.OnboardingSubmission, error) {
	if _, err := requireAdmin(p); err != nil {
		return nil, err
	}
	return s.onboarding.Queue(ctx)
}

// Decide approves or refuses a submission.
//
// Approving is the first administrative decision in this platform that the
// DATA can refuse: two submissions may name one company, nothing reserves a
// slug, and only the first approved can have it. The administrator is told
// which, rather than the request failing silently.
func (s *OnboardingService) Decide(ctx context.Context, p domain.Principal, id domain.OnboardingID, approve bool, reason string) error {
	adminID, err := requireAdmin(p)
	if err != nil {
		return err
	}
	if !approve && reason == "" {
		// The same rule every other decision follows: a refusal without a
		// reason is one the company cannot act on.
		return Coded(ErrInvalid, CodeReasonRequired, "a rejection must carry a reason")
	}

	var (
		org        *domain.Organization
		submission *port.OnboardingSubmission
		reviseCode string
	)
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		if submission, err = s.onboarding.ByID(ctx, tx, id); err != nil {
			return err
		}
		if !approve {
			if err := s.onboarding.Reject(ctx, tx, id, adminID, reason, s.clock.Now()); err != nil {
				return err
			}
			// A refusal MINTS A FRESH CODE, or the company cannot answer it.
			//
			// Revise spends an onboarding proof, and the original was consumed
			// at verify — so without this a rejected company holds a reason, a
			// link, and nothing that opens it. The route exists and reaches
			// nobody (ADR-0017 §8).
			plaintext, hash, err := s.minter.Mint()
			if err != nil {
				return fmt.Errorf("minting the revise code: %w", err)
			}
			if _, err := s.verifications.Create(ctx, tx, &port.EmailVerification{
				Email:        submission.Email,
				Purpose:      port.VerifyOrganizationOnboarding,
				OnboardingID: &submission.ID,
				ExpiresAt:    s.clock.Now().Add(OnboardingWindow),
			}, hash); err != nil {
				return err
			}
			reviseCode = plaintext
			return nil
		}
		org, err = s.onboarding.Promote(ctx, tx, id, slugify(submission.Name),
			adminID, s.clock.Now())
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return Coded(ErrConflict, CodeSlugTaken,
				"another organisation already holds that name")
		}
		return fmt.Errorf("deciding submission %s: %w", id, err)
	}

	s.notifyDecision(ctx, submission, org, approve, reason, reviseCode)
	return nil
}

// notifyDecision tells the company what happened.
//
// Failure is swallowed, unlike the code itself: a decision has already landed
// in the database, and an undelivered notice about it is worth a log line
// rather than undoing an administrator's work.
func (s *OnboardingService) notifyDecision(ctx context.Context, sub *port.OnboardingSubmission, org *domain.Organization, approved bool, reason, reviseCode string) {
	if sub == nil {
		return
	}
	data := map[string]any{
		"approved": approved, "organization": sub.Name,
		"username": sub.OwnerUsername, "submission_id": string(sub.ID),
	}
	if !approved {
		data["reason"] = reason
		// The code is what makes the refusal answerable. Without it the link
		// in the mail opens a form the company cannot submit.
		data["token"] = reviseCode
	}
	if org != nil {
		data["organization"] = org.Name
	}
	_ = s.notifier.Send(ctx, port.Notification{
		Kind: port.NotifyOnboardingDecided, Recipient: sub.Email, Data: data,
	})
}

// ExpireUnproven deletes submissions whose code lapsed unused.
func (s *OnboardingService) ExpireUnproven(ctx context.Context) (int, error) {
	n, err := s.onboarding.ExpireUnproven(ctx, s.clock.Now().Add(-OnboardingWindow))
	if err != nil {
		return 0, fmt.Errorf("expiring unproven submissions: %w", err)
	}
	return n, nil
}

// validateOnboarding checks the two fields that are not optional.
//
// Name and email only. Everything else is genuinely optional: a fully remote
// company has no office to name, a registered address is often an accountant's
// rather than the company's, and several countries have no postcode.
//
// NOT because "a freelancer has none" — an earlier comment said that, and it
// was wrong. A freelancer never reaches this form: they are an independent
// hirer and register through /auth/hirer/register (ADR-0017 §1). Everyone here
// is a company.
func validateOnboarding(in port.OnboardingForm) error {
	if in.Name == "" {
		return fmt.Errorf("an organisation needs a name: %w", ErrInvalid)
	}
	if in.Email == "" {
		return fmt.Errorf("an organisation needs a contact email: %w", ErrInvalid)
	}
	if in.Headcount != "" && !domain.ValidHeadcountBand(in.Headcount) {
		return fmt.Errorf("%q is not a headcount band: %w", in.Headcount, ErrInvalid)
	}
	// An address is all of it or none of it, matching ck_onboarding_address. A
	// city with no country is not a place anybody can post to.
	partial := in.Country != "" || in.City != "" || in.Street1 != ""
	complete := in.Country != "" && in.City != "" && in.Street1 != ""
	if partial && !complete {
		return fmt.Errorf("an address needs a country, a city and a street: %w", ErrInvalid)
	}
	return nil
}
