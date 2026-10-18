# ADR-0023 — Roles hold their candidates

**Status:** Accepted · **From:** [RFC-0023](../rfc/RFC-0023-roles-hold-their-candidates.md)
· **Schema:** edits at origin in [`RFC-0005`](../rfc/RFC-0005-ranking-and-discovery.schema), [`RFC-0008`](../rfc/RFC-0008-discovery-http-surface.schema) and [`RFC-0019`](../rfc/RFC-0019-roles.schema)
· **Supersedes parts of:** ADR-0005 §9, ADR-0008 §3a

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

## Implementation

| Layer      | What moved                                                                                                                                                                                  |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Schema     | `role_candidates` and `contact_requests.role_id` at origin in RFC-0005; the staging columns' commentary in RFC-0008; the forward-reference keys and `roles.first_contacted_at` in RFC-0019  |
| Domain     | `Shortlist`, `ShortlistEntry` and `ShortlistStatus` deleted; `RoleCandidate` gains `Note`, `AddedBy`, `AddedAt`; `ContactRequest.ShortlistID` becomes nothing — it already carried `RoleID` |
| Repository | `shortlist.go` becomes `candidates.go` on `RoleRepository`: `Stage`, `Unstage`, `Contact`, `ContactRequestsForRole`, `OverdueRatios`                                                        |
| Service    | `ShortlistService` folded into `RoleService` (`candidates.go`)                                                                                                                              |
| Controller | `/shortlists` deleted; five routes added under `/org-roles/{orgID}/roles/{roleID}`                                                                                                          |
| Jobs       | `FlagOverdueShortlists` → `FlagOverdueRoles`, keyed on `first_contacted_at`                                                                                                                 |

## Steps

1. Schema at origin, in RFC order — there is no migration tool (ADR-0006) and nothing is deployed.
2. Domain, ports, repository, service, controller, and the API wiring.
3. Fixtures: `shortlist_lifecycle` becomes `roles/candidates_are_staged_then_contacted`; four
   others move onto the role surface.
4. The client loses `Shortlists` and `ShortlistDetail`; the role's "who is on it" panel becomes
   the working surface, and search stages onto a role.
