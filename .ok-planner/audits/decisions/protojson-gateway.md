---
audit: protojson-gateway
artifact: decision:protojson-gateway
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Every HTTP+JSON bridge over a gRPC surface marshals through the canonical proto mapping

Supported. Ten non-generated sites in the tree use the canonical proto-to-JSON marshaller, and every HTTP bridge sitting over a gRPC contract is among them: the shared protocol-kit bridge that fronts the claim-producer and executor surfaces, its observability companion, the claude-agent bridge, the outbound-HTTP executor's bridge, the executors' shared observability response writer, the conformance kit's HTTP client, and rimsky's own HTTP executor client — so both ends of the wire use the same mapping. No hand-written body types shadow any proto message on those routes; the only standard-library JSON decoding in those packages handles caller-opaque payloads rather than protocol bodies. Neither Go module declares a generated-gateway dependency anywhere. Three tests on the claude-agent bridge pin the mapping's consequences directly: the HTTP response matches the gRPC outcome shape, and unknown request fields are discarded exactly as the gRPC transport discards them.
