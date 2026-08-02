---
audit: peer-tls-enforcement
artifact: decision:peer-tls-enforcement
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:12Z
---

# The `tls` key is writable on every peer kind and honored at every dial site

Supported. `parseTLSMode` (`lib/control/config/claim_producers.go`) is called for all 5 peer-entry kinds a `rimsky.yml` can declare — claim_producers (store), executors, publishers, validators, data_processors — so the key is writable and validated identically on every kind, not just the three the Choice names as examples. All 6 dial sites checked honor the resulting mode through the shared `lib/runtime/peer/credentials.go::TransportCredentials`/`TLSModeUnaryInterceptor`/`TLSModeStreamInterceptor` (store `Dial`, publisher `DialPublisher`, data-processing `DialDataProcessing`, validation `DialValidation`/`FetchExecutorValidationRoles`/`FetchPublisherValidationRoles`, the observability-handshake `dial`) or the equivalent HTTP-bridge logic in `lib/runtime/executor/client_http.go` (the executor's HTTP transport): `required` dials TLS verified against the configured root pool (system roots by default, or the deployment CA when peer-auth mTLS has installed one), `off` stays plaintext, and `required`-mode failures are wrapped naming the peer and `tls: required`. No dial site accepts-but-ignores the key.
