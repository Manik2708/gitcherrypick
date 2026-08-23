// Package scoring holds every formula in ADR-0005 and ADR-0007.
//
// It is PURE: no database, no clock, no context. Every function is inputs to a
// number, which is what lets the whole rubric be table-tested against the
// worked examples rather than exercised through a model call.
//
// It is also the only place the constants live. A weight that appeared here
// and again in a SQL expression would eventually disagree with itself, and the
// disagreement would be invisible — two contributors scored by different code
// paths, both plausible.
package scoring

import (
	"math"
	"sort"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Weights and constants, binding per ADR-0005 §Formulas.
//
// They are named rather than inlined so a change shows up as a diff on a
// constant with a comment, not as a number moving inside an expression.
const (
	// Beta blends a log curve against a percentile rank in norm(). Neither
	// alone is right: the log flatters a handful of enormous repositories, the
	// percentile flattens the difference between 200 stars and 200,000.
	Beta = 0.5

	// Damping for the Overall breadth term. Each subsequent skill counts half
	// the previous one, so a fifth skill moves the number very little.
	Damping = 0.5

	// Gamma bounds how much breadth can lift a contributor above their best
	// skill. At 0.5 a perfect generalist reaches halfway to 100 from v₁.
	Gamma = 0.5

	// SecondaryWeight discounts a secondary skill in the ordered list. A skill
	// with four PRs of evidence is not worth the same as one with five.
	SecondaryWeight = 0.5

	// ReviewWeight lifts pr-review in the ordered list, capped at 100.
	// Reviewing well is rarer than writing well and is worth more than the raw
	// number suggests.
	ReviewWeight = 1.2
)

// Dimension weights for Q (ADR-0005). They sum to 1.
var dimensionWeights = map[string]float64{
	"substance":            0.30,
	"complexity":           0.25,
	"conversation_quality": 0.20,
	"craft":                0.15,
	"skill_specificity":    0.10,
}

// Reach weights for R. Missing metrics are dropped and the rest renormalised,
// so a repository with no download count is not punished for it.
var reachWeights = map[string]float64{
	"repo_stars":        0.35,
	"repo_forks":        0.15,
	"repo_contributors": 0.20,
	"dependents":        0.20,
	"package_downloads": 0.10,
}

// Engagement weights for E.
var engagementWeights = map[string]float64{
	"review_comments": 0.40,
	"reviews":         0.30,
	"participants":    0.30,
}

// Norm maps a raw metric onto 0..1 against the platform maximum.
//
// The blend is the point. ln(1+v)/ln(1+M) alone compresses everything below
// the largest repository into a narrow band; a percentile alone says a project
// with 200 stars and one with 200,000 are merely adjacent ranks. Half of each
// keeps both the shape and the ordering.
func Norm(value float64, norms domain.GlobalNorms) float64 {
	if value <= 0 || norms.MaxValue <= 0 {
		return 0
	}
	if value > norms.MaxValue {
		value = norms.MaxValue
	}

	logPart := math.Log1p(value) / math.Log1p(norms.MaxValue)
	return clamp01(Beta*logPart + (1-Beta)*Percentile(value, norms.Quantiles))
}

// Percentile interpolates between the sampled quantile boundaries.
//
// Bucketed rather than exact: an exact percentile rank over the whole corpus
// costs a full scan per query, the blend already halves this term's weight,
// and no product decision turns on the difference between the 73rd and 74th
// percentile.
func Percentile(value float64, quantiles []float64) float64 {
	if len(quantiles) == 0 {
		return 0
	}
	sorted := append([]float64(nil), quantiles...)
	sort.Float64s(sorted)

	// Below the lowest boundary.
	if value <= sorted[0] {
		return 0
	}
	for i := 1; i < len(sorted); i++ {
		if value > sorted[i] {
			continue
		}
		lower, upper := sorted[i-1], sorted[i]
		below := float64(i-1) / float64(len(sorted)-1)
		above := float64(i) / float64(len(sorted)-1)
		if upper == lower {
			return above
		}
		return below + (above-below)*(value-lower)/(upper-lower)
	}
	return 1
}

// Quality is the weighted dimension score, 0..100.
//
// Absent dimensions count as ZERO rather than being dropped. A judgement that
// omitted "conversation_quality" would otherwise score higher than one that
// scored it honestly at 0 — and ADR-0007 §7 makes a PR with no discussion
// score exactly that.
func Quality(dimensions map[string]domain.Dimension) float64 {
	var q float64
	for name, weight := range dimensionWeights {
		q += weight * float64(dimensions[name].Score)
	}
	return clamp(q, 0, 100)
}

// Reach is project reach, 0..1.
//
// A property of the REPOSITORY rather than of the change, which is precisely
// why a typo fix in a popular project would score ~18 without disqualification
// — the bug ADR-0007 exists to fix.
//
// Missing metrics are dropped and the remaining weights renormalised.
func Reach(metrics map[string]float64, norms map[string]domain.GlobalNorms, maintainerValidated bool) float64 {
	var weighted, total float64
	for metric, weight := range reachWeights {
		value, ok := metrics[metric]
		if !ok {
			continue
		}
		weighted += weight * Norm(value, norms[metric])
		total += weight
	}
	if total == 0 {
		return 0
	}

	r := weighted / total
	if maintainerValidated {
		// A maintainer saying the work mattered is worth more than any metric
		// the platform can compute.
		r = math.Min(1, r*1.25)
	}
	return clamp01(r)
}

// Engagement is conversation volume, 0..1.
func Engagement(metrics map[string]float64, norms map[string]domain.GlobalNorms) float64 {
	var weighted, total float64
	for metric, weight := range engagementWeights {
		value, ok := metrics[metric]
		if !ok {
			continue
		}
		weighted += weight * Norm(value, norms[metric])
		total += weight
	}
	if total == 0 {
		return 0
	}
	return clamp01(weighted / total)
}

// QualityFloor is the mean dimension score below which a pair scores zero.
//
// It catches what the nine named grounds miss: work that is not disqualifiable
// but is not evidence of anything either (ADR-0007 §2).
const QualityFloor = 5

// PRScore is one (PR, skill) pair's score.
//
// Disqualification and the quality floor BYPASS the arithmetic entirely. That
// is the whole mechanism: a typo fix scores 0 rather than the ~18 the weighted
// formula floors at in a popular repository, because the formula never runs.
func PRScore(j domain.Judgement, r, e float64, mode domain.ScoringMode) float64 {
	if j.Disqualified {
		return 0
	}
	if meanDimension(j.Dimensions) < QualityFloor {
		return 0
	}

	q := Quality(j.Dimensions)
	if mode == domain.ScoringJudgedOnly {
		// pr-review drops the PR-specific arithmetic: reviewing is judged by
		// reading the threads, and diff length says nothing about it. Project
		// reach stays, because where you review still matters.
		return clamp(100*(0.80*(q/100)+0.20*r), 0, 100)
	}
	return clamp(100*(0.70*(q/100)+0.20*r+0.10*e), 0, 100)
}

// SecondaryPRScore scales a primary's score by the model's relative share.
//
// UNCAPPED: a share above 1 means the secondary skill carried the change more
// than the nominated primary did, and that happens (ADR-0007 §5).
func SecondaryPRScore(primaryScore, relativeShare float64) float64 {
	return math.Max(0, primaryScore*relativeShare)
}

// PRComponent aggregates a skill's PR scores.
//
// Fewer than five PRs is scaled by n/5 rather than averaged, so a single
// excellent PR does not outrank five good ones. Five or more takes the best
// five: a contributor is judged on their best evidence, and submitting a weak
// sixth should never lower the number.
func PRComponent(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}

	sorted := append([]float64(nil), scores...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))

	if len(sorted) >= domain.PrimaryThreshold {
		return mean(sorted[:domain.PrimaryThreshold])
	}
	return mean(sorted) * float64(len(sorted)) / float64(domain.PrimaryThreshold)
}

