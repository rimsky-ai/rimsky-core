---
issue: conflict-node-run-at-most-one-vs-sequenced-mode
kind: audit
category: conflicting
artifacts:
  - concept:node-run
  - concept:cascade-mode
  - concept:wait-set
status: repaired
opened: 2026-07-25T21:11:30Z
---

# Do multiple stale rows coexist under sequenced cascade mode?

No — confirmed in `lib/runtime/gate_evaluator.go::evaluateOneGate`, which calls `HasAdvancedSiblingInScope` (checking for any other row in `{stale, running, held, parked}` for the same `(node, run_scope)`) unconditionally, after the per-mode rule runs and before the pending→stale transition — so this check applies identically to `most-recent`, `sequenced`, and both idempotent modes. A second round always waits in `pending` until the first fully settles (reaches `fresh`/`failed`, which are excluded from the advanced-sibling query). `concept:node-run`'s at-most-one-past-pending invariant was already stated correctly; `concept:cascade-mode`'s `sequenced` row ("becomes its own queued stale row alongside any prior queued stale rows") and `concept:wait-set`'s gate-predicate description (which named only the wait-set-drained and no-in-flight-upstream conjuncts) both omitted this and were the stale artifacts.

The rules determine the fix and it changes no commitment: `concept:node-run`'s invariant and the code already establish the one-past-pending-at-a-time rule; `sequenced`'s actual, still-true guarantee (`story:sequenced-preserves-cascade-rounds`'s claims — every round dispatches, in order, with its own inputs, none dropped or coalesced) is unaffected. Repaired per the mechanical-vs-judgment rule's named example — aligning stale sentences to the commitment the code and the counterpart artifact already agree on.

Changed:
- `.ok-planner/design/concepts/cascade-mode.md` — the `sequenced` row now describes rounds queuing in `pending` behind the advanced-sibling check, advancing to `stale` one at a time, with the "no round ever dropped or coalesced" guarantee stated as what `sequenced` actually provides.
- `.ok-planner/design/concepts/wait-set.md` — the "What it is" gate-evaluator description and the corresponding Invariants bullet now state the predicate as three conjuncts (wait-set drained, no in-flight subscribed upstream, no advanced sibling for the receiver's own `(node, run-scope)`), with the self-subscription exemption scoped explicitly to the upstream conjunct only.

Verified via code reading only (`lib/runtime/gate_evaluator.go`, `lib/foundation/persistence/postgres/nodes.go::HasAdvancedSiblingInScope`); docs-only change, no build/test impact.
