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
	LoginHirer(ctx context.Context, username, password string) (*domain.Hirer, *domain.TokenPair, error)
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

// RegisterHirerRequest is an INDEPENDENT hirer signing up — someone hiring on
// their own account rather than a company's (ADR-0017 §1).
type RegisterHirerRequest struct {
	DisplayName string

	// Username is the sign-in name, and is required (ADR-0016 §1).
	//
	// ADR-0016 tabulates only the roster path, where the organization pins the
	// name. Self-serve registration has no organization to pin it, so the
	// registrant chooses — reported to the Planner as a gap in the ADR rather
	// than settled here.
	Username string

	Email    string
	Password string

	// No organisation. Registration creates an INDEPENDENT hirer and nothing
	// else; onboarding is the only thing that creates a company
	// (ADR-0017 §1). Someone whose company is already here was rostered and
	// uses /auth/hirer/redeem instead.
	Proofs []domain.VerificationProof
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
	// AddToRoster names an address, a username and a role. Owners only: an
	// owner entry grants the power to grant further seats, so a member who
	// could roster could promote themselves (ADR-0016 §7).
	AddToRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, email, username string, role domain.OrgRole) (*RosterEntry, error)

	ListRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID) ([]RosterEntry, error)

	// RemoveFromRoster deletes the entry AND revokes the seat it created, if
	// one exists. It never deletes the seat: that row authored shortlists and
	// the permanent record of who was told (ADR-0016 §5).
	RemoveFromRoster(ctx context.Context, p domain.Principal, orgID domain.OrganizationID, id domain.RosterEntryID) error

	// ListSeats returns live and revoked seats, readable by any hirer in the
	// organization — every seat already sees every round, so naming the author
	// exposes no row that was not already visible (ADR-0016 §5a).
	ListSeats(ctx context.Context, p domain.Principal, orgID domain.OrganizationID) ([]domain.Hirer, error)

	// Verification tells a hirer where their own review stands.
	//
	// Its own method rather than a field on Me: a hirer checks this while
	// waiting, and folding it into every profile read would cost a second
	// query on requests that never look at it.
	Verification(ctx context.Context, p domain.Principal) (*VerificationStatus, error)
}

// RedemptionService turns a roster entry into a seat, and proves addresses.
//
// Separate from OrganizationService because its callers hold no session: a
// person redeeming an entry has no account yet, and the endpoints are
// unauthenticated by necessity.
type RedemptionService interface {
	// StartRedemption answers the same way for a roster miss and an unknown
	// organization: telling them apart turns the endpoint into an oracle for
	// who an organization is hiring (ADR-0016 §3).
	StartRedemption(ctx context.Context, orgSlug, email string) error

	// CompleteRedemption consumes the proof and creates the seat. The username
	// and role come from the entry, not from the caller.
	CompleteRedemption(ctx context.Context, token, displayName, password string) (*domain.Hirer, *domain.TokenPair, error)

	// ResendVerification re-sends an outstanding proof rather than minting a
	// second live one. Silent about whether anything was found, for the same
	// reason StartRedemption is.
	ResendVerification(ctx context.Context, email string) error

	// ListOrganizations backs the picker a redeemer finds their employer in
	// (ADR-0016 §3a).
	//
	// It lives here rather than on OrganizationService because it is the one
	// organization read with no principal behind it, and the caller it serves
	// is mid-redemption.
	ListOrganizations(ctx context.Context) ([]domain.Organization, error)
}

