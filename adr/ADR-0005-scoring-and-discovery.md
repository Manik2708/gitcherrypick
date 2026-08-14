# ADR-0005 — Scoring, ranking & discovery

**Status:** Accepted · **From:** [RFC-0005](../rfc/RFC-0005-ranking-and-discovery.md)
· **Schema:** [RFC-0005-ranking-and-discovery.schema](../rfc/RFC-0005-ranking-and-discovery.schema)

## Decision

1. Three scores: **PR score** (per PR per skill), **Skill score** (per user per skill),
   **Overall score** (per user). Formulas below are binding.
2. Arithmetic signals normalise against **platform-wide maxima**, blending a log term and a
   percentile term.
3. **Score components are persisted**, not derived on read.
4. The **arithmetic recompute runs nightly** and exits immediately if no maximum moved.
5. **`pr-review` drops the PR-level arithmetic term but keeps project reach**: 0.80 judged +
   0.20 reach, no engagement.
6. **Diff statistics are context for the model, not a scored signal.**
7. **Percentiles are computed internally; rank and raw score are displayed.**
8. **No public leaderboard.** Contributors see only their own rank.
9. Shortlisting raises a **contact request**; email is released only on contributor
   acceptance, with the payment-verification label disclosed.
10. Organizations with **≥80% of open shortlists overdue** are flagged to admins. Nothing is
    blocked.
11. Every constant is a **rubric parameter**, loaded from a version-controlled file
    (ADR-0006).

## Implementation

### The scoring package is pure

`internal/scoring` takes values and returns values. **No database, no clock, no I/O.** Every
formula below is a function testable with a table of inputs and expected outputs, which is
the only way these stay auditable.

```go
func Normalise(v float64, m Norms) float64          // log/percentile blend
func ProjectReach(f RepoFacts, n Norms, maintainer bool) float64
func PRScore(q Quality, r, e float64, mode SkillScoringMode, w Weights) float64
func SkillScore(prScores []float64, n int, projectComponent float64, w Weights) float64
func OverallScore(skills []SkillStanding, w Weights) float64
```

### Formulas (binding)

```
norm(v)   = β·ln(1+v)/ln(1+Mₘ) + (1−β)·pct(v)                      β = 0.5

R         = 0.35·norm(stars) + 0.15·norm(forks) + 0.20·norm(contributors)
          + 0.20·norm(dependents) + 0.10·norm(downloads)
            missing metrics dropped, remaining weights renormalised
            maintainer validated → R = min(1, R · 1.25)

Q         = 0.30·substance + 0.25·complexity + 0.20·conversation_quality
          + 0.15·craft + 0.10·skill_specificity

E         = 0.40·norm(review_comments) + 0.30·norm(reviews) + 0.30·norm(participants)

PR_score  = 100·[0.70·(Q/100) + 0.20·R + 0.10·E]          standard
          = 100·[0.80·(Q/100) + 0.20·R]                   judged_only (pr-review)

PR_score(secondary) = relative_share · PR_score(nominated primary)     uncapped

PR_component = n ≥ 5 ? mean(best 5 PR_scores) : mean(all n) · (n/5)
Skill_score  = 100·[0.85·PR_component/100 + 0.15·Project_component]    standard
             = PR_component                                            judged_only

ordered list vᵢ = primaries ∪ (secondaries·σ) ∪ (pr-review·ω), desc    σ=0.5, ω=1.2 cap 100
B        = Σ(i≥2)(vᵢ/100)·d^(i−1) / Σ(i≥2) d^(i−1)                     d = 0.5
Overall  = v₁ + (100 − v₁)·γ·B                                         γ = 0.5
```

No overall score without at least one primary skill — `NULL`, not zero.

### Norms maintenance

Every enrichment writes to `metric_observations` and updates `global_norms.max_value` if
exceeded, bumping `generation`. Quantiles (p10/p25/p50/p75/p90/p99) are refreshed by the
nightly job; `pct()` interpolates between boundaries. Exact percentiles cost a full scan per
query and no product decision turns on the 73rd versus 74th percentile.

