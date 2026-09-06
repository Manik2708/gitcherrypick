package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/scoring"
)

// EvaluationService is the evaluator's business logic: judge, compute, persist.
//
// It receives a message, calls the AI through an interface, does arithmetic,
// and writes through the repository interface. It never references a queue or a
// model vendor (CLAUDE.md) — which is what lets the whole thing be driven by
// fakes in the integration suite.
type EvaluationService struct {
	claims port.ClaimRepository
	skills port.SkillRepository
	users  port.UserRepository
	evals  port.EvaluationRepository
	norms  port.NormsRepository
	ai     port.AIClient
	broker port.Broker
	github port.GitHubClient
	tx     port.TxManager
	clock  port.Clock
	rubric string
}

// NewEvaluationService wires the evaluator.
func NewEvaluationService(
	claims port.ClaimRepository,
	skills port.SkillRepository,
	users port.UserRepository,
	evals port.EvaluationRepository,
	norms port.NormsRepository,
	ai port.AIClient,
	broker port.Broker,
	github port.GitHubClient,
	tx port.TxManager,
	clock port.Clock,
	rubricVersion string,
) *EvaluationService {
	return &EvaluationService{claims: claims, skills: skills, users: users,
		evals: evals, norms: norms, ai: ai, broker: broker, github: github,
		tx: tx, clock: clock, rubric: rubricVersion}
}

var _ port.EvaluationService = (*EvaluationService)(nil)

// Process handles one queued message.
//
// IDEMPOTENT, because it has to be: delivery is at-least-once and the same job
// WILL arrive twice (ADR-0004). A redelivery of a completed evaluation acks and
// returns without spending a model call.
func (s *EvaluationService) Process(ctx context.Context, msg port.Message) error {
	claim, err := s.claims.ByID(ctx, msg.ClaimID)
	if err != nil {
		return s.deadLetter(ctx, msg, "claim not found")
	}

	// The version this run stamps is the ACTIVE one, not the one this process
	// was started with. A sweep moves it while workers are running, and a
	// worker that kept its startup value would re-judge the corpus into the
	// version it was meant to leave (RFC-0015).
	rubric, err := s.activeRubric(ctx)
	if err != nil {
		return err
	}

	done, err := s.evals.AlreadyEvaluated(ctx, msg.ClaimID, msg.Version, rubric)
	if err != nil {
		return fmt.Errorf("checking for a completed evaluation: %w", err)
	}
	if done {
		// Redelivery. Free, which is the whole requirement.
		return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return s.broker.Ack(ctx, tx, msg.ID)
		})
	}

	// Enrichment first. The model is shown the same facts the arithmetic
	// normalises against, and both come from one fetch — a second read could
	// return a different star count and make the two disagree (ADR-0004).
	if err := s.enrich(ctx, claim); err != nil {
		return s.broker.Nack(ctx, msg.ID, err.Error())
	}

	response, err := s.judge(ctx, claim, rubric)
	if err != nil {
		// Retryable: a transport failure is not the claim's fault.
		return s.broker.Nack(ctx, msg.ID, err.Error())
	}
	if response.Refused {
		// stop_reason "refusal" arrives as an HTTP 200 (ADR-0006). Treating it
		// as a valid empty judgement would score the claim zero, so it is
		// dead-lettered for a human instead.
		return s.deadLetter(ctx, msg, "the model refused to judge this claim")
	}

	scores, err := s.score(ctx, claim, response, rubric)
	if err != nil {
		return fmt.Errorf("scoring: %w", err)
	}
	// The fingerprint comes from the repository, which is also what the submit
	// path compares against. Two implementations of "the same evidence" would
	// agree until they did not.
	evidence, err := s.claims.Fingerprint(ctx, claim.ID)
	if err != nil {
		return fmt.Errorf("fingerprinting the evidence: %w", err)
	}
	evidenceFingerprint := []byte(evidence)

	// One transaction: scores, link statuses, standings, user-level numbers,
	// the claim's status, and the ack. A half-persisted evaluation would leave
	// a contributor with some skills promoted and others not, from one call.
	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		// The evaluation record comes first: it is what the scores hang off,
		// and what a redelivery matches against.
		if err := s.evals.Record(ctx, tx, port.Evaluation{
			ClaimID: claim.ID, ClaimVersion: claim.Version,
			RubricVersion: rubric, Model: "e2e-model", PromptVersion: rubric,
			Fingerprint: evidenceFingerprint, Trigger: triggerFor(msg),
		}); err != nil {
			return err
		}
		if err := s.evals.Persist(ctx, tx, claim.ID, scores, response.Suggestions); err != nil {
			return err
		}
		if err := s.applyStandings(ctx, tx, claim, scores); err != nil {
			return err
		}
		if err := s.applyUserScores(ctx, tx, claim.UserID); err != nil {
			return err
		}
		if err := s.claims.SetEvaluated(ctx, tx, claim.ID,
			s.clock.Now(), s.clock.Now().Add(domain.LockWindow)); err != nil {
			return err
		}
		if err := s.broker.Ack(ctx, tx, msg.ID); err != nil {
			return err
		}
		// A sweep is over when the queue it filled is empty, and the worker
		// that emptied it is what knows. AFTER the ack: the job being worked
		// is still in the queue until then, so a check before it would never
		// see zero.
		return s.closeDrainedSweep(ctx, tx)
	})
}

