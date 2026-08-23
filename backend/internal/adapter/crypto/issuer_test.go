package crypto_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/crypto"
	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// fixedClock pins time so expiry is asserted rather than waited for.
type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

var epoch = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// writeKeypair generates an Ed25519 pair and writes both halves as PEM,
// returning their paths.
func writeKeypair(t *testing.T, name string) (privPath, pubPath string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	dir := t.TempDir()
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)

	privPath = filepath.Join(dir, name+".pem")
	pubPath = filepath.Join(dir, name+".pub")
	require.NoError(t, os.WriteFile(privPath,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))
	require.NoError(t, os.WriteFile(pubPath,
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644))
	return privPath, pubPath
}

// newIssuer builds an issuer over a single fresh key under kid "current".
func newIssuer(t *testing.T, clock port.Clock) (*crypto.TokenIssuer, *crypto.Keyset) {
	t.Helper()
	priv, pub := writeKeypair(t, "current")
	keys, err := crypto.LoadKeyset(priv, map[string]string{"current": pub})
	require.NoError(t, err)
	return crypto.NewTokenIssuer(keys, clock), keys
}

func TestLoadKeyset(t *testing.T) {
	t.Run("derives the signing kid from the trusted set", func(t *testing.T) {
		priv, pub := writeKeypair(t, "k")
		keys, err := crypto.LoadKeyset(priv, map[string]string{"2026-08": pub})
		require.NoError(t, err)
		require.Equal(t, "2026-08", keys.SigningKID())
	})

	t.Run("refuses a signing key nothing can verify", func(t *testing.T) {
		// Otherwise the API boots happily and every token it mints is rejected
		// by every verifier, including itself.
		signing, _ := writeKeypair(t, "signing")
		_, unrelated := writeKeypair(t, "unrelated")

		_, err := crypto.LoadKeyset(signing, map[string]string{"other": unrelated})
		require.ErrorContains(t, err, "no matching --verify-key entry")
	})

	t.Run("refuses to start with no signing key", func(t *testing.T) {
		_, pub := writeKeypair(t, "k")
		_, err := crypto.LoadKeyset("", map[string]string{"k": pub})
		require.ErrorContains(t, err, "--signing-key")
	})

	t.Run("refuses to start with no verification keys", func(t *testing.T) {
		priv, _ := writeKeypair(t, "k")
		_, err := crypto.LoadKeyset(priv, nil)
		require.ErrorContains(t, err, "--verify-key")
	})

	t.Run("refuses an empty kid", func(t *testing.T) {
		priv, pub := writeKeypair(t, "k")
		_, err := crypto.LoadKeyset(priv, map[string]string{"": pub})
		require.ErrorContains(t, err, "empty kid")
	})

	t.Run("reports unreadable and malformed key material", func(t *testing.T) {
		priv, pub := writeKeypair(t, "k")
		dir := t.TempDir()

		notPEM := filepath.Join(dir, "garbage.pem")
		require.NoError(t, os.WriteFile(notPEM, []byte("not a key"), 0o600))

		// A public key where a private one belongs.
		_, err := crypto.LoadKeyset(pub, map[string]string{"k": pub})
		require.ErrorContains(t, err, "PKCS#8")

		_, err = crypto.LoadKeyset(filepath.Join(dir, "absent.pem"), map[string]string{"k": pub})
		require.ErrorContains(t, err, "reading")

		_, err = crypto.LoadKeyset(notPEM, map[string]string{"k": pub})
		require.ErrorContains(t, err, "not PEM")

		_, err = crypto.LoadKeyset(priv, map[string]string{"k": notPEM})
		require.ErrorContains(t, err, "not PEM")

		// A private key offered as a verification key.
		_, err = crypto.LoadKeyset(priv, map[string]string{"k": priv})
		require.ErrorContains(t, err, "PKIX")
	})
}

