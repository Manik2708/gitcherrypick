# RFC-0004 — Evaluation pipeline & broker

**Status:** Approved 2026-08-02 · **Binding form:** [ADR-0004-evaluation-pipeline](../adr/ADR-0004-evaluation-pipeline.md) · **Schema:** [RFC-0004-evaluation-pipeline.schema](RFC-0004-evaluation-pipeline.schema)
· **Depends on:** RFC-0001, RFC-0003

## Summary

How a validated claim becomes scores: the message that carries it, the pipeline that
processes it, and the guarantees around reproducibility and retries. This RFC defines the
**mechanism**; what earns points is the rubric in `evaluation/`, consumed here as versioned
configuration, and the formulas are RFC-0005.

## Everything goes through the Batch API

All evaluation — live submissions included — is dispatched through the Batch API at roughly
half the cost of synchronous calls.

**A contributor submits a claim and waits, up to a hard ceiling of 24 hours.** That is a
deliberate product decision: scoring quality matters more than scoring latency, and the
saving funds a stronger model. A paid tier for immediate or prioritised scoring is the
intended eventual shape; it does not exist in v1.

Three consequences the implementation must own:

**The UI is notify-based, not wait-based.** Submission confirms receipt and says when to
expect a result. Email notification on completion is therefore in v1 scope, not a later
addition.

**Batches accumulate over a window.** A batch holding one claim saves nothing in wall-clock
terms, so submissions collect for a dispatch window (initially 15 minutes) before being
sent. During quiet periods the window closes on whatever is present rather than waiting for
volume — an early user should not wait longest for the least benefit.

**24 hours is our ceiling, not just the provider's.** A batch still unreturned at 20 hours
is escalated: its claims are re-dispatched synchronously so the ceiling holds. Without
this, the provider's own 24-hour bound becomes our floor on the worst day.

## The broker abstraction

The evaluator must not know what carries its messages. Per `CLAUDE.md`, the broker is an
interface, and the v1 implementation is a table in our own database — a queue is not worth
a Kafka dependency at this scale, and the outbox property below is free when the queue and
the data share a transaction.

```go
type Message struct {
    ID         string
    Payload    []byte
    Attempt    int
    EnqueuedAt time.Time
}

type Publisher interface {
    Publish(ctx context.Context, topic string, payload []byte) error
}

type Consumer interface {
    // Receive leases up to max messages, making them invisible to other
    // consumers until the lease expires or they are acked.
    Receive(ctx context.Context, topic string, max int) ([]Message, error)
    Ack(ctx context.Context, m Message) error
    Nack(ctx context.Context, m Message, retryAfter time.Duration) error
}
```

Nothing above names a database or a queue product.

### The Postgres implementation, concretely

The queue is one table. Publishing is an `INSERT`. Consuming is a single statement:

```sql
UPDATE evaluation_jobs SET
    leased_until = now() + interval '10 minutes',
    leased_by    = $worker,
    attempt      = attempt + 1
WHERE id IN (
    SELECT id FROM evaluation_jobs
     WHERE topic = $1
       AND available_at <= now()
       AND leased_until < now()
     ORDER BY available_at
     FOR UPDATE SKIP LOCKED
     LIMIT $2
)
RETURNING id, payload, attempt;
```

`FOR UPDATE SKIP LOCKED` is what makes this a queue rather than a table: each worker's
`SELECT` skips rows another worker has locked instead of blocking on them, so N workers
claim N disjoint sets in parallel with no coordination. Postgres has supported it since
9.5, and it is the same mechanism most lightweight job libraries are built on.

`leased_until` is `NOT NULL DEFAULT '-infinity'` rather than nullable, so "never leased"
and "lease expired" are the same comparison. The alternative — `leased_until IS NULL OR
leased_until < now()` — cannot use a composite index cleanly, and cannot be expressed as a
partial index at all, because `now()` is `STABLE` and Postgres rejects non-`IMMUTABLE`
functions in an index predicate.

**Leases, not deletes.** A claimed row sets `leased_until` and is deleted only on ack. A
worker that crashes mid-evaluation loses its lease and the job returns to the pool when it
expires. Deleting on read would silently drop a claim and leave the contributor waiting
forever.

- **Ack** = `DELETE FROM evaluation_jobs WHERE id = $1`.
- **Nack** = `UPDATE ... SET leased_until = '-infinity', available_at = now() + $delay`.
- **Backoff** is `available_at` in the future; the claim query simply does not see the row.

### The outbox property

The api enqueues the job **in the same transaction** that transitions the claim to
`queued`. Either both happen or neither does. With an external broker this needs the full
transactional-outbox pattern; here it is one `INSERT`. This is the strongest single
argument for the v1 choice.

## Pipeline

```
  receive ──▶ enrich ──▶ batch ──▶ judge ──▶ compute ──▶ persist ──▶ ack
             (GitHub)             (AI)      (Go)        (repo)
```

**1 · Receive.** Deserialize and validate. A malformed payload goes straight to the
dead-letter table; retrying will not fix it.

**2 · Enrich.** Read each PR's facts through the GitHub interface — diff statistics, review
conversation depth, participants, repository signals — and each project's hard signals.
Cached onto the evidence rows so a run is reproducible from stored data.

**Cached facts expire after 7 days.** Within that window the cache is reused; past it the
PR is re-enriched. Enrichment is a separate step from judging so that a model failure never
forces us to re-spend GitHub rate limit.

**3 · Batch.** Accumulate claims into a batch request over the dispatch window, submit, and
poll for completion.

**4 · Judge.** **One model call per claim**, scoring every PR against every declared skill
in that claim. Not one call per skill, and not one per PR: the interesting comparison —
how much of this work is really Kubernetes rather than Go — is only visible when the model
sees all the evidence and all the skills together. Structured outputs make the response
validate against a schema rather than be parsed from prose.

