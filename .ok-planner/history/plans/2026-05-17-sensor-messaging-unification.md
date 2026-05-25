# Publisher Protocol Unification + Deploy Surface Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md`
**Goal:** Collapse the rimsky-side sensor-observation pipeline into the existing generic messages endpoint, rename the `Sensor` protocol to `Publisher` across every surface, persist publisher state in each bundled sensor, and ship the four bundled sensors as deployable artifacts (Dockerfiles + compose + helm). Most of the work is removal of wrong abstractions; the net code change is negative.
**Architecture:** The rimsky-side `route:POST /sensors/{watch_id}/observations` endpoint and its substitution machinery are deleted. Bundled sensors instead POST standard message envelopes to the existing `route:POST /instances/{id}/messages` with `sender_kind: "publisher"` + a `publisher_subscription_id` capability token. The protocol-level Go + proto vocabulary renames from `Sensor`/`Watch`/`StartWatch` to `Publisher`/`PublisherSubscription`/`Subscribe`. Routing fields (`target_node`, `message_kind`) move inline onto `SubscribeRequest`, eliminating the `OnObservationSpec` Go substruct. A universal `Idempotency-Key` header lands on the messages endpoint. Three bundled sensors gain per-binary state DBs to survive restart. Schema lands via direct edits to `001-baseline.sql` (pre-v1; no migrations 011/012/013).
**Tech Stack:** Go (root module + `foundation/` submodule), Postgres + SQLite under `foundation/persistence/`, gRPC protocol via `proto:protocols/proto/v1/publisher.proto`, k8s helm chart under `deploy/kubernetes/`, docker-compose under `deploy/`, scenario tests under `test/scenarios/` using `testcontainers-go`.

---

## Reading order for the implementer

This plan executes in one fresh `/execute-plan` run, start to finish. The implementer has no memory of the spec's design discussion. Before starting work, read these files in this order:

1. `.ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md` — the full spec. The plan translates it task-by-task but doesn't restate every architectural rationale.
2. `.ok-planner/design/concepts/sensor.md` — current concept doc; will be rewritten end-to-end by this plan.
3. `.ok-planner/design/concepts/subscription.md` — current template-DSL `subscribes:` block concept; will be renamed `concept:node-subscription`.
4. `.ok-planner/design/concepts/message.md` — current message concept; will gain idempotency-key subsection + `sender_kind` enum update.
5. `protocols/proto/v1/sensor.proto` — current Sensor proto; will be replaced by `publisher.proto`.
6. `control/controlapi/sensors.go` — entire file (~293 lines) deleted by this plan; understand what it does before deletion.
7. `control/controlapi/messages.go` — generic messages endpoint; gains `sender_kind: "publisher"` + idempotency-key support.
8. `runtime/sensors.go` — sensor lifecycle (StartWatchesForInstance, StopWatchesForInstance, ResyncSensorWatches); renames to `runtime/publishers.go`.
9. `foundation/persistence/postgres/migrations/001-baseline.sql` — schema baseline; edited directly to drop `rimsky_sensor_watches` → `rimsky_publisher_subscriptions` rename, drop two columns, add three columns, plus the new `rimsky_message_idempotencies` table.
10. `control/config/sensors.go` — peer registry; renames to `control/config/publishers.go`; gains a third `publishers:` config block.
11. `CLAUDE.md` — multiple surgical edits across this plan.
12. `.claude/rules/rules.md` — pre-v1 break-freely discipline applies throughout.

After reading: execute the tasks in order. Each task has files + steps + verification.

## Open items resolved at write-plan time

The spec listed six open items for write-plan decision. Resolutions (with rationale) used in this plan:

1. **`target_node` nullability**: `NOT NULL` (no empty-string sentinel). Templates that fail to populate `target_node` get a clear error at template registration via `DisallowUnknownFields()` and per-block validation. No production templates exist pre-v1; all in-tree fixtures will set `target_node`.

2. **`test/scenarios/sensor/` directory naming**: keep as-is. These scenarios exercise the bundled sensor implementations, which IS sensor-specific. Protocol-level publisher tests (if any are added) land under a new `test/scenarios/publisher/` directory in a future plan.

3. **Sensor-cron docstring fix**: tracked in Task 53 (cleanup).

4. **Capability-check rejection status code**: `403 Forbidden`. More honest about "I see the request but you're not authorized" than `404 Not Found`.

5. **Conformance binary `--instance-id` CLI flag**: add the flag. The new path is instance-scoped, so the conformance binary's fake-rimsky-endpoint setup needs the instance_id parameter.

6. **`payload_template` canonicalizer probe**: probe complete. `code:control/controlapi/templates.go:692` already uses `dec.DisallowUnknownFields()`, so `payload_template` rejection is automatic via the standard `json: unknown field "payload_template"` mechanism once `OnObservationSpec.PayloadTemplate` is deleted. No bespoke canonicalizer error message needed; operators see the standard unknown-field error. The CHANGELOG entry documents the migration path.

---

## File map

This is the spec's File map verbatim for the implementer's quick reference. The spec is the source of truth; consult it for rationale.

### Created (12-15)

- `sensors/sensor-cron/Dockerfile.sensor-cron`
- `sensors/sensor-http/Dockerfile.sensor-http`
- `sensors/sensor-object-store/Dockerfile.sensor-object-store`
- `sensors/sensor-webhook/Dockerfile.sensor-webhook`
- `sensors/sensor-http/state_db.go` (in `package main`, NOT `package persistence`)
- `sensors/sensor-object-store/state_db.go`
- `sensors/sensor-webhook/state_db.go`
- `deploy/kubernetes/rimsky-chart/templates/deployment-sensor-cron.yaml`
- `deploy/kubernetes/rimsky-chart/templates/service-sensor-cron.yaml`
- (Same deployment + service pair for sensor-http, sensor-object-store, sensor-webhook — 6 more YAML files)
- `cmd/rimsky-publisher-conformance/{main.go, main_test.go, checks.go}` (rename of `cmd/rimsky-sensor-conformance/`)
- `protocols/proto/v1/publisher.proto` (rename of `sensor.proto`)
- `runtime/publishers.go` (rename of `runtime/sensors.go`)
- `runtime/clientiface/publisher.go` (rename of `runtime/clientiface/sensor.go`)
- `runtime/remote/publisher_client.go` (rename of `runtime/remote/sensor_client.go`)
- `control/config/publishers.go` (rename of `control/config/sensors.go`)
- `foundation/persistence/publisher_subscriptions.go` (rename of `foundation/persistence/sensor_watches.go`)
- `foundation/persistence/postgres/publisher_subscriptions.go` (rename)
- `foundation/persistence/sqlite/publisher_subscriptions.go` (rename)
- `foundation/persistence/message_idempotencies.go` (new — interface for the new table)
- `foundation/persistence/postgres/message_idempotencies.go` (new — driver impl)
- `foundation/persistence/sqlite/message_idempotencies.go` (new — driver impl)
- `runtime/sweep_message_idempotencies.go` (new — retention sweep, analogous to `runtime/sweep_claim_handle_retention.go`)
- `.ok-planner/design/concepts/publisher.md` (new internal concept)
- `.ok-planner/design/concepts/publisher-subscription.md` (new internal concept; folds `sensor-watch.md` content)
- `.ok-planner/design/concepts/replica.md` (new internal concept)
- `docs/concepts/publisher.md` (new public concept)
- `docs/concepts/publisher-subscription.md` (new public concept)
- `docs/concepts/replica.md` (new public concept)
- `docs/protocols/publisher.md` (new public protocol guide)

### Modified (~60-75)

See spec §File map § Modified for the full list. Key surfaces:
- `protocols/proto/v1/publisher.proto` (new content)
- All Go-side renames: types (`SensorRegistry` → `PublisherRegistry`, etc.), functions (`StartWatchesForInstance` → `StartPublisherSubscriptionsForInstance`, etc.), file renames (delete + create pairs)
- Schema baseline: `foundation/persistence/{postgres,sqlite}/migrations/001-baseline.sql`
- `control/controlapi/messages.go::handleCreateMessage` (idempotency + capability check)
- All four bundled sensors (`sensors/sensor-{cron,http,object-store,webhook}/`) — Subscribe handler, URL construction, body shape, file docstrings
- Template DSL: `sensors:` → `publishers:` (touches `foundation/spec/template.go`, `foundation/spec/graphs.go`, `graph/template/canonical/*.go`, all fixtures)
- Concept docs (~10 files)
- CLAUDE.md (surgical edits at multiple lines)
- CHANGELOG.md (Unreleased entry)
- feature-index.md (row edits)
- licensing.yml (entry rename)
- `deploy/{build-images.sh, docker-compose.yml, rimsky.yml}` and helm chart values
- All sensor-related test files

### Dropped (~6)

- `control/controlapi/sensors.go` (entire file)
- `control/controlapi/sensors_test.go` (entire file)
- `protocols/proto/v1/sensor.proto` (renamed; `gen/sensor.pb.go` + `sensor_grpc.pb.go` deleted by regen)
- `.ok-planner/design/concepts/sensor-watch.md` (content folded into `publisher-subscription.md`)
- `cmd/rimsky-sensor-cron/` (orphan empty directory)
- `foundation/spec/graphs.go::OnObservationSpec` (Go type) + `graph/node/template.go:58` alias

---

## Tasks

### Task 1 — Read the codebase surfaces this plan touches

**Files:** none modified.

**Steps:**

1. Read all 12 files listed in the "Reading order for the implementer" section above.
2. Run an inventory grep to confirm the rename surface:
   ```
   rg -c 'StartWatch|StopWatch|ListWatches|WatchDescriptor|watch_id' --type=go | wc -l
   ```
   Expected: roughly 30 Go files with hits (per spec footprint estimate).
3. Run a parallel grep for the protocol-level type names:
   ```
   rg -c 'SensorRegistry|SensorClient|SensorWatchRow|SensorWatchTable' --type=go | wc -l
   ```
4. Verify the existing surfaces the spec assumes exist:
   ```
   ls control/controlapi/sensors.go
   ls protocols/proto/v1/sensor.proto
   ls runtime/sensors.go
   ls control/config/sensors.go
   ls foundation/persistence/sensor_watches.go
   ls cmd/rimsky-sensor-conformance/
   ls cmd/rimsky-sensor-cron/  # empty dir; per spec § Dropped
   ls foundation/persistence/postgres/migrations/001-baseline.sql
   ls foundation/persistence/sqlite/migrations/001-baseline.sql
   ```
   All should exist. If any are missing, stop and surface to the user.

**Verification:** all `ls` calls succeed. The implementer now has the codebase model in mind.

### Task 2 — Capture clean test baseline

**Files:** none modified.

**Steps:**

1. From `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky`, run:
   ```
   make build-all && make lint && make license-lint
   ```
2. Run targeted unit suites (faster than full `make test-all` for the baseline):
   ```
   go test ./foundation/persistence/... -count=1
   go test ./runtime/... -count=1
   go test ./control/... -count=1
   go test ./sensors/... -count=1
   ```
3. If anything fails before this plan touches a single file, stop and surface to the user; the baseline isn't clean.

**Verification:** all commands exit 0.

---

## Schema + idempotency infrastructure (universal, additive, lands first)

### Task 3 — Add `rimsky_message_idempotencies` to postgres baseline

**Files:** `foundation/persistence/postgres/migrations/001-baseline.sql`.

**Steps:**