// OnboardingService owns the path from a submitted form to a real company.
//
// Every method here is UNAUTHENTICATED except the queue and the decision,
// necessarily: the person describing a company has no account, and the whole
// point of ADR-0017 is that they do not get one until an administrator agrees.
type OnboardingService interface {
	// Submit records a form and emails a code. It creates no organisation, no
	// seat, and reserves no slug (ADR-0017 §2).
	//
	// Returns nothing. A caller told "that name is taken" could enumerate the
	// companies mid-onboarding before the public picker would list them.
	Submit(ctx context.Context, in OnboardingForm) error

	// Verify spends the code, claims the username and records how the owner
	// will sign in. It returns no session: there is no account to sign in as
	// until an administrator approves (ADR-0017 §6).
	Verify(ctx context.Context, token string, owner OnboardingOwnerInput) error

	// Revise records a correction to a REJECTED submission as a new row
	// pointing at the old one. The table is append-only, so an administrator
	// can see what changed (ADR-0017 §8).
	Revise(ctx context.Context, token string, in OnboardingForm) error

	// Queue is the administrator's view: proven, undecided, oldest first.
	Queue(ctx context.Context, p domain.Principal) ([]OnboardingSubmission, error)

	// Decide approves or refuses. Approving creates the organisation, its
	// address, the owner seat and the membership in one transaction — and can
	// FAIL, because two submissions may name one company and only the first
	// approved can have the slug.
	Decide(ctx context.Context, p domain.Principal, id domain.OnboardingID, approve bool, reason string) error

	// ExpireUnproven deletes submissions whose code lapsed unused. Driven by
	// cmd/jobs, like every other sweep.
	ExpireUnproven(ctx context.Context) (int, error)
}

// OnboardingForm is a company describing itself.
//
// Every field but Name and Email is optional: a fully remote company has no
// office to name, a registered address is often an accountant's rather than the
// company's, and several countries have no postcode (ADR-0017 §5).
type OnboardingForm struct {
	Name        string
	Description string
	Email       string
	Phone       string
	Headcount   domain.HeadcountBand

	Country    string
	City       string
	PostalCode string
	Street1    string
	Street2    string
}

// OnboardingOwnerInput is how the first person at a company chooses to sign in.
//
// The username is chosen here, unlike roster redemption where the organisation
// pins it — there is no organisation yet to pin it (ADR-0017 §Identity).
type OnboardingOwnerInput struct {
	Username    string
	DisplayName string
	Password    string
}

// ProfileService owns a contributor's own account of themselves (ADR-0018).
type ProfileService interface {
	// A domain.UserID rather than a principal, matching every other
	// contributor-facing service: extracting the id is the controller's job,
	// and taking it directly leaves these callable from a worker that holds no
	// principal at all (see the note above requireAdmin).

	// Profile returns work preferences, country, both year figures, and
	// whether anything could match this person at all.
	Profile(ctx context.Context, id domain.UserID) (*ContributorProfile, error)

	SaveProfile(ctx context.Context, id domain.UserID, in domain.WorkPreferences) (*ContributorProfile, error)

	// Compensation is returned to THE PERSON WHO TYPED IT and to nobody else.
	// There is no hirer-facing caller and there must never be (ADR-0018 §2).
	Compensation(ctx context.Context, id domain.UserID) (*domain.Compensation, error)
	SaveCompensation(ctx context.Context, id domain.UserID, in domain.Compensation) (*domain.Compensation, error)
}

// ContributorProfile is the profile screen's whole answer.
//
// Availability travels with it because the two COMPOSE (ADR-0018 §4): a person
// is available for the shapes they enabled and for nothing else, so showing
// the flags without the window would show half a sentence.
type ContributorProfile struct {
	Preferences  domain.WorkPreferences
	Availability *domain.Availability

	// NeedsAttention is whether this person should be PROMPTED to fill the
	// form in. True when they have never stated anything at all.
	//
	// Separate from Matchable, which can be false for a perfectly deliberate
	// reason — somebody who is not looking. Nobody should be nagged about an
	// answer they have given.
	NeedsAttention bool

	// Matchable is false when the window is live and no flag is enabled — a
	// state reachable by accident and invisible from outside (ADR-0018
	// §The unmatchable state). Computed here rather than left to a client,
	// because a client that forgot would leave somebody wondering why nobody
	// ever writes to them.
	Matchable bool
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
	Create(ctx context.Context, p domain.Principal, role domain.RoleID, name, description string, date time.Time) (*domain.Shortlist, error)
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

	// ExpireOnboarding deletes submitted companies whose code lapsed unused.
	//
	// The only sweep that DELETES rather than stamping a column. A submission
	// that was never proven can never be proven — its code is gone — so it is
	// not stale data, it is data that has become unreachable (ADR-0017 §2).
	ExpireOnboarding(ctx context.Context) (int, error)

	// VerifyPendingPRs re-attempts the pull-request fetches that could not be
	// completed (ADR-0019 §When the fetch fails).
	//
	// A job rather than a request, because the contributor whose save hit a
	// rate limit is not the person who should have to notice. Until it lands,
	// their open-source years read as unknown and clear no minimum.
	VerifyPendingPRs(ctx context.Context) (verified int, err error)
}

