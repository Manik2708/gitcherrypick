# RFC-0008 — The discovery HTTP surface

**Status:** Proposed 2026-08-15 · **Schema:** none — this RFC adds no columns
· **Amends:** RFC-0005 (and ADR-0005), RFC-0003 §Endpoints, RFC-0007 §Endpoints

## Summary

ADR-0002, ADR-0003 and ADR-0007 each fix an HTTP surface in an `### Endpoints` section.
**ADR-0005 does not.** It specifies discovery semantically — the gate order in SQL, the
same-transaction contact-request property, the exact payment disclosure — but never says
which URL any of it lives behind.

Writing the stage 3 fixtures made that gap concrete: the discovery cases could not be
written without paths, so paths were invented while writing tests. That inverts the
pipeline. This RFC moves them upstream where they can be reviewed as a design, and adds the
handful of reads elsewhere that were likewise never assigned a URL.

**No behaviour is proposed here that isn't already approved.** Every gate, disclosure and
transaction property below is quoted from an existing ADR. What is new is only the mapping
onto HTTP.

| #   | Adds                                                 | Amends   |
| --- | ---------------------------------------------------- | -------- |
| 1   | Search and leaderboards                              | RFC-0005 |
| 2   | Scorecard reads (hirer-authenticated)                | RFC-0005 |
| 3   | Shortlists and their entries                         | RFC-0005 |
| 4   | Contact requests, from both sides                    | RFC-0005 |
| 5   | Saved searches — a table with no endpoint            | RFC-0005 |
| 6   | Claim reads                                          | RFC-0003 |
| 7   | Contributor self-reads: rank, verification, standing | RFC-0002 |
| 8   | The admin skill-request queue                        | RFC-0003 |

---

## 1 · Search and leaderboards

```
GET /search        hirer, requires hiring capability
GET /leaderboard   hirer, requires hiring capability
```

### Filter names come from the schema, not from taste

`saved_searches.filters` is already specified in `RFC-0005-ranking-and-discovery.schema`
with a worked example. Its keys are the query parameter names, so a saved search is
replayable as a query string without a translation layer:

| Parameter                | Type    | Meaning                                                    |
| ------------------------ | ------- | ---------------------------------------------------------- |
| `skills`                 | csv     | **AND** — `HAVING count(distinct skill_id) = n` (ADR-0005) |
| `min_skill_score`        | number  | Applies to every skill in `skills`                         |
| `min_overall_score`      | number  | `users.overall_score`                                      |
| `min_generalist_score`   | number  | ADR-0007 §Endpoints; `idx_users_generalist_score`          |
| `availability`           | csv     | Status filter, on top of the always-on unexpired gate      |
| `evidence_within_months` | integer | Recency of the newest scored PR                            |
| `q`                      | string  | Display name / login                                       |
| `page`, `per_page`       | integer | `per_page` max 50, default 20                              |

`skills=go,kubernetes` replaces the repeated `?skill=` form the fixtures used. One
representation, and it round-trips through `saved_searches.filters` unchanged.

### The gates are not optional and not parameters

Every response is subject to the ADR-0005 gate stack, applied in SQL:

```
availability set AND expires_at > now()
AND NOT self                       -- AssertNotSelf, as a join condition
AND user_skills.standing = 'primary'
AND rubric_version = $active
```

None of these are expressible as query parameters. A hirer cannot ask to see lapsed
profiles, secondary skills, or themselves — so there is no parameter to request it.

### Leaderboard

`GET /leaderboard?kind=overall|generalist|skill&skill=<slug>` — `kind=skill` requires
`skill`, and returns only `primary` standings. Tie-breakers (ADR-0005) order the result but
**are never serialized**; ties display alphabetically.

## 2 · Scorecard reads

```
GET /contributors/{id}/scorecard   hirer, requires hiring capability, AssertNotSelf
GET /public/scorecard/{token}      unauthenticated — already in ADR-0002
```

Two paths because they are two different documents. The hirer view is the full scorecard.
The public view is what a contributor chose to publish about themselves via a share link,
and ADR-0002 already owns it. A hirer without hiring capability gets **403**, not a redacted
body — a partial scorecard is a scraping surface.

## 3 · Shortlists

```
POST   /shortlists                        create; tentative_result_date required
GET    /shortlists                        org-scoped list
GET    /shortlists/{id}
PATCH  /shortlists/{id}                   name, description, tentative_result_date
POST   /shortlists/{id}/close             -> status closed, stamps closed_at
POST   /shortlists/{id}/entries           add a contributor
DELETE /shortlists/{id}/entries/{user_id} remove before contact is answered
```

**Scoped to the organization, not the hirer.** `shortlists.organization_id` is the owner and
every member of that org sees and edits them; `created_by` and `added_by` record who acted.
A shortlist that vanished when a recruiter left the company would be worse than useless.

