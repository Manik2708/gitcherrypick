# The five judged dimensions

Each is scored **0–100 per PR, per skill**. Weights are in [rubric-v1.md](rubric-v1.md) and
are not repeated here — a definition that carries its own weight invites arguing the two
together.

All five are judged from the same inputs: the diff, the PR title and description, the full
review conversation, and the cached repository facts. Diff statistics are **context, not a
score** (ADR-0005) — the model may read that a change touched 4,000 lines and must not
conclude anything from the number alone.

## Every dimension carries a remark

The model returns **a score and a short remark for each dimension**, not one rationale for
the PR as a whole:

```json
"substance": { "score": 78, "remark": "Reworked the retry path to be idempotent; the
                                       hard part was identifying which operations were
                                       not safe to repeat." }
```

A single overall rationale cannot explain a breakdown. A contributor looking at
`complexity 85, craft 40` needs to know what the 40 was about, and a hirer reading the
scorecard is buying exactly that specificity. It is also the only way a re-evaluation
request (see [calibration.md](calibration.md)) can be about something concrete rather than
"my score feels low".

Remarks are for the contributor and the hirer to read. They are **never** an input to
arithmetic.

---

## 1 · `substance` — how much real engineering this represents

Not size. A 30-line change that fixes a race condition is more substantial than a 3,000-line
mechanical rename.

| Score  | Archetype                                                                                                                                                                                                              |
| ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | A new capability or subsystem, or a fix to a deep defect that required real diagnosis — reproducing an intermittent failure, tracing through layers, identifying a root cause that was not where the symptom appeared. |
| 70–89  | A meaningful feature or a non-trivial fix within existing structure. The author had to understand a component well to do it correctly.                                                                                 |
| 40–69  | Routine but real work: a contained feature, a straightforward bug fix with a test, a well-scoped refactor that improves something specific.                                                                            |
| 15–39  | Mechanical but not trivial: plumbing a parameter through, a behaviour-preserving refactor, a config or build change with some judgement in it.                                                                         |
| 1–14   | Trivial but defensible: a one-line fix where the reasoning behind finding it was the work.                                                                                                                             |
| 0      | Not used — see [disqualification.md](disqualification.md).                                                                                                                                                             |

**Must ignore:** lines added or removed, files touched, number of commits, whether the
repository is popular, and how long the PR took to merge.

---

## 2 · `complexity` — how hard the _problem_ was

Distinct from substance. A change can be substantial but conceptually straightforward (a
well-understood feature, carefully built), or small but hard (a two-line fix to a memory
ordering bug).

Consider: concurrency and races, distributed or partial-failure states, performance
constraints, protocol or backwards-compatibility obligations, cross-cutting changes with
many call sites, and problems where the naive solution is wrong in a non-obvious way.

| Score  | Archetype                                                                                                                                                                                                         |
| ------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Correctness-critical concurrent, distributed, or algorithmic work where being wrong is subtle and expensive. Memory ordering, consensus, lock-free structures, a fix that required reasoning about interleavings. |
| 70–89  | Real constraint work: performance under a stated budget, backwards compatibility across versions, a migration that must not lose data, careful error and retry semantics.                                         |
| 40–69  | Standard application complexity — several interacting components, some edge cases, nothing that would surprise an experienced engineer.                                                                           |
| 15–39  | Contained and local. Follows an existing pattern in the codebase.                                                                                                                                                 |
| 1–14   | Essentially no problem to solve; the difficulty was locating the site, not deciding what to do.                                                                                                                   |

**Must ignore:** how unfamiliar the domain is to a general reader, how long the diff is, and
how much the author says the problem was hard. Judge the problem, not the description of it.

---

## 3 · `conversation_quality` — what happened in review

The single most underused signal in assessing engineers, and the reason this rubric weights
it at all: a diff shows what someone built, and the review thread shows how they think.

Judge **the author's contribution to the discussion** — explaining design decisions,
responding to substantive critique, revising well, pushing back with reasons, and
identifying problems in their own change.

