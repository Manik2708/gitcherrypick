package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// stateCookie carries the OAuth state between start and callback.
//
// A cookie rather than server-side storage because the state is meaningless to
// anyone but the browser that started the flow, and storing it would mean a
// table of short-lived rows to expire. HttpOnly and SameSite=Lax: the callback
// is a top-level navigation from the provider, which Lax permits and Strict
// would break.
const stateCookie = "gcp_oauth_state"

// stateTTL bounds how long a started flow may be completed in.
const stateTTL = 10 * time.Minute

// AuthController serves /auth.
type AuthController struct {
	auth   port.AuthService
	secure bool
}

// NewAuthController wires sign-in.
//
// secure marks the state cookie Secure. It is configuration rather than a
// constant because local development runs on http, and a Secure cookie there is
// silently dropped — which presents as a state mismatch nobody can explain.
func NewAuthController(auth port.AuthService, secure bool) *AuthController {
	return &AuthController{auth: auth, secure: secure}
}

var _ port.Controller = (*AuthController)(nil)

// Routes mounts the sign-in surface. Every route here is UNAUTHENTICATED —
// these are the endpoints that produce credentials.
func (c *AuthController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Post("/github/start", c.githubStart)
	r.Get("/github/callback", c.githubCallback)

	r.Get("/google/start", c.googleStart)
	r.Get("/google/callback", c.googleCallback)

	r.Post("/hirer/register", c.registerHirer)
	r.Post("/hirer/login", c.loginHirer)
	r.Post("/admin/login", c.loginAdmin)

	r.Post("/refresh", c.refresh)
	r.Post("/logout", c.logout)

	return "/auth", r
}

// --- request and response shapes --------------------------------------------

// startResponse is what a flow's start endpoint returns.
//
// The state is echoed as well as set as a cookie. A browser client ignores it;
// the integration suite reads it, because it drives the callback directly
// rather than through a redirect.
type startResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// tokenPair is the credential half of every successful sign-in.
type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// contributorSummary is the account half of a GitHub sign-in.
type contributorSummary struct {
	ID            domain.UserID `json:"id"`
	DisplayName   string        `json:"display_name"`
	GitHubLogin   string        `json:"github_login"`
	PrincipalType string        `json:"principal_type"`

	// Created distinguishes the first callback from every later one, so a
	// client can show onboarding exactly once.
	Created bool `json:"created"`
}

type githubCallbackResponse struct {
	User contributorSummary `json:"user"`
	tokenPair
}

// organizationSummary is the organization a hirer acts for.
//
// Verified and PaymentVerified are separate: verification gates hiring, while
// payment status is disclosed to a contributor deciding whether to release
// their address (ADR-0002 §5).
type organizationSummary struct {
	ID              domain.OrganizationID `json:"id"`
	Name            string                `json:"name"`
	Verified        bool                  `json:"verified"`
	PaymentVerified bool                  `json:"payment_verified"`
}

type hirerSummary struct {
	ID            domain.HirerID      `json:"id"`
	DisplayName   string              `json:"display_name"`
	Email         string              `json:"email"`
	PrincipalType string              `json:"principal_type"`
	Verified      bool                `json:"verified"`
	Organization  organizationSummary `json:"organization"`
}

type hirerAuthResponse struct {
	Hirer hirerSummary `json:"hirer"`
	tokenPair
}

type adminSummary struct {
	ID            domain.AdminID `json:"id"`
	DisplayName   string         `json:"display_name"`
	Email         string         `json:"email"`
	PrincipalType string         `json:"principal_type"`
}

type adminAuthResponse struct {
	Admin adminSummary `json:"admin"`
	tokenPair
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type registerHirerRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	DisplayName      string `json:"display_name"`
	OrganizationName string `json:"organization_name"`
	Website          string `json:"website"`
	LinkedInURL      string `json:"linkedin_url"`
}

// --- handlers ----------------------------------------------------------------

func (c *AuthController) githubStart(w http.ResponseWriter, r *http.Request) {
	url, state, err := c.auth.GitHubAuthorizeURL(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	c.setState(w, state)
	writeJSON(w, http.StatusOK, startResponse{AuthorizeURL: url, State: state})
}

// githubCallback completes the contributor flow.
//
// The state is compared BEFORE the code is exchanged, so an attacker-supplied
// code never reaches GitHub. A mismatch is 400 rather than 401: nothing was
// authenticated, and the request itself is malformed.
func (c *AuthController) githubCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	expected, ok := c.readState(r)
	if !ok || state == "" || state != expected {
		c.clearState(w)
		writeCode(w, http.StatusBadRequest, service.CodeInvalidState)
		return
	}
	c.clearState(w)

	contributor, pair, created, err := c.auth.CompleteGitHub(r.Context(), code, state, expected)
	if err != nil {
		// A refused code is an authentication failure, distinct from the
		// malformed-request case above.
		if errors.Is(err, service.ErrInvalidCredentials) {
			writeCode(w, http.StatusUnauthorized, service.CodeOAuthExchangeFailed)
			return
		}
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, githubCallbackResponse{
		User: contributorSummary{
			ID:            contributor.ID,
			DisplayName:   contributor.DisplayName,
			GitHubLogin:   contributor.GitHubLogin,
			PrincipalType: string(domain.KindContributor),
			Created:       created,
		},
		tokenPair: pairOf(pair),
	})
}

