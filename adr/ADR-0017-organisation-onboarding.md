# ADR-0017 — Organisation onboarding

**Status:** Accepted · **From:** [RFC-0017](../rfc/RFC-0017-organisation-onboarding.md)
· **Schema:** [`RFC-0017-*.schema`](../rfc/RFC-0017-organisation-onboarding.schema)
· **Amends:** ADR-0002 §Verification, ADR-0016 §6 · **Source:** `flow-deisgn/hirer-flow.png`

## Decision

1. **Onboarding an organisation is how an owner comes to exist.** It is not something a
   signed-in person does later, and it is the only thing that creates an organisation.
2. **The form creates nothing.** `POST /organizations` writes a submission to
   `organization_onboarding`, reserves no slug, and emails a code. Anyone may call it.
3. **The organisation, its address and the owner seat are created together**, by an
   administrator approving the submission, and not one moment earlier.
4. **Nothing unproven reaches the review queue.** The emailed code must be used before a
   submission becomes visible to an administrator, enforced in the schema by
   `ck_onboarding_decided_after_verified`. This is the mechanism ADR-0016 §6 has been
   waiting for.
5. **Proving the address and choosing how to sign in are one step.** The username, display
   name and password hash are held on the submission, so approval needs no second email.
6. **The company email is the owner's email.** One value, on the organisation and on the
   owner's seat. ADR-0016 made the username the identity precisely so an address could be
   shared.
7. **`POST /auth/hirer/register` stops creating organisations.** It becomes the
   independent hirer's route — someone hiring on their own account — verified individually
   through ADR-0002's proof kinds.
8. **A rejected company revises rather than edits.** `POST /organizations/{id}/revise`
   writes a new row pointing at the old one through `supersedes_id`. The table is
   append-only.
9. **Headcount is a band**, not a number: `1-10`, `11-50`, `51-200`, `201-1000`, `1000+`.
10. **The main office is a row in `organization_addresses`**, not five columns, because a
    role posted later points at an address rather than retyping one.
11. **The hiring process belongs to a role, not to a company.** Whether there is an online
    test, how many rounds, and how long to an offer are deferred to the roles RFC.
12. **No work email domain is collected.** It constrains nothing and ADR-0016's roster is
    not narrowed.

## Implementation

### Endpoints

| Method | Path                               | Who                                  | Purpose                                            |
| ------ | ---------------------------------- | ------------------------------------ | -------------------------------------------------- |
| `POST` | `/organizations`                   | anyone                               | submit the form; a code is emailed; **202, empty** |
| `POST` | `/organizations/verify`            | anyone holding the code              | prove the address, choose how to sign in           |
| `POST` | `/organizations/{id}/revise`       | a rejected company, holding its code | correct and resubmit                               |
| `POST` | `/auth/hirer/verify/resend`        | anyone                               | re-send an unconsumed code                         |
| `GET`  | `/admin/verifications`             | admin                                | now includes proven submissions                    |
| `POST` | `/admin/verifications/{id}/decide` | admin                                | approving creates the organisation                 |

The three public routes sit beside the `GET /organizations` from ADR-0016 §3a. They serve
the same person — one with no account yet — so there is no authenticated read to leak. The
authenticated organisation surface stays at `/orgs`.

`POST /organizations` answers 202 with an empty body whether or not it sent anything. A
company name already submitted must not be distinguishable from one that is free.

`POST /organizations/verify` returns **no token pair**. There is no account to sign in as
until approval, so a session here would be a credential for a seat that does not exist.
This is where it diverges from roster redemption, which does return one.

There is no `PATCH`. A submission awaiting review is not editable: an administrator may be
reading it.

### Ordering

```
POST /organizations   →  submission, code emailed. No org, no seat, no slug taken.
POST /organizations/verify  →  proven; username claimed, owner details held.
                        →  submission enters the admin queue.
POST /admin/verifications/{id}/decide  →  organisation + address + owner, together.
```

### Identity

The username is chosen by the submitter, unlike roster redemption where the organisation
pins it (ADR-0016 §3) — there is no organisation to pin it. It is claimed out of
`hirer_usernames` at **verification**, not at approval, so two pending submissions cannot
race for one name and discover it only when the second is approved.

### Approval can fail

Two submissions may hold the same company name; nothing reserves a slug. The second to be
approved cannot have it, and the administrator is told why. This is the first admin
decision in the platform that can be refused by the data rather than by the reviewer.

### Verification evidence

The structured fields sit alongside ADR-0002's five proof kinds; they do not replace them.
ADR-0002 made `alternative` and `payment_capability` free text so a three-person studio
with no company domain could still be verified, and making an office address the evidence
would undo that.

### The process fields, when they arrive

Deferred to the roles RFC with two properties already decided:

- shown on the **contact request** and nowhere else — not searchable, not on a scorecard,
  not on the public picker;
