# Quality-of-life features — Implementation Plan

**Spec:** .ok-planner/specs/2026-05-28-quality-of-life-features-design.md
**Goal:** Add five operator/author quality-of-life features (template lint, instance force-terminate, breakpoint-hits REST route, instance status/watch CLI, claude-agent named-event emission) plus six durable concept-doc reconciliations.
**Architecture:** Three new control-api routes (`lib/control/controlapi`, go-chi + the `actions.go` registry), one new closed-enum transition reason (`lib/foundation/cascade`), CLI verbs + typed-client methods (`cmd/rimsky/cli`, stdlib `flag`), and a TypeScript MCP tool + env override in the claude-agent executor (`lib/services/executors/claude-agent`). Concept docs under `.ok-planner/design/concepts/` are edited last to match the shipped code.
**Tech Stack:** Go (chi, pgx/v5, modernc sqlite, slog), TypeScript (claude-agent), testcontainers-go for control-api/storage tests, npm/jest for the executor.

> **Environment note for the executor:** control-api and scenario tests spin up real Postgres via testcontainers — **Docker must be running** for every `go test ./lib/control/controlapi/...` and scenario invocation. The claude-agent pass needs `node`/`npm`.

> **Citations** in this plan are `path:line` against the tree at authoring time. Treat line numbers as "near here" anchors — find the named symbol if a line drifted.

---

## File-structure overview

| Area | Files touched (M = modify, N = new) |
|---|---|
| Template lint | M `lib/control/controlapi/actions.go`, M `lib/control/controlapi/templates.go`, M `cmd/rimsky/cli/client.go`, M `cmd/rimsky/cli/templates.go`, M `cmd/rimsky/main.go`, M/N `*_test.go` |
| Instance terminate | M `lib/foundation/cascade/state.go`, M `lib/control/controlapi/actions.go`, M `lib/control/controlapi/instances.go`, M `cmd/rimsky/cli/client.go`, M `cmd/rimsky/cli/instances.go`, M `cmd/rimsky/main.go`, M/N `*_test.go` |
| Breakpoint-hits route | M `lib/control/controlapi/actions.go`, M `lib/control/controlapi/breakpoints.go`, M/N `*_test.go` |
| status / watch | M `cmd/rimsky/cli/client.go`, M `cmd/rimsky/cli/instances.go`, M `cmd/rimsky/cli/run.go`, M `cmd/rimsky/main.go`, M/N `*_test.go` |
| claude-agent named events | M `lib/services/executors/claude-agent/src/{main.ts,expected-attributes-schema.ts,server.ts,http-bridge.ts,observability.ts,internal-mcp-tools.ts,internal-mcp-server.ts,token-registry.ts,agent-run.ts}`, M/N `*.test.ts` |
| Concept docs | M `.ok-planner/design/concepts/{attribute,instance,breakpoint,transition-reason,template,dry-run}.md` |

---

## Pass 1: Template lint (`POST /templates/validate` + `rimsky template lint`)

**Goal:** A read-shaped validate-only endpoint that runs the full registration validation pipeline without persisting, plus a `rimsky template lint <file>` CLI verb.
**Scope:** Tasks 1–6
**End state:** working
**Verification:** `go build ./... && go test ./lib/control/controlapi/... ./cmd/rimsky/... -count=1`

### Task 1: Register the `template:validate` action

**Files:** `lib/control/controlapi/actions.go`

The action registry is the `v1Actions` slice (starts `actions.go:200`); `ActionRegistry.Register` panics on duplicate action/route/tool, and the comment at `actions.go:180` requires the spec doc be updated in lock-step (already done — `spec:2026-05-28-quality-of-life-features`). The existing template block (`actions.go:242-261`) is the cousin.

**Steps:**
1. In `v1Actions`, immediately after the `template:read` entry, add:
   ```go
   {Action: "template:validate", IsWrite: false,
       Routes:      []Route{{"POST", "/templates/validate"}},
       MCPTools:    []string{"template_validate"},
       Description: "Validate a template spec without persisting; returns all validation findings."},
   ```
2. Confirm `auth.ValidateActionString("template:validate")` would pass (same `<noun>:<verb>` shape as siblings — no code change, just reasoning).
3. Update `lib/control/controlapi/actions_test.go`: `TestV1Registry` (~`:149`) errors on any registry action not in its `specTableActions`/`allowed` set. Add `"template:validate": true` to the `allowed` supplement map (~`:138-148`) so the new action doesn't trip the surplus-action guard.

**Verification:** `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/ -run TestV1Registry -count=1`

### Task 2: Add `handleValidateTemplate` + route

**Files:** `lib/control/controlapi/templates.go`

Mirror the validation portion of `handleDeployTemplate` (`templates.go:170-242`) but **stop before** `canonical.CanonicalSpecHash` / any `Persist.Templates().Insert`, and return 200-with-findings rather than 400.

