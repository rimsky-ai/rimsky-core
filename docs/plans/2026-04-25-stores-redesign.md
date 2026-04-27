# Stores Redesign Implementation Plan

**Goal:** Replace `Resource` abstraction with `Store` abstraction; unify lock/claim/dispatch under `rimsky_lock_holders`; replace inputs/outputs/claim_metadata with typed `attributes`; make `userdata` opaque; ship filesystem-direct + claim-store-postgres + stub-store v1; smoke fixture exercises 100 items end-to-end against the deployed stack.

**Architecture:** Per spec. New top-level packages: `core/store/` (interfaces + filesystem + claimstorepg + stub), `core/attributes/` (substitution + validation + callback). Schema rewritten in place; proto rewritten clean-break; `core/resource/` deleted. Scheduler grows lock-holder + claim-holder + visibility-timeout sweeps. Supervisor runner becomes the omnibus runner.

**Tech Stack:** Go (root module `github.com/fallguy/rimsky`), `pgx/v5`, `chi/v5`, `robfig/cron/v3`, `log/slog`. JSON Schema via `github.com/santhosh-tekuri/jsonschema/v5` (new dep). TS (`executors/claude-agent/`). gRPC + HTTP+JSON via `proto/v1/`.

**Spec:** `docs/specs/2026-04-25-stores-redesign-design.md`. Section refs in this plan are to that spec; the plan is intentionally light on duplication — subagents read the spec for details.

**Pre-v1 / break freely:** Dev DB is nuked on first run. No backwards compat. No commit / push / branch / PR steps in this plan — the user owns git. Build will be red mid-plan; only the final verification task requires green.

---

## Task ordering note

Tasks are ordered so dependencies land before dependents. The build is **NOT** required to pass between every task — many intermediate states will not compile. Per-task verification is scoped to what is verifiable at that step (e.g. "the new file's `go vet` passes when the package is buildable in isolation"). Repo-wide green build is required only at Task 64.

If a subagent needs to add a `// TODO: deleted in Task N` comment to keep an interim file compiling, that is fine. Final cleanup of such TODOs lands by Task 64.

---

## Task 1 — Rewrite `core/migrations/001-initial.sql`

**Files:** `core/migrations/001-initial.sql`, `core/migrations/002-data-ref-jsonb.sql`

**Steps:**
1. Replace the entire content of `core/migrations/001-initial.sql` with the schema from spec §9.1–§9.9 (preserved tables: `rimsky_migrations`, `rimsky_templates`, `rimsky_instances`, `rimsky_nodes`, `rimsky_supervisors` (with new `accepted_stores TEXT[]`), `rimsky_dispatch` (with new `required_stores TEXT[]`, `last_heartbeat_at`, `executor_name` nullable; `concurrency_tags` removed), `rimsky_schedules`, `rimsky_events`; new tables: `rimsky_node_attributes`, `rimsky_lock_holders` (with all five indexes including the new `_node_idx`), `rimsky_claim_holders` (with `actual_action`); removed: `rimsky_resources`, `rimsky_resource_versions`).
2. Preserve the file's leading docstring style; update the comment block to reflect the redesign.
3. Delete `core/migrations/002-data-ref-jsonb.sql`.

**Verification:**
- `go test ./core/migrations/... -count=1` — `runner_test.go` (testcontainers-backed) applies the rewritten `001-initial.sql` against a fresh postgres and passes. If the test references dropped tables, this task also fixes it.

---

## Task 2 — Rewrite `proto/v1/node_executor.proto`

**Files:** `proto/v1/node_executor.proto`

**Steps:**
1. Replace the file with the new `ExecuteRequest` and terminal-event shapes from spec §12.1–§12.2.
2. Remove `deps_data`, `reads_data`, `instance_params` from `ExecuteRequest`. Add `attributes`, `attributes_schema`, `stores` (map of `StoreHandle`), `resumed`, `run_attempt`. Define `StoreHandle` message inline.
3. Remove `result` from `Complete`; add `attributes_delta`.
4. Preserve `Heartbeat`, `Blocked`, `Errored`, `AsyncAccepted` shapes.
5. Update the leading docstring; reference the new spec at `docs/specs/2026-04-25-stores-redesign-design.md` rather than `2026-04-23-rimsky-go-port-design.md`.

**Verification:** `protoc --go_out=... proto/v1/node_executor.proto` succeeds (or, since `make proto-gen` is the canonical entry, defer the regeneration to Task 4).

---

## Task 3 — Update `proto/v1/events.proto`

**Files:** `proto/v1/events.proto`

**Steps:**
1. Add new payload messages for the new event kinds in spec §9.8: `LockAcquiredPayload`, `LockReleasedPayload`, `LockOrphanReapedPayload`, `AttributesSubstitutedPayload`, `AttributesCommittedPayload`, `AttributesValidationFailedPayload`, `ClaimAcquiredPayload`, `ClaimHeldPayload`, `ClaimResolvedPayload` (with `action`, `claim_id`, `store_name`), `TemplateResolutionFailedPayload`. Add them to the `Event.payload` `oneof` with sequential field numbers.
2. Remove `CommitPayload`, `PureCascadeCommitPayload` from the oneof (and their definitions). Remove any `RestoreVersion`-bearing fields from preserved payloads.
3. Preserve every other event-kind payload listed as preserved in §9.8.

**Verification:** `protoc proto/v1/events.proto` succeeds.

---

## Task 4 — Run `make proto-gen`

**Files:** `proto/v1/gen/*.go`

