# Nomenclature Resolution — Design Spec

**Date:** 2026-05-12
**Input:** `file:.ok-planner/sketches/2026-05-12-nomenclature-audit.md` (all 19 cross-layer decisions + per-concept ride-alongs, walked through and decided)
**Output:** This spec encodes the end-state for every rename, restructure, fold, and reorganization decided in the audit. Sub-decisions deferred to the brainstorm pass (orphan-reaper naming, `Error`/`Success`/`Snooze`/`AwaitAsyncCallback` field shapes, `/frames` scoping, `CapabilitiesProvider` factor, `kind`/`type` supersession) are resolved inline.

---

## Summary

Rimsky's vocabulary has accreted three categories of drift since the project began:

1. **Legacy aliases** kept "temporarily" — `Store = ClaimProducer`, `stores:` YAML alias, `write_semantics:` single-value shortcut, `region`/`scope`, `consumer_key`/`instance_key`, `rimsky_dispatch`/`rimsky_worker_request`, `rimsky_lock_holders`/`rimsky_claim_handle`.
2. **Cross-surface asymmetries** — proto service `NodeExecutor` vs Go interface `Executor`; persistence-side `Store` vs claim-producer-side `Store`; `GetCapabilities` (executor) vs `Capabilities` (claim-producer); `frame_resolution:` YAML vs `mode` column; concept-doc table names oscillating singular/plural.
3. **Overloaded terms** — `terminal` covering three parallel semantics; `cascade` covering two distinct walks; `transition_reason` vs `last_outcome`; `peer` as a poor fit for orchestrator-to-orchestrated services; `opacity` failing to distinguish byte-opaque inertness from structural inertness; `modeling/` too vague for what is two distinct concerns (graph runtime vs control plane).

This spec covers all 19 cross-layer decisions plus the per-concept ride-alongs that share the same touch surface. The end-state is:

- One canonical name per concept across code, schema, proto, YAML, binaries, and concept docs.
- A clean executor wire protocol with channel-mechanics (`StreamClose`) and outcome (`oneof` over `Success` / `Error` / `Snooze` / `AwaitAsyncCallback`) explicit and orthogonal.
- A single migration file (`001-baseline.sql`) reflecting the final post-rename schema. Dev Postgres requires a hard reset; pre-v1 has no production data to preserve.
- Root module reorganized from `modeling/` into `graph/` + `control/` siblings; depguard rules updated; concept-doc layer headings rebalanced.
- Concept count: 46 → 46 (one drop via `concept:held-claim` fold into `concept:claim-handle`; one rename via `concept:opacity` → `concept:inertness`; one rename via `concept:worker-request` → `concept:node-run`; one promotion via `concept:service` umbrella).

