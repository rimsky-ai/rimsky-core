---
decision: message-sender-kind-discriminator
status: as-is
---

# Message envelope sender

## Choice

The message envelope's sender-kind discriminator is the three-value enum `operator` / `publisher` / `instance`, with the orthogonal sender-subject field carrying the actor identity.

## Rationale

Namespacing sender strings by source kind on the persisted envelope keeps sender identities minted by different sources from colliding. The idempotency dedup discriminator is deliberately a different three-value enum — `operator` / `publisher` / `anonymous` — because it answers a different question: `instance` is absent (instance-sender messages are blocked at the wire by the operator-or-publisher gate), and `anonymous` buckets anonymous-mode operator sends separately so a bootstrap admin's later keyed sends do not dedup against the anonymous-floor sends that preceded the key mint. The two sender-kind enums are not the same enum and must not be conflated.

## Alternatives

- A single free-form sender string with no kind discriminator — rejected: sender identities minted by different sources (api keys, publisher subscriptions, instances) can collide, and consumers cannot tell which namespace a sender belongs to.
- One shared enum serving both the envelope and the idempotency dedup discriminator — rejected: the two surfaces need different value sets (`instance` is unreachable in dedup; the dedup-only `anonymous` bucket has no envelope meaning), and one enum smuggles impossible values into each.
