---
tension: delegation-and-fanout-share-runtime-primitive
category: duplicated
status: open
affects:
  - delegation
  - fan-out
  - run-scope
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

A unification would need no schema change — `partition_key` is already the discriminator between the delegation (`""`) and fan-out (non-empty per child) shapes.

Two emission sites (`applyTerminalCompleteSubgraphCaller`, `CreateFanOutChildren`) and two settlement paths (`CarryExitWriteback`, `resolveParentClaimChain`) implement what could be one primitive parameterized by partition descriptors + aggregation policy.

## Why it matters

- Bug-fix duplication: defects in one path (e.g., the cascade bridge missing from `applyTerminalCompleteSubgraphExit` discovered during Phase F2, or the partition-RunScope-closure-on-aggregation-settlement work) need parallel fixes in the other.
- Concept-doc duplication: `concept:delegation` and `concept:fan-out` re-state the same RunScope tree shape, the same closure-on-rendezvous invariant, the same parent-settlement cascade contract.
- Mental-model cost: a new contributor learning rimsky encounters "delegation" and "fan-out" as two things to internalize when the run-side is one shape.
- The RunScope-first structure makes the unification tractable — without RunScope, the two have different inline disambiguators on the node-run row and aren't structurally parallel. With it, they are.

## Resolution candidates (do NOT pick)

- Introduce a `child-execution` concept that owns the shared invariants; demote `delegation` and `fan-out` to "invocation patterns" docs that describe the template surface + the canonicalizer-time defaults they imply.
- Introduce a unified dispatch-child / settle-child runtime primitive pair that both delegation and fan-out route through; the delegation carry-rule collapses into the settle-child path expressed as a verbatim-carry aggregation policy.
- Keep `concept:delegation` and `concept:fan-out` but reframe both as referencing a shared cross-cutting concept; do the runtime unification without the concept-doc reshape.
- Leave the duplication; accept the cost.
