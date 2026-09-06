package controller_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

func tokenPair() *domain.TokenPair {
	return &domain.TokenPair{
		AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 900,
	}
}

// stateFrom pulls the OAuth state cookie out of a start response.
func stateFrom(t *testing.T, r response) string {
	t.Helper()

	for _, cookie := range (&http.Response{Header: r.Header}).Cookies() {
		if cookie.Name == "gcp_oauth_state" {
			return cookie.Value
		}
	}
	t.Fatal("no state cookie was set")
	return ""
}

func TestGitHubStartSetsAStateCookie(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).
		Return("https://github.com/login/oauth/authorize?state=s1", "s1", nil)

	got := h.do(t, http.MethodPost, "/auth/github/start", "", "{}")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
		State        string `json:"state"`
	}
	got.decode(t, &body)
	require.Equal(t, "s1", body.State)
	require.Contains(t, body.AuthorizeURL, "github.com")

	cookie := stateFrom(t, got)
	require.Equal(t, "s1", cookie)

	// HttpOnly so script cannot read it; Lax because the callback is a
	// top-level navigation from the provider, which Strict would break.
	raw := got.Header.Get("Set-Cookie")
	require.Contains(t, raw, "HttpOnly")
	require.Contains(t, raw, "SameSite=Lax")
}

func TestGitHubCallbackRequiresTheMatchingState(t *testing.T) {
	// The state is compared BEFORE the code is exchanged, so an
	// attacker-supplied code never reaches GitHub. mockery fails the test if
	// CompleteGitHub is called.
	h := newHarness(t)
	h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).Return("https://github.com/x", "s1", nil)

	start := h.do(t, http.MethodPost, "/auth/github/start", "", "{}")

	req := newRequest(t, http.MethodGet, "/auth/github/callback?code=c&state=not-the-state", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusBadRequest, got.Status)
	require.Equal(t, service.CodeInvalidState, got.errorCode(t))
	h.auth.AssertNotCalled(t, "CompleteGitHub", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestGitHubCallbackWithNoCookieIsRefused(t *testing.T) {
	// Nothing started this flow, so nothing may finish it.
	h := newHarness(t)

	got := h.do(t, http.MethodGet, "/auth/github/callback?code=c&state=s1", "", "")
	require.Equal(t, http.StatusBadRequest, got.Status)
	require.Equal(t, service.CodeInvalidState, got.errorCode(t))
}

func TestGitHubCallbackCreatesAndThenResolves(t *testing.T) {
	for name, created := range map[string]bool{"first callback": true, "later callback": false} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).Return("https://github.com/x", "s1", nil)
			h.auth.EXPECT().CompleteGitHub(mock.Anything, "gho_valid", "s1", "s1").
				Return(&domain.Contributor{
					ID: aliceID, DisplayName: "Nina Ferrer", GitHubLogin: "newcomer",
				}, tokenPair(), created, nil)

			start := h.do(t, http.MethodPost, "/auth/github/start", "", "{}")
			req := newRequest(t, http.MethodGet, "/auth/github/callback?code=gho_valid&state=s1", "")
			req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

			got := h.serve(req)
			require.Equal(t, http.StatusOK, got.Status)

			var body struct {
				User struct {
					GitHubLogin   string `json:"github_login"`
					PrincipalType string `json:"principal_type"`
					Created       bool   `json:"created"`
				} `json:"user"`
				AccessToken string `json:"access_token"`
				ExpiresIn   int    `json:"expires_in"`
			}
			got.decode(t, &body)
			require.Equal(t, "newcomer", body.User.GitHubLogin)
			require.Equal(t, "contributor", body.User.PrincipalType)
			require.Equal(t, created, body.User.Created)
			require.Equal(t, 900, body.ExpiresIn)
		})
	}
}

func TestGitHubCallbackReportsARefusedCode(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).Return("https://github.com/x", "s1", nil)
	h.auth.EXPECT().CompleteGitHub(mock.Anything, "gho_expired", "s1", "s1").
		Return(nil, nil, false, service.ErrInvalidCredentials)

	start := h.do(t, http.MethodPost, "/auth/github/start", "", "{}")
	req := newRequest(t, http.MethodGet, "/auth/github/callback?code=gho_expired&state=s1", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeOAuthExchangeFailed, got.errorCode(t))
}

