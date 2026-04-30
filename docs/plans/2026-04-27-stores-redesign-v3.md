# Stores Redesign v3 — Implementation Plan

**Spec:** `docs/specs/2026-04-27-stores-redesign-v3-design.md`
**Predecessor:** `docs/plans/2026-04-27-stores-redesign-v2.md` (v2 plan; landed in commit `e46b952`).
**Glossary:** `docs/glossary.md`

**Working directory:** `/Users/patrick/Documents/projects/research/verantel/submodules/rimsky`. All paths in this plan are relative to that directory unless absolute. Git submodule.

**Goal:** Move the standard store implementations out-of-process per the v3 spec — define wire protocol for the 5 + 1 verbs, build a remote-client gRPC implementation of the rimsky-side `Store` interface, migrate `filesystem` / `postgres` / `stub` impls to standalone binaries under `stores/`, redesign invariant 10 for the no-tx-sharing world, and strip the four rimsky-side substrate-knowledge violations as natural consequences.

**Architecture:** Rimsky processes (`rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`) talk to stores exclusively via the 5-verb gRPC protocol. Standard store impls become separate binaries under a new `stores/` directory mirroring `executors/`. The substrate's state is decoupled from rimsky's bookkeeping tx; substrate handles its own atomicity and crash recovery. `Factory` / `Registry.BuildAll` / `StoresConfig` are removed from rimsky.

**Tech Stack:** Go 1.21+ (root module `github.com/fallguy/rimsky`; `go.mod` is at the repo root, NOT under `core/`), pgx/v5, postgres 15, testcontainers-go (real postgres for scenario tests), stdlib `log/slog`, go-chi/chi, robfig/cron/v3, JSON Schema (santhosh-tekuri/jsonschema/v5), gRPC + protobuf (existing for executors; same toolchain reused for stores).

**Build commands** (referenced throughout):
- `go build ./...` — full-tree build
- `go test ./... -count=1` — full-tree tests (testcontainers tests pull `postgres:15`; Docker socket required)
- `go test ./... -race -count=1` — race detector
- `make lint` — golangci-lint (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive)
- `make tidy` — `go mod tidy`
- `make proto-gen` — regenerate proto bindings (only if `proto/v1/*.proto` changed)

**Pre-v1:** No production data. No backwards-compat shims. Working tree is allowed to be uncompilable at intermediate checkpoints — the plan calls out where `go build` should pass and where it's expected to fail.

**Convention used in this plan:**
- "Spec §X.Y" refers to a section in `docs/specs/2026-04-27-stores-redesign-v3-design.md`. Read the spec at T0; refer to it whenever a "spec §..." reference appears.
- "v2 spec §X.Y" refers to the predecessor spec at `docs/specs/2026-04-27-stores-redesign-v2-design.md`.
- "Glossary: X" refers to a term defined in `docs/glossary.md`.

**Implementation notes file.** Create `docs/plans/2026-04-27-stores-redesign-v3-notes.md` on first deviation. Append one entry per item per the `ok-planner:execute-plan` convention. Walk it with the user at end of run.

---

## Pre-flight

### T0: Establish baseline

**Files:** none (read-only).

**Steps:**

1. Read the spec end-to-end: `docs/specs/2026-04-27-stores-redesign-v3-design.md`.
2. Read the glossary: `docs/glossary.md`. Vocabulary from this is authoritative.
3. Read repo rules: `CLAUDE.md`, `.claude/rules/rules.md`, `.claude/rules/cold-read-cheatsheet.md`.
4. Read the current `Store` interface and types: `core/store/interface.go`, `core/store/types.go`, `core/store/registry.go`, `core/store/lockholders.go`, `core/store/conflict.go`, `core/store/tx.go`, `core/store/doc.go`.
5. Read the current store impls (about to be moved out): `core/store/filesystem/`, `core/store/postgres/`, `core/store/stub/`.
6. Read the current supervisor acquisition flow: `core/supervisor/runner.go`, `core/supervisor/runner_acquire.go`, `core/supervisor/runner_terminal.go`, `core/supervisor/auto_terminal.go`.
7. Read the current rimsky-side cmd binaries: `core/cmd/rimsky-supervisor/main.go`, `core/cmd/rimsky-scheduler/main.go`, `core/cmd/rimsky-control-api/main.go`.
8. Read the existing config layer: `core/config/supervisor.go`, `core/config/scheduler.go`, `core/config/controlapi.go`.
9. Read the existing executor protocol for proto pattern reference: `proto/v1/node_executor.proto`, `core/executor/`, one executor binary (`executors/http-node/`).
10. Read the existing scheduler sweep: `core/scheduler/sweep_locks.go`, `core/scheduler/scheduler.go`.
11. Read the queue: `core/queue/postgres/queue.go`.
12. Read the controlapi admin route: `core/controlapi/admin_claim_stores.go`, `core/controlapi/templates.go::validatorHooksFor`, `core/node/template_validator.go::RegistryHooks`.
13. Read the smoke fixture: `test/smoke/setup.go`, `test/smoke/stores_redesign_smoke_test.go`.
14. Read the deploy stack: `deploy/docker-compose.yml`, `deploy/build-images.sh`, `deploy/stores.yml`, the `Dockerfile.*` files.

**Verification:**
```sh
cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
go build ./...
go test ./... -count=1 || true        # capture baseline; pre-existing failures recorded
make lint || true                     # capture baseline
```

Note any pre-existing failures so they aren't attributed to this work later. Working tree should be clean from the v2 commit (`e46b952`).

---

## Foundation: wire protocol

These tasks define the wire surface the rest of v3 implements against.

### T1: Define `proto/v1/store_service.proto`

**Files:** `proto/v1/store_service.proto` (new file).

**Steps:**

1. Create `proto/v1/store_service.proto`. Use `proto/v1/node_executor.proto` as the structural reference for proto3 syntax, package, go_package, options.
2. Define the `StoreService` service with six RPCs per spec §4.1 + §5.1:
   - `Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse)`
   - `Open(OpenRequest) returns (OpenResponse)`
   - `Commit(CommitRequest) returns (CommitResponse)`
   - `Abandon(AbandonRequest) returns (AbandonResponse)`
   - `Delete(DeleteRequest) returns (DeleteResponse)`
   - `Release(ReleaseRequest) returns (ReleaseResponse)`
3. Define request and response messages. `claim_id` is a `string` (UUID textual form). `region`, `address`, `payload` are `bytes`. `selector`, `intent`, `alias`, `store_name`, `policy_override` are `string`. Response messages for `Open` carry the `ClaimResult` (address, payload, region all `bytes`). Response messages for `Commit` / `Abandon` / `Delete` / `Release` are empty (errors flow as gRPC status codes per spec §5.3).
4. `CapabilityStruct` message has one field: `string write_semantics` (per spec §4.8).
5. Annotate the file with a header comment referencing spec §4.1, §5.1, and noting that this is the v3 store wire protocol; HTTP+JSON bridge handlers (per spec §5.2) decode JSON to these messages.

**Verification:**
```sh
make proto-gen
ls proto/v1/gen/store_service*.go     # confirm generated bindings exist
go build ./proto/v1/gen/...
```

### T2: Audit generated bindings

**Files:** `proto/v1/gen/store_service.pb.go`, `proto/v1/gen/store_service_grpc.pb.go` (generated).

