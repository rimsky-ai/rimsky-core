---
concept: peer-auth
aliases:
  - internal-service-auth
  - mtls
  - peer_auth
---

# Peer authentication

## What it is

Peer authentication is the posture rimsky holds across its four trust boundaries, and the optional mutual-certificate mechanism that authenticates one of them. The first boundary is the control plane, the operator surface a client reaches with an api-key over a secured transport (see `concept:api-key`); it is also the identity authority the other three defer to, because every principal, human or service, is a row in the api-key ledger. The second is the boundary between rimsky's runtime roles and its peer services — the scheduler dialing claim-producers, the supervisor dialing executors, and the return legs those calls carry. The third is inbound traffic a bundled service accepts from the public web: the inbound listener a bundled sensor exposes, and the hop from the host-agent to its proxy, which is TLS in every posture, the zero-config posture included, and falls back to plaintext only behind an explicit insecure switch (see `decision:host-agent-proxy-tls`). The fourth is outbound: a bundled service presenting a credential to a third party, where rimsky is the client meeting someone else's requirement.

The mechanism for the second boundary is a deployment-level switch with two modes, off by default (see `decision:peer-auth-mtls`). Off, every internal dial is plaintext and rests on the assumption of a trusted private network. On, a certificate authority for the deployment lives in the control plane, every operator-deployed standing service enrolls to obtain a short-lived leaf certificate, and both ends of every internal connection present and verify a certificate that authority signed. The leaf's subject encodes the api-key row that enrolled for it, so the certificate is the derived identity and the api-key remains the standing secret.

## Purpose

Peer authentication lets a deployment stop resting on network isolation alone: the internal call paths authenticate each other, in both directions, on every leg. It costs local development and the single-process deployment nothing, because the secured posture is a switch an operator flips (see `decision:peer-auth-mtls`).

## Boundaries

Peer authentication owns the four-boundary framing, the two-mode switch, the deployment's certificate authority and the protection of its private key, the enrollment exchange that turns an api-key carrying the enrollment grant into a short-lived leaf certificate, and the workload identity that binds a certificate back to its api-key row.

It does not own the api-key ledger or a key's life, which belong to `concept:api-key`, nor the grant that authorizes enrollment, which belongs to `concept:permission`. It does not own the narrower per-peer posture that verifies one peer's server certificate against public roots (see `decision:peer-tls-enforcement`), integration with an external identity system, or the authentication mechanisms of the third boundary themselves, which belong to `concept:sensor` and `concept:host-agent` even though both participate in this framing. It does not own what a peer does with the work once it is authenticated: rimsky authenticates who it dispatches to, never what the payload means (see `concept:inertness`). A bundled service that dials a destination its caller supplied guards that destination itself (see `decision:destination-allowlists-default-closed`).

see also: `api-key`, `permission`, `control-api`, `service`, `executor`, `claim-producer`, `supervisor`, `host-agent`, `host-agent-proxy`, `sensor`, `anonymous-mode`, `inertness`

## Aliases

- internal-service-auth
- mtls
- peer_auth
