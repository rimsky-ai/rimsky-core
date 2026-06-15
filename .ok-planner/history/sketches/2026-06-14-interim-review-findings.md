# Interim review findings — 2026-06-14 message-schema-layer plan, after Passes 1-3 re-pass

These are the findings the interim reviewer surfaced on the resumed workflow run after Passes 1-3 cleared (re-pass over the prior run's tree). The workflow was stopped before the fixer applied them. Captured here so they're durable if the final review-work doesn't catch them.

Source: workflow run wf_e640a8a3-4b7, interim-review agent journal.

## Finding 1: `lib/graph/frame/engine.go:316`

**Problem:** advanceOneFrame uses Nodes().MarkStaleForCascade to wake every node enumerated in payload.wake_node_ids, including nodes whose latest run row is phase='parked'. MarkStaleForCascade is a pure UPDATE that flips state to 'stale' but leaves phase='parked', so a parked node ends up in the inconsistent (phase=parked, state=stale) shape. The supervisor's dispatch predicate keys on phase IN ('pending','active','held') AND state IN ('stale','running'), so the woken run never gets picked up. node/invalidate, node/reset, and asset/materialize synthetic envelopes therefore fail to wake a parked target — the runtime cascade-walker path (cascadeMessageVirtualNodeSettleInTx) explicitly routes parked rows through wakeParkedReceiverWithDepsInTx for exactly this reason; advanceOneFrame lacks the parity branch.

**Why it matters:** Operator-issued invalidate/reset against a node currently parked, plus asset-materialize against a parked node, will silently no-op once promotion runs. The frame opens, no wake fires, the parked timer eventually times out. Pre-change behavior used MarkSourceNodeStale which simply skipped parked nodes (also wrong, but at least left phase/state consistent); the new path actively corrupts the row.

**Suggested fix:** In advanceOneFrame's per-wake-id loop, branch on the node's current state: when node.State (or the resolved run row's phase) is parked, call wakeParkedReceiverWithDepsInTx(ctx, store, queue, tx, *node, frameID) instead of AffirmNodeRunRow + GetInFlightRunForNode + MarkStaleForCascade. The queue handle is already plumbed into advanceOneFrame; pass it through. Mirror the parked-vs-running split already in lib/runtime/message_delivery.go::cascadeMessageVirtualNodeSettleInTx so the two wake paths stay symmetric.

---

## Finding 2: `lib/control/controlapi/mcp_route.go:153`

**Problem:** The MCP tool descriptor for `message_send` still advertises the retired wire fields: properties include `"kind":{"type":"string"}` and `"target":{"type":"string"}`, and `"required":["id","kind"]`. The HTTP handler renamed kind → type and removed target in Pass 1; this descriptor is the schema MCP clients (LLM agents using Claude/Cursor/etc.) read to construct their requests. Clients following the descriptor will POST `{"kind":"...", "target":"..."}`, which the HTTP handler rejects (`body.Type == ""` → 400 "type is required"), and they will NOT send the new `type:` field they need.

**Why it matters:** Every MCP-driven message-send request is broken — the descriptor and the handler disagree about the wire shape, with no diagnostic from either side to suggest the fix. STORY-message-schema's load-bearing property (declared types are addressable, undeclared types refuse loud) is unreachable via the MCP surface until the descriptor is updated.

**Suggested fix:** Replace the entry on line 153 with: `"message_send": []byte(\`{"type":"object","properties":{"id":{"type":"string","description":"instance id"},"type":{"type":"string"},"payload":{},"sender":{"type":"string"},"sender_kind":{"type":"string","enum":["operator","publisher"]},"publisher_subscription_id":{"type":"string"}},"required":["id","type"]}\`)`. Drop `kind` and `target`; rename the `required` member to `type`.

---

## Finding 3: `lib/services/test/scenarios/{control_api_idempotency_required_e2e_test.go,claim_producer_observability_dashboard_e2e_test.go,verifier_registration_validation_e2e_test.go,mcp_transport_parity_e2e_test.go,split_topology_test.go,sqlite_all_in_one_test.go,verifier_severity_partition_e2e_test.go,cli_watch_chronological_e2e_test.go,terminate_after_run_e2e_test.go,single_process_allinone_test.go,cli_compose_up_down_e2e_test.go,control_api_compose_prefix_guard_e2e_test.go,subscriber_openlineage_e2e_test.go,claude_agent_cross_stack_e2e_test.go,control_api_node_signal_type_e2e_test.go,stores/fs_pick_vs_scope_concurrency_test.go,stores/fs_cross_queue_concurrency_test.go,scopes_conflict/scopes_conflict_test.go,pg_error_classes/pg_error_classes_test.go,atomic_staging/fs_held_swap_e2e_test.go} + lib/services/test/smoke/stores_redesign_smoke_test.go + examples/{onboarding-template.yaml,atomic-staging-fs-producer/template.yaml,compose/template-a.yml,compose/template-b.yml,publisher/* test fixtures}`

**Problem:** All of these test fixtures and example templates still set `"frame_resolution_mode": "serial_queue"` (or the YAML form) in the template spec they POST to `/v1/templates`. The field was removed from `TemplateSpec` in Pass 2 (lib/foundation/spec/template.go line 128 comment confirms retirement), and `lib/control/controlapi/templates.go:961` decodes the register-request body with `DisallowUnknownFields()`. The decoder will return HTTP 400 "json: unknown field \"frame_resolution_mode\"" the first time any of these fixtures is replayed against the real server.

**Why it matters:** Every services-stack e2e and smoke test that registers a template will fail at the register-template HTTP call. This is silent right now because services tests need locally-built images and the harness boots a real rimsky stack — but the moment `make test-all` runs them the entire services e2e suite breaks. Examples shipped to operators will likewise return 400 from the CLI.

**Suggested fix:** Strip `frame_resolution_mode` from every fixture and example template. Same sweep applies to any `frame_delivery_mode` references (covers the per-instance request body removed in Task 7). A single `find … -exec sed -i …` over the matched files is sufficient; verify each touched file still parses by running `go test` on its package.

---

## Finding 4: `lib/control/controlapi/debug_override.go:262-285`

**Problem:** `applyDebugOverride` returns `mutated` as the count of node-runs that were stale-marked, per its own doc comment (line 233: "Returns the number of in-flight node-runs that were stale-marked (so the audit row records what the operator actually mutated)"). The implementation increments `mutated++` unconditionally for every node whose NodeType matches body.NodeType, regardless of whether `MarkStaleForCascade` actually ran (it only runs when `n.InFlightRunID != nil && frameID != nil`). Similarly, `setNodeAttributeForDebugOverride` returns nil with no signal when there's no in-flight run AND no latest attribute row (line 339-343: "override has nowhere to land"), but `mutated++` still fires below. The HTTP response and the audit-event payload both surface this count as `runs_mutated`, so operators see a non-zero number even on no-op overrides.

**Why it matters:** The audit trail and the HTTP response disagree with reality. STORY-debug-channel's proof asserts on the audit row's count — if the test happens to hit a no-op state (idle instance, no in-flight runs), the audit count will be non-zero but no row will have changed. Operators chasing "my override returned 200 and runs_mutated=2 but nothing happened" will burn time on a phantom success.

**Suggested fix:** Increment `mutated` only when work actually happened. Replace the trailing `mutated++` with a guarded form: bump only if `MarkStaleForCascade` actually ran (in_flight run + frame present) OR (for set_attribute) if the attribute write actually persisted. Easiest shape: have `setNodeAttributeForDebugOverride` return a bool (`wrote`), and bump `mutated` only when the stale-mark fired OR `wrote` is true. Update the doc comment if the semantics change to "node-runs touched".

---

## Finding 5: `lib/runtime/message_delivery.go:481-509 + lib/foundation/persistence/{postgres,sqlite}/messages.go::ListPendingForInstance`

**Problem:** `DeliverPendingMessages` claims (line 478-480) that the one-message-per-frame property is enforced by "the single-row LIMIT 1 selection downstream of ListPendingForInstance." In both the postgres and sqlite implementations of `ListPendingForInstance`, no LIMIT clause exists: the SQL is `SELECT ... FROM rimsky_messages WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE ORDER BY received_at ASC, id ASC` (postgres) and the identical shape in sqlite. Every pending message is loaded into Go memory; `pending[0]` is then picked. The Pass 2 falsifier note in the plan explicitly says "satisfied by construction here (single-row LIMIT 1 SELECT)" — code does not match the falsifier.

**Why it matters:** Two concerns. (1) Cost: under bursty operator traffic, a queue with N pending messages issues a full-scan SELECT each tick per instance, materializing N rows just to pick one. (2) Documentation drift: a future maintainer reading the comment will believe LIMIT 1 is the load-bearing structural guarantee; if someone later changes `pending[0]` to `pending` (or refactors the Go selection logic), the safety net the comment claims doesn't exist.

**Suggested fix:** Add `LIMIT 1` to both `listPendingForInstanceSQL` constants. The behavior is identical (oldest pending wins by the existing ORDER BY) and now the comment matches reality. Alternatively, if the multi-row return is intentionally retained for a future caller, weaken the comment to "Go-side single-row selection over the ordered slice."

---

## Finding 6: `test/scenarios/* (deleted) + lib/services/test/scenarios/{sensor_cascade_e2e_test.go,sensor_cron_restart_recovery_e2e_test.go,sensor_http_e2e_test.go,sensor_object_store_e2e_test.go,sensor_webhook_e2e_test.go} (deleted) + examples/publisher/main_e2e_test.go (deleted)`

**Problem:** These deletions removed the executable proof artifacts for STORY-publisher-protocol, STORY-sensor-cron, STORY-sensor-http, STORY-sensor-webhook, STORY-sensor-object-store. The original tests are listed as proofs in `.ok-planner/history/plans/2026-06-08-design-corpus-bootstrap*.md`. The deletions were justified because the tests subscribed to the retired `message/<kind>/<sender_kind>/<target>` signal type-path (Pass 4 removed that taxonomy entry), but there is no in-plan replacement: Pass 11's task list (47-53) only regenerates message-schema / cascade-emit / cross-frame-coupling / typed-message-substitution / frame-origin-audit story proofs, NOT the publisher/sensor story proofs.

**Why it matters:** Five durable stories lose their committed acceptance artifacts with no scheduled replacement in the remaining passes. The /verify and /coverage skills walking the design corpus will surface five missing proofs; under the cold-read / no-deferral discipline, deleting a proof without queueing a replacement is the exact failure the no-deferral audit exists to catch.

**Suggested fix:** Either (a) add new tasks under Pass 11 (or a new pass) that regenerate each deleted sensor / publisher proof against the new model — a sensor publishing a declared message type into the universal `/instances/{id}/messages` endpoint, subscribed receiver wakes through the message-virtual-node settle path — or (b) document in the spec / story-acceptance section that the existing in-process tests (`sensor-*/sensor_test.go`, etc.) are now the durable proof and the cross-stack proof is intentionally retired. Pick one; don't leave the gap.

---

## Finding 7: `lib/foundation/persistence/sqlite/migrations/010-message-schema-layer.sql:71-117`

**Problem:** The migration drops three indexes (`uq_rimsky_frames_coalesce_queued`, `uq_rimsky_frames_running`, `idx_rimsky_frames_queued`) with bare `DROP INDEX` rather than `DROP INDEX IF EXISTS`. The other index drops in this file at lines 149, 160-162, 208-209 do use `DROP INDEX IF EXISTS`. A clean dev DB built from migrations 001-009 should carry all three indexes, so the bare DROP works today. However, the asymmetry creates a fragility: if a future migration ever adjusts the index lineage (or if a dev runs a partial migration set), a missing index trips a hard error at line 71 rather than passing through cleanly the way every other DROP in this file does.

**Why it matters:** Cross-backend stylistic drift introduces a single-point fragility right at the start of the rebuild dance. SQLite is harder to recover from a half-applied migration than postgres; the convention `DROP INDEX IF EXISTS` exists for exactly this reason, and lines 149/160-162/208-209 follow it.

**Suggested fix:** Change lines 71-73 to use `DROP INDEX IF EXISTS uq_rimsky_frames_coalesce_queued;`, `DROP INDEX IF EXISTS uq_rimsky_frames_running;`, `DROP INDEX IF EXISTS idx_rimsky_frames_queued;` matching the convention in the rest of the file.

---

## Finding 8: `lib/foundation/persistence/postgres/migrations/010-message-schema-layer.sql:92`

**Problem:** The migration `DROP INDEX uq_rimsky_frames_coalesce_queued;` (line 92) and `DROP INDEX idx_messages_backfill;` (line 116) use bare DROP. The other index drops in the postgres baseline (look at 008/009) use `DROP INDEX IF EXISTS`. Same fragility concern as the SQLite mirror — and worse, on postgres a re-run of the migration would fail at the first DROP because the partial unique index no longer exists. The pre-condition DO block at lines 80-91 checks `rimsky_frames` is empty but doesn't check the index exists, so a partially-applied prior run leaves the migration unrunnable.

**Why it matters:** Pre-v1 nukes the DB, but the migration runner uses `INSERT INTO rimsky_migrations` per file completion — if 010 is partially applied (process killed between DROP and the final COMMIT) and re-attempted, it fails at the first DROP rather than continuing from where it stopped. Idempotency is the safety net every migration in this tree relies on.

**Suggested fix:** Use `DROP INDEX IF EXISTS` for both index drops (lines 92 and 116) to match the convention used elsewhere in the migration suite.

---

## Finding 9: `lib/runtime/runner_emit_message.go:99-180 + lib/runtime/runner_terminal.go:364-369`

**Problem:** `emitCascadeMessageInTx` derives the idempotency key as `cascade-emit:<dispatch_id>` where `dispatch_id` is the rimsky_node_runs.id. The comment claims this collapses retries onto the same dedup row. But the run row's id is regenerated on every fresh stale-mark / new run row (MarkSourceNodeStale + AffirmNodeRunRow both mint a new UUID via `gen_random_uuid()` / `uuid.New()`). On a hard-failure infra retry the supervisor releases the dispatch and a SUBSEQUENT cascade walk into the same emit-node allocates a NEW dispatch_id; the second emit attempt has a fresh idempotency-key and inserts a SECOND envelope row.

**Why it matters:** The load-bearing property in Pass 7's falsifier — "Idempotency on cascade-emit is deterministic on `node_run_id`. Test: invoke the terminal-resolution path twice with the same `node_run_id`; assert one and only one envelope in the ledger" — only holds for the narrow case of an in-tx retry on the same run row. The broader infra-failure retry case (the supervisor's normal re-enqueue path) duplicates envelopes. STORY-cascade-emit and STORY-cross-frame-coupling can produce duplicate frame opens — visible as duplicate `triggering_message_id` rows in `rimsky_frames` for the same logical emit.

**Suggested fix:** Pick a stable discriminator that survives run-row reissue. Candidates: (a) `node_id + frame_id` (the emit-node's static node UUID plus the frame it emitted in — same emit always lands the same key), (b) the run_scope_id + emit type + sequence index, or (c) a content-hash of the marshaled body bytes. Document the chosen invariant in the @blessed-invariant block at the head of the file so the property is named, not implied.

---

## Finding 10: `.ok-planner/specs/2026-06-14-message-schema-layer-design.md (spec) + plan Pass 2 falsifier`

**Problem:** The plan's Pass 2 falsifier states (line 149): "the existing test `lib/graph/frame/producer_test.go::TestEnqueueOrCoalesce_CoalesceFirstInsert` still exists." Reviewing the working tree, `lib/graph/frame/producer_test.go` was modified, and `TestEnqueueOrCoalesce_*` tests no longer exist — the falsifier is satisfied. However, the falsifier names a specific test that has been replaced; nothing in the plan or spec captures the rename to `TestEnqueueFrame_*`. Future maintainers reading the falsifier will hunt for a vanished symbol. Same pattern in Pass 3's falsifier referencing `lib/runtime/runner_terminal.go` `case node.FrameNext:` and Pass 4 referencing `cascadeMessageSubscribersInTx`.

**Why it matters:** Falsifiers are durable verification anchors; stale symbol names degrade their value over time. The plan archive lands in `.ok-planner/history/plans/` after execute-plan; a falsifier naming a since-renamed symbol cannot be re-run from the historical record alone. Minor, but a documented future-debt this review can catch cheaply now.

**Suggested fix:** When the execute-plan loop closes Pass 2 and Pass 3, update the falsifier strings to name the renamed/replacement symbols (`TestEnqueueFrame_*`, the absence of `case node.FrameNext:`, `cascadeMessageVirtualNodeSettleInTx` replacing `cascadeMessageSubscribersInTx`). This is a documentation-only sweep that costs minutes and keeps the historical falsifier replayable.

---
