package controller_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// A service returning (nil, nil) is a bug, but a handler that dereferences it
// panics and takes the process down with it. These assert the nil guards hold,
// so the worst case is an empty body rather than a crash.

func TestNilResultsDoNotPanic(t *testing.T) {
	cases := map[string]struct {
		principal domain.Principal
		method    string
		path      string
		body      string
		arrange   func(h *harness)
	}{
		"claim": {
			contributorPrincipal(), http.MethodPost, "/claims", "",
			func(h *harness) {
				h.claims.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, nil)
			},
		},
		"rank": {
			contributorPrincipal(), http.MethodGet, "/me/rank", "",
			func(h *harness) {
				h.discovery.EXPECT().MyRank(mock.Anything, mock.Anything).Return(nil, nil)
			},
		},
		"search results": {
			hirerPrincipal(true), http.MethodGet, "/search?skills=go", "",
			func(h *harness) {
				h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil)
			},
		},
		"scorecard": {
			hirerPrincipal(true), http.MethodGet, "/contributors/" + aliceID + "/scorecard", "",
			func(h *harness) {
				h.discovery.EXPECT().Scorecard(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil)
			},
		},
		"shortlist": {
			hirerPrincipal(true), http.MethodGet, "/shortlists/" + shortlistID, "",
			func(h *harness) {
				h.shortlists.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil)
			},
		},
		"availability": {
			contributorPrincipal(), http.MethodPut, "/me/availability", `{"status":"not_looking"}`,
			func(h *harness) {
				h.auth.EXPECT().SetAvailability(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil)
			},
		},
		"cooldown": {
			contributorPrincipal(), http.MethodGet, "/me/reevaluation-status", "",
			func(h *harness) {
				h.reeval.EXPECT().Status(mock.Anything, mock.Anything).
					Return(&domain.DisputeStanding{ClaimsEligible: []domain.ClaimID{}}, nil)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("token", tc.principal)
			tc.arrange(h)

			require.NotPanics(t, func() {
				got := h.do(t, tc.method, tc.path, "token", tc.body)
				require.Less(t, got.Status, 500, "body was %s", got.Body)
			})
		})
	}
}

