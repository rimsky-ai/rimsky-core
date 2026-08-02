---
audit: attribute-carry-forward
artifact: decision:attribute-carry-forward
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:46Z
---

# Carry-forward-then-substitution-overlay hydration, scoped to the RunScope, default-on

Supported. `loadReceiverCarryForward` (`lib/runtime/gate_evaluator.go`) loads the prior-in-scope run's bag (empty map when none exists — the first-dispatch default-fallback case), and `substituteAttributesSchemaWith` (`lib/runtime/runner_dispatch.go`) seeds the output bag from that carry-forward map first, then overlays per-property `source`-substitution results, and only fills a schema `default` for a property left unset by both — i.e. carry-forward is unconditional (no opt-in flag) and substitution-bound properties overwrite it, matching the described precedence exactly. The prior-run lookup is scoped by `run_scope_id` in both the cascade gate-eval path and the non-cascade `SnapshotBagForNewRun` path (postgres and sqlite), which is what makes carry-forward RunScope-bounded and therefore intra-frame by construction. `TestSelfEdgeIntraFrameLoop_ReadOnlyAttributeCarriesForwardAcrossDispatches` confirms an executor-written readOnly property (no `source`) carries forward unchanged across same-scope dispatches; `TestAttributeCarryForwardWithinRunScopeThenSubgraphSeesSchemaDefault` confirms a fresh sub-graph RunScope starts from the schema default rather than the parent scope's carried value.
