# RFC-0007 — Rubric contract, disqualification & the generalist score

**Status:** Approved 2026-08-14 · **Binding form:** [ADR-0007](../adr/ADR-0007-rubric-contract-and-generalist-score.md) · **Schema:** [RFC-0007-rubric-contract-and-generalist-score.schema](RFC-0007-rubric-contract-and-generalist-score.schema)
· **Amends:** RFC-0002, RFC-0003, RFC-0004, RFC-0005 (and their ADRs)

## Summary

Writing the evaluation rubric (`evaluation/`) surfaced changes the approved design cannot
express. This RFC collects all of them. Approved documents are not edited — everything here
is a delta, and the sections it does not mention stay in force.

Seven changes, in dependency order:

| # | Change | Amends |
|---|---|---|
| 1 | Disqualification verdict in the judgement contract | RFC-0004 |
| 2 | Quality floor — mean dimension score below 5 zeroes the pair | RFC-0004, RFC-0005 |
| 3 | Rejected PRs excluded from standing | RFC-0003 |
| 4 | Per-dimension remarks, not one rationale | RFC-0004 |
| 5 | **Generalist score** — a second user-level score, searchable | RFC-0002, RFC-0005 |
| 6 | Re-evaluation requests with escalating cooldown | RFC-0005 |
| 7 | Constant changes: project weight, reach cases, conversation zero | RFC-0005 |

---

## 1 · Disqualification

`Agent.md` requires a typo fix to score **0**. The weighted formula cannot produce that: a
typo fix scores `Q ≈ 2.45`, but project reach is a property of the *repository* rather than
the change, so `PR_score ≈ 1.7 + 20·R + 10·E` floors near **18** in a popular repository.
A one-character README fix in Kubernetes would outscore a careful bug fix in a 200-star
library.

The model returns a verdict alongside the dimension scores:

```json
{ "disqualified": true, "disqualification_reason": "typo_or_wording" }
```

When set, `PR_score = 0` for that (PR, skill) pair — the arithmetic is **bypassed**, not
reduced. Nine exhaustive grounds, defined in
[evaluation/disqualification.md](../evaluation/disqualification.md). The model may not invent
one; anything not on the list is scored low instead.

**"AI-generated" is deliberately not among them.** A merged pull request in a repository the
author does not control has already passed the only gate that matters — someone else accepted
it. If a maintainer of a substantial project reviewed a meaningful change and merged it, it is
real work regardless of what produced the first draft. That is the same argument the product
rests on: merged PRs are expensive to fake precisely because acceptance is not in the
contributor's gift.

Detection would also be redundant. Every signal of "slop" — code that does not fit the
codebase, an author who cannot defend their decisions, output too thin to mean anything — is
already measured by `craft`, `conversation_quality`, and the quality floor, and every one of
them is equally present in careless human work. We score the change, not its authorship.

Disqualification is **per (PR, skill)**. A dependency bump disqualifies for `go` and may
still evidence `security` if the author diagnosed a CVE.

**The judgement stays in the model, not a Go pre-filter** — for the reason a trivial-PR
pre-filter was rejected in RFC-0003: a small diff may still be valuable, and splitting the
call between validation code and the rubric puts two authorities on one question.

## 2 · The quality floor

The named grounds will miss cases. The backstop:

```
mean(five dimension scores) < 5   →   PR_score = 0,  reason = below_quality_floor
```

Computed in Go, not returned by the model. A meaningless contribution to a large repository
must score zero no matter how large the repository is.

## 3 · Rejected PRs do not count toward standing

Currently `distinct_pr_count` counts every linked PR. It must count only **surviving**
evidence:

> A claim of five PRs where one is disqualified or floored gives that skill **four** distinct
> PRs — secondary, unranked, until the contributor supplies a fifth that survives.

`user_skill_pr_links` gains a `status` (`pending` → `scored` | `rejected`). Written `pending`
inside the submit transaction, which is what enforces `(user, PR, skill)` uniqueness before a
model call is spent; resolved when the evaluation persists.

**A rejected row is kept, not deleted.** It blocks the same PR being resubmitted for the same
skill in a fresh claim to reroll the verdict — a form of gaming the per-claim 7-day lock
cannot catch. Released on withdrawal; re-opened by a rubric version sweep.

This is also why the quality floor does not need to reject whole claims to deter padding:
including a weak PR costs the contributor their primary standing.

## 4 · Per-dimension remarks

The contract currently returns one rationale per (PR, skill). It returns **a score and a
remark per dimension**:

```json
"substance": { "score": 78, "remark": "Reworked the retry path to be idempotent…" }
```

A single rationale cannot explain a breakdown. A contributor seeing `complexity 85, craft 40`
needs to know what the 40 was about; the hirer scorecard is buying exactly that specificity;
and a re-evaluation request (§6) has to be about something concrete rather than "my score
feels low".

Remarks are read by humans and are **never** an input to arithmetic.

## 5 · The generalist score

RFC-0005 has one user-level score, and it is depth-first by design: `γ` caps breadth at half
the headroom above your best skill, so a genuine generalist ranks below a narrow specialist.
That is correct for a depth measure and wrong as the only measure.

