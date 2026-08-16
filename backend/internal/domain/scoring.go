package domain

import "time"

// The judgement contract and the score families (ADR-0007).
//
// Three families come out of an evaluation: per PR per skill, per skill, and
// two user-level numbers — Overall (depth, bounded 0-100) and Generalist
// (breadth, unbounded). Disqualification and the quality floor bypass the
// arithmetic entirely.

// Dimension is one axis the model scores, with the remark that justifies it.
// Per-dimension remarks rather than one rationale: a single paragraph cannot be
// checked against a single number (ADR-0007 §4).
type Dimension struct {
	Score  int    `json:"score"`
	Remark string `json:"remark"`
}

// Judgement is what the model returns for one (PR, skill) pair.
type Judgement struct {
	PRNumber  int
	SkillSlug string

	// A disqualified judgement carries a reason and no dimensions. PR_score is
	// 0 and the arithmetic is skipped — this is what makes a typo fix score 0
	// rather than the ~18 the weighted formula floors at in a popular repo.
	Disqualified           bool
	DisqualificationReason *RejectionReason

	Dimensions map[string]Dimension

	// Set only for a secondary skill. The secondary's PR score is linear —
	// share x primary — and the share is UNCAPPED, so a secondary skill can
	// outscore the nominated primary on the same PR (ADR-0007 §5).
	RelativeShare *float64
}

// RejectionReason is one of the nine disqualification grounds, plus the quality
// floor. Exhaustive: the model may not invent a tenth.
//
// "AI-generated" is deliberately NOT a ground — a merge by someone else is the
// gate that matters, and unreviewable authorship claims would make the rubric a
// guessing game.
type RejectionReason string

// The nine disqualification grounds, plus the quality floor. Exhaustive: the
// model may not invent a tenth.
const (
	TypoOrWording              RejectionReason = "typo_or_wording"
	FormattingOnly             RejectionReason = "formatting_only"
	GeneratedOutput            RejectionReason = "generated_output"
	MechanicalDependencyBump   RejectionReason = "mechanical_dependency_bump"
	RevertOnly                 RejectionReason = "revert_only"
	NotTheClaimedSkill         RejectionReason = "not_the_claimed_skill"
	AuthoredByOther            RejectionReason = "authored_by_other"
	UnrelatedToIssue           RejectionReason = "unrelated_to_issue"
	MaintainerFlaggedUnrelated RejectionReason = "maintainer_flagged_unrelated"

	// Not a model verdict. Applied by the scorer when the mean dimension score
	// falls below 5 — it catches what the named grounds miss.
	BelowQualityFloor RejectionReason = "below_quality_floor"
)

// PRSkillScore is one scored (PR, skill) pair, with the components retained so
// a score can be explained after the fact.
type PRSkillScore struct {
	ClaimID  ClaimID
	Position int
	SkillID  SkillID
	Score    float64

	QualityQ    float64 // the weighted dimension score
	ReachR      float64 // project reach, a property of the repository
	EngagementE float64 // conversation volume, normalised

	// What the model said, per dimension, with the remark that justifies each
	// number. Stored alongside the score because a score with no reasoning
	// behind it cannot be disputed, and the dispute flow is what produces the
	// labelled set (ADR-0007 §4, §6).
	Dimensions map[string]Dimension

	Disqualified    bool
	RejectionReason *RejectionReason

	// Which generation of global_norms the arithmetic used. A score computed
	// against different maxima is not comparable to this one.
	NormsGeneration int
	RubricVersion   string
}

// ScoreKind names the three snapshot families.
type ScoreKind string

// The three score families a snapshot may record.
const (
	ScoreSkill      ScoreKind = "skill"
	ScoreOverall    ScoreKind = "overall"
	ScoreGeneralist ScoreKind = "generalist"
)

