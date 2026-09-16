package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// AuthService owns sign-in, rotation and revocation.
//
// Three account types with three separate entry points (ADR-0002).
// Contributors exist only through GitHub; hirers sign in by email or Google;
// admins have their own endpoint and no OAuth path at all, because an admin
// decides hirer verification and an account mintable by whoever controls an
// identity provider would make that decision worth nothing.
type AuthService struct {
	users      port.UserRepository
	hirers     port.HirerRepository
	admins     port.AdminRepository
	sessions   port.SessionRepository
	github     port.GitHubClient
	google     port.OAuthProvider
	tokens     port.TokenIssuer
	minter     port.TokenMinter
	hasher     port.PasswordHasher
	shareLinks port.ShareLinkRepository
	search     port.SearchRepository
	tx         port.TxManager
	clock      port.Clock

	// rubric is the version this process scores under. A scorecard is only
	// comparable within one version (ADR-0005), so every card says which.
	rubric string
}

// NewAuthService wires authentication.
func NewAuthService(
	users port.UserRepository,
	hirers port.HirerRepository,
	admins port.AdminRepository,
	sessions port.SessionRepository,
	github port.GitHubClient,
	google port.OAuthProvider,
	tokens port.TokenIssuer,
	minter port.TokenMinter,
	hasher port.PasswordHasher,
	shareLinks port.ShareLinkRepository,
	search port.SearchRepository,
	tx port.TxManager,
	clock port.Clock,
	rubric string,
) *AuthService {
	return &AuthService{users: users, hirers: hirers, admins: admins, sessions: sessions,
		github: github, google: google, tokens: tokens, minter: minter, hasher: hasher,
		shareLinks: shareLinks, search: search, tx: tx, clock: clock, rubric: rubric}
}

// Token lifetimes from ADR-0002.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// ErrInvalidCredentials is every sign-in failure.
//
// One error for a wrong password, an unknown email, and an email belonging to
// a different account type. Distinguishing them would turn sign-in into an
// enumeration oracle over which companies are recruiting.
var ErrInvalidCredentials = errors.New("invalid credentials")

// GitHubAuthorizeURL starts the contributor flow.
//
// The URL is built by the GitHub adapter, which owns the client id and the
// scopes — a service that assembled it would be holding configuration that
// belongs to the adapter.
func (s *AuthService) GitHubAuthorizeURL(ctx context.Context) (string, string, error) {
	state, _, err := s.minter.Mint()
	if err != nil {
		return "", "", fmt.Errorf("minting state: %w", err)
	}
	return s.github.AuthorizeURL(state), state, nil
}

// CompleteGitHub finishes the contributor flow.
//
// Creates the account on first callback and resolves to the existing one
// afterwards, keyed on the IMMUTABLE numeric GitHub id. A login can be changed
// or given to somebody else; treating it as identity would hand one
// contributor's account to whoever claimed their old name.
//
// The state is compared BEFORE the code is exchanged. An attacker-supplied
// code must never reach GitHub.
func (s *AuthService) CompleteGitHub(ctx context.Context, code, state, expectedState string) (*domain.Contributor, *domain.TokenPair, bool, error) {
	if state == "" || state != expectedState {
		return nil, nil, false, fmt.Errorf("state does not match: %w", ErrInvalidCredentials)
	}

	identity, err := s.github.ExchangeCode(ctx, code)
	if err != nil {
		return nil, nil, false, fmt.Errorf("exchanging the code: %w", ErrInvalidCredentials)
	}

	existing, err := s.users.ByGitHubUserID(ctx, identity.GitHubUserID)
	switch {
	case err == nil:
		// GitHub is the authority on the handle. Signing in is when we learn
		// it changed, and the response reports what is true now rather than
		// what was true at signup.
		if identity.Login != "" && identity.Login != existing.GitHubLogin {
			if err := s.users.RefreshGitHubLogin(ctx, identity.GitHubUserID, identity.Login); err != nil {
				return nil, nil, false, fmt.Errorf("refreshing the github login: %w", err)
			}
			existing.GitHubLogin = identity.Login
		}
		pair, err := s.issue(ctx, principalOf(existing))
		if err != nil {
			return nil, nil, false, err
		}
		return existing, pair, false, nil
	case !errors.Is(err, port.ErrNotFound):
		return nil, nil, false, fmt.Errorf("resolving the github identity: %w", err)
	}

	// First callback. The user, the identity and the session are written
	// together: a user with no identity could never sign in again.
	var (
		created *domain.Contributor
		pair    *domain.TokenPair
	)
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		created, err = s.users.Create(ctx, tx, &domain.Contributor{
			DisplayName:  identity.Name,
			Email:        identity.Email,
			GitHubUserID: identity.GitHubUserID,
			GitHubLogin:  identity.Login,
		})
		if err != nil {
			return err
		}
		pair, err = s.startSession(ctx, tx, principalOf(created))
		return err
	})
	if err != nil {
		return nil, nil, false, fmt.Errorf("creating the contributor: %w", err)
	}
	return created, pair, true, nil
}

