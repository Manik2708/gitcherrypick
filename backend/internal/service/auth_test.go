package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

func TestAuthGitHubCallback(t *testing.T) {
	t.Run("a mismatched state is refused BEFORE the code is exchanged", func(t *testing.T) {
		// An attacker-supplied code must never reach GitHub. The github mock
		// has no ExchangeCode expectation, so reaching it fails the test.
		f := newAuthFixture(t)

		_, _, _, err := f.svc.CompleteGitHub(ctx(t), "gho_valid", "not-the-state", "the-state")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("an empty state never matches", func(t *testing.T) {
		// Otherwise a callback with no state at all would pass whenever the
		// server had none either.
		f := newAuthFixture(t)

		_, _, _, err := f.svc.CompleteGitHub(ctx(t), "gho_valid", "", "")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected an empty state to be refused, got %v", err)
		}
	})

	t.Run("a known identity resolves rather than creating a second account", func(t *testing.T) {
		f := newAuthFixture(t)
		f.github.EXPECT().ExchangeCode(mock.Anything, "gho_valid").
			Return(&port.GitHubIdentity{GitHubUserID: githubUserID, Login: "renamed"}, nil)
		f.users.EXPECT().ByGitHubUserID(mock.Anything, githubUserID).Return(contributor(), nil)
		f.expectSession()

		got, pair, created, err := f.svc.CompleteGitHub(ctx(t), "gho_valid", "s", "s")
		if err != nil {
			t.Fatalf("completing: %v", err)
		}
		if created {
			t.Error("a known identity must not create a second account")
		}
		if got.ID != userID {
			t.Errorf("expected the existing account, got %s", got.ID)
		}
		if pair.RefreshToken == "" {
			t.Error("expected a refresh token")
		}
	})

	t.Run("identity is the numeric id, not the login", func(t *testing.T) {
		// A login can be changed or given to somebody else. Resolving on it
		// would hand one contributor's account to whoever claimed their name.
		f := newAuthFixture(t)
		f.github.EXPECT().ExchangeCode(mock.Anything, "gho_valid").
			Return(&port.GitHubIdentity{GitHubUserID: githubUserID, Login: "totally-different"}, nil)
		f.users.EXPECT().ByGitHubUserID(mock.Anything, githubUserID).Return(contributor(), nil)
		f.expectSession()

		got, _, created, err := f.svc.CompleteGitHub(ctx(t), "gho_valid", "s", "s")
		if err != nil {
			t.Fatalf("completing: %v", err)
		}
		if created || got.ID != userID {
			t.Error("a renamed contributor was treated as somebody new")
		}
	})

	t.Run("a first callback creates the account and its session together", func(t *testing.T) {
		f := newAuthFixture(t)
		f.github.EXPECT().ExchangeCode(mock.Anything, "gho_new").
			Return(&port.GitHubIdentity{GitHubUserID: 100077, Login: "newcomer", Name: "Nina"}, nil)
		f.users.EXPECT().ByGitHubUserID(mock.Anything, int64(100077)).Return(nil, port.ErrNotFound)
		f.users.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.Contributor{ID: "new-user", DisplayName: "Nina"}, nil)
		f.expectSession()

		_, _, created, err := f.svc.CompleteGitHub(ctx(t), "gho_new", "s", "s")
		if err != nil {
			t.Fatalf("completing: %v", err)
		}
		if !created {
			t.Error("expected the account to be reported as created")
		}
		if !f.txCommitted {
			t.Error("the creation transaction did not commit")
		}
	})
}

