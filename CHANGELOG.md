# Changelog

## Unreleased

### Claude-agent: per-template CLI tuning via `userdata.cli`

Template authors can now configure how the Claude Code subprocess is invoked, on a per-node basis, without touching the executor's source. The previous spawn-args block was hardcoded; only `--model` (via `userdata.model`) and `--max-budget-usd` (via the `RIMSKY_DISPATCH_MAX_USD` env var) were configurable.

The new `userdata.cli` sub-object is the namespace for executor-config concerns — distinct from the agent-facing `userdata` fields (`system_prompt`, `model`, `user_prompt_template`, `cwd_from_store`) that drive what the agent does. `userdata.cli.*` controls how the executor invokes its CLI:

| `userdata.cli.*` field | Type | Default | Maps to |
|---|---|---|---|
| `bare` | bool | `false` | `--bare` (skips auto-memory, hooks, LSP, plugin sync, keychain reads, CLAUDE.md auto-discovery; sets `CLAUDE_CODE_SIMPLE=1`. Note: forces ANTHROPIC_API_KEY-only auth — OAuth is unread.) |
| `permission_mode` | string | `"bypassPermissions"` | `--permission-mode <mode>` |
| `allowed_tools` | string[] | (none) | `--allowedTools <space-separated list>` |
| `disallowed_tools` | string[] | (none) | `--disallowedTools <space-separated list>` |
| `add_dirs` | string[] | (none) | `--add-dir <path1> <path2> …` (forwarded as separate argv tokens, not a joined string) |
| `max_budget_usd` | string | (`RIMSKY_DISPATCH_MAX_USD` env var fallback) | `--max-budget-usd <amount>` |

Defaults preserve current behavior, so existing templates are unaffected. `max_budget_usd` retains its env-var fallback for deployment-wide caps; the per-template value wins when set.

Why `userdata.cli` rather than a first-class executor-protocol field: the protocol's `userdata google.protobuf.Struct` is intentionally opaque to rimsky (blessed-invariant 11). Different executors have different CLI surfaces — a hypothetical Python-eval executor would have different knobs (`--interpreter-version`, `--isolated`, etc.). Modeling these as first-class proto fields would either bloat the protocol or collapse into "userdata renamed." Keeping it in userdata + namespacing the sub-object preserves the executor-author convention and gives template authors a single place to look.

- New `buildClaudeCliArgs(req, paths)` exported from `cli-runner.ts` — pure function that composes the argv from a `CliSpawnRequest`. Tested directly with 11 cases covering each new field plus ordering.
- `CliSpawnRequest` extended with optional `bare`, `permissionMode`, `allowedTools`, `disallowedTools`, `addDirs`, `maxBudgetUsd` fields.
- New `parseCliConfig(userdata.cli)` in both `server.ts` (gRPC) and `http-bridge.ts` (HTTP). Strict type validation: non-boolean `bare` is silently dropped to undefined; non-string-array entries in the list fields are filtered; empty results return `undefined` so the executor's defaults take effect.
- `runAgent`'s `RunArgs` interface gains a `cliConfig` field threaded through to `cliRunner.spawn`.

### Foundation: supervisor heartbeat tick is DB-driven, fixes async-dispatch heartbeat-loss

Pre-fix, `Supervisor.doHeartbeat` refreshed `rimsky_nodes.last_heartbeat_at` only for entries in an in-memory `activeNodes` map. The map was populated *after* `RunNode` returned — fine for sync executor paths, but for async dispatches (e.g. the bundled claude-agent emitting `AsyncAccepted`) `RunNode` returns within milliseconds while the actual work continues on the executor side. The node never entered the tracking set, no node-level heartbeat fired during the async run, and after `HeartbeatTimeout` (default 15s) the scheduler's `SweepStaleHeartbeats` would mark the running node `stale` and the orphan reaper would yank locks from a perfectly healthy in-flight Claude run.

The fix removes the in-memory tracking entirely and reads the source of truth (the DB) on each heartbeat tick: the supervisor selects every row in `state='running' AND assigned_supervisor_id = $self` and refreshes `last_heartbeat_at` for each. Self-healing — agnostic to sync vs async, robust to any future "RunNode returns early" path.

- New `persistence.NodeStore.ListRunningBySupervisor(ctx, supervisorID, tx) ([]NodeRow, error)`. Implemented in both bundled drivers (`foundation/persistence/postgres/nodes.go`, `foundation/persistence/sqlite/nodes.go`). Adds the supervisor predicate to the existing `state='running'` filter.
- `foundation/integration/supervisor.go::doHeartbeat` switches from iterating an in-memory map to calling the new query. The `activeNodes` map and the `register-on-RunNode-return / delete-on-goroutine-exit` bookkeeping in the goroutine are removed.
- **`rimsky_supervisors.active_node_count` now reflects in-flight nodes (the same DB query) rather than dispatching goroutines.** Pre-fix, the column was written from `activeCount` — the goroutine counter the concurrency guard uses. Async dispatches whose goroutines had already returned (post-AsyncAccepted) were silently excluded, so the column under-counted real concurrency. The goroutine counter remains as `activeCount` for the concurrency guard and the shutdown drain wait — those are correctly per-goroutine concerns. The schema column's name (`active_node_count`) now matches its data.
- New conformance test `NodesListRunningBySupervisor` (`foundation/persistence/conformance/nodes_list_running_by_supervisor.go`) — runs against both postgres and sqlite drivers. Seeds four sibling nodes (running-self, running-other, stale-self, running-unassigned) and asserts the query's predicate (`state='running' AND assigned_supervisor_id = $1`) is implemented identically across drivers.
- `TestStoreMethodsRejectNilTx` (sqlite) extended with `Nodes.ListRunningBySupervisor`.

### Claude-agent: compatibility with current Claude Code CLI (2.1.x) and per-dispatch MCP

The `executors/claude-agent` reference impl was no longer wire-compatible with the Claude Code CLI it spawns. End-to-end runs through the executor surfaced six distinct gaps; all are fixed here. Verified end-to-end against Claude Code 2.1.132 with both `claude-haiku-4-5` and `claude-sonnet-4-6` against a downstream consumer's docs-pipeline (single-area smoke; both `area-pass` and `consolidate` nodes complete cleanly via `report_complete` MCP tool, attributes commit, locks release with `action: release`).

- **MCP server now uses `@modelcontextprotocol/sdk` Streamable-HTTP transport.** The previous v1 implementation was a hand-rolled JSON-RPC endpoint that handled only `tools/list` + `tools/call` — Claude Code's MCP-HTTP client requires the full MCP `initialize` handshake and silently skips servers that don't speak it. `internal-mcp-server.ts` now constructs an `McpServer` and wires it through `StreamableHTTPServerTransport`. Tool registration moved to a separate exported `registerTools(mcp, registry, log)` seam that mirrors brain's `registerTopicTools` test seam (`skillprompting/brain/src/mcp-topic-server.ts`).
- **Per-dispatch MCP server lifecycle.** The single shared MCP server in `main.ts` was found to mishandle multi-spawn lifecycles — a second dispatch's CLI silently lost visibility of the rimsky-callback tools after the first dispatch's session ended. `agent-run.ts` now starts a fresh internal MCP server per `runAgent` invocation and tears it down in the `finally` block. Mirrors brain's per-spawn pattern (`startTopicMcpServer` per session). The legacy `callback` parameter on `RunArgs` is preserved for back-compat but its `url` / `registry` are unused by the run.
- **Per-run callback token now substituted into prompts.** A new `{{callback_token}}` placeholder is rendered in the system / user prompt so the agent receives the token without needing shell access to `$RIMSKY_CALLBACK_TOKEN`. `renderTemplate` extended to accept an optional `callback_token` field; existing `{{userdata.x}}` / `{{attributes.x}}` substitutions unchanged. The env var is still set on the child for tools that DO have shell.
- **Spawn args aligned with brain (`judging-orchestrator.ts:197-207`).** Previously: `claude --model X --system-prompt-file Y --mcp-config Z` with `userPrompt` written to stdin. Now: `claude --print --model X --permission-mode bypassPermissions [--max-budget-usd $] --system-prompt-file Y --mcp-config Z -p <userPrompt>` with `stdio[0] = "ignore"`. The CLI requires `--print` for non-interactive operation, `--permission-mode bypassPermissions` for both `Edit/Write` AND MCP tool calls (`acceptEdits` is too narrow — it gates MCP), and the prompt as a positional `-p <prompt>` rather than via stdin.
- **Optional per-dispatch token-spend ceiling.** `RIMSKY_DISPATCH_MAX_USD` env var on the executor service plumbs through to `claude --max-budget-usd`. When set, the CLI ends a run that exceeds the ceiling instead of letting it spiral. Useful for smoke tests and per-area spend caps in production.
- **`@anthropic-ai/claude-code` installed in the executor image.** The Dockerfile (`deploy/Dockerfile.claude-agent`) previously built only the executor's gRPC wrapper, leaving `claude` not in `$PATH`. `cli-runner.ts` defaults to spawning `"claude"` from PATH, so every dispatch failed instantly with `subprocess_exit_before_complete`. Added `RUN npm install -g @anthropic-ai/claude-code` (and `apk add --no-cache git` since some Claude CLI flows shell out to git).
- **`google.protobuf.Struct` shape unwrapped at the gRPC / HTTP boundary.** With `@grpc/proto-loader`'s default options (`keepCase / longs:String / enums:String / defaults:true / oneofs:true`), `userdata` and `StoreHandle.handle` arrive as `{fields: {<key>: {kind, stringValue, ...}}}` — `userdata.user_prompt_template` was undefined because the actual value lived at `userdata.fields.user_prompt_template.stringValue`. Added `unwrapStruct` / `unwrapStructValue` / `unwrapStores` in `server.ts` and `http-bridge.ts` (intentionally duplicated, `@source`-annotated per the cold-read tracked-duplication convention).
- **CLI subprocess stdout/stderr now logged.** `agent-run.ts` previously discarded child output silently — debugging required `docker compose exec` into the running container. Stdout chunks log at `info` (`cli.stdout`) and stderr at `warn` (`cli.stderr`) with the `runId` / `node_id` / `dispatch_id` for trace correlation. Capped at 2000 chars/chunk to bound log volume.
- **Tests rewritten to use the SDK Client over `InMemoryTransport`** instead of probing the bare HTTP endpoint with `fetch()`. Mirrors brain's `mcp-topic-server_test.ts` pattern. `registerTools` is now exported as the test seam.

### Foundation: store-handle wire map keyed by Alias, not StoreName

`buildStoreHandles` (`foundation/integration/runner_dispatch.go::buildStoreHandles`) keyed the `map[string]*StoreHandle` by `ClaimSpec.StoreName`. A node with two store entries on the same store name (e.g. a `consolidate` node holding both `@consolidate-queue` aliased `doc` and `@guidance-root` aliased `root`, both on the `content` store) saw the second handle silently overwrite the first — the executor then failed to look up `stores["doc"]` because only `stores["content"]` existed. Switched to keying by `ClaimSpec.Alias` (defaulting to `StoreName` when empty), matching the alias-keyed lookup the executor already does for `cwd_from_store: <alias>` and the alias-keyed claim map the modeling-layer attribute substitution uses (`modeling/attribute/substitution.go::resolveClaim`). Existing single-store-per-node templates are unaffected (alias defaults to StoreName).

### Filesystem store: pick policy may root at the store root itself

`PickPolicy.Root` may now be empty (`root: ""` in YAML). The validator previously rejected this with `root: required`; emptiness is now accepted and treated identically to `"."` — the policy operates against the store root directly. Combined with `FolderPattern`, this yields a clean shape for a single-entry policy that picks one specific top-level folder, e.g.

```yaml
"@guidance-root":
  root: ""
  folder_pattern: "^guidance$"
  on_commit: recycle
  on_give_up: recycle
  visibility_timeout_seconds: 3600
```

Yields a long-lived rw claim on `<store-root>/guidance/` that recycle-on-commit returns to the queue tail after each pass — i.e. an always-available rw scope on the entire subtree. Previously the only way to express this was through concrete-path mode (`selector: "guidance"` via `openScoped`), which bypasses the pick-policy machinery entirely (no visibility timeout, no queue introspection).

- `stores/filesystem/store/store.go::validatePickPolicy` — drop the `pp.Root == ""` early reject; the existing canonicalization checks treat empty and `"."` identically (`filepath.Clean("") == "."`).
- New tests: `TestValidator_AcceptsEmptyPolicyRoot` (validator surface) and `TestOpenPickPolicy_StoreRootSingleEntry` (end-to-end Open against a `Root: ""` policy with `FolderPattern: "^guidance$"` and two non-matching siblings; asserts the address/scope shape and that the non-matching entries are filtered out).

### Pick-policy action vocabulary v2 (filesystem + postgres stores)

Per `.ok-planner/specs/2026-05-06-fs-store-pick-policy-action-vocabulary-design.md`. Replaces the legacy `release_to_back | release_to_head | delete` vocabulary with the v2 named-action set across both bundled stores.

