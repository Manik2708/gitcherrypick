package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Broker is the work queue, as a Postgres table (ADR-0006).
//
// A table rather than a message broker because the enqueue MUST join the
// transaction that created the work. With a separate broker that is the
// outbox pattern and a relay process; with a table it is one INSERT, and the
// property is free.
//
// Nothing above port.Broker knows this. The evaluator receives messages and
// never references a queue vendor (CLAUDE.md).
type Broker struct{ db *DB }

// jobPayload is the part of a job's JSON body the broker itself reads.
//
// The payload is otherwise opaque to the queue — the handler owns the rest.
// These two fields are lifted onto port.Message so a consumer can route and
// version-check without unmarshalling the body a second time.
type jobPayload struct {
	ClaimID string `json:"claim_id"`
	Version int    `json:"version"`
}

// Queue returns the broker.
func (db *DB) Queue() *Broker { return &Broker{db: db} }

var _ port.Broker = (*Broker)(nil)

// DefaultLease is how long a worker holds a message before it is redelivered.
//
// Redelivery on expiry is what makes a crashed worker recoverable, and it is
// also why every handler must be idempotent: a lease that expires while work
// is still running produces a second delivery of a job already half-done.
const DefaultLease = 5 * time.Minute

// Publish enqueues, joining the caller's transaction.
//
// The Tx parameter is the whole point (ADR-0004). Without it a claim can be
// submitted with no job, or a job can exist for a claim that was rolled back —
// and the second is worse, because it spends a model call on evidence nobody
// submitted.
func (b *Broker) Publish(ctx context.Context, t port.Tx, msg port.Message) error {
	id := msg.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generating job id: %w", err)
		}
		id = domain.JobID(generated.String())
	}

	topic := msg.Kind
	if topic == "" {
		topic = "claims.live"
	}

	payload := msg.Payload
	if len(payload) == 0 {
		encoded, err := json.Marshal(map[string]any{
			"claim_id": string(msg.ClaimID),
			"version":  msg.Version,
		})
		if err != nil {
			return fmt.Errorf("encoding payload: %w", err)
		}
		payload = encoded
	}

	if _, err := b.db.q(t).Exec(ctx,
		`INSERT INTO evaluation_jobs (id, topic, payload) VALUES ($1, $2, $3)`,
		string(id), topic, string(payload)); err != nil {
		return translate(err, "publishing job")
	}
	return nil
}

// Consume leases claimable messages.
//
// SKIP LOCKED is a THROUGHPUT property, not a correctness one. Verified:
// dropping it leaves the two-workers-never-take-the-same-message test passing,
// because plain FOR UPDATE makes the second worker block and then re-evaluate
// against the committed state, so it still cannot take a claimed row.
//
// What it buys is that the second worker skips that row and takes a different
// one instead of waiting on the head of the queue. With four workers and a
// deep queue that is the difference between four-way parallelism and one.
//
// leased_until is NOT NULL DEFAULT '-infinity' rather than nullable — which is
// why the predicate is one sargable comparison instead of
// `leased_until IS NULL OR leased_until < now()`. That OR is also what made an
// earlier index predicate non-IMMUTABLE and unbuildable.
//
// Delivery is AT-LEAST-ONCE. A worker that dies after processing but before
// Ack will see the message again, so handlers must be idempotent.
func (b *Broker) Consume(ctx context.Context, lease time.Duration, limit int) ([]port.Message, error) {
	if lease <= 0 {
		lease = DefaultLease
	}
	if limit <= 0 {
		limit = 1
	}

	rows, err := b.db.pool.Query(ctx, `
		WITH claimable AS (
		    SELECT id
		    FROM evaluation_jobs
		    WHERE leased_until < now()
		      AND available_at <= now()
		      AND attempt < max_attempts
		    ORDER BY available_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		)
		UPDATE evaluation_jobs j
		SET leased_until = now() + $2::interval,
		    leased_by    = $3,
		    attempt      = j.attempt + 1,
		    updated_at   = now()
		FROM claimable c
		WHERE j.id = c.id
		RETURNING j.id, j.topic, j.payload, j.attempt`,
		limit, lease.String(), leaseHolder())
	if err != nil {
		return nil, translate(err, "consuming jobs")
	}
	defer rows.Close()

	var out []port.Message
	for rows.Next() {
		var (
			msg     port.Message
			payload []byte
		)
		if err := rows.Scan(&msg.ID, &msg.Kind, &payload, &msg.Attempts); err != nil {
			return nil, translate(err, "scanning job")
		}
		msg.Payload = payload

		var decoded jobPayload
		if err := json.Unmarshal(payload, &decoded); err == nil {
			msg.ClaimID = domain.ClaimID(decoded.ClaimID)
			msg.Version = decoded.Version
		}
		out = append(out, msg)
	}
	return out, translate(rows.Err(), "consuming jobs")
}