**Steps:**
1. Add a handler `handleValidateTemplate(deps AppDeps) http.HandlerFunc` that:
   - `raw, err := readAllBody(req)` → `badRequest` on error.
   - `specBody, _, _, err := decodeRegisterRequest(raw)` (`templates.go:789`; tag/source accepted-but-ignored) → `badRequest` on error.
   - `spec := *specBody`; `res := node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))` (`templates.go:111` builds the hooks incl. the executor `expected_attributes_schema` cross-check via `deps.ExecutorCapabilities`).
   - `node.ApplyFrameResolutionDefaults(&spec)` then run the Validation-protocol pipeline exactly as `handleDeployTemplate` does (`templates.go:223-242`): build the `execSchemaLookup` closure from `deps.ExecutorCapabilities`, call `runtime.RunValidationPipeline(req.Context(), deps.Validators, spec, hash, deps.UnreachableValidatorPolicy, execSchemaLookup)`. NOTE: the pipeline takes the canonical `hash`; compute it with `canonical.CanonicalSpecHash(spec)` purely to feed the pipeline (validation input, not persistence) — on hash error, `writeError` and return.
   - Merge findings: static `res.Errors` (each `{path, msg}` via `e.Path`/`e.Msg`) plus the pipeline `outcome.Errors`/`outcome.Warnings`. Honor `?warnings_as_errors=true` the same way register does for the `ok` computation.
   - Write **HTTP 200** always (validation ran): `writeJSON(w, http.StatusOK, map[string]any{"ok": <len(allErrors)==0 && (!warningsAsErrors || len(warnings)==0)>, "validation_errors": <[{path,msg}...]>, "validation_warnings": <warnings>})`. Reserve non-2xx for request-level failures (bad body → `badRequest`).
2. In `registerTemplatesRoutes` (`templates.go:82`), add (place the static route so chi resolves it ahead of `/templates/{id}` — registering it alongside the others is fine; chi prefers the static segment):
   ```go
   r.Post("/templates/validate", gate(deps, "template:validate", handleValidateTemplate(deps)))
   ```

**Verification:** `go build ./lib/control/controlapi/...`

### Task 3: Handler test for validate-without-persist

**Files:** `lib/control/controlapi/templates_test.go` (or the existing template handler test file — match where `handleDeployTemplate` is tested)

**Steps:**
1. Write a test that POSTs a spec with a deliberate validation error to `/templates/validate` and asserts: HTTP 200, `ok:false`, `validation_errors` non-empty, and **no template row was persisted** (query `Persist.Templates()` count unchanged, or assert a follow-up `GET /templates/{hash}` 404s).
2. Write a test that POSTs a valid spec and asserts HTTP 200, `ok:true`, empty errors.
3. Run to verify both pass.

**Verification:** `go test ./lib/control/controlapi/ -run Validate -count=1`

### Task 4: Client `ValidateTemplate` method

**Files:** `cmd/rimsky/cli/client.go`

Cousin: `RegisterTemplate` (`client.go:231`, POSTs `/templates` with `RegisterTemplateRequest{Spec,Tag,Source}` at `client.go:184`).

**Steps:**
1. Add a `ValidateResult` type: `type ValidateResult struct { Ok bool \`json:"ok"\`; ValidationErrors []ValidationFinding \`json:"validation_errors"\`; ValidationWarnings []ValidationFinding \`json:"validation_warnings"\` }` where `ValidationFinding` is `{Path string \`json:"path"\`; Msg string \`json:"msg"\`}` (define it if no equivalent exists in the cli package).
2. Add `func (c *Client) ValidateTemplate(ctx context.Context, body RegisterTemplateRequest) (*ValidateResult, error)` POSTing to `/templates/validate`, mirroring `RegisterTemplate`'s request plumbing (it can pass `RegisterTemplateRequest{Spec: spec}` with empty tag/source).

**Verification:** `go build ./cmd/rimsky/...`

### Task 5: `rimsky template lint` CLI verb

**Files:** `cmd/rimsky/cli/templates.go`, `cmd/rimsky/main.go`

Cousin: `RunTemplateRegister` (`cli/templates.go:219`) — it uses `runWithCommon`, `readSpecFile` (`cli/templates.go:85`, resolves `{source_file:}` refs), `NewClient`, `SetAPIKey`, and prints findings.

**Steps:**
1. Add `RunTemplateLint(ctx context.Context, args []string) int`: `runWithCommon("template lint", args, nil)`; require exactly one positional `<file>` (else usage + exit 2); `spec, err := readSpecFile(rest[0])` (exit 2 on error); `c.ValidateTemplate(ctx, RegisterTemplateRequest{Spec: spec})` (exit 1 via `reportError` on transport error).
2. Print `validation_warnings` then `validation_errors` (path + msg) in human mode; `EmitJSON` the whole `ValidateResult` in `-o json`. Exit `0` if `res.Ok`, else `1` (linter convention: non-zero = findings; this deliberately extends the general `1 = runtime error` convention per the spec).
3. In `cmd/rimsky/main.go` `dispatchTemplate` (`main.go:83`), add `case "lint": return cli.RunTemplateLint(ctx, rest)`; update the two usage strings (`main.go:85` and `:104`) to include `lint`.

**Verification:** `go build ./cmd/rimsky/...`

### Task 6: CLI lint test

**Files:** `cmd/rimsky/cli/templates_test.go` (clitest harness — see existing `RunTemplateRegister` tests for the `setupClitest`/`clitest` pattern)

**Steps:**
1. Add a clitest case: lint a spec with drift → exit 1; lint a clean spec → exit 0; confirm `{source_file:}` resolution works (reuse a fixture if one exists).
2. Run to verify.

**Verification:** `go test ./cmd/rimsky/cli/ -run Lint -count=1`

---

## Pass 2: Instance terminate — cascade + server

