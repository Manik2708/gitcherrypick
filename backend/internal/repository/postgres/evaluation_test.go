package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestEvaluationRepositoryPersist(t *testing.T) {
	t.Run("scores keep the dimensions that justify them", func(t *testing.T) {
		// ADR-0007 §4: per-dimension remarks rather than one rationale. A score
		// with no reasoning behind it cannot be disputed, and the dispute flow
		// is what produces the labelled set.
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		dims := map[string]domain.Dimension{
			"substance":            {Score: 84, Remark: "Reworked the retry path to be idempotent."},
			"complexity":           {Score: 88, Remark: "Required reasoning about interleaving."},
			"conversation_quality": {Score: 79, Remark: "Drove a design discussion."},
			"craft":                {Score: 76, Remark: "Tests cover the redelivery case."},
			"skill_specificity":    {Score: 90, Remark: "Concurrency work in idiomatic Go."},
		}
		mustPersist(ctx, t, db, fx.claim, []domain.PRSkillScore{{
			Position: 1, SkillID: fx.goSkill, Score: 79.3,
			QualityQ: 83.1, ReachR: 0.71, EngagementE: 0.62,
			NormsGeneration: 1, Dimensions: dims,
		}}, nil)

		got, err := db.Evaluations().Scores(ctx, fx.claim)
		if err != nil {
			t.Fatalf("reading scores: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 score, got %d", len(got))
		}
		if got[0].Score != 79.3 {
			t.Errorf("expected 79.3, got %v", got[0].Score)
		}
		if len(got[0].Dimensions) != 5 {
			t.Fatalf("expected 5 dimensions, got %d", len(got[0].Dimensions))
		}
		if d := got[0].Dimensions["substance"]; d.Score != 84 || d.Remark == "" {
			t.Errorf("the remark did not survive the round trip: %+v", d)
		}
	})

	t.Run("suggestions are written inert", func(t *testing.T) {
		// The model suggests; only the contributor promotes (ADR-0003 §10).
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		mustPersist(ctx, t, db, fx.claim, nil, []domain.ClaimSkill{{Slug: "kubernetes"}})

		claim, err := db.Claims().ByID(ctx, fx.claim)
		if err != nil {
			t.Fatalf("reading the claim: %v", err)
		}
		var inert int
		for _, s := range claim.Skills {
			if s.IsInert() {
				inert++
			}
		}
		if inert != 1 {
			t.Errorf("expected 1 inert suggestion, got %d", inert)
		}
		if n := count(t, db,
			`SELECT count(*) FROM claim_skills WHERE claim_id = $1 AND origin = 'ai_suggested'
			   AND accepted_at IS NULL AND dismissed_at IS NULL`, string(fx.claim)); n != 1 {
			t.Error("the suggestion was not written inert")
		}
	})

	t.Run("a suggestion the contributor already declared is not duplicated", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		// The claim already declares 'go'.
		mustPersist(ctx, t, db, fx.claim, nil, []domain.ClaimSkill{{Slug: "go"}})

		if n := count(t, db,
			`SELECT count(*) FROM claim_skills WHERE claim_id = $1 AND skill_id = $2`,
			string(fx.claim), string(fx.goSkill)); n != 1 {
			t.Error("the declared skill was duplicated as a suggestion")
		}
		if n := count(t, db,
			`SELECT count(*) FROM claim_skills WHERE claim_id = $1 AND origin = 'user_declared'`,
			string(fx.claim)); n != 1 {
			t.Error("the declared skill's origin was overwritten")
		}
	})

	t.Run("a failure rolls the whole evaluation back", func(t *testing.T) {
		// A half-persisted evaluation would leave a contributor with some
		// skills promoted and others not, from one model call.
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Evaluations().Persist(ctx, tx, fx.claim, []domain.PRSkillScore{
				{Position: 1, SkillID: fx.goSkill, Score: 79.3, NormsGeneration: 1},
				// Position 9 has no evidence row, so this fails mid-write.
				{Position: 9, SkillID: fx.goSkill, Score: 50.0, NormsGeneration: 1},
			}, nil)
		})
		if err == nil {
			t.Fatal("expected the second score to fail")
		}
		if n := count(t, db, `SELECT count(*) FROM pr_skill_scores`); n != 0 {
			t.Errorf("expected the first score rolled back, found %d", n)
		}
	})
}

