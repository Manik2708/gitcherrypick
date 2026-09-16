// Package places answers what countries exist (ADR-0018 §9).
//
// One question, deliberately. A COUNTRY CODE is a closed vocabulary two
// systems must agree on — it is matched against a role's eligible countries,
// so "UK" and "GB" being different strings is a bug. A postcode is matched
// against nothing, is read by a person and posted to by a courier, and is not
// looked up at all.
package places

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Config points the adapter at a provider.
type Config struct {
	// BaseURL is the provider. In development and in the suite this is
	// cmd/fakethirdparty, which is the only provider currently wired: no live
	// vendor has been chosen (ADR-0018 §10).
	BaseURL    string
	HTTPClient *http.Client
}

// Client reads the country list.
//
// It CACHES for the process lifetime. The list changes a few times a decade,
// and a country picker should not cost a network round trip — least of all on
// the organisation onboarding form, which is the first thing a stranger sees.
type Client struct {
	baseURL string
	http    *http.Client

	once   sync.Once
	cached []domain.Country
	err    error
}

// New builds the place adapter.
func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Short, because a picker blocking on a slow third party is worse than
		// a picker that falls back. Nothing here is a security decision.
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: cfg.BaseURL, http: httpClient}
}

var _ port.PlaceService = (*Client)(nil)

// countriesResponse is the provider's shape.
type countriesResponse struct {
	Countries []countryBody `json:"countries"`
}

type countryBody struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Countries returns the picker's list.
//
// A failure is returned rather than swallowed, but callers are expected to
// FAIL OPEN on it: a form with a country field must still submit when the
// provider is down. The adapter's job is to say what happened; deciding that a
// signup should not depend on somebody else's uptime belongs above it.
func (c *Client) Countries(ctx context.Context) ([]domain.Country, error) {
	c.once.Do(func() { c.cached, c.err = c.fetch(ctx) })
	if c.err != nil {
		// The failure is cached too, but only until the next process. A
		// provider that was down at boot should not poison a long-running
		// server, so the once is reset on failure.
		c.once = sync.Once{}
		return nil, c.err
	}
	return c.cached, nil
}

func (c *Client) fetch(ctx context.Context) ([]domain.Country, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("no place provider configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/countries", nil)
	if err != nil {
		return nil, fmt.Errorf("building the country request: %w", err)
	}
	req.Header.Set("accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the country list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the place provider answered %d", resp.StatusCode)
	}

	var body countriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding the country list: %w", err)
	}

	out := make([]domain.Country, 0, len(body.Countries))
	for _, country := range body.Countries {
		// A malformed code is skipped rather than failing the whole list. One
		// bad row from a provider should not empty a picker.
		if len(country.Code) != 2 || country.Name == "" {
			continue
		}
		out = append(out, domain.Country{Code: country.Code, Name: country.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the place provider returned no usable countries")
	}
	return out, nil
}
