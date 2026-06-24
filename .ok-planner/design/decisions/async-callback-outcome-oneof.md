---
decision: async-callback-outcome-oneof
status: as-is
---

# Async-callback body shape

## Choice

Oneof `success` | `error` | `park`; exactly one outcome key. The outcomes inherit the shape of the unary `Execute` RPC's outcome: the two run-terminating outcomes (`success` and `error`) each carry `attributes_delta` and `tags` alongside the type-specific fields; `park` is dispatch-internal and carries `tags` (for audit forensics) plus its park-specific fields, but no `attributes_delta` (per `decision:uniform-attributes-delta`). There is no `events` array on the body; per-emission discriminators fold into the chosen outcome's `tags` set, and per-emission data rides on `attributes_delta` for the run-terminating outcomes.

## Rationale

Type-safe state machine; explicit error handling; the body's outcome surface matches the unary `Execute` RPC's outcome shape exactly, so the supervisor's callback handler and its sync-RPC settlement path share validation and persistence code without per-transport divergence (see `concept:terminal-tag`, `decision:uniform-attributes-delta`).
