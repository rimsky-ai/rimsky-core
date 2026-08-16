---
audit: message
artifact: concept:message
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:15:12Z
---

# The typed envelope: three send sites, one message per frame, receiver-node delivery, and universal idempotency

Supported. All nine invariants hold, as do the envelope shape and the idempotency section. Three send sites exist and no more — the operator endpoint, the same endpoint under publisher attribution with a live-subscription capability check, and a message-sender node's dispatch — and all three converge on one enqueue function that rejects an unknown sender-kind outside the three origin classes, refuses a message with no sender, and inserts the single ledger row; every other insertion in the tree is a test seed. The envelope carries identity, target instance, body, receipt timestamp and sender attribution, and no routing field. One message per frame holds by construction: delivery resolves the frame's single triggering message, returns nothing when that row is already delivered or cancelled, and marks it delivered under a conditional update, so a coalesce-cancelled trigger can never spawn a receiver run. Administrative termination cancels the instance's pending queue inside the same transaction that force-fails in-flight runs, ends open frames and marks the instance terminated, and the send endpoint rejects a message to a terminated instance. Instance creation materializes one receiver node per declared type plus one for the implicit empty type, each with an empty executor; delivery creates a non-cascade stale run against that node with the message-delivery creation reason, populates its attribute bag and dispatch input bag from the body before settle, and the scheduler settles it through the empty-executor pure-cascade path; a missing receiver node writes a dead-letter audit row and creates no run. Idempotency is required on every send, keyed over instance, requester identity and key under a uniqueness constraint, returns the original message identity on conflict with the same body shape under a different status code, and its rows are swept on a configurable trailing window inside the scheduler-tick advisory lock.

## Compliance

- Self-containment: the body names two physical storage tables (the node ledger and the node-attribute row) by their schema identifiers; the compliant text states them as "the instance's node ledger" and "the same node-attribute row", leaving the physical names to the code.
