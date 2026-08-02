# RFC-0003 — Claims, evidence & skill standing

**Status:** Draft (revision 2 — review resolved) · **Schema:** [RFC-0003-skill-claims-and-evidence.schema](RFC-0003-skill-claims-and-evidence.schema)
· **Depends on:** RFC-0001, RFC-0002

## Summary

How a contributor submits evidence, what we verify before it counts, and how ranked skill
standing is derived from it. Scoring mechanics are RFC-0004; the formulas are RFC-0005.

## The skill catalogue

Skills are **curated entries, not free text**. A contributor picks `go`, not "Golang
(expert)". Free text would fragment the population across spellings — everyone claiming
"Go" ranked separately from everyone claiming "Golang" — and would let anyone mint a skill
in which they are trivially the top-ranked person.

Contributors may **request a skill that does not exist**. Requests go to an admin review
queue with a dashboard; approval creates the catalogue entry and notifies the requester.

The queue is a free-text channel to humans, so it is bounded on two sides:

- **Rate limit: 3 requests per contributor per week.**
- **Auto-dedupe before a human sees it.** A new request is fuzzy-matched against existing
  skill names and aliases; "Golang" is rejected immediately with a pointer to `go`, and
  never reaches the queue. Most noise is spelling variants, and no admin should spend
  attention on those.

## Claims are evidence bundles

A claim is one submission:

| Component | Cardinality |
|---|---|
| PR evidence | **1 to 5** |
| Project evidence | **0 to 20** (optional) |
| Declared skills | 1 or more, one nominated primary |

The contributor names the skills those PRs demonstrate. **The whole claim is judged in a
single model call**, producing a score for every PR against every skill (RFC-0004).

## Skill standing is derived, not submitted

A skill's standing comes from **how many distinct PRs across all of a contributor's claims
evidence it**:

| PRs | Standing | Ranked? | Contributes to overall score? |
|---|---|:--:|:--:|
| 5+ | **Primary** | Yes | Yes |
| 1–4 | **Secondary** | No | Yes |
| 0 | Not held | — | — |

**Promotion is automatic.** The moment a fifth distinct PR evidences a secondary skill, it
becomes primary and enters ranking. The evidence already exists and has already been
judged, so requiring a fresh submission would only re-spend a model call to reach a
conclusion we already hold.

A contributor may hold **many primary and many secondary skills**, and needs **at least one
primary skill to receive an overall score** at all.

### Why the five-PR rule survives as a threshold

Primary standing is what gets ranked, and ranking demands comparability. Every primary
skill rests on at least five judged PRs, so no ranked score is built on thinner evidence
than another. Secondary skills carry no such guarantee, which is exactly why they are shown
but never ranked.

### Secondary skills are relative to the primary

The model scores a secondary skill as a proportion of the primary skill's score for the
same PR, judged on how much of the change it accounts for and how much it mattered.
Roughly 30% of the diff at similar impact earns roughly 30%; very high impact and
complexity can earn as much as the primary or more. **The proportion is uncapped** — the
contributor's nomination of which skill is primary does not constrain what the evidence
says.

### Zero means rejected, never recorded

Any skill scoring zero is **dropped from the claim**, not stored as a zero. If that
includes the nominated primary skill, the claim still keeps its surviving secondary skills
and the contributor is told the primary did not hold. A stored zero would imply we measured
something; we are saying the evidence does not support the claim.

### AI-suggested skills

While judging, the model may attach skills it observes that the contributor did not
declare. These are stored as **suggestions** — invisible, unscored, excluded from every
total — until the contributor accepts them. On acceptance they become ordinary claim skills
and count toward standing.

**The model is prompted to suggest at most 3 skills.** The cap is in the prompt rather than
enforced afterwards, so the model spends its judgement choosing the best three instead of
producing a long list we then truncate arbitrarily. The contributor's own declared skills
are not capped — they are describing their own work, and constraining that would be
answering a different question.

### Score and standing are orthogonal

Worth stating plainly, because the two are easy to conflate:

> **The score says what the work was worth. The PR count says whether it is rankable.**

A secondary skill can outscore a primary one and still remain secondary. Kubernetes at 91
with three PRs stays unranked; Go at 74 with five PRs is ranked. Nothing promotes a skill
except a fifth distinct PR, and no score is capped to keep the ordering tidy.

The profile therefore shows both, sorted by score, with standing as a separate attribute —
not as a hierarchy where the top skill must be primary.

## Projects are arithmetic-only

Judging a project's worth needs to know it has real users, which nothing GitHub exposes can
prove. So **the model never judges a project.** Projects contribute through hard signals
only — stars, forks, contributor count, dependents, package downloads, and maintainer
status — normalised as RFC-0005 specifies.

Supplying a project is optional. A claim with none simply has no project component.

### Maintainer status is declared, then validated

GitHub does not say who maintains a repository. Rather than guess from a proxy, we **ask
the contributor**, and have the model validate the answer.

The contributor ticks whichever sources of truth apply — listed in CODEOWNERS, merges
others' PRs, named in the README or governance file, org owner — or picks **Other** and
supplies their own evidence. The model checks the claim against the repository's observable
facts and confirms or rejects it. A contributor is the best source for a fact the API does
not expose; the validation stops that from being an honour system.

## Verification

**No evidence is trusted because the user typed it.** Every check runs against the GitHub
API, keyed on the identity verified at login (RFC-0002).

Per pull request:

1. **URL is well-formed** and resolves to `github.com/{owner}/{repo}/pull/{number}`. The
   canonical `owner/repo#number` triple is the identity; the URL is display.
2. **The PR exists and is publicly visible.**
3. **The PR is merged.** Open, closed-unmerged, and draft PRs are rejected. A merged PR is
   one another human accepted, and that gate does real work.
