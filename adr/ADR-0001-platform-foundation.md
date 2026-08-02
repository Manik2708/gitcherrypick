# ADR-0001 — Platform foundation & service topology

**Status:** Accepted · **From:** [RFC-0001](../rfc/RFC-0001-platform-overview.md)
· **Schema:** [RFC-0001-platform-overview.schema](../rfc/RFC-0001-platform-overview.schema)

## Decision

1. Two binaries: `backend/cmd/api` and `backend/cmd/evaluator`. **No migration binary** —
   the api applies the schema idempotently at boot.
2. Both are structured controllers → services → repositories, with **interfaces at every
   boundary**. The evaluator's controller is its broker consumer.
3. A **claim is an evidence bundle**, not an assertion about one skill: ≤5 PRs, 0–20
   projects, and the skills those PRs demonstrate, judged for every skill in one model call.
4. **Skill standing is derived** per `(user, skill)` from distinct evidencing PRs — 5+ is
   primary and ranked, 1–4 is secondary and unranked. Promotion is automatic.
5. **Score and standing are orthogonal.** A secondary skill may outscore a primary one and
   remains secondary. Nothing promotes a skill except a fifth distinct PR.
6. Scores change on **rubric change or evidence change only**. Never with age. The
   arithmetic recompute (ADR-0005) adjusts relative values only.
7. Projects are scored **arithmetically only** and are optional.

## Implementation

### Module layout

```
backend/
  go.mod                          module github.com/<org>/gitcherrypick/backend
  cmd/
    api/main.go                   cobra root; flags; wiring; migrate-then-serve
    evaluator/main.go             cobra root; flags; wiring; worker loop
  internal/
    domain/                       entities + errors. Imports NOTHING from internal/.
    http/                         controllers, router, middleware, DTOs
    service/                      business logic; depends on ports only
    port/                         ALL interfaces live here (Repository, Broker, AI,
                                  GitHub, Notifier, Clock)
    postgres/                     Repository implementations + migrations/*.sql
    broker/postgres/              Broker implementation
    ai/anthropic/                 AI implementation
    github/                       GitHub implementation
    notifier/resend/              Notifier implementation
    scoring/                      pure functions, no I/O (ADR-0005)
    config/                       flag definitions and validation
  e2e/                            integration tests (stage 3)
  scripts/                        docker-compose + harness
```

**`internal/port` is the only package both sides import.** Services depend on `port`;
adapters implement `port`. A service that imports `internal/postgres` is a defect, and the
import graph makes it visible.

**`internal/domain` imports nothing from `internal/`.** No `pgx` type, no `anthropic` type,
no `http` type may appear in a domain struct — that is what makes the storage engine and
model vendor swappable.

### Migrations

Numbered plain SQL at `internal/postgres/migrations/NNNN_name.sql`, embedded with
`go:embed`. Generated from the `.schema` files beside the RFCs; the `.schema` file is the
design source of truth, the migration is the executable form.

Boot sequence, in `api` only — the evaluator never migrates:

```
1. pg_advisory_lock(<constant>)          one instance applies; the rest wait
2. read schema_migrations
3. for each embedded migration not present, in lexical order:
     apply in its OWN transaction
     insert (version, checksum, duration_ms)
4. for each already-applied migration: verify checksum
     mismatch -> fail startup, do not serve
5. pg_advisory_unlock(<constant>)
```

A checksum mismatch is a **startup failure, not a warning**: it means a migration was edited
after being applied somewhere, and two databases have silently diverged.

### Configuration

Every value is a cobra flag. No `os.Getenv` outside `internal/config`. Flags bind to env
vars via `GITCHERRYPICK_` prefix for deployment convenience, but the flag is the interface.

Shared: `--database-url`, `--log-level`, `--log-format`.
`api`: `--listen-addr`, `--jwt-signing-key`, `--github-oauth-client-id`,
`--github-oauth-client-secret`, `--google-oauth-*`, `--token-encryption-key`,
`--resend-api-key`, `--public-base-url`, `--admin-seed-email`, `--admin-seed-password`.
`evaluator`: `--anthropic-api-key`, `--github-token`, `--worker-count`,
`--lease-duration`, `--batch-window`, `--rubric-version`.

**Startup fails on a missing required flag.** No defaults for secrets.

### Cross-cutting

`port.Clock` is injected everywhere a timestamp is taken. Lease expiry, the 7-day lock, the
15-day availability window, and batch escalation are all time-dependent, and none of them
can be tested against `time.Now()`.

`log/slog` with JSON output in production. Every request carries a request id in context;
every evaluator job logs its claim id.

## Steps

1. `go mod init`, pin dependencies per ADR-0006, commit `go.sum`.
2. Create the package skeleton above with a doc comment per package stating its rule.
3. Define `port.Clock` and a `clocktest` fake.
4. Write the migration runner and its unit tests: fresh DB, re-run is a no-op, concurrent
   boots, checksum mismatch fails.
5. Translate `RFC-0001..0005-*.schema` into numbered migrations, verifying each applies in
   order against PostgreSQL 16.
6. Wire `cmd/api`: cobra, config validation, migrate, then serve a `/healthz` returning
   build info and migration state.
7. Wire `cmd/evaluator`: cobra, config validation, no migration, idle worker loop.
8. Add `golangci-lint` with an import-boundary rule failing any `internal/service` →
   `internal/postgres` import.

## Consequences

- **Migrating at boot ties schema changes to deploys.** Fine at current scale; a long
  migration will block startup and needs a different strategy before it is one.
- **No migration binary** means no way to migrate without starting an api. Acceptable while
  api and schema ship together.
- **The `port` package will get large.** That is preferable to interfaces defined next to
  their implementations, which is how import cycles start.

## Revisions

| Date | Change |
|---|---|
| 2026-08-02 | Accepted from RFC-0001 |
