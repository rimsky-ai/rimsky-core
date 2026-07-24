# Quality-of-life features — design spec

**Date:** 2026-05-28
**Status:** draft
**Module(s):** `lib/control/controlapi`, `lib/graph/node`, `lib/runtime`, `cmd/rimsky/cli`, `lib/services/executors/claude-agent`

## Overview

A consolidated set of operator/author quality-of-life features triaged from two third-party consumer reports, plus a gap-fill on the claude-agent executor's protocol coverage. The bug-grade items from those reports already shipped (commit `3ebe87a`); this spec covers the feature-grade work.

Five features:

1. **Template lint** — validate a template spec without persisting (`POST /templates/validate` + `rimsky template lint <file>`).
2. **Instance terminate** — a production path to terminate a (possibly stuck) instance (`POST /instances/{idOrKey}/terminate` + `rimsky instance kill`).
3. **Breakpoint-hits REST route** — `GET /instances/{idOrKey}/breakpoint-hits`, the read primitive that `status`/`watch` consume.
4. **Instance status + watch** — client-side CLI aggregators over existing read endpoints (`rimsky instance status <id>`, `rimsky watch <id>`).
5. **claude-agent named-event emission** — an `emit_named_event` MCP tool + a `RIMSKY_EXECUTOR_DECLARED_EVENTS` env override.

### Design tenet for Feature 5 (the executor-coverage gap)

The claude-agent executor participates as a **fully-rigged executor implementor, never a privileged client of rimsky**. Everything the agent needs arrives as *inputs* through attribute substitution (the template author wires those); everything it produces leaves as *outputs* the executor protocol already defines. The agent-facing MCP surface adds **no capability beyond the protocol** and grants **no read/write access to rimsky's runtime state**.

The executor wire protocol already carries non-terminal `NamedEvent` emissions; claude-agent simply never exposed a tool to emit one. Feature 5 closes that single output-side gap. It explicitly does **not** add run-scope/run-tree state exposure (`run_scope_id`, `parent_run_id`) — that would be rimsky-state access; a fan-out child that needs its partition already gets it through the input side via the existing `{{child.partition_key}}` substitution source. It also does not add an `AwaitAsyncCallback` tool — `executor.proto:304-305` forbids chaining that outcome, and the agent already auto-emits it at dispatch.

## Scope

**In:** Features 1–5 above and the design-doc reconciliations under `## Design changes`.

**Out (explicitly deferred):**
- `drift_summary` (category-grouped validation errors). Requires a stable error-code taxonomy on the static validator first (today `node.ValidationError` is free-text `{Path, Msg}`) — a separate, meatier effort. Its own spec when wanted.
- `AwaitAsyncCallback` MCP tool (protocol forbids it).
- Run-scope / run-tree state exposure to the agent (violates the Feature 5 tenet).
- Any wire-protocol change to `executor.proto` (Feature 5 uses fields that already exist).

---

## Feature 1 — Template lint (`POST /templates/validate` + `rimsky template lint`)

### Problem

