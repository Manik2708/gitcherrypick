package controller_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// --- /me ---------------------------------------------------------------------

func TestMeShapesDifferByAccountType(t *testing.T) {
	t.Run("contributor", func(t *testing.T) {
		h := newHarness(t)
		p := contributorPrincipal()
		expires := fixedTime().Add(15 * 24 * time.Hour)
		p.Contributor.Availability = &domain.Availability{
			Status: domain.Looking, ExpiresAt: &expires,
		}
		h.signIn("alice-token", p)
		h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(&p, nil)

		got := h.do(t, http.MethodGet, "/me", "alice-token", "")
		require.Equal(t, http.StatusOK, got.Status)

		var body struct {
			GitHubLogin   string `json:"github_login"`
			PrincipalType string `json:"principal_type"`
			Availability  struct {
				Status string `json:"status"`
			} `json:"availability"`
		}
		got.decode(t, &body)
		require.Equal(t, "aliceok", body.GitHubLogin)
		require.Equal(t, "contributor", body.PrincipalType)
		require.Equal(t, "looking", body.Availability.Status)
	})

	t.Run("hirer", func(t *testing.T) {
		// The key is `kind` here and `principal_type` above. That inconsistency
		// is in the approved fixtures and is reproduced rather than unified.
		h := newHarness(t)
		p := hirerPrincipal(false)
		h.signIn("hank-token", p)
		h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(&p, nil)

		got := h.do(t, http.MethodGet, "/me", "hank-token", "")
		require.Equal(t, http.StatusOK, got.Status)

		var body struct {
			Kind         string `json:"kind"`
			Verified     bool   `json:"verified"`
			Capabilities struct {
				CanSearch         bool `json:"can_search"`
				CanShortlist      bool `json:"can_shortlist"`
				CanViewScorecards bool `json:"can_view_scorecards"`
			} `json:"capabilities"`
		}
		got.decode(t, &body)
		require.Equal(t, "hirer", body.Kind)
		require.False(t, body.Verified)
		require.False(t, body.Capabilities.CanSearch)
		require.False(t, body.Capabilities.CanShortlist)
		require.False(t, body.Capabilities.CanViewScorecards)
	})
}

func TestCapabilityNeedsBothVerifications(t *testing.T) {
	// /me must report exactly what AccessService.RequireHiringCapability
	// enforces. The gate reads the ORGANIZATION and nothing else for a seat:
	// verifying an org lifts every seat under it, and a seat's own column is
	// never consulted, because it is stale for anyone created before the org
	// was approved (ADR-0002, ADR-0008 §3a).
	//
	// "org only" is the case that matters, and it used to be asserted the
	// wrong way round. A roster-redeemed seat and an onboarded owner both have
	// verified_at NULL — what was verified is the company — so a rule of AND
	// told them they could not search while /search answered 200 for the same
	// bearer token.
	now := fixedTime()
	cases := map[string]struct {
		hirer, org bool
		want       bool
	}{
		"neither":       {false, false, false},
		"hirer only":    {true, false, false},
		"org only":      {false, true, true},
		"both verified": {true, true, true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := hirerPrincipal(false)
			if tc.hirer {
				p.Hirer.VerifiedAt = &now
			}
			if tc.org {
				p.Hirer.Organization.VerifiedAt = &now
			}
			h.signIn("hank-token", p)
			h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(&p, nil)

			got := h.do(t, http.MethodGet, "/me", "hank-token", "")
			var body struct {
				Capabilities struct {
					CanSearch bool `json:"can_search"`
				} `json:"capabilities"`
			}
			got.decode(t, &body)
			require.Equal(t, tc.want, body.Capabilities.CanSearch)
		})
	}
}

func TestSetAvailability(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	expires := fixedTime().Add(15 * 24 * time.Hour)
	h.auth.EXPECT().SetAvailability(mock.Anything, domain.UserID(aliceID), domain.Looking).
		Return(&domain.Availability{Status: domain.Looking, ExpiresAt: &expires}, nil)

	got := h.do(t, http.MethodPut, "/me/availability", "alice-token",
		`{"status":"looking"}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Status    string     `json:"status"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	got.decode(t, &body)
	require.Equal(t, "looking", body.Status)
	require.NotNil(t, body.ExpiresAt)
}

func TestMyRankReportsNullsRatherThanZeros(t *testing.T) {
	// A contributor with no primary skill has not been measured, and a zero
	// would claim otherwise (ADR-0007).
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.discovery.EXPECT().MyRank(mock.Anything, domain.UserID(aliceID)).
		Return(&domain.Rank{RubricVersion: "v1", Ranked: false, Active: true}, nil)

	got := h.do(t, http.MethodGet, "/me/rank", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Ranked  bool `json:"ranked"`
		Overall struct {
			Score *float64 `json:"score"`
			Rank  *int     `json:"rank"`
		} `json:"overall"`
		Skills []any `json:"skills"`
	}
	got.decode(t, &body)
	require.False(t, body.Ranked)
	require.Nil(t, body.Overall.Score)
	require.Nil(t, body.Overall.Rank)
	require.NotNil(t, body.Skills, "an empty list serializes as [] rather than null")
}

func TestShareLinkMintAndRevoke(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	linkID := domain.ShareLinkID("01920000-0000-7000-8000-00000000e001")
	h.auth.EXPECT().MintShareLink(mock.Anything, domain.UserID(aliceID)).
		Return("share-token", linkID, nil)

	minted := h.do(t, http.MethodPost, "/me/share-link", "alice-token", "")
	require.Equal(t, http.StatusCreated, minted.Status)

	var body struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	minted.decode(t, &body)
	require.Equal(t, "share-token", body.Token, "the plaintext is shown once, here")
	require.Contains(t, body.URL, "share-token")

	h.auth.EXPECT().RevokeShareLink(mock.Anything, domain.UserID(aliceID), linkID).Return(nil)
	revoked := h.do(t, http.MethodDelete, "/me/share-link/"+string(linkID), "alice-token", "")
	require.Equal(t, http.StatusNoContent, revoked.Status)
}

func TestContactRequestResponses(t *testing.T) {
	for path, accept := range map[string]bool{"accept": true, "decline": false} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("alice-token", contributorPrincipal())

			contactID := domain.ContactID("01920000-0000-7000-8000-00000000f001")
			h.contacts.EXPECT().Respond(mock.Anything, domain.UserID(aliceID), contactID, accept).
				Return(&domain.ContactRequest{ID: contactID, Status: domain.ContactRequestStatus("accepted")}, nil)

			got := h.do(t, http.MethodPost,
				"/me/contact-requests/"+string(contactID)+"/"+path, "alice-token", "")
			require.Equal(t, http.StatusOK, got.Status)
		})
	}
}

