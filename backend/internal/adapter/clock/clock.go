// Package clock implements port.Clock.
//
// Two implementations. System is what production runs: time.Now(), no
// allocation and no indirection. Offset adds a value fetched from a configured
// URL, which is how the integration suite moves time forward (ADR-0012).
//
// The clock is a REDIRECTED dependency, the same shape as the third-party base
// URLs (ADR-0010): with no --clock-url there is no poll, no HTTP and no
// behavioural difference from calling time.Now() directly.
package clock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultPoll is how often the offset is refreshed.
//
// Up to one interval of staleness, which no fixture can observe: the shortest
// duration any of them turns on is the seven-day claim lock.
const DefaultPoll = 50 * time.Millisecond

// System is the production clock.
type System struct{}

// NewSystem returns the real clock.
func NewSystem() System { return System{} }

var _ port.Clock = System{}

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }

// offsetResponse is what a clock source returns.
type offsetResponse struct {
	OffsetSeconds float64 `json:"offset_seconds"`
}

// Offset is system time plus a value polled from a clock source.
//
// It exists because seeding cannot reach state the API creates DURING a
// fixture — the seven-day lock is written by the submit call and must be past
// a few steps later, so there is no row to seed (ADR-0012).
type Offset struct {
	url    string
	poll   time.Duration
	client *http.Client

	// Nanoseconds, read on every Now(). Atomic rather than mutex-guarded
	// because Now() is on every request path and several times per scoring
	// pass; a lock there would serialise handlers on the clock.
	offset atomic.Int64

	stop     chan struct{}
	stopOnce sync.Once
}

var _ port.Clock = (*Offset)(nil)

// NewOffset builds a polling clock and takes its FIRST reading synchronously.
//
// A failure here is fatal to startup, matching ADR-0011's treatment of the
// signing key: a flag that was passed and does not work is a misconfiguration,
// and finding it at boot is far cheaper than finding it later from a timestamp
// that is quietly wrong.
func NewOffset(ctx context.Context, url string, poll time.Duration, client *http.Client) (*Offset, error) {
	if poll <= 0 {
		poll = DefaultPoll
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}

	c := &Offset{url: url, poll: poll, client: client, stop: make(chan struct{})}

	offset, err := c.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the clock source at %s: %w", url, err)
	}
	c.offset.Store(int64(offset))

	go c.run()
	return c, nil
}

// Now returns system time plus the last known offset.
func (c *Offset) Now() time.Time {
	return time.Now().Add(time.Duration(c.offset.Load()))
}

// Close stops polling. Safe to call more than once.
func (c *Offset) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
}

// run refreshes the offset until Close.
//
// A failed refresh keeps the LAST KNOWN offset rather than resetting or
// exiting: a harness blip must not take the API down, and must certainly not
// silently move time backwards mid-fixture.
func (c *Offset) run() {
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), c.poll+time.Second)
			offset, err := c.fetch(ctx)
			cancel()
			if err != nil {
				continue
			}
			c.offset.Store(int64(offset))
		}
	}
}

func (c *Offset) fetch(ctx context.Context) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return 0, fmt.Errorf("building the request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("clock source returned %d", resp.StatusCode)
	}

	var body offsetResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decoding the offset: %w", err)
	}
	return time.Duration(body.OffsetSeconds * float64(time.Second)), nil
}

// New picks an implementation from configuration.
//
// An empty url means production: the System clock, with nothing running in the
// background. Returned as port.Clock plus a closer, so a caller need not know
// which one it got.
func New(ctx context.Context, url string, poll time.Duration) (port.Clock, func(), error) {
	if url == "" {
		return NewSystem(), func() {}, nil
	}

	offset, err := NewOffset(ctx, url, poll, nil)
	if err != nil {
		return nil, nil, err
	}
	return offset, offset.Close, nil
}
