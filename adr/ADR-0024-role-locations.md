# ADR-0024 — Role locations

**Status:** Accepted · **From:** [RFC-0024](../rfc/RFC-0024-role-locations.md)
· **Schema:** [`RFC-0024-*.schema`](../rfc/RFC-0024-role-locations.schema) — committed **inert**; see Steps 1 and 3
· **Amends:** ADR-0017 (amendment 4), ADR-0019 (amendment 5), ADR-0020 (amendment 1), ADR-0021 (amendment 1)

Approved by the owner on 2026-10-17, as one document. The owner was offered splitting §5
and §7–§9 of the RFC into documents of their own and declined.

## Decision

### Where a role's work is

1. **A role holds a set of location entries**, replacing `roles.location`, `roles.address_id`
   and `role_eligible_countries`. There are three kinds and only three: remote from
   anywhere; remote within a named country; onsite at one of the organisation's addresses.
2. **Remote is country only.** A remote entry never points at an address, so a company may
   offer remote work where it has no office. **Onsite always points at an address** and
   names no country of its own.
3. **Remote-anywhere and remote-in-a-country never share a role.** Several remote countries
   may. A role has **at least one** entry, drafts included. A repeated entry is collapsed,
   not refused.
4. **Entries share the role's immutability** (ADR-0019 §13): written on create, on a draft's
   `PATCH` and on `revise`, never after a role opens.
5. **Reach.** Remote-anywhere admits everybody; remote-in-a-country admits whoever states
   that country; **onsite admits everybody**, including somebody with no stated country. A
   role reaches a contributor when any entry admits them. For a `full_time` role an entry
   counts only if its mode is one the contributor ticked (`open_to_remote` /
   `open_to_onsite`); the other engagements read their own flag. This gates `GET /openings`
   and nothing else.
6. **`eligible_countries` is removed outright** — table, request key, response key.
7. **A contributor sees a city and a country** for an onsite entry and a country or
   "anywhere" for a remote one. Never a street, a postcode or an address id.

### An organisation's addresses

8. **Only owners add or remove an address.** Every seat may list them. Removal is a stamp,
   refused while a draft or open role uses the address, allowed when only closed roles do.
   **An address is never edited.**
9. **The main office is never removed by the address routes.** It goes only when the whole
   organisation goes fully remote (decision 15).
10. **Only an organisation's first address is reviewed by a person.** Every later one is added
    by an owner, unreviewed.

### An organisation's kind

11. **Onboarding asks whether the organisation has an office or is fully remote.** With an
    office the address is compulsory; fully remote, none is given, and one sent anyway is
    refused.
12. **A fully remote organisation has no address, adds none, and posts no onsite entry.**
13. **A fully remote organisation may ask for an office.** An owner submits the first
    address; it waits for an administrator; nothing changes until approval. On approval it
    becomes the main office and the organisation is an office one. Office requests have
    **their own administrator queue and routes**.
14. **A rejected request leaves the organisation fully remote.** The requesting owner is
    emailed the reason and any owner may ask again at once — a new row, no limit, no wait.
    **The requesting owner is emailed on approval too.** A pending request cannot be
    withdrawn or changed.
15. **An organisation with an office may go fully remote** — **owners only**, and only while
    no draft or open role has an onsite entry. Every address, main office included, is
    stamped removed in the same transaction. It is not reviewed and emails nobody. Coming
    back is decision 13 again, reviewed again.

### Refusals

16. **Every `invalid_role` names its field**, with `position` (1-based; amendment 1 §1.6) when the field is a list. The code
    is unchanged.
17. **Onboarding refusals are `invalid_onboarding` with `field`**, replacing the accidental
    `invalid_claim` they fall through to today.
18. **Location, address and kind refusals have codes of their own** (Implementation).

## Implementation

### Endpoints

