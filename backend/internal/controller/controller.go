// Package controller is the HTTP layer.
//
// A controller serializes, deserializes and validates IO. It holds no business
// logic (CLAUDE.md): it extracts a domain.Principal from the request context
// and hands it to a service, which decides access. A controller that decided
// access itself would put the rule in as many places as there are handlers, and
// the one place it was forgotten would be a hole.
package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// maxBodyBytes caps a request body.
//
// A claim carries five PRs with prose, so the limit is generous — but unbounded
// decoding lets one request exhaust memory, and http.MaxBytesReader is the only
// thing standing between a JSON decoder and whatever a client chooses to send.
const maxBodyBytes = 1 << 20

// errorBody is the failure shape, fixed by the approved fixtures.
//
// The code is what a client branches on. The message is optional prose for the
// few failures where a human needs to know what to do next — an unverified
// hirer is told a review is pending, because "403" alone would read as a bug.
//
// The message is written HERE rather than by the service, and never contains an
// id or a reason drawn from data. Those go to the server log, where they can
// name things without telling a caller what they are not entitled to know.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// messages is the client-facing prose for the codes that carry any.
//
// A map rather than a string at each raise site, so the same code cannot
// explain itself two different ways depending on which handler produced it.
var messages = map[string]string{
	service.CodeVerificationNeeded:  "Your hiring account is awaiting review. We aim to respond within 48 hours.",
	service.CodeNoHirerAccount:      "Register an organization account before signing in with Google.",
	service.CodeForbidden:           "Scorecards are visible to verified hiring accounts only.",
	service.CodeEmailNotVerified:    "Google has not verified this address.",
	service.CodeCannotShortlistSelf: "You cannot act on your own contributor account.",
}

// statusOverrides are the codes whose HTTP status does NOT follow from their
// sentinel.
//
// Most do: a conflict is 409, an absence is 404. These are the exceptions the
// fixtures pin — an invitation that was consumed is Gone rather than merely in
// conflict, and a cooldown is a rate limit rather than an invalid request.
var statusOverrides = map[string]int{
	service.CodeInvitationExpired:    http.StatusGone,
	service.CodeInvitationAccepted:   http.StatusGone,
	service.CodeRateLimited:          http.StatusTooManyRequests,
	service.CodeReevaluationCooldown: http.StatusTooManyRequests,
	service.CodeInvalidID:            http.StatusBadRequest,
	service.CodeInvalidState:         http.StatusBadRequest,
}

// sentinelStatus maps a service sentinel onto a status and a default code.
type sentinelStatus struct {
	sentinel error
	status   int
	code     string
}

// Order matters: the first match wins, so the more specific sentinels come
// before the general ones.
var sentinelStatuses = []sentinelStatus{
	{service.ErrInvalidCredentials, http.StatusUnauthorized, service.CodeInvalidCredentials},
	{service.ErrNotCapable, http.StatusForbidden, service.CodeHiringCapability},
	{service.ErrSelf, http.StatusForbidden, service.CodeCannotShortlistSelf},
	{service.ErrForbidden, http.StatusForbidden, service.CodeForbidden},
	{service.ErrNotFound, http.StatusNotFound, service.CodeNotFound},
	{service.ErrConflict, http.StatusConflict, service.CodeVersionConflict},
	{service.ErrInvalid, http.StatusUnprocessableEntity, service.CodeInvalidClaim},
}

// statusFor decides the status and wire code for a service error.
//
// The service names the reason and the controller decides the status, because
// only the service knows why a session was refused and only the controller
// knows that HTTP exists.
func statusFor(err error) (int, string) {
	code := service.CodeOf(err)

	for _, candidate := range sentinelStatuses {
		if !errors.Is(err, candidate.sentinel) {
			continue
		}
		if code == "" {
			code = candidate.code
		}
		if status, ok := statusOverrides[code]; ok {
			return status, code
		}
		return candidate.status, code
	}

	// A coded error with no recognised sentinel still knows its own status if
	// it is one of the overrides.
	if status, ok := statusOverrides[code]; ok {
		return status, code
	}

	// Anything else is ours, not the client's. The code is deliberately
	// generic: an internal failure must not describe itself to a caller.
	if code == "" {
		code = "internal_error"
	}
	return http.StatusInternalServerError, code
}

// writeJSON sends a value.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	// The header is already written, so an encoding failure cannot change the
	// status. It is logged by the caller's middleware, not reported here.
	_ = json.NewEncoder(w).Encode(body)
}

// writeError turns a service error into a response.
//
// A 5xx is LOGGED with its underlying error. The response deliberately says
// nothing — an internal failure must not describe itself to a caller — which
// means the log is the only place the cause exists. Without this, "500" is
// undiagnosable.
func writeError(w http.ResponseWriter, err error) {
	status, code := statusFor(err)
	if status >= http.StatusInternalServerError {
		slog.Error("request failed", "code", code, "err", err)
		// An internal failure describes itself to the log and to nobody else.
		writeCode(w, status, code)
		return
	}

	detail := service.DetailOf(err)
	if len(detail) == 0 {
		writeCode(w, status, code)
		return
	}

	// A client branching on which skill a proposal duplicated, or on when a
	// rate limit lifts, needs the value rather than the sentence.
	body := map[string]any{"error": code}
	if message := messages[code]; message != "" {
		body["message"] = message
	}
	for key, value := range detail {
		body[key] = value
	}
	writeDetail(w, status, body)
}

// writeCode sends an error code, with its standard message when it has one.
//
// Also used for failures a controller raises itself — a malformed body, an
// unparseable id — which never reached a service.
func writeCode(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorBody{Error: code, Message: messages[code]})
}

// writeDetail sends an error code alongside extra machine-readable fields.
//
// One case needs it: a hirer refused for capability is told WHICH of the two
// verifications is missing, because "get verified" is useless advice when the
// person is verified and their organization is not.
func writeDetail(w http.ResponseWriter, status int, body any) {
	writeJSON(w, status, body)
}

// decode reads a JSON request body.
//
// Unknown fields are REJECTED. A client sending `{"skil": "go"}` has a typo,
// and silently ignoring it would evaluate a claim against a skill nobody
// declared — which the client would only discover from the score.
func decode(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the request body is empty")
		}
		return fmt.Errorf("decoding the request body: %w", err)
	}
	return nil
}

// decodeOptional reads a body that may legitimately be absent, for endpoints
// whose parameters are all optional.
func decodeOptional(w http.ResponseWriter, r *http.Request, into any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return decode(w, r, into)
}

// uuidPattern matches the canonical 8-4-4-4-12 form.
//
// Path ids are validated before a service is called so a malformed one is 400
// rather than a database round trip that returns 404 — and so `not-a-uuid`
// never reaches SQL at all.
var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidPattern.MatchString(s) }

// isNotFound reports an absence, whatever wrapped it.
func isNotFound(err error) bool {
	return errors.Is(err, service.ErrNotFound) || errors.Is(err, port.ErrNotFound)
}

// isNotCapable reports the hiring-capability gate refusing (ADR-0002).
func isNotCapable(err error) bool { return errors.Is(err, service.ErrNotCapable) }
