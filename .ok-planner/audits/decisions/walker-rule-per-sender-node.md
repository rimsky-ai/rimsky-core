---
audit: walker-rule-per-sender-node
artifact: decision:walker-rule-per-sender-node
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:46Z
---

# Cascade accumulation keys on sender-node identity, not on pending-existence

Supported. `ensureCascadePending` (`lib/runtime/cascade_walker.go`) finds the receiver's latest cascade-driven pending and accumulates into it unless the incoming sender's node already appears among the wait-set rows attached to that pending (`ListSenderNodesForReceiver`), in which case it opens a new pending via `CreateCascadePending` instead — exactly the described rule. `TestEnsureCascadePending_PerSenderNodeRule` directly drives all three cases against a real sqlite-backed store: no pending exists → new pending created; a different sender-node arrives while a pending exists → accumulates into the same pending (diamond case); the same sender-node's node-type arrives again after already being covered in the pending's wait-set → a new, distinct pending is created (round boundary). `resolveReceiverRunForCascade`'s within-turn memoization is exercised by the same test path and by the broader cascade e2e suite (e.g. `test/scenarios/cascade_two_node_backedge_in_frame_test.go`, `cascade_two_node_backedge_via_attribute_test.go`).
