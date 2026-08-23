package main

import (
	"encoding/json"
	"net/http"
)

// resendRequest is Resend's send payload.
type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// resendAccepted is the success body. The id is what the adapter checks for,
// so a 2xx without one is treated as having sent nothing.
type resendAccepted struct {
	ID string `json:"id"`
}

// resendRejected is Resend's 422 body.
type resendRejected struct {
	StatusCode int    `json:"statusCode"`
	Name       string `json:"name"`
	Message    string `json:"message"`
}

// resendSend records a message instead of delivering one.
//
// Nothing is ever sent. This route existing at all is what keeps the e2e suite
// from emailing real people while exercising the real notifier — which is the
// point of redirecting rather than substituting (ADR-0010).
func (s *Server) resendSend(w http.ResponseWriter, r *http.Request) {
	var req resendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, resendRejected{
			StatusCode: http.StatusBadRequest,
			Name:       "validation_error",
			Message:    "could not decode the request body",
		})
		return
	}

	// Resend's own validation. Mimicked because a fixture asserting that a
	// malformed address is a PERMANENT failure needs the real 422, not a
	// success this server invented.
	if len(req.To) == 0 || req.To[0] == "" {
		writeJSON(w, http.StatusUnprocessableEntity, resendRejected{
			StatusCode: http.StatusUnprocessableEntity,
			Name:       "validation_error",
			Message:    "Invalid `to` field. Please use a valid email address.",
		})
		return
	}

	// The two shapes are identical field-for-field, and a conversion keeps them
	// that way: adding a field to one without the other stops compiling.
	s.store.Record(SentEmail(req))

	writeJSON(w, http.StatusOK, resendAccepted{
		ID: "fake-resend-" + req.To[0],
	})
}