**Steps:**

1. Read both generated files to confirm the surface matches the proto definition.
2. Confirm field numbers are stable (proto3); confirm no unintended fields appear.
3. Run gofmt to confirm the generated bindings are formatted: `gofmt -l proto/v1/gen/`.

**Verification:**
```sh
go build ./proto/v1/gen/...
go vet ./proto/v1/gen/...
```

---

## Foundation: rimsky-side `Store` interface and helpers

### T3: Rewrite `core/store/interface.go` per spec §11.2

**Files:** `core/store/interface.go`.

**Steps:**

1. Rewrite the `Store` interface to:
   - Drop methods: `Kind() string`, `RegionsConflict(a, b []byte) bool`, `UnmarshalRegion(raw []byte) ([]byte, error)` (per spec §11.1).
   - Keep / update method signatures:
     - `Capabilities(ctx context.Context) (Capabilities, error)` (gains `ctx`; this is the startup-handshake RPC).
     - `Open(ctx, claimID, spec) (ClaimResult, error)`.
     - `Commit(ctx, claimID, region, address, policyOverride) error`.
     - `Abandon(ctx, claimID, region, address, policyOverride) error`. `address` may be nil (per spec §4.3).
     - `Delete(ctx, claimID, region) error`.
     - `Release(ctx, claimID, region, address) error`.
2. Add `Name() string` if it isn't already there (rimsky-side identification — the operator-chosen name in stores.yml; not on the wire).
3. Update file-level docstring to reference spec §3 (principles) and §4 (protocol surface).
4. Update or add `@blessed-invariant 9a` annotation per spec §11 (lock state lives only in postgres).

**Verification:**
```sh
go build ./core/store/...
```
Will fail in the rest of `core/store/` because dependents still use the old shape. Confirm `interface.go` itself compiles in isolation; subsequent tasks fix dependents.

### T4: Add `ClaimID` type alias and update `core/store/types.go`

**Files:** `core/store/types.go`.

**Steps:**

1. Add `type ClaimID string` (or `[16]byte` if the codebase prefers binary UUIDs — match the existing convention used for `rimsky_lock_holders.id`). Annotate that this is rimsky-generated, client-side, passed on every protocol verb per spec §4.2.
2. `ClaimSpec`, `ClaimResult` keep their v2 shapes (per spec §4.6, §4.7).
3. `Capabilities` keeps its single-field shape (per spec §4.8).
4. Confirm `@blessed-invariant 20` annotation on `ClaimResult` is intact.

**Verification:**
```sh
go build ./core/store/...
```
Dependents still fail. Confirm `types.go` itself compiles.

### T5: Switch `core/store/lockholders.go` to client-side UUID generation

**Files:** `core/store/lockholders.go`.

**Steps:**

1. Update `LockHolderInsertInput` to require an explicit `ID` field (UUID; client-generated by the supervisor). Document that the column default `gen_random_uuid()` stays as a safety net but the supervisor MUST supply the id.
2. Update `Insert` (and any other write paths) to use the client-supplied id rather than relying on `RETURNING id`.
3. If the existing code uses `INSERT ... RETURNING id`, switch to `INSERT (... id ...) VALUES (... $N ...)` with the client id, and drop the RETURNING clause where the id was the only returned value.
4. Confirm the UUID type (likely `uuid.UUID` from `github.com/google/uuid` if already in deps, else `pgtype.UUID`) matches `core/store/types.go::ClaimID`.

**Verification:**
```sh
go build ./core/store/...
```

### T6: Extend `core/store/conflict.go` with `RegionsByteEqual`

**Files:** `core/store/conflict.go`.

**Steps:**

1. Keep `ModeCoexists` unchanged.
2. Add `func RegionsByteEqual(a, b []byte) bool { return bytes.Equal(a, b) }`. Annotate with reference to spec §7.7 (rimsky-side byte-equal comparison; substrate canonicalizes).
3. The function is the rimsky-side replacement for the substrate-implemented `RegionsConflict` that v2 had on the `Store` interface (per spec §11.1).

**Verification:**
```sh
go build ./core/store/...
go test ./core/store/... -count=1     # tests for ModeCoexists should still pass
```

### T7: Strip `core/store/tx.go` entirely

**Files:** `core/store/tx.go` (delete file).

**Steps:**

1. Delete `core/store/tx.go` (`WithTx`, `TxFromContext`, plus any helper types). Per spec §11.1, this is removed entirely — no more tx-sharing across the wire (per §7.3).
2. Confirm no public consumers outside the file. (Consumers in `core/supervisor/runner_acquire.go` and `core/store/postgres/store.go` will be rewritten in later tasks.)

**Verification:**
```sh
go build ./core/store/...
```
Will fail in supervisor and postgres store. Confirm `core/store/` itself (without `tx.go`) compiles by removing references in subsequent tasks.

---

## Foundation: rimsky-side registry and remote client

### T8: Strip `Factory`, `Registry.BuildAll`, `StoresConfig` from `core/store/registry.go`

**Files:** `core/store/registry.go`.

**Steps:**

1. Delete the `Factory` interface entirely (per spec §11.1).
2. Delete `StoresConfig` type entirely.
3. Delete `Registry.BuildAll` method.
4. Delete `Registry.Register(Factory)` method.
5. Reduce `Registry` to a simple `map[string]Store` populated externally:
   ```go
   type Registry struct { stores map[string]Store }
   func NewRegistry() *Registry { return &Registry{stores: map[string]Store{}} }
   func (r *Registry) Add(name string, s Store) { r.stores[name] = s }
   func (r *Registry) Get(name string) (Store, bool) { s, ok := r.stores[name]; return s, ok }
   func (r *Registry) Stores() map[string]Store { ... }   // snapshot copy
   func (r *Registry) Close() { ... }                     // walks Stores, calls closer interface
   ```
6. Keep `NamedLocksConfig`, `NamedLockConfig`, `Validate()` per v2 (operator-config for named locks is unchanged).
7. Document the simplification in a file-level comment referencing spec §3.1 + §6.

**Verification:**
```sh
go build ./core/store/...
```
Dependents still fail. Confirm `core/store/registry.go` itself compiles.

### T9: Create `core/store/remote/` — gRPC client implementing `Store`

**Files:** `core/store/remote/` (new package: `client.go`, `dial.go`, `doc.go`).

**Steps:**

1. Create `core/store/remote/doc.go` — package docstring referencing spec §5 (wire format) and §3.1 (protocol-only).
2. Create `core/store/remote/client.go`:
   - `type Client struct { name string; conn *grpc.ClientConn; rpc storev1.StoreServiceClient; caps store.Capabilities }`.
   - Implement the `core/store.Store` interface (T3) by translating each call to the corresponding gRPC RPC. `claim_id` flows on every call.
   - `Capabilities()` returns the cached value (set during dial; see T9.3).
   - `Name()` returns the operator-chosen name supplied at construction.
   - Errors map gRPC status codes to Go errors per spec §5.3.
3. Create `core/store/remote/dial.go`:
   - `Dial(ctx, name, endpoint string) (*Client, error)`:
     - Dial the gRPC endpoint (use existing executor-side dial pattern from `core/executor/client.go` for reference: `grpc.NewClient` with `insecure.NewCredentials()` for now; note in code that mTLS / transport auth is operator-deployment per spec auth-blind §13.3).
     - RPC `Capabilities()` once.
     - Cache the result on the `Client`.
     - Return the constructed `Client`.
