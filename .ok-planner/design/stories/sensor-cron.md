---
story: sensor-cron
status: as-is
---

# Operator wires durable cron-driven message

## Story

As an operator wiring a cron-driven message into a workflow, I can use the bundled cron sensor to fire at declared cron expressions, persist watermarks to a configured durable state DB so a process restart doesn't lose firing position, with the documented replica posture — single-replica per `concept:sensor` / `concept:replica`; if two independent processes are ever pointed at the same subscription and state DSN anyway, the window-deterministic idempotency key deliberately collapses their duplicate fires to one enqueued message per window (benign, not leader election) — matching what the binary actually does, so that I have a cron sensor whose behavior under restart and under an unsupported multi-replica deployment is what the docs claim.

Bundled cron sensor publisher: cron-expression scheduling; durable watermark state; single-replica posture with a deliberate, deterministic same-subscription same-window collapse if that posture is ever violated (no cross-replica advisory-lock coordination is used to produce it).

Operators get a cron sensor whose behavior under restart is what the documentation claims, and whose behavior under an accidental multi-replica deployment is a safe, deterministic collapse rather than duplicate downstream messages or a surprise leader election.
