# Attribute carry-forward + in-process utility executors — Completion Report

Spec: `.ok-planner/specs/2026-06-14-attribute-carry-forward-and-inproc-utility-executors-design.md`
Plan: `.ok-planner/plans/2026-06-14-attribute-carry-forward-and-inproc-utility-executors.md`
Date: 2026-06-14

This report walks 100% of the spec's `## Manifest`: all 5 stories with
their proof artifacts, and all 11 technical decisions classified as kept
or diverged. The verification gate (build + test + lint) and the
no-deferral audit both passed before this audit ran.

---

## 1. Proof walkthrough

### STORY-attribute-carry-forward
Stateful attribute carry-forward in scope; fresh in new RunScope.

- **Artifact:** `test/scenarios/attributes/carry_forward_e2e_test.go`
  (`TestCarryForwardE2E`)
- **Exhibits:** Drives three sequential dispatches of a `kind:
  loop_counter` node in one RunScope and asserts the incoming
  attribute bag carries the schema default on dispatch #1 (`count: 0`)
  and the prior writeback on dispatches #2 and #3 (`count: 1` / `count:
  2`), with the persisted `rimsky_node_attributes.data` rows showing
  `count = 1, 2, 3` across the three runs. A sub-graph invocation then
  re-runs the same node-kind in a fresh RunScope and asserts the first
  dispatch sees `count: 0` — proving the RunScope boundary blocks
  carry-forward. The real `loop_counter` builtin is the value-delivering
  handler; a `recordingLoopCounter` wraps it as a pure observer of the
  bag so neither the count semantics nor the writeback is stubbed.
- **Invocation:** `go test ./test/scenarios/attributes/... -count=1
  -run CarryForward -v` (testcontainers Postgres).
- **Status:** EXHIBITS WORKING

### STORY-loop-counter-cap
Bounded iteration via `loop_counter` node type.

- **Artifact:** `test/scenarios/loop_counter_cap_e2e_test.go`
  (`TestLoopCounterCapE2E`)
- **Exhibits:** Wires the production `loop_counter` builtin with
  `max=4` to two test-only inproc sink handlers — one subscribed to
  `event/loop`, one to `event/done` — and cycles the counter four times
  through the cascade. Asserts `loop_sink.invocations == 3`,
  `done_sink.invocations == 1`, in that order, plus the
  `rimsky_node_attributes` rows show `count = 1, 2, 3, 4` across the
  four counter runs (`new_count < max` ⇒ `loop` on 1/2/3, `done` on 4).
  The sinks are downstream observers — the value-delivering component
  is the real builtin.
- **Invocation:** `go test ./test/scenarios/... -count=1 -run
  LoopCounter -v`
- **Status:** EXHIBITS WORKING

### STORY-claude-agent-session-resume
CLI session continues in scope, fresh in new RunScope.

- **Artifact:**
  `lib/services/test/scenarios/claude_agent_session_resume_e2e_test.go`
- **Exhibits:** Boots the real `rimsky-all-in-one:latest` image plus
  the real claude-agent executor image with a stub `claude` binary that
  exercises the production CLI runner's `--session-id` / `--resume`
  argv paths. Three in-scope dispatches drive the agent through a
  scripted exchange; the test asserts each post-first dispatch is
  launched with `--resume <prior-runId>`, that the fake CLI confirms
  semantic continuity by surfacing the prior turn's content from an
  in-container memory keyed on the resumed session id, and that the
  `rimsky_node_attributes` row for each dispatch carries `session_token
  == <that dispatch's runId>`. A sub-graph dispatch then asserts the
  fake CLI is launched WITHOUT `--resume` and the sub-graph's first
  dispatch sees `session_token == ""` — fresh conversation.
- **Invocation:** `make core-images service-images && go test
  ./lib/services/test/scenarios/... -count=1 -run
  ClaudeAgentSessionResume -v`
- **Status:** EXHIBITS WORKING

### STORY-inproc-utility-executor
Utility node types dispatched without external service deployment.

