// Package google implements port.OAuthProvider against Google's OIDC endpoints.
//
// Hirer-side sign-in only. A contributor account exists solely through GitHub
// (ADR-0002), so nothing here can create one.
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/httpx"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DefaultIssuer is Google's OIDC issuer. ADR-0010 makes it configurable so the
// integration suite can point this same client at cmd/fakethirdparty.
const DefaultIssuer = "https://accounts.google.com"

// discoveryPath is fixed by the OIDC spec, relative to the issuer.
const discoveryPath = "/.well-known/openid-configuration"

// authorizePath is Google's authorization endpoint on the issuer host.
//
// Built directly rather than discovered because AuthorizeURL performs no IO —
// it is called to build a redirect, not to make a request. The token and
// userinfo endpoints ARE discovered, because Google serves them from different
// hosts and hardcoding those would break the moment they moved.
const authorizePath = "/o/oauth2/v2/auth"

// scopes requested at authorization.
//
// openid and email are what identity requires; profile supplies a display name
// so a new seat is not nameless. Nothing here grants access to anything the
// hirer owns.
const scopes = "openid email profile"

// ErrEmailUnverified is returned when Google will not vouch for the address.
//
// port.OAuthProvider makes verification a PRECONDITION rather than a field to
// inspect: anyone can put any address in a profile they control, and the
// address is what decides which organization seat a sign-in lands on.
// An alias for port.ErrEmailUnverified rather than a separate value: the
// service decides what an unverified address means, and it must be able to
// recognise one without importing this package.
var ErrEmailUnverified = port.ErrEmailUnverified

// Config is everything the client needs.
type Config struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string

	// RedirectURI must match the one registered with Google exactly. A
	// mismatch is rejected at the token endpoint, not at authorization.
	RedirectURI string

	HTTPClient *http.Client
}

// discoveryDocument is the subset of the OIDC metadata that is used.
type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

// tokenResponse is the token endpoint's reply.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// userInfo is the OIDC userinfo claim set.
//
// EmailVerified is a pointer so an ABSENT claim is distinguishable from an
// explicit false. Treating a missing claim as verified would accept exactly the
// identity this adapter exists to refuse.
type userInfo struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Name          string `json:"name"`
}

// Client implements port.OAuthProvider.
type Client struct {
	issuer string
	cfg    Config
	http   *httpx.Client

	// Discovery is cached after the first success. It is not fetched at
	// construction: the API must start when Google is unreachable, and only
	// sign-in should fail until it comes back.
	mu        sync.Mutex
	discovery *discoveryDocument
}

// New builds the Google client, defaulting to the real issuer.
func New(cfg Config) *Client {
	return &Client{
		issuer: httpx.TrimBase(cfg.IssuerURL, DefaultIssuer),
		cfg:    cfg,
		http:   httpx.New(cfg.HTTPClient, nil),
	}
}

var _ port.OAuthProvider = (*Client)(nil)

// AuthorizeURL builds the redirect a hirer is sent to.
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id":     {c.cfg.ClientID},
		"redirect_uri":  {c.cfg.RedirectURI},
		"response_type": {"code"},
		"scope":         {scopes},
		"state":         {state},
	}
	return c.issuer + authorizePath + "?" + q.Encode()
}

// Exchange completes the flow and returns a VERIFIED identity.
//
// It never creates anything. A Google sign-in for an unregistered address is an
// error, because registration is what raises the verification request — and a
// hirer minted here would skip it entirely (ADR-0002).
func (c *Client) Exchange(ctx context.Context, code string) (*port.OAuthIdentity, error) {
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" {
		return nil, errors.New("google oauth is not configured: pass --google-client-id and --google-client-secret")
	}

	doc, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}

	var token tokenResponse
	form := url.Values{
		"code":          {code},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"redirect_uri":  {c.cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
	}
	if err := c.http.PostForm(ctx, doc.TokenEndpoint, "the google token exchange", form, &token); err != nil {
		return nil, err
	}
	if token.Error != "" {
		return nil, fmt.Errorf("google rejected the code: %s", firstNonEmpty(token.ErrorDescription, token.Error))
	}
	if token.AccessToken == "" {
		return nil, errors.New("google returned no access token")
	}

	var info userInfo
	if err := c.http.GetJSON(ctx, doc.UserInfoEndpoint, token.AccessToken,
		"the google userinfo endpoint", &info, nil); err != nil {
		return nil, err
	}

	if info.Subject == "" {
		return nil, errors.New("google returned an identity with no subject")
	}
	if info.Email == "" {
		return nil, errors.New("google returned an identity with no email")
	}
	if info.EmailVerified == nil || !*info.EmailVerified {
		return nil, fmt.Errorf("%w: %s", ErrEmailUnverified, info.Email)
	}

	return &port.OAuthIdentity{
		Subject: info.Subject,
		Email:   info.Email,
		Name:    info.Name,
	}, nil
}

// discover fetches and caches the OIDC metadata.
//
// Cached only on SUCCESS, so a transient failure does not poison the client for
// its lifetime.
func (c *Client) discover(ctx context.Context) (*discoveryDocument, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.discovery != nil {
		return c.discovery, nil
	}

	var doc discoveryDocument
	if err := c.http.GetJSON(ctx, c.issuer+discoveryPath, "",
		"the google discovery document", &doc, nil); err != nil {
		return nil, err
	}
	if doc.TokenEndpoint == "" || doc.UserInfoEndpoint == "" {
		return nil, fmt.Errorf("the google discovery document at %s names no token or userinfo endpoint", c.issuer)
	}

	c.discovery = &doc
	return c.discovery, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
