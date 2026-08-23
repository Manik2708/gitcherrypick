package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/google"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// stubGoogle is a minimal OIDC provider. Each field overrides one endpoint.
type stubGoogle struct {
	token    any
	userInfo any

	tokenStatus int
	discovery   *string

	discoveryHits atomic.Int32
}

func (s *stubGoogle) handler(t *testing.T, base func() string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		s.discoveryHits.Add(1)
		if s.discovery != nil {
			http.Error(w, *s.discovery, http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, map[string]any{
			"issuer":                 base(),
			"authorization_endpoint": base() + "/o/oauth2/v2/auth",
			"token_endpoint":         base() + "/token",
			"userinfo_endpoint":      base() + "/userinfo",
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		if s.tokenStatus != 0 {
			w.WriteHeader(s.tokenStatus)
			return
		}
		writeJSON(t, w, s.token)
	})

	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, s.userInfo)
	})
	return mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func newClient(t *testing.T, stub *stubGoogle) *google.Client {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(stub.handler(t, func() string { return srv.URL }))
	t.Cleanup(srv.Close)

	return google.New(google.Config{
		IssuerURL:    srv.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  "https://app.example.com/auth/google/callback",
		HTTPClient:   srv.Client(),
	})
}

func verified() *bool { v := true; return &v }
func unverified() *bool {
	v := false
	return &v
}

func TestAuthorizeURL(t *testing.T) {
	c := google.New(google.Config{
		ClientID:    "abc",
		RedirectURI: "https://app.example.com/cb",
	})

	parsed, err := url.Parse(c.AuthorizeURL("state-123"))
	require.NoError(t, err)

	require.Equal(t, "accounts.google.com", parsed.Host, "defaults to the real issuer")
	require.Equal(t, "/o/oauth2/v2/auth", parsed.Path)
	require.Equal(t, "abc", parsed.Query().Get("client_id"))
	require.Equal(t, "state-123", parsed.Query().Get("state"))
	require.Equal(t, "code", parsed.Query().Get("response_type"))
	require.Equal(t, "https://app.example.com/cb", parsed.Query().Get("redirect_uri"))
	require.Equal(t, "openid email profile", parsed.Query().Get("scope"))
}

func TestExchange(t *testing.T) {
	t.Run("returns a verified identity", func(t *testing.T) {
		c := newClient(t, &stubGoogle{
			token: map[string]any{"access_token": "at", "id_token": "it"},
			userInfo: map[string]any{
				"sub": "google-1", "email": "carol@corp.example",
				"email_verified": true, "name": "Carol",
			},
		})

		identity, err := c.Exchange(context.Background(), "the-code")
		require.NoError(t, err)
		require.Equal(t, "google-1", identity.Subject)
		require.Equal(t, "carol@corp.example", identity.Email)
		require.Equal(t, "Carol", identity.Name)
	})

	t.Run("refuses an unverified address", func(t *testing.T) {
		// port.OAuthProvider makes verification a precondition: the address
		// decides which organization seat a sign-in lands on, so an unverified
		// one is a route into somebody else's organization.
		c := newClient(t, &stubGoogle{
			token: map[string]any{"access_token": "at"},
			userInfo: map[string]any{
				"sub": "g", "email": "attacker@corp.example", "email_verified": false,
			},
		})

		_, err := c.Exchange(context.Background(), "c")
		require.ErrorIs(t, err, google.ErrEmailUnverified)
	})

	t.Run("refuses an ABSENT verification claim", func(t *testing.T) {
		// The pointer exists for this case. A missing claim decoded into a
		// plain bool would be false anyway — but a missing claim decoded by a
		// future refactor into "assume true" is the bug this pins shut.
		c := newClient(t, &stubGoogle{
			token:    map[string]any{"access_token": "at"},
			userInfo: map[string]any{"sub": "g", "email": "nobody@corp.example"},
		})

		_, err := c.Exchange(context.Background(), "c")
		require.ErrorIs(t, err, google.ErrEmailUnverified)
	})

	t.Run("rejects an identity missing its subject or email", func(t *testing.T) {
		for name, info := range map[string]map[string]any{
			"no subject": {"email": "a@b.c", "email_verified": true},
			"no email":   {"sub": "g", "email_verified": true},
		} {
			t.Run(name, func(t *testing.T) {
				c := newClient(t, &stubGoogle{
					token:    map[string]any{"access_token": "at"},
					userInfo: info,
				})
				_, err := c.Exchange(context.Background(), "c")
				require.Error(t, err)
			})
		}
	})

	t.Run("rejects a token response carrying an error", func(t *testing.T) {
		c := newClient(t, &stubGoogle{
			token: map[string]any{
				"error": "invalid_grant", "error_description": "Code was already redeemed.",
			},
		})
		_, err := c.Exchange(context.Background(), "used")
		require.ErrorContains(t, err, "already redeemed")
	})

	t.Run("rejects a token response with no token", func(t *testing.T) {
		c := newClient(t, &stubGoogle{token: map[string]any{}})
		_, err := c.Exchange(context.Background(), "c")
		require.ErrorContains(t, err, "no access token")
	})

	t.Run("reports an unavailable token endpoint as transient", func(t *testing.T) {
		c := newClient(t, &stubGoogle{tokenStatus: http.StatusBadGateway})
		_, err := c.Exchange(context.Background(), "c")
		require.ErrorIs(t, err, port.ErrUnavailable)
	})

	t.Run("refuses to exchange when unconfigured", func(t *testing.T) {
		c := google.New(google.Config{})
		_, err := c.Exchange(context.Background(), "c")
		require.ErrorContains(t, err, "--google-client-id")
	})
}

