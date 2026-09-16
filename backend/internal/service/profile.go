package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ProfileService owns a contributor's own account of themselves (ADR-0018).
//
// Two rules run through the whole file, and both are about power rather than
// data:
//
//   - A HIRER NEVER SEES COMPENSATION. Not here, not anywhere. A hirer who
//     knows what you will accept offers exactly that, and the expectation
//     stops being a floor and becomes a ceiling.
//   - AVAILABILITY AND THE FLAGS COMPOSE. A person is available for the shapes
//     they enabled and for nothing else, so a live window with no flag set
//     matches nothing — a state reachable by accident and invisible from
//     outside.
type ProfileService struct {
	profiles port.ProfileRepository
	users    port.UserRepository
	places   port.PlaceService

	// github establishes when a stated pull request was AUTHORED (ADR-0019 §7).
	// It is what makes open-source years the one number on a profile that is
	// evidence rather than testimony.
	github port.GitHubClient

	tx    port.TxManager
	clock port.Clock
}

// NewProfileService wires the contributor profile.
func NewProfileService(
	profiles port.ProfileRepository,
	users port.UserRepository,
	places port.PlaceService,
	github port.GitHubClient,
	tx port.TxManager,
	clock port.Clock,
) *ProfileService {
	return &ProfileService{profiles: profiles, users: users, places: places,
		github: github, tx: tx, clock: clock}
}

var _ port.ProfileService = (*ProfileService)(nil)

// Profile returns everything the profile screen shows.
//
// Availability travels with the flags because the two compose: showing what
// shapes somebody will take without whether their window is live would be
// showing half a sentence.
func (s *ProfileService) Profile(ctx context.Context, id domain.UserID) (*port.ContributorProfile, error) {
	return s.profile(ctx, id)
}

// SaveProfile replaces the stated preferences.
func (s *ProfileService) SaveProfile(ctx context.Context, id domain.UserID, in domain.WorkPreferences) (*port.ContributorProfile, error) {
	if err := s.validateWorkPreferences(ctx, in); err != nil {
		return nil, err
	}

	// The id comes from the session, never from the body. A body carrying one
	// would be an invitation to edit somebody else's profile.
	in.UserID = id

	// first_pr_url is WRITE-ONCE. When somebody started contributing is a fact
	// about the past, and a figure revisable whenever it suited would be worth
	// nothing to a hirer reading it.
	//
	// Refused here with a readable error rather than silently ignored: a client
	// that sent a new value and got a 200 would show it back and believe it had
	// saved. The repository also coalesces, so the guarantee does not rest on
	// this check alone.
	existing, err := s.profiles.WorkPreferences(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the existing profile: %w", err)
	}
	switch {
	case existing.FirstPRURL == "":
		// Nothing recorded yet. Whatever they sent, including nothing.

	case in.FirstPRURL == "":
		// OMITTED, not changed. A blank is not an attempt to rewrite history:
		// a client that stops sending an unchangeable field is doing the
		// obvious thing, and refusing it would lock them out of editing their
		// own flags — told their first pull request "cannot be changed" when
		// they never tried to change it.
		//
		// So the stored value is carried forward and the write proceeds.
		in.FirstPRURL = existing.FirstPRURL

	case in.FirstPRURL != existing.FirstPRURL:
		return nil, Coded(ErrConflict, CodeFirstPRIsFixed,
			"your first pull request is already recorded and cannot be changed")
	}

	// Establish when those pull requests were authored, BEFORE anything is
	// written. A refusal we can justify — not merged, not theirs, not there —
	// must stop the write, because first_pr_url is write-once and a bad value
	// accepted here can never be corrected.
	contributor, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the contributor: %w", err)
	}
	dates, err := s.verifyPRs(ctx, contributor.GitHubUserID, existing, &in)
	if err != nil {
		return nil, err
	}

	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.profiles.SaveWorkPreferences(ctx, tx, &in); err != nil {
			return err
		}
		dates.UserID = id
		return s.profiles.SaveVerifiedPRDates(ctx, tx, dates)
	}); err != nil {
		return nil, fmt.Errorf("saving the profile: %w", err)
	}
	return s.profile(ctx, id)
}

