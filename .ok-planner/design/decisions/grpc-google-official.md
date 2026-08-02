---
decision: grpc-google-official
---

# gRPC + protobuf libraries

## Choice

The official upstream gRPC and protobuf libraries for Go.

## Rationale

Upstream reference implementations track the protocol specs directly, carry the canonical codegen, and need no compatibility shimming.

## Alternatives

- The gogo/protobuf fork — rejected: faster codegen at the cost of tracking upstream by hand, and unmaintained against the protobuf API v2 line.
- A connect-style RPC library speaking the gRPC wire protocol — rejected: a second idiom for the same protocol, with weaker alignment to upstream codegen and conformance tooling.
