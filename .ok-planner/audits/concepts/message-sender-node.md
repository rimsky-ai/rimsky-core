---
audit: message-sender-node
artifact: concept:message-sender-node
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# Send-nodes: exact-shape validation, envelope construction, and the sole in-graph frame-opening path

Supported. All four invariants hold. The template validator enforces the exact attribute-schema-to-body-schema match in both directions — extra attribute, missing attribute, type mismatch, and required-set mismatch each produce a registration error, and unit tests cover all four plus the unknown-message-type rejection and the mutual exclusions with executor and delegate. At dispatch the runtime stamps instance-origin sender attribution naming the dispatching instance and a deterministic idempotency key derived from the dispatching node and frame, inserts the idempotency record and the envelope together in a transaction of its own — the send callback is invoked with no ambient transaction, so it opens and commits its own — and returns the original message id on a replay rather than inserting twice; unit tests cover the rollback atomicity, the replay-no-double-insert, and the distinct-node-frame-pair cases, and two end-to-end scenario tests drive a cascade send through a live stack. The claim that these nodes are the only in-graph path that opens a frame was checked by enumerating every caller of the message-enqueue primitive across the library and command trees: two exist, this one and the control API's external send endpoint, which is the operator/publisher path the concept names as out-of-graph. The builtin send-message alias guard is present and tested: a node naming the alias directly as its executor without a send declaration is rejected at registration, while the same alias reached through the send declaration validates cleanly.
