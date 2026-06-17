---
decision: subscription-edges-only-from-explicit-block
status: as-is
---

# Subscription edges come only from the explicit subscription block

## Choice

The subscription-edge map is fed by the explicit `subscribes:` block AND by one specific runtime-injection at template registration: structural-root edges keyed by sender=`""` (one per node whose `subscribes:` block is empty or absent), with `wake_on_change: true` and `force_upstream_refresh: false`, gating the runtime-implicit empty-message virtual. Substitution refs still do not contribute to the map.

## Rationale

Cascade edges remain dominantly what the author wrote. The single runtime-injection carve-out is template-determinable (computed from the same `subscribes:` blocks that feed author-declared edges), exists to map a runtime-implicit message type (the empty-message wake trigger, per `story:empty-message-wakes-roots`) onto the cascade walker's uniform path, and carries no operator-facing surface for inferring additional edges. Substitution refs continue not to contribute to the map — only explicit subscribes plus the structural-root injection.
