---
audit: message-sender-kind-discriminator
artifact: decision:message-sender-kind-discriminator
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:26:44Z
---

# Message envelope pairs a three-value sender-kind enum with a sender-subject field, distinct from the dedup enum

Unsupported. The first half holds: the envelope's sender-kind is a three-value enum, declared as three constants, validated on every enqueue against exactly those three values, and constrained to the same three by a check constraint on the messages table in both backends. Two further claims do not hold. First, the envelope carries no sender-subject field — the messages table and the envelope row and enqueue-request structures carry only a free-form sender string, and for operator sends that string is the literal constant "operator" while the actor identity (the api-key id, or the anonymous sentinel) is computed at the send route and persisted only on the idempotency row, so the envelope does not carry the actor identity the decision pairs with the discriminator. Second, the dedup discriminator is not a three-value enum with the instance value absent: of the two writers of the idempotency table, the send route maps to operator, publisher, or anonymous, but the cascade send-message path writes the instance value into the same column directly, and the table declares no constraint excluding it. Checked both writers of the idempotency table, all three sender-kind constants, both backends' schemas, and the envelope row and request structures.
