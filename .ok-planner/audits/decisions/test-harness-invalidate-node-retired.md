---
audit: test-harness-invalidate-node-retired
artifact: decision:test-harness-invalidate-node-retired
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:46Z
---

# No test-only invalidate-node surface in the scenario harnesses

Supported. Searched both harness packages (`test/support/scenario`, `lib/services/test/harness`) for any invalidate-node or ad-hoc node-invalidation helper, by name and by intent (grepping "invalidat" case-insensitively across both directories): zero matches. The only `invalidate_node`-named code in the repository is the production debug-override action (`lib/control/controlapi/debug_override.go`), a real operator surface, not a test-only bypass. The harness instead exposes `PostInstanceMessage`/`PostInstanceMessageWithAuth` — typed messages posted through the same control-API route production callers use — which is the "legitimate trigger" path the decision describes; there is no harness-side direct-state-mutation helper standing in for it.
