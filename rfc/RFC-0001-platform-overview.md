# RFC-0001 — Platform overview, domain model & service topology

**Status:** Approved 2026-08-02 · **Binding form:** [ADR-0001-platform-foundation](../adr/ADR-0001-platform-foundation.md) · **Schema:** [RFC-0001-platform-overview.schema](RFC-0001-platform-overview.schema)

## Summary

GitCherryPick lets open-source contributors demonstrate skill with **evidence** instead
of assertion, and lets verified hirers rank and find them on that basis. This RFC fixes
the vocabulary, the domain model, and the service topology that RFC-0002 through RFC-0005
build on. It proposes no user-facing feature by itself.

## Motivation

A CV claims "expert in Go." A GitHub profile shows a contribution graph. Neither answers
the hirer's actual question: *has this person done work of consequence in this technology,
and how good was it?*

The evidence to answer that is public and already exists — merged pull requests into real
projects. It is simply expensive to assess. Someone would have to open five PRs, read the
diffs, read the review conversations, and know enough about the project to know whether it
matters. That does not scale, so it does not happen.

We make that assessment once, consistently, and turn it into a comparable number.

## The core constraint

**A skill is not a string the user types.** Skill standing is earned with merged pull
requests, and the number of them decides what that standing is worth:

| Standing | Requirement | Consequence |
|---|---|---|
| **Primary** | **5 distinct PRs** evidencing the skill | Ranked. Appears in search and comparisons. |
| **Secondary** | 1–4 PRs | Visible on the profile, contributes to the overall score, **never ranked** |

A contributor may hold **many primary and many secondary skills**. A secondary skill
**auto-promotes to primary** the moment a fifth distinct PR evidences it — the evidence
already exists and has already been judged, so no resubmission is required.

**A contributor needs at least one primary skill to receive an overall score.** Without
one, they have a profile and per-skill scores but no headline number and no place in any
ranking.

### Why five

A floor invites padding. A single lucky PR proves little. Fixing primary standing at five
makes every ranked skill rest on the same quantity of evidence, so **the scores are
directly comparable**. It also forces a judgement — *which five?* — that is itself signal.

## Claims are evidence bundles, not skill assertions

A **claim** is one submission: up to five pull requests, optional supporting projects, and
the skills the contributor says those PRs demonstrate.

```
Claim ──┬── 1..5  PR evidence
        ├── 0..20 project evidence
        └── 1..N  declared skills  (one intended primary, others secondary)
```

**One claim is evaluated for every one of its skills in a single model call.** The model
reads the five diffs and their review conversations once, then scores each PR against each
declared skill. Judging the same PR separately per skill would cost more and produce
less coherent results, because the interesting comparison — how much of this work is
really *Kubernetes* rather than *Go* — is only visible when both are considered together.

Ranked standing is therefore **derived**, not submitted: a skill's PR count is the number
of distinct PRs across all of that contributor's claims that evidence it.

### Secondary skills are scored relative to the primary

A secondary skill's score is a proportion of the primary skill's score for the same PR,
set by how much of the change it accounts for and how much it mattered. Work constituting
roughly 30% of the diff at similar impact earns roughly 30%; work of very high impact and
complexity can earn as much as the primary skill or more. The proportion is **uncapped** —
if the Kubernetes work genuinely outweighed the Go work, the score says so, even though
the contributor nominated Go as primary.

### There are no zero scores

A skill that scores zero is **rejected, not recorded**. A negligible secondary skill is
dropped from the claim silently. If the *primary* skill scores zero, it is dropped too and
the contributor is told why — but the claim survives with whatever secondary skills held
up. Storing a zero would imply we measured something; we are saying the evidence does not
support the claim at all.

### The AI may suggest skills, the contributor decides

While judging a claim, the model may attach skills it observes that the contributor did not
declare. **Those suggestions are inert until accepted** — invisible, unscored, and
excluded from every total. Auto-discovery with consent, rather than being ranked for
something you never claimed.

## Actors

| Actor | Description |
|---|---|
| **Contributor** | Authenticates with GitHub. Submits claims, receives scores. |
| **Hirer** | A **separate, manually verified account** (RFC-0002). Hires employees or freelancers. Only verified hirers see full scorecards. |
| **Organization** | Groups hirers, is admin-verified, owns shortlists. Only verified organizations may hire. |
| **Admin** | Seeded account. Curates the skill catalogue, verifies hirers and organizations, handles disputes. |

Hirers looking for **freelance** work are first-class, not a special case: contributors
signal availability for employment, freelance work, or both (RFC-0002).

## Primary flows

**Onboarding.** Contributor authenticates with GitHub. We capture their verified GitHub
identity — immutable numeric account id, not the mutable login.

**Claiming.** Contributor attaches up to five PR URLs, optionally some projects, and names
the skills they demonstrate. Submission is validated (RFC-0003), then queued.

**Evaluation.** The evaluator enriches each PR from GitHub, asks the model for per-PR,
per-skill judgements in one call, computes the arithmetic itself, and updates the
contributor's derived skill standings and overall score (RFC-0004).

**Discovery.** Verified hirers search and rank contributors, then shortlist them
(RFC-0005).

## Domain model