// CompleteGitHubHirer finishes the GitHub flow in the HIRER namespace.
//
// Never creates. A GitHub sign-in that minted a hirer would bypass
// registration and therefore bypass the verification request — the same rule
// the Google path follows (ADR-0002).
func (s *AuthService) CompleteGitHubHirer(ctx context.Context, code, state, expectedState string) (*domain.Hirer, *domain.TokenPair, error) {
	if state == "" || state != expectedState {
		return nil, nil, fmt.Errorf("state does not match: %w", ErrInvalidCredentials)
	}

	identity, err := s.github.ExchangeCode(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("exchanging the code: %w", ErrInvalidCredentials)
	}

	hirer, err := s.hirers.ByGitHubUserID(ctx, identity.GitHubUserID)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, nil, fmt.Errorf(
				"register an organization account before signing in with GitHub: %w", ErrNotFound)
		}
		return nil, nil, fmt.Errorf("resolving the hirer: %w", err)
	}

	pair, err := s.issue(ctx, hirerPrincipalOf(hirer))
	if err != nil {
		return nil, nil, err
	}
	return hirer, pair, nil
}

// GoogleAuthorizeURL starts the hirer flow.
func (s *AuthService) GoogleAuthorizeURL(ctx context.Context) (string, string, error) {
	state, _, err := s.minter.Mint()
	if err != nil {
		return "", "", fmt.Errorf("minting state: %w", err)
	}
	return s.google.AuthorizeURL(state), state, nil
}

// CompleteGoogle finishes the hirer flow.
//
// It NEVER auto-creates an account. Registration is what creates the
// verification request, so a Google sign-in that minted a hirer would bypass
// verification entirely.
func (s *AuthService) CompleteGoogle(ctx context.Context, code, state, expectedState string) (*domain.Hirer, *domain.TokenPair, error) {
	if state == "" || state != expectedState {
		return nil, nil, fmt.Errorf("state does not match: %w", ErrInvalidCredentials)
	}

	identity, err := s.google.Exchange(ctx, code)
	if err != nil {
		// An unverified address is not a failed exchange. Google answered and
		// named somebody; what it declined to do is vouch for the address, and
		// the caller's remedy is to verify it rather than to sign in again.
		if errors.Is(err, port.ErrEmailUnverified) {
			return nil, nil, Coded(ErrInvalidCredentials, CodeEmailNotVerified,
				"the provider has not verified this address")
		}
		return nil, nil, fmt.Errorf("exchanging the code: %w", ErrInvalidCredentials)
	}

	hirer, err := s.hirers.ByEmail(ctx, identity.Email)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, nil, fmt.Errorf(
				"register an organization account before signing in with Google: %w", ErrNotFound)
		}
		return nil, nil, fmt.Errorf("resolving the hirer: %w", err)
	}

	pair, err := s.issue(ctx, hirerPrincipalOf(hirer))
	if err != nil {
		return nil, nil, err
	}
	return hirer, pair, nil
}

