---
decision: async-callback-post-json
status: as-is
---

# Async-callback transport

## Choice

HTTP POST with a JSON outcome body to the supervisor's async-callback endpoint, keyed by the async-acknowledgement identifier on the route path (see `concept:supervisor`). The body shape is the outcome oneof of `decision:async-callback-outcome-oneof`.

## Rationale

Simple, debuggable; the on-the-wire shape mirrors the unary `Execute` RPC's outcome so the supervisor's callback handler and its sync-RPC settlement path can share validation and persistence code.

## Alternatives

- A gRPC callback RPC back to the supervisor — rejected: forces every external executor to carry a gRPC client, while plain HTTP + JSON is reachable from any language and debuggable with curl.
- Holding the original dispatch connection open until the outcome — rejected: an async outcome must survive disconnects and supervisor restarts; tying it to a live connection defeats the mode's purpose.
