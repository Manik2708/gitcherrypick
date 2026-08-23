package crypto_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/crypto"
)

func TestArgon2Hasher(t *testing.T) {
	h := crypto.NewArgon2Hasher()

	t.Run("verifies the password it hashed", func(t *testing.T) {
		hash, err := h.Hash("correct horse battery staple")
		require.NoError(t, err)
		require.True(t, h.Verify(hash, "correct horse battery staple"))
	})

	t.Run("rejects a wrong password", func(t *testing.T) {
		hash, err := h.Hash("correct horse battery staple")
		require.NoError(t, err)
		require.False(t, h.Verify(hash, "correct horse battery stapl"))
		require.False(t, h.Verify(hash, ""))
	})

	t.Run("salts every hash separately", func(t *testing.T) {
		// Equal digests for one password would mean a stolen table could be
		// attacked once for every account sharing a password.
		first, err := h.Hash("same")
		require.NoError(t, err)
		second, err := h.Hash("same")
		require.NoError(t, err)

		require.NotEqual(t, first, second)
		require.True(t, h.Verify(first, "same"))
		require.True(t, h.Verify(second, "same"))
	})

	t.Run("encodes its parameters", func(t *testing.T) {
		hash, err := h.Hash("x")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(hash), "$argon2id$v=19$m=19456,t=2,p=1$"),
			"got %s", hash)
	})

	t.Run("verifies a hash written with different cost parameters", func(t *testing.T) {
		// The property that lets the cost be raised without locking anybody
		// out: Verify reads the parameters from the stored hash rather than
		// assuming the current constants. Built here with parameters far below
		// the current ones, exactly as a hash written years ago would be.
		const (
			memory  = 8
			passes  = 1
			lanes   = 1
			keyLen  = 32
			saltRaw = "saltsaltsaltsalt"
		)
		digest := argon2.IDKey([]byte("legacy"), []byte(saltRaw), passes, memory, lanes, keyLen)
		weak := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
			argon2.Version, memory, passes, lanes,
			base64.RawStdEncoding.EncodeToString([]byte(saltRaw)),
			base64.RawStdEncoding.EncodeToString(digest))

		require.True(t, h.Verify([]byte(weak), "legacy"))
		require.False(t, h.Verify([]byte(weak), "wrong"))
	})
}

func TestArgon2HasherRejectsMalformedHashes(t *testing.T) {
	h := crypto.NewArgon2Hasher()

	// Every one of these comes out of the database, so a corrupted or tampered
	// row must fail the comparison rather than take the process down.
	cases := map[string]string{
		"empty":              "",
		"not PHC":            "hunter2",
		"too few fields":     "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA",
		"argon2i downgrade":  "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$ZGlnZXN0",
		"argon2d downgrade":  "$argon2d$v=19$m=19456,t=2,p=1$c2FsdA$ZGlnZXN0",
		"unknown version":    "$argon2id$v=16$m=19456,t=2,p=1$c2FsdA$ZGlnZXN0",
		"unparseable params": "$argon2id$v=19$m=lots,t=2,p=1$c2FsdA$ZGlnZXN0",
		"zero lanes":         "$argon2id$v=19$m=19456,t=2,p=0$c2FsdA$ZGlnZXN0",
		"zero passes":        "$argon2id$v=19$m=19456,t=0,p=1$c2FsdA$ZGlnZXN0",
		"memory below lanes": "$argon2id$v=19$m=4,t=1,p=1$c2FsdA$ZGlnZXN0",
		"bad base64 salt":    "$argon2id$v=19$m=19456,t=2,p=1$!!!!$ZGlnZXN0",
		"bad base64 digest":  "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$!!!!",
		"empty salt":         "$argon2id$v=19$m=19456,t=2,p=1$$ZGlnZXN0",
		"empty digest":       "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$",
	}

	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			require.NotPanics(t, func() {
				require.False(t, h.Verify([]byte(hash), "anything"))
			})
		})
	}
}