`metric_observations` is pruned by reservoir sampling above a row threshold — unbounded
append would consume the Neon storage ceiling (ADR-0006).

### Nightly recompute

```
1. any global_norms.generation changed since last run? no → exit
2. recompute quantiles, bump generation
3. for each pr_skill_scores row with norms_generation < current, in batches:
     recompute R and E from stored facts; recompute score from stored quality_q
4. recompute user_skills, users.overall_score
5. snapshot with reason='arithmetic_recompute'
```

Step 3 re-reads **nothing** from GitHub and calls **no model** — `quality_q` is already
stored, which is what makes the recompute free. A new maximum invalidates every row using
that metric, so this is full-corpus; `norms_generation` makes it resumable, not smaller.

### Search

Filters compose into one query. Hard gates first, cheapest first:

```
availability status set AND expires_at > now()      -- ADR-0002
AND NOT self (hirer's github identity)              -- SQL exclusion, not post-filter
AND user_skills.standing = 'primary'                -- only ranked skills match a skill filter
AND user_skills.score >= $min
AND users.overall_score >= $min_overall
AND rubric_version = $active
```

Multiple skills means **AND** — a `HAVING count(distinct skill_id) = $n` over the filter set.

### Tie-breakers

Applied in order, **never published**: total merged contributions → repositories
contributed to or created → GitHub followers → earlier platform join → tied, displayed
alphabetically. Publishing them would turn them into optimisation targets, and each is a
weaker signal than the score.

### Shortlists and contact requests

Adding an entry creates a `contact_request` in the same transaction, copying the shortlist's
`tentative_result_date` so the record of what the contributor was told survives a later
edit. The contributor sees the organization, the date, and — when
`hirer_accounts.payment_verified_at IS NULL` — the disclosure:

> *This organization is hiring for the first time and has not verified payment capability.*

Email is released **only** on acceptance, stamping `email_released_at`. Nothing about
shortlist membership is visible to the contributor beyond their own pending requests.

Overdue flagging, daily: per organization, `open shortlists past tentative_result_date /
open shortlists ≥ 0.8` → notify the org, flag to admins. Nothing is blocked; blocking would
punish a hirer for a candidate's silence as readily as for their own neglect.

## Steps

1. `internal/scoring` with every formula above and a table-driven test per function,
   including the RFC-0005 worked examples as fixtures.
2. Rubric config loader (ADR-0006) → `scoring.Weights`.
3. `global_norms` / `metric_observations` repository; observation write on enrichment.
4. Quantile estimator + `pct()` interpolation.
5. Wire scoring into the evaluator persist step (ADR-0004 step 9), storing `quality_q`,
   `reach_r`, `engagement_e`, `norms_generation`.
6. Nightly recompute job, resumable, batched.
7. Search query builder + repository, with the self-exclusion as a join condition.
8. Tie-breaker ordering in the ranking query.
9. Shortlist CRUD; contact request creation in the same transaction.
10. Contributor accept/decline; email release.
11. Daily overdue-ratio job.

## Consequences

- **Scores drift without the contributor acting.** Every recompute snapshot carries a reason
  so this is explicable, but "my score dropped and I did nothing" will still be asked.
- **A single outlier moves everyone.** The log/percentile blend bounds this; it does not
  eliminate it.
- **Persisted components are derived data** and can disagree with the formula if someone
  changes `scoring` without a recompute. A rubric version bump must force one.
- **Contact requests will stall.** A contributor who never answers leaves the hirer with
  nothing. Accepted as the cost of consent.

## Amendments

| Date | Change |
|---|---|
| 2026-08-02 | Accepted from RFC-0005 |
| 2026-08-14 | **Amended by [ADR-0007](ADR-0007-rubric-contract-and-generalist-score.md)** — adds the **Generalist score** and the quality floor; `project_component` 0.15 → 0.30; four explicit project-reach weight cases; `conversation_quality` scores 0 with no discussion; adds the re-evaluation flow. `γ` is retained. |
