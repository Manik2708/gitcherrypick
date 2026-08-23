package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ClaimService owns the claim lifecycle and the validation pipeline.
//
// The pipeline runs in a fixed order so cheap local failures precede API calls
// (ADR-0003): structural, parse, local dedupe, pair check, GitHub, enrich. A
// claim that is 6 PRs long should not cost six GitHub round trips to discover
// it is too long.
//
// Every failure names the SPECIFIC evidence row. The contributor is doing
// curation work and a vague rejection wastes it — which is why Submit returns
// a slice of failures rather than one error.
type ClaimService struct {
	claims port.ClaimRepository
	skills port.SkillRepository
	users  port.UserRepository
	github port.GitHubClient
	broker port.Broker
	tx     port.TxManager
	clock  port.Clock
}

// NewClaimService wires the claim pipeline.
func NewClaimService(
	claims port.ClaimRepository,
	skills port.SkillRepository,
	users port.UserRepository,
	github port.GitHubClient,
	broker port.Broker,
	tx port.TxManager,
	clock port.Clock,
) *ClaimService {
	return &ClaimService{claims: claims, skills: skills, users: users,
		github: github, broker: broker, tx: tx, clock: clock}
}

// Cardinality bounds from ADR-0003.
const (
	MaxPRs      = 5
	MaxProjects = 20
)

// Create opens an empty draft.
func (s *ClaimService) Create(ctx context.Context, id domain.UserID) (*domain.Claim, error) {
	c, err := s.claims.Create(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("creating claim: %w", err)
	}
	return c, nil
}

// Get reads one of the caller's own claims.
//
// Someone else's claim is NOT FOUND, never forbidden. A 403 confirms the id
// exists, and claim ids are otherwise unguessable — the pair (403 on real, 404
// on fake) turns the endpoint into an existence oracle over other people's
// evidence (ADR-0008 §6).
func (s *ClaimService) Get(ctx context.Context, id domain.UserID, claimID domain.ClaimID) (*domain.Claim, error) {
	c, err := s.claims.ByID(ctx, claimID)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading claim: %w", err)
	}
	if c.UserID != id {
		return nil, ErrNotFound
	}
	return c, nil
}

// List reads the caller's claims, newest first.
func (s *ClaimService) List(ctx context.Context, id domain.UserID) ([]domain.Claim, error) {
	out, err := s.claims.ListByUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing claims: %w", err)
	}
	return out, nil
}

// Replace is the whole-claim edit.
//
// Structural validation runs BEFORE the write, so a claim that could never be
// valid never reaches the database — and a rejected replace leaves the version
// untouched, which is what makes the optimistic-concurrency check meaningful.
func (s *ClaimService) Replace(ctx context.Context, id domain.UserID, claimID domain.ClaimID, version int, c *domain.Claim) (*domain.Claim, error) {
	if _, err := s.Get(ctx, id, claimID); err != nil {
		return nil, err
	}
	if failures := validateStructure(c); len(failures) > 0 {
		return nil, fmt.Errorf("%s: %w", failures[0].Message, ErrInvalid)
	}

	var out *domain.Claim
	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = s.claims.Replace(ctx, tx, claimID, version, c)
		return err
	})
	switch {
	case errors.Is(err, port.ErrVersionStale):
		return nil, fmt.Errorf("the claim changed since you loaded it: %w", ErrConflict)
	case errors.Is(err, port.ErrConflict):
		// The seven-day lock. Surfaced with the repository's message, which
		// carries the expiry — ADR-0003 wants the contributor told WHEN, not
		// merely that.
		return nil, fmt.Errorf("%w: %w", err, ErrConflict)
	case err != nil:
		return nil, fmt.Errorf("replacing claim: %w", err)
	}
	return out, nil
}

// SetPREvidence replaces the PR evidence, leaving projects and skills alone.
func (s *ClaimService) SetPREvidence(ctx context.Context, id domain.UserID, claimID domain.ClaimID, prs []domain.PREvidence) (*domain.Claim, error) {
	return s.patch(ctx, id, claimID, func(c *domain.Claim) { c.PREvidence = prs })
}

