# Dashboard & Observability v1 — Design

## Status

- Spec, 2026-05-02.
- Outcome of the 2026-05-02 brainstorm covering observability surfaces and a reference dashboard implementation.
- Foundational dependencies:
  - `docs/history/2026-04-27-stores-redesign-v3-design.md` — store contract and blessed invariants this spec must respect (especially invariant 20: claim content inert in Rimsky core).
  - `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md` — control-plane v1 and the existing `rimsky.yml` handshake this spec extends.
  - `docs/history/2026-05-01-auth-and-multitenancy.md` — auth-blind v1 stance the dashboard inherits.
- Pre-v1; per `.claude/rules/rules.md`, no backwards-compat constraints on protocols, schema, or config shape.
- Companion follow-on: when "command-center" capabilities (auth, write-action UX, server-side preferences) land, they will be specified in a separate doc. v1 is read-only.

## Context

Rimsky in v1 ships orchestrator + stores + executors as three independently deployable collections, communicating via Postgres and well-defined wire protocols. There is no first-class story for **looking at what's happening**: operators read Postgres directly, users debugging a workflow have no visibility into executor-internal execution, and there is no Docker-Desktop-style at-a-glance UI for the deployment.

This spec adds:

1. **Three public observability protocols** — one per existing collection (orchestrator, executors, stores).
2. **A reference dashboard implementation** that composes them as a fourth collection (`dashboards/`).

The work is positioned so that other dashboards can be built against the same protocols. The official dashboard is a credible reference implementation, not a privileged consumer.

## Goals

- Single coherent UI where any audience (operators, template authors, debuggers, end-users-of-products-built-on-Rimsky) can answer "what is the system doing right now" and "why did this run fail" without leaving the tool.
- Three documented public protocols. Other dashboards can be built against them; alternative store/executor implementations can opt into observability without bespoke integration.
- Dashboard is officially maintained but architecturally separate from Rimsky core. No dashboard-specific shortcuts in core; no core-internal channels available to the dashboard. Same isolation as `executors/` and `stores/`.
- Bundled with the dev `docker-compose` stack out of the box; opt-in profile for production.
- Forward-compatible with future "command-center" capabilities (auth, write-action UX, server-side preferences, derived dashboards, alerting).

## Non-goals

- Auth on the observability API or the dashboard in v1. Inherits the per-project deployment / network-perimeter model from `docs/history/2026-05-01-auth-and-multitenancy.md`.
- Multi-tenancy in the dashboard. Per-project deployment per tenant.
- Replacing operational tooling (logs/metrics/traces backends like Grafana, Datadog). The dashboard composes the three Rimsky-collection observability surfaces; OTel/log forwarding stays the operator's choice and is independent of this spec.
- Server-side persistence in the dashboard (server-side preferences, saved views, custom dashboards) in v1. localStorage only.
- Write actions in the dashboard in v1 (force-fire, invalidate, register, deploy). Those endpoints already exist on control-api; wiring them into a dashboard UI is post-v1 "command-center" scope.

## Architectural backbone

- **Three protocols, three implementations, three documentation surfaces.** Rimsky observability lives on `rimsky-control-api`; executor observability is implemented per-executor; store observability is implemented per-store. Each is a distinct public contract.
- **Standard envelope, standard vocabulary, free-form fallback.** Both per-peer protocols (executor and store) share the same `Event` envelope and a small standard category vocabulary that the dashboard renders with bespoke widgets. Free-form events are first-class and render as log lines.
- **Inert-claim and inert-userdata invariants are not weakened.** Claim content (`payload`/`address`/`region`) and `userdata` remain inert in Rimsky core. The store/executor decides what to expose via *its own* observability surface; Rimsky never reads, logs, or forwards that content from a core code path.
- **The dashboard is just an HTTP client.** No DB credentials, no internal "trust me" header, no `core/` imports. If something feels easier with a private channel, it is a contract gap to fix on the public surface, not a shortcut to take.
- **Two distinct API surfaces — Rimsky observability vs. dashboard's own API — are not the same thing.** The Rimsky observability API is a public contract any dashboard targets. The dashboard's own Node-server HTTP surface is private to this dashboard. Future "command-center" features live on the latter without bloating the former. A third-party dashboard never touches the dashboard's own API.

What this spec does not cover (see §11 for the explicit list):

- CLI surface, write-action UX, dashboard-side auth, alerting, server-side preferences.
- Tenant scoping in the dashboard (depends on `docs/history/2026-05-01-auth-and-multitenancy.md` §3.2).
- OTel / metrics / log forwarding (operator concern, orthogonal to this spec).

---

## 1. Rimsky observability API

Lives on `rimsky-control-api`. Versioned namespace `/v1/observability/*`. Read-only by definition; write actions stay on the existing operator/admin endpoints. Wire format is HTTP/JSON, matching the rest of the control-api.

### 1.1 Versioning

The observability API is versioned at `/v1/observability/*` from day one. This is a deliberate departure from the bare-path control-api admin endpoints (per `docs/history/2026-05-02-rimsky-cli-and-compose-design.md` §6.2): the audience is third-party dashboards rather than operator tooling, and the stability promise is stronger. Breaking changes ship under a new version namespace.

### 1.2 Endpoint surface

Resource-oriented, comprehensive. Every state-bearing entity gets a list endpoint and a detail endpoint.

