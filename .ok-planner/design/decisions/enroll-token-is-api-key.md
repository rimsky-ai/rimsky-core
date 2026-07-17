---
decision: enroll-token-is-api-key
status: as-is
aliases:
  - join-token-is-an-api-key
  - service-enroll-grant
---

# The enrollment credential is an api-key, not a new token type

## Choice

There is no new "join token" credential type and no new table. A join token, as originally conceived, reduces exactly to a `concept:api-key` carrying a new `service:enroll` permission (see `concept:permission`). A service holding such a key calls `route:POST /v1/enroll` and receives a short-lived leaf certificate, its private key, and the CA root. The layering is: the api-key is the standing secret (authorization to OBTAIN an identity); the certificate is the derived, short-lived identity that proves it. The cert's SAN carries a SPIFFE-style principal `spiffe://rimsky/<key-id>`, so the api-key row IS the service principal; revoking the key stops renewal and the cert ages out within its TTL (see `concept:peer-auth`).

## Rationale

Rimsky has no user entity — the api-key ledger is the entire principal registry, and a service principal already is an api-key. A second credential type for enrollment would duplicate mint/revoke/audit machinery for no gain. Reusing the api-key keeps the actual secret off the wire (it transits once at enroll, then the cert proves possession per handshake) and out of rimsky's store (rimsky holds only the api-key hash plus public CA material — a dumped ledger leaks no usable service secret).

## Alternatives

- **A dedicated internal-service credential kind with its own table and lifecycle** — rejected: it re-implements the api-key ledger's mint/rotate/revoke/audit surface and splits the principal registry in two.
- **A CRL for revocation** — rejected: revoking the api-key stops certificate renewal, so short TTLs let a revoked identity age out without distributing revocation lists.
