# RFC-0017 — Organisation onboarding

**Status:** Approved 2026-09-13 · **Binding form:** [ADR-0017](../adr/ADR-0017-organisation-onboarding.md)
· **Schema:** [`RFC-0017-*.schema`](RFC-0017-organisation-onboarding.schema)
· **Amends:** ADR-0002 §Verification, ADR-0016 §6 · **Source:** `flow-deisgn/hirer-flow.png`

## Summary

Onboarding an organisation is how an **owner comes to exist**. It is not something a
signed-in person does later.

Anyone may submit the form: the company's name, description, contact email, phone, main
office address and headcount. It creates nothing — a
**submission**, and a code emailed to the address given. Whoever holds that address proves
it and picks a username and password. Only then does the submission reach an
administrator, and only when they approve it do the organisation, its address and the
owner seat come into existence together.

Sign-in is left doing one thing: signing in accounts that already exist.

## 1. Four ways an account exists, one way to sign in

| kind              | created by                                | verified                         |
| ----------------- | ----------------------------------------- | -------------------------------- |
| contributor       | GitHub OAuth                              | n/a                              |
| owner             | **this RFC** — onboarding an organisation | the organisation is reviewed     |
| org-hirer         | redeeming a roster entry (ADR-0016 §3)    | inherits the organisation's      |
| independent-hirer | `POST /auth/hirer/register`               | reviewed individually (ADR-0002) |

`POST /auth/hirer/register` stops creating organisations. It becomes the independent
hirer's route and nothing else — someone hiring on their own account rather than a
company's, verified through ADR-0002's proof kinds exactly as a small studio is today.

This answers an open question the previous draft carried: registration no longer writes
organisations, because **onboarding is the only thing that does**.

## 2. Nothing exists until an administrator approves it

The form is public, so it must not be able to create anything. It writes a
**submission**, and an organisation is made from that submission only once a person has
approved it:

```
POST /organizations              a submission. No organisation, no seat, no slug taken.
     │                           a code is emailed to the company address
     ▼
   inbox                         one code, 24 hours, single use
     │
     ▼
POST /organizations/verify       { code, username, display_name, password }
     │                           address proven, username claimed, owner details held
     ▼
   admin queue                   the submission becomes visible to an administrator
     │
     ▼
POST /admin/verifications/{id}/decide
     │
     ▼
organisation + address + owner   created together, and the owner can sign in
```

Three properties follow, and each one is the reason for a step.

**No company name can be squatted.** Anyone can submit the form, so if it wrote an
organisation directly, anyone could take `acme` — or a hundred names in a minute — and
`uq_organizations_slug` would then refuse the real company when it arrived. A submission
reserves no slug. Two pending submissions for the same name may coexist; whichever an
administrator approves first gets it, and the second fails with a reason the administrator
can read.

**An administrator never reviews a company nobody can reach.** The queue is proven
submissions only, enforced in the schema rather than in a query
(`ck_onboarding_decided_after_verified`). This is what ADR-0016 §6 asks for — a proven
email before the review queue — and `port.VerifyHirerRegistration` has been sitting in the
code unreferenced since that decision was written, waiting for exactly this.

**The owner exists the moment the organisation does.** Proving the address and choosing how
to sign in are one step, so approval has everything it needs to create the seat without a
second email and a second wait.

A submission that is never proven dies on its own. The code expires after 24 hours
(ADR-0016 §4), which makes the row permanently unusable rather than merely stale — it can
never be proven, never reach an administrator, never become a company. A fifth scheduled
job, `expire-onboarding`, deletes those: no `email_verified_at`, no live code, nobody
waiting. It sits beside `expire-availability` and `expire-contact-requests` in `cmd/jobs`,
which already do this shape of work.

Deleting rather than archiving is a deliberate trade. It loses the trail — somebody
submitting "Microsoft" ten times and never verifying leaves nothing behind — but abuse of
an endpoint that creates nothing belongs to rate limiting, not to a table nobody prunes.

The username is chosen here, unlike roster redemption where the organisation pins it
(ADR-0016 §3). There is no organisation to pin it — this flow is what creates one — so the
first person names themselves and every seat after them is named by the owner. It is
claimed out of `hirer_usernames` at verification rather than at approval, so two pending
submissions cannot race for one name and discover it only when the second is approved.

