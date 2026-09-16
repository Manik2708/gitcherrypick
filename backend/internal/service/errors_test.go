package service_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

func TestCodedErrorKeepsItsSentinel(t *testing.T) {
	// Every caller and test written before codes existed uses errors.Is. If
	// the sentinel stopped showing through, they would all silently take the
	// wrong branch rather than fail to compile.
	err := service.Coded(service.ErrConflict, service.CodeClaimLocked, "claim %s is locked", "c-1")

	require.ErrorIs(t, err, service.ErrConflict)
	require.NotErrorIs(t, err, service.ErrNotFound)
	require.Equal(t, service.CodeClaimLocked, service.CodeOf(err))
	require.Contains(t, err.Error(), "claim c-1 is locked")
}

func TestCodeSurvivesWrapping(t *testing.T) {
	// Services wrap with %w to add context on the way up. A code that only
	// survived at the top frame would be lost by the first caller that did.
	inner := service.Coded(service.ErrNotFound, service.CodeClaimNotFound, "no such claim")
	wrapped := fmt.Errorf("reading the claim: %w", inner)

	require.Equal(t, service.CodeClaimNotFound, service.CodeOf(wrapped))
	require.ErrorIs(t, wrapped, service.ErrNotFound)
}

func TestCodeOfPlainErrors(t *testing.T) {
	require.Empty(t, service.CodeOf(nil))
	require.Empty(t, service.CodeOf(errors.New("plain")))
	require.Empty(t, service.CodeOf(service.ErrConflict))
}

func TestCodedErrorWithoutASentinel(t *testing.T) {
	err := service.Coded(nil, service.CodeRateLimited, "slow down")
	require.Equal(t, "slow down", err.Error())
	require.Equal(t, service.CodeRateLimited, service.CodeOf(err))
}

func TestWireCodesAreDistinct(t *testing.T) {
	// Two constants sharing a value means one of them can never be produced,
	// and the fixture expecting it would fail with a diff of two nearly
	// identical strings.
	codes := []string{
		service.CodeInvalidCredentials, service.CodeInvalidRefreshToken,
		service.CodeTokenReuseDetected, service.CodeSessionRevoked,
		service.CodeOAuthExchangeFailed, service.CodeEmailNotVerified,
		service.CodeUnauthenticated, service.CodeAuthRequired,
		service.CodeContributorRequired, service.CodeHirerRequired,
		service.CodeAdminRequired, service.CodeHiringCapability,
		service.CodeVerificationNeeded, service.CodeNotAnOrgMember,
		service.CodeCannotShortlistSelf, service.CodeForbidden,
		service.CodeNotFound, service.CodeClaimNotFound,
		service.CodeContributorNotFound, service.CodeShortlistNotFound,
		service.CodeSavedSearchNotFound, service.CodeScorecardNotFound,
		service.CodeRosterEntryNotFound, service.CodeNoHirerAccount,
		service.CodeClaimLocked, service.CodeVersionConflict,
		service.CodeEvidenceUnchanged, service.CodeEvidencePairConflict,
		service.CodeAlreadyShortlisted, service.CodeEntryAlreadyNotified,
		service.CodeShortlistClosed, service.CodeConfirmationRequired,
		service.CodeSkillAlreadyExists, service.CodeAliasTaken, service.CodeSlugTaken,
		service.CodeRequestAlreadyDecided, service.CodeRequestAlreadyOpen,
		service.CodeSuggestionAlreadyDecided, service.CodeSweepInProgress,
		service.CodeVerificationRequired, service.CodeVerificationSpent,
		service.CodeVerificationExpired, service.CodeUsernameTaken,
		service.CodeInvalidClaim, service.CodeInvalidEvidence,
		service.CodeInvalidShortlist, service.CodeInvalidQuery,
		service.CodeInvalidFilter, service.CodeUnknownFilter, service.CodeUnknownSkill,
		service.CodeNotASuggestion, service.CodeProjectsNotAccepted,
		service.CodeReasonRequired, service.CodeUnknownRubricVersion,
		service.CodeRubricVersionUnchanged,
		service.CodeInvalidID, service.CodeInvalidState,
		service.CodeRateLimited, service.CodeReevaluationCooldown,
		service.CodeOrganizationRequired, service.CodeInvalidProfile,
		service.CodeFirstPRIsFixed,
	}

	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		require.NotEmpty(t, code)
		_, duplicate := seen[code]
		require.False(t, duplicate, "duplicate wire code %q", code)
		seen[code] = struct{}{}
	}
}
