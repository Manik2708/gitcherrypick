package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// How the fakes are reached.
//
// ADR-0001 makes GitHub, the AI provider, the broker, the notifier and the
// clock interfaces. The suite runs against fake implementations of all five.
// The question this file answers is how a fixture's `steps[].fake` block gets
// into a process the test does not share memory with.
//
// It goes over a SEPARATE control listener, which the API binary starts only
// when told to run with fake adapters:
//
//     api --adapters=fake --control-addr=127.0.0.1:8081
//
// Three properties make this safe in a way a magic request header is not:
//
//  1. It is a different socket. Production binds no control listener, so there
//     is no route to reach — not a route that checks a flag and refuses.
//  2. It is opt-in at the command line, via cobra like every other setting
//     (CLAUDE.md: config comes from CLI flags). A deployment that does not pass
//     --adapters=fake gets real adapters and no control surface at all.
//  3. It cannot be reached through the API's own port, so an attacker who can
//     talk to the public listener cannot talk to this one.
//
// The alternative — an `X-E2E-Principal` header on the main API — would be an
// authentication bypass compiled into the shipping binary, one build-tag
// mistake away from production. This suite does not have one.

// ControlURL is where the API's fake-control listener is expected.
func ControlURL() string {
	if url := os.Getenv("E2E_CONTROL_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8081"
}

// SeededPassword is the plaintext behind every seeded password hash. The
// fixtures that sign in state it literally, so the two must agree.
const SeededPassword = "correct-horse-battery"

// ApplyFakes pushes one step's `fake` block into the running API. It is called
// before the step's request, so the adapter is primed when the request arrives.
func ApplyFakes(ctx context.Context, client *http.Client, f *Fake) error {
	if f == nil {
		return nil
	}
	if f.GitHub != nil {
		if err := control(ctx, client, "/fakes/github", f.GitHub); err != nil {
			return fmt.Errorf("priming github: %w", err)
		}
	}
	if f.Google != nil {
		if err := control(ctx, client, "/fakes/google", f.Google); err != nil {
			return fmt.Errorf("priming google: %w", err)
		}
	}
	if f.AI != nil {
		if err := control(ctx, client, "/fakes/ai", f.AI); err != nil {
			return fmt.Errorf("priming ai: %w", err)
		}
	}
	if f.Rubric != nil {
		if err := control(ctx, client, "/fakes/rubric", f.Rubric); err != nil {
			return fmt.Errorf("setting rubric: %w", err)
		}
	}
	if f.Clock != nil {
		// The clock is an interface for exactly this reason: the 7-day lock,
		// the 15-day availability window, lease expiry and the escalating
		// cooldown are all untestable against a real one.
		if err := control(ctx, client, "/fakes/clock", f.Clock); err != nil {
			return fmt.Errorf("setting clock: %w", err)
		}
	}
	return nil
}

// PrimeGitHubOAuth tells the fake GitHub adapter which identity an
// authorization code resolves to, so a real callback lands on a seeded user.
func PrimeGitHubOAuth(ctx context.Context, client *http.Client, code string, c Contributor) error {
	return control(ctx, client, "/fakes/github/oauth", map[string]any{
		code: map[string]any{
			"github_user_id": c.GitHubUserID,
			"login":          c.GitHubLogin,
			"name":           c.DisplayName,
			"email":          c.Email,
		},
	})
}

// PrimeGoogleOAuth does the same for the hirer-side Google flow.
func PrimeGoogleOAuth(ctx context.Context, client *http.Client, code, email string) error {
	return control(ctx, client, "/fakes/google/oauth", map[string]any{
		code: map[string]any{"email": email, "email_verified": true},
	})
}

// RunControlAction drives one background worker synchronously.
//
// It backs the harness pseudo-methods — RUN_EVALUATOR, RUN_OVERDUE_SWEEP and
// the rest — so a fixture can assert on a job's effects without polling. The
// alternative is a sleep, and a sleep is a flake with a timer attached.
func RunControlAction(ctx context.Context, client *http.Client, action string, payload any) (json.RawMessage, error) {
	var out json.RawMessage
	if err := controlInto(ctx, client, "/actions/"+action, payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func control(ctx context.Context, client *http.Client, path string, payload any) error {
	return controlInto(ctx, client, path, payload, nil)
}

func controlInto(ctx context.Context, client *http.Client, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ControlURL()+path, jsonReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return controlUnreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("control %s returned %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func controlUnreachable(err error) error {
	return fmt.Errorf(
		"no fake-control listener at %s — the API must be started with "+
			"`--adapters=fake --control-addr=...`, which stage 4 has not built yet (%w)",
		ControlURL(), err)
}
