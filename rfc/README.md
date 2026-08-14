# RFCs

An RFC proposes a feature and the data model behind it. It is a **proposal** — it
describes what we intend to build and why, and carries no authority until the project
owner approves it.

## Lifecycle

```
Draft ──▶ In review ──▶ Approved ──▶ promoted to adr/ADR-NNNN-<slug>.md
                │
                └──▶ Rejected / Withdrawn   (kept, marked, never deleted)
```

An approved RFC is promoted to an ADR **of the same number** — RFC-0004 becomes
ADR-0004 — expanded with implementation detail and ordered steps. The RFC stays in
place as the record of the proposal; the ADR is what implementers build against.

**Only the ADR is binding.** No role may assume a database, queue, model, or endpoint
shape that exists solely in an unapproved RFC.

## File pairing

Each RFC is at most two files:

| File | Contents |
|---|---|
| `RFC-NNNN-<slug>.md` | The proposal — motivation, design, decisions, open questions |
| `RFC-NNNN-<slug>.schema` | **Only the schema delta** this RFC introduces |

The `.schema` file is PostgreSQL DDL, and it is a delta, not a snapshot: it contains
what this RFC adds or changes and nothing else. To see the full schema, read the schema
files of all approved RFCs in numeric order. If an RFC needs no schema change, it says
so in its **Schema** section and the `.schema` file is omitted.

`.schema` files are the design-time source of truth. Runnable migrations are generated
from them during implementation and live under `backend/`.

## Review

**While an RFC is Draft**, review comments are resolved by amending it in place and
committing to the same PR. Do not open a competing document to answer feedback — the
discussion and the resolution must stay together.

**Once an RFC is Approved, it is frozen.** A change gets a new number whose `.schema` holds
only the delta, and both documents cross-reference each other. Editing an approved document
destroys the record of what was decided and when, and hides the fact that a decision was
ever reconsidered.

Numbers are permanent. Never renumber or reuse an RFC number, including for a rejected one.

## Index

**RFC-0001 through 0006 were approved on 2026-08-02 and promoted to ADRs.** Those documents
are now the record of *why*; the ADRs in [`../adr/`](../adr/) are what implementers build
against. If the two disagree, the ADR wins.

**Approved documents are never edited.** A change gets its own number, and its `.schema`
holds only the delta — `ALTER TABLE`, not a rewritten definition. RFC-0007 is the first of
these: it amends four approved RFCs without touching one of them. Reading the current state
means reading the numbers in order, which is the cost of having a design history you can
audit.

| # | Title | Binding form |
|---|-------|--------------|
| [0001](RFC-0001-platform-overview.md) | Platform overview, domain model & service topology | [ADR-0001](../adr/ADR-0001-platform-foundation.md) |
| [0002](RFC-0002-identity-and-access.md) | Identity, access & organizations | [ADR-0002](../adr/ADR-0002-identity-and-access.md) |
| [0003](RFC-0003-skill-claims-and-evidence.md) | Claims, evidence & skill standing | [ADR-0003](../adr/ADR-0003-claims-and-standing.md) |
| [0004](RFC-0004-evaluation-pipeline.md) | Evaluation pipeline & broker | [ADR-0004](../adr/ADR-0004-evaluation-pipeline.md) |
| [0005](RFC-0005-ranking-and-discovery.md) | Scoring formulas, ranking & discovery | [ADR-0005](../adr/ADR-0005-scoring-and-discovery.md) |
| [0006](RFC-0006-technology-selection.md) | Technology selection | [ADR-0006](../adr/ADR-0006-technology-selection.md) |
| [0007](RFC-0007-rubric-contract-and-generalist-score.md) | Rubric contract, disqualification & the generalist score | [ADR-0007](../adr/ADR-0007-rubric-contract-and-generalist-score.md) — amends 0002–0005 |

Read them in numeric order; each assumes the ones before it.

## Revision 2

All six were revised after the owner's review. Every RFC ends with a **Resolved review
comments** table mapping each marker to what it became, so the review can be checked
without re-reading the whole document.

The structural change worth knowing before reading: a claim is no longer *(user, one skill,
5 PRs)*. It is an **evidence bundle** — up to 5 PRs, optional projects, and the skills those
PRs demonstrate — judged for every one of its skills in a single model call. Ranked
standing is **derived** per (user, skill) from how many distinct PRs evidence it: 5 or more
is primary and ranked, 1–4 is secondary and unranked.