- **Artifact:** `examples/inproc-loop-counter/template.yml` plus
  `examples/inproc-loop-counter/README.md` (the human-readable
  artifact), driven end-to-end by
  `test/scenarios/inproc_utility_executor_e2e_test.go`.
- **Exhibits:** Stands up the scenario harness with NO operator-side
  external executor configuration for `loop_counter` — the harness's
  executor map exposes only `stub` + `testexec`. The inproc registry
  seeded at supervisor startup via `builtin.RegisterAll` is the only
  resolution path. Reads
  `examples/inproc-loop-counter/template.yml` from disk, registers it
  through `POST /v1/templates` (asserts the `kind: loop_counter` sugar
  resolved locally), creates an instance, and waits for the counter's
  `done` event on the events feed within a bounded timeout. The
  example keeps the artifact alive in CI rather than silently rotting.
- **Invocation:** `go test ./test/scenarios/... -count=1 -run
  InprocUtilityExecutor -v`
- **Status:** EXHIBITS WORKING

### STORY-opaque-executor-scratch
Opaque bytes carried across recovery re-dispatch of the same row.

- **Artifact:** `test/scenarios/scratch_round_trip_e2e_test.go`
- **Exhibits:** Table-driven proof exercising every prior-dispatch
  disposition — `heartbeat_stale`, `retry_after_error`, `recalculate` —
  plus the mid-dispatch HTTP scratch callback path. Each variant seeds
  scratch bytes on a "prior" dispatch row (random suffix appended so a
  fixed-fixture match cannot mask a real round-trip break), drives the
  matching recovery enqueue site (`SweepStaleHeartbeats`, the
  `applyResolvedAction` `DispositionRetry` branch, `RecalculateNode`,
  or `POST /v1/runs/{run_id}/scratch`), and asserts the new dispatch's
  `ExecuteRequest.Scratch` is byte-for-byte the prior bytes. Inertness
  (`@blessed-invariant 21`) is the load-bearing property — verbatim
  round-trip is the assertion.
- **Invocation:** `go test ./test/scenarios/... -count=1 -run
  ScratchRoundTrip -v`
- **Status:** EXHIBITS WORKING

---

## 2. Technical decisions kept

Every TD in the manifest is enumerated here or in section 3.

1. **TD-attribute-carry-forward** — pre-substitution carry-forward step
   in attribute hydration. Honored as specified: the hydration loads
   the most-recent prior writeback for `(node, run_scope)` BEFORE
   substitution, and source-bound substitution overlays on top with
   defaults acting as a floor under carry-forward. Embodied in
   `lib/runtime/runner_dispatch.go:520-538` (`carryForward` lookup via
   `NodeAttributes().GetLatestByNode(ctx, acq.NodeID, acq.RunScopeID,
   tx)` then `substituteAttributesSchema(schema, rctx, carryForward)`),
   with the per-property overlay rules at
   `lib/runtime/runner_dispatch.go:943-1000+` (`substituteAttributesSchema`
   default-is-floor handling). Sub-graph sealing falls out from the
   `(NodeID, RunScopeID)` key — no parent-scope walk-up. Recorded:
   `.ok-planner/design/decisions/attribute-carry-forward.md`.

2. **TD-scratch-column** — node_run scratch persistence triple
   mirroring `parked_payload_*`. Embodied in
   `lib/foundation/persistence/postgres/migrations/010-node-run-scratch.sql:5-8`
   (`scratch_inline BYTEA`, `scratch_handle TEXT`,
   `scratch_handle_backend TEXT`) and
   `lib/foundation/persistence/sqlite/migrations/010-node-run-scratch.sql:4-6`
   (`BLOB` / `TEXT` / `TEXT`). Spill follows `concept:blob-backend` via
   the same `nilIfEmpty` helpers the parked-payload writes use.
   Recorded: `.ok-planner/design/decisions/scratch-column.md`.

