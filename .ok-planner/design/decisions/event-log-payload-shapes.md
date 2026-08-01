---
decision: event-log-payload-shapes
status: as-is
---

# Event log payload shape

## Choice

Typed oneof payloads for signal-class events (the node-run-transition subset uses signal-type-path discipline); free-form JSON payload for operational events (auth-related, state-transition, and the rest — see `concept:event-log`) whose payload is audit data rather than typed contract.

## Rationale

Type safety where the signal taxonomy is settled; lightweight JSON where the payload is just consumer-facing audit data. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`).

## Alternatives

- Typed oneof payloads for every kind, operational included — rejected: couples audit-only payloads to protocol schema churn (a regeneration per new operational kind) for no consumer gain.
- Free-form JSON payloads everywhere — rejected: discards type safety on the settled signal taxonomy, whose payloads are typed contract rather than audit data.
