package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RoleID identifies one opening.
type RoleID string

// Engagement is what kind of work a role is (ADR-0019 §4).
//
// An enum, unlike a contributor's OpenTo* flags, and the asymmetry is the
// point: a person will consider several kinds of work at once; an opening is
// one kind of work.
type Engagement string

// The four from the source diagram.
const (
	EngagementFullTime   Engagement = "full_time"
	EngagementContract   Engagement = "contract"
	EngagementInternship Engagement = "internship"
	EngagementFreelance  Engagement = "freelance"
)

// Valid reports whether e is one of the four.
func (e Engagement) Valid() bool {
	switch e {
	case EngagementFullTime, EngagementContract, EngagementInternship, EngagementFreelance:
		return true
	}
	return false
}

// Shape returns the work-preference flag this engagement matches.
//
// This is the join between a role and ADR-0018 §4: a contributor is available
// for the shapes they ticked and for nothing else, so an engagement has to say
// which tick it needs. Freelance is the exception — it lives on
// `availability_status` rather than on a flag, deliberately, because two
// controls that both mean "freelance" can disagree.
func (e Engagement) Shape(w *WorkPreferences) bool {
	if w == nil {
		return false
	}
	switch e {
	case EngagementFullTime:
		// A permanent role is remote or onsite; either tick admits it.
		return w.OpenToRemote || w.OpenToOnsite
	case EngagementContract:
		return w.OpenToContract
	case EngagementInternship:
		return w.OpenToInternships
	case EngagementFreelance:
		// Answered by availability, not by a flag (ADR-0018 §2).
		return true
	}
	return false
}

// Label is this engagement as a person reads it.
//
// Here rather than in a template because the notifier and every client need the
// same words, and an enum value rendered raw — "full_time" — reads as a leak of
// the database into somebody's inbox.
func (e Engagement) Label() string {
	switch e {
	case EngagementFullTime:
		return "Full time"
	case EngagementContract:
		return "Contract"
	case EngagementInternship:
		return "Internship"
	case EngagementFreelance:
		return "Freelance"
	}
	return string(e)
}

// RoleLocation is whether the work happens anywhere or somewhere.
type RoleLocation string

// The two placements.
const (
	LocationRemote  RoleLocation = "remote"
	LocationAddress RoleLocation = "address"
)

// Valid reports whether l is one of the two.
func (l RoleLocation) Valid() bool {
	return l == LocationRemote || l == LocationAddress
}

// Label is this placement as a person reads it.
func (l RoleLocation) Label() string {
	if l == LocationRemote {
		return "remote"
	}
	return "at their office"
}

// RoleStatus is where a role is in its life.
type RoleStatus string

// The three states. A draft matches nobody; a closed role is still the thing a
// live contact request refers to, which is why closing is a state and not a
// delete.
const (
	RoleDraft  RoleStatus = "draft"
	RoleOpen   RoleStatus = "open"
	RoleClosed RoleStatus = "closed"
)

// CloseReason is why a role stopped being open (ADR-0019 §14).
type CloseReason string

// The reasons a hirer may give, and the one only the system writes.
const (
	// CloseNotNeeded — the headcount went away.
	CloseNotNeeded CloseReason = "not_needed"

	// CloseHiredElsewhere — filled, by somebody found some other way. Not a
	// consolation answer: a company that keeps filling roles from elsewhere is
	// telling us something specific, and only tells us honestly if the answer
	// is as easy to give as the flattering one.
	CloseHiredElsewhere CloseReason = "hired_elsewhere"

	// CloseHiredViaPlatform — filled, by one or more people contacted through
	// here. THE ONLY OUTCOME SIGNAL THIS PLATFORM HAS. Everything else measures
	// activity; this measures whether any of it worked.
	CloseHiredViaPlatform CloseReason = "hired_via_platform"

	// CloseSuperseded is written by the system when a revision replaces a role
	// (ADR-0019 §13). Offered to nobody: it exists so that "every closed role
	// says why" holds without exception rather than nearly.
	CloseSuperseded CloseReason = "superseded"

	// CloseOther — something else, and the note says what.
	CloseOther CloseReason = "other"
)

