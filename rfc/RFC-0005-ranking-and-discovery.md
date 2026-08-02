# RFC-0005 — Scoring formulas, ranking & discovery

**Status:** Draft (revision 2 — review resolved) · **Schema:** [RFC-0005-ranking-and-discovery.schema](RFC-0005-ranking-and-discovery.schema)
· **Depends on:** RFC-0002, RFC-0003, RFC-0004

## Summary

The three scores, in full: every parameter, how each is normalised, and the arithmetic that
turns them into a number. Then ranking, discovery, and the shortlist lifecycle.

Every constant here is a **rubric parameter**, stored in versioned rubric config, not a
code literal. Changing one is a rubric version bump with the re-evaluation sweep that
implies (RFC-0004). The evaluation planner owns the values; this RFC owns the shape.

## Two kinds of signal

Every input is either **arithmetic** — a number GitHub gives us, reproducible and
verifiable — or **judged** — something only a reader of the diff and its conversation can
assess.

| Arithmetic — scored directly | Judged by the model |
|---|---|
| Repository stars, forks, contributor count | Contribution substance |
| Dependents count, package downloads | Complexity |
| Review count, review comment count, participants | Conversation quality |
| Maintainer status (declared, model-validated) | Craft (tests, docs, clarity) |
| | Skill specificity |

**Diff statistics are context, not a scored signal.** Additions, deletions, and files
changed are cached and passed to the model as facts when it judges substance and
complexity — but they feed no arithmetic term of their own. A 4,000-line generated-file
change is not four thousand lines of engineering, and any weight on raw diff size would say
it was.

The split matters because they fail differently. Arithmetic signals are gameable but
verifiable; judged signals are hard to game but vary with the model. Keeping them separate
means a rubric change can reweight one without disturbing the other, and the arithmetic
half can be recomputed without spending a single model call.

## Normalising arithmetic signals

Raw counts are useless directly — 40,000 stars and 400 stars are not "100× the
significance". Each arithmetic metric is normalised to `[0,1]` against **platform-wide
maxima maintained in the database**.

For metric *m* with raw value *v*, and running maximum *Mₘ* across everything the platform
has ever seen:

```
log_norm(v)  = ln(1 + v) / ln(1 + Mₘ)
pct_norm(v)  = fraction of observed values for m that are ≤ v
norm(v)      = β · log_norm(v) + (1 − β) · pct_norm(v)          β = 0.5
```

**Why both.** A pure ratio `v / Mₘ` collapses under outliers: once one contributor lands a
PR in a 190,000-star repository, a genuinely significant 2,000-star project scores
`2000/190000 = 0.01` — indistinguishable from nothing. The log term fixes the scale
(`ln(2001)/ln(190001) = 0.63`) but still moves whenever the maximum moves. The percentile
term is immune to outliers entirely but says nothing about absolute magnitude. Blending
them keeps a sense of real size while refusing to let one Linux contributor flatten
everybody else.

### Maxima drift, and scores drift with them

When someone lands a PR in a bigger repository than any seen before, *Mₘ* updates and every
contributor's relative standing shifts slightly. This is intended: the arithmetic half is a
statement about standing relative to the platform, and standing is relative by definition.

The **arithmetic recompute** recalculates only the normalised values and the scores derived
from them. It does not re-read GitHub, does not call the model, and does not create an
evaluation. Absolute cached facts — the 2,000 stars — never change; only their normalised
form does. Every recompute writes a score snapshot with the reason recorded, so a
contributor who sees their score move can find out why rather than concluding it is
arbitrary.

**It runs nightly, and exits immediately if no maximum moved since the last pass.**

Event-driven recompute — firing the moment a record breaks — sounds tighter and is worse
here. A new maximum changes the *divisor*, so every score using that metric goes stale at
once; the recompute is full-corpus however it is triggered. And while the platform is
small, nearly every new contributor will break some record, so event-driven means a
full-corpus pass many times a day exactly when there is least capacity to absorb it. A
nightly pass coalesces a day of broken records into one run and bounds the work.

`global_norms.generation` is stamped onto every score as `norms_generation`. It does not
make the recompute smaller — nothing can, since the divisor moved — but it makes the job
**resumable** and makes "which rows are stale" answerable without recomputing them to find
out.

### Score components are stored, not derived

Each `pr_skill_score` persists `quality_q`, `reach_r`, `engagement_e`, and the
`norms_generation` they were computed under; each `user_skill` persists `pr_component` and
`project_component`.

