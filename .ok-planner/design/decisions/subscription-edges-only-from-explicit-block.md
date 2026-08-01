---
decision: subscription-edges-only-from-explicit-block
status: as-is
---

# Subscription edges come only from the explicit subscription block

## Choice

The subscription-edge map is fed by the explicit subscribes block plus exactly one runtime injection at template registration: one structural-root edge per structural-root node — a node with no upstream of any kind (no non-self subscribes entries, no upstream attribute substitution refs, no message-body consumption; a self-subscription does not disqualify) — waking on the runtime-implicit empty-type message-receiver node's settlement without forcing upstream refresh. Substitution refs do not contribute edges to the map.

## Rationale

Cascade edges remain dominantly what the author wrote. The single runtime-injection carve-out is template-determinable (computed from the same subscribes blocks that feed author-declared edges), exists to map a runtime-implicit message type (the empty-message wake trigger, per `story:empty-message-wakes-roots`) onto the cascade walker's uniform path, and carries no operator-facing surface for inferring additional edges.

## Alternatives

- Derive subscription edges from substitution refs as well — rejected: cascade topology would stop being what the author explicitly wrote, with edges inferred from data references that give authors and operators no surface to audit.
