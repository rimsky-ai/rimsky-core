---
error: heartbeat_lost
surfaced_to: operator
---

# Heartbeat lost

## What it means

A heartbeat (supervisor ↔ scheduler, or executor ↔ supervisor) went silent past the configured timeout. The orphan reaper will sweep affected work after the cutoff (`5 × heartbeat_interval`).

## When it happens

Process crash, network partition, host overload, or a stuck blocking call. Most commonly: an executor that is silent on heartbeats while doing slow synchronous work (it should be sending periodic `Heartbeat` events).

## What to do

For executors: send `Heartbeat` events on a regular cadence — independent of the work loop. If the work is genuinely synchronous and blocking (a long C call, a slow LLM call), interpose a goroutine / async task that pumps heartbeats while the work runs.

For supervisors and schedulers: check the process is alive, the database connection is healthy, and the configured `heartbeat_interval` matches across replicas.

## See also

- [`../../concepts/executor.md`](../../concepts/executor.md)
- [`../../concepts/claim-handle.md`](../../concepts/claim-handle.md)