// fingerprint identifies the evidence a run judged.
//
// Derived from the claim's PR triples in position order: the same evidence
// yields the same digest, so a resubmission of unchanged evidence is
// recognisable as the evaluation that already happened (ADR-0004).
// triggerFor names why this run happened, which the sweep topic distinguishes.
func triggerFor(msg port.Message) string {
	if msg.Kind == "claims.sweep" {
		return "rubric_sweep"
	}
	return "submission"
}

// enrich fetches each PR's facts and caches them against the claim.
//
// A PR that cannot be read is left unenriched rather than failing the run: the
// reach terms drop out and the remaining weights renormalise, which is the
// same treatment a repository with no download count already gets.
func (s *EvaluationService) enrich(ctx context.Context, claim *domain.Claim) error {
	for i := range claim.PREvidence {
		pr := &claim.PREvidence[i]
		facts, err := s.github.PullRequest(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber)
		if err != nil {
			continue
		}
		if repo, err := s.github.Repository(ctx, pr.RepoOwner, pr.RepoName); err == nil {
			facts.Repository = *repo
		}
		pr.Facts = facts
	}

	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return s.claims.Enrich(ctx, tx, claim.ID, claim.PREvidence)
	})
}

// judge calls the model once for the whole claim.
//
// ONE call for every PR against every declared skill: the model needs the
// bundle to weigh a PR against its siblings, and a call per pair would multiply
// the bill by the product of both counts (ADR-0004).
func (s *EvaluationService) judge(ctx context.Context, claim *domain.Claim, rubric string) (*port.JudgeResponse, error) {
	var (
		skills    []domain.Skill
		nominated string
		mode      = domain.ScoringStandard
	)
	for _, cs := range claim.Skills {
		if cs.IsInert() {
			continue
		}
		skill, err := s.skills.BySlug(ctx, cs.Slug)
		if err != nil {
			return nil, fmt.Errorf("resolving skill %q: %w", cs.Slug, err)
		}
		skills = append(skills, *skill)
		if cs.IsNominatedPrimary {
			nominated = cs.Slug
			mode = skill.ScoringMode
		}
	}

	return s.ai.Judge(ctx, port.JudgeRequest{
		ClaimID: claim.ID, RubricVersion: rubric, ScoringMode: mode,
		NominatedPrimary: nominated, Skills: skills,
		Evidence: claim.PREvidence, Projects: claim.ProjectEvidence,
	})
}

