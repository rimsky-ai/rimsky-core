# Attribute carry-forward + in-process utility executors

**Date:** 2026-06-14
**Spec:** new

Adds the smallest platform foundation that lets a multi-loop coding orchestrator (build/validate with sub-graph passes) be expressed cleanly as a rimsky template. Three foundational mechanisms — scope-bounded attribute carry-forward, an in-process executor transport, and a `bytes scratch` field on `node_run` — plus two consumers — a `loop_counter` utility node and a small claude-agent change moving its CLI session token to an attribute.

## User outcomes

### STORY-attribute-carry-forward

As a template author, I can write a node whose executor sets an output attribute and observe that value present in the incoming attribute bag on subsequent dispatches of the same node within the same RunScope; in a new RunScope (sub-graph invocation, fan-out partition) the same node starts with the schema's defaults, so stateful nodes hold their state in their own attributes uniformly across the platform.

**Acceptance:** I declare a node whose executor sets `count` on its terminal; cascade re-fires the node within the same RunScope; on the next dispatch, the incoming attribute bag contains the prior `count` value. The same node in a fresh RunScope's first dispatch sees the schema's default for `count`, not any prior scope's value.

**Falsifier:** A node's executor sets `count = N` on terminal; the cascade re-fires within the same scope; the next dispatch's incoming attribute bag is missing the prior writeback (either absent, set to the schema default, or replaced only by source substitution with no carry-forward overlay). OR: a sub-graph invocation's first dispatch inherits the calling scope's prior writeback.

**Proof:** Demo — a scenario test under `test/scenarios/` that runs a deterministic stateful node through three dispatches in one RunScope (observes `count` 0→1→2→3 via writeback + carry-forward), then invokes a sub-graph and observes a node in the sub-graph sees the schema default.

### STORY-loop-counter-cap

As a template author, I can use a `kind: loop_counter` node with a `max` input attribute, and observe it emit a `loop` named event on each dispatch while count is below max and a `done` named event when count reaches max, so I can express bounded iteration without authoring a custom executor.

**Acceptance:** I declare a node `kind: loop_counter, max: 3`; cascade re-fires the node three times via a subscription on its `loop` event; on the third dispatch the node emits `done` instead of `loop`; a downstream subscriber on `done` fires.

**Falsifier:** loop_counter emits `loop` after reaching `max`. OR: it emits `done` before reaching `max`. OR: count does not carry across dispatches and `done` never fires.

**Proof:** Demo — scenario test wiring loop_counter (max=3) to a sink subscriber on `loop` and a different sink on `done`; observes `loop` fires three times then `done` fires once.

### STORY-claude-agent-session-resume

As a template author, I can wire claude-agent so its Claude Code CLI conversation continues across multiple dispatches of the agent node within a RunScope and starts fresh in a new RunScope, so build/validate orchestrators preserve agent reasoning continuity within a pass and reset across passes.

