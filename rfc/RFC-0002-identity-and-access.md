# RFC-0002 — Identity, access & organizations

**Status:** Approved 2026-08-02 · **Binding form:** [ADR-0002-identity-and-access](../adr/ADR-0002-identity-and-access.md) · **Schema:** [RFC-0002-identity-and-access.schema](RFC-0002-identity-and-access.schema)
· **Depends on:** RFC-0001

## Summary

Three separate account types with three separate authentication paths, because they are
three different trust problems. Contributors prove they own a GitHub identity. Hirers
prove they are legitimately hiring, and are verified by a human. Admins are seeded and
never self-register.

## Why three paths

**Contributors authenticate with GitHub only.** Every claim in this product is a claim
about GitHub activity. If a contributor could register with an email and then assert a
GitHub login, the entire evidence model would rest on an unverified string. Binding the
account to a GitHub identity at authentication time means authorship checks (RFC-0003)
compare a verified account id against PR metadata, never user input against user input.

**Hirers authenticate however they like — Google, email, or GitHub.** Requiring a GitHub
account of a non-technical recruiter is friction with no security benefit; a hirer makes
no evidence claims, so there is nothing for a GitHub identity to secure. What matters for
a hirer is not *which account* but *whether they are real*, and that is answered by manual
verification, not by the identity provider.

**Admins are seeded, with email and password.** Created by a CLI flag on first boot, never
self-registerable, with no dependency on an external provider being reachable. The
accounts that verify everyone else must not themselves depend on a third party.

## Scorecards are gated

**There is no public leaderboard and no public profile.** A logged-out visitor sees the
marketing surface and nothing else.

| Viewer | Can see |
|---|---|
| Logged out | Nothing |
| Contributor | Their **own** scorecard, scores, and rank. Not anyone else's. |
| Verified hirer | Full scorecards, rankings, search, and comparison across contributors |
| Admin | Everything, plus verification queues |

A contributor cannot browse the leaderboard to see who is above them. The full scorecard —
per-PR rationale, dimension breakdown, evidence — is the product, and it is what
verification buys.

The cost is real: no public ranking means no viral surface, and contributor acquisition
has to come from somewhere else. Accepted deliberately, because publishing a ranked list
of named engineers to anyone who asks is a different product with different consent
requirements.

## Contributor authentication

OAuth 2.0 authorization code flow with PKCE against GitHub.

1. Frontend requests `GET /auth/github/start`. The api generates a `state` and PKCE
   verifier, stores them server-side against a short-lived id in an httpOnly cookie, and
   returns the authorize URL.
2. User authorizes on github.com.
3. GitHub redirects to `GET /auth/github/callback?code&state`.
4. The api verifies `state`, exchanges the code plus verifier, and reads the identity.
5. The api upserts by **GitHub numeric account id** and issues our own session.

PKCE is used even though this is a confidential client — it costs nothing and removes an
assumption we would otherwise have to revisit if a native client ever appears.

### Identity is keyed on the numeric id

`github_user_id` is the key. `github_login` is cached for display and re-synced on every
login, because logins are mutable and reusable: a contributor renames, and a different
person takes the freed name. Keying on the login would silently reassign an account and
every score attached to it.

### The GitHub token

Stored encrypted at rest, used only to read public metadata on that user's behalf for the
better rate limit. Scopes: `read:user`, `public_repo`. Never a write scope, never private
repository access — we evaluate public contributions only, so anything more would be
permissions we have no use for.

## Hirer accounts

A hirer account is **separate from any contributor account**, with its own credentials.

**Every hirer account is linked to a GitHub identity**, whichever provider they signed in
with. That link exists for one reason: **a hirer cannot see, shortlist, or rank the
contributor account bound to the same GitHub identity.** Without it, someone could create a
hiring account to inspect their own scorecard and standing, which is exactly what gating
was for.

A contributor who wants to hire registers a **second, separate account**. They do not gain
a role on their existing one. The person being ranked and the person ranking stay distinct
identities, and the self-exclusion link makes the separation enforceable rather than
merely conventional.

### Verification is manual

**No hirer account can hire until an admin has verified it.** An unverified hirer can sign
in and see their own dashboard; they cannot search, view a scorecard, or shortlist.

Proof required, at least one of:

| Proof | For |
|---|---|
| Organization LinkedIn URL | Companies |
| Work email on the organization's domain | Companies with their own domain |
| Freelancer profile URL | Individuals hiring freelancers (Upwork, Toptal, etc.) |
| Payment capability evidence | Freelance hirers — see below |
| **Alternative evidence** | **Small firms with no company domain or LinkedIn page** |

The alternative path matters: a three-person studio hiring a contractor is exactly the
customer we want and often has neither a domain email nor a company LinkedIn page.
They submit whatever they have — a registration document, a website, a client reference —
in a free-text field with attachments, and an admin judges it. A verification scheme that
only large companies can satisfy would exclude most freelance hiring.

**Payment capability evidence** is one of three named artifacts: a past contract, a
payment-platform history, or a business bank reference.

**Not supplying one does not block verification — it is disclosed instead.** A hirer
verified on identity but without payment evidence is labelled, and a contributor sees that
label before deciding whether to release their email:

> *This organization is hiring for the first time and has not verified payment capability.*