// SkillScore combines the PR component with project reach.
func SkillScore(prComponent, projectComponent float64, mode domain.ScoringMode) float64 {
	if mode == domain.ScoringJudgedOnly {
		// No project term: a pr-review claim carries no project evidence,
		// because reviewing is evidenced by threads rather than by owning a
		// repository.
		return clamp(prComponent, 0, 100)
	}
	return clamp(100*(0.85*(prComponent/100)+0.15*projectComponent), 0, 100)
}

// SkillStanding is one skill's contribution to the user-level scores.
type SkillStanding struct {
	Score    float64
	Standing domain.Standing
	Mode     domain.ScoringMode
}

// OrderedValues builds the descending list the user-level scores are computed
// from.
//
// Secondaries are discounted and pr-review is lifted, both before sorting — so
// the ORDER reflects what each skill is worth rather than its raw number.
func OrderedValues(skills []SkillStanding) []float64 {
	out := make([]float64, 0, len(skills))
	for _, s := range skills {
		v := s.Score
		if s.Standing == domain.Secondary {
			v *= SecondaryWeight
		}
		if s.Mode == domain.ScoringJudgedOnly {
			v = math.Min(100, v*ReviewWeight)
		}
		out = append(out, v)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(out)))
	return out
}

// Overall is the DEPTH score, bounded 0..100.
//
// v₁ + (100−v₁)·γ·B: the best skill sets the floor and breadth lifts it toward
// 100 without ever reaching it. Bounded on purpose — a contributor with one
// outstanding skill and a contributor with six good ones should not be
// separated by an unbounded sum on a board meant to measure depth.
//
// Returns nil when there is no PRIMARY skill. Null, not zero: zero would claim
// we measured something (ADR-0007).
func Overall(skills []SkillStanding) *float64 {
	if !hasPrimary(skills) {
		return nil
	}
	values := OrderedValues(skills)
	if len(values) == 0 {
		return nil
	}

	v1 := values[0]
	overall := v1 + (100-v1)*Gamma*breadth(values)
	out := clamp(overall, 0, 100)
	return &out
}

