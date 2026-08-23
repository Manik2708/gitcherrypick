package github

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// userPayload is GitHub's authenticated-user object. Email is present only when
// the contributor made it public, which is why primaryEmail exists.
type userPayload struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// tokenPayload is the OAuth token exchange response.
//
// Error and ErrorDescription are populated on a FAILED exchange that still
// arrives as HTTP 200, which is why both are read.
type tokenPayload struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// emailPayload is one entry in the contributor's address list.
type emailPayload struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// ExchangeCode completes the OAuth flow and returns the identity.
//
// This is the only path to a contributor account (ADR-0002), so everything it
// returns is load-bearing. Identity is the immutable numeric id: a login can be
// changed or given to somebody else, and keying on it would eventually hand one
// contributor's account to whoever claimed their old name.
func (c *Client) ExchangeCode(ctx context.Context, code string) (*port.GitHubIdentity, error) {
	token, err := c.accessToken(ctx, code)
	if err != nil {
		return nil, err
	}

	var user userPayload
	if err := c.getAs(ctx, "/user", token, &user); err != nil {
		return nil, fmt.Errorf("reading the authenticated user: %w", err)
	}
	if user.ID == 0 {
		return nil, fmt.Errorf("github returned a user with no id")
	}

	email := user.Email
	if email == "" {
		// /user only carries the address when the contributor made it public.
		// The private list is why user:email is requested at all.
		if email, err = c.primaryEmail(ctx, token); err != nil {
			return nil, err
		}
	}

	name := user.Name
	if name == "" {
		name = user.Login
	}

	return &port.GitHubIdentity{
		GitHubUserID: user.ID,
		Login:        user.Login,
		Name:         name,
		Email:        email,
	}, nil
}

// accessToken trades the callback code for a token.
func (c *Client) accessToken(ctx context.Context, code string) (string, error) {
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" {
		return "", fmt.Errorf("github oauth is not configured: pass --github-client-id and --github-client-secret")
	}

	form := url.Values{
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code":          {code},
	}

	// PostForm sends Accept: application/json. Without it GitHub answers in
	// form encoding, which would decode into an empty struct and read as a
	// token-less success.
	var body tokenPayload
	if err := c.http.PostForm(ctx, c.oauth+"/login/oauth/access_token",
		"the oauth token exchange", form, &body); err != nil {
		return "", err
	}

	// A bad or expired code comes back as HTTP 200 with an error field. A
	// caller checking only the status would accept an empty token and mint a
	// session for nobody.
	if body.Error != "" {
		return "", fmt.Errorf("github rejected the code: %s", firstNonEmpty(body.ErrorDescription, body.Error))
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("github returned no access token")
	}
	return body.AccessToken, nil
}

// primaryEmail picks the address to store.
//
// Verified only, and primary by preference. An unverified address proves
// nothing — anyone can type any address into a profile they control — and it is
// what the platform emails on a contact request, so sending there would leak a
// hiring approach to whoever really owns it.
func (c *Client) primaryEmail(ctx context.Context, token string) (string, error) {
	var emails []emailPayload
	if err := c.getAs(ctx, "/user/emails", token, &emails); err != nil {
		return "", fmt.Errorf("reading the email list: %w", err)
	}

	fallback := ""
	for _, e := range emails {
		if !e.Verified {
			continue
		}
		if e.Primary {
			return e.Email, nil
		}
		if fallback == "" {
			fallback = e.Email
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errNoVerifiedEmail
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
