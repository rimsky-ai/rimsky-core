---
concept: lifecycle-subscriber
status: as-is
aliases: []
---

# Lifecycle subscriber

## What it is

A service implementing the lifecycle-subscriber protocol, which delivers control-plane state transitions and run-scope terminals to peer services that opt in. Opt-in per service by declaring the lifecycle-subscriber protocol alongside the service's primary protocol — `concept:claim-producer`, `concept:executor`, or `concept:publisher` — in its protocol list. Any peer-service kind can subscribe; the slot a service occupies in a template is orthogonal to whether it implements lifecycle-subscriber. Idempotency is tracked in a persisted per-(service, event) ledger.

## Purpose

Some peers need to react to control-plane state transitions. Archetypes the protocol enables include claim-producers that apply per-template substrate setup at template-deploy time, executors that warm caches at instance creation, and publishers that provision substrate at template-deploy and tear it down at undeploy. A separate optional protocol on the same service binary keeps primary-protocol-only implementations simple and lets reactive implementations subscribe explicitly.

## Boundaries

The protocol relays the **control-plane / instance lifecycle** — template register / deploy / undeploy / deregister, instance created / terminated, and run-scope terminal — and deliberately does NOT carry node-cascade events (individual node-run transitions such as a node parking). Node-cascade transitions live in `concept:signal` / `concept:event-log`; their omission here is an intentional boundary, not an arbitrary subset of the lifecycle. A subscriber that needs to observe node-level state changes consumes those concepts, not this protocol.

Owns: the lifecycle event taxonomy, the synchronous fan-out timing, the opt-in subscription mechanism, idempotency tracking. Does NOT own: the underlying state transitions (those happen in `concept:control-api` for template/instance events and in the `concept:supervisor` for run-scope-terminal events), the subscriber-side reaction (lives in the subscribing service). Lifecycle events fire from two firers: the supervisor and control-api. The supervisor maintains its own subscriber registry and fires the run-scope-terminal event synchronously when it closes a run scope. Adjacent: `claim-producer`, `executor`, `publisher`, `sensor`, `template`, `instance`, `control-api`, `supervisor`, `host-agent-proxy`, `signal`, `event-log`.

## Invariants

- Lifecycle-subscriber events fire synchronously from the rimsky-side process that owns the state transition: template / instance events from `concept:control-api`; run-scope-terminal events from the `concept:supervisor` that closes the scope. A slow subscriber holds up the firing process's path.
- Idempotency at the rimsky side: each `(service, event)` pair fires exactly once. The DB-tracked idempotency ledger is preserved across both firing sites.
- Peers referenced by a template but not subscribed silently skip fan-out (non-subscription is the default).
- The template-registered callback carries the template's canonical spec bytes (deterministically re-hashable).