func TestMyVerification(t *testing.T) {
	h := newHarness(t)
	p := hirerPrincipal(false)
	h.signIn("hank-token", p)

	submitted := fixedTime()
	h.orgs.EXPECT().Verification(mock.Anything, mock.Anything).Return(&port.VerificationStatus{
		Status: "pending", HirerVerified: false,
		Organization: &domain.Organization{ID: orgID, Name: "Unknown Ltd"},
		SubmittedAt:  submitted,
	}, nil)

	got := h.do(t, http.MethodGet, "/me/verification", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Status        string     `json:"status"`
		HirerVerified bool       `json:"hirer_verified"`
		ReviewedAt    *time.Time `json:"reviewed_at"`
		Reason        *string    `json:"reason"`
	}
	got.decode(t, &body)
	require.Equal(t, "pending", body.Status)
	require.False(t, body.HirerVerified)
	require.Nil(t, body.ReviewedAt)
	require.Nil(t, body.Reason)
}

func TestReevaluationStatus(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.reeval.EXPECT().Status(mock.Anything, domain.UserID(aliceID)).
		Return(&domain.DisputeStanding{ClaimsEligible: []domain.ClaimID{}}, nil)

	got := h.do(t, http.MethodGet, "/me/reevaluation-status", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		CanRequest bool `json:"can_request"`
		Tier       int  `json:"tier"`
	}
	got.decode(t, &body)
	require.True(t, body.CanRequest)
	require.Zero(t, body.Tier)
}

func TestMySkills(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.skills.EXPECT().MySkills(mock.Anything, domain.UserID(aliceID)).
		Return(&port.SkillStanding{
			Skills: []domain.UserSkill{{
				Slug: "go", Standing: domain.Primary, DistinctPRCount: 5, Score: 79.3,
			}},
			RubricVersion: "2026-01",
		}, nil)

	got := h.do(t, http.MethodGet, "/me/skills", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Skills []struct {
			Slug     string `json:"slug"`
			Standing string `json:"standing"`
		} `json:"skills"`
	}
	got.decode(t, &body)
	require.Len(t, body.Skills, 1)
	require.Equal(t, "primary", body.Skills[0].Standing)
}

// --- /claims -----------------------------------------------------------------

func TestPRURLParsing(t *testing.T) {
	// A contributor pastes what their browser shows, which often carries a
	// query string or an anchor.
	valid := map[string]string{
		"plain":          "https://github.com/acme/platform/pull/55",
		"trailing slash": "https://github.com/acme/platform/pull/55/",
		"query string":   "https://github.com/acme/platform/pull/55?diff=split",
		"anchor":         "https://github.com/acme/platform/pull/55#discussion_r1",
	}

	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("alice-token", contributorPrincipal())
			h.claims.EXPECT().SetPREvidence(mock.Anything, domain.UserID(aliceID),
				domain.ClaimID(claimID), mock.MatchedBy(func(prs []domain.PREvidence) bool {
					return len(prs) == 1 && prs[0].RepoOwner == "acme" &&
						prs[0].RepoName == "platform" && prs[0].PRNumber == 55 &&
						prs[0].Role == domain.RoleAuthor
				})).Return(&domain.Claim{
				PREvidence: []domain.PREvidence{{
					Position: 1, RepoOwner: "acme", RepoName: "platform",
					PRNumber: 55, Role: domain.RoleAuthor,
				}},
			}, nil)

			got := h.do(t, http.MethodPost, "/claims/"+claimID+"/evidence/prs", "alice-token",
				`{"prs":[{"position":1,"url":"`+raw+`"}]}`)
			require.Equal(t, http.StatusOK, got.Status, "body was %s", got.Body)
		})
	}
}

func TestMalformedPRURLsAreRecordedRatherThanRefused(t *testing.T) {
	// Accepting evidence does NOT validate it. A draft may hold anything and
	// the checks run at submit, so a URL that will not parse is stored with
	// the reason attached — which is what lets a contributor paste five and
	// then fix the ones the platform marked (ADR-0003).
	invalid := map[string]string{
		"an issue not a PR": "https://github.com/acme/platform/issues/55",
		"repo root":         "https://github.com/acme/platform",
		"no number":         "https://github.com/acme/platform/pull/",
		"not a url":         "acme/platform#55",
		"empty":             "",
		"number too wide":   "https://github.com/a/b/pull/999999999999999999999999",
	}

	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("alice-token", contributorPrincipal())

			malformed := domain.MalformedURL
			h.claims.EXPECT().SetPREvidence(mock.Anything, mock.Anything, mock.Anything,
				mock.MatchedBy(func(prs []domain.PREvidence) bool {
					return len(prs) == 1 && prs[0].InvalidReason != nil &&
						*prs[0].InvalidReason == domain.MalformedURL &&
						prs[0].RepoOwner == "" && prs[0].RawURL == raw
				})).Return(&domain.Claim{
				PREvidence: []domain.PREvidence{{
					Position: 1, Role: domain.RoleAuthor, InvalidReason: &malformed,
				}},
			}, nil)

			got := h.do(t, http.MethodPost, "/claims/"+claimID+"/evidence/prs", "alice-token",
				`{"prs":[{"position":1,"url":"`+raw+`"}]}`)
			require.Equal(t, http.StatusOK, got.Status)

			var body prEvidenceListResponse
			got.decode(t, &body)
			require.Len(t, body.PRs, 1)
			require.Equal(t, "malformed_url", body.PRs[0].InvalidReason)
			require.Nil(t, body.PRs[0].RepoOwner, "an unparseable URL has no owner")
			require.Nil(t, body.PRs[0].PRNumber)
		})
	}
}

// prEvidenceListResponse is what the PR-evidence write returns.
type prEvidenceListResponse struct {
	PRs []prEvidenceResponse `json:"prs"`
}

type prEvidenceResponse struct {
	Position      int     `json:"position"`
	RepoOwner     *string `json:"repo_owner"`
	PRNumber      *int    `json:"pr_number"`
	Role          string  `json:"role"`
	InvalidReason string  `json:"invalid_reason"`
}

func TestReviewerRoleIsExplicit(t *testing.T) {
	// Authorship is the default because it is the overwhelmingly common claim;
	// a reviewer says so (ADR-0003).
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().SetPREvidence(mock.Anything, mock.Anything, mock.Anything,
		mock.MatchedBy(func(prs []domain.PREvidence) bool {
			return prs[0].Role == domain.RoleReviewer
		})).Return(&domain.Claim{}, nil)

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/evidence/prs", "alice-token",
		`{"prs":[{"position":1,"url":"https://github.com/a/b/pull/1","role":"reviewer"}]}`)
	require.Equal(t, http.StatusOK, got.Status)
}

