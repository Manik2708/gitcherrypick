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
}

// NewServer builds the router.
func NewServer(store *Store, selfURL string) *Server {
	return &Server{store: store, selfURL: strings.TrimSuffix(selfURL, "/")}
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

	r.Post("/resend/emails", s.resendSend)

	// The control plane. Reachable only from the harness: cmd/api is an HTTP
	// CLIENT of this server and never the reverse, so nothing in the API can
	// call these even by accident.
	r.Post("/_load", s.load)
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