// SetProjectEvidence replaces the supporting projects.
func (s *ClaimService) SetProjectEvidence(ctx context.Context, id domain.UserID, claimID domain.ClaimID, projects []domain.ProjectEvidence) (*domain.Claim, error) {
	return s.patch(ctx, id, claimID, func(c *domain.Claim) { c.ProjectEvidence = projects })
}

// SetSkills replaces the declared skills.
func (s *ClaimService) SetSkills(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skills []domain.ClaimSkill) (*domain.Claim, error) {
	return s.patch(ctx, id, claimID, func(c *domain.Claim) { c.Skills = skills })
}

// patch reads, applies one change, and replaces at the current version.
//
// The three sub-resource writes are a read-modify-write on top of Replace
// rather than three more repository methods. One write path means the lock and
// the version check cannot be enforced in one place and forgotten in another.
func (s *ClaimService) patch(ctx context.Context, id domain.UserID, claimID domain.ClaimID, apply func(*domain.Claim)) (*domain.Claim, error) {
	current, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, err
	}
	apply(current)

	var out *domain.Claim
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = s.claims.Replace(ctx, tx, claimID, current.Version, current)
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, fmt.Errorf("%w: %w", err, ErrConflict)
		}
		return nil, fmt.Errorf("updating claim: %w", err)
	}
	return out, nil
}

// Submit runs the validation pipeline and enqueues the evaluation.
//
// The links and the job are written in ONE transaction — the outbox property
// (ADR-0004). A claim submitted with no job would never be judged; a job with
// no links would spend a model call on evidence whose uniqueness was never
// checked.
//
// A validation failure returns the failures and writes NOTHING. A failed
// validation must never spend a model call.
func (s *ClaimService) Submit(ctx context.Context, id domain.UserID, claimID domain.ClaimID) (*domain.Claim, []port.ValidationFailure, error) {
	claim, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, nil, err
	}
	if claim.IsLocked(s.clock.Now()) {
		return nil, nil, fmt.Errorf("claim %s is locked until %s: %w",
			claimID, claim.LockedUntil.Format(time.RFC3339), ErrConflict)
	}

	contributor, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("reading contributor: %w", err)
	}

	// 1-3: structural, parse, local dedupe. All local, all before any API call.
	failures := validateStructure(claim)
	failures = append(failures, validateNoDuplicates(claim)...)

	// 4: the pair check. Also local, and it is the cheapest way to reject a
	// resubmission of evidence already spent on this skill.
	links := buildLinks(id, claim)

	// 5: GitHub. Reached only when everything cheaper has passed.
	if len(failures) == 0 {
		failures = append(failures, s.validateAgainstGitHub(ctx, claim, contributor)...)
	}

	if len(failures) > 0 {
		if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return s.claims.SetStatus(ctx, tx, claimID, domain.ClaimInvalid)
		}); err != nil {
			return nil, nil, fmt.Errorf("marking claim invalid: %w", err)
		}
		return nil, failures, nil
	}

	// The outbox. Links, status and job in one transaction: all three or none.
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.skills.LinkPairs(ctx, tx, links); err != nil {
			return err
		}
		if err := s.claims.SetStatus(ctx, tx, claimID, domain.ClaimQueued); err != nil {
			return err
		}
		return s.broker.Publish(ctx, tx, port.Message{
			Kind: "claims.live", ClaimID: claimID, Version: claim.Version,
		})
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// A pair conflict that the local check could not see — another
			// claim took the pair between the check and the write.
			return nil, []port.ValidationFailure{{
				Reason:  domain.DuplicateInClaim,
				Message: "one of these PRs already evidences this skill",
			}}, nil
		}
		return nil, nil, fmt.Errorf("submitting claim: %w", err)
	}

	submitted, err := s.claims.ByID(ctx, claimID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the submitted claim: %w", err)
	}
	return submitted, nil, nil
}

