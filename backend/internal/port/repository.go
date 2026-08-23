package port

import (
	"context"
	"errors"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Repositories.
//
// Every method takes a domain type and returns a domain type. Translating
// between the domain model and storage rows is the repository's job, so the
// database can be swapped without a service noticing — that is the rule these
// signatures exist to enforce.
//
// Methods that must be able to join a caller's transaction take a Tx. Those
// that cannot participate in one do not, which makes the atomicity boundaries
// visible in the interface rather than buried in an implementation.

// Sentinel errors. A service distinguishes cases by errors.Is, never by
// inspecting a driver-specific error — which would leak the database upward.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrVersionStale = errors.New("version conflict")
)

// UserRepository owns contributors and their availability.
type UserRepository interface {
	ByID(ctx context.Context, id domain.UserID) (*domain.Contributor, error)
	ByGitHubUserID(ctx context.Context, githubUserID int64) (*domain.Contributor, error)

	// Create is the only path to a contributor account: there is no email
	// signup (ADR-0002). It writes the user and the GitHub identity together.
	Create(ctx context.Context, tx Tx, c *domain.Contributor) (*domain.Contributor, error)

	// SetAvailability resets expires_at to now + 15 days. The row is never
	// deleted on lapse — a returning contributor must find their setting
	// intact — so there is no ClearAvailability.
	SetAvailability(ctx context.Context, id domain.UserID, status domain.AvailabilityStatus) (*domain.Availability, error)

	// LapsingSoon drives the pre-lapse reminder job.
	LapsingSoon(ctx context.Context, within time.Duration, limit int) ([]domain.Contributor, error)
	MarkReminded(ctx context.Context, ids []domain.UserID) error

	// SetUserScores writes both user-level numbers. They are computed in one
	// transaction from the same ordered list so they cannot disagree — which is
	// why this takes both and there is no SetOverallScore.
	SetUserScores(ctx context.Context, tx Tx, id domain.UserID, overall, generalist *float64) error
}

// SessionRepository owns refresh-token families.
type SessionRepository interface {
	// ByRefreshTokenHash locks the row FOR UPDATE. Reuse detection is a
	// read-modify-write race if it does not.
	ByRefreshTokenHash(ctx context.Context, tx Tx, hash []byte) (*domain.Session, error)

	Create(ctx context.Context, tx Tx, s *domain.Session, refreshTokenHash []byte) error

	// Rotate marks the presented session used and inserts a successor sharing
	// its family id, in one transaction.
	Rotate(ctx context.Context, tx Tx, spent domain.SessionID, successor *domain.Session, hash []byte) error

	// RevokeFamily kills every session in a family. Called on reuse detection
	// and on logout alike — a deliberate sign-out must invalidate a refresh
	// token stolen beforehand.
	RevokeFamily(ctx context.Context, tx Tx, familyID string) error

	ActiveCount(ctx context.Context, principalID string) (int, error)

	// ActiveFamily returns the family a principal's live session belongs to,
	// so logout can revoke it.
	//
	// Keyed on the principal rather than on a token, because logout arrives
	// with an access token and the refresh token it should kill is held by the
	// client — which is exactly what makes revoking the family rather than the
	// presented session the right move.
	ActiveFamily(ctx context.Context, principalID string) (string, error)
}

// HirerRepository owns recruiter seats and their organizations.
type HirerRepository interface {
	ByID(ctx context.Context, id domain.HirerID) (*domain.Hirer, error)
	ByEmail(ctx context.Context, email string) (*domain.Hirer, error)
	PasswordHash(ctx context.Context, id domain.HirerID) ([]byte, error)

	// Register creates the account and its verification request in one
	// transaction (ADR-0002). Splitting them would allow an account with no
	// pending request, which nothing would ever verify.
	Register(ctx context.Context, tx Tx, h *domain.Hirer, org *domain.Organization, passwordHash []byte) (*domain.Hirer, error)

	Organization(ctx context.Context, id domain.OrganizationID) (*domain.Organization, error)
	Members(ctx context.Context, id domain.OrganizationID) ([]domain.Hirer, error)

	// SharesGitHubIdentity backs AssertNotSelf. It is asked as a question
	// rather than by fetching both identities, so the comparison lives in one
	// place instead of being re-derived at each of the three call sites.
	SharesGitHubIdentity(ctx context.Context, hirer domain.HirerID, target domain.UserID) (bool, error)
}

