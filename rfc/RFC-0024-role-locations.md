# RFC-0024 — Role locations

**Status:** Approved 2026-10-17 · **Binding form:** [ADR-0024](../adr/ADR-0024-role-locations.md)
· **Schema:** [`RFC-0024-*.schema`](RFC-0024-role-locations.schema) — **inert until the stage-4 code lands** (ADR-0024 step 3), see [Schema](#schema)
· **Amends:** ADR-0019 §2, §5, §6 and every `invalid_role` body · ADR-0017 §3 and §10 (an organisation states whether it has an office), its admin surface, and its "one row per organisation" consequence · ADR-0020 §3 and its contributor filter · ADR-0021's shape mapping

Revision 2 folded in the owner's answers to the eleven questions revision 1 asked. Revision
3 made the onboarding address compulsory. Revision 4 replaces that with the owner's later
ruling — **an organisation says whether it has an office or is fully remote** — and adds the
way a fully remote one later gains an office. Revision 5 folds in the answers to questions C
to F, one of which (E) lets an organisation with an office go fully remote.
[Resolved review comments](#resolved-review-comments) maps every question to what it became.

## Summary

A role says where the work is with one value — `remote` or `address` — and, separately, a
typed list of countries it can hire in. Three things are wrong with that, and the owner met
all three on `/roles`:

- an office role **cannot be created at all**. Nothing over HTTP lists an organisation's
  addresses, nothing adds one, and the dev seed has none, so the form has no address to send
  and the API answers `invalid_role`;
- one role is one place. "Remote, or at either office" is three roles;
- every refusal is the same bare `invalid_role`, so the form cannot say what was wrong.

So: **an organisation keeps a list of addresses its owners add to, and a role holds a set of
location entries.** An **onsite** entry is at one of those addresses. A **remote** entry is
anywhere, or names a country. `eligible_countries` goes, and where a role can hire is read
off its entries. Every `invalid_role` names the field it is about. And **an organisation
says at onboarding whether it has an office or is fully remote**: one with an office gives an
address, and a fully remote one has none, adds none and posts no onsite role until an
administrator has approved its first.

## 1. What the owner decided

These are the brief and the answers, not a proposal. Everything after this section is built
on them.

1. The choices are **Remote** and **Onsite**. Both may be chosen on one role.
2. **Onsite needs at least one address**, picked from the organisation's own.
3. **Remote is country only.** A remote entry is "anywhere" or names a country directly. It
   never points at an address, so a company may offer remote work in a country where it has
   no office. The role form has an address list for onsite and a country picker for remote.
4. **Remote-anywhere and remote-in-a-country are mutually exclusive** on one role. Several
   remote countries are fine.
5. One role may combine them — remote in India, onsite at the Bengaluru address, onsite at
   the US address.
6. **Onsite does not restrict by country.** An onsite entry reaches contributors from any
   country.
7. **`eligible_countries` is removed outright** — table, request key, response key.
8. **Only owners add an address.** Owners may remove one, as a stamp rather than a delete.
   Removal is refused while a draft or open role uses it, naming the roles, and allowed when
   only closed roles do. The main office cannot be removed on its own; it goes only when the
   whole organisation goes fully remote (§1.17). **An address is never edited.**
9. **A contributor sees the city and country of an onsite entry**, never the street or
   postcode. A remote entry shows its country, or "anywhere".
10. **Only an organisation's first address needs human review**; later ones do not.
11. **No database has to survive this.** They are rebuilt.
12. **Every `invalid_role` refusal says which field was wrong** — the existing ones, not only
    the new ones.
13. **Onboarding asks whether the organisation has an office or is fully remote** — "add an
    option of drop down where they could select if org have an office or is fully remote, if
    fully remote then adding address should be disabled and onsite role option should also be
    disabled". With an office, the address is compulsory. Fully remote, there is none.
14. **A fully remote organisation can later change to having an office.** An owner submits
    the first address; it is pending until an administrator decides; until then the
    organisation stays fully remote. On approval that address becomes the main office, and
    adding addresses and onsite roles are enabled.
15. **A rejected office request leaves the organisation fully remote.** The owner is emailed
    the reason, and an owner may submit a new request at once — a new row, no limit, no wait.
16. **Office requests have their own administrator queue and routes**:
    `GET /admin/office-requests` and `POST /admin/office-requests/{requestID}/decide`.
17. **An organisation with an office can change to fully remote**, but only while no draft or
    open role has an onsite entry, and **only an owner may do it**. The owner chose this
    knowing it makes the main office removable on that path.
18. **The owner is emailed when an office request is approved.**

## 2. The model

A role has a set of entries. There are three kinds and only three:

| Entry                | Means                       |
| -------------------- | --------------------------- |
| `remote`, nothing    | remote, from anywhere       |
| `remote`, a country  | remote, within that country |
| `onsite`, an address | at that address             |

Rules, and where each is enforced:

| Rule                                                                          | Enforced by                                      |
| ----------------------------------------------------------------------------- | ------------------------------------------------ |
| Remote never has an address; onsite always has one, and no country of its own | `CHECK` on the row                               |
| No entry appears twice; at most one remote-anywhere entry                     | `UNIQUE NULLS NOT DISTINCT`                      |
| Remote-anywhere never sits beside a remote-in-a-country (§1.4)                | service, in the transaction that writes the role |
| A role has at least one entry                                                 | service, same transaction                        |
| An onsite address is this organisation's, and not removed                     | service, same transaction                        |

The last three span rows or tables, which a `CHECK` cannot see. That is the placement
ADR-0019 already uses for `role_hires` (§Cross-table invariants).

One more rule of the same kind comes from §1.13: **a fully remote organisation's role has no
onsite entry.** The service refuses it in the same transaction, with
`organization_fully_remote` (§5). Its roles are remote-anywhere or remote-in-a-country and
nothing else.

**Entries are part of the role and share its immutability** (ADR-0019 §13). They are written
on create, on a draft's `PATCH`, and on `revise`, and never after a role opens. Adding a
country to an open role is a revision like any other change.

### Who a role reaches

An entry **admits** a contributor by place:

| Entry               | Admits                                                         |
| ------------------- | -------------------------------------------------------------- |
| remote-anywhere     | everybody                                                      |
| remote in a country | whoever states that country as `current_country`               |
| onsite              | everybody, including somebody who has stated no country (§1.6) |

A role reaches a contributor when **at least one entry admits them**. So the country gate
only ever bites on a role whose entries are all remote-in-a-country. Any onsite entry, or
remote-anywhere, opens it to every country.

**The contributor's own shape preference is applied per entry**, which is the only reading
that keeps ADR-0021 true. For a `full_time` role the opening filter picks between
`open_to_remote` and `open_to_onsite` from the role's location. With several entries, an
entry counts only if its mode is one they ticked **and** its place admits them. The other
four engagements read their own flag, as now, and then need any one entry to admit them.

Worked through for a **full-time role that is onsite in Bengaluru and remote in India**:

| Contributor                           | Shown it? | Through                                       |
| ------------------------------------- | --------- | --------------------------------------------- |
| in India, ticked remote only          | yes       | the remote-in-India entry                     |
| in Germany, ticked remote only        | no        | remote entry is India only; onsite not ticked |
| in Germany, ticked onsite             | yes       | the onsite entry                              |
| no country stated, ticked onsite      | yes       | the onsite entry                              |
| no country stated, ticked remote only | no        | an unstated country is not India              |

The same two entries on a **contract** role reach everybody who ticked contract: the onsite
entry admits them all, and the remote-in-India entry then narrows nothing. It still tells a
contributor something true, so it is accepted.

This gate applies in exactly one place, `GET /openings`, which is a contributor's own view.
A hirer's search stays a preference they edit, and they may approach anybody (ADR-0021).

### Smaller choices made here, which the owner did not state

Not open questions, because each follows from §1 or carries an existing rule forward — but
they are mine, so they are listed where they can be struck out:

- **At least one entry is required**, on a draft too. `roles.location` is `NOT NULL` today
  and this keeps that.
- **A repeated entry is collapsed, not refused** — the same country twice, or the same
  address twice. `RoleService.validate` already does this for a repeated eligible country,
  and a form that sends "India, India" has not made a mistake worth a `422`.
- **A remote country is checked for shape only** — two letters, upper-cased by the service.
  The picker comes from `port.PlaceService` and fails open (ADR-0018 §9), exactly as
  `eligible_countries` was handled.
- **Addresses are not de-duplicated and not capped**, and neither are remote countries.
- **An undecodable body stays a bare `400 invalid_role`.** It names a field when the failure
  is an unknown key or a value of the wrong type, and nothing when the JSON does not parse,
  because then there is no field to name (§5).

- **Onboarding's refusals get a code of their own, `invalid_onboarding`**, for everything
  that form can get wrong and not only the office choice (§7). They share one validator and
  today all fall through to `invalid_claim`.
- **A fully remote submission that sends an address anyway is refused**, not silently
  stripped (§7). An address object whose parts are all empty counts as not sent, because
  that is what a form with disabled fields submits.
- **A pending office request cannot be withdrawn or changed by the owner** (§8) —
  _confirmed by the owner._ A
  rejection, followed by a new request, is the way to correct one. An
  administrator may be reading it, which is ADR-0017's reason for having no `PATCH` on a
  submission.
- **Going fully remote is not reviewed and notifies nobody** — _confirmed by the owner._ It adds no address, so the
  rule that only a first address is reviewed (§1.10) does not reach it, and it takes
  capability away rather than granting any. Who did it is recorded: every address it
  removes carries `removed_by` (§9).
- **The organisation's kind is shown to its own seats and to administrators, and to nobody
  else.** Nothing here puts "fully remote" on a contact request, an opening or a scorecard.

Two choices from revision 1 are gone with the design that needed them: one address serving
as both a remote and an onsite entry, and two remote entries in one country via two
addresses. Remote no longer touches addresses.

## 3. An organisation's addresses

An organisation with an office starts with exactly one address: the main office, given at
onboarding and created when an administrator approves the company (§7). This adds the rest.
**A fully remote organisation has no address and cannot use this surface to get one** — its
first goes through §8. Mounted on the existing `/orgs` router beside the roster:

| Method   | Path                                  | Who                   | Purpose                           |
| -------- | ------------------------------------- | --------------------- | --------------------------------- |
| `GET`    | `/orgs/{orgID}/addresses`             | any hirer with a seat | live addresses, main office first |
| `POST`   | `/orgs/{orgID}/addresses`             | **owner**             | add one; `201` with the address   |
| `DELETE` | `/orgs/{orgID}/addresses/{addressID}` | **owner**             | stamp it removed; `204`           |

`GET` answers `{"workplace": "office" | "fully_remote", "addresses": […], "office_request":
null | {…}}`. **This is where the client learns the organisation's kind**: the role form
already makes this request to fill the Onsite picker, so the same response tells it to
disable Onsite and the add-address form, and whether a request is waiting (§8). For a fully
remote organisation `addresses` is empty.

`POST` on a fully remote organisation is refused, `409 organization_fully_remote`. That
holds while an office request is pending, too.

There is **no `PATCH`**. `POST` takes `country`, `city`, `street1`, and optionally
`postal_code` and `street2` — the columns ADR-0017 already defined, validated as onboarding
validates them.

**Authority.** Owners only, both ways, like the roster (ADR-0016 §4). A non-owner gets the
refusal the roster already gives one. This is deliberately **not** one of the three settings
of ADR-0019 §11: those govern roles, and an organisation that sets `role_create_authority`
to `any_hirer` still has its addresses kept by owners. `added_by` and `removed_by` are
stamped. Reading the list is open to every seat, because a member drafting a role needs it.

**Removing is a stamp, not a delete.** A closed role still says where it was, and role rows
are never deleted, so an address a role has ever used could not be deleted anyway.

- a **draft or open** role uses it → refused, `409 address_in_use`, with `roles` naming them;
- only **closed** roles use it → allowed. It leaves the picker; those roles still read it;
- the **main office** → refused, `409 main_office_not_removable`. This route never removes
  it; the only path that does is going fully remote, which removes every address (§9).

**A removed address cannot be on a role that is being written or opened**:
`address_removed` on create, on a draft's `PATCH`, on `revise`, and on `open`. Stating it on
`open` is what makes this RFC independent of whether a closed role can be reopened, which
ADR-0019 answers both ways (see "Found on the way"). If closing is final the check on `open`
never fires. If it is not, it stops a closed role coming back at a retired address.

**Why no edits, now that an address's country no longer decides who a role reaches.** An
open role is immutable (ADR-0019 §13), and its onsite entries show a contributor a city and
a country. Editing the address row would change what somebody already contacted was told,
through a table the role's immutability does not cover. A wrong address is fixed by adding
the right one and removing the wrong one.

**Review** (§1.10). Every address added through this surface is a **later** one, added by an
owner and reviewed by nobody. An organisation's **first** address never arrives here: it was
on the onboarding submission an administrator read (§7), or it is the office request an
administrator decides (§8). So this surface has no pending state.

## 4. The role bodies

### What a hirer sends

`POST /org-roles/{orgID}/roles`, `PATCH …/roles/{id}` and `POST …/roles/{id}/revise` lose
`location`, `address_id` and `eligible_countries`, and gain:

```json
"locations": [
  { "mode": "remote", "country": "IN" },
  { "mode": "remote", "country": "DE" },
  { "mode": "onsite", "address_id": "0193…" }
]
```

`{ "mode": "remote" }` alone is remote-anywhere. The three old keys are **refused**, not
ignored: the decoder already rejects unknown fields, and under §5 the refusal now names the
key, so a client still sending `eligible_countries` is told exactly that.

### What a hirer reads

`GET …/roles`, `GET …/roles/{id}` and every write's response carry `locations` with the
address **embedded**, so a list of roles needs no second request and a closed role at a
since-removed address still renders. Every entry has the same three keys:

```json
"locations": [
  { "mode": "remote", "country": "IN", "address": null },
  {
    "mode": "onsite",
    "country": null,
    "address": {
      "id": "0193…", "country": "IN", "city": "Bengaluru", "postal_code": "560001",
      "street1": "1 MG Road", "street2": "", "is_main_office": true, "removed": false
    }
  }
]
```

Ordered remote first, then by country and city, so two reads of one role agree.

### What a contributor reads

The contact request and `GET /openings` share one shape (ADR-0019 §2, ADR-0020). It loses
`location` and `eligible_countries` and gains (§1.9):

```json
"locations": [
  { "mode": "remote", "country": "IN" },
  { "mode": "onsite", "country": "IN", "city": "Bengaluru" }
]
```

A remote entry is a country, or `null` for anywhere. An onsite entry is a city and a
country. No street, no postcode, no address id.

## 5. Error codes

### Every `invalid_role` names its field

The service already writes a sentence for each refusal, and the controller drops it:
`writeError` emits a message only from a fixed table with no entry for `invalid_role`.
`logic/roleDraft.ts` exists to re-derive the sentence on the client.

The fix is the one the contact route already uses. `POST …/roles/{id}/contact` answers
`{"error": "invalid_role", "field": "tentative_result_date"}`, and a fixture asserts it.
**Every `invalid_role` now carries `field`**, the request key it is about, and `index` when
that key is a list:

| Route                    | Refusal                                                                 | `field`                                                 |
| ------------------------ | ----------------------------------------------------------------------- | ------------------------------------------------------- |
| create / update / revise | no title                                                                | `title`                                                 |
|                          | not one of the four engagements                                         | `engagement`                                            |
|                          | an amount stated with no valid currency                                 | `currency`                                              |
|                          | an amount that is not positive                                          | whichever of `yearly_ctc`, `yearly_base`, `hourly_rate` |
|                          | base above the package                                                  | `yearly_base`                                           |
|                          | hours outside 1–168                                                     | `expected_hours`                                        |
|                          | a minimum outside 0–80                                                  | `min_office_yoe` or `min_oss_yoe`                       |
|                          | rounds not positive                                                     | `max_interview_rounds`                                  |
|                          | days to offer negative                                                  | `avg_days_to_offer`                                     |
|                          | an empty screening question                                             | `questions`, with `index`                               |
| close                    | a reason that is not offered, `superseded` included                     | `reason`                                                |
|                          | `other` with no note                                                    | `note`                                                  |
|                          | hires named under another reason; none named under `hired_via_platform` | `hired`                                                 |
| settings                 | not one of the three authorities                                        | whichever of the three keys                             |
| contact                  | no date, or a date already past                                         | `tentative_result_date` — unchanged                     |
| any of them              | an unknown key, or a value of the wrong type (`400`)                    | that key                                                |

The code stays `invalid_role` for all of these, so nothing that branches on it breaks. The
sentence is still not sent: a client words its own, and `field` is what lets it put the
words beside the right control.

### The location and address refusals

A set of entries has several ways to be wrong, so these get codes of their own. Each
role-side one also carries `field: "locations"` and `index`, the entry that failed.

| Code                        | Status                         | When                                                                                                                                           |
| --------------------------- | ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `role_location_required`    | 422                            | `locations` empty or absent                                                                                                                    |
| `role_location_invalid`     | 422                            | a mode that is not `remote`/`onsite`; a remote entry with an `address_id`; an onsite entry with a `country`; a country that is not two letters |
| `onsite_needs_address`      | 422                            | an onsite entry with no `address_id` (§1.2)                                                                                                    |
| `remote_anywhere_exclusive` | 422                            | remote-anywhere beside a remote-in-a-country (§1.4)                                                                                            |
| `address_not_found`         | 422 on a role, 404 on `DELETE` | not this organisation's — identical for an id that does not exist, so a guess confirms nothing                                                 |
| `address_removed`           | 422                            | the organisation's own address, since removed; also on `open`                                                                                  |
| `invalid_address`           | 422                            | `POST …/addresses`, with `field`                                                                                                               |
| `address_in_use`            | 409                            | `DELETE`, with `roles` listing the draft and open roles                                                                                        |
| `main_office_not_removable` | 409                            | `DELETE` on the main office                                                                                                                    |

Four more come from the organisation's kind (§1.13, §1.14, §1.17):

| Code                        | Status                                                                          | When                                                                                                                                |
| --------------------------- | ------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `organization_fully_remote` | 409 on `POST …/addresses`; 422 on a role, with `field: "locations"` and `index` | a fully remote organisation adding an address, or putting an onsite entry on a role                                                 |
| `office_request_pending`    | 409                                                                             | `POST …/office-request` while one is undecided                                                                                      |
| `organization_has_office`   | 409                                                                             | `POST …/office-request` from an organisation that already has an office                                                             |
| `onsite_roles_exist`        | 409                                                                             | `POST …/fully-remote` while a draft or open role has an onsite entry, with `roles` naming them — the same shape as `address_in_use` |

## 6. Every reader of `location` and `eligible_countries` today

Found by grep over `backend/`, `frontend/src/` and `adr/`.

| #   | Reader                                                                                                                                                                                         | Today                                                                                                                                                                                                                                                                                                                                                                                                                              | Under this RFC                                                                                                                                                                                                                                                                                         |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | **`GET /openings`** — `OpeningRepository.Matching`, `repository/postgres/opening.go`. The only **gate**.                                                                                       | Country: no `role_eligible_countries` rows, or one equal to `current_country`. Shape, full-time only: `r.location = 'remote'` picks `open_to_remote`, else `open_to_onsite`.                                                                                                                                                                                                                                                       | One `EXISTS` over `role_locations`: an entry that admits them by place and, for full-time, whose mode they ticked (§2). No join to `organization_addresses` is needed to decide — an onsite entry admits everybody — only to render. The count of openings not met comes off the same pass, as now.    |
| 2   | **Role-driven search** — `frontend/src/logic/roleFilters.ts`. A **convenience**: it pre-fills `GET /search` filters the hirer then edits. The server never reads a role's location for search. | `countries` ← `eligible_countries`; `open_to` ← `remote` or `onsite` from `location`, full-time only.                                                                                                                                                                                                                                                                                                                              | `countries` ← the remote countries **only when every entry is remote-in-a-country**, otherwise undefined: one onsite or remote-anywhere entry means the role reaches every country. `open_to` ← the modes present, full-time only (OR-ed by the server, and either satisfies the role).                |
| 3   | **`for_role` on `GET /search`** (ADR-0008 amendment 3)                                                                                                                                         | Excludes people already staged. Reads neither field.                                                                                                                                                                                                                                                                                                                                                                               | Unchanged.                                                                                                                                                                                                                                                                                             |
| 4   | **Role candidates** — `GET/POST/DELETE …/roles/{id}/candidates`, `POST …/contact` (ADR-0023)                                                                                                   | Read and write `role_candidates` and `contact_requests`. No country or location check.                                                                                                                                                                                                                                                                                                                                             | Unchanged, and deliberately: a hirer may stage and contact anybody (ADR-0021 §Implementation). Their `invalid_role` gains nothing new; it already names its field.                                                                                                                                     |
| 5   | **Contact request body** — `contactRoleBody`, `controller/bodies.go`; also the role half of `GET /openings`                                                                                    | `location`, `eligible_countries`                                                                                                                                                                                                                                                                                                                                                                                                   | `locations` in the contributor shape (§4).                                                                                                                                                                                                                                                             |
| 6   | **Contact request email** — `service/candidates.go` → `role_location` in `adapter/resend/templates.go`                                                                                         | "remote" or "at their office" from `RoleLocation.Label()`                                                                                                                                                                                                                                                                                                                                                                          | One phrase from the entries at the detail of row 5: "remote", "remote within India or Germany", "onsite in Bengaluru, India", joined. Country names from `port.PlaceService`, falling back to the code.                                                                                                |
| 7   | **Hirer role body** — `roleRequest` / `roleBody`, `controller/roles.go`                                                                                                                        | `location`, `address_id`, `eligible_countries`                                                                                                                                                                                                                                                                                                                                                                                     | `locations` (§4).                                                                                                                                                                                                                                                                                      |
| 8   | **Role validation** — `RoleService.validate` / `addressBelongs`, `service/role.go`; also `Close`, `SaveSettings`                                                                               | One address check; countries normalised; every refusal a bare `invalid_role`                                                                                                                                                                                                                                                                                                                                                       | The rules of §2 with the codes of §5, and `field` on every existing refusal. Still reads `OrganizationRepository.Addresses`, which gains the removed stamp.                                                                                                                                            |
| 9   | **Role repository** — `insert`, `UpdateDraft`, `writeChildren`, `hydrate`, `roleColumns`, `repository/postgres/role.go`                                                                        | Two columns and a child table                                                                                                                                                                                                                                                                                                                                                                                                      | `role_locations` written with the other children and hydrated with the address in one query per page of roles.                                                                                                                                                                                         |
| 10  | **`RoleRepository.Matching`** and **`Role.EligibleIn` / `HiresAnywhere`**                                                                                                                      | The query behind `GET /me/roles`, retired by ADR-0019 amendment 1. Called only by its own tests; the two domain helpers have no caller at all.                                                                                                                                                                                                                                                                                     | **Deleted, not ported.** Row 1 is the one contributor-facing match, and a second copy of the rule is how the two would come to disagree.                                                                                                                                                               |
| 11  | **Domain** — `Role.Location`, `Role.AddressID`, `Role.EligibleCountries`, `RoleLocation`, `domain/role.go`                                                                                     |                                                                                                                                                                                                                                                                                                                                                                                                                                    | `Role.Locations []RoleLocation`, a named type holding a mode, a country and an optional `Address`.                                                                                                                                                                                                     |
| 12  | **Client** — `contract.ts`, `pages/Roles.tsx` (form, card), `logic/roleDraft.ts`, `pages/Openings.tsx`, `pages/Discoverability.tsx`, and their tests                                           | Select + `CountryList` labelled "Countries you can hire in"; "Can hire in GB, DE"                                                                                                                                                                                                                                                                                                                                                  | Remote and Onsite controls: the country picker moves under Remote, an address list sits under Onsite, and owners get an add-address form. The location line is rendered from `locations`. `roleDraft.ts` can shrink to what is worth checking before a round trip, since the API now says which field. |
| 13  | **ADR text**                                                                                                                                                                                   | ADR-0019 §2 lists "the location, the eligible countries" on a contact request; §5 says a role "points at an `organization_addresses` row, or is remote"; §6 says "no eligible countries means anywhere". ADR-0020 §3 says "the role states where it can hire" and its filter cites §6. ADR-0017 §10 and its consequence "one row per organisation until roles exist". ADR-0021 §Implementation names "the role-to-search mapping". | Superseded by ADR-0024 on promotion. §6's protection survives in a different form: the blank default it guarded against no longer exists, because a role must say something and "anywhere" is an explicit entry rather than an empty list.                                                             |

Readiness (ADR-0022) reads whether a contributor has stated a country, not anything on a
role, and is untouched.

## 7. Onboarding asks whether there is an office

An amendment to ADR-0017, folded in here because the role rules above rest on it. It is one
section so that it can be read, and struck, on its own.

**What ADR-0017 does today.** The address on a submission is optional and nothing is asked
about why. `ck_onboarding_address` allows all of it or none, `validateOnboarding` refuses only
a partial one, and approval writes the main office `if sub.Country != ""` under the comment
"a fully remote company has none to give". So "no address" can mean a fully remote company
or a form nobody finished, and nothing downstream can tell which.

**What changes.** The form carries an explicit choice, `workplace`: `office` or
`fully_remote`. Required, with no default.

| Where                                                             | Today                                      | Under this RFC                                                                                                                                                                                 |
| ----------------------------------------------------------------- | ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Request** — `POST /organizations`, `POST /organizations/revise` | `address`, optional                        | `workplace`, required. `office`: `address` required — country, city, street; postcode and second line optional as now. `fully_remote`: no `address`.                                           |
| **Schema** — `organization_onboarding`                            | `ck_onboarding_address`: all or none       | `workplace NOT NULL`, and `ck_onboarding_workplace_address` replaces the old constraint: `office` has a country, city and street; `fully_remote` has no part of an address, postcode included. |
| **Schema** — `organizations`                                      | nothing                                    | `workplace NOT NULL`, copied by the approval. Read on every address write and every role write.                                                                                                |
| **Validation** — `validateOnboarding`, shared by both routes      | Refuses a partial address                  | Enforces the rule above. A rejected company revising may change its answer, since a revision is a whole new submission (ADR-0017 §8).                                                          |
| **Approval** — `OnboardingRepository.Promote`                     | Main office written `if sub.Country != ""` | Keyed on the choice, not on a blank: `office` writes the organisation, its main office and the owner seat together; `fully_remote` writes the organisation and the seat, and no address.       |
| **Admin queue** — `onboardingSubmissionBody`                      | `address`, `null` when none was given      | Gains `workplace`. `address` is `null` exactly when it is `fully_remote`, so the reviewer sees a stated choice rather than an absence.                                                         |

Two facts span tables and so belong to the service, not to a `CHECK`: an `office`
organisation has exactly one main office, and a `fully_remote` one has no address rows and
no onsite entries.

**The error body.** These refusals have never had a code. `validateOnboarding` returns a bare
`ErrInvalid`, and the controller's fallback for that is `invalid_claim` — a company told its
_claim_ was rejected, which is a contributor's noun. Under the rule of §5, every one is
`422 {"error": "invalid_onboarding", "field": …}`:

| Refusal                                     | `field`           |
| ------------------------------------------- | ----------------- |
| no name                                     | `name`            |
| no email                                    | `email`           |
| not a headcount band                        | `headcount`       |
| `workplace` absent, or not one of the two   | `workplace`       |
| `office`, and no country or not two letters | `address.country` |
| `office`, and no city                       | `address.city`    |
| `office`, and no street                     | `address.street1` |
| `fully_remote`, and an address was sent     | `address`         |

`field` is the path in the request, and the address is nested there, hence the dot. The
first missing part is the one named. Both routes answer the same body; on `revise` the
verification-code refusals keep coming first, as now. `POST /organizations` still answers
`202` and empty for everything that passes validation (ADR-0017 §2).

**What it does not change.** An address is still not verification evidence. ADR-0017 keeps
ADR-0002's proof kinds so that a three-person studio with no company domain can be verified.
An administrator reads the address; it is not what they decide on.

**Client.** `pages/Onboarding.tsx` gains the dropdown. Fully remote disables the address
fields and sends none; has an office makes country, city and street required. The revise
screen is the same form.

## 8. A fully remote organisation gains an office

§1.14 to §1.16 and §1.18, designed.

| Method | Path                           | Who                   | Purpose                                          |
| ------ | ------------------------------ | --------------------- | ------------------------------------------------ |
| `POST` | `/orgs/{orgID}/office-request` | **owner**             | submit the first address; `201` with the request |
| `GET`  | `/orgs/{orgID}/addresses`      | any hirer with a seat | carries `office_request` (§3)                    |

`POST` takes the same body as `POST …/addresses` and refuses the same way
(`invalid_address` with `field`), plus `office_request_pending` and
`organization_has_office` (§5). It is a separate route from `POST …/addresses` because it is
a different act: that one adds an address at once, and this one asks a person for a change
to what the organisation is.

**While it is pending, nothing changes.** `workplace` is still `fully_remote`, `addresses`
is still empty, `POST …/addresses` and an onsite entry are still refused with
`organization_fully_remote`. `office_request` reads
`{"status": "pending", "address": {…}, "requested_at": …}`, which is what lets the client
say a request is waiting rather than offer the form again.

**Approval, in one transaction:** the address is written to `organization_addresses` as the
main office with `added_by` the owner who asked, `organizations.workplace` becomes `office`,
and the request is stamped. From then on the organisation is indistinguishable from one
onboarded with an office: owners add further addresses unreviewed (§3), and roles take onsite
entries. Existing roles are untouched — they are remote and stay so until revised.

**Storage.** A table of its own, `organization_office_requests`, holding the address flat
and promoted on approval, on the pattern of `organization_onboarding` (ADR-0017 §2, "the
form creates nothing"). A pending row in `organization_addresses` would have made "a fully
remote organisation has no address" false for exactly as long as the request was waiting,
and every reader of that table would have had to know to skip it. No status column: undecided,
rejected and approved are read off `decided_at` and `address_id`.

**The administrator's side** (§1.16). Its own pair of routes, for the reasons ADR-0017
amendment 1 gave onboarding its own: the ids come from a different table, the shape is
different (an address and an existing organisation, not a company and a proposed owner), and
the decision does something different.

| Method | Path                                        | Who   | Purpose                                                          |
| ------ | ------------------------------------------- | ----- | ---------------------------------------------------------------- |
| `GET`  | `/admin/office-requests`                    | admin | undecided requests, oldest first                                 |
| `POST` | `/admin/office-requests/{requestID}/decide` | admin | `{"approve": bool, "reason": "…"}`, as onboarding's decide takes |

A rejection without a reason is refused with `reason_required`, which every other
administrator rejection already uses. Deciding a request that is already decided is a
`409`, as it is for a submission.

**Rejection** (§1.15). The request is stamped decided with no `address_id`; the organisation
stays fully remote and nothing else changes. The owner who asked is emailed the reason. Any
owner may submit a new request at once: a new row, so the administrator reading it can see
what changed, which is how a rejected onboarding submission already works (ADR-0017 §8). No
limit and no waiting period.

**Notifications** (§1.15, §1.18). The owner who submitted the request is emailed on either
decision — the reason on a rejection, and on approval that addresses and onsite roles are
now available. Two Notifier templates, beside onboarding's.

## 9. An organisation with an office goes fully remote

§1.17, designed.

| Method | Path                         | Who       | Purpose                                                    |
| ------ | ---------------------------- | --------- | ---------------------------------------------------------- |
| `POST` | `/orgs/{orgID}/fully-remote` | **owner** | switch; no body; `200` with the `GET …/addresses` envelope |

**Who.** An owner, and only an owner — the owner's answer, and consistent with everything
else that changes the organisation's own record (the roster, ADR-0016 §4; settings,
ADR-0019 §11; addresses, §3). None of ADR-0019 §11's three role settings applies: this
changes no role. A member is refused as the roster refuses one.

**Refused when:**

- a **draft or open** role has an onsite entry → `409 onsite_roles_exist`, with `roles`
  naming them, exactly as `address_in_use` names the roles holding an address. The owner
  closes or revises those roles first; a draft can be edited to drop its onsite entries;
- the organisation is **already fully remote** → `409 organization_fully_remote`.

**What it does, in one transaction:**

1. locks the organisation's row — every role write that reads `workplace` takes the same
   lock, so a role gaining an onsite entry and this switch cannot both pass their checks;
2. checks that no draft or open role has an onsite entry;
3. stamps **every live address removed**, main office included, with `removed_by` the owner;
4. sets `organizations.workplace` to `fully_remote`.

**Closed roles** that name those addresses are untouched. They still read them, as they read
any removed address (§3): a contact request sent last month still says "onsite in
Bengaluru". If closing is not final (see "Found on the way"), reopening one is refused twice
over — `address_removed` and `organization_fully_remote` — and either reason alone is enough.

**Office requests.** None can be pending at this moment: a request is accepted only from a
fully remote organisation (`organization_has_office` otherwise), and approving one makes the
organisation an office one. So an organisation with an office never has a pending request,
and the switch has nothing to cancel.

**Going back** is §8 again, with an administrator's review: the organisation is fully remote,
and its next first address is reviewed like its first one was. The old main office is not
revived — it stays removed, and the approved address becomes a new main office row. The
schema allows this by counting only **live** main offices (`uq_org_main_office` is narrowed
to `removed_at IS NULL`) and by allowing more than one approved request over an
organisation's life; only one may be **pending** at a time.

**Client.** The role form and the addresses screen already read `workplace` from
`GET …/addresses` (§3), so they disable Onsite and the add-address form as soon as the
switch returns. The switch itself belongs with the addresses, owners only, and when refused
lists the roles in the way.

## Schema

[`RFC-0024-role-locations.schema`](RFC-0024-role-locations.schema) holds the delta:

- `organization_workplace` is created; `organization_onboarding` and `organizations` each
  gain `workplace NOT NULL`; `ck_onboarding_address` is replaced by
  `ck_onboarding_workplace_address` (§7);
- `organization_office_requests` is created, with at most one undecided request per
  organisation (§8, §9);
- `organization_addresses` gains `added_by`, `removed_at` and `removed_by`, and RFC-0017's
  `uq_org_main_office` is narrowed to at most one **live** main office (§9). There is no
  `CHECK` that the main office is never removed: the switch removes it, a `CHECK` on the row
  cannot tell the switch from the `DELETE` route, and the service refuses the latter;
- `role_location_mode` and `role_locations (role_id, mode, country, address_id)` are created;
- `roles.location`, `roles.address_id`, `ck_role_location` and the `role_location` enum are
  dropped;
- `role_eligible_countries` is dropped.

It carries no administrator-side table or column beyond the request itself.

**Existing rows.** No database has to survive (§1.11); dev and e2e are rebuilt with
`make dev ARGS='--reset'`. The delta still carries the copy so that it is complete over
seeded rows, and with remote now naming countries the copy is faithful: a remote role with
no eligible countries becomes remote-anywhere, a remote role with countries becomes one
remote entry per country, and an office role becomes one onsite entry. An office role's
eligible countries are not carried, because an onsite entry reaches every country.

**Organisations and submissions already present** get their kind from what they have: an
address means `office`, none means `fully_remote`. That is a fill for seeded rows, applied
before the column becomes `NOT NULL`, and it is faithful — a row with no address is exactly
what fully remote means. Nothing invents a main office.

**The file executes nothing yet.** `rfc/RFC-*.schema` applied in order _is_ the schema:
`e2e/schema.go`, `scripts/e2e.sh`, `scripts/db-test.sh` and `cmd/devdata` glob the directory.
A live draft would drop two columns the running backend selects and break `make e2e`,
`make db-test` and `make dev` before anybody approved anything. So the DDL is written in
full inside one block comment, and the two guard lines come out in the change that lands the
code. It was checked against PostgreSQL 16 with the guards removed, after every earlier
schema file: it applies, the copy maps all three kinds of role, existing organisations and
submissions get the right kind, and each constraint refuses what it should — including an
office with no address, a fully remote submission with one, a second pending request, and a
second live main office, while a removed main office followed by a newly approved one is
accepted.

It is written as `ALTER`/`DROP` in its own file, which is what `rfc/README` asks for.
ADR-0016 to ADR-0023 edited the origin files instead; either builds the same schema, and the
ADR can take whichever the owner prefers.

## Seeds and fixtures

Described only. Nothing under `backend/` is touched by this RFC.

- **Every seeded organisation must state its kind, and none has an address today.**
  `e2e/seed.go` and `cmd/devdata/demo.go` insert organisations directly, bypassing the
  onboarding `Promote`. `organizations.workplace` is `NOT NULL`, so each insert names one,
  and the seeds must not produce a state the product cannot: an `office` organisation gets a
  main office, and a `fully_remote` one gets no address. The suite needs **both kinds** — at
  least one office organisation with a **second address**, or a two-office role cannot be
  exercised, and at least one fully remote one, or the refusals cannot. Address ids want
  bindings (`<org>.address.main`, `<org>.address.<key>`) so a fixture can name them.
- **`orgs/onboarding_creates_nothing_until_approved` changes, and less than revision 3
  said.** Its third company submits "with no office at all" and is accepted; that company is
  now a **fully remote** one, and its `"address": null` in the admin queue stays correct.
  What changes: every submission in the fixture states `workplace`, the queue rows assert
  it, and the step text "an address is optional on purpose" no longer describes the rule —
  the address is absent because the company said it has no office. To be rewritten by the
  integration-tester, not edited to pass.
- **Repository and controller tests that build a submission or an organisation** need a
  kind, and `Promote`'s branch is now keyed on it. Stage-4 work, listed so it is not a
  surprise.
- **Both seeds insert roles with `location = 'remote'`.** Each becomes a role plus one
  remote-anywhere entry, which preserves what every existing fixture assumes about them.
- **Five fixtures assert the old keys** and must be rewritten by the integration-tester, not
  edited to pass: `roles/an_open_role_is_immutable`,
  `roles/candidates_are_staged_then_contacted`, `openings/a_bar_reaches_only_who_clears_it`,
  `discovery/contact_request_requires_consent`, `admin/verification_and_overdue_flagging`.
  The openings one keeps its meaning with the least change of the five: it gates on
  `eligible_countries: ["GB"]`, which is now a remote entry in GB.
- **`roles/an_open_role_is_immutable` also changes for §5.** It asserts a bare
  `{"error": "invalid_role"}` in five steps — an empty title, an amount with no currency,
  `other` with no note, `superseded` as a reason, and `hired_via_platform` naming nobody —
  and each now has a `field` to assert. `roles/candidates_are_staged_then_contacted` already
  asserts `field` and is the pattern.
- **New fixtures, red first** (stage 3 applies to this as to everything):
  - an owner adds an address and every seat can list it; a member's add and a member's
    remove are refused;
  - an in-use address is not removable and the refusal names the roles; one used only by a
    closed role is; the main office is not, through `DELETE`;
  - each code in the second table of §5, with its `index`;
  - `field` for each row of the first table of §5 that no fixture reaches today — the
    amounts, hours, years, rounds, days, questions, the settings keys, an unknown key;
  - a role with several remote countries and two offices round-trips;
  - the five rows of the worked example in §2, which between them pin the country gate, the
    onsite-reaches-everybody rule and the per-entry shape preference;
  - a contributor-facing body carries a city and no street;
  - onboarding: each row of §7's error table, on `POST /organizations` and on `revise`; an
    approved office organisation has exactly one main office and an approved fully remote
    one has no address;
  - a fully remote organisation: `GET …/addresses` says so, `POST …/addresses` is refused,
    and an onsite entry is refused on create, on a draft's `PATCH` and on `revise`;
  - the office request: an owner submits and a member cannot; a second is refused while one
    is pending; nothing is enabled while it waits; approval creates the main office and
    flips the kind in one step, after which an address can be added and an onsite role
    written; an organisation with an office cannot ask; the owner is emailed;
  - the administrator's side: `GET /admin/office-requests` lists undecided requests oldest
    first and is refused to a hirer; a rejection without a reason is refused; a rejection
    leaves the organisation fully remote, emails the reason, and a new request is accepted at
    once; a decided request cannot be decided again;
  - going fully remote: a member is refused; a draft role with an onsite entry and an open
    one each block it, and the refusal names both; once those are closed or revised the
    switch succeeds; every address is stamped removed, main office included, in the same
    step as `workplace` changes; a closed role still renders its removed address; afterwards
    `POST …/addresses` and an onsite entry are refused; a second switch is refused as already
    fully remote;
  - and back: a new office request after the switch is reviewed and approved, the new
    address becomes the only live main office, and the removed one is still there.
- **`make dev`** should print each seeded organisation's kind and addresses beside the
  seeded accounts.

## Consequences

- The office role, which has never worked end to end, works.
- One role can describe a job offered in several places, so a company stops writing the same
  role three times and splitting its candidates across them.
- A company can offer remote work in a country where it has no office, which revision 1
  would have taken away.
- **An address's country is now display only.** It decides nothing about who a role reaches.
  The country gate applies only to roles that are entirely remote-in-a-country.
- **An onsite role is shown to contributors in every country**, including ones who could
  never work there. Whether they can is settled in the conversation, as it is for any
  approach.
- **A member cannot add an address.** Somebody drafting an onsite role at a new office waits
  for an owner, even where `role_create_authority` lets them open the role themselves.
- **A mistake in the main office cannot be corrected in place, but it is no longer
  permanent.** It cannot be edited (§1.8) and the `DELETE` route will not remove it, so a
  typo stays on every onsite role there until something larger happens. The exit is to go
  fully remote and request an office again (§9, §8): every draft and open onsite role must be
  closed or revised first, every address is removed — the correct ones too, which then have
  to be added again — and the replacement waits for an administrator. It is an exit, not an
  edit, and its cost is meant to be felt. The owner has seen this and kept the no-edit answer.
- **A fully remote company gives no address at all**, and is never asked for one. Revision
  3's "must still give a registered address" is gone.
- **An onsite role is available only to an organisation with an office.** A fully remote one
  that opens an office waits for an administrator before it can post one.
- **The administrator's work grows by one kind of decision** (§8), with its own queue, and
  can recur for one organisation: every time it comes back from fully remote, its next first
  address is reviewed again.
- **An organisation's kind can change in both directions**, and the two are deliberately
  unequal: losing the office is the owner's own act, gaining one is an administrator's
  decision.
- "No address" stops being ambiguous. Every organisation has said which it is.
- A contributor is told a city for the first time.
- Every role refusal becomes actionable, and `logic/roleDraft.ts` stops being the only place
  that knows what was wrong.
- The contract changes under the frontend in six places: the role bodies, the contributor
  role shape, the error bodies, five new organisation routes, the onboarding form, and two
  administrator routes.
- **This document now carries four things**: role locations, field-naming on refusals, the
  onboarding choice, and an organisation's kind changing either way. Each was added on the
  owner's instruction and the last three are sections that stand alone (§5, §7, §8–9), so any
  of them can be approved with the rest or lifted into its own RFC.

## Found on the way, not part of this RFC

- **ADR-0019's endpoint table says `/orgs/{orgID}/roles`; the routes are mounted at
  `/org-roles/{orgID}/roles`**, which is also what ADR-0019's own amendments 2 and 4 and
  ADR-0020 say. The table is stale. It wants an amendment of its own.
- **ADR-0019 carries two sections numbered "3" that contradict each other** — "A closed role
  reopens" and "Closing a role is final" — and its amendments table lists both. The service
  implements the second. **This RFC does not depend on which holds** (§3: the removed-address
  check sits on `open`, and is correct either way), so it is not asked below. It still needs
  settling in ADR-0019.

## Open questions

None. Every question asked in revisions 1, 2 and 4 is answered and recorded below. The draft
is waiting on the owner's approval, not on a decision.

## Resolved review comments

| Asked in revision 1                                                                                                               | Owner's answer                                                                                                                                                                               | What it became                                                                                              |
| --------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Q1. Does onsite restrict by country?                                                                                              | No. Any country.                                                                                                                                                                             | §1.6, §2 "Who a role reaches". Reverses the recommendation.                                                 |
| Q2. Remote within a country with no office there?                                                                                 | Yes, by picking a country. Remote is country only and never an address.                                                                                                                      | §1.3; `role_locations.country`; the model in §2 rebuilt around it. Reverses the recommendation.             |
| Q3. `eligible_countries` removed or derived?                                                                                      | Removed outright.                                                                                                                                                                            | §1.7, Schema.                                                                                               |
| Q4. Who may add an address?                                                                                                       | Owners only.                                                                                                                                                                                 | §1.8, §3. Reverses the recommendation.                                                                      |
| Q5. May one be removed, and by whom?                                                                                              | Yes, owners, as a stamp.                                                                                                                                                                     | §1.8, §3.                                                                                                   |
| Q6. Removing one in use?                                                                                                          | Refused while a draft or open role uses it; allowed for closed; never the main office.                                                                                                       | §3, §5.                                                                                                     |
| Q7. May one be edited?                                                                                                            | No.                                                                                                                                                                                          | §3, with the reason restated — the original one fell with Q1.                                               |
| Q8. What does a contributor see?                                                                                                  | City and country onsite; country or "anywhere" remote; never street or postcode.                                                                                                             | §1.9, §4.                                                                                                   |
| Q9. Does a later address need review?                                                                                             | "Only first address needs human review, after that no need."                                                                                                                                 | §1.10, §3 "Review", §7, §8. A first address is reviewed at onboarding or as an office request; no other is. |
| Q10. Must any database survive?                                                                                                   | No. Rebuild.                                                                                                                                                                                 | §1.11, Schema.                                                                                              |
| Q11. Fix the other `invalid_role` refusals here too?                                                                              | Yes, here.                                                                                                                                                                                   | §1.12, §5 first table, the fixture list. Reverses the recommendation.                                       |
| A (revision 2). Does a first address added after onboarding wait for review?                                                      | First: "address should be compulsary, how can org be created without address?" Then: the form asks whether there is an office, and a fully remote organisation may later gain one, reviewed. | Yes, it waits. §1.13, §1.14, §7, §8. Revision 3's "every organisation has a main office" is superseded.     |
| B (revision 2). Does that first address become the main office?                                                                   | Yes: "on approval that address becomes the main office".                                                                                                                                     | §8.                                                                                                         |
| (revision 4, asked by the coordinator). Can a fully remote organisation later have an office, and is that first address reviewed? | Yes, and yes.                                                                                                                                                                                | §1.14, §8. What a rejection means, which queue, and the reverse direction are questions C, D and E.         |
| C (revision 4). What can an organisation do after a rejected office request?                                                      | As recommended: stays fully remote, the owner is emailed the reason, a new request may follow at once — a new row, no limit, no wait.                                                        | §1.15, §8 "Rejection".                                                                                      |
| D (revision 4). Existing administrator queue, or its own?                                                                         | As recommended: its own, `GET /admin/office-requests` and `POST /admin/office-requests/{requestID}/decide`.                                                                                  | §1.16, §8 "The administrator's side".                                                                       |
| E (revision 4). Can an organisation with an office go fully remote?                                                               | Yes, while no draft or open role has an onsite entry, knowing it makes the main office removable on that path. Only an owner may do it.                                                      | §1.17, §9, §3, Schema, Consequences. Reverses the recommendation.                                           |
| F (revision 4). Is the owner emailed on approval?                                                                                 | As recommended: yes.                                                                                                                                                                         | §1.18, §8 "Notifications".                                                                                  |
| (revision 5, asked at promotion). Which ADRs carry an amendment when this is promoted?                                            | All four it supersedes: ADR-0017 and ADR-0019, and also ADR-0020 and ADR-0021, each with a short amendment pointing at ADR-0024.                                                             | The **Amends** line in the header; applied on promotion, not before.                                        |