// RegisterHirer creates the organization, the seat and the verification
// request.
func (s *AuthService) RegisterHirer(ctx context.Context, req port.RegisterHirerRequest) (*port.HirerRegistration, error) {
	if req.Username == "" {
		return nil, fmt.Errorf("a hirer account needs a username: %w", ErrInvalid)
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return nil, fmt.Errorf("hashing the password: %w", err)
	}

	// No session is started. Registration queues a review, and until that
	// review lands the account can do nothing a token would let it do
	// (ADR-0002) — so issuing one would mint a credential whose only property
	// is that it has to be revoked later.
	var registration *port.HirerRegistration
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		registration, err = s.hirers.Register(ctx, tx, port.NewHirerAccount{
			Hirer: &domain.Hirer{
				Email: req.Email, Username: req.Username,
				DisplayName:  req.DisplayName,
				AuthProvider: domain.ProviderEmail,
			},
			PasswordHash: hash,
			Proofs:       req.Proofs,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// Only the username can clash now that registration creates no
			// organisation. Named as such, but with no detail about who holds
			// it or where (ADR-0016 §0).
			return nil, Coded(ErrConflict, CodeUsernameTaken,
				"that username is not available")
		}
		return nil, fmt.Errorf("registering: %w", err)
	}
	return registration, nil
}

// LoginHirer signs in through the email provider.
//
// Keyed on USERNAME since ADR-0016. An email identifies nobody: two seats may
// share a contact address, and a work address outlives the person who held it.
//
// ByUsername excludes disabled seats, so a revoked one fails here exactly as a
// wrong password does — sign-in must not reveal that a seat used to exist.
func (s *AuthService) LoginHirer(ctx context.Context, username, password string) (*domain.Hirer, *domain.TokenPair, error) {
	hirer, err := s.hirers.ByUsername(ctx, username)
	if err != nil {
		// Deliberately the same error, and reached by the same path, as a
		// wrong password.
		return nil, nil, ErrInvalidCredentials
	}
	if hirer.AuthProvider != domain.ProviderEmail {
		// An OAuth seat has no password. Saying so would confirm the account
		// exists and name its provider.
		return nil, nil, ErrInvalidCredentials
	}

	hash, err := s.hirers.PasswordHash(ctx, hirer.ID)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	if !s.hasher.Verify(hash, password) {
		return nil, nil, ErrInvalidCredentials
	}

	pair, err := s.issue(ctx, hirerPrincipalOf(hirer))
	if err != nil {
		return nil, nil, err
	}
	return hirer, pair, nil
}

// LoginAdmin signs in an administrator.
func (s *AuthService) LoginAdmin(ctx context.Context, email, password string) (*domain.Admin, *domain.TokenPair, error) {
	admin, err := s.admins.ByEmail(ctx, email)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	hash, err := s.admins.PasswordHash(ctx, admin.ID)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	if !s.hasher.Verify(hash, password) {
		return nil, nil, ErrInvalidCredentials
	}

	pair, err := s.issue(ctx, domain.Principal{Kind: domain.KindAdmin, Admin: admin})
	if err != nil {
		return nil, nil, err
	}
	return admin, pair, nil
}

