// Package github implements port.GitHubClient against GitHub's REST API.
//
// Every host it talks to is configurable (ADR-0010). The defaults are the real
// ones; the integration suite points them at cmd/fakethirdparty instead, and
// the SAME client runs in both — so the decoding, error mapping and pagination
// below are exercised on every e2e run rather than first meeting reality in
// production.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/httpx"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Real GitHub. Two hosts, because OAuth lives on the web host and everything
// else on the API host.
const (
	DefaultAPIBaseURL   = "https://api.github.com"
	DefaultOAuthBaseURL = "https://github.com"
)

// Scopes requested at authorization.
//
// read:user ONLY. The platform reads public contribution facts and never
// writes, and asking for more than it needs is asking a contributor to trust it
// with more than it needs.
//
// read:user already includes the account's email addresses, so /user/emails
// resolves without user:email — which would additionally grant the ability to
// CHANGE them.
// scopes is what the consent screen asks for.
//
// This was "read:user" alone, on the reasoning that read:user already covers
// the email list and that adding user:email would also grant the ability to
// CHANGE an address. Both halves of that are wrong, and live sign-in fails
// because of it:
//
//	GitHub gates "List email addresses for the authenticated user" behind
//	user:email specifically; read:user does not reach it. So primaryEmail gets
//	403, CompleteGitHub fails, and the contributor sees a refused sign-in.
//
//	user:email is READ access to addresses. Changing them needs the broader
//	"user" scope, which is not requested here and should not be.
//
// The stand-in cannot catch this: it returns an email list whatever the token
// carries, so the whole flow passes locally and only fails against github.com.
//
// Still read-only, and still no repository access — the pull requests the
// platform judges are public, and are read with the service's own token rather
// than the contributor's.
const scopes = "read:user user:email"

// apiVersion pins the REST API's dated contract, so a server-side default
// moving does not silently change what this client receives.
const apiVersion = "2022-11-28"

// Config is everything the client needs. Base URLs come from cobra flags.
type Config struct {
	APIBaseURL   string
	OAuthBaseURL string

	// ClientID and ClientSecret identify the OAuth app. Sign-in fails without
	// them; the fact-fetching methods do not use them.
	ClientID     string
	ClientSecret string

	// Token authenticates fact fetches. Unauthenticated GitHub allows 60
	// requests an hour, which one claim submission can exhaust.
	Token string

	HTTPClient *http.Client
}

// Client implements port.GitHubClient.
type Client struct {
	api   string
	oauth string
	cfg   Config
	http  *httpx.Client
}

// New builds the GitHub client, filling in the real hosts when none are given.
func New(cfg Config) *Client {
	return &Client{
		api:   httpx.TrimBase(cfg.APIBaseURL, DefaultAPIBaseURL),
		oauth: httpx.TrimBase(cfg.OAuthBaseURL, DefaultOAuthBaseURL),
		cfg:   cfg,
		http:  httpx.New(cfg.HTTPClient, mapStatus),
	}
}

var _ port.GitHubClient = (*Client)(nil)

// apiHeaders are sent on every REST call.
func apiHeaders() http.Header {
	return http.Header{
		"Accept":               []string{"application/vnd.github+json"},
		"X-GitHub-Api-Version": []string{apiVersion},
	}
}

// get issues a GET authenticated as the PLATFORM.
func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.http.GetJSON(ctx, c.api+path, c.cfg.Token, path, out, apiHeaders())
}

// getAs issues a GET authenticated as the CONTRIBUTOR, for the two endpoints
// that read their own account.
func (c *Client) getAs(ctx context.Context, path, token string, out any) error {
	return c.http.GetJSON(ctx, c.api+path, token, path, out, apiHeaders())
}

// mapStatus is GitHub's status mapping, layered over the httpx default.
//
// GitHub is the one provider that reports a rate limit as 403, so the header
// is what separates "slow down" from "you may not see this". Getting that
// backwards either retries a permission failure forever or records an outage as
// a permanent fact about a contributor's claim.
func mapStatus(resp *http.Response, what string) error {
	if resp.StatusCode == http.StatusForbidden {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("%w: %s: rate limited until %s",
				port.ErrUnavailable, what, resetTime(resp))
		}
		// A plain 403 is a private repository or a revoked token. Retrying
		// would not help.
		return fmt.Errorf("%s: forbidden", what)
	}
	return httpx.MapStatus(resp, what)
}

func resetTime(resp *http.Response) string {
	raw := resp.Header.Get("X-RateLimit-Reset")
	if raw == "" {
		return "an unknown time"
	}
	var unix int64
	if _, err := fmt.Sscanf(raw, "%d", &unix); err != nil {
		return "an unknown time"
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// AuthorizeURL builds the redirect a contributor is sent to.
//
// The adapter owns the client id and the scopes, so a service never holds
// configuration that belongs here (port.GitHubClient).
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id": {c.cfg.ClientID},
		"scope":     {scopes},
		"state":     {state},
	}
	return c.oauth + "/login/oauth/authorize?" + q.Encode()
}

// errNoVerifiedEmail is returned when GitHub yields no address we may use.
var errNoVerifiedEmail = errors.New("no verified email on the github account")
