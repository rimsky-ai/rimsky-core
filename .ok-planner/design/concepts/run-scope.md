---
concept: run-scope
status: as-is
aliases: []
---

# RunScope

## What it is

RunScope is the first-class execution context for one graph instantiation (root / sub-graph / fanout_partition) inside a single frame. Persisted as a run-scope ledger row. Each RunScope owns a set of node-run rows (the **RunSheet** in operator prose). RunScopes form a tree via their parent-RunScope pointer, rooted at the frame's root RunScope.

Three kinds:

- **Root RunScope:** the frame's top-level execution context. One per frame. No parent RunScope, no parent run. The frame row carries a non-null reference to its root RunScope.
- **Sub-graph RunScope:** a sub-graph invoked via a calling node's delegate directive. Parent = the calling node's RunScope (whatever RunScope the caller itself lives in — may be the frame's root or any descendant); parent run = the calling node's run.
- **Fan-out partition RunScope:** one per partition emitted by a fan-out node's split-scope operation. Parent = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty partition key.

Kind is derivable, not stored: no parent-RunScope pointer means root; a non-empty partition key means fanout_partition; otherwise subgraph.

## Purpose

Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (an ad-hoc parent-run plus child-key pair carried on each node-run row); enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph` as runtime defense-in-depth); enables agentic-executor recovery handoff via a prior-/current-dispatch handoff protocol.

## Boundaries

Owns: the per-RunScope node-run set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes inside one frame); the frame itself (a RunScope lives inside exactly one frame — see `concept:frame`; the frame owns the root RunScope of its tree).

Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

## Invariants

- A RunScope lives inside exactly one frame; RunScopes never span frames. A frame is a tree of RunScopes rooted at the frame's root RunScope.
- RunScope rows inserted eagerly in the tx that triggers them: root at frame start (in the same tx as the frame row insert); subgraph at calling-node success terminal; fanout_partition at split-scope sub-claim acquisition, per invariant 10.
- A RunScope is root iff it has no parent RunScope and no parent run; the persistence layer enforces that the two parent pointers stand or fall together. The frame row references the root RunScope; cascade walks and message delivery within the frame read the root from the frame.
- A non-empty partition key identifies a fanout_partition RunScope; only one such partition may be open per (parent run, partition key), enforced at the storage layer.
- Any RunScope can spawn child RunScopes (sub-graph or fan-out); the child's parent is whatever RunScope created it, not necessarily the root.
- A closed RunScope means parent-run rendezvous has fired (sub-graph carry-rule or fan-out aggregation) or the owning frame has ended (root). The lazy-allocation primitive refuses to allocate into a closed RunScope, surfacing a closed-scope error. Cascade walker reaching INTO a closed RunScope is a bug.
- The lazy-allocation primitive that affirms a node-run row is the allocation entry point; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
- Depth gating: runtime safety net that rejects a sub-graph creating a RunScope already present in the parent chain at any depth. The canonicalizer's static sub-graph-recursion rejection per `concept:sub-graph` is the primary; this is defense-in-depth.
- Carry-forward of attributes is intra-RunScope only. Because RunScopes never span frames, carry-forward is therefore also intra-frame; the runtime never carries attribute state across frame boundaries.
