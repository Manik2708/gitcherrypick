package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/google"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// This file is the load-bearing test for ADR-0010. It points the REAL adapters
// — the same code cmd/api wires up — at this server and drives them through the
// flows a fixture needs. If redirection did not work, it would fail here rather
// than in production.

// stubRenderer stands in for the notification templates, which are a product
// decision rather than a transport one.
type stubRenderer struct{}

func (stubRenderer) Render(n port.Notification) (string, string, error) {
	return "Subject for " + string(n.Kind), "<p>an organization is interested</p>", nil
}

// harness is a running fake server plus clients pointed at it.
type harness struct {
	url      string
	client   *http.Client
	github   *github.Client
	google   *google.Client
	notifier *resend.Notifier
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	store := NewStore()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewServer(store, srv.URL).Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return &harness{
		url:    srv.URL,
		client: srv.Client(),
		github: github.New(github.Config{
			APIBaseURL: srv.URL + "/github", OAuthBaseURL: srv.URL + "/github",
			ClientID: "id", ClientSecret: "secret", Token: "platform",
			HTTPClient: srv.Client(),
		}),
		google: google.New(google.Config{
			IssuerURL: srv.URL + "/google",
			ClientID:  "id", ClientSecret: "secret",
			RedirectURI: "https://app.example.com/cb",
			HTTPClient:  srv.Client(),
		}),
		notifier: resend.New(resend.Config{
			BaseURL: srv.URL + "/resend", APIKey: "key",
			From:       "GitCherryPick <no-reply@example.com>",
			HTTPClient: srv.Client(), Renderer: stubRenderer{},
		}),
	}
}

// load installs a fixture's third_party block.
func (h *harness) load(t *testing.T, block string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.url+"/_load", jsonBody(block))
	require.NoError(t, err)

	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func (h *harness) sent(t *testing.T) []SentEmail {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, h.url+"/_sent/emails", nil)
	require.NoError(t, err)

	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var out []SentEmail
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

const fullBlock = `{
  "github": {
    "oauth_codes": {
      "e2e-alice": {
        "user": {"id": 1001, "login": "alice", "name": "Alice Example"},
        "emails": [
          {"email": "old@example.com", "primary": false, "verified": false},
          {"email": "alice@example.com", "primary": true, "verified": true}
        ]
      }
    },
    "repositories": {
      "kubernetes/kubernetes": {
        "private": false, "fork": false,
        "stargazers_count": 104000, "forks_count": 39000
      }
    },
    "pull_requests": {
      "kubernetes/kubernetes#42": {
        "title": "Fix the scheduler", "merged": true,
        "merged_at": "2026-01-02T03:04:05Z",
        "additions": 120, "deletions": 30, "changed_files": 7,
        "review_comments": 14,
        "user": {"id": 2002},
        "base": {"repo": {
          "private": false, "fork": false,
          "stargazers_count": 104000, "forks_count": 39000
        }}
      }
    },
    "reviews": {
      "kubernetes/kubernetes#42": [
        {"user": {"id": 3003}, "state": "APPROVED", "body": "lgtm",
         "submitted_at": "2026-01-02T00:00:00Z"}
      ]
    }
  },
  "google": {
    "oauth_codes": {
      "e2e-carol": {
        "userinfo": {
          "sub": "google-carol", "email": "carol@corp.example",
          "email_verified": true, "name": "Carol"
        }
      }
    }
  }
}`

