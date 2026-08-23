# RFC-0012 — A controllable clock, and how fixtures trigger background work

**Status:** Approved 2026-08-23 · **Binding form:**
[ADR-0012](../adr/ADR-0012-controllable-clock-and-job-triggers.md)
· **Schema:** _(no delta)_
· **Amends:** ADR-0010 §5 and Decision 6, which are **wrong**. Removes `port.ControlPlane`.

## Summary

ADR-0010 §5 says time is controlled by seeding and that no fake clock exists. Implementing
against the approved fixtures shows that is false. `claims/seven_day_lock.json` step 4 says so
outright:

> Advance past the lock. **Only a fake clock makes this testable.**

Six steps across five fixtures issue `ADVANCE_CLOCK`, and four more pseudo-methods —
`RUN_EVALUATOR`, `RUN_OVERDUE_SWEEP`, `REDELIVER_LAST_JOB`, `REJECT_N_REEVALUATIONS` — have no
mechanism at all since ADR-0010 deleted `--adapters=fake` without replacing what it did. Those
fixtures are approved stage-3 artifacts and CLAUDE.md forbids editing one to make it pass, so
the ADR is what has to change.

## 1. Why the seeding argument failed

It holds for state that exists BEFORE a fixture runs. A contributor's availability window can
be seeded 31 days stale and the real clock reads it as lapsed.

It does not hold for state the API creates DURING a fixture. The seven-day claim lock is
written by `POST /claims/{id}/submit` at step 3 and must be past by step 5. There is no row to
seed: it did not exist when the fixture started. The same applies to the escalating
re-evaluation cooldown, invitation expiry, and the overdue window on a shortlist created
mid-fixture.

`port.Clock` anticipated this in stage 3 and named these exact cases — "the 7-day claim lock,
the 15-day availability window, job lease expiry, invitation expiry, and the escalating
re-evaluation cooldown. The alternative is a test that sleeps, which is a flake with a timer
attached." ADR-0010 §5 contradicted an interface comment that was already correct.

## 2. The clock becomes a redirected dependency

Not a fake, and not a test-only branch. The same shape as the four third-party base URLs:

```
--clock-url=http://127.0.0.1:8081/_clock    # e2e
                                            # production passes nothing
```

With no flag the clock is `time.Now()` — no poll, no HTTP, no allocation. With a flag, an
offset is fetched in the background and `Now()` returns system time plus the cached offset.

Polling rather than a call per `Now()`: `Now()` is on every request path and several times per
scoring pass, and an HTTP round trip there would change the thing under test. The cost is up
to one poll interval of staleness, which no assertion in any fixture can observe — the shortest
duration any of them turns on is the seven-day lock.

The first fetch happens at startup and a failure is fatal, matching ADR-0011's treatment of the
signing key: a flag that was passed and does not work is a misconfiguration, and discovering it
at boot is cheaper than discovering it from a wrong timestamp. Later failures keep the last
known offset, because a harness blip must not take the API down mid-fixture.

The accepted risk is the one ADR-0010 already accepted for base URLs: configuration can point
somewhere wrong. Same mitigation — the default is the real thing.

## 3. Job triggers need no new API surface

The remaining four pseudo-methods resolve without adding anything to `cmd/api`:

| Pseudo-method            | Mechanism                                                          |
| ------------------------ | ------------------------------------------------------------------ |
| `RUN_OVERDUE_SWEEP`      | `cmd/jobs run overdue-sweep` — a binary the harness executes       |
| `RUN_EVALUATOR`          | `cmd/evaluator --once` — the stage-5 binary, drained synchronously |
| `REDELIVER_LAST_JOB`     | `cmd/jobs redeliver-last` — resets one lease through the broker    |
| `REJECT_N_REEVALUATIONS` | N real `POST /admin/reevaluations/{id}/decide` calls               |

The first three are database-driven work with no dependency on the API process, so a separate
binary reaches them without a control listener. The fourth needs no shortcut at all: making
the real calls exercises the escalation path the fixture is actually about.

`RUN_EVALUATOR` belongs to stage 5. Fixtures depending on it stay red until that gate opens,
which is the pipeline working rather than a gap.

## 4. `port.ControlPlane` is removed

It describes a second listener inside `cmd/api`, bound only under `--adapters=fake` — a flag
ADR-0010 deleted. Nothing implements it and nothing can. Its `RunAction` capability is what
§3 replaces.

## Consequences

- One new adapter, `internal/adapter/clock`, with a system implementation and an offset one.
- One new binary, `cmd/jobs`, which is also where scheduled work belongs in production.
- `cmd/api` gains `--clock-url` and no test-only handler.
- ADR-0010's claim that no fake clock exists is retracted. Its reasoning about seeding survives
  for pre-existing state, which is most fixtures.
- The offset clock is real code on a production path, guarded by a flag rather than excluded
  from the binary. That is the same trade ADR-0010 §2 made and defended for base URLs.

## Open questions

_None._