func TestCreateClaim(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().Create(mock.Anything, domain.UserID(aliceID)).
		Return(&domain.Claim{ID: claimID, Status: domain.ClaimStatus("draft"), Version: 1}, nil)

	got := h.do(t, http.MethodPost, "/claims", "alice-token", "")
	require.Equal(t, http.StatusCreated, got.Status)

	var body struct {
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	got.decode(t, &body)
	require.Equal(t, "draft", body.Status)
	require.Equal(t, 1, body.Version)
}

func TestSubmitIsAcceptedNotOK(t *testing.T) {
	// 202: the judgement is asynchronous, and a 200 would suggest the work
	// finished.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	submitted := fixedTime()
	h.claims.EXPECT().Submit(mock.Anything, domain.UserID(aliceID), domain.ClaimID(claimID)).
		Return(&domain.Claim{
			ID: claimID, Status: domain.ClaimStatus("queued"), Version: 1, SubmittedAt: &submitted,
		}, nil, nil)

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/submit", "alice-token", "")
	require.Equal(t, http.StatusAccepted, got.Status)

	var body struct {
		Status                     string `json:"status"`
		EstimatedResultWithinHours int    `json:"estimated_result_within_hours"`
	}
	got.decode(t, &body)
	require.Equal(t, "queued", body.Status)
	require.Equal(t, 24, body.EstimatedResultWithinHours)
}

func TestSubmitReportsEveryValidationFailure(t *testing.T) {
	// Not just the first: the contributor is doing curation work and a vague
	// rejection wastes it.
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, []port.ValidationFailure{
			{Position: 2, Reason: domain.EvidenceInvalidReason("not_merged"), Message: "not merged"},
			{Position: 4, Reason: domain.EvidenceInvalidReason("not_public"), Message: "private"},
		}, nil)

	got := h.do(t, http.MethodPost, "/claims/"+claimID+"/submit", "alice-token", "")
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)

	var body struct {
		Error string `json:"error"`
		Items []struct {
			Position int    `json:"position"`
			Reason   string `json:"reason"`
		} `json:"items"`
	}
	got.decode(t, &body)
	require.Equal(t, service.CodeInvalidEvidence, body.Error)
	require.Len(t, body.Items, 2)
	require.Equal(t, 2, body.Items[0].Position)
	require.Equal(t, 4, body.Items[1].Position)
}

func TestSuggestionDecisionKeepsItsOrigin(t *testing.T) {
	// Origin records how the skill ARRIVED; accepted_at records that the
	// contributor chose it. Overwriting the origin would erase the distinction
	// the scorecard depends on.
	skillID := domain.SkillID("01920000-0000-7000-8000-000000009001")

	for path, accept := range map[string]bool{"accept": true, "dismiss": false} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("alice-token", contributorPrincipal())
			h.claims.EXPECT().DecideSuggestion(mock.Anything, domain.UserID(aliceID),
				domain.ClaimID(claimID), skillID, accept).
				Return(&domain.SuggestionDecision{
					Skill: decidedSkill(accept),
				}, nil)

			got := h.do(t, http.MethodPost,
				"/claims/"+claimID+"/suggestions/"+string(skillID)+"/"+path, "alice-token", "")
			require.Equal(t, http.StatusOK, got.Status)

			var body map[string]any
			got.decode(t, &body)
			require.Equal(t, "ai_suggested", body["origin"])
			if accept {
				require.Contains(t, body, "accepted_at")
				require.NotContains(t, body, "dismissed_at")
				return
			}
			require.Contains(t, body, "dismissed_at")
			require.NotContains(t, body, "accepted_at")
		})
	}
}

func TestWithdrawPreviewNamesTheDemotions(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().WithdrawPreview(mock.Anything, mock.Anything, mock.Anything).
		Return([]port.Demotion{{Skill: domain.UserSkill{Slug: "go"}, DistinctPRCountAfter: 0}}, nil)

	got := h.do(t, http.MethodGet, "/claims/"+claimID+"/withdraw-preview", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Demotions []struct {
			Skill   string `json:"skill"`
			From    string `json:"from"`
			To      string `json:"to"`
			Message string `json:"message"`
		} `json:"demotions"`
	}
	got.decode(t, &body)
	require.Len(t, body.Demotions, 1)
	require.Equal(t, "go", body.Demotions[0].Skill)
	require.Equal(t, "primary", body.Demotions[0].From)
	require.Equal(t, "secondary", body.Demotions[0].To)
	require.Contains(t, body.Demotions[0].Message, "Go")
}

func TestListClaimsSummarises(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.claims.EXPECT().List(mock.Anything, domain.UserID(aliceID)).
		Return([]port.ClaimSummary{{
			ID: claimID, Status: domain.ClaimStatus("evaluated"), Version: 1,
			PRCount: 5, SkillCount: 1, NominatedPrimary: "go",
		}}, nil)

	got := h.do(t, http.MethodGet, "/claims", "alice-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body claimListResponse
	got.decode(t, &body)
	require.Equal(t, 1, body.Total)
	require.Equal(t, 5, body.Claims[0].PRCount)
	require.Equal(t, 1, body.Claims[0].SkillCount)
	require.Equal(t, "go", body.Claims[0].NominatedPrimary)
}

// --- discovery ---------------------------------------------------------------

func TestUnknownSearchFilterIsRejected(t *testing.T) {
	// Silently dropping `min_score` when the real name is `min_skill_score`
	// would return a wider result set than the hirer asked for, with nothing
	// in the response saying so.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	got := h.do(t, http.MethodGet, "/search?skills=go&min_score=50", "hank-token", "")
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeUnknownFilter, got.errorCode(t))
	h.discovery.AssertNotCalled(t, "Search", mock.Anything, mock.Anything, mock.Anything)
}

func TestSearchFilterParsing(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything,
		mock.MatchedBy(func(q domain.SearchQuery) bool {
			return len(q.Skills) == 2 && q.Skills[0] == "go" && q.Skills[1] == "kubernetes" &&
				q.MinSkillScore != nil && *q.MinSkillScore == 70 &&
				q.IncludeInactive && q.Page == 2 && q.PerPage == 50 &&
				len(q.OpenTo) == 2
		})).Return(&domain.SearchResults{Total: 0, RankedBy: "skill:go", Page: 2, PerPage: 50}, nil)

	got := h.do(t, http.MethodGet,
		"/search?skills=go,kubernetes&min_skill_score=70&include_inactive=true"+
			"&page=2&per_page=50&open_to=remote,freelance",
		"hank-token", "")
	require.Equal(t, http.StatusOK, got.Status, "body was %s", got.Body)
}

func TestSearchPaginationDefaults(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything,
		mock.MatchedBy(func(q domain.SearchQuery) bool {
			return q.Page == 1 && q.PerPage == 20
		})).Return(&domain.SearchResults{}, nil)

	// Nonsense values fall back rather than erroring: a negative page is a
	// client bug, not a reason to refuse a search.
	got := h.do(t, http.MethodGet, "/search?page=-3&per_page=abc", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)
}