// Choosable reports whether a hirer may give this reason.
//
// CloseSuperseded is the one that fails: a hirer claiming a role was replaced
// when nothing replaced it would leave a closed role that lies about why.
func (r CloseReason) Choosable() bool {
	switch r {
	case CloseNotNeeded, CloseHiredElsewhere, CloseHiredViaPlatform, CloseOther:
		return true
	}
	return false
}

// Role is one opening.
type Role struct {
	ID    RoleID
	OrgID OrganizationID

	Title       string
	Description string

	Engagement Engagement
	Status     RoleStatus

	Location RoleLocation
	// AddressID is set when Location is LocationAddress and nil otherwise. A
	// remote role must not carry a stale pointer to the office it used to be at.
	AddressID *AddressID

	// Currency is ISO 4217 and required whenever any amount is stated.
	Currency string

	// Salaried. Minor units, matching ADR-0018 §8 — the two numbers are
	// compared against a contributor's expectation and must be the same kind of
	// number. CTC is the whole package; Base is what arrives monthly.
	YearlyCTC  *int64
	YearlyBase *int64

	// Freelance.
	HourlyRate    *int64
	ExpectedHours *int

	// EligibleCountries is ISO 3166-1 alpha-2. EMPTY MEANS ANYWHERE: an
	// organisation that never considered work authorisation must not exclude
	// the world by leaving a field blank (ADR-0019 §6).
	EligibleCountries []string

	// Minimums. Nil means no minimum, which is not the same as zero — a role
	// open to anybody and a role explicitly open to people with no commercial
	// experience are different invitations.
	MinOfficeYOE *int
	MinOSSYOE    *int

	// The process fields (ADR-0017, ADR-0019 §8). Shown on the contact request
	// and nowhere else, to the contacted contributor and the organisation and
	// to nobody else.
	RequiresOnlineTest *bool
	MaxInterviewRounds *int
	AvgDaysToOffer     *int

	Questions []RoleQuestion

	CreatedBy HirerID
	CreatedAt time.Time
	UpdatedAt time.Time

	// OpenedBy and OpenedAt are stamped under EVERY authority policy. Who may
	// open a role is configurable; that whoever did is named is not — "who
	// approved this salary" is asked after something has gone wrong, when the
	// service meant to set it is not around to be asked.
	OpenedBy *HirerID
	OpenedAt *time.Time

	// Somebody asked for this role to be closed and is waiting for an owner.
	// The role is STILL OPEN meanwhile: a request is not an outcome.
	CloseRequestedBy *HirerID
	CloseRequestedAt *time.Time

	ClosedAt    *time.Time
	ClosedBy    *HirerID
	CloseReason *CloseReason
	CloseNote   string

	// Hires is one entry per person, because a role can fill several seats.
	Hires []RoleHire

	// Advertised is whether a LIVE public opening sits on this role
	// (ADR-0020). Carried on the role because a hirer looking at a list of
	// them has no other way to tell which ones a contributor can see, and
	// "is this public" is the first thing they will want to know.
	Advertised bool

	// SupersededBy / Supersedes chain a role to its revision. An open role is
	// immutable: a change writes a new row and closes this one, so a
	// contributor contacted last week still reads the role they were shown.
	Supersedes *RoleID
}

// IsOpen reports whether this role can currently match anybody.
func (r *Role) IsOpen() bool { return r != nil && r.Status == RoleOpen }

// HiresAnywhere reports whether the role places no country restriction.
func (r *Role) HiresAnywhere() bool { return r != nil && len(r.EligibleCountries) == 0 }

// EligibleIn reports whether the role can hire somebody in this country.
//
// FAILS OPEN in both directions that matter: a role with no stated countries
// admits everybody, and a contributor who has not said where they are is not
// excluded from a role that hires anywhere.
func (r *Role) EligibleIn(country string) bool {
	if r.HiresAnywhere() {
		return true
	}
	for _, c := range r.EligibleCountries {
		if c == country {
			return true
		}
	}
	return false
}