- **New shared package `stores/common/action/`.** Defines the `Action` tagged-union type, the four action-name constants (`pop`, `pop_and_move`, `pop_and_delete`, `recycle`), the `ValidationResult` struct, and YAML unmarshal for the inline parameterized form. Both `stores/filesystem/` and `stores/postgres/` (and the test stub `stores/stub/`) import this package.
- **Filesystem store supports all four actions.** Plus a new `sync_strategy: on_drain` value (and a `drained` sentinel mechanism under `<store-root>/.fs-store/<policy>/drained`) that produces single-pass-then-refresh queue mode. The legacy `sync_strategy: on_sweep` is dropped (configs using it fail at config-load with the "must be on_open|on_drain|explicit|never" error).
- **Postgres store supports `pop` and `recycle`.** `pop_and_move` and `pop_and_delete` rejected at config-load with "not supported by postgres store"; the items-table mechanism has no separate folder concept. Old `delete` migrates to `pop`; old `release_to_back` migrates to `recycle`.
- **Stub store accepts the full vocabulary** with pop variants collapsed to "drain queue entry" (no folder concept exists in-memory). Used by scenario tests to drive both Available and Unavailable Open paths.
- **Migration:** pre-v1 break-cleanly. Old field names (`OnCommitDefault`, `OnGiveUpDefault`) and old action values (`release_to_back`, `release_to_head`, bare `delete`) are rejected at config-load with errors pointing at the new vocabulary. In-tree configs and tests have been updated.
- **YAML shape:** inline parameterized action; `on_commit: pop` is a bare string, `on_commit: { pop_and_move: target }` is a one-key map. Parser rejects number, sequence, multi-key map, or empty-map shapes (null silently zero-values per yaml.v3 behavior; the validator catches the resulting empty Kind).
- **Validator:** rejects bad combinations at config-load (e.g., `pop + sync_strategy: on_open` for fs-store; `pop_and_move` for pg-store) and warns on inert pairings (`recycle + sync_strategy: on_drain`). Returns a `ValidationResult{Errors, Warnings}` struct; warnings logged via package-level slog by the constructor.
- **Cross-filesystem check for `pop_and_move`.** Validator confirms via `filepath.EvalSymlinks` + `syscall.Stat_t.Dev` that the target root is on the same filesystem as the policy root. Different-filesystem targets fail config-load (`os.Rename` is not atomic across filesystems).
- **Concept docs added:** `docs/concepts/claim-producer-fs-store.md` and `docs/concepts/claim-producer-pg-store.md` document the per-store action support matrix and common patterns.
- **No proto wire change; comments-only update.** Strictly store-side. Stale example-value comments in `protocols/proto/v1/events.proto` on `ClaimAcquiredPayload.on_commit` / `on_give_up` and `ClaimResolvedPayload.action` updated to the new vocabulary (the payload types themselves are dead — declared in the `Event.payload` oneof but never emitted by any Go code). The reactive-loops + lifecycle-handlers work shipped 2026-05-05 stays the same.

#### Action-vocabulary review-cleanup pass 1

Eleven issues from the first review pass on the action-vocabulary work
above, fixed in this commit. Tightens the validator's containment
guards for `pop_and_move`, fixes a sweep bug that clobbered the
drained sentinel on ENOENT, surfaces postgres errors that
`findPolicyForClaim` was swallowing, and adds the SQL-path tests the
spec mandated under §10.7.

