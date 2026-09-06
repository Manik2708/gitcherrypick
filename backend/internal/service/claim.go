package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/scoring"
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
	evals  port.EvaluationRepository
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
	evals port.EvaluationRepository,
	users port.UserRepository,
	github port.GitHubClient,
	broker port.Broker,
	tx port.TxManager,
	clock port.Clock,
) *ClaimService {
	return &ClaimService{claims: claims, skills: skills, evals: evals, users: users,
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
func (s *ClaimService) List(ctx context.Context, id domain.UserID) ([]port.ClaimSummary, error) {
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
	// Read before writing, so a refusal can say what the current state IS.
	// "your version is stale" without the live version leaves the caller to
	// guess or re-fetch; ADR-0003 wants the writer that lost told enough to
	// merge.
	current, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, err
	}

	// The lock and the version are checked BEFORE the structure, because both
	// are facts about the claim rather than about the request. A stale writer
	// is stale whatever they sent, and telling them their body is malformed
	// would send them to fix the wrong thing.
	if current.IsLocked(s.clock.Now()) {
		return nil, lockedClaim(current)
	}
	if current.Version != version {
		return nil, Coded(ErrConflict, CodeVersionConflict,
			"the claim changed since you loaded it").
			WithDetail(map[string]any{
				"expected_version": current.Version,
				"provided_version": version,
			})
	}

	// Structural validation runs before the write, so a claim that could never
	// be valid never reaches the database — and a rejected replace leaves the
	// version untouched, which is what makes the concurrency check meaningful.
	if failures := validateStructure(c); len(failures) > 0 {
		return nil, fmt.Errorf("%s: %w", failures[0].Message, ErrInvalid)
	}

	var out *domain.Claim
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = s.claims.Replace(ctx, tx, claimID, version, c)
		return err
	})
	switch {
	case errors.Is(err, port.ErrVersionStale):
		// Lost a race between the read above and the write. Rare, and the
		// caller's remedy is the same: re-read and retry.
		return nil, Coded(ErrConflict, CodeVersionConflict,
			"the claim changed since you loaded it")
	case errors.Is(err, port.ErrConflict):
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
//
// Refused outright on a judged_only claim. That mode is scored on the model's
// reading alone, with no PR-level arithmetic and so no reach term for a project
// to feed (ADR-0007) — accepting the evidence would let a contributor spend
// effort assembling something that could not move the number.
func (s *ClaimService) SetProjectEvidence(ctx context.Context, id domain.UserID, claimID domain.ClaimID, projects []domain.ProjectEvidence) (*domain.Claim, error) {
	if len(projects) > 0 {
		current, err := s.Get(ctx, id, claimID)
		if err != nil {
			return nil, err
		}
		for _, skill := range current.Skills {
			if skill.ScoringMode == domain.ScoringJudgedOnly {
				return nil, Coded(ErrInvalid, CodeProjectsNotAccepted,
					"a %s claim is scored on judgement alone", skill.Slug)
			}
		}
	}
	return s.patch(ctx, id, claimID, func(c *domain.Claim) { c.ProjectEvidence = projects })
}

// SetSkills replaces the declared skills.
func (s *ClaimService) SetSkills(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skills []domain.ClaimSkill) (*domain.Claim, error) {
	resolved, err := s.resolveSkills(ctx, skills)
	if err != nil {
		return nil, err
	}
	return s.patch(ctx, id, claimID, func(c *domain.Claim) { c.Skills = resolved })
}

// resolveSkills turns declared slugs into catalogue entries.
//
// Only CANONICAL slugs are claimable. An alias matches for lookup and never for
// claiming, because the claiming population must not fragment across spellings
// — "go" and "golang" scored separately would make five PRs of Go evidence look
// like two skills with fewer each (ADR-0003).
//
// A rejected alias names its canonical form, so the contributor can act on the
// answer rather than guess at it.
func (s *ClaimService) resolveSkills(ctx context.Context, skills []domain.ClaimSkill) ([]domain.ClaimSkill, error) {
	out := make([]domain.ClaimSkill, 0, len(skills))

	for _, declared := range skills {
		if declared.Slug == "" {
			return nil, Coded(ErrInvalid, CodeUnknownSkill, "a claimed skill needs a slug")
		}

		skill, err := s.skills.BySlug(ctx, declared.Slug)
		if err == nil {
			declared.SkillID = skill.ID
			declared.ScoringMode = skill.ScoringMode
			out = append(out, declared)
			continue
		}
		if !errors.Is(err, port.ErrNotFound) {
			return nil, fmt.Errorf("resolving skill %q: %w", declared.Slug, err)
		}

		return nil, s.unknownSkill(ctx, declared.Slug)
	}
	return out, nil
}

// unknownSkill explains a slug the catalogue does not hold.
//
// If it is an alias, the canonical slug travels with the refusal. That lookup
// costs a query on a path that is already failing, and it is the difference
// between an error a contributor can act on and one they cannot.
func (s *ClaimService) unknownSkill(ctx context.Context, slug string) error {
	matches, err := s.skills.Search(ctx, slug)
	if err != nil || len(matches) == 0 {
		return Coded(ErrInvalid, CodeUnknownSkill, "no skill %q is in the catalogue", slug)
	}

	match := matches[0]
	if match.MatchedVia != "alias" {
		return Coded(ErrInvalid, CodeUnknownSkill, "no skill %q is in the catalogue", slug)
	}

	return Coded(ErrInvalid, CodeUnknownSkill,
		"%q is an alias for %q", slug, match.Skill.Slug).
		WithDetail(map[string]any{
			"suggestion": match.Skill.Slug,
			"message": fmt.Sprintf("'%s' is an alias for '%s'. Claim '%s' instead.",
				slug, match.Skill.Slug, match.Skill.Slug),
		})
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
	// The lock closes the sub-resource writes too. Enforcing it only on the
	// whole-claim PUT would leave the anti-reroll rule bypassable by editing
	// through /evidence/prs instead (ADR-0003).
	if current.IsLocked(s.clock.Now()) {
		return nil, lockedClaim(current)
	}
	apply(current)

	// Editing a SCORED claim is a material change: it resets the judgement,
	// so the version moves and any client holding the old one has to re-read.
	// Editing a draft does not — a sub-resource write must not consume the
	// version a client is holding for its next whole-claim edit.
	versioned := current.EvaluatedAt != nil

	var out *domain.Claim
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		if versioned {
			out, err = s.claims.Replace(ctx, tx, claimID, current.Version, current)
			return err
		}
		out, err = s.claims.ReplaceEvidence(ctx, tx, claimID, current)
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
		return nil, nil, lockedClaim(claim)
	}

	contributor, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("reading contributor: %w", err)
	}

	// 0: unchanged evidence. Checked FIRST, because a claim resubmitted
	// untouched would otherwise fail the pair check as a duplicate of its own
	// links — which is true, and tells the contributor nothing about why
	// (ADR-0003).
	if err := s.refuseUnchangedEvidence(ctx, claimID); err != nil {
		return nil, nil, err
	}

	// 1-3: structural, parse, local dedupe. All local, all before any API call.
	failures := validateStructure(claim)
	failures = append(failures, validateNoDuplicates(claim)...)

	// 4: the pair check. One query rather than an API call, and it is the
	// cheapest way to reject a resubmission of evidence already spent on this
	// skill — so it runs before GitHub is touched.
	links := buildLinks(id, claim)
	if len(failures) == 0 {
		conflicts, err := s.skills.ConflictingPairs(ctx, links)
		if err != nil {
			return nil, nil, fmt.Errorf("checking evidence pairs: %w", err)
		}
		failures = append(failures, pairFailures(claim, conflicts)...)
	}

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