## 3. What is collected

Name, description, company email, phone, main office address, headcount.

**The company email is the owner's email**, because this form creates the owner. It is the
address the proof goes to and the address the owner's seat carries. ADR-0016 made the
username the identity precisely so an address could be shared between seats, which is what
makes `hiring@acme.com` an ordinary owner address rather than a collision.

**The address is a row, not five columns.** An organisation has a main office; a role
posted later is either remote or at an address, and the source diagram's `AddressExist?`
branch reuses one rather than retyping it. `organization_addresses` is scoped to one
organisation — two companies in the same building are two rows, and deduplicating them
would let one company's correction rewrite another's.

**Headcount is a band, not a number** — `1-10`, `11-50`, `51-200`, `201-1000`, `1000+`.
The source field is "CurrentApproxEmployees" and approximate is what it means: nobody knows
whether 47 means 47, and a band is both easier to answer honestly and harder to answer
misleadingly. The labels carry their own boundaries rather than reading `small` and
`medium`, which would hide the one thing the value is for — an administrator judging
whether the evidence a company offers is proportionate to its size.

`postal_code` is optional: several countries have none, and a NOT NULL would lock those
organisations out entirely. `country` is ISO 3166-1 alpha-2 rather than a name, because
roles will filter on eligible countries and `"UK"`, `"U.K."` and `"Britain"` cannot all be
the same filter.

## 4. What it does NOT collect

**How the hiring process works belongs to a ROLE, not to a company.** Whether there is an
online test, how many interview rounds there are, and how long it takes to reach an offer
differ between a staff engineering role and a three-month contract at the same company.
Putting them on the organisation would state one answer where there are several, and every
role would inherit a figure that is wrong for most of them.

They are therefore deferred to the roles RFC, with two properties already decided and
recorded here so they are not lost in the handover:

- They are shown on the **contact request** and nowhere else. That is the moment a
  contributor decides about a company, beside the payment-verified fact ADR-0002 §5 already
  puts there. They are not searchable, not on a scorecard, and not on the public picker.
- They are visible only to the **contributor who was contacted and the organisation that
  contacted them**. Not to other contributors, not to other organisations, not to anyone
  browsing.

**The work email domain is deliberately absent.** It appears in the source diagram and is
dropped on the owner's instruction. Nothing consumed it, and both things it might have done
are worse than not having it: constraining the roster to a domain would stop an owner
seating a contractor on their own address, and auto-trusting a domain would remove the
roster step and with it the owner's control over who joins. ADR-0016 is implemented and
passing; this RFC does not narrow it.

## 5. Verification, unchanged

The structured fields sit **alongside** ADR-0002's five proof kinds rather than replacing
them. ADR-0002 made `alternative` and `payment_capability` free text so that a three-person
studio with no company domain could still be verified; making an office address and a phone
number the evidence would undo exactly that.

The address stays optional for its own reasons, and not the one an earlier draft gave. That
draft said a freelancer working from home has no office — which is true and irrelevant,
because **a freelancer never reaches this form**: they are an independent hirer and
register through `/auth/hirer/register` (§1). Everyone here is a company.

The real reasons are that a fully remote company has no office to name, and that a
registered address is frequently somebody else's — an accountant's, or a formation agent's
— so requiring one collects a fact that is often not about the company at all.

An administrator reads both: the facts a company asserts, and the evidence it offers for
them.

## 6. Endpoints

| Method | Path                               | Who                     | Purpose                                  |
| ------ | ---------------------------------- | ----------------------- | ---------------------------------------- |
| `POST` | `/organizations`                   | anyone                  | submit the form; a code is emailed       |
| `POST` | `/organizations/verify`            | anyone holding the code | prove the address, choose how to sign in |
| `POST` | `/auth/hirer/verify/resend`        | anyone                  | re-send an unconsumed code (exists)      |
| `GET`  | `/admin/verifications`             | admin                   | now includes proven submissions          |
| `POST` | `/admin/verifications/{id}/decide` | admin                   | approving creates the organisation       |

Both public routes sit beside the `GET /organizations` that ADR-0016 §3a already added.
They serve the same person — one with no account yet — so there is no authenticated read
to leak onto a public route. The authenticated organisation surface stays at `/orgs`,
where the roster and seats live.

