package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// How the suite controls the world outside the API.
//
// ADR-0001 makes GitHub, Google, the AI provider, the notifier and the clock
// interfaces. ADR-0010 decides how the suite substitutes them, and the answer
// is that it does NOT substitute them: the API runs its real adapters, pointed
// by flag at cmd/fakethirdparty, which is a SEPARATE PROCESS.
//
//	api --github-api-url=http://127.0.0.1:8081/github \
//	    --google-oidc-issuer=http://127.0.0.1:8081/google \
//	    --resend-api-url=http://127.0.0.1:8081/resend \
//	    --clock-url=http://127.0.0.1:8081/_clock
//
// Three properties make this safe in a way a magic request header is not:
//
//  1. It is a different PROCESS, and Go forbids importing a main package, so
//     cmd/api cannot link this code even by accident. There is no build tag to
//     forget and no flag to misconfigure.
//  2. The API is an HTTP client of the fake server and never the reverse, so
//     nothing in the API can reach the control endpoints below.
//  3. The adapters under test are the REAL ones. Their JSON decoding, error
//     mapping and pagination run on every fixture rather than first meeting
//     reality in production.
//
// The alternative — an `X-E2E-Principal` header on the API — would be an
// authentication bypass compiled into the shipping binary. This suite does not
// have one: it signs in through the real endpoints and carries a real Ed25519
// token.

// controlAddrEnv overrides where the fake server listens, for a harness running
// it on a non-default port.
const controlAddrEnv = "E2E_CONTROL_URL"

// ControlURL is the fake server's base address.
func ControlURL() string {
	if url := os.Getenv(controlAddrEnv); url != "" {
		return strings.TrimSuffix(url, "/")
	}
	return "http://127.0.0.1:8081"
}

// Control drives cmd/fakethirdparty.
//
// It holds the loaded state so a step's `fake` block can be merged into it and
// the whole thing re-loaded. The server itself stays dumb — it never merges,
// because a server that merged would be deciding what a fixture meant.
type Control struct {
	base   string
	client *http.Client
	state  map[string]json.RawMessage
}

// NewControl connects to the fake server.
func NewControl(client *http.Client) *Control {
	return &Control{
		base:   ControlURL(),
		client: client,
		state:  map[string]json.RawMessage{},
	}
}

// SentEmail is one message the API asked Resend to deliver.
type SentEmail struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// LoadCase installs a fixture's third-party state, replacing everything.
//
// The seeded principals' OAuth identities are synthesized rather than declared:
// every fixture signs somebody in, and making each one restate identities that
// principals.json already holds would be boilerplate nobody reads. Everything
// else — pull requests, repositories, judgements — comes from the fixture,
// because that is data the fixture's assertions turn on (ADR-0010).
func (c *Control) LoadCase(ctx context.Context, p Principals, seeds []string, thirdParty json.RawMessage) error {
	state := map[string]json.RawMessage{
		"github": identitiesFor(p),
		"google": googleIdentitiesFor(p),
	}

	// The `repositories` seed set is GitHub data: it never reaches the
	// database, so it has to reach the fake server instead.
	repositories, err := LoadRepositories(seeds)
	if err != nil {
		return err
	}
	if len(repositories) > 0 {
		merged, err := mergeJSON(state["github"], repositories)
		if err != nil {
			return fmt.Errorf("merging the repositories seed: %w", err)
		}
		state["github"] = merged
	}

	if len(thirdParty) > 0 {
		declared := map[string]json.RawMessage{}
		if err := json.Unmarshal(thirdParty, &declared); err != nil {
			return fmt.Errorf("decoding the fixture's third_party block: %w", err)
		}
		for provider, block := range declared {
			merged, err := mergeJSON(state[provider], block)
			if err != nil {
				return fmt.Errorf("merging third_party.%s: %w", provider, err)
			}
			state[provider] = merged
		}
	}

	c.state = state
	return c.push(ctx)
}

