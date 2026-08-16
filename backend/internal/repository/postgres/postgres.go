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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DB owns the connection pool and hands out repositories.
type DB struct {
	pool *pgxpool.Pool
}

// New wraps an existing pool. Opening the pool is the caller's job: a
// repository package that dialled a database would be deciding configuration
// that belongs to main (CLAUDE.md — config comes from CLI flags).
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

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

// querier is what every repository method actually runs against: either the
// pool or a caller's transaction.
//
// This is how a method joins someone else's transaction without knowing
// whether it is in one. A repository takes port.Tx, calls db.q(t), and writes
// one query — rather than one query and a near-identical transactional twin.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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