`POST /organizations` answers **202 with no body**, whether or not it sent anything. A
company name already submitted must not be distinguishable from one that is free, or the
endpoint reports who is mid-onboarding before the public picker would.

`POST /organizations/verify` returns **no session**. There is no account yet to sign in
as — approval is what creates one — so a token here would be a credential for a seat that
does not exist. This is the one place it diverges from roster redemption, which does
return a pair, because there the organisation is already verified and the seat is real
immediately.

`POST /organizations/{id}/revise` is how a **rejected** company corrects itself. The
rejection email carries a code, the form opens prefilled with the previous answers, and
submitting writes a **new row** pointing at the old one through `supersedes_id`.

Editing in the UI, append-only in the database. A company that mistyped a postcode should
not have to retype fifteen fields — but an administrator reading the second attempt has to
be able to tell a corrected postcode from a company rewriting the facts that got it
refused, and only keeping the original tells them which this is.

Re-proving the address is required when the email changed and skipped when it did not. A
new address is a new claim; the same one was proven by receiving the rejection.

There is no `PATCH`. A submission awaiting review is not editable at all: an administrator
may be reading it.

## 7. An independent hirer does not become an organisation

Someone hiring alone is verified individually (ADR-0002) and builds shortlists and contact
requests under that account. If they later incorporate, onboarding creates a **second
account** — a different username, owning the new organisation — and their history stays on
the first.

This is documented rather than solved. Onboarding's whole shape is "the form creates the
owner", and an authenticated variant that adopts an existing account is a real fork in that
logic for a case nobody has hit yet.

It is worth stating plainly because the platform is otherwise careful about continuity of
identity: ADR-0016 §9 keeps a departed hirer's name on their work precisely so an old round
stays legible. This is the same concern one level up, and the answer here is different —
accepted, for now, rather than argued away. If it starts happening, the fix is to let a
signed-in independent hirer submit the form and become the owner of what it creates.

## 8. The buttons

Two, both public, because neither requires an account:

- **Onboard your organisation** → the form above.
- **Hiring on your own?** → `POST /auth/hirer/register`, the independent hirer.

Someone whose company is already here uses neither: they were rostered, and `/redeem`
already serves them (ADR-0016 §3).

## Consequences

- **A third row type reaches the admin queue.** `verification_requests` covers a hirer or
  an organisation and constrains itself to exactly one of them
  (`ck_verification_single_subject`); an onboarding submission is neither. The queue reads
  `organization_onboarding` as well, and the decide endpoint learns to create an
  organisation from one.
- `POST /auth/hirer/register` changes shape and meaning: it becomes the independent
  hirer's route and stops writing organisations. The approved stage-3 fixture
  `admin/verification_and_overdue_flagging` registers at step 0 and expects a queued
  request at step 1 — under §2 that is no longer how a company arrives. The fixture must
  be rewritten by the integration-tester, not edited to pass.
- **Approval can fail**, which nothing in the admin flow currently does. Two submissions
  may hold the same company name, and the second to be approved cannot have the slug. The
  administrator is told why rather than the request silently failing.
- `POST /auth/hirer/verify/resend` must stop hardcoding `roster_redemption` and resend
  whichever proof is outstanding for the address.
- A password hash is held on a pending row before any account exists. It is argon2id, the
  same as every other stored hash, but it is the first time credentials exist for a seat
  that does not.
- `organization_onboarding` is append-only, so a company that is rejected twice leaves
  three rows. That is the point, but it means every read of "the current submission" has to
  follow the chain rather than assume one row per company.
- `email_verifications` gains a third purpose and a second subject column. Its CHECK grows
  a third arm, which keeps each purpose pointing at exactly the thing it is about.
- A fifth scheduled job joins `cmd/jobs`, and it is the first one that DELETES rather than
  stamping a column.
- An independent hirer who incorporates ends up with two accounts (§7).
- `organization_addresses` is declared before any role points at one. That is reuse the
  source diagram asks for, not speculation, but until roles exist it holds one row per
  organisation.

## Open questions

None. All six raised during review were settled above — the last of them, an independent
hirer incorporating, is documented in §7 as accepted rather than solved.
