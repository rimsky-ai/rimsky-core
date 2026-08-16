---
audit: synthetic-envelope-mechanism-retired
artifact: decision:synthetic-envelope-mechanism-retired
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:37:59Z
---

# No synthetic-envelope wake chokepoint anywhere; receivers wake only through the subscriber-side walk

Supported, and the absences are real. Searched the whole tree — Go sources, every proto file, and every migration — for the named artifacts: there is no wake-node-id field, no wait-set-pair field, and no synthetic-envelope construct in any of them, so there is no wire payload field to guard, no receipt-side reserved-field check, and no frame-engine reader at promotion. The one reserved-name check that does exist at registration reserves the empty message type for the implicit root-trigger mechanism, a different thing entirely. The positive half holds too: the cascade walk loads a per-template subscription edge map — a prefix trie built from both the declared subscriptions and the template's message references, so the augmented form — and matches the settling signal against it to find receivers, and that is the only path by which a receiver is woken. Each premise of the rationale checks out independently: instance creation only inserts nodes and enqueues no message, with a test asserting the ledger is empty afterwards; no asset-materialize route is registered anywhere; and node reset clears the failure marker and writes an audit event without transitioning state or enqueuing a dispatch.
