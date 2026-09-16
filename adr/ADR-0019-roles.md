# ADR-0019 — Roles

**Status:** Accepted · **From:** [RFC-0019](../rfc/RFC-0019-roles.md)
· **Schema:** [`RFC-0019-*.schema`](../rfc/RFC-0019-roles.schema)
· **Amends:** ADR-0005 §9 (contact requests carry a role), ADR-0018 §7 (open-source years
are verified) · **Source:** `flow-deisgn/hirer-flow.png`

## Decision

1. **A role is a description of an opening, not a pipeline.** It holds what a contributor
   needs in order to decide whether to talk, and stops there. Interviews, offers and
   rejections are somebody else's system.
2. **`SendEmailWithRoleDetails` IS the contact request.** The role, the pay, the location,
   the eligible countries, the process and the organisation are attached to the request and
   sent **by the platform**. The contributor's address is disclosed to nobody; acceptance
   releases it, exactly as `email_released_at` already records. A role is never a channel:
   no endpoint here sends mail and no column holds an address.
3. **One shortlist, one role.** `shortlists.role_id NOT NULL`. A hirer wanting the same
   contributor for a second opening opens a second shortlist, which raises a second,
   separate contact request — two jobs are two decisions.
4. **One `roles` table with `engagement` as the discriminator.** A `CHECK` keeps a freelance
   role free of yearly figures and a salaried one free of hourly rates. `engagement` is an
   enum, unlike a contributor's `OpenTo*` flags, because a person considers several kinds of
   work and an opening is one kind.
5. **A role points at an `organization_addresses` row, or is remote.** No retyped addresses;
   `ON DELETE RESTRICT`, so a posting cannot claim to be somewhere that no longer exists.
6. **No eligible countries means anywhere.** An organisation that never considered work
   authorisation must not exclude the world by leaving a field blank.
7. **Open-source years are VERIFIED, and dated from AUTHORSHIP.** The contributor gives the
   pull request; the platform fetches it and establishes the date. Merge date measures a
   project's responsiveness; authored date measures when somebody started contributing.
   `PRFacts.AuthorUserID` must equal the contributor's GitHub id, checked **before** the URL
   is stored — otherwise write-once makes a stranger's pull request permanent.
8. **The process fields live on the role.** `requires_online_test`, `max_interview_rounds`,
   `avg_days_to_offer`, shown on the contact request and nowhere else, to the contacted
   contributor and the organisation and nobody else (ADR-0017).
9. **Screening questions are yes/no and do not gate.** A free-text question is an interview
   conducted before either side agreed to talk. `expected` reports a match; it never
   filters. Answers are keyed to the **contact request**, so an answer about visa status
   does not follow somebody around.
10. **Compensation still points one way.** ADR-0018 §5 is unchanged: a contributor's
    expectation filters **their** view of roles and is never shown to a hirer.
11. **Authority is three settings, not a rule.** `role_create_authority`,
    `role_update_authority` and `role_close_authority` on `organization_settings`, each
    `any_hirer` / `draft_and_approve` / `owners_only`. Defaults: approve, approve,
    `any_hirer`. Only an owner changes them. Who may act is configurable; **that whoever
    did is named is not** — `opened_by`, `close_requested_by` and `closed_by` are stamped
    under every policy.
12. **A role does not expire.** It is open until somebody closes it. The asymmetry with
    contributor availability, which does lapse, is accepted.
13. **An open role is IMMUTABLE.** A change writes a new role with `supersedes_id` and
    closes the old one in the same transaction, so a contributor contacted last week still
    reads the salary they agreed to talk about. Drafts are mutable: the line is disclosure,
    not age.
14. **Closing asks why.** `not_needed`, `hired_elsewhere`, `hired_via_platform`, or `other`
    with a note. `superseded` is written by the system and offered to nobody.
15. **`hired_via_platform` names people, and a role may fill several seats.** `role_hires`
    takes one row per person. Every email given must be one an accepted contact request
    already released to **this** organisation — a company can only report hiring somebody
    who agreed to talk to it, and the restriction stops the field being an oracle for
    testing whether an address has an account here. An unrecognised address is a `422`
    naming which one failed.
16. **A contributor recorded as hired is told.** It is not on their profile, not searchable,
    and not visible to another organisation. Their availability is **not** touched — that is
    not the platform's call to make on the word of the other party to the deal.

## Implementation

