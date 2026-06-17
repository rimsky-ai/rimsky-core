---
decision: async-callback-post-json
status: as-is
---

# Async-callback transport

## Choice

HTTP POST with a JSON outcome body to the supervisor's async-callback endpoint, keyed by the async-acknowledgement identifier on the route path (see `concept:supervisor`). The body's shape matches the post-collapse settling-terminal: exactly one of `success` / `error` / `park`, each carrying `attributes_delta` and `tags` alongside the type-specific fields. No `events` array.

## Rationale

Simple, debuggable; the on-the-wire shape mirrors the unary `Execute` RPC's outcome so the supervisor's callback handler and its sync-RPC settlement path can share validation and persistence code.