They are recomputable from stored data, so this is redundancy on purpose. The reason is
that the alternative puts the formula in two places — the evaluator that scores, and the
read path that explains a score on the hirer scorecard. Two implementations of one formula
diverge eventually, and the failure mode is silent: a breakdown that does not add up to the
score beside it. Storing means the evaluator is the only thing that ever computes a score.

It also makes drift explicable. "Your Go score moved from 84.2 to 83.8 because the stars
maximum rose" requires knowing what `reach_r` was before, which a derived-on-read design
cannot answer at all.

**Per-metric norms are not stored.** `norm(stars)`, `norm(forks)` and the rest are
reconstructable from the raw `facts` blob plus the norms generation, and storing each would
add a dozen columns per row for debugging depth that is rarely wanted.

### Project reach

Per repository, from the normalised metrics:

```
R = 0.35·norm(stars) + 0.15·norm(forks) + 0.20·norm(contributors)
  + 0.20·norm(dependents) + 0.10·norm(downloads)
```

Missing metrics — no package published, no dependents data — are dropped and the remaining
weights renormalised, so an unpublished repository is not punished for a signal that cannot
exist.

**Maintainer bonus.** If the contributor declared maintainer status for the repository and
the model validated it (RFC-0003):

```
R ← min(1, R · μ)                                              μ = 1.25
```

Maintaining a project is materially more than contributing to it, and it is bounded so it
cannot manufacture reach that is not there.

---

## Score 1 — PR score

Per PR, **per skill**. The same PR scores differently for different skills.

### Quality, Q — judged

Five dimensions, each returned 0–100 by the model:

```
Q = 0.30·substance + 0.25·complexity + 0.20·conversation_quality
  + 0.15·craft + 0.10·skill_specificity
```

`conversation_quality` carries real weight deliberately. A one-line diff with a long,
substantive review thread often represents more engineering than a large mechanical
change, and diff size alone cannot see that.

### Engagement, E — arithmetic

```
E = 0.40·norm(review_comments) + 0.30·norm(reviews)
  + 0.30·norm(participants)
```

### Combining

```
PR_score = 100 · [ w_q·(Q/100) + w_r·R + w_e·E ]

  w_q = 0.70   judged quality
  w_r = 0.20   project reach (of which stars are 0.35 → ~7% of the PR score)
  w_e = 0.10   engagement counts
```

So roughly **70% judgement, 30% arithmetic**. Judged quality dominates because it is what
resists gaming: stars and comment counts can be inflated, an assessment of whether the
change was substantial cannot.

### Secondary skills

The model scores the nominated primary skill on the full dimension set, then returns a
`relative_share` for each secondary skill — how much of the change that skill accounts for
and how much it mattered:

```
PR_score(secondary) = relative_share · PR_score(nominated primary)
```

Roughly 30% of the diff at comparable impact yields `relative_share ≈ 0.30`. Very high
impact and complexity can yield `1.0` or more. **The share is uncapped**: if the Kubernetes
work genuinely outweighed the Go work, the score says so, regardless of which the
contributor nominated.

**Any skill computing to zero is dropped, not stored** (RFC-0003).

---

## Score 2 — Skill score

Per (contributor, skill), from the distinct PRs evidencing it.

```
                      ⎧ mean of the best 5 PR_scores        if n ≥ 5   (primary)
PR_component(n)   =   ⎨
                      ⎩ mean of all n PR_scores · (n/5)     if n < 5   (secondary)

Project_component =   mean R over projects attached to claims declaring this skill
                      (0 if none — projects are optional)

Skill_score = 100 · [ 0.85 · PR_component/100 + 0.15 · Project_component ]
```

**Best five, not most recent five.** A contributor's ranked standing should reflect their
best demonstrated work; penalising them for also submitting weaker evidence would
discourage submitting evidence at all.

**The `n/5` factor is what keeps secondary skills honest.** One PR at 90 yields 18, not 90.
Secondary skills are unranked anyway, but they feed the overall score, and without the
factor a single strong PR would be worth as much as five.

---

## Score 3 — Overall score

Depth first, breadth filling the headroom above it.

Build an ordered list of the contributor's skill scores, descending:

```
primaries    : Skill_score for each primary skill
secondaries  : Skill_score · σ  for each secondary skill        σ = 0.5
pr-review    : Skill_score · ω  (see below)                     ω = 1.2, capped at 100

v₁ ≥ v₂ ≥ … ≥ vₖ   = all of the above, sorted descending
```

Then:

