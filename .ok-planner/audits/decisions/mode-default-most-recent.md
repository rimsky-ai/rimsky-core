---
audit: mode-default-most-recent
artifact: decision:mode-default-most-recent
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Unset cascade_mode resolves to most-recent; four legal per-node values; non-cascade rows bypass mode rules entirely

Supported. `lib/foundation/cascade/state.go` declares the closed four-value `CascadeMode` enumeration (`most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled`); `lib/graph/node/template_validator.go`'s `validateCascadeMode` accepts an empty `cascade_mode` (no explicit-declaration requirement) and rejects anything outside the four; `lib/runtime/gate_evaluator.go`'s `applyCascadeModeRule` switches on the resolved mode with the empty string routed to the same branch as `CascadeModeMostRecent`. `TestCascadeModeDefaultsToMostRecentAndCoalesces` (`test/scenarios/cascade_mode_default_test.go`) checks `Nodes().GetCascadeMode` directly returns `most-recent` for a template that omits `cascade_mode`, then drives two in-flight cascade rounds through a held downstream node and confirms exactly one coalesced post-settle dispatch (not one per round) carrying the latest value — the coalescing behavior the decision claims for the default. The `cascade_mode` field lives only on the per-node `TemplateSpec` (`lib/foundation/spec/template.go`), and `SubscriptionEntry` (`lib/foundation/spec/subscription.go`) carries no per-upstream mode override field, confirming the single uniform per-node setting. Non-cascade creation reasons (`operator_invalidate`, `recalculate`, `message_delivery`) are created directly in state `stale` (per `decision:non-cascade-direct-to-stale`, cross-checked against all 3 non-cascade call sites: `lib/control/controlapi/debug_override.go`, `lib/runtime/cascade_recalculate.go`, `lib/runtime/message_delivery.go`) and never pass through `evaluateOneGate`/`applyCascadeModeRule`, which only runs over cascade-driven pending rows drained from the wait-set — confirming mode-rule immunity for the three non-cascade reasons.