func TestNilTokenPairSerializesEmpty(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().LoginAdmin(mock.Anything, mock.Anything, mock.Anything).
		Return(&domain.Admin{ID: adminID, DisplayName: "Root"}, nil, nil)

	got := h.do(t, http.MethodPost, "/auth/admin/login", "", `{"email":"a@b.c","password":"pw"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		AccessToken string `json:"access_token"`
	}
	got.decode(t, &body)
	require.Empty(t, body.AccessToken)
}

func TestHirerWithNoOrganizationSerializes(t *testing.T) {
	// The pointer is nil when a caller built the hirer without one. It must
	// serialize as an empty organization rather than panic.
	h := newHarness(t)
	h.auth.EXPECT().LoginHirer(mock.Anything, mock.Anything, mock.Anything).
		Return(&domain.Hirer{ID: hankID, DisplayName: "Orphan"}, tokenPair(), nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/login", "", `{"email":"a@b.c","password":"pw"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Hirer struct {
			Organization struct {
				Name string `json:"name"`
			} `json:"organization"`
		} `json:"hirer"`
	}
	got.decode(t, &body)
	require.Empty(t, body.Hirer.Organization.Name)
}

func TestGoogleCallbackRejectsAMismatchedState(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().GoogleAuthorizeURL(mock.Anything).Return("https://accounts.google.com/x", "s1", nil)

	start := h.do(t, http.MethodGet, "/auth/google/start", "", "")
	req := newRequest(t, http.MethodGet, "/auth/google/callback?code=c&state=wrong", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusBadRequest, got.Status)
	require.Equal(t, service.CodeInvalidState, got.errorCode(t))
	h.auth.AssertNotCalled(t, "CompleteGoogle",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestGoogleCallbackSucceeds(t *testing.T) {
	h := newHarness(t)
	now := fixedTime()
	h.auth.EXPECT().GoogleAuthorizeURL(mock.Anything).Return("https://accounts.google.com/x", "s1", nil)
	h.auth.EXPECT().CompleteGoogle(mock.Anything, "goog_valid", "s1", "s1").
		Return(&domain.Hirer{
			ID: hankID, DisplayName: "Hank Rivera", Email: "hank@acme.com", VerifiedAt: &now,
			Organization: &domain.Organization{ID: orgID, Name: "Acme", VerifiedAt: &now},
		}, tokenPair(), nil)

	start := h.do(t, http.MethodGet, "/auth/google/start", "", "")
	req := newRequest(t, http.MethodGet, "/auth/google/callback?code=goog_valid&state=s1", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusOK, got.Status, "body was %s", got.Body)
}

func TestGoogleCallbackUnexpectedFailure(t *testing.T) {
	// Not one of the three named cases: it must still become a response rather
	// than fall through.
	h := newHarness(t)
	h.auth.EXPECT().GoogleAuthorizeURL(mock.Anything).Return("https://accounts.google.com/x", "s1", nil)
	h.auth.EXPECT().CompleteGoogle(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, errors.New("upstream exploded"))

	start := h.do(t, http.MethodGet, "/auth/google/start", "", "")
	req := newRequest(t, http.MethodGet, "/auth/google/callback?code=c&state=s1", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusInternalServerError, got.Status)
}

func TestGitHubCallbackUnexpectedFailure(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).Return("https://github.com/x", "s1", nil)
	h.auth.EXPECT().CompleteGitHub(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, false, errors.New("upstream exploded"))

	start := h.do(t, http.MethodPost, "/auth/github/start", "", "{}")
	req := newRequest(t, http.MethodGet, "/auth/github/callback?code=c&state=s1", "")
	req.AddCookie(&http.Cookie{Name: "gcp_oauth_state", Value: stateFrom(t, start)})

	got := h.serve(req)
	require.Equal(t, http.StatusInternalServerError, got.Status)
}

func TestRejectedDecisionsReportRejected(t *testing.T) {
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())
	h.admin.EXPECT().DecideVerification(mock.Anything, mock.Anything, mock.Anything,
		domain.VerificationDecision{Reason: "no evidence"}).Return(nil)

	got := h.do(t, http.MethodPost, "/admin/verifications/"+requestID+"/decide", "admin-token",
		`{"decision":"rejected","reason":"no evidence"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Status string `json:"status"`
	}
	got.decode(t, &body)
	require.Equal(t, "rejected", body.Status)
}

func TestDecisionVerbSynonyms(t *testing.T) {
	// Clients differ on "approve" versus "approved"; both mean the same thing
	// and neither should cost a contributor a cooldown tier by accident.
	for verb, want := range map[string]bool{
		"approve": true, "approved": true, "accept": true, "accepted": true,
		"reject": false, "rejected": false, "decline": false, "declined": false,
	} {
		t.Run(verb, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("admin-token", adminPrincipal())
			h.admin.EXPECT().DecideVerification(mock.Anything, mock.Anything, mock.Anything,
				domain.VerificationDecision{Approve: want, Reason: "r"}).Return(nil)

			got := h.do(t, http.MethodPost, "/admin/verifications/"+requestID+"/decide",
				"admin-token", `{"decision":"`+verb+`","reason":"r"}`)
			require.Equal(t, http.StatusOK, got.Status)
		})
	}
}

func TestResultSkillsAreSerialized(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	rank := 1
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
		Return(&domain.SearchResults{
			Total: 1, RankedBy: "overall", Page: 1, PerPage: 20,
			Results: []domain.SearchResult{{
				Rank: 1, UserID: aliceID, DisplayName: "Alice", Active: true,
				Availability: &domain.Availability{Status: domain.LookingForJob},
				Skills: []domain.ResultSkill{{
					Slug: "go", Name: "Go", Standing: domain.Primary, Score: 79.3, Rank: &rank,
				}},
			}},
		}, nil)

	got := h.do(t, http.MethodGet, "/search", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Results []struct {
			Skills []struct {
				Slug     string `json:"slug"`
				Standing string `json:"standing"`
				Rank     *int   `json:"rank"`
			} `json:"skills"`
			Availability *struct {
				Status string `json:"status"`
			} `json:"availability"`
		} `json:"results"`
	}
	got.decode(t, &body)
	require.Len(t, body.Results[0].Skills, 1)
	require.Equal(t, "primary", body.Results[0].Skills[0].Standing)
	require.Equal(t, 1, *body.Results[0].Skills[0].Rank)
	require.NotNil(t, body.Results[0].Availability)
}

func TestMalformedProjectURLInAWholeClaimEdit(t *testing.T) {
	// A malformed PROJECT url is still refused, unlike a PR one. PR evidence
	// has an invalid_reason column to record the failure in and a submit-time
	// check that acts on it; a project has neither, so accepting one would
	// store a repository nothing can ever resolve.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	projects := h.do(t, http.MethodPut, "/claims/"+claimID, "alice-token",
		`{"version":1,"prs":[],"projects":[{"url":"nonsense","contribution_summary":""}],"skills":[]}`)
	require.Equal(t, http.StatusUnprocessableEntity, projects.Status)
	require.Equal(t, service.CodeInvalidEvidence, projects.errorCode(t))

	h.claims.AssertNotCalled(t, "Replace",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestClaimNotFoundIsNamed(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, service.ErrNotFound)

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/submit", "alice-token", "")
	require.Equal(t, http.StatusNotFound, got.Status)
	require.Equal(t, service.CodeClaimNotFound, got.errorCode(t))
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHarness(t)
	got := h.do(t, http.MethodDelete, "/health", "", "")
	require.Equal(t, http.StatusMethodNotAllowed, got.Status)
}
