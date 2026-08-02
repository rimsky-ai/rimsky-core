---
audit: peer-tls-enforced
artifact: story:peer-tls-enforced
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:12Z
---

# Operator setting `tls: required` on a peer gets a verified connection or a loud failure

Supported. `lib/control/config/claim_producers.go::parseTLSMode` validates the field at config load; `lib/runtime/peer/credentials.go::TransportCredentials` returns `credentials.NewTLS(...)` only for `required` and plaintext otherwise, and every dial site checked (store, executor gRPC and HTTP-bridge, publisher, data-processing, validation, observability-handshake — 6 of 6) passes its configured mode through this same function or the HTTP-bridge equivalent in `lib/runtime/executor/client_http.go`. Failure under `required` is loud and names the peer and mode: `TLSModeUnaryInterceptor`/`TLSModeStreamInterceptor` wrap transport-class errors as `peer %q (tls: required): %w`, and the HTTP bridge separately rejects a non-`https://` endpoint URL and wraps request failures the same way. Tests exercise both success and the loud-failure path: `lib/runtime/peer/dial_tls_test.go` (`TestDial_TLSModeRequired_UsesTransportCredentials`, `TestDial_TLSModeOff_DoesNotSatisfyMTLSServer`), `lib/services/executors/http-node/bridge_mtls_test.go` (accepts mutually-authed client, rejects no client cert, rejects plaintext), and `lib/control/config/peer_dial_ordering_test.go`'s four end-to-end wiring tests.
