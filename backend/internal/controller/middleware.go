package controller

import (
	"context"
	"net/http"
	"strings"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// contextKey is unexported so nothing outside this package can write a
// principal into a request context. If any package could, "authenticated" would
// stop meaning "presented a valid token".
type contextKey struct{ name string }

var principalKey = contextKey{name: "principal"}

// PrincipalResolver turns a bearer token into a domain.Principal.
//
// This is the SINGLE place a token becomes an identity. There is no test-only
// header and no bypass: the integration suite signs in through the real
// endpoints and carries a real Ed25519 token, because an authentication bypass
// compiled into the shipping binary is one mistake away from production
// (ADR-0010).
type PrincipalResolver struct {
	tokens port.TokenIssuer
	auth   port.AuthService
}

// NewPrincipalResolver wires the middleware.
func NewPrincipalResolver(tokens port.TokenIssuer, auth port.AuthService) *PrincipalResolver {
	return &PrincipalResolver{tokens: tokens, auth: auth}
}

var _ port.Middleware = (*PrincipalResolver)(nil)

// Wrap resolves credentials when present and passes the request on regardless.
//
// A missing or bad token is NOT rejected here. It leaves the principal absent,
// and the handler's requirement — Contributor, Hirer, Admin, or nothing —
// decides. Rejecting centrally would break the endpoints that are legitimately
// public: the skill catalogue, and a shared scorecard link.
//
// The token is verified before the account is read, so a forged token never
// causes a database lookup.
func (m *PrincipalResolver) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		claims, err := m.tokens.Verify(r.Context(), token)
		if err != nil {
			// An expired or forged token is treated as no token. The handler
			// then reports what it actually needs, so a client learns "you
			// must be a hirer" rather than a bare 401 that hides which of the
			// two problems it has.
			next.ServeHTTP(w, r)
			return
		}

		principal, err := m.auth.ResolvePrincipal(r.Context(), *claims)
		if err != nil {
			// The token verified but the account is gone or disabled. Also
			// treated as absent: a deleted admin must not keep acting for the
			// fifteen minutes their token has left.
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), *principal)))
	})
}

// bearerToken extracts the credential.
//
// Authorization only. A token in a query string lands in access logs, browser
// history and Referer headers, so it is not read from one even as a fallback.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "

	value := r.Header.Get("Authorization")
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

// WithPrincipal puts a principal in a context. Exported for the server to build
// contexts in tests of its own wiring.
func WithPrincipal(ctx context.Context, p domain.Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// principalFrom reads the principal, if the request carried valid credentials.
func principalFrom(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalKey).(domain.Principal)
	return p, ok
}

// require extracts a principal of the demanded kind, writing the failure itself
// when it cannot.
//
// The two failures are different and the fixtures distinguish them: no
// credentials at all is 401 `authentication_required`, while valid credentials
// of the wrong type is 403 `hirer_required`. Collapsing them would tell a
// signed-in contributor to sign in again.
func require(w http.ResponseWriter, r *http.Request, kind domain.PrincipalKind) (domain.Principal, bool) {
	p, ok := principalFrom(r.Context())
	if !ok {
		writeCode(w, http.StatusUnauthorized, service.CodeUnauthenticated)
		return domain.Principal{}, false
	}
	if p.Kind != kind {
		writeCode(w, http.StatusForbidden, kindRequired(kind))
		return domain.Principal{}, false
	}
	return p, true
}

// requireAny extracts a principal of any kind, for endpoints open to every
// signed-in account.
func requireAny(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := principalFrom(r.Context())
	if !ok {
		writeCode(w, http.StatusUnauthorized, service.CodeUnauthenticated)
		return domain.Principal{}, false
	}
	return p, true
}

func kindRequired(kind domain.PrincipalKind) string {
	switch kind {
	case domain.KindContributor:
		return service.CodeContributorRequired
	case domain.KindHirer:
		return service.CodeHirerRequired
	case domain.KindAdmin:
		return service.CodeAdminRequired
	}
	return service.CodeForbidden
}
