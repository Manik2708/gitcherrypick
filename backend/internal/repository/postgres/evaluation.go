package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// Record opens an evaluation, or returns the one already open for this
// (claim, version, evidence, rubric).
//
// ON CONFLICT DO NOTHING against uq_evaluation: a redelivered message must not
// create a second run over the same evidence, and the unique index is what
// makes that impossible rather than merely unlikely (ADR-0004).
func (r *EvaluationRepository) Record(ctx context.Context, t port.Tx, e port.Evaluation) error {
	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO evaluations
		    (id, claim_id, claim_version, rubric_version, model, prompt_version,
		     evidence_fingerprint, trigger, status, completed_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7::evaluation_trigger,
		        'succeeded', now())
		ON CONFLICT DO NOTHING`,
		string(e.ClaimID), e.ClaimVersion, e.RubricVersion, e.Model, e.PromptVersion,
		e.Fingerprint, e.Trigger); err != nil {
		return translate(err, fmt.Sprintf("recording the evaluation for claim %s", e.ClaimID))
	}
	return nil
}

// SkillPRScores reads the surviving per-PR scores for one (user, skill).
//
// Joined through the LINKS rather than through claims: a link records that
// this PR still counts for this skill, and a withdrawn claim releases its
// links — so a score whose evidence is gone drops out here without needing a
// second rule to remember that (ADR-0007).
func (r *EvaluationRepository) SkillPRScores(ctx context.Context, t port.Tx, id domain.UserID, skillID domain.SkillID) ([]float64, error) {
	rows, err := r.db.q(t).Query(ctx, `
		SELECT pss.score
		FROM user_skill_pr_links l
		JOIN claims c ON c.id = l.claim_id
		JOIN evaluations e ON e.claim_id = c.id AND e.status = 'succeeded'
		JOIN pr_skill_scores pss
		  ON pss.evaluation_id = e.id AND pss.skill_id = l.skill_id
		 AND pss.repo_owner = l.repo_owner AND pss.repo_name = l.repo_name
		 AND pss.pr_number = l.pr_number
		WHERE l.user_id = $1 AND l.skill_id = $2 AND l.status = 'scored'`,
		string(id), string(skillID))
	if err != nil {
		return nil, translate(err, "reading surviving PR scores")
	}
	defer rows.Close()

	var out []float64
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			return nil, translate(err, "scanning a PR score")
		}
		out = append(out, score)
	}
	return out, translate(rows.Err(), "reading surviving PR scores")
}

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
		// A dropped pair is NOT stored. ck_pr_skill_score_range enforces
		// score > 0 because a zero-scoring skill is dropped rather than
		// recorded as a zero (ADR-0003) — the verdict lives on the link's
		// rejection_reason, which is what a claim read reports it from.
		if s.Disqualified || s.Score <= 0 {
			continue
		}

		// The dimension scores and their remarks travel with the number they
		// justify.
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
			INSERT INTO claim_skills
			    (id, claim_id, skill_id, origin, is_nominated_primary, rationale)
			VALUES (gen_random_uuid(), $1, (SELECT id FROM skills WHERE slug = $2),
			        'ai_suggested', false, NULLIF($3, ''))
			ON CONFLICT (claim_id, skill_id) DO NOTHING`,
			string(claimID), sg.Slug, sg.Rationale); err != nil {
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
func (r *EvaluationRepository) AlreadyEvaluated(ctx context.Context, claimID domain.ClaimID, version int, rubricVersion string) (bool, error) {
	var done bool
	err := r.db.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM evaluations
		    WHERE claim_id = $1 AND claim_version = $2 AND rubric_version = $3
		      AND status = 'succeeded'
		)`, string(claimID), version, rubricVersion).Scan(&done)
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
		       coalesce(observation_count, 0), last_recomputed_at, quantiles
		FROM global_norms
		ORDER BY metric`)
	if err != nil {
		return nil, translate(err, "reading global norms")
	}
	defer rows.Close()

	out := map[string]domain.GlobalNorms{}
	for rows.Next() {
		var (
			n          domain.GlobalNorms
			recomputed *time.Time
			quantiles  []byte
		)
		// last_recomputed_at is NULL until the nightly job first runs, which is
		// the state every fresh deployment starts in. Scanning it into a
		// non-pointer made the first evaluation on a new database fail.
		if err := rows.Scan(&n.Metric, &n.Generation, &n.MaxValue, &n.MaxSource,
			&n.ObservationCount, &recomputed, &quantiles); err != nil {
			return nil, translate(err, "scanning global norm")
		}
		n.Quantiles = decodeQuantiles(quantiles)
		if recomputed != nil {
			n.LastRecomputedAt = *recomputed
		}
		out[n.Metric] = n
	}
	return out, translate(rows.Err(), "reading global norms")
}

