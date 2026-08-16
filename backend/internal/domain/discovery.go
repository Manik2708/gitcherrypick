package domain

import "time"

// Discovery: search, ranking, shortlists and contact requests (ADR-0005,
// ADR-0008).

// SearchQuery is the parsed filter set.
//
// The json tags are the QUERY PARAMETER names, and saved_searches.filters
// stores exactly them (ADR-0008 §1, §5). One representation end to end: a
// saved search replays as a query string with no translation layer, and a
// renamed parameter cannot silently diverge from what was stored.
type SearchQuery struct {
	Skills               []string             `json:"skills,omitempty"`
	MinSkillScore        *float64             `json:"min_skill_score,omitempty"`
	MinOverallScore      *float64             `json:"min_overall_score,omitempty"`
	MinGeneralistScore   *float64             `json:"min_generalist_score,omitempty"`
	Availability         []AvailabilityStatus `json:"availability,omitempty"`
	EvidenceWithinMonths *int                 `json:"evidence_within_months,omitempty"`
	Query                string               `json:"q,omitempty"`

	// Default false. Reveals contributors whose availability window lapsed.
	// It does NOT reveal not_looking, which no toggle reveals (ADR-0008 §1a).
	IncludeInactive bool `json:"include_inactive,omitempty"`

	Page    int `json:"page,omitempty"`
	PerPage int `json:"per_page,omitempty"`
}

// RankedBy names the ordering a result set was produced under.
//
// A rank is meaningless without saying rank in what: a multi-skill query has no
// single skill score to order by, so the response says which order it used
// rather than leaving `rank` to mean different things in different responses.
type RankedBy string

// The orderings a result set may be produced under. Skill orderings are built
// by RankedBySkill.
const (
	RankedByOverall    RankedBy = "overall"
	RankedByGeneralist RankedBy = "generalist"
	// Skill orderings are "skill:<slug>"; use RankedBySkill to build one.
)

// RankedBySkill builds the ordering name for a single-skill query.
func RankedBySkill(slug string) RankedBy { return RankedBy("skill:" + slug) }

// SearchResults is one page of a filtered global ranking.
//
// Rank is computed over the UNFILTERED scored population and the view filters
// it, so Results may have gaps in the rank sequence. InactiveHidden is how many
// the filter removed, returned so a gap reads as a filter rather than a bug.
type SearchResults struct {
	Total          int
	InactiveHidden int
	RankedBy       RankedBy
	Page           int
	PerPage        int
	Results        []SearchResult
}

// SearchResult is one contributor as a hirer sees them.
type SearchResult struct {
	Rank            int
	UserID          UserID
	DisplayName     string
	GitHubLogin     string
	Active          bool
	Availability    *Availability
	Skills          []ResultSkill
	OverallScore    *float64
	GeneralistScore *float64

	// Populated only when Active is false. Two numbers because they answer
	// different questions and differ by the 15-day window: how stale the signal
	// is, and how long they have been gone.
	LastConfirmedAt *time.Time
	InactiveForDays *int
}

// ResultSkill is a skill on a search result or scorecard. Rank is nil for a
// secondary standing: an unranked skill has no position, and reporting one
// would contradict the standing rule.
type ResultSkill struct {
	Slug     string
	Name     string
	Standing Standing
	Score    float64
	Rank     *int
}

// Scorecard is the full evidence dossier a verified hirer reads.
//
// Secondary skills appear here, unranked. Standing gates SEARCHABILITY, not
// disclosure — a hirer already looking at someone should see everything that
// person evidenced (ADR-0008 §2).
type Scorecard struct {
	User          SearchResult
	Skills        []ScorecardSkill
	RubricVersion string

	// Released only after a contact request is accepted.
	Email *string
}

// ScorecardSkill carries the evidence behind a standing.
type ScorecardSkill struct {
	ResultSkill
	DistinctPRCount int
	Evidence        []ScorecardEvidence
}

// ScorecardEvidence is one scored PR.
type ScorecardEvidence struct {
	Repo     string
	PRNumber int
	Title    string
	MergedAt time.Time
	Score    float64
}

// LeaderboardKind selects which board to read.
type LeaderboardKind string

// The three leaderboards.
const (
	BoardOverall    LeaderboardKind = "overall"
	BoardGeneralist LeaderboardKind = "generalist"
	BoardSkill      LeaderboardKind = "skill"
)

