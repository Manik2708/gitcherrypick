package controller_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// The middleware is the single place a token becomes an identity. These tests
// are the ones that would notice a bypass.

func TestNoCredentialsIsUnauthenticated(t *testing.T) {
	h := newHarness(t)

	got := h.do(t, http.MethodGet, "/me", "", "")
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeUnauthenticated, got.errorCode(t))
}

func TestWrongAccountTypeIsForbiddenNotUnauthorized(t *testing.T) {
	// The two failures are different and the fixtures distinguish them.
	// Collapsing them would tell a signed-in contributor to sign in again.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodGet, "/admin/verifications", "alice-token", "")
	require.Equal(t, http.StatusForbidden, got.Status)
	require.Equal(t, service.CodeAdminRequired, got.errorCode(t))
}

func TestEachKindGetsItsOwnRefusalCode(t *testing.T) {
	cases := map[string]struct {
		principal domain.Principal
		path      string
		wantCode  string
	}{
		"admin route, hirer caller": {
			hirerPrincipal(true), "/admin/verifications", service.CodeAdminRequired,
		},
		"hirer route, contributor caller": {
			contributorPrincipal(), "/shortlists", service.CodeHirerRequired,
		},
		"contributor route, hirer caller": {
			hirerPrincipal(true), "/claims", service.CodeContributorRequired,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("token", tc.principal)

			got := h.do(t, http.MethodGet, tc.path, "token", "")
			require.Equal(t, http.StatusForbidden, got.Status)
			require.Equal(t, tc.wantCode, got.errorCode(t))
		})
	}
}

func TestForgedTokenIsTreatedAsAbsent(t *testing.T) {
	// Not rejected centrally: a handler then reports what it actually needs,
	// and the genuinely public endpoints keep working.
	h := newHarness(t)
	h.tokens.EXPECT().Verify(mock.Anything, "forged").
		Return(nil, errors.New("signature mismatch"))

	got := h.do(t, http.MethodGet, "/me", "forged", "")
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeUnauthenticated, got.errorCode(t))
}

func TestForgedTokenNeverCausesAnAccountLookup(t *testing.T) {
	// The token is verified BEFORE the account is read. mockery fails the test
	// if ResolvePrincipal is called without an expectation, which is the
	// assertion here.
	h := newHarness(t)
	h.tokens.EXPECT().Verify(mock.Anything, "forged").Return(nil, errors.New("bad"))

	h.do(t, http.MethodGet, "/me", "forged", "")
	h.auth.AssertNotCalled(t, "ResolvePrincipal", mock.Anything, mock.Anything)
}

func TestDeletedAccountStopsActingImmediately(t *testing.T) {
	// A token that verifies but resolves to nothing is treated as absent. A
	// deleted admin must not keep acting for the fifteen minutes their token
	// has left (ADR-0011).
	h := newHarness(t)
	claims := port.AccessClaims{Subject: adminID, Kind: domain.KindAdmin}

	h.tokens.EXPECT().Verify(mock.Anything, "ghost").Return(&claims, nil)
	h.auth.EXPECT().ResolvePrincipal(mock.Anything, claims).
		Return(nil, service.ErrInvalidCredentials)

	got := h.do(t, http.MethodGet, "/admin/verifications", "ghost", "")
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeUnauthenticated, got.errorCode(t))
}

func TestMalformedAuthorizationHeaders(t *testing.T) {
	// None of these carry a bearer token, so none should reach Verify.
	for name, header := range map[string]string{
		"no scheme":     "alice-token",
		"wrong scheme":  "Basic YWxpY2U6cGFzcw==",
		"scheme only":   "Bearer",
		"empty value":   "Bearer ",
		"nearly bearer": "Bearer" + "x" + " token",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			req := newRequest(t, http.MethodGet, "/me", "")
			req.Header.Set("Authorization", header)
			got := h.serve(req)

			require.Equal(t, http.StatusUnauthorized, got.Status)
			h.tokens.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
		})
	}
}

func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	// RFC 7235 makes the scheme case-insensitive, and real clients send
	// "bearer".
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.auth.EXPECT().Me(mock.Anything, mock.Anything).
		Return(ptr(contributorPrincipal()), nil)

	req := newRequest(t, http.MethodGet, "/me", "")
	req.Header.Set("Authorization", "bearer alice-token")

	require.Equal(t, http.StatusOK, h.serve(req).Status)
}

func TestTokenIsNotReadFromTheQueryString(t *testing.T) {
	// A token in a query string lands in access logs, browser history and
	// Referer headers, so it is not accepted even as a fallback.
	h := newHarness(t)

	got := h.do(t, http.MethodGet, "/me?access_token=alice-token", "", "")
	require.Equal(t, http.StatusUnauthorized, got.Status)
	h.tokens.AssertNotCalled(t, "Verify", mock.Anything, mock.Anything)
}

func TestPublicEndpointsNeedNoPrincipal(t *testing.T) {
	// Central rejection of bad credentials would have broken these.
	h := newHarness(t)
	h.skills.EXPECT().Search(mock.Anything, "").Return(nil, nil)

	require.Equal(t, http.StatusOK, h.do(t, http.MethodGet, "/skills", "", "").Status)
}

func ptr[T any](v T) *T { return &v }
