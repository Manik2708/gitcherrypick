package controller_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/controller"
	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

func TestShortlistCapabilityRefusalCarriesTheDetail(t *testing.T) {
	// Same rule as search: a hirer refused for capability is told WHICH of the
	// two verifications is missing.
	h := newHarness(t)
	now := fixedTime()
	p := hirerPrincipal(false)
	p.Hirer.Organization.VerifiedAt = &now // the org is verified; the person is not

	h.signIn("hank-token", p)
	h.shortlists.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything).Return(nil,
		service.Coded(service.ErrNotCapable, service.CodeHiringCapability, "seat unverified"))

	got := h.do(t, http.MethodPost, "/shortlists", "hank-token",
		`{"role_id":"01920000-0000-7000-8000-0000000e0001","name":"R","description":"","tentative_result_date":"2026-12-01T00:00:00Z"}`)
	require.Equal(t, http.StatusForbidden, got.Status)

	var body struct {
		Error                string `json:"error"`
		HirerVerified        bool   `json:"hirer_verified"`
		OrganizationVerified bool   `json:"organization_verified"`
	}
	got.decode(t, &body)
	require.Equal(t, service.CodeHiringCapability, body.Error)
	require.False(t, body.HirerVerified)
	require.True(t, body.OrganizationVerified)
}

func TestShortlistVerificationRefusalCarriesItsMessage(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(false))
	h.shortlists.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, service.Coded(service.ErrForbidden, service.CodeVerificationNeeded, "pending"))

	got := h.do(t, http.MethodGet, "/shortlists", "hank-token", "")
	require.Equal(t, http.StatusForbidden, got.Status)

	var body errorResponse
	got.decode(t, &body)
	require.Equal(t, service.CodeVerificationNeeded, body.Error)
	require.Contains(t, body.Message, "48 hours")
}

func TestExpiredVerificationIsGone(t *testing.T) {
	// A coded expiry maps to 410 directly, without going through the generic
	// conflict path.
	h := newHarness(t)
	h.redemption.EXPECT().CompleteRedemption(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, service.Coded(service.ErrInvalidCredentials,
			service.CodeVerificationExpired, "expired"))

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/complete", "",
		`{"token":"stale","display_name":"x","password":"pw"}`)
	require.Equal(t, http.StatusGone, got.Status)
	require.Equal(t, service.CodeVerificationExpired, got.errorCode(t))
}

func TestRedemptionUnexpectedFailure(t *testing.T) {
	h := newHarness(t)
	h.redemption.EXPECT().CompleteRedemption(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, errBoom)

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/complete", "",
		`{"token":"x","display_name":"x","password":"pw"}`)
	require.Equal(t, http.StatusInternalServerError, got.Status)
}

// The profile filters, parsed (ADR-0018, ADR-0019). Country codes and shape
// names are FOLDED, because both are closed vocabularies two systems have to
// agree on — "gb" and "GB" cannot be two countries.
func TestProfileFiltersAreParsed(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything,
		mock.MatchedBy(func(q domain.SearchQuery) bool {
			return q.MinOfficeYOE != nil && *q.MinOfficeYOE == 5 &&
				q.MinOSSYOE != nil && *q.MinOSSYOE == 3 &&
				len(q.Countries) == 2 && q.Countries[0] == "GB" && q.Countries[1] == "DE" &&
				len(q.OpenTo) == 2 && q.OpenTo[0] == "remote" && q.OpenTo[1] == "contract"
		})).Return(&domain.SearchResults{}, nil)

	require.Equal(t, http.StatusOK, h.do(t, http.MethodGet,
		"/search?min_office_yoe=5&min_oss_yoe=3&countries=gb,%20De&open_to=Remote,contract",
		"hank-token", "").Status)
}

// There is no pay filter, and there must never be one: a hirer able to filter
// on what somebody expects would learn an upper bound across a few searches,
// and an expectation would stop being a floor and become a ceiling
// (ADR-0018 §5). An unknown key is refused rather than ignored.
func TestThereIsNoCompensationFilter(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	for _, key := range []string{"max_yearly", "max_hourly", "compensation", "yearly_amount"} {
		got := h.do(t, http.MethodGet, "/search?"+key+"=50000", "hank-token", "")
		require.Equal(t, http.StatusUnprocessableEntity, got.Status, key)
		require.Equal(t, service.CodeUnknownFilter, got.errorCode(t), key)
	}
}

func TestUnparseableNumericFiltersAreDroppedNotRejected(t *testing.T) {
	// A malformed number is a client bug, but refusing the whole search would
	// be worse than ignoring one filter it could not read.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything,
		mock.MatchedBy(func(q domain.SearchQuery) bool {
			return q.MinSkillScore == nil && q.MinOfficeYOE == nil
		})).Return(&domain.SearchResults{}, nil)

	require.Equal(t, http.StatusOK, h.do(t, http.MethodGet,
		"/search?min_skill_score=high&min_office_yoe=lots", "hank-token", "").Status)
}

func TestRegisterHirerRejectsAMalformedBody(t *testing.T) {
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/hirer/register", "", `{"email":`)
	require.Equal(t, http.StatusBadRequest, got.Status)
	h.auth.AssertNotCalled(t, "RegisterHirer", mock.Anything, mock.Anything)
}

func TestAdminLoginRejectsAMalformedBody(t *testing.T) {
	h := newHarness(t)

	got := h.do(t, http.MethodPost, "/auth/admin/login", "", `{"email":`)
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeInvalidCredentials, got.errorCode(t))
	h.auth.AssertNotCalled(t, "LoginAdmin", mock.Anything, mock.Anything, mock.Anything)
}

func TestPrincipalAccessor(t *testing.T) {
	// Exposed so a test can assert what middleware resolved, and so nothing
	// outside the package can inject one.
	_, ok := controller.Principal(context.Background())
	require.False(t, ok, "an untouched context carries no principal")

	ctx := controller.WithPrincipal(context.Background(), contributorPrincipal())
	p, ok := controller.Principal(ctx)
	require.True(t, ok)
	require.Equal(t, domain.KindContributor, p.Kind)
}

func TestServerListensAndShutsDown(t *testing.T) {
	ctx := context.Background()

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	h := newHarness(t)

	served := make(chan error, 1)
	go func() { served <- h.server.ListenAndServe(addr) }()

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("http://%s/health", addr), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, h.server.Shutdown())

	select {
	case err := <-served:
		require.NoError(t, err, "a deliberate shutdown is not an error")
	case <-time.After(10 * time.Second):
		t.Fatal("ListenAndServe did not return after Shutdown")
	}
}

func TestShutdownBeforeListenIsHarmless(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.server.Shutdown())
}

func TestListenOnAnUnusablePortIsReported(t *testing.T) {
	h := newHarness(t)
	require.Error(t, h.server.ListenAndServe("127.0.0.1:-1"))
}
