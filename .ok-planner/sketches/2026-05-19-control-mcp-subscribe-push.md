# Control-API MCP v2: subscribe / push surface

**Date:** 2026-05-19
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers
**Related:** `sketch:2026-05-07-agentic-telemetry` (SSE-direct event stream, partial overlap); `concept:control-api`; `concept:event-log`

## Context

The 2026-05-15 control-plane-MCP-and-auth landing (`file:.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`) shipped an in-process MCP surface at `route:POST /mcp` as a tools-only v1: every JSON-RPC call is a one-shot request/response, no streaming, no push. The catalog at `code:control/controlapi/mcp_route.go::builtinSchemas` is roughly thirty CRUD-shaped tools — `instance_list`, `instance_get`, `template_register`, `node_invalidate`, `event_list`, `message_send`, `parked_node_list`, etc.

This covers "an agent operates rimsky" — register a template, create an instance, send a message, invalidate a node. It does **not** cover "an agent watches rimsky." An external agent that wants to react to lifecycle transitions has to poll `event_list` on a cadence, paying token cost on every tick to call the tool, parse the result, and decide whether anything happened.

Two motivating use cases that have come up in the docs-pipeline work:

- **The babysitter.** Start a long-lived agent, point it at rimsky, leave it. The agent watches one or more instances, intervenes when something hits a bad state (cascade stuck, dispatch wedged past silence-timeout, attribute writeback rejected with a specific `error_class`), and otherwise stays quiet. Cost-efficient observation requires a push channel — polling every minute for 30 minutes burns ~30 tool calls and ~30 turns even when nothing happens.
- **The integrator.** A separate process (deploy bot, dashboard backend, notification router) wants the same push surface but from a non-LLM consumer. MCP-over-HTTP is the obvious wire because it already exists, already authenticates, and already audits.

MCP itself defines a `resources/subscribe` capability and a server-pushed `notifications/resources/updated` notification. The streamable-HTTP transport supports server-push via SSE — we've been using it on the executor's *internal* MCP server. The transport machinery is already there; the control-api MCP just hasn't exposed it.

The related `sketch:2026-05-07-agentic-telemetry` proposes a direct `route:GET /events/stream` SSE endpoint with `LISTEN`/`NOTIFY` plumbing on the postgres side. That sketch and this one converge on the same underlying mechanism — a push-shaped feed over rimsky's event log — but reach it from different transport directions. The HTTP-SSE route is simpler and bypasses MCP; the MCP route is heavier but lands the push surface inside the existing auth/audit/permissions pipeline. They should be considered together at spec time; the recommendation here is that MCP v2 is the strategic surface because it gives every consumer (LLM agents, dashboards, ops tooling) the same auth model, and the SSE-direct route can stay as a parallel raw-feed escape hatch for consumers that want to skip the MCP envelope overhead.

## Goals

1. **Push, not poll.** An MCP client subscribes once to a resource URI and receives `notifications/resources/updated` whenever the underlying state changes. No client-side cadence.
2. **Filterable subscriptions.** A consumer interested only in `error`-kind events on a specific instance shouldn't receive every `lock_acquired` and `attributes_substituted` row.
3. **Reuse auth.** Subscribe requests gate on the same per-action permission model the tools surface uses (`mcp:read`, plus per-resource `read` actions where finer granularity is needed). Audit rows write on subscribe/unsubscribe and on each filtered notification fan-out at a sampled rate.
4. **Reuse transport.** No new wire format. MCP-over-HTTP streamable-HTTP transport handles server-push via SSE on the open GET stream; the existing per-session map (post the bug-1 fix in `code:executors/claude-agent/src/internal-mcp-server.ts`) is the model to mirror for the control-api server-side session table.
5. **Polite to LLM clients.** The agent's prompt-side ergonomics need to be: "subscribe once at startup, react to incoming notifications, call other tools when you want to act." The notification envelope carries enough context that the agent doesn't immediately need to call `resources/read` to interpret it.

## Non-goals

