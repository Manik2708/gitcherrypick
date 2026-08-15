# ADR-0008 — The discovery HTTP surface

**Status:** Accepted · **From:** [RFC-0008](../rfc/RFC-0008-discovery-http-surface.md)
· **Schema:** [`RFC-0008-*.schema`](../rfc/RFC-0008-discovery-http-surface.schema)
· **Amends:** ADR-0002, ADR-0003, ADR-0005, ADR-0007

## Decision

1. **Discovery has a fixed HTTP surface.** Search, leaderboards, scorecards, shortlists,
   contact requests, saved searches, claim reads, contributor self-reads, and the admin
   skill-request queue are specified below and are binding.
2. **Search filter names are the `saved_searches.filters` keys.** `skills` is csv and means
   AND. The repeated `?skill=` form does not exist.
3. **Rank is global.** It is computed over the whole scored population and the view filters
   it. A filtered-out contributor leaves a gap in the sequence.
4. **Every search response names its ordering** in `ranked_by` and reports
   `inactive_hidden`. Every result row carries `rank` and `active`.
5. **A lapsed contributor is hidden by default, ranked regardless, and reachable via
   `include_inactive=true`.** This replaces the second sentence of ADR-0002 §6.
6. **`not_looking` is excluded everywhere a hirer looks, from the ranked population, and by
   no toggle.** It is an opt-out, not a lapse.
7. **Leaderboards always show everyone** and take no `include_inactive`.
8. **Shortlisting is two-phase.** Staging an entry discloses nothing; `confirm` creates every
   unnotified entry's contact request in one transaction and is irreversible.
9. **A notified entry can never be removed.** Removal is permitted only while
   `notified_at IS NULL`.
10. **Secondary skills appear on a scorecard**, unranked. Standing gates searchability, not
    disclosure.
11. **`evidence_within_months` has no default.** An unset filter filters nothing.
12. **`per_page` is capped at 50, default 20.**

## Implementation

### Endpoints

```
GET    /search                            hirer + hiring capability
GET    /leaderboard                       hirer + hiring capability
GET    /contributors/{id}/scorecard       hirer + hiring capability + AssertNotSelf

POST   /shortlists                        -> status draft
GET    /shortlists
GET    /shortlists/{id}
PATCH  /shortlists/{id}
POST   /shortlists/{id}/entries           stage; discloses nothing
DELETE /shortlists/{id}/entries/{user_id} only while notified_at IS NULL
POST   /shortlists/{id}/confirm           notify unnotified; IRREVERSIBLE
POST   /shortlists/{id}/close
GET    /shortlists/{id}/contact-requests  statuses; email only after acceptance

GET    /me/contact-requests
POST   /me/contact-requests/{id}/accept   releases email, refreshes availability
POST   /me/contact-requests/{id}/decline

POST   /saved-searches
GET    /saved-searches
GET    /saved-searches/{id}/results       replays filters AS THE CALLER
DELETE /saved-searches/{id}

GET    /claims                            own, newest first
GET    /claims/{id}                       own only — 404 otherwise

GET    /me/rank
GET    /me/verification
GET    /me/reevaluation-status

GET    /admin/skill-requests
POST   /admin/skill-requests/{id}/decide
```

### Search parameters

| Parameter                | Type    | Notes                                     |
| ------------------------ | ------- | ----------------------------------------- |
| `skills`                 | csv     | AND — `HAVING count(distinct skill_id)=n` |
| `min_skill_score`        | number  | 0–100                                     |
| `min_overall_score`      | number  | 0–100                                     |
| `min_generalist_score`   | number  | **No upper bound**                        |
| `availability`           | csv     |                                           |
| `include_inactive`       | boolean | Default false                             |
| `evidence_within_months` | integer | No default                                |
| `q`                      | string  |                                           |
| `page`, `per_page`       | integer | max 50, default 20                        |

An unknown key is **422**, never ignored.

### The gate stack

Applied in SQL, in this order, never as post-filters:

```
NOT self                                    -- AssertNotSelf, join condition
AND standing = 'primary'
AND rubric_version = $active
AND availability.status <> 'not_looking'
AND (availability.expires_at > now() OR $include_inactive)
```

