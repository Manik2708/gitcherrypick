# GitCherryPick

A platform where open-source contributors prove skill with **evidence** rather than
self-assertion, and recruiters cherry-pick from a ranked pool.

A contributor signs in with GitHub and submits a **claim**: up to five of their best merged
PRs, optional supporting projects, and the skills those PRs demonstrate. One model call
judges every PR against every declared skill.

**Skill standing is derived, not declared.** Five distinct surviving PRs make a skill
_primary_ — ranked and searchable. One to four make it _secondary_ — visible and
score-contributing, never ranked. Promotion at the fifth is automatic.

Three score families come out of it: **per PR per skill**, **per skill**, and two
user-level numbers — **Overall** (depth, 0–100) and **Generalist** (breadth, unbounded).
Hirers are separately verified accounts; scorecards are gated, and contributors see only
their own rank.

**Current gate:** Stage 4 — Core backend. Stages 1–3 are approved: the ADRs are binding, the rubric is fixed, and 31 integration fixtures are written and RED. Implementation turns them green; no implementer may edit a fixture to make one pass.

## Delivery pipeline

Work moves through fixed stages. Each has an approval gate held by the project owner.
**Nothing in a stage may start until every stage above it is approved.**

| #   | Stage                | Artifacts                                                                         |
| --- | -------------------- | --------------------------------------------------------------------------------- |
| 1   | Planning ✅          | `rfc/*.md` + linked `rfc/*.schema` → promoted to `adr/*.md` on approval           |
| 2   | Evaluation design ✅ | `evaluation/*.md` — what makes a PR worth points                                  |
| 3   | Integration tests ✅ | `backend/e2e/*_test.go`, `backend/scripts/` — **must fail before implementation** |
| 4   | Core backend         | `backend/cmd/api`, controllers/services/repositories                              |
| 5   | Evaluator backend    | `backend/cmd/evaluator`, broker + AI adapters                                     |
| 6   | Frontend             | `frontend/`                                                                       |
| 7   | Containerization     | Dockerfiles, compose, image build scripts                                         |

Stage 3 is strict TDD at the integration level: tests are written and approved first,
must be **red**, and turn green only through stage 4/5 work.

## Escalation rule

Every role reports API mismatches, IO problems, and design gaps to the **Planner** —
never to a sibling role. The Planner re-plans, gets the change approved, and hands
work back out. A frontend problem never gets fixed by editing the backend directly.

## Architecture rules (non-negotiable)

These come from `.claude/Agent.md` and apply to both backends.

- **Binaries** live at `backend/cmd/<binary_name>/main.go`. Nothing else is a main package.
- **Three layers, separately defined and implemented: controllers → services → repositories.**
  Every dependency crosses a layer boundary as an **interface**, never a concrete type.
- **Controllers** only serialize/deserialize and validate IO. No business logic.
- **Services** own business logic and talk to the repository interface. They must not
  know which database is behind it.
- **Repository models are independent of the storage engine.** Translating between the
  domain model and storage rows is the repository's job, so the DB can be swapped.
- **The evaluator additionally abstracts its broker and its AI provider.** It receives
  messages, calls the AI through an interface, does arithmetic, and writes through the
  repository interface. It must never reference a specific queue or model vendor.
- **Config comes from CLI flags**, wired with `cobra`. No direct `os.Getenv` in
  business code.
- **Unit tests mock the layer below.** Controller tests mock services; service tests
  mock repositories. Target: **95% coverage**.

## Layout

```
rfc/            Proposed features + schema deltas (.schema alongside each .md)
adr/            Approved RFCs, with implementation steps
evaluation/     Scoring rubric
backend/
  cmd/<binary>/main.go
  internal/     controllers | services | repositories | broker | ai
  e2e/          integration tests (backend only)
  scripts/      docker-compose + bash harness for e2e
frontend/       React + TypeScript
```

## Stack

Fixed by the project brief: **Frontend** HTML/CSS + React + TypeScript · **Backend** Go
· **Evaluator** Go, consuming skill-evaluation requests and calling an AI provider.

Decided in [ADR-0006](adr/ADR-0006-technology-selection.md) and now **binding**: PostgreSQL
16 on Neon (direct endpoint), a Postgres-table queue behind `port.Broker`, `claude-opus-5`
via the Batch API, `pgx/v5` with prepared statements, `chi`, `cobra`, and Resend behind
`port.Notifier`.

Read `adr/` before assuming anything. Only the ADR is binding — where an RFC and an ADR
disagree, the ADR wins and the discrepancy goes to the Planner.

## Commands

```bash
.claude/scripts/bootstrap.sh   # cold-machine setup + doctor report
```

Backend and frontend commands land here as stages 4–6 are implemented. Until then
this section is intentionally empty rather than aspirational.

## Conventions

- Go 1.24, `gofmt` enforced by a PostToolUse hook — don't hand-format.
- Errors wrap with `%w` and carry context; no bare `return err` at layer boundaries.
- One RFC per feature; the schema file is linked from the RFC and holds only the delta.
- Frontend API endpoint strings live in a **single** file; the base URL comes from
  `.env`, with a committed `.env.sample`.

## Environment

`bootstrap.sh` reports what's missing. Three things need a human:

| Need                                | Why                                                  | Blocks          |
| ----------------------------------- | ---------------------------------------------------- | --------------- |
| Docker daemon running               | e2e harness runs Postgres in a container             | Stages 3–7      |
| GitHub OAuth App id + secret        | login flow; must be created by a human on github.com | Live login only |
| `ANTHROPIC_API_KEY`, `GITHUB_TOKEN` | evaluator calls the model; PR/repo metadata fetch    | Live runs only  |

Because the AI, broker, repository, and GitHub clients are all interfaces, the
integration suite runs on **fakes plus a real Postgres container** — so only Docker
gates implementation. The secrets are needed the first time we point at live services.
