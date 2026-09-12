package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// The browser half of the OAuth dance.
//
// The fixture suite never needs this: it calls the API's callback directly
// with a code it already knows, because a test asserting on a redirect chain
// would be asserting on the browser rather than on the platform. A PERSON
// developing a client does need it — they click "Sign in with GitHub" and
// something has to send them back.
//
// So this page is the seeded cast, as links. Picking one is the whole login:
// it redirects to the API's callback with that principal's authorization code
// and the state the API issued. That also makes the three account types
// reachable, which matters for a UI whose entire shape depends on whether the
// caller is a contributor, a hirer or an admin (ADR-0002).
//
// Enabled only when --oauth-callback-url is set. Unset, the route 404s exactly
// as it did before, so nothing about the fixture suite changes.

// githubAuthorize renders the pick-a-principal page.
func (s *Server) githubAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oauthCallbackURL == "" {
		githubNotFound(w, r)
		return
	}

	// The state is echoed back untouched. The API compares it against the
	// cookie it set, and a page that invented one would defeat the CSRF check
	// it exists to exercise.
	state := r.URL.Query().Get("state")

	grants := s.store.GitHubGrants()
	if len(grants) == 0 {
		http.Error(w,
			"no identities loaded — run devdata with --control-url so the fake server "+
				"knows who exists", http.StatusServiceUnavailable)
		return
	}

	codes := make([]string, 0, len(grants))
	for code := range grants {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, authorizePage(s.oauthCallbackURL, s.hirerOAuthCallbackURL, state, codes, grants))
}

// authorizeLabel is the part of a grant's user payload worth showing.
//
// The payload is stored raw — the fake server serves what it was given and
// does not model GitHub's user object — so the page decodes only the two
// fields it renders.
type authorizeLabel struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

// authorizePage builds the sign-in page.
//
// Deliberately unstyled and unscripted. It is a development affordance, not a
// surface anyone should mistake for part of the product.
func authorizePage(callback, hirerCallback, state string, codes []string, grants map[string]GitHubGrant) string {
	var b strings.Builder

	b.WriteString("<!doctype html><meta charset=utf-8>")
	b.WriteString("<title>Sign in — development</title>")
	b.WriteString("<h1>Sign in as</h1>")
	b.WriteString("<p>This is the fake GitHub. Pick a seeded account.</p>")
	if hirerCallback != "" {
		b.WriteString(
			"<p><small>One identity can own a contributor account and a hirer seat, " +
				"so pick which you are signing in as.</small></p>")
	}
	b.WriteString("<ul>")

	for _, code := range codes {
		var who authorizeLabel
		_ = json.Unmarshal(grants[code].User, &who)

		target := callback + "?" + url.Values{
			"code":  {code},
			"state": {state},
		}.Encode()

		label := who.Name
		if label == "" {
			label = who.Login
		}
		b.WriteString(fmt.Sprintf(
			`<li><a href="%s">%s</a> <small>(%s)</small>`,
			html.EscapeString(target),
			html.EscapeString(label),
			html.EscapeString(code),
		))

		if hirerCallback != "" {
			hirerTarget := hirerCallback + "?" + url.Values{
				"code":  {code},
				"state": {state},
			}.Encode()
			b.WriteString(fmt.Sprintf(
				` — <a href="%s"><small>as a hirer</small></a>`,
				html.EscapeString(hirerTarget),
			))
		}
		b.WriteString("</li>")
	}

	b.WriteString("</ul>")
	return b.String()
}
