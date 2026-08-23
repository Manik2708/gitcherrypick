package crypto_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/crypto"
)

func TestMinter(t *testing.T) {
	m := crypto.NewMinter()

	t.Run("returns the hash of the plaintext it handed out", func(t *testing.T) {
		plaintext, hash, err := m.Mint()
		require.NoError(t, err)
		require.Equal(t, m.Hash(plaintext), hash)
	})

	t.Run("carries 256 bits of entropy", func(t *testing.T) {
		plaintext, _, err := m.Mint()
		require.NoError(t, err)

		raw, err := base64.RawURLEncoding.DecodeString(plaintext)
		require.NoError(t, err, "the plaintext must be URL-safe: it travels in links")
		require.Len(t, raw, crypto.TokenBytes)
	})

	t.Run("never repeats", func(t *testing.T) {
		seen := make(map[string]struct{}, 1000)
		for range 1000 {
			plaintext, _, err := m.Mint()
			require.NoError(t, err)
			_, duplicate := seen[plaintext]
			require.False(t, duplicate, "minted %q twice", plaintext)
			seen[plaintext] = struct{}{}
		}
	})

	t.Run("hashes deterministically", func(t *testing.T) {
		// Lookup by hash is how a presented token is found, so the same input
		// must always land on the same row.
		require.Equal(t, m.Hash("token"), m.Hash("token"))
		require.NotEqual(t, m.Hash("token"), m.Hash("token "))
	})

	t.Run("stores SHA-256 of the plaintext", func(t *testing.T) {
		sum := sha256.Sum256([]byte("token"))
		require.Equal(t, sum[:], m.Hash("token"))
	})
}

func TestEqualHash(t *testing.T) {
	m := crypto.NewMinter()

	require.True(t, crypto.EqualHash(m.Hash("a"), m.Hash("a")))
	require.False(t, crypto.EqualHash(m.Hash("a"), m.Hash("b")))
	require.False(t, crypto.EqualHash(m.Hash("a"), nil))
	require.False(t, crypto.EqualHash(m.Hash("a"), m.Hash("a")[:16]),
		"a prefix must not match: length is part of the comparison")
}
