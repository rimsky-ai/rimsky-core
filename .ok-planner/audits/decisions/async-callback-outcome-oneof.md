---
audit: async-callback-outcome-oneof
artifact: decision:async-callback-outcome-oneof
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Async-callback body is a validated success/error/park oneof mirroring the sync Execute outcome

Supported. `lib/runtime/callback.go` defines `asyncCallbackBody` as three optional keys (`success`, `error`, `park`); its parser counts populated keys and rejects any count other than exactly one with a 400. Each variant's fields (`changed`, `change_summary`, `attributes_delta`, `tags`, `scratch` for success; the equivalent set for error and park) line up field-for-field with the sync `Execute` RPC's generated `Success`/`Error`/`Park` proto messages, and the callback handler feeds the parsed outcome into the same `driveTerminal`/terminal-apply path used by the synchronous settlement route. The retired events-array alternative is mechanically foreclosed: the wire proto (`executor.proto` `AsyncCallbackBody`) reserves field 1 and the name `events`, and a dedicated proto-descriptor test in the generated package asserts the reservation.
