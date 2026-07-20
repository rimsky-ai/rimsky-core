---
concept: control-api
status: as-is
aliases: []
---

# Control API

## What it is

The operator interface exposed by the control-api binary. Serves multiple protocol skins on the same TCP port and the same operation set, covering template registration, instance lifecycle, per-instance breakpoint management, the auth surface, observability reads, and admin diagnostics. One skin is a direct request/response surface intended for scripts and operator tooling; another is an agentic-tool surface whose catalog is computed from the canonical action registry and filtered by the requesting key's permission grant.

Both skins pass through the same auth + permission middleware. Fires lifecycle-subscriber events at state transitions (synchronously; see `concept:lifecycle-subscriber`).

## Purpose

The operator, the rimsky thin-client CLI, and agentic clients all speak to this surface. The simpler request/response skin is easier to script, expose through ingress, and inspect during incidents. The agentic-tool skin is the operator-facing surface for LLM-based agents that can self-discover the catalog and dispatch tool calls.

## Boundaries

Owns: the operation surface and its handlers, the lifecycle-subscriber fan-out for template and instance events and for the administrative-termination run-scope-terminal (every remaining scope in each frame's tree, children before parents, with re-offers to unacknowledged peers), the observability read handlers, the auth middleware and endpoint surface, the agentic-tool envelope handler and catalog, and — under `peer_auth: mtls` — the enrollment endpoint `route:POST /v1/enroll` where a `service:enroll`-bearing key exchanges for a short-lived certificate plus the CA root. The control plane hosts the per-deployment CA and is the identity authority the other trust boundaries defer to. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), the sub-graph and fan-out-partition run-scope-terminal fan-out (fired by the supervisor when it closes those scopes at rendezvous), the settlement-time root run-scope-terminal fan-out (fired by the scheduler's frame engine when a frame settles), the out-of-process service protocols, the certificate lifecycle on the service side (memory-only, auto-renewed — see `concept:peer-auth`). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`, `peer-auth`.

## Invariants

- The operation surface serves a single wire version at a time, with no version negotiation and no multi-version compatibility. Rolling upgrades are operator-managed. The URL path convention (whether and which version prefix appears) is a wire detail the design does not fix.
- Lifecycle events fire synchronously from the process that owns the state transition: control-api for template and instance events and the administrative-termination run-scope-terminal, the scheduler's frame engine for the settlement-time root run-scope-terminal, the supervisor for sub-graph and fan-out-partition run-scope-terminal at rendezvous. A slow subscriber holds up the firing process's path — not necessarily the response to the request that triggered the transition, since a gracefully terminating instance's terminated event fires only once the transition actually completes.
- The reserved `compose:` prefix on tags and instance keys is server-enforced: requests originating outside the CLI's compose surface (`concept:rimsky`) are rejected when they target it.
- **Every operation is auth-gated** except the health probe, which is an unauthenticated infrastructure path. The action registry is the canonical surface-to-action mapping; an unmapped operation is a wiring bug.
- **Auth gating cannot be constructed away.** The control plane refuses to come up without an auth state wired in — there is no configuration or startup path that serves the operation surface ungated. The zero-configuration entry point for a fresh deployment is `concept:anonymous-mode`, which is itself gated (and audited) rather than a bypass of the gate.
- **No operation is reachable through two aliased routes.** An action may legitimately expose several routes when each addresses a genuinely distinct resource (a collection versus an item, or a lookup keyed a different way); it may not expose two routes that address the same resource under different paths. The action registry is the single source of the surface-to-route mapping, and pins each action's route set so a second, redundant path cannot silently accrete.
- **The agentic-tool skin shares the auth gate.** Tool invocations re-enter the routing pipeline via the catalog's invoke path, so the same action-gating middleware runs; the audit row records the protocol skin used. The skin also exposes an MCP resource surface for breakpoint-hit discovery, gated by the endpoint-level auth plus a per-read permission check rather than by router re-entry.
- **Enrollment is gated by `service:enroll`.** Under `peer_auth: mtls` the enroll endpoint passes the same auth middleware and requires the `service:enroll` grant; it issues a short-lived leaf certificate whose SAN binds the calling key's id. The control plane fails closed at startup when `mtls` is on but the CA encryption key is missing or malformed (see `concept:peer-auth`).

## Skin-as-implementation

The agentic-tool skin is hosted in-process by the control-api. Tool invocations dispatch back into the router via an in-process handler — there is no self-loopback round trip.
