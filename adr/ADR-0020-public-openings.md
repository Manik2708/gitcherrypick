# ADR-0020 — Public openings

**Status:** Accepted · **From:** [RFC-0020](../rfc/RFC-0020-public-openings.md)
· **Schema:** [`RFC-0020-*.schema`](../rfc/RFC-0020-public-openings.schema)
· **Depends on:** ADR-0005, ADR-0007, ADR-0018, ADR-0019

## Decision

1. **An opening is a role a company has published, with a bar on it.** Until now every role
   was private: a contributor learned of a job only when a contact request arrived.
2. **It sits ON a role, in its own table.** An open role is immutable (ADR-0019 §13), so a
   bar living on the role could not be raised without superseding the job underneath.
   One opening per role, enforced by a unique key.
3. **The bar is four things**: `min_overall_score`, `min_generalist_score`, a per-skill
   minimum, and `min_oss_yoe`. Countries are **not** repeated — the role states where it can
   hire, and a second list that could disagree is the bug.
4. **Per-skill bars match PRIMARY standing only**, as search does (ADR-0005). A bar cleared
   by a secondary skill would be cleared by evidence the platform declines to rank.
5. **A stated bar is NOT cleared by an absent score.** Somebody who has never submitted a
   claim has no overall score, and showing them a role asking for 60 would promise a match
   that does not exist. Fail-closed, like verified open-source years (ADR-0019 §7), and the
   opposite of `office_yoe`, which is self-reported.
6. **A contributor sees only what they clear, plus a bare count** — "4 open to you · 11 you
   do not currently meet". An empty list alone lies about why it is empty; the count answers
   that without itemising anybody's shortfall or exposing a company's bar.
7. **The bar is shown on the openings they see.** They cleared it, so it discloses nothing.
8. **THERE IS NO APPLY BUTTON, and reading an opening sends nothing** — no application, no
   interest, no view count. The consent model runs one way (ADR-0005 §9): companies
   approach, contributors answer. A second channel would have none of the first one's
   protections.
9. **Publishing is a separate act from drafting the bar**, under `role_create_authority`
   rather than a fourth setting. Only an **open** role may have a published opening.
10. **An opening has no expiry of its own**, and its age is the **role's** `opened_at`. A
    company that advertised late should not look fresher than one that published at once.

## Implementation

### Endpoints

| Method | Path                                                 | Who                         |
| ------ | ---------------------------------------------------- | --------------------------- |
| PUT    | `/org-roles/{orgID}/roles/{roleID}/opening`          | per `role_create_authority` |
| POST   | `/org-roles/{orgID}/roles/{roleID}/opening/publish`  | per `role_create_authority` |
| POST   | `/org-roles/{orgID}/roles/{roleID}/opening/withdraw` | per `role_close_authority`  |
| GET    | `/org-roles/{orgID}/roles/{roleID}/opening`          | any org hirer               |
| GET    | `/openings`                                          | contributor                 |

`GET /openings` returns the role in the **contact-request shape** (ADR-0019 §2) plus the
organisation's name and the bar — never `status`, the supersede chain, or `hires`, which
names other contributors.

### The contributor's filter

Live opening (published, not withdrawn) on an **open** role, and:

- the role hires where they are, or hires anywhere (ADR-0019 §6);
- every stated bar is met, with an absent score failing a stated one (decision 5);
- every per-skill bar is met at primary standing.

## Steps

1. **Schema.** `RFC-0020-*.schema` is the whole delta. No edits at origin.
2. **Ports.** `OpeningRepository`, and `RoleService` gains the opening calls.
3. **Repository.** Upsert, publish, withdraw, the hirer read, and the contributor match —
   which returns the matched openings and the count of the rest in one pass, because two
   queries could disagree about a row that changed between them.
4. **Service.** Authority, the open-role precondition, and validation of the bar.
5. **Controllers.** Four hirer routes on `RoleController`; `GET /openings` on `MeController`.
6. **Frontend.** The bar form on a role, and a contributor's openings list.
7. **Fixtures.** The assertions that matter: an absent score does not clear a stated bar, a
   secondary skill does not clear a skill bar, and the count reports the misses without
   naming them.

## Consequences

- The platform gets a **public face**. Everything before this was visible only to a verified
  hirer or to the person it described.
- A contributor has a concrete reason to submit a claim: without scores they clear no bar
  that states one.
- `GET /openings` is the first query comparing somebody's own scores against a company's
  threshold, and it reads `user_skills` per opening — the read worth watching as the
  catalogue grows.
- Withdrawing is a stamp, not a delete, so the advert history survives.

## Open questions
