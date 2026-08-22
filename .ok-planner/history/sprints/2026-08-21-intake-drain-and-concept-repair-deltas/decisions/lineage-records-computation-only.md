---
decision: lineage-records-computation-only
---

# Lineage records computation, not every settlement

## Choice

A leaf-run lineage record is written only at the terminal of a run that invoked an executor, and its terminal kind is one of a closed family of four. A run whose path never invokes an executor — a fan-out parent, a pure-cascade node, an acquire-phase pass disposition — writes no lineage record (see `concept:lineage-record`, `concept:lineage`).

## Rationale

Lineage records what computed something, so a consumer walking it can skip settlements that touched no data. The audit log already shows those settlements for a reader who wants them.

## Alternatives

- A record for every settlement, including pass-through hops, with a wider terminal-kind family — rejected: it dilutes the ledger with rows that carry no computation; it becomes right only if a lineage consumer needs the pure-cascade hop to connect a graph across a node that computed nothing.
