---
decision: async-callback-post-json
status: as-is
---

# Async-callback transport

## Choice

HTTP POST with a JSON outcome body to the supervisor's async-callback endpoint, keyed by the async-acknowledgement identifier on the route path (see `concept:supervisor`). The body's shape matches the unary `Execute` outcome: exactly one of `success` / `error` / `park`. The two run-terminating outcomes (`success` and `error`) carry `attributes_delta` and `tags` alongside the type-specific fields; `park` is dispatch-internal and carries only `tags` (for audit forensics) plus its park-specific fields — no `attributes_delta`, since attribute writeback is a feature of run-terminating verdicts only (per `decision:uniform-attributes-delta`). No `events` array.

## Rationale

Simple, debuggable; the on-the-wire shape mirrors the unary `Execute` RPC's outcome so the supervisor's callback handler and its sync-RPC settlement path can share validation and persistence code.
