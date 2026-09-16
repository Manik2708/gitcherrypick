# RFC-0016 — Usernames, organisation rosters, and email verification

**Status:** Approved 2026-09-13 · **Binding form:** [ADR-0016](../adr/ADR-0016-usernames-rosters-and-email-verification.md)
· **Schema:** [RFC-0016-org-roster-and-email-verification.schema](RFC-0016-org-roster-and-email-verification.schema)
· **Amends:** ADR-0002 §1, §Verification · ADR-0009 (unchanged, but read it)
· **Source:** `flow-deisgn/auth-flow.png`

## Summary

Three changes to how a hiring account comes into existence.

**A hirer signs in with a username, not an email address.** Today the email is doing two
incompatible jobs — it is the identity key (`uq_hirer_email`, `ByEmail`) and it is a
contact address (`contact.go:115`). Separating them is what makes the rest of this RFC
simple rather than fiddly.

**An organisation keeps a roster of hirer email addresses.** A hirer signs themselves
up by proving they hold a rostered address. This **replaces** the invitation-token flow
in which an organisation minted a single-use link and sent it to the invitee.

**A self-serve hirer must prove they control their email address** before an
administrator reviews them. Today they need not: `POST /auth/hirer/register` takes an
address on trust, and nothing in the platform ever checks it.

Contributors are untouched. They sign in through GitHub and their address already arrives
verified, which is the guarantee the contact-request flow depends on.

## 0. Why a username

Email-as-key forces three awkward workarounds, all of which disappear if the two jobs are
split:

| Problem with email-as-key                                                 | With a username                         |
| ------------------------------------------------------------------------- | --------------------------------------- |
| A departing person's address cannot be reused until their seat is deleted | irrelevant — the address is not the key |
| Editing an email means editing an identity                                | it is only a contact field              |
| The login identifier is a real, guessable address                         | the identifier is not an address        |

The earlier draft of this RFC solved the first row with a partial unique index on
`hirer_accounts.email`. That is no longer needed: with a username, a recycled work address
is simply a new seat for a new person who happens to share a mailbox.

**The username is set by the organisation when it rosters the address**, alongside the
role. The organisation is already deciding who holds a seat and at what level; naming the
seat belongs in the same decision, and it stops a redeemer squatting a name their
colleagues expected.

**It is globally unique.** A collision returns `409` saying only that the name is taken —
never who holds it, nor where. That follows ADR-0009's pattern exactly: attempt the
insert, translate the violation, never check first, and never turn the endpoint into an
oracle. An owner may need two attempts for a common first name; that is the price of not
disclosing another organisation's roster.

**A username is never reused.** Not after revocation, not ever. Anything attributed to
`maya` is one person forever — which is the entire reason for separating identity from a
mailbox that changes hands.

## 1. Why the invitation token is going

The current flow works and is tested, so this is a deliberate exchange rather than a
repair.

A token link puts the organisation in the delivery business. It must reach the invitee,
survive a forwarded mailbox, and be re-issued when it expires — and `organization_invitations`
carries `token_hash`, `accepted_at` and `expires_at` precisely to manage that lifecycle.
The organisation's actual intent is much smaller: _this person may hold a seat here._

A roster states that intent directly. The organisation adds an address; whoever proves
they hold it may create the seat. No link to deliver, nothing to expire, and the
organisation can revoke the intent before anyone acts on it.

The cost is that a roster is an **allowlist, not a secret**. An address on it is a
guessable fact, so the roster alone must never be sufficient to create an account. That
is §3.

## 2. The roster

`organization_hirer_roster`, one row per invited address per organisation.