// Leaderboard is the definitive global ranking. It always shows everyone,
// inactive included, and takes no include_inactive — search is the actionable
// subset of it, and this is where a hirer finds who fills a gap (ADR-0008 §1).
type Leaderboard struct {
	Kind          LeaderboardKind
	Skill         *Skill
	RubricVersion string
	Entries       []LeaderboardEntry
}

// LeaderboardEntry is one position. Tie-breakers order these but are NEVER
// serialized — publishing them would turn them into optimisation targets, and
// each is a weaker signal than the score (ADR-0005).
type LeaderboardEntry struct {
	Rank        int
	UserID      UserID
	DisplayName string
	GitHubLogin string
	Score       float64
	Active      bool
}

// Rank is a contributor's own position. They see only their own (ADR-0005), so
// this carries positions and totals and nothing identifying anyone else.
type Rank struct {
	RubricVersion  string
	Ranked         bool
	UnrankedReason string
	Active         bool
	Overall        RankPosition
	Generalist     RankPosition
	Skills         []SkillRank
}

// RankPosition is a score and where it sits. Rank is nil when the contributor
// is not in the ranked population — which, per ADR-0008 §1a, means they opted
// out rather than merely lapsed.
type RankPosition struct {
	Score *float64
	Rank  *int
	OutOf int
}

// SkillRank is one skill's position.
type SkillRank struct {
	Slug     string
	Name     string
	Standing Standing
	Score    float64
	Rank     *int
	OutOf    *int
}

// --- shortlists --------------------------------------------------------------

// ShortlistStatus tracks a hiring round.
//
// draft means nothing has been disclosed to anyone and every entry is still
// removable. open means at least one confirm has happened and those
// disclosures are permanent (ADR-0008 §3a).
type ShortlistStatus string

// The states of a hiring round. draft has disclosed nothing to anyone.
const (
	ShortlistDraft  ShortlistStatus = "draft"
	ShortlistOpen   ShortlistStatus = "open"
	ShortlistClosed ShortlistStatus = "closed"
)

// Shortlist is a hiring round, owned by the ORGANIZATION rather than the
// recruiter who made it — one that vanished when a recruiter left the company
// would be worse than useless.
type Shortlist struct {
	ID                  ShortlistID
	OrganizationID      OrganizationID
	Name                string
	Description         string
	Status              ShortlistStatus
	TentativeResultDate time.Time
	CreatedBy           HirerID
	FirstConfirmedAt    *time.Time
	ClosedAt            *time.Time
	Entries             []ShortlistEntry
}

// ShortlistEntry is one staged or notified candidate.
//
// NotifiedAt is the boundary the whole two-phase design turns on: nil means
// still private and removable, set means the contributor was told and the entry
// is permanent. An endpoint that erased the record of a disclosure that already
// happened would be pretending otherwise.
type ShortlistEntry struct {
	ShortlistID ShortlistID
	UserID      UserID
	Note        string
	AddedBy     HirerID
	NotifiedAt  *time.Time
	AddedAt     time.Time
}

// Removable reports whether this entry may still be deleted.
func (e *ShortlistEntry) Removable() bool { return e != nil && e.NotifiedAt == nil }

// ContactRequestStatus is a contributor's answer.
type ContactRequestStatus string

// A contributor's answer to a contact request.
const (
	ContactPending  ContactRequestStatus = "pending"
	ContactAccepted ContactRequestStatus = "accepted"
	ContactDeclined ContactRequestStatus = "declined"
	ContactExpired  ContactRequestStatus = "expired"
)

// ContactRequest is what a confirm creates. The contributor sees the
// organization, the date, and the payment disclosure; the hirer sees a status
// and, only after acceptance, an email.
type ContactRequest struct {
	ID             ContactID
	ShortlistID    ShortlistID
	UserID         UserID
	OrganizationID OrganizationID
	RequestedBy    HirerID
	Status         ContactRequestStatus

	// Copied from the shortlist at confirm time, so the record of what the
	// contributor was told survives a later edit.
	TentativeResultDate time.Time

	RespondedAt     *time.Time
	EmailReleasedAt *time.Time
	ExpiresAt       time.Time
}

// SavedSearch is a stored filter set, replayed AS THE CALLER. It stores a
// question, never an answer.
type SavedSearch struct {
	ID             SavedSearchID
	OrganizationID OrganizationID
	Name           string
	Filters        SearchQuery
	CreatedBy      HirerID
}
