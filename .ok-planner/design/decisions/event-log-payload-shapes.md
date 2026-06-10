---
decision: event-log-payload-shapes
status: as-is
---

# Event log payload shape

## Choice

Typed oneof payloads for signal-class events (the node-run-transition subset uses signal-type-path discipline); free-form JSON payload for operational events (`auth.*`, `state_transition`, etc.) whose payload is audit data rather than typed contract.

## Rationale

Type safety where the signal taxonomy is settled; lightweight JSON where the payload is just consumer-facing audit data. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`).

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
