// Package postgres implements the repository interfaces in internal/port
// against PostgreSQL 16.
//
// Everything above this package sees only the interfaces. A service takes a
// port.UserRepository and cannot tell whether pgx, a fake, or a different
// database is behind it — which is the whole point of the layering, and the
// reason translating between domain types and storage rows is this package's
// job rather than a service's.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DB owns the connection pool and hands out repositories.
type DB struct {
	pool  *pgxpool.Pool
	clock port.Clock
}

// PoolConfig parses a DSN and applies the session invariants this package
// depends on. Dialling stays the caller's job (CLAUDE.md — config comes from
// CLI flags); what belongs here is the settings the SQL below assumes.
//
// Reading timestamps in UTC is the one that matters. pgx decodes timestamptz
// through time.Unix, which yields the GO PROCESS's local zone — so a server in
// Asia/Kolkata emits +05:30 and one in Europe/Dublin emits Z for the same
// instant. The moment is identical; the string is not, and a client diffing two
// deployments or a snapshot pinning a value sees two different answers.
//
// Fixed by the scan codec rather than by SET timezone: the session zone governs
// what Postgres renders as text, and pgx uses the binary format, so the session
// setting never reaches this decision. Both are set anyway — the session one so
// server-side to_char and now()::text agree with the wire.
func PoolConfig(dsn string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing the database url: %w", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"

	config.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		ScanTimestampsInUTC(conn.TypeMap())
		return nil
	}
	return config, nil
}

// ScanTimestampsInUTC makes every timestamptz on this connection decode to UTC.
//
// Exported because the test harness builds its own pool config and needs the
// same guarantee; a suite reading +05:30 while production reads Z would be
// testing a different serialization than it ships.
func ScanTimestampsInUTC(m *pgtype.Map) {
	scalar := &pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	}
	m.RegisterType(scalar)
	m.RegisterType(&pgtype.Type{
		Name:  "timestamptz[]",
		OID:   pgtype.TimestamptzArrayOID,
		Codec: &pgtype.ArrayCodec{ElementType: scalar},
	})
}

// New wraps an existing pool. Opening the pool is the caller's job: a
// repository package that dialled a database would be deciding configuration
// that belongs to main (CLAUDE.md — config comes from CLI flags).
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// WithClock tells the repository what time it is.
//
// Every predicate that compares a stored timestamp against "now" — is this
// availability live, was this PR merged recently, has this session expired —
// is a BUSINESS rule, and ADR-0012 makes the platform's clock a redirected
// dependency. SQL now() answers from the database's clock instead, which is a
// second source of truth that nothing else in the system reads and no test can
// move. Writes still use now(): stamping a row with when it was written is the
// database's own business.
func (db *DB) WithClock(c port.Clock) *DB {
	db.clock = c
	return db
}

// Pool exposes the underlying pool for the few callers that legitimately need
// it — health checks and the test harness. Business code takes a repository.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// tx is the concrete handle behind port.Tx.
//
// port.Tx is deliberately opaque and has an unexported method, so only this
// package can produce one. A service can pass a Tx from one repository to
// another and cannot open, commit or roll one back itself — which is what
// keeps transaction boundaries in the service layer's control flow rather than
// scattered through repositories.
type tx struct {
	pgx.Tx
}

// TxHandle marks this as a port.Tx. It has no behaviour.
func (tx) TxHandle() {}

// InTx runs fn inside one transaction.
//
// There is no exported Begin. The closure shape is what guarantees a
// transaction cannot be left open: fn returning an error rolls back, and a
// panic rolls back and continues panicking rather than leaving the connection
// wedged mid-transaction.
func (db *DB) InTx(ctx context.Context, fn func(ctx context.Context, t port.Tx) error) error {
	pgxTx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Best effort, and on a fresh context: the caller's may already be
		// cancelled, which is exactly when a rollback matters most.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = pgxTx.Rollback(rollbackCtx)
	}()

	if err := fn(ctx, tx{pgxTx}); err != nil {
		return err
	}

	if err := pgxTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// Querier is the minimal query surface a repository method runs against:
// either the pool or a caller's transaction.
//
// This is how a method joins someone else's transaction without knowing
// whether it is in one. A repository takes port.Tx, calls db.q(t), and writes
// one query — rather than one query and a near-identical transactional twin.
//
// Exported as an alias so the integration harness can seed rows through the
// SAME code the API writes them with. A seed that reimplemented a derived
// value would be asserting its own copy of the rule rather than the one under
// test — which is how a fingerprint written as md5(claim_id) sat in the seed
// while the service hashed the evidence.
type Querier = querier

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// activeRubricVersion is the version scores are currently compared against.
//
// Read from the sweep history rather than from configuration: search gates on
// it (ADR-0008 §1) and a value that lived in a flag could differ between two
// processes serving the same corpus. With no sweep recorded it is the baseline
// — the first rubric, which is what every score predates a sweep under.
func (db *DB) activeRubricVersion(ctx context.Context) (string, error) {
	var version string
	err := db.pool.QueryRow(ctx, `
		SELECT to_rubric_version FROM rubric_sweeps
		ORDER BY created_at DESC
		LIMIT 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BaselineRubricVersion, nil
	}
	if err != nil {
		return "", translate(err, "reading the active rubric version")
	}
	return version, nil
}

// unversionedRubricVersion is what a score with no recorded evaluation counts
// as.
//
// Normally the active version: a seeded or hand-written score has not been
// superseded by anything. While a sweep is DRAINING it counts as nothing —
// the platform has moved and this score has not been re-judged, so showing it
// as current would put a v1 number on a v2 board (ADR-0008 §1).
func (db *DB) unversionedRubricVersion(ctx context.Context, active string) (string, error) {
	var sweeping bool
	if err := db.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM rubric_sweeps WHERE completed_at IS NULL)`).
		Scan(&sweeping); err != nil {
		return "", translate(err, "checking for a running sweep")
	}
	if sweeping {
		// A version string no evaluation can carry, so the comparison fails
		// for every unversioned score without a special case in the SQL.
		return "", nil
	}
	return active, nil
}

// q resolves a possibly-nil Tx to something runnable.
func (db *DB) q(t port.Tx) querier {
	if t == nil {
		return db.pool
	}
	inner, ok := t.(tx)
	if !ok {
		// Unreachable: port.Tx cannot be implemented outside this package.
		// Panicking beats silently running outside the caller's transaction,
		// which would break atomicity without any visible symptom.
		panic(fmt.Sprintf("postgres: foreign port.Tx implementation %T", t))
	}
	return inner.Tx
}

// A NOTE ON PARAMETER CASTS
//
// A $n used in two contexts that imply different types fails at execution with
// "inconsistent types deduced for parameter $n" (SQLSTATE 42P08). The usual
// shapes are an enum in a SET and text in a comparison, or a uuid in a column
// and text inside a function:
//
//	SET status = $2, ... WHERE $2 = 'queued'        -- fails
//	SET status = $2::claim_status, ... $2::claim_status = 'queued'   -- fine
//
// It has caught this package three times. Cast the parameter explicitly
// EVERYWHERE it appears, not just where the type is ambiguous.

// --- error translation -------------------------------------------------------

// Callers distinguish cases with errors.Is against the sentinels in port. A
// service that inspected a *pgconn.PgError would know which database it was
// talking to, which is the coupling this layer exists to prevent.

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
	checkViolation      = "23514"
)

// translate maps a driver error to a port sentinel, wrapping the original so
// the constraint name survives for a human reading the failure.
func translate(err error, context string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", context, port.ErrNotFound)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolation:
			return fmt.Errorf("%s: %s: %w", context, pgErr.ConstraintName, port.ErrConflict)
		case foreignKeyViolation:
			return fmt.Errorf("%s: %s: %w", context, pgErr.ConstraintName, port.ErrNotFound)
		case checkViolation:
			return fmt.Errorf("%s: %s: %w", context, pgErr.ConstraintName, port.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", context, err)
}

// rollbackTimeout bounds the deferred rollback in InTx. Short: if the database
// is unreachable the connection is already lost, and blocking here would turn
// one failure into a hang.
const rollbackTimeout = 5 * time.Second

// isNotFound reports a no-rows result, before translation.
func isNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// now is the platform's clock, or the host's when none was injected.
//
// Every value computed in Go before being written or compared — the cooldown
// escalation, how long a lapsed contributor has been gone — reads from here.
// The DATABASE's now() still stamps rows, which is what keeps timestamps
// consistent across statements within one transaction.
func (db *DB) now() time.Time {
	if db.clock == nil {
		return time.Now().UTC()
	}
	return db.clock.Now()
}
