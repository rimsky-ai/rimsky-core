# Message schema layer Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-14-message-schema-layer-design.md`

**Goal:** Make messages a typed, schema-declared primitive; collapse coalesce and the per-subscription `frame:` modifier; route every frame creation through one message-delivery path; replace ad-hoc operator invalidate with a gated debug channel; retire `concept:backfill`; collapse legacy envelope/publisher-routing fields into typed-message vocabulary.

**Architecture:** Pre-v1, pure-removal stance. Retired DSL surfaces and code paths are removed entirely; templates and requests that use them fail through the *normal* validator paths (unknown field, unknown signal type, unknown message type) — no detection rules, no migration error strings, no parser cases that name the old shape. Pre-v1 also means dev databases get nuked rather than carrying forward stale schema; the plan calls this out where it applies.

**Tech Stack:** Go (`go-chi/chi` routing, `jackc/pgx/v5` Postgres, `modernc.org/sqlite` SQLite, stdlib `log/slog`); template DSL is YAML parsed into typed Go structs under `lib/foundation/spec/`; CEL predicates over signal payloads; JSON Schema for `body_schema:` validation (existing dependency).

---

## Pass 1: Persistence schema + envelope/field renames

**Goal:** Land the storage-layer shape changes the spec requires, and update every Go struct and code path that reads or writes the renamed/removed fields. The tree must build cleanly at the end of this pass; no dangling references to retired columns or fields.

**Scope:** Tasks 1–9

**Falsifier:** the migration is not numbered into both `lib/foundation/persistence/postgres/migrations/` and `lib/foundation/persistence/sqlite/migrations/`; OR `rimsky_frames` does not carry a `triggering_message_id UUID NOT NULL` column; OR `rimsky_frames` still has a `source_node_ids` column; OR `rimsky_frames` still has a `frame_resolution_mode` column; OR `rimsky_instances` still has a `frame_delivery_mode` column; OR `rimsky_messages` still has `kind`, `target`, or `backfill_operation_id` columns (must be renamed/removed); OR `rimsky_publisher_subscriptions` still has a `message_kind` column; OR `go build ./...` fails.

**Pre-v1 note:** This migration is not backwards-compatible with existing dev databases. The migration drops columns and renames fields; existing rows will not survive a forward run. The user should nuke their dev Postgres / SQLite before running it.

### Task 1: Write Postgres migration

**Files:** `lib/foundation/persistence/postgres/migrations/010-message-schema-layer.sql` (new — the next sequential number after the current `009-subscription-mounting.sql`)

**Steps:**

1. Read the latest existing migration `lib/foundation/persistence/postgres/migrations/009-subscription-mounting.sql` to see the file-header convention (comment block, `BEGIN;` / `COMMIT;` wrapping, the `INSERT INTO rimsky_migrations` footer).
2. Create the new migration file. It must:
   - `ALTER TABLE rimsky_frames ADD COLUMN triggering_message_id UUID NOT NULL` (no DEFAULT — pre-v1 forward migration; existing rows fail this, requiring a clean dev database).
   - `ALTER TABLE rimsky_frames DROP COLUMN source_node_ids`.
   - `ALTER TABLE rimsky_frames DROP COLUMN frame_resolution_mode` (declared in `001-schema.sql:76`; retires with coalesce).
   - `DROP INDEX` for the partial uniqueness index that today enforces "at most one queued `coalesce` frame per instance." The index is declared in `001-schema.sql` (or an early migration) with a predicate like `WHERE state = 'queued' AND frame_resolution_mode = 'coalesce'`. Find the exact index name by grepping `lib/foundation/persistence/postgres/migrations/*.sql` for the predicate, or `psql -c "\d+ rimsky_frames"` against a populated dev database.
   - `ALTER TABLE rimsky_instances DROP COLUMN frame_delivery_mode`.
   - `ALTER TABLE rimsky_messages DROP COLUMN backfill_operation_id`.
   - `ALTER TABLE rimsky_messages DROP COLUMN target`.
   - `ALTER TABLE rimsky_messages RENAME COLUMN kind TO type`.
   - `ALTER TABLE rimsky_publisher_subscriptions RENAME COLUMN message_kind TO message_type`.
   - Foreign key on `rimsky_frames.triggering_message_id` → `rimsky_messages(id)` ON DELETE RESTRICT (match the conventional FK pattern used elsewhere in the schema; check sibling FK declarations in 001-schema.sql).
3. Add the trailing `INSERT INTO rimsky_migrations (id, name, applied_at) VALUES (10, '010-message-schema-layer', now())` (match the convention in 009).
4. Verify the migration applies via the testcontainers-based migration test: run `go test ./lib/foundation/persistence/postgres/ -run TestMigrate -count=1` and confirm the test boots a fresh Postgres container, applies all migrations including 010, and reports success. (Pre-v1: the NOT NULL constraint requires the test container to start clean — existing data does not survive this migration.)

### Task 2: Write SQLite migration

**Files:** `lib/foundation/persistence/sqlite/migrations/010-message-schema-layer.sql` (new — same number as Task 1)

**Steps:**

1. Read the latest existing SQLite migration (check `ls lib/foundation/persistence/sqlite/migrations/` for the highest existing number) to see the convention.
2. Create the new SQLite migration. SQLite has more limited `ALTER TABLE` semantics — for column drops and renames, you may need the rebuild-table dance (`CREATE TABLE rimsky_frames_new (...)` with the new shape, `INSERT INTO rimsky_frames_new SELECT ... FROM rimsky_frames`, `DROP TABLE rimsky_frames`, `ALTER TABLE rimsky_frames_new RENAME TO rimsky_frames`). Apply the same shape changes as the Postgres migration.
3. Verify via the SQLite migration test: `go test ./lib/foundation/persistence/sqlite/ -run TestMigrate -count=1` and confirm clean.

### Task 3: Update `rimsky_frames` Go struct and accessors

**Files:** `lib/foundation/persistence/frames.go`, `lib/foundation/persistence/postgres/frames.go`, `lib/foundation/persistence/sqlite/frames.go` (and `frames_test.go` siblings)

**Steps:**

1. In `lib/foundation/persistence/frames.go` (the interface file): add `TriggeringMessageID shared.UUID` to the `FrameRow` struct. Remove `SourceNodeIDs`. Remove `Mode FrameResolutionMode` (the per-frame mode field that retires alongside the column drop in Task 1).
2. Update the `FramesTable` interface signatures. `LookupFrameResolutionMode(ctx, instanceID, tx)` becomes irrelevant once `frame_resolution_mode` is removed; leave the signature in place for now, returning a hardcoded `serial_queue` so the build doesn't break until Pass 2 simplifies it away. (This keeps Pass 1 a pure storage-layer change.)
3. In `lib/foundation/persistence/postgres/frames.go`: update SELECT/INSERT column lists for the new shape. Drop `source_node_ids` and `frame_resolution_mode` from all SELECTs and INSERTs (currently used at lines 419, 428, 453, 477, 479, 526, 704). Add `triggering_message_id` everywhere it needs to be read/written. The `EnqueueSerialFrame` and `EnqueueCoalesceFrame` signatures should take `triggeringMessageID uuid.UUID` as a new required parameter (Pass 2 will collapse them to one `EnqueueFrame`). The partial-unique-index ON CONFLICT clause at line 479 (referencing `frame_resolution_mode = 'coalesce'`) drops with the index.
4. Same updates in `lib/foundation/persistence/sqlite/frames.go`.
5. Run `go build ./lib/foundation/persistence/...` and confirm the package compiles.

### Task 4: Update `rimsky_instances` Go struct and accessors

**Files:** `lib/foundation/persistence/instances.go`, `lib/foundation/persistence/postgres/instances.go`, `lib/foundation/persistence/sqlite/instances.go`

**Steps:**

1. Remove the `FrameDeliveryMode string` field from the `InstanceRow` struct and from the `CreateInstanceRequest` struct.
2. Remove the `frame_delivery_mode` column from the `instanceCols` constant (currently at `lib/foundation/persistence/postgres/instances.go:29` and `lib/foundation/persistence/sqlite/instances.go:23`).
3. Drop the INSERT column from `Create` and the SELECT projection.
4. Remove the defaulting logic (`if in.FrameDeliveryMode != "" { deliveryMode = in.FrameDeliveryMode }` — currently at `postgres/instances.go:68-69` and `sqlite/instances.go:58-59`).
5. Run `go build ./lib/foundation/persistence/...` and confirm.

### Task 5: Update `rimsky_messages` Go struct and accessors

**Files:** `lib/foundation/persistence/messages.go`, `lib/foundation/persistence/postgres/messages.go`, `lib/foundation/persistence/sqlite/messages.go`

**Steps:**

1. In the `MessageRow` struct: rename `Kind string` → `Type string`. Remove the `Target string` field. Remove `BackfillOperationID *string` (or whatever its current type is — check the current struct).
2. In the `EnqueueMessageRequest` struct in `lib/runtime/message_delivery.go`: rename `Kind` → `Type`, remove `Target`, remove `BackfillOperationID`.
3. Update SELECT/INSERT column lists in Postgres and SQLite accessors.
4. Update the `MessagesTable.Insert` validation (currently in `EnqueueMessage`): the switch on `req.SenderKind` stays; the `Kind == ""` check becomes `Type == ""`.
5. Run `go build ./lib/foundation/persistence/...` and confirm.

### Task 6: Update `rimsky_publisher_subscriptions` Go struct and accessors

**Files:** `lib/foundation/persistence/publisher_subscriptions.go`, `lib/foundation/persistence/postgres/publisher_subscriptions.go`, `lib/foundation/persistence/sqlite/publisher_subscriptions.go`

**Steps:**

