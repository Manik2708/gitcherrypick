package e2e

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFixtures is the entire suite. There is one Go test; every case is data.
//
// STAGE 3: nothing is implemented, so every subtest is expected to FAIL. A
// passing one means the fixture asserts something no implementation provides —
// report it to the Planner rather than editing the fixture.

var update = flag.Bool("update", false,
	"rewrite the `expect` blocks of every fixture from the live response — REVIEW THE DIFF")

const fixturesRoot = "fixtures/controllers"

func TestFixtures(t *testing.T) {
	// -short skips this. The suite needs Docker and a real Postgres, so it is
	// not something `go test ./...` should try to run on a machine that has
	// neither. `make e2e` is how you run it deliberately.
	if testing.Short() {
		t.Skip("integration suite: run it with backend/scripts/e2e.sh or `make e2e`")
	}

	cases, err := LoadAll(fixturesRoot)
	if err != nil {
		t.Fatalf("loading fixtures: %v", err)
	}

	if *update {
		// -update rewrites snapshots from live responses. With no API there is
		// nothing to record, and writing empty expectations would turn every
		// fixture into a test that asserts nothing.
		t.Fatalf("-update needs a running API to record responses from; stage 4 has not built one")
	}

	ctx := context.Background()
	pool, err := Connect(ctx)
	if err != nil {
		// Not a test failure — the harness could not start. e2e.sh is what
		// makes this work; running `go test` directly without it will land here.
		t.Fatalf("%v\n\nRun the suite through backend/scripts/e2e.sh, which starts Postgres and applies the schema.", err)
	}
	t.Cleanup(pool.Close)

	ddl, err := loadDDL()
	if err != nil {
		t.Fatalf("loading schema: %v", err)
	}

	principals, err := LoadPrincipals()
	if err != nil {
		t.Fatalf("loading principals: %v", err)
	}

	t.Logf("%d fixtures loaded from %s", len(cases), fixturesRoot)

	for _, c := range cases {
		t.Run(c.SubtestName(fixturesRoot), func(t *testing.T) {
			runCase(t, ctx, pool, ddl, principals, c)
		})
	}
}

// sessionCache signs each principal in once per case and reuses the token.
// Re-authenticating per step would mint a new session family every time, which
// would quietly defeat the fixture that asserts a replayed refresh token
// revokes the whole family.
type sessionCache struct {
	principals Principals
	sessions   map[string]*Session
}

func (s *sessionCache) For(ctx context.Context, client *http.Client, as string) (*Session, error) {
	if as == "" || as == "anonymous" {
		return nil, nil
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	if session, ok := s.sessions[as]; ok {
		return session, nil
	}
	session, err := Authenticate(ctx, client, as, s.principals)
	if err != nil {
		return nil, fmt.Errorf("signing in as %s: %w", as, err)
	}
	s.sessions[as] = session
	return session, nil
}

func runCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ddl []string, principals Principals, c *Case) {
	t.Helper()
	// The description is the record of why this case exists. Printing it means
	// a failure explains itself without opening the file.
	t.Logf("%s\n\n%s\n", c.Name, c.Description)

	schema := SchemaName(c.SubtestName(fixturesRoot))
	drop, err := CreateSchema(ctx, pool, schema)
	if err != nil {
		t.Fatalf("preparing schema: %v", err)
	}
	t.Cleanup(drop)

	if err := applyDDL(ctx, pool, schema, ddl); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	bindings, err := Seed(ctx, pool, schema, c.Seed)
	if err != nil {
		t.Fatalf("seeding %v: %v", c.Seed, err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	sessions := &sessionCache{principals: principals}

	for i, step := range c.Steps {
		label := fmt.Sprintf("step %d: %s %s", i, step.Request.Method, step.Request.Path)

		if step.Request.IsPseudo() {
			// The evaluator, the clock and the daily jobs are driven through
			// the harness rather than over HTTP. They arrive in stage 5.
			t.Errorf("%s: harness pseudo-method %q is not implemented yet (stage 5)",
				label, step.Request.Method)
			return
		}

		path, err := resolve(step.Request.Path, bindings)
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		body, err := resolveJSON(step.Request.Body, bindings)
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}

		// `fake` is applied BEFORE the request, so the adapter is primed when
		// it arrives. It goes over the API's separate control listener, which
		// exists only under --adapters=fake.
		if err := ApplyFakes(ctx, client, step.Fake); err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}

		session, err := sessions.For(ctx, client, step.As)
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}

		status, respBody, err := send(ctx, client, step, path, body, session)
		if err != nil {
			t.Errorf("%s: %v", label, err)
			// One unreachable request makes every later step meaningless.
			return
		}

		if status != step.Expect.Status {
			t.Errorf("%s: expected status %d, got %d\nbody: %s",
				label, step.Expect.Status, status, truncate(string(respBody)))
		}
		if diff := Compare(step.Expect.Body, respBody); diff != "" {
			t.Errorf("%s: body does not match the snapshot\n%s", label, diff)
		}
		if err := AssertDB(ctx, pool, schema, step.Expect.DB, bindings); err != nil {
			t.Errorf("%s: %v", label, err)
		}

		bind(bindings, step, respBody)
	}
}