// TestLoadKeysetRejectsWrongAlgorithm covers well-formed PEM holding the wrong
// kind of key. ADR-0002 fixes Ed25519; an RSA key here would parse cleanly and
// then fail at signing time, in production, on the first sign-in.
func TestLoadKeysetRejectsWrongAlgorithm(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	dir := t.TempDir()
	privDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	require.NoError(t, err)

	rsaPriv := filepath.Join(dir, "rsa.pem")
	rsaPub := filepath.Join(dir, "rsa.pub")
	require.NoError(t, os.WriteFile(rsaPriv,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))
	require.NoError(t, os.WriteFile(rsaPub,
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644))

	edPriv, edPub := writeKeypair(t, "ed")

	_, err = crypto.LoadKeyset(rsaPriv, map[string]string{"k": edPub})
	require.ErrorContains(t, err, "not an Ed25519 private key")

	_, err = crypto.LoadKeyset(edPriv, map[string]string{"k": rsaPub})
	require.ErrorContains(t, err, "not an Ed25519 public key")
}

func TestIssueAndVerify(t *testing.T) {
	clock := &fixedClock{now: epoch}
	issuer, keys := newIssuer(t, clock)
	ctx := context.Background()

	t.Run("round-trips identity", func(t *testing.T) {
		token, err := issuer.Issue(ctx,
			port.AccessClaims{Subject: "user-1", Kind: domain.KindContributor}, 15*time.Minute)
		require.NoError(t, err)

		claims, err := issuer.Verify(ctx, token)
		require.NoError(t, err)
		require.Equal(t, "user-1", claims.Subject)
		require.Equal(t, domain.KindContributor, claims.Kind)
	})

	t.Run("stamps the signing kid", func(t *testing.T) {
		token, err := issuer.Issue(ctx,
			port.AccessClaims{Subject: "user-1", Kind: domain.KindHirer}, time.Minute)
		require.NoError(t, err)

		parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
		require.NoError(t, err)
		require.Equal(t, keys.SigningKID(), parsed.Header["kid"])
		require.Equal(t, "EdDSA", parsed.Header["alg"])
	})

	t.Run("asserts identity and nothing else", func(t *testing.T) {
		// ADR-0011: an embedded capability would survive its own revocation
		// for the life of the token.
		token, err := issuer.Issue(ctx,
			port.AccessClaims{Subject: "hirer-1", Kind: domain.KindHirer}, time.Minute)
		require.NoError(t, err)

		claims := jwt.MapClaims{}
		_, _, err = jwt.NewParser().ParseUnverified(token, claims)
		require.NoError(t, err)

		require.ElementsMatch(t,
			[]string{"iss", "sub", "kind", "iat", "exp", "jti"},
			keysOf(claims))
		require.Equal(t, crypto.Issuer, claims["iss"])
		require.EqualValues(t, epoch.Unix(), claims["iat"])
		require.EqualValues(t, epoch.Add(time.Minute).Unix(), claims["exp"])
	})

	t.Run("gives every token a distinct jti", func(t *testing.T) {
		first := claimsOf(t, mustIssue(t, issuer, "user-1"))
		second := claimsOf(t, mustIssue(t, issuer, "user-1"))
		require.NotEqual(t, first["jti"], second["jti"])
	})
}

func TestIssueRefusals(t *testing.T) {
	issuer, _ := newIssuer(t, &fixedClock{now: epoch})
	ctx := context.Background()

	t.Run("refuses a token with no subject", func(t *testing.T) {
		// Reachable: domain.Principal.Subject() returns "" for a Principal
		// built from Kind alone, which is how the Refresh bug arose. Such a
		// token would authenticate and then resolve to whatever an empty id
		// matched.
		_, err := issuer.Issue(ctx,
			port.AccessClaims{Kind: domain.KindContributor}, time.Minute)
		require.ErrorContains(t, err, "no subject")
	})

	t.Run("refuses an unknown kind", func(t *testing.T) {
		_, err := issuer.Issue(ctx,
			port.AccessClaims{Subject: "u", Kind: domain.PrincipalKind("superuser")}, time.Minute)
		require.ErrorContains(t, err, "superuser")

		_, err = issuer.Issue(ctx, port.AccessClaims{Subject: "u"}, time.Minute)
		require.Error(t, err)
	})

	t.Run("refuses a non-positive lifetime", func(t *testing.T) {
		for _, ttl := range []time.Duration{0, -time.Second} {
			_, err := issuer.Issue(ctx,
				port.AccessClaims{Subject: "u", Kind: domain.KindAdmin}, ttl)
			require.ErrorContains(t, err, "lifetime")
		}
	})
}