| Method   | Path                                                 | Who                   | Notes                                                                                        |
| -------- | ---------------------------------------------------- | --------------------- | -------------------------------------------------------------------------------------------- |
| `GET`    | `/orgs/{orgID}/addresses`                            | any hirer with a seat | `{"workplace", "addresses": […], "office_request": null \| {…}}`                             |
| `POST`   | `/orgs/{orgID}/addresses`                            | owner                 | `201`; refused for a fully remote organisation                                               |
| `DELETE` | `/orgs/{orgID}/addresses/{addressID}`                | owner                 | stamp; `204`                                                                                 |
| `POST`   | `/orgs/{orgID}/office-request`                       | owner                 | `201`; fully remote organisations only                                                       |
| `POST`   | `/orgs/{orgID}/fully-remote`                         | owner                 | no body; `200` with the `GET …/addresses` envelope                                           |
| `GET`    | `/admin/office-requests`                             | admin                 | undecided, oldest first                                                                      |
| `POST`   | `/admin/office-requests/{requestID}/decide`          | admin                 | `{"decision", "reason"}` (amendment 1 §1.1); rejection without a reason is `reason_required` |
| `POST`   | `/organizations`, `/organizations/revise`            | as ADR-0017           | gain required `workplace`                                                                    |
| —        | `/org-roles/{orgID}/roles` create, `PATCH`, `revise` | as ADR-0019           | `locations` replaces `location`, `address_id`, `eligible_countries`                          |

`GET …/addresses` is where a client learns the organisation's kind: the role form already
reads it for the onsite picker, and disables Onsite and the add-address form from the same
response.

### Bodies

Hirer request: `{"mode": "remote"}`, `{"mode": "remote", "country": "IN"}`,
`{"mode": "onsite", "address_id": "…"}`. The three old keys are refused as unknown.

Hirer read: every entry carries `mode`, `country` and `address`, with the address embedded
in full (including `is_main_office` and `removed`). Ordered remote first, then country,
then city.

Contributor read (contact request, `GET /openings`): `{"mode": "remote", "country": "IN" |
null}` and `{"mode": "onsite", "country": "IN", "city": "Bengaluru"}`.

Contact email: one phrase from the entries at the contributor's level of detail, country
names from `port.PlaceService` and falling back to the code.

### Codes

| Code                           | Status                                         | When                                                                                                       |
| ------------------------------ | ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `invalid_role` + `field`       | 400 / 422                                      | every existing role, close, settings and contact refusal; RFC-0024 §5 maps each to its field               |
| `role_location_required`       | 422                                            | no entries                                                                                                 |
| `role_location_invalid`        | 422                                            | bad mode; remote with `address_id`; onsite with `country`; malformed country                               |
| `onsite_needs_address`         | 422                                            | onsite with no `address_id`                                                                                |
| `remote_anywhere_exclusive`    | 422                                            | remote-anywhere beside remote-in-a-country                                                                 |
| `address_not_found`            | 422 on a role, 404 on `DELETE`                 | not this organisation's, identical for a nonexistent id                                                    |
| `address_removed`              | 422                                            | on create, `PATCH`, `revise` and `open`                                                                    |
| `invalid_address` + `field`    | 422                                            | address and office-request bodies                                                                          |
| `address_in_use` + `roles`     | 409                                            | `DELETE` while a draft or open role uses it                                                                |
| `main_office_not_removable`    | 409                                            | `DELETE` on the main office                                                                                |
| `organization_fully_remote`    | 409 on addresses and the switch; 422 on a role | adding an address, an onsite entry, or switching when already fully remote                                 |
| `office_request_pending`       | 409                                            | a second request while one is undecided                                                                    |
| `organization_has_office`      | 409                                            | an office organisation asking for an office                                                                |
| `onsite_roles_exist` + `roles` | 409                                            | going fully remote while a draft or open role has an onsite entry                                          |
| `invalid_onboarding` + `field` | 422                                            | `name`, `email`, `headcount`, `workplace`, `address.country`, `address.city`, `address.street1`, `address` |

Role-side location codes also carry `field: "locations"` and `position`, 1-based (amendment 1 §1.5, §1.6). An undecodable body
stays a bare `400`, naming `field` only for an unknown key or a mistyped value.

### Invariants the service holds

A `CHECK` sees one row, so these live in the transactions that write the rows:

- a role has at least one entry; remote-anywhere and remote-in-a-country never share one; an
  onsite address belongs to the organisation and is live; a fully remote organisation's role
  has no onsite entry;
- an office organisation has exactly one live main office; a fully remote one has no live
  address.

**Every write that reads `organizations.workplace` locks the organisation's row first** —
role create, `PATCH`, `revise` and `open`, address add, office-request approval and the
switch. Without it, an onsite entry and the switch to fully remote could each pass their
check and both commit.

### Transactions

- **Approval of an office request:** write the address as the main office (`added_by` the
  requester), set `workplace = 'office'`, stamp the request. Email the requester.
- **Rejection:** stamp the request decided with no `address_id`. Email the requester the
  reason.
- **Going fully remote:** lock; refuse if a draft or open role has an onsite entry; stamp every
  live address removed (`removed_by` the owner); set `workplace = 'fully_remote'`.
- **Onboarding approval (`Promote`):** keyed on `workplace` — an office organisation gets its
  main office with the organisation and the seat; a fully remote one gets none.

### Deleted, not ported

`RoleRepository.Matching` (the retired `/me/roles` query), `Role.EligibleIn` and
`Role.HiresAnywhere`. `GET /openings` is the one contributor-facing match.

## Steps

1. **Fixtures first, red** (integration-tester). Rewrite, not edit to pass:
   `roles/an_open_role_is_immutable` (locations, and `field` on its five bare `invalid_role`
   steps: base above package, amount with no currency, `other` with no note, `superseded` as
   a reason, `hired_via_platform` naming nobody — amendment 1 §1.11), `roles/candidates_are_staged_then_contacted`,
   `openings/a_bar_reaches_only_who_clears_it`, `discovery/contact_request_requires_consent`,
   `admin/verification_and_overdue_flagging`, `orgs/onboarding_creates_nothing_until_approved`
   (every submission states `workplace`; the third company is fully remote). Then the new
   fixtures listed in RFC-0024 "Seeds and fixtures".
2. **Seeds** — in the same change as steps 3–8 (amendment 1 §1.12). `e2e/seed.go` and `cmd/devdata/demo.go`: every organisation states `workplace`;
   office ones get a main office, at least one a second address; at least one is fully
   remote; seeded roles get one remote-anywhere entry; address bindings
   `<org>.address.main` / `<org>.address.<key>`; `make dev` prints kinds and addresses.
3. **Schema.** Delete the two `GUARD` lines in `RFC-0024-*.schema` **in the same change that
   lands steps 4–8**, never before: `e2e/schema.go`, `scripts/e2e.sh`, `scripts/db-test.sh`
   and `cmd/devdata` apply every `rfc/*.schema`, and a live delta breaks `make e2e`,
   `make db-test` and `make dev` against code that still reads `roles.location`. The delta is
   applied as written — `ALTER`/`DROP` in its own file, no edits at origin. Rebuild dev with
   `make dev ARGS='--reset'`.
4. **Domain and ports.** `Role.Locations []RoleLocation`, the workplace kind, the office
   request; port changes; `make mocks`.
5. **Repositories.** `role_locations` read and write; the per-entry gate in `GET /openings`;
   addresses list, add, stamp; office requests and their queue; `Promote` on `workplace`; the
   switch; the organisation-row lock; the three deletions above.
6. **Services.** Location rules and codes; `field` on every existing `invalid_role`;
   `invalid_onboarding`; address and switch authority; office-request submit, decide,
   resubmit.
7. **Controllers.** The five organisation routes and two admin routes; role, contact and
   openings bodies; unknown and mistyped keys named in `field`.
8. **Notifier.** The location phrase on the contact email; office-request approved and
   rejected templates.
9. **Frontend** (stage 6). Role form with Remote (country picker) and Onsite (address list,
   disabled when fully remote); owner screens for addresses, the office request and going
   fully remote; the onboarding dropdown, address disabled when fully remote, on the revise
   screen too; the admin office-request queue; contract types and the endpoints file; `field`
   shown beside its control; `roleFilters.ts` and the openings and discoverability views from
   `locations`.

## Consequences