### Endpoints

| Method | Path                              | Who                                                  |
| ------ | --------------------------------- | ---------------------------------------------------- |
| POST   | `/orgs/{orgID}/roles`             | per `role_create_authority` — creates a draft        |
| GET    | `/orgs/{orgID}/roles`             | any org hirer                                        |
| GET    | `/orgs/{orgID}/roles/{id}`        | any org hirer                                        |
| PATCH  | `/orgs/{orgID}/roles/{id}`        | per `role_update_authority` — **drafts only**        |
| POST   | `/orgs/{orgID}/roles/{id}/revise` | per `role_update_authority` — drafts a successor     |
| POST   | `/orgs/{orgID}/roles/{id}/open`   | per `role_create_authority`                          |
| POST   | `/orgs/{orgID}/roles/{id}/close`  | per `role_close_authority` — body carries the reason |
| GET    | `/orgs/{orgID}/settings`          | any org hirer                                        |
| PUT    | `/orgs/{orgID}/settings`          | **owner only**, always                               |
| GET    | `/me/roles`                       | contributor                                          |

`/me/roles` is open roles filtered by the contributor's own profile: eligible countries
include theirs, minimums met, engagement matches a shape they ticked, and compensation not
below what they asked for — the filter ADR-0018 §5 promised, applied where only they see it.

### When the pull-request fetch fails

A save we can **answer** — the PR does not exist, is not merged, or was written by somebody
else — is `422` and **nothing is written**, so write-once never records an unverified URL.
A save we **cannot** answer — rate limit, outage — stores the URL with `first_pr_verified_at`
`NULL` and a job retries. Until it verifies, open-source years are unknown, and unknown does
not match a role that sets a minimum. That is a deliberate fail-**closed**, against the
fail-open of ADR-0018 §7: failing open would mean a minimum anybody clears with an
unverifiable link. The contributor is told on their own profile that verification is pending.

### Cross-table invariants

`hired_via_platform` has at least one `role_hires` row and no other reason has any. It spans
two tables, so it is the service's and asserted there, written in the same transaction as
the close.

## Steps

1. **Schema.** `RFC-0019-*.schema` is the delta. Two edits **at origin**: `shortlists.role_id
NOT NULL` in RFC-0005, and `first_pr_authored_at` / `latest_pr_authored_at` /
   `first_pr_verified_at` on `user_work_preferences` in RFC-0018. No `ALTER`, no backfill —
   nothing is deployed and `rfc/*.schema` applied in order is the schema. Same precedent as
   ADR-0016 step 1 and ADR-0017 step 1.
2. **Adapter.** `CreatedAt` on `domain.PRFacts`, the GitHub adapter, and the pull-request
   route in `cmd/fakethirdparty`.
3. **Ports.** `RoleRepository`, `RoleService`, `OrgSettingsRepository`, and the profile port
   gaining verification. Mocks regenerated with `make mocks`.
4. **Repositories.** Role CRUD, the revise-and-supersede transaction, the close-with-hires
   transaction, settings upsert, and the `/me/roles` match query.
5. **Services.** The authority resolution, the immutability rule, the close invariants, the
   released-email check, and open-source-year verification on profile save.
6. **Controllers.** The role routes under `/orgs/{orgID}`, `/orgs/{orgID}/settings`, and
   `/me/roles`.
7. **Notifier.** Role details on the contact-request template, and the hire notification.
8. **Frontend.** Role creation and the closing dialogue for hirers; `/me/roles` for
   contributors.
9. **Fixtures.** The assertions that matter most: a hirer never sees expected compensation
   (ADR-0018 §5 still holds with roles present), an open role cannot be mutated, and a hire
   cannot be recorded against an address the organisation was never given.

## Consequences

- A shortlist stops being a bare list and becomes a round of hiring for a stated job.
- The platform gets its **first outcome data**. `role_hires` joined to the ranking answers
  "does this produce hires"; there was no way to ask before.
- Saving a contributor profile stops being a pure write and makes a network call.
- Role rows accumulate — one per revision. The partial index on `status = 'open'` means the
  superseded ones cost nothing to read past.
- A hire recorded against a contributor is an assertion by one party about another. It is
  notified and private, and has no correction flow beyond a human.
- An independent hirer still cannot create a role: `roles.organization_id` is `NOT NULL`.
  This makes the open question about independent hirers more visible, not less.

## Open questions
