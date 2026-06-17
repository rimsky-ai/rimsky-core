---
decision: async-callback-outcome-oneof
status: as-is
---

# Async-callback body shape

## Choice

Oneof `success` | `error` | `park`; exactly one outcome key. The outcomes inherit the uniform settling-terminal shape — each carries `attributes_delta` and `tags` alongside the type-specific fields. There is no `events` array on the body; per-emission discriminators fold into the chosen outcome's `tags` set, and per-emission data rides on `attributes_delta`.

## Rationale

Type-safe state machine; explicit error handling; uniform attribute and tag surface across every settling terminal matches the unary `Execute` RPC's outcome shape (see `concept:terminal-tag`).
