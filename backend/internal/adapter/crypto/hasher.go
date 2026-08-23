// Package crypto implements the three auth primitives the platform owns:
// password hashing, opaque token generation, and access-token signing.
//
// Nothing here talks to a third party, which is why ADR-0010 keeps all of it
// REAL in the integration suite. A fake TokenIssuer would be an authentication
// bypass, and a fake PasswordHasher would mean no test ever proves a password
// can be verified.
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Argon2 parameters for new hashes, following the OWASP second-choice profile:
// 19 MiB of memory, two passes, one lane.
//
// These describe hashes written FROM NOW ON. Verify reads its parameters from
// the stored hash instead, so raising them here does not invalidate a single
// existing password — see the encoding note on Hash.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// Argon2Hasher implements port.PasswordHasher.
//
// Used only by the email provider (ADR-0002). A contributor has no password at
// all, and a hirer signing in through Google never reaches this code.
type Argon2Hasher struct{}

// NewArgon2Hasher returns the password hasher.
func NewArgon2Hasher() *Argon2Hasher { return &Argon2Hasher{} }

var _ port.PasswordHasher = (*Argon2Hasher)(nil)

// Hash derives a new argon2id hash with a fresh random salt.
//
// The result is PHC string format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<digest>
//
// Self-describing on purpose. The cost parameters travel WITH each hash, so
// they can be raised for new passwords while every existing one still verifies
// under the parameters it was written with. A bare digest would make any
// parameter change a mass lockout.
func (h *Argon2Hasher) Hash(plaintext string) ([]byte, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("reading salt: %w", err)
	}

	digest := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	)
	return []byte(encoded), nil
}

// Verify reports whether plaintext produced hash.
//
// The comparison is constant-time. It also happens INSIDE this method rather
// than at the call site, which is what the port comment means: a caller that
// fetched the hash and compared it with == would leak how many leading bytes
// matched, and that is enough to recover a digest byte by byte.
//
// Every failure — malformed encoding, unknown algorithm, wrong password —
// returns false and nothing else. The caller turns that into
// ErrInvalidCredentials, which is the same error an unknown email produces, so
// sign-in never becomes an enumeration oracle.
func (h *Argon2Hasher) Verify(hash []byte, plaintext string) bool {
	salt, digest, params, err := decodeArgon2(string(hash))
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(plaintext), salt,
		params.time, params.memory, params.threads, uint32(len(digest)))

	return subtle.ConstantTimeCompare(computed, digest) == 1
}

type argon2Params struct {
	memory  uint32
	time    uint32
	threads uint8
}

var errMalformedHash = errors.New("malformed argon2 hash")

// decodeArgon2 pulls the salt, digest and cost parameters back out of a PHC
// string.
//
// It accepts argon2id only. argon2i and argon2d are weaker against the attacks
// that matter for passwords, and silently honouring a stored variant would let
// anyone who could write to the hash column downgrade the algorithm.
func decodeArgon2(encoded string) (salt, digest []byte, p argon2Params, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return nil, nil, p, errMalformedHash
	}
	if parts[1] != "argon2id" {
		return nil, nil, p, fmt.Errorf("%w: algorithm %q", errMalformedHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, p, fmt.Errorf("%w: version: %w", errMalformedHash, err)
	}
	if version != argon2.Version {
		return nil, nil, p, fmt.Errorf("%w: version %d", errMalformedHash, version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return nil, nil, p, fmt.Errorf("%w: parameters: %w", errMalformedHash, err)
	}

	// argon2.IDKey panics on a zero lane count and on memory below 8*threads.
	// These values come out of the database, so a corrupted or tampered row
	// must fail the comparison rather than take the process down.
	if p.time == 0 || p.threads == 0 || p.memory < 8*uint32(p.threads) {
		return nil, nil, p, fmt.Errorf("%w: m=%d,t=%d,p=%d", errMalformedHash, p.memory, p.time, p.threads)
	}

	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return nil, nil, p, fmt.Errorf("%w: salt: %w", errMalformedHash, err)
	}
	if digest, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return nil, nil, p, fmt.Errorf("%w: digest: %w", errMalformedHash, err)
	}
	if len(salt) == 0 || len(digest) == 0 {
		return nil, nil, p, errMalformedHash
	}
	return salt, digest, p, nil
}
