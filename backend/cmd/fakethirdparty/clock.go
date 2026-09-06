package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// clockOffset is what the API's clock adapter polls.
type clockOffset struct {
	OffsetSeconds float64 `json:"offset_seconds"`
}

// advanceRequest moves time forward.
//
// Seconds and Duration are both accepted: a fixture reads better with "168h"
// than with 604800, and a harness computes seconds more easily. Whichever is
// set wins; Duration takes precedence when both are.
type advanceRequest struct {
	Seconds  float64 `json:"seconds"`
	Duration string  `json:"duration"`
}

// clockOffset serves the current offset.
//
// Polled rather than consulted per Now(): the API calls Now() on every request
// path, and an HTTP round trip there would change the latency of the thing
// under test (ADR-0012).
func (s *Server) clockRead(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, clockOffset{OffsetSeconds: s.store.Offset().Seconds()})
}

// clockAdvance moves the offset forward.
//
// Forward only. Time running backwards would let a fixture un-expire a lock it
// had already passed, which no real system can do and no fixture should be able
// to assert against.
func (s *Server) clockAdvance(w http.ResponseWriter, r *http.Request) {
	var req advanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decoding the advance request: %v", err), http.StatusBadRequest)
		return
	}

	by := time.Duration(req.Seconds * float64(time.Second))
	if req.Duration != "" {
		parsed, err := parseDuration(req.Duration)
		if err != nil {
			http.Error(w, fmt.Sprintf("unparseable duration %q: %v", req.Duration, err), http.StatusBadRequest)
			return
		}
		by = parsed
	}

	if by < 0 {
		http.Error(w, "the clock only moves forward", http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, clockOffset{OffsetSeconds: s.store.Advance(by).Seconds()})
}

// dayPrefix matches a leading day count, which time.ParseDuration does not
// accept.
//
// The windows this clock exists to cross are stated in days — a seven-day
// lock, a fifteen-day availability window, a fourteen-day invitation — so a
// fixture that had to write "360h" would be stating the same fact in units
// nobody reasons in.
var dayPrefix = regexp.MustCompile(`^(\d+)d`)

// parseDuration accepts Go durations, extended with a leading day count.
func parseDuration(s string) (time.Duration, error) {
	var days time.Duration
	if m := dayPrefix.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("unparseable day count %q: %w", m[1], err)
		}
		days = time.Duration(n) * 24 * time.Hour
		s = strings.TrimPrefix(s, m[0])
	}
	if s == "" {
		return days, nil
	}

	rest, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return days + rest, nil
}
