package scoring_test

import (
	"math"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/scoring"
)

// Pure arithmetic, so these need nothing but numbers. That is the point of the
// package existing: the whole rubric is checkable without a model call.

func TestPRScoreDisqualification(t *testing.T) {
	t.Run("a typo fix in a huge repo scores 0, not 18", func(t *testing.T) {
		// THE regression test for the bug ADR-0007 exists to fix. Project reach
		// is a property of the repository rather than the change, so the
		// weighted formula floors near 18 in a popular project. Disqualification
		// bypasses the arithmetic entirely.
		typo := domain.Judgement{
			Disqualified:           true,
			DisqualificationReason: reason(domain.TypoOrWording),
			Dimensions: map[string]domain.Dimension{
				"substance": {Score: 2}, "complexity": {Score: 1},
				"conversation_quality": {Score: 3}, "craft": {Score: 5},
				"skill_specificity": {Score: 2},
			},
		}

		// Maximum possible reach and engagement: the most flattering case.
		got := scoring.PRScore(typo, 1.0, 1.0, domain.ScoringStandard)
		if got != 0 {
			t.Fatalf("expected 0, got %v", got)
		}

		// Without disqualification the same PR floors well above zero, which is
		// the number the mechanism exists to avoid.
		notDisqualified := typo
		notDisqualified.Disqualified = false
		notDisqualified.Dimensions = highDimensions()
		if scoring.PRScore(notDisqualified, 1.0, 1.0, domain.ScoringStandard) < 18 {
			t.Error("the arithmetic no longer floors high; this test has lost its meaning")
		}
	})

	t.Run("the quality floor catches what the named grounds miss", func(t *testing.T) {
		// Not disqualifiable, and not evidence of anything either (ADR-0007 §2).
		belowFloor := domain.Judgement{Dimensions: map[string]domain.Dimension{
			"substance": {Score: 4}, "complexity": {Score: 3},
			"conversation_quality": {Score: 0}, "craft": {Score: 6},
			"skill_specificity": {Score: 5},
		}}
		if got := scoring.PRScore(belowFloor, 1.0, 1.0, domain.ScoringStandard); got != 0 {
			t.Errorf("expected the floor to zero it, got %v", got)
		}

		atFloor := domain.Judgement{Dimensions: map[string]domain.Dimension{
			"substance": {Score: 5}, "complexity": {Score: 5},
			"conversation_quality": {Score: 5}, "craft": {Score: 5},
			"skill_specificity": {Score: 5},
		}}
		if got := scoring.PRScore(atFloor, 0, 0, domain.ScoringStandard); got == 0 {
			t.Error("exactly at the floor must not be zeroed")
		}
	})
}

func TestQuality(t *testing.T) {
	t.Run("weights sum to one, so all-100 scores 100", func(t *testing.T) {
		if got := scoring.Quality(allDimensions(100)); math.Abs(got-100) > 0.001 {
			t.Errorf("expected 100, got %v", got)
		}
	})

	t.Run("an ABSENT dimension counts as zero", func(t *testing.T) {
		// Dropping it would let a judgement that omitted conversation_quality
		// outscore one that honestly scored it 0 — and ADR-0007 §7 makes a PR
		// with no discussion score exactly that.
		full := allDimensions(100)
		partial := allDimensions(100)
		delete(partial, "conversation_quality")

		explicitZero := allDimensions(100)
		explicitZero["conversation_quality"] = domain.Dimension{Score: 0}

		if scoring.Quality(partial) != scoring.Quality(explicitZero) {
			t.Errorf("absent (%v) and zero (%v) must score the same",
				scoring.Quality(partial), scoring.Quality(explicitZero))
		}
		if scoring.Quality(partial) >= scoring.Quality(full) {
			t.Error("omitting a dimension must not score as well as scoring it")
		}
	})
}

