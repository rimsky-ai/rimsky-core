---
decision: event-log-kind-enum
---

# Event-log kind discriminator typing

## Choice

The canonical set of operational event-kind values (the non-signal-class events: auth-related kinds, state-transition kinds, lock-acquired, work-started, attribute-substitution, breakpoint-hit, and the rest) is declared as a closed enum at the protocol layer (see `concept:event-log`). Signal-class kinds keep their existing type-path discipline — the three-class taxonomy validated at template registration. Rimsky's app logic — scheduler, supervisor, breakpoint evaluator, audit handler, read-API kind filters — consumes typed values exclusively (the generated enum for operational kinds; the parsed signal type-path for signal-class kinds), never raw strings. The persistence layer marshals typed → storage at write and storage → typed at read; an unknown string at the unmarshal boundary is a defensive error, not a control-flow input. Storage shape of the kind discriminator is a marshaling detail with no persistence-level integrity-check constraint required — the enum at the app boundary IS the gate. Every operational kind the enum declares has at least one emit site, and a fitness check enumerates the emit sites per kind, so the read route never accepts a filter on a kind nothing writes.

## Rationale

Kinds are not inert per `concept:inertness` — they drive cascade dispatch (signal-class), breakpoint evaluation (the breakpoint-hit kind), and audit-consumer filtering by canonical name (operational). A typed boundary at the app layer prevents typo-induced silent observability blind spots without coupling persistence to schema migrations. Adding a new operational kind means adding an enum value at the protocol layer and regenerating the language bindings.

## Alternatives

Declaring a kind ahead of its writer, as a placeholder — rejected: a filterable kind nothing emits returns an empty feed that a reader cannot tell apart from "this never happened here". An integrity-check constraint at persistence (couples schema to enum, requires migration per new kind, redundant when the enum gates the app boundary); a registry-table-with-foreign-key approach (introduces a mutable kind catalog through an API, which the model doesn't want); leaving operational kinds free-form (accepts the footgun for audit consumers filtering by canonical kind name).
