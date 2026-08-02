---
audit: enroll-token-is-api-key
artifact: decision:enroll-token-is-api-key
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# The enrollment credential is an ordinary api-key carrying a `service:enroll` grant, not a separate credential kind

Supported. `lib/control/controlapi/enroll.go::handleEnroll` (annotated `@decision: enroll-token-is-api-key`) is gated by the ordinary action-grant middleware on the `service:enroll` action — no separate token table or verification path — and mints the leaf via `pki.CA.IssueLeaf(ident.KeyID.String(), ...)`, embedding the caller's api-key id as the certificate's CommonName and as a `spiffe://rimsky/<key-id>` URI SAN (`lib/foundation/pki/ca.go`), so the api-key row is the certificate's principal. A repository-wide search for a dedicated join/enrollment-token table or type (`join_token`, `enroll_token`, `service_token`) found none. `lib/control/controlapi/enroll_test.go` confirms the SAN carries the caller's key id (`TestEnrollReturnsCertWithCallerKeyIDInSAN`), that the grant is enforced (`TestEnrollForbiddenWithoutPermission`), and that the route is entirely absent (404) rather than reachable-but-denied when peer-auth mTLS is off (`TestEnrollRouteAbsentWhenPeerAuthNone`); the claimed revoke-stops-renewal behavior follows from the same enforced grant since certificate renewal (`lib/runtime/peer/identity.go::MaintainIdentity`) re-calls the same gated enroll endpoint, though no dedicated test exercises a revoke-during-renewal sequence directly.
