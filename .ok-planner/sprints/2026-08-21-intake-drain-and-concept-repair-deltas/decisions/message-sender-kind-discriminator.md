---
decision: message-sender-kind-discriminator
---

# Message envelope sender

## Choice

The message envelope carries three sender fields: the sender-kind discriminator, a three-value enum `operator` / `publisher` / `instance`; the sender, the identity minted within that kind; and the sender-subject, the actor behind the send where one exists — the api-key of an operator send, the subscription of a publisher send. An instance send carries no subject.

The idempotency dedup discriminator is a separate four-value enum — `operator` / `publisher` / `instance` / `anonymous`. The cascade send path writes `instance`; it never crosses the wire gate that blocks instance senders over HTTP. `anonymous` buckets anonymous-mode operator sends so a bootstrap admin's later keyed sends do not dedup against the anonymous-floor sends that preceded the key mint. The two enums are not the same enum.

## Rationale

Namespacing sender strings by kind keeps identities minted by different sources from colliding. Carrying the sender-subject on the envelope lets an audit reader ask "which key sent this" of the message itself; the idempotency table also records the subject, but it is a dedup ledger, not an audit surface. The dedup enum differs from the envelope enum because it answers a different question and holds one value, `anonymous`, with no envelope meaning.

## Alternatives

- A single free-form sender string with no kind discriminator — rejected: sender identities minted by different sources (api-keys, publisher subscriptions, instances) can collide, and consumers cannot tell which namespace a sender belongs to.
- One shared enum serving both the envelope and the idempotency dedup discriminator — rejected: the dedup-only `anonymous` bucket has no envelope meaning.
- The sender-subject on the idempotency table only — rejected: an operator send's envelope sender is the literal word `operator`, so two api-keys are indistinguishable on the audit-visible envelope.