- **`pop_and_move` MoveTarget now goes through the same containment checks as `pp.Root`.** `validateMoveTargetContained` enforces filepath.IsAbs reject, `..`-prefix reject, and `filepath.Rel`-based containment under `storeRoot`. Without this an operator config of `pop_and_move: ../../etc/triage` could pass the same-fs check (typical case: target on the same filesystem) and let every commit `os.Rename` outside the store root. New tests: `TestValidator_RejectsTraversalTarget`, `TestValidator_RejectsAbsoluteTarget`.
- **`pop_and_move` rejects `target == policy.Root`.** Previously `os.Rename(root/folder, root/folder)` was a silent no-op, observably equivalent to plain `pop` — operators who fat-fingered the target got behavioral drift instead of an error. The validator now rejects (using `filepath.EvalSymlinks` on both sides) with "target resolves to the same directory as the policy root". New test: `TestValidator_RejectsTargetEqualsPolicyRoot`.
- **Sweep no longer clobbers the drained sentinel on ENOENT.** `os.Rename` returning ENOENT (a concurrent terminal RPC removed the in-progress sentinel just before sweep tried to rename it) used to set `reclaimed = true` anyway, causing `removeDrainedIfPresent` to clobber a still-needed drained sentinel and burn one extra Unavailable cycle on the next Open. Fix: only set `reclaimed = true` on successful rename.
- **Pg-store `findPolicyForClaim` no longer swallows postgres errors.** Signature changed to `(*PickPolicy, bool, error)`; SQL errors propagate to `applyPickAction` instead of degrading silently to a no-op while reporting `claim_committed` to the ledger.
- **Pg-store validator emits one error per slot for unsupported actions.** Previously `pop_and_move` on the pg-store produced two stacked errors (the missing-target one + the not-supported-by-pg one) for the same root cause. New helper `pgZeroOrRejected` runs the pg-rejection check first and skips the per-action `Validate()` when the kind is rejected.
- **Both validators surface a clear error when an action field is null/missing.** yaml.v3 silently skips `UnmarshalYAML` when the source is null and the target is a struct value, leaving Kind="". The previous downstream error was `unknown action ""` (with the empty quoted string). Both fs- and pg-store validators now emit `<slot>: required (got null or missing)` ahead of `Validate()`. New test: `TestValidator_RejectsNullCommit`.
- **Pg-store SQL paths now have direct test coverage.** New `TestPGAction_Pop_RowDeleted` and `TestPGAction_Recycle_RowReturnsToQueue` (in `stores/postgres/store/action_vocab_test.go`) spin up a throwaway postgres container via testcontainers, seed an items table, drive `Open` → `Commit` end-to-end, and assert the SQL state mutation. Closes the gap left by the earlier "deferred to scenario suite" rationale (the scenario tests only exercised `Recycle` via the smoke fixture, never `Pop`'s `DELETE`).
- **`TestValidator_RejectsCrossFilesystemTarget` added per spec §10.3.** Probes for an alternate-filesystem mount point at runtime; skips on platforms where two distinct filesystems can't be assembled (macOS by default). Closes the missing test for the load-bearing same-filesystem guard.
- **`TestOnDrain_RaceUnderConcurrentOpens` post-storm assertion tightened.** Adds a serialized drain-cycle check after the storm: drives bounded follow-up Opens until one returns Unavailable (proving the drained pass-boundary signal still works after the concurrent storm), and asserts drained is consumed afterwards.
- **`TestOnDrain_SinglePass` cold-read comment added** to the pass-2 block explaining that under `pop`, folders stay on disk and runSync re-discovers them on the next pass — operators using `pop + on_drain` MUST mutate the corpus externally between passes (or use `pop_and_delete` / `pop_and_move`) to actually drain. The rationale lives at the call site rather than only in the implementation notes.

### Reactive loops + lifecycle handlers — review-cleanup pass 3

Third-cycle fixes after the second cleanup pass's verification surfaced a
real correctness bug plus four hygiene items.

- **`emitLockReleased` no longer opens a nested transaction.** Called from `releaseAcquiredLock` / `releaseClaim`, which run inside an open `Persist.Transaction(...)` block in `applyTerminalComplete` / `applyTerminalAppError` / `applyTerminalInfraError` / `applyTerminalPass`. Previously opened its own fresh `Persist.Transaction(...)` for the `lock_released` event append — under SQLite (`MaxOpenConns=1`) this self-deadlocked; under postgres it tied up two pool connections concurrently. Fix: `emitLockReleased` now takes the in-flight `tx persistence.Tx` as a parameter and uses it for the event append. `emitLockAcquired` and `emitQualityRuleFailures` updated to mirror the tx-required pattern (defensive — neither was called from inside an open tx, but the uniform contract prevents future regressions of the same shape).
- **`runner_terminal.go` split into siblings to honor the cold-read 500-line guideline.** `applyTerminalAppError` + `applyResolvedAction` + `applyTerminalInfraError` + `lookupPolicyForNode` + `requiredStoresForAcq` + `invalidateTargets` moved to `runner_terminal_errors.go`. Resulting `runner_terminal.go` is 363 lines (was 585); the Complete-branch + cascade fan-out remains in one place since they interleave. Sibling files: `runner_terminal_handlers.go` (121), `runner_terminal_errors.go` (264), `runner_terminal_release.go` (217).
- **`acquisition` struct documented as not safe for concurrent use.** Doc-comment on the type spells out the single-goroutine-per-dispatched-run contract and the post-acquisition immutability of the fields, so a future refactor that parallelises any release path adds explicit synchronization (or copies the fields it needs) before sharing the pointer.
- **Contract docs updated for the lifecycle-handler landing.** Tasks 49/50 of the original plan were skipped in cycle 1 because the plan referenced `docs/specs/...` paths that don't exist; the actual files live at `.ok-planner/specs/2026-05-04-foundation-contract.md` and `.ok-planner/specs/2026-05-04-modeling-layer-contract.md`. Foundation contract gains §5.6 (lifecycle-handler-driven resolution) and a `last_outcome` mention in §6.1's `rimsky_nodes` description. Modeling contract gains the four lifecycle-handler blocks + per-emit `frame: in | next` field in §3.1, and the `last_outcome` field in §9.1's state vocabulary. Notes file at `.ok-planner/plans/2026-05-05-reactive-loops-and-lifecycle-handlers-notes.md` corrected.
- **`TestFrameTimeoutReaper` no longer flakes on postgres deadlocks.** Test seeds wedged frame state via direct DELETEs against `rimsky_worker_request` / `rimsky_claim_handle` / `rimsky_frames` and drives `frame.RunTick` manually; the supervisor poll-loop's row locks (including the new `last_progress_at` UPDATE inside `enforceAndUpdate`) raced with the seed DELETEs. Adding `NoSupervisor: true` to the harness opts removes the contention; the supervisor wasn't needed since `RunTick` is called explicitly. Verified at `-count=20`.
- **`TestWorkerRequestPhaseAdvancesOnClaim` no longer races queue-row deletion.** `Queue.Complete` runs inside the supervisor poll-goroutine AFTER `applyTerminalComplete` returns; the prior pattern (wait for `fresh` state, then SELECT count) raced. Added `Harness.WaitForWorkerRequestDeleted(nodeID, timeout)` polling helper and switched the assertion to use it. Verified at `-count=20`.

### Reactive loops + lifecycle handlers — review-cleanup pass 2

Second-cycle hygiene + correctness fixes after the first review-cleanup
pass. All issues found by the verification reviewer.

- **`invalidateInFrame` no longer self-deadlocks under SQLite on the nil-source-frame fallback.** The frame_id resolution read is now a separate short tx; only the success path opens the mutating tx. Prior code called `invalidateNextFrame` (which opens its own tx) from inside the outer tx — under SQLite (`MaxOpenConns=1`) this would deadlock, and under postgres it tied up two pool connections concurrently. Reachability path: legacy `invalidateTargets` policy chain firing `invalidate` with `frame: "in"` against a source row whose `frame_id` is currently nil. Latent (no test exercised the combination); fixed by structure rather than test.
- **`FailAllActiveByLockHolder` now claimant-guarded.** The bulk UPDATE filters via `EXISTS (... AND holder_supervisor_id = supervisorID)` — defense-in-depth against future refactors that share claim-handle ownership across supervisors. Signature change: `FailAllActiveByLockHolder(ctx, lockHolderID, supervisorID, tx)`. Postgres + SQLite impls and the lone caller in `releaseClaim` updated.
- **`emitHandlerInvalidate` no longer takes a useless `acq` parameter.** The `_ = acq // reserved for future use` comment is gone; the unresolved-target event in `resolveHandlerTargets` now keys on the source node id passed in directly. Cold-read hygiene — no behavior change.
- **`runner_terminal.go` split into siblings to honor the cold-read 500-line guideline.** `applyTerminalBlockedOrErrored` + `applyTerminalPass` moved to `runner_terminal_handlers.go` (121 lines). `releaseLocksInTx` + `releaseAcquiredLock` + `releaseClaim` + `releaseInheritedClaimsInTx` + `releaseActionString` + `emitLockReleased` moved to `runner_terminal_release.go` (205 lines). `runner_terminal.go` now ~585 lines.
- **CLAUDE.md stale `docs/vocabulary.md` reference removed.** Earlier review-cleanup pass missed this entry; the file was deleted in the doc restructure and there is no successor under `docs/`.

### Reactive loops + lifecycle handlers — review-cleanup pass

Follow-up correctness fixes after the reactive-loops + lifecycle-handlers
landing. All issues found by the post-merge review.

- **`frame: in` self-invalidate from `on_executor_complete` now actually stays in-frame.** Added `InvalidateArgs.SourceFrameID` override; the post-Complete handler.invalidate emit now passes `acq.FrameID` so `invalidateInFrame` lands on the closing frame even though the running-tx already cleared the source row's `frame_id` (defensive guard in `nodes.UpdateState` on transitions to fresh). Without this, the in-frame self-invalidate spec (§5.2 "single frame for the entire drain") silently delivered a next-frame loop. Test in `test/scenarios/reactive_loop_self_invalidate_in_frame_test.go` re-tightened from `≤4 frames` to `== 1 frame`.
- **Held-claim handle no longer leaks under `on_executor_blocked / errored: { resolve: pass }`.** When a held-claim acquirer terminates with !success, `releaseClaim` now also calls `ClaimHoldersStore.FailAllActiveByLockHolder` so every still-active inheritor row is marked failed; auto-terminal fires immediately rather than waiting for inheritors to reach a terminal they would never reach (passed does not cascade). New `FailAllActiveByLockHolder` method on `ClaimHoldersStore` interface; postgres + sqlite impls. Regression test: `test/scenarios/held_claim_acquirer_blocked_pass_test.go`.
- **`handleAcquireUnavailable` defends against nil `acq.NodeDef`.** Mirrors the pattern in `applyTerminalBlockedOrErrored` so a future refactor that exposes a nil-NodeDef path crashes in tests instead of in production.
- **`handleResetNode` clears `last_outcome` to NULL.** New `NodeStore.ClearLastOutcome` method; postgres + sqlite impls. Without this, a failed → reset → stale → running transition briefly displayed `state=stale, last_outcome=failed` in the dashboard (the COALESCE pattern in `UpdateState` preserves `last_outcome` on stale → running).
- **`resolveHandlerTargets` emits `unresolved_invalidate_target` events.** Parity with `invalidateTargets` (the error_types policy-chain path); previously only logged via `log.Warn` and dropped silently from the audit trail.
- **CLAUDE.md doc-drift fix.** All references to non-existent `docs/specs/` and `docs/history/` paths updated to the actual `.ok-planner/specs/` and `.ok-planner/history/` paths.
- **SQLite frame INSERTs now write `last_progress_at` explicitly with `nowUTC()`.** The migration's `strftime('%f')` DEFAULT only delivers ms precision; runtime writes were nano-precision, leaving the column with mixed precision across rows. The migration's DEFAULT is now consumed only by rows existing at migration time (none in dev workflows); all subsequent INSERTs include the column at uniform RFC3339Nano precision.
- **`acquire_pass_invalidate_emit_test.go` made deterministic.** Monitor now depends on worker so a non-cascading passed terminal cannot wake it; the only remaining wake path is the handler.invalidate emit, asserted as `work_completed == 1` (was `>= 2` with a race against the initial scheduler tick).

### Reactive loops + lifecycle handlers

Per `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`. Adds four declarable lifecycle-handler slots on each node (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`); a `last_outcome` column on `rimsky_nodes` capturing the resolution flavor (`fresh_changed | fresh_unchanged | passed | pure_cascade | failed`); a per-emit `frame: in | next` field on every invalidate emit declaration; and a `last_progress_at`-based refinement of `frame_timeout_ms` semantics ("no progress in window" instead of frame age).

- **Templates without lifecycle-handler blocks are unaffected.** Defaults preserve today's hardcoded supervisor behavior (silent retry on Unavailable, by_changed on Complete, route through error_types on Blocked / Errored).
- **The cascade-firing gate is now `last_outcome == fresh_changed`** instead of `t.Changed` directly. Functionally identical under the default `by_changed` resolution; diverges under explicit `always_propagate` / `never_propagate`.
- **`pass` and `error` resolutions on `on_acquire_unavailable` / `on_executor_blocked` / `on_executor_errored` call `Abandon`** on already-Open'd claims, matching `handleOrphanedClaim` semantics.
- **`frame_timeout_ms` measures "no progress in window"** via the new `rimsky_frames.last_progress_at` column, and emits a `frame.stuck.observed` slog warning when a running frame's last node-state transition was longer than `frame_timeout_ms` ago. The warning is purely informational — the frame stays `running`, no nodes are failed, the instance is not terminated. The destructive `reapOneStuckFrame` path is **removed** in this dispatch (operator clarification: timeouts must not auto-fail frames pre-v1; we will revisit this post-v1). A self-invalidate loop that progresses by one node per iteration refreshes `last_progress_at` and stays under the timeout indefinitely; a wedged frame whose nodes stop transitioning trips the warning.
- **Removed:** `Frames.FailAllPendingNodes` interface method + Postgres/SQLite impls; `modeling/frame/engine.reapOneStuckFrame`; `runReapStuckFrames` renamed to `runWarnStuckFrames`; the slog key `frame.stuck.reaped` renamed to `frame.stuck.observed`.
- **New TransitionReason kinds:** `acquire_pass`, `handler_complete` (subsumes `work_completed` for new code paths; old name kept as deprecated alias for one cycle), `handler_error` (audit-log only; not a direct NextState input), `handler_pass`.
- **Schema:** new ALTER TABLE migrations adding `last_outcome TEXT` to `rimsky_nodes` and `last_progress_at TIMESTAMPTZ NOT NULL DEFAULT now()` to `rimsky_frames`. Pre-v1; no compat shim. Existing dev DBs accept the new columns via the migration runner.
- **`Nodes.UpdateState` signature change:** added `lastOutcome shared.LastOutcome` parameter (empty string preserves the existing column value via COALESCE). All call sites updated.
- **`PolicyAction.Frame`** field added (yaml `frame: in | next`); error_types invalidate actions now propagate the frame setting through to `InvalidateNode`.
- **CLI:** `rimsky-cli admin invalidate --frame in|next` flag added.
- **Control API:** `POST /nodes/{id}/invalidate` accepts an optional `frame` field in the request body.
- **Validator:** `modeling/node/template_validator.go` rejects out-of-vocabulary handler resolutions, missing `error_class` when `resolve=error`, invalid `frame` values, unknown invalidate targets, and empty handler blocks.
- **Tests:** new scenario tests in `test/scenarios/lifecycle_handlers_test.go` covering always_propagate / never_propagate cascade gates, fresh_unchanged column gate, blocked/errored pass resolutions, operator-invalidate-target-only, pure_cascade column. Additional regression coverage in `test/scenarios/`: `reactive_loop_self_invalidate_next_frame_test.go`, `reactive_loop_self_invalidate_in_frame_test.go`, `acquire_unavailable_pass_test.go`, `acquire_unavailable_retry_default_test.go`, `acquire_unavailable_error_routing_test.go`, `held_claim_acquirer_passes_test.go`, `held_claim_mixed_upstream_test.go`, `frame_coalesce_self_invalidate_test.go`, `frame_timeout_progressing_loop_test.go`, `frame_timeout_stuck_frame_test.go`, `handler_invalidate_orthogonal_to_changed_test.go`, `acquire_pass_invalidate_emit_test.go`. Frame seed helpers updated to populate `last_progress_at` so the stuck-frame reaper test still trips on stuck-since-X seeded frames.
- **State machine:** extended `NextState` in `foundation/cascade/state.go` to accept `policy_give_up` as a `stale → failed` transition (previously only `running → failed`). Required for `on_acquire_unavailable: { resolve: error }` paired with an `error_types[X].policy = [{give_up}]` chain — under that path the node is still `stale` (Open returned Unavailable; never entered running) and the OnError give_up branch must succeed.

### Docs — `CLAUDE.md` stale `docs/internal/` references removed

Lines 176-181 of `CLAUDE.md` (the "internal/working engineering material"
section) referenced `docs/internal/node-graph-design.md`,
`docs/internal/architecture.md`, `docs/internal/operator-guide.md`,
`docs/internal/glossary.md`, `docs/internal/claim-producer-author-guide.md`,
and `docs/internal/executor-author-guide.md` — paths that no longer
exist after the layer-crystallization doc restructure (the originals
were archived to `.ok-planner/archive/internal/`). Replaced the dead
references with one short paragraph noting the restructure and pointing
at the successor public-surface homes (`docs/concepts/`,
`docs/protocols/`, `docs/glossary.md`) which were already cited in the
"external-consumer-facing material" section above.

### Docs — `CLAUDE.md` vocabulary fix

`CLAUDE.md`'s "What this repo is" vocabulary line claimed two message
types (`invalidate`, `recalculate`) and pointed at `docs/node-graph-design.md`
+ `docs/architecture.md` for the conceptual / implementation models.
Both were stale: `recalculate` is a scheduler action, not a peer
message (the `docs/concepts/` cleanup pass already corrected the
public-surface docs but missed `CLAUDE.md`); the two referenced design
docs were archived during layer-crystallization. Updated to the
current single-message vocabulary and to point at `docs/concepts/`
plus the foundation/modeling contract specs in `docs/specs/`.

### Refactor — Persistence option C: tx is required everywhere

The persistence Store interface no longer accepts `nil` for the `tx`
parameter. The auto-commit code path inside `q(tx)` is gone — every
Store method panics on a nil tx. The motivating bug: a `nil`-tx call
from inside an open `Persist.Transaction` callback used to silently
auto-commit on a fresh connection, which deadlocked under the SQLite
driver's `MaxOpenConns=1` (the only conn was held by the outer tx).
Postgres masked the same shape because its pool is large enough to
hand out a second connection.

- **`q(nil)` panics** in both `foundation/persistence/{sqlite,postgres}/backend.go`.
- **Methods with hand-rolled `if tx == nil { open my own tx }` branches**
  (`Nodes.UpdateState`, `Frames.EnqueueCoalesceFrame`, all of `LockHolders`)
  are simplified to require a non-nil tx — those branches are gone.
- **All call sites updated** to either thread an existing tx (when
  inside a `Persist.Transaction`) or open a short-lived one. Notable
  inside-tx production fixes: `runner_locks.go::buildLockSpecs /
  loadInheritedClaimsForNode / loadDepsAttributes / lookupTemplate`,
  `runner_held_claims.go::findInheritedAliasesForNode /
  pickAliasForLockHolder`. Notable outside-tx wraps: every HTTP
  handler under `modeling/controlapi/`, every observability handler
  under `modeling/observability/`, the scheduler's `ProcessSchedules`
  and `ProcessPureCascade`, every test fixture in `test/scenarios/`,
  conformance, and `modeling/scenario`.
- **Structural enforcement.** `foundation/persistence/sqlite/deadlock_guard_test.go`
  enumerates every public Store method and asserts it panics on a
  nil tx, plus pins the historical SQLite hang shape (a nil-tx call
  from inside an open Persist.Transaction must complete in
  milliseconds, not deadlock to the test deadline).
- **Conformance test deleted.** `testCoalesceFrameNilTx` explicitly
  asserted the now-removed nil-tx behavior on `EnqueueCoalesceFrame`;
  removed alongside its `Suite()` entry.
- **Quickstart switched to SQLite.** `quickstart/rimsky.yml` now uses
  `driver: sqlite` with state at `/var/lib/rimsky/state.db` in a
  Docker named volume; `quickstart/docker-compose.yml` drops the
  postgres service. The README's postgres-vs-sqlite explanation is
  gone — the unified `rimsky/all` image now runs cleanly on SQLite
  through a real cascade (both nodes reach `fresh`, instance
  terminates, no `SQLITE_BUSY`).

Audit doc archived to `docs/history/2026-05-05-nil-tx-deadlock-audit.md`.

### Licensing — first license declaration

- **Tri-license structure landed.** Apache 2.0 for the embedder layer
  (wire IDL, executor SDK, reference stores/executors, CLI, conformance,
  deploy, docs); AGPL-3.0-or-later for the orchestrator-internal layer
  (scheduler, supervisor, control-API, persistence, integration, modeling
  runtime); a Fall Guy Consulting commercial license available separately
  for organizations needing orchestrator modifications without AGPL §5/§13
  obligations. See `docs/history/2026-05-02-licensing-design.md` for
  rationale and `docs/licensing.md` for the operator FAQ.
- **Per-file headers added** across every source file per the boundary map
  in `licensing.yml`. AGPL files carry the mandatory "dual-licensed"
  wording that preserves the commercial-license track.
- **`cmd/rimsky-license-check/` binary** verifies the boundary mechanically
  on every CI run via `make license-lint`. Apache-classified Go files
  cannot import AGPL-classified packages; every source file's header must
  match its directory's classification.
- **`modeling/qualityrule/` split** to keep the Apache → AGPL boundary
  clean: spec types (`Spec`, `Failure`, `EvalInput`, `Evaluator`) stay in
  `modeling/qualityrule/` (Apache, interface-shaped) so `modeling/node`'s
  `TemplateNodeDef` can declare them; the registry, `EvaluateAll`, and
  built-in evaluators move to `modeling/qualityrule/eval/` (AGPL,
  orchestrator runtime). The supervisor now imports both packages.
- **CLA + DCO required for contributions** per `CONTRIBUTING.md` and
  `CLA.md`. Inbound=outbound + relicensing-grant shape; one-time
  signature via cla-assistant.io (off-repo configuration).

### Refactor — Layer crystallization Phase 7: documentation refresh complete

The seven doc rewrites deferred from the dispatch-7 partial completion are now landed.

- **`docs/architecture.md` rewritten** to present the four-layer model (foundation, modeling, service protocols, bundled services + examples) with the layer-crystallization architectural diagram. Documents the three Go modules (`foundation/`, `protocols/`, root) and their dependencies. References the three contracts in `docs/specs/` as authoritative for each layer. Removes references to historical specs (`docs/history/2026-04-*`) — those are now archived for context only.
- **`docs/operator-guide.md` rewritten** for the post-Phase-6 vocabulary and Option II YAML shape. The unified `rimsky.yml` now declares peers under `claim_producers:` and `executors:` with a per-peer `protocols:` field and `write_semantics_envelope:` for producers (legacy `stores:` block alias still parsed). Phase 3's deferred `region`→`scope` doc updates are folded in throughout. Schema queries updated to post-Phase-5 table names (`rimsky_worker_request`, `rimsky_claim_handle`).
- **`docs/glossary.md` rewritten.** New entries: `scope`, `claim producer`, `worker request`, `active phase`, `held phase`, `realized write semantics`, `write semantics envelope`, `lifecycle subscriber`, byte-equal-scope uniformity. Deprecated terms marked: `region` (now `scope`), `Store` at the protocol level (now `ClaimProducer`). Four-layer-model summary added at the top. Producer-internal vocabulary (`pick_policies`, `release_to_back`, etc.) explicitly documented as out-of-Rimsky-protocol.
- **`docs/protocol.md` retired** in favor of a one-page pointer to `docs/specs/2026-05-04-service-protocol-contract.md`. Updated `README.md` and `.claude/rules/rules.md` to point at the contract directly.
- **`docs/executor-author-guide.md` rewritten** for the new module layout (external Go authors import `github.com/fallguy/rimsky/protocols/executor`). References the service-protocol contract. YAML examples updated to Option II shape. Async-callback path documented (POST `${callback_url}/v1/callback/{async_ack_id}` body keyed `type` — not `kind`). Phase 3's region→scope deferral folded in.
- **`docs/store-author-guide.md` renamed** (via `git mv`) to `docs/claim-producer-author-guide.md` and rewritten as "Writing a Claim Producer". External Go authors import `github.com/fallguy/rimsky/protocols/claimproducer` (and `protocols/lifecycle` if implementing both). YAML config: `claim_producers:` block with per-peer `protocols:` and `write_semantics_envelope:`. Conformance: `rimsky-claim-producer-conformance --endpoint <yourservice>:7000`. Note clarifies that "store" is the colloquial term for data-backed producers; the protocol-level term is "claim producer". CLAUDE.md and other docs updated to the new path.
- **`docs/node-graph-design.md` updated** to reflect the foundation/modeling vocabulary distinction. New §3.7 "Under the hood — foundation primitives" maps the 4-state vocabulary to the foundation's `(has_value, has_outstanding_request, auto_recovers)` space and the 3 error actions to the foundation's parameterized failure-terminal `(auto_recovers, cascade_targets)`. Vocabulary updated throughout: `region` → `scope` (conflict-predicate-sense); `store` (protocol-level) → `claim producer`; legacy table names (`rimsky_dispatch`, `rimsky_lock_holders`) → post-Phase-5 (`rimsky_worker_request`, `rimsky_claim_handle`); `Store.Open/Commit/Abandon` → `ClaimProducer.Open/Commit/Abandon`. Three-collections architecture replaced with four-layer model.
- **Cross-doc references repaired.** `docs/specs/2026-05-02-dashboard-and-observability-design.md` references corrected (was incorrectly pointed at `docs/history/`); `docs/store-author-guide.md` references in CLAUDE.md and `.claude/rules/rules.md` updated to the new claim-producer-author-guide path.

### Refactor — Layer crystallization Phase 6: reaper + terminal-decision unification

- **Single conceptual orphan-reaper boundary.** The two existing
  reapers — `SweepOrphanedClaims` (worker-request rows with stale
  heartbeat) and `SweepLockHolders` (claim-handle rows past
  expires_at) — keep their separate implementations because they
  reap different table entities, but the documentation in
  `foundation/integration/orphan_reaper.go` now ties them together
  as one mechanism with two halves. Held-phase worker-request rows
  are NEVER reaped at the worker-request level (the SQL predicate
  excludes them via `claimed_by IS NOT NULL`); auto-terminal
  resolves them. Held-phase claim-handle rows orphaned by parent
  deletion are reaped by the claim-handle reaper once their
  `expires_at` lapses.
- **Single terminal-decision engine.** New
  `foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal`
  packages the three-step "fire producer verb (Commit on success;
  Abandon on failure) + claimant-guarded delete of claim_handle row"
  sequence as a unified primitive. Both `auto_terminal.go::CheckAndFireResolution`
  (held-terminal source) and `runner_terminal.go::releaseClaim`
  (active-terminal source) now delegate to it. The two source paths
  retain their distinct context (held-subgraph completion check vs
  acquisition-context release) but share a single audited
  verb-fire-and-delete implementation. Foundation invariants 4
  (claimant-guarded), 13 (single auto-terminal), 20 (claim content
  inert) preserved.
- **`TerminalSource` and `AggregateOutcome` types** distinguish
  active vs held terminations and Commit vs Abandon outcomes for
  logging / metrics. The engine signature accepts a `TerminalDecision`
  struct that bundles the claim_handle id, supervisor id, source,
  outcome, producer client, and scope/address bytes.

### Refactor — Layer crystallization Phase 5: worker-request consolidation

- **`rimsky_dispatch` and `rimsky_lock_holders` consolidated** into
  `rimsky_worker_request` and `rimsky_claim_handle`. Worker-request
  lifecycle has up to two phases tracked via a new `phase` column
  (`'pending' | 'active' | 'held' | 'completed'`); active rows carry
  `claimed_by` and a heartbeat timestamp, the orphan reaper covers
  `phase='active'` rows. `rimsky_claim_handle.is_held BOOLEAN`
  column marks claims that persist past the active terminal until
  the holding subgraph completes. `rimsky_claim_handle.worker_request_id`
  is an observability FK with `ON DELETE SET NULL` (held claim
  handles outlive the worker-request's active-phase terminal until
  auto-terminal resolution fires the producer verb and explicitly
  deletes them — cascade would race against held-claim resolution).
  `rimsky_claim_holders.lock_holder_id` renamed `claim_handle_id`.
  Pre-v1 dev-DB-nuke applies (postgres + sqlite migrations rewritten
  in place rather than as successors).
- **Active-phase column wired** in the Postgres + SQLite Queue
  implementations. `Enqueue` writes `phase='pending'`; `ClaimDispatchRow`
  advances to `phase='active'`; `ReleaseClaim` (orphan reaper path)
  reverts to `phase='pending'`. The dispatch DELETE at terminal is
  preserved as the worker-request's final state under the minimal-
  rename approach (the schema accepts `phase='completed'` for forward
  compatibility but no current code path emits it; the row is deleted
  outright at active terminal).
- **`is_held` populated at acquisition.** `runner_acquire.go::acquireClaim`
  computes the held flag from the holding-subgraph membership of
  `(acquirerType, alias)` and persists it on the claim_handle row
  via the new `LockHolderInsertInput.IsHeld` field. Named locks
  always carry `is_held=false`. Existing held-vs-non-held branching
  in `runner_terminal.go::releaseClaim` is unchanged (it still
  consults the in-memory `HeldSubgraphs` slice via `isAliasHeld`);
  the persisted column is for observability and forward-compatibility
  with the Phase-6 unified terminal-decision engine.
- **`LockHolderRow` gains observability fields:** `WorkerRequestID
  *shared.UUID` and `IsHeld bool`, surfaced through both Postgres and
  SQLite scanners.
- **Foundation invariants 3, 4, 5, 6, 10, 13, 15 preserved.** The
  acquisition tx, claimant-guarded release, verify-before-run, 5×
  heartbeat orphan cutoff, atomic acquisition, single auto-terminal,
  and Open-inside-acquisition-tx semantics all hold across the
  schema rename.
- **`test/scenarios/locks/worker_request_phase_test.go`** added:
  `TestWorkerRequestPhaseAdvancesOnClaim` exercises the row's
  lifecycle through `phase='pending' → 'active' → deleted`;
  `TestClaimHandleIsHeldColumnPopulated` asserts the column is
  populated at acquisition time. Both run cleanly under
  `-race -count=3`.

### Refactor — Layer crystallization Phase 4: ClaimProducer rename + LifecycleSubscriber split + write-semantics envelope

- **`Store` interface renamed to `ClaimProducer`** at the protocol layer.
  `protocols/claimproducer/` carries the Go interface and value types;
  `service Store` in proto becomes `service ClaimProducer`. The
  rimsky-side `foundation/locks.Store` is now an alias for
  `foundation/locks.ClaimProducer`. Bundled-services-layer term "store"
  survives for data-backed colloquial use (filesystem store, postgres
  store, stub store).
- **`LifecycleSubscriber` extracted as its own service** in
  `protocols/lifecycle/` (new `lifecycle.proto`). The six methods
  (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`,
  `OnInstanceCreated/Terminated`) move out of the bundled-into-Store
  pattern. Implementers return success from methods they don't react
  to; binaries that don't react to any event simply don't implement the
  service. Binaries declare which protocols they implement via a new
  `protocols:` field per peer in `rimsky.yml`. Field names on the wire
  switch from `template_id` → `template_hash` for the lifecycle events
  (the lifecycle-protocol payload was always template-content-hash; the
  rename clarifies intent).
- **Write-semantics envelope.** `Capabilities()` now returns
  `WriteSemanticsEnvelope` (a SET of permissible values); `Open` returns
  `RealizedWriteSemantics` per claim. Operator declares
  `write_semantics_envelope: [...]` per producer in YAML; startup
  validation enforces operator envelope ⊆ producer envelope. New value
  vocabulary: `sync` (was `direct`), `staged_async`, `blocking_async`
  (was `staged_blocking`), `read_only`. Uniformity invariant: two
  `Open` calls returning byte-equal `Scope` MUST return identical
  `RealizedWriteSemantics`. The persistence layer gains
  `rimsky_lock_holders.realized_write_semantics` so the in-Go scope-
  conflict check can apply `ModeCoexists` without re-dialing the
  producer.
- **Conformance suites split.** `cmd/rimsky-store-conformance` renamed
  `cmd/rimsky-claim-producer-conformance` and rewritten to cover
  Capabilities envelope + uniformity-per-(producer,scope) +
  Open/Release verbs. `cmd/rimsky-conformance` covers executor
  scenarios (default) plus a new `--check-lifecycle` mode that drives
  the six LifecycleSubscriber RPCs against a peer.
- **YAML config shape Option II.** `stores:` block renamed
  `claim_producers:`; entries gain optional `protocols:` list (defaults
  `[claim_producer]`); singular `write_semantics:` field replaced by
  required `write_semantics_envelope:` set (legacy single-value form is
  still accepted as a single-element envelope shortcut). `executors:`
  entries gain the same optional `protocols:` field. The deprecated
  `stores:` block is still parsed and treated as `claim_producers:` as
  a transitional convenience; new configs SHOULD use `claim_producers:`.
  `deploy/rimsky.yml` and `deploy/rimsky-all.yml` updated to the new
  shape; `deploy/store-postgres.yml` switched its default
  `write_semantics:` from `direct` → `sync`.
- **`rimsky_store_lifecycle` table renamed `rimsky_lifecycle_idempotency`.**
  SQL migrations rewritten in place under pre-v1 break-freely; Go
  symbols `StoreLifecycle*` renamed `LifecycleIdempotency*`. The
  rename better reflects the table's role (per-peer event-idempotency
  bookkeeping) post-LifecycleSubscriber-split. Pre-v1 dev-DB-nuke
  applies.