#### 1.2.1 Topology / declared peers

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/stores` | List of declared stores from `rimsky.yml`. Each entry: `name`, `endpoint`, `declared_capabilities`, `observability_capabilities` (from observability handshake; see §4), `reachability_status` (`reachable` / `unreachable` / `degraded`). |
| `GET /v1/observability/stores/{name}` | Per-store detail. |
| `GET /v1/observability/executors` | Same shape for executors. |
| `GET /v1/observability/executors/{name}` | Per-executor detail. |

#### 1.2.2 Templates / instances / schedules

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/templates` | List. Filters: `tag`, `state` (`registered`/`deployed`/`undeployed`). Each entry: `hash`, list of tags, deployment state, deployed-instance count. |
| `GET /v1/observability/templates/{hash}` | Detail; includes deployed-instance summary. |
| `GET /v1/observability/instances` | List. Filters: `template_hash`, `active` (boolean; derived as `terminated_at IS NULL`). Each entry: `id`, `instance_key`, `template_hash`, `created_at`, `terminated_at` (nullable). The "state" of an instance is derived from `terminated_at`; there is no dedicated `state` column on `rimsky_instances`. |
| `GET /v1/observability/instances/{id}` | Detail; includes node graph (computed server-side from template `graph` + current node states; see §1.4), frame summary, recent activity. |
| `GET /v1/observability/schedules` | List. Filter: `node_id` (optional). Each entry: `node_id`, `cron_expr`, `next_fire_at`, `last_fired_at`. (Schedule fire history — `schedule_fired` / `schedule_dispatch_failed` events — comes from `GET /v1/observability/events` filtered by kind, not from this endpoint.) |

#### 1.2.3 Runtime state

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/frames` | List. Filters: `instance_id`, `state` (`queued`/`running`/`completed`/`failed` — matches `rimsky_frames.state`). Cursor-paginated. |
| `GET /v1/observability/frames/{id}` | Detail; constituent dispatches with their states. |
| `GET /v1/observability/nodes/{instance_id}/{node_type}` | Node detail; state history, recent dispatches, current claim holdings (claim_ids only — see §1.3). Path uses `node_type` (the template-declared identifier from `rimsky_nodes.node_type`); the `(instance_id, node_type)` pair is unique per `rimsky_nodes_instance_id_node_type_idx`. |
| `GET /v1/observability/dispatches` | List of **currently-live** dispatches (the table holds only rows with no terminal yet — terminals delete the row). Filters: `state` (`pending` (`claimed_by IS NULL`) / `claimed` (`claimed_by IS NOT NULL`)), `executor_name`, `instance_id` (joined via `node_id` → `rimsky_nodes`). Cursor-paginated by `enqueued_at DESC`, tiebreaker `id DESC`. Each entry: `id`, `node_id`, `executor_name`, `state` (derived), `claimed_by`, `enqueued_at`, `claimed_at`, `last_heartbeat_at`. |
| `GET /v1/observability/dispatches/{id}` | Detail for a **currently-live** dispatch (rows are deleted on terminal). Includes `claim_id` (if any), `executor_name`, `claimed_by` (supervisor id; null if pending), `claimed_at`, `last_heartbeat_at`. The dispatch "state" is `pending` (`claimed_by IS NULL`) or `claimed` (`claimed_by IS NOT NULL`) — derived, not a column. **Terminal-outcome history** for a node lives in `rimsky_events` (kinds `work_started`, `work_completed`, `error`, `lock_acquired`, `lock_released`, etc.); the dashboard fetches it via `GET /v1/observability/events?node_id=...`. |

#### 1.2.4 Locks / claims (Rimsky-side view)

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/lock-holders` | List. Filters: `store_name`, `region` (byte-equal match against canonical region bytes), `instance_id` (joined via `holder_node_id` → `rimsky_nodes`), `node_type` (same join). Each entry: `claim_id` (the `rimsky_lock_holders.id` row UUID, exposed under the `claim_id` alias per CLAUDE.md vocabulary), `lock_kind`, `lock_name` (for named locks), `store_name`, `region_data` (base64), `intent`, `holder_supervisor_id`, `holder_node_id`, `claimed_at`, `last_heartbeat_at`, `expires_at`. **`address` is excluded** — see §1.3. |
| `GET /v1/observability/lock-holders/{id}` | Detail; includes held subgraph (`rimsky_claim_holders` rows: which nodes hold this claim and their current states). |

