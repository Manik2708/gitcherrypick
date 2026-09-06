package domain

import "time"

// A claim is an evidence bundle: up to five merged PRs, up to twenty optional
// projects, and one or more skills those PRs demonstrate. One claim is judged
// for every declared skill in ONE model call (ADR-0003).

// ClaimStatus tracks a claim through validation and evaluation.
type ClaimStatus string

// The states a claim moves through, from draft to evaluated or withdrawn.
const (
	ClaimDraft      ClaimStatus = "draft"
	ClaimValidating ClaimStatus = "validating"
	ClaimInvalid    ClaimStatus = "invalid"
	ClaimQueued     ClaimStatus = "queued"
	ClaimEvaluating ClaimStatus = "evaluating"
	ClaimEvaluated  ClaimStatus = "evaluated"
	ClaimFailed     ClaimStatus = "failed"
	ClaimWithdrawn  ClaimStatus = "withdrawn"
)

// Claim is one submission.
type Claim struct {
	ID      ClaimID
	UserID  UserID
	Status  ClaimStatus
	Version int

	PREvidence      []PREvidence
	ProjectEvidence []ProjectEvidence
	Skills          []ClaimSkill

	SubmittedAt *time.Time
	EvaluatedAt *time.Time
	WithdrawnAt *time.Time

	// evaluated_at + 7 days. Blocks edit and resubmit of THIS claim only; other
	// skills stay freely claimable (ADR-0003). The lock exists to stop
	// score-rerolling, which is both a gaming vector and a direct cost.
	LockedUntil *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// LockWindow is how long a scored claim is closed to edits (ADR-0003).
//
// The lock stops score-rerolling — resubmitting slightly different evidence
// until the number improves — which is both a gaming vector and a direct cost,
// since every submission is a model call.
//
// In domain because the evaluator sets it and the claim repository enforces
// it, and two constants that must agree are two that will eventually disagree.
const LockWindow = 7 * 24 * time.Hour

// IsLocked reports whether the seven-day lock is in force.
func (c *Claim) IsLocked(now time.Time) bool {
	return c != nil && c.LockedUntil != nil && c.LockedUntil.After(now)
}

// EvidenceRole is how the claimant participated. A pr-review claim inverts the
// usual test: the claimant must have REVIEWED the PR and must not be its author
// (ADR-0003).
type EvidenceRole string

// How a claimant participated in the pull request they submitted.
const (
	RoleAuthor   EvidenceRole = "author"
	RoleReviewer EvidenceRole = "reviewer"
)

// PREvidence is one pull request offered as evidence.
type PREvidence struct {
	Position int

	// RawURL is what the contributor pasted. Kept because a URL that did not
	// parse has no owner, name or number to reconstruct one from, and the
	// draft still has to show them what they typed.
	RawURL string

	RepoOwner string
	RepoName  string
	PRNumber  int
	Role      EvidenceRole

	// Facts cached from GitHub at enrichment. Nil before enrichment runs.
	Facts *PRFacts

	// Set when validation rejects this specific item. A claim that fails
	// validation is rejected NAMING the failing item, never generically — the
	// contributor is doing curation work and a vague rejection wastes it.
	InvalidReason *EvidenceInvalidReason

	// What the model made of this PR, one entry per skill it was judged
	// against. Empty until the claim has been evaluated.
	//
	// Per PR per skill is the finest of the three score families (ADR-0007),
	// and it is the one that explains the other two: a contributor asking why
	// a skill scored what it did is asking about these.
	Scores []PRScore

	// The model's per-dimension reasoning, and — when the PR counted toward
	// nothing — why. A score with no reasoning behind it cannot be disputed,
	// and the dispute flow is what produces the labelled set (ADR-0007 §4).
	Dimensions map[string]Dimension

	Rejected        bool
	RejectionReason string
}

// PRScore is what one skill scored on one PR, as a claim read reports it.
//
// Named by slug and carrying only the number: the components behind it live on
// PRSkillScore, which is the evaluator's record. A contributor reading their
// claim wants to see which skill got what, not the arithmetic.
type PRScore struct {
	Skill string
	Score float64
}

// EvidenceInvalidReason is why one evidence row failed validation.
type EvidenceInvalidReason string

// Why one evidence row failed validation. The last two apply to pr-review
// claims only, where the test is inverted.
const (
	MalformedURL          EvidenceInvalidReason = "malformed_url"
	NotFound              EvidenceInvalidReason = "not_found"
	NotPublic             EvidenceInvalidReason = "not_public"
	NotMerged             EvidenceInvalidReason = "not_merged"
	NotAuthoredByClaimant EvidenceInvalidReason = "not_authored_by_claimant"
	DuplicateInClaim      EvidenceInvalidReason = "duplicate_in_claim"

	// DuplicatePair is the same (PR, skill) already spent on ANOTHER claim.
	// Distinct from DuplicateInClaim, which is the same PR twice in this one:
	// the fix differs, and so does whose claim holds the evidence.
	DuplicatePair         EvidenceInvalidReason = "duplicate_pair"
	RepositoryUnavailable EvidenceInvalidReason = "repository_unavailable"
	GitHubError           EvidenceInvalidReason = "github_error"
	NotReviewedByClaimant EvidenceInvalidReason = "not_reviewed_by_claimant"
	AuthoredByClaimant    EvidenceInvalidReason = "authored_by_claimant"
)

// PRFacts are the GitHub facts a judgement and the arithmetic signals need.
type PRFacts struct {
	Title          string
	Merged         bool
	MergedAt       *time.Time
	AuthorUserID   int64
	Additions      int
	Deletions      int
	ChangedFiles   int
	ReviewComments int
	Reviews        int
	Participants   int
	Repository     RepoFacts
}

// RepoFacts are properties of the repository rather than the change. Project
// reach is computed from these (ADR-0007 §7).
type RepoFacts struct {
	Public bool
	Stars  int
	Forks  int

	// Contributors carries 0.20 of the reach weight (ADR-0005). Missing it
	// does not zero the term — the remaining weights renormalise — but it
	// silently lowers every score in a repository that has one.
	Contributors int

	Dependents int
	Downloads  int
	IsFork     bool
}

// ProjectEvidence is an optional supporting project. A pr-review claim refuses
// them: reviewing is evidenced by review threads, not by owning a repository.
type ProjectEvidence struct {
	RepoOwner           string
	RepoName            string
	ContributionSummary string
	Facts               *RepoFacts

	// Maintainer is the contributor's declaration of maintainer status, nil
	// when they made none.
	Maintainer *MaintainerDeclaration
}

// MaintainerSource is how a contributor evidences maintainer status.
type MaintainerSource string

// The declarable sources. GitHub exposes no maintainer list, so the
// contributor names where the truth lives and the model checks it (ADR-0003).
const (
	SourceCodeowners         MaintainerSource = "codeowners"
	SourceMergesOthersPRs    MaintainerSource = "merges_others_prs"
	SourceReadmeOrGovernance MaintainerSource = "readme_or_governance"
	SourceOrgOwner           MaintainerSource = "org_owner"
	SourceOtherEvidence      MaintainerSource = "other"
)

// ValidMaintainerSource reports whether a declared source is one of the five.
//
// Checked at the edge rather than left to the database: an unknown value is a
// contributor's typo, and a 422 naming it is more use than an enum violation.
func ValidMaintainerSource(s MaintainerSource) bool {
	switch s {
	case SourceCodeowners, SourceMergesOthersPRs, SourceReadmeOrGovernance,
		SourceOrgOwner, SourceOtherEvidence:
		return true
	}
	return false
}

// MaintainerDeclaration is a contributor's claim to maintainer status on a
// supporting project.
//
// It is a CLAIM, not a fact. Validated is nil until the model rules on it, and
// false means the declaration was not supported — in which case the maintainer
// bonus (Reach × 1.25, ADR-0005) is not applied. Treating the declaration
// itself as the bonus would make project reach self-asserted, which is exactly
// what this platform exists not to do.
type MaintainerDeclaration struct {
	Sources       []MaintainerSource
	OtherEvidence string

	Validated       *bool
	ValidatedAt     *time.Time
	ValidationNotes string
}

// IsValidated reports a declaration the model upheld — the only state in which
// the maintainer bonus applies.
func (m *MaintainerDeclaration) IsValidated() bool {
	return m != nil && m.Validated != nil && *m.Validated
}

// ClaimSkillOrigin records how a skill came to be on a claim.
type ClaimSkillOrigin string

// How a skill came to be attached to a claim.
const (
	UserDeclared ClaimSkillOrigin = "user_declared"
	AISuggested  ClaimSkillOrigin = "ai_suggested"
)

// ClaimSkill is one skill attached to a claim.
//
// An ai_suggested row is INERT until accepted: invisible, unscored, and
// excluded from every total. The model suggests; only the contributor promotes
// (ADR-0003 §10).
type ClaimSkill struct {
	SkillID            SkillID
	Slug               string
	Name               string
	Origin             ClaimSkillOrigin
	IsNominatedPrimary bool
	Rationale          string

	// ScoringMode is copied from the catalogue entry when the slug resolves.
	//
	// Carried here rather than looked up again at serialization time: it
	// decides which formula scores this pair, and a response that reported one
	// mode while the evaluator used another would be worse than not reporting
	// it (ADR-0007).
	ScoringMode ScoringMode

	AcceptedAt  *time.Time
	DismissedAt *time.Time

	// Set when the model returns zero for this skill: dropped, never stored as
	// a zero score (ADR-0003).
	RejectedAt      *time.Time
	RejectionReason *RejectionReason

	// The contributor's standing in this skill overall, not on this claim.
	//
	// A claim is evidence toward a skill, and what a contributor wants to know
	// on reading one back is what it moved. Score is nil until the skill has
	// been scored at all.
	Score           *float64
	Standing        Standing
	DistinctPRCount int
}

// IsInert reports whether a suggestion has yet to be decided.
func (cs *ClaimSkill) IsInert() bool {
	return cs.Origin == AISuggested && cs.AcceptedAt == nil && cs.DismissedAt == nil
}

// --- skills and standing -----------------------------------------------------

// Skill is a catalogue entry. Skills are curated, never free text: otherwise
// the population claiming "Go" ranks separately from the one claiming
// "Golang", and anyone can mint a skill in which they are trivially top-ranked.
type Skill struct {
	ID          SkillID
	Slug        string
	Name        string
	Description string
	Category    string
	ScoringMode ScoringMode
	Aliases     []string
	IsActive    bool
}

// ScoringMode selects the rubric variant. pr-review is judged entirely by the
// model reading review threads, with no PR-specific arithmetic (ADR-0007).
type ScoringMode string

// The rubric variants, matching skill_scoring_mode in RFC-0003's schema.
//
// judged_only drops the PR-specific arithmetic entirely and leaves the score to
// the model reading review threads — the pr-review skill's mode (ADR-0007).
const (
	ScoringStandard   ScoringMode = "standard"
	ScoringJudgedOnly ScoringMode = "judged_only"
)

// Standing is derived from the count of DISTINCT SCORED PRs, never declared.
// Five or more is primary and ranked; one to four is secondary, visible and
// score-contributing but never ranked. Promotion at the fifth is automatic.
type Standing string

// PrimaryThreshold is the number of DISTINCT SCORED PRs at which a skill
// promotes (ADR-0003).
//
// It lives in domain because the repository's recompute and the service's
// withdrawal preview both need it, and two constants that must agree are two
// constants that will eventually disagree.
const PrimaryThreshold = 5

// The two standings. Five or more distinct scored PRs is primary and ranked;
// one to four is secondary and never ranked.
const (
	Primary   Standing = "primary"
	Secondary Standing = "secondary"
)

// UserSkill is a contributor's standing in one skill.
//
// Score and standing are ORTHOGONAL: a secondary skill may outscore a primary
// one and stays unranked. That is the sharpest statement of what standing
// means — it gates searchability, not quality.
type UserSkill struct {
	UserID           UserID
	SkillID          SkillID
	Slug             string
	Name             string
	Standing         Standing
	DistinctPRCount  int
	Score            float64
	PRComponent      float64
	ProjectComponent float64
	PromotedAt       *time.Time
	RubricVersion    string
}

// SuggestionDecision is what accepting or dismissing a suggestion produced.
//
// Both outcomes report the skill and the stamp that closed it. Only
// acceptance reports standing, because only acceptance creates one — a
// dismissed suggestion never became a skill and has nothing to stand on.
type SuggestionDecision struct {
	Skill ClaimSkill

	// Standing is nil on dismissal.
	Standing *UserSkill
}

// PRLinkStatus tracks one (user, PR, skill) triple. Uniqueness on that triple
// is what stops the same PR evidencing the same skill twice (ADR-0007).
type PRLinkStatus string

// The states of one (user, PR, skill) triple.
const (
	LinkPending  PRLinkStatus = "pending"
	LinkScored   PRLinkStatus = "scored"
	LinkRejected PRLinkStatus = "rejected"
)
