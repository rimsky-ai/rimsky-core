---
concept: peer-auth
status: as-is
aliases:
  - internal-service-auth
  - mtls
  - peer_auth
---

# Peer authentication

## What it is

The posture governing authentication across rimsky's four trust boundaries, and specifically the optional mutual-TLS mechanism that authenticates the internal service↔service boundary. The four boundaries are:

1. **Control plane** — the control API's operator surface, authenticated by `concept:api-key` bearer tokens over TLS. This boundary is also the identity authority the other three defer to: every principal, human or service, is a row in the api-key ledger.
2. **Internal service↔service** — scheduler↔claim-producers, supervisor↔executors, and their return legs (the async-callback and publish-back-to-control-API calls). By default this boundary assumes a trusted private subnet with no authentication; when `peer_auth: mtls` is set it is mutually authenticated by CA-signed certificates.
3. **Bundled-service ingress from the public web** — the webhook sensor's inbound port and the host-agent→`concept:host-agent-proxy` hop. Each carries its own per-boundary authentication (`concept:sensor` webhook auth; the agent's pinned-root TLS).
4. **Outbound** — a bundled service presenting a credential TO a third party (the OpenLineage subscriber's optional bearer token). Here rimsky is the client complying with someone else's auth requirement.

The mutual-TLS mechanism for boundary 2 is a deployment-level switch `peer_auth: none|mtls` (default `none`). Under `none` every internal dial is plaintext against the trusted-subnet assumption — zero-config, unchanged for local dev and testcontainers. Under `mtls` a per-deployment certificate authority lives in the control plane, every operator-deployed standing service enrolls to obtain a short-lived leaf certificate, and both peers of every internal connection present and verify CA-signed certificates.

## Purpose

Production deployments want the internal call paths authenticated rather than resting solely on network isolation, but local development and the single-process all-in-one must stay zero-config. A default-off deployment switch delivers both: the secured posture is one config flip, and its absence costs nothing. mTLS rather than more api-keys because rimsky's internal peers are mostly OUTBOUND clients (the scheduler dials producers, the supervisor dials executors), and only a certificate lets the SERVER side of a connection be authenticated too — while keeping the standing secret off the wire.

## Boundaries

Owns: the four-boundary framing; the `peer_auth` deployment switch and its two modes; the per-deployment CA (issuance, the encrypted-at-rest private key, the CA root distributed to enrolled services); the enrollment exchange that turns a `service:enroll`-bearing api-key into a short-lived leaf certificate; the SPIFFE-style principal binding a certificate back to its api-key row; the rule of thumb for which credential a given participant carries.

Does NOT own: the api-key ledger itself, its lifecycle, or the `service:enroll` grant's evaluation (those are `concept:api-key` and `concept:permission`); the per-peer `tls` config key that verifies a single peer's server certificate against system roots (that is `decision:peer-tls-enforcement` — a narrower, orthogonal server-verification knob); external IdP integration (out of scope, deployment-edge concern); the webhook and agent-hop authentication mechanisms themselves (owned by `concept:sensor` and `concept:host-agent` respectively, though they participate in this framing); what an executor does with its payload once authenticated (`concept:inertness` — rimsky authenticates WHO it dispatches to, not what the work does).

Adjacent: `concept:api-key`, `concept:permission`, `concept:control-api`, `concept:service`, `concept:executor`, `concept:claim-producer`, `concept:supervisor`, `concept:host-agent`, `concept:host-agent-proxy`, `concept:sensor`, `concept:anonymous-mode`.

## Invariants

- **Default off.** `peer_auth` defaults to `none`. Under `none` internal dials are plaintext and no CA, enrollment, or certificate machinery is required — the trusted-private-subnet model, identical to the pre-mTLS posture.
- **Everything is a key.** Rimsky has no user entity; the api-key ledger is the entire principal registry. A service principal IS an api-key. A join token, as originally conceived, reduces to an api-key carrying the `service:enroll` grant — there is no separate credential type and no separate table.
- **The api-key is the standing secret; the certificate is the derived identity.** The api-key authorizes OBTAINING an identity; enrollment mints the short-lived cert that proves it. The api-key crosses the wire once (at enroll); thereafter the cert's private key proves possession by signature per handshake and the key never crosses the wire again. Rimsky stores only the api-key hash plus public CA material, so a dumped ledger leaks no usable service secret.
- **CA private key encrypted at rest under mtls.** The CA private key is encrypted with authenticated symmetric encryption under an operator-supplied key from the environment. When `peer_auth` is `mtls` and that key is missing or malformed, startup fails closed.
- **Leaf certs are short-lived, memory-only, always-rejoin.** An enrolled service receives a short-lived leaf certificate, its private key, and the CA root via the control plane's enrollment endpoint. Certificates are never persisted service-side; the service auto-renews before expiry. Revoking the api-key stops renewal, and the cert ages out within its TTL — there is no CRL.
- **The certificate SAN carries the principal.** The leaf's SAN is a SPIFFE-style principal `spiffe://rimsky/<key-id>`, so the api-key row IS the authenticated service identity on the wire; peer authorization reads the key id from the cert.
- **Mutual, both directions, every internal leg.** Under `mtls` both peers present and verify CA-signed certs. Coverage is uniform across both dispatch transports (gRPC and the HTTP dispatch bridge), the supervisor async-callback return leg, and services' publish-back-to-control-API calls.
- **Peer identity replaces the run-token.** Under `mtls` the mTLS peer identity is the authenticator on both the outbound dispatch and its return leg; under `none` the trusted-subnet assumption is. The former per-call `supervisor_id:node_run_id` scratch/callback token and the executor-chosen `async_ack_id`-as-credential are gone; `async_ack_id` remains purely a correlation key (which run a callback settles), never an authenticator.

## Rule of thumb

- Operator-deployed standing service (wherever hosted) → api-key + mTLS enrollment.
- User-owned session tooling (the host-agent) → api-key + TLS to a pinned deployment-CA root, no client cert (it is per-user session tooling, not an enrollable standing service).
- The outbound-egress SSRF guard is a property of the bundled images, not of rimsky core; core keeps its inertness guarantee.
