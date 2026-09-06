package port

import (
	"context"
	"errors"
	"fmt"
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

	// ErrUnavailable is a third party failing transiently — a 5xx, a rate
	// limit, a timeout, a refused connection.
	//
	// Distinct from ErrNotFound because the two demand opposite handling: a PR
	// that does not exist is a permanent fact about a claim, while GitHub being
	// down is a fact about right now and must not be recorded as evidence
	// against the contributor.
	ErrUnavailable = errors.New("upstream unavailable")

	// ErrEmailUnverified is an OAuth identity whose provider will not vouch for
	// the address. Distinct from a failed exchange: the exchange SUCCEEDED and
	// returned somebody, and what is wrong is the address — which is a
	// different thing to tell the caller, and a different thing to fix.
	ErrEmailUnverified = errors.New("the provider has not verified this email address")

	// An invitation that was real and no longer works, split by WHY. The
	// holder's remedy differs: one asks for a fresh invitation, the other
	// simply signs in.
	// ErrEntryNotified is a shortlist entry whose contributor was already
	// told. Removing it would destroy the record of a disclosure that
	// happened, which is the one thing the two-phase design guarantees
	// (ADR-0008 §3a).
	ErrEntryNotified = fmt.Errorf("the entry has been notified: %w", ErrConflict)

	ErrInvitationExpired  = fmt.Errorf("the invitation has expired: %w", ErrConflict)
	ErrInvitationAccepted = fmt.Errorf("the invitation was already accepted: %w", ErrConflict)
)

// UserRepository owns contributors and their availability.
type UserRepository interface {
	ByID(ctx context.Context, id domain.UserID) (*domain.Contributor, error)

	ByGitHubUserID(ctx context.Context, githubUserID int64) (*domain.Contributor, error)

	// RefreshGitHubLogin records a login the user renamed on GitHub.
	//
	// The numeric id is the identity and never changes; the login is a
	// display handle that does. A platform that kept the handle it first saw
	// would show a name the contributor no longer answers to (ADR-0002).
	RefreshGitHubLogin(ctx context.Context, githubUserID int64, login string) error

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

	// CloseFamily retires a family the holder signed out of.
	//
	// Distinct from RevokeFamily, which is the COLLATERAL case: a session
	// revoked by somebody else's replay was never spent, and telling those two
	// apart is what lets refresh answer "you signed out" rather than "your
	// session was revoked" (ADR-0002).
	CloseFamily(ctx context.Context, tx Tx, familyID string) error

	// FamilyLive reports whether a family still has an unrevoked session.
	FamilyLive(ctx context.Context, familyID string) (bool, error)

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

	// ByGitHubUserID resolves a seat that signed up through GitHub.
	//
	// A separate namespace from the contributor lookup of the same name: one
	// identity may own one contributor AND one hirer (ADR-0009), and
	// dave/dave_hiring is exactly that case. Resolving across both would hand
	// a recruiter their own contributor account, or the reverse.
	ByGitHubUserID(ctx context.Context, githubUserID int64) (*domain.Hirer, error)
	PasswordHash(ctx context.Context, id domain.HirerID) ([]byte, error)

	// Register creates the account, its organization, its verification
	// request and that request's proofs in one transaction (ADR-0002).
	// Splitting them would allow an account with no pending request, which
	// nothing would ever verify — or a request with no evidence attached,
	// which no admin could decide.
	Register(ctx context.Context, tx Tx, in NewHirerAccount) (*HirerRegistration, error)

	Organization(ctx context.Context, id domain.OrganizationID) (*domain.Organization, error)
	Members(ctx context.Context, id domain.OrganizationID) ([]domain.Hirer, error)

	// SharesGitHubIdentity backs AssertNotSelf. It is asked as a question
	// rather than by fetching both identities, so the comparison lives in one
	// place instead of being re-derived at each of the three call sites.
	SharesGitHubIdentity(ctx context.Context, hirer domain.HirerID, target domain.UserID) (bool, error)
}

// NotifiedEntryError refuses removal and says when the disclosure happened.
type NotifiedEntryError struct {
	NotifiedAt time.Time
}

func (e *NotifiedEntryError) Error() string {
	return "the entry was notified at " + e.NotifiedAt.Format(time.RFC3339)
}

