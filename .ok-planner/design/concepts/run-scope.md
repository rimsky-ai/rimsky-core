---
concept: run-scope
status: as-is
aliases: []
references:
  - ../../specs/2026-05-22-fan-out-safety-scope-first-design.md
---

# RunScope

## What it is

RunScope is the first-class execution context for one graph instantiation (main / subgraph / fanout_partition). Persisted as `rimsky_run_scopes`. Each RunScope owns a set of `rimsky_node_runs` rows (the **RunSheet** in operator prose). RunScopes form a tree via `parent_run_scope_id`.

Three kinds:

- **Main RunScope:** the top-level graph instantiation. One per instance. No parent.
- **Sub-graph RunScope:** a sub-graph invoked via a calling node's `delegate:`. Parent = the calling node's RunScope; parent run = the calling node's run.
- **Fan-out partition RunScope:** one per partition emitted by a fan-out node's `SplitScope`. Parent = the fan-out node's RunScope; parent run = the fan-out node's run; carries a non-empty `partition_key`.

Kind is derivable, not stored: `parent_run_scope_id IS NULL` → main; `partition_key != ''` → fanout_partition; else subgraph.

## Purpose

Uniform representation of execution contexts; eliminates the bug class of inline-disambiguator drift (`parent_run_id` + `child_key` on `rimsky_node_runs`); enables depth-gating via parent-chain walks (complementing canonicalizer-level recursion rejection per `concept:sub-graph` as runtime defense-in-depth); enables agentic-executor recovery handoff via the `prior_dispatch_id` / `current_dispatch_id` protocol.

## Boundaries

Owns: the per-RunScope `rimsky_node_runs` set; RunScope lifecycle (creation / closure); parent-RunScope / parent-run relationships.

Does NOT own: claim semantics (parallel structure via `concept:claim-tree`); cascade-edge semantics (`concept:cascade` traverses subscription edges within and across RunScopes); frame semantics (frames and RunScopes are orthogonal — see `concept:frame`).

Adjacent: `concept:fan-out`, `concept:delegation`, `concept:frame`, `concept:claim-tree`, `concept:cascade`, `concept:node-run`.

## Invariants

- RunScope rows inserted eagerly in the tx that triggers them: main at instance creation; subgraph at calling-node success terminal (`code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller`); fanout_partition at SplitScope sub-claim acquisition (`code:runtime/runner_subclaim.go::AcquireSubClaims`) per `@blessed-invariant 10`.
- `parent_run_scope_id IS NULL ⇔ parent_run_id IS NULL ⇔ main RunScope`. Enforced by the table's CHECK constraint `run_scope_main_has_no_parents`.
- `partition_key != ''` iff fanout_partition; uniqueness of open fanout_partition per `(parent_run_id, partition_key)` enforced by `uq_run_scopes_fanout_partition_open`.
- `closed_at IS NOT NULL` means parent-run rendezvous has fired (sub-graph carry-rule, fan-out aggregation, or instance termination). `AffirmNodeRunRow` returns `ErrRunScopeClosed`. Cascade walker reaching INTO a closed RunScope is a bug.
- `AffirmNodeRunRow` is the lazy-allocation primitive; callers must not depend on its return value beyond error/no-error (preserves lazy↔eager rewrite property).
- Depth gating: runtime safety net that rejects a sub-graph creating a RunScope already present in the parent chain at any depth. The canonicalizer's static `subgraph_recursion_unsupported` rejection per `concept:sub-graph` is the primary; this is defense-in-depth.

## Annotation sites

- `code:foundation/persistence/postgres/run_scopes.go`, `code:foundation/persistence/sqlite/run_scopes.go` — backend impls.
- `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` — sub-graph RunScope creation.
- `code:runtime/runner_subclaim.go::AcquireSubClaims` — fan-out partition RunScope creation.
- `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` — cascade walker carries RunScope.
- `code:runtime/callback.go::driveTerminal` — callback resolves RunScope via dispatch_id.

## Notes

- 2026-05-22 — Created per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`.
- 2026-05-23 — Cascade walker membership refinement: when the sender lives in a non-main RunScope (sub-graph, fanout_partition), `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTx` MUST NOT lazy-allocate run rows for receivers via `AffirmNodeRunRow`. Non-main RunScopes are CLOSED contexts: a receiver belongs to the sender's scope only if it already has an in-flight row there (dispatched explicitly by the sub-graph caller's internal cascade or by `CreateFanOutChildren`). Receivers without a same-scope row live in some ancestor scope (typically main) and are handled by the cross-scope bridge in `code:runtime/state_propagation.go::PropagateIfChildAfterTerminal` when the parent settles. The lazy-allocation discipline of `AffirmNodeRunRow` applies only to main RunScopes. Cross-scope bridge must also drain wait-set rows for the just-settled parent (cascade-then-drain pattern mirrors the standard `applyTerminalComplete` path).
