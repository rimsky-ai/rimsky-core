---
tension: delegation-and-fanout-share-runtime-primitive
category: duplicated
status: open
affects:
  - delegation
  - fan-out
  - run-scope
references:
  - ../../sketches/2026-05-23-unify-child-execution-sketch.md
---

# Delegation and fan-out are the same run-side primitive with two template surfaces

## What is muddy

`concept:delegation` and `concept:fan-out` are documented as two separate concepts and implemented in two parallel files (`code:runtime/subgraph_dispatch.go` for delegation; `code:runtime/fanout_dispatch.go` for fan-out). But on the run side they do the same thing:

- Both allocate a new RunScope rooted at the parent run (`parent_run_id` + `parent_run_scope_id` set; per `concept:run-scope`).
- Both dispatch one or more child run rows into the new RunScope.
- Both settle the parent via aggregating child outcomes and closing the child RunScope(s) at settlement.

The only structural differences are:

| Aspect                | Delegation           | Fan-out                                  |
| --------------------- | -------------------- | ---------------------------------------- |
| Partition count       | 1                    | N (producer-decided via `SplitClaimScope`) |
| `partition_key`       | `""`                 | non-empty per child                      |
| Aggregation policy    | "carry verbatim"     | author-specified (strict/threshold/best_effort/first) |
| Entry absorption      | yes (calling node IS entry) | no                                |
| Claim-tree sub-claims | none                 | one per partition                        |

The first three are degenerate cases of a unified policy/cardinality model. The latter two are genuine asymmetries.

Two emission sites (`applyTerminalCompleteSubgraphCaller`, `CreateFanOutChildren`) and two settlement paths (`CarryExitWriteback`, `resolveParentClaimChain`) implement what could be one primitive parameterized by partition descriptors + aggregation policy.

## Why it matters

- Bug-fix duplication: defects in one path (e.g., the cascade bridge missing from `applyTerminalCompleteSubgraphExit` discovered during Phase F2, or the partition-RunScope-closure-on-aggregation-settlement work) need parallel fixes in the other.
- Concept-doc duplication: `concept:delegation` and `concept:fan-out` re-state the same RunScope tree shape, the same closure-on-rendezvous invariant, the same parent-settlement cascade contract.
- Mental-model cost: a new contributor learning rimsky encounters "delegation" and "fan-out" as two things to internalize when the run-side is one shape.
- The just-landed RunScope-first reshape (per `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`) makes the unification newly tractable — pre-RunScope, the two had different inline disambiguators on `rimsky_node_runs` and weren't structurally parallel. Now they are.

## Resolution candidates (do NOT pick)

- Introduce a `child-execution` concept that owns the shared invariants; demote `delegation` and `fan-out` to "invocation patterns" docs that describe the template surface + the canonicalizer-time defaults they imply.
- Introduce a `DispatchChildExecution` + `SettleChildExecution` runtime primitive pair that both `applyTerminalCompleteSubgraphCaller` (or its successor) and `CreateFanOutChildren` (or its successor) call into; `CarryExitWriteback` collapses into `SettleChildExecution` with a `CarryVerbatim` aggregation policy.
- Keep `concept:delegation` and `concept:fan-out` but reframe both as referencing a shared cross-cutting concept; do the runtime unification without the concept-doc reshape.
- Leave the duplication; accept the cost.

A sketch at `.ok-planner/sketches/2026-05-23-unify-child-execution-sketch.md` walks the unified shape, the migration story (no schema change — `partition_key` is already the discriminator), and open questions including entry-absorption asymmetry, naming, and the timing of the refactor relative to the just-landed reshape stabilizing.

## Notes

- 2026-05-23 — Captured during walkthrough of divergences from the 2026-05-22 fan-out-safety-scope-first plan. The user observed: "a single sub-graph call should be a fan-out of one. may be an opportunity to reduce code paths so fan-out isn't a separate case. it's just triggered by a partition, but otherwise the subgraph machinery is the same." Sketch produced alongside this entry.
