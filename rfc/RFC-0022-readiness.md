# RFC-0022 — Telling a contributor whether anybody can find them

**Status:** Approved 2026-09-20 · **Binding form:** [ADR-0022](../adr/ADR-0022-readiness.md)
· **Amends:** ADR-0018 · **Schema:** none — this adds no column

## Summary

The rules that decide whether a hirer can see a contributor live in the search query:
`overall_score IS NOT NULL`, the availability gate, the lapse comparison, the country join.
None of them is visible from the contributor's own screen.

So somebody can turn on availability, fill in every field on "Being found", and appear in no
result anywhere — because no claim of theirs has been scored — with nothing on the platform
saying so. They find out by never hearing from anybody.

`GET /me/profile` now carries a **readiness** block: whether they are findable, and what is
in the way.

## 1. Blocking and limiting are different answers

| kind         | means                                                        |
| ------------ | ------------------------------------------------------------ |
| **blocking** | no hirer can find you at all                                 |
| **limiting** | you are findable, and a whole class of role cannot reach you |

Flattening them into one checklist would present an urgent thing and a deliberate choice as
the same row — the same mistake as showing a lapsed window and an opt-out alike.

**Blocking:** never answered, opted out, lapsed, and **no scored claim**.

**Limiting:** no country, no verified first pull request (or one still being verified), no
stated years in a job, and no stated preferences.

## 2. `no_scored_claim` is the one nobody knows about

Search reads `overall_score IS NOT NULL`. A contributor with availability set and every
field filled is in no result until a claim of theirs has been judged, and no screen said so.
It is the only item here that cannot be fixed on the page that reports it, which is why it
carries a link to the one that can.

## 3. Computed on the server, rendered by the client

Every condition is a clause of the search query. A client deriving them would be a second
copy of the gate stack, and the two would drift in the direction that leaves somebody
believing they are visible when they are not.

The response carries **codes**; the words belong to the screen. That matches how field
errors already work, and keeps the copy where copy is edited.

## 4. Opting out is reported, not scolded

`opted_out` appears as a blocker because it is one — but it is a decision that is working,
and what the screen owes them is that it is in force, not a prompt to undo it.

## Consequences

- `/me/profile` grows a field. No schema change: every input already exists.
- `profile_matchable_needs_both_halves` is **deleted**. It asserted the composition rule
  ADR-0021 removed, and its replacement asserts this instead.
- The panel reads its own copy of the profile, so the form tells it when a field changed —
  a panel still asking for a country somebody just gave is worse than no panel.

## Open questions

None.
