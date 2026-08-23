# RFC-0013 — One response shape per endpoint

**Status:** Proposed 2026-08-23 · **Binding form:** _(none yet)_
· **Schema:** _(no delta)_
· **Amends:** the stage-3 fixtures, which contradict each other on 11 endpoints.

## Summary

Eleven endpoints are snapshotted two or more incompatible ways across the
approved fixtures. `Compare` is strict in both directions by design — its own
comment says an endpoint returning more than the snapshot records is an
unreviewed change to the API surface — so no implementation satisfies them.

This is a defect in the stage-3 artifacts rather than something implementation
got wrong. It surfaced only when a real API started answering, because until
then every fixture failed identically at the first request.

Two of the eleven are naming disagreements and need a decision. The other nine
are snapshots that omit a field the endpoint always returns.

## 1. What "incompatible" means here

A field can be omitted from a response only when its value is absent. So if any
snapshot shows a field with an EMPTY value — `0`, `null`, `[]`, `false` — while
another omits it in the same state, no serialization rule produces both:

```
POST /shortlists          (both: a freshly created round, zero entries)
  entry_count         shortlist_lifecycle#0 = 0     absent in verification_and_overdue_flagging#5
  unnotified_count    shortlist_lifecycle#0 = 0     absent in            "
  first_confirmed_at  shortlist_lifecycle#0 = null  absent in            "
```

That is the test applied throughout. It found eleven endpoints.

## 2. The serialization rule

Stated once here so a future endpoint does not have to be argued about:

- **Collections are always present**, as `[]` when empty. `null` and absent both
  force a client to special-case a list it could otherwise iterate.
- **Counts and booleans are always present.** `false` is a fact; absent is not.
  A `stale` flag that vanishes when false is indistinguishable from one nobody
  implemented.
- **Optional scalars and objects are omitted when absent** — `locked_until` on
  an unlocked claim, `description` on a round that has none.

The nine non-naming conflicts all resolve by applying this: the snapshots that
omit an always-present field are under-specified and gain it.

## 3. `GET /me/rank` — the object form wins

Eight snapshots use `overall` / `generalist` objects; one uses `overall_rank` /
`generalist_rank`:

```
me/self_reads#1        {rubric_version, ranked, unranked_reason, active,
                        overall{score,rank,out_of}, generalist{...}, skills}
share_link#6           {overall_rank, generalist_rank, skills}
```

Beyond the 8:1 count, the object carries three values — score, rank, and the
size of the pool — and `overall_rank` can name only one of them. A contributor
seeing "rank 2" without "of 4" has been told almost nothing. `share_link#6` is
corrected to the object form.

## 4. `GET /me/reevaluation-status` — `rejection_count`

Three snapshots use `rejection_count`; one uses `rejections_since_reset`. The
same snapshot also omits `claims_eligible`, which the other three carry —
including twice as `[]`. It is the outlier on both counts and is corrected.

`rejections_since_reset` is arguably the more precise name, since the count does
reset. It loses on consistency: three call sites already read `rejection_count`,
and `tier` beside it already says where in the escalation the contributor is.

## 5. What this does NOT change

`GET /me` returns different shapes for a contributor and a hirer —
`principal_type` versus `kind`, availability versus capabilities. That is not a
conflict: they are different account types with different fields, and an earlier
draft of this analysis wrongly counted it as one. It stays as it is, including
the `kind`/`principal_type` inconsistency, which is ugly but not contradictory.

## 6. The check that should have caught this

`scripts/validate_fixtures.py` checks placeholders, seed names and bound
references. It does not compare response shapes across fixtures, which is why
eleven contradictions passed stage-3 review.

A `--check-shapes` pass is added: for each (method, path), it groups snapshots
by expected key set and fails when a field appears empty in one and absent in
another. That is the exact test in §1, and it runs in CI.

## Consequences

- Roughly fifteen fixture steps change. Every change adds a field the endpoint
  already returns, or corrects the two names in §3 and §4. No assertion about
  BEHAVIOUR is weakened, and no expected status changes.
- The fixtures stop being self-contradictory, so the remaining failures are
  implementation gaps rather than an unsatisfiable contract.
- `Compare` stays strict. An earlier proposal to relax it to subset matching is
  withdrawn: subset matching would let an endpoint return both `overall_rank`
  and `overall` and call that a pass, hiding the disagreement instead of
  resolving it.
- CLAUDE.md forbids an implementer editing a fixture to make one pass. These
  edits are made under this RFC, by the Planner, and none of them is "make the
  current implementation pass" — several require fields no controller returns
  yet.

## Open questions

_None._