func TestEvaluationRepositoryIdempotency(t *testing.T) {
	t.Run("redelivery of the same claim version is free", func(t *testing.T) {
		// The broker is at-least-once: the same job WILL arrive twice
		// (ADR-0004).
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		done, err := db.Evaluations().AlreadyEvaluated(ctx, fx.claim, 1, "v1")
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if done {
			t.Error("a pending evaluation must not read as complete")
		}

		if _, err := db.Pool().Exec(ctx,
			`UPDATE evaluations SET status = 'succeeded' WHERE claim_id = $1`, string(fx.claim)); err != nil {
			t.Fatalf("completing: %v", err)
		}

		done, err = db.Evaluations().AlreadyEvaluated(ctx, fx.claim, 1, "v1")
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if !done {
			t.Error("expected the completed evaluation to be recognised")
		}
	})

	t.Run("an edited claim is a new judgement", func(t *testing.T) {
		// Keyed on the VERSION, not the claim: editing produces genuinely new
		// evidence and must be judged again.
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)
		if _, err := db.Pool().Exec(ctx,
			`UPDATE evaluations SET status = 'succeeded' WHERE claim_id = $1`, string(fx.claim)); err != nil {
			t.Fatalf("completing: %v", err)
		}

		done, err := db.Evaluations().AlreadyEvaluated(ctx, fx.claim, 2, "v1")
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if done {
			t.Error("version 2 has not been judged and must not read as complete")
		}
	})
}

func TestEvaluationRepositorySnapshots(t *testing.T) {
	t.Run("a generalist score may exceed 100; a skill score may not", func(t *testing.T) {
		// ck_snapshot_score_range. The unbounded breadth score and the bounded
		// depth score share a table, and the constraint is what keeps them
		// distinguishable (ADR-0007 §5).
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Evaluations().Snapshot(ctx, tx, domain.ScoreSnapshot{
				UserID: fx.user, Kind: domain.ScoreGeneralist, Score: 240.0, RubricVersion: "v1"})
		}); err != nil {
			t.Fatalf("a generalist score above 100 must be storable: %v", err)
		}

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Evaluations().Snapshot(ctx, tx, domain.ScoreSnapshot{
				UserID: fx.user, Kind: domain.ScoreOverall, Score: 240.0, RubricVersion: "v1"})
		})
		if err == nil {
			t.Error("an overall score above 100 must be refused")
		}
	})

	t.Run("a skill snapshot must name its skill", func(t *testing.T) {
		// ck_snapshot_skill_id: kind and skill_id cannot disagree.
		db, ctx := newDB(t), testContext(t)
		fx := newEvaluationFixture(ctx, t, db)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Evaluations().Snapshot(ctx, tx, domain.ScoreSnapshot{
				UserID: fx.user, Kind: domain.ScoreSkill, Score: 64.2, RubricVersion: "v1"})
		})
		if err == nil {
			t.Error("a skill snapshot with no skill_id must be refused")
		}

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Evaluations().Snapshot(ctx, tx, domain.ScoreSnapshot{
				UserID: fx.user, Kind: domain.ScoreSkill, SkillID: &fx.goSkill,
				Score: 64.2, RubricVersion: "v1"})
		}); err != nil {
			t.Fatalf("a well-formed skill snapshot: %v", err)
		}
	})
}

