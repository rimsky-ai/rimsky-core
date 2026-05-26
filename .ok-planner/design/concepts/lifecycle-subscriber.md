---
concept: lifecycle-subscriber
status: as-is
aliases: []
references:
  - _discover/2026-05-10-lifecycle-subscriber-opt-in.md
  - _discover/2026-05-10-content-addressed-templates.md
---

# Lifecycle subscriber

## What it is

A service that implements the gRPC lifecycle-subscriber protocol — six event callbacks: template registered, deployed, undeployed, and deregistered, plus instance created and terminated. Opt-in per service by declaring the lifecycle-subscriber protocol (alongside claim-producer) in the service's protocol list. Idempotency is tracked in a persisted per-(service, event) ledger.

## Purpose

Some peers need to react to control-plane state transitions — e.g. the bundled postgres store wants to apply per-template DDL on the template-deployed callback. A separate optional protocol on the same service binary keeps producer-only impls simple and lets reactive impls subscribe explicitly.

## Boundaries

Owns: the six event types, the synchronous fan-out timing, the opt-in subscription mechanism, the idempotency table. Does NOT own: the underlying state transitions (those happen in control-api), the producer-side reaction (lives in the subscriber). Adjacent: `claim-producer`, `template`, `instance`, `control-api`.

## Invariants

- Events fire from control-api (not the supervisor), synchronously at state-transition time. A slow subscriber holds up the operator-facing response.
- Idempotency at the rimsky side: each `(service, event)` pair fires exactly once.
- Peers referenced by a template but not subscribed silently skip fan-out (non-subscription is the default).
- The template-registered callback carries the template's canonical JCS spec bytes (deterministically re-hashable).

## Aliases and historical names

The protocol was extracted from the claim-producer protocol under the layer-crystallization plan, Phase 4.

## Open within this concept

(no specific live tensions)

## Notes

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