func send(ctx context.Context, client *http.Client, step Step, path string, body []byte, session *Session) (int, []byte, error) {
	var reader io.Reader
	if len(body) > 0 && string(body) != "null" {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, step.Request.Method, BaseURL()+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range step.Request.Headers {
		req.Header.Set(k, v)
	}
	if session != nil {
		// A real bearer token from a real sign-in. There is no test-only
		// header: an auth bypass in the shipping binary is one build-tag
		// mistake away from production, and the auth path is a third of what
		// these fixtures assert — a suite that skips login cannot test login.
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		var netErr *net.OpError
		if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
			return 0, nil, fmt.Errorf(
				"no API is listening at %s — stage 4 has not built one yet, so this fixture cannot pass",
				BaseURL())
		}
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// loadDDL reads the approved schema deltas in RFC order. They are not
// commutative, and each statement is applied separately because
// ALTER TYPE ... ADD VALUE cannot share a transaction with its first use.
func loadDDL() ([]string, error) {
	paths, err := filepath.Glob("../../rfc/RFC-*.schema")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("no .schema files found under rfc/")
	}
	var statements []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		statements = append(statements, splitStatements(string(raw))...)
	}
	return statements, nil
}

// splitStatements breaks a .schema file into individual statements.
//
// Comments are removed BEFORE splitting, not after: a line comment may contain
// a semicolon ("This file is a DELTA; the full schema is..."), and splitting
// first tears it in half, leaving the tail as prose the parser then chokes on.
//
// Statements are applied one at a time rather than as one batch because
// ALTER TYPE ... ADD VALUE cannot share a transaction with its first use —
// verified in RFC-0008's schema against PostgreSQL 16.
func splitStatements(sql string) []string {
	var (
		out       []string
		current   strings.Builder
		runes     = []rune(sql)
		inLine    bool // -- to end of line
		inBlock   bool // /* ... */
		inString  bool // '...'
		dollarTag string
	)

	// A statement ends at a semicolon ONLY when the scanner is at top level.
	// Splitting the comment-stripped text afterwards is not equivalent: the
	// body of a $$...$$ function contains semicolons that are not terminators.
	flush := func() {
		if statement := strings.TrimSpace(current.String()); statement != "" {
			out = append(out, statement)
		}
		current.Reset()
	}

	for i := 0; i < len(runes); i++ {
		rest := string(runes[i:])

		switch {
		case inLine:
			if runes[i] == '\n' {
				inLine = false
				current.WriteRune('\n')
			}
			continue

		case inBlock:
			if strings.HasPrefix(rest, "*/") {
				inBlock = false
				i++
			}
			continue

		case dollarTag != "":
			if strings.HasPrefix(rest, dollarTag) {
				current.WriteString(dollarTag)
				i += len(dollarTag) - 1
				dollarTag = ""
				continue
			}
			current.WriteRune(runes[i])
			continue

		case inString:
			current.WriteRune(runes[i])
			if runes[i] == '\'' {
				// '' is an escaped quote, not the end of the literal.
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current.WriteRune('\'')
					i++
					continue
				}
				inString = false
			}
			continue
		}

		switch {
		case strings.HasPrefix(rest, "--"):
			inLine = true
		case strings.HasPrefix(rest, "/*"):
			inBlock = true
			i++
		case runes[i] == '\'':
			inString = true
			current.WriteRune(runes[i])
		case runes[i] == '$':
			if tag := dollarQuote(rest); tag != "" {
				dollarTag = tag
				current.WriteString(tag)
				i += len(tag) - 1
				continue
			}
			current.WriteRune(runes[i])
		case runes[i] == ';':
			flush()
		default:
			current.WriteRune(runes[i])
		}
	}
	flush()

	return out
}

// dollarQuote returns the opening tag of a $$ or $tag$ literal, or "".
func dollarQuote(s string) string {
	if !strings.HasPrefix(s, "$") {
		return ""
	}
	end := strings.Index(s[1:], "$")
	if end < 0 {
		return ""
	}
	tag := s[:end+2]
	for _, r := range tag[1 : len(tag)-1] {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return tag
}

func applyDDL(ctx context.Context, pool *pgxpool.Pool, schema string, statements []string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("%w\n\nstatement: %s", err, truncate(statement))
		}
	}
	return nil
}