func TestRealGitHubClientAgainstTheFake(t *testing.T) {
	h := newHarness(t)
	h.load(t, fullBlock)
	ctx := context.Background()

	t.Run("signs a contributor in", func(t *testing.T) {
		identity, err := h.github.ExchangeCode(ctx, "e2e-alice")
		require.NoError(t, err)
		require.Equal(t, int64(1001), identity.GitHubUserID)
		require.Equal(t, "alice", identity.Login)
		require.Equal(t, "Alice Example", identity.Name)

		// Resolved from the private list, skipping the unverified entry —
		// the branch that only runs because the fake splits the two endpoints
		// the way GitHub does.
		require.Equal(t, "alice@example.com", identity.Email)
	})

	t.Run("reads pull request facts", func(t *testing.T) {
		facts, err := h.github.PullRequest(ctx, "kubernetes", "kubernetes", 42)
		require.NoError(t, err)
		require.Equal(t, "Fix the scheduler", facts.Title)
		require.True(t, facts.Merged)
		require.Equal(t, int64(2002), facts.AuthorUserID)
		require.Equal(t, 14, facts.ReviewComments)
		require.Equal(t, 1, facts.Reviews)
		require.Equal(t, 2, facts.Participants, "the author plus one reviewer")
		require.Equal(t, 104000, facts.Repository.Stars)
		require.True(t, facts.Repository.Public)
	})

	t.Run("reads a repository", func(t *testing.T) {
		facts, err := h.github.Repository(ctx, "kubernetes", "kubernetes")
		require.NoError(t, err)
		require.Equal(t, 39000, facts.Forks)
	})

	t.Run("an undeclared PR is NOT FOUND, not a default", func(t *testing.T) {
		// The property ADR-0010 §5 turns on: a fixture that forgot to declare
		// something must fail as missing rather than pass on an invention.
		_, err := h.github.PullRequest(ctx, "kubernetes", "kubernetes", 999)
		require.ErrorIs(t, err, port.ErrNotFound)
		require.NotErrorIs(t, err, port.ErrUnavailable)
	})

	t.Run("an undeclared repository is NOT FOUND", func(t *testing.T) {
		_, err := h.github.Repository(ctx, "nobody", "nothing")
		require.ErrorIs(t, err, port.ErrNotFound)
	})

	t.Run("an undeclared code is rejected as a bad code", func(t *testing.T) {
		// GitHub answers this with HTTP 200 and an error field. The fake keeps
		// that shape, so the adapter's handling of it is what runs.
		_, err := h.github.ExchangeCode(ctx, "never-declared")
		require.ErrorContains(t, err, "not declared in this fixture")
	})

	t.Run("a PR with no declared reviews has none", func(t *testing.T) {
		h.load(t, `{"github":{"pull_requests":{"o/r#1":{"merged":true,"user":{"id":1},
			"base":{"repo":{"private":false}}}}}}`)

		facts, err := h.github.PullRequest(ctx, "o", "r", 1)
		require.NoError(t, err)
		require.Zero(t, facts.Reviews, "no reviews is different from no such PR")
	})
}

func TestRealGoogleClientAgainstTheFake(t *testing.T) {
	h := newHarness(t)
	h.load(t, fullBlock)
	ctx := context.Background()

	t.Run("signs a hirer in through discovery", func(t *testing.T) {
		// The adapter fetches /.well-known/openid-configuration and follows
		// the endpoints it names. Nothing about the token or userinfo paths is
		// hardcoded in the client.
		identity, err := h.google.Exchange(ctx, "e2e-carol")
		require.NoError(t, err)
		require.Equal(t, "google-carol", identity.Subject)
		require.Equal(t, "carol@corp.example", identity.Email)
		require.Equal(t, "Carol", identity.Name)
	})

	t.Run("refuses an unverified address declared by the fixture", func(t *testing.T) {
		h.load(t, `{"google":{"oauth_codes":{"c":{"userinfo":
			{"sub":"g","email":"spoofed@corp.example","email_verified":false}}}}}`)

		_, err := h.google.Exchange(ctx, "c")
		require.ErrorIs(t, err, google.ErrEmailUnverified)
	})

	t.Run("an undeclared code is an invalid grant", func(t *testing.T) {
		_, err := h.google.Exchange(ctx, "never-declared")
		require.Error(t, err)
	})
}

func TestRealNotifierAgainstTheFake(t *testing.T) {
	h := newHarness(t)
	h.load(t, `{}`)
	ctx := context.Background()

	require.NoError(t, h.notifier.Send(ctx, port.Notification{
		Kind:      port.NotifyContactRequest,
		Recipient: "alice@example.com",
	}))

	sent := h.sent(t)
	require.Len(t, sent, 1)
	require.Equal(t, []string{"alice@example.com"}, sent[0].To)
	require.Equal(t, "Subject for contact_request", sent[0].Subject)
	require.Equal(t, "GitCherryPick <no-reply@example.com>", sent[0].From)
}

func TestLoadIsolatesFixtures(t *testing.T) {
	// The same rule the database follows: a fixture that passes only because
	// the one before it left data behind proves nothing.
	h := newHarness(t)
	ctx := context.Background()

	h.load(t, fullBlock)
	require.NoError(t, h.notifier.Send(ctx, port.Notification{
		Kind: port.NotifyContactRequest, Recipient: "alice@example.com",
	}))
	require.Len(t, h.sent(t), 1)

	h.load(t, `{}`)

	require.Empty(t, h.sent(t), "a reload must clear recorded mail")

	_, err := h.github.PullRequest(ctx, "kubernetes", "kubernetes", 42)
	require.ErrorIs(t, err, port.ErrNotFound, "a reload must clear declared data")

	// A token issued before the reload must stop working, or one fixture's
	// session would authenticate inside the next.
	_, err = h.github.ExchangeCode(ctx, "e2e-alice")
	require.Error(t, err)
}

func TestControlPlaneRejectsGarbage(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.url+"/_load", jsonBody(`{"github": "not an object"}`))
	require.NoError(t, err)

	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// jsonBody wraps a literal for a request body.
func jsonBody(s string) *strings.Reader { return strings.NewReader(s) }