func TestAuthGoogleCallback(t *testing.T) {
	t.Run("an unknown email does NOT create a hirer", func(t *testing.T) {
		// Registration is what creates the verification request, so a Google
		// sign-in that minted an account would bypass verification entirely.
		f := newAuthFixture(t)
		f.google.EXPECT().Exchange(mock.Anything, "goog").
			Return(&port.OAuthIdentity{Email: "stranger@elsewhere.example"}, nil)
		f.hirers.EXPECT().ByEmail(mock.Anything, "stranger@elsewhere.example").
			Return(nil, port.ErrNotFound)

		_, _, err := f.svc.CompleteGoogle(ctx(t), "goog", "s", "s")
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestAuthLogin(t *testing.T) {
	t.Run("every sign-in failure is the same error", func(t *testing.T) {
		// Distinguishing them turns sign-in into an enumeration oracle over
		// which companies are recruiting.
		f := newAuthFixture(t)
		f.hirers.EXPECT().ByEmail(mock.Anything, "nobody@nowhere.example").
			Return(nil, port.ErrNotFound)

		_, _, unknown := f.svc.LoginHirer(ctx(t), "nobody@nowhere.example", "whatever")

		g := newAuthFixture(t)
		g.hirers.EXPECT().ByEmail(mock.Anything, "hank@acme.com").Return(emailHirer(), nil)
		g.hirers.EXPECT().PasswordHash(mock.Anything, hirerID).Return([]byte("hash"), nil)
		g.hasher.EXPECT().Verify([]byte("hash"), "wrong").Return(false)

		_, _, wrongPassword := g.svc.LoginHirer(ctx(t), "hank@acme.com", "wrong")

		if unknown.Error() != wrongPassword.Error() {
			t.Errorf("the two failures are distinguishable:\n  %v\n  %v", unknown, wrongPassword)
		}
	})

	t.Run("an oauth seat cannot sign in with a password", func(t *testing.T) {
		// Saying so would confirm the account exists and name its provider.
		f := newAuthFixture(t)
		google := emailHirer()
		google.AuthProvider = domain.ProviderGoogle
		f.hirers.EXPECT().ByEmail(mock.Anything, "hank@acme.com").Return(google, nil)

		_, _, err := f.svc.LoginHirer(ctx(t), "hank@acme.com", "guess")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("a contributor's email is not a hirer credential", func(t *testing.T) {
		f := newAuthFixture(t)
		f.hirers.EXPECT().ByEmail(mock.Anything, "alice@example.com").Return(nil, port.ErrNotFound)

		_, _, err := f.svc.LoginHirer(ctx(t), "alice@example.com", "anything")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})
}

func TestAuthRefreshReuseDetection(t *testing.T) {
	t.Run("a SPENT token revokes the whole family", func(t *testing.T) {
		// Reuse means the family is compromised. The honest holder is signed
		// out too, and that cost is the point (ADR-0002).
		f := newAuthFixture(t)
		used := f.now.Add(-time.Minute)
		f.tokens.EXPECT().HashRefreshToken("replayed").Return([]byte("hash"))
		f.sessions.EXPECT().ByRefreshTokenHash(mock.Anything, mock.Anything, []byte("hash")).
			Return(&domain.Session{
				ID: "session-1", FamilyID: "family-1", Kind: domain.KindContributor,
				UsedAt: &used, ExpiresAt: f.now.Add(time.Hour),
			}, nil)

		var revoked string
		f.sessions.EXPECT().RevokeFamily(mock.Anything, mock.Anything, "family-1").
			RunAndReturn(func(_ context.Context, _ port.Tx, familyID string) error {
				revoked = familyID
				return nil
			})

		_, err := f.svc.Refresh(ctx(t), "replayed")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected the replay refused, got %v", err)
		}
		if revoked != "family-1" {
			t.Error("the compromised family was not revoked")
		}
	})

	t.Run("a revoked session is refused without re-revoking", func(t *testing.T) {
		// revoked_at records when the family died. Revoking again would move
		// it, losing that.
		f := newAuthFixture(t)
		revokedAt := f.now.Add(-time.Hour)
		f.tokens.EXPECT().HashRefreshToken("dead").Return([]byte("hash"))
		f.sessions.EXPECT().ByRefreshTokenHash(mock.Anything, mock.Anything, []byte("hash")).
			Return(&domain.Session{
				ID: "session-1", FamilyID: "family-1", Kind: domain.KindContributor,
				RevokedAt: &revokedAt, ExpiresAt: f.now.Add(time.Hour),
			}, nil)

		_, err := f.svc.Refresh(ctx(t), "dead")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("an expired session is refused", func(t *testing.T) {
		f := newAuthFixture(t)
		f.tokens.EXPECT().HashRefreshToken("stale").Return([]byte("hash"))
		f.sessions.EXPECT().ByRefreshTokenHash(mock.Anything, mock.Anything, []byte("hash")).
			Return(&domain.Session{
				ID: "session-1", FamilyID: "family-1", Kind: domain.KindContributor,
				ExpiresAt: f.now.Add(-time.Minute),
			}, nil)

		_, err := f.svc.Refresh(ctx(t), "stale")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})

	t.Run("a good token rotates and keeps the family", func(t *testing.T) {
		f := newAuthFixture(t)
		f.tokens.EXPECT().HashRefreshToken("live").Return([]byte("hash"))
		f.sessions.EXPECT().ByRefreshTokenHash(mock.Anything, mock.Anything, []byte("hash")).
			Return(&domain.Session{
				ID: "session-1", FamilyID: "family-1", PrincipalID: string(userID),
				Kind: domain.KindContributor, ExpiresAt: f.now.Add(time.Hour),
			}, nil)
		f.tokens.EXPECT().NewRefreshToken().Return("successor", []byte("successor-hash"), nil)
		f.tokens.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything).Return("access", nil)

		var successor *domain.Session
		f.sessions.EXPECT().Rotate(mock.Anything, mock.Anything, domain.SessionID("session-1"),
			mock.Anything, []byte("successor-hash")).
			RunAndReturn(func(_ context.Context, _ port.Tx, _ domain.SessionID, s *domain.Session, _ []byte) error {
				successor = s
				return nil
			})

		pair, err := f.svc.Refresh(ctx(t), "live")
		if err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		if pair.RefreshToken != "successor" {
			t.Errorf("expected the new token returned, got %q", pair.RefreshToken)
		}
		if successor.FamilyID != "family-1" {
			t.Error("the successor left the family, so revocation would not reach it")
		}
	})

	t.Run("losing the rotation race reads as reuse", func(t *testing.T) {
		// Another refresh spent the token first, which is itself a replay.
		f := newAuthFixture(t)
		f.tokens.EXPECT().HashRefreshToken("live").Return([]byte("hash"))
		f.sessions.EXPECT().ByRefreshTokenHash(mock.Anything, mock.Anything, []byte("hash")).
			Return(&domain.Session{
				ID: "session-1", FamilyID: "family-1", Kind: domain.KindContributor,
				ExpiresAt: f.now.Add(time.Hour),
			}, nil)
		f.tokens.EXPECT().NewRefreshToken().Return("successor", []byte("successor-hash"), nil)
		f.sessions.EXPECT().Rotate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(port.ErrConflict)

		_, err := f.svc.Refresh(ctx(t), "live")
		if !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected ErrInvalidCredentials, got %v", err)
		}
	})
}

func TestAuthLogout(t *testing.T) {
	t.Run("revokes the whole family", func(t *testing.T) {
		// A stolen refresh token issued before sign-out must be dead
		// afterwards.
		f := newAuthFixture(t)
		f.sessions.EXPECT().ActiveFamily(mock.Anything, string(userID)).Return("family-1", nil)
		f.sessions.EXPECT().RevokeFamily(mock.Anything, mock.Anything, "family-1").Return(nil)

		if err := f.svc.Logout(ctx(t), contributorPrincipal()); err != nil {
			t.Fatalf("logging out: %v", err)
		}
	})

	t.Run("signing out twice is not an error", func(t *testing.T) {
		f := newAuthFixture(t)
		f.sessions.EXPECT().ActiveFamily(mock.Anything, string(userID)).Return("", port.ErrNotFound)

		if err := f.svc.Logout(ctx(t), contributorPrincipal()); err != nil {
			t.Fatalf("expected a second logout to succeed quietly, got %v", err)
		}
	})

	t.Run("an outage is NOT reported as a successful sign-out", func(t *testing.T) {
		// Telling a caller their session is dead while it is still live is the
		// one thing logout must never get wrong.
		f := newAuthFixture(t)
		boom := errors.New("connection refused")
		f.sessions.EXPECT().ActiveFamily(mock.Anything, string(userID)).Return("", boom)

		err := f.svc.Logout(ctx(t), contributorPrincipal())
		if err == nil {
			t.Fatal("an outage was reported as a successful sign-out")
		}
		if !errors.Is(err, boom) {
			t.Errorf("expected the underlying failure, got %v", err)
		}
	})
}

func TestAuthPublicScorecard(t *testing.T) {
	t.Run("strips rank, generalist score and email", func(t *testing.T) {
		// A contributor publishing their own evidence is not publishing their
		// position against other people (ADR-0008 §2).
		f := newAuthFixture(t)
		generalist, email := 122.7, "alice@example.com"

		f.minter.EXPECT().Hash("tok").Return([]byte("hash"))
		f.shareLinks.EXPECT().ResolveToken(mock.Anything, []byte("hash")).Return(userID, nil)
		f.search.EXPECT().Scorecard(mock.Anything, domain.HirerID(""), userID).
			Return(&domain.Scorecard{
				User:  domain.SearchResult{UserID: userID, Rank: 3, GeneralistScore: &generalist},
				Email: &email,
				Skills: []domain.ScorecardSkill{{
					ResultSkill: domain.ResultSkill{Slug: "go", Rank: intPtr(1)}}},
			}, nil)

		card, err := f.svc.PublicScorecard(ctx(t), "tok")
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if card.User.Rank != 0 {
			t.Errorf("rank leaked: %d", card.User.Rank)
		}
		if card.User.GeneralistScore != nil {
			t.Error("the generalist score leaked")
		}
		if card.Email != nil {
			t.Error("the email leaked")
		}
		if card.Skills[0].Rank != nil {
			t.Error("a skill rank leaked")
		}
	})

	t.Run("a revoked link is indistinguishable from one that never existed", func(t *testing.T) {
		f := newAuthFixture(t)
		f.minter.EXPECT().Hash("revoked").Return([]byte("hash"))
		f.shareLinks.EXPECT().ResolveToken(mock.Anything, []byte("hash")).Return("", port.ErrNotFound)

		_, err := f.svc.PublicScorecard(ctx(t), "revoked")
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

// --- fixture -----------------------------------------------------------------

type authFixture struct {
	svc        *service.AuthService
	users      *mocks.UserRepository
	hirers     *mocks.HirerRepository
	admins     *mocks.AdminRepository
	sessions   *mocks.SessionRepository
	github     *mocks.GitHubClient
	google     *mocks.OAuthProvider
	tokens     *mocks.TokenIssuer
	minter     *mocks.TokenMinter
	hasher     *mocks.PasswordHasher
	shareLinks *mocks.ShareLinkRepository
	search     *mocks.SearchRepository
	tx         *mocks.TxManager
	clock      *mocks.Clock

	now         time.Time
	txCommitted bool
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{
		users: mocks.NewUserRepository(t), hirers: mocks.NewHirerRepository(t),
		admins: mocks.NewAdminRepository(t), sessions: mocks.NewSessionRepository(t),
		github: mocks.NewGitHubClient(t), google: mocks.NewOAuthProvider(t),
		tokens: mocks.NewTokenIssuer(t), minter: mocks.NewTokenMinter(t),
		hasher: mocks.NewPasswordHasher(t), shareLinks: mocks.NewShareLinkRepository(t),
		search: mocks.NewSearchRepository(t), tx: mocks.NewTxManager(t),
		clock: mocks.NewClock(t),
		now:   time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	}
	f.clock.EXPECT().Now().Return(f.now).Maybe()
	f.tx.EXPECT().InTx(mock.Anything, mock.Anything).
		RunAndReturn(func(c context.Context, fn func(context.Context, port.Tx) error) error {
			err := fn(c, stubTx{})
			f.txCommitted = err == nil
			return err
		}).Maybe()

	f.svc = service.NewAuthService(f.users, f.hirers, f.admins, f.sessions,
		f.github, f.google, f.tokens, f.minter, f.hasher, f.shareLinks, f.search, f.tx, f.clock)
	return f
}

// expectSession makes a successful sign-in mint and record a session.
func (f *authFixture) expectSession() {
	f.tokens.EXPECT().NewRefreshToken().Return("refresh", []byte("refresh-hash"), nil)
	f.tokens.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything).Return("access", nil)
	f.sessions.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, []byte("refresh-hash")).
		Return(nil)
}

func emailHirer() *domain.Hirer {
	return &domain.Hirer{
		ID: hirerID, OrganizationID: orgID, Email: "hank@acme.com",
		DisplayName: "Hank Rivera", AuthProvider: domain.ProviderEmail,
	}
}

func intPtr(v int) *int { return &v }
