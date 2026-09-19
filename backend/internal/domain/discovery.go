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
	Skills             []string             `json:"skills,omitempty"`
	MinSkillScore      *float64             `json:"min_skill_score,omitempty"`
	MinOverallScore    *float64             `json:"min_overall_score,omitempty"`
	MinGeneralistScore *float64             `json:"min_generalist_score,omitempty"`
	Availability       []AvailabilityStatus `json:"availability,omitempty"`
	Query              string               `json:"q,omitempty"`

	// What a contributor said about themselves (ADR-0018), as opposed to what
	// their evidence says. Everything here is opt-in, and an unstated value
	// therefore does NOT clear a stated minimum: a filter that quietly matched
	// people with no figure would be a filter that does nothing, and the
	// contributor's remedy is the profile prompt that already exists.
	//
	// NOTHING HERE TOUCHES COMPENSATION, and nothing ever may. A hirer who
	// could filter on pay would learn an upper bound across a few searches,
	// and an expectation would stop being a floor and become a ceiling
	// (ADR-0018 §5).

	// MinOfficeYOE is years in a job. SELF-REPORTED, and presented as such.
	MinOfficeYOE *int `json:"min_office_yoe,omitempty"`

	// MinOSSYEO is years contributing, VERIFIED and dated from when the
	// contributor's first pull request was authored (ADR-0019 §7). Unknown
	// never clears it — the whole value of a checked number is that it cannot
	// be cleared by an unreadable link.
	MinOSSYOE *int `json:"min_oss_yoe,omitempty"`

	// Countries is ISO 3166-1 alpha-2, matched against where the contributor
	// says they are. OR, not AND: a role open to three countries wants anybody
	// in any of them.
	Countries []string `json:"countries,omitempty"`

	// ForRole excludes everybody already on a round for that job, and is the
	// only filter here that is about the HIRER'S own history rather than
	// about the contributor (ADR-0008 amendment 3).
	//
	// It must be one of the caller's own organisation's roles. Not because
	// another company's shortlist could be read through it — it could not —
	// but because the count of who was excluded would say how many people
	// matching this query that company has already approached.
	ForRole *RoleID `json:"for_role,omitempty"`

	// OpenTo are the shapes of work a contributor ticked: remote, onsite,
	// contract, internship. OR again, and composed with availability exactly
	// as ADR-0018 §4 requires — somebody is available for the shapes they
	// enabled and for nothing else.
	OpenTo []string `json:"open_to,omitempty"`

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

	// AlreadyShortlisted is how many matches were dropped for being on a
	// round for this role already, and is set only when for_role was asked
	// for.
	//
	// Reported rather than silently subtracted, for the reason ADR-0008 gives
	// about inactive contributors: a list that shrinks with no account of why
	// makes a hirer doubt the filter rather than read the result.
	AlreadyShortlisted int

	RankedBy RankedBy
	Page     int
	PerPage  int
	Results  []SearchResult
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
//
// Dimensions carries the per-dimension score and the model's remark for each.
// evaluation/dimensions.md is explicit that these are for the hirer as much as
// the contributor — "a hirer reading the scorecard is buying exactly that
// specificity" — and a bare number without the reasoning is the assertion this
// platform exists to replace.
type ScorecardEvidence struct {
	Repo       string
	URL        string
	PRNumber   int
	Title      string
	MergedAt   time.Time
	Score      float64
	Dimensions map[string]DimensionVerdict
}

