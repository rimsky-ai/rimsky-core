---
concept: lifecycle-subscriber
status: as-is
aliases: []
---

# Lifecycle subscriber

## What it is

A service implementing the lifecycle-subscriber protocol, which delivers control-plane state transitions and run-scope terminals to peer services that opt in. Opt-in per service by declaring the lifecycle-subscriber protocol alongside the service's primary protocol — `concept:claim-producer`, `concept:executor`, or `concept:publisher` — in its protocol list. Any peer-service kind can subscribe; the slot a service occupies in a template is orthogonal to whether it implements lifecycle-subscriber. Idempotency is tracked in a persisted ledger keyed by service, event type, and object.

## Purpose

Some peers need to react to control-plane state transitions. Archetypes the protocol enables include claim-producers that apply per-template substrate setup at template-deploy time, executors that warm caches at instance creation, and publishers that provision substrate at template-deploy and tear it down at undeploy. A separate optional protocol on the same service binary keeps primary-protocol-only implementations simple and lets reactive implementations subscribe explicitly.

## Boundaries

The protocol relays the **control-plane / instance lifecycle** — template register / deploy / undeploy / deregister, instance created / terminated, and run-scope terminal — and deliberately does NOT carry node-cascade events (individual node-run transitions such as a node parking). Node-cascade transitions live in `concept:signal` / `concept:event-log`; their omission here is an intentional boundary, not an arbitrary subset of the lifecycle. A subscriber that needs to observe node-level state changes consumes those concepts, not this protocol.

Owns: the lifecycle event taxonomy, the fan-out timing, the opt-in subscription mechanism, idempotency tracking. Does NOT own: the underlying state transitions (those happen in `concept:control-api` for template/instance events and main-scope run-scope-terminal, and in `concept:supervisor` for sub-graph and fan-out-partition run-scope-terminal), the subscriber-side reaction (lives in the subscribing service). Lifecycle events fire from three delivery sites: control-api for template events, instance-created, and run-scope-terminal on a main scope it closes, all fired synchronously within the request that performs the transition; the supervisor, via its own subscriber registry, for run-scope-terminal on a sub-graph or fan-out-partition scope it closes, also synchronous; and a dedicated poll loop in control-api that periodically scans for terminated instances and fires instance-terminated asynchronously, decoupled from the request that terminated the instance. Adjacent: `claim-producer`, `executor`, `publisher`, `sensor`, `template`, `instance`, `control-api`, `supervisor`, `host-agent-proxy`, `signal`, `event-log`.

## Invariants

- Lifecycle-subscriber events fire synchronously from the rimsky-side process that owns the state transition, with one exception: template events, instance-created, and main-scope run-scope-terminal events fire synchronously from `concept:control-api`; sub-graph and fan-out-partition run-scope-terminal events fire synchronously from `concept:supervisor`, via its own subscriber registry. A slow subscriber holds up the firing process's path for these. Instance-terminated is the exception: the instance-terminate request itself does not fire the event; a periodic poll loop inside control-api detects terminated instances and fires instance-terminated asynchronously, so delivery latency for this one event type is bounded by the poll interval rather than by the terminating request's own completion.
- Idempotency at the rimsky side guarantees at-least-once delivery, not exactly-once: a delivery attempt is retried until the ledger records success, so a replay of an already-recorded delivery is a no-op, but a genuine state re-transition (redeploy after undeploy, re-registration after deregistration) legitimately re-fires the corresponding callback. Subscriber handlers must be idempotent. The ledger is preserved across all delivery sites.
- Every delivery site guards its per-peer [check ledger row, deliver, mark row] section with the per-lifecycle-scope lock of `concept:advisory-lock`, inside a single transaction, so concurrent fan-outs for one scope — whether racing goroutines in one process or racing control-api/scheduler/supervisor replicas sharing the database — converge to a single delivery per peer.
- Peers referenced by a template but not subscribed silently skip fan-out (non-subscription is the default).
- For instance-keyed and run-scope-keyed events, the fan-out candidate set additionally includes any late-bound service proxies configured for the template's late-bind services, even though those proxies are never named in the template itself; template events fan out only to template-referenced peers. This is what lets `concept:host-agent-proxy` populate its per-instance service-bindings cache without appearing in the template.
- The template-registered callback carries the template's canonical spec bytes (deterministically re-hashable).
