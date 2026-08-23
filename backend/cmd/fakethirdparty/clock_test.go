package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
)

// TestRealClockAdapterAgainstTheFake closes the ADR-0012 loop: the adapter
// cmd/api wires up, polling this server, moved by the request the harness makes
// for an ADVANCE_CLOCK step.
func TestRealClockAdapterAgainstTheFake(t *testing.T) {
	srv, _ := newRawServer(t)
	ctx := context.Background()

	c, err := clock.NewOffset(ctx, srv.URL+"/_clock", 5*time.Millisecond, srv.Client())
	require.NoError(t, err)
	t.Cleanup(c.Close)

	require.WithinDuration(t, time.Now(), c.Now(), time.Second)

	// What claims/seven_day_lock.json step 4 needs.
	resp := do(t, srv, http.MethodPost, "/_clock/advance", "", `{"duration":"168h"}`)
	require.Equal(t, http.StatusOK, resp.Status)

	var offset clockOffset
	resp.decode(t, &offset)
	require.InDelta(t, 604800, offset.OffsetSeconds, 0.001)

	require.Eventually(t, func() bool {
		return c.Now().After(time.Now().Add(6 * 24 * time.Hour))
	}, 2*time.Second, 5*time.Millisecond, "the API's clock must follow the harness")
}

func TestClockAdvanceAccumulates(t *testing.T) {
	srv, _ := newRawServer(t)

	do(t, srv, http.MethodPost, "/_clock/advance", "", `{"seconds":60}`)
	resp := do(t, srv, http.MethodPost, "/_clock/advance", "", `{"seconds":30}`)

	var offset clockOffset
	resp.decode(t, &offset)
	require.InDelta(t, 90, offset.OffsetSeconds, 0.001)
}

func TestClockOnlyMovesForward(t *testing.T) {
	// Running time backwards would let a fixture un-expire a lock it had
	// already passed, which no real system can do.
	srv, _ := newRawServer(t)

	require.Equal(t, http.StatusBadRequest,
		do(t, srv, http.MethodPost, "/_clock/advance", "", `{"seconds":-60}`).Status)
	require.Equal(t, http.StatusBadRequest,
		do(t, srv, http.MethodPost, "/_clock/advance", "", `{"duration":"-1h"}`).Status)

	resp := do(t, srv, http.MethodGet, "/_clock", "", "")
	var offset clockOffset
	resp.decode(t, &offset)
	require.Zero(t, offset.OffsetSeconds)
}

func TestClockAdvanceRejectsGarbage(t *testing.T) {
	srv, _ := newRawServer(t)

	require.Equal(t, http.StatusBadRequest,
		do(t, srv, http.MethodPost, "/_clock/advance", "", `{not json`).Status)
	require.Equal(t, http.StatusBadRequest,
		do(t, srv, http.MethodPost, "/_clock/advance", "", `{"duration":"a fortnight"}`).Status)
}

func TestLoadResetsTheClock(t *testing.T) {
	// A fixture inheriting the previous one's advanced clock would see locks
	// expire that it never waited out.
	srv, _ := newRawServer(t)

	do(t, srv, http.MethodPost, "/_clock/advance", "", `{"duration":"48h"}`)
	do(t, srv, http.MethodPost, "/_load", "", `{}`)

	resp := do(t, srv, http.MethodGet, "/_clock", "", "")
	var offset clockOffset
	resp.decode(t, &offset)
	require.Zero(t, offset.OffsetSeconds)
}

// TestClockSourceIsAbsentFromProduction pins the shape of the decision: with no
// --clock-url there is no poller and no request, so this server's existence
// cannot affect a deployed API.
func TestClockSourceIsAbsentFromProduction(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c, closer, err := clock.New(context.Background(), "", 0)
	require.NoError(t, err)
	t.Cleanup(closer)

	require.WithinDuration(t, time.Now(), c.Now(), time.Second)
	time.Sleep(30 * time.Millisecond)
	require.Zero(t, hits)
}