func TestVerifyRejections(t *testing.T) {
	clock := &fixedClock{now: epoch}
	issuer, _ := newIssuer(t, clock)
	ctx := context.Background()

	t.Run("rejects an expired token", func(t *testing.T) {
		priv, pub := writeKeypair(t, "current")
		keys, err := crypto.LoadKeyset(priv, map[string]string{"current": pub})
		require.NoError(t, err)

		token := mustIssue(t, crypto.NewTokenIssuer(keys, clock), "user-1")

		// Same keyset, sixteen minutes later. The signature is still good and
		// the token is still refused.
		later := crypto.NewTokenIssuer(keys, &fixedClock{now: epoch.Add(16 * time.Minute)})
		_, err = later.Verify(ctx, token)
		require.ErrorIs(t, err, crypto.ErrInvalidToken)
	})

	t.Run("rejects a token signed by an untrusted key", func(t *testing.T) {
		other, _ := newIssuer(t, clock)
		foreign := mustIssue(t, other, "user-1")

		_, err := issuer.Verify(ctx, foreign)
		require.ErrorIs(t, err, crypto.ErrInvalidToken)
	})

	t.Run("rejects garbage", func(t *testing.T) {
		for _, token := range []string{"", "not.a.token", "a.b.c", "Bearer x"} {
			_, err := issuer.Verify(ctx, token)
			require.ErrorIs(t, err, crypto.ErrInvalidToken)
		}
	})
}

// TestVerifyRejectsAlgorithmConfusion is the attack the pinned signing method
// exists to stop: flip `alg` to HS256 and sign with the PUBLIC key as the HMAC
// secret. A verifier that trusted the header would accept it, because the
// public key is not secret.
func TestVerifyRejectsAlgorithmConfusion(t *testing.T) {
	priv, pub := writeKeypair(t, "current")
	keys, err := crypto.LoadKeyset(priv, map[string]string{"current": pub})
	require.NoError(t, err)
	issuer := crypto.NewTokenIssuer(keys, &fixedClock{now: epoch})

	publicPEM, err := os.ReadFile(pub)
	require.NoError(t, err)

	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":  crypto.Issuer,
		"sub":  "admin-1",
		"kind": string(domain.KindAdmin),
		"iat":  epoch.Unix(),
		"exp":  epoch.Add(time.Hour).Unix(),
		"jti":  "forged",
	})
	forged.Header["kid"] = "current"
	signed, err := forged.SignedString(publicPEM)
	require.NoError(t, err)

	_, err = issuer.Verify(context.Background(), signed)
	require.ErrorIs(t, err, crypto.ErrInvalidToken)
}

// TestVerifyRejectsUnknownKid pins the choice not to try every key in turn.
func TestVerifyRejectsUnknownKid(t *testing.T) {
	priv, pub := writeKeypair(t, "current")
	keys, err := crypto.LoadKeyset(priv, map[string]string{"current": pub})
	require.NoError(t, err)
	issuer := crypto.NewTokenIssuer(keys, &fixedClock{now: epoch})
	ctx := context.Background()

	t.Run("unknown kid", func(t *testing.T) {
		token := forgeWithHeader(t, priv, map[string]any{"alg": "EdDSA", "kid": "retired"})
		_, err := issuer.Verify(ctx, token)
		require.ErrorIs(t, err, crypto.ErrInvalidToken)
	})

	t.Run("absent kid", func(t *testing.T) {
		token := forgeWithHeader(t, priv, map[string]any{"alg": "EdDSA"})
		_, err := issuer.Verify(ctx, token)
		require.ErrorIs(t, err, crypto.ErrInvalidToken)
	})
}