4. Implement `Close()` so the Registry's `Close()` can release the gRPC connection (per spec §11.1's `closer` interface convention from v2's `Registry.Close()`).
5. Add a `ValidateCapabilities(declared store.Capabilities) error` helper that compares the cached capabilities against the operator-declared block (strict equality per spec §6.2).

**Verification:**
```sh
go build ./core/store/remote/...
go vet ./core/store/remote/...
```

### T10: Create `core/store/storetest/` — unit-test fake `Store`

**Files:** `core/store/storetest/` (new package: `fake.go`, `doc.go`).

**Steps:**

1. Create `core/store/storetest/doc.go` — package docstring noting this is the unit-test fake satisfying `core/store.Store` for tests where the wire isn't relevant (per spec §9.1).
2. Create `core/store/storetest/fake.go`:
   - `type Fake struct { ... }` implementing `store.Store`.
   - In-memory state: a map keyed by `claim_id` storing `(region, address, payload)` per claim.
   - Methods: `Open` returns a configurable `ClaimResult`; `Commit` / `Abandon` / `Delete` / `Release` mark internal state and return the configured error (or nil).
   - Recorder pattern: `Calls() []FakeCall` so tests can assert verb sequences.
   - Configurable: `OpenFunc func(...)` callback override; default is "echo selector as region and address."
   - Fake `Capabilities()` returns whatever the test set.

**Verification:**
```sh
go build ./core/store/storetest/...
go vet ./core/store/storetest/...
```

---

## Strip rimsky-side substrate-knowledge violations

These tasks remove the four inertness violations (per spec §3.3 + §11.1). They are independent and can be done in any order.

### T11: Strip the admin items endpoint

**Files:** `core/controlapi/admin_claim_stores.go` (delete), `core/controlapi/admin_routes_test.go` (modify), `core/controlapi/app.go` (modify).

**Steps:**

