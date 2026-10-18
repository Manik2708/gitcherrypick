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

// The browser half of Google's OAuth dance, for development.
//
// The same affordance as githubAuthorize (authorize.go): the fixture suite
// never visits it — it calls the API's Google callback with a code it already
// knows — but a person clicking "Sign in with Google" on `make dev` needs a
// page that sends them back. Each loaded Google identity is a link to the
// callback carrying that identity's code and the state the API issued.
//
// Only hirers sign in with Google (ADR-0002), so there is one callback.
//
// Enabled only when --google-callback-url is set. Unset, the route 404s, so
// nothing about the fixture suite changes.

// googleAuthorize renders the pick-an-account page.
func (s *Server) googleAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.googleCallbackURL == "" {
		http.NotFound(w, r)
		return
	}

	// Echoed untouched: the API compares it against the cookie it set, and a
	// page that invented one would defeat the CSRF check it exists to
	// exercise.
	state := r.URL.Query().Get("state")

	grants := s.store.GoogleGrants()
	if len(grants) == 0 {
		http.Error(w,
			"no Google identities loaded — run devdata with --control-url so the fake "+
				"server knows which hirers sign in with Google", http.StatusServiceUnavailable)
		return
	}

	codes := make([]string, 0, len(grants))
	for code := range grants {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, googleAuthorizePage(s.googleCallbackURL, state, codes, grants))
}

// googleAuthorizeLabel is the part of a grant's userinfo worth showing. The
// claims are stored raw, so only the two rendered fields are decoded.
type googleAuthorizeLabel struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// googleAuthorizePage builds the sign-in page. Unstyled and unscripted, like
// the GitHub one: a development affordance, not part of the product.
func googleAuthorizePage(callback, state string, codes []string, grants map[string]GoogleGrant) string {
	var b strings.Builder

	b.WriteString("<!doctype html><meta charset=utf-8>")
	b.WriteString("<title>Sign in with Google — development</title>")
	b.WriteString("<h1>Choose an account</h1>")
	b.WriteString("<p>This is the fake Google. Pick a seeded hirer who signs in with Google.</p>")
	b.WriteString("<ul>")

	for _, code := range codes {
		var who googleAuthorizeLabel
		_ = json.Unmarshal(grants[code].UserInfo, &who)

		target := callback + "?" + url.Values{
			"code":  {code},
			"state": {state},
		}.Encode()

		label := who.Name
		if label == "" {
			label = who.Email
		}
		b.WriteString(fmt.Sprintf(
			`<li><a href="%s">%s</a> <small>(%s, %s)</small></li>`,
			html.EscapeString(target),
			html.EscapeString(label),
			html.EscapeString(who.Email),
			html.EscapeString(code),
		))
	}

	b.WriteString("</ul>")
	return b.String()
}