// ApplyFake merges a step's `fake` block and reloads.
//
// Merged rather than replaced: a step declaring one PR must not erase the
// identities the fixture is signed in with.
func (c *Control) ApplyFake(ctx context.Context, f *Fake) error {
	if f == nil {
		return nil
	}

	if f.Clock != nil {
		if err := c.applyClock(ctx, f.Clock); err != nil {
			return err
		}
	}

	changed := false
	for provider, block := range map[string]json.RawMessage{
		"github": f.GitHub, "google": f.Google, "anthropic": f.AI,
	} {
		if len(block) == 0 {
			continue
		}
		merged, err := mergeJSON(c.state[provider], block)
		if err != nil {
			return fmt.Errorf("merging fake.%s: %w", provider, err)
		}
		c.state[provider] = merged
		changed = true
	}

	if !changed {
		return nil
	}
	return c.push(ctx)
}

// AdvanceClock moves the API's clock forward (ADR-0012).
func (c *Control) AdvanceClock(ctx context.Context, duration string) error {
	body, err := json.Marshal(map[string]string{"duration": duration})
	if err != nil {
		return fmt.Errorf("encoding the clock advance: %w", err)
	}
	return c.post(ctx, "/_clock/advance", body, nil)
}

// SentEmails returns every message the API sent since the last LoadCase.
func (c *Control) SentEmails(ctx context.Context) ([]SentEmail, error) {
	var out []SentEmail
	if err := c.get(ctx, "/_sent/emails", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// applyClock honours the two spellings the fixture format allows.
//
// `set` is rejected rather than approximated: the clock only moves forward
// (ADR-0012), and a fixture that set an absolute time could un-expire a lock it
// had already passed.
func (c *Control) applyClock(ctx context.Context, clock *ClockFake) error {
	switch {
	case clock.Advance != "":
		return c.AdvanceClock(ctx, clock.Advance)
	case clock.Set != "":
		return fmt.Errorf(
			"fake.clock.set is not supported: the clock only moves forward (ADR-0012), "+
				"so express %q as an advance", clock.Set)
	}
	return nil
}

func (c *Control) push(ctx context.Context) error {
	body, err := json.Marshal(c.state)
	if err != nil {
		return fmt.Errorf("encoding the third-party state: %w", err)
	}
	return c.post(ctx, "/_load", body, nil)
}

func (c *Control) post(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, path, out)
}

func (c *Control) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", path, err)
	}
	return c.do(req, path, out)
}

func (c *Control) do(req *http.Request, path string, out any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return controlUnreachable(err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, truncateBody(payload))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// controlUnreachable explains the most common setup mistake once, rather than
// letting it present as a connection error nobody recognises.
func controlUnreachable(err error) error {
	return fmt.Errorf(
		"the fake third-party server is not listening at %s: %w\n\n"+
			"Run the suite through backend/scripts/e2e.sh, which starts it alongside "+
			"Postgres and the API", ControlURL(), err)
}

// mergeJSON overlays b onto a, one level deep per collection.
//
// Deep enough that a step adding one pull request keeps the ones already
// declared, and shallow enough that declaring a pull request REPLACES it rather
// than blending two payloads into something neither fixture asked for.
func mergeJSON(base, overlay json.RawMessage) (json.RawMessage, error) {
	if len(base) == 0 {
		return overlay, nil
	}
	if len(overlay) == 0 {
		return base, nil
	}

	var into, from map[string]json.RawMessage
	if err := json.Unmarshal(base, &into); err != nil {
		return nil, fmt.Errorf("decoding the existing state: %w", err)
	}
	if err := json.Unmarshal(overlay, &from); err != nil {
		return nil, fmt.Errorf("decoding the overlay: %w", err)
	}

	for collection, entries := range from {
		existing, present := into[collection]
		if !present {
			into[collection] = entries
			continue
		}

		// Both sides name the same collection — oauth_codes, pull_requests —
		// so merge by key. One fixture's PRs and a step's addition are the
		// same map.
		var current, incoming map[string]json.RawMessage
		if err := json.Unmarshal(existing, &current); err != nil {
			into[collection] = entries
			continue
		}
		if err := json.Unmarshal(entries, &incoming); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", collection, err)
		}
		for key, value := range incoming {
			current[key] = value
		}

		merged, err := json.Marshal(current)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", collection, err)
		}
		into[collection] = merged
	}

	return json.Marshal(into)
}

func truncateBody(b []byte) string {
	const limit = 300
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…"
}