The same applies to companies. This is deliberately transparency rather than gatekeeping:
we have no reliable way to verify that someone will actually pay, and refusing every
first-time hirer would exclude exactly the small studios the alternative-evidence path
exists for. The contributor is the one carrying the risk of an unpaid engagement, so the
contributor is given the fact and the choice.

### Verification turnaround

**Target: 48 hours.** Admins are alerted when the pending queue exceeds a depth threshold
or when any request ages past target. Publishing a target we then miss silently is worse
than publishing none, so the alert exists to make sure we find out before applicants do.

Verification decisions are recorded with the deciding admin, timestamp, and reason, so a
rejection can be explained and appealed.

### On the self-hire link

The GitHub identity link stops a verified hirer from seeing the contributor account sharing
that identity. It is a **convenience guard, not a security boundary** — it costs nothing
and prevents an obvious awkwardness.

It can be defeated with a second GitHub account, and that is fine. Registering a hirer
account still requires passing manual admin review with real proof of hiring intent, so the
second account buys nothing on its own. And someone who *has* legitimately passed
verification is entitled to view scorecards regardless; the only thing the link denies them
is their own, which they can already see as a contributor. There is no exploit here worth
hardening against.

## Availability signalling

A contributor controls whether hirers see them at all, through an availability status:

| Status | Meaning |
|---|---|
| `not_looking` | Default. Invisible to hirers. |
| `looking_for_job` | Employment |
| `looking_for_freelance` | Freelance only |
| `open_to_freelance` | Employed, open to freelance work |

**The status expires after 15 days.** If the contributor does not refresh it, the profile
reverts to invisible. Stale availability is the standard failure of every hiring platform —
hirers waste effort on people who stopped looking months ago. A short expiry means a
visible profile is an actively maintained one. Contributors get a reminder before it
lapses; lapsing hides the profile and never deletes anything.

## Organizations

A hirer belongs to an organization. Organizations own shortlists (RFC-0005), so that work
survives an individual leaving.

**Only admin-verified organizations may hire.** Verification is manual and requires the
organization's LinkedIn page alongside the other proofs.

**Once verified, the organization runs itself.** It invites and removes its own members
through its dashboard, with no admin involvement. Admins verify; they do not administer
someone else's team. The organization is notified whenever a member is added, so
membership changes are never silent to the people accountable for them.

Membership roles are `owner` and `member`; only owners invite or remove. Invitations are by
email, single-use, and expire in 7 days.

## Share links — the one thing a contributor can publish

Full gating leaves nothing shareable: no leaderboard, no public profile, nothing to put on
a CV. That is a real cost, and this is the answer to it.

A contributor can generate a **share link to their own scorecard**. Off by default,
revocable at any time, and scoped to them alone — it exposes their scores and evidence, and
nothing about anyone else's ranking. They choose to publish; we never do.

This keeps the consent model intact. The objection to a public leaderboard was never that
scores are secret — it was that *we* should not publish a ranked list of named engineers to
anyone who asks. A link the subject creates and can revoke is the opposite of that.

## Privacy

- We store no contributor password; there is nothing to breach.
- **A contributor's email is never released without their approval** (RFC-0005). A
  shortlist creates a request; the contributor decides.
- Account deletion hard-deletes claims, evidence, and snapshots, and revokes all sessions.
  We keep no shadow profile.
- Visibility to hirers is opt-in via availability status, and lapses by default.

## Sessions

| Token | Form | Lifetime | Storage |
|---|---|---|---|
| Access | JWT, EdDSA (Ed25519) | 15 minutes | In memory, client-side |
| Refresh | 256-bit opaque random | 30 days, rotating | httpOnly, Secure, SameSite=Lax cookie |

The access token is a JWT so authorization needs no database round trip. It is short-lived
because it cannot be revoked; revocation acts on the refresh token, bounding a revoked
session to one access-token lifetime.

Refresh tokens are opaque, stored **hashed** (SHA-256), and rotated on every use. **Reuse
detection:** presenting an already-used token means it leaked, so the entire session family
is revoked and the user must re-authenticate.

Admin sessions use the same scheme with a 30-minute access token and a 12-hour refresh
lifetime.

## Resolved review comments

| Marker | Resolution |
|---|---|
| Hirers should not use GitHub auth; three account types needed | Three paths: contributor (GitHub), hirer (Google/email/GitHub), admin (seeded email+password) |
| Scorecard must not be public | Fully gated. Contributors see only their own rank; full cards require a verified hirer |
| Recruiters must be manually verified with proofs | Manual admin verification; LinkedIn / domain email / freelancer profile / payment evidence / free-form alternative |
| Contributor who wants to hire | Separate account, GitHub identity linked to block self-hire |
| Small firms lack org emails | Explicit alternative-evidence path, admin-judged |
| Org verification manual | Admin-verified; only verified orgs may hire |
| Org invites its own members | Org self-administers membership; org notified on every add |
| Availability options needed | Four availability statuses with a 15-day expiry |
| Q2 org email domain claiming | Dropped — not needed |
| Q3 token encryption / KMS | Simplicity for v1: key supplied by flag, KMS deferred |

## Open questions

None outstanding. Verification turnaround, payment evidence, the self-hire link, and
contributor acquisition were all resolved in this revision and are documented above.

## Schema

Introduces `users`, `user_github_identities`, `user_availability`, `hirer_accounts`,
`admin_accounts`, `sessions`, `organizations`, `organization_members`,
`organization_invitations`, and `verification_requests`.