// RoleQuestion is one yes/no question a role asks (ADR-0019 §9).
//
// Closed questions only. A free-text question is an interview, and an interview
// conducted before either side has agreed to talk is exactly the asymmetry the
// consent model exists to prevent.
type RoleQuestion struct {
	ID       string
	RoleID   RoleID
	Question string
	Position int

	// Expected is the answer the organisation hopes for, recorded so an answer
	// can be REPORTED as matching. It never filters: a required answer would be
	// a hidden rejection from a question the contributor was never shown.
	Expected *bool
}

// RoleHire is one person a role was filled with.
type RoleHire struct {
	RoleID     RoleID
	UserID     UserID
	RecordedBy HirerID
	RecordedAt time.Time
}

// RoleAuthority is who in an organisation may perform one kind of change to a
// role (ADR-0019 §11).
type RoleAuthority string

// The three answers, read the same way whichever action they govern.
const (
	// AuthorityAnyHirer — anybody with a seat does it outright.
	AuthorityAnyHirer RoleAuthority = "any_hirer"

	// AuthorityDraftAndApprove — anybody stages it; an OWNER commits it.
	AuthorityDraftAndApprove RoleAuthority = "draft_and_approve"

	// AuthorityOwnersOnly — only owners may do it at all.
	AuthorityOwnersOnly RoleAuthority = "owners_only"
)

// Valid reports whether a is one of the three.
func (a RoleAuthority) Valid() bool {
	switch a {
	case AuthorityAnyHirer, AuthorityDraftAndApprove, AuthorityOwnersOnly:
		return true
	}
	return false
}

// OrgSettings is an organisation's policy, as opposed to its identity.
//
// A row of its own rather than columns on the organisation, because settings
// accumulate and each new one should not be another edit to the organisation
// record. NO ROW MEANS THE DEFAULTS, so nothing needs backfilling and an
// organisation that never opens the screen behaves exactly like one that opened
// it and changed nothing.
type OrgSettings struct {
	OrgID OrganizationID

	// Opening a role commits the company to a salary in writing, to everybody
	// contacted.
	RoleCreateAuthority RoleAuthority

	// Changing an open role changes what people were already told.
	RoleUpdateAuthority RoleAuthority

	// Closing only ever WITHDRAWS a commitment. Permissive by default, because
	// a rule making the safe direction harder than the unsafe one gets worked
	// around.
	RoleCloseAuthority RoleAuthority
}

// DefaultOrgSettings is what an organisation that has never chosen behaves as.
func DefaultOrgSettings(org OrganizationID) OrgSettings {
	return OrgSettings{
		OrgID:               org,
		RoleCreateAuthority: AuthorityDraftAndApprove,
		RoleUpdateAuthority: AuthorityDraftAndApprove,
		RoleCloseAuthority:  AuthorityAnyHirer,
	}
}

// Permits reports whether a hirer holding this seat may perform the action the
// authority governs.
//
// AuthorityDraftAndApprove permits the STAGING half to anybody — drafting a
// role, requesting a closure — and it is the COMMITTING half (Commits, below)
// that an owner holds. Splitting it here rather than at each call site is what
// keeps the two halves from drifting apart.
func (a RoleAuthority) Permits(isOwner bool) bool {
	switch a {
	case AuthorityAnyHirer, AuthorityDraftAndApprove:
		return true
	case AuthorityOwnersOnly:
		return isOwner
	}
	return false
}

// Commits reports whether this seat may turn a staged change into a commitment.
func (a RoleAuthority) Commits(isOwner bool) bool {
	switch a {
	case AuthorityAnyHirer:
		return true
	case AuthorityDraftAndApprove, AuthorityOwnersOnly:
		return isOwner
	}
	return false
}

// prURL matches a GitHub pull request link.
//
// Anchored, and tolerant of a trailing slash or query string — somebody pastes
// what their browser shows, which often carries a #discussion anchor.
var prURL = regexp.MustCompile(`^/([^/]+)/([^/]+)/pull/(\d+)/?$`)

