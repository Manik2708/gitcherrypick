# Judging `relative_share` for secondary skills

A claim nominates one primary skill and may declare others. The model scores the nominated
primary on the full dimension set, then returns a **`relative_share`** for each secondary
skill: what proportion of the primary's score that skill earned on this PR.

```
PR_score(secondary) = relative_share · PR_score(nominated primary)
```

## The judgement

Two questions, weighed together:

**How much of the change is this skill?** Roughly what proportion of the work — not the
lines — belongs to it.

**How much did that part matter?** A small amount of decisive work outweighs a large amount
of routine work.

| `relative_share` | Archetype                                                                                                                                                                                                                      |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **≥ 1.0**        | The secondary skill carried the change. The primary was the vehicle; this was the substance. A Go PR whose difficulty was entirely the distributed-consensus reasoning scores ≥ 1.0 for that skill and may exceed the primary. |
| **0.6 – 0.9**    | Co-equal. Both skills were genuinely exercised and neither is clearly subordinate.                                                                                                                                             |
| **0.3 – 0.5**    | A real but secondary component. Present, exercised, not the point of the change.                                                                                                                                               |
| **0.1 – 0.2**    | Marginal. Touched in service of the main work.                                                                                                                                                                                 |
| **Disqualify**   | Not exercised at all. Use `not_the_claimed_skill` — do not return 0.05.                                                                                                                                                        |

## The share is uncapped

`relative_share` may exceed 1.0. The contributor's nomination of which skill is primary is
their opinion about their own work, and the evidence is allowed to disagree.

This has a consequence the model must not try to avoid: **a secondary skill can outscore the
primary.** That is a correct outcome, not an error to be smoothed over. Do not compress
shares toward 1.0 to keep the ordering tidy.

## The 30% rule, worked

The rule of thumb from the original design: _"if it constitutes 30% of the diff and has
nearly the same impact, award 30% of the primary; if impact and complexity are very high,
award up to 100% or more."_

| Change                                                                                                                         | Primary | Secondary    | Share          | Why                                                       |
| ------------------------------------------------------------------------------------------------------------------------------ | ------- | ------------ | -------------- | --------------------------------------------------------- |
| Go service gains a Kubernetes operator. Most of the diff is Go plumbing; the operator's reconciliation logic is the hard part. | `go`    | `kubernetes` | **1.1**        | Small share of the diff, all of the difficulty.           |
| Adds a caching layer in Go backed by Redis. Redis usage is idiomatic and correct but standard.                                 | `go`    | `redis`      | **0.4**        | Real, competent, not where the work was.                  |
| Rewrites a query for performance; the SQL is the change, the Go around it is a signature.                                      | `go`    | `postgres`   | **1.3**        | The primary nomination was simply wrong.                  |
| Adds a feature and updates the Dockerfile to install one package.                                                              | `go`    | `docker`     | **disqualify** | One line of unavoidable plumbing. Not evidence of Docker. |

## Rules

**Judge the share, not the skill in isolation.** The question is not "how good is their
Kubernetes?" but "how much of _this change_ was Kubernetes, and how much did it matter?"
Scoring the skill independently would ignore the evidence in front of you.

**Do not let shares sum to 1.0.** They are independent ratios, not a division of a fixed
budget. Three skills can each score 0.8 if the change genuinely exercised all three.

**Disqualify rather than score low.** Below roughly 0.1 the honest answer is that the skill
was not exercised. Returning 0.05 stores a near-zero score for a skill nobody demonstrated,
and the whole point of the zero rule is that we do not do that.

**Declared and suggested skills are judged identically.** A skill the model itself proposed
gets no benefit of the doubt over one the contributor declared.

## The share is the only thing returned for a secondary

Independently scoring each secondary skill on the full dimension set was considered and
**rejected**. The share is enough, and it is linear:

```
PR_score(secondary) = relative_share · PR_score(nominated primary)
```

Worked: a claim nominates `go`, the model scores that PR at **60**, and returns
`relative_share = 0.3` for `kubernetes`. That PR contributes **18** to the Kubernetes
evidence. When Kubernetes reaches its fifth distinct scored PR it promotes automatically,
and its skill score is computed from those accumulated per-PR values exactly like any other
skill.

Asking the model to score every secondary on all five dimensions would multiply the
judgement work per claim while producing a number the share already implies. It would also
invite the two to disagree — a skill scored 70 independently but shared at 0.3 of a 60-point
primary has two contradictory answers and no rule for choosing.
