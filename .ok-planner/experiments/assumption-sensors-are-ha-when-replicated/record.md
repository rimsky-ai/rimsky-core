---
experiment: assumption-sensors-are-ha-when-replicated
commit: PENDING
---

# Two replicas of a sensor, one subscription, one stop

## What it ran against

A private docker network carrying a postgres state store, two
`rimsky-sensor-cron` replicas sharing one `_STATE_DSN` and one network alias,
and a `rimsky-all-in-one` orchestrator whose config points one publisher at the
shared alias and two more at each replica's own alias. The shared-alias
publisher is the subscription under test; the two pinned ones are the run's
clock, so every reading waits on a firing that does not belong to the
subscription being watched. Two instances carry them. All cron expressions are
`* * * * *`, and messages carry the window they fired for, so counting messages
per `fire_at` is the whole instrument.

## What was observed

Nine checks, none failing.

The control API mounts the subscription on exactly one replica; the other never
sees it and holds no watch for it. Each closed window produced exactly one
message.

Stopping the replica that held it did not stop the schedule. The surviving
replica kept its own subscriptions firing, the watched subscription was taken up
again — the survivor's log now carries its id — and the minute sequence ran
`08:34, 08:35, 08:36` with no hole across the failover.

Made to hold the same watch on both processes at once (each replica restarted so
it recovers the subscription from the shared state store), the instance still
received exactly one message for the window. The two processes fire the same
window under the same deterministic idempotency key, and the second delivery is
absorbed.
