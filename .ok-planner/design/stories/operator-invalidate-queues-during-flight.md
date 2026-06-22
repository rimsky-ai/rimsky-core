---
story: operator-invalidate-queues-during-flight
status: as-is
---

# Operator-invalidate during in-flight produces a queued stale row

## Role

As an operator forcing a re-run of a node that currently has an in-flight node-run, I can know that my invalidate produces a stale row that dispatches after the in-flight predecessor settles. The in-flight run is not interrupted; my invalidate is not silently dropped.

## Capability

When operator-invalidate fires against a node that has an in-flight run, the runtime creates a new node-run directly in state `stale` whose creation reason is operator-invalidate, a fresh sequence number, and the carry-forward bag (the predecessor's persisted live bag at the moment of invalidate). The dispatcher's serialization gate prevents claiming this stale row until the in-flight predecessor settles to a terminal state. After the predecessor settles, the operator's stale row is claimed in sequence order.

## Business value

Operator-invalidate is an explicit human action. Silently dropping it (because something is in-flight) would leave the operator without feedback that their action took effect. Interrupting the in-flight run (force-killing it) is the destructive alternative and is not the default operator UX — that's `instance_killed`. Queuing the operator-invalidate as a stale row that dispatches in order honors both invariants: the in-flight run is sealed, and the operator's action produces an observable dispatch when the in-flight run finishes.

## Acceptance

An operator invokes the invalidate-node verb against node B while B has an in-flight run (parked, held, or running). The runtime creates a stale row for B whose creation reason is operator-invalidate, with the next sequence number. The test asserts the stale row exists and is not claimed. B's in-flight run settles. The dispatcher claims the operator's stale row in sequence order. B dispatches a second time with the operator's row as the dispatched row; lineage records the row's creation reason. Observable as: B's lineage shows two distinct runs, the second carries the operator-invalidate creation reason, and its dispatch timestamp is after the first run's terminal timestamp.

## Falsifier

The operator's invalidate is silently dropped (no new run created) — observable by inspecting B's lineage. OR the operator's invalidate kills the in-flight predecessor — observable by checking the predecessor's terminal outcome for an unexpected instance-killed terminal. OR the operator's stale row dispatches before the predecessor settles — observable by comparing dispatch timestamps.

## Proof

An executable scenario test where B's first run is parked, operator invokes the invalidate-node verb against B, the test asserts a new run exists with the operator-invalidate creation reason, the parked run is woken (via deadline or other path) and settles, the operator's run dispatches with its carry-forward bag intact.
