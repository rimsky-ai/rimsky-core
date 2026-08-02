---
audit: held-commit-cascades-success
artifact: story:held-commit-cascades-success
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# Non-member subscribers see terminal/success only at the commit moment, never at the provisional held moment

Supported. The same `transitionHolderIfFullyResolved` engine that resolves the abandon case resolves commit: a fully-resolved, unpoisoned held claim transitions the acquirer to `fresh` and emits `terminal/success` filtered to non-subgraph-members. `test/scenarios/held_commit_cascades_success_test.go` proves an `observer` non-member subscribed to `acquirer`'s `terminal/success` has zero node-runs while the acquirer is in state `held` (gated by a still-in-flight co-holder), dispatches only after the acquirer's commit, and never has an earlier `terminal/success` timestamp than the acquirer's own (audited exactly once — no held-moment audit row exists). A same-subgraph member (`inheritor`) is checked to run exactly once, ruling out a double-fire.
