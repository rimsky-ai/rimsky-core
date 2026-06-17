# Executor Protocol Coherence Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-16-executor-protocol-coherence-design.md`
**Goal:** Replace the streaming executor protocol with unary RPC + async callback, collapse named events into terminal-borne tags, uniformly carry `attributes_delta` on every settling terminal, remove the resume-context channel (push session state to attribute carry-forward), add three orthogonal dispatch deadlines (`sync_rpc_deadline`, `max_quiet_period`, `max_runtime`, all 0 = disabled) plus a dedicated keepalive endpoint, persist the async-callback registry across supervisor restarts, and reshape the design-doc catalog to match.
**Architecture:** Three runtime modules cooperate. `lib/protocols/proto/v1/` defines the wire surface (the executor RPC becomes unary; outcomes are a oneof of `Success | Error | Park | AwaitAsyncCallback`). `lib/foundation/persistence/` owns the dispatch row's new columns (`async_ack_id`, `last_progress_at`, `tags`) and the migration that drops `rimsky_node_events` plus the parked-payload columns. `lib/runtime/` connects them — the supervisor's outgoing `Execute` RPC is unary, the in-process executor path follows the same Outcome shape, `lib/runtime/callback.go` looks up async-ack-ids out of the dispatch row instead of an in-memory map, and `lib/graph/scheduler/` enforces the three deadlines from periodic sweeps.
**Tech Stack:** Go modules (root, `lib/foundation`, `lib/protocols`, `lib/services`), `jackc/pgx/v5` (postgres), `modernc.org/sqlite` (sqlite), `go-chi/chi/v5` (HTTP), stdlib `log/slog`. Proto compiled by `make proto-gen` (via `protoc`/`buf`). TypeScript executor at `lib/services/executors/claude-agent/`.

---

## Reading: orient on the spec before any pass

Before starting any pass, read the spec in full: `.ok-planner/specs/2026-06-16-executor-protocol-coherence-design.md`. The spec's `## Mechanism`, `## Technical decisions` (13 TDs), `## Design changes` (~40 entries spanning concept / decision / story files and the persistence migration + proto rename), and `## Proof changes` (four affected stories, all verdict B) together describe the full scope this plan covers. Tasks in this plan reference specific TDs and design-change bullets by name; the spec carries the prescriptive text the tasks apply.

Project rules to honor throughout: pre-v1 break-freely (`.claude/rules/rules.md` — drop columns and rewrite CHECKs in a new migration without compat shims); after-code-changes verification block (run the appropriate build / test / lint suite before reporting any pass complete); Plumbline conventions (`.claude/rules/plumbline-cheatsheet.md` — structured comment tags only, file-feature alignment, ~500-line file / ~100-line function guideline).

---

## Pass 1: Wire shape, persistence, runtime spine

**Goal:** Land the new executor protocol surface end-to-end — proto rewrite, persistence migration, runtime adaptation across supervisor / runner / callback / scheduler / signal taxonomy / subscription grammar / substitution / orphan reaper. At the end of this pass the tree builds and `make test-all` is green; built-in executor consumers (loop_counter, claude-agent) are NOT yet migrated in this pass and their tests are temporarily disabled where they would not compile.
**Scope:** Tasks 1–22
**Falsifier:** The new `Outcome` message and its four-variant oneof are not present in `lib/protocols/proto/v1/executor.proto`, OR the migration leaves `rimsky_node_events` present, OR `rimsky_node_runs` still carries `parked_payload_inline / parked_payload_handle / parked_payload_handle_backend / session_token / wake_reason`, OR `rimsky_node_runs.prior_dispatch_disposition` CHECK still admits `'heartbeat_stale'`, OR the wait-set `topic_kind` CHECK still admits `'event'`, OR `lib/runtime/callback.go::CallbackRegistry` still uses an in-memory `map[string]AsyncContext` as the only registry, OR `lib/runtime/runner_named_events.go` still exists, OR `lib/runtime/supervisor.go`'s outgoing executor RPC is still a server-streaming Recv loop, OR `make test-all` fails after the pass's edits land.

### Task 1: Rewrite `lib/protocols/proto/v1/executor.proto` to the new shape

**Files:** `lib/protocols/proto/v1/executor.proto`

The full set of edits below is one atomic proto rewrite, applied per `## Technical decisions` TD-execute-rpc-unary, TD-collapse-named-event-to-tags, TD-attributes-delta-on-all-settling-terminals, TD-remove-resume-context, TD-prior-stale-rename in the spec.

**Steps:**

1. Change the `Executor.Execute` RPC declaration from server-streaming to unary:
   ```proto
   service Executor {
     rpc Execute(ExecuteRequest) returns (Outcome);
   }
   ```
2. Remove the `ExecuteEvent`, `StreamClose`, `Heartbeat`, and `NamedEvent` messages entirely.
3. Add a new top-level `Outcome` message wrapping the four-variant oneof:
   ```proto
   message Outcome {
     oneof outcome {
       Success            success     = 1;
       Error              error       = 2;
       Park               park        = 3;
       AwaitAsyncCallback await_async = 4;
     }
   }
   ```
