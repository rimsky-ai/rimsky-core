---
audit: walker-rule-per-sender-node
artifact: decision:walker-rule-per-sender-node
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:36:14Z
---

# Whether the cascade walker's accumulate-or-queue gate keys on sender-node identity

Supported. The walker's gate reads exactly as the decision describes: it locks the receiver, finds the receiver's latest cascade-driven pending, lists the sender nodes already holding wait-set rows against that pending, and creates a fresh pending only when the arriving sender's node is among them — otherwise it returns the existing pending as the accumulation target. A unit test drives all three branches on a real database: no pending creates one, a second distinct sender node accumulates into it (the diamond case), and a repeat of the first sender's node opens a new one (the round boundary). One refinement the decision does not name sits ahead of the node check — a sender *run* that already holds a row against that pending accumulates rather than sealing, which is an idempotency guard for a re-walk, not a competing rule. The rest of the claim holds too: nothing constrains the number of cascade-driven pendings per receiver and scope, each pending carries its own wait-set rows keyed by receiver run, and the dispatcher's ordering is pinned by a cross-driver parity case asserting that with two coexisting pendings the in-flight lookup returns the sequence-earliest row. Roughly thirty annotated sites across the runtime, both persistence drivers, and the parity suite carry this rule, and the parity suite exercises the two walker primitives against both drivers.
