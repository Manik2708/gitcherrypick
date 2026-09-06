package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Replaying a rubric sweep's judgements.
//
// A sweep re-runs the whole corpus. What the model SAYS about a pull request
// should not change because the arithmetic that weighs it did — so a fixture
// that sweeps declares `fake.ai.replay_previous_judgements` and gets the same
// verdicts back, isolating the change to the scoring.
//
// The judgements come from what was already stored rather than from the
// fixture, because restating them in the file would be the fixture asserting
// against its own copy of the first run.

// replayRequest is the `fake.ai` block a sweeping fixture writes.
type replayRequest struct {
	Replay bool `json:"replay_previous_judgements"`
}

// replayedJudgement is one (PR, skill) verdict, in the shape the fake model
// server serves.
type replayedJudgement struct {
	PRNumber      int             `json:"pr_number"`
	SkillSlug     string          `json:"skill_slug"`
	Disqualified  bool            `json:"disqualified"`
	Dimensions    json.RawMessage `json:"dimensions"`
	RelativeShare *float64        `json:"relative_share,omitempty"`
}

// replayedAI is the whole `fake.ai` block, rebuilt.
type replayedAI struct {
	Judgements []replayedJudgement `json:"judgements"`
}

// dimensionNames are the five the rubric weighs (ADR-0005).
var dimensionNames = []string{
	"substance", "complexity", "conversation_quality", "craft", "skill_specificity",
}

// dimensionsOrQuality recovers a verdict that was stored only as its weighted
// total.
//
// A seeded score carries quality_q and no breakdown. Replaying five EQUAL
// dimensions at that value reproduces the same weighted total under any
// weighting — which is exactly the property a sweep needs, since what changed
// is the weights and not what the model said.
func dimensionsOrQuality(stored json.RawMessage, quality float64) json.RawMessage {
	var existing map[string]any
	if err := json.Unmarshal(stored, &existing); err == nil && len(existing) > 0 {
		return stored
	}

	flat := make(map[string]replayedDimension, len(dimensionNames))
	for _, name := range dimensionNames {
		flat[name] = replayedDimension{
			Score:  int(quality + 0.5),
			Remark: "Replayed from the stored weighted score.",
		}
	}
	encoded, err := json.Marshal(flat)
	if err != nil {
		return stored
	}
	return encoded
}

// replayedDimension is one dimension of a replayed verdict.
type replayedDimension struct {
	Score  int    `json:"score"`
	Remark string `json:"remark"`
}

// wantsReplay reports whether a step asked for the previous judgements.
func wantsReplay(fake *Fake) bool {
	if fake == nil || len(fake.AI) == 0 {
		return false
	}
	var request replayRequest
	if err := json.Unmarshal(fake.AI, &request); err != nil {
		return false
	}
	return request.Replay
}

// replayJudgements rebuilds the model's verdicts from what it said last time.
//
// The most recent judgement of each (PR, skill) wins: a claim swept twice has
// several, and the current one is what a replay should return.
func replayJudgements(ctx context.Context, pool *pgxpool.Pool, schema string) (json.RawMessage, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	rows, err := conn.Query(ctx, `
		SELECT DISTINCT ON (pss.pr_number, s.slug)
		       pss.pr_number, s.slug, pss.dimension_scores, coalesce(pss.quality_q, 0),
		       cs.is_nominated_primary, pss.score,
		       (SELECT max(peer.score)
		        FROM pr_skill_scores peer
		        JOIN claim_skills pcs
		          ON pcs.claim_id = e.claim_id AND pcs.skill_id = peer.skill_id
		        WHERE peer.evaluation_id = pss.evaluation_id
		          AND peer.pr_number = pss.pr_number
		          AND pcs.is_nominated_primary)
		FROM pr_skill_scores pss
		JOIN evaluations e ON e.id = pss.evaluation_id
		JOIN skills s ON s.id = pss.skill_id
		JOIN claim_skills cs ON cs.claim_id = e.claim_id AND cs.skill_id = pss.skill_id
		WHERE e.status = 'succeeded'
		ORDER BY pss.pr_number, s.slug, e.completed_at DESC NULLS LAST`)
	if err != nil {
		return nil, fmt.Errorf("reading previous judgements: %w", err)
	}
	defer rows.Close()

	replayed := replayedAI{Judgements: []replayedJudgement{}}
	for rows.Next() {
		var (
			judgement replayedJudgement
			quality   float64
			primary   bool
			score     float64
			topScore  *float64
		)
		if err := rows.Scan(&judgement.PRNumber, &judgement.SkillSlug,
			&judgement.Dimensions, &quality, &primary, &score, &topScore); err != nil {
			return nil, fmt.Errorf("scanning a previous judgement: %w", err)
		}
		judgement.Dimensions = dimensionsOrQuality(judgement.Dimensions, quality)

		// A secondary skill is scored as a SHARE of the nominated primary's
		// score on the same PR (ADR-0005), and the share is not stored — only
		// the product is. Recovering it from the two scores is what makes the
		// replay reproduce the number rather than approximate it.
		if !primary && topScore != nil && *topScore > 0 {
			share := score / *topScore
			judgement.RelativeShare = &share
		}
		replayed.Judgements = append(replayed.Judgements, judgement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading previous judgements: %w", err)
	}

	return json.Marshal(replayed)
}