func TestGoogleCallbackDistinguishesItsThreeFailures(t *testing.T) {
	// They demand different things of the caller: register an account, use a
	// verified address, or try again.
	cases := map[string]struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		"unknown address": {
			service.ErrNotFound, http.StatusNotFound, service.CodeNoHirerAccount,
		},
		"unverified address": {
			service.Coded(service.ErrInvalidCredentials, service.CodeEmailNotVerified, "unverified"),
			http.StatusUnauthorized, service.CodeEmailNotVerified,
		},
		"refused code": {
			service.ErrInvalidCredentials, http.StatusUnauthorized, service.CodeOAuthExchangeFailed,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.auth.EXPECT().GoogleAuthorizeURL(mock.Anything).Return("https://accounts.google.com/x", "s1", nil)
			h.auth.EXPECT().CompleteGoogle(mock.Anything, "code", "s1", "s1").Return(nil, nil, tc.err)

			start := h.do(t, http.MethodGet, "/auth/google/start", "", "")
			req := newRequest(t, http.MethodGet, "/auth/google/callback?code=code&state=s1", "")
			req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

			got := h.serve(req)
			require.Equal(t, tc.wantStatus, got.Status)
			require.Equal(t, tc.wantCode, got.errorCode(t))
		})
	}
}

func TestHirerLoginReturnsTheOrganizationInline(t *testing.T) {
	h := newHarness(t)
	now := fixedTime()
	h.auth.EXPECT().LoginHirer(mock.Anything, "sam@tiny.example", "correct-horse").
		Return(&domain.Hirer{
			ID: hankID, DisplayName: "Sam Ortega", Email: "sam@tiny.example",
			VerifiedAt: &now,
			Organization: &domain.Organization{
				ID: orgID, Name: "Tiny Studio", VerifiedAt: &now,
			},
		}, tokenPair(), nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/login", "",
		`{"email":"sam@tiny.example","password":"correct-horse"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Hirer struct {
			PrincipalType string `json:"principal_type"`
			Verified      bool   `json:"verified"`
			Organization  struct {
				Name            string `json:"name"`
				Verified        bool   `json:"verified"`
				PaymentVerified bool   `json:"payment_verified"`
			} `json:"organization"`
		} `json:"hirer"`
	}
	got.decode(t, &body)

	require.Equal(t, "hirer", body.Hirer.PrincipalType)
	require.True(t, body.Hirer.Verified)
	require.Equal(t, "Tiny Studio", body.Hirer.Organization.Name)
	require.True(t, body.Hirer.Organization.Verified)

	// Verification and payment are separate facts: payment status is disclosed
	// to a contributor at contact time and gates nothing (ADR-0002 §5).
	require.False(t, body.Hirer.Organization.PaymentVerified)
}

func TestMalformedLoginBodyIsReportedAsBadCredentials(t *testing.T) {
	// A malformed login is still a failed login. Describing the difference
	// would help someone probing the endpoint.
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/hirer/login", "", `{"email":`)
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeInvalidCredentials, got.errorCode(t))
	h.auth.AssertNotCalled(t, "LoginHirer", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminLogin(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().LoginAdmin(mock.Anything, "admin@x.test", "pw").
		Return(&domain.Admin{ID: adminID, DisplayName: "Root Admin", Email: "admin@x.test"},
			tokenPair(), nil)

	got := h.do(t, http.MethodPost, "/auth/admin/login", "",
		`{"email":"admin@x.test","password":"pw"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Admin struct {
			PrincipalType string `json:"principal_type"`
		} `json:"admin"`
	}
	got.decode(t, &body)
	require.Equal(t, "admin", body.Admin.PrincipalType)
}

func TestRefreshDistinguishesItsFailures(t *testing.T) {
	// A replayed token, a collaterally revoked session and an unknown one mean
	// different things to a client (ADR-0002).
	cases := map[string]struct {
		err      error
		wantCode string
	}{
		"reuse": {
			service.Coded(service.ErrInvalidCredentials, service.CodeTokenReuseDetected, "spent"),
			service.CodeTokenReuseDetected,
		},
		"revoked": {
			service.Coded(service.ErrInvalidCredentials, service.CodeSessionRevoked, "revoked"),
			service.CodeSessionRevoked,
		},
		"unknown": {
			service.Coded(service.ErrInvalidCredentials, service.CodeInvalidRefreshToken, "unknown"),
			service.CodeInvalidRefreshToken,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.auth.EXPECT().Refresh(mock.Anything, "rt").Return(nil, tc.err)

			got := h.do(t, http.MethodPost, "/auth/refresh", "", `{"refresh_token":"rt"}`)
			require.Equal(t, http.StatusUnauthorized, got.Status)
			require.Equal(t, tc.wantCode, got.errorCode(t))
		})
	}
}

func TestRefreshWithNoTokenNeverReachesTheService(t *testing.T) {
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/refresh", "", `{}`)
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeInvalidRefreshToken, got.errorCode(t))
	h.auth.AssertNotCalled(t, "Refresh", mock.Anything, mock.Anything)
}

