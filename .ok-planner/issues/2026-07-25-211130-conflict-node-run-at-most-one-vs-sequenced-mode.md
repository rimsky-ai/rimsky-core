---
issue: conflict-node-run-at-most-one-vs-sequenced-mode
kind: audit
category: conflicting
artifacts:
  - concept:node-run
  - concept:cascade-mode
  - concept:wait-set
status: verified
opened: 2026-07-25T21:11:30Z
---

# Two concepts describe sequenced cascade mode as stacking stale runs; the gate evaluator forbids it

A node-run is one attempt at running a node. The node-run concept states a hard invariant: at most one run per (node, run-scope) is past `pending` at any moment, enforced at the gate evaluator's pending-to-stale precondition. The cascade-mode concept's description of `sequenced` mode contradicts that — "every queued round dispatches... alongside any prior queued stale rows" — and the wait-set concept's account of the gate predicate omits the advanced-sibling check entirely.

The code sides with the node-run invariant: the gate evaluator checks for an advanced sibling unconditionally, in every mode (`code:lib/runtime/gate_evaluator.go::evaluateOneGate`), so a second round waits in `pending` until the first fully settles. What `sequenced` actually guarantees is that no round is ever dropped or coalesced — each pending round takes its turn — not that stale rows coexist. The sequenced-preserves-cascade-rounds story's observable assertions (every round dispatches, in order, with its own inputs) remain true; only the "coexist" prose is wrong.

## Options

- Correct `concept:cascade-mode`'s sequenced row and `concept:wait-set`'s predicate description to the one-past-pending-at-a-time truth. Cost: sprint work only.
- Change the code to allow coexisting stale rows — a semantic change nothing motivates, and one the at-most-one invariant exists to prevent.

## Ruling

> Generated ruling (/verify-issues): amend `concept:cascade-mode` (sequenced mode
> queues rounds in pending, one past-pending at a time, none dropped) and
> `concept:wait-set` (add the advanced-sibling clause to the gate predicate) to match
> the code-verified invariant in `concept:node-run`, which is correct as written.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
