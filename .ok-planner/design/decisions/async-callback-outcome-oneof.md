---
decision: async-callback-outcome-oneof
status: as-is
---

# Async-callback body shape

## Choice

The async-callback body is a oneof — exactly one of the `success` / `error` / `park` outcome keys — and each outcome inherits the shape of the unary `Execute` RPC's outcome (per `decision:uniform-attributes-delta`). There is no separate event stream alongside the outcome: per-emission discriminators and data fold into the chosen outcome itself.

## Rationale

Type-safe state machine; explicit error handling; the body's outcome surface matches the unary `Execute` RPC's outcome shape exactly, so the supervisor's callback handler and its sync-RPC settlement path share validation and persistence code without per-transport divergence (see `concept:terminal-tag`, `decision:uniform-attributes-delta`).

## Alternatives

- A string discriminator field with per-outcome payloads (the prior wire shape) — rejected: a body carrying zero or several outcomes surfaces only in deep validation; the oneof makes exactly-one mechanically checkable.
- A top-level events array alongside the outcome — rejected: two channels for one verdict; per-emission data belongs to the outcome that settles the dispatch.
