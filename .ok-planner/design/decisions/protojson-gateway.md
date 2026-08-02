---
decision: protojson-gateway
---

# HTTP+JSON bridge for gRPC

## Choice

The HTTP gateway marshals bodies with `protojson`, the canonical proto-to-JSON mapping.

## Rationale

REST convenience without abandoning the gRPC contract: the HTTP surface stays a mechanical projection of the proto messages, with no second JSON vocabulary to keep in sync.

## Alternatives

- Hand-written JSON DTO types marshaled with the standard library — rejected: a parallel body vocabulary that drifts from the proto contract.
- A generated gateway layer (grpc-gateway) — rejected: a heavier codegen dependency for a mapping `protojson` already provides.
