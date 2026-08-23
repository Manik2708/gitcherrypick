package service

import (
	"errors"
	"fmt"
)

// Coded errors.
//
// The sentinels in access.go say what KIND of failure happened — not found,
// conflict, forbidden — and a controller turns that into a status code. They do
// not say WHICH failure, and the approved fixtures pin more than fifty distinct
// codes: a replayed refresh token is `token_reuse_detected`, its collaterally
// revoked successor is `session_revoked`, and both are 401.
//
// A controller cannot recover that distinction. It knows the resource it asked
// about, so `claim_not_found` versus `shortlist_not_found` is fair game, but it
// cannot know why a session was refused. So the service names the reason and
// the controller decides the status.
//
// The sentinel survives underneath: errors.Is(err, ErrConflict) still answers
// correctly, because that is what every existing caller and test relies on.

// Error is a service failure with a stable wire code.
type Error struct {
	// Code is the value that reaches the client as {"error": "..."}. It is
	// part of the HTTP contract and is fixed by the fixtures — renaming one is
	// a breaking API change, not a refactor.
	Code string

	// Detail is machine-readable context the client acts on: which skill a
	// proposal duplicated, when a rate limit lifts, which of two verifications
	// is missing.
	//
	// Distinct from the message, which is prose. A client branches on Detail
	// and shows the message; parsing the message would make every wording
	// change a breaking one.
	Detail map[string]any

	kind    error
	message string
}

// WithDetail attaches machine-readable context.
func (e *Error) WithDetail(detail map[string]any) *Error {
	e.Detail = detail
	return e
}

// Coded builds an error carrying both a sentinel and a wire code.
//
// The message is for logs and humans; the code is for clients. Keeping them
// separate is what stops a client from parsing prose.
func Coded(kind error, code, format string, args ...any) *Error {
	return &Error{Code: code, kind: kind, message: fmt.Sprintf(format, args...)}
}

func (e *Error) Error() string {
	if e.kind == nil {
		return e.message
	}
	return e.message + ": " + e.kind.Error()
}

// Unwrap exposes the sentinel, so errors.Is keeps working for every caller
// written before codes existed.
func (e *Error) Unwrap() error { return e.kind }

// DetailOf returns the machine-readable context an error carries, if any.
//
// Walks the chain, like CodeOf, so context survives a caller wrapping with %w.
func DetailOf(err error) map[string]any {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Detail
	}
	return nil
}

// CodeOf returns the wire code carried by an error, or "" when it has none.
//
// Walks the chain, so a coded error stays recognisable after a caller wraps it
// with %w for context.
func CodeOf(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// Wire codes.
//
// Named constants rather than string literals at the raise site: a typo in a
// literal produces an error code no client handles, and nothing would catch it
// until a fixture failed with a diff of two nearly identical strings.
const (
	// Authentication (401).
	CodeInvalidCredentials  = "invalid_credentials"
	CodeInvalidRefreshToken = "invalid_refresh_token"
	CodeTokenReuseDetected  = "token_reuse_detected"
	CodeSessionRevoked      = "session_revoked"
	CodeOAuthExchangeFailed = "oauth_exchange_failed"
	CodeEmailNotVerified    = "email_not_verified"
	CodeUnauthenticated     = "unauthenticated"
	CodeAuthRequired        = "authentication_required"

	// Authorization (403).
	CodeContributorRequired = "contributor_required"
	CodeHirerRequired       = "hirer_required"
	CodeAdminRequired       = "admin_required"
	CodeHiringCapability    = "hiring_capability_required"
	CodeVerificationNeeded  = "verification_required"
	CodeNotAnOrgMember      = "not_an_org_member"
	CodeCannotShortlistSelf = "cannot_shortlist_self"
	CodeForbidden           = "forbidden"

	// Absence (404).
	CodeNotFound            = "not_found"
	CodeClaimNotFound       = "claim_not_found"
	CodeContributorNotFound = "contributor_not_found"
	CodeShortlistNotFound   = "shortlist_not_found"
	CodeSavedSearchNotFound = "saved_search_not_found"
	CodeScorecardNotFound   = "scorecard_not_found"
	CodeInvitationNotFound  = "invitation_not_found"
	CodeNoHirerAccount      = "no_hirer_account"

	// Conflict (409).
	CodeClaimLocked              = "claim_locked"
	CodeVersionConflict          = "version_conflict"
	CodeEvidenceUnchanged        = "evidence_unchanged"
	CodeEvidencePairConflict     = "evidence_pair_conflict"
	CodeAlreadyShortlisted       = "already_shortlisted"
	CodeEntryAlreadyNotified     = "entry_already_notified"
	CodeShortlistClosed          = "shortlist_closed"
	CodeConfirmationRequired     = "confirmation_required"
	CodeSkillAlreadyExists       = "skill_already_exists"
	CodeAliasTaken               = "alias_taken"
	CodeSlugTaken                = "slug_taken"
	CodeRequestAlreadyDecided    = "request_already_decided"
	CodeRequestAlreadyOpen       = "request_already_open"
	CodeSuggestionAlreadyDecided = "suggestion_already_decided"
	CodeSweepInProgress          = "sweep_in_progress"

	// Gone (410).
	CodeInvitationExpired  = "invitation_expired"
	CodeInvitationAccepted = "invitation_already_accepted"

	// Unprocessable (422).
	CodeInvalidClaim           = "invalid_claim"
	CodeInvalidEvidence        = "invalid_evidence"
	CodeInvalidShortlist       = "invalid_shortlist"
	CodeInvalidQuery           = "invalid_query"
	CodeInvalidFilter          = "invalid_filter"
	CodeUnknownFilter          = "unknown_filter"
	CodeUnknownSkill           = "unknown_skill"
	CodeNotASuggestion         = "not_a_suggestion"
	CodeProjectsNotAccepted    = "projects_not_accepted"
	CodeReasonRequired         = "reason_required"
	CodeUnknownRubricVersion   = "unknown_rubric_version"
	CodeRubricVersionUnchanged = "rubric_version_unchanged"

	// Bad request (400).
	CodeInvalidID    = "invalid_id"
	CodeInvalidState = "invalid_state"

	// Too many requests (429).
	CodeRateLimited          = "rate_limited"
	CodeReevaluationCooldown = "reevaluation_cooldown"
)