// TestRotation is the property the whole kid mechanism exists for: a token
// signed before the switch keeps working until it expires.
func TestRotation(t *testing.T) {
	oldPriv, oldPub := writeKeypair(t, "july")
	newPriv, newPub := writeKeypair(t, "august")
	clock := &fixedClock{now: epoch}
	ctx := context.Background()

	before, err := crypto.LoadKeyset(oldPriv, map[string]string{"july": oldPub})
	require.NoError(t, err)
	token := mustIssue(t, crypto.NewTokenIssuer(before, clock), "user-1")

	// Mid-rotation: signing has moved to august, july is still trusted.
	during, err := crypto.LoadKeyset(newPriv, map[string]string{"july": oldPub, "august": newPub})
	require.NoError(t, err)
	rotating := crypto.NewTokenIssuer(during, clock)
	require.Equal(t, "august", during.SigningKID())

	claims, err := rotating.Verify(ctx, token)
	require.NoError(t, err, "a token minted before the switch must survive it")
	require.Equal(t, "user-1", claims.Subject)

	// After july is dropped, the old token stops verifying.
	after, err := crypto.LoadKeyset(newPriv, map[string]string{"august": newPub})
	require.NoError(t, err)
	_, err = crypto.NewTokenIssuer(after, clock).Verify(ctx, token)
	require.ErrorIs(t, err, crypto.ErrInvalidToken)
}

func TestVerifyRejectsForeignIssuer(t *testing.T) {
	priv, pub := writeKeypair(t, "current")
	keys, err := crypto.LoadKeyset(priv, map[string]string{"current": pub})
	require.NoError(t, err)
	issuer := crypto.NewTokenIssuer(keys, &fixedClock{now: epoch})

	for name, claims := range map[string]jwt.MapClaims{
		"foreign issuer": {"iss": "somebody-else", "sub": "u", "kind": "admin", "exp": epoch.Add(time.Hour).Unix()},
		"no expiry":      {"iss": crypto.Issuer, "sub": "u", "kind": "admin"},
		"no subject":     {"iss": crypto.Issuer, "kind": "admin", "exp": epoch.Add(time.Hour).Unix()},
		"unknown kind":   {"iss": crypto.Issuer, "sub": "u", "kind": "root", "exp": epoch.Add(time.Hour).Unix()},
		"absent kind":    {"iss": crypto.Issuer, "sub": "u", "exp": epoch.Add(time.Hour).Unix()},
	} {
		t.Run(name, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
			token.Header["kid"] = "current"
			signed, err := token.SignedString(loadPriv(t, priv))
			require.NoError(t, err)

			_, err = issuer.Verify(context.Background(), signed)
			require.ErrorIs(t, err, crypto.ErrInvalidToken)
		})
	}
}

// --- helpers -----------------------------------------------------------------

func mustIssue(t *testing.T, issuer *crypto.TokenIssuer, subject string) string {
	t.Helper()
	token, err := issuer.Issue(context.Background(),
		port.AccessClaims{Subject: subject, Kind: domain.KindContributor}, 15*time.Minute)
	require.NoError(t, err)
	return token
}

func claimsOf(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(token, claims)
	require.NoError(t, err)
	return claims
}

func keysOf(claims jwt.MapClaims) []string {
	out := make([]string, 0, len(claims))
	for k := range claims {
		out = append(out, k)
	}
	return out
}

func loadPriv(t *testing.T, path string) ed25519.PrivateKey {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(raw)
	require.NotNil(t, block)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	key, ok := parsed.(ed25519.PrivateKey)
	require.True(t, ok)
	return key
}

// forgeWithHeader signs a valid claim set under a caller-chosen header.
func forgeWithHeader(t *testing.T, privPath string, header map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": crypto.Issuer, "sub": "u", "kind": string(domain.KindAdmin),
		"iat": epoch.Unix(), "exp": epoch.Add(time.Hour).Unix(), "jti": "x",
	})
	token.Header = header
	signed, err := token.SignedString(loadPriv(t, privPath))
	require.NoError(t, err)
	return signed
}
