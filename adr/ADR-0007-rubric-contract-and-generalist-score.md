# ADR-0007 — Rubric contract, disqualification & the generalist score

**Status:** Accepted · **From:** [RFC-0007](../rfc/RFC-0007-rubric-contract-and-generalist-score.md)
· **Schema:** [`RFC-0007-*.schema`](../rfc/RFC-0007-rubric-contract-and-generalist-score.schema)
· **Amends:** ADR-0002, ADR-0003, ADR-0004, ADR-0005

## Decision

1. The judgement contract returns, per (PR, skill): **a score and a remark per dimension**, a
   **disqualification verdict**, and `relative_share` for secondary skills.
2. **Nine disqualification grounds**, exhaustive. `PR_score = 0` bypasses the arithmetic.
   **"AI-generated" is not a ground** — a merge by someone else is the gate that matters.
3. **Quality floor:** mean dimension score `< 5` zeroes the pair, computed in Go.
4. **Standing counts surviving evidence only.** `user_skill_pr_links.status` gates it; a
   rejected row is kept, not deleted.
5. **Generalist score** — a second user-level score, unbounded, searchable. `γ` retained on
   Overall.
6. **Re-evaluation requests** with a doubling cooldown capped at 365 days.
7. Constant changes per [evaluation/rubric-v1.md](../evaluation/rubric-v1.md).

## Implementation

### The judgement contract

Structured output schema, replacing the one in ADR-0004. One object per (PR, skill):

```json
{
  "type": "object",
  "required": ["repo_owner", "repo_name", "pr_number", "skill_slug", "disqualified"],
  "additionalProperties": false,
  "properties": {
    "repo_owner": { "type": "string" },
    "repo_name": { "type": "string" },
    "pr_number": { "type": "integer" },
    "skill_slug": { "type": "string" },

    "disqualified": { "type": "boolean" },
    "disqualification_reason": {
      "type": ["string", "null"],
      "enum": [
        "typo_or_wording",
        "formatting_only",
        "generated_output",
        "mechanical_dependency_bump",
        "revert_only",
        "not_the_claimed_skill",
        "authored_by_other",
        "unrelated_to_issue",
        "maintainer_flagged_unrelated",
        null
      ]
    },

    "dimensions": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "substance",
        "complexity",
        "conversation_quality",
        "craft",
        "skill_specificity"
      ],
      "patternProperties": {
        "^(substance|complexity|conversation_quality|craft|skill_specificity)$": {
          "type": "object",
          "additionalProperties": false,
          "required": ["score", "remark"],
          "properties": {
            "score": { "type": "integer", "minimum": 0, "maximum": 100 },
            "remark": { "type": "string", "maxLength": 400 }
          }
        }
      }
    },

    "relative_share": { "type": ["number", "null"], "minimum": 0 }
  }
}
```

For `pr-review` claims the five dimension keys are `insight`, `judgement`, `communication`,
`rigour`, `outcome` instead — a second schema selected by the skill's `scoring_mode`.

`below_quality_floor` is **absent from the enum**: the model never returns it, because Go
computes it. Including it would invite the model to use it and duplicate a decision that
belongs to code.

### Scoring changes

`internal/scoring` (ADR-0005) gains the short-circuit and the second score:

```go
type Verdict struct {
    Disqualified bool
    Reason       domain.RejectionReason
}

// Returns 0 and the reason when disqualified or below the floor. The weighted
// sum is not evaluated in either case — the arithmetic is bypassed, not reduced.
func PRScore(q Quality, r, e float64, mode SkillScoringMode,
             v Verdict, w Weights) (float64, domain.RejectionReason)

// v are the ordered skill scores: primaries at full weight, secondaries pre-
// scaled by sigma, pr-review by omega, sorted descending.
func OverallScore(v []float64, w Weights) *float64      // nil without a primary
func GeneralistScore(v []float64) *float64              // nil without a primary
```

```
Overall    = v₁ + (100 − v₁)·γ·B      B = Σ(i≥2)(vᵢ/100)·d^(i−1) / Σ(i≥2) d^(i−1)
Generalist = v₁ + Σ(i≥2) vᵢ/√i
```

Both return `nil` when no primary skill exists — **not zero**. Zero is a score; absence is
not, and the distinction is what stops an unranked contributor appearing at the bottom of a
leaderboard they should not be on at all.

### Standing recompute

