---
audit: claim-producer-observability
artifact: story:claim-producer-observability
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# The observability protocol serves claim detail, a live stream, paginated inventory, and producer-declared admin views

Supported. `claim_producer_observability.proto` defines `GetClaim` (full detail incl. history), `StreamClaim` (server-streamed live claim-state events), `ListClaims` (cursor-based pagination via `ListClaimsRequest.cursor`/`ClaimList.next_cursor`), and `GetAdminView` against producer-declared `AdminViewDecl`s. Both shipped producers (filesystem, postgres) implement the full server (`lib/services/claim_producers/{filesystem,postgres}/server/observability.go`), each declaring at least one custom admin view, and both mount an HTTP+JSON bridge (`lib/protocols/serverkit/observability.go::MountObservability`, covering claim get/list/stream(SSE)/admin) so a dashboard can consume the surface without a gRPC client — directly matching "without writing a custom backplane." Coverage is checked by producer-local tests (`observability_test.go` in each server package) and by the shared conformance library (`lib/protocols/conformance/claimproducer/observability_check.go`, `observability_streamlist_test.go`), which probes all four RPCs including the streaming and pagination shapes.