// ScoreSnapshot is a point-in-time record, kept across rubric sweeps. A sweep
// supersedes scores; it does not erase the history of what was once true.
type ScoreSnapshot struct {
	UserID        UserID
	Kind          ScoreKind
	SkillID       *SkillID
	Score         float64
	RubricVersion string
	TakenAt       time.Time
}

// GlobalNorms are the shared maxima every arithmetic signal is measured
// against. Fixed per generation so a score is reproducible.
type GlobalNorms struct {
	Metric           string
	Generation       int
	MaxValue         float64
	MaxSource        string
	ObservationCount int
	Quantiles        []float64
	LastRecomputedAt time.Time
}

// --- re-evaluation -----------------------------------------------------------

// ReevaluationStatus tracks a dispute.
type ReevaluationStatus string

// The states of a re-evaluation dispute.
const (
	ReevalPending  ReevaluationStatus = "pending"
	ReevalAccepted ReevaluationStatus = "accepted"
	ReevalRejected ReevaluationStatus = "rejected"
)

// ReevaluationRequest is a contributor disputing a judgement.
//
// An ACCEPTED request neither resets nor increments the cooldown counter: a
// contributor who is repeatedly right is never throttled, and they are exactly
// the population whose disputes are worth most — each accepted one is a
// human-ranked disagreement on real evidence, which is the labelled set
// calibration said could not exist before launch.
type ReevaluationRequest struct {
	ID         RequestID
	ClaimID    ClaimID
	UserID     UserID
	Reason     string
	Status     ReevaluationStatus
	ReviewedBy *AdminID
	ReviewedAt *time.Time
	Decision   string
	CreatedAt  time.Time
}

// Cooldown is the escalating throttle on rejected disputes: 28, 56, 112, 224
// days, capped at 365. Doubling costs an honest contributor who misjudged once
// almost nothing, while making systematic disputing progressively pointless.
// The cap is deliberate — past about a year a cooldown is a ban, and a ban
// should be an admin decision with a reason (ADR-0007 §6).
type Cooldown struct {
	UserID         UserID
	RejectionCount int
	Tier           int
	CooldownUntil  *time.Time
}

// CanRequest reports whether a new dispute is permitted right now.
func (c *Cooldown) CanRequest(now time.Time) bool {
	return c == nil || c.CooldownUntil == nil || !c.CooldownUntil.After(now)
}

// RejectionsBeforeCooldown is how many rejected disputes trigger the next
// cooldown tier (ADR-0007 §6).
const RejectionsBeforeCooldown = 3

// MaxCooldownDays caps the escalation. Past about a year a cooldown is a ban,
// and a ban should be an admin decision with a reason rather than a counter
// reaching a large number.
const MaxCooldownDays = 365

// RecordRejection advances the cooldown after a rejected dispute.
//
// Three rejections trigger a tier; the counter then resets and the TIER does
// not. So the fourth rejection starts a fresh count toward a longer cooldown,
// which is what makes the escalation 28 → 56 → 112 → 224 → 365 rather than a
// single threshold anyone can sit just under.
//
// Acceptance never calls this. A contributor who is repeatedly right is never
// throttled, and they are the population whose disputes are worth most.
func (c *Cooldown) RecordRejection(now time.Time) {
	c.RejectionCount++
	if c.RejectionCount < RejectionsBeforeCooldown {
		return
	}
	c.Tier++
	c.CooldownUntil = ptr(now.Add(CooldownFor(c.Tier)))
	c.RejectionCount = 0
}

// CooldownFor returns the wait at a given tier: 28, 56, 112, 224, then 365
// forever. Doubling costs an honest contributor who misjudged once almost
// nothing, while making systematic disputing progressively pointless.
func CooldownFor(tier int) time.Duration {
	if tier <= 0 {
		return 0
	}
	days := 28 * (1 << (tier - 1))
	if days > MaxCooldownDays {
		days = MaxCooldownDays
	}
	return time.Duration(days) * 24 * time.Hour
}

func ptr[T any](v T) *T { return &v }
