package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Translation of a fixture's `oauth_exchange` block into provider wire format.
//
// A fixture says what the exchange RESOLVES TO — a GitHub user id and login, or
// a Google address and whether it is verified. The fake server serves provider
// payloads verbatim (ADR-0010), so something has to turn one into the other,
// and that something belongs in the harness: the fixture should not have to
// know that GitHub puts the address on a second endpoint, and the fake server
// must not know that a contributor called Nina exists.
//
// This is the same projection identities.go performs for seeded principals,
// applied to the identities a fixture declares for itself — the sign-ups, the
// renamed login, the unverified address. Those cannot come from principals.json
// precisely because the account must NOT exist before the callback runs.

// oauthExchange is one code's outcome, as a fixture states it.
//
// Error and the identity fields are mutually exclusive. A code carrying an
// error is one the provider REFUSES to exchange — an expired or replayed
// grant — which the adapter must surface as a failed sign-in rather than as an
// identity nobody has.
type oauthExchange struct {
	Error string `json:"error"`

	// GitHub.
	GitHubUserID int64  `json:"github_user_id"`
	Login        string `json:"login"`

	// Both.
	Name  string `json:"name"`
	Email string `json:"email"`

	// Google. A pointer because false is the interesting value: an address
	// Google has not verified is refused (ADR-0002), and an omitted field must
	// not silently become that case.
	EmailVerified *bool `json:"email_verified"`
}

// translateOAuth rewrites a fixture's provider block into the server's shape.
//
// Anything other than `oauth_exchange` — repositories, pull requests,
// judgements — is already in wire format and passes through untouched.
func translateOAuth(provider string, block json.RawMessage) (json.RawMessage, error) {
	if len(block) == 0 {
		return block, nil
	}

	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(block, &keyed); err != nil {
		return nil, fmt.Errorf("decoding the %s block: %w", provider, err)
	}
	// A bare "owner/repo#number" key is shorthand for a pull-request override.
	// The fixtures use it because that is the thing being overridden; the
	// server keys off `pull_requests`, so the routing happens here rather than
	// making every fixture spell out the nesting.
	if provider == "github" {
		prs := map[string]json.RawMessage{}
		for name, value := range keyed {
			if strings.Contains(name, "#") {
				prs[name] = value
				delete(keyed, name)
			}
		}
		if len(prs) > 0 {
			encoded, err := json.Marshal(prs)
			if err != nil {
				return nil, fmt.Errorf("encoding pull request overrides: %w", err)
			}
			if keyed["pull_requests"], err = mergeJSON(keyed["pull_requests"], encoded); err != nil {
				return nil, fmt.Errorf("merging pull request overrides: %w", err)
			}
		}
	}

	raw, ok := keyed["oauth_exchange"]
	if !ok {
		return json.Marshal(keyed)
	}
	delete(keyed, "oauth_exchange")

	var declared map[string]oauthExchange
	if err := json.Unmarshal(raw, &declared); err != nil {
		return nil, fmt.Errorf("decoding %s.oauth_exchange: %w", provider, err)
	}

	codes := map[string]json.RawMessage{}
	for code, exchange := range declared {
		if exchange.Error != "" {
			// Declared and left UNRESOLVABLE. The fake server answers a code it
			// holds no grant for the way the real provider does, so dropping
			// the entry is how a fixture says "this exchange fails" — recording
			// it would make the code work.
			continue
		}

		grant, err := grantFor(provider, code, exchange)
		if err != nil {
			return nil, err
		}
		codes[code] = grant
	}

	if len(codes) > 0 {
		encoded, err := json.Marshal(codes)
		if err != nil {
			return nil, fmt.Errorf("encoding %s oauth codes: %w", provider, err)
		}
		// Merged rather than assigned: a fixture may declare oauth_exchange
		// alongside a literal oauth_codes block, and neither should erase the
		// other.
		if keyed["oauth_codes"], err = mergeJSON(keyed["oauth_codes"], encoded); err != nil {
			return nil, fmt.Errorf("merging %s oauth codes: %w", provider, err)
		}
	}
	return json.Marshal(keyed)
}

// grantFor projects one declared exchange into the provider's payload.
func grantFor(provider, code string, e oauthExchange) (json.RawMessage, error) {
	switch provider {
	case "github":
		return json.Marshal(githubGrant{
			User: githubUser{
				ID: e.GitHubUserID, Login: e.Login, Name: e.Name, Email: e.Email,
				NodeID: fmt.Sprintf("MDQ6VXNlcjEwMDE=%d", e.GitHubUserID),
				AvatarURL: fmt.Sprintf("https://avatars.githubusercontent.com/u/%d",
					e.GitHubUserID),
			},
			// On the private list rather than the profile, so the branch that
			// reads /user/emails is exercised — the same choice identities.go
			// makes for seeded principals.
			Emails: []githubEmail{{Email: e.Email, Primary: true, Verified: true}},
		})

	case "google":
		verified := true
		if e.EmailVerified != nil {
			verified = *e.EmailVerified
		}
		return json.Marshal(googleGrant{
			UserInfo: googleUserInfo{
				Subject: "google-" + code, Email: e.Email,
				EmailVerified: verified, Name: e.Name,
			},
		})
	}
	return nil, fmt.Errorf("%s has no oauth_exchange translation", provider)
}
