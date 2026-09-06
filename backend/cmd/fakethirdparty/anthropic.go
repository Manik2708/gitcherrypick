package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// The Anthropic Batch API's three steps, served from the fixture.
//
// This server holds no judgements of its own (ADR-0010). A fixture declares
// what the model says under `fake.ai`, and everything here does is wrap it in
// the envelope the real Batch API uses — submit returns a batch id, poll
// reports it ended, results returns the verdicts verbatim.
//
// The batch is reported as already ended on submit. Nothing in these fixtures
// asserts on the waiting, and a stand-in that made the suite sleep would be
// testing the poll loop's patience rather than the evaluator's arithmetic.

// batchCreated is the submit response.
type batchCreated struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processing_state"`
}

// batchID is the one batch this server tracks.
//
// Fixed rather than generated: one claim is judged at a time here, and a
// constant makes a failing request readable in a log.
const batchID = "batch_e2e"

func (s *Server) anthropicCreateBatch(w http.ResponseWriter, r *http.Request) {
	// The request body is read and discarded. What the model was SHOWN is the
	// evaluator's business; what it SAYS is the fixture's, and this server is
	// only the wire between them.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
	}
	writeJSON(w, http.StatusOK, batchCreated{ID: batchID, ProcessingState: "ended"})
}

func (s *Server) anthropicBatchStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, batchCreated{
		ID: chi.URLParam(r, "batchID"), ProcessingState: "ended",
	})
}

// anthropicBatchResults returns the fixture's judgements verbatim.
//
// A fixture that declared none gets an empty result rather than an invention,
// so a case that forgot to say what the model returns fails as a claim scored
// on nothing instead of passing on a default.
func (s *Server) anthropicBatchResults(w http.ResponseWriter, _ *http.Request) {
	ai := s.store.AI()
	if len(ai.Judgements) == 0 && len(ai.Suggestions) == 0 && !ai.Refused {
		writeJSON(w, http.StatusOK, map[string]any{"judgements": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, ai)
}