**Acceptance:** I declare a claude-agent node in a graph; cascade re-fires the node multiple times within one RunScope; each post-first dispatch continues the same CLI conversation from the prior turn (agent has the prior turn's context). A sub-graph invocation of the same template starts the agent with a fresh Claude conversation, no carried session from the parent scope.

**Falsifier:** A re-dispatch within the same RunScope starts a brand-new CLI conversation (agent has no recollection of the prior turn). OR: a sub-graph invocation inherits the parent scope's CLI session.

**Proof:** Demo — scenario test using the bundled-services integration harness under `lib/services/test/` that runs claude-agent through three dispatches in one RunScope (agent's responses must reference content from prior turns), then invokes a sub-graph and observes the agent starts fresh.

### STORY-inproc-utility-executor

As a template author or operator, I can reference a utility node kind (e.g. `loop_counter`) in a template and have it dispatched without registering an external executor service for it, so utility nodes don't require additional deployment to function.

**Acceptance:** I deploy rimsky with no external executor configuration for `loop_counter`; I register a template referencing `node X: { kind: loop_counter, ... }`; the template registers successfully; an instance of the template dispatches X without any external IPC; X terminates as expected.

**Falsifier:** Registering a template that references `kind: loop_counter` requires the operator to also register an external executor service for it. OR: dispatch fails because no executor service is reachable for the utility node.

**Proof:** Example — a minimal template referencing `kind: loop_counter` with no external executor configured for it; the template registers and runs to completion in a deployment with no utility-executor service.

### STORY-opaque-executor-scratch

As an executor author, I can attach opaque bytes to a terminal event and observe them on the next dispatch's recovery of the same node_run, so I can carry in-flight state across the runtime's stale-heartbeat recovery cycle without rimsky inspecting or modifying the bytes.

**Acceptance:** I write an executor that writes scratch — either mid-dispatch via the executor-protocol scratch callback (HTTP POST mirroring the §12.5 attributes incremental-writeback pattern) or by attaching scratch bytes to its stream-close outcome; rimsky persists the scratch on the dispatch row that received it; when the next dispatch row for the same node is created (via cascade re-dispatch, stale-heartbeat recovery, or retry-after-error enqueue — all linked by `prior_dispatch_id`), the enqueue path copies the prior dispatch's scratch onto the new row, and the new dispatch's incoming request carries the original scratch bytes verbatim.

**Falsifier:** Scratch persisted on the prior dispatch is not present on the next dispatch's incoming request. OR: the bytes returned differ from the bytes the executor wrote (rimsky inspected or transformed them).

**Proof:** Proof — Go-side executable test exercising the round-trip: executor writes scratch (mid-dispatch via the scratch callback route, or via stream-close attach); enqueue a follow-on dispatch row with the prior-dispatch link (using the same mechanism the cascade re-dispatch / stale-heartbeat recovery / retry-after-error paths use); assert the new dispatch's request carries the original scratch bytes verbatim.

## Architecture

Four foundational changes plus two consumer changes, mapping to the stories above.

**Foundational changes:**

1. **Attribute hydration gains a pre-substitution carry-forward step.** On dispatch of node X in RunScope S, the runtime hydrates the attribute bag from the most-recent prior node_run of X in S (via a lookup joining the per-run attribute ledger — indexed by node_run_id, with denormalized node_id — with the node_run rows that carry run_scope_id, selecting the most recent prior run for (node, scope)), then applies per-field `source:` substitution from this dispatch's wait-set senders, overwriting carry-forward values for source-bound properties. Cross-RunScope hydration is forbidden — sub-graph and fan-out RunScopes start with schema defaults. Self-state carries via copy; cross-node values flow via substitution; the two channels are orthogonal and operate on different sources.

2. **`bytes scratch` column on `rimsky_node_runs`, surfaced through the executor protocol.** Opaque per-dispatch state. Every enqueue path that stamps `prior_dispatch_id` (heartbeat-stale, retry-after-error, recalculate) copies scratch from the prior row to the new row at row-creation time, so the next dispatch's `ExecuteRequest` carries the prior scratch verbatim. The executor writes scratch either at stream-close (by attaching scratch bytes to the outcome) or mid-dispatch (by POSTing to a new `POST {callback_url}/v1/runs/{run_id}/scratch` HTTP callback route mirroring the existing §12.5 attributes incremental-writeback pattern; claude-agent additionally wraps the route as an MCP tool for its CLI, in-process handlers call the writeback helper directly). Inert: rimsky never inspects (extends `concept:inertness` / `@blessed-invariant 21`).

3. **`inproc` transport on `code:lib/runtime/executor/client.go::ClientPool`.** Third case in the factory's transport switch, alongside the existing `grpc` and `http` clients. `InProcessClient` is a `Client` impl backed by a `map[executor_id]InProcessHandler` populated at runtime startup. The dispatch site at `code:lib/runtime/runner_dispatch.go#221` (`client.Execute(ctx, req)`) is unchanged — transport stays opaque to dispatch.

4. **`kind:` sugar resolver at template registration.** A template node may declare `kind: <name>` (a new optional field, distinct from the existing required `type:` routing-key field at `code:lib/foundation/spec/template.go::TemplateNodeDef`) as a shorthand for `executor: <id>`; the resolver maps `kind:` to a pre-registered inproc `ExecutorEntry`. Declaring both `kind:` and `executor:` on the same node is a registration error; unknown `kind:` values are rejected the same way unknown executors are. The existing required `type:` field is unchanged and continues to act as the dispatch routing key per `concept:node`.

**Consumer changes:**

5. **`loop_counter` utility node** — the one utility node in this spec's scope. Implements `InProcessHandler`; input attribute `max`, executor-written carry-forward attribute `count`, emits named event `loop` while count < max and `done` named event on the final dispatch.

6. **claude-agent session_token as attribute.** Schema gains `session_token` as a `readOnly: true` property; the agent writes the CLI session token via `attributes_set` on every terminal; carry-forward makes it visible on next dispatch in scope; if non-empty, executor launches CLI with `--resume <token>`. Sub-graph invocation = new RunScope = empty `session_token` = fresh CLI conversation.

**How the pieces compose to deliver the stories:**

- STORY-attribute-carry-forward is delivered by (1).
- STORY-loop-counter-cap is delivered by (3) + (4) + (5), with (1) underneath.
- STORY-claude-agent-session-resume is delivered by (6), with (1) underneath.
- STORY-inproc-utility-executor is delivered by (3) + (4).
- STORY-opaque-executor-scratch is delivered by (2).

## Technical decisions

### Attribute carry-forward

**TD-attribute-carry-forward** — The pre-substitution hydration step.

- **Choice:** On each dispatch of node X in RunScope S, the runtime hydrates the attribute bag from the most-recent prior node_run of X in S (via a lookup joining the per-run attribute ledger — indexed by node_run_id with denormalized node_id — with the node_run rows that carry run_scope_id, selecting the most recent prior run for (node, scope)), then applies per-field `source:` substitution from this dispatch's wait-set senders, overwriting carry-forward values for source-bound properties only. First dispatch of X in S = the schema's static-default values. Executor-written `readOnly: true` properties carry forward unchanged unless overwritten by a subsequent executor writeback. Cross-RunScope hydration is forbidden — sub-graph and fan-out RunScopes start with no carry-forward source, schema defaults apply. The existing sub-graph sealing semantics carry over. Default is on for all attribute properties uniformly — no opt-in flag.
- **Rationale:** Stateful nodes (loop_counter's count, claude-agent's session_token, and other executors that hold state in their own attributes) need their own prior writeback visible on the next dispatch. The denormalized index already exists for forensic lookups; making it load-bearing for hydration is the minimum change. Substitution overlay preserves cross-node data flow semantics. Scope-bounded persistence matches sub-graph sealing. Both modes — stateful via executor-written carry-forward, refresh-from-upstream via source-bound substitution — coexist naturally in the same hydration path; template authors pick per property. Default-on keeps the model uniform; the pre-v1 break-freely rule (per `code:.claude/rules/rules.md`) covers any edge case, and the invisibility worry is asymmetric (executors that had no prior absence to rely on can't have logic that breaks when prior values appear).
- **Alternatives:** Self-substitution grammar (`{{self.attribute.Y}}` source kind) — requires extending the closed substitution grammar, doesn't naturally cover executor-written readOnly properties. Shared bundle per (node, scope) mutated in place — loses per-run audit trail. Opt-in via schema flag — splits the property model and adds schema surface for a capability stateful nodes need by default.

### Scratch

**TD-scratch-column** — Persistence shape.

- **Choice:** Add `scratch_inline BYTEA`, `scratch_handle TEXT`, `scratch_handle_backend TEXT` to `rimsky_node_runs`, mirroring the existing `parked_payload_*` triple. Spill follows `concept:blob-backend` per the existing pattern. Default value is empty.
- **Rationale:** Reuses the inert-payload column pattern (parked_payload, named-event payloads); persistence-layer code stays uniform; the same blob_backend abstracts inline vs. spilled handle.

**TD-scratch-protocol** — Wire surface.

- **Choice:** Add `bytes scratch` to `proto:executor.proto::ExecuteRequest` (carries the row's current scratch on dispatch) and to each stream-close outcome variant (`Success`, `Error`, `Park`, `AwaitAsyncCallback` — per the four-variant oneof on `proto:executor.proto::StreamClose.outcome`). The async-callback body's outcome oneof variants (`success`, `error`, `park` on `proto:executor.proto::AsyncCallbackBody.outcome`) gain the same field per the existing pattern. For mid-dispatch checkpointing: add a new executor-protocol HTTP callback route `POST {callback_url}/v1/runs/{run_id}/scratch` mirroring the §12.5 attributes incremental-writeback pattern; the body is the opaque scratch bytes. Out-of-process executors POST directly; the claude-agent executor wraps the callback as an MCP tool (`scratch_set` on its internal MCP server, mirroring how it wraps the attributes callback as `attributes_set`); in-process executor handlers call the runtime-side writeback helper directly without going over the wire.
- **Rationale:** Symmetric across all stream-close outcomes means executors get a uniform "save state for next dispatch" mechanism regardless of how they exit. The HTTP callback route (with claude-agent's MCP wrapper for its CLI surface, and the runtime-helper direct call for in-process handlers) covers long-running executors that want to checkpoint without terminating; trivial for sync executors that can attach at stream-close. The inertness invariant (`@blessed-invariant 21` / `concept:inertness`) extends to scratch — rimsky never inspects.

**TD-scratch-recovery** — Behavior across re-dispatch.

- **Choice:** Per-dispatch row. Every enqueue path that creates a new dispatch row carrying `prior_dispatch_id` (per `proto:executor.proto::PriorDispatchDisposition` — `PRIOR_HEARTBEAT_STALE`, `PRIOR_RETRY_AFTER_ERROR`, `PRIOR_RECALCULATE`; not `PRIOR_NONE`) copies scratch from the prior dispatch's row to the new row at row creation. The next dispatch reads scratch from its (new) row via the normal `ExecuteRequest` hydration path. The four call sites are `code:lib/runtime/conductor.go::SweepStaleHeartbeats` (heartbeat-stale), `code:lib/runtime/cascade_recalculate.go` (recalculate), `code:lib/runtime/on_error.go` (retry-after-error), and `code:lib/runtime/runner_error_policy.go` (retry-after-error). Same shape as how `parked_payload` flows from a parked row's metadata into `ResumeContext` on the park-resume path.
- **Rationale:** STORY-opaque-executor-scratch's acceptance pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The enqueue path is the natural copy point since it already creates the new row and stamps `prior_dispatch_id`. No new sweep, no new column linkage beyond `prior_dispatch_id` (which already exists). Covers retry-after-error so a transient failure can recover its in-flight state.

**Park.payload coexistence:** `Park.payload` is unchanged by this spec. The new `bytes scratch` field is independent of `Park.payload` — both exist on the wire and on the node_run persistence side, addressing different surfaces. This spec does not touch the Park path.

### In-process executor transport

**TD-inproc-transport-client** — Third `Client` impl on the existing factory.

- **Choice:** Add `"inproc"` as a third case in `code:lib/runtime/executor/client.go::ClientPool::GetOrCreate`'s transport switch, alongside `"grpc"` and `"http"`. New constructor `NewInProcessClient(ep Endpoint, registry InProcessRegistry) (Client, error)` returns an `InProcessClient` whose `Execute` looks up the handler in the registry by `ep.URL` (the inproc executor identity, e.g. `inproc://loop_counter`). The pool keys by `(transport, tlsMode, URL)` as today — `tlsMode` is unused for inproc but slots cleanly into the key.
- **Rationale:** The existing `Client` interface and `ClientPool` factory were designed for multiple transports (gRPC and HTTP-bridge are the existing pair). Inproc is a third instance of the same pattern. The dispatch site at `code:lib/runtime/runner_dispatch.go#221` is unchanged — transport stays opaque to dispatch.

**TD-inproc-handler-interface** — What utility executors implement.

- **Choice:** A Go interface in `lib/runtime/executor/`:
  ```go
  type InProcessHandler interface {
      Execute(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink) error
  }
  type EventSink interface {
      Send(*genv1.ExecuteEvent) error
  }
  ```
  Handlers consume the request synchronously, emit events through the sink, return nil on success or an error the `InProcessClient` translates into an error terminal. Generated protobuf types are passed as DTOs at the function-call boundary — no wire encoding.
- **Rationale:** Shape-matched to the gRPC `Execute` server-streaming method but Go-idiomatic. Handlers stay simple and testable.
- **Alternatives:** Have handlers implement `genv1.ExecutorServer` directly. Heavier — drags gRPC-server streaming machinery into handlers that don't need it.

**TD-inproc-registry** — Handler registration at startup.

- **Choice:** An `InProcessRegistry` (`map[executor_id]InProcessHandler`) constructed explicitly at supervisor startup. The supervisor's setup path imports each builtin handler package, constructs handler instances, and inserts them by canonical executor identity. The registry is passed into the `ClientPool` factory. Bundled utility executors live under `lib/runtime/executor/builtin/<name>/`.
- **Rationale:** Explicit wiring is testable (the registry is constructible in tests with arbitrary handler sets) and the dependency graph stays visible — every utility handler the binary serves is an explicit import. Avoids init-time globals.
- **Alternatives:** Init-time self-registration via `init()` against a global registry. Simpler API for handler authors but introduces hidden ordering and makes test isolation harder.

**TD-inproc-eventstream** — Streaming back to the dispatch loop.

- **Choice:** Channel-backed `inprocEventStream` implementing `EventStream`. `InProcessClient.Execute` starts the handler on a goroutine that writes events to a buffered channel; `Recv()` reads from the channel; channel close signals stream end (returns `io.EOF`). The handler's return error is surfaced through the stream per existing gRPC parity.
- **Rationale:** Matches the gRPC streaming semantics the dispatch loop is built around. The supervisor's read loop doesn't have to distinguish transports. Sync utility executors (counter, loop) emit all their events synchronously and return — the channel drains quickly. Async-style ones emit events as they happen.
- **Alternatives:** Synchronous EventStream returning queued events from a slice. Avoids a goroutine but blocks the supervisor's read while the handler runs — incompatible with the streaming pattern the dispatch code assumes.

### Sugar resolver

**TD-kind-sugar-resolver** — `kind:` sugar at template registration.

- **Choice:** Add a new optional `kind: <name>` field on the template node definition (alongside the existing required `type:` routing-key field at `code:lib/foundation/spec/template.go::TemplateNodeDef`, which is unchanged). At registration, a resolver maps `kind:` to a pre-registered `ExecutorEntry` with `transport: inproc` and `URL: inproc://<name>`. The resolver consults a static kind-alias map populated alongside the `InProcessRegistry`. A node may declare `kind:` OR `executor:` but not both — mixing is a registration error. A node with neither `kind:` nor `executor:` falls through to the existing executor-resolution path (some nodes have no executor today, that path stays). Unknown `kind:` values are rejected at registration with the same error class as unknown executors.
- **Rationale:** Ergonomic shorthand — template authors writing `kind: loop_counter` skip the executor-identity vocabulary. Picked the `kind:` field name to avoid collision with the existing required `type:` routing-key field. Static map keeps registration deterministic; same authoring surface as the existing executor system.
- **Alternatives:** Auto-register utility executors as `ExecutorEntry` entries in the config layer; templates use the long form. Avoids a new template field but every utility-node reference is longer; the sugar form is a small schema change for a real ergonomic win. Overload the existing `type:` field with reserved utility-name values — would be magical and silently collide with template-author-chosen `type:` strings.

### Consumer changes

**TD-loop-counter-shape** — Schema and behavior of the loop_counter utility node.

- **Choice:** The `InProcessHandler` for `loop_counter` has:
  - Input attribute `max: integer` (required, no default, validated `> 0` at registration via JSON Schema).
  - Executor-written carry-forward attribute `count: integer, default: 0` (`readOnly: true`).
  - Declared named events: `loop`, `done`.
  - On every dispatch: read `count` from incoming attributes (carry-forward yields prior value, or `0` on first dispatch in scope); compute `new_count = count + 1`; emit `loop` named event if `new_count < max` else `done`; close the stream with the `Success` outcome carrying `attributes_delta: { count: new_count }` so the runtime persists the new count for next-dispatch carry-forward.
- **Rationale:** Minimum surface for "count up to N, emit on each step, emit a different event on the terminal step." Both events are observable from downstream subscriptions; the count attribute is visible to other nodes (and to the loop_counter itself across dispatches via carry-forward). Scope-bounded carry-forward makes the new-RunScope-resets-count behavior fall out naturally — no separate reset mechanism needed.

**TD-claude-agent-session-attribute** — Moving CLI session token to the attribute surface.

- **Choice:** claude-agent's `expected_attributes_schema` gains a property:
  - `session_token: { type: string, readOnly: true, default: "", description: "Claude Code CLI session token; carries forward in scope across dispatches; the agent passes --resume <token> on dispatch when non-empty." }`

  On dispatch: the executor reads `session_token` from incoming attributes; if non-empty, launches the CLI with `--resume <session_token>`. The CLI's session id for this dispatch is the per-dispatch `runId` (passed to the spawn via `--session-id` at `code:lib/services/executors/claude-agent/src/cli-runner.ts#260`; same value used as `sessionToken` on the Park path at `code:lib/services/executors/claude-agent/src/agent-run.ts#887`). On terminal, the agent writes that current dispatch's `runId` to the `session_token` attribute via `attributes_set({session_token: <runId>})`. The next dispatch in the same RunScope receives that value through carry-forward and resumes the prior CLI conversation. The existing `ResumeContext.session_token` plumbing on the Park path stays in place for the Park use case; the new attribute-driven path is independent and is what build/validate-style loops use.
- **Rationale:** Aligns claude-agent's session-resume with the carry-forward semantics; removes dependence on `ResumeContext` for the loop case; new RunScope (sub-graph invocation) = no carry-forward source = empty `session_token` = fresh CLI conversation — exactly the "fresh context per pass" behavior the orchestrator wants.

## Testing strategy

**Unit:**
- `code:lib/runtime/runner_dispatch.go::resolveAttributes` carry-forward step — latest-per-(node, scope) lookup; substitution overlay correctness; defaults on first dispatch in scope.
- `InProcessClient.Execute` — registry lookup, channel-backed EventStream sequencing, EOF on channel close.
- `loop_counter` handler — count increments, `loop` vs `done` emission boundary, max validation.
- `kind:` sugar resolver at template registration — kind → ExecutorEntry mapping, `kind:` + `executor:` mixed-declaration rejection, unknown-kind rejection.
- Scratch persistence — write at stream-close, write via the mid-dispatch HTTP callback route, read on next dispatch, inline + spill paths, copy-on-enqueue under each prior-dispatch disposition (heartbeat-stale, retry-after-error, recalculate).
- claude-agent `session_token` read/write + `--resume` passing.

**Scenario (under `test/scenarios/`):**
- STORY-attribute-carry-forward proof: stateful counter node, 3 dispatches in RunScope, sub-graph fresh defaults.
- STORY-loop-counter-cap proof: loop_counter max=3 with `loop` + `done` sinks.
- STORY-opaque-executor-scratch proof: scratch round-trip across a simulated prior-dispatch-linked enqueue, exercising both stream-close attach and mid-dispatch callback writes.
- STORY-inproc-utility-executor proof: minimal template using `kind: loop_counter` runs without external utility-executor config.

**Services integration (under `lib/services/test/`):**
- STORY-claude-agent-session-resume proof: claude-agent 3 dispatches in scope with CLI continuity, then sub-graph with fresh CLI.

**Race-sensitive paths:**
- `runner_dispatch.go` resolveAttributes with `-race -count=3` (concurrent dispatches to the same node in the same scope).
- `InProcessClient` channel-backed EventStream with `-race`.

**Conformance suite:**
- Add a scratch round-trip case to the executor conformance suite (any executor that attaches scratch at stream-close, or POSTs to the mid-dispatch scratch callback route, sees the scratch bytes on the next ExecuteRequest for the same node-lineage).

**`@source:` divergence note:**
- `code:lib/runtime/executor/client.go::ClientPool` has a tracked `@source:` copy at `code:lib/protocols/conformance/executor/client.go`. The conformance pool today supports `grpc` only and intentionally stays that way — conformance exercises external executor implementations, which never dispatch via `inproc`. The runtime pool's `inproc` extension is a tracked divergence (`@diverged: true`, `@reason: conformance dispatches only over the wire`).

## Error handling

- **Carry-forward source row exists but post-substitution bag fails schema validation** (schema was changed between runs): existing post-substitution validation gate catches it; dispatch fails with `attributes_schema_invalid` per existing behavior.
- **No prior node_run in scope** (first dispatch): hydrate with schema static-defaults. No error.
- **Scratch exceeds inline column limit:** spill to blob-backend per `concept:blob-backend`; same path as parked_payload's existing spill. No new error class.
- **Template references `kind: X` with no inproc handler registered:** rejected at template registration via the sugar resolver, same error class as unknown executors.
- **Mixed `kind:` and `executor:` on the same node:** rejected at template registration with `template_validation_failed`.
- **`InProcessHandler.Execute` returns error:** surfaced through the EventStream as an error terminal; standard `concept:error-policy` chain applies (same as for external executor errors).
- **`loop_counter` declared without `max`:** rejected at template registration via JSON Schema (max is required).
- **Mid-dispatch scratch callback failure** (persistence-layer error on the HTTP route, or the wrapping MCP tool for claude-agent, or the in-process helper for in-process executors): surfaces to the calling executor as a callback failure; the executor can retry or terminate.
- **claude-agent `session_token` non-empty but CLI `--resume` fails** (e.g., session expired): the executor surfaces the CLI's failure as an error terminal via the existing CLI-error path; the standard `concept:error-policy` chain applies. The agent does not attempt to clear and retry without `--resume` — the template handles the failure surface like any other dispatch error.

## Design changes

### Concept mutations

- **Mutate `concepts/attribute.md`** — Add a pre-substitution carry-forward step to the hydration model AND reconcile every existing invariant / non-goal that asserts the per-run rows are "not a cache" or that the denormalized `node_id` column is forensic-only. Specifically:

  1. Add a new section "Self-state carry-forward" after the invariants block: "On each dispatch of node X in RunScope S, the attribute bag is hydrated from the most-recent prior node_run of X in S (a JOIN of the per-run attribute ledger with the node_run rows, ordered by recency for (node, scope)), then per-field `source:` substitution overlays on top. First dispatch of X in S uses the schema's static-default values. Executor-written `readOnly: true` properties carry forward unchanged unless overwritten by a subsequent executor writeback. Cross-RunScope hydration is forbidden — sub-graph and fan-out RunScopes start with schema defaults (per `concept:run-scope`). Self-state carries via copy; cross-node values flow via substitution; the two channels are orthogonal and operate on different sources. The canonical stateful-property pattern is `readOnly: true` plus executor writeback; carry-forward is its expected behavior."

  2. Edit the "Cross-frame attribute caching" non-goal: keep the substitution-grammar rule (substitution reads of other nodes' attribute values stay frame-scoped) but replace the closing line — "State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs" — with: "Cross-node state across frames belongs in `params`, claim payloads, or threaded subgraph inputs. A node's own state across frames within a RunScope is the self-state carry-forward mechanism."

  3. Edit the storage invariant that today says "Attribute storage is per-run … A denormalized node-id column supports forensic / observability lookups by latest-per-node; the dispatch-time substitution path looks up by run against the wait-set sender runs that contributed to this dispatch in this frame." Replace with: "Attribute storage is per-run, keyed by the node-run identity (a cascade-deleting foreign key to the node-run row). A denormalized node-id column supports both forensic / observability lookups and the self-state carry-forward hydration step (latest prior run for this node in this RunScope). The dispatch-time substitution path looks up by run against the wait-set sender runs that contributed to this dispatch in this frame; the carry-forward hydration step looks up by (node-id, run-scope-id) for the same node's own prior writeback."

  4. Edit the substitution-scope invariant that today reads "Substitution reads are scoped to the current frame. A `{{nodes.X.attribute.Y}}` directive resolves to the X-run that contributed to this dispatch via the frame's wait-set; reads of X-runs from earlier frames return a missing-source error. The per-run attribute rows are the persistent record of what each node-run produced — not a cache. State that must be available across frames belongs in `params`, claim payloads, or threaded subgraph inputs." Keep the first two sentences as-is (the substitution-scope rule is unchanged). Replace the third and fourth sentences with: "The per-run attribute rows are the persistent record of what each node-run produced; the substitution path treats them as wait-set-gated per-frame reads. Self-state carry-forward is a separate hydration step that uses the same rows as the source for a node's own prior writeback. Cross-node state across frames belongs in `params`, claim payloads, or threaded subgraph inputs; a node's own state across frames within a RunScope rides carry-forward."

- **Mutate `concepts/executor.md`** — Reframe both "What it is" and "Purpose" to admit both implementation forms; add a "Scratch" section.

  1. Replace "What it is" with: "An executor implements the gRPC executor's server-streaming execute method plus an optional executor-observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool and out-of-process services (gRPC or HTTP-bridge) — and the protocol surface (execute, the four stream-close outcome variants, the observability handshake) is identical across both. The executor receives one execute request, streams zero-or-more heartbeat / named-event messages, and exactly one stream-close event carrying one of four outcome variants (success, error, park, await-async-callback). The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (per `concept:parked-state`). Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. The stub test-double executor and the bundled in-process loop-counter handler are the in-rimsky implementations."

  2. Replace "Purpose" with: "Executors are where actual work happens. Out-of-process gRPC executors give language-portability, scale-independence, and async-callback handoff for long-running work. In-process executors deliver utility-node primitives (counters, gates, simple computations) without the deploy / image / IPC overhead, sharing the same protocol surface so the dispatch path treats both forms uniformly."

  3. Edit "Owns" in the Boundaries section to add: per-dispatch executor-attached opaque scratch bytes (the executor sets scratch mid-dispatch via the scratch HTTP callback route or at stream-close by attaching scratch bytes to the outcome).

  4. Add a new section "Scratch" after "Purpose": "Every executor receives a scratch field on its execute request carrying the dispatch row's currently persisted scratch bytes (empty on first dispatch). The executor may write scratch in two ways — mid-dispatch by POSTing to a scratch HTTP callback route (paralleling the executor protocol's existing attributes incremental-writeback HTTP callback), or at stream-close by attaching scratch bytes to the outcome. Both writes persist on the dispatch row. The bytes are opaque to rimsky — the inertness invariant (`concept:inertness` / `@blessed-invariant 21`) extends to scratch — and scratch carries forward to subsequent dispatches of the same node via the recovery enqueue path that creates the new dispatch row (per `concept:node-run`)."

- **Mutate `concepts/node-run.md`** — Add to the documented row schema: `prior_dispatch_id` (already present in the persistence layer; surface it in the documented schema), `scratch_inline`, `scratch_handle`, `scratch_handle_backend` (new scratch triple mirroring the parked_payload triple). New text in the row-fields section: "A `prior_dispatch_id` nullable reference to a preceding dispatch row, set whenever a new dispatch is enqueued to follow a prior one (under any of the prior-dispatch dispositions — heartbeat-stale, retry-after-error, recalculate). Optional scratch fields — `scratch_inline`, `scratch_handle`, `scratch_handle_backend` — carry executor-attached opaque bytes per dispatch, with spill following `concept:blob-backend`. The executor sets scratch either at stream-close (by attaching scratch bytes to the outcome) or mid-dispatch (by POSTing to the scratch HTTP callback route, paralleling the executor protocol's existing attributes incremental-writeback HTTP callback); both writes persist on the dispatch row that received them. When a subsequent dispatch row is created for the same node and the new row carries a non-null `prior_dispatch_id`, the enqueue path copies scratch from the prior dispatch row onto the new row at row creation, and the executor reads it from its own row on next dispatch." Add to "Owns": executor-attached opaque scratch bytes per dispatch; the prior-dispatch linkage across re-dispatches of the same node.

- **Mutate `concepts/node.md`** — Add a new section "Kind sugar": "A template node may declare `kind: <name>` as a shorthand for an `executor:` reference. The required `type:` field (the template-author-chosen dispatch routing key) is unchanged and unrelated. At registration, a static kind-alias map resolves `kind:` to a pre-registered executor entry. A node may declare `kind:` or `executor:` but not both; mixing is rejected at registration. Unknown `kind:` values are rejected the same way unknown executors are. The sugar exists so utility nodes (counters, gates, and similar in-process executors) can be referenced without spelling out their executor identity."

- **Mutate `concepts/inertness.md`** — Add scratch to the carrier-streams list AND extend the sanctioned-read-sites enumeration. Specifically:

  1. Rewrite the main carrier list to include scratch: "Carrier streams the discipline governs: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, attribute values, named-event payloads, message payloads, scratch (per `concept:executor`), executor error payloads." (Scratch joins the existing list; the prior wording with "(seven)" plus the "Plus executor error payloads" addendum collapses into one enumeration so the count is no longer baked into prose.)

  2. Place scratch under byte-opaque inertness alongside claim scope, claim address, claim payload, and blob content. New byte-opaque list: "Applies to: claim scope (per `concept:claim-scope`), claim address, claim payload, blob content, scratch. Rimsky reads them only at substitution-leaf extraction or for transport into the executor's wire (per `@blessed-invariant 20` and `21`)."

  3. Extend the sanctioned-read-sites enumeration with one new entry: "Scratch wire-attach + row-persist + lineage-copy — on dispatch, rimsky reads the dispatch row's scratch bytes onto the executor's execute request; on stream-close, rimsky persists the executor-attached scratch bytes onto the dispatch row; on the mid-dispatch scratch callback route, rimsky persists the posted scratch bytes onto the dispatch row; on next-dispatch enqueue for the same node under any prior-dispatch disposition, the enqueue path copies scratch from the prior dispatch row onto the new dispatch row."

### New stories

- **Create `stories/attribute-carry-forward.md`** capturing STORY-attribute-carry-forward verbatim — role, capability, business value, Acceptance, Falsifier, Proof.
- **Create `stories/loop-counter-cap.md`** capturing STORY-loop-counter-cap verbatim.
- **Create `stories/claude-agent-session-resume.md`** capturing STORY-claude-agent-session-resume verbatim.
- **Create `stories/inproc-utility-executor.md`** capturing STORY-inproc-utility-executor verbatim.
- **Create `stories/opaque-executor-scratch.md`** capturing STORY-opaque-executor-scratch verbatim.

### New decisions

- **Create `decisions/attribute-carry-forward.md`** capturing TD-attribute-carry-forward: Choice, Rationale, Alternatives.
- **Create `decisions/scratch-column.md`** capturing TD-scratch-column: Choice, Rationale.
- **Create `decisions/scratch-protocol.md`** capturing TD-scratch-protocol: Choice, Rationale.
- **Create `decisions/scratch-recovery.md`** capturing TD-scratch-recovery: Choice, Rationale.
- **Create `decisions/inproc-transport-client.md`** capturing TD-inproc-transport-client: Choice, Rationale.
- **Create `decisions/inproc-handler-interface.md`** capturing TD-inproc-handler-interface: Choice, Rationale, Alternatives.
- **Create `decisions/inproc-registry.md`** capturing TD-inproc-registry: Choice, Rationale, Alternatives.
- **Create `decisions/inproc-eventstream.md`** capturing TD-inproc-eventstream: Choice, Rationale, Alternatives.
- **Create `decisions/kind-sugar-resolver.md`** capturing TD-kind-sugar-resolver: Choice, Rationale, Alternatives.
- **Create `decisions/loop-counter-shape.md`** capturing TD-loop-counter-shape: Choice, Rationale.
- **Create `decisions/claude-agent-session-attribute.md`** capturing TD-claude-agent-session-attribute: Choice, Rationale.

## Manifest

### Stories

- **STORY-attribute-carry-forward** — Stateful attribute carry-forward in scope; fresh in new RunScope (Proof: demo).
- **STORY-loop-counter-cap** — Bounded iteration via `loop_counter` node type (Proof: demo).
- **STORY-claude-agent-session-resume** — CLI session continues in scope, fresh in new RunScope (Proof: demo).
- **STORY-inproc-utility-executor** — Utility node types dispatched without external service deployment (Proof: example).
- **STORY-opaque-executor-scratch** — Opaque bytes carried across recovery re-dispatch of the same row (Proof: proof).

### Technical decisions

- **TD-attribute-carry-forward** — Pre-substitution carry-forward step in attribute hydration.
- **TD-scratch-column** — Node_run scratch persistence column triple.
- **TD-scratch-protocol** — `bytes scratch` on ExecuteRequest + stream-close outcomes + scratch HTTP callback route.
- **TD-scratch-recovery** — Per-row scratch survives stale-heartbeat recovery.
- **TD-inproc-transport-client** — Third `Client` impl on `ClientPool`.
- **TD-inproc-handler-interface** — `InProcessHandler` Go interface.
- **TD-inproc-registry** — Explicit handler registry at supervisor startup.
- **TD-inproc-eventstream** — Channel-backed inproc `EventStream`.
- **TD-kind-sugar-resolver** — `kind:` sugar at template registration.
- **TD-loop-counter-shape** — `loop_counter` attribute schema and behavior.
- **TD-claude-agent-session-attribute** — `session_token` as carry-forward attribute on claude-agent.

### Design changes

- Concept mutations: 5 (`attribute`, `executor`, `node-run`, `node`, `inertness`).
- New stories: 5 (matching User outcomes).
- New decisions: 11 (matching Technical decisions).
- No tensions opened or resolved by this spec.
