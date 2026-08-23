package main

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// githubNotFoundBody is GitHub's real 404 shape.
//
// A fixture that never declared a PR must fail as a MISSING PR, not as this
// server improvising a default (ADR-0010). Emitting GitHub's own body means the
// adapter's 404 handling is what runs, which is the whole reason the wire
// format is mimicked rather than simplified.
const githubNotFoundBody = `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`

func githubNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(githubNotFoundBody))
}

// tokenGrant is the access_token response.
type tokenGrant struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

// oauthError is GitHub's failed-exchange body, which arrives as HTTP 200.
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// githubAccessToken trades a declared code for a token.
//
// An undeclared code gets GitHub's real answer: HTTP 200 carrying an error
// field. That shape is deliberate — it is what catches an adapter checking only
// the status code, and a fixture asserting a rejected sign-in needs it.
func (s *Server) githubAccessToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unparseable form", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")

	if _, ok := s.store.GitHubCode(code); !ok {
		writeJSON(w, http.StatusOK, oauthError{
			Error:            "bad_verification_code",
			ErrorDescription: fmt.Sprintf("The code %q is not declared in this fixture's third_party.github.oauth_codes.", code),
		})
		return
	}

	token := "fake-github-token-" + code
	s.store.Grant(token, code)
	writeJSON(w, http.StatusOK, tokenGrant{
		AccessToken: token,
		TokenType:   "bearer",
		Scope:       "read:user,user:email",
	})
}

// githubUser serves the identity behind the bearer token.
func (s *Server) githubUser(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.store.GitHubGrantFor(bearer(r))
	if !ok {
		githubUnauthorized(w)
		return
	}
	if len(grant.User) == 0 {
		githubNotFound(w, r)
		return
	}
	writeRaw(w, http.StatusOK, grant.User)
}

// githubUserEmails serves the private address list.
//
// An absent list is an empty array rather than a 404: that is what GitHub
// returns for an account with no addresses, and it is the input the adapter
// must turn into "no verified email" rather than into a transport error.
func (s *Server) githubUserEmails(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.store.GitHubGrantFor(bearer(r))
	if !ok {
		githubUnauthorized(w)
		return
	}
	if len(grant.Emails) == 0 {
		writeRaw(w, http.StatusOK, []byte(`[]`))
		return
	}
	writeRaw(w, http.StatusOK, grant.Emails)
}

func (s *Server) githubRepository(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "owner") + "/" + chi.URLParam(r, "repo")
	payload, ok := s.store.Repository(key)
	if !ok {
		githubNotFound(w, r)
		return
	}
	writeRaw(w, http.StatusOK, payload)
}

func (s *Server) githubPullRequest(w http.ResponseWriter, r *http.Request) {
	payload, ok := s.store.PullRequest(prKey(r))
	if !ok {
		githubNotFound(w, r)
		return
	}
	writeRaw(w, http.StatusOK, payload)
}

// githubReviews serves a PR's review list.
//
// Pagination is honoured only to the extent of returning everything on the
// first page: no fixture has 100 reviews, and a fake that paginated would be
// testing its own paging rather than the adapter's.
func (s *Server) githubReviews(w http.ResponseWriter, r *http.Request) {
	payload, ok := s.store.Reviews(prKey(r))
	if !ok {
		// A PR with no declared reviews has none, which is different from a PR
		// that does not exist.
		writeRaw(w, http.StatusOK, []byte(`[]`))
		return
	}
	writeRaw(w, http.StatusOK, payload)
}

// prKey builds the "owner/name#number" key a fixture declares.
func prKey(r *http.Request) string {
	return fmt.Sprintf("%s/%s#%s",
		chi.URLParam(r, "owner"), chi.URLParam(r, "repo"), chi.URLParam(r, "number"))
}

func githubUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"message":"Bad credentials","status":"401"}`))
}
