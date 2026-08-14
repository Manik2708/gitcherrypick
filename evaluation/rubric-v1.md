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

**Stars, forks and contributors always exist. Dependents and downloads may not** — a web
server, a CLI, or an application has neither. Generic renormalisation would silently change
what the score means depending on the kind of project, so the four cases carry **explicit
weight sets**:

| Case | stars | forks | contributors | dependents | downloads |
|---|---:|---:|---:|---:|---:|
| **1 · All five** — published library with a dependency graph | 0.30 | 0.10 | 0.15 | 0.25 | 0.20 |
| **2 · No downloads** — depended on, not distributed as a package | 0.35 | 0.15 | 0.20 | 0.30 | — |
| **3 · No dependents** — published, nothing public depends on it yet | 0.35 | 0.15 | 0.20 | — | 0.30 |
| **4 · Neither** — application, service, CLI, infrastructure | **0.45** | **0.20** | **0.35** | — | — |

Case 4 is the common one and deserves the most thought. With only three signals available,
`contributors` is weighted up sharply: for an application, the number of people who have
successfully landed changes says far more about real scale and complexity than a star count,
which mostly measures visibility.

**Which case applies is decided by data availability, not by project type.** If a dependents
count is retrievable it is used, whatever the repository calls itself.

| Constant | Value | Meaning |
|---|---|---|
| `maintainer_multiplier` (μ) | `1.25` | Applied when a declared maintainer status is model-validated. `R = min(1, R·μ)` |

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
| `pr_component_weight` | `0.70` | Share of the skill score from PR evidence |
| `project_component_weight` | `0.30` | Share from project evidence (0 for `judged_only`) |
| `primary_threshold` | `5` | Distinct **scored** PRs required for primary standing |
| `best_n` | `5` | Number of best PR scores averaged when `n ≥ 5` |
| `quality_floor` | `5` | Mean dimension score below this zeroes the pair |

Below the threshold, `PR_component = mean(all n) · (n/5)`.

The project component is **0.30, raised from 0.15**, so that contributing to a large complex
project is clearly distinguished from contributing to a basic one. That is a deliberate
shift of weight from *how well you did it* toward *where you did it* — the trade is that a
strong contribution to a small project now scores meaningfully below the same contribution
to a major one.

Promotion is **automatic** at 5 scored PRs. No score threshold. A PR zeroed by
disqualification or the quality floor does not count toward the total (see
[disqualification.md](disqualification.md)).

## The two headline scores

Both draw on the same ordered list: primary skill scores at full weight, secondary skill
scores pre-scaled by `σ`, `pr-review` by `ω`, sorted descending as `v₁ ≥ v₂ ≥ … ≥ vₖ`.

| Constant | Value | Meaning |
|---|---|---|
| `breadth_decay` (d) | `0.5` | Each additional skill counts half the previous one |
| `breadth_cap` (γ) | `0.5` | Breadth may fill at most half the headroom above the best skill |
| `secondary_discount` (σ) | `0.5` | Flat pre-scaling on secondary skill scores |
| `review_multiplier` (ω) | `1.2` | Applied to `pr-review`, capped at 100 |

### Overall score — depth, bounded 0–100

```
Overall = v₁ + (100 − v₁) · γ · B
B       = Σ(i≥2)(vᵢ/100)·d^(i−1) / Σ(i≥2) d^(i−1)
```

Your best skill is a floor you are guaranteed; breadth fills at most half the headroom above
it. **`γ` is retained deliberately.** It was briefly removed to give breadth more room, and
reinstated once the Generalist score existed — with a dedicated breadth metric alongside,
Overall no longer has to carry both jobs, and it is more useful as a clean depth measure.

Removing `γ` also flipped a ranking: a 90-plus-75 contributor overtook a single 95. With `γ`
in place the specialist stays ahead on Overall and the two-skill contributor wins on
Generalist, which is the correct division of labour between the two numbers.

### Generalist score — breadth, unbounded

```
Generalist = v₁ + Σ(i≥2) vᵢ / √i
```

Sub-linear so that count alone cannot dominate — the ninth skill is worth about a third of
the second — but unbounded, so genuinely broad contributors separate from each other rather
than compressing near a ceiling.

**Recruiters can search on it.** That is the point: a recruiter who needs a generalist
filters on this number and finds people the depth-ranked board would bury.

| Profile | Overall | Generalist |
|---|---:|---:|
| 1 primary @ 95 | **95.0** | 95 |
| 2 primaries @ 90, 75 | 93.8 | 143 |
| 4 primaries @ 80, 70, 65, 60 | 86.7 | 197 |
| 8 primaries @ 55 | 67.4 | **240** |

The two columns rank these profiles in **opposite orders**, which is the point — it is the
evidence that they measure different things rather than one being a noisier version of the
other. A specialist tops Overall and sits bottom on Generalist; the eight-skill contributor
does the reverse. Both are findable, neither is penalised for being what they are.

Maximum reachable Overall for a given best skill, since `B ≤ v₁/100`:

```
max Overall = v₁ + v₁·(100 − v₁)/200
```

50 → 62.5 · 80 → 88.0 · 95 → 97.4. No amount of breadth lets a mediocre specialist reach the
top of the depth board; breadth amplifies depth rather than substituting for it.

**Both require at least one primary skill.** Without one there is no Overall and no
Generalist score — `NULL`, not zero. Breadth built entirely from secondary skills is
unproven breadth, and letting it into recruiter search would be a way around the five-PR bar.

## Model suggestions

| Constant | Value | Meaning |
|---|---|---|
| `max_suggested_skills` | `3` | Enforced in the prompt, not by truncation |

## Disqualification

Grounds are an exhaustive enum, not a scale: `typo_or_wording`, `formatting_only`,
`generated_output`, `mechanical_dependency_bump`, `revert_only`, `not_the_claimed_skill`,
`authored_by_other`, `unrelated_to_issue`, `maintainer_flagged_unrelated`.

**"AI-generated" is not a ground.** A merged PR in a repository the author does not control
has already passed the gate that matters — someone else accepted it. See
[disqualification.md](disqualification.md).

Plus the **quality floor**: mean dimension score below `5` zeroes the pair regardless of
verdict. See [disqualification.md](disqualification.md).

## Confidence in these numbers

Low, and stated plainly so nobody mistakes precision for accuracy. Every value above is a
reasoned starting point. Nothing here has been validated against a human ranking, and the
ones most likely to be wrong are:

| Constant | Why it is suspect |
|---|---|
| `w_q = 0.70` | The judged/arithmetic balance is the central design bet and is entirely untested. |
| `project_component = 0.30` | Newly doubled. Weights *where* you contributed heavily against *how well*; may now overshoot. |
| `d = 0.5` | Halving per skill is a guess, and it now drives two different scores. |
| `γ = 0.5` | Caps breadth's contribution to Overall. Justifiable now that Generalist exists, but the value is still arbitrary. |
| `√i` in Generalist | The damping curve is reasoned, not derived. `log₂(i+1)` would be flatter and equally defensible. |
| Case-4 reach weights | `contributors 0.35` for applications is reasoned from first principles with nothing behind it. |
| `quality_floor = 5` | Picked as "obviously meaningless". Never tested against a real distribution of dimension means. |
| `substance` vs `complexity` | These two overlap in practice and may be measuring one thing twice. |
| `ω = 1.2` | "Reviewers matter more" is a stated value, not a measured effect. |

See [calibration.md](calibration.md).
