# ADR-0004 — Evaluation pipeline & broker

**Status:** Accepted · **From:** [RFC-0004](../rfc/RFC-0004-evaluation-pipeline.md)
· **Schema:** [RFC-0004-evaluation-pipeline.schema](../rfc/RFC-0004-evaluation-pipeline.schema)

## Decision

1. **All evaluation goes through the Batch API**, live submissions included. **24 hours is a
   hard ceiling we enforce**, not the provider's.
2. Queue is a Postgres table behind `port.Broker`, using `FOR UPDATE SKIP LOCKED` and
   **leases, not deletes**.
3. The api enqueues **in the same transaction** that marks the claim `queued`.
4. **One model call per claim**, scoring every PR against every declared skill.
5. **The model returns judgements, never totals.** All arithmetic is Go (ADR-0005).
6. Enrichment cache lives **7 days**; separate from judging so a model failure never
   re-spends GitHub rate limit.
7. Partial enrichment: retry the failing PR on a longer backoff, then **score the rest**.
   Standing follows the reduced PR count automatically.
8. Live submissions and rubric sweeps use **separate topics**; sweeps drain only when live
   is empty.
9. Dispatch window **15 minutes**. Escalation alerted above **5% of batches per rolling
   week**.

## Implementation

### Ports

```go
type Broker interface {
    Publish(ctx context.Context, tx port.Tx, topic string, payload []byte) error
    Receive(ctx context.Context, topic string, max int) ([]Message, error)
    Ack(ctx context.Context, m Message) error
    Nack(ctx context.Context, m Message, retryAfter time.Duration) error
}

type AI interface {
    // SubmitBatch returns a provider batch id. Splitting oversized batches is
    // the adapter's problem, not the caller's.
    SubmitBatch(ctx context.Context, reqs []JudgementRequest) (BatchID, error)
    PollBatch(ctx context.Context, id BatchID) (BatchState, []JudgementResult, error)
    // Judge is the escalation path only.
    Judge(ctx context.Context, req JudgementRequest) (JudgementResult, error)
}
```

`Publish` takes a `port.Tx` — that is what makes the outbox property a compile-time
requirement rather than a convention. No AI SDK type appears in `JudgementRequest` or
`JudgementResult`.

### Claim query

```sql
UPDATE evaluation_jobs SET
    leased_until = now() + $lease, leased_by = $worker, attempt = attempt + 1
WHERE id IN (
    SELECT id FROM evaluation_jobs
     WHERE topic = $1 AND available_at <= now() AND leased_until < now()
     ORDER BY available_at
     FOR UPDATE SKIP LOCKED LIMIT $2)
RETURNING id, payload, attempt;
```

`leased_until` is `NOT NULL DEFAULT '-infinity'`, so "never leased" and "expired" are one
comparison and the composite index is fully usable. `now()` cannot appear in an index
predicate — it is `STABLE`, not `IMMUTABLE`, and Postgres rejects it.

Ack is `DELETE`. Nack sets `leased_until='-infinity'` and pushes `available_at` forward.
A worker renews its lease at half the lease duration while a job is in flight.

### Batch lifecycle

```
enqueued → joins the open accumulating batch (or opens one, window_closes_at = now()+15m)
         → window closes → SubmitBatch → status=submitted, escalate_at = now()+20h
         → PollBatch until completed
         → escalate_at passed → status=escalated, re-dispatch each claim via Judge()
```

Escalation exists so the 24h ceiling holds regardless of provider behaviour. It costs full
price, so it is a safety valve — above 5% of batches in a rolling week, alert.

### The model call

One request per claim. Prompt structure, ordered for cache reuse:

```
[system, cache_control: ephemeral]   rubric + dimension definitions + output contract
[user]                               this claim's evidence
```

The rubric prefix is identical across every claim and only the evidence varies, so the
breakpoint sits at the end of the stable prefix. Opus 5's minimum cacheable prefix is 512
tokens, which the rubric exceeds.