- **Control-api wires LifecycleSubscribers.** `StartControlAPI` now
  dials a separate `LifecycleClient` for any peer (under
  `claim_producers:` or `executors:`) whose `protocols:` list contains
  `lifecycle_subscriber`. Lifecycle events fan out via the new
  `AppDeps.LifecycleSubs` registry. A peer referenced by a template but
  not subscribed silently skips fan-out; explicit subscription is
  required to record idempotency rows.

### Refactor — Layer crystallization Phase 3: region → scope rename

- **`region` → `scope` everywhere on the wire and in foundation
  internals.** Proto field `bytes region` → `bytes scope`; SQL column
  `region_data` → `scope_data`; Go struct field `RegionData` →
  `ScopeData`; helper `RegionsByteEqual` → `ScopesByteEqual`;
  `LockKindRegion` → `LockKindScope`; `LockHoldersStore.UpdateRegion` →
  `UpdateScope`; `LockHoldersStore.ListByStoreRegion` →
  `ListByStoreScope`; `AdvisoryLocker.TakeRegionLockInTx` →
  `TakeScopeLockInTx`; `ClaimResult.Region` → `Scope`;
  `evaluateRegionConflict` → `evaluateScopeConflict`;
  `claimRegion` → `claimScope`; `matchesRegion` → `matchesScope`;
  `checkRegionDirectives` → `checkScopeDirectives`;
  `openRegional` → `openScoped` (filesystem store).
  The §7.7 byte-equal-region invariant is now byte-equal-scope.
  Substitution path `{{claim.<alias>.region}}` → `{{claim.<alias>.scope}}`.
  `lock_kind` enum value `'region'` → `'scope'`. Foundation contract,
  modeling-layer contract, and service-protocol contract all use the
  new vocabulary. Pre-v1 dev-DB-nuke applies; no data migration shim.

### Refactor — Layer crystallization Phase 2: module split (γ)

- **Three Go modules established.** `github.com/fallguy/rimsky/foundation`,
  `github.com/fallguy/rimsky/protocols`, and the root `github.com/fallguy/rimsky`.
  Coordinated by `go.work`. The `foundation` module owns cascade + locks +
  integration + foundation persistence; the `protocols` module owns the
  three service-protocol Go interfaces and protobuf bindings (stdlib +
  grpc/protobuf only deps); the root owns modeling + cmd binaries +
  bundled service reference impls.
- **`core/` directory dissolved.** Contents migrated to `foundation/`,
  `modeling/`, `cmd/`, or stayed at the repo root per the four-layer model.
  `core/store/` → `foundation/locks/` (with `Registry` kept next to the
  `Store` interface for now); `core/persistence/` → `foundation/persistence/`
  (postgres/sqlite drivers consolidated together); `core/supervisor/` and
  `core/scheduler/` (foundation-relevant sweeps + the supervisor runner)
  → `foundation/integration/`; `core/scheduler/` (modeling-side ProcessSchedules,
  ProcessPureCascade, schedule_ticker, scheduler.go) → `modeling/scheduler/`;
  the rest of modeling under `modeling/{attribute,canonical→template/canonical,
  controlapi,frame,observability,qualityrule,executor,cli,config,scheduler,
  shared,node,scenario,internal}/`; binaries flattened from `core/cmd/` to `cmd/`.
- **`proto/v1/` migrated to `protocols/proto/v1/`.** `option go_package`
  updated; bindings regenerated. Two proto files renamed:
  `node_executor.proto` → `executor.proto`; `store_service.proto` →
  `claim_producer.proto`. TS proto-loader path updated; `Dockerfile.claude-agent`
  COPY paths updated.
- **`persistence.Coordinator` renamed `persistence.AdvisoryLocker`.**
  Frees the `Coordinator` name space for `foundation/integration/Conductor`.
  Field name `Coordinator` on integration `Config`/`RunArgs` structs
  renamed to `AdvisoryLocker`.
- **Foundation `state.go` (state machine + transition reasons) extracted
  into `foundation/cascade/`** as the blessed-invariant-1 home.
- **Foundation tick sweeps extracted into `foundation/integration/conductor.go`**
  (`SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady`) and
  `foundation/integration/orphan_reaper.go` (`SweepLockHolders`). The modeling-
  side `core/scheduler/scheduler.go` (now `modeling/scheduler/scheduler.go`)
  composes these foundation sweeps with the modeling-side ProcessSchedules /
  ProcessPureCascade / frame.RunTick.
- **`InvalidateNode` and `RecalculateNode` moved to `foundation/integration/`**
  as cascade dispatchers. The modeling-side scheduler still wires them via
  the schedule-dispatcher adapter.
- **`.golangci.yml` depguard rules updated** for new paths; new
  `foundation-internal-isolation` rule prevents modeling/services from
  reaching into `foundation/internal/`.
- **No semantic code changes.** Renames, moves, depguard updates only.
  `go build ./...`, `go test ./... -count=1`, `make lint` all clean
  on every Phase 2 buildable gate (Tasks 12, 13e, 15).

### Docs — Layer crystallization Phase 1: contracts

- **Foundation contract finalized.** New `docs/specs/2026-05-04-foundation-contract.md`
  supersedes the 2026-05-03 draft (moved to `docs/history/`). Vocabulary
  updated (region → scope); subsystem package names settled (`cascade`,
  `locks`, `integration`); driver interface set collapsed
  (`Cascade`, `WorkerRequests`, `AdvisoryLocker`); module split commitment
  locked in.
- **Modeling-layer comprehensive contract.** New
  `docs/specs/2026-05-04-modeling-layer-contract.md`. Single source of
  truth for templates, instances, frames, schedules, attributes,
  control-plane API, public vocabularies, YAML config shape, modeling
  persistence contract, and CLI shape. Supersedes content from the
  archived per-subsystem design docs in `docs/history/`.
- **Service-protocol contract.** New
  `docs/specs/2026-05-04-service-protocol-contract.md`. Defines
  `ClaimProducer` (renamed from `Store`), `Executor`, and
  `LifecycleSubscriber`. Adds `RealizedWriteSemantics` per claim and
  `WriteSemanticsEnvelope` at handshake. Supersedes service-protocol
  content from the archived stores-redesign-v3 + cleanup overlay +
  control-plane-and-store-lifecycle docs.

### Docs — Archive landed designs and plans