func TestRefreshRotates(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().Refresh(mock.Anything, "rt").Return(tokenPair(), nil)

	got := h.do(t, http.MethodPost, "/auth/refresh", "", `{"refresh_token":"rt"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	got.decode(t, &body)
	require.Equal(t, "access", body.AccessToken)
	require.Equal(t, "refresh", body.RefreshToken)
	require.Equal(t, 900, body.ExpiresIn)
}

func TestLogoutIsAlways204(t *testing.T) {
	// A sign-out that reported "you were not signed in" would be an oracle for
	// whether a token is live.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.auth.EXPECT().Logout(mock.Anything, mock.Anything).Return(nil)

	got := h.do(t, http.MethodPost, "/auth/logout", "alice-token", "")
	require.Equal(t, http.StatusNoContent, got.Status)
	require.Empty(t, got.Body)
}

// TestLogoutWithoutCredentialsIsStillLoggedOut pins the idempotence.
//
// A sign-out with no session succeeds and does nothing. Refusing it would tell
// the caller whether the token they presented was live, which is exactly what
// an unauthenticated endpoint must not reveal (ADR-0002).
func TestLogoutWithoutCredentialsIsStillLoggedOut(t *testing.T) {
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/logout", "", "")
	require.Equal(t, http.StatusNoContent, got.Status)
	require.Empty(t, got.Body)
}

func TestRegisterHirerIsCreated(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().RegisterHirer(mock.Anything, mock.MatchedBy(func(req port.RegisterHirerRequest) bool {
		return req.Email == "new@corp.example" && req.OrganizationName == "New Corp" &&
			len(req.Proofs) == 1 && req.Proofs[0].Kind == domain.ProofWorkEmailDomain
	})).Return(&port.HirerRegistration{
		Hirer: &domain.Hirer{
			ID: hankID, DisplayName: "New Hirer", Email: "new@corp.example",
			Organization: &domain.Organization{ID: orgID, Name: "New Corp"},
		},
		VerificationRequest: &port.VerificationRequest{
			ID: domain.RequestID("01920000-0000-7000-8000-00000000ae01"), Status: "pending",
		},
	}, nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/register", "",
		`{"email":"new@corp.example","password":"pw","display_name":"New Hirer",
		  "organization":{"name":"New Corp"},
		  "proofs":[{"kind":"work_email_domain","value":"corp.example"}]}`)
	require.Equal(t, http.StatusCreated, got.Status)

	var body registeredHirerResponse
	got.decode(t, &body)
	require.Equal(t, "hirer", body.Kind)
	require.False(t, body.Verified)
	require.Equal(t, "pending", body.VerificationRequest.Status)
	require.NotContains(t, got.Body, "access_token",
		"registration queues a review; it does not sign anyone in")
}

// registeredHirerResponse is the 201 a registration returns.
type registeredHirerResponse struct {
	ID                  string                     `json:"id"`
	Kind                string                     `json:"kind"`
	Verified            bool                       `json:"verified"`
	VerificationRequest queuedVerificationResponse `json:"verification_request"`
}

type queuedVerificationResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func TestRegisterHirerRejectsAnUnknownProofKind(t *testing.T) {
	// The registrant is told WHICH proof to fix. An enum violation from the
	// database would tell them only that something was wrong.
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/hirer/register", "",
		`{"email":"new@corp.example","password":"pw","display_name":"New Hirer",
		  "organization":{"name":"New Corp"},
		  "proofs":[{"kind":"vibes","notes":"trust me"}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeInvalidRegistration, got.errorCode(t))
}