// score turns judgements into numbers.
//
// The arithmetic lives in internal/scoring, which is pure — so the rubric is
// table-testable without a model call, and there is exactly one implementation
// of each weight rather than one here and one in SQL.
func (s *EvaluationService) score(ctx context.Context, claim *domain.Claim, response *port.JudgeResponse, rubric string) ([]domain.PRSkillScore, error) {
	norms, err := s.norms.Current(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading norms: %w", err)
	}

	generation := 0
	for _, n := range norms {
		generation = n.Generation
		break
	}

	bySlug := map[string]domain.ClaimSkill{}
	for _, cs := range claim.Skills {
		bySlug[cs.Slug] = cs
	}

	// A skill the model SUGGESTED is judged in the same pass as the declared
	// ones — that judgement is the evidence behind the suggestion. It is
	// scored and stored, and attaches to nothing until accepted.
	suggested := map[string]bool{}
	for _, sg := range response.Suggestions {
		if _, declared := bySlug[sg.Slug]; !declared {
			suggested[sg.Slug] = true
		}
	}
	byPosition := map[int]domain.PREvidence{}
	for _, pr := range claim.PREvidence {
		byPosition[pr.PRNumber] = pr
	}

	// The nominated primary is scored first: a secondary's score is a share OF
	// it, so it has to exist before any secondary can be computed.
	primaryByPR := map[int]float64{}
	var out []domain.PRSkillScore

	for _, pass := range []bool{true, false} {
		for _, j := range response.Judgements {
			cs, declared := bySlug[j.SkillSlug]
			if !declared && !suggested[j.SkillSlug] {
				continue
			}
			// A suggestion is never the nominated primary — nobody nominated
			// it — so it is scored on the second pass, as a share of one.
			if cs.IsNominatedPrimary != pass {
				continue
			}
			pr, ok := byPosition[j.PRNumber]
			if !ok {
				continue
			}

			skill, err := s.skills.BySlug(ctx, j.SkillSlug)
			if err != nil {
				return nil, fmt.Errorf("resolving skill %q: %w", j.SkillSlug, err)
			}
			skillID := cs.SkillID
			if !declared {
				skillID = skill.ID
			}

			var score, q, r, e float64
			if cs.IsNominatedPrimary {
				r = scoring.Reach(reachMetrics(pr), norms, false)
				e = scoring.Engagement(engagementMetrics(pr), norms)
				q = scoring.Quality(j.Dimensions)
				score = scoring.PRScore(j, r, e, skill.ScoringMode)
				primaryByPR[j.PRNumber] = score
			} else {
				share := 0.0
				if j.RelativeShare != nil {
					share = *j.RelativeShare
				}
				if j.Disqualified {
					score = 0
				} else {
					score = scoring.SecondaryPRScore(primaryByPR[j.PRNumber], share)
				}
			}

			// The floor is not a model verdict, so it arrives with no reason
			// attached. Naming it here is what lets a claim read say "below
			// the quality floor" rather than leaving the PR silently absent
			// (ADR-0007).
			reason := j.DisqualificationReason
			if reason == nil && score == 0 && !j.Disqualified {
				floor := domain.BelowQualityFloor
				reason = &floor
			}

			out = append(out, domain.PRSkillScore{
				ClaimID: claim.ID, Position: pr.Position, SkillID: skillID,
				Score: score, QualityQ: q, ReachR: r, EngagementE: e,
				Disqualified: j.Disqualified, RejectionReason: reason,
				NormsGeneration: generation, RubricVersion: rubric,
				Dimensions: j.Dimensions, Inert: !declared,
			})
		}
	}
	return out, nil
}

// applyStandings marks each link scored or rejected and recomputes standing.
//
// A DISQUALIFIED pair is rejected rather than scored, which is what makes
// disqualification bite on standing: five PRs of which one was disqualified is
// four, and the skill stays secondary (ADR-0007 §3).
func (s *EvaluationService) applyStandings(ctx context.Context, tx port.Tx, claim *domain.Claim, scores []domain.PRSkillScore) error {
	byPosition := map[int]domain.PREvidence{}
	for _, pr := range claim.PREvidence {
		byPosition[pr.Position] = pr
	}

	touched := map[domain.SkillID]bool{}
	for _, sc := range scores {
		// An inert score belongs to a suggestion. It is already stored; what
		// it must not do is create a link, because a link is what would make
		// the suggestion count before the contributor accepted it.
		if sc.Inert {
			continue
		}
		pr, ok := byPosition[sc.Position]
		if !ok {
			continue
		}
		link := port.PRLink{
			UserID: claim.UserID, ClaimID: claim.ID, SkillID: sc.SkillID,
			RepoOwner: pr.RepoOwner, RepoName: pr.RepoName, PRNumber: pr.PRNumber,
		}

		status := domain.LinkScored
		var reason *domain.RejectionReason
		if sc.Disqualified || sc.Score == 0 {
			status, reason = domain.LinkRejected, sc.RejectionReason
		}
		if err := s.skills.SetLinkStatus(ctx, tx, []port.PRLink{link}, status, reason); err != nil {
			return err
		}
		touched[sc.SkillID] = true
	}

	for skillID := range touched {
		if _, err := s.skills.RecomputeStanding(ctx, tx, claim.UserID, skillID); err != nil {
			return err
		}

		// The skill's own score, from every SURVIVING PR the contributor has
		// against it — not just this claim's. A skill is the accumulation of
		// evidence across claims (ADR-0007), so scoring it from one claim
		// would drop what earlier ones established.
		surviving, err := s.evals.SkillPRScores(ctx, tx, claim.UserID, skillID)
		if err != nil {
			return err
		}
		prComponent := scoring.PRComponent(surviving)
		score := scoring.SkillScore(prComponent, 0, modeOf(claim, skillID))
		if err := s.skills.SetSkillScore(ctx, tx, claim.UserID, skillID,
			score, prComponent, 0); err != nil {
			return err
		}
	}
	return nil
}

