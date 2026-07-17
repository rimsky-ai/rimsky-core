---
decision: peer-auth-mtls
status: as-is
aliases:
  - peer-auth-switch
---

# Internal service auth is optional mutual TLS, default off

## Choice

A deployment-level switch `peer_auth: none|mtls`, default `none`, governs the internal service↔service boundary (scheduler↔claim-producers, supervisor↔executors, and their return legs). Under `none` the internal dials are plaintext against the trusted-private-subnet assumption — zero-config, unchanged for local dev and testcontainers. Under `mtls` a per-deployment CA lives in the control plane (its private key encrypted at rest with AES-256-GCM under `env:RIMSKY_CA_ENCRYPTION_KEY`, a base64 32-byte key; fail-closed at startup when mtls is on and the key is missing or malformed); every operator-deployed standing service enrolls to obtain a short-lived (24h) leaf certificate, its key, and the CA root; and both peers of every internal connection present and verify CA-signed certificates. Coverage is uniform across both dispatch transports (gRPC and the HTTP dispatch bridge), the supervisor async-callback return leg, and services' publish-back-to-control-API calls (see `concept:peer-auth`).

## Rationale

Production wants the internal call paths authenticated rather than resting on network isolation alone; local dev and the single-process all-in-one must stay zero-config. A default-off switch delivers both — the secured posture is one config flip and its absence costs nothing.

## Alternatives

- **More api-keys everywhere (api-key on every internal call)** — rejected. Api-keys are a server-inbound bearer pattern, but rimsky's internal peers are mostly OUTBOUND clients (the scheduler dials producers, the supervisor dials executors). Only a certificate lets the SERVER side of a connection be authenticated too, and only a certificate keeps the standing secret off the wire — the api-key transits once at enroll, then the cert's private key proves possession by signature per handshake. An api-key on every call would put a reusable secret on every internal hop.
- **Default on** — rejected: breaks zero-config local use and testcontainers, which have no CA and no operator config at all.
- **Terminating external identity (OIDC/SAML/mTLS) at rimsky's edge** — out of scope; that is a deployment-edge concern (see `concept:api-key`).