1. Open the file. Find the end (the last `CREATE TABLE` or `CREATE INDEX` block).
2. Append the new table + index from spec §Architecture details §Schema (post-baseline rewrite):
   ```sql
   CREATE TABLE rimsky_message_idempotencies (
       instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
       sender           TEXT NOT NULL,
       idempotency_key  TEXT NOT NULL,
       message_id       UUID NOT NULL,
       created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
       PRIMARY KEY (instance_id, sender, idempotency_key)
   );
   CREATE INDEX idx_message_idempotencies_created_at
       ON rimsky_message_idempotencies(created_at);
   ```

**Verification:** the file parses as valid SQL when run against a fresh Postgres. Defer actual schema verification to Task 5 below.

### Task 4 — Add `rimsky_message_idempotencies` to sqlite baseline

**Files:** `foundation/persistence/sqlite/migrations/001-baseline.sql`.

**Steps:**

1. Open the file. Find the end.
2. Append the SQLite mirror:
   ```sql
   CREATE TABLE rimsky_message_idempotencies (
       instance_id      TEXT NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
       sender           TEXT NOT NULL,
       idempotency_key  TEXT NOT NULL,
       message_id       TEXT NOT NULL,
       created_at       TEXT NOT NULL DEFAULT (datetime('now')),
       PRIMARY KEY (instance_id, sender, idempotency_key)
   );
   CREATE INDEX idx_message_idempotencies_created_at
       ON rimsky_message_idempotencies(created_at);
   ```

**Verification:** the file parses as valid SQLite when applied to a fresh DB.

### Task 5 — Verify baseline applies cleanly to both drivers

**Files:** none modified.

**Steps:**

1. Run the persistence package's test suite which exercises baseline migration:
   ```
   go test ./foundation/persistence/... -count=1
   ```
2. If the conformance tests (`foundation/persistence/conformance/`) hit testcontainers Postgres, they verify the baseline. If failures cite missing tables or columns, the baseline file has a typo.

**Verification:** `go test ./foundation/persistence/... -count=1` exits 0.

### Task 6 — Add `MessageIdempotencyTable` interface in foundation/persistence

**Files:** `foundation/persistence/message_idempotencies.go` (new).

**Steps:**

1. Create the file with the interface declaration. Match the existing interface style of `foundation/persistence/claim_handles.go` or similar. The interface should expose:
   ```go
   // Package persistence — message idempotency table interface.
   //
   // @concept: message
   package persistence

   import (
       "context"
       "time"
   )

   // MessageIdempotencyRow is the persisted dedup tuple.
   type MessageIdempotencyRow struct {
       InstanceID      InstanceID
       Sender          string
       IdempotencyKey  string
       MessageID       MessageID
       CreatedAt       time.Time
   }

   // MessageIdempotencyTable is the dedup-tuple persistence interface.
   // INSERT returns the row's MessageID (the existing one on conflict,
   // the new one on first insert).
   type MessageIdempotencyTable interface {
       // InsertOrLookup attempts INSERT; on PK conflict, returns the
       // existing row's MessageID. Returns the resulting row + a
       // boolean `inserted` flag (true for fresh insert, false for
       // conflict-replay).
       InsertOrLookup(ctx context.Context, tx Tx, row MessageIdempotencyRow) (MessageIdempotencyRow, bool, error)

       // DeleteOlderThan removes rows with created_at < cutoff.
       // Returns count of deleted rows.
       DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
   }
   ```
2. Add `MessageIdempotencies() MessageIdempotencyTable` to the `Tables` interface at `foundation/persistence/tables.go` (NOT the `Database` interface — table accessors live on `Tables`; the existing pattern is `Tables.SensorWatches() SensorWatchesTable`).

**Verification:**
```
cd foundation && go build ./...
```
Build fails on missing driver impls (expected; fixed in Task 7 + 8).

### Task 7 — Implement `MessageIdempotencyTable` in postgres driver

**Files:** `foundation/persistence/postgres/message_idempotencies.go` (new).

**Steps:**

1. Create the file matching the style of `foundation/persistence/postgres/claim_handles.go`. Implement:
   - `InsertOrLookup`: uses `INSERT ... ON CONFLICT (instance_id, sender, idempotency_key) DO UPDATE SET message_id = rimsky_message_idempotencies.message_id RETURNING message_id, created_at, (xmax = 0) AS inserted` (the `xmax = 0` trick distinguishes fresh insert from conflict-update). Alternative: `INSERT ... ON CONFLICT DO NOTHING; SELECT ...` with a separate query; the xmax trick is more atomic.
   - `DeleteOlderThan`: `DELETE FROM rimsky_message_idempotencies WHERE created_at < $1`.
2. Wire `MessageIdempotencies()` on the postgres `Tables` impl to return a new instance of this table.

**Verification:**
```
cd foundation && go build ./...
go test ./foundation/persistence/postgres/... -count=1
```

### Task 8 — Implement `MessageIdempotencyTable` in sqlite driver

**Files:** `foundation/persistence/sqlite/message_idempotencies.go` (new).

**Steps:**

1. Mirror Task 7 for SQLite. SQLite's `INSERT ... ON CONFLICT DO NOTHING` does not directly return the existing row; use the two-query approach: try INSERT, on `sql.ErrNoRows` from a RETURNING clause (or by checking affected rows), SELECT the existing row.
2. Wire `MessageIdempotencies()` on the sqlite `Tables` impl.

**Verification:**
```
cd foundation && go build ./... && go test ./foundation/persistence/sqlite/...
```

### Task 9 — Implement retention sweep for `rimsky_message_idempotencies`

**Files:** `runtime/sweep_message_idempotencies.go` (new); `runtime/retention_sweeps.go` (modified — add config field).

**Steps:**

1. Read `runtime/sweep_claim_handle_retention.go` as the reference pattern.
2. Create `runtime/sweep_message_idempotencies.go`. Follow the reference pattern from `runtime/sweep_claim_handle_retention.go` — sweep functions take the table interface directly (not the whole `Database`):
   ```go
   package runtime

   import (
       "context"
       "fmt"
       "time"

       "github.com/fallguyconsulting/rimsky/foundation/persistence"
   )

   // SweepMessageIdempotencies deletes idempotency rows older than cutoff.
   // Runs under the scheduler-tick advisory lock; no per-row claimant
   // guard required (rows have no holder).
   //
   // @concept: message
   func SweepMessageIdempotencies(ctx context.Context, mit persistence.MessageIdempotencyTable, cutoff time.Duration) (int64, error) {
       deadline := time.Now().Add(-cutoff)
       n, err := mit.DeleteOlderThan(ctx, deadline)
       if err != nil {
           return 0, fmt.Errorf("sweep message_idempotencies: %w", err)
       }
       return n, nil
   }
   ```
3. In `runtime/retention_sweeps.go` (or wherever `RetentionConfig` lives), add `MessageIdempotenciesTrailing time.Duration` field with a default of `24 * time.Hour`.
4. Find the scheduler-tick wiring (`graph/scheduler/scheduler.go::tick`) where `SweepClaimHandleRetention` is invoked. Add an analogous call for `SweepMessageIdempotencies` directly after it, gated on `cfg.Retention.MessageIdempotenciesTrailing > 0`.

**Verification:**
```
go build ./... && go test ./runtime/... -run TestSweepMessageIdempotencies
make lint
```
Build clean. The test referenced is added in Task 10.

### Task 10 — Test for `SweepMessageIdempotencies`

**Files:** `runtime/sweep_message_idempotencies_test.go` (new).

**Steps:**

1. Read `runtime/sweep_claim_handle_retention_test.go` for the pgtest pattern.
2. Create the new test file. Write three cases:
   - `TestSweepMessageIdempotencies_DeletesPastCutoff`: insert a row with `created_at = now - 25h`, run the sweep with `cutoff = 24h`, assert the row is gone.
   - `TestSweepMessageIdempotencies_PreservesWithinCutoff`: insert a row with `created_at = now - 1h`, run the sweep with `cutoff = 24h`, assert the row is still present.
   - `TestSweepMessageIdempotencies_NoOpWhenEmpty`: empty table; sweep returns 0; no error.

**Verification:**
```
go test ./runtime/... -run TestSweepMessageIdempotencies -v
```

### Task 11 — Add `Idempotency-Key` support to `handleCreateMessage`

**Files:** `control/controlapi/messages.go`.

**Steps:**

1. Find the existing `handleCreateMessage` function (and its request struct).
2. Add an `IdempotencyKey string` field to the request struct, parsed from the HTTP header `Idempotency-Key` (NOT a body field — header per spec). If the header is empty, treat as no idempotency.
3. Modify the handler logic:
   - If `Idempotency-Key` is present, compute the dedup tuple `(instance_id, sender, idempotency_key)`. Note: at this point, `sender` defaults to `"operator"` since publisher kind is not yet wired (lands in Task 31).
   - Call `db.MessageIdempotencies().InsertOrLookup(...)`.
   - If `inserted = true`: proceed with the message-insert flow as before; the message_id has been recorded against the idempotency row at insert time.
   - If `inserted = false` (conflict): skip the message insert; return the existing `message_id` with `200 OK` (NOT 201 — status code is the only signal of replay).
4. The non-idempotent path (no header) stays unchanged: 201 Created.
5. Wrap the InsertOrLookup + message insert in a single tx so a crash mid-flow doesn't leave an idempotency row pointing at a never-inserted message.

**Verification:**
```
go build ./... && go test ./control/controlapi/... -run TestCreateMessage
```

### Task 12 — Test idempotency-key behavior on `handleCreateMessage`

**Files:** `control/controlapi/messages_test.go`.

**Steps:**

1. Add test cases (mirror the style of existing `messages_test.go` tests):
   - `TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting`: POST with `Idempotency-Key: test-key-1` → 201 Created with message_id A. POST again with same key + same body → 200 OK with message_id A.
   - `TestCreateMessage_IdempotencyKeyDifferentKeysCreateSeparateMessages`: POST with `Idempotency-Key: key-1` → message_id A. POST with `Idempotency-Key: key-2` (same body) → message_id B ≠ A, 201 Created.
   - `TestCreateMessage_NoIdempotencyKeyCreatesNewMessageEachTime`: two POSTs without the header create two distinct messages, both 201 Created.

**Verification:**
```
go test ./control/controlapi/... -run TestCreateMessage_Idempotency -v
```

### Task 13 — Quiescence checkpoint: idempotency infrastructure clean

**Files:** none modified.

**Steps:**

1. Run the full verification suite:
   ```
   make build-all && make test-all && make lint && make license-lint
   ```
2. All four must exit 0. Idempotency infrastructure is now operator-callable. No sensor code touched yet.

**Verification:** all four commands exit 0.

---

## Publisher protocol rename + observation-route deletion (atomic architectural change)

The following tasks land together as one architectural unification. The proto rename + regen MUST run first; Go-source renames depend on the regenerated bindings. Schema baseline edits and code edits all happen in sequence before the next quiescence checkpoint.

### Task 14 — Rename `sensor.proto` → `publisher.proto` (proto-level rename)

**Files:** `protocols/proto/v1/sensor.proto` (deleted), `protocols/proto/v1/publisher.proto` (new).

**Steps:**

