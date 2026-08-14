# ADR-0002 — Identity, access & organizations

**Status:** Accepted · **From:** [RFC-0002](../rfc/RFC-0002-identity-and-access.md)
· **Schema:** [RFC-0002-identity-and-access.schema](../rfc/RFC-0002-identity-and-access.schema)

## Decision

1. **Three account types, three auth paths.** Contributors: GitHub OAuth only. Hirers:
   Google, email+password, or GitHub. Admins: seeded email+password, never self-registerable.
2. **Scorecards are fully gated.** Logged-out sees nothing. A contributor sees only their
   own scores and rank. Cross-contributor rankings and full scorecards require a
   **verified** hirer.
3. **Hirer accounts are separate** from contributor accounts and are linked to a GitHub
   identity so a hirer cannot see the contributor sharing it.
4. **No hirer may hire until an admin verifies them.** Target turnaround 48h, with queue
   alerts.
5. **Missing payment evidence does not block** — it is disclosed to the contributor at
   contact-request time.
6. **Availability expires after 15 days.** A lapsed contributor is invisible to hirers.
7. **Contributor-generated share links** are the only publishable surface.
8. Access tokens: Ed25519 JWT, 15 min. Refresh: opaque, hashed, rotating, 30 days, with
   reuse detection revoking the family.

## Implementation

### Endpoints

```
POST   /auth/github/start            → authorize URL + state cookie
GET    /auth/github/callback         → session
POST   /auth/hirer/register          → creates account + verification request
POST   /auth/hirer/login             → session (email path)
GET    /auth/google/start|callback   → session (hirer only)
POST   /auth/admin/login             → session
POST   /auth/refresh                 → rotate
POST   /auth/logout                  → revoke family

GET    /me
PUT    /me/availability              → status + resets expires_at to now()+15d
POST   /me/share-link                → mint (revokes any active one)
DELETE /me/share-link/{id}
GET    /public/scorecard/{token}     → unauthenticated, single contributor

GET    /admin/verifications          → pending queue
POST   /admin/verifications/{id}/decide

POST   /orgs/{id}/invitations        → org self-administers (no admin involvement)
POST   /orgs/invitations/{token}/accept
```

### Authorization

A middleware resolves the session to a `domain.Principal` — one of `Contributor`, `Hirer`,
`Admin` — and puts it in context. **Controllers never decide access.** They extract the
principal and pass it to a service, which decides.

Two service-layer gates that must exist as named, individually tested functions:

```go
// Fails unless the hirer is verified AND their org is verified.
func (s *accessService) RequireHiringCapability(p domain.Principal) error

// Fails if the target contributor shares a GitHub identity with this hirer.
func (s *accessService) AssertNotSelf(hirer domain.HirerID, target domain.UserID) error
```

`AssertNotSelf` is called in search, scorecard read, and shortlist add. Search applies it as
a SQL exclusion rather than a post-filter, so a self-match never appears in a result count.

### Sessions

Refresh is a single transaction:

```
SELECT ... FROM sessions WHERE refresh_token_hash = $1 FOR UPDATE
  not found                        -> 401
  revoked_at IS NOT NULL           -> 401
  expires_at < now()               -> 401
  used_at IS NOT NULL              -> REUSE: revoke entire family, 401
otherwise: mark used, insert successor with same family_id, return new pair
```

Reuse detection is the security-relevant branch and gets its own e2e test.

### Verification

`POST /auth/hirer/register` creates the account and a `pending` verification request with
its proofs in the same transaction. The account can sign in immediately and see its own
dashboard; `RequireHiringCapability` fails until approved.

Payment evidence is one of three artifacts (past contract, payment-platform history,
business bank reference). Absent, `payment_verified_at` stays NULL and the contact request
carries the disclosure label (ADR-0005).

Admin seeding: `--admin-seed-email` / `--admin-seed-password` on first boot creates one
admin if `admin_accounts` is empty, then logs that it did. Never on subsequent boots.

### Availability expiry

A job in the api, hourly:
- `expires_at < now()` → invisible to hirers. Enforced by the **search query**, not by
  mutating the row — a contributor whose status lapsed should see their setting preserved
  when they return, not silently reset.
- `expires_at < now() + 3 days` and `reminded_at` older than the current period → send a
  reminder, set `reminded_at`.

### Share links

32 random bytes, base64url, shown **once**. Stored as SHA-256. One active link per
contributor (partial unique index). `GET /public/scorecard/{token}` looks up by hash,
rejects revoked, increments `view_count`, and returns that contributor's scorecard only —
no rank against others, no navigation to anyone else.

## Steps

1. Domain types: `Principal`, `Contributor`, `Hirer`, `Admin`, `AvailabilityStatus`.
2. `port.UserRepository`, `port.SessionRepository`, `port.HirerRepository`,
   `port.OrgRepository`, `port.VerificationRepository`.
3. Postgres implementations + repository tests against a real database.
4. Session service: issue, rotate, reuse-detect, revoke-family. Unit tests with a fake clock.
5. GitHub OAuth: PKCE, state, callback, upsert on `github_user_id`, token encryption.
6. Hirer registration + Google and email paths; argon2id for passwords.
7. Admin seeding and login.
8. `RequireHiringCapability` and `AssertNotSelf`, with tests for the negative cases first.
9. Auth middleware and route wiring.
10. Verification queue endpoints + admin decision recording.
11. Availability endpoint and the hourly expiry/reminder job.
12. Share-link mint, revoke, and public scorecard read.

## Consequences

- **Hirers signing in with Google have no GitHub identity to link**, so `AssertNotSelf` is a
  no-op for them. The self-hire guard is best-effort by design (RFC-0002); manual
  verification is the real gate.
- **Manual verification is a human queue** and the first thing to break under growth. The
  48h target is a commitment we have to staff.
- **Full gating removes every viral surface.** Share links are the only mitigation, and they
  require the contributor to act.

## Amendments

| Date | Change |
|---|---|
| 2026-08-02 | Accepted from RFC-0002 |
| 2026-08-14 | **Amended by [ADR-0007](ADR-0007-rubric-contract-and-generalist-score.md)** — `users.generalist_score` added: a second user-level score, unbounded and searchable, alongside `overall_score`. |