- **WebSocket transport.** MCP over WebSocket is out of scope. The streamable-HTTP transport already supports server-push via SSE; adding a second transport is heavier and the use cases here don't justify it.
- **Replay-from-cursor on reconnect.** If a client disconnects and reconnects, it gets the current snapshot via `resources/read` and forward-only notifications from that point. No catch-up history replay through the MCP channel. Consumers needing replay use `event_list` with a cursor (the existing tool).
- **Cross-instance correlation.** A single subscription targets a single resource URI. Aggregations ("notify me when any instance enters `failed`") are implemented client-side by subscribing to multiple URIs.
- **Streaming partial results.** No streaming `tools/call` — tools stay one-shot request/response. The MCP `tools/list_changed` notification IS in scope (template deploys/undeploys change the operator-visible tool catalog when the per-template-runtime-action tools land), but `tools/call` streaming is not.

## Design

### URI scheme

Resources are addressed by a `rimsky://` URI:

| URI                                            | What it represents                                  | Read shape                                   | Subscribe events                                              |
|------------------------------------------------|-----------------------------------------------------|----------------------------------------------|---------------------------------------------------------------|
| `rimsky://instances`                            | The instance catalog                                | list of `{id, template_hash, state, …}`     | new instance created, terminated                              |
| `rimsky://instances/{id}`                       | One instance's state snapshot                       | `{id, template_hash, terminated_at, …}`     | state transitions, terminated_at set                          |
| `rimsky://instances/{id}/events`                | The event log for one instance                      | last-N events (filterable via query params)  | every new event row (filterable; see below)                   |
| `rimsky://instances/{id}/nodes`                 | All nodes in one instance                            | list of `{id, node_type, state, …}`         | per-node state transitions                                    |
| `rimsky://nodes/{id}`                           | One node's state                                     | `{state, last_outcome, frame_id, …}`        | state transitions on this node                                |
| `rimsky://instances/{id}/messages`              | The message log for one instance                     | last-N messages                              | every new message envelope                                    |
| `rimsky://parked`                               | All currently-parked nodes                           | list of parked rows                          | park-in / park-out transitions                                |
| `rimsky://held-frames`                          | All currently-held frames                            | list of held-frame rows                      | held / released transitions                                   |
| `rimsky://templates`                            | The template catalog                                 | list of `{hash, name, version, tags, …}`   | register / deregister / deploy / undeploy / tag-move           |

URIs are stable, opaque to clients, and match the existing chi route shape so the mapping from URI to query is mechanical.

### MCP wire

**`resources/list`** — returns the static catalog above plus dynamic per-instance / per-node entries the auth subject is permitted to see. Permission gating: same per-action model as the tools surface. The `*:read` wildcard in the bundled `viewer` role covers the whole catalog.

**`resources/read`** — one-shot snapshot of a resource URI. Equivalent to the corresponding `tools/call` (e.g. `resources/read rimsky://instances/{id}` ≡ `tools/call instance_get`) but framed as a resource read so clients can build subscribe-then-read-on-notification flows uniformly. The response is the existing API surface's JSON shape, wrapped in MCP's `ResourceContents` envelope.

**`resources/subscribe`** — JSON-RPC request:

```jsonc
{
  "method": "resources/subscribe",
  "params": {
    "uri": "rimsky://instances/036d.../events",
    "filter": {                                     // optional
      "kinds": ["state_transition", "error"],       // event kinds
      "node_id": "c2299875-...",                    // narrow by node
      "min_severity": "WARN"                        // ≥ this severity
    }
  }
}
```

Filter semantics are URI-specific. For `events`, the filter narrows by event kind / node-id / severity. For `nodes/{id}`, the filter can narrow to specific state transitions (e.g. `to_state: ["failed", "parked"]`). For `messages`, by `kind` / `target` / `sender_kind`. The filter shape is part of the URI's contract and is documented per-URI.

Server response: `{}` on success (idempotent — re-subscribing with the same URI + filter is a no-op for that session). Subscribing without permission returns the standard MCP `ErrorCode.MethodNotFound`-style error (consistent with how the tools surface gates).

