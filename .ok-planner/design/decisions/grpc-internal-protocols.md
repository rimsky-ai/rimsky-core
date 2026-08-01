---
decision: grpc-internal-protocols
status: as-is
---

# Inter-service transport

## Choice

gRPC for all service-to-service protocols.

## Rationale

Type-safe binary, codegen, streaming.

## Alternatives

- HTTP-JSON for the service-to-service protocols — rejected: hand-maintained contracts, no generated stubs, no native streaming.
- A message-broker transport between services — rejected: adds standing infrastructure to every deployment for what are point-to-point calls.