- **Moved 9 implemented designs from `docs/specs/` to `docs/history/`,
  along with their paired plans and notes.** Implementation verified
  via spot-checks against current code (`core/frame/`,
  `stores/{filesystem,postgres,stub}/`, `persistence.Open`,
  `core/cmd/rimsky-cli`, `core/controlapi/lifecycle.go`,
  `stores/filesystem/store/pick_policy.go`, `deploy/Dockerfile.all`,
  `core/cli/compose`). Plans renamed with `-plan` suffix on archive
  to disambiguate from older spec-format archives. The
  dashboard/observability design (still in active implementation)
  and the in-progress foundation-contract draft remain in
  `docs/specs/`. Archived files will be superseded by the
  comprehensive modeling-layer and service-protocol contracts coming
  out of the foundation-contract crystallization work.

### Fixed — Dashboard & observability v1 (round-3 review)

#### Spec adherence

- **Dashboard CSP wired both as a meta tag and a server response
  header.** Forbids `script-src 'unsafe-inline'`, locks `connect-src`
  to `'self'` (the proxy collapses CORS to single-origin), and allows
  `frame-src 'self' *` so operator-declared CustomUI iframes still
  render. (`dashboards/rimsky-dashboard/index.html`,
  `dashboards/rimsky-dashboard/src/server/index.ts`.) The server now
  also installs SIGTERM/SIGINT graceful-shutdown plus
  `uncaughtException` / `unhandledRejection` logging.
- **Dead `claim_url_template` field removed.** The proto reuses
  `dispatch_url_template` across executor and store CustomUI; the
  dashboard's TS type already reflected that, and the Go-side struct
  is now consistent. (`core/observability/discovery.go`.)
- **Templates list `?tag=` filter implemented end-to-end.** Added
  `Tag` field to `persistence.TemplateListFilter`; postgres + sqlite
  queries `EXISTS`-join `rimsky_template_tags`; control-api's
  `/v1/observability/templates` accepts the query param.
- **`StreamClaim` now streams live events** for both postgres and
  filesystem stores. Added `Subscribe`/`SubscribeWithSnapshot` on
  each ledger (the snapshot+subscribe pair runs under one lock so
  events landing between the two surface in the live channel),
  closed channels on terminal, broadcast non-blocking from each
  Record* method. The store servers' `StreamClaim` now replays
  history then pumps the live channel until terminal-or-disconnect-
  or-idle-timeout (default 5min, `SetIdleTimeout` overridable).
- **Conformance probes drive a canned dispatch and a retention
  check.** `core/cmd/rimsky-conformance` `--retention-test-seconds=N`
  fires an Execute, verifies `GetTrace` + `StreamTrace` surface the
  events, then sleeps past the configured retention and verifies
  `evicted: true`. The store-side equivalent verifies UNKNOWN
  preservation post-retention.
- **Idle-close timeouts on streams.** Spec §2.5/§3.5: the
  http-node, both store servers, and the claude-agent SSE handler
  all close idle streams cleanly with a final marker after a
  configurable timeout (default 5 minutes, env-overridable on the
  TS side via `RIMSKY_OBS_IDLE_TIMEOUT_MS`).

#### Code quality

- **`handleGetDispatch` is now a point lookup.** Added
  `Queue.GetByID(ctx, dispatch_id)` to persistence (postgres + sqlite);
  handler no longer paginates the live dispatch table. Same handler
  also uses the new `LockHolders.GetByFrameAndNode(ctx, nodeID,
  frameID)` instead of a `ListByHolderNode` + linear scan to resolve
  the dispatch → claim_id link.
- **Cascade-graph batch lookup eliminates the N+1.** Added
  `EventStore.LastTerminalByNodes(ctx, []nodeIDs)` (postgres uses
  `DISTINCT ON`; sqlite uses a correlated subquery). The handler
  fetches every node's last terminal in one round-trip instead of
  one per node.
- **Handshake probes run in parallel.** `RunHandshake` and
  `Discovery.refreshAll` fan out one goroutine per peer with the
  per-probe `handshakeTimeout` enforced via `context.WithTimeout`.
  Total wall-time is now bounded by the longest probe, not the sum.
- **claude-agent SSE attach race fixed.** Added
  `subscribeWithSnapshot` (atomic snapshot+listener attach in one
  synchronous call) and switched the SSE handler to it. Mirror of
  the round-3 Go fix on http-node.
- **`SetHTTPBridgeURL` is now sync.Once-guarded** on both store
  servers and the http-node executor. Documented as set-once-at-
  startup; subsequent calls are no-ops.
- **`AppendEvent` / `MarkTerminal` reject unregistered dispatch
  IDs.** Added `RegisterDispatch(id)` that the executor's dispatch
  flow calls at entry to formally claim a dispatch ID; later
  appends to unknown IDs are silently dropped (forged IDs cannot
  fill the in-memory ledger).
- **Ledger cursors encode claim_id, not positional index.** Both
  the postgres and filesystem ledgers' `List` cursor is now the
  last-returned claim_id; the previous index-based cursor would
  shift under concurrent eviction and skip records.
- **Dashboard SSE applies exponential backoff (1s → 30s, max 10
  attempts).** Successful re-connection resets the counter. After
  the cap, the wrapper reports `SseStreamLostError` so the
  consuming hook can render a "stream lost" badge.
- **Dashboard proxy gains an upstream timeout** (default 30s,
  `RIMSKY_DASHBOARD_PROXY_TIMEOUT_MS` overridable). Applied to both
  the connect phase and (for non-SSE) the body read; SSE bodies
  remain long-lived. Translates `AbortError` to 504 and other dial
  failures to 502.
- **Dashboard proxy now strips RFC 7230 hop-by-hop headers** on
  both inbound and outbound flows (`Connection`, `Keep-Alive`,
  `Transfer-Encoding`, `Upgrade`, `TE`, `Trailer`, `Proxy-*`,
  `Host`, `Content-Length`).
- **SSE write errors now break the pump loop** in the http-node
  HTTP bridge and the store-side bridge, so disconnected clients
  exit cleanly instead of looping. (`executors/http-node/
  observability_bridge.go`, `stores/internal/bridge/observability.go`.)
- **`MarkTerminal` is idempotent on `terminalAt`.** First call
  records the timestamp; subsequent calls are no-ops, so
  `trace_complete` timestamps stay stable across follow-on
  GetTrace requests.

#### Loose ends

- **Discovery cache returns null when no http_bridge_url.** The
  proxy translates that to 503 with a clear message
  (`peer X does not expose an HTTP bridge for observability`)
  instead of silently dialling a gRPC listener.
- **Dead `contextWithTimeout` wrapper inlined.** The handler now
  uses `context.WithTimeout` directly.
- **`redact.go` deleted.** Its comment moved to a doc comment on
  `LockHolderRow.Address` (where the `json:"-"` tag actually lives).
- **`itemsQueueView` errors no longer swallowed.** Postgres count
  failures are logged at WARN with selector + items_table before
  the function returns the -1 sentinel.
- **Discovery cache invalidation endpoint** at
  `POST /api/admin/refresh-discovery`; the SystemPage now renders
  cache age + a "Refresh discovery" button so operators don't have
  to wait the full TTL after rolling a peer.
- **CORS posture documented** in `docs/operator-guide.md`: the
  proxy collapses to single-origin; bypassing the proxy needs a
  CORS layer the operator owns.
- **`@blessed-invariant 11` doc note** added to the executor
  observability impls clarifying that the invariant scopes Rimsky
  core's behaviour toward the wire-format `userdata`, not the
  executor's introspection of its own trace data.
- **Filesystem ↔ postgres ledger duplication tracked.** Both files
  carry `@source` annotations now; the round-3 fixes (Subscribe,
  cursor stability, broadcast on every Record*) landed in both.

#### Smoke / test coverage

- **Smoke test drives a real dispatch.**
  `TestObservabilityDispatchEndToEnd` deploys the §11.5 template,
  fires the source node once, then asserts `/v1/observability/
  instances/{id}` returns a 4-node cascade-graph and `/events?
  instance_id=…` returns at least one row.



- **http-node `StreamTrace` no longer drops events under concurrent
  append.** Replaced the snapshot+gap+late-register pattern with a
  per-subscriber wakeup-pump model: subscribers register under the
  same lock that captures dispatch existence; `AppendEvent` appends +
  non-blocking-wakes via a coalescing capacity-1 channel; subscribers
  read directly from the per-dispatch slice at their own cursor on
  each wake. `MarkTerminal` closes a `done` channel so subscribers
  drain the tail and emit `trace_complete`. Applies to both gRPC
  `StreamTrace` (`executors/http-node/observability.go`) and the SSE
  bridge (`executors/http-node/observability_bridge.go`). Race-detector
  test exercises 16 goroutines × 25 events with no drops.
- **Schedules cursor pagination tested for dense same-timestamp
  case.** Added a conformance test
  (`testSchedulesDenseSameTimestampPagination`) that registers ≥3
  schedules sharing `next_fire_at` and asserts every row surfaces
  exactly once across pages with no duplicates and no drops.
  Validates the round-2 tuple-cursor fix on both Postgres and SQLite.
- **CustomUI panel relocated from ExecutorDetailPage to
  DispatchDetailPage.** Per spec §2.2 the executor
  `dispatch_url_template` substitution markers are `{dispatch_id,
  instance_id, node_type}` — none of which are in scope on a
  peer-detail page. The panel now renders on dispatch-detail pages
  where those markers are known; `handleGetDispatch` was extended to
  return `instance_id` and `node_type` (looked up via
  `Store.Nodes().Get`). Store-side substitutions (`{store_name,
  claim_id}`) on StoreDetailPage were already correct.
- **Postgres `items_table` regex centralized.** All three layers
  (`stores/postgres/cmd/main.go`, `stores/postgres/server/observability.go`,
  `stores/postgres/store/store.go::validIdent`) now reference
  `pgsstore.ItemsTableIdentRegex = ^[a-z_][a-z0-9_]*$`. Error
  messages aligned. Stricter than the previous cmd/server regex
  (which allowed uppercase) — Postgres folds unquoted identifiers to
  lowercase anyway.
- **Test coverage filled across the round-2 surface.** New tests:
  claude-agent gRPC `Execute` and HTTP `/execute` observability
  ledger assertions; store ledger non-terminal events
  (`RecordEvent` cases for `claim_commit_failed` /
  `claim_abandon_failed`); dashboard SSE proxy header forwarding +
  non-200 status propagation; http-node `StreamTrace` race-detector
  stress test.
- **Dashboard tsconfig project references.** `dashboards/rimsky-dashboard/tsconfig.json`
  now references `tsconfig.node.json`; the latter declares
  `composite: true` and `types: ["node"]`. Editor LSPs were resolving
  `src/server/*.ts` against the root config (which excludes `src/server`
  and lacks node types) and reporting spurious "Cannot find name
  'process'" / "Cannot find module './admin.js'" diagnostics on a
  build that was actually clean. The references entry tells the LSP
  which config governs server-side files; vite/tsc build behavior is
  unchanged.

### Fixed — Dashboard & observability v1 (round-2 review)

- **`claude-agent` traces now reach the dashboard from the gRPC dispatch
  path.** The TS executor's gRPC `Execute` handler shares a single
  `Observability` ledger with the HTTP+JSON bridge; both record events
  keyed by the supervisor-supplied `dispatch_id` (proto field 12) so
  `GET /observability/v1/trace/{dispatch_id}` resolves regardless of
  transport. Previously only the HTTP bridge fired ledger events, and
  even there it keyed by the freshly-minted `ackId` rather than
  `dispatch_id` — silently breaking the dashboard's executor-trace pane
  for the LLM executor in production.
- **Dashboard SSE proxy propagates upstream headers.** The proxy now
  sets `Content-Type: text/event-stream` (honoring the upstream value
  when present), `Cache-Control: no-cache`, and `Connection: keep-alive`
  before streaming the SSE body. Without these, browsers wouldn't run
  EventSource and intermediate caches could buffer the stream.
- **http-node observability replays without holding the writer lock.**
  Both the gRPC `StreamTrace` and the HTTP+JSON SSE handler now snapshot
  the events under the lock, release, and iterate lock-free. A slow SSE
  client can no longer stall `AppendEvent` / `MarkTerminal` / parallel
  streams across the executor.
- **Postgres store admin view rejects unsafe items_table values.** The
  `items_queue` admin view validates `pp.ItemsTable` against
  `^[a-zA-Z_][a-zA-Z0-9_]*$` before interpolating it into the
  `COUNT(*)` query, and the store-postgres binary applies the same
  check at config load. Defense-in-depth on top of `Store.New`'s
  existing identifier check.
- **`Commit`/`Abandon` failures no longer record terminal ledger
  events.** The postgres and filesystem stores now record a non-
  terminal `claim_commit_failed` / `claim_abandon_failed` event (new
  `ClaimLedger.RecordEvent` helper) when the store-side action errors;
  `RecordTerminal` runs only on success. Previously the dashboard saw
  the claim as committed even when the store rejected the transition.
- **`EventStore.Tail` removed.** Dead method dropped from the interface
  and both impls (no callers); after the recent List DESC ordering
  change, leaving Tail in place would have surprised the first future
  caller that expected oldest-first.
- **Schedules cursor encodes `(next_fire_at, node_id)`.** Both drivers
  now use a base64-JSON cursor of both fields with the strict tuple
  comparator `(next_fire_at, node_id) > ($t, $id)`. Previously dense
  scheduling (multiple schedules sharing a `next_fire_at`) silently
  lost rows at page boundaries.
- **Dashboard `useCursor` exposes `canGoBack`.** `ResourceTable` now
  disables Prev based on in-memory history depth rather than
  `cursor === ''`, prophylactically correcting the "first page after a
  non-empty initial cursor" edge case.
- **Custom UI templated URLs reach the dashboard.** Both store and
  executor detail pages now pass
  `template={caps.custom_ui.dispatch_url_template}` to `CustomUIPanel`,
  enabling the spec §2.2 / §3.5 path-templating feature. The phantom
  `claim_url_template` field is removed from the dashboard's
  `CustomUI` type — the proto reuses one field name across both peer
  kinds.
- **CLI `ListInstancesQuery.InstanceKey` removed.** The control-api's
  `/instances` endpoint never honored `instance_key`; the field is
  gone from `cli.ListInstancesQuery`, the URL builder, and the
  `clitest` fake server's `/instances` handler. Instance-key lookups
  go through `/instances/{idOrKey}`.