Settings per ADR-0006: `claude-opus-5`, adaptive thinking (default; **never** send
`budget_tokens`), effort `high`, structured outputs, **no sampling parameters** — they
return 400 on this model.

Two response-handling rules:

- **Check `stop_reason` before reading `content`.** A refusal is an HTTP 200. Reading
  `content[0]` unconditionally panics on it. Refusals dead-letter with their category; an
  identical retry will not help.
- **`max_tokens` bounds thinking plus output.** Size it for the JSON *and* adaptive
  thinking, or responses truncate mid-object.

### Persist

One transaction, in order:

```
1. evaluations                    status=succeeded, provenance, token usage
2. pr_skill_scores                per PR per surviving skill; drop every zero
3. claim_skills.rejected_at       for skills that scored zero
4. user_skill_pr_links            reconcile against surviving skills
5. user_skills                    recompute standing + score (ADR-0003)
6. users.overall_score            recompute
7. score_snapshots                reason='evaluation'
8. claims                         status=evaluated, locked_until=now()+7d
then Ack, then notify
```

Ack after commit, never before: a crash between them redelivers, and the idempotency index
makes the redelivery a no-op.

### Idempotency

Before any model call, look for a `succeeded` evaluation matching
`(claim_id, claim_version, evidence_fingerprint, rubric_version)`. Found → Ack and return.
The fingerprint is SHA-256 over sorted PR triples + sorted project list + sorted declared
skill slugs.

### Failure matrix

| Failure | Action |
|---|---|
| Malformed payload | Dead-letter. Not retryable. |
| GitHub rate limit | Nack with `Retry-After`; **does not count as an attempt** |
| GitHub 5xx/timeout | Nack, exponential backoff + jitter |
| One PR fails enrichment | Longer backoff retry, then score the rest; record `skipped_pr_positions` |
| Batch stalled at 20h | Escalate to synchronous |
| Model refusal | Dead-letter with category |
| Structured output invalid | Retry once, then dead-letter |
| Repository write fails | Nack; the transaction rolled back |

Backoff: 5s base, exponential, jitter, capped 15 min, **6 attempts**. Jitter is not
optional — without it a provider outage synchronises every worker into one retry storm.

## Steps

1. `port.Broker`, `port.AI`, `port.Tx`; message and judgement DTOs in `domain`.
2. Postgres broker + tests: concurrent claim disjointness, lease expiry redelivery, backoff
   invisibility, ack deletes.
3. Wire `Publish` into the claim submit transaction (ADR-0003 step 7).
4. Evaluator worker loop: receive, lease renewal, dispatch, ack/nack, graceful shutdown.
5. Enrichment stage with the 7-day cache rule and the partial-enrichment path.
6. Fingerprint + idempotency check.
7. AI adapter: request builder, cache breakpoint, structured output schema, `stop_reason`
   handling, batch split. Plus a **fake** returning fixed judgements for e2e.
8. Batch accumulator, poller, and the 20h escalation sweep.
9. Persist transaction in the order above.
10. Dead-letter table + admin retry endpoint.
11. Notifier call on completion (ADR-0006).

## Consequences

- **A contributor may wait 24 hours.** The UI must be notify-based; email is v1 scope
  because of this decision.
- **Batching is near-useless at launch volumes** — most batches will hold one claim. The
  discount arrives with traffic; the latency cost arrives immediately.
- **Escalation costs full price**, so a degraded provider is also a cost event.
- **`Publish` taking a `Tx` couples the broker port to the persistence port.** Deliberate:
  the outbox guarantee is worth the coupling, and a broker that cannot join our transaction
  cannot give it.

## Amendments

| Date | Change |
|---|---|
| 2026-08-02 | Accepted from RFC-0004 |
| 2026-08-14 | **Amended by [ADR-0007](ADR-0007-rubric-contract-and-generalist-score.md)** — the judgement contract returns a score **and a remark per dimension** plus a disqualification verdict; the persist transaction gains link-status resolution and a second user-level score. |
