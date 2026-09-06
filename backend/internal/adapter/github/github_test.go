package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// newClient points a real client at a test server, which is exactly what
// ADR-0010 does with cmd/fakethirdparty: same code, different base URL.
func newClient(t *testing.T, h http.Handler) *github.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return github.New(github.Config{
		APIBaseURL:   srv.URL,
		OAuthBaseURL: srv.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Token:        "platform-token",
		HTTPClient:   srv.Client(),
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func TestAuthorizeURL(t *testing.T) {
	c := github.New(github.Config{OAuthBaseURL: "https://github.com", ClientID: "abc"})

	raw := c.AuthorizeURL("state-123")
	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	require.Equal(t, "github.com", parsed.Host)
	require.Equal(t, "/login/oauth/authorize", parsed.Path)
	require.Equal(t, "abc", parsed.Query().Get("client_id"))
	require.Equal(t, "state-123", parsed.Query().Get("state"))

	// Read-only scopes. Anything broader asks a contributor to trust the
	// platform with more than it needs.
	require.Equal(t, "read:user", parsed.Query().Get("scope"))
}

func TestAuthorizeURLDefaultsToRealGitHub(t *testing.T) {
	c := github.New(github.Config{ClientID: "abc"})
	require.True(t, strings.HasPrefix(c.AuthorizeURL("s"), "https://github.com/login/oauth/authorize?"))
}

func TestExchangeCode(t *testing.T) {
	t.Run("returns the identity", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				require.NoError(t, r.ParseForm())
				require.Equal(t, "the-code", r.Form.Get("code"))
				require.Equal(t, "client-secret", r.Form.Get("client_secret"))
				require.Equal(t, "application/json", r.Header.Get("Accept"))
				writeJSON(t, w, map[string]any{"access_token": "user-token"})
			case "/user":
				// Authenticated as the CONTRIBUTOR, not as the platform.
				require.Equal(t, "Bearer user-token", r.Header.Get("Authorization"))
				writeJSON(t, w, map[string]any{
					"id": 1001, "login": "alice", "name": "Alice", "email": "alice@example.com",
				})
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))

		identity, err := c.ExchangeCode(context.Background(), "the-code")
		require.NoError(t, err)
		require.Equal(t, int64(1001), identity.GitHubUserID)
		require.Equal(t, "alice", identity.Login)
		require.Equal(t, "Alice", identity.Name)
		require.Equal(t, "alice@example.com", identity.Email)
	})

	t.Run("falls back to the private email list", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				writeJSON(t, w, map[string]any{"access_token": "user-token"})
			case "/user":
				writeJSON(t, w, map[string]any{"id": 1001, "login": "alice"})
			case "/user/emails":
				writeJSON(t, w, []map[string]any{
					{"email": "unverified@example.com", "primary": true, "verified": false},
					{"email": "secondary@example.com", "primary": false, "verified": true},
					{"email": "primary@example.com", "primary": true, "verified": true},
				})
			}
		}))

		identity, err := c.ExchangeCode(context.Background(), "the-code")
		require.NoError(t, err)
		require.Equal(t, "primary@example.com", identity.Email)
		require.Equal(t, "alice", identity.Name, "the login stands in for a missing name")
	})

	t.Run("takes a verified non-primary over an unverified primary", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				writeJSON(t, w, map[string]any{"access_token": "t"})
			case "/user":
				writeJSON(t, w, map[string]any{"id": 1, "login": "a"})
			case "/user/emails":
				writeJSON(t, w, []map[string]any{
					{"email": "unverified@example.com", "primary": true, "verified": false},
					{"email": "verified@example.com", "primary": false, "verified": true},
				})
			}
		}))

		identity, err := c.ExchangeCode(context.Background(), "c")
		require.NoError(t, err)
		require.Equal(t, "verified@example.com", identity.Email)
	})

	t.Run("refuses an account with no verified address", func(t *testing.T) {
		// The address is what a contact request is sent to. An unverified one
		// would deliver a hiring approach to whoever actually owns it.
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				writeJSON(t, w, map[string]any{"access_token": "t"})
			case "/user":
				writeJSON(t, w, map[string]any{"id": 1, "login": "a"})
			case "/user/emails":
				writeJSON(t, w, []map[string]any{
					{"email": "nope@example.com", "primary": true, "verified": false},
				})
			}
		}))

		_, err := c.ExchangeCode(context.Background(), "c")
		require.ErrorContains(t, err, "no verified email")
	})

	t.Run("rejects a 200 carrying an error field", func(t *testing.T) {
		// GitHub answers a bad code with HTTP 200. Checking only the status
		// would accept an empty token and mint a session for nobody.
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, map[string]any{
				"error":             "bad_verification_code",
				"error_description": "The code passed is incorrect or expired.",
			})
		}))

		_, err := c.ExchangeCode(context.Background(), "stale")
		require.ErrorContains(t, err, "incorrect or expired")
	})

	t.Run("rejects a 200 with no token at all", func(t *testing.T) {
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, map[string]any{})
		}))
		_, err := c.ExchangeCode(context.Background(), "c")
		require.ErrorContains(t, err, "no access token")
	})

	t.Run("rejects a user with no id", func(t *testing.T) {
		// Identity is the numeric id. A zero would collide with every other
		// zero in ByGitHubUserID.
		c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oauth/access_token":
				writeJSON(t, w, map[string]any{"access_token": "t"})
			case "/user":
				writeJSON(t, w, map[string]any{"login": "alice", "email": "a@example.com"})
			}
		}))
		_, err := c.ExchangeCode(context.Background(), "c")
		require.ErrorContains(t, err, "no id")
	})

	t.Run("refuses to exchange without oauth credentials", func(t *testing.T) {
		c := github.New(github.Config{})
		_, err := c.ExchangeCode(context.Background(), "c")
		require.ErrorContains(t, err, "--github-client-id")
	})
}

