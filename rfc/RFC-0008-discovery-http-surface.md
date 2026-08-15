# RFC-0008 — The discovery HTTP surface

**Status:** Proposed 2026-08-15 · **Schema:** [RFC-0008-discovery-http-surface.schema](RFC-0008-discovery-http-surface.schema)
· **Amends:** RFC-0002, RFC-0005 (and ADR-0002, ADR-0005), RFC-0003 §Endpoints, RFC-0007 §Endpoints

## Summary

ADR-0002, ADR-0003 and ADR-0007 each fix an HTTP surface in an `### Endpoints` section.
**ADR-0005 does not.** It specifies discovery semantically — the gate order in SQL, the
same-transaction contact-request property, the exact payment disclosure — but never says
which URL any of it lives behind.

Writing the stage 3 fixtures made that gap concrete: the discovery cases could not be
written without paths, so paths were invented while writing tests. That inverts the
pipeline. This RFC moves them upstream where they can be reviewed as a design.

Most of what follows is a mapping onto HTTP of behaviour already approved. **Three changes
are genuinely new and amend approved decisions** — each is marked and argued where it
appears:

| #   | Adds                                                 | Amends          |
| --- | ---------------------------------------------------- | --------------- |
| 1   | Search and leaderboards                              | RFC-0005        |
| 1a  | **Inactive contributors stay ranked and reachable**  | **ADR-0002 §6** |
| 2   | Scorecard reads (hirer-authenticated)                | RFC-0005        |
| 3   | Shortlists                                           | RFC-0005        |
| 3a  | **Shortlisting is two-phase: stage, then confirm**   | **ADR-0005**    |
| 4   | Contact requests, from both sides                    | RFC-0005        |
| 5   | Saved searches — a table with no endpoint            | RFC-0005        |
| 6   | Claim reads                                          | RFC-0003        |
| 7   | Contributor self-reads: rank, verification, standing | RFC-0002        |
| 8   | The admin skill-request queue                        | RFC-0003        |

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
| `availability`           | csv     | Status filter, on top of the always-on gates               |
| `include_inactive`       | boolean | **Default false.** See §1a                                 |
| `evidence_within_months` | integer | Recency of the newest scored PR. **No default**            |
| `q`                      | string  | Display name / login                                       |
| `page`, `per_page`       | integer | `per_page` max **50**, default **20**                      |

`skills=go,kubernetes` replaces the repeated `?skill=` form the fixtures used. One
representation, and it round-trips through `saved_searches.filters` unchanged.

**`evidence_within_months` has no default**, and the distinction matters because two
unrelated kinds of staleness are easy to conflate:

| Concept                  | Measures                          | Example                      |
| ------------------------ | --------------------------------- | ---------------------------- |
| Availability window      | How recently they said "I'm open" | Confirmed 46 days ago        |
| `evidence_within_months` | How old their **code** is         | Newest scored PR merged 2023 |

They are independent — someone can tick "looking for work" this morning and have shipped
nothing since 2023. A silent 24-month default would remove that person from every search
while their dashboard still showed them ranked 12th, with nothing anywhere explaining the
contradiction. An unset filter filters nothing.

### The gates are not optional and not parameters

```
NOT self                        -- AssertNotSelf, as a join condition
AND standing = 'primary'        -- only ranked skills match a skill filter
AND rubric_version = $active
AND availability.status <> 'not_looking'
```

None of these are expressible as query parameters. A hirer cannot ask to see secondary
skills, themselves, or someone who opted out — so there is no parameter to request it.

### 1a · Inactive contributors — amends ADR-0002 §6

ADR-0002 §6 reads: _"Availability expires after 15 days. A lapsed contributor is invisible
to hirers."_ **The second sentence is replaced.** A lapsed contributor is now hidden by
default, ranked regardless, and reachable when a hirer asks.

The reason the old rule was too blunt: lapsing means _"has not confirmed recently"_, which
is not the same as _"unavailable"_. Someone ranked 3rd who forgot to click a button is
still, in all likelihood, the third best match — and the platform was refusing to mention
they exist.

**Rank is global.** It is computed over the whole scored population, and the default view
_filters_ that ranking rather than recomputing it. So a hidden contributor leaves a **gap**
in the rank sequence:

```
?per_page=60                       -> 60 rows, ranks 1-61, no 50   (50 is inactive)
?per_page=60&include_inactive=true -> 60 rows, ranks 1-60          (50 returns, 61 drops)
```

