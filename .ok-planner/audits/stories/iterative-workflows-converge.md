---
audit: iterative-workflows-converge
artifact: story:iterative-workflows-converge
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Cyclic graph shapes with template-declared stop conditions run to completion inside one frame

Supported. Both cycle shapes the story names are exercised: a node re-running against its own output (the loop-counter node subscribing to its own `terminal/success`, `test/scenarios/loop_counter_cap_e2e_test.go`) and a cycle of nodes walking back to its start (`TestCascadeTwoNodeBackedgeInFrame`, `test/scenarios/cascade_two_node_backedge_in_frame_test.go` — starter → ping → pong → ping). In both, the stop condition is declared entirely in the template — a `when:` predicate over the settling signal's `payload.tags` (per `concept:signal`) — with no runtime-side, operator-configured round-count safety valve: a repo-wide search for any global max-cascade-round/depth guard found none. The back-edge test additionally asserts the whole cycle (both of ping's dispatches) shares one `frame_id` and the instance produces exactly one frame, directly verifying the "stays visible to observability as one coherent unit" clause. `concept:cascade-mode`'s design doc and `concept:signal`'s diff-gate invariant (attribute/<key>/changed fires only when the value differs from the same-scope prior) independently document the value-stability convergence path the story also cites, and are backed by the idempotent-mode dedup tests (`idempotent_mode_dedupes_test.go` and siblings) that exercise the byte-equivalence convergence mechanism at the cascade-mode layer.
