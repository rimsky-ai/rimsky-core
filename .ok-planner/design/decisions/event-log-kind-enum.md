---
decision: event-log-kind-enum
status: as-is
---

# Event-log kind discriminator typing

## Choice

The canonical set of operational `rimsky_events.kind` values (non-signal-class events: `auth.*`, `state_transition`, `lock_acquired`, `work_started`, `attributes_substituted`, `breakpoint.hit`, etc.) is declared as an enum in the events proto. Signal-class kinds keep their existing type-path discipline (the five-class taxonomy validated at template registration). Rimsky's app logic — scheduler, supervisor, breakpoint evaluator, audit handler, read-API kind filters — consumes typed values exclusively (the generated Go enum for operational kinds; the parsed signal type-path for signal-class kinds), never raw strings. The persistence layer marshals typed → storage at write and storage → typed at read; an unknown string at the unmarshal boundary is a defensive error, not a control-flow input. Column storage shape (`TEXT` today) is a marshaling detail with no `CHECK` constraint required — the enum at the app boundary IS the gate.

## Rationale

Kinds are not inert per `concept:inertness` — they drive cascade dispatch (signal-class), breakpoint evaluation (`breakpoint.hit`), and audit-consumer filtering by canonical name (operational). A typed boundary at the app layer prevents typo-induced silent observability blind spots without coupling persistence to schema migrations. Adding a new operational kind = adding an enum value in the events proto + regenerating Go bindings.

## Alternatives

CHECK constraint at persistence (couples schema to enum, requires migration per new kind, redundant when enum gates the app boundary); registry-table-with-FK (introduces a mutable kind catalog through an API, which the model doesn't want); leaving operational kinds free-form (accepts the footgun for audit consumers filtering by canonical kind name).
