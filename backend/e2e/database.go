package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The suite runs against a real Postgres (ADR-0001: everything else is an
// interface and is faked, so the database is the one thing worth running for
// real — a fake would not catch a constraint, an index predicate, or a
// transaction boundary, which is most of what these fixtures assert).
//
// Each case gets its own schema rather than its own database. Creating a
// database per case is slow and serialises on the template; a schema is cheap,
// and `search_path` makes the isolation total.

// DatabaseURL is where the suite expects Postgres. e2e.sh sets it.
func DatabaseURL() string {
	if url := os.Getenv("E2E_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://gitcherrypick:e2e@127.0.0.1:55432/gitcherrypick_e2e?sslmode=disable"
}

// BaseURL is the API under test. Empty until stage 4 builds one — which is why
// every fixture is expected to fail right now.
func BaseURL() string {
	if url := os.Getenv("E2E_BASE_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8080"
}

// Connect opens a pool and verifies it answers, rather than trusting that a
// lazy pool will work later. A connection problem discovered mid-suite looks
// like a test failure; discovered here it looks like what it is.
func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("parse E2E_DATABASE_URL: %w", err)
	}

	// Simple protocol, deliberately.
	//
	// Every case builds the full DDL in its own schema, so each one creates a
	// fresh set of enum types with fresh OIDs. A pooled connection that
	// described a statement against case A's enums caches those OIDs, and the
	// next case fails with "cache lookup failed for type NNNNN" against types
	// that no longer exist.
	//
	// The application uses prepared statements (ADR-0006); this is the test
	// harness, where portability across throwaway schemas matters more than
	// plan reuse.
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(deadline); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres did not answer at %s: %w", redact(DatabaseURL()), err)
	}
	return pool, nil
}

// SchemaName derives a Postgres identifier from a subtest name.
// "claims/seven_day_lock" becomes "e2e_claims_seven_day_lock".
func SchemaName(subtest string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, subtest)

	name := "e2e_" + safe
	// Postgres truncates identifiers at 63 bytes, which would silently collide
	// two long fixture names into one schema.
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// CreateSchema gives a case an empty, isolated namespace containing the full
// DDL. The returned function drops it.
func CreateSchema(ctx context.Context, pool *pgxpool.Pool, name string) (func(), error) {
	// Drop first: a schema left by a killed run would otherwise be reused with
	// whatever rows it had, and the failure would be blamed on the fixture.
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", name)); err != nil {
		return nil, fmt.Errorf("drop schema %s: %w", name, err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", name)); err != nil {
		return nil, fmt.Errorf("create schema %s: %w", name, err)
	}

	drop := func() {
		// Best effort, and deliberately not tied to the test's context: a
		// cancelled context is exactly when cleanup matters most.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanup, fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", name))
	}
	return drop, nil
}

// redact strips the password from a DSN so a failure message can be pasted
// into an issue.
func redact(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	slashes := strings.Index(dsn, "//")
	if at < 0 || slashes < 0 || at < slashes {
		return dsn
	}
	return dsn[:slashes+2] + "***" + dsn[at:]
}

// poolLike is the slice of *pgxpool.Pool the assertions need. Narrowing it
// keeps AssertDB testable without a live database.
type poolLike interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}
