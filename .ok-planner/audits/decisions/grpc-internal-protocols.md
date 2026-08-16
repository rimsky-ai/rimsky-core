---
audit: grpc-internal-protocols
artifact: decision:grpc-internal-protocols
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:44:49Z
---

# gRPC is the transport for all service-to-service protocols

Unsupported. The nine declared protocol services — executor, executor observability, claim producer, claim-producer observability, data processing, lifecycle subscriber, publisher, validation, and host agent — are all gRPC, so the protocol definitions carry the claim. The universal does not survive contact with the running system: three service-to-service paths speak HTTP-JSON instead. The supervisor's own executor client pool dispatches over three transports, one of which is HTTP, so supervisor-to-executor dispatch has a first-class non-gRPC form. The reverse direction is HTTP by design — the async callback, the keepalive, and the attribute-writeback routes all sit on the supervisor's HTTP listener and are the only way an async executor settles a dispatch, and a sibling decision records that transport choice and explicitly rejects a gRPC callback so external executors need not carry a gRPC client. A bundled executor additionally serves an HTTP-JSON execute-and-observability bridge alongside its gRPC surface, recorded in a third sibling decision. The Choice as written admits no exceptions and three exist.