3. **TD-scratch-protocol** — `bytes scratch` on ExecuteRequest +
   stream-close outcomes + scratch HTTP callback. Embodied in
   `lib/protocols/proto/v1/executor.proto:139`
   (`ExecuteRequest.scratch`), `:242` (`Success.scratch`), `:255`
   (`Error.scratch`), `:314` (`Park.scratch`). The supervisor
   materializes the row's scratch onto the wire at
   `lib/runtime/runner_dispatch.go:1344` (`req.Scratch = acq.Scratch`)
   and persists each terminal's `Scratch` field from
   `runner_dispatch.go:370,386,397` onto the row via the spill-aware
   `lib/runtime/runner_terminal_scratch.go`. The mid-dispatch HTTP
   route is mounted at `lib/runtime/callback.go:258`
   (`POST /v1/runs/{run_id}/scratch` → `rimskyscratch.Handler`), with
   the handler in `lib/graph/scratch/callback.go:76+`. The in-process
   equivalent helper is `lib/runtime/executor/scratch_writer.go:69`
   (`ScratchWriter.Write`). Recorded:
   `.ok-planner/design/decisions/scratch-protocol.md`.

4. **TD-scratch-recovery** — per-row scratch survives stale-heartbeat
   recovery across every prior-dispatch disposition. Embodied at all
   four enqueue sites: `lib/runtime/conductor.go:287-303`
   (`SweepStaleHeartbeats`), `lib/runtime/cascade_recalculate.go:228-244`
   (`RecalculateNode`), `lib/runtime/on_error.go:292-311`
   (`OnError`), and `lib/runtime/runner_error_policy.go:287-308`
   (`applyResolvedAction` `DispositionRetry`) plus its infra-reenqueue
   sibling at `runner_error_policy.go:440-473`. Each calls
   `Queue.LoadScratchInTx(ctx, tx, priorID)` and stamps
   `InitialScratch{Inline,Handle,HandleBackend}` onto the new
   `DispatchRequest`. `Queue.WriteScratchInTx` /
   `LoadScratchInTx` implementations:
   `lib/foundation/persistence/postgres/queue_park.go` +
   `lib/foundation/persistence/sqlite/queue_park.go`. Conformance
   coverage:
   `lib/foundation/persistence/conformance/recovery_aware_dispatch.go`.
   Recorded: `.ok-planner/design/decisions/scratch-recovery.md`.

5. **TD-inproc-transport-client** — third `Client` impl on
   `ClientPool`. Embodied at `lib/runtime/executor/client.go:141` —
   the transport-switch `case "inproc"` constructs
   `NewInProcessClient(ep, p.registry, p.newHctx)`. The dispatch site
   at `lib/runtime/runner_dispatch.go` (calling `client.Execute(ctx,
   req)`) is unchanged — transport stays opaque. Recorded:
   `.ok-planner/design/decisions/inproc-transport-client.md`.

6. **TD-inproc-handler-interface** — `InProcessHandler` Go interface
   with `EventSink`. Embodied at
   `lib/runtime/executor/inproc_handler.go:18` (`EventSink`) and `:44`
   (`InProcessHandler.Execute(ctx, req, sink, hctx) error`). The
   shape matches the spec's `EventSink` / `InProcessHandler` pair with
   the additional `HandlerContext` parameter for plumbing
   per-dispatch dependencies (see section 3 TD-inproc-handler-interface
   for the necessitated `HandlerContext` extension). Recorded:
   `.ok-planner/design/decisions/inproc-handler-interface.md`.

7. **TD-inproc-registry** — explicit handler registry at supervisor
   startup, no init-time globals. Embodied at
   `lib/runtime/executor/inproc_registry.go:19+`
   (`InProcessRegistry` struct + `Register` / `Lookup` /
   `RegisteredURLs`) and wired at
   `lib/runtime/supervisor.go:377-389`
   (`executor.NewInProcessRegistry()` + `builtin.RegisterAll(inprocReg,
   kindAliases)` — a duplicate-URL surfaces as a startup error).
   Bundled handlers live under
   `lib/runtime/executor/builtin/<name>/` per the spec. Recorded:
   `.ok-planner/design/decisions/inproc-registry.md`.