// OrganizationRepository owns invitations and seat grants.
type OrganizationRepository interface {
	CreateInvitation(ctx context.Context, tx Tx, orgID domain.OrganizationID, email string, role domain.OrgRole, invitedBy domain.HirerID, tokenHash []byte, expiresAt time.Time) (domain.RequestID, error)
	InvitationByTokenHash(ctx context.Context, tx Tx, hash []byte) (*Invitation, error)

	// AcceptInvitation creates the hirer, the membership, and marks the
	// invitation accepted in one transaction. The seat inherits the
	// organization's verification rather than earning its own.
	AcceptInvitation(ctx context.Context, tx Tx, id domain.RequestID, h *domain.Hirer, passwordHash []byte) (*domain.Hirer, error)

	// VerifyOrganization lifts every seat at once. Verification is per
	// organization, not per person (ADR-0002, ADR-0008 §3a).
	VerifyOrganization(ctx context.Context, tx Tx, id domain.OrganizationID, by domain.AdminID, reason string) error
}

// Invitation is a pending seat grant.
type Invitation struct {
	ID             domain.RequestID
	OrganizationID domain.OrganizationID
	Email          string
	Role           domain.OrgRole
	InvitedBy      domain.HirerID
	AcceptedAt     *time.Time
	ExpiresAt      time.Time
}

// ShareLinkRepository owns a contributor's publishable scorecard link.
//
// The one thing a contributor may publish about themselves (ADR-0002). Only
// the HASH is stored: the plaintext is shown once at minting, so a database
// read cannot yield a working link and neither can a backup.
//
// uq_share_link_active permits one active link per contributor, so minting a
// new one revokes any existing — a contributor who shared a link and then
// minted another must not find the old one still live.
type ShareLinkRepository interface {
	// Mint revokes any active link and issues a new one, in one statement.
	Mint(ctx context.Context, tx Tx, id domain.UserID, tokenHash []byte) (domain.ShareLinkID, error)

	// Revoke withdraws a link. Revocation is immediate: a published link the
	// contributor withdrew must stop resolving.
	Revoke(ctx context.Context, id domain.UserID, linkID domain.ShareLinkID) error

	// ResolveToken returns the contributor a live token belongs to, and counts
	// the view. A revoked link resolves to ErrNotFound, indistinguishable from
	// a token that never existed.
	ResolveToken(ctx context.Context, tokenHash []byte) (domain.UserID, error)
}

// ClaimRepository owns claims and their evidence.
type ClaimRepository interface {
	ByID(ctx context.Context, id domain.ClaimID) (*domain.Claim, error)
	ListByUser(ctx context.Context, id domain.UserID) ([]domain.Claim, error)

	Create(ctx context.Context, userID domain.UserID) (*domain.Claim, error)

	// Replace is the whole-claim write behind PUT /claims/{id}. It takes the
	// expected version and returns ErrVersionStale when it does not match, so
	// two tabs editing one draft cannot silently clobber each other.
	Replace(ctx context.Context, tx Tx, id domain.ClaimID, expectedVersion int, c *domain.Claim) (*domain.Claim, error)

	SetStatus(ctx context.Context, tx Tx, id domain.ClaimID, status domain.ClaimStatus) error
	SetEvaluated(ctx context.Context, tx Tx, id domain.ClaimID, at time.Time, lockedUntil time.Time) error

	// DecideSuggestion accepts or dismisses an inert AI suggestion. A decision
	// is final in both directions.
	DecideSuggestion(ctx context.Context, tx Tx, id domain.ClaimID, skillID domain.SkillID, accept bool) error

	// Fingerprint identifies unchanged evidence, so a resubmission that changed
	// nothing can be refused without spending a model call.
	Fingerprint(ctx context.Context, id domain.ClaimID) (string, error)

	// PendingSweep lists claims to re-queue when the active rubric changes.
	PendingSweep(ctx context.Context, fromVersion string, limit int) ([]domain.ClaimID, error)
}