// DimensionVerdict is one judged dimension: what it scored and why.
type DimensionVerdict struct {
	Score  int    `json:"score"`
	Remark string `json:"remark"`
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

	// How long the availability window has been lapsed. Nil while it is live —
	// the question only exists once somebody has gone quiet.
	InactiveForDays *int

	Overall    RankPosition
	Generalist RankPosition
	Skills     []SkillRank
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

// HirerRef names the seat that authored something.
//
// A bare HirerID is not enough since ADR-0016 §9: a round from two years ago
// may name a seat that has since been revoked, and a uuid tells the reader
// nothing about who it was. The name travels WITH the row, resolved by the
// repository, so reading a round never means fetching its author separately.
//
// Active is false for a revoked seat. It is reported rather than hidden: the
// person did the work, and erasing them from their own rounds would be a
// falsification, not a tidy-up.
type HirerRef struct {
	ID          HirerID
	Username    string
	DisplayName string
	Active      bool
}

// RefTo names a seat for attribution.
//
// Active follows DisabledAt rather than being passed in, so a caller cannot
// record a revoked seat as live by forgetting a flag.
func RefTo(h *Hirer) HirerRef {
	if h == nil {
		return HirerRef{}
	}
	return HirerRef{
		ID: h.ID, Username: h.Username, DisplayName: h.DisplayName,
		Active: h.DisabledAt == nil,
	}
}

// Shortlist is a hiring round, owned by the ORGANIZATION rather than the
// recruiter who made it — one that vanished when a recruiter left the company
// would be worse than useless.
type Shortlist struct {
	ID             ShortlistID
	OrganizationID OrganizationID

	// RoleID is the job this round is for (ADR-0019 §3). One shortlist, one
	// role: a hirer wanting the same contributor for a second opening opens a
	// second shortlist, which raises a second, separate contact request.
	//
	// Required. Without it a contact request can only say "somebody is
	// interested in you", and the role details have no route to the person
	// being contacted.
	RoleID RoleID

	Name                string
	Description         string
	Status              ShortlistStatus
	TentativeResultDate time.Time
	CreatedBy           HirerRef
	CreatedAt           time.Time
	UpdatedAt           time.Time
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

	// Resolved from the contributor, not stored on the entry. A hirer reading
	// a round is looking at people; a column of uuids would send them to fetch
	// each one.
	DisplayName string
	GitHubLogin string

	// ContactStatus is the contributor's answer, empty until the round is
	// confirmed and a request exists to answer. Email is released ONLY on
	// acceptance (ADR-0005) — staging discloses nothing.
	ContactStatus string
	Email         string

	Note       string
	AddedBy    HirerRef
	NotifiedAt *time.Time
	AddedAt    time.Time
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
	RequestedBy    HirerRef
	Status         ContactRequestStatus

	// Copied from the shortlist at confirm time, so the record of what the
	// contributor was told survives a later edit.
	TentativeResultDate time.Time

	// RoleID is the job, read through the shortlist this request came from
	// (ADR-0019 §3).
	RoleID RoleID

	// Role is the opening itself, hydrated for the CONTRIBUTOR'S view.
	//
	// This is what turns "somebody is interested in you" into "somebody is
	// interested in you, for this, at this salary, in these countries". The
	// platform sends it; the contributor's address is still released only on
	// acceptance, so nothing about the consent model moves (ADR-0019 §2).
	//
	// An OPEN ROLE IS IMMUTABLE, which is what makes this trustworthy: the
	// request points at the row it was raised against, so a company revising
	// its salary today cannot change what somebody agreed to talk about last
	// week.
	Role *Role

	// The organization, resolved. A contributor decides about a COMPANY, not
	// about the recruiter who clicked (ADR-0005), so the name and whether the
	// company's payment is verified travel with the request — the second is
	// the disclosure ADR-0002 §5 requires them to see before answering.
	OrganizationName     string
	OrganizationVerified bool
	PaymentVerified      bool

	// The contributor, resolved for the hirer's view. Email is released ONLY
	// on acceptance (ADR-0005): a hirer sees a name and a status until the
	// contributor says yes, which is the whole consent model.
	DisplayName string
	GitHubLogin string
	Email       string

	RespondedAt     *time.Time
	EmailReleasedAt *time.Time
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

// SavedSearch is a stored filter set, replayed AS THE CALLER. It stores a
// question, never an answer.
type SavedSearch struct {
	ID             SavedSearchID
	OrganizationID OrganizationID
	Name           string
	Filters        SearchQuery
	CreatedBy      HirerRef
	CreatedAt      time.Time
}
