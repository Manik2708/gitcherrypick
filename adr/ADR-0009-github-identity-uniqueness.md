# ADR-0009 — One contributor per GitHub identity

**Status:** Accepted · **From:** [RFC-0009](../rfc/RFC-0009-github-identity-uniqueness.md)
· **Schema:** [`RFC-0009-*.schema`](../rfc/RFC-0009-github-identity-uniqueness.schema)
· **Amends:** ADR-0002

## Decision

1. **A `github_user_id` identifies at most one contributor, and at most one hirer.**
2. Enforced by **two partial unique indexes**, one per namespace — not by a plain
   `UNIQUE (github_user_id)`, which would forbid the legitimate case of a contributor and a
   hirer sharing one identity.
3. **The repository does not check first.** It attempts the insert and translates the
   violation to `port.ErrConflict`.
4. `idx_github_identities_github_user` **stays**. `AssertNotSelf` queries across both
   namespaces and no partial index can serve a query that spans them.

## Implementation

```sql
CREATE UNIQUE INDEX uq_github_identity_contributor
    ON user_github_identities (github_user_id) WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX uq_github_identity_hirer_github
    ON user_github_identities (github_user_id) WHERE hirer_account_id IS NOT NULL;
```

`ck_identity_single_owner` already guarantees exactly one of `user_id` and
`hirer_account_id` is set, so the two predicates partition the table rather than
overlapping. No row is covered by both indexes, and none by neither.

### Why not a read-then-write

`UserRepository.Create` could look for an existing identity before inserting. That closes
the common case and not the race: two concurrent OAuth callbacks for the same new identity
would both find nothing and both insert. Sign-up is exactly where that race is reachable,
because it is driven by an external redirect the platform does not serialise.

The index makes it impossible rather than unlikely, and lets `translate()` turn `23505`
into `port.ErrConflict` with no extra round trip.

## Steps

1. Migration: the two `CREATE UNIQUE INDEX` statements. They must fail loudly if a duplicate
   already exists — a duplicate means two accounts believing they are the same person, and
   choosing one for them is not a decision a migration should make.
2. No repository change. `Create` already surfaces `port.ErrConflict` through `translate()`;
   the index is what makes the path reachable.

## Consequences

- **A duplicate identity is now an error where it was silently accepted.** Nothing in the
  seeded fixtures creates one, so no fixture changes.
- **The contributor and hirer namespaces stay independent**, which is what keeps the
  `dave` / `dave_hiring` self-exclusion case working.
- **`ByGitHubUserID` is now single-valued by construction.** Before this, contributor
  sign-in resolved to whichever row the planner returned — an account-takeover path on the
  one lookup ADR-0002 builds all contributor authentication on.

## Amendments

| Date       | Change                 |
| ---------- | ---------------------- |
| 2026-08-16 | Accepted from RFC-0009 |