Wire format breaks on the proto restructure (#9); migration history breaks on the baseline rebase (#3); YAML aliases retire (#6). All wire/wire-config breaks land at once — pre-v1; no consumer pin to preserve.

---

## Decision groups

The 19 cross-layer decisions group into eight implementation themes plus one ride-along group. Each theme is presented as a section below. Implementation-time sequencing is described in **Dependencies** at the end; this section describes *what* changes, not how to ship it.

### Group A — Migration baseline rebase

**Covers:** cross-layer #3 (broadened); absorbs schema portions of #4, #5, #7, #13, #14.

**End-state:** the numbered migration chain collapses into a single `file:foundation/persistence/postgres/migrations/001-baseline.sql` reflecting the final post-walkthrough schema. All schema-rename history (`consumer_key` → `instance_key`; Phase-5 renames; `region` → `scope`; singular → plural; `frame_resolution_mode`; `worker_request` → `node_runs`) is erased at the migration-file level. Rename-history comments in source files (`code:foundation/persistence/postgres/instances.go#8` and equivalents) are deleted; git log carries the rename history.

**SQLite migrations** (`file:foundation/persistence/sqlite/migrations/`) collapse to the same baseline shape, adjusted for SQLite syntax differences. Schema-equivalence test between Postgres and SQLite baselines runs in `code:foundation/persistence/conformance/`.

**Final schema, table names** (post-#13 plural + #14 node-runs + #4 frame_resolution_mode):

| Table | Notes |
|---|---|
| `table:rimsky_nodes` | (already plural) |
| `table:rimsky_node_runs` | renamed from `rimsky_worker_request`; replaces `rimsky_dispatch` (Phase-5 history erased) |
| `table:rimsky_claim_handles` | pluralized from `rimsky_claim_handle` |
| `table:rimsky_claim_holders` | (already plural) |
| `table:rimsky_frames` | column `mode` renamed to `frame_resolution_mode` |
| `table:rimsky_schedules` | (already plural) |
| `table:rimsky_instances` | column `consumer_key` already renamed to `instance_key`; rebase erases the rename trace |
| `table:rimsky_templates` | (already plural) |
| `table:rimsky_template_tags` | (already plural) |
| `table:rimsky_events` | (already plural) |
| `table:rimsky_node_events` | (already plural) |
| `table:rimsky_node_attributes` | (already plural) |
| `table:rimsky_supervisors` | (already plural) |
| `table:rimsky_lifecycle_idempotencies` | pluralized from `rimsky_lifecycle_idempotency`. The plural form is `idempotencies` (one per `(producer, scope_kind, scope_id)` row recording an idempotent-event delivery — each row is one idempotency assertion, so the plural is well-formed). |
| `table:rimsky_blob_orphans` | (already plural) |

**Column-level renames absorbed into the baseline:**

- `col:rimsky_frames.mode` → `col:rimsky_frames.frame_resolution_mode`
- `col:rimsky_claim_handles.scope_data` (already named correctly — `region` legacy erased)
- All references to `rimsky_worker_request.*` → `rimsky_node_runs.*` (including `claimed_by`, `phase`, etc.)
- FK columns referencing the renamed tables update transitively.

**Row-struct convention** (documented in `concept:persistence-driver.md`): Go-side row structs stay singular (`NodeRow`, `FrameRow`, `ClaimHandleRow`, `NodeRunRow`) even though tables are plural. Go convention is to name the row type for one instance; the table name reflects the collection.

**Operational discipline** (encoded in CHANGELOG Unreleased entry):

> This change collapses the migration history. Dev Postgres requires `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` before `cmd:rimsky-migrate` reapplies the baseline. Pre-v1; no production data to preserve.

**Out of scope:** the pre-Phase-5 migration files are not preserved in any archived form; git log is sufficient.

---

### Group B — Foundation/protocol vocabulary alignment

**Covers:** cross-layer #1 (`Store = ClaimProducer` retirement), #2 (persistence-side `Store` → `Driver`), #7 (delete legacy `region` comment), #11 (`transition-reason` / `last-outcome` doc relationship).

#### B.1 — Retire `Store = ClaimProducer` alias and `Store`-substring residue (#1)

- Retire `code:foundation/locks/interface.go::Store` type alias entirely.
- Rename `code:foundation/integration/runner.go::AcquiredLock.Store` → `AcquiredLock.Producer`.
- Rename `code:protocols/claimproducer/types.go::ClaimSpec.StoreName` → `ClaimSpec.ProducerName`. (Re-export at `code:foundation/locks/types.go::ClaimSpec` follows.)
- Rename `proto:store_observability.proto::StoreObservability` proto service + Go type → `ClaimProducerObservability`. File renames: `protocols/proto/v1/store_observability.proto` → `protocols/proto/v1/claim_producer_observability.proto`. Generated bindings regenerate via `cmd:make proto-gen`.
- Rename `code:foundation/integration/runner_dispatch.go::makeStoreHandle` → `makeClaimHandle`. Aligns with `table:rimsky_claim_handles` (post-#13 plural); the function builds an introspection handle for an opened claim, not for the producer.
- Retire `cfg:stores[]` YAML alias. Operator configs must use `claim_producers:` (the canonical key). Reference configs at `file:deploy/rimsky.yml` and any sample / test YAML sweep to canonical form.
- **Keep** the `code:stores/` directory and bundled-impl binary naming at the bundled-services layer. "Store" is layer-appropriate colloquial vocabulary for the producer-as-deployable-binary view.

#### B.2 — Persistence-side `Store` umbrella → `Driver` (#2)

- Rename `code:foundation/persistence/store.go::Store` umbrella interface → `Driver`. Aligns with `concept:persistence-driver`.
- Rename file `foundation/persistence/store.go` → `foundation/persistence/driver.go`.
- Update all call sites: `var s persistence.Store` → `var d persistence.Driver`, etc. Sweep across `foundation/`, `modeling/` (post-#19: `graph/` + `control/`), `cmd/`, `test/`, and bundled `stores/`.

**After B.1 + B.2 land:** the noun "store" appears only at the bundled-services layer (`stores/<name>/` binaries, `concept:claim-producer.md` Adjacent mentions of "store" as the layer-appropriate colloquial). No ambiguity remains in either direction.

#### B.3 — Delete legacy `region` comment (#7)

- Delete entirely the `code:foundation/locks/conflict.go#14-18` paragraph that cites "v2's per-store RegionsConflict". Git log carries the design-evolution history; if byte-equal rationale is load-bearing for future maintainers, it belongs in `concept:scope.md`, not in a code comment.
- No schema action (column already named correctly; baseline rebase per Group A absorbs any final residue at the migration level).

#### B.4 — Document `transition-reason` ↔ `last-outcome` relationship (#11)

- Add a "Relationship to sibling concept" subsection to both `concept:transition-reason.md` and `concept:last-outcome.md`. Each subsection contains a short table mapping typical pairings:
  - `transition_reason = HandlerComplete` + handler resolved `by_changed` → `last_outcome ∈ {fresh_changed, fresh_unchanged}`.
  - `transition_reason = HandlerComplete` + handler resolved `always_propagate` → `last_outcome = fresh_changed` regardless.
  - `transition_reason = OperatorReset` → `last_outcome` unchanged from prior run.
  - `transition_reason = Invalidate` / `HeartbeatLost` → no `last_outcome` write.
- Add `@concept: transition-reason` annotation at `code:foundation/cascade/state.go::TransitionReason` declaration plus a short pointer comment ("sibling to `last_outcome` — see concepts/transition-reason.md Relationship section").
- Add `@concept: last-outcome` annotation at `last_outcome`-writing call sites with the symmetric pointer.
- No code-behavior changes; readability only.

---

### Group C — YAML cleanup

**Covers:** cross-layer #6.

- Retire `cfg:stores[]` YAML alias (already noted in B.1; reaffirmed here for completeness).
- Retire `cfg:claim_producers[].write_semantics:` single-value shortcut. Configs must use the canonical `write_semantics_allowed:` list form (renamed below).
- Rename `cfg:claim_producers[].write_semantics_envelope` → `cfg:claim_producers[].write_semantics_allowed` across every surface:
  - YAML keys at `file:deploy/rimsky.yml` and any reference/test configs.
  - Proto field `proto:claim_producer.proto::Capabilities.write_semantics_envelope` → `Capabilities.write_semantics_allowed`. Wire-format-breaking; pre-v1.
  - Generated bindings regenerate via `cmd:make proto-gen`.
  - Go field `Capabilities.WriteSemanticsEnvelope` → `Capabilities.WriteSemanticsAllowed` in the handshake response struct and any internal types that mirror it.
  - Docs sweep: `concept:write-semantics.md` Boundaries/Invariants, `file:CLAUDE.md` "Non-obvious gotchas" entry.
- Reasoning: "envelope" is a precise metaphor (bounding set of allowed values, flight-envelope sense) but technically obscure. "allowed" is plain English and captures the operator-policy/permission framing exactly.

**Out of scope:** other YAML keys not flagged in the audit (e.g., `persistence:`, `named_locks:`, `executors:`) stay as-is.

---

### Group D — Code-side schema-rename residue

**Covers:** cross-layer #4 (code/Go/YAML for frame_resolution_mode), #5 (code residue post-Phase-5), #14 (code/route/Go renames for node-run).

#### D.1 — `frame_resolution_mode` canonicalization (#4)

- YAML field on template: `frame_resolution:` → `frame_resolution_mode:` (rename, no alias).
- Column rename `col:rimsky_frames.mode` → `col:rimsky_frames.frame_resolution_mode` (absorbed by Group A baseline).
- Go type `code:modeling/frame/types.go::FrameMode` → `FrameResolutionMode`. Reasoning per audit: `frame_resolution` alone is ambiguous (policy vs outcome); `frame_resolution_mode` is unambiguous and reads cleanly across YAML / column / Go.
- Helper `code:foundation/persistence/postgres/frames.go::LookupFrameMode` → `LookupFrameResolutionMode`.
- Go struct field `TemplateSpec.FrameResolution` → `TemplateSpec.FrameResolutionMode`. JCS-canonicalization layer reads `t.spec->>'frame_resolution_mode'`.
- Sweep concept-doc prose at `concept:frame.md` (currently mixes `frame_resolution` and `mode` references).

#### D.2 — Phase-5 code residue sweep (#5)

- Rename `code:foundation/integration/conductor.go::SweepOrphanedClaims` → `SweepOrphanedNodeRuns`. Operates on `rimsky_node_runs` post-rename; "claims" was confusing shorthand for `claimed_by` ownership distinct from `concept:claim`.
- Rename `code:foundation/integration/orphan_reaper.go::SweepClaimHandles` → `SweepOrphanedClaimHandles`. Adds the state-descriptor consistent with the rest of the sweep-function naming pattern (`Sweep + state-descriptor + target-noun`: `SweepStaleHeartbeats`, `SweepParkedNodes`, `SweepOrphanedBlobs`).
- **No umbrella `SweepOrphans`.** Existing convention favors split-by-purpose over coalesce-by-table (e.g., `SweepStaleHeartbeats` and `SweepOrphanedClaims` both target the worker-request table today and are still separate functions); preserve that.
- The constant `code:foundation/integration/conductor.go::OrphanedClaimTimeout` is the **shared 5×heartbeat_interval cutoff** used by both renamed sweeps (per `invariant:6` — same cutoff applies to `rimsky_node_runs` orphan reap and `rimsky_claim_handles` orphan reap). Update its doc comment from "the cutoff used by SweepOrphanedClaims" to "the cutoff used by `SweepOrphanedNodeRuns` and `SweepOrphanedClaimHandles`" so the comment matches its actual call sites. Constant name unchanged.
- Source `.proto` comments that reference Phase-5 legacy nouns (`rimsky_dispatch`, `dispatch_id` in `proto:executor.proto::ExecuteRequest.dispatch_id`) update where the comment is misleading:
  - The wire field name `dispatch_id` stays (compatibility-preserving comment cites the executor-observability protocol). The wire field name is preserved; only the comment updates to read "underlying table is `rimsky_node_runs` post-Phase-5+rename" instead of "`rimsky_worker_request` post-Phase-5 consolidation".
  - Generated bindings refresh via `cmd:make proto-gen`.
- Update `file:.claude/rules/rules.md` path references that still cite the pre-Phase-5 `core/queue/...` layout. Sweep to current `foundation/persistence/...` etc.

#### D.3 — `worker-request` → `node-run` rename (#14)

- Concept-file rename: `file:.ok-planner/design/concepts/worker-request.md` → `file:.ok-planner/design/concepts/node-run.md`. Body uses "node-run" throughout (with hyphen in prose; "node_run" / "node_runs" in code/schema contexts).
- Concept-doc body: definition becomes "one execution of one node within a frame." The frame ⊃ node-run hierarchy is documented as the model: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.
- Behavior unchanged: `phase` column values stay (`pending`/`active`/`held`/`completed`); orphan reaper lifecycle stays; held-claim semantics stay.
- Go-side identifier renames:
  - Row struct: `code:foundation/persistence/postgres/queue.go::WorkerRequest` → `NodeRunRow` (or equivalent — verify current struct name during execution).
  - Variable conventions: `workerRequest` → `nodeRun`, `workerRequestID` → `nodeRunID`, `worker_request_id` (when used as a SQL parameter name in Go code) → `node_run_id`.
  - Function names that include the legacy noun (e.g., interface methods on `persistence.Driver` like `ClaimWorkerRequest`, `GetWorkerRequest`, etc.) rename to `ClaimNodeRun`, `GetNodeRun`, etc. Sweep `code:foundation/persistence/` interfaces and Postgres/SQLite drivers.
- Operator-facing route rename: `route:GET /dispatches` → `route:GET /node-runs`. Update `code:modeling/controlapi/` handler registrations and any tests / smoke fixtures that hit the route.
- CLI: `cmd:rimsky-cli` subcommands that target dispatches rename to `node-runs` (if any; sweep `code:modeling/cli/`).
- CLAUDE.md "Vocabulary" section: update "worker-request" / "dispatch row" prose to "node-run."
- Adjacent concept docs: sweep references to `concept:worker-request` → `concept:node-run` across `concept:claim-handle`, `concept:auto-terminal`, `concept:orphan-reaper`, `concept:supervisor`, `concept:terminal-resolution`.

---

### Group E — Proto restructure

**Covers:** cross-layer #8 (`NodeExecutor` → `Executor` proto service), #9 (ExecuteEvent restructure + collapse + Snooze + lifecycle-handler 4→3), #12 (async-callback discriminator superseded by oneof), #15 (`GetCapabilities` → `Capabilities`).

#### E.1 — Rename proto service `NodeExecutor` → `Executor` (#8)

- `proto:executor.proto`: `service NodeExecutor` → `service Executor`. Wire-format-breaking; pre-v1; no consumer pin.
- Generated bindings refresh via `cmd:make proto-gen`. The codegen package emits `ExecutorServer`, `ExecutorClient`, etc. in `protocols/proto/v1/gen/`; the Go interface `code:protocols/executor/executor.go::Executor` lives in a different package (`protocols/executor/`) — no collision.
- Doc comments in `proto:executor.proto` that say "NodeExecutor is the protocol..." update to "Executor is the protocol...". Reference to `docs/history/2026-04-27-stores-redesign-v2-design.md §12` stays as-is (historical citation).

#### E.2 — ExecuteEvent restructure (#9)

The new `executor.proto::ExecuteEvent` shape:

```protobuf
message ExecuteEvent {
  oneof event {
    Heartbeat   heartbeat   = 1;
    NamedEvent  named_event = 2;
    StreamClose stream_close = 3;
  }
}

message StreamClose {
  oneof outcome {
    Success            success      = 1;
    Error              error        = 2;
    Snooze             snooze       = 3;
    AwaitAsyncCallback await_async  = 4;
  }
}

message Success {
  bool changed = 1;
  string change_summary = 2;
  google.protobuf.Struct attributes_delta = 3;
}

message Error {
  string error_class = 1;
  google.protobuf.Struct payload = 2;
}

message Snooze {
  string reason = 1;
  bytes payload = 2;
  google.protobuf.Timestamp resume_at = 3;
  string session_token = 4;
}

message AwaitAsyncCallback {
  string async_ack_id = 1;
  int64 expected_completion_ms = 2;
}

message AsyncCallbackBody {
  // Optional NamedEvent stream replayed before the outcome verdict.
  // Field number 1 stays reserved for events; oneof outcome fields
  // start at 2 to keep events numerically first.
  repeated NamedEvent events = 1;
  oneof outcome {
    Success success = 2;
    Error   error   = 3;
    Snooze  snooze  = 4;
    // No AwaitAsyncCallback — webhook is the second half; can't chain.
  }
}
```

**Payload-typing asymmetry between `Error` and `Snooze`** (intentional):

- `Error.payload` is `google.protobuf.Struct` — error diagnostics are structurally inert (`@blessed-invariant 21`) but rimsky may traverse them for transport (e.g., event-log persistence). `Struct` gives executors JSON-shaped diagnostic context for operators reading the audit log.
- `Snooze.payload` is `bytes` — preserves today's `ParkRequested.payload bytes` shape; the executor's resume material is byte-opaque to rimsky (per `@blessed-invariant 20`-style discipline), passed back verbatim on `ResumeContext.payload`.

The two payloads serve different roles: `Error.payload` is for operator-facing diagnostics; `Snooze.payload` is for executor session-resume material. The type asymmetry reflects this and matches both messages' existing predecessors (`Errored.payload Struct`, `ParkRequested.payload bytes`).

**Mapping from current shape:**

| Current message | New message | Notes |
|---|---|---|
| `Complete` | `Success` | Fields preserved (`changed`, `change_summary`, `attributes_delta`). |
| `Blocked` | `Error` (collapsed) | Today's `Blocked.reason` and `Blocked.context` collapse into `Error.error_class` + `Error.payload`. Executors emit `Error{error_class: "executor_blocked", ...}` explicitly. |
| `Errored` | `Error` | `error_class` preserved; `payload` preserved (renamed from `payload` — same name). |
| `AsyncAccepted` | `AwaitAsyncCallback` | Fields preserved (`async_ack_id`, `expected_completion_ms`). |
| `ParkRequested` | `Snooze` | Fields preserved (`reason`, `payload`, `resume_at`, `session_token`). |

**`Error` field shape decision** (locked in this brainstorm pass):

- Drop today's `Blocked.reason` field. `error_class` subsumes that role.
- Do NOT add a separate `error_message` field. Executors needing diagnostics put them in `payload`.
- Do NOT add `attributes_delta` to `Error`. Today's `Errored` doesn't carry it; adding it forces commit-time validation on the failure path. If partial-writeback-before-failure becomes desirable later, that's a separate design (and the incremental-callback path already handles it).

**Vocabulary split** (encoded in concept docs):

- "Terminal" is no longer a wire-protocol term. Wire-level name for "the last event on a reporting channel" is `StreamClose` (gRPC stream) or "the outcome" (HTTP webhook body).
- "Terminal" retains its state-machine / decision-engine meaning at `concept:terminal-resolution`, `code:foundation/integration/terminal_decision.go::*`, and `concept:node-state` ("terminal state" property — `failed`, `fresh`).

**Snooze rename, state-machine + concept impact:**

- Node state value: `'parked'` stays as the `col:rimsky_nodes.state` enum value (state-machine vocabulary stays consistent with state-machine history; `snoozing` would be a state-rename which the audit didn't lock in).
- **Decision (brainstorm sub-resolution, narrow):** the proto-level rename is `ParkRequested` → `Snooze`; the state-machine value `'parked'` and the concept slug `concept:parked-state` STAY (renamed only in proto, not in DB column or state machine).
  - Reasoning: the proto message describes an *event* (a request emitted by the executor to enter a hold). The DB column describes the resulting *state*. Decoupling them — event named `Snooze`, state named `parked` — is fine: the event causes the state transition. Renaming both compounds the blast radius without clarifying the model.
  - Concept doc `concept:parked-state.md` body updates to reflect the proto-level renames in event citations ("entered when the executor emits `proto:executor.proto::Snooze` — formerly `ParkRequested`").
- Concept slug `concept:parked-state`: keep as-is (audit's option to drop the `-state` suffix is not adopted; matches `concept:node-state` parent slug pattern).
- Functions on the runtime side keep `parked` naming: `code:foundation/integration/sweep_parked.go::SweepParkedNodes`, `runner_terminal_park.go`, the `wake_parked.go` helper, etc.

**Lifecycle-handler slot count drops from 4 to 3 (#9 collapse impact):**

- `on_executor_blocked` slot is removed. All error variants flow through `on_executor_errored` (the single error handler). Operator-declared `error_types:` policy discriminates by `error_class` (`executor_blocked`, `rate_limited`, `transient_io`, etc.).
- Concept doc `concept:lifecycle-handler.md` Boundaries + Invariants update: "four slots (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored`, plus the per-event `on_event` map)." Three lifecycle-handler slots plus `on_event` — total declarable handler entry points unchanged at 4.
- The "5 states / 4 lifecycle handlers + on_event" framing in `file:CLAUDE.md` "Vocabulary" section updates: "5 node states; 3 lifecycle handlers + `on_event` map."
- Code surface: `code:foundation/integration/runner_terminal_handlers.go::applyTerminalBlockedOrErrored` simplifies. Function rename: `applyTerminalBlockedOrErrored` → `applyTerminalError` (single path; no blocked-vs-errored fork). The "Blocked vs errored routing" decision logic is deleted.
- Resolves `tension:blocked-vs-errored-routing`. Re-resolves `tension:_resolved/handler-slot-count-drift` with the new shape.

**Implementer churn:** every executor implementation updates to the new shape — `code:executors/claude-agent/`, `code:executors/http-node/`, `code:executors/stub/`, conformance fixtures at `code:cmd/rimsky-executor-conformance/` (post-rename: `code:cmd/rimsky-executor-conformance/`), smoke fixture at `code:test/smoke/`. TS executor (`code:executors/claude-agent/`) has both the proto bindings refresh and the server-side handler rewrite; existing tests in `code:executors/claude-agent/src/server.test.ts` update.

#### E.3 — Async-callback discriminator superseded (#12)

- `tension:async-callback-body-key` is fully superseded by E.2's new `AsyncCallbackBody` shape. The new `oneof outcome` makes the field name itself the discriminator (protobuf-JSON encoding emits the active variant as a top-level key); no top-level `kind` or `type` key is needed.
- **Drop the legacy `{type: "complete"|"blocked"|"errored", ...}` parse-fallback** in the supervisor's chi route handler. New shape only; rejection produces a precise error: "expected `AsyncCallbackBody`; outcome `oneof` must be set." Pre-v1; no consumer pin.
- `col:rimsky_events.kind` (the internal audit-log column) stays as-is. No naming conflict between the internal column and the wire shape post-restructure.
- Resolves `tension:async-callback-body-key` (move to `tensions/_resolved/` with the supersession note).

#### E.4 — `GetCapabilities` → `Capabilities` (#15)

- Rename `proto:executor_observability.proto::ExecutorObservability.GetCapabilities` → `Capabilities`. Aligns with `proto:claim_producer.proto::ClaimProducer.Capabilities`. (File rename `executor_observability.proto` → `executor_observability.proto` stays; only the RPC rename inside.)
- After B.1's `StoreObservability` → `ClaimProducerObservability` proto-service rename and this RPC rename, both observability services have a `Capabilities` RPC: `ClaimProducerObservability.Capabilities` and `ExecutorObservability.Capabilities`. Symmetric.
- Hand-rolled Go interface methods rename to match:
  - `code:protocols/executor_observability/` (or equivalent — Go-side handshake interface) gets `Capabilities` method.
  - `code:modeling/observability/handshake.go` updates the discovery-cache populator call.
- **Do NOT introduce a shared `CapabilitiesProvider` Go interface.** The two Capabilities responses are protocol-specific (`ProducerCapabilities` vs `ExecutorCapabilities`); discovery-cache code that consumes them is already protocol-specific downstream. A generic `CapabilitiesProvider[C any]` would defer the type check by one frame without providing a shared call site.
- **Multi-protocol-binary pattern** (documented in `concept:service.md`, promoted in Group G below):
  - A binary implementing N rimsky protocols uses N handler types, one per protocol interface.
  - Method-name collisions across protocols (e.g., a binary that implements both `ClaimProducer` and `ExecutorObservability`, each with their own `Capabilities()` method with different signatures) are resolved at the composition site, not by interface unification. Each handler implements one interface; the binary registers them separately at the gRPC server (`gen.RegisterClaimProducerServer(s, p.producer); gen.RegisterExecutorObservabilityServer(s, p.execObs)`).
  - This matches Go's per-service-handler convention and gRPC's per-service registration model. Equivalent to the C++ multiple-inheritance + disambiguate-at-call-site pattern but expressed through composition rather than method-resolution.

Executor implementations sweep: `code:executors/claude-agent/`, `code:executors/http-node/`, `code:executors/stub/`. `file:CLAUDE.md` "4 verbs + Capabilities() startup handshake" framing already matches; no further doc sweep needed for that phrase.

---

### Group F — Cascade vocabulary

**Covers:** cross-layer #10.

Adopt a three-word vocabulary inside the `concept:cascade` umbrella:

| Word | Meaning | Implementation site |
|---|---|---|
| **walk** | Scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism. | `code:foundation/integration/conductor.go::tick` (or equivalent) |
| **propagation** | Cascade-of-stale on `fresh_changed`; mark dependents stale and recurse. | `code:foundation/integration/cascade_invalidate.go::InvalidateNode` (handler for `concept:invalidate`) |
| **fallthrough** | No-dispatch fresh-roll on `pure_cascade`; roll fresh state forward without running the node. | Per-node detection in `code:foundation/integration/cascade_recalculate.go::RecalculateNode`; executed by the scheduler's pure-cascade sweep |

- `concept:cascade.md` body rewrites to use the three-word vocabulary uniformly. The current "two walks" framing dissolves into "one walk; two node-level behaviors (propagation, fallthrough)."
- No concept splitting: keep `concept:cascade` as the single umbrella.
- No file renames: `cascade_invalidate.go` and `cascade_recalculate.go` correctly describe their entry messages (invalidate, recalculate). Renaming to `cascade_propagation.go` / `cascade_fallthrough.go` would obscure that mapping.
- Doc-comment refresh: places in source files where the prose currently says "cascade" ambiguously update to the precise three-word vocabulary. Sweep `foundation/cascade/`, `foundation/integration/cascade_*`, and `concept:cascade.md` Adjacent concepts (`concept:invalidate.md`, `concept:last-outcome.md`, `concept:transition-reason.md`) for ambiguous "cascade" usage.
- Resolves `tension:cascade-walks-overloaded` (move to `tensions/_resolved/`).

---

### Group G — Concept-doc reorganization

**Covers:** cross-layer #16 (drop `concept:held-claim`), #17 (`concept:opacity` → `concept:inertness`; reinforce `concept:userdata`), #18 (peer → service vocabulary; promote `concept:service`).

#### G.1 — Drop `concept:held-claim`; fold content into `concept:claim-handle` (#16)

- Delete `file:.ok-planner/design/concepts/held-claim.md`.
- `concept:claim-handle.md` gains:
  - A **"Held variant" subsection** describing `col:rimsky_claim_handles.is_held = TRUE`, what the column means, and per-member state tracked in `table:rimsky_claim_holders`.
  - An **"Authoring: held vs unheld" subsection** describing how a template declares inheritors → the claim becomes held through them.
- `concept:auto-terminal.md` stays as the runtime mechanism for held-resolution (no content move; verify cross-links updated).
- Sweep concept-doc Adjacent lists across other concepts that reference `held-claim` → point at `concept:claim-handle#held-variant` or `concept:auto-terminal` depending on context.
- Net concept count: −1 (offset later by +1 in G.3 — net 46).

#### G.2 — `concept:opacity` → `concept:inertness`; reinforce `concept:userdata` (#17)

- Rename file: `file:.ok-planner/design/concepts/opacity.md` → `file:.ok-planner/design/concepts/inertness.md`.
- New body describes **two sub-disciplines**:
  - **Byte-opaque inertness** — rimsky never traverses (claim scope/address/payload, blob bytes).
  - **Structural inertness** — rimsky may traverse for transport mechanics but doesn't inspect values (userdata, attribute values, named-event payloads).
- Reword `@blessed-invariant 11` text from "Userdata is opaque to rimsky" → "Userdata is inert in Rimsky" (aligns with `@blessed-invariant 20` and `21` framing). Invariant's discipline is unchanged; only wording.
- Sweep `code:foundation/persistence/blob.go::BlobBackend` annotation comment to use "inert" language consistently.
- `concept:userdata.md` keeps its first-class status (already a concept). Reinforce purpose: an escape-hatch for executor-specific config that rimsky should not need to learn about (synthetic-blocker scenarios, per-run trace artifacts, ad-hoc tuning, per-instance `userdata_overrides`). The inertness discipline is cross-linked to `concept:inertness`.
- Cross-link bidirectionally: `concept:inertness` lists `concept:userdata`, `concept:claim`, `concept:blob-backend`, `concept:named-event`, `concept:attribute` in Adjacent; each of those concepts lists `concept:inertness` in Adjacent.
- Net concept count: rename only (no add/drop).

#### G.3 — "peer" → "service"; promote `concept:service` (#18)

- Sweep "peer" → "service" across:
  - `file:CLAUDE.md` (six occurrences plus prose context).
  - `file:docs/glossary.md` (two occurrences).
  - Concept doc bodies (10+ concepts: `concept:claim-producer`, `concept:executor`, `concept:cascade-graph`, `concept:discovery-cache`, `concept:control-api`, `concept:invalidate`, `concept:conformance`, `concept:observability`, `concept:lifecycle-subscriber`, `concept:rimsky-yml`).
  - Go-code variable / function names containing "peer" (sweep with grep; rename to "service" where the noun is the load-bearing role; leave alone if "peer" is correct in context, e.g., peer-to-peer networking).
- Add new concept doc `file:.ok-planner/design/concepts/service.md`:
  - **Definition:** an out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky.
  - **Purpose:** extensibility (third-party implementations) and modularity (reference impls separate from rimsky core).
  - **Boundaries:** specific protocols (`concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:blob-backend`) are sibling concepts. Orchestration mechanics (dispatch, acquisition, supervisor coordination) live in their own concepts (`concept:supervisor`, `concept:terminal-resolution`, etc.). This concept owns: how a binary declares its protocol membership in `cfg:rimsky.yml`, the `Capabilities` startup handshake (post-E.4), conformance-validation entry points, and the multi-protocol composition pattern (from E.4).
  - **Invariants:** declared in `cfg:rimsky.yml` with explicit `protocols: [...]` list per service; protocol membership advertised via the per-protocol `Capabilities` RPC at startup; per-protocol conformance binary validates compliance; multi-protocol binaries use distinct Go handler types per protocol.
  - **Adjacent:** `executor`, `claim-producer`, `lifecycle-subscriber`, `blob-backend`, `rimsky-yml`, `conformance`, `observability`, `discovery-cache`.
- **YAML stays as-is:** `cfg:rimsky.yml` keeps protocol-specific blocks (`claim_producers:`, `executors:`). No `services:` block; the umbrella concept describes the category, but operator config disambiguates by protocol.
- Net concept count: +1.

**Net concept count delta across G.1, G.2, G.3:** −1 + 0 + 1 = 0. End count: 46.

---

### Group H — Layer reorganization

**Covers:** cross-layer #19.

**Two-way split.** Rename root-module `modeling/` → `graph/`; create new `control/` sibling under the root module. Single Go module preserved (no new `go.mod`).

**Layer contents:**

| New location | From `modeling/` | Concept ownership |
|---|---|---|
| `graph/template/` | `modeling/template/` | `concept:template` |
| `graph/node/` | `modeling/node/` | `concept:node`, `concept:lifecycle-handler`, `concept:on-event-handler`, `concept:error-policy` |
| `graph/instance/` | (currently inside `modeling/`) | `concept:instance` |
| `graph/frame/` | `modeling/frame/` | `concept:frame` |
| `graph/scheduler/` | `modeling/scheduler/` | `concept:schedule` |
| `graph/attribute/` | `modeling/attribute/` | `concept:attribute`, `concept:userdata` |
| `graph/qualityrule/` | `modeling/qualityrule/` | `concept:quality-rule` |
| `graph/shared/` | `modeling/shared/` | (shared helpers internal to graph) |
| `graph/scenario/` | `modeling/scenario/` | (test fixture) |
| `graph/internal/pgtest/` | `modeling/internal/pgtest/` | (test infra) |
| `control/controlapi/` | `modeling/controlapi/` | `concept:control-api`, `concept:cascade-graph` |
| `control/cli/` | `modeling/cli/` | `concept:rimsky-cli` |
| `control/observability/` | `modeling/observability/` | `concept:observability`, `concept:discovery-cache` |
| `control/config/` | `modeling/config/` | `concept:rimsky-yml` |

`concept:tag` (movable string alias) stays inside `graph/template/` since tags are template-scoped.

**Boundary contract** (small, clean — satisfies the "operated on independently" bar):

- `control → graph`: read access via `persistence.Driver` queries; small mutation set (create instance, force-fire schedule, register/deploy/undeploy template). All mutations go through hand-rolled service interfaces in `graph/`.
- `graph → control`: zero. Graph never calls into control. Control is one-way (operator → rimsky).

**Why two-way and not three-way (graph / runtime / control):** every node-spec field is consumed at runtime; every runtime decision reads graph state; `rimsky_nodes` carries both definition and runtime state in the same row. A genuine runtime layer would require lifting runtime mechanics out of foundation (which already owns supervisor dispatch + terminal handling + orphan reapers + cascade engine) — a much larger structural pass worth its own design effort, not this audit.

**Depguard rules** (`file:.golangci.yml`) update — explicit allow/deny shape so execute-plan can derive them mechanically:

- **New `graph-control-isolation` rule:** packages under `graph/` are DENIED from importing `control/...`. (Allow-rule shape: `graph/` is permitted to import `foundation/...`, `protocols/...`, stdlib, and other `graph/...` packages.)
- **Reciprocal allowance:** packages under `control/` ARE permitted to import `graph/...` (one-way: operator → rimsky reads graph state). No new rule needed for this direction — it is the default permission, just not denied.
- **Existing `foundation-internal-isolation` rule unchanged** (only `foundation/` may import `foundation/internal/...`).
- **Existing `pgx-isolation` rule's allowed-paths list updates:** `modeling/internal/pgtest/` → `graph/internal/pgtest/`; `modeling/scenario/` → `graph/scenario/`. Paths in `cmd/`, `stores/`, `test/smoke/`, and `foundation/persistence/postgres/` unchanged.
- **New entries in the allow lists** (if applicable to the depguard format used): `control/controlapi/`, `control/cli/`, `control/observability/`, `control/config/` permitted as importers of `graph/...` symbols where they need to read graph state.

**Import-path renames** sweep the root module. Touches:

- Every Go file under `modeling/` (rename via directory move).
- Every import statement across `cmd/`, `stores/`, `executors/`, `test/`, `mcp-servers/` referencing `github.com/fallguyconsulting/rimsky/modeling/...`.
- `file:go.work` (no change — single root module covers both).
- `file:.golangci.yml` depguard rules.
- `file:CLAUDE.md` "Package import rules" section.
- Concept doc layer headings — the audit's "Layer 3: `modeling/`" becomes "Layer 3: `graph/`" + "Layer 4: `control/`" (renumber subsequent layers if any).

**Concept-doc layer organization** in `file:.ok-planner/design/concepts.md` (auto-generated TOC) and any layer-grouped reference docs: regenerate post-rename.

**Net structural change:** the three-Go-module architecture is preserved (`foundation/`, `protocols/`, root `github.com/fallguyconsulting/rimsky`). What changes is the root module's internal directory organization. Concept count stays at 46.

---

### Group I — Ride-along renames (concept-level decisions outside the 19 cross-layer)

These were decided in the same walkthrough but listed under per-concept sections of the audit. Sharing the rename touch surface; included here for execution under the same spec.

#### I.1 — Conformance binary rename (concept:conformance)

- Rename `code:cmd/rimsky-executor-conformance/main.go` → `code:cmd/rimsky-executor-conformance/main.go`.
- All four conformance binaries now follow the pattern `rimsky-<protocol>-conformance`:
  - `rimsky-executor-conformance` (renamed)
  - `rimsky-claim-producer-conformance` (no change)
  - `rimsky-blob-backend-conformance` (no change)
- The probe sidecar `rimsky-conformance-probe` stays generically named (not protocol-specific; could plausibly extend to other protocols in the future).
- Touches: `file:Makefile` build targets, `file:deploy/docker-compose.yml` references (if any), CI workflows, `file:CLAUDE.md` "Build & test" section, `concept:conformance.md` body.
- Resolves `tension:executor-conformance-binary-asymmetry` (a new tension surfaced during the walkthrough; mark resolved on creation rather than opening then resolving).

#### I.2 — Error-policy code-surface rename (concept:error-policy)

- File: `foundation/integration/runner_terminal_errors.go` → `foundation/integration/runner_error_policy.go`.
- Function: `applyTerminalAppError` → `applyErrorPolicy`. (After Group E.2's simplification — the `applyTerminalBlockedOrErrored` → `applyTerminalError` collapse — the error-policy application becomes the sole error path.)
- `concept:error-policy.md` body: document the three-name relationship — "The design-log noun is `error-policy`; the operator-facing YAML field is `error_types:` (map of error_class → action); the implementation lives in `code:foundation/integration/runner_error_policy.go::applyErrorPolicy`."
- Keep `error_types:` as the YAML field name (descriptive of the map shape; `error_policy:` would imply "one policy" but the field is a map).
- Resolves `tension:error-action-count-drift` indirectly by being explicit about the four actions (`retry` / `invalidate` / `give_up` / `pass`) post-rename in the concept doc; the audit deferred this to brainstorm and the answer is "yes, resolves" — move to `tensions/_resolved/`.

---

## Dependencies (high-level)

This section describes inter-decision dependencies. PR/commit/atomicity decisions belong in the write-plan / execute-plan pass.

- **Group A (migration baseline rebase)** is foundational. Schema portions of Groups D (frame_resolution_mode column rename, code residue tied to node-runs naming), G (concept-doc table-name updates), and H (no schema impact) depend on the baseline being in place.
- **Group E (proto restructure)** is independent of Group A. Touches Go-side executor implementations, TS claude-agent, conformance fixtures, smoke fixtures.
- **Group B (Foundation/protocol vocabulary)** is mostly independent. B.1 + B.2 interact (both touch the noun "Store"); they coordinate to land together so the noun has one meaning after both.
- **Group C (YAML cleanup)** depends on Group E.4 (proto field rename `write_semantics_envelope` → `write_semantics_allowed`).
- **Group F (cascade vocabulary)** is doc/comment only; independent of all others.
- **Group G (concept-doc reorganization)** depends on Group B + D + E renames landing first so concept-doc citations point at the renamed artifacts.
- **Group H (layer reorganization)** is the largest structural pass. Ideally lands after all other groups so import paths only churn once.
- **Group I (ride-along renames)** is independent and small; I.1 touches binaries/build, I.2 depends on Group E.2's lifecycle-handler simplification.

The write-plan pass should expect ordering roughly along these dependency lines, with allowances for atomicity boundaries (e.g., proto regen + executor impl updates likely land together; baseline rebase + code-side schema-rename sweep likely land together).

---

## Testing strategy

End-state must pass:

- `cmd:make build-all` — all three Go modules build.
- `cmd:make test-all` — all three Go modules test, including:
  - Scenario tests in `code:test/scenarios/...` (testcontainers Postgres; require Docker).
  - Storage tests in `code:foundation/persistence/...` (Postgres + SQLite drivers; schema-equivalence between baselines).
  - Concept-doc invariant tests (if any sweep over the design log).
- `cmd:make lint` — golangci-lint clean, depguard rules updated for Group H.
- TypeScript executor: `cd executors/claude-agent && npm install && npm test && npm run build`. Verify new `AsyncCallbackBody` shape end-to-end with the supervisor.
- Conformance:
  - `cmd:rimsky-executor-conformance --transport grpc --endpoint <stub>` against the post-rename stub executor.
  - `cmd:rimsky-claim-producer-conformance --endpoint <stub>` against the stub producer.
  - `cmd:rimsky-blob-backend-conformance` against the in-memory / filesystem / pg-largeobject backends.
- Deploy smoke: `cmd:docker compose -f deploy/docker-compose.yml up -d && curl http://localhost:8080/health` passes against the unified `rimsky/all` image rebuilt with the new shape.

**Migration-baseline-specific test:**

- Verify the post-baseline schema is byte-equivalent (modulo whitespace / comment order) across Postgres and SQLite within the conformance harness.
- Verify a fresh `cmd:rimsky-migrate` run on an empty database produces the baseline schema without errors.
- Verify the dev-reset documented in CHANGELOG (`DROP SCHEMA public CASCADE; CREATE SCHEMA public;` then `cmd:rimsky-migrate`) produces a working dev environment.

**Vocabulary-lint regression test:**

- `code:cmd/rimsky-docs-lint/vocabulary_test.go` fixture entries verified for relevance against post-rename codebase. Entries that were guarding against regression to terms now fully erased (e.g., `rimsky_dispatch` if no occurrence remains) update or remain — verify each is still active. New fixture entries for terms newly retired in this spec: `Store` (foundation alias context), `stores:` (YAML), `write_semantics:` (single-value form), `frame_resolution:` (template-author YAML), `worker_request`, `NodeExecutor`, `GetCapabilities`, `opacity` (in `@blessed-invariant 11` context), `peer` (in load-bearing prose contexts).

**Wire-break verification:**

- Any client running against the old proto wire shape errors at the gRPC layer (no fallback). Verify by attempting a request from a stale client; precise error message documented.
- Any operator config using `stores:` or `write_semantics:` (single value) fails YAML validation at startup with a precise error. Verify by attempting startup with a stale config.

---

## Out of scope

Tensions deferred to later passes (not covered by this spec):

- `tension:control-api-version-prefix` — separate concern; out of nomenclature scope. Re-evaluate when API versioning policy is designed.
- `tension:stub-mode-runtime-only-gate`, `tension:blob-backend-conformance-fixture-asymmetry`, `tension:stub-mode-signature-no-proto-surface` — conformance-side; out of nomenclature scope. The conformance binary rename (Group I.1) is included; deeper conformance restructure is not.
- `tension:force-fire-204-hides-asynchrony`, `tension:coalesced-fire-observability-gap` — observability concerns; not in the nomenclature theme.
- `tension:quality-rule-severity-string-footgun`, `tension:quality-rule-custom-handler-ordering` — quality-rule-specific; separate.
- `tension:compose-prefix-client-side` — CLI-policy concern; not in nomenclature scope.
- `tension:timeout-policy-asymmetry`, `tension:heartbeat-cutoff-asymmetry`, `tension:reaper-vs-bail-abandon-asymmetry` — runtime-semantics concerns; not in nomenclature scope.
- `tension:pre-v1-hash-instability`, `tension:events-kind-no-enum`, `tension:serial-queue-per-instance`, `tension:frame-lookup-on-every-enqueue`, `tension:state-count-drift`, `tension:substitution-grammar-count-drift`, `tension:substitution-introspection-site-count`, `tension:sqlite-vs-memory-reject-asymmetry`, `tension:callback-hostname-split`, `tension:userdata-schema-as-opacity-exception` — various; outside the nomenclature theme. Re-evaluate independently.

Other explicit out-of-scope items:

- Adding new operator-facing routes beyond `/node-runs` (e.g., `/frames`). Not needed for this spec; future dashboard work will add what's needed when needed.
- Foundation-vs-runtime layer split. Audit's #19 explicitly chose two-way (graph + control) over three-way; the runtime-extraction option is a larger separate design.
- Sub-protocol concept additions (e.g., `concept:resume`, `concept:heartbeat`, `concept:cancellation`) — could be discovered later but not part of the nomenclature pass.
- Cross-language SDK surface (Python, Rust, etc.). The TS claude-agent updates are required because it ships in-tree; other-language SDKs are out of scope for this repo.

---

## Tensions resolved

The design-log impacts are exhaustively recorded in the decision groups above. Execute-plan applies the recorded mutations during the implementation pass. Tension files move from `file:.ok-planner/design/tensions/<slug>.md` to `file:.ok-planner/design/tensions/_resolved/<slug>.md` with a one-line resolution note added at the top citing this spec slug (`spec:2026-05-12-nomenclature-resolution`).

| Tension | Resolved by group |
|---|---|
| `tension:store-vs-claim-producer-vocabulary` | B.1, C |
| `tension:yaml-stores-alias` | B.1, C |
| `tension:yaml-write-semantics-alias` | C |
| `tension:frame-resolution-vs-mode-vocabulary` | D.1 |
| `tension:lock-holder-vs-claim-handle-legacy` | A, D.2 |
| `tension:consumer-key-vs-instance-key` | A |
| `tension:region-vs-scope-legacy` | A, B.3 |
| `tension:terminal-event-overloaded` | E.2 |
| `tension:cascade-walks-overloaded` | F |
| `tension:transition-reason-vs-last-outcome` | B.4 |
| `tension:blocked-vs-errored-routing` | E.2 (collapse) |
| `tension:async-callback-body-key` | E.3 (superseded) |
| `tension:error-action-count-drift` | I.2 |
| `tension:executor-conformance-binary-asymmetry` | I.1 (new-and-resolved in same spec) |

### Concept-doc mutations (recorded for execute-plan to apply)

**Concept files modified in body** (no rename):

- `concept:cascade.md` — three-word vocabulary rewrite (Group F).
- `concept:claim-handle.md` — gains "Held variant" + "Authoring: held vs unheld" subsections; sweep references to renamed table `rimsky_claim_handles`, Adjacent updates after `held-claim` drop (G.1).
- `concept:error-policy.md` — three-name relationship subsection; rename citations (I.2).
- `concept:frame.md` — `frame_resolution_mode` canonicalization (D.1).
- `concept:lifecycle-handler.md` — 4-to-3 slot count update (E.2).
- `concept:node-state.md` — sweep references and Adjacent updates.
- `concept:last-outcome.md` — Relationship to `transition-reason` subsection (B.4).
- `concept:transition-reason.md` — Relationship to `last-outcome` subsection (B.4).
- `concept:opacity.md` → renamed to `concept:inertness.md` with full body rewrite (G.2).
- `concept:userdata.md` — Purpose subsection reinforcement; inertness cross-link (G.2).
- `concept:claim-producer.md` — `Store` alias retirement notes; `StoreObservability` → `ClaimProducerObservability` proto rename citations (B.1).
- `concept:claim.md` — `ClaimSpec.StoreName` → `.ProducerName` citation (B.1).
- `concept:persistence-driver.md` — `Store` → `Driver` rename; file rename; row-struct singular convention (B.2, A).
- `concept:scope.md` — region-legacy fully erased (B.3).
- `concept:write-semantics.md` — `write_semantics_envelope` → `write_semantics_allowed` (C).
- `concept:executor.md` — `NodeExecutor` → `Executor` proto-service rename; capabilities harmonization note (E.1, E.4).
- `concept:observability.md` — `StoreObservability` → `ClaimProducerObservability`; capabilities harmonization (B.1, E.4).
- `concept:conformance.md` — binary rename to `rimsky-executor-conformance` (I.1).
- `concept:control-api.md` — `/dispatches` → `/node-runs`; layer-reorg citations (D.3, H).
- `concept:cascade-graph.md` — route rename (D.3); layer-reorg citations (H).
- `concept:orphan-reaper.md` — sweep-function renames (D.2).
- `concept:supervisor.md` — `Store = ClaimProducer` alias removal citations (B.1); `worker_request` → `node_run` references (D.3).
- `concept:parked-state.md` — `ParkRequested` → `Snooze` proto-event rename; keep `parked` state value (E.2).
- `concept:auto-terminal.md` — `claim_handle` → `claim_handles` plural; `held-claim` Adjacent removal (G.1).
- `concept:named-event.md` — `NodeExecutor` → `Executor` proto-service rename citation (E.1).
- `concept:advisory-lock.md` — `Store` → `Driver` rename citations (B.2).
- `concept:blob-backend.md` — inertness cross-link (G.2).
- `concept:module-layout.md` — `modeling/` → `graph/` + `control/` split (H); depguard rule updates.
- `concept:rimsky-yml.md` — `stores:` alias retirement; `write_semantics_envelope` → `_allowed` (C); `protocols:` block clarification (G.3).
- `concept:rimsky-cli.md` — `/dispatches` → `/node-runs` route citation if any; layer-reorg path updates (H).
- `concept:invalidate.md` — "peer" → "service" sweep (G.3).
- `concept:lifecycle-subscriber.md` — "peer" → "service" sweep (G.3).
- `concept:event-log.md` — no change (kind column stays).
- `concept:discovery-cache.md` — capabilities harmonization (E.4); "peer" → "service" sweep (G.3).
- `concept:tag.md` — layer-reorg path updates (H).
- `concept:template.md` — `FrameResolution` → `FrameResolutionMode` (D.1); layer-reorg path updates (H).
- `concept:instance.md` — layer-reorg path updates (H).
- `concept:node.md` — layer-reorg path updates (H).
- `concept:attribute.md` — inertness cross-link (G.2); layer-reorg path updates (H).
- `concept:schedule.md` — layer-reorg path updates (H).
- `concept:quality-rule.md` — layer-reorg path updates (H).
- `concept:on-event-handler.md` — slot-count framing carryover; layer-reorg path updates (E.2, H).
- `concept:terminal-resolution.md` — terminal-vocabulary clarification (E.2 "terminal" stays for state-machine sense only).
- `concept:named-lock.md` — no body change expected.

**Concept files renamed:**

- `concept:opacity` → `concept:inertness` (G.2).
- `concept:worker-request` → `concept:node-run` (D.3).

**Concept files deleted:**

- `concept:held-claim` (content folded into `concept:claim-handle`) (G.1).

**Concept files added:**

- `concept:service` (umbrella for out-of-process gRPC services) (G.3).

**Concepts.md TOC:** regenerate after the renames/adds/drops settle.

**Tension files moved to `tensions/_resolved/`** with one-line resolution notes citing this spec:

All 14 tensions in the "Tensions resolved" table above.

**Tension files unchanged** (remain open in `tensions/`):

All tensions listed in "Out of scope" above.
