package postgres_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if any goroutine outlives the suite.
//
// A repository is where leaks come from: a pgx pool holds a health-check
// goroutine per connection, an unclosed pgx.Rows keeps its connection checked
// out forever, and a context whose cancel is never called leaks a timer. None
// of those fail a test — they exhaust the pool in production, hours later,
// under load, with no stack pointing at the cause.
//
// goleak turns that into a failure here, where the change that caused it is
// still on screen.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// The pool is opened once and shared by every test, so its own
		// goroutines are alive by design at the point goleak looks. They are
		// bounded and owned — the leaks worth catching are the unbounded ones
		// a single test creates and abandons.
		goleak.IgnoreTopFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).backgroundHealthCheck"),
		goleak.IgnoreTopFunction("github.com/jackc/puddle/v2.(*Pool[...]).backgroundHealthCheck"),
		goleak.IgnoreAnyFunction("github.com/jackc/pgx/v5/pgxpool.(*Pool).triggerHealthCheck.func1"),
	)
}
