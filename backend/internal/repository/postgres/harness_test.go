package postgres_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

// These are integration tests. They run against a real PostgreSQL 16 and are
// not unit tests with a database attached.
//
// A repository's whole job is translating between domain types and storage
// rows, and every interesting thing it does — a partial index, a check
// constraint, a FOR UPDATE SKIP LOCKED lease, an enum, ON CONFLICT — is
// behaviour the database owns. Mocking that would test the mock.
//
// Run them with:
//
//	backend/scripts/db-test.sh
//
// ISOLATION. Every test starts from an empty database: newDB truncates every
// table before handing it over, never after. Cleaning up front rather than
// afterwards means a failed test leaves its rows behind for inspection, which
// is what --keep-db exists for, and it means a test that crashes cannot poison
// the next one.

const databaseURLEnv = "DB_TEST_DATABASE_URL"

// The truncate statement is derived once from the live catalogue rather than
// hard-coded: a table added by a later migration would otherwise silently stop
// being cleaned, and the first symptom would be a test that passes alone and
// fails in a suite.
//
// Only the STRING is cached. Nothing here holds a connection between tests —
// that is what lets each test own and close its own pool.
var (
	truncateOnce sync.Once
	truncateStmt string
	truncateErr  error
)

// newDB returns a *postgres.DB against an empty database.
//
// Each test gets its OWN pool, closed through t.Cleanup. A pool shared across
// the package would have to be closed by TestMain, which would put database
// lifecycle into the leak test and — worse — mean goleak could not tell a
// forgotten pool from the shared one. Per-test pools are a few connections
// against a local Postgres and they make the isolation real rather than
// conventional.
//
// The database is truncated BEFORE the test, never after: a failed test leaves
// its rows behind for inspection, which is what --keep-db is for, and a test
// that crashes cannot poison the next one.
//
// The suite skips when no database is configured, so `go test ./...` on a
// machine without Docker reports skipped rather than failed — a red suite that
// means "you didn't start Postgres" trains people to ignore red.
func newDB(t *testing.T) *postgres.DB {
	t.Helper()

	url := os.Getenv(databaseURLEnv)
	if url == "" {
		t.Skipf("%s is not set — run these through backend/scripts/db-test.sh", databaseURLEnv)
	}

	pool, err := openPool(url)
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	truncateOnce.Do(func() { truncateStmt, truncateErr = buildTruncate(context.Background(), pool) })
	if truncateErr != nil {
		t.Fatalf("building the truncate statement: %v", truncateErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, truncateStmt); err != nil {
		t.Fatalf("cleaning the database: %v", err)
	}

	return postgres.New(pool)
}

func openPool(url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", databaseURLEnv, err)
	}
	// Small, because there is one pool per test and they overlap when the
	// package runs in parallel.
	config.MaxConns = 4
	config.MinConns = 0

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("postgres did not answer: %w", err)
	}
	return p, nil
}

// buildTruncate asks the catalogue which tables exist.
//
// One TRUNCATE naming every table, with CASCADE and RESTART IDENTITY: a single
// statement means foreign keys never have to be ordered, and it is one round
// trip rather than one per table.
func buildTruncate(ctx context.Context, p *pgxpool.Pool) (string, error) {
	rows, err := p.Query(ctx, `
		SELECT quote_ident(tablename)
		FROM pg_tables
		WHERE schemaname = 'public'
		ORDER BY tablename`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("no tables in the public schema — has the schema been applied?")
	}
	return "TRUNCATE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE", nil
}

// --- assertions --------------------------------------------------------------

// count returns how many rows match, for asserting on state a repository method
// does not return — most importantly that something was NOT written.
func count(t *testing.T, db *postgres.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	return n
}

// ctx is the context every test uses. Bounded, so a query that blocks on a lock
// fails the test instead of hanging the suite.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
