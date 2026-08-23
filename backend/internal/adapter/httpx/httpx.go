// Package httpx is the shared HTTP plumbing behind every third-party adapter.
//
// It exists so the mapping from transport failures onto port sentinels is
// decided ONCE. Four adapters each deciding for themselves what a 502 means
// would eventually disagree, and the disagreement would show up as a claim
// rejected for a reason that was really an outage.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultTimeout bounds every request.
//
// http.Client's own default is no timeout at all, so a hung provider would hold
// a request open until the caller's context expired — or forever, if it had
// none.
const DefaultTimeout = 15 * time.Second

// maxDrain caps how much of an unread body is drained before closing.
//
// Draining returns the connection to the pool instead of dropping it, but an
// unbounded copy would let a hostile or broken server stream indefinitely into
// io.Discard.
const maxDrain = 1 << 20

// StatusMapper turns a non-2xx response into an error.
//
// Providers signal the same condition differently — GitHub reports a rate limit
// as 403 with a header, others as 429 — so each adapter supplies its own and
// falls back to MapStatus for the cases it does not special-case.
type StatusMapper func(resp *http.Response, what string) error

// Client is a JSON-speaking HTTP client with sentinel error mapping.
type Client struct {
	http   *http.Client
	mapper StatusMapper
}

// New wraps an http.Client. A nil client gets one with DefaultTimeout; a nil
// mapper gets MapStatus.
func New(client *http.Client, mapper StatusMapper) *Client {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	if mapper == nil {
		mapper = MapStatus
	}
	return &Client{http: client, mapper: mapper}
}

// Do sends req and decodes a JSON response into out, which may be nil.
func (c *Client) Do(req *http.Request, what string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// A refused connection, a DNS failure or a timeout means the provider
		// is unreachable NOW. It is never a statement about whether the thing
		// being asked for exists.
		return fmt.Errorf("%w: %s: %w", port.ErrUnavailable, what, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
		_ = resp.Body.Close()
	}()

	if err := c.mapper(resp, what); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", what, err)
	}
	return nil
}

// GetJSON issues an authenticated GET and decodes the result.
func (c *Client) GetJSON(ctx context.Context, url, bearer, what string, out any, headers http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", what, err)
	}
	applyHeaders(req, bearer, headers)
	return c.Do(req, what, out)
}

// PostJSON issues an authenticated POST with a JSON body.
func (c *Client) PostJSON(ctx context.Context, url, bearer, what string, body, out any, headers http.Header) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", what, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", what, err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyHeaders(req, bearer, headers)
	return c.Do(req, what, out)
}

// PostForm issues a form-encoded POST, which is what OAuth token endpoints take.
func (c *Client) PostForm(ctx context.Context, url, what string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", what, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.Do(req, what, out)
}

func applyHeaders(req *http.Request, bearer string, headers http.Header) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, values := range headers {
		for _, v := range values {
			req.Header.Set(k, v)
		}
	}
}

// MapStatus is the default status mapping.
//
// The split that matters is permanent versus transient. A 404 is a durable fact
// and may be recorded; a 429 or a 5xx is a fact about this minute and must not
// be, or evidence would be rejected because a provider had a bad afternoon.
func MapStatus(resp *http.Response, what string) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", port.ErrNotFound, what)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s: rate limited", port.ErrUnavailable, what)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s: upstream returned %d", port.ErrUnavailable, what, resp.StatusCode)
	default:
		return fmt.Errorf("%s: upstream returned %d", what, resp.StatusCode)
	}
}

// TrimBase normalises a configured base URL, falling back to a default.
func TrimBase(configured, fallback string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(configured), "/")
	if trimmed == "" {
		return strings.TrimSuffix(fallback, "/")
	}
	return trimmed
}
