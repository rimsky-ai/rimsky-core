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

Owns: the operation surface and its handlers, the lifecycle-subscriber fan-out, the observability read handlers, the auth middleware and endpoint surface, the agentic-tool envelope handler and catalog, and — under `peer_auth: mtls` — the enrollment endpoint `route:POST /v1/enroll` where a `service:enroll`-bearing key exchanges for a short-lived certificate plus the CA root. The control plane hosts the per-deployment CA and is the identity authority the other trust boundaries defer to. Does NOT own: dispatch (supervisor's job), scheduling (scheduler's job), the out-of-process service protocols, the certificate lifecycle on the service side (memory-only, auto-renewed — see `concept:peer-auth`). Adjacent: `rimsky` (CLI), `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`, `peer-auth`.

## Invariants

- The operation surface is unversioned at the wire. Rolling upgrades are operator-managed.
- Lifecycle events fire from control-api (not the supervisor) synchronously at state transitions. A slow subscriber holds up the response.
- The reserved compose prefix on tags and instance keys is server-enforced: requests originating outside compose are rejected when they target it.
- **Every operation is auth-gated** except the health and readiness probes, which are unauthenticated infrastructure paths. The action registry is the canonical surface-to-action mapping; an unmapped operation is a wiring bug.
- **The agentic-tool skin shares the auth gate.** Tool invocations re-enter the routing pipeline via the catalog's invoke path, so the same action-gating middleware runs. The audit row records the protocol skin used.
- **Enrollment is gated by `service:enroll`.** Under `peer_auth: mtls` the enroll endpoint passes the same auth middleware and requires the `service:enroll` grant; it issues a short-lived leaf certificate whose SAN binds the calling key's id. The control plane fails closed at startup when `mtls` is on but the CA encryption key is missing or malformed (see `concept:peer-auth`).

## Skin-as-implementation

The agentic-tool skin is hosted in-process by the control-api. Tool invocations dispatch back into the router via an in-process handler — there is no self-loopback round trip.
