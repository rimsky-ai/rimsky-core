---
audit: lineage-subscriber-poller
artifact: decision:lineage-subscriber-poller
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# The bundled lineage subscriber polls the durable projection rather than registering for push

Supported. The subscriber's run loop is a ticker on a configured poll interval, and each tick queries the durable lineage table for rows ordered after its cursor, forwards each as an emitted event, and advances the cursor row by row. The cursor is itself durable: it lives in its own table keyed by namespace, is loaded at startup and seeded at the epoch when absent, and is written after each row rather than at batch end, so a restart resumes where it left off and records written while the subscriber was down are picked up on the next tick — which is the restart-safety the rationale claims. At-least-once holds by construction on the transient path: a send failure that is not a permanent rejection returns from the tick without advancing, so the row is retried; only a permanent rejection or an undecodable record is dead-lettered and stepped past, and each of those has its own test. Nothing in the subscriber registers with rimsky as a lifecycle subscriber — there is no registration call, no subscription state, and no inbound listener anywhere in its sources; its only rimsky-facing connection is the read-only database pool. An end-to-end test seeds the projection and asserts the poller emits from it.