- visible only to the **contributor who was contacted and the organisation that contacted
  them**.

### An independent hirer does not become an organisation

Accepted, not solved. Someone hiring alone who later incorporates gets a second account and
split history. The fix, if it starts happening, is to let a signed-in independent hirer
submit the form and own what it creates.

## Steps

1. **Schema.** `organization_onboarding` and `organization_addresses` are the RFC-0017
   delta. `organizations` gaining description / email / phone / headcount, the
   `organization_headcount_band` enum, and `email_verifications` gaining
   `organization_onboarding` and `onboarding_id` are edits to **RFC-0002** and **RFC-0016**
   at their origin. No `ALTER`, no backfill — nothing is deployed, and `rfc/*.schema`
   applied in order is the schema. Same precedent as ADR-0016 step 1.
2. **Repositories.** `organization_onboarding` CRUD, the queue read, and a `Promote` that
   creates organisation, address, membership and owner in one transaction.
3. **Notifier.** A template for the onboarding code, and one for a rejection carrying the
   reason and the revise code.
4. **Services.** Submit, verify, revise, and the promotion an approval performs.
   `ResendVerification` stops hardcoding `roster_redemption`.
5. **Controllers.** The three public routes. `POST /auth/hirer/register` loses its
   organisation half.
6. **Jobs.** `expire-onboarding` deletes submissions never proven whose code has expired —
   the first scheduled job that deletes rather than stamping.
7. **Frontend.** Two public buttons: _Onboard your organisation_ and _Hiring on your own?_.
   The onboarding form, the verify screen, and the revise screen.
8. **Fixtures.** `admin/verification_and_overdue_flagging` registers at step 0 and expects
   a queued request at step 1; under decision 7 that is no longer how a company arrives. It
   must be **rewritten by the integration-tester**, not edited to pass.

## Consequences

- A third row type reaches the admin queue. `verification_requests` constrains itself to
  exactly one subject and an onboarding submission is neither, so the queue reads a second
  table and the decide endpoint learns to promote one.
- `organization_onboarding` is append-only. A company rejected twice leaves three rows, so
  every read of "the current submission" follows the chain rather than assuming one row.
- A password hash exists on a pending row before any account does. It is argon2id like
  every other stored hash, but it is the first time credentials exist for a seat that does
  not.
- `email_verifications` gains a third purpose and a second subject column; its CHECK grows
  a third arm so each purpose points at exactly the thing it is about.
- `organization_addresses` holds one row per organisation until roles exist.
- An independent hirer who incorporates ends up with two accounts.

## Amendments

Three, all raised during implementation and all recorded here rather than left as a silent
divergence between this document and the code.

### 1. Onboarding has its own admin routes

The endpoint table above puts submissions into `GET /admin/verifications` and decides them
through `POST /admin/verifications/{id}/decide`. Implemented instead as:

| `GET` | `/admin/onboarding` |
| `POST` | `/admin/onboarding/{submissionID}/decide` |

Three things stopped the merge. A submission has an `OnboardingID` and a verification
request has a `RequestID`, both uuids, so one decide route could not tell which table an
id belonged to without being told. The shapes differ — a submission carries an address, a
headcount and a proposed owner, none of which a verification request has. And the
decisions differ in kind: one stamps a column, the other **creates a company and can
fail**.

Merging the two _reads_ would be defensible, and a client is free to show one list. Merging
the two _decisions_ would make one endpoint mean two things.

### 2. Revision is keyed on the code, not on a path id

The endpoint table says `POST /organizations/{id}/revise`. Implemented as
`POST /organizations/revise` with the code in the body, matching `/organizations/verify`.

The id in the path invited keying the operation on it, and a revision keyed on an id alone
would let anyone rewrite any refused company's answers — every submission id is a uuid
somebody might hold. The code proves the caller is the one who received the rejection,
which is the only thing that should authorise a correction. With the code carrying the
submission, the path id is redundant as well as dangerous.

### 3. An unverified organisation is no longer reachable

Not a change to the decisions, but a consequence of them that this ADR did not state.

Decision 3 makes approval create the organisation, and approval is also the verification —
so every organisation that exists is verified, and nothing creates an unverified one. The
state ADR-0002 built its hiring gate on is gone from the product.

The safety property did not vanish; it moved. "An unverified organisation that cannot
hire" became "a submission that is not a company at all", which is a row in a different
table. That is a stronger position, not a weaker one — an unapproved company cannot be
searched _or_ signed into, rather than existing in a half-state.

What is left behind is `VerifyOrganization` and org-scoped `verification_requests`: intact,
tested, and reachable by no product flow. They are deliberately **not** deleted here.
Removing a verification mechanism is a decision for the Planner, and three repository tests
now construct the unverified state explicitly — through a `mustUnverify` helper that says
why — rather than pretending a flow still produces it.
