// Package port holds every interface that crosses a layer boundary.
//
// It is the ONLY package both sides import. A controller imports port and never
// a service implementation; a service imports port and never a repository
// implementation. Nothing in here mentions pgx, chi, cobra, Anthropic or
// GitHub — the whole point is that a dependency can be swapped without the
// layer above noticing.
//
// Nothing here has an implementation. Interfaces come first so that mocks can
// be generated before any concrete type exists, and so a test cannot invent a
// shortcut around a seam that has not been defined yet.
package port

import (
	"context"
	"time"
)

// Tx is a transaction handle passed BETWEEN repositories, so several writes
// join one atomic unit without any of them knowing which database is behind it.
//
// It is deliberately opaque. A service composes a transaction; only a
// repository implementation knows what the handle really is.
type Tx interface {
	// TxHandle is a marker with no behaviour.
	//
	// It exists so that Tx cannot be satisfied by accident — an empty
	// interface would accept anything, including a nil that silently ran
	// outside the caller's transaction.
	//
	// Commit and Rollback are deliberately absent. They belong to TxManager,
	// which owns the lifetime; a service that could commit could also forget
	// to, and a half-committed claim submission is exactly what the outbox
	// property exists to prevent.
	TxHandle()
}

// TxManager runs a function inside one transaction.
//
// The signature forces the correct shape: there is no Begin that a service
// could leak. If fn returns an error the transaction rolls back; if it panics
// the transaction rolls back and the panic continues.
type TxManager interface {
	InTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Clock is time, as an interface.
//
// Every deadline in the system is testable only because of this: the seven-day
// claim lock, the 15-day availability window, job lease expiry, invitation
// expiry, and the escalating re-evaluation cooldown. The alternative is a test
// that sleeps, which is a flake with a timer attached.
type Clock interface {
	Now() time.Time
}
