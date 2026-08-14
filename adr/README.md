# ADRs

An ADR is an **approved** RFC, expanded with implementation detail and ordered steps.

`rfc/RFC-0004-*.md` becomes `adr/ADR-0004-*.md` — same number, always. The RFC stays in
place as the record of the proposal and its reasoning; the ADR is what implementers build
against.

## Only the ADR is binding

No role may assume a database, queue, model, endpoint shape, or scoring constant that exists
only in an RFC. If the two disagree, the ADR wins and the discrepancy is a bug in the ADR —
report it to the Planner rather than choosing.

## What an ADR adds

| Section            | Purpose                                                                  |
| ------------------ | ------------------------------------------------------------------------ |
| **Decision**       | The binding statements, in imperative form. No hedging, no alternatives. |
| **Implementation** | Package layout, interface signatures, algorithms, endpoint shapes.       |
| **Steps**          | Ordered work items. An implementer starts at 1 and does not reorder.     |
| **Consequences**   | What this forecloses, and what will hurt later.                          |

Rationale lives in the RFC and is **not** repeated here. An ADR that re-argues its own case
is an RFC with the wrong filename.

## Changing an approved decision

**Write a new RFC, and promote it to a new ADR. Never edit an approved one in place.**

An approved document is a record of what was decided and when. Editing it destroys that —
the reasoning that produced the original choice disappears, and there is no way to see that
a decision was ever reconsidered, let alone why.

So a change gets its own number:

1. New RFC stating what changes and why, with a `.schema` holding **only the delta** —
   `ALTER TABLE`, `CREATE TYPE`, not a rewritten table definition.
2. On approval, promoted to the ADR of the same number.
3. Both the superseded ADR and the new one gain an **Amendments** row pointing at the other,
   so a reader arriving at either finds the current answer.

The superseding document says precisely which sections it replaces. Everything it does not
mention stays in force.

**Reading the current state therefore means reading a number in order**, not reading one
file. That is the cost, and it buys a design history you can actually audit — which decision
changed, when, and what argument moved it.

## Index

| #                                                        | Title                                                    | Status   | Schema                                                             |
| -------------------------------------------------------- | -------------------------------------------------------- | -------- | ------------------------------------------------------------------ |
| [0001](ADR-0001-platform-foundation.md)                  | Platform foundation & service topology                   | Accepted | [rfc](../rfc/RFC-0001-platform-overview.schema)                    |
| [0002](ADR-0002-identity-and-access.md)                  | Identity, access & organizations                         | Accepted | [rfc](../rfc/RFC-0002-identity-and-access.schema)                  |
| [0003](ADR-0003-claims-and-standing.md)                  | Claims, evidence & skill standing                        | Accepted | [rfc](../rfc/RFC-0003-skill-claims-and-evidence.schema)            |
| [0004](ADR-0004-evaluation-pipeline.md)                  | Evaluation pipeline & broker                             | Accepted | [rfc](../rfc/RFC-0004-evaluation-pipeline.schema)                  |
| [0005](ADR-0005-scoring-and-discovery.md)                | Scoring, ranking & discovery                             | Accepted | [rfc](../rfc/RFC-0005-ranking-and-discovery.schema)                |
| [0006](ADR-0006-technology-selection.md)                 | Technology selection & dependencies                      | Accepted | —                                                                  |
| [0007](ADR-0007-rubric-contract-and-generalist-score.md) | Rubric contract, disqualification & the generalist score | Accepted | [rfc](../rfc/RFC-0007-rubric-contract-and-generalist-score.schema) |

Schema DDL lives beside the RFCs; migrations are generated from it during implementation
(ADR-0001).

## Build order

ADR-0001 and ADR-0006 come first — they establish the module layout and the dependency set
everything else compiles against. After that, 0002 → 0003 → 0004 → 0005, because each
depends on the tables and services the previous one introduces.

Integration tests (stage 3) are written against these ADRs **before** any of it is
implemented, and must be failing when handed over.