The count is what the hirer asked for; which sixty people fill it is what the toggle
changes. `inactive_hidden` is returned alongside `total` so the gap reads as a filter
rather than a bug, and so the toggle is discoverable at the moment it is relevant.

**A rank is meaningless without saying rank _in what_**, so every response names its
ordering in `ranked_by` and the row-level `rank` is a position in exactly that order:

| Query                                      | `ranked_by`    | Because                              |
| ------------------------------------------ | -------------- | ------------------------------------ |
| exactly one `skills` value                 | `skill:<slug>` | The hirer asked about one skill      |
| `min_generalist_score` and no single skill | `generalist`   | They are filtering on breadth        |
| anything else, including multi-skill       | `overall`      | No single skill score could order it |

This is why a multi-skill search can return someone at rank 3 whose individual skill ranks
are both 2 — the list is ordered by overall score, and the row says so. Without
`ranked_by`, `rank` silently means different things in different responses.

**Inactive rows carry their staleness**, because a hirer betting on someone quiet needs to
know how quiet:

```json
{
  "rank": 50,
  "display_name": "Carol Diaz",
  "active": false,
  "availability": {
    "status": "looking_for_job",
    "last_confirmed_at": "2026-06-30",
    "inactive_for_days": 31
  }
}
```

Both numbers appear because they answer different questions — `last_confirmed_at` is how
stale the signal is, `inactive_for_days` is how long they have been gone. They differ by
the 15-day window, so showing only one invites the wrong arithmetic.

**`not_looking` is not affected by any of this.** It is an explicit opt-out rather than a
stale yes, so it is excluded everywhere a hirer looks and **no toggle reveals it**. It is
also excluded from the ranked population entirely: leaving it in would put permanent
unexplainable gaps in every board, since no parameter could ever fill them.

### Leaderboard

`GET /leaderboard?kind=overall|generalist|skill&skill=<slug>` — `kind=skill` requires
`skill` and returns only `primary` standings.

**The leaderboard always shows everyone**, inactive included, and takes no
`include_inactive` parameter. It is the definitive global ranking; search is the actionable
subset of it. That asymmetry is deliberate: when a hirer sees rank 50 missing from a search,
the board is where they find out who fills it.

Tie-breakers (ADR-0005) order the result but **are never serialized**; ties display
alphabetically.

## 2 · Scorecard reads

```
GET /contributors/{id}/scorecard   hirer, requires hiring capability, AssertNotSelf
GET /public/scorecard/{token}      unauthenticated — already in ADR-0002
```

Two paths because they are two different documents. The hirer view is the full scorecard;
the public view is what a contributor chose to publish via a share link, and ADR-0002
already owns it. A hirer without hiring capability gets **403**, not a redacted body — a
partial scorecard is a scraping surface.

**Secondary skills appear here, unranked.** Standing gates _searchability_, not disclosure:
a hirer already looking at someone should see everything that person evidenced. So a
secondary skill scoring 81 is shown with `standing: "secondary"` and `rank: null`, even
though no `skills=` filter would ever have found it. The two facts are not in tension —
one is about being found, the other about what is true once you are.

Following §1a, an **inactive contributor's scorecard is readable** and carries the same
`active: false` block. `not_looking` remains 404.

## 3 · Shortlists

```
POST   /shortlists                        create; status draft, tentative_result_date required
GET    /shortlists                        org-scoped list
GET    /shortlists/{id}
PATCH  /shortlists/{id}                   name, description, tentative_result_date
POST   /shortlists/{id}/entries           stage a contributor — discloses NOTHING
DELETE /shortlists/{id}/entries/{user_id} only while that entry is unnotified
POST   /shortlists/{id}/confirm           notify every unnotified entry — IRREVERSIBLE
POST   /shortlists/{id}/close             -> status closed, stamps closed_at
```

**Scoped to the organization, not the hirer.** `shortlists.organization_id` is the owner and
every member sees and edits them; `created_by` and `added_by` record who acted. A shortlist
that vanished when a recruiter left the company would be worse than useless.

### 3a · Staging and confirming — amends ADR-0005

ADR-0005 says adding an entry creates a `contact_request` in the same transaction. **The
trigger moves from _add_ to _confirm_.** The same-transaction property is preserved and is
in fact strengthened: one confirm writes every contact request for that round atomically.

The rule this exists to serve is that **a shortlisted contributor can never be un-shortlisted**.
Once someone has been told an organization is interested, that is a fact, and an endpoint
that erased the record of it would be pretending otherwise. But a rule that strict is only
tolerable if there is a moment _before_ it binds — otherwise a single mis-click is
permanent and irreversible.

