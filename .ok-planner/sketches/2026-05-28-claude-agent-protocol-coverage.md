# claude-agent: full executor protocol coverage

**Date:** 2026-05-28
**Touches:** `lib/services/executors/claude-agent/`
**Type:** Pre-spec sketch.

## Why

While planning an external consumer (`rimsky-github-bot`, a sibling
repo using rimsky off-the-shelf), grounding caught a gap: the spec
needed per-item fan-out via `concept:named-event` +
`concept:node-subscription`, but claude-agent's MCP tool surface
exposes no way to emit a `NamedEvent`. The wire protocol supports it;
claude-agent just never sends one (`declaredEvents = []` and no code
path emits the wire message).

The general pattern: claude-agent's agent-facing MCP covers a subset
of what the executor (+ observability) protocol allows the executor
to emit, and one outcome variant (AwaitAsyncCallback) is missing
entirely. Consumers that need the rest end up either forking
claude-agent or wrapping it in a custom executor binary. Both defeat
the "claude-agent is the universal Claude-Code-driven executor"
framing.

## Design principle

**Faithful protocol coverage, no flow escape.**

- For every OUTPUT the executor protocol allows the executor to emit,
  the agent's MCP exposes a tool to emit it.
- For every INPUT the protocol delivers to the executor, the agent
  has a way to read it.
- The MCP surface adds NO capability beyond the protocol. No
  "post a message to another instance"; no "register a template";
  no "read sibling-node attribute outside of substitution"; no
  "force-fire a downstream cascade." The agent participates as a
  fully-rigged executor implementor, not a privileged backdoor
  into rimsky orchestration.

## Audit

### Outputs (executor → rimsky)

| Wire output | Today | Gap |
|---|---|---|
| Heartbeat | Handled by Claude Code's agent harness (activity-based) | None — already implicit |
| NamedEvent (name + inert payload) | `declaredEvents = []`; no emission path | **Missing tool + missing declaration mechanism** |
| StreamClose: Success | `report_complete` | None |
| StreamClose: Error | `report_blocked` (agent/blocked class) + `report_error` (arbitrary class) | None |
| StreamClose: Park (await_callback / snooze) | `report_park` | None |
| StreamClose: AwaitAsyncCallback | — | **Missing tool** |

### Inputs (rimsky → executor / agent)

| Wire input | Today | Gap |
|---|---|---|
| Dispatched attributes (incl. substitution-resolved values) | `attributes_read`; `user_prompt` / `system_prompt` delivered in the dispatched bag | None |
| Run-scope context (run-scope id, parent-run id, partition key for fan-out children) | Unclear — needs an audit of what's surfaced to the agent today | Possible gap; close via well-known reserved attribute names if not already surfaced |
| Async-callback body (only relevant if AwaitAsyncCallback is used) | N/A today | Becomes relevant once AwaitAsyncCallback ships |

## Proposed additions

### 1. NamedEvent emission

MCP tool:

```
emit_named_event(name: string, payload: object) → void
```

- Cross-check `name` against the executor's declared events list at
  the MCP boundary; reject with a tool error if not declared.
- Payload JSON-serialized to the NamedEvent wire shape.
- Inertness discipline applies (`@blessed-invariant 21` per
  `concept:inertness`) — claude-agent does not parse, log, or
  format the payload.

### 2. Declared-events configuration override

Today `declaredEvents` is a TypeScript constant baked into the image.
For FROM-claude-agent derivative images to advertise their own event
names without forking source, accept an env var:

```
RIMSKY_AGENT_DECLARED_EVENTS=name1,name2,name3
```

Read at startup; replaces the empty default. Advertised via the
existing `Capabilities.declared_events` so rimsky's template
registration cross-checks `subscribes:` against it.

Env var (image-level identity) rather than per-dispatch attribute
(per-run state) because declared events are a property of the
binary's contract.

### 3. AwaitAsyncCallback outcome

MCP tool:

```
report_await_async_callback(
  token: string,
  async_ack_id: string,
  hint?: string
) → void
```

Emits the AwaitAsyncCallback variant of StreamClose. Lets the agent
hand off long-running work to an external system and wake on the
rimsky-issued callback at `${callback_url}/v1/callback/{async_ack_id}`
(body keyed `type`, per `concept:executor`'s invariant + the CLAUDE.md
gotcha).

Rare for typical interactive Claude Code agents, but a real protocol
variant the agent should be able to use when needed (e.g., agent
spawns a long-running batch job, parks awaiting completion).

### 4. Run-scope context exposure (audit + close)

Audit what the agent sees of:

- Run-scope id of the current dispatch.
- Parent-run id (for fan-out children, sub-graph dispatches).
- Partition key (for fan-out children specifically).

If any are not currently surfaced via the dispatched attribute bag
or a reserved attribute namespace, surface them via well-known
reserved attribute names (e.g., `_rimsky.run_scope.id`,
`_rimsky.run_scope.parent_run_id`,
`_rimsky.run_scope.partition_key`). No new tools; just ensure the
existing `attributes_read` returns these.

Agents running as fan-out children currently have no first-class
way to know they ARE fan-out children, which is information they
sometimes need for prompt construction.

## Explicit non-goals

- **No `read_node_attribute` runtime read.** Sibling-node attribute
  access stays via substitution at dispatch time. Runtime reads
  would let the agent see post-dispatch mutations and shape its
  behavior on them — an escape from rimsky's cascade-frame
  discipline.
- **No `read_node_event` runtime read.** Same reason. Named-event
  consumption stays via subscription + (for substitution) the
  existing `{{nodes.X.event.Y}}` grammar.
- **No `post_message` to instances.** Publishers post messages;
  agents don't. Cross-node coupling within an instance is
  NamedEvent + node-subscription; cross-instance messaging is a
  publisher concern.
- **No `register_template` / `create_instance` / `deploy_template`
  tools.** Lifecycle control is the operator's, not the agent's.
- **No tools that bypass the inertness invariant.** Payloads stay
  inert in rimsky-side persistence; agent-side reads only at
  sanctioned leaves.

## Spec scope

- Minimum to unblock the rimsky-github-bot consumer: #1 + #2.
- Worth folding in for completeness: #3 + #4.

#3 and #4 are independent of the bot's need; they could ship as a
follow-up if the spec wants to stay tight.

## Touch points

- `lib/services/executors/claude-agent/src/internal-mcp-tools.ts`
  (new tool definitions).
- `lib/services/executors/claude-agent/src/internal-mcp-server.ts`
  (tool handlers, calling out to the executor wire connection).
- `lib/services/executors/claude-agent/src/server.ts` and/or
  `http-bridge.ts` (the wire-write path for NamedEvent + the new
  StreamClose variant).
- `lib/services/executors/claude-agent/src/expected-attributes-schema.ts`
  (drop the hard-coded empty `declaredEvents`; read from env at
  startup).
- `lib/services/executors/claude-agent/src/observability.ts` (advertise
  the env-driven declared-events list in Capabilities).