// ParsePRURL splits a pull request link into the three things GitHub needs.
//
// Here rather than in a controller because two layers now need it: a claim
// parses evidence at the edge, and the profile service verifies a contributor's
// first pull request deep inside. Two copies of this regex would drift, and the
// symptom would be one of them accepting a URL the other refuses.
func ParsePRURL(raw string) (owner, name string, number int, err error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", 0, fmt.Errorf("unparseable url %q: %w", raw, err)
	}

	match := prURL.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", "", 0, fmt.Errorf("%q is not a pull request url", raw)
	}
	number, err = strconv.Atoi(match[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("%q has no pr number: %w", raw, err)
	}
	return match[1], match[2], number, nil
}

// OpeningID identifies one published advert.
type OpeningID string

// Opening is a role a company has made public, with a bar on it (ADR-0020).
//
// It sits ON a role rather than inside one because an open role is immutable
// (ADR-0019 §13): a bar living on the role could not be raised without
// superseding the job underneath and closing it under everybody already
// contacted.
//
// READING ONE SENDS NOTHING. There is no application, no interest, no view
// count — the consent model runs one way, and an opening only changes what a
// contributor can SEE (ADR-0020 §8).
type Opening struct {
	ID     OpeningID
	RoleID RoleID

	// The bar. Nil means no bar rather than a bar of zero: a role open to
	// anybody and one that has decided to accept the lowest score on the
	// platform are different invitations.
	//
	// A STATED BAR IS NOT CLEARED BY AN ABSENT SCORE. Somebody who has never
	// submitted a claim has no overall score, and showing them a role asking
	// for 60 would promise a match that does not exist.
	MinOverallScore    *float64
	MinGeneralistScore *float64

	// Years contributing, verified and dated from authorship (ADR-0019 §7).
	//
	// Its own field rather than a read of the role's, because the role's is
	// what the company considers when IT approaches and this is what it says
	// in public — and an open role cannot be edited, so a public bar has to be
	// changeable without revising the job.
	MinOSSYOE *int

	// Skills is the per-skill bar, matched at PRIMARY standing only, as search
	// is (ADR-0005). A bar cleared by a secondary skill would be cleared by
	// evidence the platform declines to rank.
	Skills []OpeningSkill

	PublishedAt *time.Time
	PublishedBy *HirerID
	WithdrawnAt *time.Time

	CreatedBy HirerID
	CreatedAt time.Time
	UpdatedAt time.Time

	// Role travels with it on the contributor's read. Nil on a hirer's, where
	// the caller already holds the role.
	Role *Role

	// OrganizationName is what a contributor sees instead of an id.
	OrganizationName string
}

// Live reports whether this opening is currently advertised.
//
// Published and not withdrawn. Whether its ROLE is still open is a separate
// check the query makes, because an opening cannot see the role from here.
func (o *Opening) Live() bool {
	return o != nil && o.PublishedAt != nil && o.WithdrawnAt == nil
}

// OpeningSkill is one skill and the score a contributor needs in it.
type OpeningSkill struct {
	SkillID SkillID

	// Slug and Name are resolved for display. A bar naming a uuid is a bar
	// nobody can read.
	Slug string
	Name string

	// MinScore is required rather than optional: a skill listed with no score
	// is just a skill, and this exists to say how good at it somebody has to
	// be. A role that only wants the skill names it with zero.
	MinScore float64
}

// RoleCandidate is somebody who has been put on a round for this role.
//
// The list a hirer reads to answer "who have I already approached for this
// job", and the list a closure picks its hires out of. One row per PERSON
// rather than per entry: the same contributor staged on two rounds for one
// role is one candidate who has been approached, not two.
type RoleCandidate struct {
	UserID      UserID
	DisplayName string
	GitHubLogin string

	// The round they are on. The first one, where somebody appears on
	// several — which is the one that reached them.
	ShortlistID   ShortlistID
	ShortlistName string

	// ContactStatus is empty while an entry is still STAGED. A staged entry
	// has told nobody anything and is removable; everything else has.
	ContactStatus ContactRequestStatus
	NotifiedAt    *time.Time

	// Accepted is the only state from which somebody may be recorded as a
	// hire (ADR-0019 §15): they agreed to talk, and their address was
	// released. Staged, notified-but-unanswered and declined all fail it.
	Accepted bool
}
