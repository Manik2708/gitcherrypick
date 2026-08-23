# ADR-0012 — A controllable clock, and how fixtures trigger background work

**Status:** Accepted · **From:** [RFC-0012](../rfc/RFC-0012-controllable-clock-and-job-triggers.md)
· **Schema:** _(no delta)_
· **Amends:** ADR-0010 §5 and Decision 6 — **retracted**. Removes `port.ControlPlane`.

## Decision

1. **ADR-0010's "no fake clock exists" is retracted.** The approved stage-3 fixtures require
   time to move mid-run, and CLAUDE.md forbids editing a fixture to make it pass.

2. **The clock is a redirected dependency**, the same shape as the four third-party base URLs:

   | Flag           | Default   | Behaviour                         |
   | -------------- | --------- | --------------------------------- |
   | `--clock-url`  | _(unset)_ | `time.Now()` — no poll, no HTTP   |
   | `--clock-poll` | `50ms`    | how often the offset is refreshed |

   With `--clock-url`, `Now()` returns system time plus a background-polled offset.

3. **The first offset fetch is fatal on failure**, before the listener binds. Later failures
   keep the last known offset.

4. **No control listener is added to `cmd/api`,** and no test-only handler.

5. **The remaining pseudo-methods resolve outside the API process:**

   | Pseudo-method            | Mechanism                                            |
   | ------------------------ | ---------------------------------------------------- |
   | `RUN_OVERDUE_SWEEP`      | `cmd/jobs run overdue-sweep`                         |
   | `RUN_EVALUATOR`          | `cmd/evaluator --once` (stage 5)                     |
   | `REDELIVER_LAST_JOB`     | `cmd/jobs redeliver-last`                            |
   | `REJECT_N_REEVALUATIONS` | N real `POST /admin/reevaluations/{id}/decide` calls |

6. **`port.ControlPlane` is deleted.**

## Implementation

### Why seeding was not enough

The argument in ADR-0010 §5 holds for state that exists BEFORE a fixture runs: a contributor's
availability window can be seeded 31 days stale and the real clock reads it as lapsed. Most
fixtures are like that, and for them nothing changes.

It fails for state the API creates DURING a fixture. The seven-day claim lock is written by
`POST /claims/{id}/submit` at step 3 and must be past by step 5. There is no row to seed —
it did not exist when the fixture started.

`port.Clock` named these cases in stage 3, before either ADR was written: "the 7-day claim
lock, the 15-day availability window, job lease expiry, invitation expiry, and the escalating
re-evaluation cooldown. The alternative is a test that sleeps, which is a flake with a timer
attached." ADR-0010 §5 contradicted an interface comment that was already right.

### Why polling rather than a call per Now()

`Now()` runs on every request path and several times per scoring pass. An HTTP round trip
there would change the latency of the thing under test and make the clock a dependency of
every handler. The cost of polling is up to one interval of staleness, which no fixture can
observe: the shortest duration any of them turns on is seven days.

### Why a fatal first fetch

Matching ADR-0011's treatment of the signing key. A flag that was passed and does not work is
a misconfiguration, and finding it at boot is far cheaper than finding it later from a
timestamp that is quietly wrong.

Later failures are tolerated with the cached offset, because a harness blip must not take the
API down in the middle of a fixture.

### Why no shortcut for re-evaluation rejections

`REJECT_N_REEVALUATIONS` could have been a control action. Making N real admin calls instead
exercises the escalating-cooldown path the fixture exists to test, rather than reaching around
it — a shortcut here would have tested the shortcut.

## Steps

1. `internal/adapter/clock`: `System` and `Offset`, both implementing `port.Clock`.
2. `cmd/fakethirdparty`: `GET /_clock` and `POST /_clock/advance`.
3. `cmd/api`: `--clock-url`, `--clock-poll`.
4. `cmd/jobs`: `run <job>` and `redeliver-last`.
5. Delete `port.ControlPlane`.
6. Teach the e2e runner to translate the five pseudo-methods.

## Consequences

- One new adapter and one new binary. `cmd/jobs` is where scheduled production work belongs
  anyway, so it is not test-only infrastructure.
- The offset clock is real code on a production path, guarded by a flag rather than excluded
  from the binary — the same trade ADR-0010 §2 made and defended for base URLs, and the same
  accepted risk that configuration can point somewhere wrong.
- Fixtures needing `RUN_EVALUATOR` stay red until stage 5. That is the pipeline working.
- ADR-0010's seeding reasoning survives for pre-existing state, which is most fixtures. Only
  its absolute claim is withdrawn.

## Amendments

None.
