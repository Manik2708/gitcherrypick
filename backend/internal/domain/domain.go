// Package domain holds the types the whole system is expressed in.
//
// It imports NOTHING from internal/. That is the rule that makes the layering
// real: a domain type cannot mention a database row, an HTTP request, or a
// queue message, so a repository is forced to translate rather than leak.
//
// Every type here is defined by the approved ADRs. Where a comment cites one,
// the ADR is binding and this file is a restatement — if they disagree, the ADR
// wins and this is the bug.
package domain

import "time"

// --- identifiers -------------------------------------------------------------
//
// Distinct types rather than a shared ID alias, so passing a ClaimID where a
// UserID belongs does not compile. Most of the bugs this prevents are in
// repository methods that take three uuids in a row.

// UserID identifies a contributor.
type UserID string

// HirerID identifies a recruiter seat.
type HirerID string

// AdminID identifies an administrator.
type AdminID string

// OrganizationID identifies the company a seat belongs to, and the unit
// verification applies to.
type OrganizationID string

// ClaimID identifies one evidence bundle.
type ClaimID string

// SkillID identifies a catalogue entry.
type SkillID string

// EvaluationID identifies one completed judgement run.
type EvaluationID string

// JobID identifies one queued unit of work.
type JobID string

// SessionID identifies one refresh token within a family.
type SessionID string

// ShortlistID identifies a hiring round.
type ShortlistID string

// ContactID identifies one contact request.
type ContactID string

// SavedSearchID identifies a stored filter set.
type SavedSearchID string

// RosterEntryID identifies one address an organization is willing to seat.
type RosterEntryID string

// EmailVerificationID identifies one outstanding proof of an address.
type EmailVerificationID string

// OnboardingID identifies one submitted organisation, before it is one.
//
// A submission is not an organisation and does not have an OrganizationID:
// the organisation is created when an administrator approves it, and until
// then there is nothing to identify by that name (ADR-0017 §2).
type OnboardingID string

// AddressID identifies one address an organisation holds.
type AddressID string

// WorkPreferences is what shape of work a contributor will take (ADR-0018).
//
// Four flags and no freelance one: availability_status already carries
// freelance, and two controls meaning one thing can disagree.
//
// These COMPOSE with availability rather than standing beside it. Declaring
// availability marks someone available for every flag they enabled and for
// nothing else — "available" on its own is not a state anybody is in.
type WorkPreferences struct {
	UserID UserID

	OpenToRemote      bool
	OpenToInternships bool
	OpenToOnsite      bool
	OpenToContract    bool

	// CurrentCountry is ISO 3166-1 alpha-2, typed by the person. Distinct from
	// users.location, which GitHub supplies and a sign-in overwrites.
	CurrentCountry string

	// OfficeYOE is years in a job, SELF-REPORTED. Presented as the person's
	// claim, never as something the platform vouches for — it is the one
	// number here nobody can check.
	OfficeYOE *int

	// FirstPRURL and LatestPRURL are ASKED, not derived.
	//
	// Deriving them from claims was wrong: a first contribution is often years
	// old in a repository nobody claimed here, and a claim is five PRs someone
	// chose as their BEST — not their earliest and not their most recent.
	//
	// FirstPRURL is WRITE-ONCE. When somebody started is a fact about the past;
	// a field revisable whenever it suited would be worth nothing to the hirer
	// reading it. LatestPRURL is editable, because it goes stale by definition.
	FirstPRURL  string
	LatestPRURL string

	// When those pull requests were AUTHORED, established by us rather than
	// typed by anybody (ADR-0019 §7).
	//
	// Authored, not merged: a first contribution can sit in review for eight
	// months, or be merged long after the person moved on. Merge date measures
	// a project's responsiveness; this measures when they started contributing.
	FirstPRAuthoredAt  *time.Time
	LatestPRAuthoredAt *time.Time

	// FirstPRVerifiedAt is when we last confirmed the first pull request
	// against GitHub, or nil.
	//
	// Nil with a URL present means a fetch we could not COMPLETE — a rate
	// limit, an outage — and a job will retry. A fetch that completed and
	// DISPROVED the claim never reaches here: those are refused at the service
	// and nothing is written, so a write-once field never records an
	// unverified URL.
	FirstPRVerifiedAt *time.Time

	// Stated is whether this person has ever saved the form.
	//
	// Distinct from every flag being false, which is a real answer somebody
	// may mean: "I am not open to any of these right now". Never having been
	// asked is not an answer, and the two must not look alike — one needs
	// prompting and the other does not.
	Stated bool
}

