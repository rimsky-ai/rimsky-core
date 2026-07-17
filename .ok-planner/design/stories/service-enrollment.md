---
story: service-enrollment
status: as-is
---

# Service enrolls with a service:enroll key and obtains a short-lived cert

## Role

As an operator deploying a standing service under `peer_auth: mtls`, I give it an api-key carrying the `service:enroll` grant; the service enrolls at startup and obtains a short-lived leaf certificate, its private key, and the CA root, then auto-renews before expiry — so I manage exactly one credential per service (an api-key I can mint, scope, and revoke), and revoking it stops the service's certificate from renewing.

## Capability

A `service:enroll` permission verb on the grant grammar (see `concept:permission`); an enrollment endpoint `route:POST /v1/enroll` on the control plane that authenticates the calling key, verifies the `service:enroll` grant, and returns a leaf certificate (24h TTL, SAN `spiffe://rimsky/<key-id>`), its key, and the CA root. Certificates are memory-only service-side and auto-renewed at ⅔ of TTL. There is no new credential type and no new table — the api-key is the standing secret, the cert is the derived identity (see `concept:peer-auth`, `decision:enroll-token-is-api-key`).

## Business value

Operators manage one credential per service; the api-key ledger stays the single principal registry; the standing secret transits once at enroll and never again, and a dumped ledger leaks no usable service secret. Revocation needs no CRL — revoking the key stops renewal and the cert ages out within its TTL.

## Acceptance

A service holding a `service:enroll` key calls the enroll endpoint and receives a leaf certificate whose SAN carries the key's id, its private key, and the CA root; the service uses that cert for internal mTLS and renews before expiry. A key WITHOUT the `service:enroll` grant is refused at the enroll endpoint. After the key is revoked, renewal fails and the existing cert ages out within its TTL rather than being revoked in-place.

## Falsifier

Enrollment succeeding for a key lacking the `service:enroll` grant; OR the leaf cert's SAN not binding the calling key's id; OR the certificate persisted to durable service-side storage; OR a revoked key continuing to obtain renewed certificates; OR a separate credential type or table introduced for enrollment.

## Proof

Executable proof — integration test enrolls a service with a `service:enroll` key and asserts the returned cert carries the SPIFFE principal for that key id and drives an internal mTLS handshake; companions assert a non-enroll key is refused, and that a revoked key's renewal fails.
