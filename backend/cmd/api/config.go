package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/google"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// config is every setting the API takes.
//
// All of it arrives through cobra flags (CLAUDE.md): there is no os.Getenv in
// business code, and nothing here reads the environment behind a caller's back.
type config struct {
	addr        string
	databaseURL string

	// secureCookies marks the OAuth state cookie Secure. Off by default
	// because local development runs on http, where a Secure cookie is
	// silently dropped and presents as an unexplainable state mismatch.
	secureCookies bool

	// Access tokens (ADR-0011).
	signingKeyPath string
	verifyKeys     []string
	accessTokenTTL time.Duration

	// The clock source (ADR-0012). Unset means the system clock.
	clockURL  string
	clockPoll time.Duration

	// Third-party base URLs (ADR-0010). Every default is the real host; the
	// integration suite points them at cmd/fakethirdparty.
	githubAPIURL       string
	githubOAuthURL     string
	githubClientID     string
	githubClientSecret string
	githubToken        string

	googleIssuerURL   string
	googleClientID    string
	googleSecret      string
	googleRedirectURI string

	resendAPIURL string
	resendAPIKey string
	resendFrom   string

	// rubricVersion stamps every score computed by this process, so a
	// leaderboard can tell which version judged whom (ADR-0004). Changing it
	// is a deliberate act followed by an admin sweep.
	rubricVersion string
}

// bind attaches every flag.
func (c *config) bind(cmd *cobra.Command) {
	f := cmd.Flags()

	f.StringVar(&c.addr, "addr", "127.0.0.1:8080", "address to listen on")
	f.StringVar(&c.databaseURL, "database-url", "", "PostgreSQL connection string (required)")
	f.BoolVar(&c.secureCookies, "secure-cookies", false,
		"mark cookies Secure; required in production, breaks plain-http local development")

	f.StringVar(&c.signingKeyPath, "signing-key", "",
		"path to the PKCS#8 Ed25519 private key that signs access tokens (required)")
	f.StringArrayVar(&c.verifyKeys, "verify-key", nil,
		"kid:path of a public key trusted to verify access tokens; repeatable, and the "+
			"signing key must appear here so a rotation never mints tokens nothing can verify")
	f.DurationVar(&c.accessTokenTTL, "access-token-ttl", service.AccessTokenTTL,
		"access token lifetime")

	f.StringVar(&c.clockURL, "clock-url", "",
		"clock source; unset means the system clock (ADR-0012)")
	f.DurationVar(&c.clockPoll, "clock-poll", clock.DefaultPoll,
		"how often to refresh the clock offset")

	f.StringVar(&c.githubAPIURL, "github-api-url", github.DefaultAPIBaseURL, "GitHub REST base URL")
	f.StringVar(&c.githubOAuthURL, "github-oauth-url", github.DefaultOAuthBaseURL, "GitHub OAuth base URL")
	f.StringVar(&c.githubClientID, "github-client-id", "", "GitHub OAuth app client id")
	f.StringVar(&c.githubClientSecret, "github-client-secret", "", "GitHub OAuth app client secret")
	f.StringVar(&c.githubToken, "github-token", "",
		"token for fact fetches; unauthenticated GitHub allows 60 requests an hour, "+
			"which one claim submission can exhaust")

	f.StringVar(&c.googleIssuerURL, "google-oidc-issuer", google.DefaultIssuer, "Google OIDC issuer")
	f.StringVar(&c.googleClientID, "google-client-id", "", "Google OAuth client id")
	f.StringVar(&c.googleSecret, "google-client-secret", "", "Google OAuth client secret")
	f.StringVar(&c.googleRedirectURI, "google-redirect-uri", "",
		"redirect URI registered with Google; a mismatch is rejected at the token endpoint")

	f.StringVar(&c.resendAPIURL, "resend-api-url", resend.DefaultBaseURL, "Resend API base URL")
	f.StringVar(&c.resendAPIKey, "resend-api-key", "", "Resend API key")
	f.StringVar(&c.resendFrom, "resend-from",
		"GitCherryPick <no-reply@gitcherrypick.dev>", "envelope sender")

	f.StringVar(&c.rubricVersion, "rubric-version", "v1",
		"rubric version stamped onto every score this process computes")
}

// validate refuses to start on a misconfiguration.
//
// Everything checked here would otherwise surface as a runtime failure on some
// later request — a database that cannot be reached, or a token nothing can
// verify. Failing before the listener binds turns those into a startup error,
// which is the only cheap moment to find them (ADR-0011).
func (c *config) validate() error {
	if c.databaseURL == "" {
		return fmt.Errorf("no database configured: pass --database-url")
	}
	if c.signingKeyPath == "" {
		return fmt.Errorf("no signing key configured: pass --signing-key (ADR-0011)")
	}
	if len(c.verifyKeys) == 0 {
		return fmt.Errorf("no verification keys configured: pass --verify-key kid:path (ADR-0011)")
	}
	if c.accessTokenTTL <= 0 {
		return fmt.Errorf("--access-token-ttl must be positive, got %s", c.accessTokenTTL)
	}
	if c.rubricVersion == "" {
		return fmt.Errorf("--rubric-version must not be empty: every score is stamped with it")
	}
	return nil
}

// verifyKeyPaths parses the repeatable kid:path flag.
//
// A path may itself contain colons, so the split is on the FIRST one only.
func (c *config) verifyKeyPaths() (map[string]string, error) {
	out := make(map[string]string, len(c.verifyKeys))

	for _, entry := range c.verifyKeys {
		kid, path, found := strings.Cut(entry, ":")
		if !found || kid == "" || path == "" {
			return nil, fmt.Errorf("--verify-key %q is not kid:path", entry)
		}
		if existing, duplicate := out[kid]; duplicate {
			return nil, fmt.Errorf("--verify-key kid %q given twice: %s and %s", kid, existing, path)
		}
		out[kid] = path
	}
	return out, nil
}
