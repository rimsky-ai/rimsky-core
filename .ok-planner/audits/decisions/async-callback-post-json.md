---
audit: async-callback-post-json
artifact: decision:async-callback-post-json
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Async callback is plain HTTP POST + JSON, keyed by ack id on the path

Supported. `CallbackServer.Start` registers `r.Post("/v1/callback/{async_ack_id}", c.handleCallback)` on a `chi` router; `handleCallback` reads the raw request body and unmarshals it as JSON via `parseAsyncCallback`, with no gRPC surface for the callback direction anywhere in the executor protocol (the executor-facing gRPC service is dispatch-only; the callback is the sole inbound path back to the supervisor). The body shape is exactly the outcome oneof of `decision:async-callback-outcome-oneof`, and route-level tests (`callback_routes_test.go`, `callback_validation_test.go`, `callback_mtls_test.go`) drive it with `net/http` POSTs carrying JSON bytes, confirming the transport is reachable with ordinary HTTP tooling rather than a held connection or an RPC client.
