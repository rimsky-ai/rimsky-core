---
concept: host-agent-proxy
status: as-is
aliases: []
---

# Host agent proxy

## What it is

A rimsky-stack `concept:service` implementing the multi-protocol composition pattern (per `concept:service` invariants: distinct handler types per protocol, separately registered on one server). Presents the rimsky service protocols on the supervisor-facing side. Maintains agent connections on the dev-facing side via a long-lived agent-connection protocol. Routes dispatches to whichever agent is connected for the instance's owner. Declared in the rimsky config (`concept:rimsky-yml`) once per protocol it serves, all entries pointing at the same binary.

## Purpose

Lets rimsky dispatch work to dev-machine binaries declared per-instance, while the supervisor and graph-processing layers see only the standard service protocols. The proxy implements the dispatcher and the URL-rewriting boundary; the supervisor, dispatch resolution, error vocabulary, and callback handling traffic in the platform's standard vocabulary.

## Boundaries

Owns: the agent ↔ proxy protocol, the spawn-lifecycle state machine, the per-instance service-bindings cache (populated via `concept:lifecycle-subscriber`, with a cache-miss fallback that fetches the instance directly from the control API and caches the result, and evicted on the instance's termination lifecycle callback so entries do not accumulate over a long-running proxy's lifetime and a terminated instance's binding cannot shadow a later instance that reuses the same binding name), the per-protocol dispatch handlers that proxy through to spawned processes, the callback-URL rewriting that lets spawned processes post to the agent's local listener rather than dialing the supervisor. Does NOT own: the rimsky-side service protocols themselves (those are `concept:executor`, `concept:claim-producer`, etc.), the supervisor's dispatch logic, the per-instance state (that's `concept:instance`), the lifecycle-subscriber wire protocol (that's `concept:lifecycle-subscriber`). Adjacent: `concept:host-agent`, `concept:service`, `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:instance`, `concept:rimsky-yml`, `concept:peer-auth`.

## Invariants

- Implemented via the existing multi-protocol composition pattern on `concept:service` — distinct handler types, no shared capabilities provider.
- One spawn per (run-scope, binding-name), lazy birth on first dispatch, run-scope-lifetime, reaped on run-scope termination.
- Routing resolves the serving agent by the instance owner's api-key for ordinary instances, OR — for owner-less instances created in `concept:anonymous-mode` — by a well-known anonymous routing identity under which the anonymous-mode agent registers. An owner-less-instance dispatch routes to that anonymous agent rather than hard-failing; anonymous mode and late-bound services are not mutually exclusive.
- The agent-facing side is served over TLS: the proxy presents a server certificate the agent verifies against a pinned deployment-CA root, so the agent's api-key transits an encrypted channel rather than plaintext over the dev-machine→deployment hop (see `concept:peer-auth`). The agent is user session tooling and presents no client certificate; it authenticates by api-key inside that channel.
- Registration is authenticated. `Register.api_key` carries the agent's `concept:api-key` plaintext (or the anonymous sentinel when the agent has none); the proxy verifies it against the control API's identity surface (`GET /v1/auth/whoami`) and adopts the routing identity the control API reports — the key's id for a real api-key, the anonymous routing identity when the control API itself is in `concept:anonymous-mode`. The presented value is never used verbatim as a routing identity: unknown, revoked, expired, or unverifiable credentials are rejected before any routing-table mutation, so an unauthenticated client can neither displace a registered agent nor receive an owner's dispatches. A proxy with no control-API URL fails closed and accepts no registrations.
- All dispatch failures surface as executor-error / claim-producer-unavailable terminals on the supervisor-facing protocol — no new synthetic supervisor-side acquire error classes.
- The proxy is declared in the rimsky config per protocol it serves, using the same binary across all entries (one endpoint, N namespace registrations).
- The proxy is the URL-rewriting boundary for rimsky URLs handed to spawned processes: the callback URL is the only URL it rewrites.
- The proxy's sanctioned late-bind surface is `concept:executor` and `concept:claim-producer`: both are transparent forwarders through one uniform spawn/forward mechanism, each presenting exactly the fronted service's protocol, and a service that conforms to its own protocol works behind the proxy by construction — so the proxy needs no protocol of its own to conform to; its transparency is proven instead by running the standard executor and claim-producer conformance suites against it, behind a spawned process stood up by an in-process agent test double. Late-binding `concept:publisher`, `concept:validation`, or `concept:data-processing` through the proxy is rejected loudly (an unimplemented-protocol error, not silent non-routing); the proxy is not the routing mechanism for those protocols.
- Concurrent dispatches on one spawn multiplex over the single agent connection: each dispatch carries its own stream identifier, so a slow in-flight call never blocks a faster one sharing the same spawned process, and only one Spawn is issued even when several dispatches race to be the first against a given (run-scope, binding).
- The schema a specific spawned binary actually accepts is unknowable until that binary's own capabilities handshake completes at spawn time, so attribute-schema conformance for late-bound executors is deferred from the usual registration-time check to per-dispatch: once a binary is spawned, the resolved attribute values are checked against the schema that binary itself advertised, and a mismatch settles the dispatch as a contract error rather than forwarding a payload the binary never promised to accept.