- The office role, which never worked end to end, works; one role can be offered in several
  places.
- An address's country is display only. The country gate bites only on a role that is
  entirely remote-in-a-country, and an onsite role is shown in every country.
- A member cannot add an address, even where `role_create_authority` lets them open roles.
- A wrong main office cannot be corrected in place. The exit is to go fully remote and ask
  for an office again: every draft and open onsite role closed or revised first, every
  address removed — the correct ones too — and the replacement reviewed. It is an exit, not
  an edit.
- An organisation's kind changes in both directions, deliberately unequally: losing an office
  is the owner's act, gaining one is an administrator's decision, and it can recur.
- The administrator gains a queue. Contributors learn a city for the first time.
- Every role and onboarding refusal becomes actionable.
- Six existing fixtures are rewritten and roughly a dozen new ones written.

## Open questions

## Amendments

| Date       | Change                                                                                                                             |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| 2026-10-17 | **Amendment 1** — bodies, codes, ordering and emails the fixtures needed pinned; two corrections                                   |
| 2026-10-17 | **Amendment 1, corrected** — decide refusals follow the shared handler (422 `invalid_state`); the approval email names the country |

### 1. What the fixtures needed pinned

Raised by the integration-tester while writing ADR-0024's fixtures. Every item below either
copies an existing convention or corrects this document; one (§1.3, the reason on a rejected
request) was decided by the owner. Approved by the owner on 2026-10-17.

#### 1.1 Administrator decide

`POST /admin/office-requests/{requestID}/decide` takes the shared admin decision envelope,
**not** the `{"approve", "reason"}` the endpoint table showed:

```json
{ "decision": "approved" | "rejected", "reason": "…" }
```

It answers `200` with the shared decision body `{"id", "status": "approved" | "rejected",
"reviewed_at", "reviewed_by"}`, as onboarding, verification and skill-request decisions do. A
rejection with no reason is `reason_required`. A malformed `requestID` is `400 invalid_id`. A body that does not decode, or a `decision` that is not `approved` or `rejected`, is `422 invalid_state` — the shared decide handler's answer on the other admin decide routes. (Onboarding's decide answers `invalid_registration` from a handler of its own; that is not copied.) A request already decided is **`409 request_already_decided`** with `status`
naming the standing decision — the code skill and verification requests already use.
(Onboarding's own double-decide currently answers `slug_taken`; that is a defect of ADR-0017's
route, not a convention, and is not copied.)

#### 1.2 Administrator list

`GET /admin/office-requests` answers `{"total": n, "office_requests": [...]}`, undecided only,
**oldest `created_at` first**. Each row:

```json
{
  "id": "…",
  "organization": { "id": "…", "name": "…" },
  "address": { "country", "city", "postal_code", "street1", "street2" },
  "requested_by": "<owner username>",
  "created_at": "…",
  "supersedes": "<previous rejected request id>"
}
```

`supersedes` is omitted when there is none, as on an onboarding submission, so an
administrator reading a second request can find the first.

#### 1.3 The office-request object

One object, used by `POST /orgs/{orgID}/office-request` (`201`) and by `office_request` in
`GET /orgs/{orgID}/addresses`:

```json
{
  "id": "…",
  "status": "pending" | "rejected",
  "address": { "country", "city", "postal_code", "street1", "street2" },
  "requested_at": "…",
  "decided_at": "…",
  "reason": "…"
}
```

`decided_at` and `reason` are present only when `status` is `rejected`. `office_request` in the
envelope is the **latest** request while the organisation is fully remote, and `null` when
there has never been one **and** once one is approved — the approved address is then in
`addresses` as the main office.

**A rejected request's `reason` is shown to every seat** that can read `GET …/addresses`, as
well as emailed to the owner who asked — the owner's decision. A member drafting a role can
then see why Onsite is unavailable without asking an owner what an email said; the reason is
written to the organisation about its own address and discloses nothing outside it.

#### 1.4 `roles` on a refusal

`address_in_use` and `onsite_roles_exist` carry `roles: [{"id", "title", "status"}]`, oldest
first. `status` is `draft` or `open`, because those are the only roles that block.