func TestPullRequest(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/kubernetes/kubernetes/pulls/42":
			require.Equal(t, "Bearer platform-token", r.Header.Get("Authorization"))
			writeJSON(t, w, map[string]any{
				"title": "Fix the scheduler", "merged": true, "merged_at": "2026-01-02T03:04:05Z",
				"additions": 120, "deletions": 30, "changed_files": 7,
				"review_comments": 14, "comments": 3,
				"user": map[string]any{"id": 2002},
				"base": map[string]any{"repo": map[string]any{
					"private": false, "fork": false,
					"stargazers_count": 104000, "forks_count": 39000,
				}},
			})
		case strings.HasPrefix(r.URL.Path, "/repos/kubernetes/kubernetes/pulls/42/reviews"):
			writeJSON(t, w, []map[string]any{
				{"user": map[string]any{"id": 3003}, "state": "APPROVED",
					"body": "lgtm", "submitted_at": "2026-01-02T00:00:00Z"},
				{"user": map[string]any{"id": 4004}, "state": "CHANGES_REQUESTED", "body": "nit"},
				{"user": map[string]any{"id": 3003}, "state": "APPROVED", "body": "again"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))

	facts, err := c.PullRequest(context.Background(), "kubernetes", "kubernetes", 42)
	require.NoError(t, err)

	require.Equal(t, "Fix the scheduler", facts.Title)
	require.True(t, facts.Merged)
	require.NotNil(t, facts.MergedAt)
	require.Equal(t, int64(2002), facts.AuthorUserID)
	require.Equal(t, 120, facts.Additions)
	require.Equal(t, 30, facts.Deletions)
	require.Equal(t, 7, facts.ChangedFiles)
	require.Equal(t, 14, facts.ReviewComments)
	require.Equal(t, 3, facts.Reviews)

	// Author plus two distinct reviewers; the repeat reviewer counts once.
	require.Equal(t, 3, facts.Participants)

	require.True(t, facts.Repository.Public)
	require.False(t, facts.Repository.IsFork)
	require.Equal(t, 104000, facts.Repository.Stars)
	require.Equal(t, 39000, facts.Repository.Forks)
}

func TestPullRequestReportsPrivateRepositories(t *testing.T) {
	// Public visibility is a claim precondition (ADR-0003), so the flag must
	// survive the mapping rather than defaulting to true.
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/reviews") {
			writeJSON(t, w, []map[string]any{})
			return
		}
		writeJSON(t, w, map[string]any{
			"merged": true, "user": map[string]any{"id": 1},
			"base": map[string]any{"repo": map[string]any{"private": true, "fork": true}},
		})
	}))

	facts, err := c.PullRequest(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.False(t, facts.Repository.Public)
	require.True(t, facts.Repository.IsFork)
}

