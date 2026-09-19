package controller_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/controller"
	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

// The controller layer is tested through the router with services mocked, so
// every assertion runs the real middleware, the real routing and the real
// serialization. Calling a handler directly would skip the seam that resolves
// the principal — which is precisely the seam that must not be skippable.

// errorResponse is the failure shape the fixtures pin.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// harness is a router with mocked services behind it.
type harness struct {
	server     *controller.Server
	auth       *mocks.AuthService
	tokens     *mocks.TokenIssuer
	claims     *mocks.ClaimService
	skills     *mocks.SkillService
	discovery  *mocks.DiscoveryService
	shortlists *mocks.ShortlistService
	contacts   *mocks.ContactService
	orgs       *mocks.OrganizationService
	redemption *mocks.RedemptionService
	onboarding *mocks.OnboardingService
	admin      *mocks.AdminService
	evaluation *mocks.EvaluationService
	reeval     *mocks.ReevaluationService
	profiles   *mocks.ProfileService
	places     *mocks.PlaceService
	roles      *mocks.RoleService
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		auth:       mocks.NewAuthService(t),
		tokens:     mocks.NewTokenIssuer(t),
		claims:     mocks.NewClaimService(t),
		skills:     mocks.NewSkillService(t),
		discovery:  mocks.NewDiscoveryService(t),
		shortlists: mocks.NewShortlistService(t),
		contacts:   mocks.NewContactService(t),
		orgs:       mocks.NewOrganizationService(t),
		redemption: mocks.NewRedemptionService(t),
		onboarding: mocks.NewOnboardingService(t),
		admin:      mocks.NewAdminService(t),
		evaluation: mocks.NewEvaluationService(t),
		reeval:     mocks.NewReevaluationService(t),
		profiles:   mocks.NewProfileService(t),
		places:     mocks.NewPlaceService(t),
		roles:      mocks.NewRoleService(t),
	}

	// A fixed clock: the admin queue reports how long a request has waited,
	// and a wall-clock reading would make that number differ per run.
	clock := mocks.NewClock(t)
	clock.EXPECT().Now().Return(time.Now().UTC()).Maybe()

	h.server = controller.NewServer(controller.NewPrincipalResolver(h.tokens, h.auth))
	h.server.Health()
	h.server.Mount(
		controller.NewAuthController(h.auth, h.redemption, false),
		controller.NewMeController(h.auth, h.orgs, h.skills, h.discovery, h.contacts,
			h.reeval, h.profiles, h.roles),
		controller.NewClaimController(h.claims, h.reeval),
		controller.NewSkillController(h.skills),
		controller.NewSkillRequestController(h.skills),
		controller.NewShortlistController(h.shortlists),
		controller.NewOrganizationController(h.orgs),
		controller.NewRoleController(h.roles),
		controller.NewOpeningsController(h.roles),
		controller.NewOrganizationsController(h.redemption, h.onboarding),
		controller.NewPlacesController(h.places),
		controller.NewAdminController(h.admin, h.evaluation, h.onboarding, clock),
		controller.NewPublicController(h.auth),
		controller.NewDiscoveryController(h.discovery),
	)
	return h
}

// response is a completed exchange.
type response struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r response) decode(t *testing.T, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.Body, out), "body was %s", r.Body)
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var body errorResponse
	r.decode(t, &body)
	return body.Error
}

// newRequest builds a request without sending it, for the few tests that need
// to set a header the harness would not.
func newRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(body))
	}
	return req
}

// serve drives the router.
func (h *harness) serve(req *http.Request) response {
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return response{Status: rec.Code, Body: rec.Body.Bytes(), Header: rec.Header()}
}

// do drives the router. token is sent as a bearer credential when non-empty.
func (h *harness) do(t *testing.T, method, path, token, body string) response {
	t.Helper()

	req := newRequest(t, method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return h.serve(req)
}

// fixedTime is a stable timestamp, so an assertion never depends on when it
// ran.
func fixedTime() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }

// --- principals --------------------------------------------------------------

const (
	aliceID = "01920000-0000-7000-8000-00000000a001"
	hankID  = "01920000-0000-7000-8000-00000000c001"
	orgID   = "01920000-0000-7000-8000-00000000b001"
	adminID = "01920000-0000-7000-8000-00000000d001"
	claimID = "01920000-0000-7000-8000-0000000f0001"
)

func contributorPrincipal() domain.Principal {
	return domain.Principal{
		Kind: domain.KindContributor,
		Contributor: &domain.Contributor{
			ID: aliceID, DisplayName: "Alice Okafor",
			GitHubLogin: "aliceok", Email: "alice@example.com",
		},
	}
}

func hirerPrincipal(verified bool) domain.Principal {
	h := &domain.Hirer{
		ID: hankID, OrganizationID: orgID, DisplayName: "Hank Rivera",
		Email:        "hank@acme.com",
		Organization: &domain.Organization{ID: orgID, Name: "Acme"},
	}
	if verified {
		now := fixedTime()
		h.VerifiedAt = &now
		h.Organization.VerifiedAt = &now
	}
	return domain.Principal{Kind: domain.KindHirer, Hirer: h}
}

