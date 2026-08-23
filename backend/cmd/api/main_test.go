package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/google"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

func TestDefaultsPointAtTheRealHosts(t *testing.T) {
	// ADR-0010: the default is production. The integration suite overrides
	// these; a deployment that passes nothing must reach the real providers.
	cmd := command()
	f := cmd.Flags()

	for flag, want := range map[string]string{
		"github-api-url":     github.DefaultAPIBaseURL,
		"github-oauth-url":   github.DefaultOAuthBaseURL,
		"google-oidc-issuer": google.DefaultIssuer,
		"resend-api-url":     resend.DefaultBaseURL,
	} {
		got, err := f.GetString(flag)
		require.NoError(t, err)
		require.Equal(t, want, got, "--%s", flag)
	}
}

func TestClockDefaultsToTheSystemClock(t *testing.T) {
	// No flag means no poller and no HTTP (ADR-0012).
	got, err := command().Flags().GetString("clock-url")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestCookiesAreInsecureByDefault(t *testing.T) {
	// Deliberate, and the opposite of what a security default usually looks
	// like: local development runs on http, where a Secure cookie is silently
	// dropped and presents as an unexplainable state mismatch. Production
	// passes --secure-cookies.
	got, err := command().Flags().GetBool("secure-cookies")
	require.NoError(t, err)
	require.False(t, got)
}

func TestValidateRefusesAnIncompleteConfiguration(t *testing.T) {
	// Each of these would otherwise surface as a runtime failure on some later
	// request. Failing before the listener binds is the only cheap moment.
	base := config{
		databaseURL: "postgres://x", signingKeyPath: "/k.pem",
		verifyKeys: []string{"dev:/k.pub"}, accessTokenTTL: time.Minute,
		rubricVersion: "v1",
	}
	require.NoError(t, base.validate())

	cases := map[string]struct {
		mutate func(c *config)
		want   string
	}{
		"no database":       {func(c *config) { c.databaseURL = "" }, "--database-url"},
		"no signing key":    {func(c *config) { c.signingKeyPath = "" }, "--signing-key"},
		"no verify keys":    {func(c *config) { c.verifyKeys = nil }, "--verify-key"},
		"zero ttl":          {func(c *config) { c.accessTokenTTL = 0 }, "--access-token-ttl"},
		"negative ttl":      {func(c *config) { c.accessTokenTTL = -time.Second }, "--access-token-ttl"},
		"no rubric version": {func(c *config) { c.rubricVersion = "" }, "--rubric-version"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			require.ErrorContains(t, cfg.validate(), tc.want)
		})
	}
}

func TestVerifyKeyParsing(t *testing.T) {
	t.Run("kid and path", func(t *testing.T) {
		cfg := config{verifyKeys: []string{"2026-07:/keys/july.pub", "2026-08:/keys/august.pub"}}

		got, err := cfg.verifyKeyPaths()
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			"2026-07": "/keys/july.pub",
			"2026-08": "/keys/august.pub",
		}, got)
	})

	t.Run("a path may contain colons", func(t *testing.T) {
		// Windows paths and some secret mounts do. Splitting on the last colon
		// would truncate them.
		cfg := config{verifyKeys: []string{"dev:C:/keys/dev.pub"}}

		got, err := cfg.verifyKeyPaths()
		require.NoError(t, err)
		require.Equal(t, "C:/keys/dev.pub", got["dev"])
	})

	t.Run("rejects malformed entries", func(t *testing.T) {
		for name, entry := range map[string]string{
			"no separator": "justapath",
			"empty kid":    ":/keys/dev.pub",
			"empty path":   "dev:",
		} {
			t.Run(name, func(t *testing.T) {
				cfg := config{verifyKeys: []string{entry}}
				_, err := cfg.verifyKeyPaths()
				require.ErrorContains(t, err, "kid:path")
			})
		}
	})

	t.Run("rejects a duplicated kid", func(t *testing.T) {
		// Two keys under one kid means the verifier's choice between them is
		// map-iteration order — which is to say, undefined.
		cfg := config{verifyKeys: []string{"dev:/a.pub", "dev:/b.pub"}}

		_, err := cfg.verifyKeyPaths()
		require.ErrorContains(t, err, "given twice")
	})
}

func TestRunRefusesBeforeTouchingAnything(t *testing.T) {
	err := run(context.Background(), &config{})
	require.ErrorContains(t, err, "--database-url")
}

// TestTheAPIRefusesToJudge is the assertion behind refusingAI.
//
// EvaluationService is wired into cmd/api only for the admin sweep, which is
// enqueue-only. If a future change made an API path call the model, this is
// what turns that into a loud failure rather than an invented judgement written
// to a contributor's scorecard.
func TestTheAPIRefusesToJudge(t *testing.T) {
	var ai port.AIClient = refusingAI{}

	got, err := ai.Judge(context.Background(), port.JudgeRequest{})
	require.Nil(t, got)
	require.ErrorContains(t, err, "cmd/evaluator")
}

func TestBuildReportsAnUnreachableDatabase(t *testing.T) {
	// Ping rather than trusting pgxpool.New, which is lazy: without the ping a
	// broken database would only be discovered by the first request.
	private, public := keypair(t)

	_, err := build(context.Background(), &config{
		databaseURL:    "postgres://nobody@127.0.0.1:1/none",
		signingKeyPath: private,
		verifyKeys:     []string{"dev:" + public},
		accessTokenTTL: time.Minute,
		rubricVersion:  "v1",
	})
	require.ErrorContains(t, err, "reaching the database")
}

func TestBuildRefusesASigningKeyNothingCanVerify(t *testing.T) {
	// Otherwise the API boots happily and every token it mints is rejected by
	// every verifier, including itself (ADR-0011).
	private, _ := keypair(t)
	_, unrelated := keypair(t)

	_, err := build(context.Background(), &config{
		databaseURL:    "postgres://x",
		signingKeyPath: private,
		verifyKeys:     []string{"other:" + unrelated},
		accessTokenTTL: time.Minute,
		rubricVersion:  "v1",
	})
	require.ErrorContains(t, err, "no matching --verify-key entry")
}

func TestBuildReportsAMalformedVerifyKeyFlag(t *testing.T) {
	_, err := build(context.Background(), &config{
		databaseURL: "postgres://x", signingKeyPath: "/k.pem",
		verifyKeys: []string{"nokid"}, accessTokenTTL: time.Minute, rubricVersion: "v1",
	})
	require.ErrorContains(t, err, "kid:path")
}

// keypair writes a matching Ed25519 pair and returns their paths.
//
// Generated per call, so a test that wants two UNRELATED keys simply calls it
// twice — which is what the mismatch assertion needs.
func keypair(t *testing.T) (private, public string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	dir := t.TempDir()
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)

	private = filepath.Join(dir, "key.pem")
	public = filepath.Join(dir, "key.pub")
	require.NoError(t, os.WriteFile(private,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))
	require.NoError(t, os.WriteFile(public,
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644))
	return private, public
}