// verifyPRs establishes the authored dates for a save (ADR-0019 §7).
//
// The two failure modes are treated as opposites, and that asymmetry is the
// whole design:
//
//   - a fetch we could ANSWER and that disproves the claim — no such pull
//     request, never merged, somebody else's — is refused, and NOTHING is
//     written. Write-once means a bad value accepted here is permanent.
//   - a fetch we could NOT complete — a rate limit, an outage — is not the
//     contributor's fault and must not cost them their save. The URL is stored
//     with no verification stamp, a job retries, and until it lands their
//     open-source years read as unknown.
//
// Unknown does not clear a role's minimum. That is a deliberate fail-CLOSED,
// against the fail-open this codebase otherwise prefers: failing open would
// make any minimum clearable with a link nobody could check.
func (s *ProfileService) verifyPRs(
	ctx context.Context, githubUserID int64,
	existing *domain.WorkPreferences, in *domain.WorkPreferences,
) (port.VerifiedPRDates, error) {
	var out port.VerifiedPRDates

	// The first pull request is verified only when it is NEW. Re-checking one
	// already recorded would let a repository going private years later
	// retract experience somebody has already been credited with.
	if in.FirstPRURL != "" && existing.FirstPRURL != in.FirstPRURL {
		authored, err := s.authoredAt(ctx, githubUserID, in.FirstPRURL)
		if err != nil {
			if !errors.Is(err, port.ErrUnavailable) {
				return out, err
			}
			// Unreachable right now. Store the URL, stamp nothing, retry later.
			return out, nil
		}
		out.FirstAuthoredAt = authored
		now := s.clock.Now()
		out.VerifiedAt = &now
	}

	// The latest one is editable and is re-read whenever it changes. It carries
	// no verification stamp of its own: nothing gates on it, so an outage here
	// costs a display date rather than a filter.
	if in.LatestPRURL != "" && existing.LatestPRURL != in.LatestPRURL {
		authored, err := s.authoredAt(ctx, githubUserID, in.LatestPRURL)
		if err != nil {
			if !errors.Is(err, port.ErrUnavailable) {
				return out, err
			}
			return out, nil
		}
		out.LatestAuthoredAt = authored
	}
	return out, nil
}

// authoredAt reads one pull request and checks it is this person's.
//
// Returns port.ErrUnavailable for a failure that says nothing about the pull
// request, and a coded refusal for one that does.
func (s *ProfileService) authoredAt(ctx context.Context, githubUserID int64, raw string) (*time.Time, error) {
	owner, repo, number, err := domain.ParsePRURL(raw)
	if err != nil {
		return nil, Coded(ErrInvalid, CodeInvalidProfile,
			"%q is not a pull request url", raw)
	}

	facts, err := s.github.PullRequest(ctx, owner, repo, number)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrInvalid, CodePRUnreachable,
				"we cannot read %s — it may be private, deleted, or mistyped", raw)
		}
		// Anything else is a fact about right now rather than about the pull
		// request, and must not be recorded against the contributor.
		return nil, fmt.Errorf("reading %s: %w", raw, port.ErrUnavailable)
	}

	if !facts.Merged {
		return nil, Coded(ErrInvalid, CodePRNotMerged,
			"%s has not been merged", raw)
	}

	// AUTHORSHIP, checked rather than assumed. Without this, "years
	// contributing" is a field anybody fills by pasting a stranger's 2011 pull
	// request — and write-once would make the lie permanent.
	if facts.AuthorUserID != githubUserID {
		return nil, Coded(ErrInvalid, CodePRNotYours,
			"GitHub says %s was written by somebody else", raw)
	}

	authored := facts.CreatedAt
	if authored.IsZero() {
		// A source that supplies no creation date. Fall back to the merge,
		// which is late rather than wrong — it understates experience, which is
		// the safe direction for a figure a hirer filters on.
		if facts.MergedAt == nil {
			return nil, fmt.Errorf("%s carries no dates: %w", raw, port.ErrUnavailable)
		}
		authored = *facts.MergedAt
	}
	return &authored, nil
}

