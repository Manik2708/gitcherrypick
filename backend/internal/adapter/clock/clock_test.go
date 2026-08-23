package clock_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
)

func TestSystemClock(t *testing.T) {
	before := time.Now()
	got := clock.NewSystem().Now()
	after := time.Now()

	require.False(t, got.Before(before))
	require.False(t, got.After(after))
}

func TestNewWithoutAURLIsTheSystemClock(t *testing.T) {
	// Production. No poller, no HTTP, no behavioural difference from calling
	// time.Now() directly (ADR-0012).
	c, closer, err := clock.New(context.Background(), "", 0)
	require.NoError(t, err)
	require.NotNil(t, closer)
	t.Cleanup(closer)

	require.WithinDuration(t, time.Now(), c.Now(), time.Second)
}

// offsetSource is a stub clock source whose offset the test controls.
type offsetSource struct {
	seconds atomic.Int64
	hits    atomic.Int32
	status  atomic.Int32
	body    atomic.Value
}

func (s *offsetSource) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)

		if status := s.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			return
		}
		if body, ok := s.body.Load().(string); ok && body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offset_seconds":` + itoa(s.seconds.Load()) + `}`))
	})
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func newSource(t *testing.T) (*offsetSource, string) {
	t.Helper()
	src := &offsetSource{}
	srv := httptest.NewServer(src.handler())
	t.Cleanup(srv.Close)
	return src, srv.URL
}

func TestOffsetClockTracksItsSource(t *testing.T) {
	src, url := newSource(t)
	src.seconds.Store(0)

	c, err := clock.NewOffset(context.Background(), url, 10*time.Millisecond, nil)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	require.WithinDuration(t, time.Now(), c.Now(), time.Second)

	// Advance a week, which is what seven_day_lock.json needs.
	const week = 7 * 24 * 60 * 60
	src.seconds.Store(week)

	require.Eventually(t, func() bool {
		return c.Now().After(time.Now().Add(6 * 24 * time.Hour))
	}, 2*time.Second, 5*time.Millisecond)

	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), c.Now(), 2*time.Second)
}

func TestOffsetClockFailsFastOnAnUnreachableSource(t *testing.T) {
	// A flag that was passed and does not work is a misconfiguration. Finding
	// it at boot is far cheaper than finding it later from a timestamp that is
	// quietly wrong (ADR-0012).
	srv := httptest.NewServer(http.NewServeMux())
	closed := srv.URL
	srv.Close()

	_, err := clock.NewOffset(context.Background(), closed, time.Millisecond, nil)
	require.ErrorContains(t, err, "reading the clock source")

	_, _, err = clock.New(context.Background(), closed, time.Millisecond)
	require.Error(t, err, "New must propagate the failure rather than fall back to system time")
}

func TestOffsetClockRejectsAnUnusableSource(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		src, url := newSource(t)
		src.status.Store(http.StatusInternalServerError)

		_, err := clock.NewOffset(context.Background(), url, time.Millisecond, nil)
		require.ErrorContains(t, err, "500")
	})

	t.Run("unparseable body", func(t *testing.T) {
		src, url := newSource(t)
		src.body.Store("{not json")

		_, err := clock.NewOffset(context.Background(), url, time.Millisecond, nil)
		require.ErrorContains(t, err, "decoding")
	})
}

func TestOffsetClockKeepsTheLastKnownOffsetWhenTheSourceFails(t *testing.T) {
	// A harness blip must not take the API down, and must certainly not move
	// time backwards in the middle of a fixture.
	src, url := newSource(t)
	src.seconds.Store(3600)

	c, err := clock.NewOffset(context.Background(), url, 5*time.Millisecond, nil)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	require.WithinDuration(t, time.Now().Add(time.Hour), c.Now(), 2*time.Second)

	src.status.Store(http.StatusBadGateway)
	before := src.hits.Load()
	require.Eventually(t, func() bool { return src.hits.Load() > before+2 },
		2*time.Second, 5*time.Millisecond)

	require.WithinDuration(t, time.Now().Add(time.Hour), c.Now(), 2*time.Second,
		"the offset must survive a failing source")
}

func TestCloseStopsPollingAndIsIdempotent(t *testing.T) {
	src, url := newSource(t)

	c, err := clock.NewOffset(context.Background(), url, 5*time.Millisecond, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return src.hits.Load() > 2 },
		2*time.Second, 5*time.Millisecond)

	c.Close()
	c.Close() // must not panic on a second call

	settled := src.hits.Load()
	time.Sleep(50 * time.Millisecond)
	require.LessOrEqual(t, src.hits.Load(), settled+1, "polling must stop after Close")
}
