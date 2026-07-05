---
decision: structural-root-edge-injection-at-registration
status: as-is
---

# Structural-root edge injection at template registration

## Choice

At template registration, the inverse-edge map gains one entry per structural root receiver, keyed by sender=empty-string, with wake-on-change true and force-upstream-refresh false. The augmentation lives on the runtime's derived in-memory map; the canonical template hash is over the spec bytes only and is unaffected. A structural root is a node whose author-declared subscribes block is empty or absent: any subscribes entry disqualifies the node from root status. A self-subscription (node == self) does not establish an upstream and is excluded from the disqualification.

## Rationale

Structural roots are fully template-determinable: per-instance variation in attribute overrides, params, or service bindings does not change subscription topology. Per-instance storage of the augmented edges would be gratuitous redundancy. The augmentation slots into the existing per-template cached map without changing its shape.

## Alternatives considered

Per-instance overlay (new column or table) — gratuitous redundancy across instances of the same template; receipt-time or cascade-walk-time computation — requires a special-case branch in the cascade walker for the empty sender, contradicting the uniformity property.