1. In `lib/foundation/persistence/publisher_subscriptions.go`: rename `MessageKind string` → `MessageType string` on the `PublisherSubscriptionRow` struct. There may also be a separate `Kind` field on the same struct (unrelated to `MessageKind`) — confirm via reading the file and leave non-message-kind fields untouched.
2. Update SELECT/INSERT column lists in Postgres and SQLite accessors.
3. Drop the default-to-`"invalidate"` logic if any (search for any code that defaults `MessageType` / `MessageKind` to `"invalidate"`).
4. Run `go build ./lib/foundation/persistence/...` and confirm.

### Task 7: Update control-API request/response types and handlers

**Files:** `lib/control/controlapi/messages.go`, `lib/control/controlapi/instances.go` (publisher-subscription request/response types are surfaced via `instances.go::instanceSubscriptionItem`)

**Steps:**

1. In `lib/control/controlapi/messages.go::PostInstanceMessagesRequest`: rename `Kind` → `Type` (JSON tag `type`). Remove `Target`. Update the handler that calls `EnqueueMessage` accordingly.
2. In `lib/control/controlapi/instances.go`: remove `FrameDeliveryMode *string` from the request body struct (currently `instances.go:117`), remove its JSON field; remove the validation switch at `instances.go:337-344`; remove the assignment at `instances.go:467-468`; remove the response field at `instances.go:153, 1244`.
3. In `lib/control/controlapi/instances.go`: find the `instanceSubscriptionItem` struct (around line 174) and rename its `Kind string` field (currently at line 179, JSON tag `kind`) → `MessageType string` (JSON tag `message_type`). Update the populator at line 747 that constructs `instanceSubscriptionItem{...}`.
4. Run `go build ./lib/control/...` and confirm.

### Task 8: Update runtime call sites and remaining consumers

**Files:** anywhere in `lib/runtime/`, `lib/graph/`, `lib/control/`, `cmd/` that references the renamed/removed fields.

**Steps:**

1. Run `grep -rn 'Kind' lib/runtime/message_delivery.go lib/runtime/*.go` and update every reference to `msg.Kind` → `msg.Type` (only where the noun is a `MessageRow`/`EnqueueMessageRequest`, not other unrelated `Kind` fields).
2. Run `grep -rn '\.Target' lib/runtime/ lib/graph/ lib/control/` to find references to the dropped `Target` field on message envelopes; remove or update.
3. Run `grep -rn 'FrameDeliveryMode\|frame_delivery_mode' lib/` to find every remaining reference. Update or remove. Some references are in `message_delivery.go::SweepDeliverMessagesForRunningFrames` and friends — those keep referencing the field until Pass 2 removes the whole code path, so for now hardcode `mode := FrameDeliverySerialQueue` (or equivalent default) inline.
4. Run `grep -rn 'MessageKind' lib/ cmd/` and update to `MessageType`.
5. Run `grep -rn 'source_node_ids\|SourceNodeIDs' lib/ cmd/` and remove all references. Update tests that build seed rows with the column.
6. Run `grep -rn 'backfill_operation_id\|BackfillOperationID' lib/ cmd/ --include='*.go'` and remove non-backfill-test references. (Backfill test/CLI files remain in place; they get removed wholesale in Pass 8.)
7. Run `go build ./...` and resolve any remaining build errors.

### Task 9: Update existing scenario tests' seed SQL and delete retired-behavior tests

**Files:** any `test/scenarios/*.go` and `lib/foundation/persistence/postgres/*_test.go` that include literal column lists referencing the changed columns; explicit deletions of retired-behavior tests.

**Steps:**

1. Delete the following test files wholesale — they exercise retired behavior (coalesce-mode messaging, frame:in operator invalidate) and have no equivalent under the new model:
   - `lib/control/controlapi/instance_frame_delivery_mode_test.go`
   - `test/scenarios/frame_coalesce_self_invalidate_test.go`
   - `test/scenarios/cascade_operator_frame_in_e2e_test.go`
2. Run `grep -rn 'frame_resolution_mode\|frame_delivery_mode\|source_node_ids\|backfill_operation_id\|message_kind' test/ lib/foundation/persistence/` and update each remaining test's INSERT/SELECT to match the new shape (drop the column from column lists, drop the value from VALUES tuples).
3. Tests that construct `node.TemplateSpec` literals with `FrameResolutionMode:` still compile in Pass 1 (the field still exists in the spec struct; Pass 2 removes it). Leave them for now.
4. Run `go test ./lib/foundation/persistence/postgres/... ./lib/foundation/persistence/sqlite/... -count=1` and confirm the persistence-layer unit tests pass.
5. Run `go build ./...` one more time to make sure the whole tree compiles.

---

## Pass 2: Coalesce removal + frame producer simplification

**Goal:** Remove the entire coalesce mechanism from the code — the `FrameResolutionMode` template field, the `FrameDeliveryMode` runtime type, the coalesce conflict resolver, the coalesce frame producer branch — and simplify the frame producer to one path that takes `triggering_message_id` as a required parameter.

**Scope:** Tasks 10–15

**Falsifier:** `grep -rn 'FrameDeliveryMode\|FrameResolutionMode\|coalesceDeliverSet\|buildCoalesceConflictResolver\|EnqueueCoalesceFrame\|LookupFrameResolutionMode\|TestEnqueueOrCoalesce_' lib/ cmd/ --include='*.go'` returns hits (other than retired-history files); OR the frame producer still has a mode-dispatch switch; OR `DeliverPendingMessages` returns more than one message per call; OR `go build ./... && go test ./...` fails; OR `lib/graph/frame/producer_test.go` is not the renamed `TestEnqueueFrame_*` suite (the prior `TestEnqueueOrCoalesce_CoalesceFirstInsert`, `TestEnqueueOrCoalesce_CoalesceAppendsSources`, and `TestEnqueueOrCoalesce_CoalesceDedupesSameSource` tests exercised retired coalesce behaviour and are dropped entirely; the single-path `TestEnqueueFrame_*` cases replace them).

**Load-bearing property: one message per frame.** `DeliverPendingMessages` must deliver *exactly one* pending message per invocation, never two. This is satisfied by construction here: the `ListPendingForInstance` SQL in both `lib/foundation/persistence/postgres/messages.go` and `.../sqlite/messages.go` carries `LIMIT 1` (named on the same constant comment in each backend) so the SELECT itself is the single-row gate; the Go-side `pending[0]` head pick is the cheap consumer of that gate. The cheaper shape "deliver everything pending and let downstream sort it out" must not be used. Verify with a test that loads ≥10 pending messages and confirms `DeliverPendingMessages` returns exactly one each call.

### Task 10: Remove `frame_resolution_mode` from template DSL

**Files:** `lib/foundation/spec/template.go`, `lib/graph/node/template.go` (the alias file), `lib/graph/node/template_validator.go`

**Steps:**

1. Remove the `FrameResolutionMode string` field from `TemplateSpec` in `lib/foundation/spec/template.go`. Remove the `FrameResolutionMode*` constants (`FrameResolutionSerialQueue`, `FrameResolutionCoalesce`).
2. Remove any alias re-exports in `lib/graph/node/template.go`.
3. In `lib/graph/node/template_validator.go`: remove the `Path: "frame_resolution_mode"` validation entries (currently around lines 399 and 405).
4. Run `grep -rn 'FrameResolutionMode\|FrameResolutionSerialQueue\|FrameResolutionCoalesce' lib/ cmd/ test/` and update each reference. Scenario tests that set `FrameResolutionMode: node.FrameResolutionSerialQueue` must drop the field (the field no longer exists). Tests using `FrameResolutionCoalesce` are exercising retired behavior — drop those tests entirely (e.g., `lib/graph/frame/producer_test.go::TestEnqueueOrCoalesce_CoalesceFirstInsert`, `TestEnqueueOrCoalesce_CoalesceAppendsSources`, `TestEnqueueOrCoalesce_CoalesceDedupesSameSource`).
5. Run `go build ./...` and resolve remaining references.

### Task 11: Simplify the frame producer to `EnqueueFrame`

**Files:** `lib/graph/frame/producer.go`, `lib/graph/frame/producer_test.go`, `lib/foundation/persistence/frames.go`, Postgres/SQLite accessors

**Steps:**

1. In `lib/graph/frame/producer.go`: replace `EnqueueOrCoalesce` with `EnqueueFrame(ctx, store, tx, instanceID, triggeringMessageID, frameTimeoutMs)`. No mode lookup, no switch. Calls `store.Frames().InsertFrame(...)` (rename the persistence method appropriately) which is a plain INSERT, not an upsert.
2. In `lib/foundation/persistence/frames.go`: remove `EnqueueCoalesceFrame` from the `FramesTable` interface. Rename `EnqueueSerialFrame` → `InsertFrame` (or `EnqueueFrame`) so the name no longer carries mode connotations. Remove `LookupFrameResolutionMode` from the interface.
3. In Postgres/SQLite accessor implementations: drop `EnqueueCoalesceFrame` and `LookupFrameResolutionMode` implementations. Simplify `InsertFrame` to a single INSERT (no upsert, no partial index handling).
4. Update `lib/graph/frame/producer_test.go`: drop coalesce-specific tests (mentioned in Task 10). Update `TestEnqueueOrCoalesce_SerialQueue` to `TestEnqueueFrame` exercising the new single path.
5. Find every caller of `frame.EnqueueOrCoalesce` and rename to `frame.EnqueueFrame`, passing the `triggering_message_id`. Callers to update:
   - `lib/runtime/cascade_invalidate.go:104, 214` — operator-API invalidate path
   - `lib/graph/scheduler/scheduler.go:169` — pure-cascade sweep
   - `lib/runtime/runner_terminal.go:732, 748` — the FrameNext branch (which goes away in Pass 3; for this pass, just rename so the build passes)
6. Run `go build ./...` and `go test ./lib/graph/frame/... ./lib/foundation/persistence/...` and confirm.

### Task 12: Remove `FrameDeliveryMode` runtime type and coalesce conflict resolver

**Files:** `lib/runtime/message_delivery.go`

**Steps:**