// Ack removes a completed message, joining the caller's transaction.
//
// In the SAME transaction as the work it completed. Acking separately would
// leave a window where the work is committed and the job is not — producing a
// redelivery of something already done, which is survivable — or the job gone
// and the work rolled back, which is not.
func (b *Broker) Ack(ctx context.Context, t port.Tx, id domain.JobID) error {
	tag, err := b.db.q(t).Exec(ctx, `DELETE FROM evaluation_jobs WHERE id = $1`, string(id))
	if err != nil {
		return translate(err, fmt.Sprintf("acking job %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s: %w", id, port.ErrNotFound)
	}
	return nil
}

// Nack returns a message for retry with exponential backoff.
//
// The lease is released immediately and available_at pushed out, so a failing
// message does not spin: it waits, and each attempt waits longer. Once
// attempts are exhausted the row stays put and stops being claimable, so a
// human can find it rather than it vanishing.
func (b *Broker) Nack(ctx context.Context, id domain.JobID, reason string) error {
	tag, err := b.db.pool.Exec(ctx, `
		UPDATE evaluation_jobs
		SET leased_until = '-infinity',
		    leased_by    = NULL,
		    available_at = now() + (interval '1 minute' * power(2, least(attempt, 6))),
		    last_error   = $2,
		    updated_at   = now()
		WHERE id = $1`, string(id), reason)
	if err != nil {
		return translate(err, fmt.Sprintf("nacking job %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s: %w", id, port.ErrNotFound)
	}
	return nil
}

// ExtendLease keeps a long-running job from being redelivered underneath the
// worker still processing it.
//
// Guarded on the CURRENT holder: a worker whose lease already expired and was
// taken by someone else must not be able to extend a lease it no longer owns.
func (b *Broker) ExtendLease(ctx context.Context, id domain.JobID, by time.Duration) error {
	tag, err := b.db.pool.Exec(ctx, `
		UPDATE evaluation_jobs
		SET leased_until = now() + $2::interval, updated_at = now()
		WHERE id = $1 AND leased_by = $3 AND leased_until > now()`,
		string(id), by.String(), leaseHolder())
	if err != nil {
		return translate(err, fmt.Sprintf("extending the lease on %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s is not leased by this worker: %w", id, port.ErrConflict)
	}
	return nil
}

// leaseHolder identifies this worker in leased_by.
//
// Diagnostic, not a lock: correctness comes from leased_until and
// SKIP LOCKED. It exists so a stuck job can be traced to a process.
func leaseHolder() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

// Clock is the real clock, behind port.Clock.
//
// Everything else in the system takes this as an interface so the seven-day
// lock, the 15-day availability window, lease expiry and the escalating
// cooldown are testable without waiting.
type Clock struct{}

// SystemClock returns the real clock.
func SystemClock() Clock { return Clock{} }

var _ port.Clock = Clock{}

// Now returns the current time in UTC.
//
// UTC always. A server whose local zone changed under it would otherwise move
// every deadline in the system.
func (Clock) Now() time.Time { return time.Now().UTC() }
