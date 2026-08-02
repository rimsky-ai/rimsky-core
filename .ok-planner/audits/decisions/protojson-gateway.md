---
audit: protojson-gateway
artifact: decision:protojson-gateway
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095825-http-json-bridges-not-uniformly-protojson
---

# HTTP+JSON bridge for gRPC

Unsupported. Of the HTTP+JSON bridges to gRPC-defined protocols checked, only one of three round-trips both request and response through the canonical proto-JSON mapping. A second bridge, the one carrying this decision's own citation in source and exercised for real via a shipped command-line conformance path, decodes every request through hand-written structs and only marshals its response canonically. A third, a standalone executor's own HTTP bridge, uses plain JSON encoding against a bespoke struct on both sides with no canonical mapping at all. Both are exactly the parallel hand-written body vocabulary this decision's own rationale rejects as an alternative.
