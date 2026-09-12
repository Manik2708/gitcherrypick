// Command devdata builds a database a developer can point a client at.
//
// It applies the approved schema deltas from rfc/*.schema and loads the same
// seed sets the fixture suite uses. Reusing the harness rather than writing a
// second seeder is deliberate: a development database that drifted from the
// one the tests assert against would let a frontend build against a shape no
// fixture pins, and the drift would surface as a bug in the client.
//
// There is no migration tool (ADR-0006 chose none), so applying the deltas in
// RFC order IS the schema.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/Manik2708/gitcherrypick/backend/e2e"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

// config is every flag, in one place (CLAUDE.md).
type config struct {
	databaseURL string
	rfcDir      string
	seedDir     string
	schema      string

	// seeds are the fixture seed sets to load, in order. They are not
	// commutative — scored_population extends alice_go_primary — so the order
	// given is the order applied.
	seeds []string

	// reset drops the schema first. A developer re-running this after
	// changing a delta wants the new shape, not a half-applied merge of two.
	reset bool

	// controlURL is cmd/fakethirdparty, when one is running. The seeded cast
	// has to exist on BOTH sides: the database holds the accounts, and the
	// stand-in holds the GitHub identities they sign in through. Seeding one
	// without the other produces a login that resolves to nobody.
	controlURL string
}

func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var c config

	cmd := &cobra.Command{
		Use:   "devdata",
		Short: "Apply the schema and seed a development database",
		Long: "Builds the same database shape the fixture suite runs against, " +
			"so a client developed against it is developed against the pinned contract.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), c)
		},
	}

	f := cmd.Flags()
	f.StringVar(&c.databaseURL, "database-url", "", "postgres connection string")
	f.StringVar(&c.rfcDir, "rfc-dir", "../rfc", "directory holding the RFC-*.schema deltas")
	f.StringVar(&c.schema, "schema", "public", "postgres schema to build in")
	f.StringVar(&c.seedDir, "seed-dir", "e2e/fixtures/seed",
		"directory holding the seed sets — the same files the fixture suite loads")
	f.StringSliceVar(&c.seeds, "seed", []string{"principals", "catalogue", "repositories"},
		"fixture seed sets to load, in order")
	f.BoolVar(&c.reset, "reset", false, "drop the schema before applying")
	f.StringVar(&c.controlURL, "control-url", "",
		"cmd/fakethirdparty's base URL; when set, the same principals and "+
			"repositories are loaded into it so sign-in and PR enrichment work")

	return cmd
}

func run(ctx context.Context, cfg config) error {
	if cfg.databaseURL == "" {
		return errors.New("--database-url is required")
	}

	// The seed loader reads relative to this, and the suite and this binary run
	// from different directories.
	e2e.SeedRoot = cfg.seedDir

	poolConfig, err := postgres.PoolConfig(cfg.databaseURL)
	if err != nil {
		return err
	}

	// Simple protocol, for the same reason the harness uses it: this builds a
	// fresh set of enum types, and a pooled connection that cached their OIDs
	// from a previous shape fails against types that no longer exist.
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()

	if cfg.reset {
		if err := resetSchema(ctx, pool, cfg.schema); err != nil {
			return err
		}
	}

	// A development database PERSISTS, unlike the suites' throwaway schemas, so
	// the ordinary case is that it is already built. Re-applying the deltas
	// then fails on the first CREATE TYPE and re-seeding on the first primary
	// key — neither of which means anything is wrong.
	built, err := alreadyBuilt(ctx, pool, cfg.schema)
	if err != nil {
		return err
	}

	if built {
		fmt.Printf("schema %q is already built — left alone (--reset rebuilds it)\n", cfg.schema)
	} else {
		statements, err := e2e.LoadDDL(cfg.rfcDir)
		if err != nil {
			return fmt.Errorf("reading the schema deltas: %w", err)
		}
		if err := e2e.ApplyDDL(ctx, pool, cfg.schema, statements); err != nil {
			return fmt.Errorf("applying the schema: %w", err)
		}
		fmt.Printf("schema %q built from %d statements\n", cfg.schema, len(statements))

		if _, err := e2e.Seed(ctx, pool, cfg.schema, cfg.seeds); err != nil {
			return fmt.Errorf("seeding: %w", err)
		}
		fmt.Printf("seeded %s\n", strings.Join(cfg.seeds, ", "))
	}

	// Read back rather than taken from the seed's return value: on a database
	// that was already built there was no seed call to return them.
	bindings, err := seededIDs(ctx, pool, cfg.schema)
	if err != nil {
		return err
	}

	if cfg.controlURL != "" {
		if err := loadThirdParty(ctx, cfg); err != nil {
			return err
		}
		fmt.Printf("third-party stand-in loaded at %s\n", cfg.controlURL)
	}

	// The ids are printed because a client developer needs them: they are what
	// a hand-written request references, and nothing else in the running
	// system reports them.
	report(bindings)
	return nil
}

// loadThirdParty gives the stand-in the same cast the database just got.
func loadThirdParty(ctx context.Context, cfg config) error {
	// Control reads its address from the environment, which is how the
	// fixture suite passes it. Setting it here keeps one resolution rule
	// rather than two.
	if err := os.Setenv("E2E_CONTROL_URL", cfg.controlURL); err != nil {
		return err
	}

	principals, err := e2e.LoadPrincipals()
	if err != nil {
		return fmt.Errorf("reading the seeded principals: %w", err)
	}

	control := e2e.NewControl(&http.Client{Timeout: 10 * time.Second})
	if err := control.LoadCase(ctx, principals, cfg.seeds, nil); err != nil {
		return fmt.Errorf("loading the third-party stand-in: %w", err)
	}
	return nil
}

// alreadyBuilt reports whether the schema holds this platform's tables.
//
// Keyed on `users` rather than on a migration record, because there is no
// migration tool to keep one (ADR-0006): the deltas applied in RFC order ARE
// the schema, and the only evidence they ran is what they created.
func alreadyBuilt(ctx context.Context, pool *pgxpool.Pool, schema string) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.tables
		    WHERE table_schema = $1 AND table_name = 'users'
		)`, schema).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking whether %s is built: %w", schema, err)
	}
	return exists, nil
}

// seededIDs reads back the accounts a developer will reference by hand.
func seededIDs(ctx context.Context, pool *pgxpool.Pool, schema string) (map[string]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	out := map[string]string{}
	for _, source := range []struct{ key, query string }{
		{"users", `SELECT display_name, id::text FROM users`},
		{"hirers", `SELECT email, id::text FROM hirer_accounts`},
		{"admins", `SELECT email, id::text FROM admin_accounts`},
		{"orgs", `SELECT name, id::text FROM organizations`},
	} {
		rows, err := conn.Query(ctx, source.query)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", source.key, err)
		}
		for rows.Next() {
			var label, id string
			if err := rows.Scan(&label, &id); err != nil {
				rows.Close()
				return nil, err
			}
			out[label] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// resetSchema drops and recreates, so a re-run after a delta changed lands on
// the new shape rather than merging into the old one.
func resetSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	for _, statement := range []string{
		fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema),
		fmt.Sprintf("CREATE SCHEMA %q", schema),
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("resetting schema %s: %w", schema, err)
		}
	}
	return nil
}

// report prints the seeded accounts a developer will need by hand.
func report(bindings map[string]string) {
	labels := make([]string, 0, len(bindings))
	for label := range bindings {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	fmt.Println("\nseeded accounts:")
	for _, label := range labels {
		fmt.Printf("  %-28s %s\n", label, bindings[label])
	}
}
