---
decision: fanout-parallelism-cap-per-process
---

# The fan-out parallelism cap bounds one supervisor process

## Choice

A fan-out's parallelism cap bounds how many of its clones one supervisor process dispatches to their executor concurrently. The cap is an in-process semaphore: a clone takes a slot before its synchronous executor call and returns the slot when that call returns. A deployment running several supervisor processes enforces the cap once per process rather than pooling it across the cluster (see `concept:fan-out`).

## Rationale

The cap exists to keep one fan-out from saturating an executor from one dispatcher, and an in-process semaphore delivers that with no coordination. A pooled cluster-wide cap would need a shared counter with its own lease, expiry, and recovery path. A supervisor that died holding a slot would hold that slot until the recovery path reclaimed it. An operator who needs a hard cluster-wide ceiling sets it on the executor side, where the contended resource lives.

## Alternatives

- A cluster-wide pooled cap backed by a shared counter — rejected: it adds a distributed lease with its own expiry and recovery, and a crashed supervisor leaks slots.
