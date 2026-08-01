---
story: peer-auth-mtls-mutual
status: as-is
---

# Operator enables mutual TLS on internal service traffic

## Role

As an operator hardening a production deployment, I set `peer_auth: mtls` and provide the CA encryption key, and every internal service↔service connection becomes mutually authenticated by CA-signed certificates — while my local dev and testcontainer stacks keep working unchanged with the default `none`, so I get an authenticated internal plane by one config flip and pay nothing for it when I don't need it.

## Capability

A deployment-level switch `peer_auth: none|mtls` (default `none`). Under `none` internal dials are plaintext against the trusted-private-subnet assumption. Under `mtls` a per-deployment CA lives in the control plane (its private key encrypted at rest under an operator-supplied 32-byte key; startup fails closed when the key is missing or malformed), and both peers of every internal leg — both dispatch transports (gRPC and the HTTP dispatch bridge), the supervisor async-callback return leg, and services' publish-back-to-control-API calls — present and verify CA-signed certificates (see `concept:peer-auth`, `decision:peer-auth-mtls`).

## Business value

Production deployments get their internal call paths authenticated rather than resting on network isolation alone; local dev and the single-process all-in-one stay zero-config because the secured posture is opt-in.