func TestCapabilityRefusalNamesWhichVerificationIsMissing(t *testing.T) {
	// "Get verified" is useless advice to someone who is verified and whose
	// organization is not.
	h := newHarness(t)
	now := fixedTime()
	p := hirerPrincipal(false)
	p.Hirer.VerifiedAt = &now // the person is verified; the org is not

	h.signIn("hank-token", p)
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, service.Coded(service.ErrNotCapable, service.CodeHiringCapability, "org unverified"))

	got := h.do(t, http.MethodGet, "/search?skills=go", "hank-token", "")
	require.Equal(t, http.StatusForbidden, got.Status)

	var body struct {
		Error                string `json:"error"`
		HirerVerified        bool   `json:"hirer_verified"`
		OrganizationVerified bool   `json:"organization_verified"`
	}
	got.decode(t, &body)
	require.Equal(t, service.CodeHiringCapability, body.Error)
	require.True(t, body.HirerVerified)
	require.False(t, body.OrganizationVerified)
}

func TestSearchResultsCarryTheHiddenCount(t *testing.T) {
	// A hirer seeing 12 of 40 needs to know 28 were withheld by the
	// availability toggle rather than not existing (ADR-0008 §1a).
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything).
		Return(&domain.SearchResults{
			Total: 12, InactiveHidden: 28, RankedBy: "skill:go", Page: 1, PerPage: 20,
			Results: []domain.SearchResult{{
				Rank: 1, UserID: aliceID, DisplayName: "Alice", Active: true,
			}},
		}, nil)

	got := h.do(t, http.MethodGet, "/search?skills=go", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Total          int    `json:"total"`
		InactiveHidden int    `json:"inactive_hidden"`
		RankedBy       string `json:"ranked_by"`
		Results        []struct {
			Rank int `json:"rank"`
		} `json:"results"`
	}
	got.decode(t, &body)
	require.Equal(t, 12, body.Total)
	require.Equal(t, 28, body.InactiveHidden)
	require.Equal(t, "skill:go", body.RankedBy)
	require.Len(t, body.Results, 1)
}

func TestScorecardNotFoundNamesTheContributor(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Scorecard(mock.Anything, mock.Anything, domain.UserID(aliceID)).
		Return(nil, service.ErrNotFound)

	got := h.do(t, http.MethodGet, "/contributors/"+aliceID+"/scorecard", "hank-token", "")
	require.Equal(t, http.StatusNotFound, got.Status)
	require.Equal(t, service.CodeContributorNotFound, got.errorCode(t))
}

func TestLeaderboardIsHirerOnly(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodGet, "/leaderboard?kind=skill&skill=go", "alice-token", "")
	require.Equal(t, http.StatusForbidden, got.Status)
	require.Equal(t, service.CodeHirerRequired, got.errorCode(t))
}