// refuseUnchangedEvidence stops a resubmission that would buy nothing.
//
// The fingerprint is over the evidence SET, so reordering five PRs is
// unchanged and adding a sixth is not.
func (s *ClaimService) refuseUnchangedEvidence(ctx context.Context, claimID domain.ClaimID) error {
	judged, err := s.claims.EvaluatedFingerprint(ctx, claimID)
	if err != nil {
		return fmt.Errorf("reading the judged fingerprint: %w", err)
	}
	if judged == "" {
		return nil
	}

	current, err := s.claims.Fingerprint(ctx, claimID)
	if err != nil {
		return fmt.Errorf("fingerprinting the evidence: %w", err)
	}
	if current != judged {
		return nil
	}
	return Coded(ErrConflict, CodeEvidenceUnchanged,
		"This claim has already been evaluated with identical evidence.")
}

// WithdrawPreview names the skills that would demote.
//
// Withdrawal warns before it costs something (ADR-0003): a contributor who
// does not know a withdrawal drops them from primary to secondary has not been
// given the choice.
func (s *ClaimService) WithdrawPreview(ctx context.Context, id domain.UserID, claimID domain.ClaimID) ([]port.Demotion, error) {
	claim, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, err
	}

	standings, err := s.skills.UserSkills(ctx, nil, id)
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

	var demoting []port.Demotion
	for _, us := range standings {
		n, ok := contributed[us.SkillID]
		if !ok || us.Standing != domain.Primary {
			continue
		}
		if after := us.DistinctPRCount - n; after < domain.PrimaryThreshold {
			demoting = append(demoting, port.Demotion{Skill: us, DistinctPRCountAfter: after})
		}
	}
	return demoting, nil
}

