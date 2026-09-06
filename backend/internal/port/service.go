package port

import (
	"context"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Services own the business logic.
//
// A service talks to repository INTERFACES and must not know which database is
// behind them. It receives a domain.Principal and decides access — controllers
// never do (ADR-0002).
//
// Every method takes and returns domain types or the small request/result
// structs below. Nothing here mentions http.Request, a status code, or JSON:
// the same service must be callable from a CLI or a worker without pretending
// to be an HTTP handler.

// AccessService holds the two gates ADR-0002 requires as named, individually
// tested functions. They are named methods rather than inline checks precisely
// so they can be tested once and reused at every call site.
type AccessService interface {
	// RequireHiringCapability fails unless the hirer is verified AND their
	// organization is verified.
	RequireHiringCapability(ctx context.Context, p domain.Principal) error

	// AssertNotSelf fails if the target contributor shares a GitHub identity
	// with this hirer. Called in search, scorecard read and shortlist add —
	// search applies it as a SQL exclusion so a self-match never reaches a
	// result count.
	AssertNotSelf(ctx context.Context, hirer domain.HirerID, target domain.UserID) error
}

// AuthService owns sign-in, rotation and revocation.
type AuthService interface {
	GitHubAuthorizeURL(ctx context.Context) (url string, state string, err error)
	// CompleteGitHub creates the contributor on first callback and resolves to
	// the existing account on every later one, keyed on the immutable numeric
	// GitHub id rather than the login.
	CompleteGitHub(ctx context.Context, code, state, expectedState string) (*domain.Contributor, *domain.TokenPair, bool, error)

	// CompleteGitHubHirer signs in a seat whose provider is GitHub.
	//
	// Separate from CompleteGitHub because the namespaces are separate: one
	// identity may own a contributor and a hirer, and the callback cannot
	// guess which the caller meant. It NEVER creates — like the Google path,
	// registration is what raises the verification request.
	CompleteGitHubHirer(ctx context.Context, code, state, expectedState string) (*domain.Hirer, *domain.TokenPair, error)

	GoogleAuthorizeURL(ctx context.Context) (url string, state string, err error)
	// CompleteGoogle never auto-creates an account: that would bypass
	// registration and therefore bypass the verification request.
	CompleteGoogle(ctx context.Context, code, state, expectedState string) (*domain.Hirer, *domain.TokenPair, error)

	// RegisterHirer creates the account and queues it for review. It does NOT
	// sign the registrant in: an unverified hirer can do nothing on the
	// platform, so a token pair here would be a credential with no use that
	// still has to be stored and expired.
	RegisterHirer(ctx context.Context, req RegisterHirerRequest) (*HirerRegistration, error)
	LoginHirer(ctx context.Context, email, password string) (*domain.Hirer, *domain.TokenPair, error)
	LoginAdmin(ctx context.Context, email, password string) (*domain.Admin, *domain.TokenPair, error)

	// ResolvePrincipal turns a verified token's claims into the account acting.
	//
	// The token asserts identity and nothing else (ADR-0011), so the account is
	// read on every authenticated request — which is what makes revocation
	// immediate rather than delayed by the token's remaining lifetime. A
	// disabled or deleted account resolves to an error, never to a principal.
	ResolvePrincipal(ctx context.Context, claims AccessClaims) (*domain.Principal, error)

	// Refresh rotates. Presenting a spent token revokes the whole family,
	// including the successor currently in use.
	Refresh(ctx context.Context, refreshToken string) (*domain.TokenPair, error)

	// Logout revokes the family, not the presented token, so a refresh token
	// stolen before sign-out is dead afterwards.
	Logout(ctx context.Context, p domain.Principal) error

	Me(ctx context.Context, p domain.Principal) (*domain.Principal, error)
	SetAvailability(ctx context.Context, id domain.UserID, status domain.AvailabilityStatus) (*domain.Availability, error)

	MintShareLink(ctx context.Context, id domain.UserID) (token string, linkID domain.ShareLinkID, err error)
	RevokeShareLink(ctx context.Context, id domain.UserID, linkID domain.ShareLinkID) error
	PublicScorecard(ctx context.Context, token string) (*domain.Scorecard, error)
}

// RegisterHirerRequest is a new organization signing up.
type RegisterHirerRequest struct {
	DisplayName      string
	Email            string
	Password         string
	OrganizationName string
	Website          string
	LinkedInURL      string
	Proofs           []domain.VerificationProof
}

// HirerRegistration is what a completed registration produced.
//
// The queued request is returned rather than left to be looked up, because the
// only useful thing a new registrant can do is watch it — and telling them its
// id in the same response is what makes that possible.
type HirerRegistration struct {
	Hirer               *domain.Hirer
	VerificationRequest *VerificationRequest
}

// OrganizationService owns seats. An org self-administers them, with no admin
// involvement (ADR-0002).
type OrganizationService interface {
	Invite(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, email string, role domain.OrgRole) (*Invitation, string, error)
	AcceptInvitation(ctx context.Context, token, displayName, password string) (*domain.Hirer, *domain.TokenPair, error)

	// Verification tells a hirer where their own review stands.
	//
	// Its own method rather than a field on Me: a hirer checks this while
	// waiting, and folding it into every profile read would cost a second
	// query on requests that never look at it.
	Verification(ctx context.Context, p domain.Principal) (*VerificationStatus, error)
}

// VerificationStatus is a hirer's view of their own review.
//
// HirerVerified and the organization's verification are reported separately
// because they are separate gates, and a seat told only "not verified" cannot
// tell whether to wait for their own review or their organization's.
type VerificationStatus struct {
	Status        string
	HirerVerified bool
	Organization  *domain.Organization
	SubmittedAt   time.Time
	ReviewedAt    *time.Time
	Reason        string
}

// Demotion is one skill a withdrawal would drop out of ranking.
//
// DistinctPRCountAfter is what the count becomes once this claim's evidence is
// removed — the number that decides the demotion, not the one the contributor
// currently has. Reporting the current count would show a figure at or above
// the threshold next to a warning that the threshold was missed.
type Demotion struct {
	Skill                domain.UserSkill
	DistinctPRCountAfter int
}

// ClaimService owns the claim lifecycle and the validation pipeline.
type ClaimService interface {
	Create(ctx context.Context, id domain.UserID) (*domain.Claim, error)
	Get(ctx context.Context, id domain.UserID, claimID domain.ClaimID) (*domain.Claim, error)
	List(ctx context.Context, id domain.UserID) ([]ClaimSummary, error)

	// Replace is the whole-claim edit. It refuses while the seven-day lock is
	// in force and on a stale version.
	Replace(ctx context.Context, id domain.UserID, claimID domain.ClaimID, version int, c *domain.Claim) (*domain.Claim, error)

	SetPREvidence(ctx context.Context, id domain.UserID, claimID domain.ClaimID, prs []domain.PREvidence) (*domain.Claim, error)
	SetProjectEvidence(ctx context.Context, id domain.UserID, claimID domain.ClaimID, projects []domain.ProjectEvidence) (*domain.Claim, error)
	SetSkills(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skills []domain.ClaimSkill) (*domain.Claim, error)

	// Submit runs the validation pipeline in order — cheap local failures
	// before API calls — then writes the pair links and enqueues the job in ONE
	// transaction. A failed validation must never spend a model call.
	Submit(ctx context.Context, id domain.UserID, claimID domain.ClaimID) (*domain.Claim, []ValidationFailure, error)

	// WithdrawPreview names the skills that would demote, so withdrawal warns
	// before it costs something.
	WithdrawPreview(ctx context.Context, id domain.UserID, claimID domain.ClaimID) ([]Demotion, error)
	Withdraw(ctx context.Context, id domain.UserID, claimID domain.ClaimID, confirmDemotion bool) (*domain.Claim, error)

	DecideSuggestion(ctx context.Context, id domain.UserID, claimID domain.ClaimID, skillID domain.SkillID, accept bool) (*domain.SuggestionDecision, error)
}

// ValidationFailure names one failing evidence row. Every failure is reported,
// not just the first: the contributor is doing curation work and a vague
// rejection wastes it.
type ValidationFailure struct {
	Position int
	Reason   domain.EvidenceInvalidReason
	Message  string

	// Set on a pair conflict, naming the skill the triple is already spent on
	// and the claim that owns it. Both are needed to act: the contributor has
	// to know WHICH skill this PR already evidences before deciding whether to
	// drop the row or the skill (ADR-0007).
	Skill              string
	ConflictingClaimID *domain.ClaimID
}

// SkillService owns the catalogue and skill requests.
type SkillService interface {
	Search(ctx context.Context, query string) ([]SkillMatch, error)
	RequestSkill(ctx context.Context, id domain.UserID, proposedName, rationale string) (domain.RequestID, error)
	// MySkills is the contributor's own standing, with the context that makes
	// a score readable: which rubric produced it, whether that rubric has since
	// moved, and whether a dispute is already in flight.
	MySkills(ctx context.Context, id domain.UserID) (*SkillStanding, error)
}

// SkillStanding is a contributor's own view of their skills.
type SkillStanding struct {
	Skills []domain.UserSkill

	// Nil until a skill reaches primary standing. Null is not zero: zero would
	// claim we measured something (ADR-0007).
	OverallScore    *float64
	GeneralistScore *float64

	// RubricVersion is what these SCORES were produced under — which, mid
	// sweep, is not what the platform scores under now. Labelling superseded
	// numbers with the current version would claim they had been re-judged.
	RubricVersion string

	// ActiveRubricVersion is what the platform scores under now. Carried
	// alongside rather than instead: staleness is the comparison between the
	// two, and a caller that had only one could not make it.
	ActiveRubricVersion string

	// Stale reports that at least one skill was judged under an older rubric,
	// so the numbers below are not comparable with a current leaderboard until
	// the sweep reaches them (ADR-0004).
	Stale bool

	// ReevaluationInProgress stops a contributor raising a second dispute
	// while the first is open, and tells them why.
	ReevaluationInProgress bool
}

// EvaluationService is the evaluator's business logic: judge, compute, persist.
//
// It is the only place the arithmetic lives. Disqualification and the quality
// floor short-circuit it entirely — a typo fix scores 0, not the ~18 the
// weighted formula floors at in a popular repository.
type EvaluationService interface {
	// Process handles one queued message. It MUST be idempotent: delivery is
	// at-least-once and redelivery has to be free.
	Process(ctx context.Context, msg Message) error

	// Sweep re-queues the corpus when the rubric changes. Enqueue-only: a
	// half-swept corpus mixes versions, and a leaderboard mixing them ranks
	// people by which version happened to judge them.
	Sweep(ctx context.Context, p domain.Principal, toVersion, reason string) (*domain.RubricSweep, error)
}

// DiscoveryService owns search, leaderboards, scorecards and rank.
type DiscoveryService interface {
	Search(ctx context.Context, p domain.Principal, q domain.SearchQuery) (*domain.SearchResults, error)
	Leaderboard(ctx context.Context, p domain.Principal, kind domain.LeaderboardKind, skill string) (*domain.Leaderboard, error)
	Scorecard(ctx context.Context, p domain.Principal, target domain.UserID) (*domain.Scorecard, error)

	// MyRank returns positions and totals and nothing identifying anyone else.
	MyRank(ctx context.Context, id domain.UserID) (*domain.Rank, error)

	SaveSearch(ctx context.Context, p domain.Principal, name string, q domain.SearchQuery) (*domain.SavedSearch, error)
	SavedSearches(ctx context.Context, p domain.Principal) ([]domain.SavedSearch, error)
	// ReplaySavedSearch evaluates the stored filters AS THE CALLER. A saved
	// search stores a question, never an answer.
	ReplaySavedSearch(ctx context.Context, p domain.Principal, id domain.SavedSearchID) (*domain.SearchResults, error)
	DeleteSavedSearch(ctx context.Context, p domain.Principal, id domain.SavedSearchID) error
}

// ShortlistService owns hiring rounds and the two-phase disclosure.
type ShortlistService interface {
	Create(ctx context.Context, p domain.Principal, name, description string, date time.Time) (*domain.Shortlist, error)
	List(ctx context.Context, p domain.Principal, status *domain.ShortlistStatus) ([]domain.Shortlist, error)
	Get(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*domain.Shortlist, error)
	Update(ctx context.Context, p domain.Principal, id domain.ShortlistID, name, description *string, date *time.Time) (*domain.Shortlist, error)
	Close(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*domain.Shortlist, error)

	// AddEntry stages and discloses nothing. AssertNotSelf fires HERE rather
	// than at confirm: discovering an ineligible candidate at send time is too
	// late.
	AddEntry(ctx context.Context, p domain.Principal, id domain.ShortlistID, target domain.UserID, note string) (*domain.ShortlistEntry, error)
	RemoveEntry(ctx context.Context, p domain.Principal, id domain.ShortlistID, target domain.UserID) error

	// Confirm notifies every unnotified entry and is IRREVERSIBLE.
	Confirm(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*ConfirmResult, error)

	ContactRequests(ctx context.Context, p domain.Principal, id domain.ShortlistID) ([]domain.ContactRequest, error)
}

// ConfirmResult reports what one confirm actually sent. AlreadyNotified is
// returned so a second confirm can be seen to have sent nothing.
type ConfirmResult struct {
	Shortlist       *domain.Shortlist
	Notified        int
	AlreadyNotified int
	Requests        []domain.ContactRequest
}

// ContactService is the contributor's side of consent.
type ContactService interface {
	List(ctx context.Context, id domain.UserID, status *domain.ContactRequestStatus) ([]domain.ContactRequest, error)
	// Respond releases the email on acceptance and refreshes the contributor's
	// availability window — answering yes is a clearer statement of
	// availability than the button they forgot to click.
	Respond(ctx context.Context, id domain.UserID, contact domain.ContactID, accept bool) (*domain.ContactRequest, error)
}

// AdminService owns the queues only an admin may drain.
type AdminService interface {
	PendingVerifications(ctx context.Context, p domain.Principal) ([]VerificationRequest, error)
	DecideVerification(ctx context.Context, p domain.Principal, id domain.RequestID, d domain.VerificationDecision) error

	PendingSkillRequests(ctx context.Context, p domain.Principal, status string) ([]SkillRequest, error)
	DecideSkillRequest(ctx context.Context, p domain.Principal, id domain.RequestID, approve bool, skill *domain.Skill, reason string) (*domain.Skill, error)

	Reevaluations(ctx context.Context, p domain.Principal, status string) ([]domain.ReevaluationRequest, error)
	DecideReevaluation(ctx context.Context, p domain.Principal, id domain.RequestID, accept bool, reason string) error
}

// ReevaluationService owns the contributor's side of disputes.
type ReevaluationService interface {
	Request(ctx context.Context, id domain.UserID, claimID domain.ClaimID, reason string) (*domain.ReevaluationRequest, error)
	// Status makes the cooldown legible BEFORE a contributor spends a request
	// on a 429.
	Status(ctx context.Context, id domain.UserID) (*domain.DisputeStanding, error)
}

// JobService is the set of scheduled tasks. They are exposed as an interface so
// the e2e harness can drive each one synchronously instead of sleeping.
type JobService interface {
	ExpireAvailability(ctx context.Context) (reminded int, err error)
	FlagOverdueShortlists(ctx context.Context) (flagged int, err error)
	ExpireContactRequests(ctx context.Context) (expired int, err error)
	RecomputeNorms(ctx context.Context) error
}