8. **TD-inproc-eventstream** — channel-backed `EventStream`. Embodied
   at `lib/runtime/executor/inproc_client.go:134` (`channelSink`) +
   `:148` (`inprocEventStream`). `InProcessClient.Execute` starts the
   handler on a goroutine, writes events to a buffered channel
   (capacity 16), and `Recv()` returns `io.EOF` on channel close or
   the handler's error from the `errCh`. Recorded:
   `.ok-planner/design/decisions/inproc-eventstream.md`.

9. **TD-kind-sugar-resolver** — `kind:` sugar at template registration.
   Embodied at `lib/foundation/spec/template.go:123` (`TemplateNodeDef.Kind`
   added alongside the unchanged required `Type`), with the static
   alias map at `lib/graph/node/kind_resolver.go:21+` (`KindAliasMap` —
   `Register` / `Resolve` / `CanonicalizeKindSugar`). The validator
   rejects `kind:` + `executor:` mixes and unknown kinds at
   `lib/graph/node/template_validator.go:883-925`
   (`validateKindDeclaration`), and the canonicalizer at
   `lib/graph/node/template_validator.go:456-459+` rewrites `kind:` →
   `executor:` + clears `kind` post-validation. Both paths get the
   alias map via `RegistryHooks.KindAliases`
   (`template_validator.go:236`). Recorded:
   `.ok-planner/design/decisions/kind-sugar-resolver.md`.

10. **TD-loop-counter-shape** — `loop_counter` attribute schema and
    behavior. Embodied at
    `lib/runtime/executor/builtin/loop_counter/handler.go:39-99+` —
    reads `max` (required, errors with `attributes_schema_invalid` if
    missing or `< 1`), reads `count` from incoming attributes
    (carry-forward yields prior, `0` on first dispatch in scope),
    computes `new_count = count + 1`, emits `loop` named event while
    `new_count < max` else `done`, and closes Success carrying
    `attributes_delta { count: new_count }`. Schema fragment +
    declared-events vocabulary + executor alias / kind / URL constants
    live in `lib/runtime/executor/builtin/loop_counter/schema.go`.
    Recorded: `.ok-planner/design/decisions/loop-counter-shape.md`.

11. **TD-claude-agent-session-attribute** — `session_token` as
    carry-forward attribute on claude-agent. Embodied at
    `lib/services/executors/claude-agent/src/expected-attributes-schema.ts:70-74`
    (the new `session_token: { type: "string", readOnly: true,
    default: "" }` property), with `server.ts:118-122` threading
    `attributes.session_token` into the per-dispatch ResumeContext via
    `resolveEffectiveResumeContext` (`server.ts:935+`). The terminal
    Success write in `agent-run.ts:801-830` merges `session_token:
    runId` unconditionally onto the writeback bag so every Success
    commits the current dispatch's CLI session for next-dispatch
    carry-forward. The Park-path `ResumeContext.session_token`
    plumbing is unchanged. Recorded:
    `.ok-planner/design/decisions/claude-agent-session-attribute.md`.

---

## 3. Technical decisions diverged

### TD-inproc-handler-interface — necessitated extension to `HandlerContext`
Spec choice: `InProcessHandler.Execute(ctx, req, sink) error`.
Implementation: `InProcessHandler.Execute(ctx, req, sink, hctx
HandlerContext) error` at
`lib/runtime/executor/inproc_handler.go:44`, with the bundled
`HandlerContext` at `inproc_handler.go:30+` carrying a per-dispatch
`*ScratchWriter`.

- **Flavor:** necessitated.
- **Reason:** The TD-scratch-protocol decision mandates that
  in-process handlers can persist mid-dispatch scratch via a runtime
  helper rather than HTTP. Without a per-dispatch context handles
  parameter on the interface signature, the handler has no path to
  reach that helper without re-introducing globals or wiring it
  through `ExecuteRequest`. The extension matches the spec's "and
  future helpers as the inproc surface grows" intent and is opaque to
  gRPC / HTTP-bridge dispatches. The `HandlerContextFactory` closure
  at `lib/runtime/executor/inproc_client.go:32+` is the construction
  point; the supervisor binds it at startup
  (`supervisor.go:391+`).

