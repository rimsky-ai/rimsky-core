---
decision: non-cascade-direct-to-stale
status: as-is
aliases: []
---

# Non-cascade re-run paths skip pending entirely

## Choice

Operator-invalidate, fanout-parent recalculate, and message-delivery create a new node-run directly in state `stale` with `creation_reason` set to the appropriate value (`operator_invalidate`, `recalculate`, `message_delivery`). The new row carries:

- The carry-forward bag built at row creation (the immediately-prior run's persisted live bag).
- A fresh `sequence` number monotonic per (node, run-scope) per `decision:sequence-scope-monotonic`.
- No wait-set rows (the row never goes through pending and therefore never accumulates wait-set entries).

The cascade walker's accumulation rule (a) targets only the latest *cascade-driven* pending — non-cascade stales are not accumulation targets by definition, so no carve-out is needed in the walker. The dispatcher claims non-cascade stales in `sequence` order via the same serialization gate that orders cascade-driven stales. Per-template `cascade_mode` rules (most-recent, sequenced, idempotent-*) are scoped to `creation_reason = cascade`; non-cascade stales are immune to all of them — they are never deleted by most-recent and never deduped by the idempotent variants.

## Rationale

Non-cascade re-runs have a fundamentally different shape than cascade-driven re-runs. They originate from an explicit human action (operator-invalidate), a fanout-parent recalculate after a child completes, or an in-graph message-delivery that targets the receiver-node directly. In each case, the runtime knows the input bag the new run should see — the carry-forward from the immediately-prior run, optionally pre-mutated by an explicit `set_attribute` action (for operator-invalidate) or by the message envelope (for message-delivery). There is no wait-set to drain, no upstream cascade to overlay, and no mode rule that could justify dropping the row. Policy-retry is NOT a non-cascade re-run path — it is handled in-place on the existing node-run row (see `decision:in-place-retry`); it never creates a new row.

Pushing these paths through `pending` would require a fake-drain mechanism (a wait-set with no real cascade behind it, or an immediate gate-evaluation that bypasses the normal trigger) and would force the runtime to consider whether mode rules apply (which they shouldn't — operator action should not be silently coalesced by most-recent, a recalculate after a child completes should not be silently deduped, and a message delivery should not be silently dropped by idempotent variants). Direct-to-stale avoids both: the runtime computes the bag at creation, persists it on the run's attribute bag (per `concept:attribute`), sets state=stale and writes the row.

The walker rule and the mode rule both naturally exclude non-cascade rows by scoping to `creation_reason = cascade`. No special-case code paths are needed at the walker, gate evaluator, or dispatcher — just the single column distinction.

## Alternatives

Route non-cascade through pending with synthetic wait-set rows that immediately drain — rejected as overengineering. The synthetic wait-set rows are dead weight (they exist only to trigger the drain event that bypasses real drain logic), and the mode-rule interaction has to be explicitly suppressed anyway.

Route non-cascade through pending and let mode rules apply uniformly — rejected because operator-invalidate must not be silently dropped by most-recent's "delete prior cascade-stale" rule. The operator explicitly asked for a re-run; coalescing it with a cascade is wrong UX.

Make non-cascade re-runs interrupt the in-flight predecessor (operator-invalidate as "kill in-flight and run") — rejected because operator-invalidate is the routine, non-destructive path: it queues a stale row that dispatches when the predecessor settles. Destructive cancellation of an in-flight run lives at `instance_killed`; the two operator verbs are distinct by design and the cascade-mode / walker surfaces are not the place for a destructive variant.