1. Delete the `FrameDeliveryMode` type (line 88), its constants (`FrameDeliveryCoalesce`, `FrameDeliverySerialQueue`).
2. Delete `coalesceDeliverSet` (line 567) and `coalesceConflictResolver`/`buildCoalesceConflictResolver` (lines 425, 434).
3. Simplify `DeliverPendingMessages` (line 504): it always delivers exactly the oldest one pending message — `deliverSet = live[:1]`. Remove the `mode` parameter from the signature.
4. Simplify `deliverForRunningFrame` (line 191): no mode lookup, no resolver build. Just call `DeliverPendingMessages` with the new shorter signature.
5. The `@blessed-invariant: no silent override loss under coalesce` annotation block (line 503) goes away with `coalesceDeliverSet`.
6. Run `go build ./lib/runtime/...` and confirm.

### Task 13: Wire `triggering_message_id` through the frame-creation paths

**Files:** `lib/runtime/message_delivery.go`, `lib/runtime/cascade_invalidate.go`, `lib/graph/scheduler/scheduler.go`

**Steps:**

1. In `lib/runtime/message_delivery.go`: when a frame opens to deliver a message, the new frame must be created via `frame.EnqueueFrame(..., triggeringMessageID, ...)`. Today the `SweepDeliverMessagesForRunningFrames` flow assumes a frame already exists (created elsewhere) and just stamps the existing frame_id. Look at the call site closely — there's likely a frame-creation point upstream where the `triggering_message_id` needs to thread.
2. In `lib/runtime/cascade_invalidate.go`: the operator-API invalidate path enqueues a frame. Under the new model, an operator who wants to invalidate sends a message; that message is the triggering message for the frame. Pass the message ID from the request through to `EnqueueFrame`.
3. In `lib/graph/scheduler/scheduler.go:169`: the pure-cascade sweep enqueues frames. Identify what message triggers these; if none, this path is wrong under the new model. (Pass 3 deletes the cascade walker's enqueue branch; the pure-cascade sweep may need similar attention — investigate during this task and resolve.)
4. The slog `frame.start` log line at `lib/graph/frame/engine.go:218` gains a `triggering_message_id` field.
5. Run `go build ./...` and `go test ./lib/runtime/... ./lib/graph/...` and confirm.

### Task 14: Create the cascade-graph frames-read endpoint surfacing `triggering_message_id`

**Files:** `lib/control/controlapi/frames.go` (new), `lib/control/controlapi/frames_test.go` (new), `lib/control/controlapi/app.go` (route registration), `lib/control/controlapi/actions.go` (action registration), `lib/foundation/persistence/postgres/frames.go` and `lib/foundation/persistence/sqlite/frames.go` (extended accessors)

**Steps:**

1. There is no existing frames-read control-API endpoint to extend; this task creates one. Read `lib/control/controlapi/instances.go` for the existing endpoint-handler pattern (`actions.go::registeredActions` entries + handler function + request/response structs + route registration in `app.go`). The only existing frame-touching route is `GET /admin/diagnostics/held-frames` in `lib/control/controlapi/admin_diagnostics.go:68` — different concern (held frames diagnostic), but useful as a structural cousin for read-side queries.
2. Create `lib/control/controlapi/frames.go` with two endpoints:
   - `GET /instances/{id}/frames` — list frames for an instance. Query params: `triggering_message_id=<id>` filter (reverse query — message → frames it triggered), `limit`, `cursor`. Response: JSON list of frame items, each carrying `frame_id`, `state`, `triggering_message_id`, `started_at`, `ended_at`, `last_progress_at`, and the joined message envelope (`type`, `sender`, `sender_kind`).
   - `GET /instances/{id}/frames/{frame_id}` — fetch one frame with the joined message envelope. Forward query (frame → message).
3. Add the corresponding entries to `lib/control/controlapi/actions.go::registeredActions` (e.g., `{Action: "instance:frame:read", IsWrite: false}` and `{Action: "instance:frames:list", IsWrite: false}` — match the existing pattern of `instance:*` 2-segment actions, and confirm against the action grammar before committing the segment count).
4. Add the persistence accessor methods on `FramesTable`: `ListForInstance(ctx, instanceID, filter, pagination, tx)` and `GetWithTriggeringMessage(ctx, frameID, tx)`. The list query supports the `triggering_message_id` filter via a `WHERE` clause. Implement in `lib/foundation/persistence/postgres/frames.go` and `lib/foundation/persistence/sqlite/frames.go`.
5. Register the routes in `lib/control/controlapi/app.go` (look for existing `app.Get` / chi router registrations and follow the pattern).
6. Create `lib/control/controlapi/frames_test.go` with handler-level tests: list returns all frames for an instance; filter by `triggering_message_id` returns only matching frames (the reverse query); single-frame fetch returns the joined message envelope.
7. Run `go build ./... && go test ./lib/control/controlapi/... ./lib/foundation/persistence/postgres/... ./lib/foundation/persistence/sqlite/...` and confirm.

### Task 15: Verify Pass 2 build + one-message-per-frame property

**Files:** none modified.

**Steps:**

1. Run `go build ./...` and confirm clean build.
2. Run `go test ./lib/runtime/... ./lib/graph/frame/... ./lib/foundation/persistence/postgres/... ./lib/foundation/persistence/sqlite/... -count=1` and confirm passing.
3. **One-message-per-frame verification:** confirm the existing test `lib/runtime/message_delivery_test.go` exercises the property "with N pending messages, deliver returns exactly one." If it doesn't, add a focused test that inserts 10 pending messages on an instance and asserts `DeliverPendingMessages` returns exactly one per call (the cheaper shape "return all pending" is what the validator argues against). If the test exists but with the old `serial_queue` mode parameter, simplify it to the new no-mode signature.
4. Run `make lint` and confirm clean.

---

## Pass 3: Remove `frame:` modifier from `subscribes:` entries

**Goal:** Remove the per-subscription `frame: in | next` modifier from the DSL and the runtime. Cross-frame coupling no longer rides on subscriptions; later passes wire it through the new emit-node mechanism instead. The cascade walker has one path (in-tx, in-frame).

**Scope:** Tasks 16–18

**Falsifier:** `grep -rn 'FrameIn\|FrameNext\|node.FrameIn\|node.FrameNext' lib/ cmd/ test/ --include='*.go'` returns hits (other than retired-history files); OR `lib/runtime/runner_terminal.go` still contains a switch arm or branch keyed on a per-subscription `frame:` modifier (the prior `case node.FrameNext:` arm collapsed into the single in-tx in-frame walker; reappearance under any name fails the falsifier); OR `lib/graph/node/template_validator.go` still validates a `frame:` field on `subscribes:` entries; OR existing scenario tests that set `Frame:` on a `SubscriptionEntry` were not removed/updated.

### Task 16: Drop `FrameIn` / `FrameNext` constants and the `Frame` field

**Files:** `lib/foundation/spec/template.go`, `lib/graph/node/template.go`, `lib/graph/node/template_validator.go`

**Steps:**

1. Remove the `Frame string` (or whatever type) field from `SubscriptionEntry` in `lib/foundation/spec/template.go`.
2. Remove the `FrameIn` and `FrameNext` constants. Remove any aliases in `lib/graph/node/template.go` (currently at lines 86–87).
3. In `lib/graph/node/template_validator.go`: remove the validation block (currently around line 681) that checks `case "", FrameIn, FrameNext` on the Frame field, including the error message.
4. Run `grep -rn 'FrameIn\|FrameNext' lib/ cmd/ test/ --include='*.go'` and resolve every remaining reference. Test templates that used `Frame: node.FrameNext` must drop the field.
5. Run `go build ./...`. The build will break in `runner_terminal.go` (the next task fixes it).

### Task 17: Collapse the cascade walker to a single in-frame path

**Files:** `lib/runtime/runner_terminal.go`

**Steps:**

1. Find the `switch edge.Frame` block at `runner_terminal.go:731`.
2. Delete the entire `case node.FrameNext:` arm (lines 732–766). This includes the call to `frame.EnqueueFrame` (post-rename), the parked-receiver wake call (`wakeParkedReceiverInTx`), and the `continue` statement.
3. Remove the `switch` wrapper entirely — what's left is the `FrameIn / default` body. Flatten it: the cascade walker now has one path, the in-tx in-frame walk.
4. Update the `runner_terminal_test.go` siblings to drop or rewrite any tests that exercised the `FrameNext` arm (look for `frame: next` / `node.FrameNext` literals).
5. The settled-this-frame guard at `runner_terminal.go:868` (the self-edge bypass) survives unchanged — it's still load-bearing for the standard in-frame cascade self-edge case.
6. Run `go build ./...` and `go test ./lib/runtime/...` and confirm.

### Task 18: Verify the cascade walker has exactly one frame-creation entry point left

**Files:** none modified.

**Steps:**

1. Run `grep -rn 'frame\.EnqueueFrame\|EnqueueOrCoalesce' lib/runtime/ lib/graph/` and confirm the only call sites are the message-delivery path and the operator-API invalidate path. The cascade walker (`runner_terminal.go`) should NOT have a call. (This is the load-bearing single-frame-creation-path property.)
2. Run `grep -rn 'EnqueueOrCoalesce' lib/ cmd/ test/ --include='*.go'` and confirm zero hits — the old name should be gone after Pass 2.
3. Run `make lint` and confirm clean.

---

## Pass 4: Message-virtual-node settle (replace `subscribes:[message]` topic kind)

**Goal:** Atomically remove the `message/<kind>/<sender_kind>/<target>` signal type-path and the by-envelope-fields subscription walk, AND add the new path: a message arrival is a virtual node-type settling with `terminal/success`, the standard cascade walker drains wait-set rows for subscribers. Tree stays working: receivers still wake on message arrival, just through a different mechanism.

**Scope:** Tasks 19–22

**Falsifier:** the `message/*` type-path remains in `concept:signal`'s canonical taxonomy validator (`lib/foundation/signal/taxonomy.go`); OR `lib/runtime/message_delivery.go` still defines a by-envelope-fields subscription walker (the prior `cascadeMessageSubscribersInTx` is replaced by `cascadeMessageVirtualNodeSettleInTx`, which walks the standard subscription-edge map by the message's `type` as the virtual-node sender key; reappearance of the by-envelope-fields shape under any name fails the falsifier); OR an existing scenario test that subscribed to a message kind (e.g., `type: message/invalidate/operator/self`) still passes (it must fail through the unknown-type-path validator); OR a delivered message does not stale-mark its declared subscribers in the new frame; OR the wait-set `topic_kind` enum still admits `message` as a discriminator value.

