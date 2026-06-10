---
story: sensor-cron
status: as-is
---

# Operator wires durable cron-driven message

## Role

As an operator wiring a cron-driven message into a workflow, I can use the bundled `sensor-cron` to fire at declared cron expressions, persist watermarks to a configured durable state DB so a process restart doesn't lose firing position, with the documented replica posture (one replica fires per window once; N independent replicas fire N times — no cross-replica advisory coordination) matching what the binary actually does, so that I have a cron sensor whose behavior under restart and under multi-replica deployment is what the docs claim.

## Capability

Bundled `sensor-cron` publisher: cron-expression scheduling; durable watermark state; replica posture matching documentation (no cross-replica leader election).

## Business value

Operators get a cron sensor whose behavior under restart and under multi-replica deployment is what the documentation claims — no surprise leader election, no silently lost firings on restart.

## Acceptance

A `sensor-cron` instance, configured with a state DSN pointing at a real durable store, holds a publisher-subscription whose `next_fire_at` is future; restarting the binary preserves the subscription and the binary fires at the originally-scheduled window without external re-subscribe. With an empty DSN the in-memory default takes over. One running sensor instance with a due subscription POSTs exactly one message per window; two independently-running instances sharing the same subscription POST exactly two per window — no advisory-lock coordination silently leaders-elect.

## Falsifier

State persists but the binary refuses to honor it on restart, OR two replicas fire only once per window (silent leader election), OR cron advancement uses wall clock instead of the row's prior `next_fire_at`.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