### TD-inproc-transport-client — necessitated factory extension
Spec choice: `NewInProcessClient(ep, registry) (Client, error)`.
Implementation:
`NewInProcessClient(endpoint, registry, newHctx
HandlerContextFactory) (Client, error)` at
`lib/runtime/executor/inproc_client.go:46+`, plus a paired
`NewClientPoolWithInProcess(registry, newHctx)` constructor at
`lib/runtime/executor/client.go` and the pool struct gains
`registry *InProcessRegistry` + `newHctx HandlerContextFactory`
fields.

- **Flavor:** necessitated.
- **Reason:** Same root cause as the TD-inproc-handler-interface
  divergence — the `HandlerContext` carrying the per-dispatch
  `ScratchWriter` has to reach the handler, which means the factory
  has to receive a per-dispatch context-builder closure. The pool's
  `NewClientPool()` constructor (no inproc) stays for tests that
  don't exercise the inproc path.

### Supervisor-startup factoring — necessitated extraction to `builtin.RegisterAll`
Spec choice (and plan Task 27): supervisor.go calls
`inprocReg.Register(loop_counter.InProcURL, loop_counter.New())` and
`kindAliases.Register(loop_counter.KindName,
loop_counter.ExecutorAlias)` inline.
Implementation: `lib/runtime/executor/builtin/builtins.go:36+`
(`RegisterAll(reg, aliases)`) holds the registration pairs, called
from `lib/runtime/supervisor.go:379` AND from the control-API
template-validation path (which validates `kind:` references without
dispatching). The supervisor's call site stays a single line.

- **Flavor:** improved.
- **Reason:** Two call sites need the same per-builtin constants
  (dispatch-side supervisor + template-validation-side control-API).
  Extracting the registration pairs into the `builtin` package keeps
  the per-handler constants as the single source of truth — a new
  utility executor added under `lib/runtime/executor/builtin/<name>/`
  becomes visible to every process by adding one line in
  `builtins.go`. The plan's inline approach would have forced two
  hand-maintained registration lists.

### TD-scratch-protocol — necessitated `runner_terminal_scratch.go` extraction
Spec choice (and plan Task 16): inline the spill-aware
`Queue.WriteScratchInTx` call inside each of `applyTerminalComplete`,
`applyTerminalError` / `applyErrorPolicy`, and `applyTerminalPark`.
Implementation: a shared helper at
`lib/runtime/runner_terminal_scratch.go` is called from each terminal
site, picking inline-vs-spill via the existing `shouldSpillBlob`
helper, falling back to inline on spill failure, and excluding
sub-graph exit dispatch rows in one place.

- **Flavor:** improved.
- **Reason:** The plan's three-site inlining would have drifted on
  the sub-graph-exit exclusion rule and the spill-fallback policy.
  Centralizing the gate keeps Success / Error / Park / Infra-reenqueue
  branches consistent and prevents one site from quietly developing
  a different scratch-persist contract from another. No behavior
  change vs. the spec; the persistence call is still the same
  `Queue.WriteScratchInTx` per terminal.

### TD-scratch-recovery — necessitated extra enqueue site in `runner_error_policy.go`
Spec choice (and plan Task 12 Step 4): one enqueue site in
`applyResolvedAction`.
Implementation: scratch-copy at both the `applyResolvedAction`
`DispositionRetry` branch (`runner_error_policy.go:287-308`) AND its
infra-reenqueue sibling (`runner_error_policy.go:440-473`).

