---
concept: run-scope
status: as-is
aliases: []
---

# RunScope

## What it is

RunScope is the first-class execution context for one graph instantiation (main / subgraph / fanout_partition). Persisted as a run-scope ledger row. Each RunScope owns a set of node-run rows (the **RunSheet** in operator prose). RunScopes form a tree via their parent-RunScope pointer.

Three kinds:

- **Main RunScope:** the top-level graph instantiation. One per instance. No parent.
- **Sub-graph RunScope:** a sub-graph invoked via a calling node's delegate directive. Parent = the calling node's RunScope; parent run = the calling node's run.
- **Fan-out partition RunScope:** one per partition emitted by a fan-out node's split-scope operation. Parent = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty partition key.

Kind is derivable, not stored: no parent-RunScope pointer means main; a non-empty partition key means fanout_partition; otherwise subgraph.

## Purpose

Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (an ad-hoc parent-run plus child-key pair carried on each node-run row); enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph` as runtime defense-in-depth); enables agentic-executor recovery handoff via a prior-/current-dispatch handoff protocol.

## Boundaries

Owns: the per-RunScope node-run set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes); frame semantics (frames and RunScopes are orthogonal — see `concept:frame`).

Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

## Invariants

- RunScope rows inserted eagerly in the tx that triggers them: main at instance creation; subgraph at calling-node success terminal; fanout_partition at split-scope sub-claim acquisition, per invariant 10.
- A RunScope is main iff it has no parent RunScope and no parent run; the persistence layer enforces that the two parent pointers stand or fall together.
- A non-empty partition key identifies a fanout_partition RunScope; only one such partition may be open per (parent run, partition key), enforced at the storage layer.
- A closed RunScope means parent-run rendezvous has fired (sub-graph carry-rule, fan-out aggregation, or instance termination). The lazy-allocation primitive refuses to allocate into a closed RunScope, surfacing a closed-scope error. Cascade walker reaching INTO a closed RunScope is a bug.
- The lazy-allocation primitive that affirms a node-run row is the allocation entry point; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
- Depth gating: runtime safety net that rejects a sub-graph creating a RunScope already present in the parent chain at any depth. The canonicalizer's static sub-graph-recursion rejection per `concept:sub-graph` is the primary; this is defense-in-depth.
