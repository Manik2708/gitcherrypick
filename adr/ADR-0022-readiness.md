# ADR-0022 — Telling a contributor whether anybody can find them

**Status:** Accepted · **From:** [RFC-0022](../rfc/RFC-0022-readiness.md)
· **Amends:** ADR-0018 · **Schema:** none

## Decision

1. **`GET /me/profile` reports readiness**: whether a hirer can find them today, and what is
   in the way. No schema change — every input already exists.
2. **Blocking and limiting are separate lists.** Blocking means no hirer can find you;
   limiting means you are findable and a class of role cannot reach you. One list would
   present an urgent thing and a deliberate choice alike.
3. **`no_scored_claim` is a blocker.** Search reads `overall_score IS NOT NULL`, so somebody
   with availability set and every field filled is in no result until a claim is judged.
   Nothing said so before; it is the only item that cannot be fixed on the page reporting it,
   so it links to the one that can.
4. **Computed on the server.** Every condition is a clause of the search query, and a client
   copy would drift in the direction that leaves somebody believing they are visible.
5. **The response carries codes, not sentences.** Words belong to the screen, as with field
   errors.
6. **Opting out is reported, not scolded.** It is a decision that is working.

## Implementation

`port.ContributorProfile.Readiness`, computed in `ProfileService.profile` from the
contributor, their preferences and the platform clock. The controller flattens it to
`{findable, blocking[], limiting[]}`.

The panel sits at the top of "Being found" — above availability, because it is the question
the page is about and everything below it is a field.

## Steps

1. **Domain.** `Readiness`, `ReadinessItem` and the codes.
2. **Service.** `readiness()` beside the profile read, restating the search gates.
3. **Controller.** `readiness` on the profile body.
4. **Frontend.** `ReadinessPanel`, and the form telling the page when a field changed.
5. **Fixtures.** `readiness_says_what_is_left` replaces
   `profile_matchable_needs_both_halves`, which pinned a rule ADR-0021 deleted.

## Consequences

- A contributor can see why they are not being found, which the platform could previously
  only demonstrate by silence.
- The rules are now stated in two voices — SQL and prose — and the prose is generated from
  the same conditions rather than written beside them. If a gate changes, this changes.

## Open questions
