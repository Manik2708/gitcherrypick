package e2e_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/e2e"
)

// Normalisation decides what a recorded snapshot asserts, so a rule that is too
// broad here silently weakens every fixture that runs through it — without
// failing anything, which is what makes it worth a test of its own.

// normalised runs a body through Normalise and returns the value at one key.
//
// Decoded rather than substring-matched: json.Marshal escapes "<" as <, so
// searching the raw bytes for "<token>" finds nothing even when the placeholder
// is exactly what was written.
func normalised(t *testing.T, body string) map[string]any {
	t.Helper()

	out, err := e2e.Normalise(json.RawMessage(body))
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out, &decoded))
	return decoded
}

func TestNormaliseKeepsAStableURL(t *testing.T) {
	// The bug this pins: `url` was normalised to <token> wholesale, because it
	// once only ever meant a share link. It now also means a pull request, and
	// a PR link replaced by a placeholder asserts only "some string over 16
	// characters" — so the thing the field exists for, a verdict the reader can
	// click through to and check, would be pinned by nothing.
	got := normalised(t, `{"url":"https://github.com/acme/platform/pull/55"}`)
	require.Equal(t, "https://github.com/acme/platform/pull/55", got["url"])
}

func TestNormaliseStillHidesSecrets(t *testing.T) {
	// The other half. A share link is minted per run, so recording it verbatim
	// would fail the next run — and it is a credential, which is reason enough
	// not to write it into a file either way.
	const minted = "vB3s1Q7nQx2fK9aL0pR4tE6yU8iO2wZ5cM7xN1bV3dQ"

	cases := map[string]string{
		"a share link":    `{"url":"/public/scorecard/` + minted + `"}`,
		"a bare token":    `{"token":"` + minted + `"}`,
		"a refresh token": `{"refresh_token":"` + minted + `"}`,
		"an access token": `{"access_token":"header.payload.a-long-signature"}`,
		"a share_token":   `{"share_token":"` + minted + `"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			for _, v := range normalised(t, body) {
				require.Equal(t, e2e.PlaceholderToken, v)
			}
		})
	}
}

// A short path segment is a name, not a secret. This is the line the two rules
// above are drawn on, so it is worth stating directly.
func TestNormaliseLeavesShortSegmentsAlone(t *testing.T) {
	for _, url := range []string{
		"https://github.com/acme/bigrepo/pull/101",
		"https://github.com/acme/platform",
		"/me/share-link",
	} {
		t.Run(url, func(t *testing.T) {
			got := normalised(t, `{"url":`+quote(url)+`}`)
			require.Equal(t, url, got["url"], "a stable url must survive normalisation")
		})
	}
}

// Nesting is where a blanket rule does the most damage: the PR links live
// inside prs[] and evidence[], not at the root.
func TestNormaliseDescendsIntoArrays(t *testing.T) {
	out, err := e2e.Normalise(json.RawMessage(
		`{"prs":[{"url":"https://github.com/acme/platform/pull/55","pr_number":55}]}`))
	require.NoError(t, err)

	var decoded struct {
		PRs []struct {
			URL string `json:"url"`
		} `json:"prs"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded))
	require.Equal(t, "https://github.com/acme/platform/pull/55", decoded.PRs[0].URL)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