// modeOf reports how a skill on this claim is scored.
func modeOf(claim *domain.Claim, skillID domain.SkillID) domain.ScoringMode {
	for _, cs := range claim.Skills {
		if cs.SkillID == skillID && cs.ScoringMode != "" {
			return cs.ScoringMode
		}
	}
	return domain.ScoringStandard
}

// applyUserScores recomputes Overall and Generalist together.
//
// Both, from the same ordered list, in one call: ADR-0007 computes them in one
// transaction precisely so they cannot disagree, and a method that wrote one
// without the other is how they eventually would.
func (s *EvaluationService) applyUserScores(ctx context.Context, tx port.Tx, id domain.UserID) error {
	// Through the TRANSACTION: the standings were rewritten in it moments ago,
	// and the pool would still return the values from before.
	standings, err := s.skills.UserSkills(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("reading standings: %w", err)
	}

	skills := make([]scoring.SkillStanding, 0, len(standings))
	for _, us := range standings {
		skill, err := s.skills.BySlug(ctx, us.Slug)
		if err != nil {
			return fmt.Errorf("resolving skill %q: %w", us.Slug, err)
		}
		skills = append(skills, scoring.SkillStanding{
			Score: us.Score, Standing: us.Standing, Mode: skill.ScoringMode,
		})
	}

	return s.users.SetUserScores(ctx, tx, id, scoring.Overall(skills), scoring.Generalist(skills))
}

// Sweep re-queues the corpus when the rubric changes.
//
// ENQUEUE ONLY. A half-swept corpus mixes rubric versions, and a leaderboard
// mixing them ranks people by which version happened to judge them — so search
// gates on the active version and the corpus drains into it (ADR-0008).
func (s *EvaluationService) Sweep(ctx context.Context, p domain.Principal, toVersion, reason string) (*domain.RubricSweep, error) {
	admin, err := requireAdmin(p)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return nil, Coded(ErrInvalid, CodeReasonRequired,
			"a sweep invalidates every score on the platform and must say why")
	}

	// Unknown BEFORE unchanged: "v99" is a typo, and telling its author the
	// corpus is already at v1 would answer a question they did not ask.
	if !domain.KnownRubricVersion(toVersion) {
		return nil, Coded(ErrInvalid, CodeUnknownRubricVersion,
			"%q is not a rubric version this build implements", toVersion).
			WithDetail(map[string]any{"provided": toVersion})
	}

	active, err := s.activeRubric(ctx)
	if err != nil {
		return nil, err
	}

	// One sweep at a time. Two overlapping sweeps leave the corpus split
	// across three versions with no order in which it converges (RFC-0015).
	if open, err := s.evals.OpenSweep(ctx); err != nil {
		return nil, fmt.Errorf("checking for a running sweep: %w", err)
	} else if open.InFlight() {
		remaining, err := s.evals.SweptClaimsRemaining(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("counting the running sweep: %w", err)
		}
		return nil, Coded(ErrConflict, CodeSweepInProgress,
			"a sweep to %s is still draining", open.To).
			WithDetail(map[string]any{
				"id": open.ID, "claims_remaining": remaining,
			})
	}

	if toVersion == active {
		return nil, Coded(ErrInvalid, CodeRubricVersionUnchanged,
			"the corpus is already at %s", active).
			WithDetail(map[string]any{"active_rubric_version": active})
	}

	claims, err := s.claims.PendingSweep(ctx, active, 10000)
	if err != nil {
		return nil, fmt.Errorf("listing claims to sweep: %w", err)
	}

	var recorded *domain.RubricSweep
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		recorded, err = s.evals.RecordSweep(ctx, tx, &domain.RubricSweep{
			From: active, To: toVersion, Reason: reason,
			RequestedBy: admin, ClaimsEnqueued: len(claims),
			CreatedAt: s.clock.Now(),
		})
		if err != nil {
			return err
		}

		for _, id := range claims {
			claim, err := s.claims.ByID(ctx, id)
			if err != nil {
				return err
			}
			if err := s.claims.SetStatus(ctx, tx, id, domain.ClaimQueued); err != nil {
				return err
			}
			if err := s.broker.Publish(ctx, tx, port.Message{
				Kind: "claims.sweep", ClaimID: id, Version: claim.Version,
			}); err != nil {
				return err
			}
		}

		// A sweep with nothing to re-judge is complete the moment it is
		// recorded. Leaving it open would block the next one forever.
		if len(claims) == 0 {
			return s.evals.CompleteSweep(ctx, tx, recorded.ID, s.clock.Now())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("sweeping: %w", err)
	}
	return recorded, nil
}

