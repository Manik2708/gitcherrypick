# RFC-0006 — Technology selection

**Status:** Approved 2026-08-02 · **Binding form:** [ADR-0006-technology-selection](../adr/ADR-0006-technology-selection.md) · **Schema:** none — this RFC introduces no tables
· **Depends on:** RFC-0001 … RFC-0005

## Summary

The database, its host, the queue, the model, and the supporting libraries. `CLAUDE.md`
states that no role may assume any of these until the corresponding ADR is approved — this
RFC is the proposal that unblocks that.

Go, React + TypeScript, and the two-backend split are fixed by the project brief.

## Guiding principle

`CLAUDE.md` requires the database, broker, and AI provider to sit behind interfaces. That
mandate is what makes these choices **low-stakes**: each is one adapter and one wiring line
from being replaced. The bar is "clearly good enough, and cheap to reverse", and the
reversal cost is stated for each.

## Decisions

| Concern | Choice | Reversal cost |
|---|---|---|
| Database | PostgreSQL 16 | High — schema + repository adapters |
| Host | **Neon** (free tier, **direct endpoint**) | Low — connection string |
| Email | **Resend**, behind a `Notifier` interface | Low |
| Rubric config | Version-controlled file, no DB copy | Low |
| Queue | Postgres table, `FOR UPDATE SKIP LOCKED` | **Low** — one Broker adapter |
| Auth | Three paths per RFC-0002 | High — it is the trust root |
| AI provider | Anthropic `claude-opus-5`, **Batch API** | **Low** — one AI adapter |
| SQL access | `pgx/v5` with **prepared statements** | Medium |
| HTTP router | `chi` | Low |
| Migrations | Plain `.sql`, applied idempotently by the api at boot | Low |
| CLI | `cobra` (mandated) | — |
| Mocks | `mockery` | Low |
| Assertions | `testify` | Low |
| Logging | `log/slog` (stdlib) | Low |
| Frontend | React 18 + TypeScript + Vite | High |
| Server state | TanStack Query | Medium |

### Database — PostgreSQL 16

We need relational integrity, JSON for rubric-owned dimension scores, trigram search, and a
queue. Postgres does all four natively, so the alternative is not "Postgres vs X" but
"Postgres vs Postgres plus two more systems to operate."

`jsonb` covers the genuinely schema-fluid parts — `dimension_scores`, `filters`,
`quantiles` — without giving up constraints everywhere else.

### Host — Neon

Free tier, real Postgres 16, and **database branching**, which is the deciding feature: the
integration suite can branch production schema per test run instead of maintaining a
separate seeded database. Scale-to-zero means a cold start adds roughly a second to the
first query after idle, which is irrelevant for a service whose evaluation path already
tolerates 24 hours.

Local development and the e2e harness still run Postgres in Docker — nothing in the schema
or repository layer depends on the host, so the two stay interchangeable.

**We use Neon's direct endpoint, not the pooled one, and pool in the application.** Neon's
pooled endpoint runs PgBouncer in transaction mode, which is incompatible with the
session-scoped prepared statements chosen below: PgBouncer hands a different backend
connection to each transaction, so a statement prepared on one is missing on the next. The
workaround is to disable pgx's statement cache, which would surrender both the plan caching
and the structural injection guarantee that motivated prepared statements in the first
place.

`pgx` pools in-process instead. With two backends at bounded replica counts, connection
count is not the constraint PgBouncer exists to solve. This is worth revisiting only if we
deploy something connection-hungry — a serverless function per request, say — and it is a
configuration change, not a code change.

**Free-tier ceilings**, to be aware of rather than to design around: 0.5 GiB storage, 190
compute-hours per month, one project. Storage is the one that binds first — the persisted
score components (RFC-0005) are ~40 bytes per `pr_skill_score` row, so 100k claims at 5 PRs
× 3 skills is roughly 60 MB, or 12% of the ceiling. Comfortable, but not unlimited, and the
`metric_observations` table needs the sampling-based pruning RFC-0005 describes rather than
unbounded append.

### Queue — the database, behind the Broker interface

**How it actually works.** The queue is one table, `evaluation_jobs`. Publishing is an
`INSERT`. Consuming is a single statement:

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

`FOR UPDATE SKIP LOCKED` is the whole trick. Ordinarily two transactions selecting the same
row for update serialise — the second waits. `SKIP LOCKED` tells Postgres to skip locked
rows instead of waiting, so ten workers running this statement simultaneously each get a
**disjoint** set of jobs with no coordination, no polling collisions, and no external
broker. It has been in Postgres since 9.5 and is what most lightweight job libraries are
built on.

The rest follows from three columns:

- **`leased_until`** — a claimed row is not deleted, only leased. Ack is `DELETE`. A worker
  that crashes loses its lease and the job returns to the pool when it expires. Deleting on
  read would drop the claim silently and leave the contributor waiting forever. It is
  `NOT NULL DEFAULT '-infinity'`, so "never leased" and "lease expired" are one comparison
  — a nullable column would need `IS NULL OR <` , which no index serves well and which
  cannot be a partial-index predicate, since `now()` is not `IMMUTABLE`.
- **`available_at`** — backoff. Nack sets it into the future; the claim query simply cannot
  see the row until then.
- **`topic`** — live submissions and rubric sweeps are separate topics, and workers drain
  sweeps only when live is empty (RFC-0004).

