# ADR-0016 — Usernames, organisation rosters, and email verification

**Status:** Accepted · **From:** [RFC-0016](../rfc/RFC-0016-org-roster-and-email-verification.md)
· **Schema:** [`RFC-0016-*.schema`](../rfc/RFC-0016-org-roster-and-email-verification.schema)
· **Amends:** ADR-0002 §1 and §Verification · **Source:** `flow-deisgn/auth-flow.png`

## Decision

1. **A hirer signs in with a username, not an email address.** Email stops being an
   identity and becomes a contact field. The username is globally unique and is **never
   reused**, including after revocation.
2. **The organisation pins the username and the role** when it adds an address to its
   roster. The redeemer chooses only a password.
3. **An organisation keeps a roster of hirer email addresses**, replacing the
   invitation-token flow. `POST /orgs/{orgID}/invitations` and
   `POST /orgs/invitations/{token}/accept` are removed.
4. **Only an owner may add to or remove from a roster.** An owner entry grants the power
   to grant further seats, so a member who could roster could promote themselves.
5. **A roster entry is an allowlist, not a credential.** Redeeming one requires proving
   control of the address. A roster miss and an unknown organisation both answer `401`.
6. **Self-serve registration requires a proven email** before it reaches the review
   queue. Google sign-in is exempt — the issuer already asserts `email_verified`.
7. **Verification tokens are hashed, single-use, and live 24 hours**, with resend.
8. **Removing a roster entry revokes; it never deletes.** The seat is stamped
   `disabled_at`, its session families are revoked, and every row it authored stays.
9. **A departed hirer's work stays readable.** Author references name a person rather
   than an id, revoked seats stay listable, and any hirer in the organisation may read
   both.
10. **`GET /organizations` is public**, returning verified organisations by name and slug
    only. This is an accepted disclosure, not an oversight.

## Implementation

### Endpoints

| Method   | Path                             | Who                  | Purpose                                  |
| -------- | -------------------------------- | -------------------- | ---------------------------------------- |
| `GET`    | `/organizations`                 | anyone               | verified orgs, name and slug only        |
| `GET`    | `/orgs/{orgID}/roster`           | owner                | entries, redeemed or not                 |
| `POST`   | `/orgs/{orgID}/roster`           | owner                | add `{email, username, role}`            |
| `DELETE` | `/orgs/{orgID}/roster/{entryID}` | owner                | remove, and revoke the seat              |
| `GET`    | `/orgs/{orgID}/seats`            | any hirer in the org | live and revoked seats                   |
| `POST`   | `/auth/hirer/redeem/start`       | anyone               | `{org_slug, email}` → proof sent, or 401 |
| `POST`   | `/auth/hirer/redeem/complete`    | anyone               | `{token, password}` → seat created       |
| `POST`   | `/auth/hirer/verify/resend`      | anyone               | re-send an unconsumed proof              |
| `POST`   | `/auth/hirer/login`              | anyone               | now `{username, password}`               |

### Identity

`hirer_accounts.username` is `NOT NULL` and carries an unconditional unique index.
Unconditional, deliberately: a revoked seat keeps its name forever, which is what makes
`maya` unambiguous on a round from two years ago.

`hirer_accounts` carries no unique constraint on `email` at all. Two people in one
organisation may share an alias such as `hiring@acme.com`, and the address no longer
identifies anyone.

A username collision follows ADR-0009's pattern — attempt the insert, translate the
violation to `port.ErrConflict`, never check first. The `409` names nothing about who
holds the name or where.

### Disabling a seat

`hirer_accounts.disabled_at` follows the convention `admin_accounts` already sets:

- `ByUsername` (sign-in) **filters disabled rows out** — sign-in must not reveal that a
  disabled account exists.
- `ByID` **selects the column** — a caller already holding a valid token is entitled to
  learn their access was withdrawn.

Every read path that resolves a hirer must honour it. A missed one leaves a revoked seat
working, which is this column's whole risk.

**The password is not rotated and no credential is issued to anyone.** A disabled seat
cannot sign in whatever its password is. Handing an owner a working credential for a seat
they do not hold would attribute their actions to the departed person, destroying the
audit trail revocation exists to protect.

### Attribution

Wherever a response emits `created_by`, `added_by` or `requested_by`, it emits
`{id, username, display_name, active}` rather than a bare id. `active: false` marks a
revoked seat.

## Steps

1. **Schema.** Nothing is deployed, and `rfc/*.schema` applied in order _is_ the schema
   (`e2e/schema.go`), so the change is made at its origin rather than migrated:
   `hirer_accounts.username` / `disabled_at` / `disabled_by` and the removal of
   `organization_invitations` are edits to **RFC-0002**; `organization_hirer_roster` and
   `email_verifications` are the RFC-0016 delta. No `ALTER`, no `DROP`, no backfill —
   carrying migration scaffolding for a history that never existed would be a lie about
   how this database came to be.
2. **Repositories.** `ByUsername`, roster CRUD, `email_verifications` CRUD, seat listing
   including revoked. Honour `disabled_at` everywhere a hirer is resolved.
3. **Notifier.** Add `NotifyEmailVerification` — the enum has nine kinds and none proves
   an address.
4. **Services.** Redemption, verification, resend, revocation-on-removal. Owner-only
   authorisation on roster writes.
5. **Controllers.** The nine endpoints above. Remove the two invitation routes.
6. **Attribution.** Widen the author references across shortlists, saved searches and
   contact requests.
7. **Frontend.** Sign-in takes a username. Roster and seats screens. Redemption and
   verification flows. `AcceptInvitation` is deleted.
8. **Fixtures.** The e2e fixtures covering invitations must be **rewritten by the
   integration-tester**, not edited to pass by whoever implements this.

## Consequences

**Email delivery becomes load-bearing.** Today a `port.Notifier` outage costs a missed
reminder. After this it blocks hirer signup entirely. That is a real availability coupling
and should be monitored as one.

**The public organisation list discloses the customer base.** Only verified organisations
appear, so it also signals who is approved to hire. Accepted in exchange for a redeemer
being able to find their employer without the organisation sending them anything. The
payload is narrowed to name and slug — no roster size, no seat count, no payment status.

**Usernames are a finite namespace.** Global uniqueness plus no reuse means common names
are consumed permanently. An owner will sometimes need two attempts.

**A roster entry may never be redeemed.** Inert and harmless, but rosters will drift out
of date. No expiry: an entry is a standing intent, and expiring it silently would surprise
an organisation that does not re-check.

**This is stage-4 work arriving during stage 6.** It touches controllers, services,
repositories and the schema, and it removes two endpoints the frontend currently calls.

## Amendments

None yet.
