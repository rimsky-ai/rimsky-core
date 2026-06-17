---
decision: empty-sender-key-edge-disambiguation
status: as-is
---

# Empty-sender-key edge disambiguation

## Choice

Every subscription edge carries a sender-bound-to-empty flag. Cross-cutting subscriptions set the flag false (consulted on every settled sender). Runtime-injected structural-root edges set the flag true (consulted only when the actual settling sender's type is empty). The cascade walker reads the flag on every empty-sender-key lookup. Author-declared subscribes entries cannot set the flag; the runtime owns it.

## Rationale

Preserves a single edge data structure and a single walker code path. The disambiguation is a one-field branch at lookup time. Cross-cutting subscribers continue to fire on every emission (including the empty-message virtual when their predicate matches); structural-root injected receivers fire only on the empty-message virtual.

## Alternatives considered

Separate sentinel key for structural-root edges — doubles the lookup surface for one logical sender; semantic overload without disambiguation — conflates two edge kinds.