**Goal:** A `POST /instances/{idOrKey}/terminate` endpoint that marks an instance terminal and force-fails its in-flight node-runs, with a new `instance_killed` transition reason the state machine accepts.
**Scope:** Tasks 7–10
**End state:** working
**Verification:** `go build ./... && go test ./lib/foundation/cascade/... ./lib/control/controlapi/... -count=1`

### Task 7: Add the `instance_killed` transition reason + NextState arms

**Files:** `lib/foundation/cascade/state.go`, `lib/foundation/cascade/state_test.go`

`NextState(current, reason)` (`state.go:184`) is the closed switch; `UpdateState` (in `lib/foundation/persistence/{postgres,sqlite}/nodes.go`) validates every transition through it and returns `ErrIllegalTransition` if the arm is missing. The reason constants are the `Reason* = TransitionReason{Kind: "..."}` block (`state.go:52-164`).

The force-terminate handler (Task 9) only transitions the instance's **resource-holding** node-runs — those in `running` (incl. the await_async-stuck case) or `parked` (a parked node retains its held claim across the park boundary, per the `NextState` comment at `state.go:219-221`). `fresh`/`stale` node-runs hold no claim and are not dispatched, and they have a nil `RunScopeID`, so they are NOT force-failed here (a terminated instance's pending nodes are inert). So `instance_killed` only needs `running → failed` and `parked → failed` arms.

**Steps:**
1. Add the constant near the others (the `Reason* = TransitionReason{Kind: ...}` block, `state.go:52-164`):
   ```go
   // ReasonInstanceKilled — forced instance teardown. Drives a
   // resource-holding non-terminal node-run (running | parked) → failed
   // when an operator force-terminates the instance. State-machine-
   // validation-only: NOT emitted as an audit-event kind (the teardown's
   // audit identity is the `instance_terminated` event-log row written by
   // the control handler).
   ReasonInstanceKilled = TransitionReason{Kind: "instance_killed"}
   ```
2. In `NextState`, add an `instance_killed → NodeStateFailed` arm to the `NodeStateRunning` case (`:212`) and the `NodeStateParked` case (`:243`):
   ```go
   if reason.Kind == "instance_killed" {
       return NodeStateFailed, nil
   }
   ```
   Do NOT add arms to `NodeStateFresh`/`NodeStateStale` (the handler never transitions those) or `NodeStateFailed` (already terminal — must stay rejected).
   Also update the `@blessed-invariant 1` docstring on `NextState` (`state.go:176-183`), which enumerates the legitimate `parked` transitions and claims "all other transitions involving parked ... are illegal": add `parked → failed under instance_killed` to that enumeration so the annotated docstring stays accurate (cold-read rule: annotated docstrings track the annotated code).
3. **Reconcile the existing exhaustive tests in `state_test.go`** (these are `allReasons`-driven and WILL break otherwise):
   - Add `ReasonInstanceKilled` to the `allReasons` slice (its docstring at `state_test.go:14` requires it to list every reason the machine knows).
   - In `TestTransitionTable`'s `valid[from][reason]` map, add the two legal entries: `running → instance_killed → NodeStateFailed` and `parked → instance_killed → NodeStateFailed`. (Every pair not in the map is asserted to return `ErrIllegalTransition`, so `fresh`/`stale`/`failed` + `instance_killed` are covered as illegal automatically.)
   - In `TestParkedToParkedRejected` (`:297`), add `instance_killed` to the exception list (alongside `handler_resume`/`park_timeout`) — `parked → instance_killed` is now legal.
4. Run to verify the table tests and the new arms pass.

**Verification:** `go test ./lib/foundation/cascade/... -count=1`

### Task 8: Register the `instance:kill` action

**Files:** `lib/control/controlapi/actions.go`

`instance:terminate` is already bound to `DELETE /instances/{idOrKey}` with tool `instance_terminate` (`actions.go:210-213`) — do **not** reuse it. Cousin block: the instance entries (`actions.go:202-221`).

**Steps:**
1. After the `instance:resume` entry, add:
   ```go
   {Action: "instance:kill", IsWrite: true,
       Routes:      []Route{{"POST", "/instances/{idOrKey}/terminate"}},
       MCPTools:    []string{"instance_kill"},
       Description: "Force-terminate an instance: mark it terminal and abandon in-flight node-runs."},
   ```
2. Update `lib/control/controlapi/actions_test.go`: add `"instance:kill": true` to the `allowed` supplement map in `TestV1Registry` (~`:138-148`), so the new action doesn't trip the surplus-action guard.

**Verification:** `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/ -run TestV1Registry -count=1`

### Task 9: Add `handleTerminateInstance` + route

**Files:** `lib/control/controlapi/instances.go`

Cousins in the same file: `handleDeleteInstance` (`:617` — the 409 terminal guard at `:634-639`, the `WriteDryRunResponse(w, req, "would_have_terminated", ...)` call at `:641`, the run-scope close + claim release + `FanOutInstanceEvent` patterns), `handleGetInstance` (`:587`, `toInstanceItem`), `resolveInstance` (`lib/control/controlapi/nodes.go:338`, returns `(*InstanceRow, error)`), `registerInstancesRoutes` (`:183`). `Instances().MarkTerminated(ctx, id, tx)` is the idempotent `terminated_at = now() WHERE terminated_at IS NULL` UPDATE (`lib/foundation/persistence/instances.go:84`).

