---
concept: host-agent
---

# Host agent

## What it is

The host agent is a long-running daemon on a developer's machine. It dials a `concept:host-agent-proxy` outbound over TLS, verifying the proxy's server certificate against a pinned deployment-CA root, and it authenticates inside that channel as the user. It carries the user's `concept:api-key`, or, when the user has configured none, an anonymous routing identity: either a routing label the agent supplies, or a name the proxy assigns on first connect and the agent re-presents thereafter (see `concept:host-agent-proxy`). That hop runs TLS in every posture (see `decision:host-agent-proxy-tls`). The agent serves spawn, dispatch, and reap requests against binaries running locally, and relays the local callbacks those binaries raise onward to the proxy.

## Purpose

The host agent lets a developer run local binaries as rimsky services for the length of one invocation, with no deployment configuration. The agent starts the local process, makes it reachable to rimsky, and tears it down when the work finishes, so the developer does none of that by hand.

## Boundaries

The agent owns process spawn and exec on the dev machine, including each binding's own environment, arguments, working directory, and readiness timeout (see `story:host-agent-per-binding-overrides`). It owns how a binding's path resolves (see `decision:host-agent-path-resolution-anchored-to-agent-cwd`). It owns the port a spawned child listens on (see `decision:host-agent-port-assignment-no-handshake`). It owns its two local listeners, one for bootstrap enrollment and one mutually authenticated for dispatch and callbacks, its own end of the connection to the proxy, and the reaping of children when a reap arrives or that connection closes. It owns the self-contained local certificate authority that secures the loopback between itself and a spawned child: a trust domain separate from the deployment's (see `concept:peer-auth`), minting no `concept:api-key` and needing no `concept:permission`. It owns the local state that carries an assigned anonymous routing identity across restarts.

The agent does not own service discovery, or capability advertisement, which the spawned binary makes through a handshake the agent drives against the child on the protocols the proxy names. It holds no across-restart behavior state beyond the anonymous routing identity. It does not own the supervisor-facing service protocols, which live on the proxy, or a client certificate for its own hop to the proxy, where it is user session tooling rather than a service enrolled in the deployment CA (see `concept:peer-auth`). See also `concept:host-agent-proxy`, `concept:service`, and `concept:anonymous-mode`.