// Unwrap keeps errors.Is(err, ErrEntryNotified) working for callers that only
// need to know which case this is.
func (e *NotifiedEntryError) Unwrap() error { return ErrEntryNotified }

// Evaluation is one judging run, as the evaluator opens it.
type Evaluation struct {
	ClaimID       domain.ClaimID
	ClaimVersion  int
	RubricVersion string
	Model         string
	PromptVersion string

	// Fingerprint identifies the evidence that was judged. Two runs over the
	// same evidence under the same rubric are the same evaluation, which is
	// what makes redelivery free.
	Fingerprint []byte

	Trigger string
}

// SkillCollision is an existing catalogue entry a proposed one would clash with.
//
// The catalogue is curated precisely so that one skill has one name (ADR-0003),
// which makes a collision the normal outcome of a careless approval rather than
// an exceptional one. An admin refused needs to know WHICH entry they hit and
// on which term, or their only recourse is to guess.
type SkillCollision struct {
	Skill domain.Skill

	// Term is the proposed slug or alias that collided.
	Term string

	// TermIsAlias reports that Term came from the proposed ALIASES rather than
	// the proposed slug — which is the difference between alias_taken and
	// slug_taken on the wire.
	TermIsAlias bool

	// MatchedAlias reports that Term hit an existing ALIAS rather than an
	// existing slug. A slug colliding with somebody's alias is still a slug
	// problem, but the admin needs to be told which alias to look at.
	MatchedAlias bool
}

// SkillRef names a catalogue entry the way the wire does: slug to key on, name
// to show.
type SkillRef struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// SkillRequester is who asked for a catalogue addition.
//
// Resolved rather than referenced: an admin deciding whether to add a skill is
// weighing who is asking, and three uuids in a queue tell them nothing.
type SkillRequester struct {
	ID          domain.UserID
	DisplayName string
}

// ClaimSummary is one claim as the list shows it.
//
// NominatedPrimary is the slug the contributor put forward for primary
// standing on this claim, empty when they nominated none.
type ClaimSummary struct {
	ID               domain.ClaimID
	Status           domain.ClaimStatus
	Version          int
	NominatedPrimary string
	PRCount          int
	SkillCount       int
	SubmittedAt      *time.Time
	EvaluatedAt      *time.Time
	LockedUntil      *time.Time
}

// NewHirerAccount is everything one registration writes.
//
// A struct rather than six parameters: the call already crossed the line where
// positional arguments stop being readable, and proofs made it worse.
type NewHirerAccount struct {
	Hirer        *domain.Hirer
	Organization *domain.Organization
	PasswordHash []byte
	Proofs       []domain.VerificationProof
}

