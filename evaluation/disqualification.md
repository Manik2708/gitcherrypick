# Disqualification — when a PR scores zero

> ⚠️ **This file requires an amendment to ADR-0004 and ADR-0005 before it can work.**
> The change is specified at the bottom and needs owner approval. Everything else in this
> directory is implementable as-is; this is not.

## The problem

`Agent.md` is explicit: *"if it is a typo fix then 0 score for that PR."* The weighted
formula cannot produce that.

Work it through. A typo fix in a large, popular repository scores roughly:

```
substance 2 · complexity 1 · conversation_quality 5 · craft 10 · specificity 1
Q = 0.30(2) + 0.25(1) + 0.20(5) + 0.15(10) + 0.10(1) = 3.45

PR_score = 100·[0.70·(3.45/100) + 0.20·R + 0.10·E]
         = 2.4 + 20·R + 10·E
```

With `R ≈ 0.8` for a well-known repository, that is **≈ 18 before engagement**. The
arithmetic terms are properties of *the repository*, not the change, so they float every PR
in a popular project off the floor. A one-character fix to a README in Kubernetes would
score higher than a genuine, careful bug fix in a 200-star library.

Lowering the dimension scores cannot fix this — they are already near zero. The floor comes
from `R`, and `R` is doing its job correctly for real contributions.

## The mechanism

The model returns a **disqualification verdict** alongside the dimension scores. When set,
`PR_score = 0` for that skill regardless of dimensions, reach, and engagement — the
arithmetic is bypassed entirely, not merely reduced.

A disqualified skill scores zero, and **zero means the skill is dropped, never stored**
(ADR-0003). If every skill on a claim disqualifies, the claim is evaluated and stores
nothing but the reason.

Disqualification is **per (PR, skill)**, not per PR. A dependency bump disqualifies for `go`
but may legitimately evidence `security` if the bump was a vulnerability response the author
diagnosed and explained.

## Grounds for disqualification

The model returns one of these reasons, or none. **This list is exhaustive** — anything not
on it is scored low, not disqualified. A rubric that lets the model invent grounds for a
zero is one where nobody can predict what happens.

| Reason | Definition |
|---|---|
| `typo_or_wording` | Corrects spelling, grammar, or wording, in code comments, docs, or strings, with no behavioural change. |
| `formatting_only` | Whitespace, import ordering, linter-driven reformatting. No semantic change. |
| `generated_output` | The diff is machine-generated — lockfiles, protobuf output, vendored dependencies, snapshots. The author ran a tool. |
| `mechanical_dependency_bump` | A version number changed with no accompanying adaptation, diagnosis, or explanation. |
| `revert_only` | Reverts an earlier change with no new reasoning. Reverting is often correct and rarely evidences skill. |
| `not_the_claimed_skill` | The change is real but does not exercise this skill at all. `skill_specificity` would be scoring 0, not 5. |
| `authored_by_other` | The claimant is not meaningfully the author — the diff is someone else's work merged under their name. |

### Deliberately not grounds

- **Small diffs.** A one-line fix can be the hardest change in a release. Size is not a
  ground and never will be.
- **No tests.** Scored in `craft`, and often the project's convention rather than the
  author's choice.
- **No review conversation.** Scored in `conversation_quality`, and often says more about
  the project than the author.
- **An unpopular repository.** Reach is already scored arithmetically. Disqualifying on it
  would punish the same fact twice.
- **Old PRs.** Scores never decay (ADR-0001).

### The judgement calls

Three of these need explicit instruction, because the naive reading catches real work:

**Documentation is not automatically `typo_or_wording`.** Writing a design document,
an architecture guide, or genuine API documentation is engineering work and scores normally.
The ground is for *corrections*, not for authorship. A PR rewriting a confusing README
section into something accurate is real work; fixing "recieve" to "receive" is not.

**A dependency bump with diagnosis is not mechanical.** If the author identified why the
upgrade was needed, adapted call sites to a changed API, or explained a CVE in the
description, that is the work — the version number is incidental. `mechanical_` is the
operative word.

**Generated output accompanying real changes does not disqualify the PR.** Judge the
hand-written part. A change that regenerates protobuf bindings *and* implements the service
method is a real change with generated files in it.

## Required contract change

The judgement schema in ADR-0004 currently returns dimension scores, `relative_share`, and a
rationale. It needs one more field per (PR, skill):

```json
{
  "disqualified": true,
  "disqualification_reason": "typo_or_wording",
  "rationale": "..."
}
```

Three changes follow:

| Where | Change |
|---|---|
| **ADR-0004** | Structured-output schema gains `disqualified` and `disqualification_reason`. The compute step short-circuits `PR_score` to 0 when set. |
| **ADR-0005** | `PRScore()` takes the verdict and returns 0 without evaluating the weighted sum. |
| **RFC-0004 schema** | `pr_skill_scores` cannot store the row at all — its `CHECK (score > 0)` forbids zero, and zero-scoring skills are dropped by design. The **reason** should be recorded on `claim_skills.rejection_reason`, which already exists, using these enum values. |

That last row matters: the schema needs **no new column**, because the existing
"zero is dropped, reason recorded on the claim skill" design already has the right shape.
The only change is that `rejection_reason` gains a defined vocabulary instead of free text.

**Recommended:** promote the seven reasons to a Postgres enum, so a rejection reason is a
value rather than a string a prompt happened to produce.

## Open question for the owner

Is the model the right place for this judgement at all? The alternative is a pre-filter in
Go — refuse a PR whose diff is only whitespace before spending a model call.

I recommend **against** it, for the reason you gave when rejecting a trivial-PR pre-filter
in RFC-0003: a small or odd-looking diff may still be valuable, and splitting the judgement
between validation code and the rubric puts two authorities on the same question. The model
already reads the diff; it can tell a typo from a fix, and it can tell a mechanical version
bump from a security response, which a whitespace check cannot.