// SkillRepository owns the catalogue, standings and the (user, PR, skill) links.
type SkillRepository interface {
	BySlug(ctx context.Context, slug string) (*domain.Skill, error)

	// Search resolves aliases. An alias matches for lookup and is NEVER
	// claimable, so the claiming population cannot fragment across spellings.
	Search(ctx context.Context, query string) ([]SkillMatch, error)

	Create(ctx context.Context, tx Tx, s *domain.Skill) (*domain.Skill, error)

	UserSkills(ctx context.Context, id domain.UserID) ([]domain.UserSkill, error)

	// LinkPairs writes (user, PR, skill) rows as pending, in the submitting
	// transaction. The uniqueness constraint on that triple is what stops the
	// same PR evidencing the same skill twice — enforced BEFORE a model call is
	// spent, which is why this joins the caller's Tx.
	LinkPairs(ctx context.Context, tx Tx, links []PRLink) error
	SetLinkStatus(ctx context.Context, tx Tx, links []PRLink, status domain.PRLinkStatus) error

	// RecomputeStanding derives standing from the count of DISTINCT SCORED PRs
	// and promotes at five, automatically. Standing is derived, never declared,
	// so there is no SetStanding.
	RecomputeStanding(ctx context.Context, tx Tx, id domain.UserID, skillID domain.SkillID) (*domain.UserSkill, error)

	// CreateRequest is rate-limited and deduped BEFORE the queue; the
	// implementation returns ErrConflict when a trigram match already exists.
	CreateRequest(ctx context.Context, userID domain.UserID, proposedName, rationale string) (domain.RequestID, error)
	PendingRequests(ctx context.Context, status string) ([]SkillRequest, error)
	DecideRequest(ctx context.Context, tx Tx, id domain.RequestID, by domain.AdminID, approve bool, reason string, created *domain.Skill) error
}

// SkillMatch is a catalogue hit and how it was found.
type SkillMatch struct {
	Skill        domain.Skill
	MatchedVia   string // "name" or "alias"
	MatchedAlias string
}

// PRLink is one (user, PR, skill) triple.
type PRLink struct {
	UserID    domain.UserID
	ClaimID   domain.ClaimID
	Position  int
	SkillID   domain.SkillID
	RepoOwner string
	RepoName  string
	PRNumber  int
}

// SkillRequest is a proposed catalogue entry awaiting a decision.
type SkillRequest struct {
	ID           domain.RequestID
	UserID       domain.UserID
	ProposedName string
	Rationale    string
	Status       string
	Reason       string
	CreatedAt    time.Time
	ReviewedAt   *time.Time
}

// EvaluationRepository owns judgements, scores and the job record.
type EvaluationRepository interface {
	// Persist writes every score, link status and standing change for one
	// evaluated claim in ONE transaction. A half-persisted evaluation would
	// leave a contributor with some skills promoted and others not.
	Persist(ctx context.Context, tx Tx, claimID domain.ClaimID, scores []domain.PRSkillScore, suggestions []domain.ClaimSkill) error

	Scores(ctx context.Context, claimID domain.ClaimID) ([]domain.PRSkillScore, error)
	Snapshot(ctx context.Context, tx Tx, s domain.ScoreSnapshot) error

	// AlreadyEvaluated makes redelivery free. The broker is at-least-once, so
	// the same job WILL arrive twice and the second time must cost nothing.
	AlreadyEvaluated(ctx context.Context, claimID domain.ClaimID, version int) (bool, error)

	DeadLetter(ctx context.Context, jobID domain.JobID, reason string, payload []byte) error
}

// NormsRepository owns the shared maxima every arithmetic signal is measured
// against.
type NormsRepository interface {
	Current(ctx context.Context) (map[string]domain.GlobalNorms, error)
	Observe(ctx context.Context, tx Tx, metric string, value float64) error
	Recompute(ctx context.Context, generation int) error
}

// SearchRepository owns the ranked query.
//
// One method, because ADR-0008 §1 specifies one query: rank over the unfiltered
// population in a CTE, then filter, then page. Splitting it would invite a
// second code path where the gates are applied as post-filters, and a
// post-filtered gate is a gate that leaks into a result count.
type SearchRepository interface {
	Search(ctx context.Context, caller domain.HirerID, q domain.SearchQuery) (*domain.SearchResults, error)
	Leaderboard(ctx context.Context, kind domain.LeaderboardKind, skill *domain.SkillID, limit int) (*domain.Leaderboard, error)
	Scorecard(ctx context.Context, caller domain.HirerID, target domain.UserID) (*domain.Scorecard, error)
	Rank(ctx context.Context, id domain.UserID) (*domain.Rank, error)
}