// VerifyPending re-attempts the fetches that could not be completed.
//
// Driven by a job (ADR-0012), never by a request: the contributor whose save
// hit a rate limit is not the person who should have to notice.
func (s *ProfileService) VerifyPending(ctx context.Context, limit int) (int, error) {
	ids, err := s.profiles.PendingVerification(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("listing profiles awaiting verification: %w", err)
	}

	done := 0
	for _, id := range ids {
		prefs, err := s.profiles.WorkPreferences(ctx, id)
		if err != nil {
			return done, fmt.Errorf("reading work preferences for %s: %w", id, err)
		}
		contributor, err := s.users.ByID(ctx, id)
		if err != nil {
			return done, fmt.Errorf("reading contributor %s: %w", id, err)
		}

		authored, err := s.authoredAt(ctx, contributor.GitHubUserID, prefs.FirstPRURL)
		if err != nil {
			if errors.Is(err, port.ErrUnavailable) {
				// Still down. Leave it for the next pass rather than
				// abandoning the rest of the queue.
				continue
			}
			// Answerable and wrong. Nothing is stamped, so the row stays in the
			// queue — which is right: the contributor can correct it, and a
			// stranger's pull request must never become verified experience.
			continue
		}

		now := s.clock.Now()
		if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return s.profiles.SaveVerifiedPRDates(ctx, tx, port.VerifiedPRDates{
				UserID: id, FirstAuthoredAt: authored, VerifiedAt: &now,
			})
		}); err != nil {
			return done, fmt.Errorf("recording verification for %s: %w", id, err)
		}
		done++
	}
	return done, nil
}

// profile assembles the screen's answer.
func (s *ProfileService) profile(ctx context.Context, id domain.UserID) (*port.ContributorProfile, error) {
	prefs, err := s.profiles.WorkPreferences(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading work preferences: %w", err)
	}

	contributor, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the contributor: %w", err)
	}

	return &port.ContributorProfile{
		Preferences:  *prefs,
		Availability: contributor.Availability,
		// Matchable is the composition rule, computed once here rather than in
		// every client: a live window AND at least one shape.
		Matchable: matchable(contributor.Availability, prefs, s.clock.Now()),

		// Never asked, so ask. Somebody who has answered — even by ticking
		// nothing — has answered, and prompting them again would be nagging.
		NeedsAttention: !prefs.Stated,
	}, nil
}

// matchable reports whether any role could reach this contributor.
//
// Both halves, because they compose (ADR-0018 §4):
//
//   - the window must be LIVE. Availability.IsActive already encodes that rule
//     — not_looking is an opt-out, a lapsed window is hidden by default
//     (ADR-0008 §1a) — and reimplementing it here would be a second place for
//     it to drift;
//   - at least one SHAPE must be enabled. A person who never said what work
//     they would take is invisible however live their window is.
//
// The second case is the dangerous one. It is reachable by accident, and a
// contributor in it has no way to tell from outside that nobody can see them,
// which is why this is reported as a field rather than left to a client.
func matchable(a *domain.Availability, w *domain.WorkPreferences, now time.Time) bool {
	return a.IsActive(now) && w.Matchable()
}

// Compensation returns what a contributor expects to be paid.
//
// To the person who typed it, and to nobody else. There is no hirer-facing
// caller of this method and there must never be (ADR-0018 §2).
func (s *ProfileService) Compensation(ctx context.Context, id domain.UserID) (*domain.Compensation, error) {
	out, err := s.profiles.Compensation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading compensation: %w", err)
	}
	return out, nil
}

// SaveCompensation replaces it.
func (s *ProfileService) SaveCompensation(ctx context.Context, id domain.UserID, in domain.Compensation) (*domain.Compensation, error) {
	if err := validateCompensation(in); err != nil {
		return nil, err
	}
	in.UserID = id

	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return s.profiles.SaveCompensation(ctx, tx, &in)
	}); err != nil {
		return nil, fmt.Errorf("saving compensation: %w", err)
	}
	return s.profiles.Compensation(ctx, id)
}