An entry is added by an existing hirer of that organisation (today's `invite` permission)
and carries the role the seat will get. Removing an entry is §5.

The organisation must be verified before its roster can be used, because a roster entry
is how an unknown person becomes a hirer inside it. An unverified organisation may build
a roster; nobody may redeem one.

## 3. Redeeming a roster entry

The flow's `Hirer → associated with any org? → yes` branch:

```
selects organisation (from the public list, §3a), enters email
        │
        ▼
  email on that org's roster, unredeemed?
        │
    no ─┴─ 401           ← same answer as "no such organisation"
        │
       yes
        ▼
  prove the address  (§4)
        │
        ▼
  set a password → seat created with the username and role
                   the organisation pinned on the entry
```

**The diagram routes straight from "email exists in org" to "password enter".** This RFC
inserts the proof step between them, because the roster is guessable: without it, anyone
who guesses `someone@acme.com` is on Acme's roster gets a seat in Acme.

The owner chose "roster + still verify the email" for exactly this reason.

**A roster miss answers 401, and so does an unknown organisation.** The two are
deliberately indistinguishable: distinguishing them turns the endpoint into an oracle for
who an organisation is hiring.

A redeemed seat is verified **by inheritance** — it belongs to an organisation an
administrator already approved, so it does not queue for review again. That is the
existing rule in ADR-0002: capability belongs to the organisation, not the person.

The redeemer chooses **only their password**. The username and the role were fixed by the
organisation when it created the entry, so nothing about the seat's identity or privilege
is self-selected.

## 3a. Listing organisations

`GET /organizations` returns verified organisations so the redeemer can pick theirs. It is
unauthenticated, because the person using it has no account yet.

**This is a deliberate disclosure, and it is the only one in this RFC.** Since only
verified organisations appear, the list states who has been approved to hire here —
effectively a customer list, and by implication a signal of who is actively recruiting.
That is a real cost, accepted in exchange for a redeemer being able to find their employer
without the organisation having to send them anything.

It returns name and slug only. Not roster size, not seat count, not verification dates,
not payment status — nothing about how much an organisation is hiring or whether it can
pay.

## 4. Email verification

`email_verifications`, holding a hashed single-use token with an expiry, in the shape
`sessions` already uses for refresh tokens: **plaintext to the recipient, hash to the
database**, so a database read yields nothing usable.

It serves both branches of the flow:

| Path                    | When                                      |
| ----------------------- | ----------------------------------------- |
| Roster redemption (§3)  | before the seat is created                |
| Self-serve registration | before the verification request is queued |

The self-serve branch is the flow's `verified email? → no → verify email → manual
verification`. An administrator should not spend attention on an account whose address
may not exist; requiring proof first is also a cheap filter on the review queue.

**Google sign-in skips it.** Google asserts `email_verified` in the ID token, and
re-proving an address a trusted issuer has already proven is friction with no gain. The
flow's `verified email? → yes` branch is this case.

A new notification kind, `email_verification`, is required: the enum currently has nine
and none of them proves an address.

## 5. Removing a roster entry

The diagram reads `removes hirer email → delete user`. This RFC implements it as
**revoke, not delete**.

A hirer who has been active is attached to shortlists, contact requests, and the record
of which candidates were told an organisation was interested. ADR-0008 makes that record
permanent: a notified candidate can never be un-notified. Deleting the row would orphan
or erase the audit trail a contributor relies on to know who approached them.

So removal does three things:

1. deletes the roster entry, so the address cannot be redeemed again
2. revokes every session family belonging to that hirer — access ends within 15 minutes,
   bounded by the access-token TTL, and immediately for anything that re-checks capability
3. stamps `hirer_accounts.disabled_at`, so the seat is closed to sign-in

The rows stay. The history stays. The username stays spent. The person cannot get back in.

**`disabled_at` is a new column**, and every read path that resolves a hirer must honour
it, or a disabled seat keeps working.

**The password is not rotated, and no credential is handed to anyone.** A disabled seat
cannot sign in whatever its password is, so rotation would add nothing that `disabled_at`
does not already guarantee. Giving an owner a working credential for a seat they do not
hold would be worse than useless: every action taken with it would be recorded as the
departed person, destroying the very audit trail that revoke-over-delete exists to
protect. If the concern is that revocation might be incompletely enforced, the answer is
to enforce `disabled_at` on every read path — not to issue a spare key.

**Nothing is ever hard-deleted.** The diagram's `Delete User` box is deliberately not
implemented; §5 is what happens instead, and §5a is what the organisation keeps as a
result. A seat that was never redeemed has no history to
protect, but it also has no row — an unredeemed roster entry is just a deleted entry.

## 5a. Reading a departed hirer's work

Revocation must not cost the organisation the work. This section is why the answer to
"remove a hirer" is revoke rather than delete, stated from the owner's side.

**The rows already survive.** Shortlists, saved searches and contact requests are keyed by
`organization_id`, not by the hirer who made them — `ShortlistRepository.List` filters
`WHERE s.organization_id = $1`. A revoked seat takes nothing with it, and colleagues keep
seeing every round.

**What does not survive is legibility.** `created_by` serialises as a bare `HirerID`, so a
round made by someone who left reads as a UUID. The history is present and unreadable,
which for an owner is nearly the same as absent.

Three changes fix that, and they are small because the data is already there:

**1. Author references carry a person, not an identifier.** Wherever a response today
emits `created_by`, `added_by` or `requested_by` as a raw id, it emits:

```json
{ "id": "…", "username": "maya", "display_name": "Maya Renner", "active": false }
```

`active: false` marks a revoked seat. This is what the never-reused username in §0 buys:
`maya` on a two-year-old round is unambiguously one person, forever.

**2. `GET /orgs/{orgID}/seats`** lists the organisation's seats — live and revoked, with
revoked ones flagged and dated. Without it an owner cannot resolve an author at all; with
it, old history stays readable indefinitely. Hiding revoked seats by default would defeat
the purpose, since the seats an owner most needs to look up are precisely the departed
ones.

**3. Any hirer in the organisation may read all of it.** Not owners only. Shortlists are
already org-scoped, so every seat already sees every round — naming the author exposes no
row that was not already visible, only who created it. Restricting attribution would add
an authorisation rule that protects nothing.

**This is what makes the `Delete User` box wrong.** Deleting the row would orphan
`created_by` across every round the person touched, and no attribution could recover it.
Revocation keeps the work legible and still ends access completely.

## 6. What this does NOT change

- **Contributors.** GitHub OAuth only, address verified by GitHub, no roster.
- **Admins.** Seeded, never self-registerable.
- **ADR-0009's uniqueness rule.** A `github_user_id` still identifies at most one
  contributor and at most one hirer; rosters are keyed by email, not by GitHub identity.
- **Verification of the organisation itself.** Still an administrator reading proofs.
- **The contact-request disclosure.** Payment verification remains separate.

## Consequences

**An organisation can pre-authorise people who never join.** A roster entry with no seat
is inert and costs nothing, but the list will drift out of date. No expiry is proposed:
an entry is a standing intent, and expiring it silently would surprise an organisation
that does not re-check.

**Email delivery becomes load-bearing.** Today `port.Notifier` failing is a degraded
experience — a lapse reminder is missed. After this, a notifier outage blocks new hirer
signups entirely. That is a real availability coupling and should be stated plainly
rather than discovered.

**Two more unverified-address paths close.** The registration path today accepts any
address and notifies it on `contact_accepted`, meaning an unverified inbox learns that a
named contributor consented to contact. §4 closes that.

**A migration must dispose of `organization_invitations`.** Outstanding unaccepted
invitations become roster entries; accepted ones are already seats and need nothing. The
table is dropped only after that conversion.

**This is a stage-4 change arriving during stage 6.** It touches controllers, services,
repositories and the schema. The e2e fixtures covering invitations will fail and must be
rewritten by the integration-tester — not edited to pass by whoever implements this.

## 7. Endpoints

Stated here because ADR-0002 states its own, and an RFC that leaves an implementer to
invent five routes has not finished the job.

| Method   | Path                             | Who                  | Purpose                                       |
| -------- | -------------------------------- | -------------------- | --------------------------------------------- |
| `GET`    | `/organizations`                 | anyone               | verified orgs, name and slug only (§3a)       |
| `GET`    | `/orgs/{orgID}/roster`           | owner of that org    | list entries, redeemed or not                 |
| `GET`    | `/orgs/{orgID}/seats`            | any hirer in the org | live and revoked seats (§5a)                  |
| `POST`   | `/orgs/{orgID}/roster`           | **owner only**       | add `{email, username, role}`                 |
| `DELETE` | `/orgs/{orgID}/roster/{entryID}` | **owner only**       | remove; revokes the seat (§5)                 |
| `POST`   | `/auth/hirer/redeem/start`       | anyone               | `{org_slug, email}` → sends the proof, or 401 |
| `POST`   | `/auth/hirer/redeem/complete`    | anyone               | `{token, password}` → creates the seat        |
| `POST`   | `/auth/hirer/verify/resend`      | anyone               | re-sends an unconsumed proof                  |
| `POST`   | `/auth/hirer/login`              | anyone               | now takes `{username, password}`              |

**Only an owner may add or remove.** A roster entry grants a seat, and an owner entry
grants the power to grant more — so a member who could roster could promote themselves
through it, and the role pinning in §8 would stop being a boundary. This makes the first
seat of an organisation necessarily an owner, created by the registration path rather
than by redemption.

`POST /orgs/{orgID}/invitations` and `POST /orgs/invitations/{token}/accept` are removed.

## 8. Settled questions

Decided with the owner on 2026-09-13. Recorded here rather than left open, because each
one changes what gets built.

**The roster entry pins the username and the role.** The organisation chooses both when
it adds the address; the redeemer chooses only a password. An owner seat can roster
further people, so a redeemer who selected their own role could escalate into one.

**Usernames are globally unique, and never reused.** A collision answers `409` naming
nothing. A revoked username stays spent forever, so anything attributed to it is
unambiguously one person.

**A departed hirer's work stays readable, to everyone in the organisation.** Author
references name a person rather than an id, revoked seats remain listable, and any hirer
in the org may read both. Revocation ends access without costing the organisation its
history — which is the requirement that makes revoke-over-delete correct rather than
merely cautious.

**Email is no longer an identity.** It is a contact address and a roster key. One address
may appear on two rosters at different organisations — the recycled-work-address case —
because it no longer determines who anyone is.

**The roster does NOT require the address to match a verified domain.** Only one of the
five verification proofs is `work_email_domain`; ADR-0002 admits a studio verified by a
business registration or a client reference precisely so a small firm with no company
domain can hire. Requiring a domain match would lock out the organisations that rule
exists to include. The organisation vouches for whoever it rosters, and that is the whole
content of an entry.

**Un-verification is out of scope, because it does not exist.** Nothing clears
`organizations.verified_at`; the admin decide endpoint only moves a request from `pending`
to approved or rejected. The question was hypothetical. When un-verification is built, the
answer should be that capability stops while sessions live — mirroring how an unapproved
account already behaves, and avoiding the reuse of session revocation, which currently
means "theft detected", to express an administrative decision.

**Verification tokens live 24 hours.** Long enough to survive a mail delay or an overnight
gap; short enough that a forwarded or archived message stops working the next day. This is
only defensible because resend exists — an expiry with no resend is a dead end, so resend
is part of this RFC, not a follow-up.

## Open questions

None. All eight raised during review were settled above.