// Refresh rotates a token pair, and detects reuse.
//
// Presenting a SPENT token means somebody is replaying one the legitimate
// holder already used. The only safe reading is that the family is
// compromised, so the whole family dies — including the successor currently in
// honest use. That cost is the point (ADR-0002).
//
// Unlike sign-in, this DISTINGUISHES its failures. Sign-in returns one error
// for every cause because telling them apart would build an enumeration oracle
// over which addresses hold accounts. Here the caller already holds the token
// being judged, so naming the reason tells them only about their own session —
// and the difference matters to a client deciding whether to prompt for
// credentials or to warn that a session was revoked underneath them.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*domain.TokenPair, error) {
	hash := s.minter.Hash(refreshToken)

	var pair *domain.TokenPair

	// Reuse is detected inside the transaction and REPORTED after it commits.
	// Returning the error from the callback would roll back the revocation it
	// just performed, leaving the compromised family alive — the one outcome
	// detecting reuse exists to prevent.
	var reuse error

	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		session, err := s.sessions.ByRefreshTokenHash(ctx, tx, hash)
		if err != nil {
			return Coded(ErrInvalidCredentials, CodeInvalidRefreshToken,
				"no session holds that refresh token")
		}

		// A token that is both spent AND revoked was retired by its own
		// holder logging out. Nothing was compromised and nothing is
		// collateral: the token is simply no longer a token.
		if session.UsedAt != nil && session.RevokedAt != nil {
			return Coded(ErrInvalidCredentials, CodeInvalidRefreshToken,
				"session %s was closed at %s", session.ID, session.RevokedAt)
		}

		// REUSE. Revoke the family and refuse — in the same transaction, so a
		// concurrent replay cannot slip between the detection and the
		// revocation.
		if session.UsedAt != nil {
			if err := s.sessions.RevokeFamily(ctx, tx, session.FamilyID); err != nil {
				return fmt.Errorf("revoking the compromised family: %w", err)
			}
			reuse = Coded(ErrInvalidCredentials, CodeTokenReuseDetected,
				"refresh token %s was already spent; family %s revoked",
				session.ID, session.FamilyID)
			return nil
		}

		// Revoked is reported separately from expired-or-unknown because it is
		// the COLLATERAL case: the honest holder of the successor token is
		// logged out by somebody else's replay, and a client that says so is
		// telling the truth about what happened.
		if session.RevokedAt != nil {
			return Coded(ErrInvalidCredentials, CodeSessionRevoked,
				"session %s was revoked at %s", session.ID, session.RevokedAt)
		}
		if !session.ExpiresAt.After(s.clock.Now()) {
			return Coded(ErrInvalidCredentials, CodeInvalidRefreshToken,
				"session %s expired at %s", session.ID, session.ExpiresAt)
		}

		plaintext, successorHash, err := s.minter.Mint()
		if err != nil {
			return fmt.Errorf("minting the successor: %w", err)
		}
		successor := &domain.Session{
			FamilyID:    session.FamilyID,
			PrincipalID: session.PrincipalID,
			Kind:        session.Kind,
			ExpiresAt:   s.clock.Now().Add(RefreshTokenTTL),
		}
		if err := s.sessions.Rotate(ctx, tx, session.ID, successor, successorHash); err != nil {
			return err
		}

		// The subject is the rotated session's principal. Refresh does not
		// re-read the account: the session row is the authority on who this
		// family belongs to, and it cannot change during rotation.
		access, err := s.tokens.Issue(ctx, port.AccessClaims{
			Subject: session.PrincipalID, Kind: session.Kind, Family: session.FamilyID,
		}, AccessTokenTTL)
		if err != nil {
			return fmt.Errorf("issuing the access token: %w", err)
		}
		pair = &domain.TokenPair{
			AccessToken: access, RefreshToken: plaintext,
			ExpiresIn: int(AccessTokenTTL.Seconds()),
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// The rotation lost a race — another refresh spent the token
			// first, which is itself reuse.
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if reuse != nil {
		return nil, reuse
	}
	return pair, nil
}

// ResolvePrincipal turns a verified token's claims into the account acting.
//
// Called on EVERY authenticated request. That is the cost ADR-0011 accepted
// deliberately: the token carries identity and nothing else, so authorization
// state is read fresh and a revoked capability stops working immediately rather
// than fifteen minutes later.
//
// A missing account is ErrInvalidCredentials rather than ErrNotFound. The
// caller is presenting a token for something that no longer exists, which is an
// authentication failure — reporting it as a 404 would tell them their own
// account is gone through a channel that has not authenticated them.
func (s *AuthService) ResolvePrincipal(ctx context.Context, claims port.AccessClaims) (*domain.Principal, error) {
	if claims.Subject == "" {
		return nil, fmt.Errorf("token carries no subject: %w", ErrInvalidCredentials)
	}

	// A signed-out family ends its access tokens with it. Fifteen minutes of
	// residual authority after a deliberate logout is not a logout (ADR-0002).
	if claims.Family != "" {
		live, err := s.sessions.FamilyLive(ctx, claims.Family)
		if err != nil {
			return nil, fmt.Errorf("checking the session family: %w", err)
		}
		if !live {
			return nil, Coded(ErrInvalidCredentials, CodeSessionRevoked,
				"session family %s is closed", claims.Family)
		}
	}

	switch claims.Kind {
	case domain.KindContributor:
		contributor, err := s.users.ByID(ctx, domain.UserID(claims.Subject))
		if err != nil {
			return nil, resolveFailure("contributor", err)
		}
		return &domain.Principal{Kind: domain.KindContributor, Contributor: contributor}, nil

	case domain.KindHirer:
		hirer, err := s.hirers.ByID(ctx, domain.HirerID(claims.Subject))
		if err != nil {
			return nil, resolveFailure("hirer", err)
		}
		// A revoked seat keeps a signed access token for up to fifteen
		// minutes. Removing a roster entry revokes the session families too,
		// so the family check above usually catches this first — but that
		// check is skipped for a token carrying no family, and a seat disabled
		// by any other path would otherwise keep searching the pool until its
		// token expired. Access ends when the owner says it ends (ADR-0016 §5).
		if hirer.DisabledAt != nil {
			return nil, Coded(ErrInvalidCredentials, CodeSessionRevoked,
				"hirer %s is disabled", hirer.ID)
		}
		return &domain.Principal{Kind: domain.KindHirer, Hirer: hirer}, nil

	case domain.KindAdmin:
		admin, err := s.admins.ByID(ctx, domain.AdminID(claims.Subject))
		if err != nil {
			return nil, resolveFailure("admin", err)
		}
		// A disabled admin keeps a valid token for up to fifteen minutes.
		// Checking here is what stops them using it — and an admin decides
		// hirer verification, so the delay is not one to accept.
		if admin.DisabledAt != nil {
			return nil, fmt.Errorf("admin %s is disabled: %w", admin.ID, ErrInvalidCredentials)
		}
		return &domain.Principal{Kind: domain.KindAdmin, Admin: admin}, nil
	}

	return nil, fmt.Errorf("token claims an unknown kind %q: %w", claims.Kind, ErrInvalidCredentials)
}

