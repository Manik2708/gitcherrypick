package controller_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// The handlers and branches the behavioural tests above do not reach: the
// remaining verbs, and the failure path of every service call. A handler that
// dropped an error on the floor would return 200 with an empty body, and only a
// test that makes the service fail would notice.

const (
	requestID   = "01920000-0000-7000-8000-00000000ab01"
	shortlistID = "01920000-0000-7000-8000-0000000c0001"
	skillID     = "01920000-0000-7000-8000-000000009001"
)

// errBoom is an unremarkable service failure.
var errBoom = errors.New("the database went away")

func TestEveryHandlerPropagatesAServiceFailure(t *testing.T) {
	// Table-driven over the whole surface. Each entry primes exactly one
	// service call to fail and asserts the status is not a success — a handler
	// that ignored the error would answer 200.
	cases := map[string]struct {
		principal domain.Principal
		method    string
		path      string
		body      string
		arrange   func(h *harness)
	}{
		"claim create": {
			contributorPrincipal(), http.MethodPost, "/claims", "",
			func(h *harness) {
				h.claims.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"claim list": {
			contributorPrincipal(), http.MethodGet, "/claims", "",
			func(h *harness) {
				h.claims.EXPECT().List(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"claim replace": {
			contributorPrincipal(), http.MethodPut, "/claims/" + claimID,
			`{"version":1,"prs":[],"projects":[],"skills":[]}`,
			func(h *harness) {
				h.claims.EXPECT().Replace(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"claim set skills": {
			contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/skills",
			`{"skills":[{"slug":"go","nominated_primary":true}]}`,
			func(h *harness) {
				h.claims.EXPECT().SetSkills(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"claim set projects": {
			contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/evidence/projects",
			`{"projects":[{"url":"https://github.com/acme/bigrepo","contribution_summary":"Maintainer."}]}`,
			func(h *harness) {
				h.claims.EXPECT().SetProjectEvidence(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"claim submit": {
			contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/submit", "",
			func(h *harness) {
				h.claims.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil, errBoom)
			},
		},
		"claim withdraw": {
			contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/withdraw",
			`{"confirm_demotion":true}`,
			func(h *harness) {
				h.claims.EXPECT().Withdraw(mock.Anything, mock.Anything, mock.Anything, true).
					Return(nil, errBoom)
			},
		},
		"claim withdraw preview": {
			contributorPrincipal(), http.MethodGet, "/claims/" + claimID + "/withdraw-preview", "",
			func(h *harness) {
				h.claims.EXPECT().WithdrawPreview(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"claim suggestion": {
			contributorPrincipal(), http.MethodPost,
			"/claims/" + claimID + "/suggestions/" + skillID + "/accept", "",
			func(h *harness) {
				h.claims.EXPECT().DecideSuggestion(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, true).Return(nil, errBoom)
			},
		},
		"reevaluation request": {
			contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/reevaluation",
			`{"reason":"PR 102 was judged as a dependency bump."}`,
			func(h *harness) {
				h.reeval.EXPECT().Request(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"reevaluation status": {
			contributorPrincipal(), http.MethodGet, "/me/reevaluation-status", "",
			func(h *harness) {
				h.reeval.EXPECT().Status(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"me": {
			contributorPrincipal(), http.MethodGet, "/me", "",
			func(h *harness) {
				h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"set availability": {
			contributorPrincipal(), http.MethodPut, "/me/availability", `{"status":"looking_for_job"}`,
			func(h *harness) {
				h.auth.EXPECT().SetAvailability(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"my skills": {
			contributorPrincipal(), http.MethodGet, "/me/skills", "",
			func(h *harness) {
				h.skills.EXPECT().MySkills(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"my rank": {
			contributorPrincipal(), http.MethodGet, "/me/rank", "",
			func(h *harness) {
				h.discovery.EXPECT().MyRank(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"my verification": {
			hirerPrincipal(true), http.MethodGet, "/me/verification", "",
			func(h *harness) {
				h.orgs.EXPECT().Verification(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"mint share link": {
			contributorPrincipal(), http.MethodPost, "/me/share-link", "",
			func(h *harness) {
				h.auth.EXPECT().MintShareLink(mock.Anything, mock.Anything).Return("", "", errBoom)
			},
		},
		"revoke share link": {
			contributorPrincipal(), http.MethodDelete, "/me/share-link/" + skillID, "",
			func(h *harness) {
				h.auth.EXPECT().RevokeShareLink(mock.Anything, mock.Anything, mock.Anything).
					Return(errBoom)
			},
		},
		"contact requests": {
			contributorPrincipal(), http.MethodGet, "/me/contact-requests", "",
			func(h *harness) {
				h.contacts.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"contact respond": {
			contributorPrincipal(), http.MethodPost,
			"/me/contact-requests/" + skillID + "/decline", "",
			func(h *harness) {
				h.contacts.EXPECT().Respond(mock.Anything, mock.Anything, mock.Anything, false).
					Return(nil, errBoom)
			},
		},
		"search": {
			hirerPrincipal(true), http.MethodGet, "/search?skills=go", "",
			func(h *harness) {
				h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"leaderboard": {
			hirerPrincipal(true), http.MethodGet, "/leaderboard?kind=overall", "",
			func(h *harness) {
				h.discovery.EXPECT().Leaderboard(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"scorecard": {
			hirerPrincipal(true), http.MethodGet, "/contributors/" + aliceID + "/scorecard", "",
			func(h *harness) {
				h.discovery.EXPECT().Scorecard(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"saved searches": {
			hirerPrincipal(true), http.MethodGet, "/saved-searches", "",
			func(h *harness) {
				h.discovery.EXPECT().SavedSearches(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"save search": {
			hirerPrincipal(true), http.MethodPost, "/saved-searches",
			`{"name":"n","filters":{"skills":["go"]}}`,
			func(h *harness) {
				h.discovery.EXPECT().SaveSearch(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"replay saved search": {
			hirerPrincipal(true), http.MethodGet, "/saved-searches/" + skillID + "/results", "",
			func(h *harness) {
				h.discovery.EXPECT().ReplaySavedSearch(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"delete saved search": {
			hirerPrincipal(true), http.MethodDelete, "/saved-searches/" + skillID, "",
			func(h *harness) {
				h.discovery.EXPECT().DeleteSavedSearch(mock.Anything, mock.Anything, mock.Anything).
					Return(errBoom)
			},
		},
		"shortlist create": {
			hirerPrincipal(true), http.MethodPost, "/shortlists",
			`{"name":"R","description":"","tentative_result_date":"2026-12-01T00:00:00Z"}`,
			func(h *harness) {
				h.shortlists.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"shortlist list": {
			hirerPrincipal(true), http.MethodGet, "/shortlists", "",
			func(h *harness) {
				h.shortlists.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"shortlist update": {
			hirerPrincipal(true), http.MethodPatch, "/shortlists/" + shortlistID, `{"name":"New"}`,
			func(h *harness) {
				h.shortlists.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"shortlist close": {
			hirerPrincipal(true), http.MethodPost, "/shortlists/" + shortlistID + "/close", "",
			func(h *harness) {
				h.shortlists.EXPECT().Close(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"shortlist add entry": {
			hirerPrincipal(true), http.MethodPost, "/shortlists/" + shortlistID + "/entries",
			`{"user_id":"` + aliceID + `","note":""}`,
			func(h *harness) {
				h.shortlists.EXPECT().AddEntry(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"shortlist remove entry": {
			hirerPrincipal(true), http.MethodDelete,
			"/shortlists/" + shortlistID + "/entries/" + aliceID, "",
			func(h *harness) {
				h.shortlists.EXPECT().RemoveEntry(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(errBoom)
			},
		},
		"shortlist confirm": {
			hirerPrincipal(true), http.MethodPost, "/shortlists/" + shortlistID + "/confirm", "",
			func(h *harness) {
				h.shortlists.EXPECT().Confirm(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"shortlist contact requests": {
			hirerPrincipal(true), http.MethodGet,
			"/shortlists/" + shortlistID + "/contact-requests", "",
			func(h *harness) {
				h.shortlists.EXPECT().ContactRequests(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"invite": {
			hirerPrincipal(true), http.MethodPost, "/orgs/" + orgID + "/invitations",
			`{"email":"x@y.com"}`,
			func(h *harness) {
				h.orgs.EXPECT().Invite(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything).Return(nil, "", errBoom)
			},
		},
		"admin verifications": {
			adminPrincipal(), http.MethodGet, "/admin/verifications", "",
			func(h *harness) {
				h.admin.EXPECT().PendingVerifications(mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"admin decide verification": {
			adminPrincipal(), http.MethodPost, "/admin/verifications/" + requestID + "/decide",
			`{"decision":"approved"}`,
			func(h *harness) {
				h.admin.EXPECT().DecideVerification(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errBoom)
			},
		},
		"admin skill requests": {
			adminPrincipal(), http.MethodGet, "/admin/skill-requests", "",
			func(h *harness) {
				h.admin.EXPECT().PendingSkillRequests(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"admin decide skill request": {
			adminPrincipal(), http.MethodPost, "/admin/skill-requests/" + requestID + "/decide",
			`{"decision":"rejected","reason":"covered by go"}`,
			func(h *harness) {
				h.admin.EXPECT().DecideSkillRequest(mock.Anything, mock.Anything, mock.Anything,
					false, mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"admin reevaluations": {
			adminPrincipal(), http.MethodGet, "/admin/reevaluations", "",
			func(h *harness) {
				h.admin.EXPECT().Reevaluations(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errBoom)
			},
		},
		"admin decide reevaluation": {
			adminPrincipal(), http.MethodPost, "/admin/reevaluations/" + requestID + "/decide",
			`{"decision":"rejected","reason":"the judgement stands"}`,
			func(h *harness) {
				h.admin.EXPECT().DecideReevaluation(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything, mock.Anything).Return(errBoom)
			},
		},
		"admin sweep": {
			adminPrincipal(), http.MethodPost, "/admin/evaluations/sweep",
			`{"rubric_version":"v2","reason":"weights"}`,
			func(h *harness) {
				h.evaluation.EXPECT().Sweep(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(nil, errBoom)
			},
		},
		"skill search": {
			contributorPrincipal(), http.MethodGet, "/skills?q=go", "",
			func(h *harness) {
				h.skills.EXPECT().Search(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"skill request": {
			contributorPrincipal(), http.MethodPost, "/skill-requests",
			`{"proposed_name":"WebAssembly","rationale":"why"}`,
			func(h *harness) {
				h.skills.EXPECT().RequestSkill(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return("", errBoom)
			},
		},
		"public scorecard": {
			domain.Principal{}, http.MethodGet, "/public/scorecard/tok", "",
			func(h *harness) {
				h.auth.EXPECT().PublicScorecard(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"github start": {
			domain.Principal{}, http.MethodPost, "/auth/github/start", "{}",
			func(h *harness) {
				h.auth.EXPECT().GitHubAuthorizeURL(mock.Anything).Return("", "", errBoom)
			},
		},
		"google start": {
			domain.Principal{}, http.MethodGet, "/auth/google/start", "",
			func(h *harness) {
				h.auth.EXPECT().GoogleAuthorizeURL(mock.Anything).Return("", "", errBoom)
			},
		},
		"hirer register": {
			domain.Principal{}, http.MethodPost, "/auth/hirer/register",
			`{"email":"a@b.c","password":"pw","display_name":"A","organization":{"name":"O"}}`,
			func(h *harness) {
				h.auth.EXPECT().RegisterHirer(mock.Anything, mock.Anything).Return(nil, errBoom)
			},
		},
		"admin login": {
			domain.Principal{}, http.MethodPost, "/auth/admin/login",
			`{"email":"a@b.c","password":"pw"}`,
			func(h *harness) {
				h.auth.EXPECT().LoginAdmin(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, nil, errBoom)
			},
		},
		"logout": {
			contributorPrincipal(), http.MethodPost, "/auth/logout", "",
			func(h *harness) {
				h.auth.EXPECT().Logout(mock.Anything, mock.Anything).Return(errBoom)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			token := ""
			if tc.principal.Kind != "" {
				token = "token"
				h.signIn(token, tc.principal)
			}
			tc.arrange(h)

			got := h.do(t, tc.method, tc.path, token, tc.body)
			require.GreaterOrEqual(t, got.Status, 400,
				"a dropped error would answer %d with body %s", got.Status, got.Body)
		})
	}
}

func TestMalformedBodiesAreRejectedPerEndpoint(t *testing.T) {
	// Each of these decodes a body. A handler that ignored a decode failure
	// would act on a zero-valued request.
	cases := map[string]struct {
		principal domain.Principal
		method    string
		path      string
	}{
		"set availability":  {contributorPrincipal(), http.MethodPut, "/me/availability"},
		"claim replace":     {contributorPrincipal(), http.MethodPut, "/claims/" + claimID},
		"claim skills":      {contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/skills"},
		"claim prs":         {contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/evidence/prs"},
		"claim projects":    {contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/evidence/projects"},
		"reevaluation":      {contributorPrincipal(), http.MethodPost, "/claims/" + claimID + "/reevaluation"},
		"shortlist create":  {hirerPrincipal(true), http.MethodPost, "/shortlists"},
		"shortlist update":  {hirerPrincipal(true), http.MethodPatch, "/shortlists/" + shortlistID},
		"shortlist entries": {hirerPrincipal(true), http.MethodPost, "/shortlists/" + shortlistID + "/entries"},
		"save search":       {hirerPrincipal(true), http.MethodPost, "/saved-searches"},
		"invite":            {hirerPrincipal(true), http.MethodPost, "/orgs/" + orgID + "/invitations"},
		"sweep":             {adminPrincipal(), http.MethodPost, "/admin/evaluations/sweep"},
		"admin decide":      {adminPrincipal(), http.MethodPost, "/admin/reevaluations/" + requestID + "/decide"},
		"accept invitation": {domain.Principal{}, http.MethodPost, "/orgs/invitations/tok/accept"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			token := ""
			if tc.principal.Kind != "" {
				token = "token"
				h.signIn(token, tc.principal)
			}

			got := h.do(t, tc.method, tc.path, token, `{"broken":`)
			require.GreaterOrEqual(t, got.Status, 400, "body was %s", got.Body)
		})
	}
}

func TestWithdrawAcceptsAnAbsentBody(t *testing.T) {
	// Its only parameter is optional, so an empty body means "do not confirm
	// demotion" rather than "malformed request".
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().Withdraw(mock.Anything, mock.Anything, mock.Anything, false).
		Return(&domain.Claim{ID: claimID, Status: domain.ClaimStatus("withdrawn")}, nil)

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/withdraw", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)
}

func TestMalformedIDsAcrossTheSurface(t *testing.T) {
	cases := map[string]struct {
		principal domain.Principal
		method    string
		path      string
	}{
		"scorecard":       {hirerPrincipal(true), http.MethodGet, "/contributors/nope/scorecard"},
		"saved search":    {hirerPrincipal(true), http.MethodGet, "/saved-searches/nope/results"},
		"delete search":   {hirerPrincipal(true), http.MethodDelete, "/saved-searches/nope"},
		"shortlist get":   {hirerPrincipal(true), http.MethodGet, "/shortlists/nope"},
		"remove entry":    {hirerPrincipal(true), http.MethodDelete, "/shortlists/" + shortlistID + "/entries/nope"},
		"invite org":      {hirerPrincipal(true), http.MethodPost, "/orgs/nope/invitations"},
		"admin decide":    {adminPrincipal(), http.MethodPost, "/admin/verifications/nope/decide"},
		"claim reeval":    {contributorPrincipal(), http.MethodPost, "/claims/nope/reevaluation"},
		"claim submit":    {contributorPrincipal(), http.MethodPost, "/claims/nope/submit"},
		"claim withdrawp": {contributorPrincipal(), http.MethodGet, "/claims/nope/withdraw-preview"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("token", tc.principal)

			got := h.do(t, tc.method, tc.path, "token", `{"decision":"approved","email":"x@y.com"}`)
			require.Equal(t, http.StatusBadRequest, got.Status, "body was %s", got.Body)
			require.Equal(t, service.CodeInvalidID, got.errorCode(t))
		})
	}
}

func TestNotFoundIsNamedPerResource(t *testing.T) {
	// The service returns a generic absence; only the controller knows what
	// the caller was asking about.
	cases := map[string]struct {
		principal domain.Principal
		method    string
		path      string
		arrange   func(h *harness)
		wantCode  string
	}{
		"saved search": {
			hirerPrincipal(true), http.MethodDelete, "/saved-searches/" + skillID,
			func(h *harness) {
				h.discovery.EXPECT().DeleteSavedSearch(mock.Anything, mock.Anything, mock.Anything).
					Return(service.ErrNotFound)
			},
			service.CodeSavedSearchNotFound,
		},
		"replayed search": {
			hirerPrincipal(true), http.MethodGet, "/saved-searches/" + skillID + "/results",
			func(h *harness) {
				h.discovery.EXPECT().ReplaySavedSearch(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, service.ErrNotFound)
			},
			service.CodeSavedSearchNotFound,
		},
		"shortlist entry": {
			hirerPrincipal(true), http.MethodDelete,
			"/shortlists/" + shortlistID + "/entries/" + aliceID,
			func(h *harness) {
				h.shortlists.EXPECT().RemoveEntry(mock.Anything, mock.Anything, mock.Anything,
					mock.Anything).Return(service.ErrNotFound)
			},
			service.CodeShortlistNotFound,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("token", tc.principal)
			tc.arrange(h)

			got := h.do(t, tc.method, tc.path, "token", "")
			require.Equal(t, http.StatusNotFound, got.Status)
			require.Equal(t, tc.wantCode, got.errorCode(t))
		})
	}
}

func TestSuccessfulWritesAcrossTheSurface(t *testing.T) {
	t.Run("shortlist update", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("hank-token", hirerPrincipal(true))
		h.shortlists.EXPECT().Update(mock.Anything, mock.Anything, domain.ShortlistID(shortlistID),
			mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.Shortlist{ID: shortlistID, Name: "Renamed"}, nil)

		got := h.do(t, http.MethodPatch, "/shortlists/"+shortlistID, "hank-token", `{"name":"Renamed"}`)
		require.Equal(t, http.StatusOK, got.Status)
	})

	t.Run("shortlist list and entries", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("hank-token", hirerPrincipal(true))
		h.shortlists.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).
			Return([]domain.Shortlist{{ID: shortlistID, Name: "R", Status: "draft"}}, nil)
		require.Equal(t, http.StatusOK,
			h.do(t, http.MethodGet, "/shortlists?status=draft", "hank-token", "").Status)

		h.shortlists.EXPECT().AddEntry(mock.Anything, mock.Anything, mock.Anything,
			domain.UserID(aliceID), "strong go").
			Return(&domain.ShortlistEntry{UserID: aliceID, Note: "strong go"}, nil)
		require.Equal(t, http.StatusCreated,
			h.do(t, http.MethodPost, "/shortlists/"+shortlistID+"/entries", "hank-token",
				`{"user_id":"`+aliceID+`","note":"strong go"}`).Status)

		h.shortlists.EXPECT().RemoveEntry(mock.Anything, mock.Anything, mock.Anything,
			domain.UserID(aliceID)).Return(nil)
		require.Equal(t, http.StatusNoContent,
			h.do(t, http.MethodDelete, "/shortlists/"+shortlistID+"/entries/"+aliceID,
				"hank-token", "").Status)

		h.shortlists.EXPECT().ContactRequests(mock.Anything, mock.Anything, mock.Anything).
			Return([]domain.ContactRequest{{
				ID: "01920000-0000-7000-8000-00000000f001", Status: "pending",
				TentativeResultDate: fixedTime(), ExpiresAt: fixedTime(),
			}}, nil)
		require.Equal(t, http.StatusOK,
			h.do(t, http.MethodGet, "/shortlists/"+shortlistID+"/contact-requests",
				"hank-token", "").Status)
	})

	t.Run("claim replace and projects", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("alice-token", contributorPrincipal())

		h.claims.EXPECT().Replace(mock.Anything, mock.Anything, mock.Anything, 1,
			mock.MatchedBy(func(c *domain.Claim) bool {
				return len(c.PREvidence) == 1 && len(c.Skills) == 1 &&
					c.Skills[0].Origin == domain.UserDeclared
			})).Return(&domain.Claim{ID: claimID, Version: 2, Status: "draft"}, nil)

		got := h.do(t, http.MethodPut, "/claims/"+claimID, "alice-token",
			`{"version":1,"prs":[{"position":1,"url":"https://github.com/a/b/pull/1"}],
			  "projects":[],"skills":[{"slug":"go","nominated_primary":true}]}`)
		require.Equal(t, http.StatusOK, got.Status, "body was %s", got.Body)

		h.claims.EXPECT().SetProjectEvidence(mock.Anything, mock.Anything, mock.Anything,
			mock.MatchedBy(func(ps []domain.ProjectEvidence) bool {
				return len(ps) == 1 && ps[0].RepoOwner == "acme" && ps[0].RepoName == "bigrepo"
			})).Return(&domain.Claim{ProjectEvidence: []domain.ProjectEvidence{{
			RepoOwner: "acme", RepoName: "bigrepo", ContributionSummary: "Maintainer.",
		}}}, nil)

		projects := h.do(t, http.MethodPost, "/claims/"+claimID+"/evidence/projects", "alice-token",
			`{"projects":[{"url":"https://github.com/acme/bigrepo","contribution_summary":"Maintainer."}]}`)
		require.Equal(t, http.StatusOK, projects.Status, "body was %s", projects.Body)
	})

	t.Run("claim skills and reevaluation", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("alice-token", contributorPrincipal())

		h.claims.EXPECT().SetSkills(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.Claim{Skills: []domain.ClaimSkill{{
				Slug: "go", Origin: domain.UserDeclared, IsNominatedPrimary: true,
			}}}, nil)
		require.Equal(t, http.StatusOK,
			h.do(t, http.MethodPost, "/claims/"+claimID+"/skills", "alice-token",
				`{"skills":[{"slug":"go","nominated_primary":true}]}`).Status)

		h.reeval.EXPECT().Request(mock.Anything, mock.Anything, mock.Anything, "wrong call").
			Return(&domain.ReevaluationRequest{
				ID: requestID, ClaimID: claimID, Status: "pending", CreatedAt: fixedTime(),
			}, nil)
		require.Equal(t, http.StatusCreated,
			h.do(t, http.MethodPost, "/claims/"+claimID+"/reevaluation", "alice-token",
				`{"reason":"wrong call"}`).Status)
	})

	t.Run("admin decide verification", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("admin-token", adminPrincipal())
		h.admin.EXPECT().DecideVerification(mock.Anything, mock.Anything,
			domain.RequestID(requestID), domain.VerificationDecision{
				Approve: true, Reason: "looks legitimate",
			}).Return(nil)

		got := h.do(t, http.MethodPost, "/admin/verifications/"+requestID+"/decide", "admin-token",
			`{"decision":"approved","reason":"looks legitimate"}`)
		require.Equal(t, http.StatusOK, got.Status)

		var body struct {
			Status string `json:"status"`
		}
		got.decode(t, &body)
		require.Equal(t, "approved", body.Status)
	})

	t.Run("contact request list filters by status", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("alice-token", contributorPrincipal())
		h.contacts.EXPECT().List(mock.Anything, domain.UserID(aliceID),
			mock.MatchedBy(func(s *domain.ContactRequestStatus) bool {
				return s != nil && *s == domain.ContactRequestStatus("pending")
			})).Return([]domain.ContactRequest{{
			ID: "01920000-0000-7000-8000-00000000f001", Status: "pending",
			TentativeResultDate: fixedTime(), ExpiresAt: fixedTime().Add(72 * time.Hour),
		}}, nil)

		got := h.do(t, http.MethodGet, "/me/contact-requests?status=pending", "alice-token", "")
		require.Equal(t, http.StatusOK, got.Status)

		var body struct {
			Total    int `json:"total"`
			Requests []struct {
				Status string `json:"status"`
			} `json:"requests"`
		}
		got.decode(t, &body)
		require.Equal(t, 1, body.Total)
		require.Equal(t, "pending", body.Requests[0].Status)
	})

	t.Run("scorecard with released email", func(t *testing.T) {
		// Released only after a contact request is accepted (ADR-0002 §5).
		h := newHarness(t)
		h.signIn("hank-token", hirerPrincipal(true))

		email := "alice@example.com"
		h.discovery.EXPECT().Scorecard(mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.Scorecard{
				User:  domain.SearchResult{UserID: aliceID, DisplayName: "Alice"},
				Email: &email,
				Skills: []domain.ScorecardSkill{{
					ResultSkill:     domain.ResultSkill{Slug: "go", Name: "Go", Standing: domain.Primary},
					DistinctPRCount: 5,
					Evidence: []domain.ScorecardEvidence{{
						Repo: "acme/bigrepo", PRNumber: 101, Title: "Fix", Score: 79.3,
					}},
				}},
			}, nil)

		got := h.do(t, http.MethodGet, "/contributors/"+aliceID+"/scorecard", "hank-token", "")
		require.Equal(t, http.StatusOK, got.Status)

		// The contributor is nested under `user`, the same as everywhere else
		// a person appears in a hirer-facing body.
		var body struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Email  *string `json:"email"`
			Skills []struct {
				Slug            string `json:"slug"`
				DistinctPRCount int    `json:"distinct_pr_count"`
				Evidence        []struct {
					PRNumber int `json:"pr_number"`
				} `json:"evidence"`
			} `json:"skills"`
		}
		got.decode(t, &body)
		require.Equal(t, aliceID, body.User.ID, "an authenticated scorecard is identified")
		require.NotNil(t, body.Email)
		require.Equal(t, 5, body.Skills[0].DistinctPRCount)
		require.Len(t, body.Skills[0].Evidence, 1)
	})

	t.Run("rank with positions", func(t *testing.T) {
		h := newHarness(t)
		h.signIn("alice-token", contributorPrincipal())

		score, rank, outOf := 64.2, 2, 4
		h.discovery.EXPECT().MyRank(mock.Anything, mock.Anything).Return(&domain.Rank{
			RubricVersion: "v1", Ranked: true, Active: true,
			Overall:    domain.RankPosition{Score: &score, Rank: &rank, OutOf: 4},
			Generalist: domain.RankPosition{Score: &score, Rank: &rank, OutOf: 4},
			Skills: []domain.SkillRank{{
				Slug: "go", Name: "Go", Standing: domain.Primary,
				Score: 79.3, Rank: &rank, OutOf: &outOf,
			}},
		}, nil)

		got := h.do(t, http.MethodGet, "/me/rank", "alice-token", "")
		require.Equal(t, http.StatusOK, got.Status)

		var body struct {
			Ranked  bool `json:"ranked"`
			Overall struct {
				Rank  *int `json:"rank"`
				OutOf int  `json:"out_of"`
			} `json:"overall"`
			Skills []struct {
				Slug string `json:"slug"`
			} `json:"skills"`
		}
		got.decode(t, &body)
		require.True(t, body.Ranked)
		require.Equal(t, 2, *body.Overall.Rank)
		require.Equal(t, 4, body.Overall.OutOf)
		require.Len(t, body.Skills, 1)
	})
}

func TestUnrankedReasonIsReportedWhenPresent(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.discovery.EXPECT().MyRank(mock.Anything, mock.Anything).Return(&domain.Rank{
		Ranked: false, UnrankedReason: "no primary skill", Active: false,
	}, nil)

	got := h.do(t, http.MethodGet, "/me/rank", "alice-token", "")
	var body struct {
		UnrankedReason *string `json:"unranked_reason"`
	}
	got.decode(t, &body)
	require.NotNil(t, body.UnrankedReason)
	require.Equal(t, "no primary skill", *body.UnrankedReason)
}

func TestAdminMe(t *testing.T) {
	h := newHarness(t)
	p := adminPrincipal()
	h.signIn("admin-token", p)
	h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(&p, nil)

	got := h.do(t, http.MethodGet, "/me", "admin-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		PrincipalType string `json:"principal_type"`
	}
	got.decode(t, &body)
	require.Equal(t, "admin", body.PrincipalType)
}

func TestVerificationWithADecision(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(false))

	reviewed := fixedTime()
	h.orgs.EXPECT().Verification(mock.Anything, mock.Anything).Return(&port.VerificationStatus{
		Status: "rejected", SubmittedAt: fixedTime().Add(-48 * time.Hour),
		ReviewedAt: &reviewed, Reason: "no public evidence of trading",
		Organization: &domain.Organization{ID: orgID, Name: "Unknown Ltd"},
	}, nil)

	got := h.do(t, http.MethodGet, "/me/verification", "hank-token", "")
	var body struct {
		Status     string     `json:"status"`
		ReviewedAt *time.Time `json:"reviewed_at"`
		Reason     *string    `json:"reason"`
	}
	got.decode(t, &body)
	require.Equal(t, "rejected", body.Status)
	require.NotNil(t, body.ReviewedAt)
	require.Equal(t, "no public evidence of trading", *body.Reason)
}