- **Flavor:** necessitated.
- **Reason:** The infra-reenqueue site also creates a new dispatch row
  carrying `prior_dispatch_id` (under the `retry_after_error`
  disposition). Without copying scratch there, an infra-class error on
  a scratch-bearing dispatch would silently lose the executor's
  in-flight state on recovery — the same load-bearing property the
  spec pins for the policy-retry path. The implementer fixed both.

### Post-audit hardening additions (recorded after the completion auditor ran)

The following 8 entries landed in the code-review cleanup cycle that
ran after the initial completion audit. Each is either a bug surfaced
by the reviewer (which the project's "Fix Every Bug You Find" rule
pulled into scope) or defensive hardening based on the spec's intent.
None changes a spec contract.

### claude-agent stub `probe_park` branch — necessitated handler for the conformance Park-reason probe
Spec choice: not addressed by this plan's spec; the conformance
executor's `park_reason_emission` scenario expects executors in stub
mode to honor `probe_park: true` by returning a Park terminal whose
reason is in the closed two-value set `{await_callback, snooze}`.
Implementation: new branch in
`lib/services/executors/claude-agent/src/agent-run.ts::runAgentStub`
plus an extension to the malformed-attribute escape-hatch check in
`runAgent`. Validates `attrs.park_reason` against the closed set;
rejects with `agent/attribute_invalid` otherwise; an undefined
`park_reason` defaults to `await_callback`.

- **Flavor:** necessitated.
- **Reason:** Surfaced by the verifier when running the conformance
  executor against claude-agent. The gap pre-dated this plan but
  blocked the final verification gate. Per the project's "Fix Every
  Bug You Find" rule, the cleanup cycle landed the fix.

### `validateExecutorCoherence` recognizes kind-sugar nodes — necessitated signature change
Spec choice: not directly addressed; the spec's "kind is sugar for
executor" claim implies kind-sugar nodes are treated equivalently to
`executor:`-form nodes throughout validation.
Implementation:
`lib/graph/node/template_validator.go::validateExecutorCoherence`
signature now takes `hooks RegistryHooks`; the "neither set" check
routes through `effectiveExecutor(n, hooks)` so kind-sugar nodes are
recognized as having an executor.

- **Flavor:** necessitated.
- **Reason:** Without this, every `kind: loop_counter` template that
  declared attributes (the typical shape — `loop_counter` requires
  `max`) triggered a spurious "pure-cascade node declares attributes"
  warning. Under `warnings_as_errors`, this would block registration.
  The published example template `examples/inproc-loop-counter/template.yml`
  exhibited the warning before the fix.

### `validateErrorTypes` keys on `effectiveExecutor` — necessitated parity fix
Spec choice: not directly addressed; same "kind is sugar for executor"
implication.
Implementation:
`lib/graph/node/template_validator.go::validateErrorTypes` uses
`effectiveExecutor(n, hooks)` instead of `n.Executor` directly.

- **Flavor:** necessitated.
- **Reason:** Without this, a kind-sugar node with `error_types:`
  clauses skipped error-class vocabulary validation entirely (because
  `n.Executor == ""` causes `vocabularyKnown` to stay false), creating
  an asymmetry between `kind:` and `executor:` forms.

### `applyTerminalInfraError` sub-graph exit short-circuit — improved structural carve-out
Spec choice: the scratch carve-out for sub-graph exits lives at the
scratch-persist site (`applyTerminalScratchInTx`).
Implementation:
`lib/runtime/runner_error_policy.go::applyTerminalInfraError`
early-returns `(nil, nil)` if `isSubgraphExitNode(acq)`, skipping both
the scratch persist AND the infra-reenqueue.

- **Flavor:** improved.
- **Reason:** The persist-site carve-out only stopped the WRITE; the
  subsequent LOAD still read whatever was on the row, creating
  asymmetric scratch handling (terminal scratch dropped vs.
  mid-dispatch HTTP scratch carried forward). Eliminating the
  asymmetry at one site beats documenting it.

### `substituteAttributesSchema` carry-forward projection — RETRACTED during closing walk
Original code-review-cycle change: the explicit-properties path
projected the carry-forward seed through `properties:`, dropping
any key the current schema didn't declare; the nil-schema and
no-properties paths were also tightened to return empty.

