// Package resend implements port.Notifier against Resend's send API.
//
// Every notification the platform sends goes through here, and every one of
// them is called AFTER a transaction commits (port.Notifier): an email cannot
// be rolled back, so sending inside a transaction that then fails would tell a
// contributor about interest that never existed.
package resend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/httpx"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultBaseURL is Resend's API. ADR-0010 makes it configurable.
const DefaultBaseURL = "https://api.resend.com"

// Config is everything the notifier needs.
type Config struct {
	BaseURL string

	// APIKey authenticates the sender. Sending fails without it rather than
	// silently succeeding, so a misconfigured deployment is discovered on the
	// first notification instead of by a contributor who never heard from us.
	APIKey string

	// From is the envelope sender, e.g. "GitCherryPick <no-reply@example.com>".
	// Resend rejects a domain the account has not verified.
	From string

	HTTPClient *http.Client

	// Renderer turns a notification into a subject and body. Injected because
	// template content is a product decision, not a transport one.
	Renderer Renderer
}

// Renderer produces the human-visible part of a notification.
type Renderer interface {
	Render(n port.Notification) (subject, html string, err error)
}

// sendRequest is Resend's send payload.
type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// sendResponse carries the provider's id for the accepted message, which is
// what makes a delivery traceable later.
type sendResponse struct {
	ID string `json:"id"`
}

// errorResponse is Resend's failure body.
type errorResponse struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Notifier implements port.Notifier.
type Notifier struct {
	base string
	cfg  Config
	http *httpx.Client
}

// New builds the notifier, defaulting to the real Resend host.
func New(cfg Config) *Notifier {
	return &Notifier{
		base: httpx.TrimBase(cfg.BaseURL, DefaultBaseURL),
		cfg:  cfg,
		http: httpx.New(cfg.HTTPClient, mapStatus),
	}
}

var _ port.Notifier = (*Notifier)(nil)

// Send delivers one notification.
//
// A failure is returned rather than swallowed, but callers deliberately ignore
// it for contact requests: the disclosure already happened in the database and
// the contributor sees it on their dashboard whether or not the email lands
// (ADR-0008). Retrying here would risk sending twice, which is worse.
func (n *Notifier) Send(ctx context.Context, notification port.Notification) error {
	if n.cfg.APIKey == "" {
		return errors.New("resend is not configured: pass --resend-api-key")
	}
	if notification.Recipient == "" {
		return fmt.Errorf("notification %s has no recipient", notification.Kind)
	}
	if n.cfg.Renderer == nil {
		return errors.New("no notification renderer is configured")
	}

	subject, html, err := n.cfg.Renderer.Render(notification)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", notification.Kind, err)
	}

	var out sendResponse
	body := sendRequest{
		From:    n.cfg.From,
		To:      []string{notification.Recipient},
		Subject: subject,
		HTML:    html,
	}
	what := fmt.Sprintf("sending %s", notification.Kind)
	if err := n.http.PostJSON(ctx, n.base+"/emails", n.cfg.APIKey, what, body, &out, nil); err != nil {
		return err
	}
	if out.ID == "" {
		// A 2xx with no id means Resend accepted nothing. Treating it as
		// success would lose a notification silently.
		return fmt.Errorf("%s: resend returned no message id", what)
	}
	return nil
}

// mapStatus is Resend's status mapping, used in place of the httpx default.
//
// Resend reports a validation failure as 422 with a machine-readable name. That
// is PERMANENT — a malformed address, or a sending domain the account has not
// verified — so it must not become port.ErrUnavailable and be retried forever.
func mapStatus(resp *http.Response, what string) error {
	if resp.StatusCode == http.StatusUnprocessableEntity {
		var body errorResponse
		// Best effort: the reason is useful in a log, but its absence must not
		// turn a clear rejection into a decoding error.
		_ = json.NewDecoder(resp.Body).Decode(&body)

		reason := strings.TrimSpace(body.Name + " " + body.Message)
		if reason == "" {
			reason = "no reason given"
		}
		return fmt.Errorf("%s: resend rejected the message: %s", what, reason)
	}
	return httpx.MapStatus(resp, what)
}