**Steps:**
1. If `protoc-gen-go` is missing: `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`.
2. If `protoc` is missing: download the platform-appropriate release tarball from `https://github.com/protocolbuffers/protobuf/releases/latest` (e.g. `protoc-26.1-osx-aarch_64.zip` for Apple Silicon mac), unzip into `~/.local/protoc/`, and add `~/.local/protoc/bin` to `PATH` for the current session: `export PATH="$HOME/.local/protoc/bin:$PATH"`. Pick the URL non-interactively via `curl -sLO`. Do **not** use `brew` (may not be installed in the agent's environment).
3. Run `make proto-gen` from the repo root.
4. Verify `git status proto/v1/gen/` shows changes (the regen succeeded).

**Verification:** `make proto-gen` exits 0; running it again produces no diff (`git diff --exit-code proto/v1/gen/`).

---

## Task 5 — Create `core/store/` interface package

**Files:**
- `core/store/interface.go` (new)
- `core/store/types.go` (new)
- `core/store/registry.go` (new)
- `core/store/tx.go` (new)
- `core/store/doc.go` (new)

**Steps:**
1. `core/store/doc.go` — package comment summarising spec §5.1–§5.6.
2. `core/store/interface.go` — declare `Store`, `ClaimableStore`, `ResumableStore` interfaces per spec §8.5 / §8.5.1, including `ReleaseClaimItem` on `ClaimableStore`. Declare `LockSpec` interface and `NamedLockSpec`, `RegionLockSpec`, `ClaimLockSpec` types per §8.3. Declare `LockMode` const and `LockModeMutex`/`LockModeCounting`. Declare `ReleaseAction` const block per §8.4. Declare `Capabilities` struct per §8.2 (no `SupportsAtomicMulti` / `KeepVersionsMax`). Annotate `RegionsConflict`/`UnmarshalRegion` purity contract (§18 invariant 14) and `Store` lock-state-postgres-only contract (§18 invariant 9) with `@blessed-invariant` block comments.
3. `core/store/types.go` — declare `LockHandle`, `ClaimResult`, `CommitResult`, `NativeHandle` (sealed marker interface), `FilesystemDirectHandle`, `ClaimStoreHandle` per §8.4 and §8.6.
4. `core/store/registry.go` — declare `Factory` interface, `Registry` struct, `Register`, `BuildAll(StoresConfig)`, `GetStore`, and `StoresConfig` per §8.7.
5. `core/store/tx.go` — declare `WithTx` and `TxFromContext` per §8.4.1, importing `pgx/v5`.

**Verification:** `go vet ./core/store/...` passes; `go build ./core/store/...` passes (file is self-contained — only imports `pgx/v5` and stdlib).

---

## Task 6 — Implement `core/store/filesystem/` direct-mode store

**Files:**
- `core/store/filesystem/factory.go` (new)
- `core/store/filesystem/store.go` (new)
- `core/store/filesystem/region.go` (new — path-glob conflict logic)
- `core/store/filesystem/region_test.go` (new)
- `core/store/filesystem/store_test.go` (new)

**Steps:**
1. `factory.go` — `Factory` impl with `Kind() string = "filesystem"`. `Build(name, cfg)` validates `mode == "direct"`, reads `root` from cfg, returns `*Store`.
2. `region.go` — pure `RegionsConflict([]string, []string) bool` for path globs (any glob in left overlapping with any in right is a conflict). Use `path/filepath.Match` for individual globs; expand `**` semantics manually (a glob with `**` matches any path under its prefix). Pure helper `globsOverlap(a, b string) bool`.
3. `store.go` — `*Store` implements `Store`. `AcquireLock` is a no-op for `RegionLockSpec`, returns `LockHandle` with no `ClaimResult` payload. `OpenHandle` returns `FilesystemDirectHandle{Path: root, WriteRegions: spec.Region.([]string), ReadRegions: spec.ReadRegions}` (the runner threads ReadRegions through; expose them on `RegionLockSpec` if not already). `Commit` is a no-op returning `CommitResult{Changed: true}`. `ReleaseLock` is a no-op for all actions. `Capabilities() {SupportsRegionLock:true, SupportsResume:true}`. `LockEligible` returns true unconditionally (the supervisor pre-checks via `RegionsConflict`). `RegionsConflict` delegates to `region.go`. `UnmarshalRegion(b)` json-unmarshal into `[]string`.
4. `region_test.go` — table-driven: disjoint globs don't conflict; overlapping globs do conflict; `**` semantics.
5. `store_test.go` — happy path `AcquireLock` → `OpenHandle` → write to path → `Commit` → `ReleaseLock`.

**Verification:** `go test ./core/store/filesystem/... -count=1` passes.

---

## Task 7 — Implement `core/store/claimstorepg/` claim-store-postgres

**Files:**
- `core/store/claimstorepg/factory.go` (new)
- `core/store/claimstorepg/store.go` (new)
- `core/store/claimstorepg/acquire.go` (new)
- `core/store/claimstorepg/release.go` (new)
- `core/store/claimstorepg/holders.go` (new — §5.6.4 resolution algorithm)
- `core/store/claimstorepg/factory_test.go` (new)
- `core/store/claimstorepg/store_test.go` (new — testcontainers-backed)
- `core/store/claimstorepg/holders_test.go` (new — testcontainers-backed)

**Steps:**
1. `factory.go` — `Factory` impl with `Kind() = "claim_store"`. `Build` reads `backend`, `items_table`, `on_commit_default`, `on_give_up_default`, `visibility_timeout_seconds` from cfg; validates `backend == "postgres"`. Verifies the items_table exists with the §9.10 columns (SELECT against `information_schema.columns`); fails fast on mismatch.
2. `store.go` — `*Store` carries config + items-table name + a `*pgxpool.Pool`. Implements `Store` + `ClaimableStore` + `ResumableStore`. `Capabilities() {SupportsClaim:true, SupportsDiscard:true, SupportsResume:true}`. `LockEligible` defers to `HasClaimableItem` for `ClaimLockSpec`. `RegionsConflict` returns false (claim stores have no regions). `UnmarshalRegion` returns `nil, nil`.
3. `acquire.go` — `AcquireLock(ctx, ClaimLockSpec)` reads the open tx via `store.TxFromContext(ctx)`, runs the §13.3 SQL: `UPDATE <items_table> SET state='in_progress', claim_token=$1, claimed_at=now() WHERE item_id = (SELECT item_id FROM <items_table> WHERE state='available' [+ criteria predicate] ORDER BY enqueued_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING item_id, payload`. On no-row, return empty `ClaimResult`. On row, return `ClaimResult{Payload: <decoded jsonb>, ClaimID: item_id, ResolvedRegion: nil}`.
4. `release.go` — `ReleaseLock(ctx, lh, action)` reads tx via TxFromContext; for `ReleaseCommit`/`ReleaseGiveUp`/`ReleaseDiscard` apply the configured `on_commit_default` / `on_give_up_default` (or the per-spec override; the supervisor passes the resolved action to `ReleaseLock` indirectly — TODO: thread through). `ReleaseClaimItem(ctx, claimID, action)` does the items-table flip per `release_to_back` (set `state='available'`, `claim_token=NULL`, `enqueued_at=now()`) or `release_to_head` (set `state='available'`, `claim_token=NULL`, `enqueued_at=now() - X` to push to the front of the FIFO order — pick a deterministic value, e.g. `now() - INTERVAL '1 year'`).
5. `holders.go` — implement the §5.6.4 resolution algorithm. `ResolveOnTerminal(ctx, claimID, holderNodeID, terminalOutcome string) error` runs the pseudocode: SELECT FOR UPDATE the holder row, determine action from `on_commit`/`on_give_up`, execute the delete-vs-release branch, update `actual_action`, update sibling state; commit logic is the caller's responsibility (caller drives the outer tx). For the delete branch, the items-table row is deleted via `DELETE FROM <items_table> WHERE item_id = ?`.
6. `factory_test.go` — `Build` with valid + invalid configs.
7. `store_test.go` — testcontainers-backed: spin up postgres, create items_table, insert items, `AcquireLock` returns first item, second `AcquireLock` returns next, etc.
8. `holders_test.go` — testcontainers-backed: linear chain (1 holder, delete) → items-table row gone; fan-out 2-holders both release → second resolution fires `ReleaseClaimItem`; fan-out delete+release → delete wins regardless of order.

**Verification:** `go test ./core/store/claimstorepg/... -count=1` passes.

---

## Task 8 — Implement `core/store/stub/` stub store

**Files:**
- `core/store/stub/factory.go` (new)
- `core/store/stub/store.go` (new)
- `core/store/stub/store_test.go` (new)

**Steps:**
1. `store.go` — `*Store` carries in-memory state: a `map[string]ClaimItem` for claim-store mode, a `map[string][]string` for region-lock mode, a `map[string]int` for named-lock count tracking. Implements `Store`, `ClaimableStore`, `ResumableStore`.
2. `factory.go` — `Build(name, cfg)` reads a `kind` config key (`stub_filesystem` | `stub_claim_store`) and capabilities flags. Returns the configured stub.
3. The store is thread-safe (uses a `sync.Mutex`), since scenario tests run goroutines.
4. `store_test.go` — exercise the in-memory state machine directly.

**Verification:** `go test ./core/store/stub/... -count=1` passes.

---

## Task 9 — Create `core/attributes/` package

**Files:**
- `core/attributes/doc.go` (new)
- `core/attributes/substitution.go` (new)
- `core/attributes/substitution_test.go` (new)
- `core/attributes/validate.go` (new)
- `core/attributes/validate_test.go` (new)
- `core/attributes/callback.go` (new)
- `core/attributes/callback_test.go` (new)
- `core/attributes/store.go` (new — postgres helpers for `rimsky_node_attributes`)
- `core/attributes/store_test.go` (new — testcontainers-backed)

**Steps:**
1. Add `github.com/santhosh-tekuri/jsonschema/v5` as a `go.mod` dependency (`go get github.com/santhosh-tekuri/jsonschema/v5`). Run `go mod tidy` after the `go get` to keep `go.sum` clean.
2. `substitution.go` — `Substitute(rawValue string, ctx ResolveContext) (string, error)` performs single-pass `{{...}}` replacement. `ResolveContext` carries `Deps map[string]map[string]any`, `Claims map[string]ClaimResult`, `Params map[string]any`. The grammar matches `{{deps.<n>.<f>}}` / `{{claim.<store>.<f...path>}}` / `{{params.<key>}}`; nested paths use dot-notation. Empty-result for required field returns a typed `ErrMissingSource` carrying the failed key. Optional-field omission returns the empty string with no error (the caller decides to omit the key). Recursion is not performed (a result containing `{{...}}` is returned literal).
3. `substitution_test.go` — table-driven for all three source kinds, missing-required, missing-optional, nested paths, recursive-not-performed.
4. `validate.go` — `Validate(schema map[string]any, data map[string]any) error` wraps santhosh-tekuri's `jsonschema.Compile` + `Validate`. Returns a typed error so the supervisor's policy chain can route on `attributes_schema_failed`.
5. `validate_test.go` — happy path; type mismatch; missing required.
6. `callback.go` — `Handler(deps storage.NodeAttributesStore) http.Handler` returning a `chi`-compatible handler for `POST /v1/attributes/{node_id}` per §12.5. Auth via `Authorization` header matching the supervisor-issued cancel-token.
7. `callback_test.go` — happy path: POST `{"delta": {"x": 1}}` merges into `data`.
8. `store.go` — `Get(ctx, nodeID) (Row, error)`, `Upsert(ctx, nodeID, runAttempt, data) error`, `MergeDelta(ctx, nodeID, delta) error`. `MergeDelta` does an `UPDATE rimsky_node_attributes SET data = data || $1::jsonb, updated_at = now() WHERE node_id = $2`.
9. `store_test.go` — testcontainers-backed CRUD round-trip.
10. Annotate the §18 invariants 11 + 12 (`@blessed-invariant`) on the relevant files: invariant 11 (userdata-opaque) on `substitution.go` (no path inspects userdata); invariant 12 (two-gate validation) on `validate.go`.

**Verification:** `go vet ./core/attributes/...` passes; `go test ./core/attributes/... -count=1` will pass after Tasks 10 and 11 land (the package depends on `storage.NodeAttributesStore` from Task 10's interface and the postgres implementation from Task 11). Re-run at Task 11's verification.

---

## Task 10 — Update `core/storage/interfaces.go`

**Files:** `core/storage/interfaces.go`

**Steps:**
1. Remove `ResourceRegistry`, `ResourceDataStore` interfaces and their accessor methods on `StorageBackend`.
2. Add accessors: `LockHolders() LockHoldersStore`, `NodeAttributes() NodeAttributesStore`, `ClaimHolders() ClaimHoldersStore`.
3. Define the three new interfaces (CRUD + sweep helpers + the §13.5 sweep predicates).
4. Remove `ConcurrencyTags` field from `NodeRow`.
5. Remove `OutputData` / similar fields if any (replaced by `rimsky_node_attributes`).
6. Add `RequiredStores []string` to `DispatchRow` / `EnqueueRequest` and `LastHeartbeatAt *time.Time` to `DispatchRow`.
7. Make `DispatchRow.ExecutorName *string` (nullable).
8. Add `AcceptedStores []string` to `SupervisorRow`.

**Verification:** `go build ./core/storage/...` will fail until Task 11 lands; defer to Task 11's verification.

---

## Task 11 — Implement `core/store/lockholders.go` and `core/storage/postgres/` new accessors

**Files:**
- `core/store/lockholders.go` (new — postgres helpers for `rimsky_lock_holders`, lives at the top of `core/store/` per spec §16.1 because it owns the unified lock-holder mechanism, not `core/storage/postgres/`)
- `core/storage/postgres/node_attributes.go` (new)
- `core/storage/postgres/claim_holders.go` (new)
- `core/storage/postgres/supervisors.go` (modified — add `accepted_stores` to Upsert + scan)
- `core/storage/postgres/nodes.go` (modified — drop `concurrency_tags` from inserts/reads)
- `core/storage/postgres/backend.go` (modified — register new accessors; drop `Resources()` / `ResourceData()`)

**Steps:**
1. `core/store/lockholders.go` — implement the postgres helpers for `rimsky_lock_holders`. Methods: `Insert(ctx, tx pgx.Tx, row LockHolderRow) error`, `DeleteByID(ctx, tx pgx.Tx, id, supervisorID string) error` (claimant-guarded), `RefreshHeartbeat(ctx, db, supervisorID string, heartbeatSeconds int) error` (per §13.4 SQL with the `holder_node_id IN running-nodes` filter), `ListExpired(ctx, db) ([]LockHolderRow, error)`, `ListByNodeAndStore(ctx, db, nodeID, storeName, supervisorID string) ([]LockHolderRow, error)` (for §13.3 step 3a rebind), `RebindForResume(ctx, tx pgx.Tx, existingRowID, supervisorID string, heartbeatSeconds int) (LockHolderRow, error)`. Imports `pgx/v5` and `core/shared` only — no dependency on `core/storage/`. The file's annotation `@blessed-invariant 9 (lock state lives only in postgres)` lives here.
2. `node_attributes.go` — implement `NodeAttributesStore`. Methods: `Get`, `Upsert`, `MergeDelta`, `IncrementRunAttempt`, `ClearExecutorPopulated(ctx, nodeID, schema)` (the source-aware retry-clear per §5.7.3 — takes the schema to know which fields to preserve).
3. `claim_holders.go` — implement `ClaimHoldersStore`. Methods: `InsertHoldersForClaim(ctx, tx, claimID, storeName, terminals, onCommit, onGiveUp)`, `Get(ctx, claimID, holderNodeID)`, `MarkCompleted(ctx, tx, id, actualAction)`, `CountActive(ctx, claimID)`, `CountDeleteWinners(ctx, claimID)`, `ListLeakedForGC(ctx)` (per §13.5 step 3).
4. `supervisors.go` — extend `Upsert` SQL: add `accepted_stores` column to the INSERT/UPDATE; extend the row scan to read it.
5. `nodes.go` — remove `concurrency_tags` from all SQL fragments and from the `NodeRow` scan.
6. `backend.go` — drop `Resources()`, `ResourceData()` methods; register `NodeAttributes()`, `ClaimHolders()` accessors. The lock-holder accessor lives at `core/store/lockholders.go` and is reachable via the package directly (`store.NewLockHoldersClient(pool)` returns a wrapper); `backend.go` exposes `LockHolders() *store.LockHoldersClient` as a thin convenience method that constructs the client.

**Verification:** `go build ./core/storage/...` passes. `go test ./core/storage/postgres/... -count=1` passes (testcontainers spin-up time will cover this).

---

## Task 12 — Delete resource files from storage and controlapi layers

**Files (deleted):**
- `core/storage/postgres/resources.go`
- `core/storage/postgres/resource_data.go`
- `core/controlapi/resources.go`

**Steps:**
1. `git rm` (or just `rm`) the three files. The package's other files still reference them via the `storage.StorageBackend` interface — those callers are updated in Tasks 13–14.

**Verification:** `ls core/storage/postgres/resources.go` returns "No such file" (success).

---

## Task 13 — Trim `core/storage/postgres/postgres_test.go`

**Files:** `core/storage/postgres/postgres_test.go`

**Steps:**
1. Identify resource-specific tests; delete them.
2. Preserve nodes / dispatch / events / supervisors / instances / templates tests.
3. If any preserved test references `ConcurrencyTags`, drop the assertion.
4. If any preserved test references `Resources()` accessor, drop it.

**Verification:** `go test ./core/storage/postgres/... -count=1` passes.

---

## Task 14 — Delete `core/resource/` package

**Files (deleted):**
- `core/resource/interface.go`
- `core/resource/register.go`
- `core/resource/errors.go`
- `core/resource/inlinejsonb/` (entire directory)
- `core/resource/externalsql/` (entire directory)

**Steps:**
1. `rm -rf core/resource/`.
2. Importers will fail until Tasks 15–35 land. That is expected.

**Verification:** `ls core/resource` returns "No such file or directory" (success).

---

## Task 15 — Update `core/queue/interface.go`

**Files:** `core/queue/interface.go`

**Steps:**
1. Remove `ConcurrencyTags []string` from `EnqueueRequest`.
2. Add `RequiredStores []string` to `EnqueueRequest`.
3. Drop the `concurrency_tags` parameter from any helper signature.
4. Add `LockSpecs []store.LockSpec` to `ClaimNextRequest` (or similar) — the runner passes the candidate's lock specs from the in-memory template registry.
5. Add `ListLockHolders(...)` and `RebindLockHandle(ctx, tx, existingRowID, supervisorID) (LockHandle, error)` if not on the `LockHoldersStore` interface (move it there if cleaner).
6. Document the new claim-time eligibility model in package doc.

**Verification:** `go build ./core/queue/...` will fail until Task 16; defer.

---

## Task 16 — Rewrite `core/queue/postgres/queue.go`

**Files:** `core/queue/postgres/queue.go`, `core/queue/postgres/queue_test.go`

**Steps:**
1. Update `Enqueue` SQL to write `required_stores` and to drop `concurrency_tags`.
2. Drop the existing `pg_advisory_xact_lock(hashtext('rimsky_tag:'||tag))` per-tag locking helpers — these move to the runner.
3. Update `ListOrphanedClaims` predicate from `claimed_at` to `last_heartbeat_at`.
4. Implement the §13.3 step 1 candidate-selection SQL helper: `SELECT FROM rimsky_dispatch FOR UPDATE SKIP LOCKED LIMIT 1` with pool-specialization predicate (`required_stores <@ $1`, `executor_name = ANY($2) OR executor_name IS NULL`). Helper returns the candidate row + node + node-type for the runner to look up lock specs.
5. Implement the §13.3 step 3c claimant-guarded UPDATE helper: `UPDATE rimsky_dispatch SET claimed_by=$1, claimed_at=now(), last_heartbeat_at=now() WHERE id=$2 AND claimed_by IS NULL RETURNING 1`.
6. Add the `pg_advisory_xact_lock(hashtext('rimsky_lock:'||$1))` helper used by the runner per spec §13.3 step 3b.
7. Rewrite `queue_test.go` against the new shape: enqueue row carries `required_stores`; orphan reaper uses `last_heartbeat_at`; advisory-lock helper.

**Verification:** `go test ./core/queue/postgres/... -count=1` passes (testcontainers-backed).

---

## Task 17 — Update `core/node/template.go`

**Files:** `core/node/template.go`

**Steps:**
1. Remove `OwnsResources []ResourceDef`, `ReadsResources []ReadResourceDef`, `ConcurrencyTags []string` from `TemplateNodeDef`.
2. Remove `ResourceDef`, `ReadResourceDef` types.
3. Add new types: `NodeStoreRef` (`{Name, Claim, Hold, Write []string, Read []string, OnCommit, OnGiveUp string, Resumable bool}`), `NodeLockRef` (`{Name string, Mode store.LockMode, Limit int}`), `NodeAttributesDef` (`{Schema map[string]any}`), `ClaimResolutionRef` (`{Source, Store, OnCommit, OnGiveUp string}`).
4. Add fields on `TemplateNodeDef`: `Stores []NodeStoreRef`, `Locks []NodeLockRef`, `Attributes NodeAttributesDef`, `ClaimResolutions []ClaimResolutionRef`. Userdata field stays as `Userdata map[string]any`.
5. Add helper `RequiredStores(node TemplateNodeDef) []string` — distinct names from `Stores`.

**Verification:** `go build ./core/node/...` will fail until Task 18; defer.

---

## Task 18 — Rewrite `core/node/template_validator.go`

**Files:** `core/node/template_validator.go`, `core/node/template_validator_test.go`

**Steps:**
1. Remove `validateOwnsResources`, `validateConcurrencyTags`.
2. Add `validateStores` (every name resolves to a known store kind in the registry; per-node duplicate name rejected; `claim:true` requires a `claim_store`-kind store; `write/read` only on `filesystem`-kind stores).
3. Add `validateLocks` (mode is `mutex` or `counting`; counting requires `limit >= 1`).
4. Add `validateAttributesSchema` (the JSON Schema parses; source-directives are syntactically valid `{{...}}` referencing `deps.<n>.<f>`, `claim.<store>.<f...>`, or `params.<k>`; referenced upstream node names exist in the template; referenced store names are in this node's `Stores` list).
5. Add `validateClaimResolutions` per §11.4: for each node with `hold:true` claim source, walk the dependency DAG, identify terminal-leaves of the holding subgraph, ensure each leaf's `claim_resolutions` covers the held claim. Helper `findHoldingTerminals(template, sourceNode, storeName) []string`.
6. Rewrite `template_validator_test.go`: drop resource/concurrency-tag tests. Add tests for each new validator including the §11.4 DAG walk (linear chain happy path, fan-out happy path, missing-resolution failure).

**Verification:** `go test ./core/node/... -count=1` passes.

---

## Task 19 — Update `core/node/state.go` and `state_test.go`

**Files:** `core/node/state.go`, `core/node/state_test.go`

**Steps:**
1. Remove `ReasonRestoreVersion` constant.
2. Remove the state-machine branch keying off `ReasonRestoreVersion`.
3. Preserve every other transition; the `running → running` reject (blessed invariant 1) stays.
4. Drop the corresponding tests in `state_test.go`.

**Verification:** `go test ./core/node/... -count=1` passes; `grep -r ReasonRestoreVersion core/` returns nothing.

---

## Task 20 — Update `core/node/policy.go`

**Files:** `core/node/policy.go`

**Steps:**
1. Drop `RestoreVersion` field from `ErrorActionDef` / `PolicyAction` (whichever struct carries it).
2. Drop any related JSON tag handling.

**Verification:** `grep -rn RestoreVersion core/node/` returns nothing.

---

## Task 21 — Update `core/scheduler/invalidate.go`

**Files:** `core/scheduler/invalidate.go`

**Steps:**
1. Delete the `invalidateRestorePath` function entirely.
2. Drop the `RestoreVersion` field from `InvalidateArgs` and `InvalidateRequest`.
3. Update any caller within the file that branched on `RestoreVersion`.
4. Preserve the rest of the invalidate logic (cascade, kill_requested propagation, message-emit).

**Verification:** `grep -rn RestoreVersion core/scheduler/invalidate.go` returns nothing; `go build ./core/scheduler/...` will fail until Task 22+; defer.

---

## Task 22 — Update `core/scheduler/recalculate.go`

**Files:** `core/scheduler/recalculate.go`

**Steps:**
1. Drop the `n.ConcurrencyTags` reference at the existing `Enqueue` call site (current line ~82).
2. Update the `Enqueue` call to the new signature (passing `RequiredStores` from the in-memory template registry by `node_type`).
3. Drop any `RestoreVersion` plumbing in this file.

**Verification:** `grep -rn ConcurrencyTags core/scheduler/recalculate.go` returns nothing.

---

## Task 23 — Update `core/scheduler/scheduler.go`

**Files:** `core/scheduler/scheduler.go`

**Steps:**
1. Drop the existing `n.ConcurrencyTags` references in `sweepHeartbeatLost` and `sweepReady` (current lines ~253 and ~308).
2. Update the dispatch-claim sweep predicate from `claimed_at` to `last_heartbeat_at` per §13.5 step 1.
3. Add `lockHolderSweep`: per §13.5 step 2, find rows with `expires_at < now()`, for each call `store.ReleaseLock(tx, lh, ReleaseGiveUp)` if `lock_kind='claim'` (running the §5.6.4 algorithm if a `rimsky_claim_holders` row is active), then `DELETE` claimant-guarded.
4. Add `claimHolderGC`: per §13.5 step 3, find leaked rows whose holder node is `failed`/`fresh` and state is `'active'`, run the §5.6.4 algorithm with `actual_action = on_give_up`.
5. Add `visibilityTimeoutSweep`: per §13.5 step 4 + §7.7, iterate the local store registry's `claim-store-postgres` instances, run the SQL in §7.7 per items_table.
6. Wire all four sweeps into the existing tick under the same `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`.

**Verification:** `go test ./core/scheduler/... -count=1` passes (after Tasks 24+ land too).

---

## Task 24 — Update `core/scheduler/pure_cascade.go`

**Files:** `core/scheduler/pure_cascade.go`

**Steps:**
1. The current logic short-circuits empty-`executor` nodes at line ~76. Update to differentiate "pure cascade" (empty executor, no claim store) from "native claim-only" (empty executor, has at least one `stores: [{claim:true}]`).
2. Pure-cascade nodes still skip enqueue and emit the synthesized commit. Native claim-only nodes enqueue normally and run via the omnibus runner per §17.1 step 4b.
3. Read each node's template via the in-memory template registry.

**Verification:** `go test ./core/scheduler/... -count=1` passes.

---

## Task 25 — Search-and-cleanup remaining `RestoreVersion` / `restore_version` sites

**Files:** any remaining occurrences across the repo.

**Steps:**
1. Run `grep -rn 'RestoreVersion\|restore_version' --include='*.go' --include='*.proto' --include='*.ts' .`.
2. For each site outside already-handled files, remove the field/branch/test. Sites likely include: `core/scheduler/schedule_ticker.go` (~line 36), `core/scheduler/messages.go` (if it exists), `core/controlapi/nodes.go` (`invalidateNodeRequest.RestoreVersion`), `core/scheduler/invalidate_test.go`, `core/scheduler/invalidate_util.go`, `core/node/state_test.go` (covered in Task 19).

**Verification:** `grep -rn 'RestoreVersion\|restore_version' --include='*.go' --include='*.proto' --include='*.ts' .` returns nothing (or only matches in `docs/` which are fine — the spec keeps the term in §2 non-goals).

---

## Task 26 — Update `core/supervisor/supervisor.go`

**Files:** `core/supervisor/supervisor.go`

**Steps:**
1. Drop the `core/resource` import.
2. Add `StoreRegistry *store.Registry` field on the supervisor struct.
3. Initialize `StoreRegistry` from the `core/config/supervisor.go` config object passed in.
4. Extend the heartbeat case in `runLoop` per §13.4: gate the `rimsky_lock_holders` UPDATE on `holder_node_id IN (running nodes)`. The SQL is in §13.4.
5. Annotate blessed invariant 4 (claimant-guarded release) and 10 (atomic dispatch+lock acquisition) on the file as the §16.2 hosting location.

**Verification:** `go build ./core/supervisor/...` will fail until later supervisor tasks; defer.

---

## Task 27 — Rewrite `core/supervisor/runner.go`

**Files:** `core/supervisor/runner.go`

**Steps:**
1. Implement the omnibus runner per §17.1, structured as the algorithm in spec §13.3 + §17.1.
2. Steps 1–3 of §13.3 happen in one `pgx.Tx`: candidate selection (FOR UPDATE SKIP LOCKED), in-Go eligibility, advisory-lock + recount per named lock, claimant-guarded UPDATE on `rimsky_dispatch`, region re-evaluation, `Store.AcquireLock` per spec, `INSERT INTO rimsky_lock_holders`. Use `store.WithTx(ctx, tx)` to thread the tx into stores.
3. Step 3a rebind path: before `AcquireLock` for a region/claim spec, check for an existing `rimsky_lock_holders` row for `(holder_node_id, store_name)` owned by this supervisor with `expires_at > now()`. If found, refresh and reuse; mark `resumed=true` for the spec.
4. Step 4 verify-before-run: separate read after commit; orphan-claim-lost-race handler runs `Store.ReleaseLock(give_up)` and claimant-guarded `DELETE` per inserted lock-holder.
5. Step 4.5: separate short tx that transitions `rimsky_nodes.state` to `'running'` with reason `'dispatch_claimed'`. State-machine guard rejects non-fresh; on rejection bail to orphan handler.
6. Step 5: open native handles via `Store.OpenHandle(ctx, lh, resumed)`.
7. Step 6: dispatch path per §17.1 step 4 — has-executor → executor RPC; has-claim-no-executor → native commit; pure-cascade → handled upstream.
8. Heartbeat loop per §17.1 step 5; kill_requested polling.
9. On terminal events per §17.1 step 6 + §12.6: branch on terminal type and policy chain action; for `Complete{changed: true}` validate attributes (call `attributes.Validate`), run quality rules, run §17.1 step 6c tx (Commit + ReleaseLock + §5.6.4 resolution if `lock_kind=='claim'` and active claim-holder row exists + DELETE-or-preserve lock-holder + persist final attributes + state→fresh).
10. Annotate blessed invariants 3 (sorted multi-lock), 5 (verify-before-run), 10 (atomic dispatch+lock) on the file.

**Verification:** `go build ./core/supervisor/...` passes once Tasks 28–32 land too; defer.

---

## Task 28 — Update `core/supervisor/commit.go`

**Files:** `core/supervisor/commit.go`

**Steps:**
1. Drop the `core/resource` import.
2. Rewrite the commit path to call `Store.Commit(tx, handle)` then `Store.ReleaseLock(tx, handle, ReleaseCommit)` then run the §5.6.4 algorithm via `core/store/claimstorepg/holders.go` for held claims, all in one tx as per §17.1 step 6c.
3. Delete the `resource.CommitVersion` and `resource.RestoreVersion` call sites and their callers.

**Verification:** `grep -rn '"github.com/fallguy/rimsky/core/resource' core/supervisor/commit.go` returns nothing.

---

## Task 29 — Update `core/supervisor/terminal_outcome.go`

**Files:** `core/supervisor/terminal_outcome.go`

**Steps:**
1. Drop the `core/resource` import.
2. Rewrite per §12.6: terminal events map to ReleaseAction values (`commit` / `discard` / `give_up` / `preserve_for_resume`).
3. The mapping table from §12.6 is the source: `Complete{changed:true} → commit`, `Complete{changed:false} → commit`, `Blocked|Errored + discard_then_retry → give_up`, `Blocked|Errored + resume_then_retry → preserve_for_resume`, `Blocked|Errored + give_up → give_up`, `Errored + invalidate(targets) → give_up`.
4. Drop concurrency-tag references.

**Verification:** `go build ./core/supervisor/...` passes (after Tasks 27 + 28).

---

## Task 30 — Update `core/supervisor/on_error.go`

**Files:** `core/supervisor/on_error.go`

**Steps:**
1. Add handlers for the new error classes: `template_resolution_failed`, `attributes_schema_failed`. Default policy chain for both: `[{give_up}]` (per spec §10.4); template overrides apply normally.
2. Drop concurrency-tag references.

**Verification:** `go test ./core/supervisor/... -run TestOnError -count=1` passes (after Tasks 31–32).

---

## Task 31 — Update supervisor tests

**Files:**
- `core/supervisor/commit_test.go`
- `core/supervisor/callback_test.go`
- `core/supervisor/runner_test.go`
- `core/supervisor/supervisor_test.go`

**Steps:**
1. `commit_test.go` — drop `core/resource` imports; rewrite to test the new commit path (Store.Commit + ReleaseLock + claim-holder resolution).
2. `callback_test.go` — drop `core/resource` imports; rewrite to assert against the new attributes-callback path.
3. `runner_test.go` — drop `core/resource` imports; rewrite assertions for the omnibus runner.
4. `supervisor_test.go` — drop `core/resource` imports; update wiring to use `core/store` registry.

**Verification:** `go test ./core/supervisor/... -count=1` passes.

---

## Task 32 — Update `core/controlapi/app.go`

**Files:** `core/controlapi/app.go`

**Steps:**
1. Drop `registerResourcesRoutes(r, ...)` call and its definition.
2. Add `registerClaimsRoutes(r, ...)` for `GET /claims/:claim_id/holders` (handler defined in Task 33).
3. Add `registerAdminClaimStoresRoutes(r, ...)` for `POST /admin/claim-stores/:name/items` (handler defined in Task 33).
4. Add `registerAdminScheduleRoutes(r, ...)` for `POST /admin/scheduled-nodes/:node_id/force-fire` (handler defined in Task 33).
5. Drop the `core/resource` import.

**Verification:** `go build ./core/controlapi/...` will fail until Task 33; defer.

---

## Task 33 — Add new control-api handlers

**Files:**
- `core/controlapi/claims.go` (new)
- `core/controlapi/admin_claim_stores.go` (new)
- `core/controlapi/admin_force_fire.go` (new)

**Steps:**
1. `claims.go` — `GET /claims/:claim_id/holders` returns JSON `{holders: [...]}` from `storage.ClaimHolders().GetByClaimID(ctx, claimID)`.
2. `admin_claim_stores.go` — `POST /admin/claim-stores/:name/items` accepts `{items: [{payload: ...}, ...]}` and bulk-inserts into the items_table for the named claim store. Auth via existing admin token.
3. `admin_force_fire.go` — `POST /admin/scheduled-nodes/:node_id/force-fire` runs `UPDATE rimsky_schedules SET next_fire_at = now() WHERE node_id = $1` and returns `204` immediately. Auth via existing admin token.

**Verification:** `go test ./core/controlapi/... -run "TestClaimsRoute|TestAdminClaimStoresRoute|TestAdminForceFireRoute" -count=1` passes (write minimal handler tests as part of this task).

---

## Task 34 — Update remaining control-api handlers

**Files:**
- `core/controlapi/nodes.go`
- `core/controlapi/instances.go`
- `core/controlapi/templates.go`
- `core/controlapi/app_test.go`

**Steps:**
1. `nodes.go` — drop concurrency-tag refs and the `RestoreVersion` field from `invalidateNodeRequest`.
2. `instances.go` — drop concurrency-tag handling on instance create; integrate attributes substitution (call `core/attributes/substitution.go` for any `{params.x}` substitutions on instance creation).
3. `templates.go` — drop concurrency-tag refs; integrate the new template validation pipeline (`core/node/template_validator.go`).
4. `app_test.go` — drop resource refs; rewrite to cover new template-shape validation and the new routes from Task 33.

**Verification:** `go test ./core/controlapi/... -count=1` passes.

---

## Task 35 — Update `core/cmd/*/main.go` and `core/config/*.go`

**Files:**
- `core/cmd/rimsky-supervisor/main.go`
- `core/cmd/rimsky-control-api/main.go`
- `core/cmd/rimsky-scheduler/main.go`
- `core/cmd/rimsky-conformance/main.go`
- `core/config/supervisor.go`
- `core/config/controlapi.go`
- `core/config/scheduler.go`

**Steps:**
1. `core/config/supervisor.go` — drop `GetResource`, `ResourceFactories` fields; add `StoreFactories []store.Factory`, `Stores map[string]any` (the parsed `stores.yml`).
2. `core/config/controlapi.go` — same.
3. `core/config/scheduler.go` — add `StoreFactories`, `Stores` map. (The scheduler now needs the store registry for the visibility-timeout sweep.)
4. `core/cmd/rimsky-supervisor/main.go` — drop resource registry wiring; load `RIMSKY_STORES_CONFIG` (default `/etc/rimsky/stores.yml`); build `*store.Registry` via `Registry.BuildAll`; pass through to supervisor handle.
5. `core/cmd/rimsky-control-api/main.go` — same.
6. `core/cmd/rimsky-scheduler/main.go` — same.
7. `core/cmd/rimsky-conformance/main.go` — update CLI flags / surface for the new protocol shape (drop result-passing flags, etc.).
8. Drop `core/shared/types.go`'s `ConcurrencyTag` type if defined there.

**Verification:** `go build ./core/cmd/... ./core/config/...` passes.

---

## Task 36 — Update `executors/http-node/`

**Files:** `executors/http-node/server.go`, `executors/http-node/main.go` (touchup if needed), tests.

**Steps:**
1. `server.go` — receive `attributes` in the request body via the new proto. Pass through to the target endpoint as JSON. `userdata` opaque (stays in the request unchanged).
2. Drop `result` handling in the response path; map terminal events to the new shape (`Complete.attributes_delta`).
3. Update tests.

**Verification:** `go test ./executors/http-node/... -count=1` passes.

---

## Task 37 — Update `executors/stub/`

**Files:** `executors/stub/stub.go`, tests.

**Steps:**
1. Accept the new `ExecuteRequest` shape; expose `attributes` and `userdata` to test code.
2. In stub mode, return an immediate `Complete{changed: true, attributes_delta: <fixture>}`. Provide a small map of `node_type → field defaults` exported as `StubAttributesFor(nodeType string) map[string]any`; default behaviour returns `{}`.
3. Tests assert the new shape.

**Verification:** `go test ./executors/stub/... -count=1` passes.

---

## Task 38 — Update `executors/claude-agent/src/`

**Files:**
- `executors/claude-agent/src/attributes-tools.ts` (new — per spec §16.1, MCP tool wrappers for read / set on attributes)
- `executors/claude-agent/src/server.ts`
- `executors/claude-agent/src/internal-mcp-tools.ts`
- `executors/claude-agent/src/internal-mcp-server.ts`
- `executors/claude-agent/src/agent-run.ts`
- `executors/claude-agent/src/cli-runner.ts`
- `executors/claude-agent/src/http-bridge.ts`
- `executors/claude-agent/src/server.test.ts` and other `*.test.ts`

**Steps:**
1. `attributes-tools.ts` (new) — exports `attributesReadTool` and `attributesSetTool` MCP tool definitions (input schema, handler). The set tool POSTs `{delta: {...}}` to `${callbackUrl}/v1/attributes/${nodeId}`; the read tool returns the dispatch-time `attributes` object as captured at executor spawn. Auth header carries the supervisor-issued cancel-token.
2. `server.ts` — accept `attributes`, `attributes_schema`, `stores` in `Execute`; drop `deps_data` / `reads_data` / `instance_params` / `result`. Surface `userdata` opaquely. Wire the incremental writeback callback. Preserve the chi-route-shape async-callback POST body keying (`type`, not `kind`).
3. `internal-mcp-tools.ts` — drop the result-write tool. Re-export the tools from `attributes-tools.ts`.
4. `internal-mcp-server.ts` — wire the new tools from `attributes-tools.ts`; drop the old result-write tool registration.
5. `agent-run.ts` — agent loop consumes `attributes` (instead of `result`); ensure final `Complete` event omits `result` and optionally carries `attributes_delta` for terminal-final, or empty for incremental.
6. `cli-runner.ts` — adjust local-run scaffolding for the new attributes shape.
7. `http-bridge.ts` — adjust HTTP+JSON serialization to match the new request/event shapes.
8. Update all `*.test.ts`: mocks, fixtures, assertions for the new protocol. Preserve the async-handoff E2E test against a fake supervisor with the real chi route shape.

**Verification:** `cd executors/claude-agent && npm install && npm test && npm run build` passes.

---

## Task 39 — Update `conformance/` package

**Files:**
- `conformance/runner.go`
- `conformance/scenarios/result_serialization.go`
- `conformance/scenarios/async_handoff.go`
- `conformance/scenarios/terminal_is_last.go`
- `conformance/scenarios/execute_happy_path.go`
- any other `conformance/scenarios/*.go`

**Steps:**
1. `runner.go` — update to the new ExecuteRequest / TerminalEvent shapes; the per-scenario contracts surface from `result` to `attributes_delta`.
2. Each scenario file — replace `Complete.GetResult()` reads with `Complete.GetAttributesDelta()`. Update assertions. Drop scenarios that exercise the removed `result` semantics if they have no analog under attributes.
3. `result_serialization.go` — rename to `attributes_serialization.go`; rewrite to validate `attributes_delta` shape across encoder boundaries.
4. `async_handoff.go` — preserve, update for new shape.
5. `terminal_is_last.go` — preserve, update.
6. `execute_happy_path.go` — update for new request fields.

**Verification:** `go build ./conformance/... ./core/cmd/rimsky-conformance/...` passes; running the conformance binary against the stub executor `go run ./core/cmd/rimsky-conformance --endpoint <stub-addr> --transport grpc` succeeds.

---

## Task 40 — Rewrite `core/scenario/harness.go`

**Files:**
- `core/scenario/harness.go`
- `core/scenario/harness_test.go`
- `core/scenario/harness_util.go`

**Steps:**
1. Drop all `core/resource/...` imports; add `core/store/...`, `core/attributes/...`.
2. Replace `factories.Register("inline-jsonb", ...)` (current line ~82-83) with `core/store/stub/` registration as the default test store.
3. Delete `getResourceForOwner` and adjacent resource helpers (current lines ~181-206).
4. Rewrite `templateSpecToJSON` (current lines ~308-412): every `concurrency_tags`, `owns_resources`, `reads_resources`, `restore_version` key is replaced with `stores`, `locks`, `attributes`, `claim_resolutions`. Add Go helpers `withStores(...)`, `withLocks(...)`, `withAttributes(...)`, `withClaimResolutions(...)` so tests can construct templates fluently.
5. Wire the attributes-substitution + JSON Schema validation paths into the in-process supervisor + control-api the harness starts.
6. Wire the lock-holder + claim-holder + visibility-timeout sweeps into the in-process scheduler.
7. Preserve the existing `Clock` injection (`shared.Clock` interface) so scenarios can advance test time past `5 × heartbeat_interval` for orphan-reap tests.
8. Update `harness_test.go`, `harness_util.go` to the new harness signature.

**Verification:** `go test ./core/scenario/... -count=1` passes.

---

## Task 41 — Delete obsolete scenario tests

**Files (deleted):**
- `test/scenarios/double_buffering_test.go`
- `test/scenarios/rollback_via_restore_version_test.go`

**Steps:**
1. `rm` both files.

**Verification:** `ls test/scenarios/double_buffering_test.go` returns "No such file" (success).

---

## Task 42 — Migrate existing scenario tests batch 1

**Files:**
- `test/scenarios/cascade_invalidate_test.go`
- `test/scenarios/fan_out_pattern_test.go`
- `test/scenarios/pure_cascade_test.go`
- `test/scenarios/happy_path_executor_test.go`

**Steps:**
1. For each test, rewrite the template construction to use the new harness fluent helpers (`withStores`, `withLocks`, `withAttributes`).
2. Where the test verifies "this resource has version N", switch to "this node's `rimsky_node_attributes.data` contains field X".
3. Where the test wires up resources for data flow between nodes, replace with `attributes.schema.properties.<f>.source: "{{deps.<n>.<f>}}"` directives.
4. Preserve the test's behavioural intent.

**Verification:** `go test ./test/scenarios -run "TestCascadeInvalidate|TestFanOutPattern|TestPureCascade|TestHappyPathExecutor" -count=1` passes.

---

## Task 43 — Migrate existing scenario tests batch 2

**Files:**
- `test/scenarios/agentic_executor_async_handoff_test.go`
- `test/scenarios/executor_blocked_test.go`
- `test/scenarios/give_up_test.go`
- `test/scenarios/heartbeat_loss_reenqueue_test.go`

**Steps:**
1. Rewrite each per Task 42's pattern.
2. `heartbeat_loss_reenqueue_test.go` — assertions about lock-holder cleanup now check `rimsky_lock_holders` directly (in addition to `rimsky_dispatch.claimed_by`).

**Verification:** `go test ./test/scenarios -run "TestAgenticExecutorAsyncHandoff|TestExecutorBlocked|TestGiveUp|TestHeartbeatLossReenqueue" -count=1` passes.

---

## Task 44 — Migrate existing scenario tests batch 3

**Files:**
- `test/scenarios/no_op_commit_test.go`
- `test/scenarios/orphaned_claim_test.go`
- `test/scenarios/scheduled_node_test.go`
- `test/scenarios/state_machine_same_state_rejected_test.go`

**Steps:**
1. `no_op_commit_test.go` — rewrite to assert `Commit` returns `Changed: false` and no `attributes_committed` event is emitted (the `current_version_id` assertion no longer applies).
2. `orphaned_claim_test.go` — rewrite to exercise the new `rimsky_lock_holders` orphan path.
3. `scheduled_node_test.go` — preserve schedule semantics; update template construction.
4. `state_machine_same_state_rejected_test.go` — rewrite to use `ReasonDispatchClaimed` (existing) without resource references.

**Verification:** `go test ./test/scenarios -run "TestNoOpCommit|TestOrphanedClaim|TestScheduledNode|TestStateMachineSameStateRejected" -count=1` passes.

---

## Task 45 — Migrate existing scenario tests batch 4 + rename concurrency_tag_limit

**Files:**
- `test/scenarios/unresolved_executor_test.go`
- `test/scenarios/verify_before_run_race_test.go`
- `test/scenarios/concurrency_tag_limit_test.go` (renamed/moved)
- `test/scenarios/locks/named_lock_counting_test.go` (new path)

**Steps:**
1. `unresolved_executor_test.go` — preserve; minor template-shape update.
2. `verify_before_run_race_test.go` — preserve; uses the new harness with stub store.
3. Move `concurrency_tag_limit_test.go` to `test/scenarios/locks/named_lock_counting_test.go`; rename test functions; semantics preserved.

**Verification:** `go test ./test/scenarios/... -count=1` passes.

---

## Task 46 — Add `test/scenarios/stores/` scenarios

**Files (new):**
- `test/scenarios/stores/filesystem_direct_write_test.go`
- `test/scenarios/stores/filesystem_direct_disjoint_regions_test.go`
- `test/scenarios/stores/filesystem_direct_overlapping_regions_test.go`
- `test/scenarios/stores/filesystem_direct_read_concurrent_with_write_test.go`
- `test/scenarios/stores/store_pool_specialization_test.go`

**Steps:**
1. Implement each per the spec §19.1 description. Use the new harness; wire a real `core/store/filesystem/` store.
2. `store_pool_specialization_test.go` spins up two supervisors, each with different `accepted_stores`, asserts dispatch routing.

**Verification:** `go test ./test/scenarios/stores/... -count=1` passes.

---

## Task 47 — Add `test/scenarios/locks/` scenarios

**Files (new in `test/scenarios/locks/`):**
- `named_lock_mutex_test.go`
- `region_lock_conflict_test.go`
- `lock_atomic_acquisition_test.go`
- `lock_heartbeat_extends_expiry_test.go`
- `lock_orphan_reap_test.go`
- `lock_sorted_acquisition_no_deadlock_test.go`
- `lock_claimant_guarded_release_test.go`

(plus `named_lock_counting_test.go` from Task 45.)

**Steps:**
1. Each test exercises the §19.1 behaviour against the new harness.
2. `lock_atomic_acquisition_test.go` — assert that a forced `Store.AcquireLock` failure rolls back the whole tx (no dangling lock-holder rows, no claimed dispatch row). Use the stub store with a configurable failing AcquireLock.
3. `lock_orphan_reap_test.go` — kill a supervisor mid-run; advance the test clock past `5 × heartbeat_interval`; assert lock-holder rows reaped and downstream re-dispatchable.
4. `lock_sorted_acquisition_no_deadlock_test.go` — two supervisors contending on overlapping lock sets; no deadlock under `-race -count=10`.

**Verification:** `go test ./test/scenarios/locks/... -count=1 -race` passes.

---

## Task 48 — Add `test/scenarios/attributes/` scenarios

**Files (new in `test/scenarios/attributes/`):**
- `attributes_substitution_from_deps_test.go`
- `attributes_substitution_from_claim_test.go`
- `attributes_substitution_from_params_test.go`
- `attributes_required_missing_template_resolution_failed_test.go`
- `attributes_optional_missing_omitted_test.go`
- `attributes_schema_validation_at_commit_test.go`
- `attributes_incremental_writeback_test.go`
- `attributes_terminal_final_writeback_test.go`
- `attributes_resumable_preserve_test.go`
- `attributes_resumable_false_clears_test.go`
- `attributes_substitution_race_lost_test.go`
- `userdata_opaque_test.go`

**Steps:**
1. Each test per §19.1.
2. `userdata_opaque_test.go` — set userdata to `{"prompt": "{{deps.x.value}}"}` and assert the executor receives that string verbatim (no substitution by rimsky).
3. `attributes_substitution_race_lost_test.go` — uses the harness's clock controls to invalidate an upstream node between eligibility and substitution; asserts the runner bails with `orphaned_claim_lost_race`.

**Verification:** `go test ./test/scenarios/attributes/... -count=1` passes.

---

## Task 49 — Add `test/scenarios/claim_stores/` scenarios

**Files (new in `test/scenarios/claim_stores/`):**
- `queue_claim_fifo_test.go`
- `claim_empty_no_dispatch_test.go`
- `claim_concurrent_supervisors_atomic_test.go`
- `queue_on_commit_delete_test.go`
- `queue_on_give_up_release_to_head_test.go`
- `ring_buffer_release_to_back_test.go`
- `claim_hold_linear_chain_test.go`
- `claim_hold_fan_out_first_delete_wins_test.go`
- `claim_hold_fan_out_release_count_test.go`
- `claim_resolutions_missing_template_deploy_fails_test.go`
- `claim_resumption_test.go`
- `multi_claim_test.go`

**Steps:**
1. Each test per §19.1.
2. Use the real `core/store/claimstorepg/` store against a testcontainers postgres (the harness already provisions one).
3. `multi_claim_test.go` — node has two `stores: [{name:X, claim:true}, {name:Y, claim:true}]` from different stores; both populate `attributes` (under namespaced keys via `{{claim.X.payload.f}}` and `{{claim.Y.payload.f}}`); both resolve per their store's `on_commit`.

**Verification:** `go test ./test/scenarios/claim_stores/... -count=1` passes.

---

## Task 50 — Implement smoke fixture

**Files:**
- `test/smoke/setup.go` (new)
- `test/smoke/stores_redesign_smoke_test.go` (new)
- `test/smoke/fixtures/template.yml` (new — the §11.5 template)

**Steps:**
1. `setup.go` — `BringUpStack(t)` per §19.2: testcontainers postgres; run migration; create `topics_items` items table; start scheduler/supervisor/control-api in-process with 50ms scheduler tick; build the stores config **programmatically** in Go (because the filesystem-direct root must be a per-test `t.TempDir()`) — the helper constructs an in-memory `store.StoresConfig{Stores: ...}` directly rather than loading a static YAML file. Returns handles.
2. (No static `stores.yml` fixture file; the config is built in-process. The fixtures directory may still hold the template YAML below.)
3. `fixtures/template.yml` — the §11.5 four-node template (claim-topic + scope + draft + review) with `model-budget: limit: 50`.
4. `stores_redesign_smoke_test.go` — implements the §19.2 test:
   - `BringUpStack(t)`.
   - Bulk-insert 100 items.
   - Deploy template via `POST /templates`; create instance via `POST /instances`.
   - Phase 1: 100 sequential `POST /admin/scheduled-nodes/{claim-topic-node-id}/force-fire`, each followed by a `WaitForState` poll on the source node (per-fire timeout `5*time.Second`; poll interval 50ms).
   - Phase 2: poll every 250ms (timeout 300s) for steady-state per §19.2.
   - Final assertions per §19.2.
5. Stub claude-agent + http-node-stub configured per §19.2 step 5: returns `Complete{changed: true, attributes_delta: <node-type defaults>}`.

**Verification:** `go test ./test/smoke/... -count=1 -timeout 10m` passes.

---

## Task 51 — Update `deploy/`

**Files:**
- `deploy/docker-compose.yml`
- `deploy/stores.yml` (new)
- `deploy/Dockerfile.go-base` / `Dockerfile.http-node` / `Dockerfile.claude-agent` (touchups if needed)
- `deploy/build-images.sh` (touchups if needed)

**Steps:**
1. `deploy/stores.yml` — declares `content` (filesystem direct at `/workspace/content`) and `topics-ring` (claim-store-postgres, items_table `topics_items`).
2. `deploy/docker-compose.yml` — add `RIMSKY_STORES_CONFIG=/etc/rimsky/stores.yml` to scheduler / supervisor / control-api services; mount `./stores.yml` into each. Add a one-shot init container or `command: bash -c '… create topics_items table …'` to the postgres service so the items table is in place when the smoke test runs (or document operator responsibility).
3. Dockerfiles: ensure paths are still correct after any source-tree shuffles.
4. `build-images.sh`: re-run; ensure no errors.

**Verification:** `deploy/build-images.sh` exits 0; `docker compose -f deploy/docker-compose.yml up -d` brings the stack up; `curl -fsS http://localhost:8080/health` returns 200; `docker compose down -v` cleans up.

---

## Task 52 — Update Helm chart (best-effort)

**Files:** `deploy/kubernetes/rimsky-chart/...`

**Steps:**
1. Per CLAUDE.md "Helm chart is known stale". Update env-var names where they have drifted: add `RIMSKY_STORES_CONFIG` references; correct `RIMSKY_SUPERVISOR_CONFIG` paths. Add a stores-config ConfigMap mirroring `deploy/stores.yml`.
2. Run `helm lint deploy/kubernetes/rimsky-chart`.
3. If `helm lint` exits non-zero, **do not** attempt deeper repairs — just prepend a `# TODO: stale, see CHANGELOG entry for stores-redesign` block at the top of `Chart.yaml` and document the remaining drift in CHANGELOG. Then exit the task.
4. If `helm` is not installed, skip steps 2–3 and leave the TODO block in place; record a note in CHANGELOG.

**Verification:** Either `helm lint deploy/kubernetes/rimsky-chart` exits 0, **or** the `# TODO: stale` block is present at the top of `Chart.yaml` and CHANGELOG records the deferral. Both outcomes are acceptable per the chart's pre-existing stale status.

---

## Task 53 — Update `docs/architecture.md`

**Files:** `docs/architecture.md`

**Steps:**
1. §1.2 — rename "Resource library" to "Store library"; update prose for the redesign.
2. §3 (import rules) — list `core/store/` allowed importers; remove `core/resource/`.
3. §5 — list blessed invariants 9–14 with file references (per spec §18 source annotations).
4. §8 — enumerate the new storage tables (per §9 of the spec).

**Verification:** `grep -rn 'core/resource' docs/architecture.md` returns nothing; the doc references `core/store/` and `core/attributes/`.

---

## Task 54 — Rewrite `docs/protocol.md`

**Files:** `docs/protocol.md`

**Steps:**
1. Full rewrite per spec §12. Sources: the rewritten `proto/v1/node_executor.proto`.
2. Cover the request shape, terminal-event shapes, async handoff, incremental attributes callback, the supervisor-side action mapping (§12.6 table).

**Verification:** `grep -rn 'deps_data\|reads_data\|instance_params' docs/protocol.md` returns nothing.

---

## Task 55 — Update `docs/node-graph-design.md`

**Files:** `docs/node-graph-design.md`

**Steps:**
1. §3.4 — userdata opaque section.
2. §4 — rename "Resources" to "Stores"; rewrite content for the new vocabulary (Store, Region, Lock, Handle, Sidecar, Claim, Attributes).
3. §6 — add `template_resolution_failed` and `attributes_schema_failed` to the error model.
4. §7 — substitution two-phase per spec §10.1.
5. §8 — node contract reflects new fields (`stores`, `locks`, `attributes`, `claim_resolutions`).

**Verification:** `grep -rn 'owns_resources\|reads_resources\|concurrency_tags' docs/node-graph-design.md` returns nothing.

---

## Task 56 — Update `docs/operator-guide.md`

**Files:** `docs/operator-guide.md`

**Steps:**
1. Document `RIMSKY_STORES_CONFIG` env var and `stores.yml` schema.
2. Document `POST /admin/claim-stores/:name/items` endpoint and its admin-token auth.
3. Document `POST /admin/scheduled-nodes/:node_id/force-fire`.
4. Document the dev-DB-nuking step required when adopting the redesign.
5. Document operator-owned items-table creation for `claim-store-postgres`.

**Verification:** `grep 'RIMSKY_STORES_CONFIG\|/admin/claim-stores' docs/operator-guide.md` matches.

---

## Task 57 — Update `docs/executor-author-guide.md`

**Files:** `docs/executor-author-guide.md`

**Steps:**
1. Rewrite for the new protocol: receiving `attributes` + `attributes_schema` + `stores` map; opaque `userdata`; incremental writeback callback `POST /v1/attributes/{node_id}`; per-store handle shapes; terminal-event shapes; async-handoff path-binding.

**Verification:** `grep 'deps_data\|reads_data' docs/executor-author-guide.md` returns nothing.

---

## Task 58 — Add `docs/store-author-guide.md`; delete `docs/resource-author-guide.md`

**Files:**
- `docs/store-author-guide.md` (new)
- `docs/resource-author-guide.md` (deleted)

**Steps:**
1. `store-author-guide.md` — Worked example: implementing a custom store. Cover: capability declaration, `Store` interface, optional `ClaimableStore` / `ResumableStore`, `RegionsConflict` purity contract, `UnmarshalRegion`, transaction semantics for stores that mutate during `AcquireLock` (`store.TxFromContext`).
2. Worked example: re-implementing the filesystem direct-mode store from scratch.
3. Document known limitations: multi-store atomic commit not provided; direct-mode quality rules warned-and-ignored.
4. `rm docs/resource-author-guide.md`.

**Verification:** `ls docs/store-author-guide.md` exists; `ls docs/resource-author-guide.md` returns "No such file".

---

## Task 59 — Move companion design doc to `docs/history/`

**Files:**
- `docs/2026-04-25-stores-redesign.md` (moved)
- `docs/history/2026-04-25-stores-redesign.md` (new location)

**Steps:**
1. `mkdir -p docs/history`.
2. `mv docs/2026-04-25-stores-redesign.md docs/history/`.

**Verification:** `ls docs/history/2026-04-25-stores-redesign.md` exists; `ls docs/2026-04-25-stores-redesign.md` returns "No such file".

---

## Task 60 — Update `CHANGELOG.md`

**Files:** `CHANGELOG.md`

**Steps:**
1. Rewrite the `## Unreleased` section as a single bullet describing the stores redesign.
2. Mark explicitly: `**BREAKING:** dev DB must be nuked before adoption (no migrations from the old schema).`
3. Mention every removed concept: resources, concurrency tags, restore-version, deps_data/reads_data/instance_params, result.
4. Mention every added concept: stores (filesystem-direct + claim-store-postgres + stub), attributes, locks (named/region/claim), force-fire admin endpoint.

**Verification:** `grep '^## Unreleased' CHANGELOG.md` matches and the text below it covers the redesign.

---

## Task 61 — Update `CLAUDE.md`

**Files:** `CLAUDE.md`

**Steps:**
1. "Package import rules" — replace `core/resource/` rule with `core/store/` rule.
2. "Blessed invariants" — list the 14 invariants per spec §18 with file references.
3. "Non-obvious gotchas" — add: `RIMSKY_STORES_CONFIG` is loaded by control-api / supervisor / scheduler; userdata is never substituted by rimsky; held-claim resolution algorithm is in `core/store/claimstorepg/holders.go`; force-fire endpoint is admin-only.
4. "Where to look first" — add `docs/store-author-guide.md`; remove `docs/resource-author-guide.md`.
5. "Build & test" — preserve.

**Verification:** `grep '@blessed-invariant\|core/store' CLAUDE.md` matches.

---

## Task 62 — Cleanup any remaining `core/resource` imports across the repo

**Files:** any.

**Steps:**
1. `grep -rn '"github.com/fallguy/rimsky/core/resource' --include='*.go' .` returns nothing.
2. If anything remains, fix the importer (drop the import + any uses).

**Verification:** the grep above returns no matches.

---

## Task 63 — Cleanup any remaining `concurrency_tags` / `ConcurrencyTags` references across the repo

**Files:** any.

**Steps:**
1. `grep -rn 'concurrency_tags\|ConcurrencyTags' --include='*.go' --include='*.sql' --include='*.proto' .` returns nothing (matches only in spec doc / changelog / history file are fine).
2. If anything remains in source/test/sql, fix.

**Verification:** the grep above returns no matches outside `docs/`.

---

## Task 64 — Final verification

**Files:** none modified.

**Steps:**
1. `go build ./...` exits 0.
2. `go test ./... -count=1` exits 0 (testcontainers spin-up time accepted).
3. `go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3` exits 0.
4. `make lint` exits 0.
5. `make proto-gen && git diff --exit-code proto/v1/gen/` exits 0.
6. `cd executors/claude-agent && npm install && npm test && npm run build` all exit 0.
7. `go test ./test/smoke/... -count=1 -timeout 10m` exits 0.
8. `deploy/build-images.sh` exits 0.
9. `docker compose -f deploy/docker-compose.yml up -d`, then `curl -fsS http://localhost:8080/health` returns 200, then `docker compose -f deploy/docker-compose.yml down -v`.
10. `grep -rn '"github.com/fallguy/rimsky/core/resource' --include='*.go' .` returns nothing.
11. `grep -rn 'concurrency_tags\|ConcurrencyTags' --include='*.go' --include='*.sql' --include='*.proto' .` returns nothing outside `docs/`.

**Verification:** all 11 checks pass.

**Tooling preconditions for Task 64:** `docker` (for testcontainers and docker-compose), `node` and `npm`, `go`, `protoc` + `protoc-gen-go` (installed by Task 4 if missing). If `helm` is unavailable, Task 52's TODO escape hatch covers it.

Run `go mod tidy` once at the start of Task 64 to ensure go.sum/go.mod are consistent after the resource removal cascade.

---

## Manual checks after completion

None. Every verification in this plan is automatable. The user reviews the resulting working-tree diff and decides whether to commit / push.