Template authors iterating a YAML against an evolving executor `expected_attributes_schema` currently round-trip through `POST /templates` (register), which persists a row on success. There is no "validate only" affordance. (Note: the prior report's claim that registration "bails on the first error" is **false** — `node.ValidateTemplate` accumulates all errors and the handler returns the full `validation_errors` array. The real gap is purely validate-without-persist as a first-class, read-shaped operation.)

### Design

**New control-api action + route.**
- Action `template:validate`, `IsWrite: false`, route `POST /templates/validate`, optional MCP tool `template_validate`. Added as an `ActionEntry` in `v1Actions` (`lib/control/controlapi/actions.go:200`) and registered in `registerTemplatesRoutes` (`lib/control/controlapi/templates.go:82`) as `r.Post("/templates/validate", gate(deps, "template:validate", handleValidateTemplate(deps)))`.
- chi resolves the static segment `/templates/validate` ahead of the `/templates/{id}` param route, so no ordering conflict; the `Route.Path` in `actions.go` is the literal `"/templates/validate"`.

**Why a dedicated endpoint, not the existing dry-run path.** A `mode: dry_run` grant on `template:register` already runs validation and returns `would_have_registered` without persisting (`templates.go:261-268`). But that is gated by *write* permission and a per-grant mode. A read-shaped `template:validate` action lets an author who can lint hold a narrower grant than register, and `IsWrite: false` means the dry-run `mode` modifier is correctly ignored (`concept:dry-run` invariant: read actions ignore mode). So validate is **orthogonal** to `concept:dry-run`, not an extension of it.

**Handler `handleValidateTemplate(deps AppDeps)`** mirrors the validation portion of `handleDeployTemplate` (`templates.go:170`) and stops before any persistence:
1. Read + decode the request body to a `node.TemplateSpec`, reusing the same `{spec, tag?, source?}` decode shape (`decodeRegisterRequest`, `templates.go:789`). `tag`/`source` are accepted-but-ignored for symmetry.
2. `node.ApplyFrameResolutionDefaults(&spec)` then `res := node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))` — reuses `validatorHooksFor` (`templates.go:111`), so the executor `expected_attributes_schema` cross-check runs against the live discovery cache via `deps.ExecutorCapabilities` for **every** executor the spec references (late-bind names bypass, same as register). This is why no `--against-executor` flag is needed — the server cross-checks all referenced executors automatically.
3. Run the Validation-protocol RPC pipeline `runtime.RunValidationPipeline` (`templates.go:235`) so service-side `validate` checks surface too — same as register.
4. Return **HTTP 200** with `{"ok": <bool>, "validation_errors": [{"path","msg"}...], "validation_warnings": [...]}`. Validation *ran* successfully even when it found problems — 200-with-findings is the lint semantic (this diverges deliberately from register's 400-on-failure). Non-2xx is reserved for request-level errors (malformed body → 400, auth → 401/403).
5. **No** `Persist.Templates().Insert`, no hashing-for-persistence side effects, no fan-out.

**CLI `rimsky template lint <file>`.**
- `RunTemplateLint(ctx, args)` in `cmd/rimsky/cli/templates.go`, dispatched from a new `case "lint":` in `dispatchTemplate` (`cmd/rimsky/main.go:83`), with the two usage strings updated.
- Reuses `readSpecFile` (`templates.go:85`) verbatim (so `{source_file: ...}` refs resolve identically to register).
- New typed client method `Client.ValidateTemplate(ctx, RegisterTemplateRequest{Spec: spec}) (*ValidateResult, error)` in `cmd/rimsky/cli/client.go`, POSTing to `/templates/validate`.
- Prints `validation_warnings` then `validation_errors` (path + msg), human or `-o json`. Exit codes: `0` clean, `1` validation drift found (`ok:false`), `2` usage/local error (file not found, decode failure). Note the `1` here follows the linter convention (non-zero exit = findings, so scripts can gate on it) — a deliberate extension of the general CLI `1 = runtime/control-api error` convention (`templates.go` header), since `lint` reaching the server and getting a clean validation *run* that reports drift is not itself a runtime error.

### Error handling
- Malformed spec file / YAML → exit 2, local message.
- Unreachable executor in the discovery cache → the same `permissive_warn`/`strict` behavior register already applies (`concept:validation`); surfaced as warnings unless `strict`.

---

## Feature 2 — Instance terminate (`POST /instances/{idOrKey}/terminate` + `rimsky instance kill`)

### Problem

`DELETE /instances/{idOrKey}` refuses any instance whose `terminated_at` is nil with 409 `"instance is not in terminal state; wait for terminated_at to be set"` (`lib/control/controlapi/instances.go:634-639`). But **nothing in production sets `terminated_at`** — `InstanceTable.MarkTerminated` (`lib/foundation/persistence/instances.go:84`) is called only from tests. So today an instance can never be terminated or removed, and a node stuck in `running` awaiting an async callback that never arrives (the `transient/await_async` path, `lib/runtime/runner_dispatch.go:243-281`, where the node stays `running` indefinitely) has no cleanup path short of DB surgery.

`terminate` is therefore the **first production instance-teardown path**, not merely a stuck-instance fix.

### Design

**Relationship to DELETE (decision (a)).** `terminate` makes an instance *terminal*; `DELETE` remains the *reaper* that removes the row and frees `instance_key`. The existing `instance_terminator` worker (`lib/control/controlapi/instance_terminator.go`) already settles terminated-but-undeleted rows (closes the main run-scope, fires `EventInstanceTerminated` to lifecycle subscribers). So `terminate` does the minimum the worker can't: force-fail in-flight work and set `terminated_at`. `terminate` does **not** delete the row or free the key.

**New control-api action + route.**
- Action `instance:kill` (the string `instance:terminate` is already bound to `DELETE` with MCP tool `instance_terminate` — reusing it would collide in `ActionRegistry.Register` and create an MCP route ambiguity). `IsWrite: true`, route `POST /instances/{idOrKey}/terminate`, MCP tool `instance_kill`. Added to `v1Actions` and registered in `registerInstancesRoutes` (`instances.go:183`).

**Handler `handleTerminateInstance(deps AppDeps)`:**
1. Resolve the instance via `resolveInstance(...)` (`nodes.go:338`); 404 if absent.
2. If `inst.TerminatedAt != nil` → idempotent **200** returning the current (already-terminal) instance item; no further effect.
3. **Dry-run branch.** `instance:kill` is a write action, so a `{action: instance:kill, mode: dry_run}` grant must short-circuit via `WriteDryRunResponse(w, req, "would_have_terminated", {...})` (`dryrun.go:70`) — listing the in-flight node-runs that *would* be force-failed — and mutate nothing. This keeps `instance:kill` consistent with `concept:dry-run`'s stated ownership of a per-handler dry-run branch on every write action (rather than being a silent exception like the auth mutations).
4. Otherwise, in one `deps.Persist.Transaction`:
   a. **Force-fail in-flight work.** For every node-run of the instance in a non-terminal state (`running`, including the await_async-stuck case; also `stale`/`parked`/`fresh`), transition it to `failed` via `Nodes().UpdateState(..., cascade.NodeStateFailed, ReasonInstanceKilled, ...)` and abandon its in-flight (uncommitted) claims. `ReasonInstanceKilled` is a new closed-enum transition reason (see `## Design changes`) — no existing reason drives a forced `running → failed` teardown (`ReasonHandlerError` is a rejected sentinel; `ReasonPolicyGiveUp` is policy-chain-driven; `ReasonOperatorReset/Invalidate` are not terminal-failure). **`UpdateState` validates every transition through `cascade.NextState(current, reason)` (`lib/foundation/persistence/nodes.go`), so the closed `NextState` switch (`lib/foundation/cascade/state.go`) must gain `instance_killed → NodeStateFailed` arms for each non-terminal current state (`running`, `stale`, `parked`, `fresh`); without those arms the transition returns the illegal-transition sentinel and force-fail silently fails.**
   b. **Mark terminal.** `Instances().MarkTerminated(ctx, inst.ID, tx)` (the idempotent `terminated_at = now() WHERE terminated_at IS NULL` UPDATE).
   c. **Record the reason.** Append an event-log row with the **administrative (non-signal) audit kind `instance_terminated`** (underscore form — the slash form `*/*` is reserved for the `concept:signal` type-path taxonomy validated at registration; administrative audit kinds use the underscore/dot form like `work_started`, `message_emitted`), payload `{"reason": <reason>}`, so the teardown cause is auditable. Reason comes from the request body `{reason}` (optional; default empty).
5. Return **200** with the now-terminal instance item.

The `instance_terminator` worker then closes the main run-scope and fires `EventInstanceTerminated`. Held-*durable* claim release (`runtime.ReleaseHeldDurableClaims`, `lib/runtime/instance_termination.go:59`) and `instance_key` freeing remain `DELETE`'s job (whose guard now passes for terminated instances).

**CLI `rimsky instance kill <id> --reason "<...>" --force`.**
- `RunInstanceKill(ctx, args)` in `cmd/rimsky/cli/instances.go`, dispatched from a new `case "kill":` in `dispatchInstance` (`cmd/rimsky/main.go:137`), usage strings updated.
- Flags: `--reason` (string, optional), and a confirmation gate. Termination is destructive (abandons in-flight work), so it refuses without explicit confirmation: require `--force` **or** the common `--yes` flag (`CommonFlags.Yes`, `cmd/rimsky/cli/flags.go:78`, currently declared-but-unread — this is its first consumer). Without confirmation → exit 2 with `"refusing to terminate without --force"`.
- New typed client method `Client.TerminateInstance(ctx, idOrKey string, reason string) (*Instance, error)` POSTing `{reason}` to `/instances/{idOrKey}/terminate`.
- To also free `instance_key`, the operator follows with `rimsky instance delete <id>` (which now succeeds). A future `kill --purge` that chains the delete is an easy addition but is out of scope here.

### Error handling
- Already-terminal instance → idempotent 200 (not an error).
- Claim-abandon failure during force-fail → WARN-logged, non-fatal (mirrors the best-effort discipline in `handleDeleteInstance`'s claim release); termination still completes (the row is marked terminal so the operator isn't wedged).

---

## Feature 3 — Breakpoint-hits REST route (`GET /instances/{idOrKey}/breakpoint-hits`)

### Problem

Pending breakpoint hits are reachable **only** through the MCP resource `rimsky://instances/{id}/breakpoint-hits` (`lib/control/controlapi/mcp_resources.go`). A typed REST CLI (Features 3/4) can't cleanly consume them. This route is the enabling primitive.

### Design

- Route `GET /instances/{idOrKey}/breakpoint-hits`, registered in `registerBreakpointsRoutes` (`lib/control/controlapi/breakpoints.go:44`) under the **existing** `breakpoint:read` action — add the route to that `ActionEntry`'s `Routes` list (`actions.go:223`). Hits already gate on `breakpoint:read` in the MCP resource (`mcp_resources.go:74,127`), so no new action is minted. (Because this route's tool, if any, is read-shaped and the `breakpoint:read` action's existing tool `breakpoint_list` maps to `/breakpoints`, do **not** add an MCP tool for the hits route — leave it HTTP-only to avoid a second `breakpoint:read` GET-tool whose canonical-route selection would be ambiguous.)
- Handler `handleListBreakpointHits(deps)` mirrors `handleListBreakpoints` (`breakpoints.go:249`) for instance resolution + short-tx discipline, and the MCP resource for the data path: `deps.Persist.BreakpointHits().ListSinceForInstance(ctx, inst.ID, since, limit, tx)` (`lib/foundation/persistence/breakpoints.go:97`).
- Query params `?since=<seq>&limit=<n>` (default `since=0`, `limit=100`, max `500` — reuse the `resourceReadDefaultLimit`/`resourceReadMaxLimit` constants' values).
- Response shape mirrors the MCP resource exactly: `{"hits": [...], "next_since": <int64>, "truncated": <bool>}`, with each hit projected by the same logic as `hitToWireShape` (`mcp_resources.go:219`). The projection + limit constants should be shared between the MCP resource and this route rather than duplicated (extract to a small shared helper; the plan decides the location).

---

## Feature 4 — Instance status + watch (CLI aggregators)

Both are **client-side** aggregators over existing read endpoints — no new server endpoint beyond Feature 3. They share one assembly core (fan-out + per-section rendering).

### `rimsky instance status <id>` (single snapshot)
- `RunInstanceStatus(ctx, args)` in `cmd/rimsky/cli/instances.go`, dispatched from a new `case "status":` in `dispatchInstance`.
- Fans out (resolving a non-UUID key→UUID first via `GetInstance(...).UUID()`, the pattern already in `RunInstanceEvents`, `instances.go:264-271`):
  - `GetInstance(ctx, id)` → terminal flag (`terminated_at`), paused, template hash.
  - `ListInstanceNodes(ctx, id)` → per-node `state`, `current_error_class`, retry counter, last heartbeat.
  - `ListEvents(ctx, ListEventsQuery{InstanceID: <uuid>, Limit: N})` → recent activity.
  - New `ListBreakpointHits(ctx, id, since=0, limit=N)` → pending hits.
- Renders one combined view (human) or one JSON object `{instance, nodes, recent_events, breakpoint_hits}` (`-o json`). The `/nodes/{id}` "404 on a valid instance" the report cited is **not a bug** — that route takes a node UUID; per-instance node state is `GET /instances/{idOrKey}/nodes`, which `status` uses.

### `rimsky watch <id>` (live tail)
- `RunWatch(ctx, args)` as a **top-level** verb (a new `case "watch":` in the `cmd/rimsky/main.go:23` switch, beside the existing `logs`).
- Reuses the `RunInstanceEvents` poll loop (`instances.go:245`): a high-watermark cursor over `/events`, draining full pages without sleep, sleeping `--poll-interval` (default 1s) on partial pages, honoring `signal.NotifyContext`.
- Interleaves three poll sources into one chronological feed: events (high-watermark `lastSeenID`), breakpoint hits (`since`=last-seen seq), and the instance terminal flag (`GetInstance`). Prints `frame.start` / node terminations (with outcome) / `breakpoint.hit` / terminal lines.
- **Exits when the instance terminates** (`terminated_at` set).

### New client methods
- `Client.ListBreakpointHits(ctx, idOrKey string, since int64, limit int) (*BreakpointHitsResponse, error)` (`GET /instances/{idOrKey}/breakpoint-hits`). The first breakpoint-touching client method.

---

## Feature 5 — claude-agent named-event emission

Source under `lib/services/executors/claude-agent/`. See the design tenet in the Overview.

### Wire reality (ground truth)
- The executor protocol carries `NamedEvent` (`lib/protocols/proto/v1/executor.proto`), delivered either on the gRPC `ExecuteEvent` stream or in the async-callback body's `events` repeated field (`AsyncCallbackBody.events`, field 1). claude-agent closes its gRPC stream at dispatch (it emits `Heartbeat` + `StreamClose{AwaitAsyncCallback}` immediately, `server.ts:370-384`) and reports the real outcome via the HTTP callback. **Therefore emitted named events ride the async-callback body's `events[]` array, not the gRPC stream.**
- rimsky consumes that array with no runtime name gate: `lib/runtime/callback.go:456-462` maps each `events[]` entry to a `namedEventRecord`, and `lib/runtime/runner_named_events.go::persistOneNamedEvent` (`:66`) persists **any** name to `rimsky_node_events`. The only declared-events enforcement is **registration-time** in `lib/graph/node/template_validator.go` (the `ExecutorDeclaredEvents` hook, sites at `:250-268` and `:611-636`). `concept:named-event` invariant: "unknown event names at runtime are treated as no-ops." So an executor-side check at the MCP boundary is an early-feedback/UX choice, not a correctness requirement — and it grants no rimsky access.

### 5a. `emit_named_event` MCP tool
- New tool `emit_named_event(name: string, payload: object)` added to `TOOL_DEFINITIONS` (`src/internal-mcp-tools.ts`) and registered with a handler in `registerTools` (`src/internal-mcp-server.ts`), under the existing `rimsky-callback` MCP server (`CALLBACK_MCP_SERVER_NAME`).
- Handler behavior:
  1. **Declared-name guard (self-consistency, not rimsky access):** reject with a tool error if `name` is not in the executor's resolved declared-events list (see 5b). This is the sole runtime guard against an undeclared name (rimsky would otherwise persist it as a downstream no-op).
  2. **Inertness (`concept:inertness` §21 / `@blessed-invariant 21`):** serialize `payload` to JSON bytes and pass through **opaquely** — the tool does not log, format, validate-beyond-serialization, or transform the payload.
  3. **Buffer:** append `{name, payload-bytes}` to a per-dispatch event sink keyed by the dispatch token (in the per-run state threaded through `src/agent-run.ts` / `src/token-registry.ts`).
- Both `outcomeToCallbackBody` variants (`src/server.ts:498`, `src/http-bridge.ts:259`) populate the async-callback body's `events` field from the per-dispatch sink when the agent reaches a terminal outcome.

### 5b. `RIMSKY_EXECUTOR_DECLARED_EVENTS` env override
- Today `declaredEvents` is a hardcoded empty module-level `const` (`src/expected-attributes-schema.ts:97`), imported by `src/server.ts` and `src/observability.ts`.
- Read `RIMSKY_EXECUTOR_DECLARED_EVENTS` (comma-separated names; trimmed; empties dropped) at startup; the parsed list replaces the empty default. Env name uses the executor binary's `RIMSKY_EXECUTOR_*` namespace (consistent with `RIMSKY_EXECUTOR_PORT_GRPC` etc.), not `RIMSKY_AGENT_*`.
- The resolved list is advertised via `ObservabilityCapabilities.declared_events` (`executor_observability.proto:25`, field `declared_events = 7` at `:59`) in both the gRPC capabilities (`server.ts`) and the HTTP `capabilitiesPayload` (`observability.ts`), so rimsky's registration-time `subscribes:` cross-check sees a derivative image's event names without a source fork.
- Because `declaredEvents` is currently a module const imported directly, the plan threads the env-resolved value through (either a module-load-time parse in `expected-attributes-schema.ts` or through the server/capabilities config) — an implementation detail, not a contract change.

### Out
- No `read_node_attribute`/`read_node_event` runtime reads, no `post_message`, no template/instance lifecycle tools, no run-scope state surfacing — all would breach the tenet.

---

## Cross-cutting: action-registry discipline

Every new control-api route (Features 1, 2, 3) requires three coordinated edits, enforced by `ActionRegistry.Register` (panics on collision):
1. Route registration with `gate(deps, "<action>", handler)` in the relevant `registerXRoutes`.
2. An `ActionEntry` in `v1Actions` (`actions.go:200`) — the in-code comment notes updates "must be made here AND in the spec document."
3. An MCP tool name in `MCPTools` only when MCP exposure is intended (Feature 1 optional `template_validate`; Feature 2 `instance_kill`; Feature 3 HTTP-only).

Action strings must pass `auth.ValidateActionString`.

## Testing strategy

- **Feature 1:** handler test asserting (a) no template row is persisted, (b) the full `validation_errors` + `validation_warnings` are returned, (c) HTTP 200 even on drift, (d) the executor cross-check fires when the discovery cache has the schema. CLI `RunTemplateLint` test (clitest harness): exit 0 clean, exit 1 on drift, `{source_file:}` resolution. 
- **Feature 2:** handler/integration test (testcontainers): terminate sets `terminated_at`, force-fails a `running` node to `failed` with `ReasonInstanceKilled`, records the reason event, is idempotent on an already-terminal instance, and a subsequent `DELETE` succeeds (guard passes). A scenario test for the await_async-stuck case: a node parked in `running` on an async ack is freed by terminate. CLI `kill` confirmation-gate test (refuses without `--force`/`--yes`).
- **Feature 3:** handler test asserting the REST response matches the MCP resource's `{hits, next_since, truncated}` shape for the same instance; pagination via `since`/`limit`.
- **Feature 4:** clitest tests for `instance status` (combined JSON has all four sections) and `watch` (interleaves events + hits, exits on terminal). 
- **Feature 5:** claude-agent `npm test` — `emit_named_event` accepts a declared name, rejects an undeclared one, treats payload opaquely, and the event lands in the async-callback body's `events[]`; env-override parsing populates `declared_events` in both capability surfaces. Go-side: a `lib/runtime/callback.go` test that a callback body carrying `events[]` persists each to `rimsky_node_events` (the consumption path is shared regardless of emitter).
- **Full gates per `CLAUDE.md`:** `go build ./... && go test ./... && make lint`; scenario/storage changes run under testcontainers; claude-agent changes run `npm install && npm test && npm run build`.

## Design changes

These mutate the durable design docs and are applied by `execute-plan` alongside the code.

- **Concept: mutate `concepts/attribute.md`.** Reconcile the unified-attribute-surface invariant with the validator behavior shipped in `3ebe87a`. In the Invariants section, the rule "Each property satisfies one of: has `source:`, has `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema`" gains a **fourth** satisfying condition: *"or it is a property the executor does not enumerate, under an executor schema that does not constrain it — either the executor declares no `properties` block at all (a fully permissive schema), or it declares `additionalProperties` that is not `false`. In both cases the executor has delegated naming authority for unenumerated properties."* The adjacent invariant "L2 cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`" gains the same carve-out: an unenumerated property under such an open executor schema may be author-marked `readOnly: true`. (The no-`properties`-block permissive case predates this change in the code; the explicit-`additionalProperties` case is what `3ebe87a` added — the invariant text should describe both, since enumerated properties are still fully checked.) Append a Notes entry: `2026-05-28 — open-schema extension-property exemption clarified in the attribute-surface invariant per spec:2026-05-28-quality-of-life-features; an unenumerated property under an executor schema that does not constrain it (no properties block, or additionalProperties not false) is admitted without source/default/executor-readOnly and may be author-marked readOnly, while enumerated properties remain fully checked.`

- **Concept: mutate `concepts/instance.md`.** The concept is currently silent on termination/terminal state. Add an Invariant: *"An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, abandoning any in-flight node-runs (transitioning them to failed). Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal."* Append a Notes entry: `2026-05-28 — termination invariant added per spec:2026-05-28-quality-of-life-features; force-terminate is the first production path to mark an instance terminal, distinct from the row-delete reaper that frees the instance key.`

- **Concept: mutate `concepts/breakpoint.md`.** The Boundaries "Does NOT own" clause currently names only the MCP transport for hit delivery. Broaden it: hit *delivery* is owned by `concept:control-api`, which exposes **both** the MCP resource and a read-only REST route for hits; the breakpoint concept owns the ledger, not the transport. Append a Notes entry: `2026-05-28 — hit-delivery boundary broadened to include a REST read route alongside the MCP resource per spec:2026-05-28-quality-of-life-features.`

- **Concept: mutate `concepts/transition-reason.md`.** Add a new value to the closed enum: a forced-instance-teardown reason (`instance_killed`) that drives any non-terminal node state to failed, written by the force-terminate control path and accepted by the next-state function for each non-terminal current state. It is a **state-machine-validation-only** reason — it is **not** emitted as an audit-event kind; the teardown's auditable cause is recorded once by the administrative `instance_terminated` event-log row (per Feature 2), not per-node by the reason kind (the per-node state update writes run-row state only, no audit row). Note it is distinct from `policy_give_up` (policy-chain-driven) and the operator reset/invalidate reasons. Append a Notes entry: `2026-05-28 — instance_killed transition reason added per spec:2026-05-28-quality-of-life-features for forced instance teardown of in-flight node-runs; validation-only, not an audit kind.`

- **Concept: append a Notes entry to `concepts/template.md`.** `2026-05-28 — a validate-only control-api action (template:validate) now runs the full registration validation pipeline (static attribute-schema check + the validation-protocol RPC fan-out) without persisting, per spec:2026-05-28-quality-of-life-features; registration remains the only persisting entry point.`

- **Concept: mutate `concepts/dry-run.md`.** Add the new write action `instance:kill` to the exhaustive per-handler dry-run-branch enumeration in the Owns clause (alongside `instance:create`, `instance:terminate`, etc.) — Feature 2 gives `instance:kill` a dry-run branch returning a `would_have_terminated` envelope, so the enumeration must list it. Append a Notes entry: `2026-05-28 — instance:kill added to the dry-run-branch enumeration per spec:2026-05-28-quality-of-life-features; the force-terminate write action returns a would_have_terminated envelope under a dry_run grant.`

(No new concept is introduced and no tension is opened or resolved: Feature 5 fills an executor-coverage gap squarely within `concept:named-event` as already defined, and the `MarkTerminated`-never-called observation is an operational gap that Feature 2 closes rather than a standing design tension.)