1. Read `protocols/proto/v1/sensor.proto` end-to-end to understand the current shape.
2. Create `protocols/proto/v1/publisher.proto` with the new content from spec §Architecture details §Publisher protocol verbs + types:
   ```protobuf
   syntax = "proto3";

   package rimsky.protocols.v1;

   option go_package = "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen;genv1";

   // Publisher is a peer service that publishes messages into rimsky.
   // Sensors are one kind of publisher; other kinds may exist.
   //
   // Verbs:
   //   Capabilities      — publisher advertises supported kinds.
   //   Subscribe         — rimsky registers a publisher-subscription.
   //   Unsubscribe       — rimsky tears down a publisher-subscription.
   //   ListSubscriptions — reconcile-on-startup verb.
   service Publisher {
     rpc Capabilities      (CapabilitiesRequest)      returns (PublisherCapabilities);
     rpc Subscribe         (SubscribeRequest)         returns (SubscribeResponse);
     rpc Unsubscribe       (UnsubscribeRequest)       returns (UnsubscribeResponse);
     rpc ListSubscriptions (ListSubscriptionsRequest) returns (ListSubscriptionsResponse);
   }

   message CapabilitiesRequest {}

   message PublisherCapabilities {
     repeated PublisherKindCapability kinds = 1;
   }

   message PublisherKindCapability {
     string kind = 1;
   }

   message SubscribeRequest {
     string publisher_subscription_id = 1;
     string instance_id               = 2;
     string kind                      = 3;
     bytes  resolved_config           = 4;
     string target_node               = 5;
     string message_kind              = 6;  // default 'invalidate' if empty
   }
   message SubscribeResponse {}

   message UnsubscribeRequest {
     string publisher_subscription_id = 1;
   }
   message UnsubscribeResponse {}

   message ListSubscriptionsRequest {}

   message ListSubscriptionsResponse {
     repeated PublisherSubscriptionDescriptor subscriptions = 1;
   }

   message PublisherSubscriptionDescriptor {
     string publisher_subscription_id = 1;
     string instance_id               = 2;
     string kind                      = 3;
     bytes  resolved_config           = 4;
     string target_node               = 5;
     string message_kind              = 6;
     string state                     = 7;
   }
   ```
3. Delete `protocols/proto/v1/sensor.proto`:
   ```
   rm protocols/proto/v1/sensor.proto
   ```

**Verification:**
```
ls protocols/proto/v1/publisher.proto
test ! -f protocols/proto/v1/sensor.proto
```

### Task 15 — Regenerate proto Go bindings

**Files:** `protocols/proto/v1/gen/publisher.pb.go` (new), `protocols/proto/v1/gen/publisher_grpc.pb.go` (new); `protocols/proto/v1/gen/sensor.pb.go` (deleted by regen), `protocols/proto/v1/gen/sensor_grpc.pb.go` (deleted by regen).

**Steps:**

1. Delete the old generated files:
   ```
   rm protocols/proto/v1/gen/sensor.pb.go
   rm protocols/proto/v1/gen/sensor_grpc.pb.go
   ```
2. Run the regeneration command:
   ```
   make proto-gen
   ```

**Verification:**
```
ls protocols/proto/v1/gen/publisher.pb.go
ls protocols/proto/v1/gen/publisher_grpc.pb.go
test ! -f protocols/proto/v1/gen/sensor.pb.go
test ! -f protocols/proto/v1/gen/sensor_grpc.pb.go
```
Build will be broken at this point (Go code references `genv1.SensorServer` etc.); next tasks fix that.

### Task 16 — Rename `runtime/sensors.go` → `runtime/publishers.go` with full identifier rename

**Files:** `runtime/sensors.go` (deleted); `runtime/publishers.go` (new with renamed content).

**Steps:**

1. Read `runtime/sensors.go` end-to-end.
2. Create `runtime/publishers.go` with the content from `sensors.go` and apply these renames inside:
   - `StartWatchesForInstance` → `StartPublisherSubscriptionsForInstance` (current name has no `Sensor` prefix; verify at execute time via `rg 'StartWatchesForInstance' --type=go`)
   - `StopWatchesForInstance` → `StopPublisherSubscriptionsForInstance`
   - `ResyncSensorWatches` → `ResyncPublisherSubscriptions`
   - `SensorLifecycleDeps` → `PublisherLifecycleDeps`
   - Field `Sensors` (on lifecycle deps) → `Publishers`
   - `SensorClient` → `PublisherClient`
   - `SensorRegistry` → `PublisherRegistry`
   - `genv1.SensorClient`, `genv1.SensorServer` → `genv1.PublisherClient`, `genv1.PublisherServer`
   - All proto types: `StartWatchRequest` → `SubscribeRequest`, `StopWatchRequest` → `UnsubscribeRequest`, `ListWatchesResponse` → `ListSubscriptionsResponse`, `WatchDescriptor` → `PublisherSubscriptionDescriptor`, `SensorCapabilities` → `PublisherCapabilities`, `SensorKindCapability` → `PublisherKindCapability`
   - All `watch_id` → `publisher_subscription_id` (in JSON tags, log keys, error messages)
   - All `WatchID` → `PublisherSubscriptionID` (Go-side field names)
   - Log key `sensor.start.on_observation_marshal_failed` → `publisher.subscribe.marshal_failed`
   - Wrap the Subscribe RPC call with retry-with-backoff per spec §Architecture details §Rimsky-side Subscribe retry: 3 attempts, exp 200ms → 1.6s, jittered. The failure-mode-after-exhaustion is unchanged (warn-log + flip row to `state='failed'`).