// OSSYears is how long this contributor has been contributing, as of now.
//
// Nil means UNKNOWN, which is not zero. Unknown does not clear a role's
// minimum (ADR-0019 §When the fetch fails): failing open there would make any
// minimum clearable with a link nobody could check.
func (w *WorkPreferences) OSSYears(now time.Time) *int {
	if w == nil || w.FirstPRAuthoredAt == nil || w.FirstPRVerifiedAt == nil {
		return nil
	}
	years := int(now.Sub(*w.FirstPRAuthoredAt).Hours() / 24 / 365.25)
	if years < 0 {
		// A PR authored in the future is a clock disagreement, not negative
		// experience. Report none rather than nonsense.
		years = 0
	}
	return &years
}

// VerificationPending is whether a stated first pull request is still waiting
// to be confirmed. Surfaced to the contributor, because they are the only
// person who can do anything about it.
func (w *WorkPreferences) VerificationPending() bool {
	return w != nil && w.FirstPRURL != "" && w.FirstPRVerifiedAt == nil
}

// Matchable reports whether any role could reach this contributor.
//
// A live availability window with no flag enabled matches NOTHING, and that is
// reachable by accident: refresh the window, believe you are findable, and be
// invisible to every role because you never said what work you would take
// (ADR-0018 §The unmatchable state). Reported as a field rather than left for
// a client to derive, because a client that forgot would leave someone
// wondering why nobody writes.
func (w *WorkPreferences) Matchable() bool {
	return w != nil && (w.OpenToRemote || w.OpenToInternships ||
		w.OpenToOnsite || w.OpenToContract)
}

// Compensation is what a contributor expects to be paid.
//
// A HIRER NEVER SEES THIS (ADR-0018 §2). It exists to filter what the
// CONTRIBUTOR is shown, never to filter, rank or annotate a hirer's view of
// people — a hirer who knows what you will accept offers exactly that.
type Compensation struct {
	UserID UserID

	// Currency is ISO 4217, required once either amount is set.
	Currency string

	// Minor units. Never a float: money that does not add up is money nobody
	// trusts. Nil means not stated, which is different from zero.
	HourlyRate   *int64
	YearlyAmount *int64
}

// Country is one entry in the picker, from port.PlaceService.
type Country struct {
	Code string // ISO 3166-1 alpha-2
	Name string
}

// ShareLinkID identifies a contributor's published scorecard link.
type ShareLinkID string

// RequestID identifies a queued human decision — a verification, a skill
// request, an invitation, or a re-evaluation dispute.
type RequestID string

// --- principals --------------------------------------------------------------

// PrincipalKind distinguishes the three account types. They share no table and
// no credential; a hirer's password is not an admin's (ADR-0002).
type PrincipalKind string

// The three account types. They share no table and no credential.
const (
	KindContributor PrincipalKind = "contributor"
	KindHirer       PrincipalKind = "hirer"
	KindAdmin       PrincipalKind = "admin"
)

// Principal is who a request is acting as. Middleware resolves the session to
// one of these; controllers extract it and pass it to a service, which decides
// access. Controllers never decide access (ADR-0002).
type Principal struct {
	Kind PrincipalKind

	// Exactly one of these is set, matching Kind.
	Contributor *Contributor
	Hirer       *Hirer
	Admin       *Admin
}

// Subject is the account id as a string, for the access token's `sub` claim
// (ADR-0011). The three kinds carry different id types and share no table, so
// `sub` only means something alongside `kind`.
//
// Empty when the matching entity is nil — a caller that built a Principal from
// Kind alone has no subject to assert, and a token claiming to identify nobody
// must be refused rather than minted.
func (p Principal) Subject() string {
	switch p.Kind {
	case KindContributor:
		if p.Contributor != nil {
			return string(p.Contributor.ID)
		}
	case KindHirer:
		if p.Hirer != nil {
			return string(p.Hirer.ID)
		}
	case KindAdmin:
		if p.Admin != nil {
			return string(p.Admin.ID)
		}
	}
	return ""
}

// Contributor is an open-source contributor. They exist only through GitHub —
// there is no email path to a contributor account (ADR-0002).
type Contributor struct {
	ID           UserID
	DisplayName  string
	Email        string
	GitHubUserID int64
	GitHubLogin  string
	AvatarURL    string
	Bio          string
	Location     string

	// Nil until at least one skill reaches primary standing. Null is not zero:
	// zero would claim we measured something (ADR-0007).
	OverallScore    *float64
	GeneralistScore *float64

	Availability *Availability
	CreatedAt    time.Time
}