1. Delete `core/controlapi/admin_claim_stores.go` entirely.
2. In `core/controlapi/app.go`, drop the `registerAdminClaimStoresRoutes` invocation.
3. In `core/controlapi/admin_routes_test.go`, delete the `TestAdminPickPolicyInsertRoute` function (and the associated harness helpers if they're only used there).

**Verification:**
```sh
go build ./core/controlapi/...
```
Should compile clean for the controlapi package after the deletion.

### T12: Strip the pick-policy validator hook

**Files:** `core/node/template_validator.go`, `core/controlapi/templates.go`, plus any tests that exercised the hook.

**Steps:**

1. In `core/node/template_validator.go`:
   - Remove the `IsPickPolicySelector` field from `RegistryHooks`.
   - Remove the validation branch that uses it (the one enforcing `intent: rw` on pick-policy selectors).
   - Keep `NamedLockDeclared` and `StoreKindOf` (or rename `StoreKindOf` to `StoreDeclared`-like since there's no per-kind dispatch any more — see T22 below).
2. In `core/controlapi/templates.go::validatorHooksFor`, drop the pick-policy hook construction.
3. Update or delete tests in `core/node/template_validator_test.go` and `core/controlapi/templates_test.go` (if any) that exercise the removed validation.

**Verification:**
```sh
go build ./core/node/... ./core/controlapi/...
go test ./core/node/... -count=1
```

### T13: Strip the scheduler visibility-timeout sweep; modify `reapRegionRow`

**Files:** `core/scheduler/sweep_locks.go`, `core/scheduler/scheduler.go`, plus any sweep tests.

**Steps:**

1. In `core/scheduler/sweep_locks.go`, delete the function (e.g. `reapItemsTable` or the equivalent) that walks substrate items tables for the visibility-timeout sweep. Per spec §3.3 + §11.1, this is store-internal — the postgres store will own its own sweep.
2. In the same file, modify `reapRegionRow` (the orphan reap on `rimsky_lock_holders`):
   - Keep the `DELETE FROM rimsky_lock_holders WHERE id = $1 AND ...` portion.
   - Remove the `Store.Abandon` call (per spec §7.5: orphan reaper does not RPC `Abandon`; substrate's TTL handles cleanup).
3. In `core/scheduler/scheduler.go`, drop the call site for the deleted visibility-timeout sweep. The orphan reaper call site stays.
4. Delete any tests in `core/scheduler/...` and `test/scenarios/...` that exercised the deleted visibility-timeout sweep.

**Verification:**
```sh
go build ./core/scheduler/...
go test ./core/scheduler/... -count=1
```

### T14: Strip substrate-only methods from the in-process `*pgstore.Store`

**Note:** `core/store/postgres/store.go` is about to be moved to `stores/postgres/` in T17. The substrate-only methods (`InsertItems`, `PickPolicyConfig`, `PickPolicies`) are removed as part of the move because they no longer have any rimsky-side consumer (admin endpoint and validator hook were stripped in T11–T12). Track this as a note in T17 — no separate "delete now" task needed.

**Verification:** none in this task; the methods are removed in T17.

---

## Move standard store impls to `stores/`

Each standard store-impl moves out of `core/store/<kind>/` and becomes a standalone binary under `stores/<kind>/`. Per spec §8 and §11.3.

### T15: Create `stores/` directory; `git mv core/store/filesystem → stores/filesystem`

**Files:** `core/store/filesystem/` → `stores/filesystem/` (preserve git history).

**Steps:**

1. `mkdir stores`.
2. `git mv core/store/filesystem stores/filesystem`.
3. Confirm files moved with history preserved: `git log --follow stores/filesystem/store.go`.
4. Update package declaration: in each `.go` file under `stores/filesystem/`, change `package filesystem` → `package store` (or keep `filesystem` if internal subpackage organization is preferred — see T19).

**Verification:**
```sh
ls stores/filesystem/
go build ./stores/filesystem/... 2>&1 || true   # likely fails until T19 reshapes it
```

### T16: `git mv core/store/postgres → stores/postgres`

**Files:** `core/store/postgres/` → `stores/postgres/`.

**Steps:**

1. `git mv core/store/postgres stores/postgres`.
2. In files under `stores/postgres/`, delete the substrate-only methods (`InsertItems`, `PickPolicyConfig`, `PickPolicies`) per T14 plan-note. Rationale: their only consumers were the admin endpoint (deleted in T11) and the validator hook (deleted in T12). The store-service's own admin endpoint (T20) replaces `InsertItems`'s external API.
3. Strip `TxFromContext` usage: postgres store `Open` no longer reaches into a rimsky-supplied tx. Substrate runs in its own pool's tx (per spec §7.3 and §7.8 obligation #1).
4. (Filesystem-glob removal is for the filesystem store, handled in T19 — postgres doesn't have globs to strip.)

**Verification:**
```sh
go build ./stores/postgres/... 2>&1 || true   # likely fails until T20
```

### T17: `git mv core/store/stub → stores/stub`

**Files:** `core/store/stub/` → `stores/stub/`.

**Steps:**

1. `git mv core/store/stub stores/stub`.
2. Strip `TxFromContext` usage if present.

**Verification:**
```sh
go build ./stores/stub/... 2>&1 || true
```

### T18: Confirm `core/store/` no longer contains `filesystem/`, `postgres/`, `stub/`

**Files:** `core/store/`.

**Steps:**

1. `ls core/store/` should show only: `interface.go`, `types.go`, `registry.go`, `lockholders.go`, `conflict.go`, `doc.go`, `remote/`, `storetest/` (plus tests).
2. No `filesystem/`, `postgres/`, `stub/`, or `tx.go`.
3. Update `core/store/doc.go`: the package now exposes the rimsky-side `Store` interface plus the `remote/` gRPC client and the `storetest/` test fake; substrate impls live under `stores/`.

**Verification:**
```sh
ls core/store/ | sort
go build ./core/store/...     # interface + types + registry + lockholders + conflict + remote + storetest
```

### T19: Reshape `stores/filesystem/` to the standard store-service layout

**Files:** `stores/filesystem/cmd/main.go`, `stores/filesystem/server/server.go`, `stores/filesystem/store/store.go`, `stores/filesystem/testfixture/testfixture.go`, `stores/filesystem/Dockerfile.filesystem`, `stores/filesystem/config-example.yml` (all new or restructured).

**Steps:**

1. Create the layout per spec §8.1 + §8.5:
   - `cmd/main.go` — `package main` binary entry point. Loads its YAML config from `STORE_FILESYSTEM_CONFIG`, opens listeners on configured ports (gRPC + HTTP), calls `server.Run(ctx, cfg, grpcListener, httpListener)`. Per Go convention, `cmd/main.go` is `package main` with only `main()`; the callable `Run` lives in `server/`.
   - `server/server.go` — `package server`. Defines `Run(ctx context.Context, cfg Config, grpcListener, httpListener net.Listener) error`. Implements `proto/v1/gen.StoreServiceServer`. HTTP+JSON bridge per spec §5.2 (decode JSON, marshal to proto type, call shared internal handler that the gRPC handler also calls). Both `cmd/main.go` and `testfixture/` invoke `server.Run`.
   - `store/store.go` — substrate-internal logic (file I/O, atomic-rename publish if any future staging). All 5 verbs implemented honestly. **No glob support** — concrete paths only per spec §8.1.
   - `testfixture/testfixture.go` — helper for tests: `Start(t *testing.T, root string) (endpoint string, teardown func())`. Spawns `server.Run` on a goroutine bound to an ephemeral port; returns the gRPC endpoint and a teardown closure.
2. The store advertises `write_semantics: direct` in its `Capabilities()` response.
3. `Open` for an `r` claim on `direct` returns the file path as `address` (and as `region` for byte-equal conflict). For `rw` similarly. Per spec §4.7 + §8.1.
4. `Commit` is a no-op (direct-mode). `Abandon` is degenerate. `Delete` removes the file. `Release` is a no-op.
5. `claim_id` is recorded internally before any state mutation in `Open` (per spec §7.8 obligation #2). For direct-mode filesystem there's no staging, so the obligation is satisfied trivially.
6. Add `Dockerfile.filesystem` modeled on `deploy/Dockerfile.go-base` (or `executors/http-node`'s Dockerfile). Multi-stage build; final image runs `/usr/local/bin/store-filesystem`.
7. Add `config-example.yml` showing the operator-facing config schema for this store-service (`root`, gRPC port, HTTP port). Substrate-defined; rimsky doesn't see it.

**Verification:**
```sh
go build ./stores/filesystem/...
go vet ./stores/filesystem/...
```

### T20: Reshape `stores/postgres/` to the standard store-service layout

**Files:** `stores/postgres/cmd/main.go`, `stores/postgres/server/server.go`, `stores/postgres/store/store.go`, `stores/postgres/store/sweep.go` (substrate-internal sweep), `stores/postgres/store/admin.go` (substrate-internal admin endpoint for items insertion), `stores/postgres/testfixture/testfixture.go`, `stores/postgres/Dockerfile.postgres`, `stores/postgres/config-example.yml`.

**Steps:**

1. Same overall shape as T19: `cmd/main.go` (`package main`) → `server.Run(ctx, cfg, grpcListener, httpListener, adminListener)` (in `server/server.go`, `package server`) → `store/` substrate-internal logic. `cmd/main.go` only contains `main()`; `server.Run` is the shared entry point invoked by both `main()` and `testfixture/`.
2. The store loads its own Postgres connection pool from its YAML config (`connection: postgres://...`).
3. Substrate-internal logic in `store/store.go`:
   - 5 verbs implemented; pick-policy resolution from the substrate's own `pick_policies` config (queue/ring/etc. — schema is substrate-defined per spec §6.3).
   - `Open` for a pick-policy claim: SELECT FOR UPDATE the next available item per pick-policy semantics, flip its state to `in_progress` with `claim_token` keyed by `claim_id`, return `address` / `region` / `payload` per spec §4.7.
   - `Open` for a regional claim (non-pick-policy selector): echo the selector as `address` and `region`.
   - `Commit` / `Abandon` / `Release` honor the `policy_override` field for pick-policy claims; resolve to substrate-internal actions (`release_to_back`, `release_to_head`, `delete`).
   - All terminal verbs idempotent in `claim_id` per spec §7.8 obligation #3.
4. `store/sweep.go` — substrate-internal goroutine implementing the §7.8 obligation #1 TTL/sweep. Walks the items table for rows with `claim_token` set and `claimed_at` older than the configured visibility timeout; flips them back to `available`. This replaces the rimsky-side scheduler sweep that T13 removed.
5. `store/admin.go` — substrate-internal admin endpoint for items insertion (per spec §13.1 operator-guide bullet). Listens on a separate port from the gRPC + HTTP store-protocol listener. Endpoint shape: `POST /admin/items/{policy}` accepting `{"items": [{"payload": ...}]}`. Documented in `config-example.yml` and operator-guide §3.4.X (T34).
6. The store advertises `write_semantics: direct` in its `Capabilities()` response.
7. `Dockerfile.postgres` similar to filesystem.
8. `config-example.yml` with operator-facing schema: `connection`, `pick_policies`, gRPC + HTTP + admin listen ports.

**Verification:**
```sh
go build ./stores/postgres/...
go vet ./stores/postgres/...
```

### T21: Reshape `stores/stub/` to the standard store-service layout

**Files:** `stores/stub/cmd/main.go`, `stores/stub/server/server.go`, `stores/stub/store/store.go`, `stores/stub/testfixture/testfixture.go`, `stores/stub/Dockerfile.stub`, `stores/stub/config-example.yml`.

**Steps:**

1. Same shape as T19 / T20: `cmd/main.go` (`package main`) → `server.Run` (in `server/`) → `store/` in-memory logic; `testfixture/` invokes `server.Run` on an ephemeral port.
2. Minimal in-memory state (per spec §8.3). Predictable behavior: `Open` returns deterministic addresses (e.g., the selector echoed); terminal verbs are no-ops or update in-memory state for test assertions.
3. `Capabilities()` returns whatever the config requests (configurable `write_semantics` so tests can exercise different modes).
4. Used by tests; no production deployment.

**Verification:**
```sh
go build ./stores/stub/...
go vet ./stores/stub/...
```

### T22: Confirm all store-services build

**Files:** none (verification-only).

**Verification:**
```sh
go build ./stores/...
go vet ./stores/...
go test ./stores/... -count=1     # may have minimal tests at this point
```

---

## Rewire rimsky processes

These tasks update the rimsky-side machinery to use the new `Store` interface and the remote-client.

### T23: Update `core/config/{supervisor,scheduler,controlapi}.go`

**Files:** `core/config/supervisor.go`, `core/config/scheduler.go`, `core/config/controlapi.go`.

**Steps:**

1. Drop the `StoreFactories []store.Factory` field from each config struct.
2. Replace the `Stores store.StoresConfig` field with a thin shape per spec §6.1:
   ```go
   type StoreConfig struct {
       Endpoint     string
       Capabilities store.Capabilities  // operator-declared requirements
   }
   type RemoteStoresConfig struct {
       Stores map[string]StoreConfig
   }
   ```
   Or directly inline as a `Stores map[string]StoreConfig` field on each config struct.
3. Replace the `buildStoreRegistry` helper with `dialRemoteStores(ctx, cfg.Stores) (*store.Registry, error)`:
   - For each entry: call `remote.Dial(ctx, name, endpoint)`, then `client.ValidateCapabilities(declared)`. On any failure (unreachable, mismatch), close already-dialed clients and return the error so the rimsky process exits per spec §6.2.
   - On success, populate a `*store.Registry` via `Registry.Add(name, client)`.
4. Update the existing `NamedLocksConfig` plumbing to stay as-is (per spec §6.1).
5. The shutdown path (`Handle.Shutdown`) calls `registry.Close()` as before — the `closer` interface walks each remote client and releases its gRPC connection.

**Verification:**
```sh
go build ./core/config/...
```

### T24: Update `core/cmd/rimsky-supervisor/main.go`

**Files:** `core/cmd/rimsky-supervisor/main.go`.

**Steps:**

1. Drop the `pgstore` alias import and the `Factory{}` instantiation.
2. Drop the `filesystem.Factory{}` import and instantiation.
3. Load the new `stores.yml` schema (per spec §6.1) into `config.SupervisorConfig.Stores`.
4. The cmd binary's `main()` is now thinner: it does not instantiate any Factory; the `dialRemoteStores` helper in `core/config/` does the dialing.

**Verification:**
```sh
go build ./core/cmd/rimsky-supervisor/...
```

### T25: Update `core/cmd/rimsky-scheduler/main.go`

**Files:** `core/cmd/rimsky-scheduler/main.go`.

**Steps:** mirror T24.

**Verification:**
```sh
go build ./core/cmd/rimsky-scheduler/...
```

### T26: Update `core/cmd/rimsky-control-api/main.go`

**Files:** `core/cmd/rimsky-control-api/main.go`.

**Steps:** mirror T24.

**Verification:**
```sh
go build ./core/cmd/rimsky-control-api/...
```

### T27: Rewrite `core/supervisor/runner_acquire.go` for the new acquisition flow

**Files:** `core/supervisor/runner_acquire.go`.

**Steps:**

1. Implement the 7-step flow per spec §7.3:
   1. Open a rimsky-side `pgx.Tx`.
   2. Claim the dispatch row (UPDATE rimsky_dispatch SET claimed_by = ...).
   3. Generate a UUID per claim (client-side); INSERT rimsky_lock_holders rows with the UUIDs (`id = claim_id`) and empty `address`.
   4. RPC `Store.Open(ctx, claim_id, ClaimSpec)` for each claim. **Outside** the rimsky-side tx in the wire-call sense (the tx is still open but the substrate is in a separate process — there's no `TxFromContext` plumbing). On any RPC failure: roll back the rimsky tx, return the error.
   5. UPDATE rimsky_lock_holders.address with each returned address. Pool-empty handling: if `ClaimResult` is all-empty (per spec §4.7's pool-empty signal), the claim attempt is skipped — supervisor rolls back the tx and returns "no work eligible" without dispatching.
   6. INSERT rimsky_claim_holders rows for held claims (per the inheritance algorithm; unchanged from v2).
   7. COMMIT the rimsky-side tx.
2. The conflict predicate uses byte-equal comparison on `region_data` per spec §7.7. This is the per-INSERT pre-check; the queue eligibility predicate (T29) does the same thing for candidate selection.
3. Drop all `store.WithTx` / `store.TxFromContext` calls — those plumbed the supervisor's tx into the substrate. No longer applicable.
4. Multi-lock sort order per spec §7.7's note: sort by `(lock_kind, lock_name | (store_name, region_data_canonical))` where canonical = raw bytes.

**Verification:**
```sh
go build ./core/supervisor/...
```
Will fail until T28 + T29 are also done. Confirm `runner_acquire.go` itself compiles given the updated `core/store` package.

### T28: Update `core/supervisor/runner_terminal.go`, `runner_terminal_outcome.go`, `auto_terminal.go`

**Files:** `core/supervisor/runner_terminal.go`, `core/supervisor/runner_terminal_outcome.go`, `core/supervisor/auto_terminal.go`.

**Steps:**

1. All terminal-verb calls (`Commit`, `Abandon`, `Delete`, `Release`) now go through the remote client. Each call passes the `claim_id` (read from the lock-holder row) on every verb.
2. Substrate-side action runs in its own tx (substrate-internal). The lock-holder row DELETE in `auto_terminal.go::CheckAndFireResolution` is in rimsky's own tx, no longer linked to the substrate's action by tx-sharing.
3. Per spec §7.8 obligation #3, terminal verbs are at-least-once-delivery — substrate handles idempotency. Rimsky's retry policy on transport errors stays as today.
4. Read `address` and `region_data` from the lock-holder row (NOT from in-memory `lk.ClaimResult`) at terminal time, per the cycle-1 fix in v2. Confirm this is preserved.

**Verification:**
```sh
go build ./core/supervisor/...
```

### T29: Update `core/queue/postgres/queue.go`

**Files:** `core/queue/postgres/queue.go`.

**Steps:**

1. The eligibility predicate (used by `SelectCandidates` / `ClaimDispatchRow`) joins `rimsky_dispatch` against `rimsky_lock_holders` to find candidates that are not blocked by existing holders.
2. The conflict portion of the predicate uses byte-equal comparison on `region_data` per spec §7.7. Today's predicate may delegate to the substrate's `RegionsConflict` (now removed); rewrite to use SQL byte-equal (`region_data = $N`) directly.
3. Mode coexistence (sync vs async, r vs w) is unchanged — same C3.1 matrix as v2; the table-mode is now derived from the operator's declared `write_semantics` for the store (read from the registry, not the substrate).
4. No other shape change to the predicate.

**Verification:**
```sh
go build ./core/queue/...
go test ./core/queue/postgres/... -count=1
```

### T30: Compile-clean checkpoint

**Files:** none (verification-only).

**Steps:**

1. `go build ./...` — full tree should now compile clean.
2. `go vet ./...` — no warnings.

**Verification:**
```sh
go build ./...
go vet ./...
```

If anything still fails to compile, identify the file and fix it before proceeding. Common candidates: any test file that uses the old `Store` interface, any importer of the deleted `core/store/{filesystem,postgres,stub}` packages.

---

## Test fixtures

### T31: Update unit tests in `core/...` to use `core/store/storetest.Fake`

**Files:** various `*_test.go` files under `core/...` that previously used `core/store/stub.NewStore` directly.

**Steps:**

1. `grep -rln 'core/store/stub' core/` — finds the unit-test files that depended on the in-process stub.
2. For each: replace the stub import with `core/store/storetest`, replace `stub.NewStore(...)` with `storetest.NewFake(...)`. Adjust assertions if needed (the recorder API may differ).
3. Tests where the store interface isn't relevant (pure rimsky-side logic) can use a hand-rolled local fake; that's fine.

**Verification:**
```sh
go test ./core/... -count=1
```

### T32: Add testfixture-driven scenario tests using loopback gRPC

**Files:** `test/scenarios/locks/`, `test/scenarios/stores/`, `test/scenarios/attributes/`, `test/scenarios/claim_stores/`, `test/scenarios/frame_resolution/`. Existing scenario tests are mostly placeholders from the v2 cleanup; this task lands the full suite per spec §9.4.

**Steps:**

1. For each scenario test (existing or rewritten), the test setup pattern is:
   - Start testcontainers Postgres for rimsky's control plane (existing helper: `core/internal/pgtest.Start`).
   - Start one or more loopback gRPC store-services using `stores/<kind>/testfixture.Start(t, ...)`. The helper returns the gRPC endpoint.
   - Build `core/config.SupervisorConfig.Stores` programmatically with the loopback endpoints and declared capabilities.
   - Start in-process `rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`.
   - Run the scenario.
2. Land the priority scenario tests per spec §9.4 + the `docs/2026-04-27-store-protocol-inertness-cleanup.md` follow-up list:
   - `test/scenarios/locks/atomic_acquisition_test.go` — exercises the 7-step acquisition flow under contention.
   - `test/scenarios/locks/named_lock_counting_test.go` and `named_lock_mutex_test.go` — named-lock primitive coverage.
   - `test/scenarios/locks/sorted_acquisition_no_deadlock_test.go` — multi-lock sort-order invariant.
   - `test/scenarios/locks/claimant_guarded_release_test.go` — invariant 4 / 6 sanity.
   - `test/scenarios/locks/orphan_reap_test.go` — orphan-reap deletes lock-holder without `Abandon` per spec §7.5.
   - `test/scenarios/stores/regional_claim_test.go` — 5-verb path end-to-end on regional access.
   - `test/scenarios/stores/single_writer_per_region_test.go` — invariant 4b.
   - `test/scenarios/attributes/substitution_dispatch_test.go` — invariant 12.
   - `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go` — invariant 13.
   - `test/scenarios/claim_stores/auto_terminal_failure_propagation_test.go`.
   - `test/scenarios/claim_stores/inheritance_validation_test.go`.
   - `test/scenarios/claim_stores/address_inheritance_lifetime_test.go`.
   - `test/scenarios/claim_stores/value_pass_lifetime_test.go`.
   - `test/scenarios/stores/frame_id_observability_only_test.go` — invariant 4b / observability claim.
   - `test/scenarios/stores/inertness_audit_test.go` — invariant 20 behavioral coverage.
   - `test/scenarios/stores/staged_async_protocol_present_no_substrate_test.go` — protocol-honest failure.
   - `test/scenarios/stores/verify_open_inside_acquisition_tx_test.go` — invariant 15 (revised).
3. Drop any test files under `test/scenarios/{locks,stores,attributes,claim_stores}/` that are placeholder stubs once they're replaced by substantive tests.

**Verification:**
```sh
go test ./test/scenarios/... -count=1
```

### T33: Rewrite `test/smoke/setup.go` and `test/smoke/stores_redesign_smoke_test.go` for OOP

**Files:** `test/smoke/setup.go`, `test/smoke/stores_redesign_smoke_test.go`.

**Steps:**

1. `setup.go::BringUpStack`:
   - Spin up testcontainers Postgres for rimsky's control plane.
   - Start loopback gRPC servers for `store-filesystem`, `store-postgres`, `store-stub` via the `testfixture.Start` helpers (each calls `server.Run` on a goroutine bound to an ephemeral port).
   - Build `stores.yml` programmatically with the loopback endpoints and declared capabilities.
   - Start in-process `rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`.
   - Return a `SmokeStack` that the test drives.
2. `stores_redesign_smoke_test.go`:
   - Verify the 100-fire pipeline still drives end-to-end through the OOP stores.
   - The 4-node template (claim-topic / scope / draft / review) needs auditing for filesystem-glob usage per spec §10. If the existing template uses globs against filesystem, rewrite to concrete paths. If it doesn't, no change.
   - Item seeding: switch from the (deleted) `POST /admin/stores/...` endpoint to direct SQL insertion via the postgres-store-service's substrate-internal admin endpoint (T20's `store/admin.go`) — call it from the test via HTTP.

**Verification:**
```sh
go test ./test/smoke/... -count=1
```
Smoke runs ~60s; full pipeline must complete with no failures.

---

## Deployment

### T34: New per-store Dockerfiles

**Files:** `stores/filesystem/Dockerfile.filesystem`, `stores/postgres/Dockerfile.postgres`, `stores/stub/Dockerfile.stub`.

**Steps:** confirm each Dockerfile from T19 / T20 / T21 is in place. Multi-stage build; final image runs the binary at `/usr/local/bin/store-<kind>`.

**Verification:**
```sh
docker build -f stores/filesystem/Dockerfile.filesystem -t rimsky/store-filesystem:test .
docker build -f stores/postgres/Dockerfile.postgres -t rimsky/store-postgres:test .
docker build -f stores/stub/Dockerfile.stub -t rimsky/store-stub:test .
docker rmi rimsky/store-filesystem:test rimsky/store-postgres:test rimsky/store-stub:test
```

### T35: Update `deploy/build-images.sh`

**Files:** `deploy/build-images.sh`.

**Steps:**

1. Add three new image-build invocations after the existing rimsky / executor builds:
   ```sh
   docker build -f stores/filesystem/Dockerfile.filesystem -t rimsky/store-filesystem:$VERSION -t rimsky/store-filesystem:latest .
   docker build -f stores/postgres/Dockerfile.postgres -t rimsky/store-postgres:$VERSION -t rimsky/store-postgres:latest .
   docker build -f stores/stub/Dockerfile.stub -t rimsky/store-stub:$VERSION -t rimsky/store-stub:latest .
   ```
2. Update the script's final echo to count the new images.

**Verification:**
```sh
bash deploy/build-images.sh
docker images | grep rimsky/store-
```

### T36: Rewrite `deploy/docker-compose.yml`

**Files:** `deploy/docker-compose.yml`.

**Steps:**

1. Add three new compose services for `store-filesystem`, `store-postgres`, `store-stub`:
   - Each with its own image, env vars, volumes (filesystem store mounts the content volume), depends_on (`postgres` for the postgres store).
   - Each exposes its gRPC port (e.g. `9100`, `9101`, `9102`) and HTTP port (e.g. `9110`, `9111`, `9112`) on the compose network only.
   - The postgres store-service additionally exposes its substrate-internal admin port (e.g. `9121`) for items seeding.
2. Update the existing rimsky processes' env vars:
   - `RIMSKY_STORES_CONFIG` points at the new `stores.yml` schema (T37).
   - Drop any env vars that referenced the removed in-process Factory wiring.
3. Update `depends_on` for the rimsky processes so they wait for the store-services to be reachable before starting.
4. Remove the `init-items` one-shot service if it's currently doing items-table creation — the postgres store-service is now responsible for its own items table (substrate-internal; created at first connect or via the substrate's own one-shot per T20).

**Verification:**
```sh
docker compose -f deploy/docker-compose.yml config            # validates the YAML
```

### T37: Rewrite `deploy/stores.yml`

**Files:** `deploy/stores.yml`.

**Steps:**

1. Rewrite to the new shape per spec §6.1:
   ```yaml
   stores:
     content:
       endpoint: "grpc://store-filesystem:9100"
       capabilities:
         write_semantics: direct
     topics:
       endpoint: "grpc://store-postgres:9101"
       capabilities:
         write_semantics: direct

   named_locks:
     model-calls:        { limit: 5 }
     pipeline-singleton: { limit: 1 }
   ```
2. The schema explicitly does NOT contain: `kind`, `connection`, `pick_policies`, or any other substrate-specific keys (per spec §6.1).

**Verification:**
```sh
yq . deploy/stores.yml > /dev/null     # syntax validation
```

### T38: Per-store config-example YAMLs

**Files:** `stores/filesystem/config-example.yml`, `stores/postgres/config-example.yml`, `stores/stub/config-example.yml`.

**Steps:**

1. Each store-service ships a `config-example.yml` documenting its substrate-defined config schema (per spec §6.3 + §8).
2. `stores/filesystem/config-example.yml`: `root`, gRPC port, HTTP port.
3. `stores/postgres/config-example.yml`: `connection`, `pick_policies` block, gRPC + HTTP + admin ports.
4. `stores/stub/config-example.yml`: `write_semantics`, gRPC + HTTP ports.

**Verification:**
```sh
yq . stores/filesystem/config-example.yml > /dev/null
yq . stores/postgres/config-example.yml > /dev/null
yq . stores/stub/config-example.yml > /dev/null
```

---

## Documentation

### T39: Rewrite `docs/protocol.md`

**Files:** `docs/protocol.md`.

**Steps:**

1. Replace v2's in-process `Store` interface description with the v3 wire-protocol shape per spec §4 + §5: 5+1 verbs, request/response messages, gRPC + HTTP+JSON encoding, error mapping per spec §5.3.
2. Authoritative source remains `proto/v1/store_service.proto` — link to it.
3. Confirm the executor protocol section (`proto/v1/node_executor.proto`) is unchanged.

### T40: Update `docs/architecture.md`

**Files:** `docs/architecture.md`.

**Steps:**

1. Update the process-topology section: stores are now separate processes; `core/store/` no longer contains substrate impls; `core/store/remote/` is the only concrete impl in rimsky; the `stores/` directory is the new home for store implementations.
2. Update the package import rules section: `core/store/` no longer has `filesystem/`, `postgres/`, `stub/` subpackages; the new `stores/` top-level directory is sibling to `executors/` per spec §11.3.
3. Update the blessed-invariants list to reference the v3 changes per spec §4.10 (invariant 10 clarified, 14 retired, 15 revised).

### T41: Rewrite `docs/store-author-guide.md`

**Files:** `docs/store-author-guide.md`.

**Steps:**

1. Authoring a store now means: implement the 5 + 1 RPC handlers (per spec §4.1), define your own config schema, ship a binary + Dockerfile + config example.
2. Document the five store-author obligations from spec §7.8:
   - Sweep / TTL for orphan reclamation.
   - `claim_id` keyed orphan identification (record `claim_id` before any state mutation in `Open`).
   - Idempotent terminal verbs in `claim_id`.
   - No dependence on rimsky `Abandon` for orphan cleanup.
   - Canonical `region` bytes for byte-equal conflict correctness (per spec §7.7).
3. Document the auth-blind advisory per spec §13.3 (slimmed).
4. Document the no-store-side-serialization rule (invariant 9b restatement per spec scope row I10).
5. Preserve the §17.5-style distinction between **claim content** (under invariant 20) and **store-config bytes** (operator-managed; not under invariant 20).

### T42: Update `docs/operator-guide.md`

**Files:** `docs/operator-guide.md`.

**Steps:**

1. §3.4 (stores config) — rewrite for the new `stores.yml` shape per spec §6.1 (name → endpoint + declared capabilities).
2. §5.5 (admin endpoints) — drop the items endpoint section.
3. New §3.4.X "**Runtime item seeding for pick-policy stores**" (per spec §13.1):
   - Each store-service that supports pick policies owns its own admin surface (out of scope for the rimsky 5+1 protocol).
   - The reference postgres store-service ships with a documented admin endpoint for items insertion (separate listener port).
   - Operators configure their item-seeding tooling to talk to the store-service directly, never through rimsky.
4. Section 3.X (auth-blind advisory) — slimmed per spec §13.3.

### T43: Update `docs/executor-author-guide.md`

**Files:** `docs/executor-author-guide.md`.

**Steps:**

1. Minor update: the executor↔storage data path is unchanged.
2. Add a one-liner clarifying that addresses now come from remote store-services rather than in-process code (no behavioral change for the executor).
3. Slim the auth-blind section per spec §13.3.

### T44: Update `docs/node-graph-design.md`

**Files:** `docs/node-graph-design.md`.

**Steps:**

1. Minor: the DSL is unchanged in v3.
2. Update the deployment description in §4 (the "operator runs rimsky processes" picture) to add store-services as siblings.

### T45: Update `docs/glossary.md`

**Files:** `docs/glossary.md`.

**Steps:**

1. Confirm "store" / "store implementation" / "store-service" are used consistently per spec scope J4.
2. Add or confirm an entry for `claim_id` per spec §4.2.
3. Add or confirm "store-service" — the standalone binary that implements the store protocol.

### T46: Update `CHANGELOG.md`

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append an Unreleased bullet referencing this spec, summarizing: 5+1 verb wire protocol; standard stores out-of-process; invariant 10 clarified, 14 retired, 15 revised; Factory / Registry.BuildAll / StoresConfig removed; admin items endpoint replaced by per-store-service admin path.

### T47: Update `CLAUDE.md`

**Files:** `CLAUDE.md`.

**Steps:**

1. Update the gotchas list per spec §13.5:
   - Invariant 10 redesign: store owns its tx; rimsky's bookkeeping atomicity decoupled from store state mutation. The `WithTx` / `TxFromContext` mechanism is gone.
   - New deployment topology: stores are separate processes; rimsky processes dial them at startup.
   - New `RIMSKY_STORES_CONFIG` schema: thin "name → endpoint + declared capabilities" form. Fail-fast `Capabilities()` handshake.
   - The 4 inertness violations are gone — structurally impossible.
   - The `Factory` / `Registry.BuildAll` / `StoresConfig` machinery is gone.
   - Held-claim resolution mechanically updated: substrate verb calls now go through the remote-client gRPC path; substrate-side action runs in its own tx.
   - Region conflict is byte-equal; substrates canonicalize per spec §7.7.
   - `rimsky_lock_holders.id` is generated client-side.
2. Update the blessed-invariants list:
   - Invariant 10 — replace wording per spec §4.10 ("Lock acquisition is atomic with dispatch claim (rimsky-side). ...").
   - Invariant 14 — DELETE entirely.
   - Invariant 15 — replace wording per spec §4.10 ("Open fires inside the rimsky-side acquisition transaction. ...").
3. Update the "Where to look first" section: the v3 spec is the new contract; v2 spec is now historical.

### T48: Update `@blessed-invariant` annotations in source code

**Files:** any `.go` file containing `@blessed-invariant 10`, `@blessed-invariant 14`, or `@blessed-invariant 15`.

**Steps:**

1. `grep -rln '@blessed-invariant 10' core/` — locate all annotations.
2. Update each invariant 10 annotation to reflect the spec §4.10 revised wording (or replace with a cross-reference to CLAUDE.md / spec).
3. `grep -rln '@blessed-invariant 14' core/` — DELETE all such annotations.
4. `grep -rln '@blessed-invariant 15' core/` — update to the spec §4.10 revised wording.

**Verification:**
```sh
grep -rln '@blessed-invariant 14' core/ stores/      # should return nothing
go build ./...                                      # confirm comment changes don't break build
```

### T49: Update `docs/2026-04-27-store-protocol-inertness-cleanup.md`

**Files:** `docs/2026-04-27-store-protocol-inertness-cleanup.md`.

**Steps:**

1. Append a status-update note that the cleanup was folded into v3 per the v3 spec, and the design notes here are superseded by the v3 spec.

---

## Final verification

### T50: Full proto regen

**Files:** generated `proto/v1/gen/store_service*.go`.

**Verification:**
```sh
make proto-gen
go build ./proto/v1/gen/...
```

### T51: Full build

**Verification:**
```sh
go build ./...
```

### T52: Full test suite

**Verification:**
```sh
go test ./... -count=1
```
All packages green. Smoke must pass end-to-end (~60s).

### T53: Race detector

**Verification:**
```sh
go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3
```

### T54: Lint

**Verification:**
```sh
make lint
```
No new lint failures.

### T55: TS executor

**Verification:**
```sh
cd executors/claude-agent
npm install
npm test
npm run build
```
No changes expected for v3 (executor protocol untouched), but verify nothing broke incidentally.

### T56: Docker stack health

**Verification:**
```sh
deploy/build-images.sh
docker compose -f deploy/docker-compose.yml up -d
sleep 10
curl -fsS http://localhost:8080/health
docker compose -f deploy/docker-compose.yml down -v
```

### T57: Conformance probe

**Verification:**
```sh
docker compose -f deploy/docker-compose.yml up -d
sleep 10
go run ./core/cmd/rimsky-conformance --endpoint http://localhost:9091 --transport grpc --require-stub-mode
go run ./core/cmd/rimsky-conformance --endpoint http://localhost:9090 --transport grpc --require-stub-mode
docker compose -f deploy/docker-compose.yml down -v
```

If `rimsky-conformance` doesn't exercise store-services in v3, this verifies the executors only. If a `rimsky-store-conformance` is added (out of scope for v3 itself but a natural follow-up), invoke it here too.

### T58: Vocabulary leakage grep

**Verification:**
```sh
grep -rn 'StoreFactories\|StoresConfig\|Factory.MaxWriteSemantics\|InsertItems\|PickPolicyConfig\|PickPolicies\|RegionsConflict\|UnmarshalRegion\|TxFromContext\|WithTx' core/ stores/ proto/ test/
```
Should return zero hits in active code (historical-decision docs may still mention these — that's the point of capturing them in the v3 spec rather than retroactively editing v2).

### T59: Spec cross-check

**Verification:** runnable existence checks for every removal and addition in spec §11.1 + §11.3. The script must exit 0; any failed check exits non-zero with a clear message naming the missing/present file.

```sh
set -e

# Removals (these must NOT exist):
! test -f core/store/tx.go || { echo "FAIL: core/store/tx.go still exists"; exit 1; }
! test -d core/store/filesystem || { echo "FAIL: core/store/filesystem still exists"; exit 1; }
! test -d core/store/postgres || { echo "FAIL: core/store/postgres still exists"; exit 1; }
! test -d core/store/stub || { echo "FAIL: core/store/stub still exists"; exit 1; }
! test -f core/controlapi/admin_claim_stores.go || { echo "FAIL: admin_claim_stores.go still exists"; exit 1; }

# Removals (these symbols must NOT appear in active code):
! grep -rn 'type Factory interface' core/store/ || { echo "FAIL: Factory interface still present"; exit 1; }
! grep -rn 'type StoresConfig' core/store/ || { echo "FAIL: StoresConfig still present"; exit 1; }
! grep -rn 'BuildAll\|MaxWriteSemantics\|InsertItems\|PickPolicyConfig\|PickPolicies\|RegionsConflict\|UnmarshalRegion\|TxFromContext\|WithTx' core/ stores/ || { echo "FAIL: removed symbol still referenced"; exit 1; }

# Additions (these must exist):
test -f proto/v1/store_service.proto || { echo "FAIL: missing store_service.proto"; exit 1; }
test -f proto/v1/gen/store_service.pb.go || { echo "FAIL: missing generated bindings"; exit 1; }
test -d core/store/remote || { echo "FAIL: missing core/store/remote"; exit 1; }
test -d core/store/storetest || { echo "FAIL: missing core/store/storetest"; exit 1; }
test -d stores/filesystem/cmd && test -d stores/filesystem/server && test -d stores/filesystem/store && test -d stores/filesystem/testfixture || { echo "FAIL: stores/filesystem/ layout incomplete"; exit 1; }
test -d stores/postgres/cmd && test -d stores/postgres/server && test -d stores/postgres/store && test -d stores/postgres/testfixture || { echo "FAIL: stores/postgres/ layout incomplete"; exit 1; }
test -d stores/stub/cmd && test -d stores/stub/server && test -d stores/stub/store && test -d stores/stub/testfixture || { echo "FAIL: stores/stub/ layout incomplete"; exit 1; }
test -f stores/filesystem/Dockerfile.filesystem || { echo "FAIL: missing filesystem Dockerfile"; exit 1; }
test -f stores/postgres/Dockerfile.postgres || { echo "FAIL: missing postgres Dockerfile"; exit 1; }
test -f stores/stub/Dockerfile.stub || { echo "FAIL: missing stub Dockerfile"; exit 1; }
test -f stores/filesystem/config-example.yml && test -f stores/postgres/config-example.yml && test -f stores/stub/config-example.yml || { echo "FAIL: missing config-example.yml"; exit 1; }

# Blessed-invariant 14 annotations must be gone:
! grep -rn '@blessed-invariant 14' core/ stores/ proto/ || { echo "FAIL: invariant 14 annotation still present"; exit 1; }

echo "OK: spec cross-check passes"
```

---

## Manual checks after completion

These items require human judgment or environment access the implementer doesn't have. Run after the automated implementation and review pipeline complete clean.

1. **Visual review of CLAUDE.md changes.** Confirm the invariant-10/14/15 wording reads naturally alongside the other invariants and gotchas, not as bolt-on edits.
2. **Operator-guide examples readability.** Skim `docs/operator-guide.md` §3.4 + §3.4.X (items seeding) — confirm an operator who has never seen rimsky can follow the new shape.
3. **Docker smoke + conformance under realistic load.** The automated docker-smoke (T56) brings the stack up briefly. For a real shake-out, leave it running for a few minutes, push a few templates through it, watch logs.
4. **Store-author guide.** Confirm a hypothetical store-author could implement a new substrate by reading just `docs/store-author-guide.md` + `proto/v1/store_service.proto` + `stores/stub/` as a reference. Self-review whether the obligation list (spec §7.8) is reproduced clearly.