#### 1.2.5 Events

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/events` | Paginated event log. Backed by `rimsky_events` (the existing application event log: `id`, `instance_id`, `node_id`, `kind`, `payload`, `occurred_at`). Filters: `instance_id`, `node_id`, `kind`, `kind_in` (comma-separated list for multi-kind queries), `since` (timestamp). Cursor-paginated by descending `occurred_at`. |

The `kind` filter is open-ended — the dashboard treats the kind enum as **opaque and extensible**. v1 dashboard rendering knows about the kinds Rimsky currently emits (`work_started`, `work_completed`, `error`, `lock_acquired`, `lock_released`, `schedule_fired`, `schedule_dispatch_failed`, `heartbeat_lost`, `orphaned_claim_released`, `pure_cascade_commit`, `message_emitted`, `message_received`, `attributes_substituted`, `attributes_schema_failed`, `quality_rule_failed`, `unresolved_executor`, `unresolved_invalidate_target`, `template_resolution_failed`); unknown kinds render as generic event rows with `kind`/`payload` displayed verbatim.

**Lifecycle events** (`template_registered`, `template_deployed`, etc.) are **not in `rimsky_events` today** — they fire RPC-style and are tracked for delivery in `rimsky_store_lifecycle` (which carries last-acked state per (store, scope), not a timeline). A queryable lifecycle-event timeline is **out of v1** (§11) and would require control-api to begin writing a `rimsky_events` row alongside firing each lifecycle event RPC. The dashboard surfaces the per-store lifecycle delivery state via `GET /v1/observability/stores/{name}` instead, which can read `rimsky_store_lifecycle` directly.

#### 1.2.6 Summary

| Endpoint | Purpose |
|---|---|
| `GET /v1/observability/system/health` | Deployment-wide health: control-api liveness, scheduler/supervisor heartbeat status, store/executor reachability, postgres connectivity. |
| `GET /v1/observability/system/summary` | At-a-glance counts: instances active vs. terminated (derived from `terminated_at`), frames by state (`queued`/`running`/`completed`/`failed`), dispatches currently claimed, terminal failures in the last hour (counted from `rimsky_events` where `kind = 'error'`), active lock-holders, etc. |

### 1.3 Inert-claim invariant in observability responses

Lock-holder detail responses **MUST NOT** include claim payload or address bytes. They include `claim_id` and `region_data` (the latter is already in `rimsky_lock_holders` and is part of Rimsky's conflict-detection surface, not opaque claim content).

To view claim payload/address, the dashboard follows `claim_id` to the store's observability protocol (§3), which returns whatever the store has chosen to expose. This preserves blessed invariant 20 in Rimsky-side code paths: payload/address bytes are never read or logged by Rimsky-core endpoints.

### 1.4 Cascade graph

`GET /v1/observability/instances/{id}` returns the node graph computed server-side from the template's `graph` field plus current node states. Each node is rendered as:

```json
{
  "node_type": "ingest",
  "node_id": "11111111-2222-3333-4444-555555555555",
  "state": "running",
  "current_error_class": null,
  "retry_counter": 0,
  "active_dispatch_id": "d-abc123",
  "last_terminal_event": {
    "kind": "work_completed",
    "occurred_at": "2026-05-02T10:14:32Z"
  },
  "edges_in": ["raw_pull"],
  "edges_out": ["transform", "qa_check"]
}
```

Field derivations:

- `node_type`, `state`, `current_error_class`, `retry_counter`, `node_id` come straight from `rimsky_nodes` (template-declared identifier; the four-state machine `fresh`/`stale`/`running`/`failed`; the existing error-class column; retry counter; row UUID).
- `active_dispatch_id` is the `rimsky_dispatch.id` for this `node_id` if a row exists in `rimsky_dispatch` (the table holds only live dispatches; rows are deleted on terminal). At most one per node by the `UNIQUE (node_id)` constraint.
- `last_terminal_event` is the most recent `rimsky_events` row for this `node_id` whose `kind` is in the dispatch-terminal set (`work_completed`, `error`, plus future additions). Null when the node has never dispatched. The dashboard uses this for "last completed at" / "last failed at" badges.
- `edges_in` / `edges_out` come from the template spec's `graph` declaration, projected by `node_type`.

The dashboard renders the DAG; layout logic is the dashboard's concern, not the API's. This avoids forcing every dashboard to re-implement template-graph extraction.

### 1.5 Conventions

- **Pagination**: cursor-based (`?cursor=...&limit=...`). Default `limit` 50, max 500. Cursor is opaque to the client.
- **Filtering**: simple query-param matches per endpoint. No DSL. The spec enumerates allowed filters per resource (above).
- **Errors**: standard JSON error body `{ "error": { "code": "<machine-readable>", "message": "<human>", "details": {...} } }`. HTTP status follows REST convention (404 missing, 400 bad query, 500 server).
- **No write endpoints** in `/v1/observability/*`. Write actions stay on existing operator/admin paths (`/admin/scheduled-nodes/{id}/force-fire`, etc.). The dashboard composes both surfaces; observability is read-only by definition.
- **Live updates**: `system/*` and resource list/detail endpoints are polled by the dashboard on a configurable interval (default 5s; per-page overrides exist in dashboard config). Live trace and live claim views use the per-peer streaming endpoints (§2.5, §3.5), not Rimsky observability streams. This keeps the Rimsky observability API request/response only — no SSE on this surface in v1.

---

## 2. Executor observability protocol

New proto service in `proto/v1/executor_observability.proto`. Lives alongside `node_executor.proto`; implementations may co-host the listener with their dispatch service or split it. The dispatch protocol is unchanged.

### 2.1 Service surface

```proto
service ExecutorObservability {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (ObservabilityCapabilities);
  rpc GetTrace(GetTraceRequest) returns (Trace);                  // snapshot
  rpc StreamTrace(StreamTraceRequest) returns (stream TraceEvent); // append-only stream
}
```

HTTP+JSON bridge (consistent with the existing executor protocol pattern):

| HTTP | gRPC |
|---|---|
| `GET /observability/v1/capabilities` | `GetCapabilities` |
| `GET /observability/v1/trace/{dispatch_id}` | `GetTrace` |
| `GET /observability/v1/trace/{dispatch_id}/stream` (SSE) | `StreamTrace` |

SSE encoding: each event is one `data:` line containing JSON-serialized `TraceEvent`. SSE keepalive comments per convention.

### 2.2 ObservabilityCapabilities

```proto
message ObservabilityCapabilities {
  bool supports_trace_get = 1;       // GetTrace returns meaningful data
  bool supports_trace_stream = 2;    // StreamTrace returns meaningful data
  uint64 retention_after_terminal_seconds = 3;  // 0 = no retention guarantee
  CustomUI custom_ui = 4;            // optional; nil/zero = no custom UI
  string http_bridge_url = 5;        // optional; absolute base URL where the peer serves the HTTP+JSON observability bridge (e.g. "http://http-node:9092"). When empty, the peer exposes only the gRPC surface; the dashboard cannot proxy to it from the browser.
}

message CustomUI {
  string ui_url = 1;                 // base URL of the UI; opaque to Rimsky/dashboard
  EmbedMode embed_mode = 2;          // LINK | IFRAME | BOTH
  string dispatch_url_template = 3;  // optional; e.g. "/trace/{dispatch_id}"
}

enum EmbedMode {
  EMBED_MODE_UNSPECIFIED = 0;
  LINK = 1;
  IFRAME = 2;
  BOTH = 3;
}
```

`ui_url` is opaque from Rimsky and the dashboard's perspective. It can point to:

- The executor's own embedded UI (same process, same image)
- A sidecar service the operator runs alongside the executor
- An entirely external service (a Grafana board, a Datadog dashboard, an in-house tool)
- Anywhere else that knows how to render the URL-template-substituted path

Topology of who serves the custom UI is the operator's call; the protocol just defines the URL contract.

`dispatch_url_template` substitution markers are a fixed enumeration. For executors: `{dispatch_id}`, `{instance_id}`, `{node_type}`. For stores: `{claim_id}`, `{store_name}` (declared in §3.5). Spec enumerates allowed markers per template type. This substitution syntax is **explicitly distinct** from Rimsky's `{{...}}` attribute substitution (blessed invariant 11) — different token shape, different semantics, no overlap.

### 2.3 TraceEvent shape

Generic envelope plus standard vocabulary plus free-form fallback.

```proto
message TraceEvent {
  string event_id = 1;               // unique within trace
  string parent_event_id = 2;        // optional; for tree structure
  google.protobuf.Timestamp timestamp = 3;
  Severity severity = 4;             // DEBUG | INFO | WARN | ERROR
  string category = 5;               // standard vocab token OR free-form
  string message = 6;
  google.protobuf.Struct attributes = 7;  // arbitrary JSON
}

message Trace {
  string dispatch_id = 1;
  bool evicted = 2;                  // true if executor has dropped this dispatch's events
  bool complete = 3;                 // true if dispatch terminal-ed and no more events will arrive
  repeated TraceEvent events = 4;
}

enum Severity {
  SEVERITY_UNSPECIFIED = 0;
  DEBUG = 1;
  INFO = 2;
  WARN = 3;
  ERROR = 4;
}
```

### 2.4 Standard vocabulary (executor)

The dashboard renders standard categories with bespoke widgets. Standard categories are part of the public protocol, not part of the dashboard — a third-party dashboard would do the same against the same vocabulary.

| `category` | Required `attributes` keys | Dashboard renders as |
|---|---|---|
| `step_started` | `step_id` (string) | Tree node, expandable |
| `step_completed` | `step_id` | Tree node closes; success styling |
| `step_failed` | `step_id`, `error` (string) | Tree node closes; failure styling |
| `subcall_started` | `subcall_id`, `target` (string) | Nested-invocation block, distinct icon |
| `subcall_completed` | `subcall_id` | — |
| `tool_call` | `tool_name` (string), `arguments` (object), `result` (object), `duration_ms` (number) | Inspector panel — collapsible JSON for args + result |
| `log` | (free-form) | Log line |
| `error` | `error` (string), `stack` (string, optional) | Error block, distinct styling |
| `trace_complete` | — | Stream-close marker; not user-rendered |

Tree structure: `parent_event_id` is canonical when present. `step_*` and `subcall_*` events implicitly form a tree via `step_id` / `subcall_id` matching when `parent_event_id` is absent. Executors SHOULD emit `parent_event_id` for trees deeper than one level.

Free-form `category` strings are passed through; the dashboard renders them as plain log lines with `message` + `attributes`.

### 2.5 Streaming semantics

`StreamTrace`:

- **Initial response**: replay all retained events from the start of the trace (so a dashboard joining late still sees history).
- **Subsequent**: stream new events as they are produced.
- **Termination**: when the dispatch terminals, the executor sends a final `TraceEvent` with `category: "trace_complete"` and closes the connection. Clients use this to distinguish "no more events" from "connection dropped."
- **Disconnect**: clients may disconnect at any time. Reconnecting starts a fresh `StreamTrace` call with full replay; there is no resume cursor in v1.
- **Server-side timeout**: executors MAY close idle streams after a server-configured timeout (default 5 minutes of no events). Clients reconnect.

Executors that don't truly stream MAY implement `StreamTrace` as: send snapshot, hold connection until terminal or timeout, send `trace_complete`, close. Conformance accepts this implementation strategy.

### 2.6 Retention

`ObservabilityCapabilities.retention_after_terminal_seconds` declares how long the executor retains a completed dispatch's trace. After eviction:

- `GetTrace(evicted dispatch_id)` returns `Trace{ dispatch_id, evicted: true, complete: true, events: [] }`.
- `StreamTrace(evicted dispatch_id)` immediately closes with the same payload.

During the active window (the dispatch has not yet terminal-ed), retention is required: the executor MUST be able to serve the full event stream. Eviction applies only to terminal-ed dispatches.

### 2.7 Inert-userdata invariant

The executor's trace is not constrained by blessed invariant 11. The executor knows what its own `userdata` means; it MAY include parsed `userdata`-derived information in trace event attributes if it wants to. Rimsky never sees this trace data — it is fetched by the dashboard from the executor directly, never proxied through Rimsky core.

---

## 3. Store observability protocol

Same envelope shape as §2; store-specific vocabulary; adds optional store-specific admin views.

### 3.1 Service surface

```proto
service StoreObservability {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (StoreObservabilityCapabilities);
  rpc GetClaim(GetClaimRequest) returns (ClaimDetail);
  rpc StreamClaim(StreamClaimRequest) returns (stream ClaimEvent);
  rpc ListClaims(ListClaimsRequest) returns (ClaimList);     // optional
  rpc GetAdminView(GetAdminViewRequest) returns (AdminView); // optional
}
```

HTTP+JSON bridge:

| HTTP | gRPC |
|---|---|
| `GET /observability/v1/capabilities` | `GetCapabilities` |
| `GET /observability/v1/claims/{claim_id}` | `GetClaim` |
| `GET /observability/v1/claims/{claim_id}/stream` (SSE) | `StreamClaim` |
| `GET /observability/v1/claims?...&cursor=&limit=` | `ListClaims` |
| `GET /observability/v1/admin/{view_name}?...` | `GetAdminView` |

### 3.2 ClaimDetail

```proto
message ClaimDetail {
  string claim_id = 1;
  ClaimState state = 2;              // OPEN | COMMITTED | ABANDONED | RELEASED | UNKNOWN
  google.protobuf.Struct address = 3;   // store's view; may be null
  google.protobuf.Struct payload = 4;   // store's view; may be null
  google.protobuf.Struct region = 5;    // store's view; may be null
  google.protobuf.Timestamp opened_at = 6;
  google.protobuf.Timestamp closed_at = 7;  // optional
  repeated ClaimEvent history = 8;   // append-only
}

enum ClaimState {
  CLAIM_STATE_UNSPECIFIED = 0;
  OPEN = 1;
  COMMITTED = 2;
  ABANDONED = 3;
  RELEASED = 4;
  UNKNOWN = 5;                       // store does not have / has evicted this claim
}
```

The store decides what to expose in `address` / `payload` / `region`. They MAY be null (store opted out), redacted, partial, or fully rendered — the store's call. This is **not** Rimsky reading claim content; it is the store's own observability surface, distinct from the inert-claim invariant in Rimsky core (which still holds — Rimsky never asks the store for these fields).

### 3.3 Standard vocabulary (store)

| `category` | Required `attributes` keys | Dashboard renders as |
|---|---|---|
| `claim_opened` | — | Timeline anchor |
| `claim_committed` | — | Terminal success |
| `claim_abandoned` | `reason` (string, optional) | Terminal failure |
| `claim_released` | — | Terminal release |
| `conflict_detected` | `conflicting_claim_id` (string) | Highlighted warning |
| `log` | (free-form) | Log line |
| (free-form) | (free-form) | Log line |

`ClaimEvent` envelope is identical to `TraceEvent` (§2.3): `event_id`, optional `parent_event_id`, `timestamp`, `severity`, `category`, `message`, `attributes`. Reusing the envelope keeps SDKs and dashboard rendering uniform.

### 3.4 Admin views

Optional. Stores that want to expose store-internal admin surfaces (postgres pick-policy queue depth, items table contents; filesystem mount roots; etc.) declare them in `StoreObservabilityCapabilities.admin_views` and serve them via `GetAdminView`. The dashboard renders each declared view as a named tab on the store's detail page.

```proto
message AdminViewDecl {
  string name = 1;            // e.g. "items_queue"
  string title = 2;           // human-readable
  string description = 3;
  repeated AdminViewParam params = 4;
}

message AdminViewParam {
  string name = 1;
  string type = 2;            // "string" | "int" | "bool"
  string description = 3;
  bool required = 4;
}

message AdminView {
  AdminViewSchema schema = 1;       // column shape for table-style; arbitrary structure for richer views
  google.protobuf.Struct data = 2;  // the view payload
  string render_hint = 3;           // "table" | "raw_json" (v1); "chart" | "tree" (future)
}

message AdminViewSchema {
  repeated AdminViewColumn columns = 1;
}

message AdminViewColumn {
  string name = 1;
  string type = 2;            // "string" | "int" | "bool" | "json" | "timestamp"
}
```

v1 dashboard renders `table` and `raw_json` hints. Richer hints (`chart`, `tree`) can be added without breaking the contract — older dashboards fall back to `raw_json` for unknown hints.

### 3.5 StoreObservabilityCapabilities

```proto
message StoreObservabilityCapabilities {
  bool supports_claim_get = 1;
  bool supports_claim_stream = 2;
  bool supports_list_claims = 3;
  uint64 retention_after_terminal_seconds = 4;
  CustomUI custom_ui = 5;
  repeated AdminViewDecl admin_views = 6;
  string http_bridge_url = 7;        // optional; absolute base URL where the store serves the HTTP+JSON observability bridge (e.g. "http://store-postgres:9111"). When empty, the store exposes only the gRPC surface; the dashboard cannot proxy to it from the browser.
}
```

`CustomUI` is the same message as in §2.2. URL template markers for the store protocol: `{claim_id}`, `{store_name}`. Per §2.2, the marker enumeration is per-template-type and is part of the public spec.

### 3.6 Streaming + retention semantics

Mirror §2.5 + §2.6 exactly. `StreamClaim` replays history then streams new events; closes with a `claim_terminal` marker (`category: "claim_terminal"`) when the claim reaches a terminal state and no further events will arrive. Retention applies to terminal-ed claims; eviction returns `ClaimDetail{ state: UNKNOWN, ... }`.

### 3.7 ListClaims

Optional. Stores that support it expose paginated browsing of their claims independent of any specific Rimsky lock-holder context. Useful for debugging "what does this store currently have open?" without going through Rimsky.

```proto
message ListClaimsRequest {
  string state_filter = 1;      // optional; "OPEN" / "COMMITTED" / etc.
  string cursor = 2;
  uint32 limit = 3;             // default 50, max 500
}

message ClaimList {
  repeated ClaimSummary claims = 1;
  string next_cursor = 2;
}

message ClaimSummary {
  string claim_id = 1;
  ClaimState state = 2;
  google.protobuf.Timestamp opened_at = 3;
  google.protobuf.Timestamp closed_at = 4;
}
```

Stores that don't support it return `Unimplemented`; the dashboard hides the "browse claims" affordance for those stores.

---

## 4. Discovery & handshake

`rimsky-control-api` performs an **observability handshake** at startup, alongside the existing dispatch-protocol `Capabilities()` handshake from `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md` §3. The two handshakes have **different failure semantics** and must not be conflated:

- **Dispatch handshake (existing, fail-fast):** unreachable peers or capability mismatches abort control-api startup. Required for correctness — Rimsky cannot dispatch without the dispatch contract.
- **Observability handshake (new, best-effort):** unreachable peers or absent observability endpoints are recorded as `reachability_status: "unreachable"` and `observability_capabilities: null`. Control-api startup proceeds. Observability is optional; the dashboard degrades gracefully when peers don't expose it.

A control-api background task re-probes unreachable observability endpoints on a configurable interval (default 60s) so transient unreachability heals.

### 4.1 `rimsky.yml` schema additions

Each executor and store entry gains an optional `observability_endpoint:` field. When omitted, the dispatch endpoint is reused. When present, control-api dials the override for the observability handshake but continues to use the dispatch `endpoint` for dispatch.

```yaml
executors:
  - name: claude-agent
    transport: grpc
    endpoint: claude-agent:9090
    observability_endpoint: claude-agent:9091   # optional; defaults to endpoint
    tls: false

stores:
  - name: postgres-default
    endpoint: store-postgres:7070
    observability_endpoint: store-postgres:7071  # optional; defaults to endpoint
    capabilities:
      write_semantics: staged_async
```

Both fields are operator-managed; Rimsky validates that the format parses but does not introspect endpoint contents.

### 4.2 Capabilities exposure

Observability capabilities are exposed via `GET /v1/observability/executors/{name}` and `GET /v1/observability/stores/{name}`. The dashboard renders capabilities-driven UI affordances: trace pane visible only when `supports_trace_get` is true; claim browse tab visible only when `supports_list_claims` is true; custom-UI hook rendered only when `custom_ui` is non-null; admin-view tabs rendered per `admin_views` list; etc.

---

## 5. Reference dashboard implementation

### 5.1 Stack

- **SPA**: React 18+ / Vite / TypeScript.
- **Styling + components**: Tailwind + shadcn/ui (Radix primitives).
- **Data layer**: TanStack Query (React Query). SSE handled via `EventSource` with reconnection logic.
- **Server**: Hono on Node. (Express acceptable as a fallback; Hono picked for size and DX.)
- **Build**: `vite build` produces a static bundle; Hono serves the bundle and the proxy endpoints in a single Node process.

### 5.2 Repo location

`dashboards/rimsky-dashboard/` — parallel to `executors/`, `stores/`. Plural matches existing collection convention and leaves room for `dashboards/<other>/` if a use case ever emerges. The directory is its own subproject with its own `package.json`, `tsconfig.json`, `Dockerfile`, and tests.

The dashboard MUST NOT import from `core/`. Like `executors/claude-agent/`, it is a wire-protocol consumer only.

### 5.3 Server responsibilities (v1)

The Node server is small but deliberately positioned to grow. v1 responsibilities:

- Serve the SPA's static assets (vite build output) at `/`.
- Reverse-proxy `/api/control/*` → `${RIMSKY_CONTROL_API_URL}/v1/observability/*`.
- Reverse-proxy `/api/exec/{name}/*` → that executor's observability endpoint (looked up via control-api's `/v1/observability/executors/{name}` at startup; refreshed periodically and on cache miss).
- Reverse-proxy `/api/store/{name}/*` → that store's observability endpoint (same lookup pattern).
- Forward SSE streams transparently.
- Health endpoint at `/healthz` (returns 200 if the server is up and control-api is reachable).
- Configuration: read `RIMSKY_CONTROL_API_URL` (default `http://control-api:8080`, matching the existing compose service name) and `PORT` (default `8090`) from env. No other config in v1.

The proxy collapses CORS to a single origin (the dashboard's own); operators don't need to configure CORS on every executor/store.

### 5.4 Dashboard's own API (forward scope)

The Node server is the seam for future capabilities:

- Dashboard-side auth (front-end-authenticate users; translate to control-api calls which remain auth-blind in v1)
- Server-side preferences (saved filters, pinned views, dashboard layouts) backed by SQLite
- Derived/cached aggregations (top-N failing nodes by hour, etc.)
- Write-action UX wrapping control-api admin endpoints (force-fire, invalidate, deploy, register)
- Alerting / notification routing
- Custom user-defined dashboards

This API is **separate from the Rimsky observability API** (§1):

- Rimsky observability API = public contract; any dashboard targets it.
- Dashboard's own API = private to this dashboard; growing it is fine.

A third-party dashboard never touches the dashboard's own API. The split lets future "command-center" features land in the official dashboard without bloating the public observability contract.

### 5.5 v1 MVP feature scope

Each item exercises at least one protocol end-to-end:

- **System landing page** — uses `system/health` + `system/summary`; cards for declared executors/stores with reachability + observability badges.
- **Browse pages** — list views for templates, instances, frames, nodes, dispatches, lock-holders, schedules, and the unified `rimsky_events` timeline (filterable by kind/node/instance per §1.2.5). Cursor pagination; filter chips per page. Per-store lifecycle-delivery state is shown on the store detail page rather than as a top-level browse view, because v1 has no queryable lifecycle-event timeline (see §11).
- **Instance detail page** — cascade graph (rendered from `GET /v1/observability/instances/{id}`); node states color-coded by `state`; frame timeline.
- **Node detail page** — state history; recent dispatches with terminal outcomes; click-through to dispatch detail.
- **Dispatch detail page** — full record + active-trace pane (snapshot via `GetTrace`, live updates via `StreamTrace`); standard-vocab events render as step tree, tool-call inspector, structured error block; free-form events render as log lines.
- **Lock-holder detail page** — Rimsky's view (instance, node, region bytes, supervisor) + click-through to the store's per-claim view.
- **Store detail page** — declared capabilities, observability capabilities, recent claims (via `ListClaims` if supported), per-claim view (fetched directly from the store's observability protocol per §3 — state, history, and whatever payload/address/region the store has chosen to expose) + admin view tabs. Rimsky-side endpoints never surface payload/address (see §1.3).
- **Executor detail page** — declared capabilities, observability capabilities, recent dispatches (filtered by executor target), custom-UI hook badge.
- **Custom-UI rendering** — for executors/stores that declare a `CustomUI`: render iframe (mode `IFRAME` or `BOTH` with iframe preference), or link button (mode `LINK` or `BOTH` with link preference); always show "Open in new tab" alongside. See §5.6 for iframe sandboxing requirements.

Out of v1 (see §11): search, alerting, multi-deployment views, custom user-defined dashboards, server-side preferences, shared annotations, replay / time-travel, write-action UX.

### 5.6 Iframe security

Custom-UI iframes load arbitrary peer-controlled content. Rimsky's auth-blind perimeter assumption (§7) handles network access but does not address browser-level threats — a compromised executor or store could host XSS, clickjacking, or storage-exfiltration content. The dashboard MUST apply browser-level isolation:

- Every custom-UI iframe is rendered with `sandbox="allow-scripts allow-forms allow-same-origin"` minus `allow-same-origin` whenever the iframe origin differs from the dashboard's origin (the default for any externally-hosted UI). Same-origin is allowed only when the operator has deliberately co-hosted the custom UI on the dashboard's own origin.
- The dashboard sets a strict `Content-Security-Policy` on its own document forbidding inline script and locking `frame-src` to `*` (iframes are allowed but cannot escape the sandbox).
- `referrerpolicy="no-referrer"` on every custom-UI iframe so the dashboard's URL structure isn't leaked to peers.
- The "Open in new tab" affordance is always present; users who don't trust an iframe can open the URL directly.

This is a v1 hardening baseline, not a substitute for operator due diligence. Operators are responsible for the executors and stores they declare in `rimsky.yml`; the iframe sandbox is defense-in-depth, not the primary trust boundary.

### 5.7 Client state

localStorage only. Saved filters, pinned views, color theme. No dashboard-side persistence in v1. SQLite arrives with auth and server-side preferences (forward scope, §5.4).

### 5.8 Packaging contract

What the dashboard exposes to the deployment layer (the *what*, not the *how* — compose / Helm / k8s mechanics belong in those files, not in this spec):

- A standalone Dockerfile at `dashboards/rimsky-dashboard/Dockerfile` producing a single image that runs the Node server and serves the SPA.
- Configuration via env: `RIMSKY_CONTROL_API_URL`, `PORT` (defaults in §5.3).
- Listens on a single HTTP port (default `8090`); no other ports.
- `/healthz` endpoint suitable for compose/k8s liveness probes.
- The image is suitable for inclusion alongside the existing Rimsky services (compose, the existing Helm chart shape).

---

## 6. Conformance

Extend existing tooling additively; do not fork.

- `rimsky-executor-conformance` gains a `--check-observability` flag. When set, after the dispatch-protocol probes succeed, the binary additionally:
  - Calls `ExecutorObservability.GetCapabilities()`. Validates the proto message shape.
  - If `supports_trace_get`, runs a canned dispatch (in stub mode) and calls `GetTrace(dispatch_id)`. Validates the `Trace` envelope, validates that any standard-vocabulary events have the required `attributes` keys, validates that `complete: true` is set after dispatch terminal-s.
  - If `supports_trace_stream`, runs a canned dispatch and consumes `StreamTrace`. Validates that the stream replays history, that `trace_complete` is sent at terminal, that the connection closes cleanly.
  - Validates retention behavior: after `retention_after_terminal_seconds` elapses (or, if it is 0, immediately after terminal), `GetTrace` returns `evicted: true`.
- `rimsky-store-conformance` gains the same `--check-observability` flag for `StoreObservability`. Same shape: capabilities probe, then per-method probes scoped by declared capability bits, then retention check.
- Both protocols are optional. An executor/store that does not implement observability passes `--check-observability` trivially as long as `GetCapabilities()` returns `supports_*: false` flags.
- Both conformance binaries continue to honor `--require-stub-mode` (which gates the run on stub-mode probes for LLM-calling executors). The `--check-observability` flag composes with it: `--check-observability --require-stub-mode` runs both layers and passes only when both layers pass.

---

## 7. Auth

V1 inherits the per-project deployment / network-perimeter model from `docs/history/2026-05-01-auth-and-multitenancy.md`. The dashboard runs behind the same perimeter as control-api; observability endpoints are unauthenticated; no principal field; no tenant scoping.

The Node server in §5.3 is the natural future seat for richer auth, because it is the user-facing surface. When the §2 forward path in the auth doc is taken (principal field on control-api requests), the dashboard's Node server can front-end-authenticate users and pass the resulting principal through to control-api calls. Control-api stays auth-blind in v1; the dashboard's Node server is where session and identity concerns will land. None of that is v1 work.

The dashboard MUST NOT bypass any future control-api auth; it is just another HTTP client and inherits whatever auth control-api ends up enforcing.

---

## 8. Schema

No new Postgres tables or columns. The Rimsky observability API is a read view over existing state:

- `rimsky_templates`, `rimsky_template_tags`
- `rimsky_instances`
- `rimsky_frames`
- `rimsky_nodes`
- `rimsky_dispatch`
- `rimsky_lock_holders`, `rimsky_claim_holders`
- `rimsky_schedules`
- `rimsky_store_lifecycle`

Endpoints are queries against these tables, joined and projected for the documented response shapes. No background processes write new state. No new migrations.

---

## 9. Repo layout

What this spec adds and where:

```
dashboards/
  rimsky-dashboard/                   # NEW collection
    package.json
    tsconfig.json
    vite.config.ts
    Dockerfile
    src/
      client/                         # React SPA
        main.tsx
        routes/
        components/
        lib/
      server/                         # Hono Node server
        index.ts
        proxy.ts
        config.ts
      shared/                         # types shared between client and server
    public/
    tests/
      unit/                           # vitest
      e2e/                            # playwright
    README.md

proto/v1/
  executor_observability.proto        # NEW
  store_observability.proto           # NEW

core/
  observability/                      # NEW: control-api endpoint handlers + handshake
    handler.go
    handshake.go
    handler_test.go
  controlapi/
    routes.go                         # adds /v1/observability/* mount
  config/
    rimsky_yml.go                     # adds optional observability_endpoint field per executor
                                      # and per store entry (§4.1)
  cmd/
    rimsky-executor-conformance/
      main.go                         # adds --check-observability flag
      observability_check.go          # NEW
    rimsky-store-conformance/
      main.go                         # adds --check-observability flag
      observability_check.go          # NEW

executors/
  claude-agent/src/observability.ts   # NEW: implements ExecutorObservability
  http-node/observability.go          # NEW: implements ExecutorObservability
  stub/observability.go               # NEW: minimal capabilities response

stores/
  filesystem/observability.go         # NEW
  postgres/observability.go           # NEW; includes admin views (items queue, pick-policy state)
  stub/observability.go               # NEW: minimal capabilities response
```

The `core/observability/` package depends on `core/persistence/` (the unified persistence abstraction backed by either Postgres or SQLite) for read access, follows the existing import rules (no scheduler/supervisor/controlapi imports across each other; this package is read-only and may be imported by `controlapi/` only; driver-specific subpackages `core/persistence/postgres/` and `core/persistence/sqlite/` are NOT imported), and exposes pure handler functions wired by `controlapi/`'s router. Backend-agnostic by construction.

Out of scope for this spec (operator/deploy concerns): the `deploy/docker-compose.yml` and Helm chart updates that include the dashboard image. Those are deployment-shape changes that consume the §5.8 packaging contract.

---

## 10. Testing strategy

- **Rimsky observability API**: integration tests in `core/observability/handler_test.go`, parameterized over `persistence.Driver` so each test runs against both the Postgres backend (using the existing testcontainers-postgres harness at `core/internal/pgtest`) and the SQLite backend (in-process, file-backed; reference `core/persistence/sqlite/integration_test.go`). Each endpoint verified against fixtures: empty state, single-instance happy path, multi-instance pagination, filter combinations, error responses.
- **Executor observability protocol**: unit tests for the proto handlers per executor. The shipped executors (`http-node`, `claude-agent`, `stub`) each gain a small test that exercises `GetCapabilities` + `GetTrace` + `StreamTrace`. Plus the additive `--check-observability` conformance probe.
- **Store observability protocol**: same shape per store. Filesystem and postgres get a richer probe (admin views), stub gets the minimal capabilities-only probe.
- **Dashboard reference implementation**:
  - Vitest unit tests for SPA components (event-rendering for each standard category, capability-gated UI affordances).
  - Vitest unit tests for the Node server's proxy and config layer.
  - Playwright end-to-end tests against a running stack: `docker compose up`, drive the UI, assert flows (browse instances → frame → dispatch → trace; browse stores → claim; trigger custom-UI iframe render).
- **Cross-protocol smoke**: extend `test/smoke/` (the existing scenario harness) with a smoke test that boots the full stack (rimsky core + http-node + claude-agent stub + filesystem store + postgres store + dashboard), runs a frame, and asserts that the dashboard's HTTP endpoints serve the expected views (system summary contains the running frame; instance detail contains the cascade graph; trace endpoints return events).

---

## 11. Out of v1 / future scope

- **Dashboard-side auth + sessions.** Front-end auth on the Node server; pass-through to a future control-api principal field. Depends on `docs/history/2026-05-01-auth-and-multitenancy.md` §2.
- **Server-side preferences** (saved filters, dashboards, annotations, theme sync across devices) backed by SQLite in the dashboard's Node server.
- **Derived / cached aggregations** (top-N failing nodes by hour; flake-frequency by template; etc.) computed by the dashboard's Node server.
- **Write-action UX** (force-fire schedule, invalidate node, register/deploy template, undeploy, instance terminate) wrapping existing control-api admin endpoints.
- **Search** across templates / instances / dispatches / events.
- **Alerting / notifications** with operator-configurable rules.
- **Multi-deployment views** — one dashboard, many Rimsky deployments.
- **Custom user-defined dashboards** — operator-built layouts saved server-side.
- **Replay / time-travel** of past frames (depends on retention being increased and an event-sourced view layer).
- **Tenant-aware views** (depends on `docs/history/2026-05-01-auth-and-multitenancy.md` §3.2 — rimsky-enforced multi-tenancy).
- **OTel / metrics / log forwarding integration.** Dashboard composes the three observability protocols; OTel/Datadog/Grafana integration stays the operator's choice and is independent of this spec.
- **Queryable lifecycle-event timeline.** v1 surfaces per-store lifecycle delivery state from `rimsky_store_lifecycle` via the per-store endpoint (§1.2.1), but does not expose a unified `template_registered` / `template_deployed` / `instance_terminated` timeline. A future addition to control-api could write a `rimsky_events` row alongside firing each lifecycle RPC; the dashboard would then surface it via the existing `/v1/observability/events` endpoint with no further protocol change.

---

## 12. Open questions

Major shape decisions are resolved; the items below are implementation-time choices to make as the work lands:

1. **Proxy cache invalidation for `/api/exec/{name}/*` and `/api/store/{name}/*`.** The dashboard's Node server caches the executor/store endpoint lookup it gets from control-api. When control-api re-resolves a peer (the 60s observability re-probe in §4) the Node server should refresh on a similar cadence. Decide: refresh interval, behavior on cache miss (re-probe synchronously vs. fail the request), and whether to expose an admin trigger. Defaults can land with the implementation.

2. **CSP `frame-src` value (§5.6).** v1 sets `frame-src *` to permit any operator-declared custom-UI host. A safer default would be to enumerate hosts derived from the declared `custom_ui.ui_url` values seen during the observability handshake. Decide whether enumeration is worth the extra plumbing for v1.

3. **Conformance suite scope on the `--check-observability` retention probe.** The probe in §6 verifies that eviction happens after `retention_after_terminal_seconds`. For executors that declare large retention values (hours) the probe would block. Decide: cap the probe wait, skip the eviction sub-check above a threshold, or require executors to provide a stub-mode "fast retention" override.

4. **`ListClaims` filter surface for stores (§3.7).** v1 declares only `state_filter`. Real-world store debugging may want time-range filters (`opened_after`, `opened_before`). Decide whether to add them as part of v1 or wait for a concrete need.
