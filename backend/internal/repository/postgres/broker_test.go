package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestBrokerOutbox(t *testing.T) {
	t.Run("a rolled-back submission enqueues nothing", func(t *testing.T) {
		// The property Publish takes a Tx for (ADR-0004). A job for a claim
		// that was rolled back is the worse failure: it spends a model call on
		// evidence nobody submitted.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		sentinel := errors.New("validation failed")
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			if err := db.Queue().Publish(ctx, tx, port.Message{
				ClaimID: claim.ID, Version: 1}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected the sentinel, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM evaluation_jobs`); n != 0 {
			t.Errorf("expected nothing enqueued, found %d job(s)", n)
		}
	})

	t.Run("a committed submission enqueues exactly one", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustPublish(ctx, t, db, claim.ID, 1)
		if n := count(t, db, `SELECT count(*) FROM evaluation_jobs`); n != 1 {
			t.Errorf("expected 1 job, found %d", n)
		}
	})
}

func TestBrokerConsume(t *testing.T) {
	t.Run("leases and carries the claim through", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		got, err := db.Queue().Consume(ctx, time.Minute, 10)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 message, got %d", len(got))
		}
		if got[0].ClaimID != claim.ID {
			t.Errorf("expected claim %s, got %s", claim.ID, got[0].ClaimID)
		}
		if got[0].Attempts != 1 {
			t.Errorf("expected attempt 1, got %d", got[0].Attempts)
		}
	})

	t.Run("a leased message is not redelivered while the lease holds", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		if _, err := db.Queue().Consume(ctx, time.Hour, 10); err != nil {
			t.Fatalf("first consume: %v", err)
		}
		again, err := db.Queue().Consume(ctx, time.Hour, 10)
		if err != nil {
			t.Fatalf("second consume: %v", err)
		}
		if len(again) != 0 {
			t.Errorf("a leased message was redelivered: %+v", again)
		}
	})

	t.Run("an expired lease redelivers, which is why handlers must be idempotent", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		if _, err := db.Queue().Consume(ctx, time.Minute, 10); err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if _, err := db.Pool().Exec(ctx,
			`UPDATE evaluation_jobs SET leased_until = now() - interval '1 minute'`); err != nil {
			t.Fatalf("expiring the lease: %v", err)
		}

		again, err := db.Queue().Consume(ctx, time.Minute, 10)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if len(again) != 1 {
			t.Fatalf("expected redelivery after the lease expired, got %d", len(again))
		}
		if again[0].Attempts != 2 {
			t.Errorf("expected attempt 2, got %d", again[0].Attempts)
		}
	})

	t.Run("two workers never take the same message", func(t *testing.T) {
		// FOR UPDATE SKIP LOCKED. Without it both would read the same
		// claimable row and both would process it.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		const jobs = 20
		for i := range jobs {
			claim := mustCreateDraft(ctx, t, db, user.ID)
			mustPublish(ctx, t, db, claim.ID, i+1)
		}

		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			seen = map[domain.JobID]int{}
		)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					got, err := db.Queue().Consume(ctx, time.Hour, 3)
					if err != nil || len(got) == 0 {
						return
					}
					mu.Lock()
					for _, m := range got {
						seen[m.ID]++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		for id, n := range seen {
			if n != 1 {
				t.Errorf("job %s was delivered %d times concurrently", id, n)
			}
		}
		if len(seen) != jobs {
			t.Errorf("expected all %d jobs consumed, got %d", jobs, len(seen))
		}
	})

	t.Run("an exhausted message stops being claimable", func(t *testing.T) {
		// A poisonous message that retried forever would starve the queue
		// behind it. The row stays so a human can find it.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		if _, err := db.Pool().Exec(ctx,
			`UPDATE evaluation_jobs SET attempt = max_attempts`); err != nil {
			t.Fatalf("exhausting: %v", err)
		}

		got, err := db.Queue().Consume(ctx, time.Minute, 10)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if len(got) != 0 {
			t.Error("an exhausted message was delivered again")
		}
		if n := count(t, db, `SELECT count(*) FROM evaluation_jobs`); n != 1 {
			t.Error("the exhausted row must stay so a human can find it")
		}
	})
}

func TestBrokerAckNack(t *testing.T) {
	t.Run("ack joins the transaction that did the work", func(t *testing.T) {
		// Acking separately would leave a window where the job is gone and the
		// work rolled back.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		got, err := db.Queue().Consume(ctx, time.Minute, 1)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}

		sentinel := errors.New("persist failed")
		err = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			if err := db.Queue().Ack(ctx, tx, got[0].ID); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected the sentinel, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM evaluation_jobs`); n != 1 {
			t.Error("the ack survived a rolled-back transaction")
		}
	})

	t.Run("nack releases the lease and backs off", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		got, err := db.Queue().Consume(ctx, time.Hour, 1)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if err := db.Queue().Nack(ctx, got[0].ID, "github unreachable"); err != nil {
			t.Fatalf("nacking: %v", err)
		}

		// Released, but not immediately claimable — that is the backoff.
		again, err := db.Queue().Consume(ctx, time.Hour, 1)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if len(again) != 0 {
			t.Error("a nacked message must wait rather than spin")
		}

		var lastError string
		scanRow(ctx, t, db, []any{&lastError}, `SELECT last_error FROM evaluation_jobs`)
		if lastError != "github unreachable" {
			t.Errorf("expected the failure recorded, got %q", lastError)
		}
	})

	t.Run("acking an unknown job reports not found", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Queue().Ack(ctx, tx, "01920000-0000-7000-8000-00000000dead")
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})
}

func TestBrokerExtendLease(t *testing.T) {
	t.Run("extends a lease this worker holds", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		got, err := db.Queue().Consume(ctx, time.Minute, 1)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}

		var before time.Time
		scanRow(ctx, t, db, []any{&before}, `SELECT leased_until FROM evaluation_jobs`)

		if err := db.Queue().ExtendLease(ctx, got[0].ID, time.Hour); err != nil {
			t.Fatalf("extending: %v", err)
		}

		var after time.Time
		scanRow(ctx, t, db, []any{&after}, `SELECT leased_until FROM evaluation_jobs`)
		if !after.After(before) {
			t.Errorf("expected the lease extended, %s to %s", before, after)
		}
	})

	t.Run("cannot extend a lease that already expired and was taken", func(t *testing.T) {
		// A worker whose lease lapsed must not reclaim it underneath whoever
		// picked the job up.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)
		mustPublish(ctx, t, db, claim.ID, 1)

		got, err := db.Queue().Consume(ctx, time.Minute, 1)
		if err != nil {
			t.Fatalf("consuming: %v", err)
		}
		if _, err := db.Pool().Exec(ctx,
			`UPDATE evaluation_jobs SET leased_until = now() - interval '1 minute', leased_by = 'another-worker'`); err != nil {
			t.Fatalf("expiring: %v", err)
		}

		if err := db.Queue().ExtendLease(ctx, got[0].ID, time.Hour); !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})
}

func TestSystemClock(t *testing.T) {
	// UTC always. A server whose local zone changed under it would otherwise
	// move every deadline in the system.
	if got := postgres.SystemClock().Now(); got.Location() != time.UTC {
		t.Errorf("expected UTC, got %s", got.Location())
	}
}

// --- helpers -----------------------------------------------------------------

func mustPublish(ctx context.Context, t *testing.T, db *postgres.DB, claim domain.ClaimID, version int) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Queue().Publish(ctx, tx, port.Message{ClaimID: claim, Version: version})
	}); err != nil {
		t.Fatalf("publishing: %v", err)
	}
}