// Withdraw retires a claim, demoting whatever it was holding up.
//
// confirmDemotion is required when the preview is non-empty. The warning is
// worth nothing if it can be skipped by not asking for it.
func (s *ClaimService) Withdraw(ctx context.Context, id domain.UserID, claimID domain.ClaimID, confirmDemotion bool) (*domain.Claim, error) {
	claim, err := s.Get(ctx, id, claimID)
	if err != nil {
		return nil, err
	}

	demoting, err := s.WithdrawPreview(ctx, id, claimID)
	if err != nil {
		return nil, err
	}
	if len(demoting) > 0 && !confirmDemotion {
		// The warning names the skills. "Confirm to proceed" without saying
		// what is lost is not a warning, and the confirmation it collects is
		// not informed (ADR-0003).
		return nil, Coded(ErrConflict, CodeConfirmationRequired,
			"withdrawing demotes %d skill(s); confirm to proceed", len(demoting)).
			WithDetail(map[string]any{"demotions": demotionsOf(demoting)})
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.claims.SetStatus(ctx, tx, claimID, domain.ClaimWithdrawn); err != nil {
			return err
		}
		// The links go first. Standing is recomputed from what survives, so
		// releasing the evidence after the recompute would count PRs the
		// claim no longer holds.
		if err := s.skills.UnlinkClaim(ctx, tx, claimID); err != nil {
			return err
		}
		// Every skill the claim touched, not only the demoting ones: the links
		// are gone either way, and a count left stale is a count that will
		// disagree with the links table.
		for _, cs := range claim.Skills {
			if cs.IsInert() {
				continue
			}
			if _, err := s.skills.RecomputeStanding(ctx, tx, id, cs.SkillID); err != nil {
				return err
			}
		}

		// The user-level numbers are derived from primary standings, so they
		// have to follow. A contributor whose last primary skill just went
		// unevidenced has not been measured — and null is what says that,
		// where a stale 64.2 would keep them on a leaderboard they no longer
		// qualify for (ADR-0007).
		return s.recomputeUserScores(ctx, tx, id)
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
func (s *ClaimService) DecideSuggestion(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skillID domain.SkillID, accept bool) (*domain.SuggestionDecision, error) {
	if _, err := s.Get(ctx, id, claimID); err != nil {
		return nil, err
	}

	var out domain.SuggestionDecision
	var existing *domain.ClaimSkill

	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		decided, err := s.claims.DecideSuggestion(ctx, tx, claimID, skillID, accept)
		if err != nil {
			existing = decided
			return err
		}
		out.Skill = *decided
		if !accept {
			return nil
		}
		// Accepting is what makes an inert suggestion count. Its PRs were
		// already judged, so standing follows immediately rather than waiting
		// for another evaluation (ADR-0003 §10).
		if err := s.skills.LinkJudgedEvidence(ctx, tx, id, claimID, skillID); err != nil {
			return err
		}
		out.Standing, err = s.skills.RecomputeStanding(ctx, tx, id, skillID)
		if err != nil || out.Standing == nil {
			return err
		}
		return s.rescore(ctx, tx, id, skillID, out.Standing)
	})
	if err != nil {
		return nil, decisionRefusal(err, existing)
	}
	return &out, nil
}