4. Modify `Success`:
   - Keep `bool changed = 1;`, `string change_summary = 2;`, `google.protobuf.Struct attributes_delta = 3;`, `bytes scratch = 4;` (existing).
   - Add `repeated string tags = 5;` (set semantics — implemented by deduplicating at decode in step 6's accessor wrappers; the wire shape itself is a list).
5. Modify `Error`:
   - Keep `string error_class = 1;`, `google.protobuf.Struct payload = 2;`, `bytes scratch = 3;` (existing).
   - Add `google.protobuf.Struct attributes_delta = 4;`
   - Add `repeated string tags = 5;`.
6. Modify `Park`:
   - Keep `ParkReason reason = 1;`, `google.protobuf.Timestamp resume_at = 3;`, `string reason_note = 5;`, `string reason_label = 6;`, `bytes scratch = 7;`.
   - **Remove** `bytes payload = 2;` and `string session_token = 4;`. Reserve their tag numbers: `reserved 2, 4;` `reserved "payload", "session_token";`.
   - Add `google.protobuf.Struct attributes_delta = 8;`
   - Add `repeated string tags = 9;`.
7. Remove the `ResumeContext` message entirely.
8. Modify `ExecuteRequest`:
   - **Remove** field 13 `ResumeContext resume_context = 13;`. Reserve: `reserved 13;` `reserved "resume_context";`.
9. In the `PriorDispatchDisposition` enum:
   - Rename `PRIOR_HEARTBEAT_STALE = 1;` to `PRIOR_STALE_RECOVERY = 1;` (same field number). Update the inline comment to: *PRIOR_STALE_RECOVERY: the supervisor's stale-recovery sweep reaped the prior run (sync RPC connection broken in-band, or async dispatch exceeded `max_quiet_period`); this dispatch is the re-enqueue.*
10. Modify `AsyncCallbackBody`:
    - **Remove** `repeated NamedEvent events = 1;`. Reserve `reserved 1;` `reserved "events";`. The outcome `oneof` numbering (2/3/4) stays.
11. Update the in-proto doc comments above each modified message to drop references to streaming / heartbeats / named events / resume_context / Park.payload / Park.session_token. The new comments describe the unary semantic and the new fields.
12. Run `gofmt`-equivalent check by running `make proto-gen` (next task) and ensuring no protoc errors surface.

### Task 2: Regenerate Go bindings and verify proto compiles

**Files:** `lib/protocols/proto/v1/gen/executor.pb.go` (regenerated), `lib/protocols/proto/v1/gen/executor_grpc.pb.go` (regenerated)

**Steps:**
1. Run `make proto-gen`.
2. Confirm `lib/protocols/proto/v1/gen/executor.pb.go` now exports `Outcome`, `Outcome_Success`, `Outcome_Error`, `Outcome_Park`, `Outcome_AwaitAsync`, and that `ExecuteEvent`, `StreamClose`, `Heartbeat`, `NamedEvent`, `ResumeContext` are gone (`grep -nE 'type (ExecuteEvent|StreamClose|Heartbeat|NamedEvent|ResumeContext)' lib/protocols/proto/v1/gen/executor.pb.go` returns no hits).
3. Confirm `Success`, `Error`, `Park` structs now have `AttributesDelta` and `Tags` fields by running `grep -A 20 'type Success struct' lib/protocols/proto/v1/gen/executor.pb.go | grep -E 'AttributesDelta|Tags'` and similar for Error / Park.
4. Confirm `Park` no longer has `Payload` or `SessionToken` fields.
5. Confirm `PriorDispatchDisposition_PRIOR_HEARTBEAT_STALE` is gone and `PriorDispatchDisposition_PRIOR_STALE_RECOVERY` exists.
6. `go build ./lib/protocols/...` — protocols module compiles. Do NOT yet run a wider `go build ./...` — the runtime references the old types and will fail until Task 9+.

### Task 3: Rename `declared_events` → `declared_tags` in observability proto

**Files:** `lib/protocols/proto/v1/executor_observability.proto`, `lib/protocols/proto/v1/gen/executor_observability.pb.go` (regenerated)

**Steps:**
1. In `lib/protocols/proto/v1/executor_observability.proto`, change the `ObservabilityCapabilities.declared_events` field (currently field 7 per the grep run at planning time):
   ```proto
   // declared_tags is the set of tag names this executor may emit on a settling
   // outcome. Rimsky validates at template registration that every subscription's
   // CEL filter over `payload.tags` references a tag in declared_tags.
   repeated string declared_tags = 7;
   ```
2. Update the surrounding doc comment that references "event names" / "may emit" to refer to "tag names" / "may include on settling outcomes."
3. Run `make proto-gen`.
4. Confirm `grep -n 'DeclaredEvents\|declared_events' lib/protocols/proto/v1/gen/executor_observability.pb.go` returns no hits and `DeclaredTags` is present.

### Task 4: Author postgres migration 013

**Files:** `lib/foundation/persistence/postgres/migrations/013-executor-protocol-coherence.sql` (new)

**Steps:**
1. Create the file with the following migration script (pre-v1 break-freely per `.claude/rules/rules.md` — DROPs without compat shims):
   ```sql
   -- 013-executor-protocol-coherence.sql
   --
   -- Reshape rimsky_node_runs for the post-streaming executor protocol:
   -- drop the rimsky_node_events ledger; drop the parked-payload / session-token / wake-reason
   -- columns; add async-callback-registry columns + a liveness timestamp + a tags column;
   -- rewrite the prior_dispatch_disposition CHECK to use 'stale_recovery'; drop 'event' from
   -- the wait-set topic_kind CHECK.

   -- Drop the named-event ledger entirely.
   DROP TABLE IF EXISTS rimsky_node_events;

   -- Drop parked-state columns no longer carried on the dispatch row,
   -- the heartbeat timestamp column, and its supporting index.
   DROP INDEX IF EXISTS rimsky_node_runs_heartbeat_idx;
   ALTER TABLE rimsky_node_runs
       DROP COLUMN IF EXISTS parked_payload_inline,
       DROP COLUMN IF EXISTS parked_payload_handle,
       DROP COLUMN IF EXISTS parked_payload_handle_backend,
       DROP COLUMN IF EXISTS session_token,
       DROP COLUMN IF EXISTS wake_reason,
       DROP COLUMN IF EXISTS last_heartbeat_at;

   -- The supervisor and claim-handle ledgers also carry heartbeat timestamps;
   -- drop those columns (and any indexes on them) now that orphan detection
   -- keys on last_progress_at + RPC connection state instead.
   DROP INDEX IF EXISTS rimsky_supervisors_last_heartbeat_at_idx;
   ALTER TABLE rimsky_supervisors DROP COLUMN IF EXISTS last_heartbeat_at;
   ALTER TABLE rimsky_claim_handles DROP COLUMN IF EXISTS last_heartbeat_at;

   -- Add async-callback registry + liveness + tags.
   ALTER TABLE rimsky_node_runs
       ADD COLUMN async_ack_id              TEXT NULL,
       ADD COLUMN async_ack_registered_at   TIMESTAMPTZ NULL,
       ADD COLUMN last_progress_at          TIMESTAMPTZ NULL,
       ADD COLUMN tags                      TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

   -- Indexed lookup for the callback handler.
   CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
       ON rimsky_node_runs (async_ack_id)
       WHERE async_ack_id IS NOT NULL;

   -- Rewrite prior_dispatch_disposition CHECK: heartbeat_stale → stale_recovery.
   ALTER TABLE rimsky_node_runs
       DROP CONSTRAINT IF EXISTS rimsky_node_runs_prior_dispatch_disposition_check;
   ALTER TABLE rimsky_node_runs
       ADD CONSTRAINT rimsky_node_runs_prior_dispatch_disposition_check
       CHECK (prior_dispatch_disposition IS NULL
              OR prior_dispatch_disposition IN ('stale_recovery', 'retry_after_error', 'recalculate'));

   -- Drop 'event' from wait-set topic_kind CHECK. The constraint name
   -- `rimsky_wait_set_topic_kind_check` is the postgres auto-generated
   -- name, confirmed against migration 012 which used the same form.
   -- The remaining allowed values are ('state', 'attribute', 'transient', 'terminal').
   ALTER TABLE rimsky_wait_set
       DROP CONSTRAINT IF EXISTS rimsky_wait_set_topic_kind_check;
   ALTER TABLE rimsky_wait_set
       ADD CONSTRAINT rimsky_wait_set_topic_kind_check
       CHECK (topic_kind IN ('state', 'attribute', 'transient', 'terminal'));
   ```

   Note on the `rimsky_node_runs_prior_dispatch_disposition_check` constraint name: the original CHECK in migration 001 was declared inline without an explicit `CONSTRAINT <name>` clause, so postgres auto-generated the name. The implementer must verify the auto-generated name by querying `pg_constraint` (e.g., `SELECT conname FROM pg_constraint WHERE conrelid = 'rimsky_node_runs'::regclass AND contype = 'c'`) and adjust the `DROP CONSTRAINT IF EXISTS` line above to the actual name if the convention guess (`rimsky_node_runs_prior_dispatch_disposition_check`) does not match — fall back to dropping by querying pg_constraint and constructing the DROP statement dynamically if needed.
2. Add the file to the postgres `embed.go` registry (the embed.go file is generated / pattern-driven — verify by `grep -n 013 lib/foundation/persistence/postgres/migrations/embed.go` returns the new entry; if the embed list is hand-maintained, add the file to it; if generated, run whatever generator the makefile invokes).

### Task 5: Author sqlite migration 013

**Files:** `lib/foundation/persistence/sqlite/migrations/013-executor-protocol-coherence.sql` (new)

**Steps:**
1. Create the parallel sqlite migration. SQLite has limited `ALTER TABLE DROP COLUMN` support (varies by version) but recent `modernc.org/sqlite` builds support it. Use the same script structure as postgres:
   ```sql
   -- 013-executor-protocol-coherence.sql

   DROP TABLE IF EXISTS rimsky_node_events;

   DROP INDEX IF EXISTS rimsky_node_runs_heartbeat_idx;
   ALTER TABLE rimsky_node_runs DROP COLUMN parked_payload_inline;
   ALTER TABLE rimsky_node_runs DROP COLUMN parked_payload_handle;
   ALTER TABLE rimsky_node_runs DROP COLUMN parked_payload_handle_backend;
   ALTER TABLE rimsky_node_runs DROP COLUMN session_token;
   ALTER TABLE rimsky_node_runs DROP COLUMN wake_reason;
   ALTER TABLE rimsky_node_runs DROP COLUMN last_heartbeat_at;

   DROP INDEX IF EXISTS rimsky_supervisors_last_heartbeat_at_idx;
   ALTER TABLE rimsky_supervisors DROP COLUMN last_heartbeat_at;
   ALTER TABLE rimsky_claim_handles DROP COLUMN last_heartbeat_at;

   ALTER TABLE rimsky_node_runs ADD COLUMN async_ack_id TEXT NULL;
   ALTER TABLE rimsky_node_runs ADD COLUMN async_ack_registered_at TEXT NULL;
   ALTER TABLE rimsky_node_runs ADD COLUMN last_progress_at TEXT NULL;
   ALTER TABLE rimsky_node_runs ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';

   CREATE UNIQUE INDEX rimsky_node_runs_async_ack_id_idx
       ON rimsky_node_runs (async_ack_id)
       WHERE async_ack_id IS NOT NULL;
   ```
2. SQLite has no native `TEXT[]` array type or `TIMESTAMPTZ`. Use `TEXT` and store tags as a JSON array string (`json_encode(tags)` round-tripping in the accessor layer). Use `TEXT` for timestamps (the existing migrations use ISO-8601 string convention — match it).
3. SQLite does NOT support `ALTER TABLE ... DROP CONSTRAINT`. To rewrite the `prior_dispatch_disposition` CHECK and the wait-set `topic_kind` CHECK, the conventional SQLite move is: create new tables with the desired CHECK, copy data, drop old, rename new. If either CHECK is a row-level constraint on a single column that doesn't gate any insert paths the tests hit, the test suite may pass without a CHECK rewrite — but we cannot ship without it (the runtime would silently accept invalid values). Implement the create-table-copy pattern:
   ```sql
   -- Rewrite prior_dispatch_disposition CHECK on rimsky_node_runs:
   -- SQLite's table rebuild pattern.
   CREATE TABLE rimsky_node_runs_new AS SELECT * FROM rimsky_node_runs WHERE 0;
   -- (apply full CREATE TABLE DDL matching the desired shape including new CHECK; for the
   -- prior_dispatch_disposition CHECK, the new allowed set is ('stale_recovery',
   -- 'retry_after_error', 'recalculate'). The full table DDL must match the post-migration-012
   -- shape plus this migration's column adds — copy from migration 001 and apply migrations
   -- 002–012 transformations as ground truth, then apply this migration's deltas.)
   INSERT INTO rimsky_node_runs_new SELECT * FROM rimsky_node_runs;
   DROP TABLE rimsky_node_runs;
   ALTER TABLE rimsky_node_runs_new RENAME TO rimsky_node_runs;
   -- Recreate any indexes that were on the original.
   -- (Apply the same table-rebuild pattern to rimsky_wait_set to drop 'event'
   -- from its topic_kind CHECK — full CREATE TABLE DDL with the new allowed
   -- set ('state', 'attribute', 'transient', 'terminal'), copy, drop, rename.)
   ```
4. Because SQLite table rebuild is verbose, write helper SQL or factor the rebuild into the migration script directly. The implementer reads `lib/foundation/persistence/sqlite/migrations/001-schema.sql` and the intervening migrations to assemble the correct post-state DDL.
5. Update `lib/foundation/persistence/sqlite/migrations/embed.go` similarly.

### Task 6: Update persistence-layer Go types for the new dispatch row shape

**Files:** `lib/foundation/persistence/node_runs.go` (and any sibling files in `lib/foundation/persistence/` that define the dispatch-row struct, the parked-row struct, or the Queue interface methods that read/write these columns)

**Steps:**
1. Read `lib/foundation/persistence/node_runs.go` to find the `NodeRun` struct (or equivalent named differently). Identify the fields corresponding to `parked_payload_inline / parked_payload_handle / parked_payload_handle_backend / session_token / wake_reason` and remove them.
2. Add fields:
   ```go
   AsyncAckID            *string    // populated when phase='active' and the executor returned AwaitAsyncCallback
   AsyncAckRegisteredAt  *time.Time // populated alongside AsyncAckID
   LastProgressAt        *time.Time // bumped by §12.5 writeback or POST /v1/runs/{id}/keepalive. Distinct from Frame.LastProgressAt (which lives on rimsky_frames) — this field tracks per-dispatch liveness on rimsky_node_runs.
   Tags                  []string   // set semantics, deduplicated at decode by the accessor
   ```
3. Find and update every accessor / mapper between SQL row scan and the Go struct (postgres + sqlite implementations). For sqlite, decode/encode tags as JSON string ↔ `[]string`.
4. Find and update the `Queue` interface (the spec cites `code:Queue.ListParkedReadyForResume` and `code:Queue.GetParkedByNode` — start there and walk the file). Remove parameters or struct fields named after the dropped columns; add accessors for the new fields.
5. Add a `LookupRunByAsyncAckID(ctx, tx, ackID string) (*NodeRun, error)` accessor that the callback handler will use after the proto-RPC redesign.
6. Add a `BumpLastProgressAt(ctx, tx, runID UUID, now time.Time) error` accessor.
7. Add a method to register an `async_ack_id` against a dispatch row in the same tx as the AwaitAsync handling: `RegisterAsyncAck(ctx, tx, runID UUID, ackID string, now time.Time) error`.
8. Remove the `NodeEvent` struct, the `NodeEventOrphan` struct (the (handle, backend) pair returned by `DeleteByInstance` / `DeleteOlderThan`), and the `NodeEventTable` interface entirely from the persistence package (`lib/foundation/persistence/node_events.go`) — they're tied to the dropped `rimsky_node_events` table. Delete the file itself if nothing else lives in it.
9. Run `go build ./lib/foundation/persistence/...` and `go test ./lib/foundation/persistence/... -count=1`. The persistence tests use testcontainers — Docker must be running. Tests should pass with the new schema and accessors. Fix breakage iteratively.

**Load-bearing constraints (state these explicitly in code comments on the new accessors):**
- `RegisterAsyncAck` MUST run in the caller-provided `tx`. The async-ack registration must commit atomically with the dispatch state-mutation that triggered the AwaitAsync (otherwise an executor's eventual callback could arrive before its registration is durable). Do NOT use a separate `db.Exec` outside the tx; do NOT defer the write.
- `BumpLastProgressAt` MUST run in the caller-provided tx if the caller is in one (the §12.5 attribute writeback handler should bundle the bump with the attribute write in the same tx); the standalone keepalive endpoint can open its own short tx for the bump.
- `Tags` field on `NodeRun` is deduplicated at decode. The decoder collapses duplicates and preserves first-appearance order for the substitution layer; tests should assert this.

### Task 7: Update persistence conformance tests for the new shape

**Files:** `lib/foundation/persistence/conformance/` (multiple files)

**Steps:**
1. Read every file under `lib/foundation/persistence/conformance/`. Find tests that reference the dropped columns (`parked_payload_*`, `session_token`, `wake_reason`), the `rimsky_node_events` table, or the old NodeEvent type. List them.
2. Update each to use the new columns / drop assertions against removed columns. Where a test exercises the park-resume path with `session_token`, replace with an `attributes_delta`-carry-forward flow.
3. Add a new conformance test exercising `RegisterAsyncAck` + `LookupRunByAsyncAckID` round-trip under both backends.
4. Add a new conformance test exercising `BumpLastProgressAt` and verifying the timestamp updates atomically.
5. Add a conformance test that emits a settling terminal with `tags = ["foo", "bar", "foo"]` and asserts the persisted row's tags decode as `["foo", "bar"]` (set semantics).
6. Run `go test ./lib/foundation/persistence/conformance/... -count=1` — must be green before the runtime tasks begin.

### Task 8: Remove `lib/runtime/runner_named_events.go` and its test file

**Files:** `lib/runtime/runner_named_events.go` (delete), `lib/runtime/runner_named_events_test.go` if present (delete)

**Steps:**
1. Locate and `rm` `lib/runtime/runner_named_events.go`.
2. `rg -n 'processNamedEvents|persistOneNamedEvent|eventPayloadAsMap' lib/runtime/` — every caller must be removed or updated in subsequent tasks; this task removes only the source file.
3. The package will not compile until Task 9 updates the callers. That's expected for this pass.

### Task 9: Replace streaming `Execute` with unary in the supervisor and runner

**Files:** `lib/runtime/supervisor.go`, `lib/runtime/runner.go`, `lib/runtime/runner_dispatch.go`, `lib/runtime/runner_terminal.go`, `lib/runtime/runner_terminal_*.go` (one per outcome variant, find with `ls lib/runtime/runner_terminal*.go`)

**Steps:**
1. In `lib/runtime/supervisor.go` and `lib/runtime/runner.go`, find every call to the executor's `Execute` RPC. They currently look like `stream, err := client.Execute(ctx, req); for { ev, err := stream.Recv(); ... }`. Replace with: `outcome, err := client.Execute(ctx, req)` — a single unary call returning `*genv1.Outcome`.
2. The stream-event loop that classified each `ExecuteEvent` (Heartbeat / NamedEvent / StreamClose) becomes a direct switch on `outcome.GetOutcome()` (the oneof type). Remove the Heartbeat and NamedEvent handling code paths entirely. Keep the four terminal-handling paths (Success / Error / Park / AwaitAsync), each driven directly by `outcome.GetSuccess()` / `GetError()` / `GetPark()` / `GetAwaitAsync()`.
3. Add a shared tag-validation helper used by every settling-terminal path before the verdict is persisted. The helper looks up the emitter executor's cached `declared_tags` (from `concept:discovery-cache`) and verifies every string in the outcome's `tags` field is present in that set. Any undeclared tag rejects the outcome — instead of persisting the verdict, the runner synthesizes an `Error{error_class: "executor_protocol_violation", payload: {"reason": "undeclared_tag", "tag": <name>, "declared_tags": [...]}}` and drives THAT through the terminal-handling path. This preserves the gate-2 runtime validation that today's `concept:named-event` surface has at the supervisor's terminal handler and that the new `concept:terminal-tag` invariant requires (*"emissions of undeclared names are rejected at the supervisor's terminal handler"*). Add a unit test that emits a Success with an undeclared tag and asserts the dispatch settles as Error with `error_class: "executor_protocol_violation"` instead of as Success.

4. In the Success-terminal path (`runner_terminal_handlers.go` carries `applyTerminalError`; the Success handler lives nearby — locate via `grep -rn 'applyTerminalSuccess\|applyTerminalComplete' lib/runtime/`) update the path that consumes the success outcome to first call the tag-validation helper from step 3, then persist `attributes_delta` AND the new `tags` field on the dispatch row. The tx that already commits the verdict gains two more column writes (attributes_delta delta-merge + tags array).
5. In the Error-terminal path (`runner_terminal_handlers.go::applyTerminalError`), the Error outcome now carries `attributes_delta` and `tags`; call the tag-validation helper from step 3 first, then persist both alongside the verdict in the same tx. Note: if the Error itself is the synthetic `executor_protocol_violation` produced by the validation helper, skip re-validation to avoid an infinite loop — that synthetic terminal's tags are always empty.
6. In `runner_terminal_park.go`:
   - Drop all code reading `outcome.GetPark().Payload` and `outcome.GetPark().SessionToken` (the fields are gone).
   - Call the tag-validation helper from step 3 on `outcome.GetPark().Tags` before persisting.
   - Read `outcome.GetPark().AttributesDelta` and `outcome.GetPark().Tags` and persist them alongside the parked-row write.
   - Drop the parked-row column writes for `parked_payload_inline / parked_payload_handle / parked_payload_handle_backend / session_token / wake_reason`. Drop the blob-spill code path for `parked_payload` (those handles and the spill threshold check go too — `parked_payload` no longer exists).
7. For AwaitAsync (`outcome.GetAwaitAsync()`): register the `async_ack_id` against the dispatch row via `Queue.RegisterAsyncAck(ctx, tx, dispatchID, ackID, now)` in the same tx as the runner's existing dispatch-state mutation for the AwaitAsync path. Remove the in-memory `CallbackRegistry.Register(ackID, ctx)` call in this code path — the registry is persistent now (Task 10 reshapes the CallbackRegistry struct). AwaitAsync carries no tags or attributes_delta; the eventual callback body's outcome carries those and is validated then.
8. In-process executors (loop_counter, anything under `lib/runtime/executor/builtin/`) deliver an Outcome directly. Two files redefine the in-process surface:
   - `lib/runtime/executor/inproc_handler.go`: rewrite the `EventSink` interface and the `Handler.Execute` signature. The current signature is `Execute(ctx context.Context, req *genv1.ExecuteRequest, sink EventSink, hctx HandlerContext) error`. The new signature is `Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error)`. Remove the `EventSink` interface entirely (its purpose was streaming Heartbeat/NamedEvent/StreamClose events back; with unary RPC the handler simply returns the Outcome).
   - `lib/runtime/executor/inproc_client.go`: rewrite the adapter that bridges the in-process handler into the runner's dispatch path. The adapter's `Execute` method now invokes `handler.Execute(ctx, req, hctx)`, receives a `*genv1.Outcome`, and returns it to the caller. Remove the sink-driven event-relay code that classified Heartbeat/NamedEvent/StreamClose events.
   - This interface change MUST land in Pass 1 because every in-process consumer (loop_counter in Pass 2 Task 23, any other built-in handler) compiles against this interface. The interface rewrite is a precondition for Task 19 (deadline wiring) and Pass 2.
9. Update RunArgs and acquisition shapes to drop the `ResumeContext` parameter / argument anywhere the runner threaded it. Substitute `incomingAttrs := req.GetAttributes()` reads at the executor for what `resume_context` used to carry.
10. `go build ./lib/runtime/...` — the package compiles. Fix compile errors iteratively.
11. `go test ./lib/runtime/... -count=1 -race` for the touched files. Some tests will fail until later tasks land (heartbeats, callback registry, scheduler); skip those temporarily by adding a `t.Skip("re-enable after Task N")` annotation pointing to which downstream task re-enables them.

**Load-bearing constraint:** AttributesDelta + Tags on Success/Error/Park MUST commit in the same transaction as the verdict mutation. Do NOT defer either write into a separate goroutine or a post-tx hook. The whole point of carrying them on the terminal verdict is single-atomic-commit. Reviewer: every callsite that writes the verdict must also write attributes_delta and tags in the same `Persist.Transaction` block; if any call commits the verdict and writes attributes_delta separately, that's a regression.

### Task 10: Update the callback registry to be persistent

**Files:** `lib/runtime/callback.go`

**Steps:**
1. Replace the `CallbackRegistry` struct's in-memory `pending map[string]AsyncContext` with a no-op shim or remove it entirely. The new lookup path goes through `Queue.LookupRunByAsyncAckID(ctx, tx, ackID)`.
2. Rewrite `handleCallback`:
   - `ackID := chi.URLParam(r, "async_ack_id")`
   - Open a tx; call `Queue.LookupRunByAsyncAckID(ctx, tx, ackID)`. If not found, return 404 with the existing `unknown_async_ack_id` body. If found, reconstruct the AsyncContext from the persisted dispatch row + the surrounding system state (the supervisor's `RunArgs`-equivalent context the callback handler already holds), then drive the same `applyTerminal*` path the synchronous executor RPC call now drives.
   - The `AsyncContext` struct can collapse: only the dispatch_id is strictly needed to reconstruct everything else from the persisted dispatch row. Decide based on what fields handleCallback consumes today; keep the struct as a logical wrapper but populate it from the DB on each callback.
3. Drop the `Register` and `Pop` methods (or leave them as no-ops with a comment explaining the persistent path is the new mechanism). The runner's AwaitAsync handling (Task 9 step 7) writes directly to the dispatch row, so callers of `Register` get migrated to that.
4. The HTTP `route:POST /v1/callback/{async_ack_id}` route stays at `lib/runtime/callback.go::handleCallback`. The unique-index on the persisted column guarantees at-most-one dispatch per ackID; the lookup is one indexed read.
5. Verify the supervisor restart story manually: start the supervisor, register an ack id, kill the supervisor, restart, POST the callback — the eventual terminal should land correctly on the persisted dispatch row. (This is the load-bearing property TD-persist-async-callback-registry was authored for; the load test in Task 12 exercises it under restart.)

**Load-bearing constraint:** The async-callback registry MUST survive supervisor restart. Do NOT keep any in-memory cache that's the only source of truth for ackID→dispatch. The DB is the canonical record. An in-memory cache for hot lookups is OK only if it's read-through (DB is queried on miss).

### Task 11: Add the dedicated keepalive endpoint

**Files:** `lib/runtime/callback.go` (route registration), `lib/runtime/keepalive.go` (new — handler)

**Steps:**
1. In `lib/runtime/callback.go::Start`, register a new chi route alongside the existing ones:
   ```go
   r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
   ```
2. Create `lib/runtime/keepalive.go` with the handler. It:
   - Authenticates via the existing `cancel_token` header — reuse `c.attributesAuth(token, runID)` from `lib/runtime/callback.go` to validate the bearer.
   - Bumps `last_progress_at = now()` on the dispatch row in a short tx by calling `Queue.BumpLastProgressAt(ctx, tx, runID, now)`.
   - On success returns `204 No Content` with no body.
   - On 401 (auth failure): `http.Error(w, "unauthorized", http.StatusUnauthorized)`.
   - On 404 (run not found): `http.Error(w, "unknown_run_id", http.StatusNotFound)`.
3. Add a unit test in `lib/runtime/keepalive_test.go` that exercises: success → 204; auth failure → 401; unknown run → 404.

### Task 12: Wire the §12.5 attribute writeback to bump `last_progress_at` as a side effect

**Files:** `lib/graph/attribute/callback.go` (the §12.5 handler) and adapters in `lib/runtime/callback.go::attributesStoreAdapter`

**Steps:**
1. Find the existing handler at `lib/graph/attribute/callback.go` that handles `POST /v1/runs/{run_id}/attributes`. It currently calls `store.MergeDelta(ctx, runID, delta)` or `store.Upsert(ctx, runID, nodeID, data)`.
2. In the `attributesStoreAdapter` at `lib/runtime/callback.go::attributesStoreAdapter::MergeDelta` and `Upsert`, after the existing call inside the tx, add a `BumpLastProgressAt` call against the same tx. The two writes must commit together.
3. Add a unit test in `lib/runtime/callback_test.go` (or a sibling test file) that POSTs to `/v1/runs/{id}/attributes` and asserts `last_progress_at` advanced on the persisted dispatch row.

**Load-bearing constraint:** The bump MUST share the writeback's transaction. Do NOT split it into a separate `Persist.Transaction` call after the writeback commits — a crash between would leave `last_progress_at` unbumped while the writeback succeeded, biasing the orphan detection.

### Task 13: Add the three dispatch deadlines: configuration, propagation, and scheduler enforcement

**Files:** `lib/graph/node/template_validator.go` (or the file that owns executor-template config validation), `lib/graph/scheduler/scheduler.go`, `lib/runtime/runner_dispatch.go`, plus any config-structure files for executor-template / node-template settings

**Steps:**
1. Add three optional configuration knobs:
   - `sync_rpc_deadline_seconds: int` — executor-template-level, default 30 (sentinel `0` = disabled / no enforcement).
   - `max_quiet_period_seconds: int` — executor-template-level or per-node, default 0 (disabled).
   - `max_runtime_seconds: int` — per-node, default 0 (disabled).
2. Propagate `sync_rpc_deadline` into the supervisor's outgoing `Execute` RPC context: when calling the unary `Execute(ctx, req)`, derive a `ctx` with `context.WithTimeout(parent, deadline)` if `deadline > 0`; if `0`, use the parent context unchanged. Cancellation of the RPC due to deadline expiry surfaces to the runner as the existing `ctx.Err() == context.DeadlineExceeded` path; that path produces a synthetic `Error{error_class: "executor_sync_timeout"}` terminal.
3. Propagate `max_quiet_period` and `max_runtime` into the dispatch row (or carry them at dispatch time via the `RunArgs` chain — the scheduler will read them per-sweep). For async dispatches (the dispatch is in `phase='active'` with `async_ack_id` set), the scheduler periodic sweep:
   - If `max_quiet_period > 0` AND `now - last_progress_at > max_quiet_period`: produce a synthetic `Error{error_class: "executor_quiet"}` terminal.
   - If `max_runtime > 0` AND `now - dispatched_at > max_runtime`: produce a synthetic `Error{error_class: "max_runtime_exceeded"}` terminal.
4. Add a new sweep function in `lib/graph/scheduler/scheduler.go` (or extend the existing `SweepReady` / `SweepParkedNodes` family) that walks active dispatches and applies the two deadline checks. This is `SweepExecutorDeadlines` — keep its file layout symmetrical with `SweepParkedNodes`.
5. Update the existing orphan reaper sweep at `lib/runtime/sweep_parked.go` (or its sibling that handles orphan-active-rows) to drop heartbeat-loss as a signal; the new orphan detection for active rows is the supervisor's gRPC client failure (in-band for sync) plus the deadline sweeps (out-of-band for async). The sweep's existing `OrphanedClaimTimeout = 5 * HeartbeatTimeout` constant goes away; replace with `max_runtime`-driven cleanup.
6. Add unit tests covering: sync RPC timeout fires `executor_sync_timeout`; quiet-period exceeded fires `executor_quiet`; max_runtime exceeded fires `max_runtime_exceeded`; `0`-disabled sentinel skips each check; coexistence (set `max_runtime` but not `max_quiet_period`) works.
7. Run `go test ./lib/graph/scheduler/... ./lib/runtime/... -count=1 -race`. Fix breakage iteratively.

### Task 14: Remove heartbeat-related code paths

**Files:** `lib/runtime/supervisor.go`, `lib/runtime/runner.go`, `lib/runtime/callback.go`, `lib/runtime/conductor.go`, `lib/graph/scheduler/scheduler.go`, `lib/foundation/persistence/node_runs.go`, anywhere else `grep -rn 'last_heartbeat\|LastHeartbeat\|heartbeat_interval\|HeartbeatTimeout\|OrphanedClaimTimeout' lib/` surfaces hits

**Steps:**
1. Run `grep -rn 'Heartbeat\|heartbeat\|last_heartbeat\|HeartbeatTimeout\|OrphanedClaimTimeout' lib/ --include='*.go'`. Categorize each hit: (a) deletable (the streaming Heartbeat handler, the heartbeat-extend code in claim-handle, the `last_heartbeat` accessor, the `LastHeartbeatAt` persistence field, the `rimsky_supervisors`-side heartbeat record); (b) consumer-facing API that requires migration to the new name (less likely — most are internal). The cutoff constants `HeartbeatTimeout` and `OrphanedClaimTimeout` live in TWO places: `lib/graph/scheduler/scheduler.go` (around lines 85-87 and 179-192) and `lib/runtime/conductor.go` (around lines 55-67). Both sites must be cleaned up.
2. Delete the obvious dead code: Heartbeat handler in the stream-reader (already gone after Task 9); the persistence accessor `LastHeartbeatAt`; the claim-handle heartbeat-extend method; the `rimsky_supervisors` heartbeat tracking (the column was dropped in Task 4/5 so any code reading/writing it must go).
3. Replace `HeartbeatTimeout` / `OrphanedClaimTimeout` with the new orphan-detection constants if any are still needed (the new sweep keys on `max_runtime` per dispatch, not a global cutoff — so the global constants likely delete outright; verify no code that needs to remain depends on them).
4. Update the conductor's doc comments (`lib/runtime/conductor.go` lines 11-16) to reflect the new orphan-detection signals (RPC connection state for sync; quiet-period / max_runtime sweep for async).
5. Build and test: `go build ./... && go test ./... -count=1`. Note: `make test-all` may still fail because of consumer code in `lib/services/` (loop_counter, claude-agent) referencing the old API; those are migrated in Pass 2.

### Task 15: Update the signal taxonomy — remove `event/<name>`

**Files:** `lib/foundation/signal/taxonomy.go`, `lib/foundation/signal/types.go`, `lib/foundation/signal/payloads.go`, `lib/foundation/signal/cel.go`, `lib/foundation/signal/taxonomy_test.go`, `lib/foundation/signal/payloads_test.go`, `lib/foundation/signal/cel_test.go`, anywhere else that constructs or matches `event/<name>` signals

**Steps:**
1. In `lib/foundation/signal/taxonomy.go` and `lib/foundation/signal/types.go`, find the signal type-path taxonomy registration and any enum/constants for the top-level kinds. Remove the `event/<name>` type-path validation / construction code path. The taxonomy now enumerates four top-level kinds: `terminal/*`, `transient/*`, `attribute/<key>/changed`, `message/*`.
2. In `lib/foundation/signal/payloads.go`, add a `tags` field to the `terminal/*` signal payload struct. Update the existing struct so subscribers' CEL `when:` predicates can reference `payload.tags`.
3. In `lib/foundation/signal/cel.go`, update the CEL environment binding to expose `payload.tags` (`list<string>`) for the `terminal/*` payload schema. Verify the existing CEL `in` operator works against `payload.tags` (it should — CEL's stdlib covers list membership).
4. Remove the `event_payload` field-naming convention entry from `payloads.go` (Park.payload → `park_payload` rename stays; the named-event payload → `event_payload` rename goes away).
5. Update signal-emit sites: the runner's call to construct a `terminal/success` / `terminal/error/<class>` / `terminal/park/<reason>` signal now populates the `tags` field from the verdict's tags. Look for `signalpkg.Signal{Type: ...}` constructions in `lib/runtime/` and pass the tags from the outcome through.
6. Update all touched tests (`taxonomy_test.go`, `payloads_test.go`, `cel_test.go`) to drop `event/<name>` cases and add `terminal/*` + `payload.tags` cases.
7. Run `go test ./lib/foundation/signal/... -count=1`.

### Task 16: Update node-subscription validators and the cascade walker for tag-based subscriptions

**Files:** `lib/graph/node/template_validator.go` and siblings (the subscription-grammar validator), `lib/foundation/cascade/state.go` (the state-machine transitions), `lib/runtime/cascade_invalidate.go` + `lib/runtime/cascade_recalculate.go` (the cascade walker — invalidation and recalculate paths). The `cascadeSubscribersStaleInTx` function lives in this `lib/runtime/` set; locate via `grep -rn cascadeSubscribersStaleInTx lib/`.

**Steps:**
1. In the node-subscription validator (in `lib/graph/node/`): reject `subscribes: [{type: event/...}]` as an unknown signal type. Accept `subscribes: [{type: terminal/*, when: "tag" in payload.tags}]` — verify the CEL parser binds `payload.tags` correctly (handled by the signal-side update in Task 15).
2. Validate that any tag referenced in a `when:` predicate's `in payload.tags` form against an exact `type: terminal/*` subscription is declared by the emitter's `declared_tags` observability capability. This is the new template-registration gate that replaces the old `declared_events` validation.
3. In the cascade walker (the `lib/runtime/cascade_invalidate.go` / `cascade_recalculate.go` family), the path that matched `event/<name>` signals is gone. The terminal-walk path already handles `terminal/*` signals; verify it picks up `payload.tags` correctly during CEL evaluation and inserts wait-set rows for subscribers whose `when:` predicate fires.
4. Update the cascade walker tests (`lib/runtime/cascade_invalidate_test.go`, `lib/runtime/hard_dep_cascade_test.go`, and their siblings): remove tests that constructed `event/<name>` signals; add tests where a terminal signal's tag triggers a subscriber match.
5. Run `go test ./lib/graph/... ./lib/foundation/cascade/... ./lib/runtime/... -count=1 -race`. Fix breakage.

### Task 17: Update the attribute substitution path — drop `nodes.<X>.event.<name>.<field-path>`

**Files:** `lib/graph/attribute/substitution.go` (or wherever the substitution grammar lives — `grep -rn 'nodes\..*\.event\.' lib/` locates the source kind handler)

**Steps:**
1. Find the substitution-grammar handler that parses `nodes.<X>.event.<name>.<field-path>`. Remove that source-kind entirely.
2. Update any registration / source-kind enumeration: the remaining kinds are `nodes.<X>.attribute.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`.
3. Update tests under `lib/graph/attribute/` to remove the event-substitution test cases.
4. Run `go test ./lib/graph/attribute/... -count=1`.

### Task 18: Update wait-set `topic_kind` handling in the persistence layer

**Files:** `lib/foundation/persistence/postgres/` and `lib/foundation/persistence/sqlite/` accessors for `rimsky_wait_set`

**Steps:**
1. Find any code that writes `topic_kind = 'event'` to `rimsky_wait_set`. After the cascade-walker change in Task 16, this code path should no longer exist; verify by `grep -rn '"event"' lib/foundation/persistence/` and inspecting hits.
2. Update any enum / constant string referring to the `event` topic_kind value — remove it.
3. Run the persistence conformance tests again: `go test ./lib/foundation/persistence/conformance/... -count=1`.

### Task 19: Wire `sync_rpc_deadline` into in-process executor dispatch

**Files:** `lib/runtime/executor/inproc_client.go` (or equivalent)

**Steps:**
1. The in-process executor adapter wraps an in-process handler (like `loop_counter`) behind the same `Execute(req)` shape as the gRPC client. Apply the `sync_rpc_deadline` deadline by deriving a `ctx, cancel := context.WithTimeout(parent, deadline)` if `deadline > 0`. In-process executors are fast and rarely hit this, but the surface should be uniform.
2. Verify with a unit test that an in-process executor whose handler `time.Sleep`s longer than the deadline produces an `Error{error_class: "executor_sync_timeout"}` terminal.

### Task 20: Update breakpoint matchers to drop `event/<name>` matcher type-paths

**Files:** `lib/graph/breakpoint/` (or wherever breakpoint matcher type-paths are validated — `grep -rn 'event/' lib/graph/` and `lib/runtime/` to locate)

**Steps:**
1. Find the breakpoint matcher validator. Reject `type: event/...` matchers; accept `type: terminal/*` with CEL `when:` filters on `payload.tags`.
2. Update any tests under `lib/graph/breakpoint/`.
3. Run `go test ./lib/graph/breakpoint/... -count=1`.

### Task 21: Update lineage-record references to named-event payloads

**Files:** `lib/foundation/lineage/` (or wherever lineage records are constructed — `grep -rn 'named.event\|named_event' lib/foundation/lineage/`)

**Steps:**
1. Find any lineage-record citation that references a named-event payload. After the named-event ledger is gone, those citations must move to terminal-tag citations (the per-emission data is now on the emitter's attribute_delta).
2. Update accordingly.

### Task 22: Verify the tree builds and core tests pass at end of Pass 1

**Files:** (verification only — no edits)

**Steps:**
1. Run `go build ./...` — entire tree compiles.
2. Run `make test-all` — full test suite passes. (Note: Pass 1 does NOT migrate `lib/services/executors/claude-agent/` or other consumers; if `make test-all` runs TypeScript tests for claude-agent, they may fail. Skip the TypeScript portion until Pass 2.)
3. Run `make lint` — clean.
4. Run `go test ./lib/scenarios/... -count=1` (the integration scenario tests). Some will fail because they use named-event-based scenarios; mark each failing test with a `t.Skip("re-enable after Pass 2 migrates loop_counter/claude-agent tests")` pointing to Pass 2. Acceptable for now.
5. Run `go test ./test/scenarios/... -count=1` (the other scenario tree if it exists). Same approach.

If anything else is broken, fix it iteratively before declaring Pass 1 complete.

**Pass 1 leaves the tree:** building, persistence + signal + cascade + scheduler tests green, a small number of consumer-side scenario tests skipped pending Pass 2.

---

## Pass 2: Consumers and proof artifacts (acceptance pass — STORY-executor-protocol, STORY-loop-counter-cap, STORY-opaque-executor-scratch, STORY-cascade-signal-blind)

**Goal:** Migrate every consumer of the old protocol (built-in executors, claude-agent, scenarios, demos, conformance), rewrite the four affected stories' proof artifacts to exhibit the new protocol surface end-to-end, and clean up Plumbline annotations across the affected code. End of pass: `make test-all` is fully green including TypeScript and scenario tests; the four proof artifacts run successfully against a booted rimsky stack.
**Scope:** Tasks 23–38
**Falsifier:** `loop_counter`'s handler still emits a `NamedEvent` instead of including a tag on its Success outcome, OR `claude-agent` still writes `Park.sessionToken` instead of `attributes_delta.session_token`, OR `lib/services/executors/claude-agent/src/server.ts::resolveEffectiveResumeContext` still has the dual-path code, OR any scenario test under `test/scenarios/` or `lib/services/test/` is skipped because it depends on the old protocol, OR `lib/protocols/conformance/executor/` still has named-event / heartbeat conformance scenarios, OR any of the four proof artifacts (executor-protocol example, loop-counter-cap scenario, opaque-executor-scratch executable proof, cascade-signal-blind table-driven proof) is missing or stubbed, OR any of those artifacts lacks an `@story:<slug>` annotation in a top-of-file comment.

### Task 23: Migrate `loop_counter` to emit a tag instead of a NamedEvent

**Files:** `lib/runtime/executor/builtin/loop_counter/handler.go`, `lib/runtime/executor/builtin/loop_counter/handler_test.go`, `lib/runtime/executor/builtin/loop_counter/schema.go`

**Steps:**
1. In `handler.go::Execute`: replace the `sink.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_NamedEvent{...}})` call (which no longer compiles after Pass 1) with the new Outcome-returning shape:
   - Compute the tag name as before (`"loop"` while count < max, else `"done"`).
   - Build the Success outcome carrying `tags: ["loop"]` or `tags: ["done"]` plus the existing `attributes_delta: { count: new_count }`.
   - Return `&genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{Changed: true, ChangeSummary: ..., AttributesDelta: ..., Tags: [...]}}}`.
2. The handler no longer uses the streaming `EventSink` interface. Update its signature to `Execute(ctx context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error)` matching the new in-process handler interface (defined in Task 19's in-process adapter shape).
3. Update `schema.go` if the schema referenced `declared_events`; rename to `declared_tags` and adjust the registration string.
4. Update `handler_test.go` to assert against the new Outcome shape: success carrying the expected tag and the expected attributes_delta.
5. Run `go test ./lib/runtime/executor/builtin/loop_counter/... -count=1`.

**Load-bearing constraint (state in the handler's GoDoc):** The tag is included on the Success outcome; do NOT call a separate emit method or compose a NamedEvent — that surface no longer exists. The tag rides the verdict.

### Task 24: Migrate `claude-agent` (Go-side wiring + observability declaration)

**Files:** `lib/services/executors/claude-agent/` — TypeScript implementation files, primarily `src/server.ts` and `src/agent-run.ts`

This task is large. The claude-agent currently runs in a streaming-RPC dual-path model. The migration profile per spec: async mode (returns AwaitAsyncCallback immediately, spawns CLI subprocess, POSTs the verdict from the subprocess), `max_quiet_period` generous (5 minutes), `max_runtime = 0`, session token rides attributes only.

**Steps:**
1. Read `lib/services/executors/claude-agent/src/server.ts` end-to-end. The gRPC service handler currently exposes `Execute` as a server-streaming method. Migrate to unary:
   - The `Execute` handler signature becomes `(call: ServerUnaryCall<ExecuteRequest, Outcome>, callback: sendUnaryData<Outcome>) => void` per the gRPC TypeScript convention.
   - The handler returns `AwaitAsyncCallback` immediately with a fresh `async_ack_id` (a UUID) and spawns the CLI subprocess in the background via `agent-run.ts::runAgent`.
2. In `src/agent-run.ts`:
   - Remove `sessionToken: opts.runId` and `payload: new Uint8Array()` from the Park outcome construction. The Park outcome's new fields are `reason`, `resume_at`, `reason_note`, `reason_label`, `attributes_delta` (carrying `session_token: opts.runId`), `tags`, `scratch`.
   - Remove `resumeContext?.sessionToken` reads. Replace with `req.attributes.session_token` (incoming attributes).
   - Update `resolveEffectiveResumeContext` (at `src/server.ts:955`): collapse to the single attribute-driven branch. The function may be deletable; its callers can read `req.attributes.session_token` directly. If kept as a helper, it returns `{ sessionToken: req.attributes.session_token }` when non-empty, else undefined.
3. Send the eventual settling Outcome via HTTP POST to `${callback_url}/v1/callback/${async_ack_id}` with an `AsyncCallbackBody` body carrying the chosen outcome variant (Success / Error / Park). No `events` field; tags ride on the chosen outcome.
4. Add keepalive: in `agent-run.ts::runAgent`, after the CLI subprocess is spawned and on each natural milestone (tool-call boundary, turn boundary), POST to `${callback_url}/v1/runs/${runId}/keepalive` with the existing cancel_token bearer. Use a debounced cadence (don't POST on every micro-event; batch to a sensible interval — once per tool-call or once per 30s, whichever fires first).
5. Update observability registration to declare tags (formerly events). The set of declared tags claude-agent uses today is the named-event names emitted during a run (typically including diagnostic events). Inventory them via `grep -rn 'declared_events' lib/services/executors/claude-agent/` and migrate each to the `declared_tags` registration.
6. Update `lib/services/executors/claude-agent/src/server.test.ts` (and any other TS test files) to assert the new behavior: `Execute` returns AwaitAsync immediately; the subsequent HTTP callback delivers the real Outcome; session_token reads/writes happen against attributes only; keepalive POSTs land.
7. `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`.

**Load-bearing constraint (state in agent-run.ts's GoDoc-style block above the Park-construction site):** session_token MUST be written to attributes_delta; do NOT carry it on the Park outcome itself (the field no longer exists). Reads on next dispatch come from req.attributes.session_token. Do not synthesize a session_token from anywhere else.

### Task 25: Migrate scenario / integration tests in `test/scenarios/` and `lib/services/test/`

**Files:** every `*.go` file under `test/scenarios/`, `lib/services/test/`, and any sibling scenario directories — `grep -rn 'declared_events\|NamedEvent\|Heartbeat\|StreamClose\|ResumeContext\|sessionToken' test/ lib/services/test/`

**Steps:**
1. Inventory failing scenario tests (those marked `t.Skip` in Pass 1).
2. For each, identify which protocol element it exercised: NamedEvent → tag; Heartbeat → keepalive (or delete the heartbeat-specific portion); ResumeContext → attributes carry-forward; old Park outcome → new Park with attributes_delta.
3. Rewrite the test to use the new shape. Use the new helpers introduced in Pass 1 (`Queue.LookupRunByAsyncAckID`, `BumpLastProgressAt`, etc.) where the tests need to inspect intermediate state.
4. Re-enable each (remove the `t.Skip`).
5. Run `go test ./test/scenarios/... ./lib/services/test/... -count=1` — all green.

### Task 26: Migrate conformance suite

**Files:** `lib/protocols/conformance/executor/runner.go`, `lib/protocols/conformance/executor/callback_receiver.go`, `lib/protocols/conformance/executor/await_terminal_test.go`, and every file under `lib/protocols/conformance/executor/scenarios/`

**Steps:**
1. The conformance suite at `lib/protocols/conformance/executor/scenarios/` currently has ten scenario files: `async_handoff.go`, `attributes_serialization.go`, `cancel.go`, `execute_happy_path.go`, `heartbeats.go`, `malformed_attributes.go`, `park_reason_emission.go`, `stream_close_without_terminal.go`, `terminal_is_last.go`, `unknown_ack_id.go`. Every one of these invokes the streaming `Execute` and will not compile after Pass 1. Walk each:
   - `async_handoff.go` → rewrite: assert AwaitAsyncCallback registers persistently and the callback arrives + drives the verdict. Assert callback survives a simulated supervisor restart.
   - `attributes_serialization.go` → rewrite: assert `attributes_delta` round-trips on Success AND on Error AND on Park (the new uniformity).
   - `cancel.go` → rewrite: the cancel_token cancellation path now applies to the unary RPC context. Assert that cancellation surfaces as the RPC's error.
   - `execute_happy_path.go` → rewrite: assert the unary `Execute(req) → Outcome{Success}` round-trip succeeds with the expected attributes_delta and tags.
   - `heartbeats.go` → delete (heartbeats are gone; do not replace with a keepalive scenario — keepalive is HTTP, not part of the executor gRPC surface and so does not belong in executor conformance).
   - `malformed_attributes.go` → rewrite: schema-validation rejection still applies on the unary path; assert the Error outcome with the documented `error_class`.
   - `park_reason_emission.go` → rewrite: assert Park outcome carries the right `reason` enum value (still snooze | await_callback) and that subscribers see the `terminal/park/<reason>` signal. Drop assertions about `Park.payload` / `Park.session_token` (gone).
   - `stream_close_without_terminal.go` → delete (no stream; "stream close without terminal" has no analogue under unary RPC).
   - `terminal_is_last.go` → delete or repurpose; the unary RPC has a single response by construction, so "terminal is last" is structurally guaranteed and needs no test.
   - `unknown_ack_id.go` → rewrite: assert the callback handler returns 404 / `unknown_async_ack_id` when an inbound POST carries an ack_id that has no persisted row.
2. Add new conformance scenarios:
   - `tags_round_trip.go` — emit Success with tags, assert downstream subscriber matches via CEL `in payload.tags`.
   - `attributes_delta_on_error_park.go` — emit Error and Park each with attributes_delta; assert persistence onto the dispatch row.
   - `async_callback_survives_restart.go` — distinct from `async_handoff.go`: explicitly kill and restart the supervisor between AwaitAsyncCallback and the callback POST; assert the callback still lands. This exercises the persistent-registry property (TD-persist-async-callback-registry).
3. Update `runner.go`, `callback_receiver.go`, `await_terminal_test.go` to drop heartbeat / stream-close handling code paths.
4. Run the conformance suite against a stub executor and against claude-agent:
   - `go run ./cmd/rimsky conformance executor --endpoint <stub> --transport grpc`
   - `go run ./cmd/rimsky conformance executor --endpoint <claude-agent> --transport grpc`

**Load-bearing constraint (state in the new `async_callback_survives_restart.go` scenario):** the persistent-registry property is load-bearing. The scenario must actually restart the supervisor between registration and callback — a same-process replay of the callback handler does not exercise this property. Use the testcontainers harness's stop/start primitives.

### Task 27: Plumbline cleanup — annotations and inline-comment hygiene

**Files:** every file under `lib/` that this spec's changes touched

**Steps:**
1. Run `make lint` — Plumbline `comment_hygiene` is currently disabled per `CLAUDE.md`, but `source_validity` and `blessed_invariant_test_coverage` are enabled. Verify no new `@source:` references break.
2. Run `grep -rn '@blessed-invariant 6' lib/` — `@blessed-invariant 6` was the heartbeat-cutoff invariant. With heartbeats gone, this invariant is retired. Remove its annotations (or migrate to a new invariant id if the spirit survives in the new sweep). Update `.plumbline.json` if its tag-vocabulary or the blessed-invariant catalog needs adjustment. Add new annotations for the load-bearing properties introduced by this spec: the AttributesDelta-with-verdict atomic-commit property; the persistent-registry-survives-restart property.
3. Run `bash -c "make lint"` and confirm clean.
4. Run `make test-all` and confirm fully green (no skipped tests anywhere in this plan's affected paths).

### Task 28: Proof artifact rewrite — STORY-executor-protocol

**Story:** STORY-executor-protocol
**Proof form (from spec, post-rewrite Acceptance):** Example — a shipped executor reference paired with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end (unary execute, async-callback registration + delivery, error-class routing, tag-based subscription).

**Files:** `examples/executor-protocol/` (new directory if not present), specifically `examples/executor-protocol/README.md` (the worked walkthrough) and `examples/executor-protocol/custom_executor.go` (the executor reference) plus any supporting template / docker-compose / config files needed to boot a rimsky stack against it.

**Steps:**
1. Survey the codebase for the existing pre-spec proof of STORY-executor-protocol (`grep -rn '@story: executor-protocol' .` and `grep -rn 'executor-protocol' examples/ docs/`). If a previous artifact exists, refactor it; if not, create the new example.
2. Author `examples/executor-protocol/custom_executor.go`: a self-contained Go program that:
   - Implements the new unary `Execute(ctx, req) → Outcome` RPC.
   - Declares two tags (`work_started`, `work_done`) via the observability `declared_tags` capability.
   - On `Execute`, runs a small piece of work synchronously and returns `Outcome{Success{tags: ["work_started", "work_done"], attributes_delta: {result: "ok"}}}`.
   - Also demonstrates the async path: a second `Execute` invocation (or a separate node-type entry) returns AwaitAsyncCallback, spawns a goroutine that sleeps briefly, then POSTs the callback body with Success.
   - Declares an `error_class: "demo_failure"` and a node that triggers it; the operator-side template's error-policy routes it.
3. Author `examples/executor-protocol/README.md`: a worked walkthrough that:
   - States what the example demonstrates (custom executor plugs into the new unary protocol).
   - Names the commands to boot a rimsky stack with this executor wired in (e.g., `docker compose up rimsky-all-in-one` plus the example executor running as a sidecar — concrete commands).
   - Walks the user through registering a template that references the custom executor's node-type, instantiating it, watching the dispatch land and settle.
   - Shows the operator dashboard event log (or `rimsky events <instance_id>`) where the `terminal/success` signal carries `payload.tags = ["work_started", "work_done"]` and a downstream subscriber on `when: "work_done" in payload.tags` dispatches.
   - Shows the async-callback path delivering a verdict after a simulated supervisor restart.
   - Includes verifying snippets / expected outputs at each step.
4. **Annotation:** add `// @story: executor-protocol` as the first comment line in `custom_executor.go`, and as a top-of-file HTML comment in `README.md` (`<!-- @story: executor-protocol -->`).
5. Run the walkthrough end-to-end against a fresh rimsky stack: `cd examples/executor-protocol && bash run.sh` (write a wrapper script if useful). Capture the expected output in the README.
6. Verify the example exhibits real work (the executor's `Execute` body does real work and returns real-looking attributes, not stubbed).

### Task 29: Proof artifact rewrite — STORY-loop-counter-cap

**Story:** STORY-loop-counter-cap
**Proof form (from spec, post-rewrite):** Demo — scenario test wiring a loop-counter node (maximum count of three) to a sink subscriber via `subscribes: [{node: <emitter>, type: terminal/success, when: "loop" in payload.tags}]` and a different sink subscriber on `"done" in payload.tags`; observes the `loop` tag fires three times then the `done` tag fires once.

**Files:** `test/scenarios/loop_counter_cap_scenario_test.go` (refactor existing or create new). Use the bundled-services integration harness pattern under `lib/services/test/scenarios/`.

**Steps:**
1. Find the existing scenario test for loop-counter-cap (`grep -rn '@story: loop-counter-cap' . && grep -rn 'loop_counter' test/scenarios/`).
2. Rewrite the test to:
   - Boot a rimsky-all-in-one stack via testcontainers (`make core-images` was previously run; the image is available).
   - Register a template with a loop-counter node configured `max: 3` and two downstream sink subscribers:
     ```yaml
     - subscribes: [{ node: loop_counter, type: terminal/success, when: "\"loop\" in payload.tags" }]
     - subscribes: [{ node: loop_counter, type: terminal/success, when: "\"done\" in payload.tags" }]
     ```
   - Instantiate the template, drive cascade for three iterations, assert: the loop-sink dispatched three times; the done-sink dispatched exactly once on the third iteration; the carry-forward `count` attribute crossed dispatches correctly.
3. **Annotation:** add `// @story: loop-counter-cap` as the first comment line.
4. Run the test: `go test ./test/scenarios/... -run LoopCounterCap -count=1 -v`. Pass.

### Task 30: Proof artifact rewrite — STORY-opaque-executor-scratch

**Story:** STORY-opaque-executor-scratch
**Proof form (from spec, post-rewrite — Acceptance verbatim):** Executable proof exercising the round-trip: executor writes scratch (mid-dispatch via the scratch callback, or via terminal-outcome attach); enqueue a follow-on dispatch row with the prior-dispatch link (using the same mechanism the cascade re-dispatch, stale-recovery, and retry-after-error paths use); assert the new dispatch's request carries the original scratch bytes verbatim.

**Files:** `test/scenarios/opaque_executor_scratch_test.go` (refactor existing or create new), plus any required executor stub.

**Steps:**
1. Find the existing executable proof (`grep -rn '@story: opaque-executor-scratch' .`).
2. Rewrite to use the new unary-RPC + persistent-registry path:
   - Boot rimsky-all-in-one.
   - Register an executor that on dispatch reads `req.scratch`, writes it to attributes_delta as a sanity assertion, and returns Success with `scratch: <some new bytes>`.
   - Trigger a stale-recovery enqueue via the `PRIOR_STALE_RECOVERY` disposition (force a max_quiet_period exceedance, or call the test helper that synthesizes the disposition).
   - Assert: the new dispatch's `req.scratch` carries the bytes the prior dispatch wrote; the executor's attribute_delta reflects that it observed them.
   - Repeat for retry-after-error (synthesize an Error with policy `retry`) and recalculate (synthesize an upstream invalidate that re-fires the same node).
3. **Annotation:** add `// @story: opaque-executor-scratch`.
4. Run: `go test ./test/scenarios/... -run OpaqueExecutorScratch -count=1 -v`. Pass.

### Task 31: Proof artifact rewrite — STORY-cascade-signal-blind

**Story:** STORY-cascade-signal-blind
**Proof form (from spec, post-rewrite):** Executable proof — table-driven scenario test that iterates over the cascade-firing signal types (`terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`) and asserts, for each: (a) a per-sender subscription on that type-path dispatches its subscriber when the upstream emits the signal; (b) a cross-cutting (`instance: true`) subscription on that type-path dispatches; (c) the audit row for the signal lands in the event log; (d) trailing-`*` prefix subscription shapes match every type-path with that prefix. Additionally: a subscription on `terminal/success` with `when: "x" in payload.tags` dispatches when a sender's Success carries `tags: ["x"]`.

**Files:** `test/scenarios/cascade_signal_blind_test.go` (refactor existing).

**Steps:**
1. Find the existing table-driven proof (`grep -rn '@story: cascade-signal-blind' .`).
2. Rewrite the table to use the post-collapse taxonomy. Remove the `event/<name>` row; add a `terminal/success + tags` row that exercises the CEL tag filter.
3. **Annotation:** add `// @story: cascade-signal-blind`.
4. Run: `go test ./test/scenarios/... -run CascadeSignalBlind -count=1 -v`. Pass.

### Task 32: Update other annotated artifacts that reference dropped surfaces

**Files:** anywhere `grep -rn '@source: .*executor.proto\|@source: .*ResumeContext\|@source: .*StreamClose\|@source: .*Heartbeat' .` surfaces

**Steps:**
1. Run the grep across the working tree.
2. For each hit, update the `@source:` annotation to point at the new symbol (e.g., a former `@source: executor.proto::StreamClose` becomes `@source: executor.proto::Outcome`).
3. Re-run `make lint` — `source_validity` must remain clean.

### Task 33: Re-verify the full test surface

**Files:** (verification only)

**Steps:**
1. `go build ./...` — clean.
2. `make test-all` — fully green; no skipped tests anywhere this plan touched.
3. `cd lib/services/executors/claude-agent && npm test && npm run build` — green.
4. `make lint` — clean.
5. `go run ./cmd/rimsky conformance executor --endpoint <stub-executor>` — passes.
6. Optionally rebuild the touched core images via `make core-images && make service-images` — sanity check that the docker build chains still work.

### Task 34–37: Reserved for follow-on consumer/scenario fixes if Pass 2's primary tasks surface additional cleanup needs

(These tasks are placeholders — the implementer extends them only if additional consumer migrations surface during Pass 2; otherwise the tasks remain unauthored and the pass proceeds to Task 38.)

### Task 38: Mark Pass 2 complete

**Files:** (verification only)

**Steps:**
1. Re-confirm `make test-all && make lint` — clean.
2. Re-confirm the four proof artifacts are present, annotated, and exercise the real assembled product (each artifact boots or invokes the rimsky stack and observes a real observable outcome).
3. Re-confirm no scenario test under `test/scenarios/` or `lib/services/test/` is `t.Skip`-ed.

**Pass 2 leaves the tree:** fully green test suite, all consumers migrated, four proof artifacts exhibiting the four affected stories' new Acceptance language.

---

## Pass 3: Design docs — concept, decision, story mutations

**Goal:** Apply every `## Design changes` entry from the spec to the `.ok-planner/design/` catalog. Concept files, decision files, and story files all reach their new prescriptive shape. Per the brainstorm pipeline, design docs and code change as one unit; this pass closes that loop.
**Scope:** Tasks 39–63
**Falsifier:** Any `## Design changes` bullet from the spec is not reflected in the corresponding file under `.ok-planner/design/` (concept, decision, or story), OR a concept file's body still carries a reference to a retired surface (named-event, streaming, heartbeats, resume context, Park.payload, Park.session_token, `event/<name>` signal), OR `concepts/named-event.md` is still in the live `concepts/` directory rather than `concepts/_retired/`, OR `concepts/terminal-tag.md` is missing, OR the concepts TOC (`concepts.md`) doesn't list `terminal-tag` and doesn't note `named-event` as retired, OR any of the four affected story files (executor-protocol.md, loop-counter-cap.md, opaque-executor-scratch.md, cascade-signal-blind.md) still contains the pre-mutation Acceptance / Falsifier / Proof text.

For each task in this pass: the spec's `## Design changes` bullet specifies the prescriptive text. The plan task names which file to edit and which bullet from the spec to apply. The implementer reads the bullet and writes the file's new body to match — current-state-only (no `## Notes`, no dated audit trail, no "previously X" phrasing) per the ok-planner rules.

### Task 39: Mutate `.ok-planner/design/concepts/executor.md`

**Files:** `.ok-planner/design/concepts/executor.md`

**Steps:**
1. Read the spec's `## Design changes` first bullet (executor.md mutation). It prescribes a full rewrite of the "What it is", "Scratch", "Boundaries", and "Invariants" sections.
2. Apply the prescribed rewrite to the file. Preserve the frontmatter (`---\nconcept: executor\nstatus: as-is\naliases: []\n---`). Drop the streaming-related text; the new body is the spec's prescribed text verbatim.
3. Run `bash plumbline ... <path>` (or `make lint` if the project's plumbline lint command is wired into the Makefile). Confirm clean.

### Task 40: Mutate `.ok-planner/design/concepts/parked-state.md`

**Files:** `.ok-planner/design/concepts/parked-state.md`

**Steps:**
1. Apply the spec's `## Design changes` parked-state bullet (drop payload/session_token/resume_reason/ResumeContext refs; replace the "Resume context" subsection with the spec's prescribed text; drop the heartbeat-exemption phrasing; drop the parked_payload Adjacent parenthetical).

### Task 41: Mutate `.ok-planner/design/concepts/signal.md`

**Files:** `.ok-planner/design/concepts/signal.md`

**Steps:**
1. Apply the spec's signal.md bullet (remove `event/<name>` subsection and the named-event/event_payload field-naming table row; rewrite the wait-set projection invariant; update the taxonomy enumeration to four kinds).

### Task 42: Mutate `.ok-planner/design/concepts/node-subscription.md`

**Files:** `.ok-planner/design/concepts/node-subscription.md`

**Steps:**
1. Apply the spec's node-subscription bullet (update the grammar examples to use `terminal/*` + CEL tag filter; drop `event/<name>` as a valid `type:` form).

### Task 43: Mutate `.ok-planner/design/concepts/blob-backend.md`

**Files:** `.ok-planner/design/concepts/blob-backend.md`

**Steps:**
1. Apply the blob-backend bullet (collapse the surfaces enumeration to one: attribute values).

### Task 44: Mutate `.ok-planner/design/concepts/auto-terminal.md`

**Files:** `.ok-planner/design/concepts/auto-terminal.md`

**Steps:**
1. Apply the auto-terminal bullet (heartbeat-extend → liveness-extend in Invariants).

### Task 45: Mutate `.ok-planner/design/concepts/supervisor.md`

**Files:** `.ok-planner/design/concepts/supervisor.md`

**Steps:**
1. Apply the supervisor bullet (replace the heartbeat-queryable-timestamps sentence with the new liveness wording; replace `heartbeating` in Owns).

### Task 46: Mutate `.ok-planner/design/concepts/claim-handle.md`

**Files:** `.ok-planner/design/concepts/claim-handle.md`

**Steps:**
1. Apply the claim-handle bullet (rewrite `active`-state definition; replace heartbeat references in Does-NOT-own and Invariants).

### Task 47: Mutate `.ok-planner/design/concepts/orphan-reaper.md`

**Files:** `.ok-planner/design/concepts/orphan-reaper.md`

**Steps:**
1. Apply the orphan-reaper bullet (rewrite "What it is", "Purpose", and the heartbeat-cutoff invariant).

### Task 48: Mutate `.ok-planner/design/concepts/node-run.md`

**Files:** `.ok-planner/design/concepts/node-run.md`

**Steps:**
1. Apply the node-run bullet (rewrite parked-fields portion; drop last-heartbeat / heartbeat-fields language; add async_ack_id / async_ack_registered_at / last_progress_at / tags; rewrite the two heartbeat-based invariants; update the prior-dispatch-dispositions parenthetical).

### Task 49: Mutate `.ok-planner/design/concepts/cascade.md`

**Files:** `.ok-planner/design/concepts/cascade.md`

**Steps:**
1. Apply the cascade bullet (drop event/<name> references; cascade-fire keys on terminal/* with CEL tag filters).

### Task 50: Mutate `.ok-planner/design/concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

**Steps:**
1. Apply the attribute bullet (add the attributes_delta-on-all-settling-terminals + writeback-bumps-last_progress_at language; drop `nodes.<X>.event.<name>.<field-path>` from the substitution-grammar invariant).

### Task 51: Mutate `.ok-planner/design/concepts/breakpoint.md`

**Files:** `.ok-planner/design/concepts/breakpoint.md`

**Steps:**
1. Apply the breakpoint bullet (drop event/<name> matcher type-paths; tags via CEL filter).

### Task 52: Mutate `.ok-planner/design/concepts/lineage-record.md`

**Files:** `.ok-planner/design/concepts/lineage-record.md`

**Steps:**
1. Apply the lineage-record bullet (drop named-event citations).

### Task 53: Mutate `.ok-planner/design/concepts/rimsky.md`

**Files:** `.ok-planner/design/concepts/rimsky.md`

**Steps:**
1. Apply the rimsky bullet (update high-level executor-protocol description to unary + async callback).

### Task 54: Mutate `.ok-planner/design/concepts/terminal-resolution.md`

**Files:** `.ok-planner/design/concepts/terminal-resolution.md`

**Steps:**
1. Apply the terminal-resolution bullet (rewrite the four-decisions framing; surface is now settling-terminal-driven via terminal/* + tags).

### Task 55: Mutate `.ok-planner/design/concepts/inertness.md`

**Files:** `.ok-planner/design/concepts/inertness.md`

**Steps:**
1. Apply the inertness bullet (drop named-event payloads from carrier streams + structural-inertness applies-to + sanctioned read sites + adjacent concepts; rewrite §21; collapse pattern-matches three-list to two; drop stream-close wording from scratch-related entry).

### Task 56: Mutate `.ok-planner/design/concepts/event-log.md`

**Files:** `.ok-planner/design/concepts/event-log.md`

**Steps:**
1. Apply the event-log bullet (drop the @blessed-invariant 21 contrast parenthetical; drop the named-event-ledger boundary entry; drop named-event from adjacent concepts).

### Task 57: Mutate `.ok-planner/design/concepts/observability.md`

**Files:** `.ok-planner/design/concepts/observability.md`

**Steps:**
1. Apply the observability bullet (drop named-event from adjacent concepts).

### Task 58: Mutate `.ok-planner/design/concepts/message.md`

**Files:** `.ok-planner/design/concepts/message.md`

**Steps:**
1. Apply the message bullet (drop event-emissions-from-executors / named-event references in Boundaries).

### Task 59: Create `.ok-planner/design/concepts/terminal-tag.md` and update `concepts.md` TOC

**Files:** `.ok-planner/design/concepts/terminal-tag.md` (new), `.ok-planner/design/concepts.md`

**Steps:**
1. Create the file with the prescribed full body from the spec's `## Design changes` terminal-tag-create bullet. Frontmatter:
   ```yaml
   ---
   concept: terminal-tag
   status: as-is
   aliases: []
   ---
   ```
   Body: Definition / Purpose / Boundaries / Invariants as prescribed. Path-free (no file paths, no code citations).
2. Edit `.ok-planner/design/concepts.md` (the TOC) — add a `terminal-tag` entry in the alphabetical concepts list with a one-sentence description matching the new concept's Definition.

### Task 60: Retire `.ok-planner/design/concepts/named-event.md` to `_retired/`

**Files:** `.ok-planner/design/concepts/named-event.md` (move), `.ok-planner/design/concepts/_retired/named-event.md` (new), `.ok-planner/design/concepts.md`

**Steps:**
1. `git mv .ok-planner/design/concepts/named-event.md .ok-planner/design/concepts/_retired/named-event.md` (or `mv` if not tracked — verify via `git ls-files`).
2. Open the moved file and update its body. Replace the existing definition body with the retirement note prescribed in the spec:
   > **Retired** by `spec:2026-06-16-executor-protocol-coherence-design`. The capability folds into the settling-terminal's `tags` set field on the verdict, plus per-emission data on `attributes_delta` (see `concept:terminal-tag`).
   Update the frontmatter's `status:` from `as-is` to `retired`.
3. Edit `.ok-planner/design/concepts.md` (the TOC):
   - Remove the live `named-event` entry from the alphabetical Concepts list.
   - Add an entry under "Retired concepts" with the prescribed one-line description (`See concepts/_retired/named-event.md — replaced by concept:terminal-tag; tags ride on the settling terminal verdict and per-emission data on attributes_delta.`).

### Task 61: Mutate the seven affected decisions

**Files:**
- `.ok-planner/design/decisions/claude-agent-session-attribute.md`
- `.ok-planner/design/decisions/async-callback-outcome-oneof.md`
- `.ok-planner/design/decisions/async-callback-post-json.md`
- `.ok-planner/design/decisions/scratch-protocol.md`
- `.ok-planner/design/decisions/scratch-column.md`
- `.ok-planner/design/decisions/scratch-recovery.md`
- `.ok-planner/design/decisions/loop-counter-shape.md`

**Steps:**
1. For each file, apply the corresponding spec `## Design changes` decision-mutation bullet to rewrite the affected sections (Choice / Rationale / Alternatives where named). Current-state-only.
2. Run `bash plumbline` against each.

### Task 62: Create the thirteen new decision files

**Files:**
- `.ok-planner/design/decisions/executor-unary-rpc.md` (TD-execute-rpc-unary)
- `.ok-planner/design/decisions/terminal-tags.md` (TD-collapse-named-event-to-tags)
- `.ok-planner/design/decisions/uniform-attributes-delta.md` (TD-attributes-delta-on-all-settling-terminals)
- `.ok-planner/design/decisions/no-resume-context.md` (TD-remove-resume-context)
- `.ok-planner/design/decisions/async-callback-persistent-registry.md` (TD-persist-async-callback-registry)
- `.ok-planner/design/decisions/three-dispatch-deadlines.md` (TD-three-dispatch-deadlines)
- `.ok-planner/design/decisions/keepalive-endpoint.md` (TD-keepalive-endpoint)
- `.ok-planner/design/decisions/writeback-bumps-progress.md` (TD-writeback-bumps-progress)
- `.ok-planner/design/decisions/tag-based-subscription.md` (TD-subscription-grammar-shift)
- `.ok-planner/design/decisions/no-event-substitution.md` (TD-remove-event-substitution-path)
- `.ok-planner/design/decisions/orphan-reaper-connection-state.md` (TD-orphan-reaper-no-heartbeat)
- `.ok-planner/design/decisions/claude-agent-attribute-only-session.md` (TD-claude-agent-session-attribute-only)
- `.ok-planner/design/decisions/prior-stale-recovery-rename.md` (TD-prior-stale-rename)

**Steps:**
1. For each TD, read the spec's `## Technical decisions` block for the named TD. Create the corresponding decision file with:
   ```yaml
   ---
   decision: <slug>
   status: as-is
   aliases: []
   ---
   ```
   Body: `# <title>`, `## Choice`, `## Rationale`, `## Alternatives` (when the TD has alternatives), each section's text copied from the TD's prescriptive content. Current-state-only — the decision body describes the choice as it stands, no audit trail.

### Task 63: Mutate the four affected story files

**Files:**
- `.ok-planner/design/stories/executor-protocol.md`
- `.ok-planner/design/stories/loop-counter-cap.md`
- `.ok-planner/design/stories/opaque-executor-scratch.md`
- `.ok-planner/design/stories/cascade-signal-blind.md`

**Steps:**
1. For each file, apply the prescribed Role / Capability / Acceptance / Falsifier / Proof rewrites from the spec's `## Design changes` story-mutation bullets. Each story file's frontmatter stays (slug, `status: as-is`, aliases). Body sections get replaced in place per the prescription.
2. Run `bash plumbline` across the design directory — clean.

---

## End-of-plan verification

After Task 63:

1. `go build ./...` clean.
2. `make test-all` fully green.
3. `make lint` clean.
4. `cd lib/services/executors/claude-agent && npm test && npm run build` clean.
5. `bash plumbline .` clean (or `make lint` if the lint step wraps it).
6. `grep -rn 'NamedEvent\|StreamClose\|ExecuteEvent\|Heartbeat\|ResumeContext\|parked_payload\|session_token\|wake_reason\|@blessed-invariant 6\|declared_events\|nodes\..*\.event\.\|topic_kind = .event\|PRIOR_HEARTBEAT_STALE\|heartbeat_stale' . --include='*.go' --include='*.ts' --include='*.proto' --include='*.sql' --include='*.md'` returns either no hits or only hits in the spec / plan / `.ok-planner/history/` (point-in-time records).
7. `grep -rn '@story:' . --include='*.go' --include='*.ts'` surfaces at least four annotated proof artifacts (one per affected story).
8. `grep -rn '@concept: terminal-tag' . --include='*.go' --include='*.ts'` surfaces at least one annotation at the runner-side site that handles tag persistence and cascade-fire (the terminal-tag concept must have at least one code-side anchor).

---

## Manual checks after completion

These cannot be expressed as runnable commands; the user should walk them after the automated plan is green:

- **Restart-survival sanity check.** Boot a local rimsky-all-in-one. Register a template using claude-agent (or the new shipped custom-executor example from Task 28) in async mode. Trigger a dispatch. While the dispatch is in `phase='active'` with an async_ack_id registered, kill the supervisor and restart. POST the callback. Verify the eventual terminal lands on the dispatch row correctly. (Task 10's load-bearing constraint is the property; this is the human-eye confirmation.)
- **Operator-cancel of a pending callback.** Manually inspect `table:rimsky_node_runs` for a row with a registered async_ack_id; use admin tooling (or a direct SQL update via `psql`) to flip the row to a failed terminal; confirm a subsequent callback POST for that ackID returns 404 / rejected_run_terminal rather than landing.
- **Keepalive cadence by eye.** Watch the claude-agent's CLI run produce keepalive POSTs at the cadence configured. The cadence should land in operator-friendly territory (every ~30s or per tool-call boundary), not flooding the supervisor with sub-second pings.
