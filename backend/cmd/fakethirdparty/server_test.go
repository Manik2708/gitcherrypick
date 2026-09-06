package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The adapter-driven tests in main_test.go cover the happy paths. These reach
// the branches a well-behaved client never takes — which is where a fake
// quietly answering "yes" would hide a real bug.

// reply is a completed response. The body is read and closed before it is
// returned, so a test never holds a connection open.
type reply struct {
	Status int
	Body   []byte
}

func (r reply) decode(t *testing.T, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.Body, out))
}

func newRawServer(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	store := NewStore()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewServer(store, srv.URL).Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, store
}

func do(t *testing.T, srv *httptest.Server, method, path, bearer, body string) reply {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path,
		strings.NewReader(body))
	require.NoError(t, err)

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	switch {
	case method != http.MethodPost:
	case strings.HasPrefix(body, "{"):
		req.Header.Set("Content-Type", "application/json")
	default:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return reply{Status: resp.StatusCode, Body: payload}
}

func TestGitHubRejectsUnknownCredentials(t *testing.T) {
	srv, _ := newRawServer(t)

	for _, path := range []string{"/github/user", "/github/user/emails"} {
		t.Run(path+" with no token", func(t *testing.T) {
			require.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, path, "", "").Status)
		})

		t.Run(path+" with an unissued token", func(t *testing.T) {
			require.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, path, "made-up", "").Status)
		})
	}
}

func TestGitHubGrantWithNoUserIsNotFound(t *testing.T) {
	// A code declared with no user payload. Answering 200 with an empty body
	// would make the adapter report "a user with no id" instead of the truth.
	srv, _ := newRawServer(t)
	do(t, srv, http.MethodPost, "/_load", "", `{"github":{"oauth_codes":{"c":{}}}}`)

	issued := do(t, srv, http.MethodPost, "/github/login/oauth/access_token", "", "code=c")
	require.Equal(t, http.StatusOK, issued.Status)

	var grant tokenGrant
	issued.decode(t, &grant)
	require.NotEmpty(t, grant.AccessToken)

	require.Equal(t, http.StatusNotFound,
		do(t, srv, http.MethodGet, "/github/user", grant.AccessToken, "").Status)
}

func TestGitHubUnknownRouteIs404(t *testing.T) {
	srv, _ := newRawServer(t)

	resp := do(t, srv, http.MethodGet, "/github/nothing/here", "", "")
	require.Equal(t, http.StatusNotFound, resp.Status)
	require.Contains(t, string(resp.Body), "Not Found",
		"GitHub's own body, so the adapter's 404 handling is what runs")
}

func TestGoogleUserInfoRejections(t *testing.T) {
	srv, _ := newRawServer(t)

	t.Run("an unissued token", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized,
			do(t, srv, http.MethodGet, "/google/userinfo", "made-up", "").Status)
	})

	t.Run("a code declaring no userinfo", func(t *testing.T) {
		do(t, srv, http.MethodPost, "/_load", "", `{"google":{"oauth_codes":{"c":{}}}}`)

		issued := do(t, srv, http.MethodPost, "/google/token", "", "code=c")
		require.Equal(t, http.StatusOK, issued.Status)

		var grant googleTokenGrant
		issued.decode(t, &grant)

		require.Equal(t, http.StatusNotFound,
			do(t, srv, http.MethodGet, "/google/userinfo", grant.AccessToken, "").Status)
	})
}

func TestGoogleDiscoveryPointsAtThisServer(t *testing.T) {
	srv, _ := newRawServer(t)

	resp := do(t, srv, http.MethodGet, "/google/.well-known/openid-configuration", "", "")
	require.Equal(t, http.StatusOK, resp.Status)

	var doc discoveryDocument
	resp.decode(t, &doc)

	// Absolute, and pointing back here. A relative endpoint would send the
	// adapter to whatever host it happened to be talking to.
	require.Equal(t, srv.URL+"/google/token", doc.TokenEndpoint)
	require.Equal(t, srv.URL+"/google/userinfo", doc.UserInfoEndpoint)
	require.True(t, strings.HasPrefix(doc.Issuer, "http"))
}

func TestResendRejections(t *testing.T) {
	srv, store := newRawServer(t)

	t.Run("an undecodable body is a 400", func(t *testing.T) {
		require.Equal(t, http.StatusBadRequest,
			do(t, srv, http.MethodPost, "/resend/emails", "", `{not json`).Status)
	})

	t.Run("an empty recipient is a 422", func(t *testing.T) {
		// Resend's own validation, mimicked so a fixture asserting a PERMANENT
		// send failure gets the real status rather than an invented success.
		resp := do(t, srv, http.MethodPost, "/resend/emails", "", `{"to":[],"subject":"x"}`)
		require.Equal(t, http.StatusUnprocessableEntity, resp.Status)

		var body resendRejected
		resp.decode(t, &body)
		require.Equal(t, "validation_error", body.Name)
	})

	require.Empty(t, store.Sent(), "a rejected message must not be recorded as sent")
}

func TestFormParsingFailuresAreReported(t *testing.T) {
	srv, _ := newRawServer(t)

	for _, path := range []string{"/github/login/oauth/access_token", "/google/token"} {
		require.Equal(t, http.StatusBadRequest,
			do(t, srv, http.MethodPost, path, "", "%zz").Status, path)
	}
}

func TestHealth(t *testing.T) {
	srv, _ := newRawServer(t)
	require.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/_health", "", "").Status)
}

func TestCommandDefaults(t *testing.T) {
	cmd := command()

	addr, err := cmd.Flags().GetString("addr")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8081", addr,
		"loopback by default: this server must not be reachable off the machine")

	self, err := cmd.Flags().GetString("self-url")
	require.NoError(t, err)
	require.Empty(t, self)
}

func TestRunServesAndShutsDown(t *testing.T) {
	ctx := context.Background()

	// A free port, so the test never collides with a real 8081.
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	// No --now: this exercises the server, not the pinned clock.
	go func() { done <- run(runCtx, addr, "http://"+addr, "") }()

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("http://%s/_health", addr), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a cancelled context is a clean shutdown, not an error")
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}
}

func TestRunRejectsAMalformedPin(t *testing.T) {
	// --now is a fixed instant, and a run that silently ignored a typo in it
	// would produce results that depend on the calendar date again.
	require.Error(t, run(context.Background(), "127.0.0.1:0", "http://127.0.0.1", "yesterday"))
}

func TestRunReportsAnUnusablePort(t *testing.T) {
	require.Error(t, run(context.Background(), "127.0.0.1:-1", "http://127.0.0.1", ""))
}
