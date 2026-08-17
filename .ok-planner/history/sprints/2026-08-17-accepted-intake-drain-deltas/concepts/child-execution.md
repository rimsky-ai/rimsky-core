---
concept: child-execution
---

# Child execution

## What it is

Child execution is the umbrella term covering the two distinct mechanisms by which a parent node-run dispatches work into child execution contexts: **fan-out** (cloning the calling node N times across N partitions, fan-in via claim-handle aggregation) and **sub-graph delegation** (substituting into an absorbed entry and dispatching the sub-graph's distinct internal nodes into one shared child context, fan-in via a designated exit node's writeback). The two mechanisms are structurally different, not variants of one shape — see `decision:fan-out-and-delegation-are-distinct-mechanisms`.

What they share is a thin run-side dispatch helper that accepts a partitions × children matrix and creates the corresponding child run-scopes and child runs. Fan-out passes that helper N partitions × 1 child (the cloned calling node); delegation passes it 1 partition × N children (the sub-graph's internal nodes other than the absorbed entry). Settlement is split — each mechanism has its own settle primitive because their fan-in shapes differ:

- **Carry** (delegation's settle): fires once when the sub-graph's designated exit terminates. Copies the exit's writeback verbatim onto the calling node's attribute row, closes the child run-scope, fires the parent-settlement cascade.
- **Aggregate** (fan-out's settle): fires per clone as each sub-claim resolves, recording the child's outcome on the parent claim-handle. Only once every claim holder and every child claim-handle on the parent is no longer active does it compute the aggregate verdict via the author-chosen aggregation policy, close all partition contexts together in that firing, settle the parent's claim, and fire the parent-settlement cascade. Per-clone attribute writebacks do NOT aggregate onto the parent's attribute bag.

The carry / aggregate names refer to the two settle primitives and are stable; the settle primitive is selected by which dispatch path created the children, not by an author-configured mode.

## Purpose

Name the two distinct child-execution mechanisms and the thin dispatch helper they share so callers and reviewers can refer to the shapes by stable terms (fan-out, sub-graph delegation, carry-settle, aggregate-settle) without conflating them. The umbrella exists to make the distinction explicit, not to claim a common shape.

## Boundaries

Owns the names of the two mechanisms (fan-out, sub-graph delegation), the names of their two settle primitives (carry, aggregate), and the thin shared dispatch helper that creates child run-scopes and child runs from a partitions × children matrix. The execution contexts themselves and their tree structure are owned by `concept:run-scope`. Template surfaces are owned by `concept:delegation` and `concept:fan-out`. The aggregation policy — the fan-out-only knob with four values (`strict | threshold | best_effort | first`) — is owned by `concept:fan-out`. Sub-claim acquisition is owned by `concept:fan-out` — the dispatch helper accepts already-acquired sub-claims as input and never calls the producer's split itself. Adjacent: `concept:run-scope`, `concept:delegation`, `concept:fan-out`, `concept:claim-tree`, `concept:node-run`, `concept:cascade`.

## Invariants

- `concept:run-scope` owns scope lifecycle and enumerates every path that closes a child execution context. This concept claims only what a settle primitive adds when it closes one — see the last invariant.
- Fan-out's clones share the calling node's template node-type, executor, and attribute schema — they ARE the same node, dispatched N times into N distinct run-scopes. Per-clone attribute writebacks do NOT aggregate onto the parent's attribute bag (per `concept:fan-out`).
- Sub-graph delegation's carry settle fires exactly once per invocation, on the designated exit's terminal; a sub-graph declares at most one exit by construction — the template grammar represents it as a single field, not a runtime-validated constraint.
- Entry absorption is a property of the delegation mechanism, not of child-execution as such — the dispatch helper does not compute it; the delegate template surface carries it.
- The parent-settlement cascade cannot be skipped by either settle path: the cascade bridge fires inside the settle primitive, not alongside it at call sites.
- The settle path's outcome carry — carry's exit-attribute writeback or aggregate's parent-claim Commit/Abandon — is atomic with closing the child execution context (invariant: exit-node-writeback).
