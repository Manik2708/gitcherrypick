package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// EvaluationRepository owns judgements, scores and the job record.
//
// Persist writes everything one evaluated claim produced in ONE transaction.
// A half-persisted evaluation would leave a contributor with some skills
// promoted and others not, from the same model call — a state nothing could
// explain and nothing would repair.
type EvaluationRepository struct{ db *DB }

// Evaluations returns the evaluation repository.
func (db *DB) Evaluations() *EvaluationRepository { return &EvaluationRepository{db: db} }

var _ port.EvaluationRepository = (*EvaluationRepository)(nil)

// Persist writes the per-pair scores and the AI's skill suggestions.
//
// It takes a Tx rather than opening its own, so the caller can join it to the
// link-status and standing writes that follow. All of it is one atomic unit,
// which is what ADR-0004 step 9 means by the persist step.
//
// Suggestions are written INERT — origin 'ai_suggested', no accepted_at. The
// model suggests; only the contributor promotes (ADR-0003 §10).
func (r *EvaluationRepository) Persist(ctx context.Context, t port.Tx, claimID domain.ClaimID, scores []domain.PRSkillScore, suggestions []domain.ClaimSkill) error {
	q := r.db.q(t)

	var evaluationID string
	if err := q.QueryRow(ctx, `
		SELECT id FROM evaluations
		WHERE claim_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, string(claimID)).Scan(&evaluationID); err != nil {
		return translate(err, fmt.Sprintf("finding the evaluation for claim %s", claimID))
	}

	for _, s := range scores {
		// The dimension scores and their remarks travel with the number they
		// justify. A disqualified pair has none, and an empty object is the
		// honest encoding of that rather than a missing column.
		dims := s.Dimensions
		if dims == nil {
			dims = map[string]domain.Dimension{}
		}
		dimensions, err := json.Marshal(dims)
		if err != nil {
			return fmt.Errorf("encoding dimensions: %w", err)
		}

		var owner, repo string
		var prNumber int
		if err := q.QueryRow(ctx, `
			SELECT repo_owner, repo_name, pr_number
			FROM claim_pr_evidence WHERE claim_id = $1 AND position = $2`,
			string(claimID), s.Position).Scan(&owner, &repo, &prNumber); err != nil {
			return translate(err, fmt.Sprintf("locating evidence at position %d", s.Position))
		}

		if _, err := q.Exec(ctx, `
			INSERT INTO pr_skill_scores
			    (id, evaluation_id, skill_id, repo_owner, repo_name, pr_number, position,
			     dimension_scores, score, quality_q, reach_r, engagement_e, norms_generation)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			evaluationID, string(s.SkillID), owner, repo, prNumber, s.Position,
			string(dimensions), s.Score, s.QualityQ, s.ReachR, s.EngagementE, s.NormsGeneration); err != nil {
			return translate(err, fmt.Sprintf("writing score for position %d", s.Position))
		}
	}

	for _, sg := range suggestions {
		if _, err := q.Exec(ctx, `
			INSERT INTO claim_skills (id, claim_id, skill_id, origin, is_nominated_primary)
			VALUES (gen_random_uuid(), $1, (SELECT id FROM skills WHERE slug = $2), 'ai_suggested', false)
			ON CONFLICT (claim_id, skill_id) DO NOTHING`,
			string(claimID), sg.Slug); err != nil {
			return translate(err, fmt.Sprintf("writing suggestion %q", sg.Slug))
		}
	}
	return nil
}

// Scores reads what a claim was awarded.
func (r *EvaluationRepository) Scores(ctx context.Context, claimID domain.ClaimID) ([]domain.PRSkillScore, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT s.skill_id, s.position, s.score,
		       coalesce(s.quality_q, 0), coalesce(s.reach_r, 0), coalesce(s.engagement_e, 0),
		       coalesce(s.norms_generation, 0), e.rubric_version, s.dimension_scores
		FROM pr_skill_scores s
		JOIN evaluations e ON e.id = s.evaluation_id
		WHERE e.claim_id = $1
		ORDER BY s.position, s.skill_id`, string(claimID))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading scores for claim %s", claimID))
	}
	defer rows.Close()

	var out []domain.PRSkillScore
	for rows.Next() {
		s := domain.PRSkillScore{ClaimID: claimID}
		var dimensions []byte
		if err := rows.Scan(&s.SkillID, &s.Position, &s.Score,
			&s.QualityQ, &s.ReachR, &s.EngagementE, &s.NormsGeneration, &s.RubricVersion,
			&dimensions); err != nil {
			return nil, translate(err, "scanning score")
		}
		if err := json.Unmarshal(dimensions, &s.Dimensions); err != nil {
			return nil, fmt.Errorf("decoding dimensions: %w", err)
		}
		out = append(out, s)
	}
	return out, translate(rows.Err(), "reading scores")
}

// Snapshot records a score at a point in time.
//
// Snapshots are kept across rubric sweeps: a sweep supersedes scores, it does
// not erase the history of what was once true. ck_snapshot_score_range permits
// a generalist score above 100 and nothing else, which is what makes the
// unbounded breadth score storable alongside the bounded depth one.
func (r *EvaluationRepository) Snapshot(ctx context.Context, t port.Tx, s domain.ScoreSnapshot) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generating snapshot id: %w", err)
	}

	var skillID *string
	if s.SkillID != nil {
		v := string(*s.SkillID)
		skillID = &v
	}

	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO score_snapshots (id, user_id, skill_id, kind, score, reason, rubric_version)
		VALUES ($1, $2, $3, $4::score_kind, $5, $6::snapshot_reason, $7)`,
		id.String(), string(s.UserID), skillID, string(s.Kind), s.Score,
		"evaluation", s.RubricVersion); err != nil {
		return translate(err, "recording score snapshot")
	}
	return nil
}