// AvailabilityStatus is what a contributor says about being open to work.
type AvailabilityStatus string

// The availability states a contributor may set.
const (
	NotLooking          AvailabilityStatus = "not_looking"
	LookingForJob       AvailabilityStatus = "looking_for_job"
	LookingForFreelance AvailabilityStatus = "looking_for_freelance"
	OpenToFreelance     AvailabilityStatus = "open_to_freelance"
)

// Availability expires 15 days after it is set (ADR-0002 §6).
//
// The row is never mutated on expiry — a returning contributor must find their
// setting intact — so "active" is a comparison, not a stored flag.
type Availability struct {
	Status     AvailabilityStatus
	ExpiresAt  *time.Time
	LastSetAt  time.Time
	RemindedAt *time.Time
}

// IsActive reports whether a hirer sees this contributor by default.
//
// ADR-0008 §1a: lapsed means hidden-by-default and still ranked, reachable with
// include_inactive. not_looking is an explicit opt-out and is excluded
// everywhere, by no toggle.
func (a *Availability) IsActive(now time.Time) bool {
	if a == nil || a.Status == NotLooking || a.ExpiresAt == nil {
		return false
	}
	return a.ExpiresAt.After(now)
}

// OptedOut reports an explicit refusal, as distinct from having gone quiet.
func (a *Availability) OptedOut() bool {
	return a == nil || a.Status == NotLooking
}

// Hirer is a recruiter seat. Capability is a property of the ORGANIZATION:
// verifying one lifts every seat, and an invitation inherits whatever the org
// has (ADR-0002, ADR-0008 §3a).
type Hirer struct {
	ID             HirerID
	OrganizationID OrganizationID
	DisplayName    string

	// Username is the sign-in identifier, not the email (ADR-0016). Globally
	// unique and never reused, so an authored row stays attributable to one
	// person after their seat is revoked.
	Username string

	// Email is a contact field. It may be shared by two seats and it may
	// change; nothing identifies a person by it.
	Email        string
	AuthProvider AuthProvider
	VerifiedAt   *time.Time
	OrgRole      OrgRole

	// GitHub identity, when the seat signed up through it. This is what
	// AssertNotSelf keys on.
	GitHubUserID *int64

	// DisabledAt marks a revoked seat. The row survives because it authored
	// shortlists and the permanent record of which candidates were told an
	// organization was interested (ADR-0008, ADR-0016 §5).
	DisabledAt *time.Time

	// Organization the seat acts for, populated by the repository.
	//
	// Navigable because capability IS a property of the organization: nearly
	// every read of a hirer immediately needs to know whether their org is
	// verified, and an id alone forces a second query at each of those call
	// sites. Nil when a caller built the hirer without one.
	Organization *Organization
}

// AuthProvider is how a hirer signs in. A password exists only for email.
type AuthProvider string

// The sign-in providers a hirer seat may use.
const (
	ProviderEmail  AuthProvider = "email"
	ProviderGoogle AuthProvider = "google"
	ProviderGitHub AuthProvider = "github"
)

// OrgRole is a seat's authority within its organization.
type OrgRole string

// The authority a seat holds within its organization.
const (
	RoleOwner  OrgRole = "owner"
	RoleMember OrgRole = "member"
)

// Organization is the unit verification applies to.
type Organization struct {
	ID                OrganizationID
	Name              string
	Slug              string
	Website           string
	LinkedInURL       string
	VerifiedAt        *time.Time
	PaymentVerifiedAt *time.Time

	// --- from onboarding (ADR-0017) ---------------------------------------
	//
	// Every one of these is optional on the type as well as in the column,
	// because organisations created before ADR-0017 have none of them and a
	// freelancer legitimately has no office address.
	Description string

	// Email is the company address AND the owner's. One value, which works
	// because ADR-0016 made the username the identity (ADR-0017 §6).
	Email     string
	Phone     string
	Headcount HeadcountBand

	// MainOffice is nil until one is given. Resolved by the repository from
	// organization_addresses rather than stored inline.
	MainOffice *Address
}

// HeadcountBand is how many people work somewhere, approximately.
//
// A band rather than a number: the source field is "CurrentApproxEmployees"
// and approximate is what it means. Nobody knows whether 47 means 47, and a
// band is both easier to answer honestly and harder to answer misleadingly
// (ADR-0017 §9).
type HeadcountBand string

