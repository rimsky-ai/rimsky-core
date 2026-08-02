---
audit: held-abandon-cascades-abandoned
artifact: story:held-abandon-cascades-abandoned
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# Non-member subscribers see terminal/error/abandoned only at the abandon moment, never earlier

Supported. `lib/runtime/held_cascade_defer.go::transitionHolderIfFullyResolved` computes the held-claim's poisoned aggregate and, on poison, transitions the acquirer to `failed` and emits `terminal/error/abandoned` filtered to non-subgraph-members only (`subgraphNonMemberFilter`); the member-filtered immediate cascade at the held moment is a separate, distinct emission. `test/scenarios/held_abandon_cascades_abandoned_test.go` proves this directly: a non-member `observer` subscribed to `acquirer`'s `terminal/error/abandoned` has zero node-runs while the acquirer sits in state `held`, dispatches only after the acquirer's audited terminal event (exactly one: `terminal/error/abandoned`, no held-moment audit row), and its own `terminal/success` timestamp is never earlier than the acquirer's abandon timestamp. A same-subgraph member (`inheritor`, also subscribed to the acquirer's `terminal/*`) is checked to run exactly once, ruling out a double-fire through the non-member filter.
