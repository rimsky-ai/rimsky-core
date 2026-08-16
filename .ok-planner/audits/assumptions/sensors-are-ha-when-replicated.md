---
assumption: sensors-are-ha-when-replicated
commit: PENDING
disposition: held
synthesized: 2026-08-16T05:48:16Z
---

# running two replicas of a sensor gives failover without duplicate messages, because message sends are idempotency-keyed.

As operator running for availability, I would take it that running two replicas of a sensor gives failover without duplicate messages, because message sends are idempotency-keyed.

## Source

ecosystem-prior — idempotent ingest plus containerized services normally implying safe horizontal scaling

## What a run would observe

run two replicas of `sensor-cron` on one subscription and count the messages that reach the instance per window.

## Measured

Experiment `assumption-sensors-are-ha-when-replicated` (nine checks, none
failing) ran two `rimsky-sensor-cron` replicas at this tree's tag behind one
network alias with one shared state store, against a `rimsky-all-in-one`
orchestrator, and counted messages per `fire_at` window. Two further
subscriptions pinned to each replica act as the run's clock, so no reading waits
on the subscription under test. The prior holds, in both halves.

Failover: the control API mounts the subscription on exactly one replica, and
the other holds no watch for it — so the second replica is a cold spare rather
than a hot one. Stopping the holder did not stop the schedule: the subscription
was taken up again by the survivor, whose log then carries its id, and the
minute sequence ran `08:34, 08:35, 08:36` with no window skipped.

No duplicates: made to hold the same watch on both processes at once — each
replica restarted so it recovers the subscription from the shared state store —
the instance still received exactly one message for the window. Both processes
fire the same window under the same deterministic idempotency key, and the
second delivery is absorbed rather than delivered twice.