3. Apply the `SensorWatchRow` → `PublisherSubscriptionRow` type rename for the persistence-row type used in this file.
4. Apply the routing-fields-inline change: the SubscribeRequest no longer carries `on_observation`; it carries `target_node` + `message_kind` directly as inline scalars. Persist these fields on the `PublisherSubscriptionRow` (populated from the template's `publishers:` block).
5. Delete `runtime/sensors.go`:
   ```
   rm runtime/sensors.go
   ```

**Verification:**
```
ls runtime/publishers.go
test ! -f runtime/sensors.go
```
Build will fail; other files still reference `SensorRegistry` etc. Fixed in subsequent tasks.

### Task 17 — Rename `runtime/clientiface/sensor.go` → `runtime/clientiface/publisher.go`

**Files:** `runtime/clientiface/sensor.go` (deleted); `runtime/clientiface/publisher.go` (new).

**Steps:**

1. Read `runtime/clientiface/sensor.go`. It defines the Go-side `SubscribeRequest` struct mirroring the proto type, plus interface bindings.
2. Create `runtime/clientiface/publisher.go` with renamed content:
   - File-level package doc: "Publisher protocol Go-side type mirrors."
   - `SensorClient` interface → `PublisherClient` interface.
   - `SensorRegistry` → `PublisherRegistry`.
   - `StartWatchRequest` → `SubscribeRequest` with fields `{PublisherSubscriptionID, InstanceID, Kind, ResolvedConfig, TargetNode, MessageKind}` (inline routing).
   - `StopWatchRequest` → `UnsubscribeRequest{PublisherSubscriptionID}`.
   - `ListWatchesResponse` → `ListSubscriptionsResponse{Subscriptions []PublisherSubscriptionDescriptor}`.
   - `WatchDescriptor` → `PublisherSubscriptionDescriptor`.
3. Delete `runtime/clientiface/sensor.go`.

**Verification:**
```
ls runtime/clientiface/publisher.go
test ! -f runtime/clientiface/sensor.go
```

### Task 18 — Rename `runtime/remote/sensor_client.go` → `runtime/remote/publisher_client.go`

**Files:** `runtime/remote/sensor_client.go` (deleted); `runtime/remote/publisher_client.go` (new).

**Steps:**

1. Read `runtime/remote/sensor_client.go` (the gRPC wire-mapper).
2. Create `runtime/remote/publisher_client.go` with renamed content: `SensorClient` struct → `PublisherClient`; method names map StartWatch → Subscribe, StopWatch → Unsubscribe, ListWatches → ListSubscriptions; gRPC client uses `genv1.NewPublisherClient`.
3. Delete `runtime/remote/sensor_client.go`.

**Verification:**
```
ls runtime/remote/publisher_client.go
test ! -f runtime/remote/sensor_client.go
```

### Task 19 — Rename `control/config/sensors.go` → `control/config/publishers.go`

**Files:** `control/config/sensors.go` (deleted); `control/config/publishers.go` (new).

**Steps:**

1. Read `control/config/sensors.go` end-to-end. It contains:
   - `sensorRegistryImpl` (a `map[name]SensorClient` adapter)
   - `validationRegistryImpl`
   - `dataProcessingRegistryImpl`
   - `DialSensorAndValidationRegistries` (the central dial function)
   - The `ProtocolSensor` constant location (or it's in `stores.go` — check)
2. Create `control/config/publishers.go` with renamed content:
   - `sensorRegistryImpl` → `publisherRegistryImpl`.
   - `DialSensorAndValidationRegistries` → `DialPublisherAndValidationRegistries`.
   - Function signature gains a third argument: `(ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig, publishers RemotePublishersConfig) (runtime.PublisherRegistry, runtime.ValidationRegistry, runtime.DataProcessingRegistry, error)`.
   - The dial path walks three peer maps now: `claim_producers`, `executors`, `publishers`. For each map entry, dispatch per advertised `protocols:` list. The new `publishers:` block is dispatched to the publisher registry; legacy dual-block discovery (sensor-advertising peer found under claim_producers/executors) is deprecated and emits a warn-log at startup if hit.
   - All references to `Sensor*` types → `Publisher*` types.
3. Delete `control/config/sensors.go`.

**Verification:**
```
ls control/config/publishers.go
test ! -f control/config/sensors.go
```

### Task 20 — Add `RemotePublishersConfig` to `control/config/stores.go`

**Files:** `control/config/stores.go`.

**Steps:**

1. Read `control/config/stores.go`. It defines `RemoteStoresConfig`, `RimskyConfig`, `LoadRimskyConfigYAML`, and the `ProtocolSensor` constant (likely line ~56).
2. Add new types and constants:
   ```go
   // RemotePublishersConfig is the parsed `publishers:` block from rimsky.yml.
   type RemotePublishersConfig struct {
       Publishers []PublisherEntry `yaml:"publishers"`
   }

   // PublisherEntry is one peer entry under the publishers: block.
   type PublisherEntry struct {
       Name      string   `yaml:"name"`
       Endpoint  string   `yaml:"endpoint"`
       Protocols []string `yaml:"protocols"`
       // Plus any TLS / auth fields paralleling StoreEntry — copy from
       // StoreEntry if it has more fields.
   }
   ```
3. Add a `Publishers RemotePublishersConfig` field on `RimskyConfig`.
4. Add the new `Publishers` field on the YAML wrapper struct in `LoadRimskyConfigYAML`.
5. Rename the protocol constant: `ProtocolSensor` → `ProtocolPublisher`. Sweep all callers (`rg 'ProtocolSensor' --type=go`) to update them.
6. In the startup validation `case ProtocolSensor, ProtocolValidation, ProtocolDataProcessing:` at line ~437 (or wherever — re-grep), update to `ProtocolPublisher`.
7. Add the new top-level `publishers:` block to the YAML acceptance list.

**Verification:**
```
go build ./...
```
Will likely still fail elsewhere; this task only ensures `control/config/stores.go` compiles.

### Task 21 — Update call site for `DialPublisherAndValidationRegistries`

**Files:** `control/config/controlapi.go` (or wherever `DialSensorAndValidationRegistries` was called; verify with grep).

**Steps:**

1. Run:
   ```
   rg 'DialSensorAndValidationRegistries' --type=go
   ```
2. At each call site, update the call to `DialPublisherAndValidationRegistries(...)` with the new three-argument signature: pass `cfg.Publishers` as the third argument.

**Verification:**
```
rg 'DialSensorAndValidationRegistries' --type=go | wc -l
```
Should return 0.

### Task 22 — Rename `appDeps.Sensors` → `appDeps.Publishers` across all consumers

**Files:** `control/controlapi/app.go`, `control/controlapi/instances.go`, plus any other reader (use grep to enumerate).

**Steps:**

1. Run:
   ```
   rg 'appDeps\.Sensors|AppDeps\.Sensors' --type=go
   ```
2. At each hit, rename:
   - The field declaration (in `runtime.AppDeps` struct, presumably `runtime/AppDeps` or similar) → `Publishers runtime.PublisherRegistry`.
   - All reader sites: `appDeps.Sensors` → `appDeps.Publishers`.

**Verification:**
```
rg 'appDeps\.Sensors|AppDeps\.Sensors' --type=go | wc -l
```
Should return 0.

### Task 23 — Schema baseline rewrite: `rimsky_sensor_watches` → `rimsky_publisher_subscriptions` (postgres)

**Files:** `foundation/persistence/postgres/migrations/001-baseline.sql`.

**Steps:**

1. Open the file. Find the existing `CREATE TABLE rimsky_sensor_watches (...)` block.
2. Replace it with the new shape from spec §Architecture details §Schema:
   ```sql
   CREATE TABLE rimsky_publisher_subscriptions (
       id              UUID NOT NULL,
       instance_id     UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
       publisher_name  TEXT NOT NULL,
       kind            TEXT NOT NULL,
       resolved_config JSONB NOT NULL,
       target_node     TEXT NOT NULL,
       message_kind    TEXT NOT NULL DEFAULT 'invalidate',
       started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
       state           TEXT NOT NULL CHECK (state IN ('active','failed','stopped')),
       PRIMARY KEY (publisher_name, id)
   );
   ```
3. Drop any old `rimsky_sensor_watches` index lines; replace with new index lines as needed (the renamed table doesn't need new indexes for the spec's stated query patterns — verify by grep against the rimsky_sensor_watches index lines that were dropped).
4. Replace every other reference to `rimsky_sensor_watches` in this file (e.g., FK references from other tables, comments) with `rimsky_publisher_subscriptions`.
5. Replace `sensor_name` column with `publisher_name`.
6. Drop the `on_observation` column line.
7. Drop the `last_observed_at` column line.
8. Add `target_node TEXT NOT NULL` and `message_kind TEXT NOT NULL DEFAULT 'invalidate'` (already shown in the CREATE TABLE above).

**Verification:**
```
go build ./...
```
The migration is embed-loaded via go:embed; build verifies the file is well-formed at compile time.

### Task 24 — Schema baseline rewrite: same for sqlite

**Files:** `foundation/persistence/sqlite/migrations/001-baseline.sql`.

**Steps:**

1. Mirror Task 23 for SQLite. SQLite-specific differences: `UUID` → `TEXT`; `TIMESTAMPTZ` → `TEXT`; `JSONB` → `TEXT`; `DEFAULT now()` → `DEFAULT (datetime('now'))`.

**Verification:**
```
go build ./...
```

### Task 25 — Rename persistence interface file: `foundation/persistence/sensor_watches.go` → `publisher_subscriptions.go`

**Files:** `foundation/persistence/sensor_watches.go` (deleted); `foundation/persistence/publisher_subscriptions.go` (new).

**Steps:**

1. Read `foundation/persistence/sensor_watches.go`.
2. Create `foundation/persistence/publisher_subscriptions.go` with renamed content:
   - File package doc references publisher-subscription.
   - `SensorWatchesTable` (current plural-form name; verify via `rg 'SensorWatchesTable' --type=go`) → `PublisherSubscriptionsTable`.
   - `SensorWatchRow` → `PublisherSubscriptionRow` with fields `{PublisherSubscriptionID, InstanceID, PublisherName, Kind, ResolvedConfig json.RawMessage, TargetNode, MessageKind, StartedAt time.Time, State string}`. Drop the old `OnObservation` + `LastObservedAt` fields.
   - All method signatures rename `SensorWatch` → `PublisherSubscription`; method names that referenced "Watch" rename accordingly (e.g., `ListActiveSensorWatchesForInstance` → `ListActivePublisherSubscriptionsForInstance` if such a method exists; check actual method list and rename consistently).
3. Update the `Tables` interface in `foundation/persistence/tables.go`: rename `SensorWatches() SensorWatchesTable` → `PublisherSubscriptions() PublisherSubscriptionsTable`.
4. Delete `foundation/persistence/sensor_watches.go`.

**Verification:**
```
ls foundation/persistence/publisher_subscriptions.go
test ! -f foundation/persistence/sensor_watches.go
cd foundation && go build ./...
```

### Task 26 — Rename persistence driver files (postgres + sqlite)

**Files:** `foundation/persistence/postgres/sensor_watches.go` (deleted); `foundation/persistence/postgres/publisher_subscriptions.go` (new); same for sqlite.

**Steps:**

1. Read `foundation/persistence/postgres/sensor_watches.go`.
2. Create `foundation/persistence/postgres/publisher_subscriptions.go` mirroring the rename pattern. SQL columns flip: `sensor_name` → `publisher_name`; `on_observation` and `last_observed_at` dropped; `target_node` and `message_kind` added. Internal struct field references rename to match the renamed `PublisherSubscriptionRow`.
3. Same for `foundation/persistence/sqlite/sensor_watches.go` → `publisher_subscriptions.go`.
4. Delete the old files.

**Verification:**
```
cd foundation && go build ./... && go test ./...
```

### Task 27 — Delete `OnObservationSpec` Go type + alias

**Files:** `foundation/spec/graphs.go`, `graph/node/template.go`.

**Steps:**

1. In `foundation/spec/graphs.go`, find the `OnObservationSpec` struct declaration (~line 88-99 per spec). Delete the entire struct definition.
2. In `graph/node/template.go:58`, delete the `type OnObservationSpec = spec.OnObservationSpec` alias line.

**Verification:**
```
rg 'OnObservationSpec' --type=go | wc -l
```
Should return 0.

### Task 28 — Rename `tpl.Sensors` → `tpl.Publishers` (template-DSL `sensors:` block rename)

**Files:** `foundation/spec/template.go`, `foundation/spec/graphs.go`, `runtime/validation_pipeline.go`, plus any test fixture using the `sensors:` YAML key.

**Steps:**

1. In `foundation/spec/template.go`, find `Sensors []SensorSpec` (line ~42). Rename:
   - Field name `Sensors` → `Publishers`.
   - Type `SensorSpec` → `PublisherSpec` (or whatever the analog is — verify by reading the struct definition; the inner struct fields rename too: `on_observation` block removed; `target_node` + `message_kind` become inline fields on `PublisherSpec`).
   - YAML tag `sensors,omitempty` → `publishers,omitempty`.
   - JSON tag `sensors,omitempty` → `publishers,omitempty`.
2. In `foundation/spec/graphs.go`, find any per-block parsing/canonicalization that references the `sensors:` block. Rename to `publishers:`. Also drop any `OnObservationSpec` references (already deleted in Task 27).
3. In `runtime/validation_pipeline.go:99`, find `tpl.Sensors`. Rename to `tpl.Publishers`. Iteration body may need adjustment if it referenced `OnObservation` fields — flatten to read `entry.TargetNode` + `entry.MessageKind` directly.
4. Run a sweep across YAML fixtures:
   ```
   rg -l 'sensors:' --type=yaml --type=go
   ```
   For each YAML file or in-Go string that declares a `sensors:` block on a template (NOT the unrelated `cfg:rimsky.yml::sensors:` which never existed at the top level), rename to `publishers:` and flatten the `on_observation:` substruct into inline `target_node:` + `message_kind:` keys at the same indent.

**Verification:**
```
rg 'tpl\.Sensors|TemplateSpec\.Sensors|SensorSpec\b' --type=go | wc -l
```
Should return 0.

### Task 29 — Template canonicalizer accepts `publishers:` key; rejects `sensors:` key

**Files:** `graph/template/canonical/jcs.go` (or wherever the canonicalizer's struct definition lives — verify with grep).

**Steps:**

1. The canonicalizer uses `dec.DisallowUnknownFields()` per `code:control/controlapi/templates.go:692`. After Task 28 renames the field to `Publishers` with the YAML tag `publishers`, the canonicalizer will automatically:
   - Accept `publishers:` blocks (they bind to the renamed field).
   - Reject `sensors:` blocks via the standard `json: unknown field "sensors"` error.
2. No bespoke canonicalizer code is needed. Verify by reading the canonicalizer once to confirm it doesn't have a separate sensors-block-specific code path.
3. `payload_template` rejection is also free: with `OnObservationSpec` deleted, `payload_template:` becomes an unknown field; the standard error covers it.

**Verification:**
```
rg 'sensors|payload_template' graph/template/canonical/ --type=go
```
Should show no special-case handling for either key.

### Task 30 — Rename `cmd/rimsky-sensor-conformance/` → `cmd/rimsky-publisher-conformance/`

**Files:** `cmd/rimsky-sensor-conformance/{main.go, main_test.go, checks.go, …}` (deleted); `cmd/rimsky-publisher-conformance/` (new with all files).

**Steps:**

1. Move the directory:
   ```
   mv cmd/rimsky-sensor-conformance cmd/rimsky-publisher-conformance
   ```
2. Inside the new directory, sweep file contents for renames:
   - `SensorClient` → `PublisherClient`
   - `StartWatch` → `Subscribe`
   - `StopWatch` → `Unsubscribe`
   - `ListWatches` → `ListSubscriptions`
   - `WatchDescriptor` → `PublisherSubscriptionDescriptor`
   - `watch_id` → `publisher_subscription_id`
   - The receiver path-matcher `/sensors/<id>/observations` → `/instances/<id>/messages`
   - The check-doc text describing the observation-route flow → describe the unified-messages flow
3. Add a `--instance-id` CLI flag to `main.go` so the fake-rimsky-endpoint setup can be parameterized.
4. Update test path-matchers in `main_test.go` from `/sensors/...` to `/instances/.../messages`.

**Verification:**
```
ls cmd/rimsky-publisher-conformance/main.go
test ! -d cmd/rimsky-sensor-conformance
go build ./cmd/rimsky-publisher-conformance
go test ./cmd/rimsky-publisher-conformance -count=1
```

### Task 31 — Add `sender_kind: "publisher"` + capability check to `handleCreateMessage`

**Files:** `control/controlapi/messages.go`.

**Steps:**

1. Find the request struct for `handleCreateMessage`. Add fields:
   - `Sender string` (or use whatever shape the handler expects today)
   - `SenderKind string`
   - `PublisherSubscriptionID string` — body field; alternative is to require it as a separate parameter.
2. In the handler logic, before the message insert:
   - Read the request's `SenderKind`. Default to `"operator"` if empty (preserves Stage 1 behavior for operator-side requests).
   - If `SenderKind == "publisher"`:
     - Require `PublisherSubscriptionID` is non-empty; if empty, return `400 Bad Request: "publisher_subscription_id required for sender_kind=publisher"`.
     - Lookup the publisher-subscription row: `SELECT publisher_name FROM rimsky_publisher_subscriptions WHERE id = $1 AND instance_id = $2 AND state = 'active'`.
     - If no row: return `403 Forbidden: "publisher-subscription not active for this instance"`.
     - Overwrite `body.Sender = row.PublisherName`. Don't trust the request's Sender field for publisher requests; derive from the authoritative row.
3. Persist the message with `sender_kind = "publisher"` (or `"operator"` per the default).

**Verification:**
```
go test ./control/controlapi/... -run TestCreateMessage_SenderKindPublisher -v
```
Test added in Task 32.

### Task 32 — Test capability-check rejection cases

**Files:** `control/controlapi/messages_test.go`.

**Steps:**

1. Add test cases:
   - `TestCreateMessage_SenderKindPublisherUnknownSubscriptionReturns403`: POST with `sender_kind: "publisher"` + `publisher_subscription_id: <random UUID>` → 403 Forbidden.
   - `TestCreateMessage_SenderKindPublisherCrossInstanceReturns403`: insert subscription for instance A; POST to instance B's messages endpoint with that subscription_id → 403.
   - `TestCreateMessage_SenderKindPublisherStoppedSubscriptionReturns403`: insert subscription, set `state='stopped'`, POST → 403.
   - `TestCreateMessage_SenderKindPublisherMissingSubscriptionIDReturns400`: POST with `sender_kind: "publisher"` and empty `publisher_subscription_id` → 400.
   - `TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds`: insert active subscription; POST with matching subscription_id → 201, message persisted with sender = subscription's `publisher_name`.

**Verification:**
```
go test ./control/controlapi/... -run 'TestCreateMessage_SenderKindPublisher' -v
```

### Task 33 — Delete `control/controlapi/sensors.go` + `sensors_test.go`

**Files:** `control/controlapi/sensors.go` (deleted); `control/controlapi/sensors_test.go` (deleted).

**Steps:**

1. ```
   rm control/controlapi/sensors.go
   rm control/controlapi/sensors_test.go
   ```

**Verification:**
```
test ! -f control/controlapi/sensors.go
test ! -f control/controlapi/sensors_test.go
```

### Task 34 — Remove `registerSensorObservationsRoutes` call from `app.go`

**Files:** `control/controlapi/app.go`.

**Steps:**

1. Find the line `registerSensorObservationsRoutes(rr, deps)` (around line 195 per spec).
2. Delete the entire line.
3. If there's a now-unused import on `sensors.go` symbols, `goimports` will catch it; manually verify no dangling imports remain.

**Verification:**
```
rg 'registerSensorObservationsRoutes' --type=go | wc -l
```
Should return 0.

### Task 35 — Bundled sensor cutover: `sensors/sensor-cron/`

**Files:** `sensors/sensor-cron/main.go`, `sensors/sensor-cron/sensor.go`, `sensors/sensor-cron/sensor_test.go`, `sensors/sensor-cron/multi_replica_test.go`.

**Steps:**

1. In `main.go`: register `genv1.PublisherServer` (was `SensorServer`):
   ```go
   genv1.RegisterPublisherServer(srv, svc)
   ```
2. In `sensor.go`:
   - The `SensorService` struct stays named `SensorService` (sensor-internal vocabulary is fine).
   - `StartWatch` method → `Subscribe`. Accepts `*genv1.SubscribeRequest` with inline `target_node` + `message_kind`. Persist these fields on the `Watch` struct (in-memory map entry).
   - `StopWatch` → `Unsubscribe`.
   - `ListWatches` → `ListSubscriptions`. Returns `*genv1.ListSubscriptionsResponse{Subscriptions: []*genv1.PublisherSubscriptionDescriptor{...}}`.
   - `Capabilities` returns `*genv1.PublisherCapabilities` (was `SensorCapabilities`).
   - At fire time, in `fireOne` (or equivalent), build the message envelope per spec §Publisher Subscribe payload + message-envelope shape:
     ```json
     {
       "kind": w.MessageKind,
       "target": w.TargetNode,
       "payload": <raw observation JSON>,
       "sender": "sensor-cron",
       "sender_kind": "publisher",
       "publisher_subscription_id": w.WatchID,
       "idempotency_key": "<cron-specific key>"
     }
     ```
     The idempotency key for sensor-cron: `fmt.Sprintf("%s+%s", w.WatchID, w.NextFireAt.UTC().Format(time.RFC3339))` (publisher-subscription-id + fire-window).
   - URL-construction site at line 246 (per spec): change from `/sensors/<watch_id>/observations` to `/instances/<instance_id>/messages`.
   - POST with `Idempotency-Key: <same>` HTTP header.
   - Update file-level docstring at line 7: rewrite to describe the new POST target.
   - Update docstring at lines 17-19: replace "at most one extra fire per restart per watch" with "at most one MISSED fire per restart per publisher-subscription".
   - Sensor-internal `Watch` struct can stay named `Watch` (sensor-local; not on the wire).
3. In `sensor_test.go`: update the fake-receiver path-matcher at line 125 from `/sensors/w1/observations` to `/instances/<instance_id>/messages` shape. Update test fixtures that construct `genv1.StartWatchRequest` to use `genv1.SubscribeRequest` with inline routing fields.
4. In `multi_replica_test.go`: rewrite the docstring per spec § Modified (drop the "When the advisory-lock implementation lands" wording; reflect that single-replica is the v1 contract per `concept:replica`).

**Verification:**
```
go build ./sensors/sensor-cron && go test ./sensors/sensor-cron -count=1
```

### Task 36 — Bundled sensor cutover: `sensors/sensor-http/`

**Files:** `sensors/sensor-http/main.go`, `sensors/sensor-http/sensor.go`, `sensors/sensor-http/sensor_test.go`.

**Steps:**

1. Apply the same pattern as Task 35:
   - `main.go`: register `PublisherServer`.
   - `sensor.go`: rename method names (Subscribe / Unsubscribe / ListSubscriptions); accept inline `target_node` + `message_kind`; build message envelope at fire time; idempotency key = `subscription_id + body_sha256`; URL construction at line 356 swaps to unified path.
   - File-level docstring at line 8 swap.
   - `sensor_test.go`: path-matcher at line 124 swap; fixtures updated.

**Verification:**
```
go build ./sensors/sensor-http && go test ./sensors/sensor-http -count=1
```

### Task 37 — Bundled sensor cutover: `sensors/sensor-object-store/`

**Files:** `sensors/sensor-object-store/main.go`, `sensors/sensor-object-store/sensor.go`, `sensors/sensor-object-store/sensor_test.go`.

**Steps:**

1. Same pattern. URL construction at line 333; idempotency key = `subscription_id + object_etag`. No file-level docstring for this sensor cites the old route; the URL site is the only hit.

**Verification:**
```
go build ./sensors/sensor-object-store && go test ./sensors/sensor-object-store -count=1
```

### Task 38 — Bundled sensor cutover: `sensors/sensor-webhook/`

**Files:** `sensors/sensor-webhook/main.go`, `sensors/sensor-webhook/sensor.go`, `sensors/sensor-webhook/sensor_test.go`.

**Steps:**

1. Same pattern. URL construction at line 289; idempotency key = `subscription_id + idempotency_header_value`. File-level docstring at `main.go:6` swap.

**Verification:**
```
go build ./sensors/sensor-webhook && go test ./sensors/sensor-webhook -count=1
```

### Task 39 — Rewrite scenario test: `test/scenarios/sensor/observation_routing_test.go` → `message_routing_test.go`

**Files:** `test/scenarios/sensor/observation_routing_test.go` (deleted); `test/scenarios/sensor/message_routing_test.go` (new).

**Steps:**

1. Read `observation_routing_test.go` end-to-end to understand current coverage.
2. Create `message_routing_test.go` driving the unified-route path. Tests:
   - Happy path: sensor fires; observation lands as a message envelope on `route:POST /instances/.../messages`.
   - All four capability-check rejection cases from Task 32 (but at scenario-level: drive a real publisher binary trying to bypass).
3. Delete the old file.

**Verification:**
```
ls test/scenarios/sensor/message_routing_test.go
test ! -f test/scenarios/sensor/observation_routing_test.go
go test ./test/scenarios/sensor/... -run TestMessageRouting -count=1
```

### Task 40 — Touch `test/scenarios/sensor/lifecycle_start_stop_test.go`

**Files:** `test/scenarios/sensor/lifecycle_start_stop_test.go`.

**Steps:**

1. Rename usage of `StartWatch` → `Subscribe`, `StopWatch` → `Unsubscribe`, `ListWatches` → `ListSubscriptions` throughout the test.
2. Update any `Watch` struct construction to populate `target_node` + `message_kind` as inline fields.

**Verification:**
```
go test ./test/scenarios/sensor/... -run TestLifecycle -count=1
```

### Task 41 — Update `test/scenarios/messages/sensor_invalidate_to_cascade_test.go`

**Files:** `test/scenarios/messages/sensor_invalidate_to_cascade_test.go`.

**Steps:**

1. The file's docstring at line 8 cites `POST /sensors/{watch_id}/observations`. Rewrite to cite `POST /instances/{id}/messages` with `sender_kind: "publisher"`.
2. The test body should already exercise the message-cascade flow; if it constructs old-route URLs, swap to the unified path.

**Verification:**
```
go test ./test/scenarios/messages/... -run TestSensorInvalidate -count=1
```

### Task 42 — Update `test/smoke/data_platform_smoke_test.go` (lines 209-315)

**Files:** `test/smoke/data_platform_smoke_test.go`.

**Steps:**

1. Find the block at lines 209-315 that constructs `pushURL := fmt.Sprintf("%s/sensors/%s/observations", rimsky.URL, watchID)` (line 290 per spec).
2. Migrate the entire block to:
   - Construct `pushURL := fmt.Sprintf("%s/instances/%s/messages", rimsky.URL, instanceID)`.
   - Build the message envelope with `sender_kind: "publisher"`, `publisher_subscription_id: <id>`, `payload: <observation>`.
   - Set `Idempotency-Key` header.
   - Assert the response is 201 (or 200 on replay).

**Verification:**
```
go test ./test/smoke -run TestDataPlatformSmoke -count=1
```

### Task 43 — Source-doc cleanup across runtime + graph

**Files:** `runtime/message_delivery.go`, `graph/scheduler/scheduler.go`.

**Steps:**

1. In `runtime/message_delivery.go:11-12`, find the file-level docstring citing `POST /sensors/{watch_id}/observations`. Rewrite to cite the unified path.
2. In `graph/scheduler/scheduler.go:13`, find the same citation in its docstring. Rewrite.

**Verification:**
```
rg 'POST /sensors/' runtime/ graph/ --type=go | grep -v '_test.go' | wc -l
```
Should return 0 (test files may keep historical references in clearly-historical context; non-test source must be clean).

### Task 44 — Run full Go build + lint after the rename

**Files:** none modified.

**Steps:**

1. ```
   make build-all && make lint && make license-lint
   ```
2. If any failures: each one points at a missed rename site. Address them by reading the error message, finding the file, applying the rename, repeating.

**Verification:** all three exit 0.

### Task 45 — Run full Go test suite + race

**Files:** none modified.

**Steps:**

1. ```
   make test-all
   go test ./runtime/... ./control/... ./foundation/... ./subscribers/... ./sensors/... -race -count=1
   ```

**Verification:** both exit 0. Test flakes (testcontainers port races, etc.) may require one re-run for confirmation.

### Task 46 — Quiescence checkpoint: publisher unification clean

**Files:** none modified.

**Steps:**

1. ```
   make build-all && make test-all && make lint && make license-lint
   cd dashboards/rimsky-dashboard && npm test && npm run build && cd -
   ```
2. All commands exit 0.
3. Sanity greps:
   ```
   rg 'SensorRegistry|SensorWatchTable|SensorWatchRow|StartWatch|StopWatch|ListWatches|WatchDescriptor' --type=go | wc -l
   rg 'rimsky_sensor_watches' --type=go | wc -l
   rg 'OnObservationSpec' --type=go | wc -l
   rg 'POST /sensors/' --type=go | grep -v '_test.go' | wc -l
   ```
   All four should return 0 (or only intentional-historical context — verify by inspecting each remaining hit).

**Verification:** all four greps return 0; all commands exit 0.

---

## State persistence + deploy + docs

### Task 47 — Add state DB module for sensor-http

**Files:** `sensors/sensor-http/state_db.go` (new).

**Steps:**

1. Create the file in `package main` (NOT `package persistence`):
   ```go
   package main

   import (
       "context"
       "database/sql"
       "fmt"
       "os"
       "time"

       _ "github.com/jackc/pgx/v5/stdlib"
   )

   // stateDB is sensor-http's per-binary state persistence.
   // Configured via env RIMSKY_SENSOR_HTTP_STATE_DSN.
   // If the env var is empty, in-memory-only mode is used.
   type stateDB struct {
       db *sql.DB
   }

   func openStateDB(ctx context.Context) (*stateDB, error) {
       dsn := os.Getenv("RIMSKY_SENSOR_HTTP_STATE_DSN")
       if dsn == "" {
           return nil, nil
       }
       db, err := sql.Open("pgx", dsn)
       if err != nil {
           return nil, fmt.Errorf("open state db: %w", err)
       }
       if err := db.PingContext(ctx); err != nil {
           return nil, fmt.Errorf("ping state db: %w", err)
       }
       s := &stateDB{db: db}
       if err := s.bootstrap(ctx); err != nil {
           return nil, fmt.Errorf("bootstrap state db: %w", err)
       }
       return s, nil
   }

   func (s *stateDB) bootstrap(ctx context.Context) error {
       const schema = `
           CREATE TABLE IF NOT EXISTS sensor_http_state (
               publisher_subscription_id TEXT PRIMARY KEY,
               instance_id               TEXT NOT NULL,
               url                       TEXT NOT NULL,
               poll_interval             TEXT NOT NULL,
               match_status              TEXT NOT NULL,
               match_json_key            TEXT,
               match_json_val            TEXT,
               target_node               TEXT NOT NULL,
               message_kind              TEXT NOT NULL,
               last_poll_at              TIMESTAMPTZ,
               last_hash                 TEXT,
               started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
           );
       `
       _, err := s.db.ExecContext(ctx, schema)
       return err
   }

   // Insert, Get, UpdateLastHash, Delete, ListAll methods follow.
   // Implement each per the sensor's actual state-management needs.
   ```
2. Wire `openStateDB` into `main.go`: if non-nil, hand it to the `SensorService` constructor; `SensorService` uses it to persist subscriptions on Subscribe / Unsubscribe and to update `last_hash` after each successful POST.
3. If DSN is empty, the service runs in pure in-memory mode (current behavior).

**Verification:**
```
go build ./sensors/sensor-http
```

### Task 48 — Add state DB module for sensor-object-store

**Files:** `sensors/sensor-object-store/state_db.go` (new).

**Steps:**

1. Mirror Task 47 for object-store. Schema:
   ```sql
   CREATE TABLE IF NOT EXISTS sensor_object_store_state (
       publisher_subscription_id TEXT PRIMARY KEY,
       instance_id               TEXT NOT NULL,
       backend                   TEXT NOT NULL,
       bucket                    TEXT NOT NULL,
       prefix                    TEXT NOT NULL,
       poll_interval             TEXT NOT NULL,
       watermark_field           TEXT NOT NULL,
       target_node               TEXT NOT NULL,
       message_kind              TEXT NOT NULL,
       last_poll_at              TIMESTAMPTZ,
       watermark_name            TEXT,
       watermark_time            TIMESTAMPTZ,
       started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   ```
2. Env var: `RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN`.

**Verification:**
```
go build ./sensors/sensor-object-store
```

### Task 49 — Add state DB module for sensor-webhook

**Files:** `sensors/sensor-webhook/state_db.go` (new).

**Steps:**

1. Mirror for webhook. Schema:
   ```sql
   CREATE TABLE IF NOT EXISTS sensor_webhook_state (
       publisher_subscription_id TEXT PRIMARY KEY,
       instance_id               TEXT NOT NULL,
       path_prefix               TEXT NOT NULL,
       idempotency_header        TEXT,
       target_node               TEXT NOT NULL,
       message_kind              TEXT NOT NULL,
       last_idempotency_key      TEXT,
       last_seen_at              TIMESTAMPTZ,
       started_at                TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   ```
2. Env var: `RIMSKY_SENSOR_WEBHOOK_STATE_DSN`.

**Verification:**
```
go build ./sensors/sensor-webhook
```

### Task 50 — Add (optional) state DB module for sensor-cron

**Files:** `sensors/sensor-cron/state_db.go` (new).

**Steps:**

1. Add the env-var plumbing (`RIMSKY_SENSOR_CRON_STATE_DSN`); when set, persist watches. When empty, in-memory mode is the default. Per spec, sensor-cron's state IS reconstructible; the in-memory cost is "at most one MISSED fire per restart per publisher-subscription" — acceptable.
2. Schema:
   ```sql
   CREATE TABLE IF NOT EXISTS sensor_cron_state (
       publisher_subscription_id TEXT PRIMARY KEY,
       instance_id               TEXT NOT NULL,
       cron_expr                 TEXT NOT NULL,
       target_node               TEXT NOT NULL,
       message_kind              TEXT NOT NULL,
       next_fire_at              TIMESTAMPTZ NOT NULL,
       started_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
       last_fire_at              TIMESTAMPTZ
   );
   ```

**Verification:**
```
go build ./sensors/sensor-cron
```

### Task 51 — Add state-persistence tests for sensor-http, sensor-object-store, sensor-webhook

**Files:** `sensors/sensor-http/state_db_test.go`, `sensors/sensor-object-store/state_db_test.go`, `sensors/sensor-webhook/state_db_test.go`.

**Steps:**

1. For each sensor, write a test using testcontainers Postgres. The existing pgtest helper lives at `foundation/internal/pgtest/pgtest.go`, but `foundation-internal-isolation` depguard rule (`.golangci.yml:39-46`) blocks `github.com/fallguyconsulting/rimsky/foundation/internal` from external packages. Either extend the depguard's allow-list to include the sensor packages (preferred — small allow-list edit alongside the `pgx-isolation` work in Task 52), OR write a minimal local testcontainers helper in each sensor package (~40 lines). Recommend extending depguard. Mirror the fixture-pattern shape from `foundation/internal/pgtest/pgtest.go`:
   - Open the state DB.
   - Insert a subscription.
   - Simulate a process restart by closing + reopening the DB.
   - Load subscriptions; assert the inserted subscription is present.
   - For sensor-http: also assert `last_hash` persists across restart.
   - For sensor-object-store: also assert watermark cursor persists.
   - For sensor-webhook: also assert `last_idempotency_key` persists.

**Verification:**
```
go test ./sensors/sensor-http -run TestStateDB -count=1
go test ./sensors/sensor-object-store -run TestStateDB -count=1
go test ./sensors/sensor-webhook -run TestStateDB -count=1
```

### Task 52 — Update depguard allow-list for new pgx imports

**Files:** `.golangci.yml`.

**Steps:**

1. Find the `pgx-isolation` depguard rule. Extend its allow-list to include:
   - `github.com/fallguyconsulting/rimsky/sensors/sensor-http`
   - `github.com/fallguyconsulting/rimsky/sensors/sensor-object-store`
   - `github.com/fallguyconsulting/rimsky/sensors/sensor-webhook`
   - (And `sensor-cron` if you wired it.)
2. Find the `foundation-internal-isolation` depguard rule (lines ~39-46 of `.golangci.yml`). Extend its allow-list to include the same sensor packages so they can import `foundation/internal/pgtest` from their `_test.go` files for testcontainers fixtures.

**Verification:**
```
make lint
```

### Task 53 — Sensor-cron docstring fix

**Files:** `sensors/sensor-cron/sensor.go` (lines 17-19).

**Steps:**

1. Find the pre-existing docstring text at lines 17-19: "at most one extra fire per restart per watch" (or near that phrasing).
2. Rewrite to: "at most one MISSED fire per restart per publisher-subscription" (per spec § Open items #3).

**Verification:**
```
rg 'extra fire per restart' sensors/sensor-cron/ | wc -l
```
Should return 0.

### Task 54 — Sensor-cron `multi_replica_test.go` docstring rewrite

**Files:** `sensors/sensor-cron/multi_replica_test.go`.

**Steps:**

1. Find the test file's docstring.
2. Drop wording like "When the advisory-lock implementation lands" (per spec; won't land per Tension 1 resolution).
3. Rewrite to reflect that single-replica is the v1 contract per `concept:replica`.
4. Update any in-test references to `Watch` / `watch state` to `publisher-subscription` vocabulary where they describe operator-facing concepts; sensor-internal variable names (`Watch` struct, etc.) can stay.

**Verification:**
```
go test ./sensors/sensor-cron -run TestMultiReplica -count=1
```

### Task 55 — Dockerfile for sensor-cron

**Files:** `sensors/sensor-cron/Dockerfile.sensor-cron` (new).

**Steps:**

1. Read `stores/postgres/Dockerfile.postgres` to mirror its multi-stage pattern.
2. Create the Dockerfile:
   ```dockerfile
   # Multi-stage build for sensor-cron.
   FROM golang:1.25-alpine AS builder
   WORKDIR /src
   COPY . .
   RUN CGO_ENABLED=0 go build -o /out/sensor-cron ./sensors/sensor-cron

   FROM gcr.io/distroless/static:nonroot
   COPY --from=builder /out/sensor-cron /usr/local/bin/sensor-cron
   USER nonroot:nonroot
   ENTRYPOINT ["/usr/local/bin/sensor-cron"]
   ```
3. NOT `deploy/Dockerfile.go-base` — the sensor binaries have `main.go` at the package root, not under `cmd/`. Per the spec.

**Verification:**
```
docker build -f sensors/sensor-cron/Dockerfile.sensor-cron -t rimsky/sensor-cron:test .
```

### Task 56 — Dockerfiles for sensor-http, sensor-object-store, sensor-webhook

**Files:** `sensors/sensor-http/Dockerfile.sensor-http` (new), `sensors/sensor-object-store/Dockerfile.sensor-object-store` (new), `sensors/sensor-webhook/Dockerfile.sensor-webhook` (new).

**Steps:**

1. Mirror Task 55 for each sensor, swapping the build path.

**Verification:**
```
docker build -f sensors/sensor-http/Dockerfile.sensor-http -t rimsky/sensor-http:test .
docker build -f sensors/sensor-object-store/Dockerfile.sensor-object-store -t rimsky/sensor-object-store:test .
docker build -f sensors/sensor-webhook/Dockerfile.sensor-webhook -t rimsky/sensor-webhook:test .
```

### Task 57 — Extend `deploy/build-images.sh`

**Files:** `deploy/build-images.sh`.

**Steps:**

1. After the existing store/executor builds, add:
   ```bash
   docker build -f sensors/sensor-cron/Dockerfile.sensor-cron -t rimsky/sensor-cron:$VERSION -t rimsky/sensor-cron:latest .
   docker build -f sensors/sensor-http/Dockerfile.sensor-http -t rimsky/sensor-http:$VERSION -t rimsky/sensor-http:latest .
   docker build -f sensors/sensor-object-store/Dockerfile.sensor-object-store -t rimsky/sensor-object-store:$VERSION -t rimsky/sensor-object-store:latest .
   docker build -f sensors/sensor-webhook/Dockerfile.sensor-webhook -t rimsky/sensor-webhook:$VERSION -t rimsky/sensor-webhook:latest .
   ```
2. Update the image-count echo line at the end (currently "Built 11 images"; bump to 15).

**Verification:**
```
deploy/build-images.sh
```

### Task 58 — Add sensor services to `deploy/docker-compose.yml`

**Files:** `deploy/docker-compose.yml`.

**Steps:**

1. Add four new services, each minimally configured:
   ```yaml
   sensor-cron:
     image: rimsky/sensor-cron:latest
     environment:
       RIMSKY_ENDPOINT: http://control-api:8080
       RIMSKY_SENSOR_CRON_HOST: 0.0.0.0
       RIMSKY_SENSOR_CRON_PORT: "9081"
     depends_on:
       - control-api
     ports:
       - "9081:9081"

   sensor-http:
     image: rimsky/sensor-http:latest
     environment:
       RIMSKY_ENDPOINT: http://control-api:8080
       RIMSKY_SENSOR_HTTP_STATE_DSN: postgres://rimsky:rimsky@postgres:5432/sensor_http_state?sslmode=disable
     depends_on:
       - control-api
       - postgres

   sensor-object-store:
     image: rimsky/sensor-object-store:latest
     environment:
       RIMSKY_ENDPOINT: http://control-api:8080
       RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN: postgres://rimsky:rimsky@postgres:5432/sensor_object_store_state?sslmode=disable
     depends_on:
       - control-api
       - postgres

   sensor-webhook:
     image: rimsky/sensor-webhook:latest
     environment:
       RIMSKY_ENDPOINT: http://control-api:8080
       RIMSKY_SENSOR_WEBHOOK_STATE_DSN: postgres://rimsky:rimsky@postgres:5432/sensor_webhook_state?sslmode=disable
     depends_on:
       - control-api
       - postgres
   ```
2. Adjust DSN values if the compose stack has different Postgres credentials or DB-name conventions.

**Verification:**
```
docker compose -f deploy/docker-compose.yml config
```
Should parse without errors.

### Task 59 — Add helm chart deployment + service for sensor-cron

**Files:** `deploy/kubernetes/rimsky-chart/templates/deployment-sensor-cron.yaml` (new), `deploy/kubernetes/rimsky-chart/templates/service-sensor-cron.yaml` (new).

**Steps:**

1. Read `deploy/kubernetes/rimsky-chart/templates/deployment-http-node.yaml` and `service-http-node.yaml` as the reference pattern.
2. Create `deployment-sensor-cron.yaml`:
   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: {{ include "rimsky.fullname" . }}-sensor-cron
     labels:
       {{- include "rimsky.labels" . | nindent 4 }}
       app.kubernetes.io/component: sensor-cron
   spec:
     replicas: {{ .Values.sensorCron.replicas | default 1 }}
     selector:
       matchLabels:
         {{- include "rimsky.selectorLabels" . | nindent 6 }}
         app.kubernetes.io/component: sensor-cron
     template:
       metadata:
         labels:
           {{- include "rimsky.selectorLabels" . | nindent 8 }}
           app.kubernetes.io/component: sensor-cron
       spec:
         containers:
         - name: sensor-cron
           image: rimsky/sensor-cron:{{ .Values.sensorCron.imageTag | default "latest" }}
           env:
           - name: RIMSKY_ENDPOINT
             value: {{ .Values.rimskyEndpoint | quote }}
           - name: RIMSKY_SENSOR_CRON_HOST
             value: "0.0.0.0"
           - name: RIMSKY_SENSOR_CRON_PORT
             value: "9081"
           {{- with .Values.sensorCron.stateDSN }}
           - name: RIMSKY_SENSOR_CRON_STATE_DSN
             value: {{ . | quote }}
           {{- end }}
           ports:
           - name: grpc
             containerPort: 9081
   ```
3. Create `service-sensor-cron.yaml`:
   ```yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: {{ include "rimsky.fullname" . }}-sensor-cron
   spec:
     type: ClusterIP
     selector:
       {{- include "rimsky.selectorLabels" . | nindent 4 }}
       app.kubernetes.io/component: sensor-cron
     ports:
     - name: grpc
       port: 9081
       targetPort: grpc
   ```

**Verification:**
```
helm lint deploy/kubernetes/rimsky-chart
```

### Task 60 — Helm chart deployment + service for sensor-http, sensor-object-store, sensor-webhook

**Files:** 6 new YAML files (3 deployments + 3 services).

**Steps:**

1. Mirror Task 59 for each of the three remaining sensors. Update port numbers if applicable. For each, include the corresponding state-DSN env var.

**Verification:**
```
helm lint deploy/kubernetes/rimsky-chart
```

### Task 61 — Update helm chart `values.yaml` with sensor defaults

**Files:** `deploy/kubernetes/rimsky-chart/values.yaml`.

**Steps:**

1. Add per-sensor sections:
   ```yaml
   sensorCron:
     replicas: 1
     imageTag: latest
     stateDSN: ""  # empty → in-memory mode
   sensorHttp:
     replicas: 1
     imageTag: latest
     stateDSN: ""
   sensorObjectStore:
     replicas: 1
     imageTag: latest
     stateDSN: ""
   sensorWebhook:
     replicas: 1
     imageTag: latest
     stateDSN: ""
   ```
2. Also add a `rimskyEndpoint:` top-level key if it doesn't already exist (used by all sensors' deployment templates).

**Verification:**
```
helm lint deploy/kubernetes/rimsky-chart
helm template deploy/kubernetes/rimsky-chart | grep -c 'sensor-cron'
```
The grep should find sensor-cron templating in the rendered output.

### Task 62 — Add four publisher entries to `deploy/rimsky.yml`

**Files:** `deploy/rimsky.yml`.

**Steps:**

1. Read the current file to understand the structure.
2. Add a new top-level `publishers:` block (alongside `claim_producers:` + `executors:`):
   ```yaml
   publishers:
   - name: sensor-cron
     endpoint: "sensor-cron:9081"
     protocols: [publisher]
   - name: sensor-http
     endpoint: "sensor-http:9082"
     protocols: [publisher]
   - name: sensor-object-store
     endpoint: "sensor-object-store:9083"
     protocols: [publisher]
   - name: sensor-webhook
     endpoint: "sensor-webhook:9084"
     protocols: [publisher]
   ```

**Verification:**

Validate the YAML parses against `code:control/config.LoadRimskyConfigYAML`. The most direct check is a unit test or a one-shot Go invocation:
```
go test ./control/config/... -run TestLoadRimskyConfigYAML -count=1
```
If the existing test suite doesn't cover the `publishers:` block path yet, add a small subtest in `control/config/config_test.go` (or wherever loader tests live) that exercises a minimal `publishers:` block. The loader will return a clear error if the YAML schema doesn't match `RimskyConfig`.

### Task 63 — Rename feature-index.md rows + sweep stale references

**Files:** `feature-index.md`.

**Steps:**

1. At line ~78: rename `rimsky-sensor-conformance` → `rimsky-publisher-conformance`.
2. At line ~81: **delete the row entirely** (stale "Reference sensor binary (cron firing)" pointing at the non-existent `cmd/rimsky-sensor-cron/` directory).
3. Sweep the file for any row citing `concept:subscription` — update to `concept:node-subscription`.
4. Sweep for `rimsky_sensor_watches` — update to `rimsky_publisher_subscriptions`.

**Verification:**
```
rg 'rimsky-sensor-cron|rimsky-sensor-conformance|rimsky_sensor_watches' feature-index.md | wc -l
```
Should return 0.

### Task 64 — Delete orphan `cmd/rimsky-sensor-cron/` directory

**Files:** `cmd/rimsky-sensor-cron/` (deleted).

**Steps:**

1. ```
   rmdir cmd/rimsky-sensor-cron
   ```
2. If the directory isn't empty, list its contents and decide per-file; per spec, it should be empty.

**Verification:**
```
test ! -d cmd/rimsky-sensor-cron
```

### Task 65 — Rename `licensing.yml` entry

**Files:** `licensing.yml`.

**Steps:**

1. Find the entry at line ~47 referencing `cmd/rimsky-sensor-conformance/`.
2. Rename to `cmd/rimsky-publisher-conformance/`.

**Verification:**
```
make license-lint
```

### Task 66 — Write new internal concept doc: `publisher.md`

**Files:** `.ok-planner/design/concepts/publisher.md` (new).

**Steps:**

1. Create the file per spec §Concept doc changes §New: `concept:publisher`. Definition: A publisher is a peer service that pushes messages into rimsky. Publishers implement the `proto:publisher.proto::Publisher` protocol (4 verbs: `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`). Publishers are out-of-process, gRPC-addressed, peer-services in the same trust perimeter as executors and claim-producers.
2. Body should match the structure of existing concept docs (Definition / Purpose / Boundaries / Invariants / Notes sections; consult `.ok-planner/design/concepts/sensor.md` for reference, but write fresh content).
3. Add cross-references to `concept:sensor`, `concept:publisher-subscription`, `concept:message`.

**Verification:**
```
ls .ok-planner/design/concepts/publisher.md
```

### Task 67 — Write new internal concept doc: `publisher-subscription.md`

**Files:** `.ok-planner/design/concepts/publisher-subscription.md` (new).

**Steps:**

1. Create the file. Definition per spec §Concept doc changes §New: `concept:publisher-subscription`.
2. The naming note in the body: explain why NOT named `subscription.md` (the slug was already taken).
3. The spec lists `concept:sensor-watch` as folded-into-publisher-subscription, but `.ok-planner/design/concepts/sensor-watch.md` does not currently exist (verify with `ls .ok-planner/design/concepts/sensor-watch.md` — file is absent). The "folding" is conceptual: write the publisher-subscription content fresh per the spec's definition section, drawing on the spec's schema description and the rimsky-side lifecycle (Subscribe / Unsubscribe / ResyncPublisherSubscriptions). No file to read; no file to delete.

**Verification:**
```
ls .ok-planner/design/concepts/publisher-subscription.md
```

### Task 68 — Rename internal concept doc: `subscription.md` → `node-subscription.md`

**Files:** `.ok-planner/design/concepts/subscription.md` (deleted); `.ok-planner/design/concepts/node-subscription.md` (new).

**Steps:**

1. ```
   git mv .ok-planner/design/concepts/subscription.md .ok-planner/design/concepts/node-subscription.md
   ```
   (Or plain `mv` if `.ok-planner/` isn't git-tracked; check first.)
2. Edit the renamed file to add a leading clarifier paragraph at the top:
   > "This concept describes the **receiver-side** template-DSL subscription declared in a node's `subscribes:` block — a node's wait-set on a sibling's terminal-changed signal. The separate concept `concept:publisher-subscription` describes the **publisher-side** binding between a publisher peer and a rimsky instance. They are orthogonal."
3. Cross-reference sweep — update every `concept:subscription` citation in concept docs to `concept:node-subscription`:
   - `invalidate.md:41`, `node.md:42`, `last-outcome.md:49`, `message.md:30`, `lifecycle-handler.md:66`, `cascade.md:48`, `wait-set.md:32`, `_retired/on-event-handler.md:11`.
   - Plus any tension files surfaced by re-grep at execute time:
     ```
     rg 'concept:subscription' .ok-planner/design/
     ```

**Verification:**
```
test ! -f .ok-planner/design/concepts/subscription.md
ls .ok-planner/design/concepts/node-subscription.md
rg 'concept:subscription\b' .ok-planner/design/ | grep -v 'concept:subscription[\-_]' | wc -l
```
Last grep should return 0.

### Task 69 — Sweep in-code `@concept: subscription` annotations

**Files:** `foundation/`, `graph/`, `runtime/`, `control/` (any file with `@concept: subscription`).

**Steps:**

1. ```
   rg '@concept: subscription\b' --type=go
   ```
2. At each hit, change to `@concept: node-subscription`.

**Verification:**
```
rg '@concept: subscription\b' --type=go | wc -l
```
Should return 0.

### Task 70 — Write new internal concept doc: `replica.md`

**Files:** `.ok-planner/design/concepts/replica.md` (new).

**Steps:**

1. Create the file per spec §Concept doc changes §New: `concept:replica`.
2. Definition: A replica is one running pod/process of a binary, behind a load-balancing layer in the deployment tier.
3. Boundaries section per spec.
4. Cross-references: add a "replica behavior" subsection to `concept:executor`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:claim-producer` pointing back to this concept.

**Verification:**
```
ls .ok-planner/design/concepts/replica.md
```

### Task 71 — Verify `sensor-watch.md` is absent (no-op cleanup)

**Files:** none.

**Steps:**

1. The spec lists `concept:sensor-watch` as deleted/folded; the file `.ok-planner/design/concepts/sensor-watch.md` is already absent today (verified pre-plan). This task is a no-op verification that no stale file exists.
2. If by some chance the file exists at execute time (e.g., a parallel branch landed it), delete it:
   ```
   rm -f .ok-planner/design/concepts/sensor-watch.md
   ```

**Verification:**
```
test ! -f .ok-planner/design/concepts/sensor-watch.md
```

### Task 72 — Rewrite internal concept doc: `sensor.md` (end-to-end)

**Files:** `.ok-planner/design/concepts/sensor.md`.

**Steps:**

1. Replace the entire file content per spec §Rewrite: `concept:sensor` (end-to-end, not additive).
2. New text: "Sensor is a class of Publisher implementation that observes external state. Protocol methods are inherited from Publisher (`Subscribe` / `Unsubscribe` / `ListSubscriptions`). Examples in this repo: sensor-cron, sensor-http, sensor-object-store, sensor-webhook. State-persistence and multi-replica HA are the sensor implementation's concern; rimsky models one named publisher peer per `protocols: [publisher]` advertisement on an entry in the top-level `publishers:` block of `file:deploy/rimsky.yml`, and does not model pod counts. The bundled reference impls declare single-replica deployment models with state-persistence (where required) to survive restart."
3. Sections: Definition / Purpose / Boundaries / Invariants / Notes / Annotation sites.
4. Old content drops the protocol-surface table (StartWatch/StopWatch/ListWatches) and any aspirational language about advisory locks.

**Verification:**
```
grep -c 'StartWatch\|StopWatch\|ListWatches' .ok-planner/design/concepts/sensor.md
```
Should return 0.

### Task 73 — Refresh internal concept docs: `message.md`, `invalidate.md`, `named-event.md`, `backfill.md`, `frame.md`

**Files:** 5 concept files.

**Steps:**

1. `.ok-planner/design/concepts/message.md`:
   - Add idempotency-key subsection (header pattern, dedup tuple, 24h TTL, the `rimsky_message_idempotencies` table).
   - Rewrite "Three emit sites" line: drop `POST /sensors/{watch_id}/observations` citation; replace with `POST /instances/{id}/messages` with `sender_kind: "publisher"`.
   - Update `sender_kind` enum at lines 22-23 from `(operator | sensor | instance)` → `(operator | publisher | instance)`.
   - Annotation-sites list: drop `code:control/controlapi/sensors.go::handleSensorObservation` entry.
2. `.ok-planner/design/concepts/invalidate.md:42`: rewrite the line citing the old route to cite the unified path.
3. `.ok-planner/design/concepts/named-event.md:51`: replace "sensor observation (...)" with "publisher-origin message" + the unified-route citation.
4. `.ok-planner/design/concepts/backfill.md:59`: replace "sensor observations" with "publisher-origin messages" + the cite.
5. `.ok-planner/design/concepts/frame.md:18`: replace "sensor observations" (frame-creation site) with "publisher-origin messages" + the cite.

**Verification:**
```
rg 'POST /sensors/' .ok-planner/design/concepts/ | wc -l
```
Should return 0.

### Task 74 — Write public concept docs

**Files:** `docs/concepts/publisher.md`, `docs/concepts/publisher-subscription.md`, `docs/concepts/replica.md`, `docs/protocols/publisher.md` (all new); `docs/concepts/subscription.md` (renamed to `node-subscription.md`); `docs/concepts/sensor.md` (refreshed if it exists).

**Steps:**

1. Mirror the internal concept docs to their public counterparts. The public docs may be more polished but the load-bearing content is the same.
2. Rename `docs/concepts/subscription.md` → `docs/concepts/node-subscription.md` (mirror Task 68).
3. Write `docs/protocols/publisher.md` as the public protocol guide. Replace any prior `docs/protocols/sensor.md`.

**Verification:**
```
ls docs/concepts/publisher.md docs/concepts/publisher-subscription.md docs/concepts/replica.md docs/concepts/node-subscription.md docs/protocols/publisher.md
test ! -f docs/concepts/subscription.md
```

### Task 75 — Update concept-catalog TOC

**Files:** `.ok-planner/design/concepts.md`.

**Steps:**

1. Open `concepts.md`.
2. Drop the old `subscription` entry.
3. Add new entries: `node-subscription`, `publisher`, `publisher-subscription`, `replica`. Place them alphabetically (or per the existing TOC's conventions).
4. Each entry follows the existing format (`- slug — one-sentence description`).

**Verification:**
```
grep -c '^- subscription$\|^- subscription —' .ok-planner/design/concepts.md
```
Should return 0 (old slug gone).

### Task 76 — Update public concept-catalog TOC

**Files:** `docs/concepts/README.md` (the public TOC file in the rimsky repo; verify with `ls docs/concepts/README.md`).

**Steps:**

1. Open the file.
2. Mirror the TOC update from Task 75: drop the old `subscription` slug entry; add new entries for `node-subscription`, `publisher`, `publisher-subscription`, `replica`. Follow the file's existing format conventions (read the rest of the file first).

**Verification:**
```
test -f docs/concepts/README.md && grep -c 'subscription$\|^- subscription' docs/concepts/README.md
```
Should return 0 (old slug gone).

### Task 77 — CLAUDE.md surgical edits

**Files:** `CLAUDE.md`.

**Steps:**

1. "Sensor protocol" → "Publisher protocol" (sweep).
2. "sensor watch" / "watch" (when referring to rimsky-side binding) → "publisher-subscription" (sweep).
3. `rimsky_sensor_watches` → `rimsky_publisher_subscriptions` (~5 hits).
4. `rimsky_messages.sender_kind` enum at line ~154: update from `(operator | sensor | instance)` → `(operator | publisher | instance)`.
5. cfg description: introduce top-level `publishers:` block alongside `claim_producers:` + `executors:`.
6. Drop the `POST /sensors/{watch_id}/observations` route reference; replace with unified-route operator-facing note.
7. Cron-firing gotcha: align vocabulary to publisher/publisher-subscription where appropriate; "bundled sensor" stays.
8. New gotcha block: universal `Idempotency-Key` header on `POST /instances/{id}/messages`.
9. New gotcha block: dropped `POST /sensors/{watch_id}/observations` route; flag for operators with external sensors pointing at the old URL.
10. Schema row for `rimsky_publisher_subscriptions`: update column list per spec. No `on_observation`, no `last_observed_at`.
11. `concept:subscription` citations in CLAUDE.md (referring to template-DSL block) → `concept:node-subscription`; new publisher-binding citations use `concept:publisher-subscription`.

**Verification:**
```
rg 'POST /sensors/\|rimsky_sensor_watches\|Sensor protocol' CLAUDE.md | wc -l
```
Should return 0.

### Task 78 — CHANGELOG entry

**Files:** `CHANGELOG.md`.

**Steps:**

1. Under `## Unreleased`, add:
   ```
   - **Publisher protocol unification.** Replaced the `Sensor` protocol with `Publisher`; sensors are now one kind of publisher. The special observation deposit endpoint (`POST /sensors/{watch_id}/observations`) is deleted; bundled sensors now POST to the existing generic `POST /instances/{id}/messages` endpoint with `sender_kind: "publisher"` + `publisher_subscription_id` capability validation. Routing fields (`target_node`, `message_kind`) move inline onto `SubscribeRequest`, eliminating the `OnObservationSpec` Go type. The `payload_template` substitution machinery is removed entirely (downstream consumers read raw observation bytes via `{{trigger.message.payload.<path>}}`). The `rimsky_sensor_watches` table renames to `rimsky_publisher_subscriptions`. A universal `Idempotency-Key` header lands on the messages endpoint with a new `rimsky_message_idempotencies` table + retention sweep. Three bundled sensors (sensor-http, sensor-object-store, sensor-webhook) gain per-binary state DBs to survive restart; sensor-cron stays in-memory by default. All four bundled sensors gain Dockerfiles + compose entries + helm templates. Dev databases must be wiped and recreated.
   ```
2. Older CHANGELOG entries describing the retired observation route remain unchanged as historical record.

**Verification:**
```
git diff CHANGELOG.md | head -10
```

### Task 79 — Final quiescence: all suites + sanity greps

**Files:** none modified.

**Steps:**

1. Run the full verification suite:
   ```
   make build-all && make test-all && make lint && make license-lint
   go test ./runtime/... ./control/... ./foundation/... ./subscribers/... ./sensors/... -race -count=1
   cd dashboards/rimsky-dashboard && npm test && npm run build && cd -
   ```
2. Run sanity greps:
   ```
   rg 'SensorRegistry|SensorClient|SensorWatchRow|SensorWatchTable|sensorRegistryImpl' --type=go | wc -l
   rg 'rimsky_sensor_watches|on_observation|last_observed_at' --type=go --type=sql | wc -l
   rg 'OnObservationSpec' --type=go | wc -l
   rg 'POST /sensors/' --type=go | grep -v '_test.go' | wc -l
   rg 'StartWatch|StopWatch|ListWatches|WatchDescriptor' --type=go | grep -v '_test.go' | wc -l
   rg 'cfg:rimsky.yml::sensors\[\]' --type=md | wc -l
   rg 'POST /sensors/' --type=md | wc -l
   rg 'concept:subscription\b' .ok-planner/design/ | grep -v 'publisher-subscription\|node-subscription' | wc -l
   rg '@concept: subscription\b' --type=go | wc -l
   rg 'cmd/rimsky-sensor-conformance\|cmd/rimsky-sensor-cron' --type=go --type=yaml --type=md | wc -l
   ```
3. All greps should return 0 (or only clearly-historical context — inspect each remaining hit).
4. All test/build commands exit 0.

**Verification:** see step 2 + 3 above.

---

## Manual checks after completion

None of these block the automated run; they're for the user to verify after the implementation + review cycle completes.

1. **Reference docker-compose stack end-to-end smoke**: spin up the full stack and verify sensors fire:
   ```
   cd deploy && docker compose up -d
   curl http://localhost:8080/health
   # Create a template with a publishers: block targeting sensor-cron
   # Create an instance
   # Wait for cron tick; verify rimsky_messages contains a message envelope from publisher
   ```
2. **Visual review** of the rendered helm chart against a real cluster (if one is available).
3. **Operator UX check** for the `payload_template` rejection error message — confirm operators see a clear message (the standard `json: unknown field "payload_template"` form is what they'll see; if it's too terse, a follow-up plan can add a custom error).