4. **The claimant is the author**, compared on GitHub account id, not login (RFC-0002).
   Primary authorship only — co-authorship is out of scope for v1.
5. **No duplicate within the claim.**

**We do not pre-filter trivial-looking PRs.** A one-line change with no discussion may
still be valuable to the project, and a small diff can carry a long, substantive review
conversation. Every PR goes to the model, and the rubric decides. Splitting that judgement
between validation code and the rubric would put two authorities on the same question.

A claim failing a check is rejected **naming the specific failing item** — "PR 3 is not
merged", not "invalid submission". The contributor is doing curation work; vague errors
waste it.

### Reuse across skills, and the uniqueness rule

The same PR may evidence **several different skills** — that is the mechanism by which
secondary skills accumulate toward promotion, and a change that adds a Kubernetes operator
in Go genuinely evidences both.

What is not allowed is the same PR evidencing the **same skill** twice:

> **`(user, PR, skill)` is unique across a contributor's active claims.**

Enforced by the primary key of `user_skill_pr_links`, so it is a database invariant rather
than a validation convention. This closes a hole that would otherwise exist: without it,
two claims could each offer PR #7 as evidence of Go, the same PR would be judged for Go
twice with two different scores, and there would be no principled answer to which one
counts. With it, the question cannot arise and `distinct_pr_count` is a plain row count.

**A claim containing a pair the contributor already holds is rejected**, naming the
conflict — "PR 3 already evidences Go in claim X" — and the contributor swaps the PR or
drops the skill. The same treatment as any other evidence error, for the same reason:
silently judging a claim for fewer skills than were asked for is worse than refusing it.

**Withdrawing a claim releases its pairs.** A withdrawn claim's PRs stop counting toward
standing, so holding their pairs reserved would block evidence that no longer counts for
anything.

## Lifecycle

```
   draft ──submit──▶ validating ──┬──▶ invalid ──edit──▶ draft
     ▲                            │
     │                            └──▶ queued ──▶ evaluating ──┬──▶ evaluated
     └──────────── edit ───────────────────────────────────────┤
                                                               └──▶ failed ──retry──▶ queued
   (any state) ──withdraw──▶ withdrawn
```

| Status | Meaning |
|---|---|
| `draft` | Being assembled. Evidence mutable. |
| `validating` | Checks running against GitHub. |
| `invalid` | A check failed. Per-item reasons attached. Editable. |
| `queued` | Passed validation, awaiting the evaluator. **Evidence frozen.** |
| `evaluating` | In a batch with the AI provider. |
| `evaluated` | Scored. Standings updated. |
| `failed` | Errored after retries. Admin-retryable. |
| `withdrawn` | Retracted. Its PRs no longer count toward standing, and their `(PR, skill)` pairs are released. |

### Withdrawal warns before demoting

Withdrawing a claim can drop a primary skill below five PRs. The contributor is **told
before confirming** — "this will demote Go to secondary and remove it from ranking" — and
the demotion then happens immediately on confirmation.

Standing never disagrees with the evidence behind it, and nobody loses a ranking without
having been asked. A grace period would have kept standing and evidence out of step; a
block would have taken away a contributor's control over their own submissions.

**Evidence freezes at `queued`**, because a claim whose evidence changed mid-evaluation
would produce a score for input that no longer exists. Editing an evaluated claim bumps
`version`, returns it to `draft`, and leaves the previous evaluation intact and attributed
to the previous version.

**Editing evidence triggers a rescore**, as does a rubric change. Nothing else does; scores
never decay with age (RFC-0001).

## The 7-day lock

Once a claim is scored, **that claim cannot be edited or resubmitted for 7 days.** The
contributor may still create and submit claims for *other* skills freely.

This exists to stop score-rerolling — resubmitting slightly different evidence until the
number improves — which is both a gaming vector and a direct cost, since every submission
is a model call. Locking only the scored claim targets that behaviour without freezing a
contributor who is midway through building out several skills.

A paid bypass is the intended eventual shape. **Payments are entirely out of scope for
v1**: there is no gateway, no plan, and no user counter. The lock is unconditional, and the
platform is free.

Also enforced:

- **Re-submission of unchanged evidence is refused**, using the evidence fingerprint
  (RFC-0004) — a cheap equality check that stops a no-op model call.
- **A withdrawn claim may be re-claimed**, subject to the same lock.

## Resolved review comments

| Marker | Resolution |
|---|---|
| Per-PR scores; editing a PR updates skill and profile score | PR × skill scores stored; edits trigger rescore and standings recompute |
| 7-day lock after scoring, payment later | Per-claim 7-day lock, other skills unaffected; payments out of scope for v1 |
| Q1 pre-filter trivial PRs | Dropped — every PR is judged by the model; a small diff may still matter |
| Q2 co-authored PRs | Primary authorship only |
| Q3 deleted repositories | Not re-checked until a rubric change forces re-evaluation |
| Q4 skill request flow | Request endpoint + admin review dashboard |
| Secondary skills hold 1–4 PRs | Standing derived from distinct PR count; auto-promotes at 5 |
| Projects optional | 0..20, arithmetic-only, never judged by the model |
| Maintainer status | Contributor declares with a source of truth; model validates |

## Open questions

None outstanding. Suggestion volume is bounded at the prompt (at most 3 per claim), and
skill requests are rate-limited and auto-deduped before reaching a human — both documented
above.

## Schema

Introduces `skills`, `skill_aliases`, `skill_requests`, `claims`, `claim_skills`,
`claim_pr_evidence`, `claim_project_evidence`, `project_maintainer_declarations`, and the
derived `user_skills` standing table.