// resolveFailure keeps a genuine database error distinguishable from an absent
// account, so an outage is not reported to every client as a bad token.
func resolveFailure(kind string, err error) error {
	if errors.Is(err, port.ErrNotFound) {
		return fmt.Errorf("no %s account for that token: %w", kind, ErrInvalidCredentials)
	}
	return fmt.Errorf("resolving the %s account: %w", kind, err)
}

// Logout revokes the whole family, not the presented session.
//
// A deliberate sign-out has to invalidate a refresh token stolen beforehand,
// which revoking only the current session would not do.
func (s *AuthService) Logout(ctx context.Context, p domain.Principal) error {
	familyID, err := s.familyOf(ctx, p)
	switch {
	case errors.Is(err, port.ErrNotFound):
		// No live session. Signing out twice is not an error worth surfacing.
		return nil
	case err != nil:
		// Anything else is propagated. Reporting success on an outage would
		// tell a caller their session is dead while it is still live — which
		// is the one thing logout must never get wrong.
		return fmt.Errorf("resolving the session family: %w", err)
	}
	return s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return s.sessions.CloseFamily(ctx, tx, familyID)
	})
}

// familyOf reads the family the caller's live session belongs to.
//
// The access token carries the principal but not the family, so it is resolved
// from the principal. A caller with no live session has nothing to revoke.
func (s *AuthService) familyOf(ctx context.Context, p domain.Principal) (string, error) {
	return s.sessions.ActiveFamily(ctx, p.Subject())
}

// Me reads the caller's own account.
func (s *AuthService) Me(ctx context.Context, p domain.Principal) (*domain.Principal, error) {
	switch p.Kind {
	case domain.KindContributor:
		c, err := s.users.ByID(ctx, p.Contributor.ID)
		if err != nil {
			return nil, ErrNotFound
		}
		return &domain.Principal{Kind: domain.KindContributor, Contributor: c}, nil
	case domain.KindHirer:
		h, err := s.hirers.ByID(ctx, p.Hirer.ID)
		if err != nil {
			return nil, ErrNotFound
		}
		return &domain.Principal{Kind: domain.KindHirer, Hirer: h}, nil
	default:
		return &p, nil
	}
}

// SetAvailability states availability and resets the 15-day window.
func (s *AuthService) SetAvailability(ctx context.Context, id domain.UserID, status domain.AvailabilityStatus) (*domain.Availability, error) {
	a, err := s.users.SetAvailability(ctx, id, status)
	if err != nil {
		return nil, fmt.Errorf("setting availability: %w", err)
	}
	return a, nil
}