The first four are unconditional. Only the last is a parameter.

### Ranking

`RANK() OVER (ORDER BY <ordering> DESC)` is computed over the **unfiltered** scored
population in a CTE, then filtered, then paged. Filtering after ranking is what produces the
gaps, and is required — ranking after filtering would renumber and the board would disagree.

| Query                                   | `ranked_by`    |
| --------------------------------------- | -------------- |
| exactly one `skills` value              | `skill:<slug>` |
| `min_generalist_score`, no single skill | `generalist`   |
| anything else                           | `overall`      |

### Inactive rows

`active` is `availability.expires_at > now()`, computed at query time. Never stored — ADR-0002
requires the row to survive a lapse so a returning contributor finds their setting intact.

```
last_confirmed_at = user_availability.updated_at
inactive_for_days = floor(now() - expires_at)
```

### Confirm

```
BEGIN
  SELECT ... FROM shortlist_entries WHERE shortlist_id = $1 AND notified_at IS NULL FOR UPDATE
  INSERT INTO contact_requests (...)          -- one row per selected entry
  UPDATE shortlist_entries SET notified_at = now() WHERE ...
  UPDATE shortlists SET status='open', first_confirmed_at = coalesce(first_confirmed_at, now())
  Notifier.Send(...)                          -- through port.Notifier, after commit
COMMIT
```

`uq_contact_request (shortlist_id, user_id)` makes a concurrent double confirm safe
independently of `notified_at`.

## Steps

1. Migration: `ALTER TYPE shortlist_status ADD VALUE 'draft'` **in its own file**, ahead of
   everything else. Verified: batching it with the following statements fails with
   `unsafe use of new value "draft"`.
2. Migration: `shortlists.status` default → `'draft'`; `shortlists.first_confirmed_at`;
   `shortlist_entries.notified_at`; `idx_shortlist_entries_unnotified`.
3. `port.SearchRepository` — one query builder producing the CTE above. The self-exclusion is
   a join condition; `include_inactive` is the only branch.
4. `ranked_by` resolution as a pure function of the parsed filter set, unit-tested.
5. Search + leaderboard controllers. Leaderboard rejects `include_inactive` as unknown.
6. Scorecard read, with secondary skills included and `rank: null`.
7. Shortlist CRUD, staging, and `DELETE` guarded on `notified_at IS NULL`.
8. `confirm` as the transaction above, with the notifier called after commit.
9. Contact request accept/decline; acceptance refreshes availability.
10. Saved searches, with filter validation shared with step 3's parser.
11. Claim reads and the three `/me` self-reads.
12. Admin skill-request queue and decide, with slug/alias collision checks.

## Consequences

- **A rank sequence with holes is a normal response.** Every client must treat `rank` as a
  label, not an index. Code assuming `results[i].rank == i + 1` is wrong.
- **Two write paths exist where ADR-0005 had one**, and the second is irreversible. A bug
  that confirms early cannot be undone by any means the platform offers.
- **`not_looking` contributors are unrankable.** Their `/me/rank` returns nulls with
  `unranked_reason: "opted_out"`. If that proves discouraging in practice it needs a new RFC,
  not a patch here.
- **The ranking CTE is full-population on every search.** Step 3 is the place performance
  will hurt first, and open question 1 is about exactly that.

## Open questions

Carried from RFC-0008, **unresolved at acceptance**. The values below are in force until a
later ADR changes them; none blocks stage 4.

| #   | Question                                                               | In force                  |
| --- | ---------------------------------------------------------------------- | ------------------------- |
| 1   | `per_page` max 50 — chosen to bound the scorecard join, never measured | 50, revisit after stage 4 |
| 2   | Should `inactive_for_days` be bucketed rather than exact?              | Exact                     |
| 3   | Should `q=` name search surface inactive people without the toggle?    | Yes — see RFC-0008 §OQ3   |

## Amendments

| Date       | Change                 |
| ---------- | ---------------------- |
| 2026-08-15 | Accepted from RFC-0008 |