**A second score, computed from the same ordered skill list:**

```
Overall    = v₁ + (100 − v₁) · γ · B          bounded 0–100, depth      (unchanged)
Generalist = v₁ + Σ(i≥2) vᵢ / √i              unbounded, breadth        (new)
```

| Profile | Overall | Generalist |
|---|---:|---:|
| 1 primary @ 95 | **95.0** | 95 |
| 2 primaries @ 90, 75 | 93.8 | 143 |
| 4 primaries @ 80, 70, 65, 60 | 86.7 | 197 |
| 8 primaries @ 55 | 67.4 | **240** |

The two columns rank these profiles in **opposite orders**, which is the evidence they
measure different things. `√i` damping keeps count from dominating outright — the ninth skill
is worth about a third of the second — while staying unbounded so broad contributors separate
from each other instead of compressing near a ceiling.

`γ` is **retained**. It was briefly removed to give breadth more room and reinstated once
this score existed: with a dedicated breadth metric alongside, Overall no longer has to carry
both jobs.

**Recruiters can filter on `min_generalist_score`.** That is the point — a recruiter needing
a generalist finds people the depth board buries.

**Both scores require at least one primary skill.** Breadth built entirely from secondary
skills is unproven breadth, and admitting it to search would be a way around the five-PR bar.

## 6 · Re-evaluation requests

RFC-0005 left calibration blocked: validating the constants needs claims a human has ranked,
and hand-building that set only proves the rubric agrees with its author.

**A dispute flow generates it as a side effect.** A contributor who thinks a score is wrong
requests re-evaluation with a reason. An admin accepts — re-queueing the claim and recording
the disagreement for tuning — or rejects.

Rejections drive an **escalating cooldown**, resetting each time it expires:

| Rejections | Cooldown |
|---|---|
| First 3 | 28 days |
| Next 3 | 56 days |
| Next 3 | 112 days |
| Next 3 | 224 days |
| Thereafter | **365 days (cap)** |

Doubling costs an honest contributor who misjudged once almost nothing while making
systematic disputing progressively pointless. The cap is deliberate: past about a year a
cooldown stops being a deterrent and becomes a ban, and a ban should be an admin decision
with a recorded reason rather than an automatic counter reaching a large number.

**Accepted requests neither reset nor increment the counter.** Only rejections accumulate,
so a contributor who is repeatedly right is never throttled — exactly the population whose
disputes are most valuable.

Admins retain a **global re-evaluation sweep** for prompt or model changes, independent of a
rubric version bump.

This also settles the launch posture: **ship fully and tune with real data**. Withholding
ranking until tuned would leave the one mechanism that generates labelled data dormant,
because nobody disputes a score they cannot see in context.

## 7 · Constant changes

| Constant | Was | Now | Why |
|---|---|---|---|
| `project_component_weight` | 0.15 | **0.30** | Contributing to a large complex project should be clearly distinguished from a basic one |
| `pr_component_weight` | 0.85 | **0.70** | Complement of the above |
| Project reach weights | one set, renormalised | **four explicit cases** | Stars/forks/contributors always exist; dependents and downloads do not. Generic renormalisation silently changes what the score means by project type |
| `conversation_quality` with no discussion | ~40 | **0** | No conversation means no conversational skill demonstrated. Makes the five-PR choice a real trade-off the contributor controls |
| `craft` | tests, docs, durability | **+ readability** | Naming, abstraction level, design-pattern fit. Pure formatting still excluded |
| `quality_floor` | — | **5** | New (§2) |

The four reach cases:

| Case | stars | forks | contributors | dependents | downloads |
|---|---:|---:|---:|---:|---:|
| All five | 0.30 | 0.10 | 0.15 | 0.25 | 0.20 |
| No downloads | 0.35 | 0.15 | 0.20 | 0.30 | — |
| No dependents | 0.35 | 0.15 | 0.20 | — | 0.30 |
| Neither | 0.45 | 0.20 | 0.35 | — | — |

Case 4 is the common one — applications, services, CLIs. `contributors` is weighted up
sharply because for an application the number of people who have landed changes says more
about real scale than a star count, which mostly measures visibility. Which case applies is
decided by **data availability, not project type**.

## Open questions

**None.** All three raised in the draft were resolved in review:

| Question | Resolution |
|---|---|
| `ai_generated_slop` is hard to apply | **Ground removed.** A big project, a meaningful PR, and a merge is enough — provenance does not matter once someone else has accepted the change. See §1. |
| `√i` vs `log₂(i+1)` damping | **`√i`.** Revisit if calibration shows breadth over- or under-weighted. |
| Two scores, two leaderboards, which is "real" | **A false premise.** There were never two. There is a leaderboard per skill, plus overall, plus generalist. Adding one more axis to an already multi-axis ranking is not a competing answer — each board answers a different question, and a recruiter picks the one matching what they need. |

## Schema

Introduces `skill_rejection_reason`, `pr_link_status`, `score_kind`, `reevaluation_status`;
adds `users.generalist_score`, `user_skill_pr_links.status`, `score_snapshots.kind`; and
creates `reevaluation_requests` and `reevaluation_cooldowns`.