func TestRepository(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"private": false, "fork": false,
			"stargazers_count": 900, "forks_count": 120,
		})
	}))

	facts, err := c.Repository(context.Background(), "o", "r")
	require.NoError(t, err)
	require.Equal(t, 900, facts.Stars)
	require.Equal(t, 120, facts.Forks)

	// GitHub publishes neither. They must reach Reach as ABSENT, never as a
	// zero that would drag a project's score down (ADR-0005).
	require.Zero(t, facts.Dependents)
	require.Zero(t, facts.Downloads)
}

func TestReviewsPagination(t *testing.T) {
	// A truncated list would turn a valid pr-review claim into "carries no
	// review by you".
	var pages int
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		page := r.URL.Query().Get("page")
		require.Equal(t, "100", r.URL.Query().Get("per_page"))

		if page == "1" {
			full := make([]map[string]any, 100)
			for i := range full {
				full[i] = map[string]any{"user": map[string]any{"id": i + 1}, "state": "COMMENTED"}
			}
			writeJSON(t, w, full)
			return
		}
		writeJSON(t, w, []map[string]any{
			{"user": map[string]any{"id": 999}, "state": "APPROVED"},
		})
	}))

	reviews, err := c.Reviews(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.Len(t, reviews, 101)
	require.Equal(t, 2, pages)
	require.Equal(t, int64(999), reviews[100].AuthorUserID)
}

func TestReviewsStopsPaginating(t *testing.T) {
	// An endpoint that always returns a full page must not become an infinite
	// request storm against a third party.
	var pages int
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		full := make([]map[string]any, 100)
		for i := range full {
			full[i] = map[string]any{"user": map[string]any{"id": i + 1}}
		}
		writeJSON(t, w, full)
	}))

	reviews, err := c.Reviews(context.Background(), "o", "r", 1)
	require.NoError(t, err)
	require.Equal(t, 10, pages)
	require.Len(t, reviews, 1000)
}

// TestStatusMapping is the split the whole error contract rests on: permanent
// facts about a claim versus transient facts about this minute.
func TestStatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		headers  map[string]string
		wantIs   error
		wantText string
	}{
		{name: "missing PR", status: 404, wantIs: port.ErrNotFound},
		{name: "server error", status: 500, wantIs: port.ErrUnavailable},
		{name: "bad gateway", status: 502, wantIs: port.ErrUnavailable},
		{
			name: "rate limited as 403", status: 403,
			headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000000"},
			wantIs:  port.ErrUnavailable,
		},
		{name: "rate limited as 429", status: 429, wantIs: port.ErrUnavailable},
		{
			// A plain 403 is a permission failure — a private repository or a
			// revoked token — and retrying it forever would not help.
			name: "forbidden without a rate limit", status: 403,
			wantText: "forbidden",
		},
		{name: "unprocessable", status: 422, wantText: "422"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
			}))

			_, err := c.Repository(context.Background(), "o", "r")
			require.Error(t, err)
			if tc.wantIs != nil {
				require.ErrorIs(t, err, tc.wantIs)
				require.NotErrorIs(t, err, other(tc.wantIs))
			}
			if tc.wantText != "" {
				require.ErrorContains(t, err, tc.wantText)
				require.NotErrorIs(t, err, port.ErrUnavailable)
				require.NotErrorIs(t, err, port.ErrNotFound)
			}
		})
	}
}

func TestRateLimitErrorNamesTheResetTime(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1800000000")
		w.WriteHeader(http.StatusForbidden)
	}))

	_, err := c.Repository(context.Background(), "o", "r")
	require.ErrorContains(t, err, "2027-01-15T08:00:00Z")
}

func TestUnreachableHostIsTransient(t *testing.T) {
	// A refused connection says nothing about whether the PR exists, so it
	// must never be recorded as evidence against a contributor.
	srv := httptest.NewServer(http.NewServeMux())
	closed := srv.URL
	srv.Close()

	c := github.New(github.Config{APIBaseURL: closed, Token: "t"})
	_, err := c.Repository(context.Background(), "o", "r")
	require.ErrorIs(t, err, port.ErrUnavailable)
	require.NotErrorIs(t, err, port.ErrNotFound)
}

func TestMalformedJSONIsAnError(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "{not json")
	}))

	_, err := c.Repository(context.Background(), "o", "r")
	require.ErrorContains(t, err, "decoding")
}

// other returns the sentinel a case must NOT match, so the two never collapse.
func other(want error) error {
	if errors.Is(want, port.ErrNotFound) {
		return port.ErrUnavailable
	}
	return port.ErrNotFound
}
