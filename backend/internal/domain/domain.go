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
	Email          string
	AuthProvider   AuthProvider
	VerifiedAt     *time.Time
	OrgRole        OrgRole

	// GitHub identity, when the seat signed up through it. This is what
	// AssertNotSelf keys on.
	GitHubUserID *int64

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
