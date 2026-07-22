# claude-agent: full executor protocol coverage

**Date:** 2026-05-28 (rewritten 2026-06-17; piece #1 retired 2026-06-18 — see "Revision note" at end)
**Touches:** `lib/services/executors/claude-agent/`
**Type:** Pre-spec sketch.

## Finding — piece #1 retired (2026-06-18)

The "missing AwaitAsyncCallback emit tool" framing was a misread of the executor layering. The claude-agent executor is **inherently async at the envelope layer** — its gRPC `Execute` always pre-replies `Outcome.AwaitAsyncCallback` carrying an executor-chosen `async_ack_id`; the agent script never emits that outcome itself. The script's only settling options are the three terminals (`Success` / `Error` / `Park`), which already have tools (`report_complete` / `report_error` / `report_park`). The proto explicitly forbids chaining `AwaitAsyncCallback` inside `AsyncCallbackBody` for exactly this reason — the webhook IS the second half of the async path.

For the "wait on an external system" use case that motivated #1, `report_park(reason: await_callback)` is already the right tool: the supervisor parks the node and an external invalidate wakes it. There is no remaining gap on the emit side; piece #1 is closed without work.

Piece #2 (dispatch-context exposure) is the live, remaining gap. Spec scope below collapses to #2 only.

## Why

`lib/services/executors/claude-agent/` is rimsky's reference Claude-Code-driven executor and the canonical example consumers fork or derive from. Its MCP tool surface should expose a way to emit every output the executor wire protocol allows, and to read every input the wire protocol delivers — anything less pushes consumers to fork the executor or wrap it, which defeats the "claude-agent is the universal Claude-Code-driven executor" framing.

Today it doesn't. Two coverage gaps are live:

1. The wire has four `proto:executor.proto::Outcome` variants — `Success`, `Error`, `Park`, `AwaitAsyncCallback`. The agent's MCP exposes tools for the first three (`report_complete`, `report_error` + `report_blocked`, `report_park`) but not for `AwaitAsyncCallback`. An agent that wants to hand off long-running work to an external system and resume via rimsky-issued callback has no way to emit the verdict.
2. `proto:executor.proto::ExecuteRequest` carries `run_scope_id`, `dispatch_id`, `prior_dispatch_id`, and `prior_dispatch_disposition` (the `PriorDispatchDisposition` classifier — `stale_recovery` / `retry_after_error` / `recalculate`). None of those reach the agent. The agent's `attributes_read` returns only the per-run attribute bag captured at spawn — no `concept:run-scope` identity, no dispatch identity, no retry-vs-fresh signal. An agent on a retry-after-error or stale-recovery dispatch has no first-class way to know it.

## Design principle

**Faithful protocol coverage, no flow escape.**

- For every output the executor protocol allows the executor to emit, the agent's MCP exposes a tool to emit it.
- For every input the protocol delivers to the executor, the agent has a way to read it.
- The MCP surface adds NO capability beyond the protocol. No "post a message to another instance"; no "register a template"; no "read sibling-node attribute outside of substitution"; no "force-fire a downstream cascade." The agent participates as a fully-rigged executor implementor, not a privileged backdoor into rimsky orchestration.

## Audit (verified 2026-06-17)

### Outputs (executor → rimsky)

| Wire output | Today | Gap |
|---|---|---|
| Heartbeat | Handled by Claude Code's agent harness (activity-based) | None — already implicit |
| `proto:executor.proto::Outcome.Success` | `report_complete` | None |
| `proto:executor.proto::Outcome.Error` | `report_error` (arbitrary class) + `report_blocked` (agent_blocked class) | None |
| `proto:executor.proto::Outcome.Park` (typed `ParkReason`: `await_callback` / `snooze`) | `report_park` | None |
| `proto:executor.proto::Outcome.AwaitAsyncCallback` | Resolved by `report_park(reason: await_callback)` — see "Finding — piece #1 retired" at top | None (closed without work) |

### Inputs (rimsky → executor / agent)

| `ExecuteRequest` field | Today | Gap |
|---|---|---|
| `attributes` (substitution-resolved, includes `{{child.partition_key}}` and `{{claim.<name>.payload}}` for fan-out children) | `attributes_read` snapshot | None — partition keys reach fan-out children via substitution, as the protocol intends |
| `attributes_schema` | Surfaced via the dispatched bag | None |
| `callback_url` | Used internally by `attributes_set`; not surfaced as a tool input the agent reads | None — the agent calls back through the tool, not by URL |
| `dispatch_id` (field 12) | — | **Not surfaced** |
| `run_scope_id` (field 16) | — | **Not surfaced** |
| `prior_dispatch_id` (field 14) | — | **Not surfaced** |
| `prior_dispatch_disposition` (field 15: `stale_recovery` / `retry_after_error` / `recalculate`) | — | **Not surfaced** |

## Proposed additions

### 1. AwaitAsyncCallback emission — RETIRED

See "Finding — piece #1 retired" at top of file. The original proposal below is preserved for context but is no longer in scope. `report_park(reason: await_callback)` is the right tool for the use case, and the proto explicitly forbids `AwaitAsyncCallback` chaining inside `AsyncCallbackBody`. The agent script never emits `AwaitAsyncCallback`; only the executor's gRPC envelope does, and it already does so unconditionally at pre-reply time.

Original proposal (retired):

```
report_await_async_callback(
  token: string,
  async_ack_id: string,
  expected_completion_ms?: number
)
```

### 2. Dispatch-context exposure

The four `ExecuteRequest` context fields above need to reach the agent. Two candidate shapes — both consistent with the "no flow escape" principle (read-only snapshot captured at spawn, same lifetime as `attributes_read`):

**Option A — new tool.**

```
dispatch_context_read(token: string) → {
  dispatch_id: string,
  run_scope_id: string,
  prior_dispatch_id?: string,
  prior_dispatch_disposition?: "stale_recovery" | "retry_after_error" | "recalculate"
}
```

Cleanest separation: the per-node attribute bag stays the per-node attribute bag; rimsky-injected dispatch identity sits in its own tool. Maps 1:1 onto the wire fields, so future additions to `ExecuteRequest` extend this tool's return shape.

**Option B — reserved keys on `attributes_read`.**

Inject `_rimsky.dispatch_id`, `_rimsky.run_scope_id`, `_rimsky.prior_dispatch_id`, `_rimsky.prior_dispatch_disposition` into the snapshot `attributes_read` already returns. No new tool; one shape to learn.

Spec-time choice; A is the cleaner separation. Mentioned together because both decisions land at the same code site.

## Explicit non-goals

- **No replacement for the retired NamedEvent surface.** `proto:executor.proto` field 1 on `AsyncCallbackBody` and field 1 on `Outcome` Success/Error/Park is reserved (`"was: repeated NamedEvent events = 1"`). Per-emission events collapse into the chosen outcome's `tags` set with per-emission data on `attributes_delta`; that is the protocol's answer to "something happened mid-run," and the agent's `report_complete` / `report_error` / `report_park` already accept it.
- **No `read_node_attribute` runtime read.** Sibling-node attribute access stays via substitution at dispatch time.
- **No `read_node_event` runtime read.** No more NamedEvents to read; downstream coupling rides `tags` + subscription `when:` filters at the template layer.
- **No `post_message` to instances.** Publishers post messages; agents don't.
- **No `register_template` / `create_instance` / `deploy_template` tools.** Lifecycle control is the operator's.
- **No tools that bypass the inertness invariant.** Payloads stay inert in rimsky-side persistence; agent-side reads only at sanctioned leaves.

## Spec scope

- **#1 (AwaitAsyncCallback tool):** RETIRED — see "Finding — piece #1 retired" at top of file. No work.
- **#2 (dispatch-context exposure):** the live remaining gap; one new MCP tool + the snapshot capture path.

## Touch points

- `code:lib/services/executors/claude-agent/src/internal-mcp-tools.ts` — add `ReportAwaitAsyncCallbackInput` + the tool definition; optionally `DispatchContextReadInput` for #2A.
- `code:lib/services/executors/claude-agent/src/internal-mcp-server.ts` — handler that calls the wire-write path; optionally the dispatch-context snapshot capture for #2.
- `code:lib/services/executors/claude-agent/src/server.ts` and/or `http-bridge.ts` — emit the `AwaitAsyncCallback` outcome on the wire-write path.
- `code:lib/services/executors/claude-agent/src/attributes-tools.ts` — only if #2B is the chosen shape (extend the snapshot under reserved `_rimsky.*` keys).

## Revision note (2026-06-17)

This sketch was originally written 2026-05-28 alongside an `rimsky-github-bot` plan that wanted per-item fan-out via `concept:named-event` + `concept:node-subscription`. Three things changed since:

- **`concept:named-event` was retired.** `proto:executor.proto` field 1 on `Outcome.Success` / `Outcome.Error` / `Outcome.Park` / `AsyncCallbackBody` is reserved with the comment `"was: repeated NamedEvent events = 1"`. The original sketch's items #1 (`emit_named_event` MCP tool) and #2 (`RIMSKY_AGENT_DECLARED_EVENTS` env-driven declared-events override) are obsolete — the wire no longer carries the message they would emit. Both are dropped from this rewrite.
- **The bot's fan-out need moved to `concept:fan-out` via `SplitScope`.** That work lives in the companion `sketch:bundled-stores-split-scope`. Per-dispatch sub-claim access for fan-out children already rides `{{child.partition_key}}` + `{{claim.<name>.payload}}` substitution, not runtime tools.
- **`report_park` exists with typed `ParkReason` (`await_callback` / `snooze`).** The original sketch's "missing tool for AwaitAsyncCallback" was conflated in places with "missing park-reason discrimination"; that conflation is resolved — `report_park` carries the typed reason today and `AwaitAsyncCallback` remains its own outcome variant.

What remains and is still live: **#3 from the original (AwaitAsyncCallback tool) and #4 (run-scope context exposure)**, renumbered #1 and #2 here.