| Score  | Archetype                                                                                                                                                           |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Substantive technical discussion the author drove: a real design question raised, alternatives weighed with reasons, the outcome better than the original proposal. |
| 70–89  | Meaningful review engaged with properly. The author explained decisions, took feedback on board, and revised with understanding rather than compliance.             |
| 40–69  | Ordinary review handled competently. Some back-and-forth, changes made, nothing notable in either direction.                                                        |
| 15–39  | Minimal engagement — feedback applied without discussion, or a thread consisting of nits.                                                                           |
| 1–14   | No discussion of substance.                                                                                                                                         |

**A PR with no discussion scores 0 on this dimension.** No exceptions, no benefit of the
doubt for a change that was self-evident or merged by a trusting maintainer. If there was no
conversation, no conversational skill was demonstrated, and we are not going to infer one.

This is stated plainly to contributors before they submit, because it turns the five-PR
choice into a real decision. A technically brilliant PR merged in silence contributes
nothing here; a moderate change with a substantive design thread contributes a lot. **The
contributor decides which of their five carries which strength** — one for technical depth,
another for how they argue. That trade-off is theirs to make, and making it well is itself
a signal.

A zero here does not disqualify the PR. It is one dimension of five, weighted 0.20, and a
strong change with no discussion still scores respectably overall.

**Must ignore:** comment count, thread length, number of participants, and how polite the
exchange was. A terse, correct disagreement beats a warm, empty one. Those counts feed the
`E` term arithmetically (ADR-0005) and must not be double-counted here.

---

## 4 · `craft` — readability and the qualities that survive the merge

Two things, weighed together: **will this be understandable to the next reader**, and **will
it hold up**.

_Readability:_ naming that says what a thing is, functions at one level of abstraction,
control flow that can be followed without a diagram, design patterns chosen because they fit
rather than because they are known. Comments that explain constraints rather than restate
the line below.

_Durability:_ tests that would catch a regression, documentation updated where behaviour
changed, errors handled at the right boundary, backwards compatibility respected.

| Score  | Archetype                                                                                                                                                                                      |
| ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | Reads as though the codebase always contained it. Names carry meaning, the structure is the obvious one in hindsight, tests would catch a real regression, docs updated where behaviour moved. |
| 70–89  | Clear and well-tested. A reviewer might name a better abstraction but would not block.                                                                                                         |
| 40–69  | Adequate. Works and can be followed, but naming is vague in places or the testing is thin.                                                                                                     |
| 15–39  | Careless: names that mislead, a function doing three things, no tests where they were warranted, friction added for the next reader.                                                           |
| 1–14   | Actively degrades the codebase — copy-paste, dead code left behind, error handling that swallows failures, a pattern imposed that fights the surrounding code.                                 |

**Judge against the project's own conventions**, not an abstract ideal. A project with no
test suite cannot be faulted for a PR without tests; a project with a strict one can. The
same applies to structure: matching the surrounding code is craft, and imposing a
personally-preferred architecture on a codebase that does not use it is not.

**Must ignore:** whether tests exist in the repository at all, and **pure formatting** —
line length, indentation, brace placement, import ordering. Those are settled by the
project's linter and say nothing about the author. Readability is about whether a human can
follow the code, not whether a formatter approves of it.

---

## 5 · `skill_specificity` — how much this PR is _about_ the skill being judged

The lowest-weighted dimension and the one doing the most structural work: it is what stops a
single PR being strong evidence for six skills at once.

Judge how central the skill is to what the change actually does — not whether the file
extension matches.

| Score  | Archetype                                                                                                                                                                 |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 90–100 | The change is fundamentally an exercise of this skill. Judging it means judging this skill.                                                                               |
| 70–89  | The skill is central to the change, alongside others.                                                                                                                     |
| 40–69  | The skill is genuinely used, but the change is mostly about something else.                                                                                               |
| 15–39  | Incidental contact. The skill appears but the change would have been much the same without it.                                                                            |
| 1–14   | Present only because of the language a file is written in. Editing a YAML file does not evidence Kubernetes; writing a Go test for a Python service does not evidence Go. |

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
