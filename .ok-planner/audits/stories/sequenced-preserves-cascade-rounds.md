---
audit: sequenced-preserves-cascade-rounds
artifact: story:sequenced-preserves-cascade-rounds
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Sequenced mode dispatches once per cascade round

Supported. A node-type opts into `cascade_mode: sequenced` (`concept:cascade-mode`), under which the gate evaluator never drops or coalesces a queued round; `lib/runtime/substitution_context.go::pinnedSenderRunsForReceiver` resolves each round's substitution bag from the specific sender run its wait-set row pins, so distinct rounds never share a bag. `test/scenarios/sequenced_preserves_cascade_rounds_test.go` checks this end-to-end: an upstream self-cascades through 3 attribute-changed rounds while a sequenced-mode downstream subscriber is asserted to dispatch exactly 3 times, in arrival order, with each dispatch's substituted `snapshot_x` value matching that specific round's upstream value (r1, r2, r3) — proving no two dispatches see the same bag and no round is coalesced away.