func TestReach(t *testing.T) {
	norms := map[string]domain.GlobalNorms{
		"repo_stars":        {Metric: "repo_stars", MaxValue: 190000, Quantiles: []float64{5, 30, 240, 2100, 12000, 90000}},
		"repo_forks":        {Metric: "repo_forks", MaxValue: 52000, Quantiles: []float64{1, 8, 60, 500, 3000, 22000}},
		"repo_contributors": {Metric: "repo_contributors", MaxValue: 5000, Quantiles: []float64{1, 3, 12, 60, 300, 2000}},
	}

	t.Run("missing metrics are dropped and the rest renormalised", func(t *testing.T) {
		// A repository with no download count is not punished for it.
		partial := map[string]float64{"repo_stars": 190000, "repo_forks": 52000, "repo_contributors": 5000}
		if got := scoring.Reach(partial, norms, false); got < 0.9 {
			t.Errorf("maxed-out present metrics should reach ~1, got %v", got)
		}
	})

	t.Run("no metrics at all is zero, not a divide by zero", func(t *testing.T) {
		if got := scoring.Reach(map[string]float64{}, norms, false); got != 0 {
			t.Errorf("expected 0, got %v", got)
		}
	})

	t.Run("maintainer validation lifts but never exceeds 1", func(t *testing.T) {
		small := map[string]float64{"repo_stars": 100}
		plain := scoring.Reach(small, norms, false)
		lifted := scoring.Reach(small, norms, true)

		if lifted <= plain {
			t.Errorf("expected validation to lift %v, got %v", plain, lifted)
		}
		big := map[string]float64{"repo_stars": 190000, "repo_forks": 52000, "repo_contributors": 5000}
		if got := scoring.Reach(big, norms, true); got > 1 {
			t.Errorf("reach exceeded 1: %v", got)
		}
	})
}