```
        Σ(i=2..k) (vᵢ/100) · d^(i−1)
B   =   ────────────────────────────                d = 0.5,  B ∈ [0,1]
            Σ(i=2..k) d^(i−1)

Overall = v₁ + (100 − v₁) · γ · B                   γ = 0.5
```

`v₁` is the floor: your best skill is the score you are guaranteed. Everything else can
only fill the space above it, and only half of that space (`γ`), with each additional skill
counting half as much as the one before (`d`).

**Worked examples:**

| Skills | v₁ | B | Overall |
|---|---|---|---|
| One primary at 90 | 90 | — | **90.0** |
| 90, 60 | 90 | 0.60 | **93.0** |
| 90, 60, 60 | 90 | 0.60 | **93.0** |
| 90, 88 | 90 | 0.88 | **94.4** |
| 60, 60, 60 | 60 | 0.60 | **72.0** |

**Why not a sum, a mean, or a max.** Summing makes the leaderboard a count of claims. A
mean punishes range — adding a genuine second skill at 60 drags a 95 down, so the rational
move is to claim exactly one thing. A max ignores breadth and cannot tell a specialist from
a strong generalist. This form rewards depth first and pays for breadth out of remaining
headroom, so a second skill always helps and never hurts.

**A contributor with no primary skill has no overall score** (RFC-0001), not a zero.

### The PR Review skill

`pr-review` is **claimed like any other skill** (RFC-0003) — the contributor submits review
threads on other people's pull requests, 1–5 of them, primary at 5. It differs from every
other skill in exactly one way, and that difference is in the scoring:

```
PR_score(pr-review) = 100 · [ 0.80·(Q/100) + 0.20·R ]        no E
Skill_score(pr-review) = PR_component                        no project component
```

**Project reach still counts.** Reviewing in a large, complex, widely-depended-on codebase
is harder and worth more than reviewing in a toy repository, and `R` is exactly the measure
of that. It keeps its 0.20 weight.

**PR-specific arithmetic does not.** Additions, deletions, files changed, and the engagement
counts `E` all describe *the author's* pull request, not the reviewer's work on it. A
reviewer who leaves one precise comment on a 4,000-line change has not done 4,000 lines of
review, and a thread with forty comments may just be two people disagreeing. Counting them
would measure the wrong person.

So `E`'s 0.10 weight moves to `Q`: the model reads the full conversation and judges review
quality directly, which is the only thing that can see a single well-aimed comment beating
fifty "LGTM"s.

This is the only skill with `scoring_mode = 'judged_only'` — meaning *no PR-level
arithmetic*. Every other skill keeps the full 70/20/10 split.

**Zero means rejected**, exactly as for any other skill. And `ω = 1.2` still applies in the
overall score, because a good reviewer raises the quality of everyone else's work, which is
worth more to a project than another contributor. Capped at 100 so the multiplier cannot
manufacture a score above the scale.

---

## Ranking and display

Rankings exist per skill (by `Skill_score`, primary standing only) and globally (by
`Overall`).

**Percentiles are computed internally; rank and raw score are what get displayed.** A
percentile is more statistically honest and much more confusing to read — "you are in the
84th percentile of Go" invites the question of the population it is over. Rank plus score
is legible. The percentile still drives internal filtering and search relevance.

Both filter to the active rubric version, so a partially completed sweep never mixes
incomparable scores into one list.

### Tie-breakers

Applied in order, and **deliberately not published** — publishing them turns them into
optimisation targets, and every one of them is a weaker signal than the score itself:

1. Total merged contributions
2. Repositories contributed to or created
3. GitHub followers
4. Earlier platform join date
5. Genuinely tied — same rank, displayed alphabetically

Contribution counts are poor quality signals, which is exactly why they break ties rather
than contribute to scores.

### Who can see rankings

Nobody public. Per RFC-0002: a contributor sees **only their own** rank and scorecard;
full scorecards and cross-contributor rankings require a **verified hirer** account.

## Hirer search

Filters, all optional and combinable: skill (multiple means AND), minimum skill score,
minimum overall score, availability status, location, and evidence recency. Free-text
search over name, login, and bio uses the trigram index from RFC-0001.

Results include only contributors whose **availability status is set and unexpired**
(RFC-0002). A lapsed status removes them from every result until refreshed.

Hirers see what the contributor has published plus the full scorecard — per-PR rationale,
dimension breakdown, evidence. They never see email without approval, never see a claim in
`draft` or `invalid`, and never see a withdrawn one.

## Shortlists

