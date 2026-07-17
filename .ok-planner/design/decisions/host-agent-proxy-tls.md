---
decision: host-agent-proxy-tls
status: as-is
aliases:
  - agent-proxy-tls
  - agent-child-loopback-mtls
---

# The agent dials the proxy over pinned-root TLS and secures the child loopback with mandatory mTLS from a self-contained local enrollment authority

## Choice

Two distinct host-agent hops, secured two different ways.

The agent→proxy hop (dev-machine→deployment): the host-agent dials the `concept:host-agent-proxy` over TLS, verifying the proxy's server certificate against a PINNED deployment-CA root; the user's api-key rides inside the encrypted channel. The agent authenticates as the USER (api-key identity, already verified by the proxy via `route:GET /v1/auth/whoami`) and does NOT receive a client certificate — it is per-user session tooling, not a service enrolled in the deployment CA (see `concept:peer-auth`, `concept:host-agent`).

The agent↔spawned-child loopback: secured by mandatory, always-on mutual mTLS, with the daemon acting as a self-contained LOCAL enrollment authority — a trust domain entirely separate from the deployment's `peer_auth` CA, requiring no rimsky permission and minting no api-key ledger rows. On startup the daemon generates a local CA (reusing the same certificate-authority machinery `concept:peer-auth` uses) and issues itself a leaf; it serves a plaintext bootstrap enroll endpoint (`route:POST /v1/enroll`) on a local listener. Per spawn it mints a fresh bootstrap token and provisions the child's environment with `env:RIMSKY_PEER_AUTH` = `mtls`, `env:RIMSKY_API_KEY` = the token, and `env:RIMSKY_CONTROL_API_URL` = the daemon's local enroll base; the child self-enrolls exactly as any service enrolls under `concept:peer-auth` (unchanged executor code — the bundled peer-auth path), and the daemon validates its own token and issues the child a short-lived leaf from the local CA. Both loopback legs — the agent→child dispatch and the child→agent callback — then run mutual mTLS against that local CA, across two local listeners (plaintext enroll vs mTLS dispatch/callback). Because the CA is self-contained, the loopback is secured independently of the deployment's `peer_auth` posture — it does not require the deployment to run in `mtls` mode.

## Rationale

The agent→proxy hop crosses a dev-machine→deployment boundary; sending the api-key in plaintext there exposed it. Pinning the deployment-CA root gives the agent a server it can trust without a public PKI. A client cert would be the wrong tool on that hop — the agent is short-lived per-user tooling, not a standing service with an enrollment lifecycle.

For the loopback, a per-spawn shared secret was an ad-hoc reimplementation of what mutual TLS already does: the TLS handshake IS the proof-of-possession. Reusing enrollment + mTLS is the single-idiom choice — it costs no new executor code (the child runs the existing bundled peer-auth self-enroll path) and closes the port-squat dispatch-interception, forged-callback, and forged-dispatch cases uniformly in one mechanism, rather than guarding a single direction with a bespoke credential. A port-squatter or a plaintext-only binary holds no local-CA leaf and fails the handshake. The consequence is deliberate: a plaintext-only binary is no longer a valid late-bound executor — a pre-v1 contract change accepted for the uniform closure it buys.

## Alternatives

- **A per-spawn shared secret (env var plus a callback header) guarding only the callback leg** — rejected: it reimplements the proof-of-possession the TLS handshake already provides, secures only the child→agent callback direction (leaving forged dispatch and dispatch interception open), and adds a bespoke credential path instead of reusing enrollment. Backed out entirely in favor of loopback mTLS.
- **Give the agent a client certificate via deployment enrollment** — rejected for the agent→proxy hop: the agent is session tooling keyed to a human's api-key, not an operator-deployed service; api-key-over-TLS is the right credential for it. (The loopback CA is a separate, LOCAL authority the daemon runs itself, not deployment enrollment.)
- **Trust the loopback unconditionally** — rejected: other local processes (other UIDs) can reach a loopback listener, so an unauthenticated dispatch or callback surface is forgeable.
- **Gate the loopback mTLS on the deployment being in `peer_auth: mtls`** — rejected: the loopback threat (local port-squatting) exists regardless of the deployment's posture, and dev boxes routinely run against `none`-mode deployments; a self-contained local CA secures the loopback always.
