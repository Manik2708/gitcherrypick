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
	contributor, ok := p.Contributor(principal)
	if !ok {
		return nil, fmt.Errorf("contributor %q is not seeded", principal)
	}

	var start struct {
		State string `json:"state"`
	}
	if err := call(ctx, client, "POST", "/auth/github/start", nil, "", &start); err != nil {
		return nil, fmt.Errorf("github start: %w", err)
	}

	// The code is arbitrary — the fake adapter maps it to the identity the
	// control endpoint was told about. A real GitHub never sees this.
	code := "e2e-" + principal
	if err := PrimeGitHubOAuth(ctx, client, code, contributor); err != nil {
		return nil, fmt.Errorf("priming github oauth: %w", err)
	}

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

	// A Google-provider hirer has no password, so signing them in with one
	// would be testing a path that does not exist for them.
	if hirer.AuthProvider == "google" {
		var start struct {
			State string `json:"state"`
		}
		if err := call(ctx, client, "GET", "/auth/google/start", nil, "", &start); err != nil {
			return nil, fmt.Errorf("google start: %w", err)
		}
		code := "e2e-" + principal
		if err := PrimeGoogleOAuth(ctx, client, code, hirer.Email); err != nil {
			return nil, fmt.Errorf("priming google oauth: %w", err)
		}
		var session sessionResponse
		path := fmt.Sprintf("/auth/google/callback?code=%s&state=%s", code, start.State)
		if err := call(ctx, client, "GET", path, nil, "", &session); err != nil {
			return nil, fmt.Errorf("google callback: %w", err)
		}
		return session.session()
	}

	body := map[string]string{"email": hirer.Email, "password": SeededPassword}
	var session sessionResponse
	if err := call(ctx, client, "POST", "/auth/hirer/login", body, "", &session); err != nil {
		return nil, fmt.Errorf("hirer login: %w", err)
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