`tentative_result_date` is required at creation because ADR-0005 makes it the thing the
contributor is told, and the overdue ratio is computed against it. Editing it later does not
rewrite any contact request already sent — those carry their own copy, by design.

### Adding an entry is not a neutral act

Per ADR-0005, adding an entry creates a `contact_request` **in the same transaction**. So
`POST /shortlists/{id}/entries` is the endpoint that discloses an organization's interest to
a contributor, and it is where `AssertNotSelf` is called for the third time.

## 4 · Contact requests

```
GET  /me/contact-requests                   contributor; their own pending + answered
POST /me/contact-requests/{id}/accept       releases email, stamps email_released_at
POST /me/contact-requests/{id}/decline
GET  /shortlists/{id}/contact-requests      hirer; statuses only
```

The asymmetry is deliberate and follows ADR-0005: the contributor sees the organization, the
`tentative_result_date`, and the payment disclosure when
`hirer_accounts.payment_verified_at IS NULL`. The hirer sees a status and, **only after
acceptance**, an email. Nothing about shortlist membership is visible to the contributor
beyond their own pending requests — so there is no endpoint that would tell them.

## 5 · Saved searches

```
POST   /saved-searches
GET    /saved-searches
GET    /saved-searches/{id}/results        replay: same response shape as GET /search
DELETE /saved-searches/{id}
```

The table exists in the approved schema with no way to reach it. Org-scoped, like
shortlists. `filters` is stored verbatim and validated against the §1 parameter set, so an
unknown key is a 422 rather than a filter that silently does nothing.

`/results` replays the stored filters **as the caller**, not as whoever saved them. The
gates in §1 are properties of the requester — so a colleague replaying a saved search still
cannot see themselves, and a search saved while an org was verified returns 403 if that
verification is later revoked. A saved search stores a question, never an answer.

## 6 · Claim reads

```
GET /claims        contributor; own claims, newest first
GET /claims/{id}   contributor; own claim only — 404 for anyone else
```

ADR-0003 specifies every claim **write** and `GET /claims/{id}/withdraw-preview`, but no
plain read. **404, not 403,** for a claim belonging to someone else: a 403 confirms the id
exists, and claim ids are otherwise unguessable.

## 7 · Contributor self-reads

```
GET /me/rank                 own rank per primary skill, plus overall and generalist
GET /me/verification         hirer; own verification request status
GET /me/reevaluation-status  contributor; cooldown tier and cooldown_until
```

`GET /me/rank` exists because ADR-0005 says contributors see **only their own** rank. Absent
a dedicated endpoint the number has no home, and the temptation is to leak it into the
leaderboard response.

It returns positions and totals and **nothing identifying anyone else** — no neighbours, no
names, no adjacent scores. A ladder showing who sits immediately above you turns the
platform into a competition against named individuals, which is what the ADR-0005 rule is
guarding against.

**Rank is computed over the same population the leaderboard shows** — availability set and
unexpired. Ranking a lapsed contributor against a board they do not appear on would make the
two numbers disagree, so a lapsed contributor gets `rank: null` with an `unranked_reason`
rather than a position nobody else can see. Their _scores_ are unaffected: going quiet hides
you, it does not unmake what your evidence was worth.

`GET /me/reevaluation-status` makes the ADR-0007 cooldown legible before a contributor
spends a request on a 429.

## 8 · Admin skill requests

```
GET  /admin/skill-requests            pending queue
POST /admin/skill-requests/{id}/decide  approve -> creates the skill + aliases; reject
```

RFC-0003 specifies `POST /skill-requests` and auto-dedupe, but nothing that drains the
queue. Approval is the only way a skill enters the catalogue, so without this the catalogue
is immutable after seeding.

---

## Consequences

The fixtures written in stage 3 currently encode a repeated `?skill=` parameter and a
`GET /shortlists/{id}` payload chosen without review. On approval both are rewritten against
this document. That rewrite is the point of the RFC — better a fixture edited once now than
an API shaped permanently by whatever a test author typed.

Three endpoints here have no fixture yet and gain one: `PATCH /shortlists/{id}`,
`DELETE /shortlists/{id}/entries/{user_id}`, and the `/saved-searches` trio.

## Open questions

1. **`per_page` maximum of 50** — chosen to bound the scorecard join, not measured. Lower it
   to 25 if the ranked query proves expensive.
2. **`evidence_within_months`** is in the saved-search example but has no stated default.
   Proposed: unset means no recency filter. A default would silently hide contributors whose
   evidence is old but whose standing is current.
3. **`DELETE /shortlists/{id}/entries/{user_id}` after acceptance** — proposed to 409 once
   `email_released_at` is set. The disclosure already happened and removing the entry would
   destroy the record of it.