**`notifications/resources/updated`** — server-pushed:

```jsonc
{
  "method": "notifications/resources/updated",
  "params": {
    "uri": "rimsky://instances/036d.../events",
    "delta": {
      "kind": "state_transition",
      "node_id": "c2299875-...",
      "occurred_at": "2026-05-19T11:08:24.001Z",
      "payload": { "from": "running", "to": "fresh", "reason": "handler_complete" }
    }
  }
}
```

The notification carries the event payload inline so the client doesn't need to immediately call `resources/read` to interpret. For high-volume resources (think: an instance with hundreds of events per second; not the normal case but possible with sensor-driven workloads), the notification can degrade to a coalesce signal — `delta: { coalesce: true, since: "<timestamp>" }` — meaning "something changed; call `resources/read` for the catch-up." Threshold: if the per-session notification queue exceeds a configurable depth (default 64), coalesce on. This is the server-side back-pressure mechanism.

**`resources/unsubscribe`** — by URI. Idempotent.

**`notifications/resources/list_changed`** — fires when the catalog of subscribable resources changes (new instance created, template registered, etc.) so a long-lived client can refresh its catalog without re-listing on a timer.

### Notification source

A trigger on `table:rimsky_events` fires a postgres `NOTIFY` on a channel keyed by instance_id. The MCP server holds one `LISTEN` connection per running pod (NOT per session — the listener fans out internally). Each notification carries the new event row id; the server reads the row, walks its in-memory `Map<sessionId, Set<SubscriptionSpec>>`, applies per-spec filters, and pushes `notifications/resources/updated` to matching sessions over their open MCP transports.

For SQLite (the rimsky/all dev image), no `NOTIFY`. Fallback: a 1-second poll on `rimsky_events.id > last_seen` per pod. Cheap because reads are local, and SQLite is dev-only anyway.

State-snapshot resources (`rimsky://instances/{id}`, `rimsky://nodes/{id}`) hook a parallel trigger on the underlying tables (`rimsky_instances`, `rimsky_nodes`). For high-write-rate tables that change without producing an event row (e.g., per-tick heartbeat updates on supervisor rows), explicit hooks at the relevant writers fire the notification rather than a blanket table trigger.

### Auth + audit fit

- **Subscribe gates** on a per-resource-class `read` action. `rimsky:instance:read`, `rimsky:event:read`, etc. The bundled `viewer` role's `*:read` wildcard covers the whole catalog. The 2026-05-15 spec's existing action vocabulary already names these per route; we reuse them. The `mcp:read` umbrella action (per the 2026-05-15 cycle-2 cleanup) covers the protocol-level `resources/list` + `resources/subscribe` machinery.
- **Audit row per subscribe / unsubscribe** with the full filter spec in `request_params` so the audit log captures who's watching what. Fan-out notifications **don't** write per-message audit rows by default — that volume can overwhelm the audit dispatcher. Instead, on subscribe we record the contract; on unsubscribe we record the contract closure plus a count of notifications delivered. An optional `audit_each: true` flag on subscribe forces per-notification rows for high-trust-required deployments; default off.
- **Identity scoping.** Subscriptions are per-session (one client connection). Auth identity rotation via `rimsky auth rotate-key` doesn't preserve subscriptions across the rotation grace window — the client's old key kept its session alive; the new key starts fresh. This is the same lifetime contract as the existing tools surface.

### Per-session machinery

The control-api MCP server's session state extends from "transport + bound McpServer" (the bug-1 fix shape) to also hold `Map<resource URI, FilterSpec>`. Subscribe inserts into the map; unsubscribe deletes; transport close drops the entry as part of session eviction.

The internal dispatcher is a goroutine pool that pulls from the `LISTEN` channel (or polling fallback), looks up matching subscriptions across all sessions, and fan-outs notifications to each session's MCP transport via the SDK's per-session notification API.

Back-pressure: each session has a bounded outbound queue (default 256). Overflow triggers the coalesce-degrade path (single coalesce notification, client re-reads). If even the coalesce notification can't drain, the session is closed with a `resources/lagged` error and the client must reconnect.

