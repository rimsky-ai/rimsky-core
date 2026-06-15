---
decision: subscription-mounting-state
status: as-is
---

# Publisher subscriptions are desired-state rows with a visible lifecycle

## Choice

The publisher-subscription state set covers mounting / active / failed / stopped; instance-create inserts subscription rows in the mounting state and returns; the instance-detail surface exposes per-subscription state (see `concept:publisher-subscription`).

## Rationale

The row set is already documented as the source of truth that publisher-side state reconciles against; an observable mounting state is robust against contention and matches the desired-state row-set discipline, while a synchronous inline Subscribe at instance-create would force a failure mode the row-set already absorbs. Failing on a timeout is arbitrary.
