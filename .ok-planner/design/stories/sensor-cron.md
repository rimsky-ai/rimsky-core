---
story: sensor-cron
status: as-is
---

# Operator wires durable cron-driven message

## Role

As an operator wiring a cron-driven message into a workflow, I can use the bundled cron sensor to fire at declared cron expressions, persist watermarks to a configured durable state DB so a process restart doesn't lose firing position, with the documented replica posture — single-replica per `concept:sensor` / `concept:replica`; if two independent processes are ever pointed at the same subscription and state DSN anyway, the window-deterministic idempotency key deliberately collapses their duplicate fires to one enqueued message per window (benign, not leader election) — matching what the binary actually does, so that I have a cron sensor whose behavior under restart and under an unsupported multi-replica deployment is what the docs claim.

## Capability

Bundled cron sensor publisher: cron-expression scheduling; durable watermark state; single-replica posture with a deliberate, deterministic same-subscription same-window collapse if that posture is ever violated (no cross-replica advisory-lock coordination is used to produce it).

## Business value

Operators get a cron sensor whose behavior under restart is what the documentation claims, and whose behavior under an accidental multi-replica deployment is a safe, deterministic collapse rather than duplicate downstream messages or a surprise leader election.

## Acceptance

A cron-sensor instance, configured with a state DSN pointing at a real durable store, holds a publisher-subscription whose next-fire time is future; restarting the binary preserves the subscription and the binary fires at the originally-scheduled window without external re-subscribe. With an empty DSN the in-memory default takes over. One running sensor instance with a due subscription posts exactly one message per window. Two independently-running instances sharing the same subscription and state DSN — an unsupported deployment per `concept:replica`'s single-replica contract — each still fire (no advisory-lock coordination suppresses either replica's own dispatch), but both compute the identical subscription+window idempotency key, so a real control-API dedup collapses the pair to exactly one enqueued message; a distinct subscription or a distinct fire window never shares a key and is never collapsed.

## Falsifier

State persists but the binary refuses to honor it on restart, OR two replicas' same-subscription same-window idempotency keys differ (breaking the collapse), OR a distinct subscription or distinct window collapses with another, OR cron advancement uses wall clock instead of the row's prior next-fire time, OR the source carries a cross-replica advisory-lock/leader-election coordination primitive.

## Proof

Executable proof.