// AlreadyEvaluated reports whether this claim VERSION has been judged.
//
// The broker is at-least-once, so the same job WILL arrive twice and the
// second delivery has to cost nothing (ADR-0004). Keyed on the version rather
// than the claim, because an edited claim is a genuinely new judgement.
func (r *EvaluationRepository) AlreadyEvaluated(ctx context.Context, claimID domain.ClaimID, version int) (bool, error) {
	var done bool
	err := r.db.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM evaluations
		    WHERE claim_id = $1 AND claim_version = $2 AND status = 'succeeded'
		)`, string(claimID), version).Scan(&done)
	if err != nil {
		return false, translate(err, "checking for a completed evaluation")
	}
	return done, nil
}

// DeadLetter parks a message that cannot be processed.
//
// The payload is kept so a human can see what arrived. A poisonous message
// that retried forever would starve the queue behind it, and one discarded
// silently would lose a contributor's submission with no trace.
func (r *EvaluationRepository) DeadLetter(ctx context.Context, jobID domain.JobID, reason string, payload []byte) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generating dead letter id: %w", err)
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	if _, err := r.db.pool.Exec(ctx, `
		INSERT INTO evaluation_dead_letters
		    (id, topic, payload, attempts, failure_kind, last_error, first_seen_at)
		VALUES ($1, 'evaluation', $2, 1, $3, $4, now())`,
		id.String(), string(payload), reason, reason); err != nil {
		return translate(err, fmt.Sprintf("dead-lettering job %s", jobID))
	}
	return nil
}

// NormsRepository owns the shared maxima every arithmetic signal is measured
// against.
//
// Fixed per generation, so a score is reproducible. Norms that moved with every
// observation would make every pinned score a moving target, and two
// contributors judged a week apart would not be comparable.
type NormsRepository struct{ db *DB }

// Norms returns the norms repository.
func (db *DB) Norms() *NormsRepository { return &NormsRepository{db: db} }

var _ port.NormsRepository = (*NormsRepository)(nil)

// Current reads the active maxima, one row per metric.
func (r *NormsRepository) Current(ctx context.Context) (map[string]domain.GlobalNorms, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT metric, generation, max_value, coalesce(max_source, ''),
		       coalesce(observation_count, 0), last_recomputed_at
		FROM global_norms
		ORDER BY metric`)
	if err != nil {
		return nil, translate(err, "reading global norms")
	}
	defer rows.Close()

	out := map[string]domain.GlobalNorms{}
	for rows.Next() {
		var n domain.GlobalNorms
		if err := rows.Scan(&n.Metric, &n.Generation, &n.MaxValue, &n.MaxSource,
			&n.ObservationCount, &n.LastRecomputedAt); err != nil {
			return nil, translate(err, "scanning global norm")
		}
		out[n.Metric] = n
	}
	return out, translate(rows.Err(), "reading global norms")
}

// Observe records one measurement.
//
// Written in the enrichment transaction, so an observation cannot exist for a
// PR whose evidence was rolled back. Observations accumulate; they do not move
// the maxima until Recompute runs.
func (r *NormsRepository) Observe(ctx context.Context, t port.Tx, metric string, value float64) error {
	_, err := r.db.q(t).Exec(ctx,
		`INSERT INTO metric_observations (metric, value) VALUES ($1::arithmetic_metric, $2)`,
		metric, value)
	return translate(err, fmt.Sprintf("observing %s", metric))
}

// Recompute advances every metric to a new generation.
//
// ONE ROW PER METRIC, updated in place — global_norms is keyed on metric alone
// (RFC-0005). The generation is a counter, not a version history: a score
// records which generation it was computed against so a nightly recompute can
// tell which scores are stale and resume where it left off, not so the old
// maxima can be read back.
//
// Absolute cached facts never change. What moves is everyone's RELATIVE value
// when a bigger repository than any seen before enters the corpus.
func (r *NormsRepository) Recompute(ctx context.Context, generation int) error {
	_, err := r.db.pool.Exec(ctx, `
		INSERT INTO global_norms (metric, generation, max_value, max_source, observation_count, last_recomputed_at)
		SELECT metric, $1, max(value), 'recompute', count(*), now()
		FROM metric_observations
		GROUP BY metric
		ON CONFLICT (metric) DO UPDATE
		  SET generation        = EXCLUDED.generation,
		      max_value         = EXCLUDED.max_value,
		      observation_count = EXCLUDED.observation_count,
		      last_recomputed_at = now()`, generation)
	return translate(err, "recomputing global norms")
}
