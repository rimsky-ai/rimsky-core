---
story: operator-invalidate-queues-during-flight
status: as-is
---

# Operator-invalidate during in-flight produces a queued stale row

## Story

As an operator forcing a re-run of a node that currently has an in-flight node-run, I can know that my invalidate produces a stale row that dispatches after the in-flight predecessor settles. The in-flight run is not interrupted; my invalidate is not silently dropped.

When operator-invalidate fires against a node that has an in-flight run, the runtime creates a new node-run directly in state `stale` whose creation reason is operator-invalidate, a fresh sequence number, and the carry-forward bag (the predecessor's persisted live bag at the moment of invalidate). The dispatcher's serialization gate prevents claiming this stale row until the in-flight predecessor settles to a terminal state. After the predecessor settles, the operator's stale row is claimed in sequence order.

Operator-invalidate is an explicit human action. Silently dropping it (because something is in-flight) would leave the operator without feedback that their action took effect. Interrupting the in-flight run (force-killing it) is the destructive alternative and is not the default operator UX — that's `instance_killed`. Queuing the operator-invalidate as a stale row that dispatches in order honors both invariants: the in-flight run is sealed, and the operator's action produces an observable dispatch when the in-flight run finishes.
