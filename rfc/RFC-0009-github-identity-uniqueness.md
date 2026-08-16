# RFC-0009 — One contributor per GitHub identity

**Status:** Approved 2026-08-16 · **Binding form:** [ADR-0009](../adr/ADR-0009-github-identity-uniqueness.md)
· **Schema:** [RFC-0009-github-identity-uniqueness.schema](RFC-0009-github-identity-uniqueness.schema)
· **Amends:** RFC-0002 (and ADR-0002)

## Summary

Implementing `UserRepository` surfaced a gap in the approved schema. `user_github_identities`
constrains **one identity per account** and never **one account per identity**, so two
contributors may hold the same `github_user_id`.

ADR-0002 makes `ByGitHubUserID` the entire basis of contributor sign-in — identity is the
immutable numeric id, never the login. With a non-unique index behind it, that lookup is
ambiguous: an OAuth callback resolves to whichever row the query planner returns.

This was found by a test asserting that two contributors cannot claim one GitHub identity.
The test currently fails, and it is the reason this RFC exists.

## The constraint cannot be a plain UNIQUE

A contributor and a hirer may legitimately share one GitHub identity. That is precisely the
`dave` / `dave_hiring` case `AssertNotSelf` exists for (ADR-0002), and it is seeded in the
integration fixtures. `UNIQUE (github_user_id)` would forbid it and break the self-exclusion
tests along with the real behaviour they pin.

So uniqueness is **per namespace**, as two partial indexes:

```sql
CREATE UNIQUE INDEX uq_github_identity_contributor
    ON user_github_identities (github_user_id) WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX uq_github_identity_hirer_github
    ON user_github_identities (github_user_id) WHERE hirer_account_id IS NOT NULL;
```

`ck_identity_single_owner` already guarantees exactly one of the two columns is set, so the
predicates partition the table rather than overlapping.

The existing non-unique `idx_github_identities_github_user` stays: `AssertNotSelf` queries
across both namespaces, and neither partial index can serve a query that spans them.

## Why an index rather than a repository check

`UserRepository.Create` could look for an existing identity before inserting. That closes
the common case and not the race — two concurrent OAuth callbacks for the same new identity
would both see nothing and both insert. Sign-up is exactly the path where that race is
reachable, since it is driven by an external redirect the platform does not serialise.

A unique index is the only thing that makes it impossible, and it lets the repository
translate the violation into `port.ErrConflict` rather than checking first.

## Consequences

- **A duplicate identity becomes an error where it was silently accepted.** Nothing in the
  seeded fixtures creates one, so no fixture changes.
- **Migration order matters if any duplicates already exist.** None can, in an unreleased
  system with no production data, but the migration should still fail loudly rather than
  deduplicate silently — a duplicate would mean two accounts believing they are the same
  person, and choosing one for them is not a decision a migration should make.
