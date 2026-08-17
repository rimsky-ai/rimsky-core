---
decision: event-log-payload-shapes
---

# Event log payload shape

## Choice

Typed oneof payloads for a settled subset of operational event kinds (state-transition, work-started, lock-acquired, claim-acquired, and the rest of the oneof's members — see `concept:event-log`); free-form JSON for signal-class events (the `terminal/*`, `transient/*`, `attribute/*` taxonomy) and every operational kind outside the typed subset. A descriptor test rejects any oneof case that looks signal-class. Rimsky's internal write and read path is independent of that proto wrapper: it carries a payload as an opaque value whose only constructor takes a declared generated message, so a map literal does not compile at an emit site.

## Rationale

Type safety where a subset of the operational vocabulary is settled and worth a typed schema; lightweight JSON where the payload is audit data (the rest of the operational kinds) or where the signal taxonomy's shape is expressed through the signal type-path rather than a payload schema. Kind-discriminator typing is a separate decision (see `decision:event-log-kind-enum`). Constructing the internal payload from a generated message rather than a map closes the drift the split otherwise invites: a field nobody sets and a key nobody declared are both unrepresentable.

## Alternatives

- Typed oneof payloads for every kind, signal-class included — rejected: the signal taxonomy is typed through its type-path discipline, not a payload schema, so a oneof case per signal kind buys no consumer gain.
- Free-form JSON payloads everywhere — rejected: discards type safety on the settled subset of the operational vocabulary that benefits from a typed schema.
- Free-form maps on the internal write and read path — rejected: it is the shape that let declaration and emission drift apart, and nothing mechanical caught it.