**Load-bearing property: message arrival fires subscribers in the new frame.** The replacement path must actually wake receivers — the cheaper shape "remove the old path and leave it broken" must not be used. A scenario test that subscribes to a message-type as a virtual node and confirms the receiver runs after the message is delivered exercises this end-to-end.

### Task 19: Remove `message/*` from the signal taxonomy

**Files:** `lib/foundation/signal/taxonomy.go`

**Steps:**

1. Find the canonical emit-shape list (around line 20) and remove the `message/*` entry. The four surviving canonical kinds are `terminal/*`, `transient/*`, `attribute/*/changed`, `event/*`.
2. Find the `ValidateSubscriptionType` function and update it accordingly — any subscription with `type: message/*` is no longer in the taxonomy, so the existing canonical-type validator (which rejects anything not in the taxonomy) handles the rejection. No new rule needed; the rejection happens through the normal validator path.
3. Find the `matchesCanonical()` function (if it has any literal `message` handling) and remove.
4. Update tests in `lib/foundation/signal/taxonomy_test.go` (or equivalent) — tests that asserted `message/<kind>/<sender_kind>/<target>` validates must be removed.
5. Run `go build ./lib/foundation/signal/... && go test ./lib/foundation/signal/...` and confirm.

### Task 20: Remove `message` from wait-set `topic_kind`

**Files:** `lib/runtime/runner_terminal.go::waitSetTopicKindFor` (line ~1307), and the wait-set persistence layer

**Steps:**

1. In `lib/runtime/runner_terminal.go::waitSetTopicKindFor`: remove the case that maps `message/*` type-paths to a `message` `topic_kind` value.
2. In `lib/foundation/persistence/postgres/wait_set.go` (and SQLite equivalent): remove any code that treats `message` as a valid `topic_kind` discriminator value. The `state` defensive fallback survives.
3. Update `lib/foundation/persistence/postgres/migrations/...` only if there's an enum constraint or CHECK on `topic_kind`; if so, write a migration that drops `'message'` from the allowed set, OR add it to the Pass 1 migration. (If it's a free-form string column with no constraint, no migration is needed.)
4. Run `go build ./... && go test ./lib/foundation/persistence/postgres/... -count=1` and confirm.

### Task 21: Delete `cascadeMessageSubscribersInTx` and wire message-virtual-node settle

**Files:** `lib/runtime/message_delivery.go`

**Steps:**

1. Delete `cascadeMessageSubscribersInTx` (line 298). Delete the helpers it uses that no other code references (`messagePayloadAsMap` — verify with grep before deleting).
2. In `deliverForRunningFrame` (line 191), after the `MarkDelivered` call, replace the existing call to `cascadeMessageSubscribersInTx` with a new mechanism: for each delivered message, emit a `terminal/success` signal whose `sender_node_type` is the message's `type` (the virtual node-type) and whose payload is the message body (the existing `payload` bytes, unmarshaled to a map for CEL evaluation via the existing `messagePayloadAsMap` helper — keep it if cascade-walker reads payloads, or inline).
3. The cascade walker (`runner_terminal.go::cascadeSubscribersStaleInTx`) receives this signal and walks the subscription edges. Subscribers with `node: <message-type>, type: terminal/success` match and get stale-marked in the new frame. No new code path; the existing walker handles it.
4. Validate: each delivered message produces one `terminal/success` signal for the virtual-node-type. The existing audit emission at line 250 (`signalaudit.EmitSignal`) writes the audit row.
5. Run `go build ./... && go test ./lib/runtime/...` and confirm.

### Task 22: Add the subscription-edge map registration for message-virtual-node-types

**Files:** `lib/graph/node/template_validator.go`, `lib/graph/node/subscription_edges.go` (or wherever the inverse-edge map is built — check `lib/graph/node/`)

**Steps:**

1. The subscription-edge map is built from the template at registration time. When the validator builds the map, every `subscribes:` entry of the form `node: <X>, type: terminal/success` indexes under sender node-type `X` — including when `X` is a message-type from the `messages:` registry (added in Pass 5). For Pass 4, the validator must accept `node:` values that are *either* a declared node-type OR a declared message-type-from-`messages:`-block. (Pass 5 adds the `messages:` block; for Pass 4, leave the validator open to unknown `node:` values that look like type-paths — Pass 5 tightens it.)
2. Update `lib/graph/node/template_validator.go` to allow `subscribes:` entries with `node: <message-type-path>` (e.g., `node: ping/recheck`). The validator currently rejects unknown node-type names; loosen the rule to "accept any `node:` value whose syntactic shape is a type-path."
3. Run `go build ./... && go test ./lib/graph/node/... -count=1` and confirm.

---

## Pass 5: `messages:` template-level registry

**Goal:** Add the `messages:` block to the template DSL. Each entry declares an accepted message type and its body shape. Registration-time validation rejects unknown types in substitution refs; receipt-time lookup rejects unknown types at `POST /instances/{id}/messages`.

**Scope:** Tasks 23–26

**Falsifier:** `TemplateSpec` does not have a `Messages` field; OR the validator accepts a template that declares two `messages:` entries with the same `type:`; OR `POST /instances/{id}/messages` with a `type:` not declared in the registry returns 2xx (must return 400 with the declared set); OR Pass 4's loosened `subscribes:` rule does not tighten to require `node:` values to be a declared message-type (when of message-type shape) or a declared node-type. (The `{{messages.<type>.<field>}}` substitution-ref validation is Pass 6's responsibility — see Pass 6's Falsifier.)

**Load-bearing property: unknown message types are refused loudly at receipt.** The cheaper shape "accept and silently dead-letter" is what the spec's `STORY-message-schema` falsifier argues against. The validator at the message-emit handler must perform the lookup before persisting; an unknown type returns 400 with both the unknown type name and the declared set in the response body.

### Task 23: Add `Messages` field to `TemplateSpec` and the `MessageSchema` struct

**Files:** `lib/foundation/spec/template.go`, `lib/graph/node/template.go`

**Steps:**

1. In `lib/foundation/spec/template.go`: add `Messages []MessageSchema` field on `TemplateSpec`. Define the struct:

```go
type MessageSchema struct {
    Type       string          `json:"type" yaml:"type"`
    BodySchema json.RawMessage `json:"body_schema" yaml:"body_schema"` // JSON Schema bytes
}
```

2. Re-export the type via `lib/graph/node/template.go` if that file's pattern uses aliases.
3. Update the canonical-JCS hash function (`lib/graph/template/canonical/jcs.go`) if the new field is part of the template's content-addressed identity (it should be — message-type declarations are template-level).
4. Run `go build ./...` and confirm.

### Task 24: Registration-time validation of `messages:` entries

**Files:** `lib/graph/node/template_validator.go`

**Steps:**

1. Add a validator pass that walks `tmpl.Messages` and checks:
   - Each `Type` is non-empty and matches the type-path grammar (slash-separated, validator-enforced — reuse the canonical-type-path validator from `concept:signal`).
   - Each `Type` is unique across entries (no duplicates).
   - Each `BodySchema` parses as a valid JSON Schema (use the existing JSON-schema dependency the project uses for `attributes:` validation).
