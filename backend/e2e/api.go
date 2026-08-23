package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// One API process per fixture.
//
// The suite isolates each fixture in its own PostgreSQL schema, so the API has
// to be pointed at that schema — and a connection pool's search_path is fixed
// when it opens. A single long-running API would serve every fixture from
// whichever schema it started with, which is how the first run of this harness
// failed: `relation "users" does not exist`.
//
// The alternative was a control endpoint that re-pointed a running API, and
// that is exactly the kind of test-only route ADR-0010 refuses to compile into
// the shipping binary. Starting a process is slower and buys real isolation:
// each fixture also gets a fresh pool, a fresh clock reading and no session
// carried over from the last one.

const (
	// apiBinaryEnv names the binary e2e.sh built. Built once rather than
	// `go run` per fixture, which would recompile thirty-one times.
	apiBinaryEnv = "E2E_API_BINARY"

	// signingKeyEnv and verifyKeyEnv are the throwaway Ed25519 pair. ADR-0011
	// has no generate-if-absent fallback, so the harness must supply one.
	signingKeyEnv = "E2E_SIGNING_KEY"
	verifyKeyEnv  = "E2E_VERIFY_KEY"
)

// apiStartTimeout bounds how long a fixture waits for its API to answer.
const apiStartTimeout = 20 * time.Second

// API is a running api process, scoped to one fixture's schema.
type API struct {
	cmd     *exec.Cmd
	logPath string
}

// StartAPI launches the API against one schema and waits for it to answer.
//
// Every third-party base URL points at the fake server. These are the SAME
// flags a deployment uses to reach the real hosts; there is no test-only branch
// inside the binary (ADR-0010).
func StartAPI(ctx context.Context, schema string) (*API, error) {
	binary := os.Getenv(apiBinaryEnv)
	if binary == "" {
		return nil, fmt.Errorf(
			"%s is not set: run the suite through backend/scripts/e2e.sh, which builds "+
				"cmd/api and points the harness at it", apiBinaryEnv)
	}

	dsn, err := schemaScopedDSN(os.Getenv(databaseURLEnvForJobs), schema)
	if err != nil {
		return nil, err
	}

	log, err := os.CreateTemp("", "e2e-api-*.log")
	if err != nil {
		return nil, fmt.Errorf("opening a log for the api: %w", err)
	}
	defer func() { _ = log.Close() }()

	control := ControlURL()
	cmd := exec.CommandContext(ctx, binary,
		"--addr="+addrOf(BaseURL()),
		"--database-url="+dsn,
		"--signing-key="+os.Getenv(signingKeyEnv),
		"--verify-key=e2e:"+os.Getenv(verifyKeyEnv),
		"--clock-url="+control+"/_clock",
		"--github-api-url="+control+"/github",
		"--github-oauth-url="+control+"/github",
		"--github-client-id=e2e", "--github-client-secret=e2e", "--github-token=e2e",
		"--google-oidc-issuer="+control+"/google",
		"--google-client-id=e2e", "--google-client-secret=e2e",
		"--google-redirect-uri="+BaseURL()+"/auth/google/callback",
		"--resend-api-url="+control+"/resend",
		"--resend-api-key=e2e",
	)
	cmd.Stdout = log
	cmd.Stderr = log

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the api: %w", err)
	}

	api := &API{cmd: cmd, logPath: log.Name()}
	if err := api.waitReady(ctx); err != nil {
		api.Stop()
		return nil, err
	}
	return api, nil
}

// Stop terminates the process and removes its log.
//
// Waited on rather than merely signalled: the next fixture binds the same port,
// and a process still holding it would fail the next start for a reason that
// has nothing to do with that fixture.
func (a *API) Stop() {
	if a.cmd == nil || a.cmd.Process == nil {
		return
	}
	_ = a.cmd.Process.Kill()
	_, _ = a.cmd.Process.Wait()
	_ = os.Remove(a.logPath)
}

// Log returns whatever the API wrote, for a failure that needs explaining.
//
// The API answers a 5xx with a bare code and no detail, deliberately — so this
// is the only place the cause exists.
func (a *API) Log() string {
	raw, err := os.ReadFile(a.logPath)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (a *API) waitReady(ctx context.Context) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(apiStartTimeout)

	for time.Now().Before(deadline) {
		if a.cmd.ProcessState != nil && a.cmd.ProcessState.Exited() {
			return fmt.Errorf("the api exited during startup:\n%s", a.Log())
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL()+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the api never answered at %s:\n%s", BaseURL(), a.Log())
}

// schemaScopedDSN points a connection at one fixture's schema.
//
// `ext` stays on the path because the extensions live there and the schema
// files reference them unqualified.
func schemaScopedDSN(base, schema string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("%s is not set", databaseURLEnvForJobs)
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing the database url: %w", err)
	}

	q := parsed.Query()
	q.Set("search_path", schema+",ext")
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// addrOf strips the scheme, because --addr takes host:port.
func addrOf(base string) string {
	return strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
}
