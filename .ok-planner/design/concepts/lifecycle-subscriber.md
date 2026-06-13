---
concept: lifecycle-subscriber
status: as-is
aliases: []
---

# Lifecycle subscriber

## What it is

A service that implements the gRPC lifecycle-subscriber protocol — seven event callbacks: template registered, deployed, undeployed, and deregistered, plus instance created and terminated, plus run-scope terminal (carrying the run-scope id and a terminal reason). Opt-in per service by declaring the lifecycle-subscriber protocol (alongside claim-producer) in the service's protocol list. Idempotency is tracked in a persisted per-(service, event) ledger.

## Purpose

Some peers need to react to control-plane state transitions — the archetype the protocol enables is a store applying per-template DDL on the template-deployed callback. This is the *shape the protocol exists to support*, not a behavior the bundled stores ship: the bundled postgres store ships a no-op skeleton on the deployed callback — a documented fork-point an operator extends — so DDL-on-deploy is the pattern the protocol makes possible, not a shipped default. A separate optional protocol on the same service binary keeps producer-only impls simple and lets reactive impls subscribe explicitly.

## Boundaries

The protocol relays the **control-plane / instance lifecycle** — template register / deploy / undeploy / deregister, instance created / terminated, and run-scope terminal — and deliberately does NOT carry node-cascade events (individual node-run transitions such as a node parking). Node-cascade transitions live in `concept:signal` / `concept:event-log`; their omission here is an intentional boundary, not an arbitrary subset of the lifecycle. A subscriber that needs to observe node-level state changes consumes those concepts, not this protocol.

Owns: the seven event types, the synchronous fan-out timing, the opt-in subscription mechanism, the idempotency table. Does NOT own: the underlying state transitions (those happen in `concept:control-api` for template/instance events and in the `concept:supervisor` for run-scope-terminal events), the producer-side reaction (lives in the subscriber). Lifecycle events fire from two firers: the supervisor and control-api. The supervisor maintains its own subscriber registry and fires the run-scope-terminal event synchronously when it closes a run scope. Adjacent: `claim-producer`, `template`, `instance`, `control-api`, `supervisor`, `host-agent-proxy`, `signal`, `event-log`.

## Invariants

- Lifecycle-subscriber events fire synchronously from the rimsky-side process that owns the state transition: template / instance events from `concept:control-api`; run-scope-terminal events from the `concept:supervisor` that closes the scope. A slow subscriber holds up the firing process's path.
- Idempotency at the rimsky side: each `(service, event)` pair fires exactly once. The DB-tracked idempotency ledger is preserved across both firing sites.
- Peers referenced by a template but not subscribed silently skip fan-out (non-subscription is the default).
- The template-registered callback carries the template's canonical JCS spec bytes (deterministically re-hashable).
