# Rubric v1 — constants

Every number the scoring code uses. This file is the **source of truth**; the embedded YAML
at `backend/internal/scoring/rubric/v1.yaml` (ADR-0006) is generated to match it, and the
two disagreeing is a bug in the YAML.

Changing any value here is a **rubric version bump** — a new file, a re-evaluation sweep
(ADR-0004), and no comparison with prior scores.

## Normalisation

| Constant | Value | Meaning |
|---|---|---|
| `beta` | `0.5` | Log/percentile blend. `norm(v) = β·ln(1+v)/ln(1+Mₘ) + (1−β)·pct(v)` |
| `quantile_boundaries` | `p10, p25, p50, p75, p90, p99` | Sampled boundaries; `pct()` interpolates between them |

## Project reach — `R`

| Metric | Weight |
|---|---|
| `stars` | 0.35 |
| `forks` | 0.15 |
| `contributors` | 0.20 |
| `dependents` | 0.20 |
| `downloads` | 0.10 |

| Constant | Value | Meaning |
|---|---|---|
| `maintainer_multiplier` (μ) | `1.25` | Applied when a declared maintainer status is model-validated. `R = min(1, R·μ)` |

Missing metrics are dropped and the remaining weights renormalised, so an unpublished
repository is not punished for a signal that cannot exist for it.

## Authoring dimensions — `Q`

| Dimension | Weight |
|---|---|
| `substance` | 0.30 |
| `complexity` | 0.25 |
| `conversation_quality` | 0.20 |
| `craft` | 0.15 |
| `skill_specificity` | 0.10 |

Definitions: [dimensions.md](dimensions.md).

## Review dimensions — `Q` for `pr-review`

| Dimension | Weight |
|---|---|
| `insight` | 0.30 |
| `judgement` | 0.25 |
| `communication` | 0.20 |
| `rigour` | 0.15 |
| `outcome` | 0.10 |

Definitions: [pr-review.md](pr-review.md).

## Engagement — `E`

| Metric | Weight |
|---|---|
| `review_comments` | 0.40 |
| `reviews` | 0.30 |
| `participants` | 0.30 |

Not used for `pr-review` (`judged_only`).

## PR score

| Scoring mode | Judged `w_q` | Reach `w_r` | Engagement `w_e` |
|---|---|---|---|
| `standard` | 0.70 | 0.20 | 0.10 |
| `judged_only` | 0.80 | 0.20 | — |

## Skill score

| Constant | Value | Meaning |
|---|---|---|
| `pr_component_weight` | `0.85` | Share of the skill score from PR evidence |
| `project_component_weight` | `0.15` | Share from project evidence (0 for `judged_only`) |
| `primary_threshold` | `5` | Distinct PRs required for primary standing |
| `best_n` | `5` | Number of best PR scores averaged when `n ≥ 5` |

Below the threshold, `PR_component = mean(all n) · (n/5)`.

## Overall score

| Constant | Value | Meaning |
|---|---|---|
| `breadth_decay` (d) | `0.5` | Each additional skill counts half the previous one |
| `breadth_cap` (γ) | `0.5` | Breadth may fill at most half the headroom above the best skill |
| `secondary_discount` (σ) | `0.5` | Applied to secondary skill scores before ordering |
| `review_multiplier` (ω) | `1.2` | Applied to `pr-review`, capped at 100 |

```
Overall = v₁ + (100 − v₁) · γ · B
B       = Σ(i≥2)(vᵢ/100)·d^(i−1) / Σ(i≥2) d^(i−1)
```

No overall score without at least one primary skill — `NULL`, not zero.

## Model suggestions

| Constant | Value | Meaning |
|---|---|---|
| `max_suggested_skills` | `3` | Enforced in the prompt, not by truncation |

## Disqualification

Grounds are an exhaustive enum, not a scale: `typo_or_wording`, `formatting_only`,
`generated_output`, `mechanical_dependency_bump`, `revert_only`, `not_the_claimed_skill`,
`authored_by_other`. See [disqualification.md](disqualification.md).

**Requires an ADR amendment before it can be implemented.**

## Confidence in these numbers

Low, and stated plainly so nobody mistakes precision for accuracy. Every value above is a
reasoned starting point. Nothing here has been validated against a human ranking, and the
ones most likely to be wrong are:

| Constant | Why it is suspect |
|---|---|
| `w_q = 0.70` | The judged/arithmetic balance is the central design bet and is entirely untested. |
| `d = 0.5` | Halving per skill is a guess. It may punish genuine generalists too hard. |
| `γ = 0.5` | Caps a strong generalist well below a narrow specialist. Possibly correct, possibly not. |
| `substance = 0.30` vs `complexity = 0.25` | These two overlap in practice and may be measuring one thing twice. |
| `ω = 1.2` | "Reviewers matter more" is a stated value, not a measured effect. |

See [calibration.md](calibration.md).