2. Cross-block validation:
   - Every `{{messages.<type>.<field>}}` substitution ref in any node's `attributes:` block resolves against a declared message type's `body_schema` (the `<field>` is one of the schema's named properties). Defer the substitution-ref extraction and validation to Pass 6 (it lands together with the substitution-engine extension).
   - Every `subscribes:` entry with `node: <message-type-shaped-value>` resolves against a declared `messages:` entry's `type:`. Update the loosened rule from Task 22 to tighten here: reject `node:` values that look like message-type-paths but aren't declared.
3. Add tests in `lib/graph/node/template_validator_test.go` covering each rule.
4. Run `go build ./... && go test ./lib/graph/node/...` and confirm.

### Task 25: Receipt-time registry lookup at message-emit endpoint

**Files:** `lib/control/controlapi/messages.go`

**Steps:**

1. In the `handlePostInstanceMessages` handler: before calling `EnqueueMessage`, look up the requested `type` against the target instance's template's `messages:` registry.
2. Path: fetch the instance, fetch its template by hash, walk `template.Messages` for a matching `Type`. If no match, return `HTTP 400` with a body shaped:

```json
{
  "error": "unknown message type",
  "type": "<the rejected type>",
  "declared_types": ["<type1>", "<type2>", ...]
}
```

3. The lookup is read-only and runs before idempotency check (so a 400 doesn't pollute the idempotency ledger).
4. Update tests in `lib/control/controlapi/messages_test.go` to cover both legs: declared type accepted, undeclared type refused with 400.
5. Run `go build ./... && go test ./lib/control/controlapi/...` and confirm.

### Task 26: Verify the registry path

**Files:** none modified.

**Steps:**

1. Run `go build ./... && go test ./...` and confirm clean.
2. Run `make lint` and confirm.

---

## Pass 6: Substitution grammar `{{messages.<type>.<field>}}`

**Goal:** Extend the attribute-substitution engine so that `{{messages.<type>.<field>}}` is recognized as a substitution source, resolves against the triggering message body, and is validated at template registration against the declared `messages:` schema. The auto-subscribe rule extends: a node whose attribute schema references `{{messages.<type>.<field>}}` implicitly subscribes to that message-virtual-node-type.

**Scope:** Tasks 27–29

**Falsifier:** the substitution engine does not recognize `{{messages.<type>.<field>}}` (resolution returns empty / errors with "unknown source"); OR registration accepts a `{{messages.<type>.<field>}}` ref where `<type>` is not in `messages:` or `<field>` is not in the body schema; OR a node referencing `{{messages.X.Y}}` does not implicitly subscribe to message-virtual-node X's `terminal/success`; OR the runtime substitutes `{{messages.X.Y}}` and `{{nodes.X.attribute.Y}}` through two different resolver functions.

**Load-bearing property: one substitution engine, two surfaces.** The same resolver function services both `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}`. The cheaper shape "fork a parallel resolver for messages" is what `STORY-typed-message-substitution` falsifier argues against. Verify with a code-path assertion test in `lib/graph/attribute/substitution_test.go` that the same function is invoked.

### Task 27: Extend `ResolveContext` and the substitution resolver

**Files:** `lib/graph/attribute/substitution.go`, `lib/runtime/substitution_context.go`

**Steps:**

1. In `lib/graph/attribute/substitution.go`: extend `ResolveContext` with a new field `TriggerMessageType string` (the type-path of the message that opened the frame, e.g., `ping/recheck`). The existing `TriggerMessagePayload json.RawMessage` at line 110 stays as the body bytes; the new `TriggerMessageType` is the discriminator the resolver uses to match the receiver's `{{messages.<type>.<field>}}` directive.
2. Extend the resolver to recognize the new directive shape. Look at the existing source-kind handler (around line 11–16 in the enumeration). Add `{{messages.<type>.<field>}}` as a sixth kind. The resolver:
   - Parses the directive into `(messageType, fieldPath)`.
   - Asserts that `ctx.TriggerMessageType == messageType`; if not (the receiver is reading from a message type that isn't the frame's trigger), return an error at dispatch time (the validator catches the more common static case at registration via the auto-subscribe rule — see Task 28; this dispatch-time check covers dynamic edge cases).
   - Walks the triggering message's body via the existing `walkPath` helper (the same one used for `{{trigger.message.payload.X}}`-style reads at line 526 — preserves inertness per `@blessed-invariant: 21`).
3. In `lib/runtime/substitution_context.go`: when building `ResolveContext` for a dispatch, populate `TriggerMessageType` from the frame's `triggering_message_id`-joined envelope's `type` field. Same SQL join that populates `TriggerMessagePayload` today.
4. Run `go build ./... && go test ./lib/graph/attribute/... -count=1` and confirm.

### Task 28: Registration-time validation of `{{messages.<type>.<field>}}` refs

**Files:** `lib/graph/node/template_validator.go`

**Steps:**

1. Add a pass that extracts every `{{messages.<type>.<field>}}` reference from every node's `attributes:` schema sources.
2. For each ref: confirm `<type>` is declared in `template.Messages`; confirm `<field>` is a named property in that message-type's `body_schema`. Reject with a clear error message if either fails.
3. Extend the auto-subscribe rule (currently in `concept:node-subscription` for `{{nodes.X.attribute.Y}}` and `{{nodes.X.event.Y}}`): a node with `{{messages.<type>.<field>}}` in its attribute schema implicitly gets a `subscribes:` entry `{ node: <type>, type: terminal/success }` (the message-virtual-node's settle signal). Apply this implicit subscription in the subscription-edge map builder.
4. Add tests covering: typo'd `<type>` rejects; typo'd `<field>` rejects; implicit subscription appears in the subscription-edge map.
5. Run `go build ./... && go test ./lib/graph/node/... -count=1` and confirm.

### Task 29: Code-path assertion test for shared substitution

**Files:** `lib/graph/attribute/substitution_test.go` (or new test file)

**Steps:**

1. Add a focused test that confirms both `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}` flow through the same `resolveDirectiveValueRaw` (or whatever the single resolver function is) — assert via a synthetic resolver substitution or a code-coverage trace. (If the function is too deep to assert by name, use a structural test: invoke the resolver with both directive shapes and confirm both succeed against a shared `ResolveContext`.)
2. Add a property test: a typo'd field in either directive shape rejects at registration via the same validator path.
3. Run `go test ./lib/graph/attribute/... -count=1` and confirm.

---

## Pass 7: Message-emitter node-kind (`emits_message:`)

**Goal:** Add the new node dispatch mode. A node declares `emits_message: <type>` instead of `executor:` or `delegate:`. Its `attributes:` block exactly matches the destination message type's `body_schema`. At terminal-resolution, the runtime constructs the envelope (sender_kind=instance, idempotency-key from node_run_id, body=resolved attribute set) and inserts into the message ledger inside the same tx as the node's terminal.

**Scope:** Tasks 30–33

**Falsifier:** `TemplateNodeDef` has no `EmitsMessage` field; OR `executor:`, `delegate:`, and `emits_message:` are not mutually exclusive (the validator accepts a node declaring two of them); OR a message-emitter node's `attributes:` schema can declare fields the destination message's `body_schema` doesn't, without registration-time error; OR the dispatch path produces no message envelope in the ledger when the emit-node settles; OR the envelope's `Idempotency-Key` is not deterministic on `node_run_id` (a tx retry produces a second ledger row); OR the envelope insert is not in the same tx as the terminal (a rollback after the insert leaves the envelope in the ledger).

**Load-bearing properties:**
- **Envelope insert is atomic with the sender's terminal-resolution tx.** Test: induce a forced tx-rollback after the emit-node's body composition; assert no envelope in `rimsky_messages`.
- **Idempotency on cascade-emit is deterministic on `node_run_id`.** Test: invoke the terminal-resolution path twice with the same `node_run_id`; assert one and only one envelope in the ledger.

### Task 30: Add `EmitsMessage` field to `TemplateNodeDef`

**Files:** `lib/foundation/spec/template.go`, `lib/graph/node/template.go`

**Steps:**

1. Add `EmitsMessage string` field on `TemplateNodeDef` with JSON tag `emits_message`.
2. Re-export via `lib/graph/node/template.go` if the aliasing pattern requires it.
3. Update the canonical-JCS hash function to include the new field.
4. Run `go build ./...` and confirm.

### Task 31: Validator — mutual exclusion + body shape match

**Files:** `lib/graph/node/template_validator.go`

**Steps:**

1. Add a node-level validator pass:
   - Exactly one of `Executor`, `Delegate`, `EmitsMessage`, fan-out's partition-request shape (if it's distinct) must be set. Reject if zero or two are set.
   - If `EmitsMessage` is set, lookup the named message type in `tmpl.Messages`. Reject if the type doesn't exist in the registry.
   - If `EmitsMessage` is set, the node's `attributes:` block must declare exactly the same field-set as the destination message type's `body_schema`. Same field names, same types. Superset is rejected. (Use the existing JSON Schema comparison utility, or write a focused comparator — the structural-equality predicate is: same set of `properties` keys, each with the same `type`, plus the same `required` set.)
2. Add tests in `lib/graph/node/template_validator_test.go` covering: mutual exclusion violation rejects; unknown emit message-type rejects; superset attribute schema rejects; exact-match attribute schema accepts.
3. Run `go build ./... && go test ./lib/graph/node/... -count=1` and confirm.

### Task 32: Dispatch path — runtime construction of the emit envelope

**Files:** `lib/runtime/runner_dispatch.go`, `lib/runtime/runner_terminal.go`, possibly a new `lib/runtime/runner_emit_message.go`

**Steps:**

1. In the dispatch path: when a node's dispatch mode is `emits_message`, the runtime does NOT invoke an executor. Instead, the runtime resolves the node's attributes through the standard attribute-substitution path (the same one that resolves attributes for `executor:` dispatches), then immediately transitions the node-run to terminal/success with the resolved attribute set as the body.
2. The terminal-resolution path (or a sibling, in `lib/runtime/runner_emit_message.go`): inside the same tx that commits the node's terminal, construct the message envelope:
   - `Type`: the node's `EmitsMessage` value.
   - `SenderKind`: `"instance"`.
   - `Sender`: `instance:<instance_id>`.
   - `InstanceID`: the running instance.
   - `Payload`: the serialized attribute set (JSON-marshal the resolved attributes).
   - `Idempotency-Key`: derive deterministically from the node-run's `ID` (e.g., `cascade-emit:<node_run_id>` so the dedup tuple `(instance_id, sender_kind, sender, sender_subject, idempotency_key)` collapses on retry).
3. Insert the envelope through the same `EnqueueMessage` helper that operator-API and publisher emits use. The insert IS in the sender's tx — if the tx rolls back, the envelope rolls back.
4. After the envelope inserts and the node's terminal commits, the next-frame-boundary sweep (Pass 2's `SweepDeliverMessagesForRunningFrames`) picks up the new envelope and opens the next frame.
5. Update tests: a focused unit test that exercises the dispatch path with `emits_message` and asserts the envelope lands in the ledger atomically.
6. Run `go build ./... && go test ./lib/runtime/... -count=1` and confirm.

### Task 33: Verify Pass 7 — atomicity and idempotency

**Files:** `lib/runtime/runner_emit_message_test.go` (or wherever Task 32's tests live)

**Steps:**

1. Add a test that forces a tx-rollback after the envelope insert but before the terminal commit; assert the `rimsky_messages` ledger has zero rows for that emit.
2. Add a test that invokes the dispatch path twice with the same `node_run_id`; assert exactly one envelope in the ledger (idempotency dedup works).
3. Run `go test ./lib/runtime/... -count=1 -race` (use `-race` to catch any concurrency issues with the in-tx envelope insert).
4. Run `make lint` and confirm.

---

## Pass 8: Backfill collapse

**Goal:** Remove the entire backfill subsystem — control-API endpoints, CLI subcommands, lineage chain key, target-validity checks. Backfill is now a use case of the general typed-message machinery, requiring no special code.

**Scope:** Tasks 34–36

**Falsifier:** `lib/control/controlapi/backfills.go` still exists; OR `cmd/rimsky/cli/backfill.go` still exists; OR the `concept:backfill` documentation directory still has the file; OR there's a `backfill_operation_id` column reference left in code (Pass 1 removed the column; this pass removes the readers/writers); OR a test in `test/scenarios/backfill/` still exists.

### Task 34: Delete control-API backfill endpoints

**Files:** `lib/control/controlapi/backfills.go` (delete), `lib/control/controlapi/backfills_test.go` (delete), `lib/control/controlapi/app.go` (remove route registration)

**Steps:**

1. Delete `lib/control/controlapi/backfills.go` and `backfills_test.go`.
2. In `lib/control/controlapi/app.go`: remove the route registrations for the five backfill endpoints. Look for the handler-method registrations and delete them.
3. Run `grep -rn 'backfill\|Backfill' lib/control/controlapi/ --include='*.go'` and resolve any remaining references.
4. Run `go build ./lib/control/...` and confirm.

### Task 35: Delete CLI backfill subcommands

**Files:** `cmd/rimsky/cli/backfill.go` (delete), `cmd/rimsky/cli/backfill_test.go` (delete), `cmd/rimsky/main.go` (modify), `cmd/rimsky/cli/client.go` (modify)

**Steps:**

1. Delete `cmd/rimsky/cli/backfill.go` and `cmd/rimsky/cli/backfill_test.go` entirely (these contain the `RunBackfillCreate` / `List` / `Show` / `Cancel` subcommand handlers).
2. In `cmd/rimsky/main.go`: remove the `case "backfill":` dispatcher arm (look around lines 49, 262, 277, 280, 382 for backfill routing). Remove the backfill help/usage line in the help printer.
3. In `cmd/rimsky/cli/client.go` (around lines 901, 940, 986–1085): remove the backfill request structs (`BackfillItem`, the create/list/get/partitions/cancel structs) and the client methods that call the backfill endpoints. Be careful to leave non-backfill types in place.
4. Run `grep -rn 'backfill\|Backfill' cmd/ --include='*.go'` and resolve any remaining references.
5. Run `go build ./cmd/... && go test ./cmd/... -count=1` and confirm.

### Task 36: Remove scenario tests and demos that exercise backfill

**Files:** `test/scenarios/backfill/` (delete directory), `test/scenarios/backfill_partition_override_fullstack_test.go` (delete), `test/scenarios/backfill_ops_lifecycle_e2e_test.go` (delete), any other backfill-named files surfaced by grep

**Steps:**

1. Delete the `test/scenarios/backfill/` directory and all contents.
2. Delete the sibling backfill scenario files at the parent level: `test/scenarios/backfill_partition_override_fullstack_test.go` and `test/scenarios/backfill_ops_lifecycle_e2e_test.go`.
3. Run `grep -rln 'backfill\|Backfill' test/scenarios/ examples/ --include='*.go' --include='*.sh' --include='*.yaml'` and review every remaining file. Delete files dedicated to backfill flows; in mixed-purpose files, strip backfill-only references.
4. Run `go build ./... && go test ./test/scenarios/... -count=1` and confirm.

---

## Pass 9: Debug channel endpoint

**Goal:** Add the new control-API endpoint `POST /instances/{id}/debug/override` for ad-hoc operator overrides. Gated to instances that are paused or holding a pause-mode breakpoint hit. New permission scope, new audit event kind.

**Scope:** Tasks 37–39

**Falsifier:** the route does not exist (`curl POST /instances/{id}/debug/override` returns 404); OR the gate does not enforce paused-or-breakpoint (a healthy instance accepts the override); OR a key without the new permission scope can call the endpoint; OR the audit log has no `debug.override.applied` row after a successful override; OR the `invalidate_node` action does not stale-mark a node; OR the `set_attribute` action does not write the attribute.

**Load-bearing property: the gate is enforced inside the request tx.** The cheaper shape "check the gate before the tx, then apply" admits TOCTOU — between gate-check and apply, an external pause-toggle changes state. The check + mutation must share the tx. Verify with a test that interleaves a `paused = false` toggle between gate-check and apply; the override either fully applies or fully rejects, never half.

### Task 37: Define the new permission action and the audit event kind

**Files:** `lib/control/controlapi/actions.go`, `lib/protocols/proto/v1/events.proto`

**Steps:**

1. In `lib/control/controlapi/actions.go::registeredActions` (the slice of `Action` entries currently at lines 293–446): add a new entry `{Action: "instance:debug-override", IsWrite: true}` (2-segment `<noun>:<verb>` form matching the prevailing convention; the spec's working name `instance:debug:override` had three segments — this plan resolves it to two for consistency). Match the syntactic shape and placement of similar `instance:*` write actions in the slice.
2. Add a new `OperationalKind` enum value `OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED` to `lib/protocols/proto/v1/events.proto`. The highest currently assigned value is `OPERATIONAL_KIND_PARKED_RESUME_STARTED = 81`; assign 82 (skip 81 if it's reserved next-up; check the proto file for any reservations).
3. Run `make proto-gen` to regenerate Go bindings.
4. Run `go build ./...` and confirm.

### Task 38: Add the route, handler, and gate enforcement

**Files:** `lib/control/controlapi/debug_override.go` (new), `lib/control/controlapi/app.go` (route registration), test sibling

**Steps:**

1. Create `lib/control/controlapi/debug_override.go` with:
   - `DebugOverrideRequest` struct with fields `Action` (enum `invalidate_node` / `set_attribute`), `NodeType`, `AttributeKey`, `AttributeValue`.
   - `handleDebugOverride` function that:
     - Opens a tx.
     - **Inside the same tx**, reads the instance row and checks `paused = TRUE` OR a pause-mode breakpoint hit row exists blocking a runner in the running frame. The check and the mutation that follows share the tx — between gate-read and mutation, no external write can change the gate-state (load-bearing TOCTOU resistance). If neither condition holds, return `HTTP 409` with body `{"error": "instance not in debuggable state", "states": ["paused", "breakpoint"]}`.
     - If gate passes (still inside the same tx):
       - For `invalidate_node`: find every node-run of `node_type` in the running frame and transition state to `stale`.
       - For `set_attribute`: write the attribute value to the attribute ledger for the named node-type's run, then stale-mark.
     - Emit a `debug.override.applied` audit event (the new `OPERATIONAL_KIND_DEBUG_OVERRIDE_APPLIED` from Task 37) with the action, node_type, attribute_key, attribute_value, and the gate-state that authorized it.
     - Commit the tx.
   - Permission check at handler entry: requires the `instance:debug-override` action authorized.
2. In `lib/control/controlapi/app.go`: register the route `POST /instances/{id}/debug/override` → `handleDebugOverride`.
3. Add tests covering: paused state accepts; breakpoint state accepts; healthy state refuses with 409; missing permission scope refuses with 403; both actions produce the expected mutation; audit event row is written.
4. Run `go build ./... && go test ./lib/control/controlapi/...` and confirm.

### Task 39: Verify TOCTOU resistance

**Files:** `lib/control/controlapi/debug_override_test.go`

**Steps:**

1. Add a test that simulates an interleaved `paused = false` write between the gate-check and the mutation. The test should assert atomicity: either the entire override applies (the read at gate-check time wins) or it fully rejects (the interleaved write wins). Never a partial state.
2. Run `go test ./lib/control/controlapi/... -count=1 -race` and confirm.
3. Run `make lint` and confirm.

---

## Pass 10: Design-doc mutations

**Goal:** Apply every entry in the spec's `## Design changes` section to `.ok-planner/design/`. Mutate concept, story, and decision files in place per the spec's directives. Move retired concepts and stories to `_retired/`. Move resolved tensions to `_resolved/` with resolution blocks. Create new concept, story, and decision files with the content the spec dictates. Regenerate `.ok-planner/design/concepts.md`, `stories.md`, `decisions.md` TOCs.

**Scope:** Tasks 40–46

**Falsifier:** the `concepts.md` TOC has entries pointing at files that no longer exist (the retired ones), OR is missing the new entries (message-schema, message-emitter-node); OR `concepts/invalidate.md` still lives at its original path (must be at `concepts/_retired/invalidate.md`); OR `stories/_retired/` does not exist and `stories/backfill-ops.md` still lives at its original path; OR any new artifact body cites a file path or external doc (violates self-containment); OR any artifact body contains a `## Notes` / `## History` / `## Changelog` section or backward-looking / forward-looking phrasing (violates current-state-only rule); OR a tension's `## Resolution candidates` section was not converted to a resolution block.

### Task 40: Mutate concept files (7 files)

**Files:** `.ok-planner/design/concepts/message.md`, `frame.md`, `cascade.md`, `node-subscription.md`, `signal.md`, `publisher-subscription.md`, `cascade-graph.md`

**Steps:**

For each concept, follow the spec's `## Design changes` → `### Concept changes` → "Mutate `concepts/<file>.md`" directives exactly. The replacement text in each directive is path-free; copy verbatim. Specifically:

1. **`concepts/message.md`**: replace Definition, Boundaries, Invariants sections per the spec's directive. Drop any `kind` references in favor of `type`. Drop `target` envelope field. The new Definition / Boundaries / Invariants text comes verbatim from the spec.
2. **`concepts/frame.md`**: replace Definition, Purpose, Boundaries, Invariants per the spec. Drop the coalesce/serial_queue two-modes section. Drop Common pitfalls references to coalesce-as-debouncer and serial_queue-as-template-wide.
3. **`concepts/cascade.md`**: replace the Boundaries "Does NOT own" listing and the Invariants third bullet per the spec.
4. **`concepts/node-subscription.md`**: replace the "What it is" section and the Invariants third bullet per the spec. Drop the "Self-subscription is first-class" section.
5. **`concepts/signal.md`**: drop the `message/<kind>/<sender_kind>/<target>` subsection from the taxonomy. Update the "Five top-level kinds" preamble to "Four top-level kinds." Remove the `message envelope payload | message_payload` row from the Field-naming convention table. Update the Invariants section's `topic_kind` bullet to admit four (terminal, transient, attribute, event).
6. **`concepts/publisher-subscription.md`**: rename `message_kind` → `message_type` in Boundaries and Invariants. The `"invalidate"` default retires.
7. **`concepts/cascade-graph.md`**: extend the "What it is" paragraph's enumeration of read-endpoint coverage to include forward and reverse joins by `triggering_message_id`.

For each file, run `git diff <file>` after the edit and confirm only the named sections changed.

### Task 41: Retire concept files (2 files)

**Files:** `.ok-planner/design/concepts/_retired/invalidate.md`, `.ok-planner/design/concepts/_retired/backfill.md` (new — moved from parent)

**Steps:**

1. `git mv .ok-planner/design/concepts/invalidate.md .ok-planner/design/concepts/_retired/invalidate.md`.
2. `git mv .ok-planner/design/concepts/backfill.md .ok-planner/design/concepts/_retired/backfill.md`.
3. Edit each retired file's body. The new body is a minimal redirect, current-state-only, no Notes / History sections. Use the spec's directives:
   - `_retired/invalidate.md`: a one-paragraph redirect note pointing to `concept:message`, `concept:message-schema`, `concept:message-emitter-node` as the successor concepts.
   - `_retired/backfill.md`: a one-paragraph redirect note pointing to `concept:message`, `concept:message-schema`, `concept:fan-out`.
4. Update frontmatter: `status: retired` on each.

### Task 42: Create new concept files (2 files)

**Files:** `.ok-planner/design/concepts/message-schema.md` (new), `.ok-planner/design/concepts/message-emitter-node.md` (new)

**Steps:**

1. Create `concepts/message-schema.md` with frontmatter (`concept: message-schema`, `status: as-is`) and the body verbatim from the spec's `## Design changes` directive for this concept. Sections: Definition, Purpose, Boundaries, Invariants. Path-free.
2. Create `concepts/message-emitter-node.md` with frontmatter and body verbatim from the spec's directive. Sections: Definition, Purpose, Boundaries, Invariants. Path-free.
3. Verify neither file cites code paths, file paths, or external docs.

### Task 43: Create story files (7 files), mutate one (1 file), retire two (2 files)

**Files:** `.ok-planner/design/stories/message-schema.md`, `cascade-emit.md`, `cross-frame-coupling.md`, `one-message-per-frame.md`, `frame-origin-audit.md`, `typed-message-substitution.md`, `debug-channel.md` (all new); `stories/message-bus.md` (mutate); `stories/_retired/backfill-ops.md`, `stories/_retired/claim-handoff-across-frames.md` (new directory + moves)

**Steps:**

1. Create the seven new story files. Each uses the structured-heading format of existing files in `design/stories/` (frontmatter `story: <slug>`, `status: as-is`; sections `# <title>`, `## Role`, `## Capability`, `## Business value`, `## Acceptance`, `## Falsifier`, `## Proof`). Copy the Role / Capability / Business value / Acceptance / Falsifier / Proof bodies verbatim from the spec's `## Design changes` → `### Story changes` directives.
2. Mutate `stories/message-bus.md`: replace Acceptance and Falsifier sections per the spec's directive.
3. Create the directory `.ok-planner/design/stories/_retired/`: `mkdir -p .ok-planner/design/stories/_retired/`.
4. Retire backfill-ops: `git mv .ok-planner/design/stories/backfill-ops.md .ok-planner/design/stories/_retired/backfill-ops.md`. Update frontmatter `status: retired`.
5. Retire claim-handoff-across-frames: `git mv .ok-planner/design/stories/claim-handoff-across-frames.md .ok-planner/design/stories/_retired/claim-handoff-across-frames.md`. Update frontmatter `status: retired`. The story's body relies on the retired `frame: next` and `instance: true → frame: next` DSL surfaces (Pass 3 retired the `frame:` modifier and Pass 11 reframes cross-frame coupling through message-emitter nodes); its proof artifact (`test/scenarios/claim_handoff_across_frames_e2e_test.go`) was deleted in Pass 3. Replace the body with a one-paragraph retirement note: "This story's cross-frame coupling shape (`frame: next` + `instance: true → frame: next`) retires with the `frame:` modifier. Cross-frame coupling under the message-schema-layer redesign is expressed through message-emitter nodes (`concept:message-emitter-node`) whose dispatch lands a message that opens the next frame. The original claim-lifetime-across-frames concern survives as the surviving `concept:claim-handle` invariant — a held claim's lifetime is governed by the holding subgraph, not by the frame. → `story:cross-frame-coupling`, `concept:message-emitter-node`, `concept:claim-handle`."

### Task 44: Create decision files (7 files)

**Files:** `.ok-planner/design/decisions/emit-as-node-kind.md`, `attribute-set-as-body.md`, `single-frame-creation-path.md`, `debug-channel-gate-paused-or-breakpoint.md`, `envelope-type-discriminator.md`, `one-message-per-frame.md`, `pre-v1-pure-removal-for-retired-surfaces.md` (all new)

**Steps:**

1. Create each new decision file. Format matches existing files in `design/decisions/` (frontmatter `decision: <slug>`, `status: as-is`; sections `# <title>`, `## Choice`, `## Rationale`, `## Alternatives considered` when present). Copy the Choice / Rationale / Alternatives bodies verbatim from the spec's `## Design changes` → `### Decision changes` directives. The bodies are path-free (no `code:` citations, no file paths).

### Task 45: Resolve tension files (3 files)

**Files:** `.ok-planner/design/tensions/_resolved/serial-queue-per-instance.md`, `coalesced-fire-observability-gap.md`, `frame-lookup-on-every-enqueue.md` (all moved from parent)

**Steps:**

1. For each tension:
   - `git mv .ok-planner/design/tensions/<slug>.md .ok-planner/design/tensions/_resolved/<slug>.md`.
   - Update frontmatter: `status: resolved`, add `spec: 2026-06-14-message-schema-layer-design`, add a `resolution:` block with the resolution summary text from the spec's directive.
2. Each tension's `## Resolution candidates` section is path-free already; the resolution block in frontmatter supersedes it. The body is preserved as historical record per the resolved-tension convention.

### Task 46: Regenerate the design TOCs

**Files:** `.ok-planner/design/concepts.md`, `.ok-planner/design/stories.md`, `.ok-planner/design/decisions.md`

**Steps:**

1. The TOCs are auto-generated. Check if there's a script (`scripts/regenerate-design-tocs.sh` or similar) or whether they're hand-maintained.
2. If a script: run it.
3. If hand-maintained: update each TOC manually. Add new concept/story/decision entries with one-sentence summaries. Move retired concept entries to the "Retired concepts" section. Confirm the listing matches the actual files in each directory.
4. Verify by inspection that the TOCs match the current state of the design tree.

---

## Pass 11: Acceptance — schema, emit, cascade, substitution, audit stories (acceptance pass — STORY-message-schema, STORY-cascade-emit, STORY-cross-frame-coupling, STORY-one-message-per-frame, STORY-frame-origin-audit, STORY-typed-message-substitution)

**Goal:** Deliver the six template-DSL stories end-to-end. The mechanism passes (1–10) built all the pieces; this pass adds the final integrating wiring (if any) and the proof artifacts that exhibit each story through the assembled product. Each proof boots a real rimsky stack (testcontainers) and drives the real surface the story names.

**Scope:** Tasks 47–53

**Falsifier:** any of the six stories has no committed proof artifact in `test/scenarios/` or `examples/`; OR any proof artifact stubs the value-delivering component (uses a canned response in place of the real persistence-backed delivery / receiver-side substitution / emit-node dispatch); OR any proof asserts at a layer below user-observable outcome (asserts the function returned the right struct rather than asserting the next frame opened with the expected `triggering_message_id`); OR the six proofs can pass without the wiring from Passes 1–10 actually doing anything; OR `make test-all` fails.

### Task 47: STORY-message-schema acceptance — declared types stale-mark, undeclared refuse

**Files:** `test/scenarios/story_message_schema_e2e_test.go` (new)

**Story:** STORY-message-schema
**Proof form (from spec):** Executable proof. Declared type opens a frame and stale-marks subscribed receivers; undeclared type refuses with the expected error.

**Steps:**

1. Create a new scenario test under `test/scenarios/`. Use the harness pattern from `test/scenarios/instance_lifecycle_fullstack_test.go` (the `scenario.Start(t, scenario.HarnessOpts{})` boot pattern). Deploy a template with:
   - A `messages:` block declaring two types: one with a simple body schema, one with a more complex body.
   - A node-type subscribed to the first declared message-type as a virtual node.
2. Drive the proof: 
   - POST `POST /instances/{id}/messages` with a declared type → assert HTTP 200/201, assert the next frame opens with `triggering_message_id` set, assert the subscribed node-run transitions to `stale` then `running` then `terminal/success`.
   - POST with an undeclared type → assert HTTP 400, assert the response body names the rejected type and lists the declared set, assert no row in `rimsky_messages`.
3. Run `go test ./test/scenarios/story_message_schema_e2e_test.go -count=1 -v` and confirm.

### Task 48: STORY-cascade-emit acceptance — emit-node dispatches a message

**Files:** `test/scenarios/story_cascade_emit_e2e_test.go` (new)

**Story:** STORY-cascade-emit
**Proof form (from spec):** Executable proof. Emit-node dispatches when its subscriptions fire; resulting message body contains the expected substituted values; mismatched schemas reject at registration.

**Steps:**

1. Scenario test with a template containing:
   - A `messages:` declaring an emitted type.
   - A regular `executor:` node that produces an attribute on terminal/success.
   - An emit-node (`emits_message: <type>`) subscribed to the executor node's `terminal/success`, with `attributes:` pulling from the executor node's attributes.
2. Drive the proof:
   - Send an initial trigger message to wake the executor node.
   - Assert the executor node runs and emits its attribute.
   - Assert the emit-node fires next; assert a new envelope appears in `rimsky_messages` with the expected `type` and body.
   - Assert the next frame opens with `triggering_message_id` = the new envelope's id.
3. Add a sub-test for the rejection leg: attempt to register a template where the emit-node's `attributes:` schema declares an extra field; assert registration fails.
4. Run `go test ./test/scenarios/story_cascade_emit_e2e_test.go -count=1 -v` and confirm.

### Task 49: STORY-cross-frame-coupling acceptance — executable proof for the back-edge + self-drain

**Files:** `test/scenarios/story_cross_frame_coupling_e2e_test.go` (new)

**Story:** STORY-cross-frame-coupling
**Proof form (from spec):** All-of-the-above. Executable proofs for the back-edge cycle and the self-drain convergence, plus a demo walking through the scenario succeeding. This task handles the executable proofs; Task 50 handles the demo.

**Steps:**

1. Scenario test 1 (back-edge cycle): template with a 2-cycle A → B → A where:
   - A is an `executor:` node subscribed to a message of type T.
   - B is an `executor:` node subscribed to A's terminal/success.
   - An emit-node E is subscribed to B's terminal/success, with `emits_message: T` and `attributes:` pulling from B.
   - A's attribute schema reads `{{messages.T.<field>}}` carrying B's data.
2. Drive: send the initial type-T message. Assert: A runs, B runs, E emits a new type-T message, the next frame opens and A re-runs reading B's data via the typed-message substitution. The cycle converges when B settles with the same value twice.
3. Scenario test 2 (self-drain): an emit-node E subscribed to its own emit-source (the same message type) with `when: payload.changed`. Drive: send the initial message with `changed=true` in the body; the emit-node re-fires until it settles with `changed=false`; assert the loop converges in a bounded number of frames (e.g., 5 frames for a 5-iteration drain).
4. Run `go test ./test/scenarios/story_cross_frame_coupling_e2e_test.go -count=1 -v` and confirm.

### Task 50: STORY-cross-frame-coupling acceptance — demo

**Files:** `examples/cross-frame-coupling-demo.sh` (new), `examples/cross-frame-coupling-demo-template.yaml` (new)

**Story:** STORY-cross-frame-coupling (demo half of "all-of-the-above")
**Proof form (from spec):** Demo walking through the scenario succeeding.

**Steps:**

1. Create `examples/cross-frame-coupling-demo-template.yaml` with the back-edge template from Task 49.
2. Create `examples/cross-frame-coupling-demo.sh` that:
   - Starts the rimsky-all-in-one image (`docker run rimsky-all-in-one:latest`) or assumes a running instance (the script can take the control-API URL as an argument).
   - Registers the template via the CLI (`rimsky template register cross-frame-coupling-demo-template.yaml`).
   - Creates an instance.
   - POSTs the initial message via the message-emit endpoint.
   - Polls the cascade-graph observability endpoint until the cycle converges.
   - Prints each frame's `triggering_message_id` and the substituted body values, so a reader sees the back-edge flow.
3. Match the script style of existing demos under `examples/` (look at `examples/host-agent-control-plane-demo.sh` or similar for the pattern).
4. The script must be self-checking: at the end, after polling for convergence, it greps its own captured output for required strings (e.g., at least one line per converged frame showing the `triggering_message_id` and the back-edge body value) and exits non-zero if any required line is missing. Run the demo against a local rimsky stack and confirm the script exits zero.

### Task 51: STORY-one-message-per-frame acceptance — N messages produce N distinct frames

**Files:** `test/scenarios/story_one_message_per_frame_e2e_test.go` (new)

**Story:** STORY-one-message-per-frame
**Proof form (from spec):** Executable proof. N messages posted in succession produce N distinct frames, each carrying one message, each settling cleanly with body substitution resolving the expected values.

**Steps:**

1. Scenario test: template with a `messages:` declaring one type and a node-type subscribed to it.
2. Drive: POST 10 messages of the declared type in quick succession (within the same outer test tick).
3. Poll the cascade-graph endpoint until all 10 frames have settled.
4. Assert: exactly 10 frame rows exist in `rimsky_frames` for the instance; each frame has a distinct `triggering_message_id`; each frame carried exactly one message in its message-ledger join; each frame's subscribed receiver ran once and the substitution resolved against the expected body field values.
5. Run `go test ./test/scenarios/story_one_message_per_frame_e2e_test.go -count=1 -v` and confirm.

### Task 52: STORY-frame-origin-audit acceptance — demo showing every frame has a triggering message

**Files:** `examples/frame-origin-audit-demo.sh` (new), `examples/frame-origin-audit-demo-template.yaml` (new)

**Story:** STORY-frame-origin-audit
**Proof form (from spec):** Demo. Every frame in a representative end-to-end run (including back-edge cycles and self-drain) has an originating message visible through the observability surface.

**Steps:**

1. Create `examples/frame-origin-audit-demo-template.yaml` — a template that exercises a back-edge cycle (similar to Task 50's template).
2. Create `examples/frame-origin-audit-demo.sh` that:
   - Boots the rimsky stack.
   - Registers the template, creates an instance, sends an initial message.
   - Polls the cascade-graph `GET /instances/{id}/frames` endpoint.
   - For each frame, prints: `frame_id`, `triggering_message_id`, the joined message envelope's `type` and `sender`.
   - The output reads as "every frame, its triggering message" — exhibiting the property to a third party.
3. The script must be self-checking: at the end, it asserts that every frame line in the captured output has a non-empty `triggering_message_id` field (use `grep -c` or `awk` to count frame lines and matched ID lines; assert they're equal). Exits non-zero if any frame line lacks a `triggering_message_id`. Run the demo and confirm the script exits zero.

### Task 53: STORY-typed-message-substitution acceptance — registration rejects typos, substitution resolves correctly, code-path test

**Files:** `test/scenarios/story_typed_message_substitution_e2e_test.go` (new), `lib/graph/attribute/substitution_test.go` (extended)

**Story:** STORY-typed-message-substitution
**Proof form (from spec):** Executable proof. Typo'd field names reject at registration in both directions; a running back-edge cycle's receiver reads through the typed-message grammar and resolves correctly; an assertion confirms a single substitution-resolution function services both surfaces.

**Steps:**

1. Scenario test: 
   - Sub-test 1: register a template where a receiver attribute schema references `{{messages.<type>.<typo'd-field>}}` — assert registration rejects with an error naming the missing field.
   - Sub-test 2: register a template where an emit-node's `attributes:` declares a field the destination message type's body_schema doesn't have — assert registration rejects.
   - Sub-test 3: a working back-edge cycle (similar to Task 49); assert the receiver's substitution resolves the message body values correctly through the typed-message grammar.
2. In `lib/graph/attribute/substitution_test.go`: add a focused unit test that asserts the same resolver function is invoked for both `{{nodes.X.attribute.Y}}` and `{{messages.X.Y}}` — call the resolver with both directive shapes against a shared `ResolveContext` and confirm both succeed.
3. Run `go test ./test/scenarios/story_typed_message_substitution_e2e_test.go ./lib/graph/attribute/... -count=1 -v` and confirm.

---

## Pass 12: Acceptance — debug channel (acceptance pass — STORY-debug-channel)

**Goal:** Deliver STORY-debug-channel end-to-end. The mechanism is in Pass 9; this pass adds the proof artifact that exhibits the story through the real control-API surface against a real rimsky stack.

**Scope:** Task 54

**Falsifier:** there is no committed proof artifact for STORY-debug-channel; OR the proof stubs the gate-state check (uses a synthetic paused/breakpoint state rather than driving the real instance through pause / breakpoint installation); OR the proof asserts at a layer below the user-observable outcome (asserts the handler returned the right status struct rather than asserting the override actually mutated the node-run / attribute and then observing the cascade resume); OR `make test-all` fails.

### Task 54: STORY-debug-channel acceptance — override accepted on legal states, refused on healthy

**Files:** `test/scenarios/story_debug_channel_e2e_test.go` (new)

**Story:** STORY-debug-channel
**Proof form (from spec):** Executable proof. Override accepted on both legal states (paused, breakpoint); refused on a healthy running instance with the expected error.

**Steps:**

1. Scenario test using `scenario.Start(t, scenario.HarnessOpts{})`. Deploy a simple template with a node-type that the override will target. Create an API key with the new `instance:debug-override` action authorized.
2. **Leg 1 — healthy instance refuses:** Create an instance; do not pause; do not set a breakpoint. POST to `POST /instances/{id}/debug/override` with `action: invalidate_node`. Assert HTTP 409 with a body naming the gate predicate.
3. **Leg 2 — paused instance accepts:** Same instance. POST `paused: true` via the existing instance-pause endpoint. POST `debug/override` with `action: invalidate_node`. Assert HTTP 200; assert the named node-run row transitions to `stale`; assert an audit-event row of kind `debug.override.applied` appears.
4. **Leg 3 — breakpoint state accepts:** Resume the instance. Install a pause-mode breakpoint on a node-type using the existing breakpoint API. Trigger a dispatch that hits the breakpoint (the runner blocks). POST `debug/override` with `action: set_attribute`. Assert HTTP 200; assert the attribute row commits; assert the audit-event row.
5. **Leg 4 — permission denied:** Use an API key without `instance:debug-override` authorized. POST `debug/override`. Assert HTTP 403.
6. Run `go test ./test/scenarios/story_debug_channel_e2e_test.go -count=1 -v` and confirm.

---

## Manual checks after completion

None. Every story has an executable proof or runnable demo committed alongside the code. The completion auditor walks each STORY-* and TD-* in the spec's manifest; the validator at each acceptance pass exercises the real user-observable outcome.
