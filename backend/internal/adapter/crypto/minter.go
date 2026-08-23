package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// TokenBytes is the entropy behind every opaque token: refresh tokens, share
// links, invitations, and OAuth state.
//
// 256 bits. These are bearer credentials with no password behind them and, for
// refresh tokens, a thirty-day life, so the only thing standing between an
// attacker and an account is that the value cannot be guessed.
const TokenBytes = 32

// Minter implements port.TokenMinter.
//
// One generator for every opaque token in the system. ADR-0011 folded the
// refresh-token methods off TokenIssuer for this reason: two generators are two
// places to decide entropy and hash algorithm, and two places to get it wrong.
type Minter struct{}

// NewMinter returns the opaque-token generator.
func NewMinter() *Minter { return &Minter{} }

var _ port.TokenMinter = (*Minter)(nil)

// Mint returns the plaintext to hand out and the hash to store.
//
// Returning both is what forces the caller to store the hash — there is no
// method here that would let it store the plaintext instead. A database read
// cannot yield a usable token, and neither can a backup.
func (m *Minter) Mint() (plaintext string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("reading random bytes: %w", err)
	}

	plaintext = base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, m.Hash(plaintext), nil
}

// Hash is the stored form of a token.
//
// A plain SHA-256, deliberately, where passwords get argon2id. The difference
// is the input: this is 256 bits of uniform randomness with no structure to
// guess, so there is no dictionary to slow down and a work factor would only
// tax every lookup. A password is low-entropy and human-chosen, which is
// exactly what argon2 exists for.
func (m *Minter) Hash(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// EqualHash compares two token hashes in constant time.
//
// Hash output is not secret, but the comparison still runs constant-time: a
// caller looking a token up by hash and then confirming it with == would leak
// the matching prefix length, and that is enough to walk a valid hash out one
// byte at a time.
func EqualHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
