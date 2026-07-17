# Intent Dossier: peer-auth

Recorded 2026-07-16 for the peer-authentication posture change (operator-directed
security-architecture work). Companion to `concept:peer-auth`. Same ledger conventions
as the rest of the intent corpus: later intent supersedes earlier; transcript outranks
artifact.

## Net position

- Rimsky's auth model is four trust boundaries: (1) the control plane (api-key + TLS, and the identity authority everything defers to); (2) internal service↔service (optionally mutually authenticated via mTLS); (3) bundled-service ingress from the public web (webhook sensor, host-agent→proxy); (4) outbound (a bundled service presenting a credential TO a third party) (2026-07-16, peer-auth-posture, transcript).
- Everything is a key: rimsky has no user entity, so the api-key ledger is the entire principal registry — a human holds a key's plaintext, and a service principal IS an api-key. This is the load-bearing premise for the rest (2026-07-16, peer-auth-posture, transcript).
- Boundary 2 is a deployment-level switch `peer_auth: none|mtls`, default `none`. `none` = the existing trusted-private-subnet model, zero-config, plaintext (local dev + testcontainers unchanged). `mtls` = a per-deployment control-plane CA (private key encrypted at rest, AES-256-GCM, key from RIMSKY_CA_ENCRYPTION_KEY base64 32 bytes, fail-closed at startup if mtls is on and the key is missing/malformed); services enroll with a `service:enroll` api-key at POST /v1/enroll for a short-lived (24h) leaf cert + key + CA root; certs are memory-only, auto-renewed at ⅔ TTL; both peers present and verify CA-signed certs; the SAN carries `spiffe://rimsky/<key-id>` (2026-07-16, peer-auth-posture, transcript).
- The join token is just an api-key: no new credential type, no new table. The api-key is the standing secret (authorization to OBTAIN an identity); the cert is the derived short-lived identity. The secret transits once at enroll, then the cert's private key proves possession per handshake; rimsky stores only the api-key hash plus public CA material (2026-07-16, peer-auth-posture, transcript).
- mTLS over api-key-everywhere: api-keys are a server-inbound bearer pattern, but rimsky's internal peers are mostly OUTBOUND clients; only a certificate authenticates the SERVER side of a connection and keeps the secret off the wire (2026-07-16, peer-auth-posture, transcript).
- Rule of thumb: operator-deployed standing service → api-key + mTLS enrollment; user-owned session tooling (the host-agent) → api-key + TLS to a pinned CA root (no client cert). The outbound-egress SSRF guard is a property of the bundled images, not of rimsky core — core keeps its inertness guarantee (2026-07-16, peer-auth-posture, transcript).

## Required behaviors (open promises)

- `peer_auth: none` (default) requires no CA/enrollment/cert material and dials plaintext; `peer_auth: mtls` mutually authenticates every internal leg — both dispatch transports (gRPC and the HTTP dispatch bridge), the async-callback return leg, and services' publish-back-to-control-API calls (2026-07-16, peer-auth-posture, transcript).
- Under `mtls`, startup fails closed when RIMSKY_CA_ENCRYPTION_KEY is missing or malformed (2026-07-16, peer-auth-posture, transcript).
- Enrollment is gated by the `service:enroll` permission; the leaf's SAN binds the calling key's id; revoking the key stops renewal and the cert ages out within its TTL (no CRL) (2026-07-16, peer-auth-posture, transcript).
- The run-token (`supervisor_id:node_run_id`) and the executor-chosen `async_ack_id`-as-credential are swept; the return leg is authenticated by the mTLS peer identity under `mtls` and the trusted-subnet assumption under `none`; `async_ack_id` is correlation-only (2026-07-16, peer-auth-posture, transcript).
- The host-agent→proxy hop uses TLS with a pinned deployment-CA root and the user's api-key inside the channel (no client cert). The agent↔spawned-child loopback is secured by mandatory mutual mTLS against a self-contained LOCAL CA the agent runs as its own enrollment authority — a trust domain separate from the deployment's peer_auth CA, minting no ledger keys and needing no rimsky permission; the child self-enrolls via the unchanged bundled peer-auth path against a plaintext bootstrap enroll endpoint, and both dispatch and callback legs run mTLS, so the loopback is secured independently of the deployment's peer_auth posture (2026-07-16, peer-auth-posture, transcript).
- The webhook sensor requires per-subscription auth (hmac | secret_header | none), fail-loud — an absent `auth` block is refused at bind time (2026-07-16, peer-auth-posture, transcript).
- The OpenLineage subscriber can present an optional outbound bearer token to a secured receiver (2026-07-16, peer-auth-posture, transcript).
- The HTTP `/v1/Execute` dispatch bridge is a boundary-2 surface (rimsky's own HTTP executor client dials it, not a public endpoint); under `mtls` it enforces the same client-cert verification as gRPC, and its port is no longer published by default (2026-07-16, peer-auth-posture, transcript).

## Intentional absences

- A separate internal-service credential kind or table — the api-key with `service:enroll` IS the enrollment credential; a second credential type would split the principal registry (2026-07-16, peer-auth-posture, transcript).
- A CRL — short TTLs plus stop-renewal-on-revoke replace it (2026-07-16, peer-auth-posture, transcript).
- A client certificate for the host-agent — it is per-user session tooling keyed to a human's api-key, not an enrollable standing service (2026-07-16, peer-auth-posture, transcript).
- `peer_auth: mtls` on by default — rejected: breaks zero-config local dev and testcontainers, which have no CA (2026-07-16, peer-auth-posture, transcript).
- External IdP termination (OIDC/SAML/mTLS-termination at the edge) as a rimsky feature — still out of scope by design; the new mTLS is INTERNAL (service↔service) and derives from the api-key ledger, distinct from terminating an external identity at the edge (2026-07-16, peer-auth-posture, transcript).
- Rimsky-core ownership of the SSRF egress guard — that is a bundled-image property; core authenticates WHO it dispatches to, not what the work does (2026-07-16, peer-auth-posture, transcript).

## Corrections and restorations (drift-fight record)

- The internal-service-auth-unspeced tension (open, cataloged as "no mechanism for rimsky-process-to-rimsky-process authentication; deployment-level network isolation instead; not solved in v1") is RESOLVED by this posture: mTLS via per-process certificates was chosen, but the cert is derived from an api-key rather than a new credential kind, and it is optional (default off) rather than mandatory (2026-07-16, peer-auth-posture, transcript).
- Prior dossier entries recording process-to-process auth as an intentional v1 absence (host-agent, host-agent-proxy, service, control-api) are superseded for the boundary-2 axis by this posture; they remain accurate for the DEFAULT (`none`) mode (2026-07-16, peer-auth-posture, transcript).

## Conflicts needing human ruling

None recorded.