// OrganizationRepository owns invitations and seat grants.
type OrganizationRepository interface {
	// CreateInvitation returns the stored row rather than just its id, so the
	// created_at on the wire is the one the database wrote — not a second
	// clock reading taken next to it, which would differ under load.
	CreateInvitation(ctx context.Context, tx Tx, orgID domain.OrganizationID, email string, role domain.OrgRole, invitedBy domain.HirerID, tokenHash []byte, expiresAt time.Time) (*Invitation, error)
	InvitationByTokenHash(ctx context.Context, tx Tx, hash []byte) (*Invitation, error)

	// AcceptInvitation creates the hirer, the membership, and marks the
	// invitation accepted in one transaction. The seat inherits the
	// organization's verification rather than earning its own.
	// AcceptInvitation takes `now` rather than using the database clock.
	// Expiry is a business rule measured on the platform's clock (ADR-0012),
	// and SQL now() would answer from a second one nothing else in the system
	// reads.
	AcceptInvitation(ctx context.Context, tx Tx, id domain.RequestID, h *domain.Hirer, passwordHash []byte, now time.Time) (*domain.Hirer, error)

	// VerifyOrganization lifts every seat at once. Verification is per
	// organization, not per person (ADR-0002, ADR-0008 §3a).
	VerifyOrganization(ctx context.Context, tx Tx, id domain.OrganizationID, by domain.AdminID, reason string) error

	// VerificationFor reads a seat's own request, so a hirer can see where
	// they stand without an admin telling them.
	//
	// Takes both subjects because a request names exactly one: registration
	// raises it against the organization, while a later seat is verified in
	// its own right (ADR-0002). Returns the most recent when both exist.
	VerificationFor(ctx context.Context, hirer domain.HirerID, org domain.OrganizationID) (*VerificationRequest, error)
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
	CreatedAt      time.Time
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
	// ListByUser returns SUMMARIES, not claims. The list view shows counts,
	// and loading five PRs and their scores for every claim to report "5"
	// would be several joins per row to produce one integer.
	ListByUser(ctx context.Context, id domain.UserID) ([]ClaimSummary, error)

	Create(ctx context.Context, userID domain.UserID) (*domain.Claim, error)

	// Replace is the whole-claim write behind PUT /claims/{id}. It takes the
	// expected version and returns ErrVersionStale when it does not match, so
	// two tabs editing one draft cannot silently clobber each other.
	Replace(ctx context.Context, tx Tx, id domain.ClaimID, expectedVersion int, c *domain.Claim) (*domain.Claim, error)

	// Enrich caches the GitHub facts a judgement and the arithmetic need.
	//
	// Stored rather than re-fetched at scoring time so a run is reproducible
	// from what the database holds: the reach and engagement terms normalise
	// against these numbers, and a repository whose star count moved between
	// two reads would make the same evidence score differently (ADR-0004).
	Enrich(ctx context.Context, tx Tx, id domain.ClaimID, prs []domain.PREvidence) error

	// ReplaceEvidence writes a sub-resource without consuming the version.
	//
	// The version is the concurrency token for the whole-claim edit. Setting
	// evidence is a different operation, and advancing it there would make a
	// contributor's next PUT fail with a staleness they never caused.
	ReplaceEvidence(ctx context.Context, tx Tx, id domain.ClaimID, c *domain.Claim) (*domain.Claim, error)

	SetStatus(ctx context.Context, tx Tx, id domain.ClaimID, status domain.ClaimStatus) error
	SetEvaluated(ctx context.Context, tx Tx, id domain.ClaimID, at time.Time, lockedUntil time.Time) error

	// DecideSuggestion accepts or dismisses an inert AI suggestion. A decision
	// is final in both directions.
	DecideSuggestion(ctx context.Context, tx Tx, id domain.ClaimID, skillID domain.SkillID, accept bool) (*domain.ClaimSkill, error)

	// EvaluatedFingerprint is the evidence the last successful judgement ran
	// over, or "" if the claim has never been judged.
	//
	// Compared against the claim's current fingerprint on submit: resubmitting
	// evidence that was already judged buys nothing and costs a model call
	// (ADR-0003).
	EvaluatedFingerprint(ctx context.Context, id domain.ClaimID) (string, error)

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

	// UserSkills takes a Tx so a caller mid-transaction reads what it has
	// already written. Passing nil reads through the pool, which is what every
	// plain read wants; passing the tx is what a recompute needs, since a
	// separate connection cannot see uncommitted standings.
	UserSkills(ctx context.Context, tx Tx, id domain.UserID) ([]domain.UserSkill, error)

	// LinkPairs writes (user, PR, skill) rows as pending, in the submitting
	// transaction. The uniqueness constraint on that triple is what stops the
	// same PR evidencing the same skill twice — enforced BEFORE a model call is
	// spent, which is why this joins the caller's Tx.
	LinkPairs(ctx context.Context, tx Tx, links []PRLink) error

	// LinkJudgedEvidence claims, for one skill, every PR on a claim that has
	// already been judged against it.
	//
	// Accepting an AI suggestion is the case: the model judged those PRs
	// against the suggested skill while it was still inert, so acceptance has
	// nothing left to evaluate and only needs the evidence attached
	// (ADR-0003 §10).
	LinkJudgedEvidence(ctx context.Context, tx Tx, id domain.UserID,
		claimID domain.ClaimID, skillID domain.SkillID) error

	// SetSkillScore writes a skill's score and the components behind it.
	//
	// The components are STORED rather than derived on read: the formula lives
	// in the evaluator, and a read path recomputing it would be a second
	// implementation that eventually disagrees (ADR-0005).
	SetSkillScore(ctx context.Context, tx Tx, id domain.UserID, skillID domain.SkillID,
		score, prComponent, projectComponent float64) error

	// UnlinkClaim drops every (user, PR, skill) link a claim holds.
	//
	// Withdrawal has to release them or the evidence stays spent: the pairs
	// would keep counting toward standing and keep blocking a resubmission of
	// PRs the contributor no longer claims anything with (ADR-0007).
	UnlinkClaim(ctx context.Context, tx Tx, id domain.ClaimID) error

	// ConflictingPairs reports which of these triples another claim already
	// holds. The unique index catches them anyway, but a constraint violation
	// cannot say WHICH claim owns the pair — and "one of these is a duplicate"
	// leaves the contributor to find it by elimination.
	ConflictingPairs(ctx context.Context, links []PRLink) ([]PairConflict, error)
	// SetLinkStatus takes the reason alongside the status. A rejected link is
	// KEPT rather than deleted — it blocks the same PR being resubmitted for
	// the same skill to reroll the verdict (ADR-0007) — so it has to say what
	// the verdict was.
	SetLinkStatus(ctx context.Context, tx Tx, links []PRLink, status domain.PRLinkStatus, reason *domain.RejectionReason) error

	// RecomputeStanding derives standing from the count of DISTINCT SCORED PRs
	// and promotes at five, automatically. Standing is derived, never declared,
	// so there is no SetStanding.
	RecomputeStanding(ctx context.Context, tx Tx, id domain.UserID, skillID domain.SkillID) (*domain.UserSkill, error)

	// MatchSkill finds the catalogue entry a proposed name duplicates, by
	// name, slug, alias or trigram similarity. ErrNotFound means the proposal
	// is genuinely new.
	//
	// Asked as a question so the DEDUP DECISION stays in the service: whether
	// a near-match blocks a request is a product rule, not a storage detail.
	MatchSkill(ctx context.Context, proposedName string) (*domain.Skill, error)

	// RequestByID reads one catalogue request, decided or not. Used to report
	// the standing decision when a second one is refused.
	RequestByID(ctx context.Context, id domain.RequestID) (*SkillRequest, error)

	// CollidesWith reports the first exact clash between a proposed catalogue
	// entry and the existing one, or nil when there is none. Exact, unlike
	// MatchSkill: a fuzzy match is advice to a contributor, while this decides
	// whether a write is allowed.
	CollidesWith(ctx context.Context, slug string, aliases []string) (*SkillCollision, error)

	// RequestsSince returns a contributor's requests within a window, oldest
	// first, so the service can apply the rolling limit and say when it lifts.
	RequestsSince(ctx context.Context, userID domain.UserID, since time.Time) ([]SkillRequest, error)

	// CreateRequest inserts. It applies no policy: deduplication and rate
	// limiting are business rules and live in the service (CLAUDE.md).
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

// PairConflict is one (PR, skill) pair already evidenced by another claim.
type PairConflict struct {
	Link      PRLink
	SkillSlug string
	SkillName string
	ClaimID   domain.ClaimID
}

// SkillRequest is a proposed catalogue entry awaiting a decision.
type SkillRequest struct {
	ID           domain.RequestID
	RequestedBy  SkillRequester
	ProposedName string
	Rationale    string
	Status       string
	Reason       string
	CreatedAt    time.Time
	ReviewedAt   *time.Time
}

// EvaluationRepository owns judgements, scores and the job record.
type EvaluationRepository interface {
	// Record opens the evaluation a judgement belongs to, in the transaction
	// that persists it. The row is what AlreadyEvaluated later matches on, so
	// creating it separately would leave a window where a redelivery could not
	// tell a finished run from one that never started (ADR-0004).
	Record(ctx context.Context, tx Tx, e Evaluation) error

	// ActiveRubricVersion is the version scores are currently compared
	// against: the target of the most recent sweep, or the configured
	// fallback when none has run (RFC-0015).
	//
	// Read rather than configured, because search gates on it and a value that
	// lived in a flag could differ between two processes serving the same
	// corpus.
	ActiveRubricVersion(ctx context.Context, fallback string) (string, error)

	// OpenSweep returns the sweep still draining, or nil.
	OpenSweep(ctx context.Context) (*domain.RubricSweep, error)

	// RecordSweep writes the sweep, in the transaction that enqueues it. A
	// sweep row with nothing queued, or a queue with no sweep to explain it,
	// are both states nothing could account for.
	RecordSweep(ctx context.Context, tx Tx, s *domain.RubricSweep) (*domain.RubricSweep, error)

	// CompleteSweep closes a sweep whose corpus has drained.
	CompleteSweep(ctx context.Context, tx Tx, id domain.RequestID, at time.Time) error

	// SweptClaimsRemaining counts what a sweep has left to judge.
	//
	// Takes a Tx because the worker asks it immediately after acking its own
	// job: outside the transaction that ack is not yet visible, and the count
	// would never reach zero.
	SweptClaimsRemaining(ctx context.Context, tx Tx) (int, error)

	// SkillPRScores returns every surviving per-PR score a contributor holds
	// for one skill, across all their claims. The skill's score is the
	// accumulation of evidence, not a per-claim number (ADR-0007).
	// Takes a Tx: it is called mid-evaluation, after the link statuses this
	// query filters on have been written but before they are committed. The
	// pool would still see the previous run's verdicts.
	SkillPRScores(ctx context.Context, tx Tx, id domain.UserID, skillID domain.SkillID) ([]float64, error)

	// Persist writes every score, link status and standing change for one
	// evaluated claim in ONE transaction. A half-persisted evaluation would
	// leave a contributor with some skills promoted and others not.
	Persist(ctx context.Context, tx Tx, claimID domain.ClaimID, scores []domain.PRSkillScore, suggestions []domain.ClaimSkill) error

	Scores(ctx context.Context, claimID domain.ClaimID) ([]domain.PRSkillScore, error)
	Snapshot(ctx context.Context, tx Tx, s domain.ScoreSnapshot) error

	// AlreadyEvaluated makes redelivery free. The broker is at-least-once, so
	// the same job WILL arrive twice and the second time must cost nothing.
	AlreadyEvaluated(ctx context.Context, claimID domain.ClaimID, version int, rubricVersion string) (bool, error)

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
	// ByID resolves the subject of a verified access token. An admin decides
	// hirer verification, so a disabled one must stop acting at once rather
	// than when their token expires (ADR-0011).
	ByID(ctx context.Context, id domain.AdminID) (*domain.Admin, error)
	ByEmail(ctx context.Context, email string) (*domain.Admin, error)
	PasswordHash(ctx context.Context, id domain.AdminID) ([]byte, error)

	PendingVerifications(ctx context.Context) ([]VerificationRequest, error)
	DecideVerification(ctx context.Context, tx Tx, id domain.RequestID, by domain.AdminID, d domain.VerificationDecision) error

	// SeedAdmin creates the first admin if none exists, and only then.
	SeedAdmin(ctx context.Context, email string, passwordHash []byte) (bool, error)
}

// VerificationRequest is a hirer or organization awaiting review.
type VerificationRequest struct {
	ID domain.RequestID

	// The subject, resolved. An admin draining this queue is deciding about a
	// company and a person, and a pair of uuids tells them nothing — so the
	// repository joins rather than making the service fetch each one.
	//
	// ck_verification_single_subject means exactly one is set.
	Hirer        *VerificationHirer
	Organization *VerificationOrganization

	Status     string
	CreatedAt  time.Time
	ReviewedAt *time.Time

	// DecisionReason is what an admin wrote when refusing. Shown back to the
	// hirer, because "rejected" with no reason gives them nothing to fix.
	DecisionReason string

	// Proofs is the evidence attached to the request.
	//
	// An admin cannot decide a verification without seeing what was submitted,
	// and the two unstructured kinds are precisely the ones a small studio
	// depends on (ADR-0002).
	Proofs []domain.VerificationProof
}

// VerificationHirer is the seat awaiting review.
type VerificationHirer struct {
	ID          domain.HirerID
	DisplayName string
	Email       string
}

// VerificationOrganization is the company awaiting review.
type VerificationOrganization struct {
	ID   domain.OrganizationID
	Name string
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

	// OpenRequest returns the dispute a contributor already has in flight, or
	// nil. One at a time: a queue of disputes from one person is a way to
	// spend an admin's attention rather than to be heard.
	OpenRequest(ctx context.Context, id domain.UserID) (*domain.ReevaluationRequest, error)

	// ByStatus drains the admin queue for one status.
	ByStatus(ctx context.Context, status string) ([]domain.ReevaluationRequest, error)
}