So building a shortlist is private, and sending it is deliberate:

```
POST /shortlists                     -> draft
POST /shortlists/{id}/entries  x3       nothing sent, all three removable
DELETE .../entries/{user_id}            fine — they were never told
POST /shortlists/{id}/confirm        -> 3 emails, status open, those 3 now permanent

  ... day 3, a fourth candidate ...

POST /shortlists/{id}/entries           removable — this one is unnotified
POST /shortlists/{id}/confirm        -> 1 email. The first three are not re-notified.
```

Confirm notifies exactly the entries where `notified_at IS NULL`, so a shortlist stays a
living round rather than fragmenting across several lists with several result dates. One
rule covers every entry, whenever it was added: **removable until confirmed, permanent
after.**

The confirm response and the UI preceding it must state the irreversibility explicitly.
`uq_contact_request (shortlist_id, user_id)` makes a double confirm safe regardless.

`tentative_result_date` is required at creation because ADR-0005 makes it the thing the
contributor is told and computes the overdue ratio against it. Editing it later does not
rewrite any contact request already sent — those carry their own copy, by design.

**`AssertNotSelf` is called at stage time**, not at confirm. Failing at confirm would mean
discovering at send time that one of your candidates was never eligible.

## 4 · Contact requests

```
GET  /me/contact-requests                   contributor; their own pending + answered
POST /me/contact-requests/{id}/accept       releases email, stamps email_released_at
POST /me/contact-requests/{id}/decline
GET  /shortlists/{id}/contact-requests      hirer; statuses only
```

The asymmetry follows ADR-0005: the contributor sees the organization, the
`tentative_result_date`, and the payment disclosure when
`hirer_accounts.payment_verified_at IS NULL`. The hirer sees a status and, **only after
acceptance**, an email. Nothing about shortlist membership is visible to the contributor
beyond their own pending requests — so there is no endpoint that would tell them.

An inactive contributor receives an ordinary contact request; being quiet is not refusal,
and the email is plausibly what brings them back. **Accepting refreshes their availability
window**, since answering yes is a clearer statement of availability than the button they
forgot to click.

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
platform into a competition against named individuals, which is what the ADR-0005 rule
guards against.

Per §1a, **rank is global and survives lapsing.** A contributor who has gone quiet still
sees their true position, flagged with `active: false` so they understand they are hidden
from the default view. Only `not_looking` yields `rank: null`, with
`unranked_reason: "opted_out"` — and that is a state they chose.

`GET /me/reevaluation-status` makes the ADR-0007 cooldown legible before a contributor
spends a request on a 429.

## 8 · Admin skill requests

```
GET  /admin/skill-requests              pending queue
POST /admin/skill-requests/{id}/decide  approve -> creates the skill + aliases; reject
```

RFC-0003 specifies `POST /skill-requests` and auto-dedupe, but nothing that drains the
queue. Approval is the only way a skill enters the catalogue, so without this the catalogue
is immutable after seeding.

---

## Consequences

**A rank sequence with holes in it is now a normal response.** Every client that renders
search results has to treat rank as a label rather than an index, and any code that assumes
`results[i].rank == i + 1` is wrong. This is the cost of keeping one global ranking instead
of recomputing per query, and it is the reason `inactive_hidden` is returned.

**Two write paths now exist where ADR-0005 had one.** Staging an entry and confirming a
shortlist are separate transactions with different disclosure consequences, and the second
is irreversible. The migration ordering constraint on `ALTER TYPE ... ADD VALUE` is verified
in the schema file rather than assumed.

**Fixtures written before this RFC encode the superseded behaviour** — a repeated `?skill=`
parameter, entry removal after email release, and a lapsed Carol who is unranked. All three
are rewritten against this document on approval. Better a fixture edited once now than an
API shaped permanently by whatever a test author typed.

## Open questions

1. **`per_page` max 50** is chosen to bound the per-row scorecard join, not measured. Worth
   revisiting once the ranked query is benchmarked against a realistic corpus in stage 4.
2. **Should `inactive_for_days` be bucketed** (`"31 days"` vs `"1-2 months"`)? Precise day
   counts on a hidden profile are a small amount of behavioural information about someone
   who is not actively participating.
3. **Does an inactive contributor appear in `q=` name search** when `include_inactive` is
   false? Proposed: yes, because searching someone by name means you already know they
   exist, and a hirer looking for a specific person is not browsing the market.
