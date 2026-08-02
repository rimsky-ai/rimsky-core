---
audit: grpc-internal-protocols
artifact: decision:grpc-internal-protocols
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:46Z
---

# gRPC is the transport for every service-to-service protocol

Supported. Of the 10 proto files under `lib/protocols/proto/v1/`, 9 declare a `service` block (`claim_producer`, `claim_producer_observability`, `data_processing`, `executor`, `executor_observability`, `host_agent`, `lifecycle`, `publisher`, `validation` — the tenth, `events.proto`, is message-only, shared by the others) and each of those 9 is served via `grpc.NewServer`/dialed via `grpc.NewClient` somewhere in the tree — bundled services (`lib/services/claim_producers/*`, `lib/services/executors/*`), the reference `examples/` implementations, and the runtime's own `lib/runtime/peer` clients all confirm gRPC as the live transport, with no message-broker transport present anywhere.
