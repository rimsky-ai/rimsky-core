---
assessment: claim-producer-protocol--open-with-resolved-scope
subject: story:claim-producer-protocol
way: open-with-resolved-scope
release: d977250c
outcome: held
warrant: experiment:claim-producer-protocol
---
# The producer receives an open request already resolved against the instance

The producer's own call record shows each Open arriving with the selector resolved from the instance parameter — the template declares a placeholder, the producer receives the concrete value — together with the declared intent, the declared alias, and the node's opaque data blob byte-for-byte as the template wrote it. An author therefore writes against concrete values and can carry their own configuration through the blob untouched, rather than re-implementing rimsky's substitution inside their producer.

## Unverified remainder

None: the passing run demonstrates the way as promised.