```
Organization ──< HirerAccount ──▶ UserGithubIdentity   (link prevents self-hire)
                      │
                      └──< Shortlist >── ShortlistEntry ──┐
                                                          │
User ──< UserGithubIdentity                               │
  │                                                       ▼
  ├──< Claim ──┬──< PrEvidence       (1..5)          (contributor)
  │            ├──< ProjectEvidence  (0..20)
  │            └──< ClaimSkill       (declared or AI-suggested)
  │
  ├──< Evaluation ──< PrSkillScore   (per PR, per skill)
  ├──< UserSkill     (derived standing: PR count, score, primary|secondary)
  └──< ScoreSnapshot
```

| Entity | Meaning |
|---|---|
| **User** | A contributor. |
| **UserGithubIdentity** | The verified GitHub account bound to a user or hirer. |
| **HirerAccount** | A verified hiring identity, separate from any contributor account. |
| **Skill** | A catalogue entry (`go`, `kubernetes`, `postgres`). Curated, not free text. |
| **Claim** | One submission of evidence, carrying the skills it is claimed to demonstrate. |
| **ClaimSkill** | One skill attached to a claim, declared by the user or suggested by the model. |
| **PrEvidence** | One pull request in a claim. |
| **ProjectEvidence** | One repository, scored **arithmetically only** (see below). |
| **Evaluation** | One scoring run over one claim version. Immutable. |
| **PrSkillScore** | One PR judged against one skill by one evaluation. |
| **UserSkill** | Derived standing per (user, skill): PR count, score, primary or secondary. |
| **ScoreSnapshot** | A user's skill and overall scores at a point in time. |

### Projects are scored arithmetically only

Judging a project's worth requires knowing it has real users, and that is not provable
from anything GitHub exposes. So the model is never asked to judge a project. Projects
contribute through **hard signals only** — stars, forks, contributor count, dependents,
package downloads, and maintainer status — normalised as RFC-0005 specifies. Supplying a
project is optional.

### Three invariants

**Evidence is verified, not trusted.** Authorship, merge status, and repository facts come
from the GitHub API, never from what the user typed. A user cannot claim a PR they did not
write. RFC-0003 specifies the checks.

**Evaluations are immutable and versioned.** Each records the rubric version, model, and
prompt version that produced it. Scores are compared only within a rubric version.

**Scores change for exactly two reasons** — the rubric changed, or the evidence changed.
They never decay with age. A third, narrower motion exists: the **arithmetic recompute**
(RFC-0005), which refreshes only the *relative* component of a score as platform-wide
maxima move. It re-reads nothing from GitHub and calls no model.

## Service topology

```
  Browser ──HTTPS──▶  api        ──▶ database
  (React)                 │              ▲
                          │              │
                       broker ──▶ evaluator ──▶ AI provider (Batch)
                                     └────────▶ GitHub API
```

| Component | Binary | Responsibility |
|---|---|---|
| **api** | `backend/cmd/api` | REST for the frontend. Auth, claims, search. Never calls the AI. Applies the schema idempotently at boot. |
| **evaluator** | `backend/cmd/evaluator` | Consumes broker messages, scores claims, writes results. No client-facing surface. |
| **frontend** | `frontend/` | React + TypeScript. Talks only to `api`. |

**There is no migration binary.** The api applies the schema idempotently on startup:
pending migrations are applied in order and already-applied ones are skipped, so a boot
against a current database is a no-op. Migrations are written when a change actually
lands, not maintained speculatively.

The api and the evaluator share a database and a domain model but **not** a process. They
communicate only through the broker. The evaluator is the only component holding
credentials for the AI provider.

## Non-goals

- **We do not host or mirror code.** We reference PRs by URL and cache metadata.
- **We do not evaluate private contributions.** Only what is publicly verifiable counts.
- **We do not rank organizations, only people.**
- **We publish no job postings and accept no applications.** Shortlists carry a tentative
  result date so hirers are accountable for rounds they open (RFC-0005), but there is no
  public listing, no application flow, and no messaging in v1.
- **We do not rank a contributor for a skill they have not accepted.** The model may
  suggest; only the contributor promotes.

## Resolved review comments

| Marker | Resolution |
|---|---|
| Freelance hirers missing from actors | Hirer actor covers both; availability signalled per contributor (RFC-0002) |
| Project evidence is unprovable | Kept, but scored on hard signals only — no AI judgement, and optional |
| Why a migrate binary now? | Removed. Schema applied idempotently by the api at boot |
| Skill catalogue governance | Request endpoint + admin dashboard (RFC-0003) |
| One PR backing two skills | Yes. One claim, one model call, per-skill scores. 5 distinct PRs promotes to primary |
| Score decay | None. Scores move only on rubric change or evidence change; arithmetic recompute adjusts relative values only |
| Discovery consent | Availability statuses with a 15-day refresh (RFC-0002); no public leaderboard (RFC-0005) |

## Open questions

None outstanding at this level. Skill-request throughput and suggestion volume are bounded
in RFC-0003; the uncapped-secondary presentation is resolved by treating score and standing
as orthogonal (below).

## Score and standing are orthogonal

The single most confusable thing in this design, so it is stated here as well as in
RFC-0003:

> **The score says what the work was worth. The PR count says whether it is rankable.**

A secondary skill may outscore a primary one and remains secondary regardless. Kubernetes
at 91 with three PRs is not ranked; Go at 74 with five PRs is. Nothing promotes a skill
except a fifth distinct PR, and no score is capped to keep the ordering tidy.