- **Schedules page paginates.** `SchedulesPage` rebuilt on
  `ResourceTable` so deployments with more than ~50 schedules are
  reachable.
- **Dashboard proxy splits on the URL parameter.** `/api/exec/:name/*`
  and `/api/store/:name/*` strip the prefix using `c.req.param('name')`
  instead of the resolved peer's `.name`. Same string today, but no
  longer relies on identity if discovery ever returns aliased peers.

### Added — Dashboard & observability v1

- **Three public observability protocols.** Per
  `docs/specs/2026-05-02-dashboard-and-observability-design.md`:
  - **Rimsky observability API** at `/v1/observability/*` on
    `rimsky-control-api`. Read-only resource-oriented browse + detail
    endpoints over `rimsky_*` tables (templates, instances, frames,
    nodes, dispatches, lock-holders, schedules, events, system
    health/summary). Backed by `core/observability/` — a new package
    that imports `core/persistence/` for shared types but is forbidden
    from importing `core/config/`, the per-driver subpackages, or any
    of `core/scheduler/`/`core/supervisor/`/`core/controlapi/`.
  - **Executor observability protocol** in
    `proto/v1/executor_observability.proto`. `GetCapabilities`,
    `GetTrace`, `StreamTrace`. Capabilities response includes a new
    `http_bridge_url` field (spec §2.2) so dashboards can dial the
    peer's HTTP+JSON bridge directly instead of guessing from the
    dispatch endpoint. Reference impls landed for `executors/stub/`
    (capabilities-only), `executors/http-node/` (in-memory trace
    store with retention sweep + per-dispatch broadcaster + dispatch
    hooks emitting `step_started`/`step_completed`/`step_failed`/
    `error` events keyed by the new `dispatch_id` field on
    `ExecuteRequest`), and `executors/claude-agent/` (HTTP+JSON
    bridge mounting `/observability/v1/*` routes; bounded ledger;
    spec-§2.6 evicted-shape semantics).
  - **Store observability protocol** in
    `proto/v1/store_observability.proto`. `GetCapabilities`,
    `GetClaim`, `StreamClaim`, `ListClaims`, `GetAdminView`.
    Capabilities response includes the same new `http_bridge_url`
    field (spec §3.5). Reference impls landed for `stores/stub/`
    (capabilities-only), `stores/filesystem/` (admin views:
    `pick_policies`, `policy_items` + per-claim ledger), and
    `stores/postgres/` (admin views: `pick_policies`, `items_queue`
    + per-claim ledger). Both store impls expose the HTTP+JSON
    bridge via the new `bridge.MountObservability` helper.
- **Observability handshake on control-api.** `core/config/StartControlAPI`
  now runs a best-effort `Capabilities()` probe against each declared
  executor and store endpoint at startup, captures the peer's
  `http_bridge_url`, exposes it on `PeerEntry.http_bridge_url`, and
  starts a background re-prober (`RIMSKY_OBSERVABILITY_REFRESH_INTERVAL`,
  default `60s`). Distinct from the existing fail-fast dispatch
  handshake — observability is optional; unreachable peers do not
  abort startup.
- **`rimsky.yml` schema additions.** Each `executors:` and `stores:`
  entry gains an optional `observability_endpoint:` field — used when
  a peer splits its gRPC observability service onto a separate port
  from dispatch. The HTTP+JSON bridge URL is per-peer-config (e.g.
  `http_bridge_url:` in the filesystem/postgres store YAML, or
  `RIMSKY_EXECUTOR_HTTP_NODE_HTTP_BRIDGE_URL`/
  `RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL` env vars on the
  executors), advertised through the capabilities handshake.
- **`--check-observability` flag** on `rimsky-conformance` and
  `rimsky-store-conformance`. Probes capabilities, validates the
  spec-§2.6/§3.6 missing-dispatch / missing-claim shape via
  `GetTrace`/`StreamTrace`/`GetClaim`/`StreamClaim`, exercises
  `ListClaims` when supported, validates the spec §2.4 standard-
  vocab attribute requirements on any returned events, and
  round-trips every parameter-less admin view per spec §6.
- **Reference dashboard** at `dashboards/rimsky-dashboard/`. React +
  Vite + TypeScript SPA + Hono Node server; bundled with the dev
  `docker-compose` stack and started by default. The Node server
  exposes `/healthz` and reverse-proxies `/api/control/*`,
  `/api/exec/{name}/*`, `/api/store/{name}/*` to the corresponding
  observability endpoints (with SSE pass-through, using the
  handshake-derived `http_bridge_url` per peer). 18 routes across
  System, Templates, Instances, Frames, Nodes, Dispatches,
  LockHolders, Schedules, Events, Stores, Executors. Tailwind v3 +
  hand-rolled shadcn-style primitives; vitest + dashboard proxy
  unit tests.

- **Filesystem store: pick-policy support.** The standard `stores/filesystem/`
  store-service grows a `pick_policies` config block paralleling the pg
  store's. Auto-discovery: folders under each policy's configured sub-root
  are queue items; `mkdir`/`rm -rf` is the insertion/removal mechanism
  (no admin items endpoint). Three actions ship: `release_to_back`,
  `release_to_head` (absolute mtime-zero bump — stronger than pg's relative
  priority increment), and `delete` (`os.RemoveAll`). Atomic claim is
  `rename(2)` between `<root>/.fs-store/<policy>/{available,in_progress}/`.
  Bump-to-head admin endpoint at `POST /admin/bump-to-head/{selector}`.
  `sync_strategy: on_open` (default) or `on_sweep` per policy.
  Per `docs/history/2026-05-03-fs-store-pick-policies-design.md`.

- **Conformance coverage for per-feature interface methods + tighter
  pgx-isolation depguard.** Added four cross-driver conformance areas
  exercising the methods landed during the Tasks 23-28 pgx-removal
  refactor — `Queue.EnqueueInTx` / `RemoveForNodeInTx` /
  `GetDispatchNode` (`queue_in_tx.go`), `LockHoldersStore.UpdateRegion`
  (`lock_holders_update_region.go`), `NodeStore.MarkStaleForCascade`
  (`nodes_mark_stale_for_cascade.go`), and
  `NodeAttributesStore.MergeDelta` (`node_attributes_merge_delta.go`,
  including the wrapped-`persistence.ErrNotFound` sentinel check). All
  four pass against both Postgres and SQLite drivers. Migrated
  `core/frame/{engine,producer}_test.go` and the four
  `test/scenarios/frame_resolution/*_test.go` files that still reached
  for raw pgx (`coalesce_concurrent_invalidates_test.go`,
  `frame_start_atomicity_test.go`, `frame_timeout_reaper_test.go`,
  `orphan_dispatch_reaper_claimant_guarded_test.go`) onto the
  persistence driver + a new pair of harness helpers
  (`scenario.Harness.ExecSQL` / `QueryRowSQL` / `QuerySQL`) plus a
  pgtest helper (`pgtest.QueryForTest`) that walks rows without
  exposing `pgx.Rows` to non-whitelisted packages. With those files
  off pgx, the depguard `pgx-isolation` allow-list shed two carve-outs
  (`!**/core/frame/*_test.go`,
  `!**/test/scenarios/frame_resolution/*_test.go`).
- **Persistence layer pluggable; unified `rimsky/all` image scaffold.**
  Land Tasks 19-22, 29-33, 36, 39-45 from
  `docs/plans/2026-05-02-persistence-pluggable-and-unified-image.md`.
  - `core/persistence/` is now the protocol package: `Driver` (open /
    close / migrate / accessor surface), `Coordinator` (advisory locks +
    migrate locks), `Store` (per-feature accessors including the new
    `FrameStore`), and `Queue`. Driver impls live under `postgres/` (the
    canonical pgx-backed impl, lifted from `core/storage/postgres/` and
    `core/queue/postgres/`) and `sqlite/` (a dev-only `modernc.org/sqlite`
    driver).
  - `FrameStore` interface added so the frame engine no longer depends
    directly on `*pgxpool.Pool`; the postgres backend implements it and
    the supervisor / scheduler / controlapi packages drive it through
    `persistence.Store` instead of bare SQL.
  - All four cmd binaries (`rimsky-migrate`, `rimsky-scheduler`,
    `rimsky-supervisor`, `rimsky-control-api`) now open a
    `persistence.Driver` via `persistence.Open(ctx, cfg.Persistence)` at
    startup. `RIMSKY_DB_URL` is gone; persistence config moved into the
    `persistence:` block of `rimsky.yml` (`RIMSKY_CONFIG`).
  - **Transition window.** The runtime packages still hold
    `*pgxpool.Pool` internally during Tasks 23-26; the cmd binaries
    extract the pool via the temporary
    `pgpersist.PoolFromDriverOrNil(driver)` helper. When the driver is
    not postgres (i.e. SQLite today), the binaries log a clear hint and
    exit 1 — the SQLite driver is **not** yet wired through to the
    runtime packages, and `Driver.Store()` / `Driver.Queue()` return nil
    until Tasks 34-35 land.
  - `rimsky-entrypoint` PID-1 process supervisor added under
    `core/cmd/rimsky-entrypoint/`. Runs `rimsky-migrate` synchronously,
    spawns the three runtime binaries concurrently, forwards
    SIGTERM/SIGINT, exits when any child exits or the deadline fires.
    Used by the new `rimsky/all` unified Docker image.
  - `deploy/Dockerfile.all` + `deploy/rimsky-all.yml` ship the unified
    image. **The bundled SQLite default is currently a structural
    skeleton** — operators must override `/etc/rimsky/rimsky.yml` to
    point at `driver: postgres` to run end-to-end work today (until
    Tasks 23-26 + 34-35 land). Documented in `CLAUDE.md` and
    `docs/operator-guide.md` §2.5.
  - SQLite driver: single hand-written `001-initial.sql` capturing the
    union schema; coordinator backed by `sync.Mutex` (single-process is
    the only supported topology); loud startup banner per spec §1.
    Per-feature impls (Task 34) and queue impl (Task 35) are still
    pending — `Driver.Store()` and `Driver.Queue()` return nil until they
    land. Integration tests query PRAGMA state on the driver's actual
    `*sql.DB` (via the test-only `pgsqlite.DBFromDriver` accessor) so
    they can't pass against a parallel handle.
  - `RIMSKY_LOG_BINARY` env var added: when set, every binary's slog
    output gains a structured `binary` field. Used by `rimsky-entrypoint`
    to disambiguate combined stdout/stderr in the unified image.
  - **Still ahead.** Tasks 23-26 (drop pgx from supervisor / scheduler /
    controlapi; delete escape hatches in `core/persistence/postgres/transition.go`
    and the `core/storage/` + `core/queue/` adapter packages); Tasks 34-35
    (SQLite per-feature + queue impls); Tasks 37-38 (conformance suite
    scaffolding + test bodies across 11 areas).

- **`claude-agent` `cwd_from_store` workspace binding.** The TypeScript
  claude-agent executor now reads two new optional `userdata` keys at
  dispatch and, when set, `chdir`s the spawned `claude` subprocess into a
  workspace the supervisor has already serialized via a store claim:
  - `userdata.cwd_from_store: <store-name>` — looks up
    `ExecuteRequest.stores[<store-name>].handle.address` (the
    filesystem store fills this with an absolute path) and uses it as
    the CLI's cwd.
  - `userdata.cwd: <path>` — raw override of last resort; lower
    priority than `cwd_from_store`.
  The address must point to an existing directory at spawn time;
  validation errors surface as `invalid_cwd_from_store` errored
  outcomes before the spawn. Closes the gap where the supervisor was
  delivering store handles via `ExecuteRequest.stores` but the executor
  silently dropped them. Combined with the filesystem store's
  concrete-path conflict semantics (two claims on the same path
  conflict, two claims on different paths do not — `stores/filesystem`),
  this gives templates a clean primitive: declare a directory selector
  with `intent: rw`, set `cwd_from_store` to that store's name, and the
  spawned agent owns that directory exclusively for the duration of
  the run. **Operator note:** the executor pod must mount the
  store-service's volume at the same absolute path the store-service
  uses, since the address bytes flow through verbatim.

- **`claude-agent` CLI auth precedence + env hygiene.** The TypeScript
  claude-agent executor (`executors/claude-agent/`) now reads
  `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` at startup and
  requires at least one in non-stub mode (it exits fatally otherwise).
  Resolution order: `ANTHROPIC_API_KEY` wins (production — written to a
  0600 temp file, `apiKeyHelper` shell wrapper points
  `$HOME/.claude/settings.json` at it, key never enters the child env);
  `CLAUDE_CODE_OAUTH_TOKEN` is the dev fallback (passed through on the
  child env). The spawned `claude` subprocess no longer inherits the
  parent `process.env` — only `HOME`, `PATH`, the auth env, and the
  per-run `RIMSKY_CALLBACK_URL` / `RIMSKY_CALLBACK_TOKEN` reach it,
  keeping unrelated pod env (DB DSNs, internal callback secrets) out of
  the CLI. Pattern ported from `skillprompting/brain/src/cli-env.ts`.
  New `cli-env.ts` module with `buildCliEnv` + cleanup hook;
  `createClaudeCliRunner` now requires a `CliAuthConfig`
  (breaking — pre-v1 break-freely rule applies).

- **Persistence cutover phase 4: lock-holders + attributes accessor consolidation.**
  Land Tasks 17–18 from
  `docs/plans/2026-05-02-persistence-pluggable-and-unified-image.md`.
  - Delete `core/store/lockholders.go`. The supervisor and scheduler
    now reach the rimsky-lock-holders accessor through
    `persistence.LockHoldersStore` (sourced from
    `pgpersist.StoreFromPool(pool).LockHolders()` while the cmd
    binaries remain on `*pgxpool.Pool`; this collapses to a clean
    `Driver.Store().LockHolders()` call when Task 22 lands).
  - Delete `core/attributes/store.go` (the standalone pgx-backed
    `*Store` impl). The local `attributes.NodeAttributesStore` interface
    and `Row` type move into `core/attributes/callback.go` since only
    the §12.5 incremental-writeback HTTP handler depends on them. The
    canonical persistence-side impl lives at
    `core/persistence/postgres/node_attributes.go`; the supervisor's
    callback handler still bridges through its existing
    `attributesStoreAdapter` (which now wraps `storage.NodeAttributesStore`
    until Task 23 switches the supervisor's `cfg.Storage` to
    `persistence.Store`).
  - The storage-package adapter (`core/storage/postgres/lock_holders.go`)
    delegates to `persistence.LockHoldersStore` instead of the deleted
    `*store.LockHoldersClient`. Tests that previously constructed
    `store.NewLockHoldersClient(pool)` for `RunArgs.LockHolders` now use
    `pgpersist.StoreFromPool(pool).LockHolders()`. Pre-v1 break-freely
    rule applies; no behavioral change.