func TestDiscovery(t *testing.T) {
	t.Run("is fetched once and cached", func(t *testing.T) {
		stub := &stubGoogle{
			token: map[string]any{"access_token": "at"},
			userInfo: map[string]any{
				"sub": "g", "email": "c@corp.example", "email_verified": verified(),
			},
		}
		c := newClient(t, stub)

		for range 3 {
			_, err := c.Exchange(context.Background(), "c")
			require.NoError(t, err)
		}
		require.EqualValues(t, 1, stub.discoveryHits.Load())
	})

	t.Run("is NOT cached after a failure", func(t *testing.T) {
		// Caching a failure would poison the client for its whole lifetime, so
		// a blip at the first sign-in would break sign-in until a redeploy.
		boom := "down"
		stub := &stubGoogle{discovery: &boom}
		c := newClient(t, stub)

		_, err := c.Exchange(context.Background(), "c")
		require.ErrorIs(t, err, port.ErrUnavailable)

		_, err = c.Exchange(context.Background(), "c")
		require.Error(t, err)
		require.EqualValues(t, 2, stub.discoveryHits.Load(), "it must try again")
	})

	t.Run("rejects a document naming no endpoints", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, map[string]any{"issuer": "https://accounts.google.com"})
		}))
		t.Cleanup(srv.Close)

		c := google.New(google.Config{
			IssuerURL: srv.URL, ClientID: "id", ClientSecret: "secret",
			HTTPClient: srv.Client(),
		})
		_, err := c.Exchange(context.Background(), "c")
		require.ErrorContains(t, err, "no token or userinfo endpoint")
	})
}

func TestUnverifiedFlagIsHonouredExactly(t *testing.T) {
	// Guards the three-state decode: true accepts, false refuses, absent
	// refuses.
	for name, tc := range map[string]struct {
		claim   any
		wantErr bool
	}{
		"true":   {claim: verified(), wantErr: false},
		"false":  {claim: unverified(), wantErr: true},
		"absent": {claim: nil, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			info := map[string]any{"sub": "g", "email": "a@b.c"}
			if tc.claim != nil {
				info["email_verified"] = tc.claim
			}
			c := newClient(t, &stubGoogle{
				token: map[string]any{"access_token": "at"}, userInfo: info,
			})

			_, err := c.Exchange(context.Background(), "c")
			if tc.wantErr {
				require.ErrorIs(t, err, google.ErrEmailUnverified)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDefaultIssuerIsGoogle(t *testing.T) {
	require.True(t, strings.HasPrefix(google.DefaultIssuer, "https://"))
	require.Equal(t, "https://accounts.google.com", google.DefaultIssuer)
}
