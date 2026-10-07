# GitCherryPick

A platform where open-source contributors prove skill with **evidence** rather than
self-assertion, and recruiters cherry-pick from a ranked pool.

A contributor signs in with GitHub and submits a **claim**: up to five of their best
merged pull requests, optional supporting projects, and the skills those pull requests
demonstrate. One model call judges every pull request against every declared skill.

**Skill standing is derived, not declared.** Five distinct surviving pull requests make a
skill _primary_: ranked and searchable. One to four make it _secondary_: visible and
score-contributing, never ranked. Promotion at the fifth is automatic.

Hirers are separately verified accounts. Scorecards are gated, and a contributor sees
only their own rank.

## Status

In development, and not deployed anywhere.

- The API, the frontend and the integration suite run locally end to end.
- The evaluator's model call is a stub. A submitted claim is queued and can be drained
  against the local stand-in, but nothing here produces a real judgement yet.
- There are no container images or deployment scripts yet.

## Quick start

The local setup steps are not written yet. That is
[#13](https://github.com/Manik2708/gitcherrypick/issues/13), and it is open to anyone.

## Commands

| Command          | What it does                                                | Needs Docker |
| ---------------- | ----------------------------------------------------------- | ------------ |
| `make check`     | Formatting, lint, doc and fixture validators, Go unit tests | No           |
| `make db-test`   | Repository suite against a real Postgres                    | Yes          |
| `make e2e`       | Integration fixtures against a real Postgres                | Yes          |
| `make check-all` | All three of the above, which is everything CI runs         | Yes          |
| `make fmt`       | Fixes what is fixable: `gofmt`, `goimports`, Prettier       | No           |
| `make mocks`     | Regenerates the mocks from the port interfaces              | No           |
| `make dev`       | Postgres, the third-party stand-in and the API, seeded      | Yes          |
| `make up`        | `make dev` plus the frontend dev server                     | Yes          |

`make dev ARGS='--reset'` rebuilds the database after a schema change.

The frontend's own checks run from `frontend/`: `npm run typecheck`, `npm run lint` and
`npm test`.

## Layout

```
rfc/            Proposed features, each with its schema delta alongside
adr/            Approved RFCs. These are binding; an RFC is only a proposal
evaluation/     The scoring rubric
backend/
  cmd/<binary>/main.go
  internal/     controller | service | repository | domain | port | adapter | scoring
  mocks/        Generated from internal/port. Never hand-written
  e2e/          Integration tests and their JSON fixtures
  scripts/      docker-compose files and the harness scripts
frontend/       React + TypeScript
scripts/        Repository validators (Python)
```

## How it is built

**Backend:** Go, in three layers: controllers, services, repositories. Every dependency
crosses a layer boundary as an interface defined in `backend/internal/port`. PostgreSQL 16
through `pgx/v5`, `chi` for routing, `cobra` for configuration.

**Evaluator:** a separate Go binary that consumes skill-evaluation requests from a queue
and calls an AI provider. Both the queue and the provider sit behind interfaces.

**Frontend:** React and TypeScript, built with Vite.

Every decision is recorded. Start with [`adr/`](adr/) for what is decided,
[`rfc/`](rfc/) for why, and [`evaluation/`](evaluation/) for how a pull request is scored.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Issues labelled
[`good first issue`](https://github.com/Manik2708/gitcherrypick/labels/good%20first%20issue)
are scoped to be picked up without knowing the whole codebase.

## Licence

No licence has been chosen yet.
