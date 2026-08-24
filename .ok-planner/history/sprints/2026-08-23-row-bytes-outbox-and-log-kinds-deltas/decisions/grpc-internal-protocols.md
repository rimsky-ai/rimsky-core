---
decision: grpc-internal-protocols
---

# Inter-service transport

## Choice

gRPC for the declared service protocols. Three shipped service-to-service surfaces are HTTP-JSON by deliberate choice and are the named exceptions: the supervisor's HTTP executor transport and the bundled executor's HTTP bridge (see `decision:http-bridge-preserved`), and the executor-to-supervisor callback, keepalive and attribute-writeback routes (see `decision:async-callback-post-json`).

## Rationale

Type-safe binary, codegen, streaming. The named exceptions each buy something gRPC cannot: the bridge keeps a working surface for callers that already speak HTTP-JSON, and the callback routes let an executor report an outcome with an ordinary HTTP client rather than serve a gRPC server for the supervisor to dial back on.

## Alternatives

- HTTP-JSON for the declared service protocols too — rejected: hand-maintained contracts, no generated stubs, no native streaming.
- A message-broker transport between services — rejected: adds standing infrastructure to every deployment for what are point-to-point calls.
- gRPC with no exceptions — rejected: it would retire the HTTP bridge and force every callback-posting executor to serve gRPC, both of which the decisions that own those surfaces chose against on their own merits.
