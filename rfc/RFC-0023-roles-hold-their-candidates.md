# RFC-0023 — Roles hold their candidates

**Status:** Approved 2026-09-20 · **Binding form:** [ADR-0023](../adr/ADR-0023-roles-hold-their-candidates.md)
· **Supersedes parts of:** ADR-0005 §9, ADR-0008 §3a
· **Schema:** edits at origin in `RFC-0005`, `RFC-0008` and `RFC-0019` (nothing is deployed)

## Problem

A shortlist was a hiring round: a name, a description, a status, a promised date, and a list of
people. ADR-0019 §3 then made it **one round, one role** — a round could not exist without a job,
and a second job needed a second round.

From that moment the round was a second name for the job. It carried a second title next to the
role's title, a second description next to the role's description, and a second status that could
say `open` while the role said `closed`. A hirer answering "who have we approached for this
opening" had to hold two objects in their head and know which one the question was about.

The nesting also spread. `contact_requests.shortlist_id` reached the job through the round, so the
one thing a contributor is actually told about — the role — was two joins away from the request
that told them. The search exclusion ("stop offering people already on this job") had to go
candidates → rounds → role, and the candidate list needed `DISTINCT ON (user_id)` to undo the
duplication a round-per-role model could produce.

## Decision

1. **There is no `shortlists` table.** `role_candidates (role_id, user_id, note, added_by,
added_at, notified_at)` replaces `shortlist_entries`, and `contact_requests.role_id` replaces
   `shortlist_id`.
2. **Staging and contacting move onto the role.** `POST …/roles/{id}/candidates`,
   `DELETE …/candidates/{userID}`, `POST …/roles/{id}/contact`, `GET …/contact-requests`.
   `/shortlists` is gone.
3. **Nothing about disclosure changes.** Staging tells nobody; the contact step is the
   irreversible act; the address is released only by an acceptance.
4. **The promised date is asked at the moment of contact**, not when a job is written. It is what
   the people being told are promised, and roles stay immutable (ADR-0019 §13) — a date that lived
   on the role could not be revised without superseding the job.
5. **`roles.first_contacted_at`** replaces `shortlists.first_confirmed_at`, and the overdue sweep
   counts roles that have it set against the latest date their contact requests promised.
6. **One row per person per job** is the primary key, so the candidate list is one line per human
   by construction rather than by `DISTINCT ON`.

## Consequences

- The duplicate a round-per-role model allowed — the same contributor staged twice for one job —
  cannot be represented.
- The exclusion query loses a join and a `DISTINCT`.
- A hirer sees one object where they had two, and the UI loses a whole screen.
- The word "shortlist" survives in exactly one place: `already_shortlisted` on a search response,
  which is a count of people excluded and reads correctly either way.