// WithdrawPreview names the skills that would demote.
//
// Withdrawal warns before it costs something (ADR-0003): a contributor who
// does not know a withdrawal drops them from primary to secondary has not been
// given the choice.
func (s *ClaimService) WithdrawPreview(ctx context.Context, id domain.UserID, claimID domain.ClaimID) ([]domain.UserSkill, error) {
	claim, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, err
	}

	standings, err := s.skills.UserSkills(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading standings: %w", err)
	}

	// How many scored PRs this claim contributes to each skill.
	contributed := map[domain.SkillID]int{}
	for _, cs := range claim.Skills {
		if cs.IsInert() {
			continue
		}
		contributed[cs.SkillID] = len(claim.PREvidence)
	}

	var demoting []domain.UserSkill
	for _, us := range standings {
		n, ok := contributed[us.SkillID]
		if !ok || us.Standing != domain.Primary {
			continue
		}
		if us.DistinctPRCount-n < domain.PrimaryThreshold {
			demoting = append(demoting, us)
		}
	}
	return demoting, nil
}

// Withdraw retires a claim, demoting whatever it was holding up.
//
// confirmDemotion is required when the preview is non-empty. The warning is
// worth nothing if it can be skipped by not asking for it.
func (s *ClaimService) Withdraw(ctx context.Context, id domain.UserID, claimID domain.ClaimID, confirmDemotion bool) (*domain.Claim, error) {
	demoting, err := s.WithdrawPreview(ctx, id, claimID)
	if err != nil {
		return nil, err
	}
	if len(demoting) > 0 && !confirmDemotion {
		return nil, fmt.Errorf(
			"withdrawing demotes %d skill(s); confirm to proceed: %w", len(demoting), ErrConflict)
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.claims.SetStatus(ctx, tx, claimID, domain.ClaimWithdrawn); err != nil {
			return err
		}
		// Standing is recomputed for every affected skill, not only the
		// demoting ones: a claim's links are gone either way, and a count left
		// stale is a count that will disagree with the links table.
		for _, us := range demoting {
			if _, err := s.skills.RecomputeStanding(ctx, tx, id, us.SkillID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("withdrawing claim: %w", err)
	}
	return s.claims.ByID(ctx, claimID)
}

// DecideSuggestion accepts or dismisses an inert AI suggestion.
//
// Accepting recomputes standing in the same transaction: the PRs were already
// judged, so the skill's count changes the moment the contributor says yes.
func (s *ClaimService) DecideSuggestion(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skillID domain.SkillID, accept bool) (*domain.UserSkill, error) {
	if _, err := s.Get(ctx, id, claimID); err != nil {
		return nil, err
	}

	var out *domain.UserSkill
	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.claims.DecideSuggestion(ctx, tx, claimID, skillID, accept); err != nil {
			return err
		}
		if !accept {
			return nil
		}
		var err error
		out, err = s.skills.RecomputeStanding(ctx, tx, id, skillID)
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, fmt.Errorf("this suggestion has already been decided: %w", ErrConflict)
		}
		return nil, fmt.Errorf("deciding suggestion: %w", err)
	}
	return out, nil
}

// --- the pipeline ------------------------------------------------------------

// validateStructure is step 1: cardinality and shape.
//
// Local and free, so it runs before anything that costs a round trip.
func validateStructure(c *domain.Claim) []port.ValidationFailure {
	var out []port.ValidationFailure

	if len(c.PREvidence) == 0 {
		out = append(out, port.ValidationFailure{
			Message: "a claim needs at least one pull request"})
	}
	if len(c.PREvidence) > MaxPRs {
		out = append(out, port.ValidationFailure{
			Message: fmt.Sprintf("a claim carries at most %d pull requests, not %d", MaxPRs, len(c.PREvidence))})
	}
	if len(c.ProjectEvidence) > MaxProjects {
		out = append(out, port.ValidationFailure{
			Message: fmt.Sprintf("a claim carries at most %d projects, not %d", MaxProjects, len(c.ProjectEvidence))})
	}

	declared, nominated := 0, 0
	for _, cs := range c.Skills {
		if cs.Origin == domain.AISuggested {
			continue
		}
		declared++
		if cs.IsNominatedPrimary {
			nominated++
		}
	}
	if declared == 0 {
		out = append(out, port.ValidationFailure{Message: "a claim needs at least one skill"})
	}
	if declared > 0 && nominated != 1 {
		out = append(out, port.ValidationFailure{
			Message: fmt.Sprintf("exactly one skill is nominated primary, not %d", nominated)})
	}
	return out
}