// decodeQuantiles turns the stored {p10: …, p25: …} object into boundaries.
//
// Stored as a labelled object because that is how a statistician reads it, and
// consumed as a sorted slice because that is what the percentile blend needs.
// The labels carry no meaning beyond their order, so they are dropped here.
func decodeQuantiles(raw []byte) []float64 {
	if len(raw) == 0 {
		return nil
	}
	var labelled map[string]float64
	if err := json.Unmarshal(raw, &labelled); err != nil {
		return nil
	}

	out := make([]float64, 0, len(labelled))
	for _, v := range labelled {
		out = append(out, v)
	}
	sort.Float64s(out)
	return out
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

// --- rubric sweeps (RFC-0015) ------------------------------------------------

// ActiveRubricVersion is the target of the most recent sweep.
//
// Falls back to the configured version when none has run, which is the state
// of a fresh platform. Read on the search path, so it is one indexed row.
func (r *EvaluationRepository) ActiveRubricVersion(ctx context.Context, fallback string) (string, error) {
	var version string
	err := r.db.pool.QueryRow(ctx, `
		SELECT to_rubric_version FROM rubric_sweeps
		ORDER BY created_at DESC
		LIMIT 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return "", translate(err, "reading the active rubric version")
	}
	return version, nil
}

// sweepColumns is the projection every sweep read shares.
const sweepColumns = `id, from_rubric_version, to_rubric_version, reason,
	requested_by, claims_enqueued, created_at, completed_at`

// OpenSweep returns the sweep still draining, or nil.
func (r *EvaluationRepository) OpenSweep(ctx context.Context) (*domain.RubricSweep, error) {
	var s domain.RubricSweep
	err := r.db.pool.QueryRow(ctx,
		`SELECT `+sweepColumns+` FROM rubric_sweeps WHERE completed_at IS NULL`).
		Scan(&s.ID, &s.From, &s.To, &s.Reason, &s.RequestedBy,
			&s.ClaimsEnqueued, &s.CreatedAt, &s.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err, "reading the open sweep")
	}
	return &s, nil
}

// RecordSweep writes the sweep alongside the work it enqueued.
func (r *EvaluationRepository) RecordSweep(ctx context.Context, t port.Tx, s *domain.RubricSweep) (*domain.RubricSweep, error) {
	id := s.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating sweep id: %w", err)
		}
		id = domain.RequestID(generated.String())
	}

	var created domain.RubricSweep
	err := r.db.q(t).QueryRow(ctx, `
		INSERT INTO rubric_sweeps
		    (id, from_rubric_version, to_rubric_version, reason, requested_by,
		     claims_enqueued, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+sweepColumns,
		string(id), s.From, s.To, s.Reason, string(s.RequestedBy),
		s.ClaimsEnqueued, s.CreatedAt).
		Scan(&created.ID, &created.From, &created.To, &created.Reason,
			&created.RequestedBy, &created.ClaimsEnqueued,
			&created.CreatedAt, &created.CompletedAt)
	if err != nil {
		return nil, translate(err, "recording the sweep")
	}
	return &created, nil
}

// CompleteSweep closes a sweep whose corpus has drained.
func (r *EvaluationRepository) CompleteSweep(ctx context.Context, t port.Tx, id domain.RequestID, at time.Time) error {
	_, err := r.db.q(t).Exec(ctx,
		`UPDATE rubric_sweeps SET completed_at = $2 WHERE id = $1 AND completed_at IS NULL`,
		string(id), at)
	return translate(err, fmt.Sprintf("completing sweep %s", id))
}

// SweptClaimsRemaining counts what a sweep has left to judge.
//
// Counted from the QUEUE rather than from the sweep row: the row records what
// was taken on, and what is left is a fact about the queue right now.
func (r *EvaluationRepository) SweptClaimsRemaining(ctx context.Context, t port.Tx) (int, error) {
	var remaining int
	if err := r.db.q(t).QueryRow(ctx, `
		SELECT count(*) FROM evaluation_jobs WHERE topic = 'claims.sweep'`).
		Scan(&remaining); err != nil {
		return 0, translate(err, "counting the sweep's remaining claims")
	}
	return remaining, nil
}