// The bands. Labels carry their own boundaries rather than reading "small"
// and "medium", which would hide the one thing the value is for: judging
// whether the evidence a company offers is proportionate to its size.
const (
	Headcount1To10     HeadcountBand = "1-10"
	Headcount11To50    HeadcountBand = "11-50"
	Headcount51To200   HeadcountBand = "51-200"
	Headcount201To1000 HeadcountBand = "201-1000"
	Headcount1000Plus  HeadcountBand = "1000+"
)

// ValidHeadcountBand reports whether a submitted band is one of the five.
//
// Checked in the controller so a typo comes back as a named field error rather
// than as an enum violation, which tells a caller nothing about which field.
func ValidHeadcountBand(b HeadcountBand) bool {
	switch b {
	case Headcount1To10, Headcount11To50, Headcount51To200,
		Headcount201To1000, Headcount1000Plus:
		return true
	}
	return false
}

// Address is somewhere an organisation is.
//
// A type of its own because a role posted later is either remote or AT an
// address, and the source diagram reuses one rather than retyping it
// (ADR-0017 §10).
type Address struct {
	ID             AddressID
	OrganizationID OrganizationID
	Country        string // ISO 3166-1 alpha-2
	City           string

	// PostalCode is optional: several countries have none, and requiring it
	// would lock those organisations out of onboarding entirely.
	PostalCode string

	Street1 string
	Street2 string

	// IsMainOffice is a flag rather than a pointer from organizations, which
	// would make the two tables mutually dependent.
	IsMainOffice bool
}

// IsVerified reports identity verification — the gate on hiring capability.
func (o *Organization) IsVerified() bool { return o != nil && o.VerifiedAt != nil }

// PaymentVerified is NOT a gate. Missing payment evidence is disclosed to the
// contributor at contact time rather than blocking the org (ADR-0002 §5).
func (o *Organization) PaymentVerified() bool { return o != nil && o.PaymentVerifiedAt != nil }

// Admin decides hirer verification, skill requests and re-evaluation disputes.
// There is deliberately no OAuth path to an admin session (ADR-0002).
type Admin struct {
	ID          AdminID
	DisplayName string
	Email       string
	DisabledAt  *time.Time
}

// --- sessions ----------------------------------------------------------------

// Session is one refresh token in a family. Rotation issues a successor with
// the same FamilyID; replaying a spent token revokes the whole family
// (ADR-0002).
type Session struct {
	ID          SessionID
	FamilyID    string
	PrincipalID string
	Kind        PrincipalKind
	IssuedAt    time.Time
	ExpiresAt   time.Time
	UsedAt      *time.Time
	RevokedAt   *time.Time
}

// TokenPair is what a sign-in or a refresh returns.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// VerificationDecision is an admin's answer to one verification request.
//
// Payment capability is decided SEPARATELY from the account. A studio can be
// demonstrably real and still have nothing but self-reported payment history,
// and a contributor deciding whether to release their address is told which of
// the two was established (ADR-0002 §5).
type VerificationDecision struct {
	Approve         bool
	Reason          string
	PaymentVerified bool
}

// VerificationProof is one piece of evidence that a hiring account is real.
//
// Five kinds, and the two unstructured ones matter most: `alternative` and
// `payment_capability` exist so a three-person studio with no company domain
// and no LinkedIn page can still be verified (ADR-0002). Without them,
// verification would be satisfiable only by large companies.
type VerificationProof struct {
	Kind          VerificationProofKind
	Value         string
	Notes         string
	AttachmentURL string
}

// VerificationProofKind names the shape of one proof.
type VerificationProofKind string

// The proof kinds. Link-shaped ones carry a Value; the last two carry Notes.
const (
	ProofOrganizationLinkedIn VerificationProofKind = "organization_linkedin"
	ProofWorkEmailDomain      VerificationProofKind = "work_email_domain"
	ProofFreelancerProfile    VerificationProofKind = "freelancer_profile"
	ProofPaymentCapability    VerificationProofKind = "payment_capability"
	ProofAlternative          VerificationProofKind = "alternative"
)

// ValidProofKind reports whether a kind is one the catalogue accepts.
//
// Checked before the insert so a typo comes back as a named field error rather
// than as an enum violation from Postgres.
func ValidProofKind(k VerificationProofKind) bool {
	switch k {
	case ProofOrganizationLinkedIn, ProofWorkEmailDomain, ProofFreelancerProfile,
		ProofPaymentCapability, ProofAlternative:
		return true
	}
	return false
}
