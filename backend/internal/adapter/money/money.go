// Package money answers what currencies exist (ADR-0018 amendment 3).
//
// The same shape as the country adapter next door, and for the same reason: an
// ISO 4217 code is a closed vocabulary two systems must agree on. A role's
// salary is compared against a contributor's expectation only when the codes
// match exactly (repository/postgres/opening.go), so a hirer who types "usd"
// or "US$" has written a salary that is compared against nothing and told
// nothing about it.
//
// A picker is how that is prevented at the point it happens, rather than by a
// validator that refuses a form afterwards.
package money

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
	// vendor has been chosen, and choosing one is this field and a flag.
	BaseURL    string
	HTTPClient *http.Client
}

// Client reads the currency list.
//
// It CACHES for the process lifetime. The ISO 4217 list changes about as often
// as the country list, and a picker on a form should not cost a round trip.
type Client struct {
	baseURL string
	http    *http.Client

	once   sync.Once
	cached []domain.Currency
	err    error
}

// New builds the money adapter.
func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Short, because a picker blocking on a slow third party is worse than
		// a picker that falls back. Nothing here is a security decision.
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{baseURL: cfg.BaseURL, http: httpClient}
}

var _ port.MoneyService = (*Client)(nil)

// currenciesResponse is the provider's shape.
type currenciesResponse struct {
	Currencies []currencyBody `json:"currencies"`
}

type currencyBody struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Currencies returns the picker's list.
//
// A failure is returned rather than swallowed, but callers are expected to
// FAIL OPEN on it: a role form with a currency field must still submit when
// the provider is down. The adapter's job is to say what happened.
func (c *Client) Currencies(ctx context.Context) ([]domain.Currency, error) {
	c.once.Do(func() { c.cached, c.err = c.fetch(ctx) })
	if c.err != nil {
		// The failure is cached too, but only until the next call. A provider
		// that was down at boot should not poison a long-running server.
		c.once = sync.Once{}
		return nil, c.err
	}
	return c.cached, nil
}

func (c *Client) fetch(ctx context.Context) ([]domain.Currency, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("no currency provider configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/currencies", nil)
	if err != nil {
		return nil, fmt.Errorf("building the currency request: %w", err)
	}
	req.Header.Set("accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the currency list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the currency provider answered %d", resp.StatusCode)
	}

	var body currenciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding the currency list: %w", err)
	}

	out := make([]domain.Currency, 0, len(body.Currencies))
	for _, currency := range body.Currencies {
		// A malformed code is skipped rather than failing the whole list. One
		// bad row from a provider should not empty a picker.
		if len(currency.Code) != 3 || currency.Name == "" {
			continue
		}
		out = append(out, domain.Currency{Code: currency.Code, Name: currency.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the currency provider returned no usable currencies")
	}
	return out, nil
}
