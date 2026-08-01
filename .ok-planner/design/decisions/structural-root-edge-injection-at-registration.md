---
decision: structural-root-edge-injection-at-registration
status: as-is
---

# Structural-root edge injection at template registration

## Choice

At template registration, the runtime's derived in-memory inverse-edge map gains one synthetic entry per structural-root receiver, waking on a success terminal without forcing upstream refresh. The augmentation lives on the derived map only; the canonical template hash is over the spec bytes and is unaffected. A structural root is a node with no upstream of any kind: no non-self subscribes entries, no upstream attribute substitution refs, and no message-body consumption — the latter two being sugar-form subscriptions that derive real edges in the inverse-edge map. A self-subscription (node == self) does not establish an upstream and is excluded from the disqualification.

## Rationale

Structural roots are fully template-determinable: per-instance variation in attribute overrides, params, or service bindings does not change subscription topology. Per-instance storage of the augmented edges would be gratuitous redundancy. The augmentation slots into the existing per-template cached map without changing its shape.

## Alternatives

- Per-instance overlay (new column or table) — rejected: gratuitous redundancy across instances of the same template.
- Receipt-time or cascade-walk-time computation — rejected: requires a special-case branch in the cascade walker for the synthetic sender, contradicting the uniformity property.
