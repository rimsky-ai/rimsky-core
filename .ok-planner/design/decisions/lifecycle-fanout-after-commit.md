---
decision: lifecycle-fanout-after-commit
---

# Lifecycle fan-out runs after the transition commits

## Choice

Every lifecycle-subscriber fan-out runs outside the transaction that performs the transition, after it commits. A subscriber's error is recorded for retry under the at-least-once ledger and never refuses or rolls back the transition (see `concept:lifecycle-subscriber`). Refusing a template is `concept:validation`'s act, through its own protocol.

## Rationale

A notification delivered inside the transition's transaction turns a peer's error into a veto through placement alone, and nothing in the corpus grants a subscriber that authority. Moving the fan-out after commit keeps every transition independent of a peer being down or slow, and keeps refusal where the protocol for it lives.

## Alternatives

- Fan-out inside the transition's transaction, so a subscriber error rolls the transition back — rejected: it gives a subscriber a veto, puts an external service in the path of every control-plane transition, and duplicates validation's job through an unwritten side effect.