```
scored := SELECT count(*) FROM user_skill_pr_links
           WHERE user_id=$1 AND skill_id=$2 AND status='scored'
standing := scored >= 5 ? primary : secondary
```

The partial index `idx_user_skill_pr_links_scored` serves exactly this. A skill dropping to
zero scored links has its `user_skills` row **deleted**, consistent with "zero is never
stored".

### Persist, revised

ADR-0004's persist transaction gains two steps, in this order:

```
2.  pr_skill_scores        — surviving pairs only
2a. user_skill_pr_links    — status := 'scored' | 'rejected' + reason, resolved_at
3.  claim_skills           — rejected_at + rejection_reason for zeroed skills
5.  user_skills            — recompute standing from SCORED links only
6.  users                  — overall_score AND generalist_score
7.  score_snapshots        — one row per skill (kind='skill'), plus kind='overall'
                             and kind='generalist'
```

### Re-evaluation cooldown

```go
// On admin rejection.
func (s *reevalService) recordRejection(ctx, c *domain.Cooldown, now time.Time) {
    c.RejectionCount++
    if c.RejectionCount < 3 { return }
    c.Tier++
    c.CooldownUntil = now.Add(cooldownFor(c.Tier))
    c.RejectionCount = 0                 // resets; tier does not
}

// 28, 56, 112, 224, then 365 forever.
func cooldownFor(tier int) time.Duration {
    d := 28 * (1 << (tier - 1))
    if d > 365 { d = 365 }
    return time.Duration(d) * 24 * time.Hour
}
```

**Acceptance touches neither counter.** A contributor who is repeatedly right is never
throttled — which is the population whose disputes are worth most.

Requests are refused while `now() < cooldown_until`, with the expiry date in the error so a
contributor knows when they may try again.

### Endpoints

```
POST /claims/{id}/reevaluation        contributor; 429 while cooling down
GET  /admin/reevaluations             pending queue
POST /admin/reevaluations/{id}/decide accept → re-queue claim + record for tuning
                                      reject → recordRejection()
POST /admin/evaluations/sweep         global re-run for prompt/model change
```

Search gains `min_generalist_score`, served by `idx_users_generalist_score`.

## Steps

1. Domain types: `RejectionReason`, `PRLinkStatus`, `ScoreKind`, `Verdict`, `Cooldown`.
2. Migrations from `RFC-0007-*.schema`. **The `ALTER TYPE ... ADD VALUE` must be its own
   file**, ahead of anything referencing `'reevaluation'` — Postgres forbids using a new enum
   value in the transaction that added it.
3. Rewrite the structured-output schema in `internal/ai/anthropic/schema.go`; add the
   `pr-review` variant selected by `scoring_mode`.
4. `PRScore` short-circuit + quality floor, with table tests covering: disqualified,
   floor-triggered, and a high-`R` typo fix scoring **0 rather than 18** — that last case is
   the regression test for the bug this ADR exists to fix.
5. `GeneralistScore`, with the four RFC-0007 profiles as fixtures.
6. Persist transaction steps 2a/5/6/7.
7. Standing recompute against `status='scored'`.
8. Re-evaluation endpoints, cooldown service, admin queue.
9. `min_generalist_score` filter and index.

## Consequences

- **A rejected PR silently costs primary standing.** Correct, and it will surprise
  contributors. The result must say which PR was rejected and why, or the first
  re-evaluation request will be about confusion rather than disagreement.
- **Rejected links are permanent until withdrawal or a rubric sweep.** A contributor who
  disagrees cannot simply resubmit — which is the point, and it makes the dispute flow the
  only route. If that queue is slow, this is where the pain lands.
- **Two user-level scores means two leaderboards to keep consistent.** They are computed in
  the same transaction from the same ordered list, so they cannot disagree — but any future
  code path that writes one must write both.
- **The contract is larger.** Five dimensions × score + remark, per skill, per PR. More
  output tokens per claim, and `max_tokens` must account for it on top of adaptive thinking.

## Amendments

| Date       | Change                                                                                                                                                                            |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-08-14 | Accepted from RFC-0007. Amends ADR-0002, 0003, 0004, 0005.                                                                                                                        |
| 2026-08-15 | **Amended by [ADR-0008](ADR-0008-discovery-http-surface.md)** — `min_generalist_score` gains its parameter shape and `ranked_by: generalist`; adds `GET /me/reevaluation-status`. |
