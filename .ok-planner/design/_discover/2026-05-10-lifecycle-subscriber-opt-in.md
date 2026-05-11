---
topic: lifecycle-subscriber-opt-in
kind: boundary
---

# `LifecycleSubscriber` is a separate opt-in gRPC service; peers list `protocols: [...]` to subscribe

## Description

Peer services sometimes need to react to control-plane state transitions. Six events matter: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. These could be bundled onto `ClaimProducer`, encoded as special claim verbs, sent over a control-api websocket, or extracted into a separate gRPC service.

Rimsky chose extraction. `LifecycleSubscriber` is a standalone gRPC service (`protocols/proto/v1/lifecycle.proto:24-31`) defined alongside the claim-producer service but separately. A peer binary that wants to react implements both `ClaimProducer` and `LifecycleSubscriber`; a peer that doesn't subscribe simply doesn't implement the second service.

Per-peer subscription is declared in operator config: `protocols: [claim_producer, lifecycle_subscriber]` in `rimsky.yml`. Idempotency is tracked rimsky-side in `rimsky_lifecycle_idempotency` (`lifecycle.proto:12-16`); subscribers can assume each `(peer, event)` pair fires exactly once from rimsky's side (the table records `producer`, `scope_kind`, `scope_id`).

The proto comment (lines 4-10) gives the rationale: "extracted from claim_producer.proto under the layer-crystallization plan, Phase 4. Implementer pattern: return success from any method the binary doesn't react to. Binaries that don't react to any event simply don't implement the service." The explicit extraction (rather than bundling) lets a producer-only binary be a single-protocol implementation.

CLAUDE.md "Non-obvious gotchas" notes two important properties:

- **Events fire from control-api**, not the supervisor. This is because state transitions happen there: `POST /templates/.../deploy`, `POST /instances`, `DELETE /instances/{id}` are control-api routes; the supervisor doesn't see them.
- **Synchronous fan-out at state-transition time.** A slow subscriber holds up the control-api response. Subscribers should be fast; push slow work to an internal queue.
- **Bundled producer binaries can ship a no-op LifecycleSubscriber** via `enable_lifecycle: true` config without forking the binary — useful for the postgres reference store wanting per-template DDL.

The six events carry these payloads:

- `OnTemplateRegistered.spec` carries the canonical bytes (the same bytes rimsky hashed).
- `OnTemplateDeployed.tags` carries the movable aliases at deploy time.
- `OnTemplateUndeployed.template_hash`.
- `OnTemplateDeregistered.template_hash`.
- `OnInstanceCreated.template_hash` + `instance_id` + `params`.
- `OnInstanceTerminated.template_hash` + `instance_id` + `reason`.

`docs/concepts/lifecycle-subscriber.md` is explicit: "Peers referenced by a template but not subscribed silently skip fan-out. There's no error — non-subscription is the default." This is useful for stub producers in conformance: a third-party producer can ignore lifecycle entirely if it doesn't care.

## Code surface

- `protocols/proto/v1/lifecycle.proto` — entire file (~50 lines).
- `protocols/lifecycle/lifecycle.go` — Go interface.
- `modeling/controlapi/templates.go`, `modeling/controlapi/instances.go` — event-firing sites.
- `foundation/persistence/lifecycle_idempotency.go` — idempotency CRUD.
- `foundation/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql` — `rimsky_lifecycle_idempotency` schema.
- `stores/postgres/` — reference store that subscribes (with `enable_lifecycle: true`).

## Prose surface

- `docs/concepts/lifecycle-subscriber.md` — concept-doc treatment.
- `docs/protocols/lifecycle-subscriber.md` — implementer's guide.
- `CLAUDE.md` "What this repo is" — LifecycleSubscribers section.
- `CLAUDE.md` "Non-obvious gotchas" — synchronous fan-out, enable_lifecycle config.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` §3 — the three protocols.

## Adjacent topics

- `2026-05-10-out-of-process-claim-producers` — same out-of-process gRPC model.
- `2026-05-10-content-addressed-templates` — `template_hash` is carried in all template events.
- `2026-05-10-observability-optional-protocols` — sibling opt-in protocol on the same binary.
- `2026-05-10-unified-rimsky-yml-config` — `protocols: [...]` lists subscription per peer.

## Observations

- "Synchronous fan-out" creates a subtle SLA: a slow subscriber slows down operator-facing operations. Operators who don't realize a producer is also a lifecycle subscriber may attribute slow `POST /instances` to the wrong cause. CLAUDE.md flags this but the control-api response latency isn't surfaced as a metric per-subscriber.
- The proto comment (`lifecycle.proto:12-16`) describes idempotency as "each (peer, event) pair fires exactly once on the rimsky side" — but `docs/concepts/lifecycle-subscriber.md` adds "Subscribers should still write idempotent handlers because their own internal effects may not be idempotent by default." So idempotency is two-sided: rimsky guarantees no duplicate fan-out; subscribers must handle their own internal effect idempotency.
- Bundled producers can ship LifecycleSubscriber as a no-op via `enable_lifecycle: true` (CLAUDE.md), but the proto comment doesn't mention this config field — it's an operator-config affordance, not a protocol property.
- `OnTemplateRegistered.spec` carries the canonical JCS bytes (`2026-05-10-content-addressed-templates`). A subscriber that wants to re-hash the spec for cross-checking against `template_hash` can do so deterministically.
