---
concept: lifecycle-subscriber
status: as-is
aliases: []
---

# Lifecycle subscriber

## What it is

A service that implements the gRPC lifecycle-subscriber protocol — seven event callbacks: template registered, deployed, undeployed, and deregistered, plus instance created and terminated, plus run-scope terminal (carrying the run-scope id and a terminal reason). Opt-in per service by declaring the lifecycle-subscriber protocol (alongside claim-producer) in the service's protocol list. Idempotency is tracked in a persisted per-(service, event) ledger.

## Purpose

Some peers need to react to control-plane state transitions — e.g. the bundled postgres store wants to apply per-template DDL on the template-deployed callback. A separate optional protocol on the same service binary keeps producer-only impls simple and lets reactive impls subscribe explicitly.

## Boundaries

Owns: the seven event types, the synchronous fan-out timing, the opt-in subscription mechanism, the idempotency table. Does NOT own: the underlying state transitions (those happen in `concept:control-api` for template/instance events and in the `concept:supervisor` for run-scope-terminal events), the producer-side reaction (lives in the subscriber). The supervisor process is now also a lifecycle-event firer (in addition to control-api): it maintains its own subscriber registry and fires the run-scope-terminal event synchronously when it closes a run scope. Adjacent: `claim-producer`, `template`, `instance`, `control-api`, `supervisor`, `host-agent-proxy`.

## Invariants

- Lifecycle-subscriber events fire synchronously from the rimsky-side process that owns the state transition: template / instance events from `concept:control-api` as today; run-scope-terminal events from the `concept:supervisor` that closes the scope. A slow subscriber holds up the firing process's path. (This relaxes the earlier "events fire from control-api, never the supervisor" invariant.)
- Idempotency at the rimsky side: each `(service, event)` pair fires exactly once. The DB-tracked idempotency ledger is preserved across both firing sites.
- Peers referenced by a template but not subscribed silently skip fan-out (non-subscription is the default).
- The template-registered callback carries the template's canonical JCS spec bytes (deterministically re-hashable).

## Aliases and historical names

The protocol was extracted from the claim-producer protocol under the layer-crystallization plan, Phase 4.

## Open within this concept

(no specific live tensions)

## Notes

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] Extends the method count from six to seven (adds the run-scope-terminal event) and relaxes the firing-site invariant so the `concept:supervisor` fires the run-scope-terminal event directly. Rationale: run-scope close is a runtime concern, not a control-plane concern; routing it through control-api would require new internal-service plumbing for no semantic gain. The supervisor dials its own subscriber set via the same protocol-membership walk control-api uses (no new top-level config block). The peer filter is extended so that when a template declares `late_bind_services`, the late-bind proxy joins the instance- and run-scope-keyed fan-out only (not template-event fan-out — the proxy doesn't consume template events). Per spec 2026-05-24-host-agent-and-proxy-design.