func (c *AuthController) googleStart(w http.ResponseWriter, r *http.Request) {
	url, state, err := c.auth.GoogleAuthorizeURL(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	c.setState(w, state)
	writeJSON(w, http.StatusOK, startResponse{AuthorizeURL: url, State: state})
}

// googleCallback completes the hirer flow.
//
// It never creates an account. Registration is what raises the verification
// request, so a Google sign-in that minted a hirer would bypass verification
// entirely (ADR-0002) — which is why an unknown address is 404 with advice
// rather than a silent signup.
func (c *AuthController) googleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	expected, ok := c.readState(r)
	if !ok || state == "" || state != expected {
		c.clearState(w)
		writeCode(w, http.StatusBadRequest, service.CodeInvalidState)
		return
	}
	c.clearState(w)

	hirer, pair, err := c.auth.CompleteGoogle(r.Context(), code, state, expected)
	if err != nil {
		c.writeGoogleFailure(w, err)
		return
	}

	writeJSON(w, http.StatusOK, hirerAuthResponse{
		Hirer:     summarizeHirer(hirer),
		tokenPair: pairOf(pair),
	})
}

// writeGoogleFailure separates the three ways a Google callback fails.
//
// They demand different things of the caller: register an account, use a
// verified address, or try again. One status for all three would leave a client
// with no way to say which.
func (c *AuthController) writeGoogleFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeCode(w, http.StatusNotFound, service.CodeNoHirerAccount)
	case service.CodeOf(err) == service.CodeEmailNotVerified:
		writeCode(w, http.StatusUnauthorized, service.CodeEmailNotVerified)
	case errors.Is(err, service.ErrInvalidCredentials):
		writeCode(w, http.StatusUnauthorized, service.CodeOAuthExchangeFailed)
	default:
		writeError(w, err)
	}
}

func (c *AuthController) registerHirer(w http.ResponseWriter, r *http.Request) {
	var body registerHirerRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidState)
		return
	}

	hirer, pair, err := c.auth.RegisterHirer(r.Context(), port.RegisterHirerRequest{
		Email:            body.Email,
		Password:         body.Password,
		DisplayName:      body.DisplayName,
		OrganizationName: body.OrganizationName,
		Website:          body.Website,
		LinkedInURL:      body.LinkedInURL,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, hirerAuthResponse{
		Hirer:     summarizeHirer(hirer),
		tokenPair: pairOf(pair),
	})
}

func (c *AuthController) loginHirer(w http.ResponseWriter, r *http.Request) {
	var body credentialsRequest
	if err := decode(w, r, &body); err != nil {
		// Reported as bad credentials rather than a bad body. A malformed
		// login is still a failed login, and describing the difference would
		// help someone probing the endpoint.
		writeCode(w, http.StatusUnauthorized, service.CodeInvalidCredentials)
		return
	}

	hirer, pair, err := c.auth.LoginHirer(r.Context(), body.Email, body.Password)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, hirerAuthResponse{
		Hirer:     summarizeHirer(hirer),
		tokenPair: pairOf(pair),
	})
}

func (c *AuthController) loginAdmin(w http.ResponseWriter, r *http.Request) {
	var body credentialsRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnauthorized, service.CodeInvalidCredentials)
		return
	}

	admin, pair, err := c.auth.LoginAdmin(r.Context(), body.Email, body.Password)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, adminAuthResponse{
		Admin: adminSummary{
			ID:            admin.ID,
			DisplayName:   admin.DisplayName,
			Email:         admin.Email,
			PrincipalType: string(domain.KindAdmin),
		},
		tokenPair: pairOf(pair),
	})
}

// refresh rotates a token pair.
//
// The failure codes are distinct — a replayed token, a collaterally revoked
// session, and an unknown one mean different things to a client (ADR-0002).
func (c *AuthController) refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if err := decode(w, r, &body); err != nil || body.RefreshToken == "" {
		writeCode(w, http.StatusUnauthorized, service.CodeInvalidRefreshToken)
		return
	}

	pair, err := c.auth.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pairOf(pair))
}

// logout revokes the whole family, not the presented session.
//
// Always 204, including when nothing was revoked. A sign-out that reported
// "you were not signed in" would be an oracle for whether a token is live.
func (c *AuthController) logout(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAny(w, r)
	if !ok {
		return
	}

	if err := c.auth.Logout(r.Context(), p); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers -----------------------------------------------------------------

func (c *AuthController) setState(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/auth",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c *AuthController) readState(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(stateCookie)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

// clearState expires the cookie as soon as the flow ends, successfully or not.
// A state that outlived its callback could be replayed against a second one.
func (c *AuthController) clearState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    "",
		Path:     "/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func pairOf(p *domain.TokenPair) tokenPair {
	if p == nil {
		return tokenPair{}
	}
	return tokenPair{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		ExpiresIn:    p.ExpiresIn,
	}
}

func summarizeHirer(h *domain.Hirer) hirerSummary {
	out := hirerSummary{
		ID:            h.ID,
		DisplayName:   h.DisplayName,
		Email:         h.Email,
		PrincipalType: string(domain.KindHirer),
		Verified:      h.VerifiedAt != nil,
	}
	if h.Organization != nil {
		out.Organization = organizationSummary{
			ID:              h.Organization.ID,
			Name:            h.Organization.Name,
			Verified:        h.Organization.VerifiedAt != nil,
			PaymentVerified: h.Organization.PaymentVerified(),
		}
	}
	return out
}
