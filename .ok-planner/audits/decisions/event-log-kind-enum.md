---
audit: event-log-kind-enum
artifact: decision:event-log-kind-enum
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:34Z
---

# Event-log kind discriminator typing

Supported. `events.proto` declares `OperationalKind` as a closed enum (46
live values plus 2 retired/reserved slots); `lib/foundation/events/kinds.go`
wraps it in a `Kind` type whose only constructors are
`OperationalKindFromProto` (checked against a wire-form map) and
`SignalKind` (the signal type-path), and a package `init()` panics at
process start if any non-`UNSPECIFIED` proto enum value lacks a wire-form
mapping, mechanically preventing the typed set from drifting out of sync
with the proto. `persistence.EventAppendInput.Kind` is typed as
`events.Kind`, not a string, so every write path is structurally forced
through the typed constructors; `ParseKindString` is the sole unmarshal
path from storage and returns a defensive error on an unrecognized string.
Consumer call sites checked — the audit handler (`insertEvent`), the
scheduler/supervisor's signal-emission path (`EmitSignal`), and the
read-API kind filters (`/v1/audit`, `/v1/events`) — all route through
`ParseKindString`/typed `Kind` values rather than raw string comparisons.
