# Disqualification — when a PR scores zero

## The problem

`Agent.md` is explicit: _"if it is a typo fix then 0 score for that PR."_ The weighted
formula cannot produce that.

Work it through. A typo fix in a large, popular repository scores roughly:

```
substance 2 · complexity 1 · conversation_quality 0 · craft 10 · specificity 1
Q = 0.30(2) + 0.25(1) + 0.20(0) + 0.15(10) + 0.10(1) = 2.45

PR_score = 100·[0.70·(2.45/100) + 0.20·R + 0.10·E]
         = 1.7 + 20·R + 10·E
```

With `R ≈ 0.8` for a well-known repository, that is **≈ 18 before engagement**. The
arithmetic terms are properties of _the repository_, not the change, so they float every PR
in a popular project off the floor. A one-character fix to a README in Kubernetes would
score higher than a genuine, careful bug fix in a 200-star library.

Lowering the dimension scores cannot fix this — they are already near zero. The floor comes
from `R`, and `R` is doing its job correctly for real contributions.

## Two mechanisms

**1 · Named grounds.** The model returns a disqualification verdict for cases it can
recognise (below). Deliberate, explicable, and the reason is shown to the contributor.

**2 · The quality floor.** If the **mean of the five dimension scores is below 5**, the pair
scores 0 regardless of the verdict. This is the backstop for everything the named grounds
miss — and there will be cases they miss, which is fine for v1. A meaningless contribution
to a large repository must score 0 no matter how large the repository is.

```
mean(substance, complexity, conversation_quality, craft, skill_specificity) < 5
    →  PR_score = 0
```

Either mechanism bypasses the arithmetic entirely rather than reducing it.

## Scope: per (PR, skill), never the whole claim

A zeroed pair is dropped. **The other four PRs and the other skills on the same PR are
unaffected.** One weak PR does not reject a claim containing four good ones.

But it does have a consequence for standing:

> **A rejected PR does not count toward the distinct-PR total.**

A claim of five PRs where one floors out gives that skill **four** distinct PRs — secondary,
unranked, until the contributor supplies a fifth that survives. Standing follows surviving
evidence, not submitted evidence. This is why the floor does not need to reject the whole
claim to be a real deterrent: including padding costs the contributor their primary standing.

Disqualification is per (PR, skill) for the same reason. A dependency bump disqualifies for
`go` but may legitimately evidence `security` if the bump was a vulnerability response the
author diagnosed and explained.

## Grounds for disqualification

The model returns one of these reasons, or none. **This list is exhaustive** — anything not
on it is scored low, not disqualified. A rubric that lets the model invent grounds for a
zero is one where nobody can predict what happens.

| Reason                         | Definition                                                                                                                                                                        |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `typo_or_wording`              | Corrects spelling, grammar, or wording, in code comments, docs, or strings, with no behavioural change.                                                                           |
| `formatting_only`              | Whitespace, import ordering, linter-driven reformatting. No semantic change.                                                                                                      |
| `generated_output`             | The diff is machine-generated — lockfiles, protobuf output, vendored dependencies, snapshots. The author ran a tool.                                                              |
| `mechanical_dependency_bump`   | A version number changed with no accompanying adaptation, diagnosis, or explanation.                                                                                              |
| `revert_only`                  | Reverts an earlier change with no new reasoning. Reverting is often correct and rarely evidences skill.                                                                           |
| `not_the_claimed_skill`        | The change is real but does not exercise this skill at all. `skill_specificity` would be scoring 0, not 5.                                                                        |
| `authored_by_other`            | The claimant is not meaningfully the author — the diff is someone else's work merged under their name.                                                                            |
| `unrelated_to_issue`           | The PR claims to address an issue and does something else, or nothing the issue asked for.                                                                                        |
| `maintainer_flagged_unrelated` | A maintainer stated in the thread that the change was unrelated, unwanted, or merged for a reason other than its merit. The people who own the project are the authority on this. |

### Why "AI-generated" is not a ground

It was proposed and **removed**. How a change was produced is not a ground for disqualifying
it.

A merged pull request in a repository the author does not control has already passed the only
gate that matters: **someone else accepted it.** If a maintainer of a substantial project
reviewed a meaningful change and merged it, the change is real work regardless of what tools
produced the first draft. That is the same argument the entire product rests on — merged PRs
are expensive to fake precisely because acceptance is not in the contributor's gift.

Trying to detect provenance would also be bad at its job. The signals — plausible-looking
code, a description that does not match the diff, an author who cannot explain their own
change — are all things the existing dimensions already measure, and they are equally present
in careless human work. `craft` catches code that does not fit the codebase.
`conversation_quality` catches an author who cannot defend their decisions. The quality floor
catches output too thin to mean anything.

So we score the change, not its authorship. `generated_output` remains a ground for a
genuinely different case: a diff that is _entirely_ machine output with no hand-written part,
like a regenerated lockfile — where the author ran a tool and committed the result.

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
The ground is for _corrections_, not for authorship. A PR rewriting a confusing README
section into something accurate is real work; fixing "recieve" to "receive" is not.

**A dependency bump with diagnosis is not mechanical.** If the author identified why the
upgrade was needed, adapted call sites to a changed API, or explained a CVE in the
description, that is the work — the version number is incidental. `mechanical_` is the
operative word.

**Generated output accompanying real changes does not disqualify the PR.** Judge the
hand-written part. A change that regenerates protobuf bindings _and_ implements the service
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

| Where               | Change                                                                                                                                                                                                                                                    |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ADR-0004**        | Structured-output schema gains `disqualified` and `disqualification_reason`. The compute step short-circuits `PR_score` to 0 when set.                                                                                                                    |
| **ADR-0005**        | `PRScore()` takes the verdict and returns 0 without evaluating the weighted sum.                                                                                                                                                                          |
| **RFC-0004 schema** | `pr_skill_scores` cannot store the row at all — its `CHECK (score > 0)` forbids zero, and zero-scoring skills are dropped by design. The **reason** should be recorded on `claim_skills.rejection_reason`, which already exists, using these enum values. |

That last row matters: the schema needs **no new column** for the reason, because the
existing "zero is dropped, reason recorded on the claim skill" design already has the right
shape. `rejection_reason` becomes a **Postgres enum** carrying these ten values, so a
rejection reason is a value rather than a string a prompt happened to produce.

One column _is_ added: `user_skill_pr_links.status` (`pending` | `scored` | `rejected`), so
that standing counts surviving evidence only (see Scope above).

## Where the judgement lives

**In the model, not in a Go pre-filter.** Settled.

A pre-filter — refusing a PR whose diff is only whitespace before spending a model call —
would be cheaper, and it is the wrong trade for the reason a trivial-PR pre-filter was
rejected in RFC-0003: a small or odd-looking diff may still be valuable, and splitting the
judgement between validation code and the rubric puts two authorities on the same question.

The model already reads the diff. It can tell a typo from a fix, a mechanical version bump
from a security response, and generated slop from a tool used well. A whitespace check
cannot do any of those.