// validateWorkPreferences checks the fields that can be wrong.
//
// Every refusal is CODED. A bare ErrInvalid falls back to invalid_claim at the
// controller, which would tell a contributor their EVIDENCE was rejected when
// what was wrong was a country code — "claim" is a specific noun here.
//
// The flags cannot be: a boolean is always one of two legal answers, and every
// combination of them is a real thing somebody might mean.
func (s *ProfileService) validateWorkPreferences(ctx context.Context, w domain.WorkPreferences) error {
	if w.CurrentCountry != "" {
		if !validCountryCode(w.CurrentCountry) {
			return Coded(ErrInvalid, CodeInvalidProfile,
				"%q is not a two-letter country code", w.CurrentCountry)
		}
		if err := s.knownCountry(ctx, w.CurrentCountry); err != nil {
			return err
		}
	}
	if w.OfficeYOE != nil && (*w.OfficeYOE < 0 || *w.OfficeYOE > 80) {
		// Zero is legal — somebody contributing before their first job — but a
		// negative career is not, and nobody has worked for 80 years.
		return Coded(ErrInvalid, CodeInvalidProfile,
			"office years must be between 0 and 80")
	}
	return nil
}

// validateCompensation refuses an amount with no currency.
//
// A number with no currency is not a rate. Guessing one from the country would
// be worse than refusing: people are paid in currencies they do not live in.
func validateCompensation(c domain.Compensation) error {
	stated := c.HourlyRate != nil || c.YearlyAmount != nil
	if stated && c.Currency == "" {
		return Coded(ErrInvalid, CodeInvalidProfile, "an amount needs a currency")
	}
	if c.Currency != "" && !validCurrencyCode(c.Currency) {
		return Coded(ErrInvalid, CodeInvalidProfile,
			"%q is not a three-letter currency code", c.Currency)
	}
	if c.HourlyRate != nil && *c.HourlyRate <= 0 {
		return Coded(ErrInvalid, CodeInvalidProfile, "an hourly rate must be positive")
	}
	if c.YearlyAmount != nil && *c.YearlyAmount <= 0 {
		return Coded(ErrInvalid, CodeInvalidProfile, "a yearly amount must be positive")
	}
	return nil
}

// knownCountry checks MEMBERSHIP, and fails open.
//
// Decision 9 calls the country list a closed vocabulary two systems must agree
// on — which only means something if somebody checks. Without this, "ZZ" was
// accepted and the promise in §PlaceService that the check "falls back to
// accepting any well-formed code when the provider is down" described a state
// the service was permanently in.
//
// Failing open is deliberate and is the other half of that promise. A provider
// having a bad day must not stop somebody saying where they live: nothing here
// is a security decision, and the shape check above already rejects nonsense.
func (s *ProfileService) knownCountry(ctx context.Context, code string) error {
	countries, err := s.places.Countries(ctx)
	if err != nil || len(countries) == 0 {
		// Open, deliberately.
		//
		//nolint:nilerr // Swallowing this IS the rule (ADR-0018 §PlaceService).
		// A provider having a bad day must not stop somebody saying where they
		// live: nothing here is a security decision, and the shape check above
		// already rejects nonsense. Returning the error would turn a third
		// party's outage into a form nobody can submit.
		return nil
	}
	for _, c := range countries {
		if c.Code == code {
			return nil
		}
	}
	return Coded(ErrInvalid, CodeInvalidProfile, "%q is not a country we know", code)
}

// validCountryCode checks the SHAPE, which is a different question.
//
// Whether "gb!" could ever be a country code is answerable with no network at
// all, and answering it here means a malformed value is refused with a named
// field error rather than surviving a failed-open membership check.
func validCountryCode(s string) bool { return isUpperAlpha(s, 2) }

// validCurrencyCode is the same check, three letters (ISO 4217).
func validCurrencyCode(s string) bool { return isUpperAlpha(s, 3) }

func isUpperAlpha(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
