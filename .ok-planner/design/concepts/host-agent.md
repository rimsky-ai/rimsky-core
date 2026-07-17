---
concept: host-agent
status: as-is
aliases: []
---

# Host agent

## What it is

A long-running daemon on a user's dev machine. Dials a `concept:host-agent-proxy` outbound over TLS — verifying the proxy's server certificate against a pinned deployment-CA root — and authenticates inside that encrypted channel as the USER, carrying the user's `concept:api-key`, or, when none is configured, the well-known anonymous routing identity that anonymous-mode agents register under. Serves spawn / dispatch / reap requests against locally-running binaries, and relays local-HTTP callback requests those binaries raise on to the proxy.

## Purpose

Lets users run arbitrary local binaries as rimsky services on a per-invocation basis without static deployment configuration. Eliminates the manual "start the local process, wire up reachability, trigger an instance, tear down on completion" setup that would otherwise be required for dev workflows.

## Boundaries

Owns: dev-machine process spawn/exec, its two local listeners (a plaintext bootstrap-enroll endpoint and a mutual-mTLS dispatch/callback listener), the agent-side end of the agent ↔ proxy bidi stream, child-process reaping on Reap or connection close, and the self-contained LOCAL certificate authority that secures the agent↔spawned-child loopback — a trust domain separate from the deployment's `concept:peer-auth` CA, minting no `concept:api-key` ledger rows and requiring no `concept:permission`. Does NOT own: service discovery, capability advertisement (the spawned binary advertises its own Capabilities via a handshake the agent itself drives against the child, on protocols the proxy names), persistent state across restarts, the supervisor-facing service protocols (those live on the proxy), a client certificate for the agent→proxy hop (on that hop the agent is user session tooling, not a service enrolled in the deployment CA — it authenticates to the proxy by api-key over TLS rather than by mTLS enrollment — see `concept:peer-auth`). Adjacent: `concept:host-agent-proxy`, `concept:service`, `concept:api-key`, `concept:peer-auth`.

## Invariants

- No capability config; the agent does not know in advance what binaries exist, though spawn paths may optionally be bounded by an `--allow-paths` glob allowlist, refusing any spawn outside it.
- Path resolution happens at exec time, without a shell: absolute paths are used as-is, relative paths resolve against the spawn's working directory, and bare names resolve via a `PATH` lookup.
- The agent↔spawned-child loopback runs mandatory, always-on mutual mTLS, and the agent is a self-contained LOCAL enrollment authority for it: on startup it generates a local CA (reusing the same certificate-authority machinery `concept:peer-auth` uses) and issues itself a leaf, and it serves a plaintext bootstrap enroll endpoint (`route:POST /v1/enroll`) on a local listener. The CA is a trust domain separate from the deployment's `peer_auth` CA — it mints no `concept:api-key` ledger rows and needs no `concept:permission` — so the loopback is secured independently of the deployment's `peer_auth` posture and does not require the deployment to run in `mtls` mode.
- Spawned children inherit the agent's full environment, overridable per binding on any name collision by that binding's own declared env vars, plus a per-spawn provisioning that makes the child enroll: the agent mints a fresh bootstrap token and sets `env:RIMSKY_PEER_AUTH` = `mtls`, `env:RIMSKY_API_KEY` = the token, and `env:RIMSKY_CONTROL_API_URL` = the agent's local enroll base in the child's environment. The child self-enrolls exactly as any service enrolls under `concept:peer-auth` (the bundled peer-auth path, no executor-side code change), and the agent validates its own token and issues the child a short-lived leaf from the local CA.
- Both loopback legs — the agent→child dispatch and the child→agent callback — verify the peer's local-CA leaf. A port-squatter or a plaintext-only binary holds no such leaf and fails the handshake, so forged dispatches, forged callbacks, and dispatch interception all fail closed in one mechanism. A late-bound binary that speaks only plaintext is therefore no longer a valid executor — a deliberate pre-v1 contract change.
- On bidi-stream close (clean or unclean), all live children are sent a terminate signal and force-killed after a configurable grace period.
- The agent keeps no state that changes its behavior across restarts — the informational connection-status and pid markers it and its CLI wrapper leave on disk are never read back to affect behavior — and its authentication is the user's `concept:api-key`, supplied via the `RIMSKY_API_KEY` environment variable or the `--api-key` flag (there is no active-context config), verified by the proxy before any routing.
