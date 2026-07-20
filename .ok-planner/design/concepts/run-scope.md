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

Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (an ad-hoc parent-run plus child-key pair carried on each node-run row); persists the parent-chain a depth-gating query could walk, complementing the canonicalizer-level recursion rejection per `concept:sub-graph`; enables agentic-executor recovery handoff via a prior-/current-dispatch handoff protocol.

## Boundaries

Owns: the per-RunScope node-run set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes inside one frame); the frame itself (a RunScope lives inside exactly one frame — see `concept:frame`; the frame owns the root RunScope of its tree).

Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

## Invariants

- A RunScope lives inside exactly one frame; RunScopes never span frames. A frame is a tree of RunScopes rooted at the frame's root RunScope.
- RunScope rows inserted eagerly in the tx that triggers them: root at frame start (in the same tx as the frame row insert); subgraph at calling-node success terminal. The fan-out-partition RunScope is created by the shared child-execution dispatch helper (`concept:child-execution`) in its own transaction, after split-scope sub-claim acquisition — not as part of the acquisition transaction itself.
- A RunScope is root iff it has no parent RunScope and no parent run; the persistence layer enforces that the two parent pointers stand or fall together. The frame row references the root RunScope; cascade walks and message delivery within the frame read the root from the frame.
- A non-empty partition key identifies a fanout_partition RunScope; only one such partition may be open per (parent run, partition key), enforced at the storage layer.
- Any RunScope can spawn child RunScopes (sub-graph or fan-out); the child's parent is whatever RunScope created it, not necessarily the root.
- A closed RunScope means parent-run rendezvous has fired (sub-graph carry-rule or fan-out aggregation), the owning frame settled (root — closed in the same transaction that ends the frame, along with any straggler open child left behind by a cut-short rendezvous), or the owning instance was administratively terminated — termination walks each frame's full RunScope tree, closing every remaining open RunScope children-before-parents, root and child alike. The lazy-allocation primitive refuses to allocate into a closed RunScope, surfacing a closed-scope error. Cascade walker reaching INTO a closed RunScope is a bug.
- Every closure fires the scope's run-scope-terminal peer fan-out — at rendezvous for child scopes, at settlement for the root (terminal reason `frame_settled`), and at administrative termination for whatever the kill cut short. Delivery is exactly-once per scope per peer via the lifecycle-idempotency ledger; termination re-offers the fan-out for already-closed scopes in the tree so a peer that failed an earlier delivery still hears, and dedupe makes the re-offer a no-op for peers already acknowledged.
- The lazy-allocation primitive that affirms a node-run row is the allocation entry point; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
- Depth gating: the parent-chain a runtime walk would need to reject a sub-graph creating a RunScope already present at any depth is persisted and queryable, but no runtime caller performs that walk. The canonicalizer's static sub-graph-recursion rejection per `concept:sub-graph` is the sole enforced defense against sub-graph recursion.
- Carry-forward of attributes is intra-RunScope only. Because RunScopes never span frames, carry-forward is therefore also intra-frame; the runtime never carries attribute state across frame boundaries.