// activeRubric is the version the platform currently compares scores against.
func (s *EvaluationService) activeRubric(ctx context.Context) (string, error) {
	active, err := s.evals.ActiveRubricVersion(ctx, s.rubric)
	if err != nil {
		return "", fmt.Errorf("reading the active rubric version: %w", err)
	}
	return active, nil
}

// closeDrainedSweep stamps a sweep whose last claim has just been judged.
//
// Called after a sweep-triggered evaluation rather than on a timer: the
// platform is only out of the split-version state once the queue is empty, and
// the worker that emptied it is what knows.
func (s *EvaluationService) closeDrainedSweep(ctx context.Context, tx port.Tx) error {
	open, err := s.evals.OpenSweep(ctx)
	if err != nil || !open.InFlight() {
		return err
	}
	remaining, err := s.evals.SweptClaimsRemaining(ctx, tx)
	if err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	return s.evals.CompleteSweep(ctx, tx, open.ID, s.clock.Now())
}

// deadLetter parks a message a human has to look at, and acks it.
//
// A poisonous message that retried forever would starve the queue behind it;
// one discarded silently would lose a contributor's submission with no trace.
func (s *EvaluationService) deadLetter(ctx context.Context, msg port.Message, why string) error {
	if err := s.evals.DeadLetter(ctx, msg.ID, why, msg.Payload); err != nil {
		return fmt.Errorf("dead-lettering: %w", err)
	}
	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		err := s.broker.Ack(ctx, tx, msg.ID)
		if errors.Is(err, port.ErrNotFound) {
			return nil
		}
		return err
	})
}

func reachMetrics(pr domain.PREvidence) map[string]float64 {
	if pr.Facts == nil {
		return nil
	}
	// Zero means UNKNOWN here, not "none". Reach drops an absent metric and
	// renormalises the rest, so passing an unobtainable number as 0 would
	// count its weight against every repository — which is the failure the
	// GitHub adapter's own comment warns about.
	repo := pr.Facts.Repository
	metrics := map[string]float64{}
	for name, value := range map[string]int{
		"repo_stars":        repo.Stars,
		"repo_forks":        repo.Forks,
		"repo_contributors": repo.Contributors,
		"dependents":        repo.Dependents,
		"package_downloads": repo.Downloads,
	} {
		if value > 0 {
			metrics[name] = float64(value)
		}
	}
	return metrics
}

func engagementMetrics(pr domain.PREvidence) map[string]float64 {
	if pr.Facts == nil {
		return nil
	}
	return map[string]float64{
		"review_comments": float64(pr.Facts.ReviewComments),
		"reviews":         float64(pr.Facts.Reviews),
		"participants":    float64(pr.Facts.Participants),
	}
}
