package port

import (
	"context"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Outbound adapters: the queue, the model, GitHub, auth primitives and the
// notifier.
//
// None of these names a vendor. The evaluator "receives messages, calls the AI
// through an interface, does arithmetic, and writes through the repository
// interface. It must never reference a specific queue or model vendor"
// (CLAUDE.md). ADR-0006 picks a Postgres-table queue and claude-opus-5 behind
// them; nothing above this file knows that.

// --- queue -------------------------------------------------------------------

// Broker is the work queue.
//
// Publish takes a Tx, and that is the whole point: the enqueue MUST join the
// transaction that created the work. Otherwise a claim can be submitted without
// its job, or a job can exist for a claim that was never written. This is the
// outbox property from ADR-0004, expressed in a signature so it cannot be
// forgotten.
type Broker interface {
	Publish(ctx context.Context, tx Tx, msg Message) error

	// Consume leases messages with FOR UPDATE SKIP LOCKED semantics, so two
	// workers never take the same job. Delivery is AT-LEAST-ONCE: the same job
	// will arrive twice and the handler must be idempotent.
	Consume(ctx context.Context, lease time.Duration, limit int) ([]Message, error)

	Ack(ctx context.Context, tx Tx, id domain.JobID) error

	// Nack returns a message for retry, or dead-letters it once attempts are
	// exhausted. A poisonous message that retried forever would starve the
	// queue behind it.
	Nack(ctx context.Context, id domain.JobID, reason string) error

	// ExtendLease keeps a long-running job from being redelivered underneath
	// the worker still processing it.
	ExtendLease(ctx context.Context, id domain.JobID, by time.Duration) error
}

// Message is one unit of queued work.
type Message struct {
	ID       domain.JobID
	Kind     string
	ClaimID  domain.ClaimID
	Version  int
	Payload  []byte
	Attempts int
}

// --- model -------------------------------------------------------------------

// AIClient judges evidence.
//
// One call judges a whole claim — every PR against every declared skill —
// because the model needs the bundle to weigh a PR against its siblings, and
// because a call per pair would multiply the bill by the product of both counts
// (ADR-0004).
type AIClient interface {
	// Judge submits a claim and returns the judgements. Implementations batch;
	// the caller does not know or care.
	Judge(ctx context.Context, req JudgeRequest) (*JudgeResponse, error)
}

// JudgeRequest is everything the model is shown.
type JudgeRequest struct {
	ClaimID          domain.ClaimID
	RubricVersion    string
	ScoringMode      domain.ScoringMode
	NominatedPrimary string
	Skills           []domain.Skill
	Evidence         []domain.PREvidence
	Projects         []domain.ProjectEvidence
}

// JudgeResponse is the structured output.
//
// Refused reflects a stop_reason of "refusal", which arrives as an HTTP 200 —
// a caller checking only the status code would treat a refusal as a valid
// empty judgement and score the claim zero.
type JudgeResponse struct {
	Judgements   []domain.Judgement
	Suggestions  []domain.ClaimSkill
	Refused      bool
	InputTokens  int
	OutputTokens int
	DurationMS   int
}

// --- github ------------------------------------------------------------------

// GitHubClient fetches the facts evidence is validated and enriched against.
type GitHubClient interface {
	// AuthorizeURL builds the redirect a contributor is sent to. The adapter
	// owns the client id and the scopes; a service that assembled this would
	// be holding configuration that belongs here.
	AuthorizeURL(state string) string

	// ExchangeCode completes the OAuth flow and returns the identity. This is
	// the only path to a contributor account.
	ExchangeCode(ctx context.Context, code string) (*GitHubIdentity, error)

	PullRequest(ctx context.Context, owner, repo string, number int) (*domain.PRFacts, error)
	Repository(ctx context.Context, owner, repo string) (*domain.RepoFacts, error)

	// Reviews backs the pr-review scoring mode, where the claimant must have
	// reviewed the PR and must not be its author.
	Reviews(ctx context.Context, owner, repo string, number int) ([]Review, error)
}

// GitHubIdentity is what an OAuth exchange yields. Identity is the immutable
// numeric id, never the login — a login can be changed or reused.
type GitHubIdentity struct {
	GitHubUserID int64
	Login        string
	Name         string
	Email        string
}

// Review is one review left on a pull request.
type Review struct {
	AuthorUserID int64
	State        string
	Body         string
	CommentCount int
	SubmittedAt  time.Time
}

// OAuthProvider is the hirer-side identity provider (Google today).
//
// A verified email is a precondition rather than a field to inspect: anyone can
// put any address in a profile they control, so an implementation that cannot
// prove verification must return an error rather than an unverified identity.
type OAuthProvider interface {
	AuthorizeURL(state string) string
	Exchange(ctx context.Context, code string) (*OAuthIdentity, error)
}

// OAuthIdentity is a verified third-party identity.
type OAuthIdentity struct {
	Subject string
	Email   string
	Name    string
}

// --- auth primitives ---------------------------------------------------------

// AccessClaims is everything an access token asserts (ADR-0011): identity, and
// nothing else. No organization, no hiring capability, no verification state —
// embedding any of those would let a suspended organization keep acting until
// the token expired, when RequireHiringCapability exists to revoke immediately.
type AccessClaims struct {
	// Subject is the account id, stringified. Which id type it parses back to
	// depends on Kind, because the three account types share no table.
	Subject string
	Kind    domain.PrincipalKind

	// Family ties the token to the session that issued it.
	//
	// Without it, logging out revokes the refresh chain and leaves the access
	// token working for up to fifteen minutes — which is not logging out. It
	// is the family rather than the session because rotation replaces the
	// session row and must not invalidate a token already in flight.
	Family string
}

// TokenIssuer mints and verifies access tokens.
//
// It deals in claims rather than domain.Principal, and the distinction is
// load-bearing. A Principal carries a whole *Contributor — scores, availability,
// bio — which a 15-minute bearer token cannot hold, so middleware reads the
// entity from Postgres on every request regardless (ADR-0011). Taking a
// Principal here would mean callers passing a half-filled one, and a
// *Contributor with a zeroed OverallScore is indistinguishable from a measured
// zero.
//
// Refresh tokens are NOT here. They are opaque random bytes, which is exactly
// what TokenMinter makes, and one generator means one place where entropy and
// hash algorithm are decided.
type TokenIssuer interface {
	Issue(ctx context.Context, c AccessClaims, ttl time.Duration) (string, error)
	Verify(ctx context.Context, token string) (*AccessClaims, error)
}

// PasswordHasher is the password primitive, for the email provider only.
//
// Verify takes both so the comparison stays constant-time inside the
// implementation; a caller that fetched the hash and compared it itself would
// leak timing.
type PasswordHasher interface {
	Hash(plaintext string) ([]byte, error)
	Verify(hash []byte, plaintext string) bool
}

// TokenMinter generates the opaque single-use tokens behind share links and
// invitations: 32 random bytes, shown once, stored as a hash.
type TokenMinter interface {
	Mint() (plaintext string, hash []byte, err error)
	Hash(plaintext string) []byte
}

// --- places ------------------------------------------------------------------

// PlaceService answers what countries exist (ADR-0018 §9).
//
// One method, deliberately. A COUNTRY CODE is a closed vocabulary two systems
// must agree on — it is matched against a role's eligible countries, so "UK"
// and "GB" being different strings is a bug. A postcode is matched against
// nothing, is read by a person and posted to by a courier, and is not looked
// up at all.
//
// Callers must FAIL OPEN. A provider that is down falls back to accepting any
// well-formed alpha-2 code: nothing here is a security decision, and a signup
// path that depends on a third party's uptime stops working on their bad day.
type PlaceService interface {
	Countries(ctx context.Context) ([]domain.Country, error)
}

// MoneyService answers what currencies exist (ADR-0018 amendment 3).
//
// Separate from PlaceService rather than a second method on it: a currency is
// not a place. They are wired to the same stand-in today, but a live vendor
// for one is not a live vendor for the other, and an interface that bundled
// them would make choosing a country provider a decision about salaries.
//
// Callers must FAIL OPEN, exactly as they do for countries. A provider that is
// down falls back to accepting any well-formed alpha-3 code: a hirer writing a
// role at 2am does not care whose uptime the picker depends on.
type MoneyService interface {
	Currencies(ctx context.Context) ([]domain.Currency, error)
}

// --- notifier ----------------------------------------------------------------

// Notifier delivers email.
//
// Called AFTER commit, never inside a transaction: an email cannot be rolled
// back, so sending one for a transaction that then fails tells a contributor
// something that did not happen.
type Notifier interface {
	Send(ctx context.Context, n Notification) error
}

// NotificationKind names what is being sent.
type NotificationKind string

// The messages the platform sends. Each maps to one template in the notifier
// implementation, which is why a service never composes markup.
const (
	NotifyContactRequest      NotificationKind = "contact_request"
	NotifyContactAccepted     NotificationKind = "contact_accepted"
	NotifyEvaluationComplete  NotificationKind = "evaluation_complete"
	NotifyAvailabilityLapsing NotificationKind = "availability_lapsing"
	NotifyEmailVerification   NotificationKind = "email_verification"

	// NotifyOnboardingVerification proves a COMPANY address (ADR-0017 §2).
	//
	// Its own kind rather than a reuse of NotifyEmailVerification: the two
	// carry the reader somewhere different — one to claim a seat, one to
	// finish describing a company — and a template that had to ask which
	// would be one template serving two messages.
	NotifyOnboardingVerification NotificationKind = "onboarding_verification"

	// NotifyOnboardingDecided is an administrator's answer on a submitted
	// company: approved and ready to sign in, or refused with a reason and a
	// way to correct it (ADR-0017 §8).
	NotifyOnboardingDecided NotificationKind = "onboarding_decided"

	NotifyVerificationDecided NotificationKind = "verification_decided"
	NotifySkillRequestDecided NotificationKind = "skill_request_decided"
	NotifyReevaluationDecided NotificationKind = "reevaluation_decided"
	NotifyOverdueShortlists   NotificationKind = "overdue_shortlists"
)

// Notification is one message. Data is rendered by the implementation, so a
// service never composes markup.
type Notification struct {
	Kind      NotificationKind
	Recipient string
	Data      map[string]any
}