Shortlists belong to the **organization**, not the individual hirer, so the work survives
someone leaving. Any member may create and edit; only owners may delete.

A shortlist carries a **tentative result date** and an open/closed status. There is no
separate job or posting entity — no public listing, no application flow — but a hirer who
opens a shortlist commits to a date by which candidates will hear something.

### Shortlisting requires the contributor's approval

Adding someone to a shortlist creates a **contact request**, not a disclosure. The
contributor is told which organization shortlisted them and the tentative result date, and
**their email is released only if they accept.**

This is the deliberate inversion of how sourcing tools usually work. Releasing contact
details on the strength of one side's interest is what makes those tools feel extractive to
the people in them, and the entire consent model here (RFC-0002) would be hollow if a
shortlist click bypassed it.

The cost is a funnel that stalls when contributors do not respond. Accepted.

**The request discloses what we know about the hirer**, so the decision is informed: the
organization, the tentative result date, and — where payment capability was never verified
(RFC-0002) — the label saying so:

> *This organization is hiring for the first time and has not verified payment capability.*

A contributor deciding whether to hand over their email is the person carrying the risk of
an unpaid engagement. Withholding that fact to keep the funnel moving would be choosing the
hirer's interest over theirs, at the exact moment the platform is asking them to trust it.

### Overdue shortlists

Nothing is blocked when a tentative date passes — blocking punishes the hirer for a
candidate's silence as readily as for their own neglect.

Instead: an organization where **80% or more of its open shortlists are past their tentative
date** is notified, and flagged to admins. That pattern is a real signal — an organization
opening rounds and never closing them is wasting contributors' time — and it is the
organization, not the individual shortlist, that the pattern belongs to.

## Score history

Every completed evaluation and every arithmetic recompute writes a snapshot, with the
reason. This gives contributors a trend line and gives us the audit trail for "why did my
score change?" — a question that arrives the first time maxima drift or a rubric version
bumps.

## Rate limiting

Search and profile reads are rate-limited per hirer account, but **lightly**, and this is
explicitly **not MVP scope** — at launch volumes the concern is theoretical. The real risk
is a verified hirer scraping the whole index to rebuild the ranking elsewhere, which
becomes worth defending against when the index is worth stealing.

## Resolved review comments

| Marker | Resolution |
|---|---|
| Explain the three scores; write all parameters and formulas | This RFC, in full |
| Split arithmetic vs AI parameters | Two signal classes, ~30/70 weighting, separately recomputable |
| Arithmetic relative to shared numbers in the DB | Platform-wide maxima in `global_norms`, log + percentile blend |
| Periodic recompute of relative numbers | Arithmetic recompute — relative values only, no re-enrichment, no model calls |
| Add a PR Review skill; reviewers matter more | Claimed like any skill, scored 100% by the model with no arithmetic, ω = 1.2, zero means rejected |
| Maintainer status should count | Declared with a source of truth, model-validated, μ = 1.25 on reach |
| Project reach beyond stars | Stars, forks, contributors, dependents, downloads combined |
| Shortlist button, email release, tentative date | Contact request + contributor approval; tentative date on the shortlist |
| Overdue rounds | No blocking; org flagged at ≥80% overdue |
| Q2 percentiles | Computed internally, rank and raw score displayed |
| Q3 tie-breakers | Five-level chain, unpublished |
| Q4 rate limiting | Light, and out of MVP scope |

## Open questions

1. **Constants are guesses.** Every weight here is a reasoned starting point, not a measured
   one — particularly `d`, `γ`, and `w_q`. Validating them needs a set of claims a human has
   independently ranked, which cannot exist before there are claims. **Owned by the
   Evaluation Planner** (stage 2), together with the labelled set itself.

   The `pr-review` scope question that sat here is gone: the skill is claimed rather than
   derived (RFC-0003), so the contributor chooses the evidence and there is no review
   history to fetch, bound, or sample.

### Decided without escalation

- **Percentiles are bucketed, not exact.** Exact percentile ranks over the whole corpus
  cost a full scan per query. `global_norms.quantiles` holds sampled boundaries — p10, p25,
  p50, p75, p90, p99 — refreshed by the same nightly job, and `pct_norm(v)` interpolates
  between them. The blend already halves the weight of this term, and no product decision
  turns on the difference between the 73rd and 74th percentile.

## Schema

Introduces `global_norms`, `metric_observations`, `discovery_preferences` (folded into
RFC-0002's availability), `score_snapshots`, `shortlists`, `shortlist_entries`,
`contact_requests`, and `saved_searches`.
