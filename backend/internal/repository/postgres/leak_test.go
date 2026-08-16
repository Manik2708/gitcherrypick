package postgres_test

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain closes the shared pool and then fails the package if any goroutine
// outlived the suite.
//
// Order matters, and it is the whole reason this is not a plain
// goleak.VerifyTestMain(m): that helper runs m.Run() and checks immediately,
// which would catch the pool's own background goroutines still running —
// because nothing had closed the pool. The tempting fix is to add
// goleak.IgnoreTopFunction for each of them, and that is the wrong fix twice
// over. It leaves the pool leaked, and it blinds the check to a genuinely
// leaked pool somewhere else, which is exactly the bug worth catching.
//
// So: run, release what the suite owns, then look for what is left. There are
// no ignore options, and there should never need to be.
//
// A leak here is not cosmetic. An unclosed pgx.Rows keeps its connection
// checked out forever; the symptom is pool exhaustion hours later under load,
// with no stack pointing at the cause. goleak turns that into a failure while
// the change that caused it is still on screen.
func TestMain(m *testing.M) {
	code := m.Run()

	// Whatever the suite opened, the suite closes. closePool is a no-op when
	// no test ever asked for a database — the skip path when Docker is absent.
	closePool()

	// Only on success. A failing suite may have abandoned goroutines precisely
	// because it failed, and reporting those on top of the real failure buries
	// it.
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
			code = 1
		}
	}

	os.Exit(code)
}