**The model returns judgements, never totals.** Per-PR, per-skill dimension scores and
rationale. It is never asked for a skill score, an overall score, or a weighted average.

**5 · Compute.** The evaluator does the arithmetic in Go, per RFC-0005: dimension scores ×
rubric weights → per-PR-per-skill score; those plus normalised arithmetic signals → skill
score; skill scores → overall score. Deterministic, unit-testable without a model, and
identical for everyone. Asking a language model to do weighted arithmetic would make scores
unauditable for no gain.

**6 · Persist.** Write the evaluation, per-PR-per-skill scores, recomputed `user_skills`
standings, and snapshots through the repository interface, in one transaction. Then ack.

### Skills that score zero are dropped here

Step 5 discards any skill scoring zero rather than writing it (RFC-0003). If the nominated
primary is among them, the claim persists with its surviving skills and the contributor is
told. No zero-scored `user_skills` row is ever written.

## Reproducibility

| Field                  | Why                                                 |
| ---------------------- | --------------------------------------------------- |
| `claim_version`        | Which version of the evidence was scored            |
| `rubric_version`       | Which rubric. Scores compare only within a version. |
| `model`                | Which model produced the judgements                 |
| `prompt_version`       | Which prompt template                               |
| `evidence_fingerprint` | Hash of the canonical evidence set                  |

The fingerprint hashes the sorted `owner/repo#number` triples, the project list, and the
declared skills. It gives idempotency free — a message whose fingerprint already has a
completed evaluation at the same rubric version is acked without re-spending — and it is
what refuses an unchanged re-submission (RFC-0003).

**Changing the rubric does not rewrite history.** Bumping `rubric_version` requires an
explicit re-evaluation sweep; until it completes, both versions coexist and ranking reads
filter to the active one.

**The arithmetic recompute is not an evaluation.** When platform-wide maxima move
(RFC-0005), only the relative component of existing scores is recalculated. It re-reads
nothing from GitHub, calls no model, and creates no evaluation row.

## Failure handling

| Failure                             | Response                                                                 |
| ----------------------------------- | ------------------------------------------------------------------------ |
| Malformed payload                   | Dead-letter immediately. Not retryable.                                  |
| GitHub rate limit                   | Nack with the delay from `Retry-After`. Does not count as an attempt.    |
| GitHub 5xx / timeout                | Retry with exponential backoff + jitter.                                 |
| **One PR of five fails enrichment** | **Score the rest. See below.**                                           |
| Batch not returned by 20h           | Escalate to synchronous dispatch to hold the 24h ceiling.                |
| Model refusal                       | Dead-letter with the refusal category. An identical retry will not help. |
| Structured output fails validation  | Retry once, then dead-letter.                                            |
| Repository write failure            | Nack; the transaction rolled back, so retry is safe.                     |

Backoff is exponential with jitter from a 5-second base, capped at 15 minutes, **6
attempts**. Exhaustion dead-letters the job and moves the claim to `failed`, which is
admin-retryable. Jitter matters because a provider outage otherwise synchronises every
worker into one thundering retry.

### Partial enrichment

If four PRs enrich and one cannot, we **retry the failing one on a longer backoff** in case
it is transient. If it stays broken, we score the four we have. The claim is evaluated and
scored; the affected skills simply have fewer distinct PRs behind them, and a skill that
consequently falls below five is secondary rather than primary — no special case needed,
since standing is derived from PR count anyway (RFC-0003). The contributor is told which PR
could not be read.

## Queue topics

Live submissions and rubric sweeps use **separate topics**, and workers drain the sweep
topic only when the live topic is empty. A sweep of thousands of claims can therefore never
delay a contributor waiting on a submission. Both feed the same batching machinery.

## Triggers for evaluation

1. A claim is submitted and passes validation.
2. A contributor edits an evaluated claim (new version, re-queued).
3. An admin forces re-evaluation.
4. A rubric version bump, sweeping affected claims in throttled batches.

## Resolved review comments

| Marker                       | Resolution                                                           |
| ---------------------------- | -------------------------------------------------------------------- |
| Q1 use the Batch API         | Batch for everything, including live submissions                     |
| Q2 enrichment cache lifetime | 7 days, then re-enrich                                               |
| Q3 cost ceiling              | No per-day cap; the 7-day per-claim lock (RFC-0003) is the control   |
| Q4 partial enrichment        | Retry the failing PR, then score the rest; standing follows PR count |
| 24 hours must be the maximum | Hard ceiling, enforced by escalating stalled batches at 20h          |
| Why three judgements per PR? | Corrected — one call per claim scores every PR against every skill   |

## Open questions

None outstanding.

### Decided without escalation

- **Escalation is alerted above 5% of batches in a rolling week.** Synchronous re-dispatch
  at 20h costs full price, so it is a safety valve, not a running mode. Below 5% it is
  absorbed silently; above, it means the provider is degraded or our ceiling is wrong, and
  someone should be told rather than discovering it on the invoice.

- **Dispatch window: 15 minutes.** At launch volumes most batches will hold one or two
  claims and the discount is largely theoretical, but latency stays low and nothing is lost
  by keeping it short. Revisit once real submission rates exist.
- **Batch splitting lives behind the AI interface.** A sweep large enough to exceed one
  batch request is split into several by the adapter, which reassembles the results. The
  service asks to evaluate N claims and is never told how many HTTP requests that took —
  splitting is a provider constraint, not a domain concept.
- **Notification via Resend** (RFC-0006), behind a `Notifier` interface with a log-only
  implementation for tests.

## Schema

Introduces `evaluation_jobs`, `evaluation_dead_letters`, `evaluation_batches`,
`evaluations`, and `pr_skill_scores`.