// Generalist is the BREADTH score, deliberately unbounded.
//
// v₁ + Σ(i≥2) vᵢ/√i. The square root decays slowly enough that a fifth and a
// sixth skill still count, which is the entire difference from Overall — one
// board rewards depth and the other rewards range, and a contributor can top
// one while sitting mid-table on the other.
func Generalist(skills []SkillStanding) *float64 {
	if !hasPrimary(skills) {
		return nil
	}
	values := OrderedValues(skills)
	if len(values) == 0 {
		return nil
	}

	total := values[0]
	for i := 1; i < len(values); i++ {
		total += values[i] / math.Sqrt(float64(i+1))
	}
	out := math.Max(0, total)
	return &out
}

// breadth is B: the damped mean of every skill after the first.
func breadth(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var weighted, total float64
	for i := 1; i < len(values); i++ {
		w := math.Pow(Damping, float64(i-1))
		weighted += (values[i] / 100) * w
		total += w
	}
	if total == 0 {
		return 0
	}
	return clamp01(weighted / total)
}

func hasPrimary(skills []SkillStanding) bool {
	for _, s := range skills {
		if s.Standing == domain.Primary {
			return true
		}
	}
	return false
}

func meanDimension(dimensions map[string]domain.Dimension) float64 {
	if len(dimensions) == 0 {
		return 0
	}
	var total float64
	for _, d := range dimensions {
		total += float64(d.Score)
	}
	return total / float64(len(dimensions))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func clamp01(v float64) float64       { return clamp(v, 0, 1) }
