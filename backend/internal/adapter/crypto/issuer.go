package crypto

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Issuer is the token name in `iss`, and the only value Verify accepts.
const Issuer = "gitcherrypick"

// signingMethod is the only algorithm this issuer will produce or accept.
//
// Pinned rather than read from the token header. Trusting the header is the
// classic JWT break: an attacker flips `alg` to HS256, signs with the public
// key as the HMAC secret, and the verifier accepts it.
var signingMethod = jwt.SigningMethodEdDSA

// ErrInvalidToken is every verification failure.
//
// One error for an expired token, an unknown `kid`, a bad signature and a
// malformed body. The caller turns it into 401, and a client learns only that
// the token did not work — never which part of it was wrong.
var ErrInvalidToken = errors.New("invalid access token")

// Keyset is one signing key and every key still trusted for verification.
//
// Rotation is: add the new key as verify-only, switch signing to it, then drop
// the old one once every token it signed has expired — fifteen minutes later
// (ADR-0011). Both keys are trusted in the middle step, which is what makes the
// switch invisible to anyone signed in.
type Keyset struct {
	signingKID string
	signing    ed25519.PrivateKey
	verify     map[string]ed25519.PublicKey
}

// LoadKeyset reads the signing key and the trusted verification keys from disk.
//
// Files rather than flag or environment values: a PEM passed as an argument
// reaches the process table, crash dumps, and anything that logs argv.
// Kubernetes secrets, Docker secrets and systemd credentials all present as
// files, so one flag shape works across every deployment unchanged.
//
// There is deliberately no generate-if-absent path. A production that forgot
// the flag would boot successfully and silently, sign with a key nobody holds,
// and log every user out on each deploy — or, across replicas, mint tokens the
// other replicas reject. Failing here converts that into an error at the only
// moment it is cheap to fix.
func LoadKeyset(signingKeyPath string, verifyKeyPaths map[string]string) (*Keyset, error) {
	if signingKeyPath == "" {
		return nil, errors.New("no signing key configured: pass --signing-key")
	}
	if len(verifyKeyPaths) == 0 {
		return nil, errors.New("no verification keys configured: pass --verify-key kid:path")
	}

	signing, err := loadPrivateKey(signingKeyPath)
	if err != nil {
		return nil, err
	}

	verify := make(map[string]ed25519.PublicKey, len(verifyKeyPaths))
	for kid, path := range verifyKeyPaths {
		if kid == "" {
			return nil, fmt.Errorf("a verification key has an empty kid: %s", path)
		}
		pub, err := loadPublicKey(path)
		if err != nil {
			return nil, fmt.Errorf("verification key %q: %w", kid, err)
		}
		verify[kid] = pub
	}

	// The signing kid is DERIVED by matching the private key against the
	// trusted set, never configured separately. Two flags naming the same key
	// could disagree, and the failure would be tokens that verify nowhere —
	// visible only once every request started returning 401.
	signingPub, ok := signing.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("signing key %s is not Ed25519", signingKeyPath)
	}
	kid := ""
	for candidate, pub := range verify {
		if pub.Equal(signingPub) {
			kid = candidate
			break
		}
	}
	if kid == "" {
		return nil, fmt.Errorf(
			"the signing key %s has no matching --verify-key entry, so nothing could verify "+
				"the tokens it signs: add it as a verification key under its kid", signingKeyPath)
	}

	return &Keyset{signingKID: kid, signing: signing, verify: verify}, nil
}

// SigningKID is the kid stamped on newly issued tokens.
func (k *Keyset) SigningKID() string { return k.signingKID }

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	block, err := readPEM(path)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing %s as a PKCS#8 private key: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an Ed25519 private key", path, parsed)
	}
	return key, nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	block, err := readPEM(path)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing %s as a PKIX public key: %w", path, err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an Ed25519 public key", path, parsed)
	}
	return key, nil
}

func readPEM(path string) (*pem.Block, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s is not PEM", path)
	}
	return block, nil
}

// TokenIssuer implements port.TokenIssuer over Ed25519 (ADR-0002 §8).
type TokenIssuer struct {
	keys  *Keyset
	clock port.Clock
}

// NewTokenIssuer wires the access-token issuer.
func NewTokenIssuer(keys *Keyset, clock port.Clock) *TokenIssuer {
	return &TokenIssuer{keys: keys, clock: clock}
}

var _ port.TokenIssuer = (*TokenIssuer)(nil)

// Issue signs an access token asserting identity and nothing else.
//
// No organization, no hiring capability, no verification state. Embedding any
// of those would let a suspended organization keep acting until the token
// expired, when RequireHiringCapability exists to revoke immediately
// (ADR-0011).
//
// A claims value with no subject is refused rather than signed. That case is
// reachable — domain.Principal.Subject() returns "" for a Principal built from
// Kind alone — and a token identifying nobody is worse than no token, because
// it would authenticate and then resolve to whichever row an empty id matched.
func (t *TokenIssuer) Issue(ctx context.Context, c port.AccessClaims, ttl time.Duration) (string, error) {
	if c.Subject == "" {
		return "", errors.New("refusing to issue an access token with no subject")
	}
	if !validKind(c.Kind) {
		return "", fmt.Errorf("refusing to issue an access token for kind %q", c.Kind)
	}
	if ttl <= 0 {
		return "", fmt.Errorf("refusing to issue an access token with a %s lifetime", ttl)
	}

	now := t.clock.Now()
	token := jwt.NewWithClaims(signingMethod, jwt.MapClaims{
		"iss":  Issuer,
		"sub":  c.Subject,
		"kind": string(c.Kind),
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
		"jti":  uuid.NewString(),
	})
	token.Header["kid"] = t.keys.signingKID

	signed, err := token.SignedString(t.keys.signing)
	if err != nil {
		return "", fmt.Errorf("signing the access token: %w", err)
	}
	return signed, nil
}

// Verify checks a token and returns what it asserts.
//
// It returns CLAIMS, not a principal. The claims carry an id and a kind;
// turning those into a domain.Principal means reading the account, which is
// middleware's job and deliberately not cached in the token (ADR-0011).
func (t *TokenIssuer) Verify(ctx context.Context, token string) (*port.AccessClaims, error) {
	parsed, err := jwt.Parse(token, t.keyFor,
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.clock.Now),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected claim type %T", ErrInvalidToken, parsed.Claims)
	}

	subject, _ := claims["sub"].(string)
	if subject == "" {
		return nil, fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	rawKind, _ := claims["kind"].(string)
	kind := domain.PrincipalKind(rawKind)
	if !validKind(kind) {
		// Without a kind there is no table to read: the three account types
		// share none, and probing all three would let a contributor id resolve
		// to a hirer.
		return nil, fmt.Errorf("%w: kind %q", ErrInvalidToken, rawKind)
	}

	return &port.AccessClaims{Subject: subject, Kind: kind}, nil
}

// keyFor resolves the token's kid against the trusted set.
//
// An unknown kid is rejected outright rather than falling back to trying every
// key in turn. Trying keys would make a retired key indistinguishable from a
// current one at the point of use, and it is the behaviour that makes rotation
// impossible to reason about — which is why ADR-0011 ships kid from the start
// rather than adding it later.
func (t *TokenIssuer) keyFor(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, errors.New("no kid header")
	}
	key, ok := t.keys.verify[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return key, nil
}

func validKind(k domain.PrincipalKind) bool {
	switch k {
	case domain.KindContributor, domain.KindHirer, domain.KindAdmin:
		return true
	}
	return false
}