func TestPRComponent(t *testing.T) {
	for _, tt := range []struct {
		name   string
		scores []float64
		want   float64
	}{
		// Fewer than five is SCALED, not averaged: one excellent PR must not
		// outrank five good ones.
		{"one PR is scaled by 1/5", []float64{100}, 20},
		{"two PRs are scaled by 2/5", []float64{100, 100}, 40},
		{"four PRs are scaled by 4/5", []float64{100, 100, 100, 100}, 80},
		{"five PRs are the mean", []float64{100, 100, 100, 100, 100}, 100},
		{"none is zero", nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := scoring.PRComponent(tt.scores); math.Abs(got-tt.want) > 0.001 {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}

	t.Run("takes the BEST five, so a weak sixth cannot lower the score", func(t *testing.T) {
		// A contributor is judged on their best evidence. If a sixth submission
		// could reduce the number, the incentive is to submit less.
		best := []float64{90, 80, 70, 60, 50}
		withWeak := append(append([]float64(nil), best...), 10)

		if scoring.PRComponent(withWeak) != scoring.PRComponent(best) {
			t.Errorf("a weak sixth changed the score: %v vs %v",
				scoring.PRComponent(withWeak), scoring.PRComponent(best))
		}
	})
}

func TestSecondaryPRScore(t *testing.T) {
	t.Run("is UNCAPPED", func(t *testing.T) {
		// A share above 1 means the secondary skill carried the change more
		// than the nominated primary did (ADR-0007 §5).
		if got := scoring.SecondaryPRScore(70, 1.15); math.Abs(got-80.5) > 0.001 {
			t.Errorf("expected 80.5, got %v", got)
		}
	})

	t.Run("is linear", func(t *testing.T) {
		if got := scoring.SecondaryPRScore(60, 0.5); math.Abs(got-30) > 0.001 {
			t.Errorf("expected 30, got %v", got)
		}
	})
}

func TestUserLevelScores(t *testing.T) {
	t.Run("no primary skill means NULL, not zero", func(t *testing.T) {
		// Zero would claim we measured something (ADR-0007).
		secondaryOnly := []scoring.SkillStanding{
			{Score: 81, Standing: domain.Secondary, Mode: domain.ScoringStandard},
		}
		if scoring.Overall(secondaryOnly) != nil {
			t.Error("expected nil overall")
		}
		if scoring.Generalist(secondaryOnly) != nil {
			t.Error("expected nil generalist")
		}
	})

	t.Run("Overall is bounded and Generalist is not", func(t *testing.T) {
		// The whole reason there are two numbers.
		broad := []scoring.SkillStanding{
			{Score: 100, Standing: domain.Primary}, {Score: 100, Standing: domain.Primary},
			{Score: 100, Standing: domain.Primary}, {Score: 100, Standing: domain.Primary},
			{Score: 100, Standing: domain.Primary}, {Score: 100, Standing: domain.Primary},
		}
		overall, generalist := scoring.Overall(broad), scoring.Generalist(broad)

		if *overall > 100 {
			t.Errorf("overall exceeded 100: %v", *overall)
		}
		if *generalist <= 100 {
			t.Errorf("expected generalist to exceed 100, got %v", *generalist)
		}
	})

	t.Run("one deep skill beats many shallow ones on Overall", func(t *testing.T) {
		deep := []scoring.SkillStanding{{Score: 90, Standing: domain.Primary}}
		shallow := []scoring.SkillStanding{
			{Score: 55, Standing: domain.Primary}, {Score: 52, Standing: domain.Primary},
			{Score: 48, Standing: domain.Primary},
		}
		if *scoring.Overall(deep) <= *scoring.Overall(shallow) {
			t.Error("Overall must reward depth")
		}
	})

	t.Run("...and loses to them on Generalist", func(t *testing.T) {
		// Both numbers are true and they rank the same people differently,
		// which is the entire argument for having two.
		deep := []scoring.SkillStanding{{Score: 90, Standing: domain.Primary}}
		shallow := []scoring.SkillStanding{
			{Score: 55, Standing: domain.Primary}, {Score: 52, Standing: domain.Primary},
			{Score: 48, Standing: domain.Primary}, {Score: 45, Standing: domain.Primary},
		}
		if *scoring.Generalist(deep) >= *scoring.Generalist(shallow) {
			t.Errorf("Generalist must reward breadth: %v vs %v",
				*scoring.Generalist(deep), *scoring.Generalist(shallow))
		}
	})

	t.Run("a secondary skill counts, at half weight", func(t *testing.T) {
		primaryOnly := []scoring.SkillStanding{{Score: 60, Standing: domain.Primary}}
		withSecondary := append(append([]scoring.SkillStanding(nil), primaryOnly...),
			scoring.SkillStanding{Score: 80, Standing: domain.Secondary})

		if *scoring.Overall(withSecondary) <= *scoring.Overall(primaryOnly) {
			t.Error("a secondary skill must contribute")
		}
		// 80 discounted to 40 sits below the primary's 60, so it cannot become
		// v₁ — which is what keeps an unranked skill from setting the floor.
		bothPrimary := []scoring.SkillStanding{
			{Score: 60, Standing: domain.Primary}, {Score: 80, Standing: domain.Primary},
		}
		if *scoring.Overall(withSecondary) >= *scoring.Overall(bothPrimary) {
			t.Error("a secondary must be worth less than the same score as a primary")
		}
	})

	t.Run("pr-review is lifted but capped at 100", func(t *testing.T) {
		review := []scoring.SkillStanding{
			{Score: 90, Standing: domain.Primary, Mode: domain.ScoringJudgedOnly},
		}
		plain := []scoring.SkillStanding{{Score: 90, Standing: domain.Primary}}

		if *scoring.Overall(review) <= *scoring.Overall(plain) {
			t.Error("pr-review must be lifted")
		}
		if *scoring.Overall(review) > 100 {
			t.Errorf("the lift exceeded the cap: %v", *scoring.Overall(review))
		}
	})
}

func TestSkillScore(t *testing.T) {
	t.Run("judged_only drops the project term", func(t *testing.T) {
		// A pr-review claim carries no project evidence: reviewing is evidenced
		// by threads, not by owning a repository.
		if got := scoring.SkillScore(70, 0.9, domain.ScoringJudgedOnly); got != 70 {
			t.Errorf("expected the PR component unchanged, got %v", got)
		}
	})

	t.Run("standard blends the project component", func(t *testing.T) {
		without := scoring.SkillScore(70, 0, domain.ScoringStandard)
		with := scoring.SkillScore(70, 1, domain.ScoringStandard)
		if with <= without {
			t.Error("project reach must contribute")
		}
		if with > 100 {
			t.Errorf("skill score exceeded 100: %v", with)
		}
	})
}

// --- helpers -----------------------------------------------------------------

func allDimensions(score int) map[string]domain.Dimension {
	return map[string]domain.Dimension{
		"substance": {Score: score}, "complexity": {Score: score},
		"conversation_quality": {Score: score}, "craft": {Score: score},
		"skill_specificity": {Score: score},
	}
}

func highDimensions() map[string]domain.Dimension { return allDimensions(60) }

func reason(r domain.RejectionReason) *domain.RejectionReason { return &r }