// rescore recomputes one skill's score from every PR that survives against it.
//
// The same arithmetic the evaluator applies, reached from the other
// direction: acceptance changes which evidence counts, and a standing whose
// score still reflected the evidence before it would be stale the moment it
// was written (ADR-0005).
func (s *ClaimService) rescore(ctx context.Context, tx port.Tx, id domain.UserID,
	skillID domain.SkillID, standing *domain.UserSkill) error {

	surviving, err := s.evals.SkillPRScores(ctx, tx, id, skillID)
	if err != nil {
		return err
	}
	prComponent := scoring.PRComponent(surviving)
	score := scoring.SkillScore(prComponent, 0, domain.ScoringStandard)
	if err := s.skills.SetSkillScore(ctx, tx, id, skillID, score, prComponent, 0); err != nil {
		return err
	}

	// Rounded to the scale the column stores, so the value returned to the
	// caller is the value a later read reports. An unrounded copy would make
	// the response and the next GET disagree in the last digits.
	standing.Score, standing.PRComponent = math.Round(score*100)/100, prComponent
	return nil
}

// decisionRefusal says WHICH of the three refusals happened.
//
// "This is not an undecided suggestion" is true of a declared skill and of one
// already accepted, and the contributor needs a different answer for each: one
// is a mistake they can correct, the other is a decision they already made.
func decisionRefusal(err error, existing *domain.ClaimSkill) error {
	if !errors.Is(err, port.ErrConflict) {
		if errors.Is(err, port.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("deciding suggestion: %w", err)
	}

	if existing != nil && existing.Origin != domain.AISuggested {
		return Coded(ErrInvalid, CodeNotASuggestion,
			"%s was declared, not suggested", existing.Slug).
			WithDetail(map[string]any{"origin": string(existing.Origin)})
	}

	refusal := Coded(ErrConflict, CodeSuggestionAlreadyDecided,
		"this suggestion has already been decided")
	switch {
	case existing == nil:
		return refusal
	case existing.AcceptedAt != nil:
		return refusal.WithDetail(map[string]any{"accepted_at": existing.AcceptedAt})
	case existing.DismissedAt != nil:
		return refusal.WithDetail(map[string]any{"dismissed_at": existing.DismissedAt})
	}
	return refusal
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

// diagnoseFetch decides WHY a pull request could not be read.
//
// A PR in a private repository 404s exactly as a nonexistent one does, and the
// two demand different things of the contributor: one is a repository they
// cannot use as evidence at all, the other is a typo. Asking about the
// repository separates them, at the cost of one extra call on a path that has
// already failed.
func (s *ClaimService) diagnoseFetch(ctx context.Context, pr domain.PREvidence) (domain.EvidenceInvalidReason, string) {
	if repo, err := s.github.Repository(ctx, pr.RepoOwner, pr.RepoName); err == nil && !repo.Public {
		return domain.NotPublic, fmt.Sprintf("PR %d (%s/%s#%d) is not publicly visible.",
			pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)
	}
	return domain.GitHubError, fmt.Sprintf("PR %d (%s/%s#%d) could not be read from GitHub.",
		pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)
}

// pairFailures names each (PR, skill) already spent on another claim.
//
// The conflicting claim id travels with the refusal: the contributor's remedy
// is to look at that claim, and without its id they cannot.
func pairFailures(c *domain.Claim, conflicts []port.PairConflict) []port.ValidationFailure {
	out := make([]port.ValidationFailure, 0, len(conflicts))
	for _, conflict := range conflicts {
		claimID := conflict.ClaimID
		out = append(out, port.ValidationFailure{
			Position:           conflict.Link.Position,
			Reason:             domain.DuplicatePair,
			Skill:              conflict.SkillSlug,
			ConflictingClaimID: &claimID,
			Message: fmt.Sprintf("PR %d (%s/%s#%d) already evidences %s in another claim.",
				conflict.Link.Position, conflict.Link.RepoOwner, conflict.Link.RepoName,
				conflict.Link.PRNumber, conflict.SkillName),
		})
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
		// A row that never parsed has nothing to ask GitHub about. Reported
		// from what is already known rather than by fetching "/#0", which
		// would come back as an unreadable repository and send the
		// contributor looking for a permissions problem they do not have.
		if pr.InvalidReason != nil {
			out = append(out, failure(pr, *pr.InvalidReason,
				fmt.Sprintf("PR %d is not a GitHub pull request URL.", pr.Position)))
			continue
		}

		facts, err := s.github.PullRequest(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber)
		if err != nil {
			reason, message := s.diagnoseFetch(ctx, pr)
			if reason == domain.GitHubError {
				// Unreadable RIGHT NOW is a fact about GitHub, not about the
				// claim. ADR-0004 retries it on a longer backoff and scores the
				// rest; failing the submit would record an outage as evidence
				// against the contributor. The affected skill simply ends up
				// with fewer distinct PRs, which standing already handles.
				continue
			}
			out = append(out, failure(pr, reason, message))
			continue
		}
		if !facts.Repository.Public {
			out = append(out, failure(pr, domain.NotPublic,
				fmt.Sprintf("PR %d (%s/%s#%d) is not publicly visible.",
					pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			continue
		}
		if !facts.Merged {
			out = append(out, failure(pr, domain.NotMerged,
				fmt.Sprintf("PR %d (%s/%s#%d) is not merged.",
					pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			continue
		}

		switch pr.Role {
		case domain.RoleReviewer:
			if facts.AuthorUserID == contributor.GitHubUserID {
				out = append(out, failure(pr, domain.AuthoredByClaimant,
					fmt.Sprintf("PR %d (%s/%s#%d) was authored by you. Reviewing your own pull request is not review work.",
						pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
				continue
			}
			reviews, err := s.github.Reviews(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber)
			if err != nil {
				out = append(out, failure(pr, domain.GitHubError,
					fmt.Sprintf("PR %d's reviews could not be read.", pr.Position)))
				continue
			}
			if !reviewedBy(reviews, contributor.GitHubUserID) {
				out = append(out, failure(pr, domain.NotReviewedByClaimant,
					fmt.Sprintf("PR %d (%s/%s#%d) has no review from you.",
						pr.Position, pr.RepoOwner, pr.RepoName, pr.PRNumber)))
			}
		default:
			if facts.AuthorUserID != contributor.GitHubUserID {
				out = append(out, failure(pr, domain.NotAuthoredByClaimant,
					fmt.Sprintf("PR %d (%s/%s#%d) was authored by someone else.",
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

var _ port.ClaimService = (*ClaimService)(nil)

// recomputeUserScores rewrites Overall and Generalist from what survives.
//
// Overall is the best primary score, Generalist their sum — both nil when no
// primary standing remains, because a score of zero would claim a measurement
// that was not made.
func (s *ClaimService) recomputeUserScores(ctx context.Context, tx port.Tx, id domain.UserID) error {
	// Read through the TRANSACTION: the standings this is derived from were
	// rewritten moments ago in it, and the pool would still see the old ones.
	standings, err := s.skills.UserSkills(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("reading standings: %w", err)
	}

	var overall, generalist *float64
	for _, us := range standings {
		if us.Standing != domain.Primary {
			continue
		}
		score := us.Score
		if overall == nil || score > *overall {
			best := score
			overall = &best
		}
		if generalist == nil {
			zero := 0.0
			generalist = &zero
		}
		*generalist += score
	}

	if err := s.users.SetUserScores(ctx, tx, id, overall, generalist); err != nil {
		return fmt.Errorf("recomputing user scores: %w", err)
	}
	return nil
}

// lockedClaim refuses an edit during the seven-day window.
//
// Carries locked_until, because ADR-0003 wants the contributor told WHEN they
// may edit rather than merely that they may not.
func lockedClaim(c *domain.Claim) error {
	return Coded(ErrConflict, CodeClaimLocked,
		"claim %s is locked until %s", c.ID, c.LockedUntil.Format(time.RFC3339)).
		WithDetail(map[string]any{
			"locked_until": c.LockedUntil,
			"reason":       "A scored claim cannot be edited for seven days.",
		})
}

// SkillDemotion is one skill a withdrawal would drop out of ranking, as the
// refusal reports it.
type SkillDemotion struct {
	Skill string `json:"skill"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func demotionsOf(in []port.Demotion) []SkillDemotion {
	out := make([]SkillDemotion, 0, len(in))
	for _, d := range in {
		out = append(out, SkillDemotion{
			Skill: d.Skill.Slug,
			From:  string(domain.Primary),
			To:    string(domain.Secondary),
		})
	}
	return out
}
