# The `pr-review` skill

`pr-review` is claimed like any other skill (ADR-0003): the contributor submits 1–5 review
threads on **other people's** pull requests. It differs in two ways that matter here.

**The claimant is the reviewer, not the author.** Validation enforces it — a PR they wrote
is rejected with `authored_by_claimant`.

**Scoring is `judged_only`** (ADR-0005): `0.80·Q + 0.20·R`, no engagement term. Comment
counts are excluded deliberately — a single well-aimed comment that changes a design beats
fifty "LGTM"s, and counting would score the opposite.

## Different dimensions

The five dimensions in [dimensions.md](dimensions.md) assess _authoring_ and do not transfer.
`craft` asks whether the author wrote tests; a reviewer wrote nothing. `pr-review` uses its
own five, which the model is given instead.

---

### 1 · `insight` — did the review see something worth seeing?

The core of the skill.

| Score  | Archetype                                                                                                                                                                              |
| ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Identified a defect or design flaw that would have shipped: a race, a security hole, a boundary case, an interface that would age badly. Something a careful reader would have missed. |
| 70–89  | Caught a real problem — a bug, a missing case, an inconsistency with the rest of the codebase.                                                                                         |
| 40–69  | Useful observations that improved the change without altering its direction.                                                                                                           |
| 15–39  | Surface-level: naming, style, minor cleanliness. Correct, low-value.                                                                                                                   |
| 1–14   | Approval with no substantive observation.                                                                                                                                              |

**A short review can score at the top.** "This retries on a non-idempotent endpoint" is one
sentence and may be the most valuable comment on the PR.

---

### 2 · `judgement` — did they weigh what mattered?

Reviewing well is mostly deciding what _not_ to raise. A reviewer who blocks on formatting
while missing a correctness bug has inverted the priority.

| Score  | Archetype                                                                                                               |
| ------ | ----------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Focused precisely on what mattered; explicitly let smaller things go; distinguished blocking concerns from suggestions. |
| 70–89  | Good prioritisation. Substance first, nits marked as nits.                                                              |
| 40–69  | Reasonable, undifferentiated — everything raised at the same weight.                                                    |
| 15–39  | Poorly calibrated: blocking on preference, or waving through something that needed scrutiny.                            |
| 1–14   | No evident prioritisation.                                                                                              |

---

### 3 · `communication` — was it actionable and respectful of the author?

| Score  | Archetype                                                                                                                                                              |
| ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Every comment specific and actionable, with the _why_ included. Disagreement expressed as reasoning, not authority. The author could act without a follow-up question. |
| 70–89  | Clear and specific. Reasoning present where it mattered.                                                                                                               |
| 40–69  | Understandable, sometimes terse or unexplained.                                                                                                                        |
| 15–39  | Vague ("this feels wrong"), or correct but dismissive.                                                                                                                 |
| 1–14   | Unactionable, or hostile.                                                                                                                                              |

**Judge clarity and reasoning, not warmth.** A blunt, well-argued objection scores higher
than a friendly comment with no content. Hostility is penalised because it makes review
harder to act on, not because it is impolite.

---

### 4 · `rigour` — how thoroughly did they engage?

| Score  | Archetype                                                                                                                                                       |
| ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Evidently read the whole change and its context — checked call sites, considered interactions with other components, verified a claim rather than accepting it. |
| 70–89  | Thorough within the diff.                                                                                                                                       |
| 40–69  | Read what was in front of them.                                                                                                                                 |
| 15–39  | Partial — commented on one file of a change spanning several.                                                                                                   |
| 1–14   | Skimmed.                                                                                                                                                        |

**Must ignore:** review length, number of comments, and elapsed time. Rigour is
demonstrated by what the comments show the reviewer understood.

---

### 5 · `outcome` — did the review improve the change?

| Score  | Archetype                                                                                                                                                    |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 90–100 | The change is materially better because of this review. The author adopted it, or a discussion converged on something better than either party started with. |
| 70–89  | The author acted on the substance.                                                                                                                           |
| 40–69  | Some feedback taken; the change is marginally improved.                                                                                                      |
| 15–39  | Little effect — the review happened, the change did not move.                                                                                                |
| 1–14   | No effect, or the reviewer withdrew.                                                                                                                         |

**Do not penalise a reviewer for an author who ignored them.** If a review raised a real
problem and the author merged regardless, that is the _author's_ outcome. Score the review
on whether the feedback deserved to be acted on, and note the divergence in the rationale.

---

## Combining

```
Q(pr-review) = 0.30·insight + 0.25·judgement + 0.20·communication
             + 0.15·rigour + 0.10·outcome

PR_score = 100·[ 0.80·(Q/100) + 0.20·R ]
```

`R` is the reach of the repository the review happened in. Reviewing in a large, complex,
widely-depended-on codebase is harder and carries more responsibility than reviewing in a
toy repository (ADR-0005).

## Disqualification

The grounds in [disqualification.md](disqualification.md) apply as written, with two
adaptations:

- **`typo_or_wording`** covers a review consisting only of spelling or wording corrections.
- **`authored_by_claimant`** — reviewing your own pull request is not review work. Caught in
  validation, but the model must not score it if one slips through.

**An approval with no comment disqualifies.** A bare "LGTM" or an empty approve is not
evidence of review skill. Use `typo_or_wording`'s sibling reasoning: nothing was
demonstrated.

## What this deliberately does not measure

- **How many reviews the contributor has done.** They submit five; the total is not scored.
- **Whether they had merge authority.** Maintainer status is scored via `R` (ADR-0005), not
  here. A thoughtful review from an outside contributor is worth as much.
- **Whether the PR they reviewed was any good.** Reviewing a weak change well is still good
  reviewing.

## Remarks

As with the authoring dimensions, the model returns **a score and a short remark per
dimension** (see [dimensions.md](dimensions.md)), not one rationale for the review as a
whole.
