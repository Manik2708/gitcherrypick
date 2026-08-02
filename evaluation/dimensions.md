# The five judged dimensions

Each is scored **0–100 per PR, per skill**. Weights are in [rubric-v1.md](rubric-v1.md) and
are not repeated here — a definition that carries its own weight invites arguing the two
together.

All five are judged from the same inputs: the diff, the PR title and description, the full
review conversation, and the cached repository facts. Diff statistics are **context, not a
score** (ADR-0005) — the model may read that a change touched 4,000 lines and must not
conclude anything from the number alone.

---

## 1 · `substance` — how much real engineering this represents

Not size. A 30-line change that fixes a race condition is more substantial than a 3,000-line
mechanical rename.

| Score | Archetype |
|---|---|
| 90–100 | A new capability or subsystem, or a fix to a deep defect that required real diagnosis — reproducing an intermittent failure, tracing through layers, identifying a root cause that was not where the symptom appeared. |
| 70–89 | A meaningful feature or a non-trivial fix within existing structure. The author had to understand a component well to do it correctly. |
| 40–69 | Routine but real work: a contained feature, a straightforward bug fix with a test, a well-scoped refactor that improves something specific. |
| 15–39 | Mechanical but not trivial: plumbing a parameter through, a behaviour-preserving refactor, a config or build change with some judgement in it. |
| 1–14 | Trivial but defensible: a one-line fix where the reasoning behind finding it was the work. |
| 0 | Not used — see [disqualification.md](disqualification.md). |

**Must ignore:** lines added or removed, files touched, number of commits, whether the
repository is popular, and how long the PR took to merge.

---

## 2 · `complexity` — how hard the *problem* was

Distinct from substance. A change can be substantial but conceptually straightforward (a
well-understood feature, carefully built), or small but hard (a two-line fix to a memory
ordering bug).

Consider: concurrency and races, distributed or partial-failure states, performance
constraints, protocol or backwards-compatibility obligations, cross-cutting changes with
many call sites, and problems where the naive solution is wrong in a non-obvious way.

| Score | Archetype |
|---|---|
| 90–100 | Correctness-critical concurrent, distributed, or algorithmic work where being wrong is subtle and expensive. Memory ordering, consensus, lock-free structures, a fix that required reasoning about interleavings. |
| 70–89 | Real constraint work: performance under a stated budget, backwards compatibility across versions, a migration that must not lose data, careful error and retry semantics. |
| 40–69 | Standard application complexity — several interacting components, some edge cases, nothing that would surprise an experienced engineer. |
| 15–39 | Contained and local. Follows an existing pattern in the codebase. |
| 1–14 | Essentially no problem to solve; the difficulty was locating the site, not deciding what to do. |

**Must ignore:** how unfamiliar the domain is to a general reader, how long the diff is, and
how much the author says the problem was hard. Judge the problem, not the description of it.

---

## 3 · `conversation_quality` — what happened in review

The single most underused signal in assessing engineers, and the reason this rubric weights
it at all: a diff shows what someone built, and the review thread shows how they think.

Judge **the author's contribution to the discussion** — explaining design decisions,
responding to substantive critique, revising well, pushing back with reasons, and
identifying problems in their own change.

| Score | Archetype |
|---|---|
| 90–100 | Substantive technical discussion the author drove: a real design question raised, alternatives weighed with reasons, the outcome better than the original proposal. |
| 70–89 | Meaningful review engaged with properly. The author explained decisions, took feedback on board, and revised with understanding rather than compliance. |
| 40–69 | Ordinary review handled competently. Some back-and-forth, changes made, nothing notable in either direction. |
| 15–39 | Minimal engagement — feedback applied without discussion, or a thread consisting of nits. |
| 1–14 | No discussion of substance. |

**A PR with no comments is not automatically low.** A self-evident change merged directly by
a maintainer who trusted it is a different thing from a change nobody looked at. Weigh who
merged it and whether the change needed discussion. Where the thread is empty and the change
was non-trivial, score the *absence of needed review* as a property of the project, not a
failing of the author — around 40, not 5.

**Must ignore:** comment count, thread length, number of participants, and how polite the
exchange was. A terse, correct disagreement beats a warm, empty one. Those counts feed the
`E` term arithmetically (ADR-0005) and must not be double-counted here.

---

## 4 · `craft` — the qualities that survive the merge

Tests, documentation, error handling, backwards compatibility, and whether the change leaves
the codebase easier or harder to work in.

| Score | Archetype |
|---|---|
| 90–100 | Tests that would actually catch a regression, documentation updated where behaviour changed, errors handled at the right boundary, and the change fits the codebase as though it had always been there. |
| 70–89 | Well-tested and clear. Minor gaps a reviewer would mention but not block on. |
| 40–69 | Adequate. Works, is readable, testing is thin or partial. |
| 15–39 | Functional but careless: no tests where tests were warranted, or a change that adds friction for the next reader. |
| 1–14 | Actively degrades the codebase — copy-paste, dead code left behind, error handling that swallows failures. |

**Judge against the project's own conventions**, not an abstract ideal. A project with no
test suite cannot be faulted for a PR without tests; a project with a strict one can.
Comment density, naming style, and formatting are project decisions and are **not** craft.

**Must ignore:** whether tests exist in the repository at all, code style preferences, and
line-length or formatting conventions.

---

## 5 · `skill_specificity` — how much this PR is *about* the skill being judged

The lowest-weighted dimension and the one doing the most structural work: it is what stops a
single PR being strong evidence for six skills at once.

Judge how central the skill is to what the change actually does — not whether the file
extension matches.

| Score | Archetype |
|---|---|
| 90–100 | The change is fundamentally an exercise of this skill. Judging it means judging this skill. |
| 70–89 | The skill is central to the change, alongside others. |
| 40–69 | The skill is genuinely used, but the change is mostly about something else. |
| 15–39 | Incidental contact. The skill appears but the change would have been much the same without it. |
| 1–14 | Present only because of the language a file is written in. Editing a YAML file does not evidence Kubernetes; writing a Go test for a Python service does not evidence Go. |

**Must ignore:** file extensions, directory names, and whether the repository's primary
language matches the skill. A Go repository does not make every PR in it evidence of Go.

---

## Scoring discipline

**Use the full range.** A rubric where everything lands between 60 and 80 has measured
nothing. If a change is mechanical, 25 is the correct answer and 55 is a failure of nerve.

**Score each dimension independently.** A PR can be highly complex and poorly crafted, or
substantial with no review conversation. Letting one dimension pull the others toward it —
halo scoring — collapses five signals into one and is the most likely failure mode here.

**When evidence is absent, say so rather than assuming.** An empty review thread is
information about the project. A missing test suite is information about the project. Score
what is there.
