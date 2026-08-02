---
concept: service-address-book
---

# Service address book

## What it is

The shared, persisted catalog of the deployment's declared dispatch peers — executor names and claim-producer store names, each mapped to its endpoint. The control plane publishes the deployment's declarations into it at startup and on configuration reload; every supervisor resolves executor and store names against it read-through (a short-lived cache in front of the shared record) rather than holding a boot-time snapshot of its own process configuration.

## Purpose

Makes dispatch routing state shared and current instead of per-process and frozen at boot. A supervisor never acts on another runtime moment's service list — it is handed the current one, or refreshes it, when it goes to work. Because every supervisor can resolve every declared name, a queued run can never wait unclaimable because no supervisor's private accept-list matches its executor or its required stores: a name that resolves nowhere fails inside a claimed dispatch, in the loud unresolved-service error path, instead of stalling silently in the queue.

## Boundaries

Owns: the persisted name-to-endpoint catalog for executors and claim-producer stores, its publish-on-boot-and-reload lifecycle, and the read-through resolution semantics. Does NOT own: instance-scoped late-bound service bindings (see `concept:host-agent-proxy`), dispatch itself (see `concept:supervisor`), claim-state semantics (see `concept:claim-producer`), registration-time reference validation mechanics (see `concept:template`). Adjacent: `concept:supervisor`, `concept:executor`, `concept:claim-producer`, `concept:control-api`, `concept:host-agent-proxy`.

## Invariants

- The control plane is the sole writer: it publishes the full declared executor and store sets at startup and on configuration reload.
- Supervisors resolve executor and store names read-through against the shared catalog with a short-lived cache; no supervisor registers or persists a private accepted-executor or accepted-store set, and no service name participates in claim-time candidate filtering.
- Template registration validates declared executor and store names against the same catalog (late-bind names excepted, per `concept:template`), so the registration-time check and dispatch-time resolution share one source of truth.
- Universal reachability is a deployment requirement: every supervisor can reach every declared executor and store. There is no implicit reachability partitioning; if partitioning is ever genuinely needed it arrives as a deliberate, explicit feature, never as a side effect of per-process configuration.
- A name that resolves nowhere fails the claimed dispatch with an unresolved-service error; a queue row that no supervisor could ever claim on account of its executor or store names is unrepresentable.
