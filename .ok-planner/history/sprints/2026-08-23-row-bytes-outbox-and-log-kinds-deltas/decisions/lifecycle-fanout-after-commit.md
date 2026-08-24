---
decision: lifecycle-fanout-after-commit
---

# Lifecycle fan-out runs after the transition commits

## Choice

Every lifecycle-subscriber delivery runs outside the transaction that performs the transition, after it commits: the transition stages the delivery as an outbox row, and the drain delivers it afterwards. A subscriber's error leaves the row pending for retry and never refuses or rolls back the transition (see `concept:lifecycle-subscriber`). Refusing a template is `concept:validation`'s act, through its own protocol.

## Rationale

A notification delivered inside the transition's transaction turns a service's error into a veto through placement alone, and nothing in the corpus grants a subscriber that authority. Staging the row with the transition and delivering after commit keeps every transition independent of a service being down or slow, keeps refusal where the protocol for it lives, and makes "after commit" durable: the row exists exactly when the transition does.

## Alternatives

- Fan-out inside the transition's transaction, so a subscriber error rolls the transition back — rejected: it gives a subscriber a veto, puts an external service in the path of every control-plane transition, and duplicates validation's job through an unwritten side effect.
- Call the service directly after commit without staging a row — rejected: a crash between the commit and the call loses the event, and a failed call has no record to retry from.
