---
story: service-enrollment
status: as-is
---

# Service enrolls with a service:enroll key and obtains a short-lived cert

## Story

As an operator deploying a standing service under `peer_auth: mtls`, I give it an api-key carrying the `service:enroll` grant; the service enrolls at startup and obtains a short-lived leaf certificate, its private key, and the CA root, then auto-renews before expiry — so I manage exactly one credential per service (an api-key I can mint, scope, and revoke), and revoking it stops the service's certificate from renewing.

A `service:enroll` permission verb on the grant grammar (see `concept:permission`); an enrollment endpoint `route:POST /v1/enroll` on the control plane that authenticates the calling key, verifies the `service:enroll` grant, and returns a leaf certificate (24h TTL, SAN `spiffe://rimsky/<key-id>`), its key, and the CA root. Certificates are memory-only service-side and auto-renewed at ⅔ of TTL. There is no new credential type and no new table — the api-key is the standing secret, the cert is the derived identity (see `concept:peer-auth`, `decision:enroll-token-is-api-key`).

Operators manage one credential per service; the api-key ledger stays the single principal registry; the standing secret transits once at enroll and never again, and a dumped ledger leaks no usable service secret. Revocation needs no CRL — revoking the key stops renewal and the cert ages out within its TTL.
