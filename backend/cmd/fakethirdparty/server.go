package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Server routes provider-shaped requests at a fixture-loaded Store.
type Server struct {
	store *Store

	// selfURL is what the Google discovery document advertises. Discovery has
	// to name absolute URLs, so the server must know where a client reaches it.
	selfURL string

	// oauthCallbackURL turns this server into something a BROWSER can sign in
	// through, by giving /login/oauth/authorize somewhere to send the user
	// back to.
	//
	// Empty for the fixture suite, which never visits an authorize page — it
	// calls the callback directly with a code it already knows. Set by dev.sh,
	// where a person is clicking "Sign in with GitHub" and there is no real
	// GitHub to bounce off (ADR-0010: the provider is redirected, so the
	// redirect has to lead somewhere).
	oauthCallbackURL string

	// hirerOAuthCallbackURL is the same door for the other account type.
	//
	// One GitHub identity may own a contributor AND a hirer seat (ADR-0009),
	// and the API's authorize URL carries no redirect_uri to tell them apart —
	// a real OAuth app configures its callback once. So the sign-in page
	// offers both, which is the distinction a person actually has to make.
	hirerOAuthCallbackURL string
}

// NewServer builds the router.
func NewServer(store *Store, selfURL string) *Server {
	return &Server{store: store, selfURL: strings.TrimSuffix(selfURL, "/")}
}

// WithOAuthCallback enables the browser sign-in page.
func (s *Server) WithOAuthCallback(contributor, hirer string) *Server {
	s.oauthCallbackURL = strings.TrimSuffix(contributor, "/")
	s.hirerOAuthCallbackURL = strings.TrimSuffix(hirer, "/")
	return s
}

// Handler mounts every route.
//
// The prefixes match what cmd/api is pointed at:
//
//	--github-api-url     http://127.0.0.1:8081/github
//	--github-oauth-url   http://127.0.0.1:8081/github
//	--google-oidc-issuer http://127.0.0.1:8081/google
//	--resend-api-url     http://127.0.0.1:8081/resend
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Route("/github", func(r chi.Router) {
		r.Get("/login/oauth/authorize", s.githubAuthorize)
		r.Post("/login/oauth/access_token", s.githubAccessToken)
		r.Get("/user", s.githubUser)
		r.Get("/user/emails", s.githubUserEmails)
		r.Get("/repos/{owner}/{repo}", s.githubRepository)
		r.Get("/repos/{owner}/{repo}/pulls/{number}", s.githubPullRequest)
		r.Get("/repos/{owner}/{repo}/pulls/{number}/reviews", s.githubReviews)
		r.NotFound(githubNotFound)
	})

	r.Route("/google", func(r chi.Router) {
		r.Get("/.well-known/openid-configuration", s.googleDiscovery)
		r.Post("/token", s.googleToken)
		r.Get("/userinfo", s.googleUserInfo)
	})

	// The Batch API (ADR-0006). Mounted under /anthropic so one server can
	// stand in for every provider at distinct paths.
	r.Route("/anthropic/v1/messages/batches", func(r chi.Router) {
		r.Post("/", s.anthropicCreateBatch)
		r.Get("/{batchID}", s.anthropicBatchStatus)
		r.Get("/{batchID}/results", s.anthropicBatchResults)
	})

	r.Post("/resend/emails", s.resendSend)

	// The control plane. Reachable only from the harness: cmd/api is an HTTP
	// CLIENT of this server and never the reverse, so nothing in the API can
	// call these even by accident.
	r.Post("/_load", s.load)
	// The country list (ADR-0018 §10). No live vendor is wired: this is the
	// only provider, and choosing a real one later is an adapter and a flag.
	r.Get("/places/countries", s.countries)

	r.Get("/_clock", s.clockRead)
	r.Post("/_clock/advance", s.clockAdvance)
	r.Get("/_sent/emails", s.sentEmails)
	r.Get("/_health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// load replaces the entire state.
func (s *Server) load(w http.ResponseWriter, r *http.Request) {
	var state State
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		http.Error(w, fmt.Sprintf("decoding the third_party block: %v", err), http.StatusBadRequest)
		return
	}
	s.store.Load(state)
	w.WriteHeader(http.StatusNoContent)
}

// countries answers the country picker.
//
// A short list rather than all 250: the suite and `make dev` need enough to
// pick from and to prove the shape, not a complete gazetteer. The codes are
// real ISO 3166-1 alpha-2 values, because a fixture asserting "GB" should be
// asserting something true.
func (s *Server) countries(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"countries": fakeCountries})
}

// fakeCountries is the stand-in's world.
//
// Deliberately spread across continents and including one country with no
// postcodes at all (IE had none until 2015, AE has none now), so that a form
// exercising the "postcode is optional" path has somewhere to exercise it.
var fakeCountries = []map[string]string{
	{"code": "AE", "name": "United Arab Emirates"},
	{"code": "AU", "name": "Australia"},
	{"code": "BR", "name": "Brazil"},
	{"code": "CA", "name": "Canada"},
	{"code": "DE", "name": "Germany"},
	{"code": "ES", "name": "Spain"},
	{"code": "FR", "name": "France"},
	{"code": "GB", "name": "United Kingdom"},
	{"code": "IE", "name": "Ireland"},
	{"code": "IN", "name": "India"},
	{"code": "JP", "name": "Japan"},
	{"code": "KE", "name": "Kenya"},
	{"code": "NG", "name": "Nigeria"},
	{"code": "NL", "name": "Netherlands"},
	{"code": "PL", "name": "Poland"},
	{"code": "SG", "name": "Singapore"},
	{"code": "US", "name": "United States"},
	{"code": "ZA", "name": "South Africa"},
}

func (s *Server) sentEmails(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Sent())
}

// writeJSON encodes a value.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeRaw echoes a fixture-declared payload verbatim.
//
// No re-encoding: whatever the fixture wrote is exactly what the adapter
// parses, so a test failure is never this server's rendering.
func writeRaw(w http.ResponseWriter, status int, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// bearer extracts a token from the Authorization header.
func bearer(r *http.Request) string {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimPrefix(value, prefix)
}