func TestNormsRepository(t *testing.T) {
	t.Run("observations do not move the maxima until recompute", func(t *testing.T) {
		// Norms that moved with every observation would make every pinned
		// score a moving target.
		db, ctx := newDB(t), testContext(t)

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			for _, v := range []float64{100, 5000, 190000} {
				if err := db.Norms().Observe(ctx, tx, "repo_stars", v); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("observing: %v", err)
		}

		current, err := db.Norms().Current(ctx)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if len(current) != 0 {
			t.Errorf("observations moved the maxima before recompute: %+v", current)
		}

		if err := db.Norms().Recompute(ctx, 1); err != nil {
			t.Fatalf("recomputing: %v", err)
		}
		current, err = db.Norms().Current(ctx)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		stars, ok := current["repo_stars"]
		if !ok {
			t.Fatal("expected repo_stars after recompute")
		}
		if stars.MaxValue != 190000 {
			t.Errorf("expected the maximum observed, got %v", stars.MaxValue)
		}
		if stars.ObservationCount != 3 {
			t.Errorf("expected 3 observations, got %d", stars.ObservationCount)
		}
	})

	t.Run("a recompute bumps the generation in place", func(t *testing.T) {
		// One row per metric (RFC-0005). The generation is a counter that tells
		// a nightly recompute which scores are stale, not a version history —
		// so the row is updated, not appended to.
		db, ctx := newDB(t), testContext(t)

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Norms().Observe(ctx, tx, "repo_stars", 100)
		}); err != nil {
			t.Fatalf("observing: %v", err)
		}
		if err := db.Norms().Recompute(ctx, 1); err != nil {
			t.Fatalf("recomputing: %v", err)
		}

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Norms().Observe(ctx, tx, "repo_stars", 500)
		}); err != nil {
			t.Fatalf("observing: %v", err)
		}
		if err := db.Norms().Recompute(ctx, 2); err != nil {
			t.Fatalf("recomputing: %v", err)
		}

		current, err := db.Norms().Current(ctx)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if current["repo_stars"].Generation != 2 {
			t.Errorf("expected generation 2 to be current, got %d", current["repo_stars"].Generation)
		}
		if n := count(t, db, `SELECT count(*) FROM global_norms WHERE metric = 'repo_stars'`); n != 1 {
			t.Errorf("expected one row per metric, found %d", n)
		}
		if current["repo_stars"].MaxValue != 500 {
			t.Errorf("expected the new maximum, got %v", current["repo_stars"].MaxValue)
		}
	})
}

func TestEvaluationRepositoryDeadLetter(t *testing.T) {
	t.Run("keeps the payload a human has to read", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)

		payload := []byte(`{"claim_id":"01920000-0000-7000-8000-0000000f0001","version":1}`)
		if err := db.Evaluations().DeadLetter(ctx, "job-1", "refusal", payload); err != nil {
			t.Fatalf("dead-lettering: %v", err)
		}

		var stored, kind string
		scanRow(ctx, t, db, []any{&stored, &kind},
			`SELECT payload::text, failure_kind FROM evaluation_dead_letters`)
		if kind != "refusal" {
			t.Errorf("expected the failure kind recorded, got %q", kind)
		}
		if !strings.Contains(stored, "01920000-0000-7000-8000-0000000f0001") {
			t.Errorf("the payload was not kept: %s", stored)
		}
	})
}

// --- helpers -----------------------------------------------------------------

type evaluationFixture struct {
	user    domain.UserID
	claim   domain.ClaimID
	goSkill domain.SkillID
}

// newEvaluationFixture builds a claim with one PR of evidence, one declared
// skill, and a pending evaluation to hang scores off.
func newEvaluationFixture(ctx context.Context, t *testing.T, db *postgres.DB) evaluationFixture {
	t.Helper()
	seedCatalogue(ctx, t, db)
	user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
	claim := mustCreateDraft(ctx, t, db, user.ID)

	mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
		PREvidence: []domain.PREvidence{{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 101}},
		Skills:     []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
	})

	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO evaluations
		    (id, claim_id, claim_version, rubric_version, model, prompt_version,
		     evidence_fingerprint, trigger, status)
		VALUES (gen_random_uuid(), $1::uuid, 1, 'v1', 'test', 'v1',
		        decode(md5($1::text), 'hex'), 'submission', 'pending')`,
		string(claim.ID)); err != nil {
		t.Fatalf("creating the evaluation: %v", err)
	}

	return evaluationFixture{user: user.ID, claim: claim.ID, goSkill: skillID(ctx, t, db, "go")}
}

func mustPersist(ctx context.Context, t *testing.T, db *postgres.DB, claim domain.ClaimID, scores []domain.PRSkillScore, suggestions []domain.ClaimSkill) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Evaluations().Persist(ctx, tx, claim, scores, suggestions)
	}); err != nil {
		t.Fatalf("persisting: %v", err)
	}
}
