package postgres_test

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// Leak detection, and nothing else.
//
// This file does not open, close, or know about a database. Every test owns
// its own pool and closes it through t.Cleanup (see harness_test.go), so by
// the time TestMain looks, anything still running is a leak — including a pool
// a test forgot to close, which is the failure mode worth catching here.
//
// That is also why there are no goleak ignore options. Ignoring pgxpool's
// background goroutines would excuse exactly the bug this exists to find.

// keepDBEnv is set by db-test.sh --keep-db.
//
// That flag means the developer is debugging and wants the database left
// alive. Reporting a leak in that mode would be reporting the thing they asked
// for, so the check is skipped.
const keepDBEnv = "DB_TEST_KEEP_DB"

func TestMain(m *testing.M) {
	code := m.Run()

	if os.Getenv(keepDBEnv) == "1" {
		fmt.Fprintf(os.Stderr, "goleak: skipped (%s=1 — the database is being kept alive on purpose)\n", keepDBEnv)
		os.Exit(code)
	}

	// Unconditional, unlike goleak.VerifyTestMain, which only looks when the
	// suite passed. A failing suite can leak too, and learning about it only
	// after the failure is fixed means fixing in sequence two things that were
	// introduced together.
	if err := goleak.Find(); err != nil {
		fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
		code = 1
	}

	os.Exit(code)
}
