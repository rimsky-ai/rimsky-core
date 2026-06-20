---
topic: observability-optional-protocols
kind: boundary
---

# Observability is a separate optional gRPC service per peer; not part of dispatch protocol

## Description

A peer service (executor or producer) can have rich per-dispatch trace information useful to operator dashboards. Bundling that into the dispatch RPC would force every peer to either implement trace storage or silently degrade, making simple peers harder. Rimsky's choice: a separate optional protocol per peer kind.

`ExecutorObservability` (`protocols/proto/v1/executor_observability.proto:17-21`) offers three methods:

- `GetCapabilities(GetCapabilitiesRequest) → ObservabilityCapabilities` — startup handshake.
- `GetTrace(GetTraceRequest) → Trace` — pull a previously-streamed trace by `dispatch_id`.
- `StreamTrace(StreamTraceRequest) → stream<TraceEvent>` — stream live events for an in-flight or recent dispatch.

A separate `StoreObservability` (`protocols/proto/v1/store_observability.proto`) mirrors the pattern for claim-producers, with `SupportsClaimGet`, `SupportsClaimStream`, `SupportsListClaims`, and an `admin_views` block listing operator-actionable views the producer exposes.

The Capabilities response carries:

- Booleans: `supports_trace_get`, `supports_trace_stream`.
- `retention_after_terminal_seconds` — how long the executor keeps per-dispatch trace.
- `CustomUI` — optional URL + embed mode for browser dashboards.
- `http_bridge_url` — optional HTTP+JSON proxy for browsers.
- **`declared_events`** (line 46-54) — source of truth for the substitution-side `nodes.<emitter>.event.<name>.<path>` resolver. Templates referring to an undeclared event are rejected at registration when the executor is reachable.
- **`userdata_schema`** (line 36-44) — executor's optional JSON Schema for userdata bytes. Rimsky validates at template registration AND at dispatch post-merge/post-substitution. Validation failure routes via `error_class: "userdata_validation_failed"`.

The `userdata_schema` placement is subtle. It's **not** in the dispatch protocol — it's reported by the observability protocol — but rimsky reads it. The implicit reasoning (consistent with the userdata-opacity invariant): the schema is metadata about what the executor accepts; schema validation is a structural check (does the JSON match the schema's keyword constraints) and does not "inspect" the bytes the way logging or substitution would. CLAUDE.md "Non-obvious gotchas" doesn't surface this; the observability-as-place-for-userdata-schema decision is internal.

The handshake itself is at startup. `modeling/observability/handshake.go::RunHandshake` (lines 174-196) dials each declared executor and producer's observability endpoint with a 30-second per-peer timeout (`handshakeTimeout` constant, line 22). Per the spec §4 the handshake is best-effort: unreachable peers or absent endpoints are recorded as `Unreachable` and never abort startup. A `RefreshLoop` (line 259-276) re-probes periodically (default 60s) to heal transient unreachability.

`Discovery` (`modeling/observability/discovery.go`) is the in-memory cache; the cache is the only consumer of `ObservabilityCapabilities` apart from the per-request handlers.

`docs/concepts/operational-health.md` "Surfaces" describes the operator's view of observability: dashboards mount on `/observability/*`; the `http_bridge_url` lets browser dashboards talk directly to peers without proxying through rimsky.

## Code surface

- `protocols/proto/v1/executor_observability.proto` — entire file (~120 lines).
- `protocols/proto/v1/store_observability.proto` — entire file.
- `modeling/observability/handshake.go` — startup probe + refresh loop.
- `modeling/observability/discovery.go` — in-memory cache.
- `modeling/observability/handler.go` — HTTP handlers (mounted by control-api).
- `modeling/observability/userdata_validator.go` — userdata schema validation.
- `executors/http-node/observability.go` — reference impl.
- `executors/claude-agent/src/observability.ts` — TS reference impl.

## Prose surface

- `docs/concepts/operational-health.md` — operator-facing observability.
- `docs/concepts/executor.md` — `ExecutorObservability` is the optional second protocol.
- `CLAUDE.md` "What this repo is" — observability mentioned in passing.

## Adjacent topics

- `2026-05-10-opacity-of-userdata-claim-blob` — `userdata_schema` is the one place rimsky's userdata-touch is schema-only, not content.
- `2026-05-10-attribute-substitution-grammar` — `declared_events` cross-checked at template registration.
- `2026-05-10-lifecycle-subscriber-opt-in` — sibling opt-in protocol.
- `named-events-and-on-event-handlers` — `declared_events` is the registry side.

## Observations

- The handshake skips silently for peers with no `observability_endpoint` configured; CLAUDE.md notes this as a deliberate "non-subscription is the default" stance, but a casual operator might miss the "no observability" status on a peer they thought was instrumented.
- `userdata_schema` validation is the only place rimsky reads userdata-adjacent metadata. The schema bytes themselves are kept in the `PeerEntry.Capabilities.UserdataSchema` cache field; rimsky never validates the schema's *content* (the JSON Schema text), only uses it as the validator input.
- `declared_events` is consumed by template-registration cross-check ("does the template reference any event names this executor doesn't declare?"); the check is skipped silently when the executor is unreachable. CLAUDE.md "Non-obvious gotchas" calls this out: unknown event names are treated as no-ops at runtime.
- The HTTP bridge URL (`http_bridge_url` in Capabilities) lets a browser dashboard call into the peer without crossing rimsky. This is the per-peer escape hatch from the otherwise-strict rimsky-as-mediator pattern.
- `StoreObservability.admin_views` is a producer-side affordance: a postgres store can expose "items pending in queue" as an admin view; the dashboard renders it dynamically. The schema for this is in `store_observability.proto` and is a project-agnostic surface for producer-specific views.