- **Postgres store: drop unused `type:` field on pick policies.** The
  `pick_policies[*].type` YAML key (and `PickPolicy.Type` Go field) was
  parsed from config and propagated into the in-memory struct, but no
  code path read it — `Open` / `Commit` / `Abandon` / sweep behavior
  was already fully governed by `on_commit_default` /
  `on_give_up_default` (`delete` = drain, `release_to_back` = recycle,
  `release_to_head` = retry-at-front). Removed the field from the
  struct, the YAML schema, the package-doc example, the
  `config-example.yml` reference, the `deploy/store-postgres.yml`
  reference, the operator-guide example, and the test/smoke fixture.
  Queue-vs-ring is documented as emergent from the action defaults, not
  switched on a discriminator. No behavioral change; the YAML loader
  uses non-strict `yaml.Unmarshal`, so legacy configs that still carry
  `type:` are silently ignored at startup — operators may remove the
  key at their convenience. Pre-v1 break-freely rule applies (no
  production data; no compat shim).

- **`rimsky-cli` and `rimsky-compose.yml`.** Add an operator-facing CLI
  (`core/cmd/rimsky-cli/`) plus a `rimsky-compose.yml` declarative
  manifest format (`core/cli/compose/`). The CLI is a thin client over
  the existing control-api: ergonomic top-level verbs (`run`, `register`,
  `deploy`, `instantiate`, `ls`, `logs`), literal API subgroups
  (`template`, `tag`, `instance`, `node`, `admin`), kubectl-style
  contexts (`ctx list/use/add/rm/current`), and compose-style
  reconciliation (`compose up/down/plan/status`, `dev up/down/status`).
  Compose owns project-prefixed names (`compose:<project>:<tag>`,
  `compose:<project>:<name>`); manual API calls outside that prefix are
  invisible to compose. Apply-once-and-exit, fail-fast with resumable
  retry, exit code 3 on `compose plan` drift (mirrors `terraform plan
  -detailed-exitcode`). Distribution: GitHub Releases, install script,
  Homebrew tap, `go install`, distroless `rimsky/cli` Docker image. Per
  `docs/history/2026-05-02-rimsky-cli-and-compose-design.md`.

  Post-implementation review fixes:
  - Embedded `deploy/docker-compose.yml` v1 scaffold trimmed to the
    minimal init-supported services (no `store-postgres` / `init-items`)
    and remounts `./.rimsky/rimsky.yml` to match the materialization
    target so `dev up` against a fresh `init` directory does not
    block on missing files. `cli-sync-embedded` Makefile target rewrites
    the same transforms when re-syncing from `deploy/`.
  - Endpoint resolution split into `ResolveEndpoint` (non-compose:
    flag > env > config) and `ResolveEndpointForCompose` (compose:
    manifest-pin > flag > env > config), matching spec §4.1's
    compose-verb override clause and unblocking the manifest's role
    as a deployment pin.
  - `instance events --follow` now tracks a watermark across poll
    cycles instead of relying on `next_cursor` (which the live
    control-api only sets on full pages); the clitest fake mirrors
    that contract.
  - `tag mv` and `tag rm` reject the `compose:` prefix, matching the
    existing `tag create` / `template register --tag` guard.
  - Stateful clitest fixture's `GetTemplate`, `ListTemplates`,
    `GetNode`, `ListNodes` now return value copies, eliminating a
    latent race when concurrent tests mutate state.
  - Plan steps carry an explicit `Destructive bool` set at plan time;
    `destructive()` is a one-line check on the bool plus the live
    undeploy-active-bindings precheck (computed once per apply, not
    once per step).
  - `dev up` / `dev down` forward `--no-color` and `-o` onto the
    delegated compose verb.
  - `--no-color` is consumed by `EmitTable` (bold ANSI headers when
    color is on) and by `formatStep` (green/red `+`/`-` markers).
  - `dev down` loads the manifest once and threads it through the
    optional `infra.down` hook.
  - Compose-up `Source` field sent to the control-api is now
    `manifest:<project>:<tag>` rather than the operator's absolute
    filesystem path.
  - Embedded scaffold's `example.yml` is validated against the same
    executors / stores declared in the embedded `rimsky-compose.yml.tmpl`
    so a misspelled executor in the example would fail the embedded
    test rather than at first `dev up`.
  - Cycle-3 review: `compose plan` now exits 3 when params drift on a
    non-terminal compose-owned instance is detected (mirrors `terraform
    plan -detailed-exitcode`), driven by a new `Plan.HasDriftWarnings`
    field set by `ComputePlan` when it emits the stderr warning;
    embedded `docker-compose.yml` no longer ships the unused
    `claude-agent` executor (it isn't declared by the init scaffold's
    inline `rimsky_config:` and would block the supervisor's
    `depends_on`); `cli-sync-embedded` Makefile target gained a
    matching trim for `claude-agent` plus a buffered-comment pass so
    orphan comments above stripped service blocks no longer leak;
    `RunHealth` and `RunCtxList` now propagate `--no-color` via
    `SetActiveCommonFlags`; smoke test cleanup falls back to a direct
    `docker compose down -v` when the CLI invocation can't reach the
    control-api; dead `ApplyOpts.Yes` field, dead helper functions
    (`hasReservedPrefix` / `hasComposePrefix` / `truncShort` /
    `truncHash`) consolidated to `strings.HasPrefix` and a single
    exported `cli.TruncHash`.

- **Template-spec JSON tags.** Add `json:` struct tags to every wire-relevant
  field of `core/node/template.go`, `core/node/policy.go`, and
  `core/qualityrule/spec.go`, then delete the JSON shadow-type tree and
  `toTemplateSpec` mapper from `core/controlapi/templates.go`, the
  `toJSONShape` helper from `core/cli/templates.go`, the `yamlToJSON` helper
  and YAML→generic-map round-trip from `core/cli/compose/resolver.go`, and the
  `hashRewrite` defense from `core/cli/compose/apply.go::ApplyPlan` (which
  existed only to absorb the JSON-tag asymmetry that this change fixes).

  **Hash-bytes change.** `canonical.CanonicalSpecHash` now marshals
  `TemplateSpec` with lowercase-snake-case JSON keys (`name`, `nodes`,
  `params_schema`, …) instead of the old capital-cased Go-field-name keys
  (`Name`, `Nodes`, …) that came from the missing tags. As a follow-up,
  `TemplateNodeDef.Attributes` is now `*NodeAttributesDef` (pointer)
  rather than a value, restoring the deleted shadow-tree's `omitempty`
  behaviour so nodes without an `attributes:` block no longer emit a
  bloated `"attributes":{}` into the canonicalized bytes — this shifts
  hashes a second time within the same Unreleased window. Every existing
  template's content hash changes. There are no production templates;
  dev-DB users must drop and recreate the postgres volume:

  ```
  docker compose -f deploy/docker-compose.yml down -v
  docker compose -f deploy/docker-compose.yml up -d
  ```

  Per `docs/history/2026-05-02-template-spec-json-tags-design.md`.

