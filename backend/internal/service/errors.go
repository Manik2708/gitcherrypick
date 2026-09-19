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

	// CodeUsernameTaken names nothing about who holds it or where. A message
	// that said "taken at Acme" would turn the roster endpoint into an oracle
	// over other organizations' hiring (ADR-0016 §0).
	CodeUsernameTaken = "username_taken"

	// CodeVerificationRequired means the address has not been proven yet.
	CodeVerificationRequired = "email_verification_required"

	// CodeVerificationSpent distinguishes a token already used from one that
	// never existed. The holder of a spent token is entitled to the first
	// answer; a bare refusal would send them to support.
	CodeVerificationSpent = "verification_already_used"

	// CodeVerificationExpired is separated from spent for the same reason:
	// the remedy is a resend, not a new roster entry.
	CodeVerificationExpired = "verification_expired"
	CodeEmailNotVerified    = "email_not_verified"
	CodeUnauthenticated     = "unauthenticated"
	CodeAuthRequired        = "authentication_required"

	// Authorization (403).
	CodeContributorRequired = "contributor_required"
	CodeHirerRequired       = "hirer_required"
	CodeAdminRequired       = "admin_required"
	CodeHiringCapability    = "hiring_capability_required"

	// CodeOrganizationRequired marks the gap ADR-0017 left between an
	// INDEPENDENT hirer and the organisation-scoped hiring model: shortlists,
	// saved searches and contact requests all have organization_id NOT NULL,
	// and such an account has none.
	//
	// Distinct from hiring_capability_required so the two are tellable apart:
	// that one means "you are not verified yet", which waiting fixes, and this
	// one means "this account cannot do this at all", which waiting does not.
	CodeOrganizationRequired = "organization_required"
	CodeVerificationNeeded   = "verification_required"
	CodeNotAnOrgMember       = "not_an_org_member"
	CodeCannotShortlistSelf  = "cannot_shortlist_self"
	CodeForbidden            = "forbidden"

	// Absence (404).
	CodeNotFound            = "not_found"
	CodeClaimNotFound       = "claim_not_found"
	CodeContributorNotFound = "contributor_not_found"
	CodeShortlistNotFound   = "shortlist_not_found"
	CodeSavedSearchNotFound = "saved_search_not_found"
	CodeScorecardNotFound   = "scorecard_not_found"
	CodeNoHirerAccount      = "no_hirer_account"

	// CodeRosterEntryNotFound is raised on an OWNER's path — removing an entry
	// that is not there. It is never raised on redemption, which stays silent
	// about whether an address is rostered (ADR-0016 §3).
	CodeRosterEntryNotFound = "roster_entry_not_found"

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

	// Gone (410). A proof that WAS valid and is no longer.
	//
	// Distinct from 401: the holder of a spent or expired link is entitled to
	// know which happened, because the remedies differ — sign in, or ask for
	// another link (ADR-0016 §4).

	// Unprocessable (422).
	CodeInvalidRegistration = "invalid_registration"
	CodeInvalidClaim        = "invalid_claim"

	// CodeInvalidProfile is a contributor's own profile, not a skill claim.
	// Reusing invalid_claim here would name the wrong thing: "claim" is a
	// specific noun on this platform, and a client branching on it would think
	// their evidence was rejected.
	CodeInvalidProfile = "invalid_profile"

	// CodeFirstPRIsFixed is a second, different first pull request. When
	// somebody started is a fact about the past (ADR-0018).
	CodeFirstPRIsFixed = "first_pr_is_fixed"

	// CodePRNotYours is a pull request GitHub says somebody else wrote.
	//
	// Checked BEFORE the URL is stored, because first_pr_url is write-once: a
	// stranger's 2011 pull request accepted here would make "years
	// contributing" permanently wrong and unfixable (ADR-0019 §7).
	CodePRNotYours = "pull_request_not_yours"

	// CodePRNotMerged is a pull request that exists but was never merged. An
	// unmerged contribution is not a contribution the platform counts.
	CodePRNotMerged = "pull_request_not_merged"

	// CodePRUnreachable is a pull request GitHub says nothing about — deleted,
	// private, or never there.
	CodePRUnreachable = "pull_request_unreachable"

	// CodeInvalidOpening is a public bar that cannot be met or cannot be read.
	CodeInvalidOpening = "invalid_opening"

	// CodeOpeningNotFound is a role nobody has advertised.
	CodeOpeningNotFound = "opening_not_found"

	// CodeInvalidRole is a role whose fields do not describe an opening.
	CodeInvalidRole = "invalid_role"

	// CodeRoleNotFound is an opening that is not this organisation's.
	CodeRoleNotFound = "role_not_found"

	// CodeRoleImmutable is an attempt to edit a role that has been opened.
	// A change writes a successor instead (ADR-0019 §13).
	CodeRoleImmutable = "role_is_open"

	// CodeOwnerApprovalRequired is a seat that may stage a change but not
	// commit it, under the organisation's authority setting (ADR-0019 §11).
	CodeOwnerApprovalRequired = "owner_approval_required"

	// CodeHireNotAccepted is somebody who never accepted a contact request
	// for this role.
	//
	// A company can only report hiring somebody who agreed to talk to it
	// (ADR-0019 §15). Staged, notified-but-unanswered and declined all fail,
	// and they fail IDENTICALLY — telling them apart would report a
	// contributor's answer to whoever asked about somebody else's hiring.
	CodeHireNotAccepted        = "hire_not_accepted"
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

// FilterProblem names one filter a search was refused for.
//
// A machine-readable sibling of the message: a client highlighting the field
// the recruiter typed wrong needs the field, not a sentence containing it.
type FilterProblem struct {
	Field  string `json:"field"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
	Max    *int   `json:"max,omitempty"`
}

// intPtr is the address of a literal, for the optional bounds above.
func intPtr(v int) *int { return &v }