// ShortlistRepository owns hiring rounds, staging and confirmation.
type ShortlistRepository interface {
	ByID(ctx context.Context, id domain.ShortlistID) (*domain.Shortlist, error)
	ListByOrganization(ctx context.Context, id domain.OrganizationID, status *domain.ShortlistStatus) ([]domain.Shortlist, error)

	Create(ctx context.Context, s *domain.Shortlist) (*domain.Shortlist, error)
	Update(ctx context.Context, id domain.ShortlistID, name, description *string, date *time.Time) (*domain.Shortlist, error)
	Close(ctx context.Context, id domain.ShortlistID) (*domain.Shortlist, error)

	// AddEntry stages a candidate and discloses NOTHING.
	AddEntry(ctx context.Context, e *domain.ShortlistEntry) (*domain.ShortlistEntry, error)

	// RemoveEntry is permitted only while notified_at IS NULL. It returns
	// ErrConflict afterwards: the contributor was told, and that is a fact.
	RemoveEntry(ctx context.Context, id domain.ShortlistID, user domain.UserID) error

	// Confirm notifies exactly the entries where notified_at IS NULL, writing
	// every contact request in one transaction and stamping the entries. This
	// is the irreversible act, and it is one call because a partially confirmed
	// round would have told some people and not others.
	Confirm(ctx context.Context, tx Tx, id domain.ShortlistID) ([]domain.ContactRequest, error)

	// OverdueRatios drives the daily flagging job: open rounds past their date
	// over open rounds. A draft never promised anything, so it does not count.
	OverdueRatios(ctx context.Context, now time.Time) ([]OverdueOrg, error)
}

// OverdueOrg is one organization's delivery record.
type OverdueOrg struct {
	OrganizationID domain.OrganizationID
	Name           string
	OpenShortlists int
	Overdue        int
	Ratio          float64
}

// ContactRepository owns the consent record.
type ContactRepository interface {
	ByID(ctx context.Context, id domain.ContactID) (*domain.ContactRequest, error)
	ListForUser(ctx context.Context, id domain.UserID, status *domain.ContactRequestStatus) ([]domain.ContactRequest, error)
	ListForShortlist(ctx context.Context, id domain.ShortlistID) ([]domain.ContactRequest, error)

	// Respond records the answer and, on acceptance, stamps email_released_at.
	// The email itself is read from users.email; this records that release was
	// authorised and when.
	Respond(ctx context.Context, tx Tx, id domain.ContactID, accept bool, at time.Time) (*domain.ContactRequest, error)

	ExpireStale(ctx context.Context, now time.Time) (int, error)
}

// SavedSearchRepository owns stored filter sets.
type SavedSearchRepository interface {
	ByID(ctx context.Context, id domain.SavedSearchID) (*domain.SavedSearch, error)
	ListByOrganization(ctx context.Context, id domain.OrganizationID) ([]domain.SavedSearch, error)
	Create(ctx context.Context, s *domain.SavedSearch) (*domain.SavedSearch, error)
	Delete(ctx context.Context, id domain.SavedSearchID) error
}

// AdminRepository owns the verification queue.
type AdminRepository interface {
	ByEmail(ctx context.Context, email string) (*domain.Admin, error)
	PasswordHash(ctx context.Context, id domain.AdminID) ([]byte, error)

	PendingVerifications(ctx context.Context) ([]VerificationRequest, error)
	DecideVerification(ctx context.Context, tx Tx, id domain.RequestID, by domain.AdminID, approve bool, reason string) error

	// SeedAdmin creates the first admin if none exists, and only then.
	SeedAdmin(ctx context.Context, email string, passwordHash []byte) (bool, error)
}

// VerificationRequest is a hirer or organization awaiting review.
type VerificationRequest struct {
	ID             domain.RequestID
	HirerID        *domain.HirerID
	OrganizationID *domain.OrganizationID
	Status         string
	CreatedAt      time.Time
	ReviewedAt     *time.Time
}

// ReevaluationRepository owns disputes and the escalating cooldown.
type ReevaluationRepository interface {
	Create(ctx context.Context, r *domain.ReevaluationRequest) (*domain.ReevaluationRequest, error)
	Pending(ctx context.Context) ([]domain.ReevaluationRequest, error)

	// Decide records the outcome and, on rejection only, advances the cooldown.
	// Acceptance never counts against a contributor — passing the verdict in
	// keeps that rule in one place rather than at each call site.
	Decide(ctx context.Context, tx Tx, id domain.RequestID, by domain.AdminID, accept bool, reason string) error

	Cooldown(ctx context.Context, id domain.UserID) (*domain.Cooldown, error)
}
