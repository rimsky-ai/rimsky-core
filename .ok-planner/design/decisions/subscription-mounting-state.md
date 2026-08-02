---
decision: subscription-mounting-state
---

# Publisher subscriptions are desired-state rows with a visible lifecycle

## Choice

The publisher-subscription state set covers mounting / active / failed / stopped; instance-create inserts subscription rows in the mounting state and returns; the instance-detail surface exposes per-subscription state (see `concept:publisher-subscription`).

## Rationale

The row set is already documented as the source of truth that publisher-side state reconciles against; an observable mounting state is robust against contention and matches the desired-state row-set discipline.

## Alternatives

- Synchronous inline Subscribe at instance-create — rejected: blocks instance-create on publisher availability and forces a timeout-based failure mode whose cutoff is arbitrary, a failure the desired-state row set absorbs instead.