func adminPrincipal() domain.Principal {
	return domain.Principal{
		Kind:  domain.KindAdmin,
		Admin: &domain.Admin{ID: adminID, DisplayName: "Root Admin", Email: "admin@x.test"},
	}
}

// signIn primes the token issuer and the principal resolver for one token.
func (h *harness) signIn(token string, p domain.Principal) {
	claims := port.AccessClaims{Subject: p.Subject(), Kind: p.Kind}

	h.tokens.EXPECT().Verify(mock.Anything, token).Return(&claims, nil).Maybe()
	h.auth.EXPECT().ResolvePrincipal(mock.Anything, claims).Return(&p, nil).Maybe()
}

// --- error mapping -----------------------------------------------------------

func TestErrorMapping(t *testing.T) {
	// The sentinel decides the status; a coded error names the reason. Both
	// halves are asserted here because a controller that reported the right
	// status with the wrong code would still break every client.
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", service.ErrNotFound, http.StatusNotFound, service.CodeClaimNotFound},
		{
			"coded conflict",
			service.Coded(service.ErrConflict, service.CodeClaimLocked, "locked"),
			http.StatusConflict, service.CodeClaimLocked,
		},
		{
			"cooldown overrides conflict to 429",
			service.Coded(service.ErrConflict, service.CodeReevaluationCooldown, "wait"),
			http.StatusTooManyRequests, service.CodeReevaluationCooldown,
		},
		{
			"invalid becomes 422",
			service.Coded(service.ErrInvalid, service.CodeInvalidEvidence, "bad url"),
			http.StatusUnprocessableEntity, service.CodeInvalidEvidence,
		},
		{
			"forbidden",
			service.Coded(service.ErrForbidden, service.CodeVerificationNeeded, "pending"),
			http.StatusForbidden, service.CodeVerificationNeeded,
		},
		{
			"wrapped code survives",
			fmt.Errorf("reading: %w",
				service.Coded(service.ErrConflict, service.CodeShortlistClosed, "closed")),
			http.StatusConflict, service.CodeShortlistClosed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("alice-token", contributorPrincipal())
			h.claims.EXPECT().Get(mock.Anything, domain.UserID(aliceID), domain.ClaimID(claimID)).
				Return(nil, tc.err)

			got := h.do(t, http.MethodGet, "/claims/"+claimID, "alice-token", "")
			require.Equal(t, tc.wantStatus, got.Status, "body was %s", got.Body)
			require.Equal(t, tc.wantCode, got.errorCode(t))
		})
	}
}

func TestUnexpectedErrorIsInternalAndOpaque(t *testing.T) {
	// An internal failure must not describe itself to a caller.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("pq: connection refused on 10.0.0.5"))

	got := h.do(t, http.MethodGet, "/claims/"+claimID, "alice-token", "")
	require.Equal(t, http.StatusInternalServerError, got.Status)
	require.Equal(t, "internal_error", got.errorCode(t))
	require.NotContains(t, string(got.Body), "10.0.0.5")
}

func TestErrorMessagesAreAttachedByCode(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(false))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, service.Coded(service.ErrForbidden, service.CodeVerificationNeeded, "pending"))

	got := h.do(t, http.MethodGet, "/search?skills=go", "hank-token", "")
	require.Equal(t, http.StatusForbidden, got.Status)

	var body errorResponse
	got.decode(t, &body)
	require.Equal(t, service.CodeVerificationNeeded, body.Error)
	require.Contains(t, body.Message, "awaiting review")
}

// --- request bodies ----------------------------------------------------------

func TestUnknownJSONFieldsAreRejected(t *testing.T) {
	// A client sending {"skil": "go"} has a typo. Ignoring it would evaluate a
	// claim against a skill nobody declared.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/skills", "alice-token",
		`{"skills":[{"slug":"go"}],"unexpected":true}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
}

func TestMalformedIDIsBadRequestNotNotFound(t *testing.T) {
	// `not-a-uuid` is a bad request. Reporting it as absent would suggest the
	// resource might exist under some other spelling — and it must never reach
	// SQL.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodGet, "/claims/not-a-uuid", "alice-token", "")
	require.Equal(t, http.StatusBadRequest, got.Status)
	require.Equal(t, service.CodeInvalidID, got.errorCode(t))
}

func TestUnroutedPathIsNotFound(t *testing.T) {
	h := newHarness(t)
	got := h.do(t, http.MethodGet, "/nothing/here", "", "")
	require.Equal(t, http.StatusNotFound, got.Status)
	require.Equal(t, service.CodeNotFound, got.errorCode(t))
}

func TestHealthNeedsNoCredentials(t *testing.T) {
	// A load balancer has none, and it reveals nothing.
	h := newHarness(t)
	got := h.do(t, http.MethodGet, "/health", "", "")
	require.Equal(t, http.StatusOK, got.Status)
}