- **Control-plane v1 + store lifecycle protocol.** Templates are now
  content-addressed (`rimsky_templates.id` is `sha256-<64-hex>` over RFC 8785
  JCS-canonicalized spec); tags are movable aliases in `rimsky_template_tags`.
  Four-state template lifecycle (registered/deployed/undeployed/deregistered).
  Six new RPCs on `StoreService` (`OnTemplateRegistered`/`Deployed`/
  `Undeployed`/`Deregistered` + `OnInstanceCreated`/`Terminated`); all stores
  implement all six (the rimsky-side `Store` interface ships an embeddable
  `LifecycleNoop` for stores that don't react). `OpenRequest` gains
  `template_id` and `instance_id` fields. Per-(store, scope) bookkeeping in
  `rimsky_store_lifecycle` drives idempotent fan-out. Unified `rimsky.yml`
  (`RIMSKY_CONFIG`) replaces `RIMSKY_STORES_CONFIG` and the supervisor's
  `executors:` block — declares stores, named_locks, and executors in one
  place. Control-api gains `ExecutorDeclared` validation hook. Per
  `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`.
  Pre-v1: drop+recreate of `rimsky_templates`/`rimsky_instances`; existing
  dev DBs nuked.

- **Stores Protocol Cleanup — store-internal-vocabulary excision.**
  Drops `policy_override` from `CommitRequest` / `AbandonRequest`,
  deletes the `Delete` wire verb (4+1 verbs, was 5+1), replaces
  `OpenResponse`'s implicit all-empty-bytes pool-empty signal with
  an explicit `oneof Acquired | Unavailable` discriminator, and
  removes the `claim_resolutions` template grammar
  (`node.ClaimResolution` Go type deleted; `selectResolutionAction`
  and `fireResolutionVerb` deleted from
  `core/supervisor/auto_terminal.go`). Store disposition
  (commit-vs-release-vs-delete on the store's own state) is
  governed entirely by per-store config (e.g. the postgres
  reference store-service's per-pick-policy `on_commit_default` /
  `on_give_up_default`). Bridge handler switches from
  `encoding/json` to `protojson` for response marshaling so the
  new oneof round-trips correctly. Spec:
  `docs/history/2026-04-30-stores-protocol-cleanup-design.md`.
  Supersedes v3 §4.1 / §4.5 / §4.7 third-paragraph / §4.10
  invariant 13.1 / §5.1 / §5.2 / §7.8 obligation #3.

- **http-node: fix stub-mode userdata validation ordering bug.**
  `executeCore` validated `userdata.url` before the stub-mode
  short-circuit, so the conformance suite's executor-agnostic
  scenarios (which send `{stub_probe: true}` with no URL) errored
  out before reaching `executeStub`. Move the stub-probe escape
  hatch ahead of URL validation so the suite passes; the
  `malformed_userdata` scenario (which omits `stub_probe`) still
  exercises the URL check. Discovered while running the v3 T57
  conformance verification against the reference http-node.
  (`executors/http-node/server.go`)

- **Stores Redesign v3 — out-of-process store-services.** Standard
  store implementations (`filesystem`, `postgres`, `stub`) move from
  in-process Go subpackages of `core/store/` to standalone binaries
  under `stores/<kind>/`. Rimsky processes (`rimsky-supervisor`,
  `rimsky-scheduler`, `rimsky-control-api`) talk to them exclusively
  via the new 5+1-verb gRPC protocol defined in
  `proto/v1/store_service.proto` (Open / Commit / Abandon / Delete /
  Release plus a startup Capabilities() handshake). Spec:
  `docs/history/2026-04-27-stores-redesign-v3-design.md`.

  Headline changes:
  - **`Factory` / `Registry.BuildAll` / `StoresConfig` removed**
    from `core/store/`. Registry collapses to a name → Store map
    populated externally by each rimsky cmd binary at startup.
  - **`stores.yml` schema rewritten**: thin name → endpoint +
    declared capabilities form (no `kind`, no `connection`, no
    `pick_policies` — store-service-specific keys live in each
    store-service's own config).
  - **Atomicity decoupled** (invariant 10 clarified): rimsky's
    bookkeeping tx is independent of the store-service's tx. The v2
    tx-sharing mechanism (`store.WithTx` / `TxFromContext`) is gone
    along with `core/store/tx.go`. Store atomicity is the store's
    concern (per the new §7.8 obligations).
  - **Region conflict is byte-equal** (invariant 14 retired):
    `Store.RegionsConflict` and `Store.UnmarshalRegion` are removed;
    rimsky compares `rimsky_lock_holders.region_data` byte-for-byte.
    Stores canonicalize region bytes such that byte-equal
    indicates conflict.
  - **Filesystem store-service: glob support dropped**
    (concrete-paths only). Operators needing globs write a custom
    store-service.
  - **4 inertness violations gone (structurally impossible)**: the
    rimsky-side admin items endpoint
    (`/admin/stores/.../pick-policies/.../items`), the pick-policy
    validator hook, the scheduler visibility-timeout sweep, and the
    `*pgstore.Store` store-internal methods (`InsertItems`,
    `PickPolicyConfig`, `PickPolicies`). The postgres store-service
    ships with its own admin endpoint for items insertion (separate
    listener port).
  - **`rimsky_lock_holders.id` generated client-side** (so it can be
    passed to `Store.Open` as `claim_id` per spec §4.2). Column
    default `gen_random_uuid()` retained as safety net.
  - **Invariant 15 revised**: `Open` still fires inside the
    rimsky-side acquisition tx, but the store's state mutation runs
    in its own tx.
  - **Held-claim resolution mechanically updated**: store verb
    calls go through the remote-client gRPC path; the store-side
    action runs in its own tx (no longer shares a tx with the
    lock-holder DELETE).
  - **Deployment**: three new Dockerfiles (`stores/{filesystem,
    postgres,stub}/Dockerfile.<kind>`); `deploy/build-images.sh`
    builds all 9 images; `deploy/docker-compose.yml` adds two new
    services (`store-filesystem`, `store-postgres`) and removes the
    `init-items` one-shot's coupling to rimsky's admin route.

- **Stores Redesign v2 — code-review correctness pass.** Closed the
  `findInheritedAliasesForNode` cartesian-product bug (per-row
  resolution joining the lock-holder back to the acquirer NodeType +
  alias; aliases disambiguated via the substituted selector when an
  acquirer declares multiple stores against the same store_name). The
  per-node claim_holders flip is now a single targeted UPDATE on
  `(lock_holder_id, holder_node_id)` (`CompleteByLockHolderAndNode`)
  rather than the prior list-then-loop. The terminal release path
  reads `region_data` and `address` from the lock-holder row inside
  the release tx (per spec §13.6), removing the dependency on
  `lk.ClaimResult` for async-callback resumed flows. `buildLockSpecs`
  now resolves `{{claim.<alias>...}}` substitutions against the
  inheritor's live claim-holder rows so downstream selectors that
  reference inherited claims resolve at dispatch time. Inheritance
  validator now rejects ambiguous-acquirer inheritance (multiple
  reachable acquirers per inheritor) at deploy time, and
  `HoldingSubgraphsForTemplate` reproduces the same deps-walk so
  deploy-time and runtime subgraph computations agree. `FrameID` is
  now plumbed through `storage.LockHolderInsertInput` and
  `storage.ClaimHolderInsertInput` so the storage adapter populates
  `frame_id` on writes (observability-only per spec §12.10/§12.11).
  Postgres factory honors `connection: postgres://...` per-store
  config — opens its own pool for the store rather than silently
  reusing the platform pool. Strict equality `err == pgx.ErrNoRows`
  in `auto_terminal.go` switched to `errors.Is`. Cleaned dead
  `RebindForResume` / `ListByNodeAndStore` / `ClaimEligibilityInput`
  / `ClaimHolderAction*` symbols and the stale doc comments
  referencing the dissolved `AcquireLock` / `OpenHandle` /
  `ReleaseLock` / `claim_store-postgres` vocabulary. CLAUDE.md now
  references the v2 spec; proto and TS executor stale spec
  references repointed; TS bindings dropped the wire-reserved
  `resumed?` field; `make proto-gen` regenerated `node_executor.pb.go`
  with the reserved fields removed. Reconciled blessed-invariant 20
  doc-blocks: `walkPath` is the sanctioned payload-walk site,
  `stringifyRaw` is the sanctioned address/region shape-flattener,
  and `runner_dispatch.go::makeStoreHandle` is the sanctioned
  wire-encoding site. Added substantive tests: postgres store
  Open/Commit/Abandon/regional/factory-rejects-bad-items-table/
  factory-honors-connection (`core/store/postgres/store_test.go`),
  CompleteByLockHolderAndNode + LockHolders FrameID round-trip
  (`core/storage/postgres/postgres_test.go`), CheckAndFireResolution
  aggregate-completed and aggregate-failed paths (`core/supervisor/
  auto_terminal_test.go`), end-to-end claim release at terminal
  (`test/scenarios/locks/atomic_acquisition_test.go`), regional
  claim run-to-completion (`test/scenarios/stores/
  regional_claim_test.go`), params substitution at dispatch and
  required-source-failure routing (`test/scenarios/attributes/
  substitution_dispatch_test.go`), and auto-terminal aggregate-
  outcome with active-row-blocking guard (`test/scenarios/claim_stores/
  auto_terminal_aggregate_outcome_test.go`).

- Stores Redesign v2 (third major rewrite of core/store/):
  - 5 protocol verbs (Open, Commit, Abandon, Delete, Release) replace the prior AcquireLock/OpenHandle/Commit/ReleaseLock shape.
  - Two-noun primitives split: claim (store-bound) vs named lock (store-independent).
  - Pick policies are store-side via store-recognized selector forms (`@policy-name` convention).
  - Held claims via explicit `inherits:` declarations; auto-terminal at holding-subgraph completion.
  - Capability struct collapsed to one field (write_semantics).
  - Schema: rimsky_lock_holders gains address column, drops claim_id; rimsky_claim_holders gains lock_holder_id FK, drops actual_action/delete_won.
  - Inertness invariant 20 added; pre-sweep type-hardening of claim-content fields to json.RawMessage.
  - Operator config gains named_locks: top-level block.
  - Versions permanently eliminated; versioned mode does not exist.
  - claim-store-postgres renamed to postgres; pick_policies block configures multiple named pick policies per store.

- **Held-claim resolution: per-active-cycle uniqueness + frame-scoped sibling counts.** The smoke test was reproducibly stranding 2–3 items in `topics_items.state='in_progress'` after Phase 2 cascade-steady-state. Root cause: ring-buffer claim stores reuse `claim_id` (= items-table `item_id`) across cycles, but the `rimsky_claim_holders` unique index on `(claim_id, holder_node_id)` was unconditional. The second cycle's `insertHeldClaimHolders` failed the unique constraint against the prior cycle's now-completed row; the supervisor's commit tx rolled back, but the acquisition tx had already flipped the items-table row to `in_progress`, leaving it stranded. Fixes: (1) the unique index is now partial on `state='active'` (`core/migrations/001-initial.sql`), enforcing "one ACTIVE holder per (claim, leaf) at a time" while permitting historical rows to coexist; (2) `claimstorepg.Store.ResolveOnTerminal` filters its FOR UPDATE SELECT on `state='active'` and scopes the §5.6.4 sibling-count predicates by `frame_id IS NOT DISTINCT FROM R.frame_id` so prior cycles' completed delete/delete_won rows don't leak into the "did anyone delete?" check for a fresh cycle on a reused claim_id. Spec §5.6.4 + §9.9.3 updated to match. Smoke test gains an on-failure diagnostic dump in `assertFinalState` so a future regression prints stuck items + their claim_holders + dispatch rows without manual `psql` instrumentation. Smoke now passes 3-of-3 consecutive runs (~47s each).
- **`docs/architecture.md`** gains a new §4.1.1 "Frame engine" section describing `core/frame/` (producer + engine), how `frame.RunTick` runs under the existing scheduler advisory lock, and how `frame_id` propagates through the schema. Cross-references the frame-resolution design doc and the conceptual section in `docs/node-graph-design.md`.
- **`runner_terminal.go` cascade-message comment** updated to describe what the SQL guard `(state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))` actually defends against under the frame model — `Create()` defaults to `'fresh'`, so the `stale + no-frame_id` branch is a defensive backstop for orphan-reap recovery / future paths, not the initial-create case the prior comment named.

- **Frame resolution** (single coherent change; see `docs/history/2026-04-26-frame-resolution-design.md`). The cascade engine gains a first-class **frame** primitive — a complete pass over the reachable subgraph from one or more invalidation sources, executing serially per instance. Two modes: `frame_resolution: serial_queue` (each invalidate produces a distinct frame, FIFO) and `frame_resolution: coalesce` (mid-render invalidates collapse into one trailing frame). Required at the template level — control-api rejects template uploads without it. Default `frame_timeout_ms` is 600000 (10 min), hard floor 60000. Closes the smoke-test cascade-coalescing gap. **BREAKING:** dev DB must be nuked; templates must declare `frame_resolution`.
  - **New schema:** `rimsky_frames` table (frame_id, instance_id, mode, state, source_node_ids, queued_at, started_at, ended_at, frame_timeout_ms) with `uq_rimsky_frames_running` (at most one running per instance) and `uq_rimsky_frames_coalesce_queued` (at most one pending coalesce row per instance). `frame_id` columns added to `rimsky_dispatch` (NOT NULL), `rimsky_nodes` (nullable, cleared at fresh, preserved at failed), `rimsky_lock_holders` (observability), `rimsky_claim_holders` (observability).
  - **New package:** `core/frame/` — `EnqueueOrCoalesce` producer helper (called from `core/scheduler/invalidate.go`'s `InvalidateNode`, schedule_ticker, and admin force-fire indirectly) and `RunTick` engine (frame-end detection, queued→running advancement, stuck-frame reaper, orphan-dispatch reaper). The scheduler tick invokes `frame.RunTick` under the existing `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`.
  - **Removed:** `rimsky_nodes.kill_requested` column, `core/supervisor/runner_dispatch.go::isKillRequested`, the heartbeat-tick kill-poll path in `core/supervisor/supervisor.go`, the controlapi `POST /nodes/{id}/kill` route + `handleKillNode` handler, and the `KillRequested` field on storage's `NodeRow` + storage interface. Operator-originated invalidates now enqueue or coalesce a frame; in-flight work is never preempted.
  - **New blessed invariants** (§18 — 15-19): (15) frame_resolution mode is mandatory and per-template; (16) at most one running frame per instance; (17) at most one queued coalesce frame per instance; (18) frame-start atomicity (queued→running CAS + source-stale writes in one tx); (19) frame_id flows with cascade — no NULL on dispatch rows or non-fresh node rows.
  - **New scenario suite** at `test/scenarios/frame_resolution/` (14 tests covering all 5 invariants, both modes, async handoff, pruning via changed:false, frame timeout reaper, failed-node frame outcomes, controlapi rejection of bad templates).
  - **Smoke fixture** declares `frame_resolution: serial_queue`, restoring the §19.2 acceptance predicate (≥100 terminal commits per 100 force-fires).
  - **Migration 002** (`core/migrations/002-frame-resolution.sql`): creates `rimsky_frames`, adds `frame_id` columns, drops `kill_requested`. Pre-v1 destructive: in-flight cascades are abandoned (`rimsky_nodes.state` forced to `failed` for stale/running rows; `rimsky_dispatch` truncated).

- **Stores redesign** (single coherent change; see `docs/history/2026-04-25-stores-redesign-design.md`). The `Resource` abstraction and its surrounding template/dispatch/protocol vocabulary have been replaced end-to-end by a unified **store** abstraction with explicit **lock**, **claim**, **region**, and **attributes** vocabulary. **BREAKING:** dev DB must be nuked before adoption (no migrations from the old schema — `core/migrations/001-initial.sql` is rewritten in place to the §9 end-state schema; `core/migrations/002-data-ref-jsonb.sql` is deleted).
  - **Removed concepts:** the `core/resource/` package and its two impls (`inlinejsonb`, `externalsql`); template fields `owns_resources` / `reads_resources` / `instance_params` / `concurrency_tags` and the matching node columns; `Complete.result` and the `deps_data` / `reads_data` request fields on the wire and in storage; `RestoreVersion` everywhere (template grammar, `InvalidateArgs`, scheduler `invalidateRestorePath`, control-api `nodes.go` decode, `node.ReasonRestoreVersion`, related event payloads); `core/storage/postgres/resources.go` + `resource_data.go`; `core/controlapi/resources.go`; the legacy `concurrency_tags` predicate in the dispatch SQL; the two scenario tests `double_buffering_test.go` and `rollback_via_restore_version_test.go` (sidecar mode + versioned mode are post-v1); `docs/resource-author-guide.md`.
  - **Added concepts:** the `core/store/` package — `Store` / `LockSpec` / `LockHandle` / `Capabilities` / `ReleaseAction` / `ClaimResult` interfaces plus a `Registry` (`core/store/registry.go`), shared `rimsky_lock_holders` postgres helpers (`core/store/lockholders.go`), and three reference implementations: `core/store/filesystem/` (direct-mode, region-glob `RegionsConflict` purity, `SupportsRegionLock` + `SupportsResume`), `core/store/claimstorepg/` (postgres-backed claim store with FIFO acquire / on-commit release-actions / hold + reference-counted resolution per §5.6.4 in `holders.go`), and `core/store/stub/` (in-process test fixture used by the migrated scenarios). New template grammar: `stores: [{name, claim?, hold?, write?, read?}]`, `locks: [{name, mode, limit?}]`, `attributes: {schema}` with `properties[*].source: "{{deps.<n>.<f>}}" | "{{claim.<store>.payload.<f>}}" | "{{params.<k>}}"`, and `claim_resolutions: [{store, on_commit, on_give_up}]` (§11.4 holding-subgraph DAG walk validated at template-deploy). New `core/attributes/` package owns single-pass substitution (`substitution.go`), JSON-Schema validation at both dispatch and commit gates (`validate.go`), and the §12.5 incremental-writeback HTTP handler `POST /v1/attributes/{node_id}` (`callback.go`). New unified locks: `kind in ('named','region','claim')` rows in `rimsky_lock_holders`, atomic dispatch-claim + lock-insert + store `AcquireLock` per §13.3, deterministic sorted acquisition, and the `5 × heartbeat_interval` orphan reap. New error classes `template_resolution_failed` and `attributes_schema_failed` in the policy chain. New admin endpoints on control-api: `GET /claims/{claim_id}/holders`, `POST /admin/claim-stores/{name}/items`, and `POST /admin/scheduled-nodes/{node_id}/force-fire` (used by the smoke fixture; immediately updates `rimsky_schedules.next_fire_at = now()` and returns 204). New stores config plumbing: `RIMSKY_STORES_CONFIG` (loaded by `rimsky-supervisor`, `rimsky-control-api`, and `rimsky-scheduler`); reference `deploy/stores.yml` declaring `content` (filesystem direct) + `topics-ring` (claim-store-postgres). New protocol: `proto/v1/node_executor.proto` rewritten per §12 (`ExecuteRequest{NodeId, InstanceId, NodeType, Userdata, Attributes, Stores[], Locks[], CancelToken, CallbackUrl}`, `Complete.attributes_delta` replacing `result`, `userdata` opaque end-to-end). New scenario buckets `test/scenarios/{stores,locks,attributes,claim_stores}/` and the §19.2 smoke fixture at `test/smoke/`. New doc `docs/store-author-guide.md`.
  - **Blessed invariants** are now 14 (§18): the eight pre-existing invariants plus four new ones — (9) lock state lives only in postgres; (10) lock acquisition is atomic with dispatch claim; (11) userdata is opaque to rimsky; (12) attributes validate twice (dispatch + commit); (13) first-delete-wins, last-released-wins held-claim resolution; (14) `RegionsConflict` and `UnmarshalRegion` are pure. Invariants 3 and 4 are generalised: all locks (named, region, claim) acquired in deterministic sorted order, and every `rimsky_lock_holders` delete plus every `rimsky_dispatch.claimed_by = NULL` is claimant-guarded.
  - **Helm chart drift** at `deploy/kubernetes/rimsky-chart/` was best-effort updated (stores-config ConfigMap mounted on scheduler / supervisor / control-api; env vars realigned to the binaries' actual contracts; supervisor ConfigMap rewritten to the `yamlConfig` shape consumed by `core/cmd/rimsky-supervisor/main.go`). Remaining known drift not repaired: chart not validated via `helm lint` / `helm template`; no provisioning Job for the operator-owned `topics_items` table; no shared PVC for the `content` filesystem store across supervisor + executors; no Service for the supervisor's callback endpoint. Pick this back up under live cluster validation.
  - **Smoke acceptance status:** `go test ./test/smoke/... -count=1 -timeout 10m` reaches steady state on the ring-buffer / dispatch / lock-holders / claim-holders predicates, but the `>= 100 work_completed` counter coalesces to ~2-4 review completions per run because the cascade implementation merges successive upstream invalidates into single downstream runs. All other suites (`./core/supervisor/...`, `./core/attributes/...`, `./core/store/...`, `./core/node/...`, `./test/scenarios/...`, `./conformance/...`) pass.
