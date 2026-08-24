---
concept: service
---

# Service

## What it is

A service is a rimsky-orchestrated implementation of one or more of rimsky's service protocols. A service runs either as a binary in its own process or as a handler inside the rimsky all-in-one process. Rimsky dispatches to both forms across the same protocol surface.

## Purpose

A service is the orchestrated-resource side of rimsky's runtime: rimsky's own roles orchestrate services, and services do the work. That split makes a third-party implementation first-class and keeps the bundled reference implementations decoupled from rimsky core.

## Boundaries

A service owns how it declares which protocols it implements. A service in its own process declares its protocol membership in the deployment's one configuration file (see `concept:rimsky-yml`); a bundled in-process handler declares the same membership by registering programmatically (see `decision:bundled-registry-entrypoint`). A service owns its startup capabilities handshake, one call per protocol it implements — `concept:observability` carries the handshake, and `concept:discovery-cache` carries the cache that consumes it. A service owns its conformance-validation entry points (see `concept:conformance`). A service implementing several protocols composes one handler per protocol rather than collapsing them into one (see `decision:multi-protocol-service-distinct-handler-per-protocol`). A service authenticates to its services under `concept:service-auth`.

The individual service protocols are sibling concepts: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, and `concept:publisher`, of which `concept:sensor` is one class. Orchestration mechanics are their own concepts too: dispatch and supervisor coordination in `concept:supervisor`, terminal resolution in `concept:terminal-resolution` and `concept:auto-terminal`, and reclamation of abandoned work in `concept:orphan-reaper`.

See also: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:publisher`, `concept:sensor`, `concept:supervisor`, `concept:rimsky-yml`, `concept:conformance`, `concept:observability`, `concept:discovery-cache`, `concept:service-auth`.
