package e2e

import (
	"encoding/json"
	"fmt"
)

// OAuth identities for the seeded cast.
//
// Synthesized from principals.json rather than declared per fixture. Every
// fixture signs somebody in, and restating identities the seed file already
// holds would be boilerplate in thirty-one places — with thirty-one chances to
// drift from the rows actually seeded.
//
// This is not the fake server inventing data (ADR-0010): principals.json IS
// fixture data, and these are a projection of it into the shape GitHub and
// Google return.

// AuthCode is the code a fixture's sign-in presents for a named principal.
//
// Derived rather than random so a failure names something searchable, and so a
// fixture that wants to override one identity knows what key to declare.
func AuthCode(principal string) string { return "e2e-" + principal }

// githubUser is GitHub's /user payload.
//
// userRef, the abbreviated actor, is in repositories.go alongside the payloads
// that embed it.
type githubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	NodeID    string `json:"node_id"`
	AvatarURL string `json:"avatar_url"`
}

// githubEmail is one entry in GitHub's /user/emails list.
type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// githubGrant is what one authorization code resolves to.
type githubGrant struct {
	User   githubUser    `json:"user"`
	Emails []githubEmail `json:"emails"`
}

// googleUserInfo is the OIDC claim set.
//
// EmailVerified is always true here: an unverified address is refused by the
// adapter, so a fixture wanting that case declares its own entry rather than
// getting one by accident.
type googleUserInfo struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

type googleGrant struct {
	UserInfo googleUserInfo `json:"userinfo"`
}

// identitiesFor projects every seeded contributor into GitHub's shape.
//
// Hirers are included too: a seat may sign in through GitHub (ADR-0002), and
// dave/dave_hiring deliberately share one GitHub identity across the two
// namespaces — which is the case AssertNotSelf exists for.
func identitiesFor(p Principals) json.RawMessage {
	codes := map[string]githubGrant{}

	for _, c := range p.Contributors {
		codes[AuthCode(c.Key)] = githubGrant{
			User: githubUser{
				ID: c.GitHubUserID, Login: c.GitHubLogin, Name: c.DisplayName,
				Email:  c.Email,
				NodeID: fmt.Sprintf("MDQ6VXNlcjEwMDE=%d", c.GitHubUserID),
				AvatarURL: fmt.Sprintf("https://avatars.githubusercontent.com/u/%d",
					c.GitHubUserID),
			},
			// The address is on the private list rather than the profile, so
			// every fixture exercises the branch that reads /user/emails.
			Emails: []githubEmail{{Email: c.Email, Primary: true, Verified: true}},
		}
	}

	for _, h := range p.Hirers {
		if h.GitHubUserID == nil {
			continue
		}
		codes[AuthCode(h.Key)] = githubGrant{
			User: githubUser{
				// A hirer's seeded identity carries no login of its own — the
				// seed writes their email into github_login — so the key
				// stands in. Nothing reads it: ADR-0002 keys identity on the
				// numeric id.
				ID: *h.GitHubUserID, Login: h.Key, Name: h.DisplayName,
				Email: h.Email,
			},
			Emails: []githubEmail{{Email: h.Email, Primary: true, Verified: true}},
		}
	}

	return mustMarshal(map[string]any{"oauth_codes": codes})
}

// googleIdentitiesFor projects every Google-provider hirer.
func googleIdentitiesFor(p Principals) json.RawMessage {
	codes := map[string]googleGrant{}

	for _, h := range p.Hirers {
		if h.AuthProvider != "google" {
			continue
		}
		codes[AuthCode(h.Key)] = googleGrant{
			UserInfo: googleUserInfo{
				Subject: "google-" + h.Key, Email: h.Email,
				EmailVerified: true, Name: h.DisplayName,
			},
		}
	}

	return mustMarshal(map[string]any{"oauth_codes": codes})
}

// mustMarshal encodes a value the harness built itself.
//
// A failure here is a harness bug rather than a fixture problem, and returning
// an error would push a nil check into every call site for a case that cannot
// happen with these types.
func mustMarshal(v any) json.RawMessage {
	out, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("encoding synthesized identities: %v", err))
	}
	return out
}
