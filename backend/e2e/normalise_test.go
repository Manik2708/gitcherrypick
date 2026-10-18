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

func TestUnpinnedMatchesAnyValueButNotAbsence(t *testing.T) {
	// <unpinned> marks a value the binding ADR leaves open. It must accept
	// whatever shape the implementation chooses — null included — and still
	// fail when the key is missing, or a gap would hide a dropped field.
	want := json.RawMessage(`{"error":"address_in_use","roles":"<unpinned>"}`)
	for _, got := range []string{
		`{"error":"address_in_use","roles":["0193"]}`,
		`{"error":"address_in_use","roles":[{"id":"x","title":"y"}]}`,
		`{"error":"address_in_use","roles":null}`,
	} {
		require.Empty(t, e2e.Compare(want, json.RawMessage(got)), got)
	}
	require.NotEmpty(t, e2e.Compare(want, json.RawMessage(`{"error":"address_in_use"}`)))
	require.NotEmpty(t, e2e.Compare(want, json.RawMessage(`{"error":"other","roles":[]}`)))
}

func TestEmailBodyFiltersReadTheUnescapedText(t *testing.T) {
	// html/template escapes an apostrophe as &#39;. A reason the administrator
	// typed must match as typed, or every reason with an apostrophe in it is
	// unassertable.
	body := `<p>Reason: The lease is in a director&#39;s own name, not the company&#39;s.</p>`
	reason := "The lease is in a director's own name, not the company's."
	require.True(t, e2e.BodyMatches(body, []string{reason}, nil))
	require.False(t, e2e.BodyMatches(body, nil, []string{"director's"}))
	require.False(t, e2e.BodyMatches(body, []string{"landlord's"}, nil))
	require.True(t, e2e.BodyMatches(`<p>1 &amp; 2</p>`, []string{"1 & 2"}, nil))
}
