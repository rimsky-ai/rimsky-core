---
audit: inproc-transport-client
artifact: decision:inproc-transport-client
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:52Z
---

# In-process is a third transport case on the same client-pool factory as gRPC and HTTP-bridge

Supported. `ClientPool.GetOrCreate` in `lib/runtime/executor/client.go` switches on `ep.Transport` across exactly 3 cases — `"grpc"`, `"http"`, `"inproc"` — constructing `NewGRPCClient`, `NewHTTPClient`, or `NewInProcessClient` respectively, all returning the same `Client` interface (`Execute`/`Close`). The single dispatch call site (`lib/runtime/runner_dispatch.go`'s `args.Pool.GetOrCreate(ep)`) is transport-agnostic, matching the claim that transport stays opaque to dispatch; `NewInProcessClient` resolves handlers from the `InProcessRegistry` by the endpoint's `inproc://` URL as the canonical in-process identity, per `decision:inproc-handler-interface`'s handler-context construction.