## Open questions

1. **Wildcard subscriptions.** Should `rimsky://instances` (no id) imply "notify on every event in every instance"? Useful for dashboards; dangerous for unaware LLM clients. Default off, opt-in via an explicit `wildcard: true` filter flag.
2. **Per-event vs. per-resource notification semantics.** The MCP spec's `notifications/resources/updated` is technically about a resource's contents changing, not about a delta. For event-log resources we're stretching the semantic (each new event is "the resource changed by adding one row"). Alternative: spec the notification as a custom `notifications/rimsky/event` to be more honest about the shape. Probably stay within the MCP spec's vocabulary and let the URI's contract document what `updated` means for event-shaped resources.
3. **Subscription persistence across pod restarts.** Today the supervisor + control-api are in one binary; restart means all sessions drop. With multi-pod (per the deployed posture), should subscriptions survive a pod restart by getting persisted to a `rimsky_subscriptions` table? Probably no — MCP is connection-scoped by design, and the client reconnects after a transport drop anyway. Persistence would be a v3 concern if it comes up.
4. **Cost-bounded notifications.** Should the server expose its outbound queue depth via a resource (e.g. `rimsky://debug/subscriptions`) so clients can self-throttle? Probably yes — it's cheap to add and useful for the LLM-as-operator case where the agent wants to monitor its own observability load.
5. **Resource templates per MCP spec.** The MCP `resources/templates/list` shape lets servers expose parameterized resource URIs. Worth considering — `rimsky://instances/{id}/events?kind={kind}` as a template the client can fill in — but adds wire complexity. V2 stays static; v3 can add templates if a real need surfaces.
6. **Multi-pod fan-out.** With control-api running on multiple replicas, the `LISTEN` connection is per-pod. A notification fired in pod A needs to reach a subscription held in pod B. Postgres `NOTIFY` is broadcast — every listening pod receives it, so this works naturally. SQLite has no equivalent; the dev posture stays single-pod and this concern stays Postgres-only.

## What this enables

**The babysitter, in eight lines of agent prompt:**

```text
You are watching rimsky instance {{id}}. At startup, subscribe to:
  - rimsky://instances/{{id}} (state transitions)
  - rimsky://instances/{{id}}/events (kind: ["error","work_completed"])
On any notification, decide whether to intervene:
  - terminated_at set + final_state == "failed" → call event_list,
    summarize the failures, call message_send with kind: "report".
  - error event with error_class == "silence_timeout" → call
    node_invalidate to retry; report the retry attempt.
Otherwise stay quiet. End when terminated_at is set.
```

The agent's per-iteration cost goes from "wake every minute, poll, decide nothing happened, sleep" to "sleep until pushed; react to push." Token spend is proportional to interesting events, not to wall-clock time.

**Integration-side, the same surface:** the firm-dashboard or admin dashboard backends subscribe to the same URIs via the same MCP transport over the same auth/audit pipeline. No second observability stack.

**Cross-cuts with `sketch:2026-05-07-agentic-telemetry`:** the cost / subprocess-lifecycle events that sketch proposes become first-class subscribable resources here. An operator agent can subscribe to `rimsky://instances/{id}/events?kinds=[executor.cost_recorded]` and intervene when a runaway dispatch crosses a USD threshold. The two sketches reinforce each other; if both land, the babysitter pattern is fully self-contained.

## Sketch boundaries

This sketch deliberately stops short of:

- Specifying the per-URI filter grammar in detail (kinds, operators, regex support).
- Choosing between custom-notification vocabulary (`notifications/rimsky/event`) and stock MCP (`notifications/resources/updated`) for event-shaped pushes.
- Designing the multi-pod listener-coordination protocol beyond noting that postgres `NOTIFY` fans out naturally.
- Building any client-side library / agent prompt convention beyond the eight-line example.

A proper spec settles each. This sketch's job is to argue that the MCP surface is the strategic place to land push observability (vs. a parallel HTTP-SSE endpoint), and to capture the use cases that motivate the answer.
