---
decision: host-daemon-proxy-tls
aliases:
  - daemon-proxy-tls
  - daemon-child-loopback-mtls
---

# The daemon dials the proxy over pinned-root TLS and secures the child loopback with mandatory mTLS from a self-contained local enrollment authority

## Choice

Two distinct host-daemon hops, secured two different ways.

The daemon→proxy hop (dev machine → deployment): the host-daemon dials the `concept:host-daemon-proxy` over TLS, verifying the proxy's server certificate against a PINNED deployment-CA root; the user's api-key rides inside the encrypted channel. The daemon authenticates as the USER and holds no client certificate — it is per-user session tooling, not a service enrolled in the deployment CA (see `concept:service-auth`, `concept:host-daemon`). The proxy's daemon-facing listener serves TLS in every posture: from a mounted keypair, from its mutual-TLS enrollment leaf, or — in the zero-config posture, where neither exists — from a leaf the proxy mints under a CA it generates locally and publishes for the daemon to pin. Plaintext on this hop exists only behind an explicit insecure switch set on both ends, for a deployment whose network already encrypts the hop.

The daemon↔spawned-child loopback: secured by mandatory, always-on mutual mTLS, with the daemon acting as a self-contained LOCAL enrollment authority — a trust domain entirely separate from the deployment's service-auth CA, requiring no rimsky permission and minting no api-key ledger rows. Per spawn the daemon provisions the child with a fresh bootstrap credential, and the child self-enrolls through the same enrollment idiom any service uses under `concept:service-auth` (unchanged executor code), receiving a short-lived leaf from the local CA. Both loopback legs — the daemon→child dispatch and the child→daemon callback — then run mutual mTLS against that local CA. Because the authority is self-contained, the loopback is secured independently of the deployment's service-auth posture.

## Rationale

The daemon→proxy hop crosses a dev-machine→deployment boundary; sending the api-key in plaintext there exposed it. Pinning the deployment-CA root gives the daemon a server it can trust without a public PKI. A client cert would be the wrong tool on that hop — the daemon is short-lived per-user tooling, not a standing service with an enrollment lifecycle.

For the loopback, a per-spawn shared secret was an ad-hoc reimplementation of what mutual TLS already does: the TLS handshake IS the proof-of-possession. Reusing enrollment + mTLS is the single-idiom choice — it costs no new executor code (the child runs the same bundled service-auth self-enroll path as any service) and closes the port-squat dispatch-interception, forged-callback, and forged-dispatch cases uniformly in one mechanism, rather than guarding a single direction with a bespoke credential. A port-squatter or a plaintext-only binary holds no local-CA leaf and fails the handshake. The consequence is deliberate: a plaintext-only binary is not a valid late-bound executor — a pre-v1 contract change accepted for the uniform closure it buys.

## Alternatives

- **A per-spawn shared secret (env var plus a callback header) guarding only the callback leg** — rejected: it reimplements the proof-of-possession the TLS handshake already provides, secures only the child→daemon callback direction (leaving forged dispatch and dispatch interception open), and adds a bespoke credential path instead of reusing enrollment.
- **Plaintext by default on the daemon→proxy hop, TLS opt-in** — rejected: the api-key crosses a dev-machine→deployment boundary, and a default that sends it in the clear is the exposure this decision exists to close; a locally generated CA keeps zero-config local dev zero-config.
- **Give the daemon a client certificate via deployment enrollment** — rejected for the daemon→proxy hop: the daemon is session tooling keyed to a human's api-key, not an operator-deployed service; api-key-over-TLS is the right credential for it. (The loopback CA is a separate, LOCAL authority the daemon runs itself, not deployment enrollment.)
- **Trust the loopback unconditionally** — rejected: other local processes (other UIDs) can reach a loopback listener, so an unauthenticated dispatch or callback surface is forgeable.
- **Gate the loopback mTLS on the deployment being in mTLS service-auth mode** — rejected: the loopback threat (local port-squatting) exists regardless of the deployment's posture, and dev boxes routinely run against no-auth deployments; a self-contained local CA secures the loopback always.