#### 1.5 Refusal precedence on a role

Checked in this order; the first that applies is the answer:

1. `locations` empty or absent → `role_location_required`, `field: "locations"`, no `position`.
2. Each entry, in request order:
   1. mode not `remote`/`onsite`, remote with `address_id`, onsite with `country`, malformed
      country → `role_location_invalid`;
   2. onsite, and the organisation is fully remote → `organization_fully_remote`;
   3. onsite with no `address_id` → `onsite_needs_address`;
   4. address not this organisation's or nonexistent → `address_not_found`;
   5. address removed → `address_removed`.
3. Remote-anywhere beside remote-in-a-country → `remote_anywhere_exclusive`, naming the
   **later** of the two in request order.

Repeated entries are collapsed before step 3. A fully remote organisation's onsite entry is
refused for being onsite, whatever is wrong with its address.

#### 1.6 `position`, not `index`

Every place this ADR says `index` reads **`position`, 1-based** — the convention of claim
failure items and of `role_questions.position`. It applies to `locations` refusals and to an
`invalid_role` naming `questions`.

#### 1.7 Order of entries

Both the hirer read and the contributor read order entries remote first, then by country,
then by city. A contributor's remote countries that repeat are shown once.

#### 1.8 Addresses

`POST /orgs/{orgID}/addresses` answers `201` with the same address object a role embeds:
`{"id", "country", "city", "postal_code", "street1", "street2", "is_main_office", "removed"}`.
`GET` lists live addresses with the main office first, then by `created_at` ascending, so an
owner sees them in the order they were added. `invalid_address` names top-level fields —
`country`, `city`, `street1` — because the address is the whole body there, not a nested key.

#### 1.9 Onboarding

- Revising checks **the form first and the code second**, and **a refused form does not
  spend the code** — today's behaviour, kept. RFC-0024 §7 said the code refusals "keep
  coming first, as now"; they never did. Kept because a company that mistypes its city
  should not have to wait for a new code to correct it.
- `office` with no address is refused naming `address.country`, the first missing part.
- `fully_remote` with an address object whose parts are **all empty** is accepted as "not
  sent": that is what a form with disabled fields submits. Any non-empty part is
  `invalid_onboarding`, `field: "address"`.

#### 1.10 Office-request emails

To the owner who submitted the request, one email per decision, in the subject style of the
existing templates:

| Kind     | Subject                                | Body must contain               |
| -------- | -------------------------------------- | ------------------------------- |
| approved | "Your office address was approved"     | the city and the country's name |
| rejected | "Your office address was not approved" | the administrator's reason      |

The country is named as the contact email names it (Implementation, Bodies): from
`port.PlaceService`, falling back to the ISO code only when the place service cannot answer.
Fixtures run against the stand-in, so they assert the name — "Portugal", not "PT".

Fixtures assert recipient, count, subject, and that the rejection body contains the reason.
Further wording is not pinned.

#### 1.11 Corrections to this document and to RFC-0024

- **Step 1** named the five bare `invalid_role` steps of `roles/an_open_role_is_immutable`
  only by count; RFC-0024 "Seeds and fixtures" listed them, wrongly, with "an empty title".
  They are: **base above package**, amount with no currency, `other` with no note,
  `superseded` as a reason, and `hired_via_platform` naming nobody. Step 1 now lists them.
  RFC-0024 is approved and frozen, so its list is corrected here and not in place.
- The endpoint table's decide body and every `index` in Decision 16 and Codes now read as
  §1.1 and §1.6 say, with a pointer here.

#### 1.12 Steps 2 and 3 land together

Step 2 (seeds) cannot precede step 3: `cmd/devdata/demo.go` and `e2e/seed.go` insert
`roles.location`, which the live delta drops, and their new form inserts `role_locations`,
which the inert delta does not create. So **steps 2–8 and the removal of the guards are one
change**. In that change the seeds' legacy branches — the `location` inserts and anything that
assumes an organisation without `workplace` — are **deleted**, not kept beside the new ones.
Until then `make dev` runs against the inert delta and the old seed, as it does today.