**Why not Kafka.** This workload is claims per minute, not messages per second. More
importantly, the api enqueues the job **in the same transaction** that marks the claim
`queued` — either both commit or neither does. With an external broker that guarantee
requires the full transactional-outbox pattern; here it is one `INSERT`. That property is
the strongest argument for the choice, and the interface means swapping to Kafka later is
one adapter.

### AI — `claude-opus-5` via the Batch API

Score quality is the product, so this is the wrong place to economise on capability. The
Batch API recovers roughly half the cost in exchange for latency we have explicitly decided
we can spend (RFC-0004).

| Setting | Value | Why |
|---|---|---|
| Model | `claude-opus-5` | Strongest reasoning at this tier ($5/$25 per MTok) |
| Dispatch | Batch API | ~50% cheaper; 24h ceiling enforced by us, not the provider |
| Thinking | adaptive (the default) | On by default on Opus 5 — `budget_tokens` is rejected |
| Effort | **`high`** | Quality is not being compromised for cost |
| Output | structured outputs (`output_config.format`) | Response validates against a schema, not parsed from prose |
| Sampling params | **none** | `temperature`, `top_p`, `top_k` return 400 on this model |

Three things the implementation must handle:

- **`max_tokens` bounds thinking *plus* output.** Sizing it tightly around the expected
  JSON will truncate mid-response once adaptive thinking runs.
- **`stop_reason: "refusal"` is an HTTP 200.** Code reading `content[0]` unconditionally
  breaks on it. Check `stop_reason` first and dead-letter refusals (RFC-0004).
- **The prompt is cacheable.** The rubric and instructions are identical across every claim
  and only the evidence varies, so a `cache_control` breakpoint at the end of the stable
  prefix cuts input cost substantially. Opus 5's minimum cacheable prefix is 512 tokens,
  which the rubric will exceed comfortably.

Client: the official `github.com/anthropics/anthropic-sdk-go`, used **only inside the AI
adapter**. No SDK type may appear in a service signature.

### SQL — `pgx/v5` with prepared statements

Hand-written SQL, no `sqlc` and no ORM: the repository layer is deliberately the only place
SQL exists, and generated types would leak a second model into it.

**All queries are prepared statements.** pgx caches them per connection, which buys the
query plan back on repeat execution and — more importantly — makes SQL injection
structurally impossible rather than a matter of discipline. Correctness is proven by the
integration suite running against a real Postgres, not by unit tests against a mocked
driver.

### Email — Resend, behind a `Notifier` interface

Notification became v1 scope the moment evaluation went asynchronous (RFC-0004): a
contributor who waits up to 24 hours has to be told when the result lands. Contact requests
and availability-lapse reminders need it too.

**Resend**: 3,000 emails/month free, a small API, good deliverability, and no domain
verification or sandbox-exit request before the first send. SES is cheaper at volumes we do
not have and slower to start; Postmark has no meaningful free tier.

It sits behind a `Notifier` interface with a log-only implementation, so the integration
suite sends nothing and asserts on calls, and swapping to SES later is one adapter.

### Rubric configuration — a file, and only a file

Every constant in RFC-0005 — the dimension weights, `d`, `γ`, `σ`, `ω`, `μ`, `β` — lives in
a version-controlled YAML file per rubric version, embedded in the evaluator binary at
build time. `evaluations.rubric_version` records which file produced a score.

No database copy. The evaluator loads the file at boot and does the arithmetic in Go; the
nightly recompute does the same. A synced table would buy foreign-key integrity and
SQL-queryable weights, neither of which anything needs, at the cost of a sync path that can
drift and a second source of truth for something that must have exactly one.

Changing a weight is therefore a reviewed commit and a deploy. That is the right amount of
friction: a weight change rescores every contributor on the platform, and doing that from an
admin form with no review or audit trail is how a scoring system loses its credibility.

**One operational rule: rubric files are never deleted.** An old evaluation's constants must
stay recoverable from git for as long as the score it produced is displayed.

### GitHub client

`go-github`, wrapped in our own narrow interface exposing only what enrichment needs.
Wrapping matters twice: the integration suite runs against a fake — no network, no rate
limit, deterministic fixtures — and a later REST-to-GraphQL migration becomes an adapter
swap. Conditional requests with ETags; `Retry-After` honoured on 403/429.

### Frontend

Vite for dev server and build. TanStack Query for server state — the app is almost entirely
server state, and claim status transitions need polling, which is exactly the wheel it
exists to stop us reinventing. No Redux; there is little genuine client state.

Per `CLAUDE.md` and RFC-0002, every endpoint string lives in one module and the base URL
comes from `.env`.

## Resolved review comments

| Marker | Resolution |
|---|---|
| Want a free hosted Postgres | **Neon** free tier; Docker remains for local and e2e |
| Explain how the Postgres queue works | Full mechanism above — `SKIP LOCKED`, leases, `available_at`, topics |
| Q2 effort level | `high`, fixed. Quality not compromised for cost |
| Q3 Batch API | Resolved in RFC-0004: Batch for everything, 24h ceiling |
| Q4 sqlc vs hand-written | Hand-written with prepared statements, proven by integration tests |

## Open questions

None outstanding. Neon's ceilings, connection pooling, the email provider, and rubric
config storage were all resolved in this revision and are documented above.

## Schema

None. This RFC records decisions; the tables they support are introduced by RFC-0002
through RFC-0005.
