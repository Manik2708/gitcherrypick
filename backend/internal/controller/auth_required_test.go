package controller_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// Every authenticated route, driven with no credentials at all.
//
// This is the sweep that would notice a handler wired without its require()
// guard: the service is mocked with NO expectations, so reaching one fails the
// test rather than quietly succeeding.
func TestEveryAuthenticatedRouteRefusesAnonymousCallers(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/me"},
		{http.MethodPut, "/me/availability"},
		{http.MethodGet, "/me/skills"},
		{http.MethodGet, "/me/rank"},
		{http.MethodGet, "/me/verification"},
		{http.MethodGet, "/me/reevaluation-status"},
		{http.MethodPost, "/me/share-link"},
		{http.MethodDelete, "/me/share-link/" + skillID},
		{http.MethodGet, "/me/contact-requests"},
		{http.MethodPost, "/me/contact-requests/" + skillID + "/accept"},
		{http.MethodPost, "/me/contact-requests/" + skillID + "/decline"},

		{http.MethodPost, "/claims"},
		{http.MethodGet, "/claims"},
		{http.MethodGet, "/claims/" + claimID},
		{http.MethodPut, "/claims/" + claimID},
		{http.MethodPost, "/claims/" + claimID + "/evidence/prs"},
		{http.MethodPost, "/claims/" + claimID + "/evidence/projects"},
		{http.MethodPost, "/claims/" + claimID + "/skills"},
		{http.MethodPost, "/claims/" + claimID + "/submit"},
		{http.MethodGet, "/claims/" + claimID + "/withdraw-preview"},
		{http.MethodPost, "/claims/" + claimID + "/withdraw"},
		{http.MethodPost, "/claims/" + claimID + "/suggestions/" + skillID + "/accept"},
		{http.MethodPost, "/claims/" + claimID + "/suggestions/" + skillID + "/dismiss"},
		{http.MethodPost, "/claims/" + claimID + "/reevaluation"},

		{http.MethodPost, "/skill-requests"},

		{http.MethodGet, "/search"},
		{http.MethodGet, "/leaderboard"},
		{http.MethodGet, "/contributors/" + aliceID + "/scorecard"},
		{http.MethodGet, "/saved-searches"},
		{http.MethodPost, "/saved-searches"},
		{http.MethodGet, "/saved-searches/" + skillID + "/results"},
		{http.MethodDelete, "/saved-searches/" + skillID},

		{http.MethodPost, "/shortlists"},
		{http.MethodGet, "/shortlists"},
		{http.MethodGet, "/shortlists/" + shortlistID},
		{http.MethodPatch, "/shortlists/" + shortlistID},
		{http.MethodPost, "/shortlists/" + shortlistID + "/close"},
		{http.MethodPost, "/shortlists/" + shortlistID + "/confirm"},
		{http.MethodPost, "/shortlists/" + shortlistID + "/entries"},
		{http.MethodDelete, "/shortlists/" + shortlistID + "/entries/" + aliceID},
		{http.MethodGet, "/shortlists/" + shortlistID + "/contact-requests"},

		{http.MethodPost, "/orgs/" + orgID + "/invitations"},

		{http.MethodGet, "/admin/verifications"},
		{http.MethodPost, "/admin/verifications/" + requestID + "/decide"},
		{http.MethodGet, "/admin/skill-requests"},
		{http.MethodPost, "/admin/skill-requests/" + requestID + "/decide"},
		{http.MethodGet, "/admin/reevaluations"},
		{http.MethodPost, "/admin/reevaluations/" + requestID + "/decide"},
		{http.MethodPost, "/admin/evaluations/sweep"},

		{http.MethodPost, "/auth/logout"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			h := newHarness(t)

			got := h.do(t, route.method, route.path, "", `{}`)
			require.Equal(t, http.StatusUnauthorized, got.Status,
				"anonymous caller reached the handler; body was %s", got.Body)
			require.Equal(t, service.CodeAuthRequired, got.errorCode(t))
		})
	}
}

func TestEmptyBodyIsRejectedWhereOneIsRequired(t *testing.T) {
	// decode reports an empty body distinctly from a malformed one, because
	// "you sent nothing" and "you sent nonsense" are different client bugs.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/skills", "alice-token", " ")
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
}

func TestOversizedBodyIsRejected(t *testing.T) {
	// Unbounded decoding lets one request exhaust memory.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	huge := `{"skills":[{"slug":"` + strings.Repeat("x", 2<<20) + `"}]}`
	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/skills", "alice-token", huge)
	require.GreaterOrEqual(t, got.Status, 400)
}

func TestPRNumberTooLargeIsRejected(t *testing.T) {
	// The pattern matches digits, so a number wider than an int still reaches
	// the conversion. It must fail rather than wrap.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/evidence/prs", "alice-token",
		`{"prs":[{"position":1,"url":"https://github.com/a/b/pull/999999999999999999999999"}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeInvalidEvidence, got.errorCode(t))
}
