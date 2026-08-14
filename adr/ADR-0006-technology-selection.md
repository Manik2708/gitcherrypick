# ADR-0006 — Technology selection & dependencies

**Status:** Accepted · **From:** [RFC-0006](../rfc/RFC-0006-technology-selection.md)
· **Schema:** none

## Decision

| Concern            | Binding choice                                               |
| ------------------ | ------------------------------------------------------------ |
| Language / runtime | Go 1.24                                                      |
| Database           | PostgreSQL 16                                                |
| Host               | Neon free tier, **direct endpoint** (not pooled)             |
| SQL                | `pgx/v5`, hand-written, **prepared statements**              |
| Queue              | Postgres table behind `port.Broker`                          |
| Router             | `chi/v5`                                                     |
| CLI                | `cobra`                                                      |
| Model              | `claude-opus-5`, Batch API, effort `high`                    |
| AI SDK             | `anthropic-sdk-go` — **only inside `internal/ai/anthropic`** |
| GitHub             | `go-github` behind `port.GitHubClient`                       |
| Email              | Resend behind `port.Notifier`                                |
| Logging            | `log/slog`, JSON                                             |
| Mocks              | `mockery`                                                    |
| Assertions         | `testify`                                                    |
| Frontend           | React 18 + TypeScript + Vite + TanStack Query                |
| Rubric config      | Version-controlled YAML, **never deleted**                   |

## Implementation

### Dependency policy

Pin exact versions in `go.mod`; commit `go.sum`. Adding a direct dependency needs a
one-line justification in the PR — every one is a supply-chain surface and a future
migration.

**Vendor types never cross a port boundary.** `pgx.Rows`, `anthropic.Message`,
`github.PullRequest`, and `chi.Router` may appear only inside their own adapter package.
`golangci-lint` enforces this with an `importas`/`depguard` rule, so the mandate is checked
by CI rather than by review attention.

### Neon

Direct endpoint. Neon's pooled endpoint runs PgBouncer in transaction mode, which hands a
different backend connection to each transaction — a statement prepared on one is missing on
the next. `pgx` pools in-process instead; with two backends at bounded replica counts,
connection count is not the constraint PgBouncer exists to solve.

`pgxpool` with `MaxConns` sized from `--db-max-conns` (default 10 for api, 5 for evaluator),
`DefaultQueryExecMode` left at the default so prepared statements are cached per connection.

**Free-tier ceilings to watch:** 0.5 GiB storage, 190 compute-hours/month, one project.
Storage binds first — `metric_observations` must be sampled rather than appended without
bound (ADR-0005).

Local development and e2e run Postgres 16 in Docker. Nothing in the schema or repository
layer depends on the host.

### The AI adapter

```
internal/ai/anthropic/
  client.go      constructs anthropic.NewClient(); the ONLY file importing the SDK
  request.go     domain.JudgementRequest -> messages; cache breakpoint placement
  schema.go      structured-output JSON schema for the judgement contract
  response.go    stop_reason check, parse, -> domain.JudgementResult
  batch.go       SubmitBatch / PollBatch, splitting oversized batches
```

Request settings, all binding:

- `model: "claude-opus-5"`
- **Adaptive thinking** — the default on Opus 5. Do **not** send `budget_tokens`; it is
  rejected with a 400.
- `output_config.effort: "high"` — quality is not traded for cost here.
- **Structured outputs** via `output_config.format`, so the response validates against a
  schema rather than being parsed out of prose.
- **No `temperature`, `top_p`, or `top_k`** — all return 400 on this model.
- `cache_control: ephemeral` at the end of the rubric prefix.
- `max_tokens` sized for the JSON **plus** adaptive thinking, which shares the same budget.

`stop_reason` is checked before `content` is touched. A refusal is an HTTP 200 and code
indexing `content[0]` unconditionally will panic on it.

### Rubric configuration

```
backend/internal/scoring/rubric/
  v1.yaml
  v2.yaml
  embed.go        //go:embed *.yaml
```

Loaded at boot, selected by `--rubric-version`. No database copy: the evaluator does the
arithmetic in Go and the recompute does too, so a synced table would be a second source of
truth for something that must have exactly one.

**Rubric files are never deleted.** An old evaluation's constants must stay recoverable for
as long as the score it produced is displayed.

### Frontend

Vite dev server and build. TanStack Query for server state — the app is almost entirely
server state, and claim status polling is exactly what it exists for. No Redux.

Per `CLAUDE.md`: **every endpoint string in one module**, base URL from `.env`, committed
`.env.sample` carrying every key, and a markdown document explaining how to obtain each
value and run the app from a clean clone.

### Containers

Multi-stage builds, distroless runtime, non-root. One image per binary plus one for the
frontend. `docker compose` for local and e2e, with Postgres 16 and health-gated startup
ordering.

## Steps

1. `go mod init`; add and pin: `pgx/v5`, `chi/v5`, `cobra`, `anthropic-sdk-go`, `go-github`,
   `google/uuid`, `testify`, `mockery`.
2. `.golangci.yml` with `depguard` rules encoding the vendor-type boundary.
3. `pgxpool` construction in `internal/postgres`, with `--db-max-conns` and health check.
4. Anthropic adapter skeleton + the fake used by e2e.
5. `go-github` adapter + fake with the fixture set from ADR-0003 step 4.
6. Resend notifier + log-only implementation.
7. Rubric YAML v1 with every ADR-0005 constant, embedded and loaded.
8. Dockerfiles per binary; `docker-compose.yml`; build script in `backend/scripts`.
9. Frontend scaffold: Vite + TS, endpoints module, `.env.sample`, setup document.

## Consequences

- **Direct endpoint caps connection scaling.** Fine now; a serverless deployment would force
  the pooled endpoint and cost us prepared statements.
- **Neon's free tier sleeps.** First query after idle adds ~1s. Irrelevant for a 24h
  evaluation path, visible on a cold api request.
- **Rubric changes require a deploy.** Deliberate friction — a weight change rescores
  everyone.
- **Pinned dependencies drift.** Someone has to own updating them; nobody does yet.

## Revisions

| Date       | Change                 |
| ---------- | ---------------------- |
| 2026-08-02 | Accepted from RFC-0006 |
