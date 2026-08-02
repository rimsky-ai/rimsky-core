---
audit: service-enrollment
artifact: story:service-enrollment
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# One api-key enrolls a standing service and its serving credential auto-renews

Supported. `lib/control/controlapi/enroll.go`'s `/enroll` route requires an authenticated api-key principal (the standard control-API auth gate, no separate join-token type) and mints a short-lived leaf keyed to that principal's key id — checked by 5 tests in `lib/control/controlapi/enroll_test.go` (cert SAN carries the caller's key id, label logging, permission denial, route absence when peer-auth is off, and clock defaulting). `lib/services/internal/peerauth/config.go`'s `Load` obtains the serving credential at startup via a real HTTP round trip to `/enroll` and fails closed if unreachable (`TestLoadMTLSFailsClosedWhenEnrollUnreachable`), then `Identity.Maintain` renews at 2/3 of the leaf's TTL without operator action (`TestShouldRenewAtTwoThirdsTTL`, `TestNeedsRenewalUsesInjectedClock`, `TestRefreshHotSwapsCert` — the last of which confirms a live in-memory cert swap). `lib/services/executors/http-node/bridge_mtls_test.go` exercises this identity against a real mTLS listener (accepts a mutually-authed client, rejects no-cert and plaintext peers). Revocation stopping future issuance rests on the same generic auth-denial path every authenticated route uses, proven by `test/scenarios/auth/lifecycle_test.go`'s `TestAuditContent_AccessDeniedRevoked` (a revoked key is denied with `denial_reason=revoked`); `/enroll` sits behind that identical gate, so a revoked key can mint no new leaf, and — per the no-CRL design the concept doc states and this audit did not find contradicted — an already-issued leaf simply ages out at its TTL.
