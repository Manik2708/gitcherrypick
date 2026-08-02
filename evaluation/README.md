# Evaluation rubric

What earns points. ADR-0005 decides how signals *combine*; this directory decides what the
signals *mean*.

The split is deliberate: two authorities on the same question is how a scoring system
becomes unauditable. ADR-0005 owns the arithmetic and never says what
`contribution_substance` is. These files own that definition and never change a weight.

## Status

**Draft — not approved.** No implementation may consume this until the owner approves it
(stage 2 gate). One item below requires an amendment to ADR-0004 and ADR-0005 before it can
work at all; it is flagged in `disqualification.md` and needs deciding first.

## Files

| File | Contents |
|---|---|
| [dimensions.md](dimensions.md) | The five judged dimensions, each with a 0–100 scale, anchors, and what it must ignore |
| [disqualification.md](disqualification.md) | When a PR scores **zero** regardless of everything else — and the contract change that requires |
| [secondary-skills.md](secondary-skills.md) | How `relative_share` is judged for a skill that is not the nominated primary |
| [pr-review.md](pr-review.md) | The `pr-review` skill's own dimensions — reviewing is judged differently from authoring |
| [rubric-v1.md](rubric-v1.md) | Every constant, in one table. Source of truth for the embedded YAML (ADR-0006) |
| [calibration.md](calibration.md) | How we find out whether any of this is right |

## What the model is asked for, and what it is never asked for

**Asked for:** per PR, per skill — a score 0–100 on each dimension, a `relative_share` for
secondary skills, a disqualification verdict with a reason, and a short rationale.

**Never asked for:** a PR score, a skill score, an overall score, a weighted average, or a
rank. Every total is computed in Go from the dimension scores (ADR-0005). A language model
doing weighted arithmetic produces numbers nobody can audit and that change between runs
for no stated reason.

## Design rules these files follow

**Prefer signals that are hard to inflate.** Anything a contributor can raise by doing more
of something cheap is either excluded or bounded. Lines changed, commit count, and comment
volume are all trivially inflatable, and none of them carries weight of its own.

**Say what a dimension must ignore.** A definition that only says what to consider leaves
the model to infer the rest, and it will infer differently across runs. Each dimension
below has an explicit exclusion list.

**Anchor every scale with archetypes, not examples from live repositories.** A linked PR can
be force-pushed, deleted, or edited, and a rubric anchored to one silently drifts. The
archetypes describe a *kind* of change precisely enough to be applied consistently.

**State the non-goals.** Silence reads as an omission; a stated exclusion reads as a
decision. See below.

## What this rubric deliberately does not measure

- **Seniority, tenure, or employer.** Not visible in a diff and not what is being claimed.
- **Volume of contribution.** Five PRs is the evidence set. How many others exist is not
  scored — it only breaks ties (ADR-0005), where it is a weak signal by design.
- **Popularity of the contributor.** Followers and stars on *their* profile are irrelevant.
  Repository reach is scored; personal reach is not.
- **Code style preferences.** Formatting, naming conventions, and tabs-versus-spaces are
  project decisions, not skill signals.
- **Language or ecosystem prestige.** A skilled change is a skilled change.
- **Recency.** Scores never decay with age (ADR-0001). A 2019 PR is judged on its merits.

## Versioning

The rubric is versioned as a whole, not per file. `rubric-v1.md` is the constant table for
`v1`; the definitions here apply to `v1` unless a later file supersedes them.

A change to any definition or constant is a **rubric version bump**, which requires a
re-evaluation sweep (ADR-0004) and invalidates comparison with prior scores. That friction
is intentional.