Status: **retracted**. The projection step was rolled back during
the closing walk after a substantive design discussion. The current
behavior is verbatim seeding in the explicit-properties path; the
nil-schema and no-properties branches still return empty (those
branches are reached only by pure-cascade nodes with no `attributes:`
block, where carry-forward is empty in practice).

- **Flavor:** retracted.
- **Reason:** The projection step was over-eager. The "schema
  evolution" justification doesn't hold (template versions don't
  change within a RunScope, and carry-forward is RunScope-bounded);
  the "executor bug" justification only fires if our own validation
  is broken (defense-in-depth against a code bug, not a real path).
  More importantly, the step imposed strict-allow-list behavior at
  carry-forward regardless of the schema's stated
  `additionalProperties`, overriding the template author's intent for
  permissive schemas and being redundant for closed schemas (where
  commit-time validation already enforced the strict invariant on the
  prior writeback). The right invariant: trust the schema author's
  declaration, let the bag carry forward verbatim, and let
  `lib/graph/attribute/attributes.Validate` at the dispatch and commit
  checkpoints honor `additionalProperties` as written. Post-revert
  re-run of the in-tree scenarios sweep is clean (0 FAILs across all
  21 packages; the carry-forward proof passes).

### `ScratchWriter` optional `Logger` + inline-fallback on spill failure — improved harmonization
Spec choice (and plan Task 19): in-process executor handlers persist
mid-dispatch scratch through a runtime helper.
Implementation:
`lib/runtime/executor/scratch_writer.go::ScratchWriter` gained an
optional `Logger` field; spill failure falls back to inline with a
logged Warn, matching `lib/runtime/callback.go::scratchStoreAdapter`'s
HTTP-route behavior. Wired with `loggerCap := cfg.Logger` at
`lib/runtime/supervisor.go:471`.

- **Flavor:** improved.
- **Reason:** Reviewer flagged asymmetric spill-failure policy between
  the in-process writer (returned the error) and the HTTP-route
  adapter (fell back to inline). An executor author switching between
  transports got different failure modes for the same condition.
  Harmonizing reduces the surface for one side drifting from the
  other.

### claude-agent stub Success returns `session_token: opts.runId` — necessitated invariant fix
Spec choice (and plan Task 32): every terminal Success commits
`session_token=runId`.
Implementation: all three stub Success returns in
`lib/services/executors/claude-agent/src/agent-run.ts::runAgentStub`
now stamp `session_token: opts.runId`, and the matching three
TypeScript test files were updated to assert it.

- **Flavor:** necessitated.
- **Reason:** The spec's "every terminal Success" wording was clear;
  the stub path silently violated it because the implementer treated
  stub mode as a special case. Aligning fixes the invariant for any
  test that ever exercises the stub under a carry-forward template.

### `seedInprocExecutorAlias` warns on silent skip — improved diagnostic
Spec choice (and plan Task 27): seed the inproc executor alias into
the resolver at supervisor startup.
Implementation: `lib/runtime/supervisor.go::seedInprocExecutorAlias`
now returns `bool`; both call sites emit `cfg.Logger.Warn` with the
alias name and the `%T` of the resolver shape when the seed is
silently skipped.

- **Flavor:** improved.
- **Reason:** Without the warn, a custom resolver wrapper (added for
  observability, rate-limiting, etc.) would silently break inproc with
  no diagnostic. The warn surfaces the skip and identifies the
  resolver shape blocking the seed.

### http-node stub `probe_park` parity — necessitated parity fix landed during walk
Spec choice: not addressed by this plan's spec; the
`park_reason_emission` conformance scenario expects every executor in
stub mode to honor `probe_park: true` (mirror of the claude-agent
hatch).
Implementation: added a `probe_park` escape hatch in
`lib/services/executors/http-node/server.go::Execute` (placed
immediately after the existing `stub_probe` hatch and before the
URL-required check) plus a new helper
`executeParkProbe` that reads `attributes.park_reason` (defaulting to
`await_callback`), validates it against the closed two-value set
`{await_callback, snooze}`, rejects unrecognized values with
`http/attribute_invalid`, and emits a Park terminal with a typed
`ParkReason`. `snooze` carries a finite `resume_at`; `await_callback`
leaves it unset.