**Exact API shapes to use (verified):**
- `Persist.Nodes().UpdateState` is **7 args**: `UpdateState(ctx, id, runScopeID, state, reason, settlingSignalType, tx)` (`lib/foundation/persistence/nodes.go:139`). A terminal `→ failed` transition passes the node-run's `RunScopeID` and a **non-nil** settling signal — see the give-up cousin `lib/runtime/on_error.go:296` (`sig := "terminal/error/" + class; UpdateState(ctx, id, runScopeID, NodeStateFailed, ReasonPolicyGiveUp, &sig, tx)`). `NodeRow.RunScopeID` is `*shared.UUID`.
- The event log accessor is `Persist.Events().Append(ctx, persistence.EventAppendInput{...}, tx)` — there is **no `Insert`** (`EventTable`, `lib/foundation/persistence/events.go:52`). `EventAppendInput` carries `InstanceID *shared.UUID`, `Kind string`, `Payload map[string]any` (cousin caller: `lib/runtime/runner.go:606`).
- Find the "list node-runs for an instance" accessor on the `NodeTable` interface (grep `lib/foundation/persistence/nodes.go`); if only a broad lister exists, filter in Go to the resource-holding states (`running`, `parked`).

**Steps:**
1. Add `handleTerminateInstance(deps AppDeps) http.HandlerFunc`:
   - Resolve: `inst := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))`; 404 (`notFoundResp(w, foundationshared.ErrInstanceNotFound.Error())`) if nil.
   - Idempotent: if `inst.TerminatedAt != nil` → `writeJSON(w, 200, toInstanceItem(*inst, redact))` and return.
   - Decode optional `{reason}` from the body (tolerate empty/no body — reuse the nil-safe decode idiom; do not nil-deref `req.Body`).
   - **Dry-run branch:** gather the would-be-failed node-runs (the instance's `running`/`parked` rows), then `if WriteDryRunResponse(w, req, "would_have_terminated", map[string]any{"instance_id": inst.ID.String(), "reason": reason, "would_fail_node_runs": <ids/count>}) { return }`. In execute mode `WriteDryRunResponse` returns false and writes nothing.
   - Transaction (`deps.Persist.Transaction`):
     a. **Force-fail resource-holding node-runs.** Select the instance's node-runs in `running` or `parked` (these hold/await a claim and carry a non-nil `RunScopeID`; `fresh`/`stale` are not dispatched and are left as-is). For each, build a settling signal `sig := "terminal/error/instance_killed"` and call `deps.Persist.Nodes().UpdateState(ctx, run.ID, *run.RunScopeID, cascade.NodeStateFailed, cascade.ReasonInstanceKilled, &sig, tx)`. Then abandon that node-run's in-flight (uncommitted) claim handles; abandon failures are WARN-logged, non-fatal. Guard the `*run.RunScopeID` deref — only the selected running/parked rows are dereferenced, and those always have a non-nil `RunScopeID`. **Do NOT call `runtime.ReleaseHeldDurableClaims` here** — per spec Feature 2, committed-durable-claim release and `instance_key` freeing stay `DELETE`'s job; terminate only abandons the uncommitted in-flight claims of the node-runs it force-fails. Find the uncommitted-claim accessor on `Persist.ClaimHandles()` (grep the interface for a per-node-run / per-instance claim lister filtered to a non-committed state).
     b. **Mark terminal.** `deps.Persist.Instances().MarkTerminated(ctx, inst.ID, tx)`.
     c. **Record reason.** `deps.Persist.Events().Append(ctx, persistence.EventAppendInput{InstanceID: &inst.ID, Kind: "instance_terminated", Payload: map[string]any{"reason": reason}}, tx)` — kind **`instance_terminated`** is the underscore administrative audit form (matching `work_started`/`message_emitted`); the slash form is reserved for `concept:signal` type-paths. (Confirm the exact `EventAppendInput` field set against `lib/foundation/persistence/events.go` and the cousin caller `lib/runtime/runner.go:606`.)
   - Re-fetch the instance and return `writeJSON(w, 200, toInstanceItem(updated, redact))`.
2. In `registerInstancesRoutes` (`:183`), add:
   ```go
   r.Post("/instances/{idOrKey}/terminate", gate(deps, "instance:kill", handleTerminateInstance(deps)))
   ```

**Verification:** `go build ./lib/control/controlapi/...`

### Task 10: Terminate handler test

**Files:** `lib/control/controlapi/instances_test.go` (testcontainers — Docker required)

**Steps:**
1. Test: create an instance, drive a node into a non-terminal state, `POST /instances/{id}/terminate {reason}` → assert 200, `terminated_at` set, the node-run is `failed`, an `instance_terminated` event-log row with the reason exists, and a subsequent `DELETE /instances/{id}` now succeeds (the 409 guard passes).
2. Test: calling terminate on an already-terminated instance is idempotent (200, no error).
3. Run to verify.

**Verification:** `go test ./lib/control/controlapi/ -run Terminate -count=1`

---

## Pass 3: Instance terminate — CLI

**Goal:** `rimsky instance kill <id> --reason "..." --force`.
**Scope:** Tasks 11–13
**End state:** working
**Verification:** `go build ./... && go test ./cmd/rimsky/... -count=1`

### Task 11: Client `TerminateInstance` method

**Files:** `cmd/rimsky/cli/client.go`

Cousin: `DeleteInstance` (`client.go:541`, `DELETE /instances/{idOrKey}`), `GetInstance` (`:528`).

**Steps:**
1. Add `func (c *Client) TerminateInstance(ctx context.Context, idOrKey string, reason string) (*Instance, error)` POSTing `{"reason": reason}` to `/instances/{idOrKey}/terminate`, decoding the returned instance item into `*Instance` (`client.go:445`).

**Verification:** `go build ./cmd/rimsky/...`

### Task 12: `rimsky instance kill` verb + confirmation gate

**Files:** `cmd/rimsky/cli/instances.go`, `cmd/rimsky/main.go`

Cousins: `RunInstanceDelete` (`cli/instances.go:194`), `RunInstanceGet` (`:159`). `CommonFlags.Yes` (`cli/flags.go:78`, the `--yes` flag) is currently declared-but-unread — this is its first consumer.

**Steps:**
1. Add `RunInstanceKill(ctx, args) int`: `runWithCommon("instance kill", args, func(fs){ fs.String("reason", "", "...") ; fs.Bool("force", false, "confirm forced termination") })`; require one positional `<id>`.
2. **Confirmation gate:** require `--force` OR `common.Yes` (`--yes`); if neither, print `refusing to terminate without --force` to stderr and return 2.
3. `c.TerminateInstance(ctx, rest[0], reason)`; on error `reportError`; on success print the terminal instance (KV or JSON). Note in the human output that `instance delete` is the follow-up to free the instance key.
4. In `dispatchInstance` (`main.go:137`), add `case "kill": return cli.RunInstanceKill(ctx, rest)`; update **all three** usage surfaces that enumerate instance subcommands: the `dispatchInstance` usage string (`main.go:139`), the `help` case usage (`main.go:158`), and the top-level `printRootUsage` instance line (`main.go:358`).

**Verification:** `go build ./cmd/rimsky/...`

### Task 13: CLI kill test

**Files:** `cmd/rimsky/cli/instances_test.go`

**Steps:**
1. clitest case: `kill` without `--force`/`--yes` → exit 2 (refused); `kill <id> --force` against a created instance → exit 0 and the instance is terminal.
2. Run to verify.

**Verification:** `go test ./cmd/rimsky/cli/ -run Kill -count=1`

---

## Pass 4: Breakpoint-hits REST route (`GET /instances/{idOrKey}/breakpoint-hits`)

**Goal:** A read-only REST route surfacing pending breakpoint hits, the primitive `status`/`watch` consume.
**Scope:** Tasks 14–15
**End state:** working
**Verification:** `go build ./... && go test ./lib/control/controlapi/... -count=1`

### Task 14: Add `handleListBreakpointHits` + route under `breakpoint:read`

**Files:** `lib/control/controlapi/breakpoints.go`, `lib/control/controlapi/actions.go`

The MCP resource handler and these helpers all live in **the same package** (`controlapi`), so reuse them directly — no extraction needed: `hitToWireShape(h persistence.BreakpointHitRow) map[string]any` (`mcp_resources.go:219`), `parseSinceLimit(q url.Values)` (`mcp_resources.go:320`), the consts `resourceReadDefaultLimit`=100 / `resourceReadMaxLimit`=500 (`mcp_resources.go:40-41`), and `Persist.BreakpointHits().ListSinceForInstance(ctx, instanceID, sinceSeq, limit, tx)` (`lib/foundation/persistence/breakpoints.go:97`). Cousin handler: `handleListBreakpoints` (`breakpoints.go:249`).

**Steps:**
1. In `actions.go`, add `{"GET", "/instances/{idOrKey}/breakpoint-hits"}` to the **existing** `breakpoint:read` ActionEntry's `Routes` list (`actions.go:224`). Do **not** add a new action and do **not** add a new MCP tool (HTTP-only — avoids a second `breakpoint:read` GET tool whose canonical-route selection would be ambiguous).
2. In `breakpoints.go`, add `handleListBreakpointHits(deps AppDeps) http.HandlerFunc`: resolve the instance (`resolveInstance`, 404 if nil); `since, limit, mcpErr := parseSinceLimit(req.URL.Query())` (map any parse error to `badRequest`); fetch `limit+1` rows via `ListSinceForInstance` to compute `truncated`; project each kept row with `hitToWireShape`; `writeJSON(w, 200, map[string]any{"hits": <projected>, "next_since": <max seq>, "truncated": <bool>})` — the exact shape the MCP resource returns (`mcp_resources.go:201-203`).
3. In `registerBreakpointsRoutes` (`breakpoints.go:44`), add `r.Get("/instances/{idOrKey}/breakpoint-hits", gate(deps, "breakpoint:read", handleListBreakpointHits(deps)))`.

**Verification:** `go build ./lib/control/controlapi/...`

### Task 15: Breakpoint-hits route test

**Files:** `lib/control/controlapi/breakpoints_test.go` (testcontainers — Docker required)

**Steps:**
1. Test: with an instance that has breakpoint hits, `GET /instances/{id}/breakpoint-hits` returns the same `{hits, next_since, truncated}` shape the MCP resource produces for that instance; assert `since`/`limit` pagination behaves (fetch-+1 truncation).
2. Run to verify.

**Verification:** `go test ./lib/control/controlapi/ -run BreakpointHits -count=1`

---

## Pass 5: Instance status + watch (CLI aggregators)

**Goal:** `rimsky instance status <id>` (one-shot snapshot) and `rimsky watch <id>` (live tail), both client-side over existing reads + the new hits route.
**Scope:** Tasks 16–19
**End state:** working
**Verification:** `go build ./... && go test ./cmd/rimsky/... -count=1`

### Task 16: Client `ListBreakpointHits` method

**Files:** `cmd/rimsky/cli/client.go`

Cousin: `ListInstanceNodes` (`client.go:550`), `ListEvents` (`:689`).

**Steps:**
1. Add `type BreakpointHitsResponse struct { Hits []map[string]any \`json:"hits"\`; NextSince int64 \`json:"next_since"\`; Truncated bool \`json:"truncated"\` }`.
2. Add `func (c *Client) ListBreakpointHits(ctx context.Context, idOrKey string, since int64, limit int) (*BreakpointHitsResponse, error)` → `GET /instances/{idOrKey}/breakpoint-hits?since=&limit=`.

**Verification:** `go build ./cmd/rimsky/...`

### Task 17: `rimsky instance status` verb

**Files:** `cmd/rimsky/cli/instances.go`, `cmd/rimsky/main.go`

Cousins: `RunInstanceGet` (`:159`), `RunInstanceNodes` (`:215`), `RunInstanceEvents` key→UUID resolution (`:264-271`: `if !LooksLikeUUID(id) { c.GetInstance → inst.UUID() }`).

**Steps:**
1. Add `RunInstanceStatus(ctx, args) int`: resolve key→UUID, then fan out `GetInstance`, `ListInstanceNodes`, `ListEvents(ListEventsQuery{InstanceID: <uuid>, Limit: N})`, `ListBreakpointHits(<uuid>, 0, N)`. Assemble into one struct.
2. Human mode: print instance terminal/paused/template + a per-node state table + recent events + pending hits. `-o json`: `EmitJSON({instance, nodes, recent_events, breakpoint_hits})`.
3. Factor the fan-out+assembly into a small helper (`gatherInstanceStatus(ctx, c, uuid) (..., error)`) so `watch` (Task 18) reuses it.
4. In `dispatchInstance` (`main.go:137`), add `case "status": return cli.RunInstanceStatus(ctx, rest)`; update the same three usage surfaces as Task 12 (`dispatchInstance` usage `main.go:139`, the `help` case `main.go:158`, and `printRootUsage` `main.go:358`).

**Verification:** `go build ./cmd/rimsky/...`

### Task 18: `rimsky watch` top-level verb

**Files:** `cmd/rimsky/cli/run.go` (or a new `cli/watch.go`), `cmd/rimsky/main.go`

Cousins: `RunLogs` (`cli/run.go:67`, top-level alias of `instance events --follow`), `RunInstanceEvents` poll loop (`cli/instances.go:245` — `lastSeenID` high-watermark, full-page drain, `--poll-interval` sleep, `signal.NotifyContext`).

**Steps:**
1. Add `RunWatch(ctx, args) int`: resolve key→UUID; run a poll loop interleaving three sources into one chronological feed — events (high-watermark `lastSeenID` over `ListEvents`, reusing the `RunInstanceEvents` cursor pattern), breakpoint hits (`ListBreakpointHits` with a since-seq watermark), and the instance terminal flag (`GetInstance`). Print `frame.start` / node-termination / `breakpoint.hit` / terminal lines. **Exit when `terminated_at` is set.** Honor `--poll-interval` and `signal.NotifyContext(ctx, os.Interrupt)`.
2. In `cmd/rimsky/main.go`, add a top-level `case "watch": return cli.RunWatch(ctx, os.Args[2:])` in the main switch (`main.go:23`, beside the existing `logs` case at `:70`); add it to the top-level usage.

**Verification:** `go build ./cmd/rimsky/...`

### Task 19: status + watch CLI tests

**Files:** `cmd/rimsky/cli/instances_test.go` (and/or `run_test.go`)

**Steps:**
1. clitest: `instance status <id>` against a created instance returns exit 0 and `-o json` contains all four sections (instance, nodes, recent_events, breakpoint_hits).
2. clitest: `watch <id>` exits 0 promptly when the instance is already terminal (terminate it first, or use a short poll-interval + an instance that terminates). Keep the test deterministic and bounded (small `--poll-interval`, terminal instance).
3. Run to verify.

**Verification:** `go test ./cmd/rimsky/cli/ -run 'Status|Watch' -count=1`

---

## Pass 6: claude-agent named-event emission

**Goal:** An `emit_named_event(name, payload)` MCP tool whose events ride the async-callback `events[]` array, plus a `RIMSKY_EXECUTOR_DECLARED_EVENTS` env override that populates `ObservabilityCapabilities.declared_events`.
**Scope:** Tasks 20–23
**End state:** working
**Verification:** `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`

> Design tenet (spec §"Design tenet for Feature 5"): the agent is a fully-rigged executor implementor, never a privileged rimsky client. The declared-name check is self-consistency, not rimsky access. Payloads are **inert** (`concept:inertness` §21 / `@blessed-invariant 21`): the tool serializes the payload to bytes and passes it through opaquely — it never logs, formats, or transforms it.

### Task 20: `RIMSKY_EXECUTOR_DECLARED_EVENTS` env override

**Files:** `lib/services/executors/claude-agent/src/expected-attributes-schema.ts`, `src/main.ts`, `src/server.ts`, `src/observability.ts`

Today `declaredEvents` is a hardcoded empty `const` (`expected-attributes-schema.ts:97`) imported directly by `server.ts` and `observability.ts`. Existing env vars use the `RIMSKY_EXECUTOR_*` namespace (`main.ts`, e.g. `RIMSKY_EXECUTOR_PORT_GRPC`).

**Steps:**
1. Replace the hardcoded `declaredEvents` with a resolver that reads `process.env.RIMSKY_EXECUTOR_DECLARED_EVENTS` (comma-separated, trimmed, empties dropped) at startup, defaulting to `[]` when unset. Thread the resolved value to the two consumers (`server.ts` gRPC `ObservabilityCapabilities` + `observability.ts` `capabilitiesPayload`, `observability.ts:220`) — either via a module-load-time parse or by threading through the server/capabilities config; pick whichever matches how `main.ts` already passes config to the server.
2. Confirm `ObservabilityCapabilities.declared_events` (field 7, `lib/protocols/proto/v1/executor_observability.proto:59`) carries the resolved list in both surfaces.

**Verification:** `cd lib/services/executors/claude-agent && npm run build`

### Task 21: `emit_named_event` tool + per-run event sink

**Files:** `src/internal-mcp-tools.ts`, `src/internal-mcp-server.ts`, `src/token-registry.ts`, `src/agent-run.ts`

Cousins: the `mcp.tool(name, desc, {zodSchema}, async (args) => {...})` registrations in `internal-mcp-server.ts` (e.g. `attributes_set` at `:404` — takes `token`, looks up `registry.lookup(args.token)`, calls a per-run callback, returns `{content:[{type:"text", text: JSON.stringify(...)}]}`); `TOOL_DEFINITIONS` (`internal-mcp-tools.ts:80`); the per-run `TokenEntry` (`token-registry.ts`, holds `attributesAtSpawn`, `onAttributesSet`, etc.).

**Steps:**
1. Add a per-run event buffer to the `TokenEntry` (e.g. `emittedEvents: { name: string; payload: Buffer }[]`) plus an `emitNamedEvent(name, payloadBytes)` accessor, initialized empty per dispatch.
2. Add `emit_named_event` to `TOOL_DEFINITIONS` and register it in `registerTools` (mirror `attributes_set`): schema `{ token: tokenField, name: z.string(), payload: z.unknown() }`. Handler: `registry.lookup(args.token)` (→ `unknownToken` if missing); **reject** (tool error) if `args.name` is not in the resolved declared-events list (thread the list to the registry/entry from Task 20); serialize `args.payload` to JSON bytes opaquely; append `{name, payload}` to the entry's buffer; return an ack `{status:"accepted"}`. Do not log/format the payload.
3. Add the new tool name to the `mcp__rimsky-callback__*` allowlist set the executor passes to the CLI (the same derive-from-`TOOL_DEFINITIONS` path added in commit `3ebe87a` — confirm `emit_named_event` is included automatically since it's derived from `TOOL_DEFINITIONS`).

**Verification:** `cd lib/services/executors/claude-agent && npm run build`

### Task 22: Surface emitted events in the async-callback body

**Files:** `src/server.ts`, `src/http-bridge.ts`

`outcomeToCallbackBody` (`server.ts:498`, `http-bridge.ts:259`) builds the async-callback body; the wire slot is `AsyncCallbackBody.events` (field 1, `lib/protocols/proto/v1/executor.proto:310`). The gRPC stream already closed at dispatch (`server.ts:370-384`), so events must ride this body, not the stream.

**Steps:**
1. In both `outcomeToCallbackBody` variants, read the per-dispatch event buffer (Task 21) and populate the body's `events` array as `[{name, payload}]` (payload as the opaque bytes; match the proto-JSON encoding the Go side expects — `lib/runtime/callback.go:456-462` reads `events[].{name, payload}` with base64 payload).
2. Confirm an empty buffer yields an absent/empty `events` array (no behavior change for agents that emit nothing).

**Verification:** `cd lib/services/executors/claude-agent && npm run build`

### Task 23: claude-agent tests

**Files:** `lib/services/executors/claude-agent/src/internal-mcp-server.test.ts` (or the existing tool test file), `src/server.test.ts`/`http-bridge.test.ts`

**Steps:**
1. Test `emit_named_event`: a declared name is accepted and buffered; an undeclared name returns a tool error; the payload is passed through unmodified (assert the buffered bytes equal the input serialization).
2. Test the env override: `RIMSKY_EXECUTOR_DECLARED_EVENTS="a,b"` produces `declared_events: ["a","b"]` in both capability surfaces.
3. Test `outcomeToCallbackBody`: with buffered events, the body's `events[]` is populated; with none, it's empty/absent.
4. Run `npm test`.

**Verification:** `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`

---

## Pass 7: Concept-doc reconciliations (design changes)

**Goal:** Apply the six `## Design changes` from the spec so the durable design docs match the shipped code. Edit Definition/Boundaries/Invariants in place; append dated Notes entries. Keep all body text self-contained (no file paths, code symbols, route literals, or external-doc refs — action strings, DSL keys, concept slugs, dates, spec slugs are allowed).
**Scope:** Tasks 24–29
**End state:** working
**Verification:** `for f in attribute instance breakpoint transition-reason template dry-run; do grep -q "2026-05-28" .ok-planner/design/concepts/$f.md || echo "MISSING: $f"; done` (prints nothing when all six carry the dated Notes entry)

### Task 24: `concepts/attribute.md` — open-schema extension exemption

**Files:** `.ok-planner/design/concepts/attribute.md`

**Steps:**
1. In the Invariants section, extend the rule "Each property satisfies one of: has `source:`, has `default:`, or is marked `readOnly: true` in the executor's `expected_attributes_schema`" with a fourth condition: *"or it is a property the executor does not enumerate, under an executor schema that does not constrain it — either the executor declares no `properties` block at all (a fully permissive schema), or it declares `additionalProperties` that is not `false`. In both cases the executor has delegated naming authority for unenumerated properties."*
2. Extend the adjacent invariant "L2 cannot set `readOnly: true` on a property the executor's schema does not also mark `readOnly: true`" with the same carve-out: an unenumerated property under such an open executor schema may be author-marked `readOnly: true`. Enumerated properties remain fully checked.
3. Append a Notes entry: `2026-05-28 — open-schema extension-property exemption clarified in the attribute-surface invariant per spec:2026-05-28-quality-of-life-features; an unenumerated property under an executor schema that does not constrain it (no properties block, or additionalProperties not false) is admitted without source/default/executor-readOnly and may be author-marked readOnly, while enumerated properties remain fully checked.`

**Verification:** `grep -q "additionalProperties" .ok-planner/design/concepts/attribute.md`

### Task 25: `concepts/instance.md` — termination invariant

**Files:** `.ok-planner/design/concepts/instance.md`

**Steps:**
1. Add an Invariant: *"An instance is terminal exactly when its terminal timestamp is set. The force-terminate control action is the production mechanism that sets it, abandoning any in-flight node-runs (transitioning them to failed). Terminal is not removal: the instance key is freed for reuse only by the subsequent row delete, which is permitted only once the instance is terminal."*
2. Append a Notes entry: `2026-05-28 — termination invariant added per spec:2026-05-28-quality-of-life-features; force-terminate is the first production path to mark an instance terminal, distinct from the row-delete reaper that frees the instance key.`

**Verification:** `grep -q "force-terminate" .ok-planner/design/concepts/instance.md`

### Task 26: `concepts/breakpoint.md` — REST hit-delivery boundary

**Files:** `.ok-planner/design/concepts/breakpoint.md`

**Steps:**
1. In Boundaries, broaden the "Does NOT own ... MCP transport for hit delivery" clause: hit *delivery* is owned by `concept:control-api`, which exposes **both** the MCP resource and a read-only REST route for hits; the breakpoint concept owns the ledger, not the transport.
2. Append a Notes entry: `2026-05-28 — hit-delivery boundary broadened to include a REST read route alongside the MCP resource per spec:2026-05-28-quality-of-life-features.`

**Verification:** `grep -q "REST" .ok-planner/design/concepts/breakpoint.md`

### Task 27: `concepts/transition-reason.md` — `instance_killed` value

**Files:** `.ok-planner/design/concepts/transition-reason.md`

**Steps:**
1. Add `instance_killed` to the closed enum description: a forced-instance-teardown reason that drives any non-terminal node state to failed, accepted by the next-state function for each non-terminal current state. **State-machine-validation-only** — NOT emitted as an audit-event kind; the teardown's auditable cause is the administrative `instance_terminated` event-log row, not the reason kind (the per-node state update writes run-row state only). Distinct from `policy_give_up` (policy-chain-driven) and the operator reset/invalidate reasons.
2. Append a Notes entry: `2026-05-28 — instance_killed transition reason added per spec:2026-05-28-quality-of-life-features for forced instance teardown of in-flight node-runs; validation-only, not an audit kind.`

**Verification:** `grep -q "instance_killed" .ok-planner/design/concepts/transition-reason.md`

### Task 28: `concepts/template.md` — validate-only sibling note

**Files:** `.ok-planner/design/concepts/template.md`

**Steps:**
1. Append a Notes entry: `2026-05-28 — a validate-only control-api action (template:validate) now runs the full registration validation pipeline (static attribute-schema check + the validation-protocol RPC fan-out) without persisting, per spec:2026-05-28-quality-of-life-features; registration remains the only persisting entry point.`

**Verification:** `grep -q "template:validate" .ok-planner/design/concepts/template.md`

### Task 29: `concepts/dry-run.md` — `instance:kill` in the enumeration

**Files:** `.ok-planner/design/concepts/dry-run.md`

**Steps:**
1. In the Owns clause, add `instance:kill` to the exhaustive per-handler dry-run-branch enumeration (alongside `instance:create`, `instance:terminate`, etc.).
2. Append a Notes entry: `2026-05-28 — instance:kill added to the dry-run-branch enumeration per spec:2026-05-28-quality-of-life-features; the force-terminate write action returns a would_have_terminated envelope under a dry_run grant.`

**Verification:** `grep -q "instance:kill" .ok-planner/design/concepts/dry-run.md`

---

## Manual checks after completion

None — every feature is verifiable via automated tests (Go handler/integration tests under testcontainers, clitest harness for CLI verbs, npm/jest for the executor). End-to-end claude-agent named-event emission against a live Claude CLI is not unit-tested; the executor-side tool behavior (Pass 6) and the rimsky-side callback `events[]` consumption (`lib/runtime/callback.go`, exercised by Go tests) are each covered independently, which is sufficient for this plan.