// RoleService owns openings (ADR-0019).
type RoleService interface {
	// Create writes a DRAFT. A role is never born open: opening is a separate
	// act, which is what makes the authority setting enforceable at all.
	Create(ctx context.Context, p domain.Principal, org domain.OrganizationID, in domain.Role) (*domain.Role, error)

	// Update replaces a DRAFT's contents. An open role is refused: the two are
	// different acts with different consequences, and a client that meant one
	// must not silently get the other.
	Update(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Role) (*domain.Role, error)

	// Revise publishes a successor to an OPEN role and closes the original, so
	// everybody already contacted keeps reading the role they were shown.
	Revise(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Role) (*domain.Role, error)

	Open(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Role, error)

	// Close ends a role and records WHY. A seat that may stage but not commit
	// gets its request recorded and the role stays open.
	Close(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in CloseRequest) (*domain.Role, error)

	List(ctx context.Context, p domain.Principal, org domain.OrganizationID, status *domain.RoleStatus) ([]domain.Role, error)
	Role(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Role, error)

	// Candidates is everybody already on a round for this role — the list a
	// hirer reads before searching again, and the list a closure picks its
	// hires from.
	Candidates(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) ([]domain.RoleCandidate, error)

	// --- public openings (ADR-0020) ---------------------------------------

	// Opening reads the advert on a role, or ErrNotFound when it has none.
	Opening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error)

	// SaveOpening writes the BAR. It does not publish.
	SaveOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Opening) (*domain.Opening, error)

	// PublishOpening makes it visible to contributors. Only an OPEN role may
	// have one: a draft is not a commitment and a closed role is a withdrawn
	// job, so neither is something anybody should be reading about.
	PublishOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error)
	WithdrawOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error)

	// Openings is the CONTRIBUTOR'S read: what they clear, and a count of what
	// they do not. Reading it sends nothing to anybody (ADR-0020 §8).
	Openings(ctx context.Context, id domain.UserID, limit, offset int) (*OpeningResults, error)

	Settings(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*domain.OrgSettings, error)
	SaveSettings(ctx context.Context, p domain.Principal, org domain.OrganizationID, in domain.OrgSettings) (*domain.OrgSettings, error)
}

// CloseRequest is what a hirer says when they close a role.
//
// HiredEmails carries ADDRESSES rather than ids because that is what a hirer
// has: they know who they hired, not what this platform calls them. The service
// resolves each against the contact requests the organisation was already
// given, and stores ids.
type CloseRequest struct {
	Reason domain.CloseReason

	// Note is required when Reason is CloseOther.
	Note string

	// Hired is non-empty exactly when Reason is CloseHiredViaPlatform, and
	// every id must be a candidate on THIS role who accepted.
	//
	// Ids picked off the role's own candidate list, not addresses typed in.
	// A hirer knows who they hired, and making them retype an email was both
	// worse to use and — until the released-address rule was added — a way to
	// test whether an arbitrary address had an account here. Choosing from a
	// list they already hold removes the question entirely.
	Hired []domain.UserID
}

// ProfileVerifier re-establishes pull request dates that could not be read the
// first time (ADR-0019 §7).
//
// A narrow interface of its own rather than a method on ProfileService: the job
// binary needs exactly this one call, and handing it the whole profile surface
// would let a sweep save somebody's compensation by accident.
type ProfileVerifier interface {
	VerifyPending(ctx context.Context, limit int) (int, error)
}