// validateNoDuplicates is step 3: the same PR twice in one claim.
//
// Local, and it catches the mistake a contributor is most likely to make while
// assembling five URLs by hand.
func validateNoDuplicates(c *domain.Claim) []port.ValidationFailure {
	seen := map[string]int{}
	var out []port.ValidationFailure
	for _, pr := range c.PREvidence {
		key := fmt.Sprintf("%s/%s#%d", pr.RepoOwner, pr.RepoName, pr.PRNumber)
		if first, ok := seen[key]; ok {
			out = append(out, port.ValidationFailure{
				Position: pr.Position,
				Reason:   domain.DuplicateInClaim,
				Message:  fmt.Sprintf("PR %d repeats %s from position %d", pr.Position, key, first),
			})
			continue
		}
		seen[key] = pr.Position
	}
	return out
}

// buildLinks turns the claim into the (user, PR, skill) triples step 4 checks.
func buildLinks(id domain.UserID, c *domain.Claim) []port.PRLink {
	var links []port.PRLink
	for _, cs := range c.Skills {
		if cs.IsInert() {
			continue
		}
		for _, pr := range c.PREvidence {
			links = append(links, port.PRLink{
				UserID: id, ClaimID: c.ID, Position: pr.Position, SkillID: cs.SkillID,
				RepoOwner: pr.RepoOwner, RepoName: pr.RepoName, PRNumber: pr.PRNumber,
			})
		}
	}
	return links
}

// validateAgainstGitHub is step 5: exists, public, merged, and the role check.
//
// The role check inverts for pr-review: an author claim needs the claimant to
// have written it, a reviewer claim needs them to have reviewed it and NOT
// written it (ADR-0003). Reviewing your own PR is not review work.
func (s *ClaimService) validateAgainstGitHub(ctx context.Context, c *domain.Claim, contributor *domain.Contributor) []port.ValidationFailure {
	var out []port.ValidationFailure

	for _, pr := range c.PREvidence {
		facts, err := s.github.PullRequest(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber)
		if err != nil {
			out = append(out, failure(pr, domain.GitHubError,
				fmt.Sprintf("PR %d (%s/%s#%d) could not be read from GitHub",
					pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			continue
		}
		if !facts.Repository.Public {
			out = append(out, failure(pr, domain.NotPublic,
				fmt.Sprintf("PR %d (%s/%s#%d) is not publicly visible",
					pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			continue
		}
		if !facts.Merged {
			out = append(out, failure(pr, domain.NotMerged,
				fmt.Sprintf("PR %d (%s/%s#%d) is not merged",
					pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			continue
		}

		switch pr.Role {
		case domain.RoleReviewer:
			if facts.AuthorUserID == contributor.GitHubUserID {
				out = append(out, failure(pr, domain.AuthoredByClaimant,
					fmt.Sprintf("PR %d was written by you; reviewing your own work is not review",
						pr.Position)))
				continue
			}
			reviews, err := s.github.Reviews(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber)
			if err != nil {
				out = append(out, failure(pr, domain.GitHubError,
					fmt.Sprintf("PR %d's reviews could not be read", pr.Position)))
				continue
			}
			if !reviewedBy(reviews, contributor.GitHubUserID) {
				out = append(out, failure(pr, domain.NotReviewedByClaimant,
					fmt.Sprintf("PR %d carries no review by you", pr.Position)))
			}
		default:
			if facts.AuthorUserID != contributor.GitHubUserID {
				out = append(out, failure(pr, domain.NotAuthoredByClaimant,
					fmt.Sprintf("PR %d (%s/%s#%d) was authored by someone else",
						pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			}
		}
	}
	return out
}

func reviewedBy(reviews []port.Review, githubUserID int64) bool {
	for _, r := range reviews {
		if r.AuthorUserID == githubUserID {
			return true
		}
	}
	return false
}

func failure(pr domain.PREvidence, reason domain.EvidenceInvalidReason, message string) port.ValidationFailure {
	return port.ValidationFailure{Position: pr.Position, Reason: reason, Message: message}
}
