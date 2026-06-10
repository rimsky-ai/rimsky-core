---
concept: run-scope
status: as-is
aliases: []
---

# RunScope

## What it is

RunScope is the first-class execution context for one graph instantiation (main / subgraph / fanout_partition). Persisted as a run-scope ledger row. Each RunScope owns a set of node-run rows (the **RunSheet** in operator prose). RunScopes form a tree via `parent_run_scope_id`.

Three kinds:

- **Main RunScope:** the top-level graph instantiation. One per instance. No parent.
- **Sub-graph RunScope:** a sub-graph invoked via a calling node's `delegate:`. Parent = the calling node's RunScope; parent run = the calling node's run.
- **Fan-out partition RunScope:** one per partition emitted by a fan-out node's split-scope operation. Parent = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty `partition_key`.

Kind is derivable, not stored: `parent_run_scope_id IS NULL` → main; `partition_key != ''` → fanout_partition; else subgraph.

## Purpose

Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (a `parent_run_id` + `child_key` pair carried on each node-run row); enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph` as runtime defense-in-depth); enables agentic-executor recovery handoff via the `prior_dispatch_id` / `current_dispatch_id` protocol.

## Boundaries

Owns: the per-RunScope node-run set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes); frame semantics (frames and RunScopes are orthogonal — see `concept:frame`).

Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

## Invariants

- RunScope rows inserted eagerly in the tx that triggers them: main at instance creation; subgraph at calling-node success terminal; fanout_partition at split-scope sub-claim acquisition, per `@blessed-invariant 10`.
- `parent_run_scope_id IS NULL ⇔ parent_run_id IS NULL ⇔ main RunScope`. Enforced by a table CHECK constraint requiring a main RunScope to carry no parents.
- `partition_key != ''` iff fanout_partition; uniqueness of open fanout_partition per `(parent_run_id, partition_key)` enforced by a partial unique index over open fan-out partitions.
- `closed_at IS NOT NULL` means parent-run rendezvous has fired (sub-graph carry-rule, fan-out aggregation, or instance termination). The lazy-allocation primitive refuses to allocate into a closed RunScope, surfacing a closed-scope error. Cascade walker reaching INTO a closed RunScope is a bug.
- The lazy-allocation primitive that affirms a node-run row is the allocation entry point; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
- Depth gating: runtime safety net that rejects a sub-graph creating a RunScope already present in the parent chain at any depth. The canonicalizer's static sub-graph-recursion rejection per `concept:sub-graph` is the primary; this is defense-in-depth.