func TestLeaderboard(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.discovery.EXPECT().Leaderboard(mock.Anything, mock.Anything,
		domain.LeaderboardKind("skill"), "go").
		Return(&domain.Leaderboard{
			Kind: "skill", RubricVersion: "v1",
			Skill:   &domain.Skill{Slug: "go", Name: "Go"},
			Entries: []domain.LeaderboardEntry{{Rank: 1, UserID: aliceID, Score: 79.3, Active: true}},
		}, nil)

	got := h.do(t, http.MethodGet, "/leaderboard?kind=skill&skill=go", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	// The skill is an OBJECT, not a slug: a board headed "go" would make a
	// client look the display name up again.
	var body struct {
		Kind  string `json:"kind"`
		Skill *struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"skill"`
		Entries []struct {
			Rank int `json:"rank"`
		} `json:"entries"`
	}
	got.decode(t, &body)
	require.Equal(t, "skill", body.Kind)
	require.NotNil(t, body.Skill)
	require.Equal(t, "go", body.Skill.Slug)
	require.Equal(t, "Go", body.Skill.Name)
	require.Len(t, body.Entries, 1)
}

func TestSavedSearchLifecycle(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	searchID := domain.SavedSearchID("01920000-0000-7000-8000-00000000a0f1")
	query := domain.SearchQuery{Skills: []string{"go"}}

	h.discovery.EXPECT().SaveSearch(mock.Anything, mock.Anything, "Go seniors", mock.Anything).
		Return(&domain.SavedSearch{ID: searchID, Name: "Go seniors", Filters: query}, nil)
	created := h.do(t, http.MethodPost, "/saved-searches", "hank-token",
		`{"name":"Go seniors","filters":{"skills":["go"]}}`)
	require.Equal(t, http.StatusCreated, created.Status)

	h.discovery.EXPECT().SavedSearches(mock.Anything, mock.Anything).
		Return([]domain.SavedSearch{{ID: searchID, Name: "Go seniors", Filters: query}}, nil)
	listed := h.do(t, http.MethodGet, "/saved-searches", "hank-token", "")
	require.Equal(t, http.StatusOK, listed.Status)

	// Replaying evaluates the filters AS THE CALLER: a saved search stores a
	// question, never an answer.
	h.discovery.EXPECT().ReplaySavedSearch(mock.Anything, mock.Anything, searchID).
		Return(&domain.SearchResults{Total: 3}, nil)
	replayed := h.do(t, http.MethodGet, "/saved-searches/"+string(searchID)+"/results", "hank-token", "")
	require.Equal(t, http.StatusOK, replayed.Status)

	h.discovery.EXPECT().DeleteSavedSearch(mock.Anything, mock.Anything, searchID).Return(nil)
	deleted := h.do(t, http.MethodDelete, "/saved-searches/"+string(searchID), "hank-token", "")
	require.Equal(t, http.StatusNoContent, deleted.Status)
}

// --- /shortlists -------------------------------------------------------------

func TestShortlistConfirmReportsWhatItSent(t *testing.T) {
	// A second confirm must be SEEN to have sent nothing, or a hirer is left
	// wondering whether they emailed everyone twice.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	shortlistID := domain.ShortlistID("01920000-0000-7000-8000-0000000c0001")
	h.shortlists.EXPECT().Confirm(mock.Anything, mock.Anything, shortlistID).
		Return(&port.ConfirmResult{
			Shortlist:       &domain.Shortlist{ID: shortlistID, Name: "Round 1"},
			Notified:        0,
			AlreadyNotified: 3,
		}, nil)

	got := h.do(t, http.MethodPost, "/shortlists/"+string(shortlistID)+"/confirm", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Notified        int `json:"notified"`
		AlreadyNotified int `json:"already_notified"`
	}
	got.decode(t, &body)
	require.Zero(t, body.Notified)
	require.Equal(t, 3, body.AlreadyNotified)
}

func TestNotifiedEntryIsNotRemovable(t *testing.T) {
	// Deleting it would destroy the record of a disclosure that happened
	// (ADR-0008 §3a). The rule lives on the REMOVAL, not on the read: the
	// round detail reports who is on the list and what they said, and a
	// `removable` flag there would be a second place for the same rule to
	// disagree with itself.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	shortlistID := domain.ShortlistID("01920000-0000-7000-8000-0000000c0001")
	notified := fixedTime()
	h.shortlists.EXPECT().RemoveEntry(mock.Anything, mock.Anything, shortlistID, domain.UserID(aliceID)).
		Return(service.Coded(service.ErrConflict, service.CodeEntryAlreadyNotified,
			"this contributor has been told; the entry stays").
			WithDetail(map[string]any{"notified_at": notified}))

	got := h.do(t, http.MethodDelete,
		"/shortlists/"+string(shortlistID)+"/entries/"+aliceID, "hank-token", "")
	require.Equal(t, http.StatusConflict, got.Status)
	require.Equal(t, "entry_already_notified", got.errorCode(t))
}

func TestAnotherOrganizationsShortlistIsNotFound(t *testing.T) {
	// A competitor must not be able to confirm that an id belongs to somebody.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	shortlistID := domain.ShortlistID("01920000-0000-7000-8000-0000000c0009")
	h.shortlists.EXPECT().Get(mock.Anything, mock.Anything, shortlistID).
		Return(nil, service.ErrNotFound)

	got := h.do(t, http.MethodGet, "/shortlists/"+string(shortlistID), "hank-token", "")
	require.Equal(t, http.StatusNotFound, got.Status)
	require.Equal(t, service.CodeShortlistNotFound, got.errorCode(t))
}

func TestAddEntryRejectsAMalformedTarget(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	shortlistID := domain.ShortlistID("01920000-0000-7000-8000-0000000c0001")
	got := h.do(t, http.MethodPost, "/shortlists/"+string(shortlistID)+"/entries", "hank-token",
		`{"user_id":"not-a-uuid","note":""}`)
	require.Equal(t, http.StatusBadRequest, got.Status)
	require.Equal(t, service.CodeInvalidID, got.errorCode(t))
}

func TestShortlistCreateAndClose(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	shortlistID := domain.ShortlistID("01920000-0000-7000-8000-0000000c0001")
	date := fixedTime().Add(30 * 24 * time.Hour)

	// The round names the job it is for (ADR-0019 §3).
	roleID := domain.RoleID("01920000-0000-7000-8000-0000000e0001")
	h.shortlists.EXPECT().Create(mock.Anything, mock.Anything, roleID, "Round 1", "Backend hires", date).
		Return(&domain.Shortlist{ID: shortlistID, RoleID: roleID, Name: "Round 1", Status: "draft"}, nil)
	created := h.do(t, http.MethodPost, "/shortlists", "hank-token",
		`{"role_id":"`+string(roleID)+`","name":"Round 1","description":"Backend hires",`+
			`"tentative_result_date":"`+date.Format(time.RFC3339)+`"}`)
	require.Equal(t, http.StatusCreated, created.Status)

	h.shortlists.EXPECT().Close(mock.Anything, mock.Anything, shortlistID).
		Return(&domain.Shortlist{ID: shortlistID, Status: "closed"}, nil)
	closed := h.do(t, http.MethodPost, "/shortlists/"+string(shortlistID)+"/close", "hank-token", "")
	require.Equal(t, http.StatusOK, closed.Status)
}

// --- /orgs -------------------------------------------------------------------

func TestRosterEntryCarriesNoToken(t *testing.T) {
	// A roster entry is an ALLOWLIST fact, not a credential. Returning a token
	// here would make being listed sufficient, which is exactly what ADR-0016
	// §3 separates: proving control of the address is the second step.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	// The role goes through EMPTY: defaulting it is a business rule, and the
	// service owns it. A controller that filled it in would be the second
	// place the default lived.
	h.orgs.EXPECT().AddToRoster(mock.Anything, mock.Anything, domain.OrganizationID(orgID),
		"new@acme.com", "newseat", domain.OrgRole("")).
		Return(&port.RosterEntry{
			ID:    domain.RosterEntryID("01920000-0000-7000-8000-00000000aa01"),
			Email: "new@acme.com", Username: "newseat", Role: domain.RoleMember,
			AddedBy: domain.HirerRef{
				ID: hankID, Username: "hank", DisplayName: "Hank Rivera", Active: true,
			},
			CreatedAt: fixedTime(),
		}, nil)

	got := h.do(t, http.MethodPost, "/orgs/"+orgID+"/roster", "hank-token",
		`{"email":"new@acme.com","username":"newseat"}`)
	require.Equal(t, http.StatusCreated, got.Status)
	require.NotContains(t, string(got.Body), "token")

	var body struct {
		Username   string     `json:"username"`
		Role       string     `json:"role"`
		RedeemedAt *time.Time `json:"redeemed_at"`
		AddedBy    struct {
			Username string `json:"username"`
			Active   bool   `json:"active"`
		} `json:"added_by"`
	}
	got.decode(t, &body)
	require.Equal(t, "newseat", body.Username)
	require.Equal(t, "member", body.Role, "member is the default role")
	require.Nil(t, body.RedeemedAt, "a fresh entry has not been redeemed")

	// ADR-0016 §9 is unconditional: an author is named wherever one appears,
	// and the roster is no exception.
	require.Equal(t, "hank", body.AddedBy.Username)
	require.True(t, body.AddedBy.Active)
}

func TestRosteringIntoAnotherOrganizationIsRefused(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.orgs.EXPECT().AddToRoster(mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(nil, service.ErrForbidden)

	got := h.do(t, http.MethodPost,
		"/orgs/01920000-0000-7000-8000-00000000b009/roster", "hank-token",
		`{"email":"x@y.com","username":"xy"}`)
	require.Equal(t, http.StatusForbidden, got.Status)
	require.Equal(t, service.CodeNotAnOrgMember, got.errorCode(t))
}

func TestATakenUsernameNamesNobody(t *testing.T) {
	// 409 and nothing else. A message saying where the name is held would turn
	// the roster endpoint into an oracle over other organizations' hiring
	// (ADR-0016 §0).
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	h.orgs.EXPECT().AddToRoster(mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, service.Coded(service.ErrConflict, service.CodeUsernameTaken, "taken"))

	got := h.do(t, http.MethodPost, "/orgs/"+orgID+"/roster", "hank-token",
		`{"email":"x@y.com","username":"hank"}`)
	require.Equal(t, http.StatusConflict, got.Status)
	require.Equal(t, service.CodeUsernameTaken, got.errorCode(t))

	var body errorResponse
	got.decode(t, &body)
	require.NotContains(t, body.Message, "Acme")
}

func TestRemovingAnEntryIsNoContent(t *testing.T) {
	// 204 with no body: the seat row SURVIVES removal — it authored shortlists
	// and the record of who was told — so a body saying "deleted" would lie
	// (ADR-0016 §5).
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))
	entryID := domain.RosterEntryID("01920000-0000-7000-8000-00000000aa01")
	h.orgs.EXPECT().RemoveFromRoster(mock.Anything, mock.Anything,
		domain.OrganizationID(orgID), entryID).Return(nil)

	got := h.do(t, http.MethodDelete,
		"/orgs/"+orgID+"/roster/"+string(entryID), "hank-token", "")
	require.Equal(t, http.StatusNoContent, got.Status)
	require.Empty(t, got.Body)
}

func TestSeatsListTheRevokedToo(t *testing.T) {
	// A departed hirer stays listed so the author of an old round can still be
	// resolved by name rather than by a bare id (ADR-0016 §5a).
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	gone := fixedTime()
	h.orgs.EXPECT().ListSeats(mock.Anything, mock.Anything, domain.OrganizationID(orgID)).
		Return([]domain.Hirer{
			{ID: hankID, Username: "hank", DisplayName: "Hank Rivera", OrgRole: domain.RoleOwner},
			{ID: domain.HirerID("01920000-0000-7000-8000-00000000aa02"),
				Username: "maya", DisplayName: "Maya Okafor", DisabledAt: &gone},
		}, nil)

	got := h.do(t, http.MethodGet, "/orgs/"+orgID+"/seats", "hank-token", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Seats []struct {
			Username string `json:"username"`
			Active   bool   `json:"active"`
		} `json:"seats"`
	}
	got.decode(t, &body)
	require.Len(t, body.Seats, 2)
	require.True(t, body.Seats[0].Active)
	require.False(t, body.Seats[1].Active, "a revoked seat is listed, marked inactive")
}

// --- redemption --------------------------------------------------------------

func TestRedemptionIsUnauthenticated(t *testing.T) {
	// The redeemer has no account yet; the token in the body is their only
	// credential, and the seat is created by spending it.
	h := newHarness(t)
	h.redemption.EXPECT().CompleteRedemption(mock.Anything, "proof-token", "New Seat", "pw").
		Return(&domain.Hirer{
			ID: hankID, DisplayName: "New Seat", Username: "newseat",
			Organization: &domain.Organization{ID: orgID, Name: "Acme"},
		}, tokenPair(), nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/complete", "",
		`{"token":"proof-token","display_name":"New Seat","password":"pw"}`)
	require.Equal(t, http.StatusCreated, got.Status)
}

func TestStartingRedemptionIsSilent(t *testing.T) {
	// 202 whether or not an address is rostered. A miss that answered
	// differently from a hit would be an oracle for who an organization is
	// hiring (ADR-0016 §3) — so the service returns nil either way and the
	// status cannot depend on which it was.
	h := newHarness(t)
	h.redemption.EXPECT().StartRedemption(mock.Anything, "acme", "stranger@nowhere.example").
		Return(nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/start", "",
		`{"org_slug":"acme","email":"stranger@nowhere.example"}`)
	require.Equal(t, http.StatusAccepted, got.Status)
	require.Empty(t, got.Body)
}

func TestResendIsSilentToo(t *testing.T) {
	h := newHarness(t)
	h.redemption.EXPECT().ResendVerification(mock.Anything, "nobody@nowhere.example").
		Return(nil)

	got := h.do(t, http.MethodPost, "/auth/hirer/verify/resend", "",
		`{"email":"nobody@nowhere.example"}`)
	require.Equal(t, http.StatusAccepted, got.Status)
}

func TestSpentProofIsGone(t *testing.T) {
	// A token that never existed is 401 and says no more. A token that WAS
	// valid and has been consumed is 410, because its holder is entitled to
	// know which happened: the remedy is to sign in, not to ask again.
	h := newHarness(t)
	h.redemption.EXPECT().CompleteRedemption(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, service.Coded(service.ErrInvalidCredentials,
			service.CodeVerificationSpent, "used"))

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/complete", "",
		`{"token":"spent","display_name":"x","password":"pw"}`)
	require.Equal(t, http.StatusGone, got.Status)
	require.Equal(t, service.CodeVerificationSpent, got.errorCode(t))
}

func TestUnknownProofIsUnauthorized(t *testing.T) {
	// NOT 404. A distinguishable absence would let anyone enumerate live
	// verification tokens by asking.
	h := newHarness(t)
	h.redemption.EXPECT().CompleteRedemption(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil, service.Coded(service.ErrInvalidCredentials,
			service.CodeVerificationRequired, "no such link"))

	got := h.do(t, http.MethodPost, "/auth/hirer/redeem/complete", "",
		`{"token":"nope","display_name":"x","password":"pw"}`)
	require.Equal(t, http.StatusUnauthorized, got.Status)
	require.Equal(t, service.CodeVerificationRequired, got.errorCode(t))
}

// --- /organizations ----------------------------------------------------------

func TestPublicOrganizationsAreNameAndSlugOnly(t *testing.T) {
	// The picker discloses who is approved to hire here, which ADR-0016 §3a
	// accepts. It must go no further: nothing about roster size, seat count or
	// payment status, which would say how much a company is hiring.
	h := newHarness(t)
	verifiedAt := fixedTime()
	h.redemption.EXPECT().ListOrganizations(mock.Anything).
		Return([]domain.Organization{{
			ID: orgID, Name: "Acme", Slug: "acme",
			VerifiedAt: &verifiedAt,
		}}, nil)

	got := h.do(t, http.MethodGet, "/organizations", "", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Organizations []map[string]any `json:"organizations"`
	}
	got.decode(t, &body)
	require.Len(t, body.Organizations, 1)
	require.Equal(t, "Acme", body.Organizations[0]["name"])
	require.Equal(t, "acme", body.Organizations[0]["slug"])
	require.Len(t, body.Organizations[0], 2, "name and slug and nothing else")
}

// --- /admin ------------------------------------------------------------------

func TestAdminDecisionRequiresARecognisedVerb(t *testing.T) {
	// Defaulting to "rejected" would let a client typo cost a contributor a
	// cooldown tier.
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	requestID := "01920000-0000-7000-8000-00000000ab01"
	got := h.do(t, http.MethodPost, "/admin/reevaluations/"+requestID+"/decide", "admin-token",
		`{"decision":"maybe","reason":"unsure"}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeInvalidState, got.errorCode(t))
	h.admin.AssertNotCalled(t, "DecideReevaluation",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRejectingAReevaluationRequiresAReason(t *testing.T) {
	// A rejection costs the contributor a cooldown tier, so it must say why.
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	requestID := "01920000-0000-7000-8000-00000000ab01"
	got := h.do(t, http.MethodPost, "/admin/reevaluations/"+requestID+"/decide", "admin-token",
		`{"decision":"rejected"}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeReasonRequired, got.errorCode(t))
}

func TestAcceptingAReevaluationNeedsNoReason(t *testing.T) {
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	requestID := domain.RequestID("01920000-0000-7000-8000-00000000ab01")
	h.admin.EXPECT().DecideReevaluation(mock.Anything, mock.Anything, requestID, true, "").
		Return(nil)

	got := h.do(t, http.MethodPost, "/admin/reevaluations/"+string(requestID)+"/decide",
		"admin-token", `{"decision":"approved"}`)
	require.Equal(t, http.StatusOK, got.Status)
}

func TestApprovingASkillRequestNamesTheEntry(t *testing.T) {
	// The catalogue is curated rather than crowd-sourced: an admin may create
	// an entry that is not what the contributor proposed (ADR-0003).
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	requestID := domain.RequestID("01920000-0000-7000-8000-00000000ac01")
	h.admin.EXPECT().DecideSkillRequest(mock.Anything, mock.Anything, requestID, true,
		mock.MatchedBy(func(s *domain.Skill) bool {
			return s != nil && s.Slug == "opentelemetry" && len(s.Aliases) == 1
		}), "").Return(&domain.Skill{Slug: "opentelemetry", Name: "OpenTelemetry"}, nil)

	got := h.do(t, http.MethodPost, "/admin/skill-requests/"+string(requestID)+"/decide",
		"admin-token",
		`{"decision":"approved","slug":"opentelemetry","name":"OpenTelemetry",
		  "category":"observability","aliases":["otel"]}`)
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		CreatedSkill struct {
			Slug string `json:"slug"`
		} `json:"created_skill"`
	}
	got.decode(t, &body)
	require.Equal(t, "opentelemetry", body.CreatedSkill.Slug)
}

func TestApprovingASkillRequestNeedsASlugAndName(t *testing.T) {
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	requestID := "01920000-0000-7000-8000-00000000ac01"
	got := h.do(t, http.MethodPost, "/admin/skill-requests/"+requestID+"/decide", "admin-token",
		`{"decision":"approved"}`)
	require.Equal(t, http.StatusUnprocessableEntity, got.Status)
	require.Equal(t, service.CodeUnknownSkill, got.errorCode(t))
}

func TestSweepIsAccepted(t *testing.T) {
	// Enqueue-only: a half-swept corpus mixes rubric versions, and a
	// leaderboard mixing them ranks people by which version judged them.
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())
	h.evaluation.EXPECT().Sweep(mock.Anything, mock.Anything, "v2", "weights changed").
		Return(&domain.RubricSweep{
			ID: "01920000-0000-7000-8000-0000000fa001", From: "v1", To: "v2",
			Reason: "weights changed", ClaimsEnqueued: 7,
		}, nil)

	got := h.do(t, http.MethodPost, "/admin/evaluations/sweep", "admin-token",
		`{"rubric_version":"v2","reason":"weights changed"}`)
	require.Equal(t, http.StatusAccepted, got.Status)

	var body sweptCorpus
	got.decode(t, &body)
	require.Equal(t, 7, body.ClaimsEnqueued)
	require.Equal(t, "v1", body.From)
	require.Equal(t, "v2", body.To)
}

// decidedSkill is the claim_skills row a decision writes: the stamp that
// closed it, on whichever side the contributor chose.
func decidedSkill(accept bool) domain.ClaimSkill {
	at := time.Now().UTC()
	skill := domain.ClaimSkill{Slug: "kubernetes", Origin: domain.AISuggested}
	if accept {
		skill.AcceptedAt = &at
	} else {
		skill.DismissedAt = &at
	}
	return skill
}

// sweptCorpus is the sweep response, as a client reads it.
type sweptCorpus struct {
	From           string `json:"from_rubric_version"`
	To             string `json:"to_rubric_version"`
	ClaimsEnqueued int    `json:"claims_enqueued"`
}

func TestAdminQueues(t *testing.T) {
	h := newHarness(t)
	h.signIn("admin-token", adminPrincipal())

	h.admin.EXPECT().PendingVerifications(mock.Anything, mock.Anything).
		Return([]port.VerificationRequest{{
			ID: "01920000-0000-7000-8000-00000000ad01", Status: "pending",
			CreatedAt: fixedTime(),
		}}, nil)
	verifications := h.do(t, http.MethodGet, "/admin/verifications", "admin-token", "")
	require.Equal(t, http.StatusOK, verifications.Status)

	h.admin.EXPECT().PendingSkillRequests(mock.Anything, mock.Anything, "").
		Return([]port.SkillRequest{{
			ID: "01920000-0000-7000-8000-00000000ac01", ProposedName: "WebAssembly",
			Status: "pending", CreatedAt: fixedTime(),
		}}, nil)
	skillRequests := h.do(t, http.MethodGet, "/admin/skill-requests", "admin-token", "")
	require.Equal(t, http.StatusOK, skillRequests.Status)

	h.admin.EXPECT().Reevaluations(mock.Anything, mock.Anything, mock.Anything).
		Return([]domain.ReevaluationRequest{{
			ID: "01920000-0000-7000-8000-00000000ab01", ClaimID: claimID,
			Status: domain.ReevaluationStatus("pending"), CreatedAt: fixedTime(),
		}}, nil)
	reevaluations := h.do(t, http.MethodGet, "/admin/reevaluations", "admin-token", "")
	require.Equal(t, http.StatusOK, reevaluations.Status)
}

// --- /skills and /public -----------------------------------------------------

func TestSkillSearchReportsHowItMatched(t *testing.T) {
	// A contributor searching "golang" and getting "Go" needs to see the alias
	// that bridged them, or the result looks like a mistake.
	h := newHarness(t)
	h.skills.EXPECT().Search(mock.Anything, "golang").Return([]port.SkillMatch{{
		Skill:        domain.Skill{Slug: "go", Name: "Go", Category: "language"},
		MatchedVia:   "alias",
		MatchedAlias: "golang",
	}}, nil)

	got := h.do(t, http.MethodGet, "/skills?q=golang", "", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		Results []struct {
			Slug         string `json:"slug"`
			MatchedVia   string `json:"matched_via"`
			MatchedAlias string `json:"matched_alias"`
		} `json:"results"`
	}
	got.decode(t, &body)
	require.Len(t, body.Results, 1)
	require.Equal(t, "go", body.Results[0].Slug)
	require.Equal(t, "alias", body.Results[0].MatchedVia)
	require.Equal(t, "golang", body.Results[0].MatchedAlias)
}

func TestSkillRequestIsContributorOnly(t *testing.T) {
	// The catalogue describes contribution; a hirer proposing entries would
	// let demand shape what counts as evidence.
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	got := h.do(t, http.MethodPost, "/skill-requests", "hank-token",
		`{"proposed_name":"WebAssembly","rationale":"why"}`)
	require.Equal(t, http.StatusForbidden, got.Status)
	require.Equal(t, service.CodeContributorRequired, got.errorCode(t))
}

func TestSkillRequestIsCreated(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())
	h.skills.EXPECT().RequestSkill(mock.Anything, domain.UserID(aliceID), "WebAssembly", "why").
		Return(domain.RequestID("01920000-0000-7000-8000-00000000ac02"), nil)

	got := h.do(t, http.MethodPost, "/skill-requests", "alice-token",
		`{"proposed_name":"WebAssembly","rationale":"why"}`)
	require.Equal(t, http.StatusCreated, got.Status)
}

func TestPublicScorecardOmitsIdentifiers(t *testing.T) {
	// A published link proves nothing about who is reading it, so it carries
	// presentation and not a handle to query the contributor by.
	h := newHarness(t)
	h.auth.EXPECT().PublicScorecard(mock.Anything, "share-token").
		Return(&domain.Scorecard{
			User: domain.SearchResult{
				UserID: aliceID, DisplayName: "Alice Okafor", GitHubLogin: "aliceok",
			},
			RubricVersion: "v1",
		}, nil)

	got := h.do(t, http.MethodGet, "/public/scorecard/share-token", "", "")
	require.Equal(t, http.StatusOK, got.Status)

	var body struct {
		User map[string]any `json:"user"`
		Rest map[string]any `json:"-"`
	}
	got.decode(t, &body)
	require.Equal(t, "Alice Okafor", body.User["display_name"])
	require.Equal(t, "aliceok", body.User["github_login"])
	require.NotContains(t, body.User, "id")
	require.NotContains(t, body.User, "rank")
}

func TestRevokedShareLinkIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	h := newHarness(t)
	h.auth.EXPECT().PublicScorecard(mock.Anything, "revoked").
		Return(nil, service.ErrNotFound)

	got := h.do(t, http.MethodGet, "/public/scorecard/revoked", "", "")
	require.Equal(t, http.StatusNotFound, got.Status)
	require.Equal(t, service.CodeScorecardNotFound, got.errorCode(t))
}

// claimListResponse is the list view: counts rather than the evidence itself.
type claimListResponse struct {
	Total  int                    `json:"total"`
	Claims []claimSummaryResponse `json:"claims"`
}

type claimSummaryResponse struct {
	PRCount          int    `json:"pr_count"`
	SkillCount       int    `json:"skill_count"`
	NominatedPrimary string `json:"nominated_primary"`
}

// An INDEPENDENT hirer has no organisation (ADR-0017 §1), so there is none to
// read capability through — their own approval is all there is.
func TestIndependentHirerCapability(t *testing.T) {
	now := fixedTime()
	for name, verified := range map[string]bool{"approved": true, "awaiting review": false} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := hirerPrincipal(false)
			p.Hirer.Organization = nil
			p.Hirer.OrganizationID = ""
			if verified {
				p.Hirer.VerifiedAt = &now
			}
			h.signIn("hank-token", p)
			h.auth.EXPECT().Me(mock.Anything, mock.Anything).Return(&p, nil)

			got := h.do(t, http.MethodGet, "/me", "hank-token", "")
			require.Equal(t, http.StatusOK, got.Status)

			var body struct {
				Verified     bool `json:"verified"`
				Capabilities struct {
					CanSearch bool `json:"can_search"`
				} `json:"capabilities"`
			}
			got.decode(t, &body)
			require.Equal(t, verified, body.Capabilities.CanSearch)
			require.Equal(t, verified, body.Verified)
		})
	}
}

// A hirer must not reach compensation, at all (ADR-0018 §2).
//
// Decision 2 is a NEGATIVE — "no hirer-facing response carries this" — and a
// negative is easy to believe and hard to be sure of. The e2e suite asserts the
// absence from every hirer-facing body; this asserts the other half, that the
// one endpoint which does return it refuses anybody but the person who typed it.
//
// Both halves are needed. A shape that omits the field today can gain it in a
// refactor; a gate that refuses the wrong principal cannot be widened by
// accident.
func TestCompensationIsContributorOnly(t *testing.T) {
	for name, p := range map[string]domain.Principal{
		"a hirer":  hirerPrincipal(true),
		"an admin": adminPrincipal(),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn("token", p)

			// mockery fails the test if the service is called at all, which is
			// the assertion: the refusal happens before anything reads a row.
			read := h.do(t, http.MethodGet, "/me/compensation", "token", "")
			require.Equal(t, http.StatusForbidden, read.Status)

			write := h.do(t, http.MethodPut, "/me/compensation", "token",
				`{"currency":"GBP","hourly_rate":4500,"yearly_amount":null}`)
			require.Equal(t, http.StatusForbidden, write.Status)
		})
	}
}

// The same gate on the profile, which carries the country and the self-reported
// years. Less sensitive than pay, and still nobody else's to read or write.
func TestProfileIsContributorOnly(t *testing.T) {
	h := newHarness(t)
	h.signIn("hank-token", hirerPrincipal(true))

	require.Equal(t, http.StatusForbidden,
		h.do(t, http.MethodGet, "/me/profile", "hank-token", "").Status)
	require.Equal(t, http.StatusForbidden,
		h.do(t, http.MethodPut, "/me/profile", "hank-token", `{"open_to_remote":true}`).Status)
}

// A field this endpoint does not have is refused, not ignored.
//
// Ignoring is worse than it sounds: a client that sent something and got a 200
// would believe it had saved, and discover otherwise only by reading back
// carefully. DisallowUnknownFields turns that into an answer.
func TestUnknownProfileFieldIsRefused(t *testing.T) {
	h := newHarness(t)
	h.signIn("alice-token", contributorPrincipal())

	got := h.do(t, http.MethodPut, "/me/profile", "alice-token",
		`{"open_to_remote":true,"open_to_internships":false,"open_to_onsite":false,
		  "open_to_contract":false,"current_country":"GB","office_yoe":6,
		  "first_pr_url":"","latest_pr_url":"","invented_field":40}`)
	// 400: the body could not be read. 422 is for one that parsed and then
	// failed a rule — the rest of the codebase draws the line there, and two
	// approved fixtures pin it.
	require.Equal(t, http.StatusBadRequest, got.Status,
		"a body naming a field nothing here has must be refused, not quietly ignored")
	require.Equal(t, service.CodeInvalidProfile, got.errorCode(t),
		"invalid_claim would tell a client its EVIDENCE was rejected")
}