// MintShareLink issues a contributor's publishable scorecard link.
//
// The plaintext is returned ONCE and never stored. Minting revokes any
// existing link, so a contributor who shares one and then mints another does
// not leave the first live.
func (s *AuthService) MintShareLink(ctx context.Context, id domain.UserID) (string, domain.ShareLinkID, error) {
	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return "", "", fmt.Errorf("minting the share link: %w", err)
	}

	var linkID domain.ShareLinkID
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		linkID, err = s.shareLinks.Mint(ctx, tx, id, hash)
		return err
	})
	if err != nil {
		return "", "", fmt.Errorf("recording the share link: %w", err)
	}
	return plaintext, linkID, nil
}

// RevokeShareLink withdraws a published link. Revocation is immediate.
func (s *AuthService) RevokeShareLink(ctx context.Context, id domain.UserID, linkID domain.ShareLinkID) error {
	if err := s.shareLinks.Revoke(ctx, id, linkID); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("revoking the share link: %w", err)
	}
	return nil
}

// PublicScorecard reads a scorecard by share token, unauthenticated.
//
// A DIFFERENT document from the hirer view (ADR-0008 §2): no rank, no
// generalist score, no email. A contributor publishing their own evidence is
// not publishing their position against other people, so the ranks are
// stripped here rather than trusted to the caller.
//
// It is NOT subject to the availability gate. This is the contributor's own
// publication, not a search result.
func (s *AuthService) PublicScorecard(ctx context.Context, token string) (*domain.Scorecard, error) {
	id, err := s.shareLinks.ResolveToken(ctx, s.minter.Hash(token))
	if err != nil {
		// A revoked link and a token that never existed are the same answer.
		return nil, Coded(ErrNotFound, CodeScorecardNotFound, "no such scorecard")
	}

	// The hirer id is empty: AssertNotSelf has nobody to compare against on an
	// unauthenticated read.
	card, err := s.search.Scorecard(ctx, "", id)
	if err != nil {
		return nil, Coded(ErrNotFound, CodeScorecardNotFound, "no such scorecard")
	}

	card.User.Rank = 0
	card.Email = nil
	card.RubricVersion = s.rubric
	for i := range card.Skills {
		card.Skills[i].Rank = nil
	}
	return card, nil
}

// --- session plumbing --------------------------------------------------------

// issue starts a new session family outside an existing transaction.
func (s *AuthService) issue(ctx context.Context, p domain.Principal) (*domain.TokenPair, error) {
	var pair *domain.TokenPair
	err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		pair, err = s.startSession(ctx, tx, p)
		return err
	})
	if err != nil {
		return nil, err
	}
	return pair, nil
}

// startSession mints a pair and records the session, inside a caller's
// transaction.
//
// Only the HASH is stored. A database read cannot yield a usable token, and
// neither can a backup.
func (s *AuthService) startSession(ctx context.Context, tx port.Tx, p domain.Principal) (*domain.TokenPair, error) {
	plaintext, hash, err := s.minter.Mint()
	if err != nil {
		return nil, fmt.Errorf("minting the refresh token: %w", err)
	}

	session := &domain.Session{
		PrincipalID: p.Subject(),
		Kind:        p.Kind,
		ExpiresAt:   s.clock.Now().Add(RefreshTokenTTL),
	}
	if err := s.sessions.Create(ctx, tx, session, hash); err != nil {
		return nil, fmt.Errorf("creating the session: %w", err)
	}

	access, err := s.tokens.Issue(ctx, port.AccessClaims{
		Subject: p.Subject(), Kind: p.Kind, Family: session.FamilyID,
	}, AccessTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("issuing the access token: %w", err)
	}
	return &domain.TokenPair{
		AccessToken: access, RefreshToken: plaintext,
		ExpiresIn: int(AccessTokenTTL.Seconds()),
	}, nil
}

func principalOf(c *domain.Contributor) domain.Principal {
	return domain.Principal{Kind: domain.KindContributor, Contributor: c}
}

func hirerPrincipalOf(h *domain.Hirer) domain.Principal {
	return domain.Principal{Kind: domain.KindHirer, Hirer: h}
}

// slugify makes a URL-safe organization slug.
func slugify(name string) string {
	out := make([]rune, 0, len(name))
	lastDash := true
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
			lastDash = false
		case !lastDash:
			out = append(out, '-')
			lastDash = true
		}
	}
	if n := len(out); n > 0 && out[n-1] == '-' {
		out = out[:n-1]
	}
	return string(out)
}

var _ port.AuthService = (*AuthService)(nil)