- **Flavor:** necessitated.
- **Reason:** Identified during the closing walk. The walk noted that
  http-node carried the identical gap claude-agent's earlier fix
  closed — both stubs handled `stub_probe` but not `probe_park`. The
  workflow verifier had only run the conformance executor against
  claude-agent, so http-node's gap stayed below the verifier's radar.
  The project's "Fix Every Bug You Find" rule pulled the parity fix
  into the closing walk. Verified post-fix: `go run ./cmd/rimsky
  conformance executor --endpoint <http-node-stub> --transport grpc
  --require-stub-mode` returns **8 passed, 0 failed, 1 skipped**
  (the skipped scenario is `async_handoff`, which http-node does not
  implement). `park_reason_emission` is among the passes. The
  http-node service image was rebuilt and re-tagged `latest` so
  downstream consumers carry the fix.

---

## Coverage check

- **Stories:** 5 exhibited / 5 in manifest. No GAPs. All five proof
  artifacts were re-verified end-to-end against the post-code-review
  tree during the closing walk (scenarios sweep + image rebuilds +
  bundled-services sweep, all zero-FAIL).

- **Technical decisions:** 11 kept + 0 spec-listed TDs outright
  diverged + 5 spec-anchored necessitated/improved-flavor entries in
  section 3 + 8 post-audit hardening additions from the code-review
  cleanup (one of which — `substituteAttributesSchema` carry-forward
  projection — was retracted during the closing walk) + 1 closing-walk
  parity fix (http-node `probe_park`) = 11 spec TDs fully accounted
  for, with 13 effective implementation-shape divergences the spec
  did not anticipate but were either required by the necessity rule
  or surfaced as bugs/asymmetries during cleanup or the walk. The
  retracted entry is preserved in section 3 for audit-trail purposes.

  - Spec TDs: 11 (TD-attribute-carry-forward, TD-scratch-column,
    TD-scratch-protocol, TD-scratch-recovery,
    TD-inproc-transport-client, TD-inproc-handler-interface,
    TD-inproc-registry, TD-inproc-eventstream, TD-kind-sugar-resolver,
    TD-loop-counter-shape, TD-claude-agent-session-attribute).
  - In section 2 (kept): 11. In section 3 (diverged): 2 spec TDs
    appear with necessitated implementation-shape extensions
    (TD-inproc-handler-interface, TD-inproc-transport-client) plus 3
    necessitated entries the spec did not name
    (supervisor-startup factoring, terminal-scratch helper extraction,
    extra enqueue site in `runner_error_policy.go`) plus 8 post-audit
    hardening additions (claude-agent `probe_park` handler,
    `validateExecutorCoherence` recognizes kind-sugar,
    `validateErrorTypes` keys on `effectiveExecutor`,
    `applyTerminalInfraError` sub-graph exit short-circuit,
    `substituteAttributesSchema` empty-projection on no-properties,
    `ScratchWriter` optional `Logger` + inline-fallback,
    claude-agent stub Success commits `session_token`,
    `seedInprocExecutorAlias` warns on silent skip).

  Section-2 and section-3 entries are not mutually exclusive: a spec
  TD that was kept in spirit (the persistence call, the registration
  shape, the wire field) is still listed under section 2 even if a
  necessitated implementation-shape extension lands it also in
  section 3. No spec TD is missing from this report.

- **Known follow-up:** the http-node executor's stub has the same
  `probe_park` gap that the claude-agent fix above closed. Recorded
  in section 3's "Known follow-up" subsection.

No GAPs and no process defects. The verification gate (build + tests
+ lint), the no-deferral audit, and the closing-walk re-verification
all passed before archival.
