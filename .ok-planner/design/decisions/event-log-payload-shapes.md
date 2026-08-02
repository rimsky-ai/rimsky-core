---
decision: event-log-payload-shapes
---

# Event log payload shape

## Choice

Typed oneof payloads for a settled subset of operational event kinds (state-transition, work-started, lock-acquired, claim-acquired, and the rest of the oneof's members — see `concept:event-log`); free-form JSON `payload_raw` for signal-class events (the `terminal/*`, `transient/*`, `attribute/*` taxonomy) and every operational kind outside the typed subset. Mechanically enforced by `TestEventPayloadOneof_ExcludesSignalClassKinds` (`lib/protocols/proto/v1/gen/event_payload_split_test.go`), which rejects any oneof case that looks signal-class. The proto's typed oneof is not currently consumed by rimsky's internal write/read path (`EventRow.Payload`, `EventAppendInput.Payload`, `signal.Signal.Payload` are `map[string]any` free-form JSON for both event classes alike) — no production code constructs the proto `Event` message.

## Rationale

Type safety where a subset of the operational vocabulary is settled and worth a typed schema; lightweight JSON where the payload is audit data (the rest of the operational kinds) or where the signal taxonomy's shape is expressed through the signal type-path rather than a payload schema. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`).

## Alternatives

- Typed oneof payloads for every kind, signal-class included — rejected: the signal taxonomy is typed through its type-path discipline, not a payload schema, so a oneof case per signal kind buys no consumer gain.
- Free-form JSON payloads everywhere — rejected: discards type safety on the settled subset of the operational vocabulary that benefits from a typed schema.
