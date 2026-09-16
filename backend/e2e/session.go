package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The suite authenticates the way a real client does — through the endpoints in
// ADR-0002 — and never through a test-only header.
//
// An `X-E2E-Principal: alice` header would be simpler, and it would also be an
// authentication bypass compiled into the production binary. Anything that can
// be forgotten behind a build tag can be shipped. More to the point, the auth
// path is a third of what these fixtures assert: state binding, family
// revocation, cross-principal-type credential reuse. A suite that skips login
// cannot test login.
//
// So a case that acts `as: "alice"` causes a real OAuth round trip against the
// fake GitHub adapter, and every later step carries the bearer token it
// returned. When the refresh-rotation fixture revokes a family, the session
// this file established is the one that dies.

// sessionCache signs each principal in once per case and reuses the token.
// Re-authenticating per step would mint a new session family every time, which
// would quietly defeat the fixture that asserts a replayed refresh token
// revokes the whole family.
type sessionCache struct {
	principals Principals
	sessions   map[string]*Session
}

// For returns the named principal's session, signing them in on first use.
func (s *sessionCache) For(ctx context.Context, client *http.Client, as string) (*Session, error) {
	if as == "" || as == "anonymous" {
		return nil, nil
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	if session, ok := s.sessions[as]; ok {
		return session, nil
	}
	session, err := Authenticate(ctx, client, as, s.principals)
	if err != nil {
		return nil, fmt.Errorf("signing in as %s: %w", as, err)
	}
	s.sessions[as] = session
	return session, nil
}

// Adopt records a session a FIXTURE created, so later `as:` steps use it
// rather than signing in again behind its back.
//
// Without this a fixture that logs in explicitly ends up with two live
// sessions for one principal, and a case about logging out revokes only the
// one the harness happened to mint.
func (s *sessionCache) Adopt(as string, session *Session) {
	if as == "" || as == "anonymous" || session == nil {
		return
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	s.sessions[as] = session
}

// KeyForEmail names the principal a login body belongs to.
//
// A login step is `as: anonymous` — it is establishing the identity, so it
// cannot declare one. The email is what identifies who signed in.
func (p Principals) KeyForEmail(email string) string {
	if email == "" {
		return ""
	}
	for _, a := range p.Admins {
		if strings.EqualFold(a.Email, email) {
			return a.Key
		}
	}
	for _, h := range p.Hirers {
		if strings.EqualFold(h.Email, email) {
			return h.Key
		}
	}
	for _, c := range p.Contributors {
		if strings.EqualFold(c.Email, email) {
			return c.Key
		}
	}
	return ""
}

// KeyForUsername names the hirer a username belongs to.
//
// Since ADR-0016 a hirer signs in with a username, so it — not the address —
// is what identifies who a `/auth/hirer/login` step just authenticated as.
// Hirers only: usernames exist in no other namespace.
func (p Principals) KeyForUsername(username string) string {
	if username == "" {
		return ""
	}
	for _, h := range p.Hirers {
		if strings.EqualFold(h.Username, username) {
			return h.Key
		}
	}
	return ""
}

// Reset forgets every cached session.
//
// Called after the clock moves: the tokens it holds were minted against the
// old time and a fixture that kept using them would be testing expiry rather
// than whatever it meant to test.
func (s *sessionCache) Reset() { s.sessions = nil }

// Session is an authenticated principal's credentials for one case.
type Session struct {
	AccessToken  string
	RefreshToken string
}

// Authenticate signs in as a seeded principal and returns its session.
//
// Which path it uses is a property of the principal, not of the fixture:
// contributors exist only through GitHub, hirers through email or Google, and
// admins through their own endpoint with no OAuth path at all (ADR-0002).
func Authenticate(ctx context.Context, client *http.Client, principal string, p Principals) (*Session, error) {
	switch {
	case p.IsContributor(principal):
		return authenticateGitHub(ctx, client, principal, p)
	case p.IsHirer(principal):
		return authenticateHirer(ctx, client, principal, p)
	case p.IsAdmin(principal):
		return authenticateAdmin(ctx, client, principal, p)
	}
	return nil, fmt.Errorf("%q is not a seeded principal", principal)
}

// authenticateGitHub drives the real two-leg flow. The fake GitHub adapter is
// primed with the principal's identity first, so the callback resolves to the
// seeded user rather than minting a new one.
func authenticateGitHub(ctx context.Context, client *http.Client, principal string, p Principals) (*Session, error) {
	if _, ok := p.Contributor(principal); !ok {
		return nil, fmt.Errorf("contributor %q is not seeded", principal)
	}

	var start startResponse
	if err := call(ctx, client, "POST", "/auth/github/start", nil, "", &start); err != nil {
		return nil, fmt.Errorf("github start: %w", err)
	}

	// The code resolves to this principal's identity, which the fake server
	// was given by LoadCase before the fixture's first step. A real GitHub
	// never sees it.
	//
	// The state travels back on a cookie the client's jar carries, so this is
	// the same round trip a browser makes — including the check that rejects a
	// callback nobody started.
	code := AuthCode(principal)

	var session sessionResponse
	path := fmt.Sprintf("/auth/github/callback?code=%s&state=%s", code, start.State)
	if err := call(ctx, client, "GET", path, nil, "", &session); err != nil {
		return nil, fmt.Errorf("github callback: %w", err)
	}
	return session.session()
}

func authenticateHirer(ctx context.Context, client *http.Client, principal string, p Principals) (*Session, error) {
	hirer, ok := p.Hirer(principal)
	if !ok {
		return nil, fmt.Errorf("hirer %q is not seeded", principal)
	}

	// A GitHub-provider seat signs in exactly as a contributor does, and
	// resolves to the HIRER namespace because the two are separate account
	// types that happen to share an identity provider (ADR-0002).
	if hirer.AuthProvider == "github" {
		return authenticateGitHubHirer(ctx, client, principal)
	}

	// A Google-provider hirer has no password either, so signing them in with
	// one would test a path that does not exist for them.
	if hirer.AuthProvider == "google" {
		var start startResponse
		if err := call(ctx, client, "GET", "/auth/google/start", nil, "", &start); err != nil {
			return nil, fmt.Errorf("google start: %w", err)
		}
		code := AuthCode(principal)
		var session sessionResponse
		path := fmt.Sprintf("/auth/google/callback?code=%s&state=%s", code, start.State)
		if err := call(ctx, client, "GET", path, nil, "", &session); err != nil {
			return nil, fmt.Errorf("google callback: %w", err)
		}
		return session.session()
	}

	// USERNAME, not email (ADR-0016): the address stopped being an identity.
	body := map[string]string{"username": hirer.Username, "password": SeededPassword}
	var session sessionResponse
	if err := call(ctx, client, "POST", "/auth/hirer/login", body, "", &session); err != nil {
		return nil, fmt.Errorf("hirer login: %w", err)
	}
	return session.session()
}

// authenticateGitHubHirer drives the contributor OAuth flow for a seat that
// signed up through GitHub.
func authenticateGitHubHirer(ctx context.Context, client *http.Client, principal string) (*Session, error) {
	var start startResponse
	if err := call(ctx, client, "POST", "/auth/hirer/github/start", nil, "", &start); err != nil {
		return nil, fmt.Errorf("github start: %w", err)
	}

	var session sessionResponse
	path := fmt.Sprintf("/auth/hirer/github/callback?code=%s&state=%s", AuthCode(principal), start.State)
	if err := call(ctx, client, "GET", path, nil, "", &session); err != nil {
		return nil, fmt.Errorf("github callback: %w", err)
	}
	return session.session()
}

func authenticateAdmin(ctx context.Context, client *http.Client, principal string, p Principals) (*Session, error) {
	admin, ok := p.Admin(principal)
	if !ok {
		return nil, fmt.Errorf("admin %q is not seeded", principal)
	}
	body := map[string]string{"email": admin.Email, "password": SeededPassword}
	var session sessionResponse
	if err := call(ctx, client, "POST", "/auth/admin/login", body, "", &session); err != nil {
		return nil, fmt.Errorf("admin login: %w", err)
	}
	return session.session()
}

// startResponse is what an OAuth start endpoint returns.
//
// The state is echoed back on the callback and compared there. The suite
// carries it rather than inventing one, because a state the server never issued
// is exactly what the check exists to reject.
type startResponse struct {
	State string `json:"state"`
}

type sessionResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (s sessionResponse) session() (*Session, error) {
	if s.AccessToken == "" {
		return nil, fmt.Errorf("sign-in returned no access_token")
	}
	return &Session{AccessToken: s.AccessToken, RefreshToken: s.RefreshToken}, nil
}

// call is a minimal JSON request used by the harness itself — for signing in
// and for driving the control endpoint. Fixture steps do NOT go through it:
// they need the raw status and body, unparsed, to compare against a snapshot.
func call(ctx context.Context, client *http.Client, method, path string, body any, bearer string, out any) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequestWithContext(ctx, method, BaseURL()+path, reader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, BaseURL()+path, nil)
	}
	if err != nil {
		return err
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return unreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s returned %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// unreachable turns a dial failure into the message stage 3 needs: the suite is
// red because nothing is listening, not because a fixture is wrong.
func unreachable(err error) error {
	if strings.Contains(err.Error(), "connection refused") {
		return fmt.Errorf(
			"no API is listening at %s — stage 4 has not built one yet, so this fixture cannot pass",
			BaseURL())
	}
	return err
}

// loginBody is the part of an authentication request that names who is
// signing in.
//
// Two fields because the three sign-in paths do not agree on one: an admin
// presents an email (ADR-0002), a hirer presents a username (ADR-0016), and
// either may name a seeded principal.
type loginBody struct {
	Email    string `json:"email"`
	Username string `json:"username"`
}

// tokenPair is the part of an authentication response worth keeping.
type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// adoptSession hands a fixture's own login to the session cache.
//
// Only /auth/ responses carrying both tokens qualify, and only when the email
// in the request names a seeded principal — a rotation or a failed login
// leaves the cache alone.
func adoptSession(sessions *sessionCache, principals Principals, step Step, respBody []byte) {
	if !strings.HasPrefix(step.Request.Path, "/auth/") || len(respBody) == 0 {
		return
	}

	var pair tokenPair
	if err := json.Unmarshal(respBody, &pair); err != nil {
		return
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		return
	}

	var login loginBody
	_ = json.Unmarshal(step.Request.Body, &login)
	key := principals.KeyForUsername(login.Username)
	if key == "" {
		key = principals.KeyForEmail(login.Email)
	}
	if key == "" {
		return
	}
	sessions.Adopt(key, &Session{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken})
}
