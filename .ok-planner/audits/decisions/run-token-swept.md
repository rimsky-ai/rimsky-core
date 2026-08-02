---
audit: run-token-swept
artifact: decision:run-token-swept
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# The async callback route authenticates by peer identity, not the ack id

Supported. `CallbackServer.handleCallback` (the `/v1/callback/{async_ack_id}` route, `@decision: run-token-swept`) authorizes solely via `authorizePeer`, which under `peer_auth: mtls` requires and verifies a TLS peer certificate chain and, when the dispatch recorded an expected principal, cross-checks it (`enforceCallbackPrincipal`); under any other peer-auth mode it is a no-op, relying on the trusted-subnet assumption. Neither path inspects or requires a per-call token, and the `async_ack_id` URL parameter is used purely to look up the correlating `AsyncContext` (in-memory registry, falling back to a persisted lookup) — never compared against a credential. This is a distinct mechanism from the two mid-dispatch callback routes (`/v1/runs/{run_id}/keepalive`, `/v1/runs/{run_id}/attributes`), which both call `authorizeCancelToken` to require an `Authorization: Bearer` per-dispatch token layered underneath peer identity, confirming the decision's carve-out. mTLS coverage is exercised by eight tests in `callback_mtls_test.go`, including valid-cert acceptance, principal-mismatch rejection, principal-match binding, and no-cert/impostor-CA rejection at the TLS layer.
