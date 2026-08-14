# ADR-0003 — Claims, evidence & skill standing

**Status:** Accepted · **From:** [RFC-0003](../rfc/RFC-0003-skill-claims-and-evidence.md)
· **Schema:** [RFC-0003-skill-claims-and-evidence.schema](../rfc/RFC-0003-skill-claims-and-evidence.schema)

## Decision

1. A claim carries **1–5 PR evidence items**, **0–20 optional projects**, and **1+ skills**,
   one nominated primary.
2. **`(user, PR, skill)` is unique** across a contributor's active claims, enforced by the
   primary key of `user_skill_pr_links`. A conflicting submission is **rejected, naming the
   conflict**.
3. **Standing is derived**: 5+ distinct evidencing PRs → primary and ranked; 1–4 →
   secondary, unranked, still contributing to the overall score. Promotion is automatic.
4. **Zero means rejected, never stored.** Including for the nominated primary — the claim
   survives with its remaining skills.
5. **Every PR is judged.** No pre-filtering of trivial-looking changes.
6. **Withdrawal warns before demoting**, then demotes immediately, and **releases its
   `(PR, skill)` pairs**.
7. **7-day lock per claim** after scoring. Other skills stay freely claimable. No paid
   bypass — payments are out of scope for v1.
8. **`pr-review` is claimed, not derived**: evidence role `reviewer`, scoring mode
   `judged_only`, no project evidence accepted.
9. Skill requests: **3 per contributor per week**, auto-deduped against names and aliases
   before reaching a human.
10. The model **suggests at most 3 skills**, capped in the prompt. Contributor declarations
    are uncapped. Suggestions are inert until accepted.

## Implementation

### Endpoints

```
GET    /skills?q=                    catalogue search (name + alias, trigram)
POST   /skill-requests               rate-limited, auto-deduped

POST   /claims                       create draft
PUT    /claims/{id}                  edit; 409 while locked_until > now()
POST   /claims/{id}/evidence/prs     add/replace PR evidence
POST   /claims/{id}/evidence/projects
POST   /claims/{id}/skills           declare skills
POST   /claims/{id}/submit           validate → queue
POST   /claims/{id}/withdraw         requires ?confirm_demotion=true if it demotes
GET    /claims/{id}/withdraw-preview which skills would demote
POST   /claims/{id}/suggestions/{skill_id}/accept | /dismiss

GET    /me/skills                    standings + scores
```

### Validation pipeline

Runs in `validating`, in this order, so cheap local failures precede API calls:

```
1. structural   cardinality (1-5 PRs, 0-20 projects, >=1 skill, exactly 1 nominated
                primary); pr-review claims reject project evidence
2. parse        URL -> (owner, repo, number); malformed_url
3. local dedupe duplicate_in_claim
4. pair check   (user, PR, skill) against user_skill_pr_links; on conflict return the
                owning claim id -> "PR 3 already evidences Go in claim X"
5. github       exists, public, merged; role check:
                  author   -> author id == claimant github_user_id
                  reviewer -> claimant reviewed it AND is not the author
6. enrich       cache facts onto the evidence rows
```

Every failure records `invalid_reason` **on the specific evidence row** and the claim goes
to `invalid`, not to a generic error. The contributor is doing curation work; a vague
rejection wastes it.

### The pair table is the invariant

`user_skill_pr_links` is written **only** in the transaction that transitions a claim to
`queued`, and deleted in the transaction that withdraws it or moves it back to `draft`. Its
primary key means a race between two concurrent submissions of the same PR+skill loses at
the database rather than by application check-then-act.

```sql
-- in the submit transaction
INSERT INTO user_skill_pr_links (user_id, skill_id, repo_owner, repo_name, pr_number, claim_id)
SELECT ... FROM claim_pr_evidence e CROSS JOIN claim_skills s WHERE ...
-- a unique violation here aborts the submit and is reported as the pair conflict
```

### Standing recomputation

After an evaluation persists (ADR-0004), for each affected `(user, skill)`:

```
n     := SELECT count(*) FROM user_skill_pr_links WHERE user_id=$1 AND skill_id=$2
standing := n >= 5 ? primary : secondary
score := scoring.SkillScore(...)          -- ADR-0005
UPSERT user_skills; if standing flipped to primary, set promoted_at
```

A skill dropping to zero PRs has its `user_skills` row **deleted**, not zeroed —
consistent with "zero is never stored".

### Withdrawal

`GET /claims/{id}/withdraw-preview` returns the skills that would demote. `POST .../withdraw`
requires `confirm_demotion=true` when that list is non-empty; without it, 409 with the list.
Standing never disagrees with evidence, and nobody loses a ranking unasked.

### Skill requests

Before insert: trigram-match `proposed_name` against `skills.name`, `skills.slug`, and
`skill_aliases.alias` at a similarity threshold. A match is rejected immediately with a
pointer to the existing skill and **never enters the queue**. Most noise is spelling
variants and no admin should spend attention on them.

Rate limit: 3 per rolling 7 days per contributor, counted on `skill_requests.created_at`.

### The 7-day lock

`claims.locked_until = evaluated_at + 7 days`, set when an evaluation succeeds. Enforced in
the claim service on edit, submit, and withdraw. Creating a claim for a _different_ skill is
unaffected — the lock is per claim, not per account.

## Steps

1. Domain: `Claim`, `PrEvidence`, `ProjectEvidence`, `ClaimSkill`, `UserSkill`, `Standing`,
   `EvidenceRole`.
2. `port.ClaimRepository`, `port.SkillRepository`, `port.StandingRepository`,
   `port.GitHubClient`.
3. Postgres implementations; `user_skill_pr_links` maintenance inside the submit transaction.
4. GitHub client + **fake** with fixtures for: merged, not merged, wrong author, reviewed,
   authored-by-claimant, 404, private, rate-limited.
5. Validation pipeline as ordered, individually tested stages.
6. Claim CRUD endpoints, draft mutability rules, lock enforcement.
7. Submit: validate → write links → enqueue → transition, **all in one transaction**.
8. Withdraw preview and confirmation.
9. Standing recomputation as a pure function over link counts plus scores, called by the
   evaluator.
10. Skill catalogue search, request endpoint, trigram dedupe, rate limit.
11. Suggestion accept/dismiss.

## Consequences

- **The pair uniqueness rule can surprise.** A contributor reusing a favourite PR across
  several claims for the same skill is refused. The error names the owning claim, which is
  the only thing making it navigable.
- **Auto-promotion means standing can change without the contributor acting** — a claim for
  Go can promote Kubernetes. Correct, but the UI must explain it.
- **Trigram dedupe will produce false positives.** "Rust" and "Trust" are close. Threshold
  needs tuning against real requests, and a rejected request should say which skill it
  matched so a contributor can push back.

## Amendments

| Date       | Change                                                                                                                                                                                                                                         |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-08-02 | Accepted from RFC-0003                                                                                                                                                                                                                         |
| 2026-08-14 | **Amended by [ADR-0007](ADR-0007-rubric-contract-and-generalist-score.md)** — `distinct_pr_count` counts only `status='scored'` links, so a rejected PR leaves a skill at four and secondary. `claim_skills.rejection_reason` becomes an enum. |
