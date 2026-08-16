---
issue: lineage-terminal-kinds-and-pass-through-exclusions
kind: audit
category: conflicting
artifacts:
  - concept:lineage-record
  - concept:lineage
status: verified
opened: 2026-08-16T09:04:56Z
---

# The lineage record's terminal-kind family is six values, not four, and two "no record" cases emit one

A lineage record is written when a node-run settles, carrying a terminal kind. The lineage-record concept calls that a closed family of four (complete, errored, park, subgraph-call) and says two settlement shapes emit no record — an acquire-phase pass disposition and a pure-cascade node — pointing readers at the audit log instead; the lineage concept restates the pure-cascade exclusion. The code writes six kinds: the acquire-phase pass path shares the error-policy hook that emits under a handler-pass kind, and the scheduler's pure-cascade sweep emits a leaf-run record on every settlement. The fan-out-parent exclusion holds. Two concepts give a coherent reason for the exclusion; nothing confirms the emissions are intended. The ruling decides whether a no-computation settlement gets a lineage record.

## Options

- Amend both concepts to enumerate six kinds and accept the emissions; cost: the "lineage means computation" rationale goes.
- Stop the two sites from emitting, restoring the stated design; cost: removes records a consumer may already read.
- Split the family into computation-anchored and settlement-anchored kinds; cost: a taxonomy redesign.

The ruling decides what a lineage record means.

## Ruling

> Recommended ruling (/verify-issues): Restore the design — stop emitting lineage records for acquire-phase pass dispositions and pure-cascade settlements, and keep the closed family of four; the audit log is where those settlements are already visible.
>
> Rationale: two concepts state the exclusion with a reason (lineage records what computed something), no artifact blesses the extra emissions, and a consumer walking lineage benefits from not stepping through settlements that touched no data. Flip case: if a lineage consumer needs the pure-cascade hop to connect a graph across a node that computed nothing (a walk that otherwise breaks), the third option keeps both meanings honest.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
