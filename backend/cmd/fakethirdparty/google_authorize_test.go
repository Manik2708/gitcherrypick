package main

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

const devGoogleCallback = "http://localhost:5173/auth/google/callback"

// newGoogleSignInServer is the stand-in as dev.sh runs it: the Google sign-in
// page enabled.
func newGoogleSignInServer(t *testing.T, callback string) *httptest.Server {
	t.Helper()
	store := NewStore()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewServer(store, srv.URL).WithGoogleCallback(callback).Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// googleCast is two seeded Google identities, as devdata loads them.
const googleCast = `{"google":{"oauth_codes":{
	"e2e-hank":{"userinfo":{"sub":"google-hank","email":"hank@acme.com","email_verified":true,"name":"Hank Rivera"}},
	"e2e-zed":{"userinfo":{"sub":"google-zed","email":"zed@example.com","email_verified":true}}
}}}`

var googleLink = regexp.MustCompile(`href="([^"]+)"`)

func TestGoogleAuthorizeIsOffUnlessConfigured(t *testing.T) {
	// The fixture suite runs without the flag and must see exactly what it
	// saw before: no page.
	srv, _ := newRawServer(t)
	do(t, srv, http.MethodPost, "/_load", "", googleCast)

	require.Equal(t, http.StatusNotFound,
		do(t, srv, http.MethodGet, "/google/o/oauth2/v2/auth?state=s", "", "").Status)
}

func TestGoogleAuthorizeNeedsALoadedCast(t *testing.T) {
	srv := newGoogleSignInServer(t, devGoogleCallback)

	got := do(t, srv, http.MethodGet, "/google/o/oauth2/v2/auth?state=s", "", "")
	require.Equal(t, http.StatusServiceUnavailable, got.Status)
	require.Contains(t, string(got.Body), "--control-url")
}

func TestGoogleAuthorizeLinksEachIdentityToTheCallback(t *testing.T) {
	srv := newGoogleSignInServer(t, devGoogleCallback+"/")
	do(t, srv, http.MethodPost, "/_load", "", googleCast)

	// A state with characters that must survive encoding untouched.
	state := "abc+/=&def"
	got := do(t, srv, http.MethodGet,
		"/google/o/oauth2/v2/auth?client_id=dev&response_type=code&state="+url.QueryEscape(state), "", "")
	require.Equal(t, http.StatusOK, got.Status)

	body := string(got.Body)
	require.Contains(t, body, "Hank Rivera")
	require.Contains(t, body, "zed@example.com", "an identity with no name is shown by its address")

	links := googleLink.FindAllStringSubmatch(body, -1)
	require.Len(t, links, 2, "one link per loaded identity, in code order")

	for i, wantCode := range []string{"e2e-hank", "e2e-zed"} {
		target, err := url.Parse(html.UnescapeString(links[i][1]))
		require.NoError(t, err)
		require.Equal(t, devGoogleCallback, target.Scheme+"://"+target.Host+target.Path,
			"the configured callback, without a trailing slash")
		require.Equal(t, wantCode, target.Query().Get("code"))
		require.Equal(t, state, target.Query().Get("state"), "state is echoed untouched")
	}

	t.Run("the code it links to is one the token endpoint honours", func(t *testing.T) {
		require.Equal(t, http.StatusOK,
			do(t, srv, http.MethodPost, "/google/token", "", "code=e2e-hank").Status)
	})
}

func TestGoogleAuthorizePageEscapesWhatItShows(t *testing.T) {
	page := googleAuthorizePage(devGoogleCallback, "s", []string{"c"}, map[string]GoogleGrant{
		"c": {UserInfo: []byte(`{"email":"x@y.z","name":"<script>alert(1)</script>"}`)},
	})
	require.NotContains(t, page, "<script>alert")
	require.Contains(t, page, "&lt;script&gt;")
}
