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
	done, err := s.evals.AlreadyEvaluated(ctx, msg.ClaimID, msg.Version)
	if err != nil {
		return fmt.Errorf("checking for a completed evaluation: %w", err)
	}
	if done {
		// Redelivery. Free, which is the whole requirement.
		return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return s.broker.Ack(ctx, tx, msg.ID)
		})
	}

	claim, err := s.claims.ByID(ctx, msg.ClaimID)
	if err != nil {
		return s.deadLetter(ctx, msg, "claim not found")
	}

	response, err := s.judge(ctx, claim)
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

	scores, err := s.score(ctx, claim, response)
	if err != nil {
		return fmt.Errorf("scoring: %w", err)
	}

	// One transaction: scores, link statuses, standings, user-level numbers,
	// the claim's status, and the ack. A half-persisted evaluation would leave
	// a contributor with some skills promoted and others not, from one call.
	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
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
		return s.broker.Ack(ctx, tx, msg.ID)
	})
}

// judge calls the model once for the whole claim.
//
// ONE call for every PR against every declared skill: the model needs the
// bundle to weigh a PR against its siblings, and a call per pair would multiply
// the bill by the product of both counts (ADR-0004).
func (s *EvaluationService) judge(ctx context.Context, claim *domain.Claim) (*port.JudgeResponse, error) {
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
		ClaimID: claim.ID, RubricVersion: s.rubric, ScoringMode: mode,
		NominatedPrimary: nominated, Skills: skills,
		Evidence: claim.PREvidence, Projects: claim.ProjectEvidence,
	})
}

// score turns judgements into numbers.
//
// The arithmetic lives in internal/scoring, which is pure — so the rubric is
// table-testable without a model call, and there is exactly one implementation
// of each weight rather than one here and one in SQL.
func (s *EvaluationService) score(ctx context.Context, claim *domain.Claim, response *port.JudgeResponse) ([]domain.PRSkillScore, error) {
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
			cs, ok := bySlug[j.SkillSlug]
			if !ok {
				continue
			}
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

			out = append(out, domain.PRSkillScore{
				ClaimID: claim.ID, Position: pr.Position, SkillID: cs.SkillID,
				Score: score, QualityQ: q, ReachR: r, EngagementE: e,
				Disqualified: j.Disqualified, RejectionReason: j.DisqualificationReason,
				NormsGeneration: generation, RubricVersion: s.rubric,
				Dimensions: j.Dimensions,
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
		pr, ok := byPosition[sc.Position]
		if !ok {
			continue
		}
		link := port.PRLink{
			UserID: claim.UserID, ClaimID: claim.ID, SkillID: sc.SkillID,
			RepoOwner: pr.RepoOwner, RepoName: pr.RepoName, PRNumber: pr.PRNumber,
		}

		status := domain.LinkScored
		if sc.Disqualified || sc.Score == 0 {
			status = domain.LinkRejected
		}
		if err := s.skills.SetLinkStatus(ctx, tx, []port.PRLink{link}, status); err != nil {
			return err
		}
		touched[sc.SkillID] = true
	}

	for skillID := range touched {
		if _, err := s.skills.RecomputeStanding(ctx, tx, claim.UserID, skillID); err != nil {
			return err
		}
	}
	return nil
}

// applyUserScores recomputes Overall and Generalist together.
//
// Both, from the same ordered list, in one call: ADR-0007 computes them in one
// transaction precisely so they cannot disagree, and a method that wrote one
// without the other is how they eventually would.
func (s *EvaluationService) applyUserScores(ctx context.Context, tx port.Tx, id domain.UserID) error {
	standings, err := s.skills.UserSkills(ctx, id)
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
func (s *EvaluationService) Sweep(ctx context.Context, p domain.Principal, toVersion, reason string) (int, error) {
	if _, err := requireAdmin(p); err != nil {
		return 0, err
	}
	if toVersion == "" {
		return 0, fmt.Errorf("a sweep needs a target rubric version: %w", ErrInvalid)
	}
	if toVersion == s.rubric {
		return 0, fmt.Errorf("the corpus is already at %s: %w", toVersion, ErrConflict)
	}

	claims, err := s.claims.PendingSweep(ctx, s.rubric, 10000)
	if err != nil {
		return 0, fmt.Errorf("listing claims to sweep: %w", err)
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
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
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("sweeping: %w", err)
	}
	return len(claims), nil
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
	repo := pr.Facts.Repository
	return map[string]float64{
		"repo_stars":        float64(repo.Stars),
		"repo_forks":        float64(repo.Forks),
		"dependents":        float64(repo.Dependents),
		"package_downloads": float64(repo.Downloads),
	}
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
