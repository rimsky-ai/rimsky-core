# Host-agent and proxy implementation plan

**Spec:** .ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md
**Goal:** Let users run arbitrary local binaries as rimsky services on a per-invocation basis (`rimsky run --service codegen=./binary`) via a new `rimsky-host-agent-proxy` stack service + `rimsky-host-agent` daemon bundled into the CLI.
**Architecture:** Proxy is a normal multi-protocol rimsky service (declared in `rimsky.yml` under `executors:` and `claim_producers:` blocks with `protocols: [..., lifecycle_subscriber]` on its executor entry). It implements the existing wire protocols on the supervisor-facing side and a new `proto:host_agent.proto::HostAgent.Connect` bidi stream on the agent-facing side. The agent dials the proxy outbound, receives Spawn/Reap frames, exec()s local binaries, and forwards their gRPC streams + local HTTP traffic back through the bidi stream. No tunnel-awareness leaks into the supervisor; graph processing, dispatch resolution, error vocabulary, and callbacks are bit-identical to today.
**Tech Stack:** Go 1.x, gRPC + protobuf (`protocols/proto/v1/`), Postgres + SQLite via `foundation/persistence/`, `go-chi/chi` for HTTP, `log/slog` for logging. Build via `make build-all`, `make test-all`, `make lint`, `make proto-gen`.

---

## Implementer onboarding

This plan modifies code across the entire rimsky tree. Key conventions to follow (per `.claude/rules/rules.md` and the cold-read cheatsheet):

- **Logging:** `log/slog` only. No Zap, no Zerolog.
- **Postgres:** `jackc/pgx/v5`. SQLite: `modernc.org/sqlite` (pure-Go, no CGO).
- **HTTP routing:** `go-chi/chi`.
- **Layer boundaries** (enforced by `.golangci.yml` depguard): `foundation/` < `graph/` < `runtime/` < `control/`. The `runtime-purity` rule forbids `runtime/` from importing `control/`. Any new cross-layer wiring uses function pointers (see the `LifecyclePeersForSpec` pattern in Pass 7).
- **Multi-module workspace:** `go.work` ties four modules: root, `foundation/`, `protocols/`, `sdk/go/`. Run tests/builds at the module level (`cd foundation && go test ./...`) where indicated.
- **`concept:lifecycle-subscriber` has two parallel surfaces.** The proto service (`proto:lifecycle.proto::LifecycleSubscriber`) AND a Go interface (`code:protocols/lifecycle/lifecycle.go::LifecycleSubscriber`) with re-export aliases in `code:foundation/locks/lifecycle.go`. The peer-client adapter at `code:runtime/peer/lifecycle_client.go` converts Go struct → proto on outbound calls. **Every proto change to lifecycle.proto requires a parallel Go-interface change**, or downstream consumers won't compile.
- **No skipping hooks:** never use `--no-verify`, `--no-gpg-sign`. If a check fails, fix the underlying issue.
- **No commits:** this plan produces working-tree edits only. The user commits when they're ready.
- **Verification convention:** every pass ends with a runnable `go build` / `go test` / `make` command whose exit code is the pass gate.

When a task says "extend `X` with new field `Y`," the implementer must:
1. Read the current shape of `X` first.
2. Add `Y` in the position matching the file's existing conventions (struct field ordering, YAML key ordering, etc.).
3. Update every site that constructs `X` (whether or not the task enumerates each — find them with `grep` or `rg`) so the codebase still compiles.

When in doubt, prefer matching the prevailing pattern in the immediate neighborhood over inventing a new one.

---

## Pass 1: Protocol surfaces (proto + Go interface)

**Goal:** Land the new `host_agent.proto` file and the lifecycle.proto extensions (new `service_bindings` + `owner_api_key_id` fields on `OnInstanceCreatedRequest`; new `OnRunScopeTerminal` RPC + request message). Regenerate Go bindings. Extend the parallel Go interface in `protocols/lifecycle/` and the alias re-exports in `foundation/locks/lifecycle.go`. Update the `runtime/peer/lifecycle_client.go` adapter to translate the new fields/method. Update all existing test fakes that implement `LifecycleSubscriber`.
**Scope:** Tasks 1–7
**End state:** working
**Verification:** `make proto-gen && cd protocols && go build ./... && cd .. && cd foundation && go build ./... && cd .. && go build ./...`

### Task 1: Create `protocols/proto/v1/host_agent.proto`

**Files:** `protocols/proto/v1/host_agent.proto` (new)

**Steps:**

1. Create the file with this content (verbatim — structure follows the existing convention in sibling proto files):

```protobuf
// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.

syntax = "proto3";

package rimsky.v1;

option go_package = "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen;genv1";

// HostAgent is the protocol that rimsky-host-agent-proxy uses to communicate
// with connected rimsky-host-agent daemons running on user dev machines.
// Single bidi long-lived stream per connected agent. Frame oneof shapes
// enumerated below.
//
// This protocol is INTERNAL to the proxy binary; it is not part of the
// public service-protocol surface. Operators do not implement against it.
service HostAgent {
  rpc Connect(stream ClientFrame) returns (stream ServerFrame);
}

message ClientFrame {
  oneof body {
    Register         register          = 1;
    Heartbeat        heartbeat         = 2;
    SpawnAck         spawn_ack         = 3;
    Reaped           reaped            = 4;
    DispatchFrame    dispatch_frame    = 5;  // from spawned process → supervisor
    LocalHttpForward http_forward      = 6;  // from spawned process → rimsky-side
  }
}

message ServerFrame {
  oneof body {
    RegisterAck       register_ack       = 1;
    HeartbeatAck      heartbeat_ack      = 2;
    Spawn             spawn              = 3;
    Reap              reap               = 4;
    DispatchFrame     dispatch_frame     = 5;  // from supervisor → spawned process
    LocalHttpResponse http_response      = 6;  // from rimsky-side → spawned process
  }
}

message Register {
  string api_key   = 1;
  string agent_label = 2;       // e.g., "hostname-pid"; for multi-agent disambiguation
  string agent_version = 3;
  // Base URL the agent's local HTTP listener serves for spawned processes.
  // The proxy uses this to rewrite callback_url and other rimsky-side URLs
  // before tunneling them into the spawned process.
  string local_callback_base_url = 4;
}

message RegisterAck {
  string proxy_version = 1;
  // If a prior agent for this api_key was connected, this ack carries
  // a notice that the prior connection has been displaced.
  bool displaced_prior = 2;
}

message Heartbeat       { int64 sent_at_unix_ms = 1; }
message HeartbeatAck    { int64 received_at_unix_ms = 1; }

message Spawn {
  string spawn_id           = 1;
  Binding binding           = 2;
  string cwd                = 3;
  string run_scope_id       = 4;
  repeated string expected_protocols = 5;  // e.g., ["executor"], ["claim_producer"], or both
  int32  ready_timeout_seconds = 6;
}

message Binding {
  string path               = 1;
  // future-extensible: args, env, per-binding cwd, etc.
}

message SpawnAck {
  string spawn_id           = 1;
  enum SpawnStatus {
    SPAWN_STATUS_UNSPECIFIED = 0;
    SPAWN_STATUS_READY       = 1;
    SPAWN_STATUS_FAILED      = 2;
  }
  SpawnStatus status        = 2;
  // On READY: per-protocol Capabilities responses, keyed by protocol name.
  // Bytes are the serialized CapabilitiesResponse for each protocol.
  map<string, bytes> capabilities = 3;
  Error  error              = 4;        // populated when status = SPAWN_STATUS_FAILED
}

message Reap            { string spawn_id = 1; int32 sigterm_grace_seconds = 2; }
message Reaped          { string spawn_id = 1; bool clean = 2; Error error = 3; }

message DispatchFrame {
  string spawn_id           = 1;
  string protocol           = 2;  // "executor", "claim_producer", etc. — used at dispatch start
  bytes  payload            = 3;  // serialized gRPC frame for the named protocol
  // Stream multiplexing: a single spawn_id can host concurrent dispatch
  // streams (e.g., concurrent ClaimProducer.Open calls). Each stream
  // carries a stream_id.
  string stream_id          = 4;
  enum DispatchFrameKind {
    DISPATCH_FRAME_KIND_UNSPECIFIED = 0;
    DISPATCH_FRAME_KIND_DATA        = 1;
    DISPATCH_FRAME_KIND_HALF_CLOSE  = 2;
    DISPATCH_FRAME_KIND_CANCEL      = 3;
  }
  DispatchFrameKind kind    = 5;
}

message LocalHttpForward {
  string forward_id         = 1;
  string method             = 2;
  string url                = 3;       // full URL as the spawned process saw it
  bytes  body               = 4;
  map<string, string> headers = 5;
  string spawn_id           = 6;       // for routing back through the proxy if needed
}

message LocalHttpResponse {
  string forward_id         = 1;
  int32  status             = 2;
  bytes  body               = 3;
  map<string, string> headers = 4;
}

message Error {
  string class              = 1;       // matches rimsky's error-class vocabulary
  string message            = 2;
}
```

2. Verify file exists.

**Verification:** `test -f protocols/proto/v1/host_agent.proto`.

### Task 2: Extend `protocols/proto/v1/lifecycle.proto`

**Files:** `protocols/proto/v1/lifecycle.proto`

**Steps:**

1. Read the current file. Today has 6 RPCs (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`, `OnInstanceCreated`, `OnInstanceTerminated`) and corresponding request messages.

2. In `service LifecycleSubscriber { ... }`, add a 7th RPC after `OnInstanceTerminated`:

```protobuf
  // OnRunScopeTerminal fires when a run-scope reaches terminal state.
  // Fired from the rimsky-side process that owns the state transition:
  // control-api for main scopes (polling-driven via instance_terminator.tick);
  // the supervisor for sub-graph and fanout-partition scopes (synchronous,
  // in-tx). DB-tracked idempotency via rimsky_lifecycle_idempotencies
  // (scope_kind="run_scope", state="run_scope_terminal") is preserved across
  // both firing sites.
  rpc OnRunScopeTerminal(OnRunScopeTerminalRequest) returns (LifecycleAck);
```

3. Extend `OnInstanceCreatedRequest`. Find the message and add two fields after `params`:

```protobuf
  // service_bindings carries the per-instance late-bound service catalog
  // (opaque JSONB bytes). Empty when the instance has no late-bound services.
  // Consumed by the host-agent-proxy to populate its binding cache.
  bytes service_bindings = 5;

  // owner_api_key_id is the api-key whose authenticated request created the
  // instance (empty string for anonymous-mode-created instances). Consumed
  // by the host-agent-proxy to route dispatches to the right user's agent.
  string owner_api_key_id = 6;
```

4. Add a new `OnRunScopeTerminalRequest` message at the bottom of the file:

```protobuf
message OnRunScopeTerminalRequest {
  string run_scope_id    = 1;
  string terminal_reason = 2;
}
```

**Verification:** `grep -q 'rpc OnRunScopeTerminal' protocols/proto/v1/lifecycle.proto && grep -q 'service_bindings = 5' protocols/proto/v1/lifecycle.proto && grep -q 'OnRunScopeTerminalRequest' protocols/proto/v1/lifecycle.proto`.

### Task 3: Add `host_agent.proto` to the `proto-gen` Makefile target

**Files:** `Makefile`

**Steps:**

1. Find the `proto-gen:` rule. The proto-file list currently ends with `publisher.proto`.

2. Add `host_agent.proto` to the end of the list.

**Verification:** `grep 'host_agent.proto' Makefile`.

### Task 4: Regenerate Go bindings

**Files:** `protocols/proto/v1/gen/` (auto-regenerated)

**Steps:**

1. Run `make proto-gen`.

2. Confirm new generated files for `host_agent.proto` exist:
   - `protocols/proto/v1/gen/host_agent.pb.go`
   - `protocols/proto/v1/gen/host_agent_grpc.pb.go`

3. Confirm `lifecycle.pb.go` / `lifecycle_grpc.pb.go` reflect the new RPC + field additions.

4. Build the protocols module to confirm generated code compiles: `cd protocols && go build ./... && cd ..`.

**Verification:** `make proto-gen && test -f protocols/proto/v1/gen/host_agent.pb.go && test -f protocols/proto/v1/gen/host_agent_grpc.pb.go && cd protocols && go build ./... && cd ..`.

### Task 5: Extend the Go `LifecycleSubscriber` interface + request structs

**Files:** `protocols/lifecycle/types.go`, `protocols/lifecycle/lifecycle.go`

**Steps:**

1. Read `protocols/lifecycle/types.go`. Find the existing `On*Request` Go structs (`OnTemplateRegisteredRequest`, etc., `OnInstanceCreatedRequest` at line 39).

2. Extend `OnInstanceCreatedRequest` with two new fields (matching Task 2's proto change). Position the fields after `Params` to match the proto field order:

```go
// ServiceBindings carries the per-instance late-bound service catalog
// (opaque JSON bytes). Empty when the instance has no late-bound services.
// Consumed by the host-agent-proxy to populate its binding cache.
ServiceBindings json.RawMessage

// OwnerAPIKeyID is the api-key whose authenticated request created the
// instance. Empty string for anonymous-mode-created instances. Consumed
// by the host-agent-proxy to route dispatches to the right user's agent.
OwnerAPIKeyID string
```

(If `encoding/json` is not already imported in this file, add it.)

3. Add a new request struct at the bottom of `types.go`:

```go
// OnRunScopeTerminalRequest fires when a run-scope reaches terminal state.
// Fired from the rimsky-side process that owns the state transition
// (control-api for main scopes; the supervisor for sub-graph and
// fanout-partition scopes). Consumed by the host-agent-proxy to drive
// spawn reaping.
//
// RunScopeID is a string (UUID hex form) for consistency with the other
// On*Request structs in this file, which all use string UUIDs.
type OnRunScopeTerminalRequest struct {
    RunScopeID     string
    TerminalReason string
}
```

4. Read `protocols/lifecycle/lifecycle.go`. Find the `LifecycleSubscriber` interface (6 methods today). Add a 7th method:

```go
// OnRunScopeTerminal is fired when a run-scope reaches terminal state.
// See OnRunScopeTerminalRequest documentation for firing semantics.
OnRunScopeTerminal(ctx context.Context, req OnRunScopeTerminalRequest) error
```

5. Build the protocols module: `cd protocols && go build ./... && cd ..`. (Code that *consumes* the interface — peer client, test fakes — won't compile yet. Tasks 6 + 7 fix those.)

**Verification:** `cd protocols && go build ./... && cd ..`.

### Task 6: Add type alias + peer-client adapter for the new method

**Files:** `foundation/locks/lifecycle.go`, `runtime/peer/lifecycle_client.go`

**Steps:**

1. Read `foundation/locks/lifecycle.go`. It re-exports `lifecycle.On*Request` as `locks.On*Request` via type aliases. Add:

```go
OnRunScopeTerminalRequest = lifecycle.OnRunScopeTerminalRequest
```

(Place in alphabetic/file order with the existing aliases.)

2. Read `runtime/peer/lifecycle_client.go`. The file implements each Go-interface method by translating to the gen-v1 proto and calling the wrapped gRPC client (`c.rpc.OnInstanceCreated(ctx, ...)` etc.).

3. Update the existing `OnInstanceCreated` method to set the two new proto fields:

```go
func (c *LifecycleClient) OnInstanceCreated(ctx context.Context, req lifecycle.OnInstanceCreatedRequest) error {
    _, err := c.rpc.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{
        InstanceId:      req.InstanceID.String(),
        TemplateHash:    req.TemplateHash,
        InstanceKey:     req.InstanceKey,
        Params:          req.Params,
        ServiceBindings: req.ServiceBindings,  // NEW
        OwnerApiKeyId:   req.OwnerAPIKeyID,    // NEW
    })
    return err
}
```

4. Add a new method matching the 7th interface method:

```go
func (c *LifecycleClient) OnRunScopeTerminal(ctx context.Context, req lifecycle.OnRunScopeTerminalRequest) error {
    _, err := c.rpc.OnRunScopeTerminal(ctx, &genv1.OnRunScopeTerminalRequest{
        RunScopeId:     req.RunScopeID,  // already a string
        TerminalReason: req.TerminalReason,
    })
    return err
}
```

5. Build the peer module: `go build ./runtime/peer/...`. (Full-tree build fails until Task 7 updates test fakes.)

**Verification:** `go build ./runtime/peer/...`.

### Task 7: Update all `LifecycleSubscriber` implementations (test fakes + any other implementers)

**Files:** Sweep with `rg 'lifecycle\.LifecycleSubscriber|locks\.LifecycleSubscriber' --include='*.go'` to find every implementation.

**Steps:**

1. For each Go type implementing `LifecycleSubscriber`, add a stub `OnRunScopeTerminal` method. Test fakes that record method calls (look for patterns like `recordedEvents`, `lifecycleCalls`, or method-tracking maps) append entries to their recording structure following the file's existing convention for `OnInstanceCreated`.

2. For non-test implementations (likely zero in v1, but verify), add the method body matching the implementation's pattern. The host-agent-proxy's LifecycleSubscriber implementation in Pass 8 supplies the real handler — for any other v1 implementation, return nil.

3. Test fakes that record an `OnInstanceCreatedRequest` should also record the two new fields (`ServiceBindings`, `OwnerAPIKeyID`).

4. Build and test: `go build ./... && cd foundation && go build ./... && cd .. && cd protocols && go build ./... && cd ..`.

**Verification:** `go build ./... && cd foundation && go build ./... && cd .. && cd protocols && go build ./... && cd ..`.

---

## Pass 2: Schema + persistence types

**Goal:** Add the two new columns on `rimsky_instances` (`service_bindings JSONB`, `created_by_api_key_id UUID`), extend `LifecycleIdempotencyScope*` and `LifecycleIdempotencyState*` enums with the new `RunScope` / `RunScopeTerminal` values, update `InstanceCreateInput` / `InstanceRow` shapes, update Postgres + SQLite INSERT/SELECT SQL on instances, and extend `SelectCandidatesRequest` with the new admit-list fields.
**Scope:** Tasks 8–13
**End state:** working
**Verification:** `cd foundation && go build ./... && go test ./persistence/... -count=1 && cd ..`

### Task 8: Postgres migration 002 — new columns + lifecycle idempotency scope

**Files:** `foundation/persistence/postgres/migrations/002-host-agent-proxy.sql` (new)

**Steps:**

1. Read `foundation/persistence/postgres/migrations/001-schema.sql`. Find the `rimsky_instances` CREATE TABLE (around the top) to confirm column shapes. Find the `rimsky_lifecycle_idempotencies` CREATE TABLE (around line 420+) to identify the existing inline CHECK constraints on `scope_kind` and `state` columns — Postgres auto-names these (typically `rimsky_lifecycle_idempotencies_scope_kind_check` and `rimsky_lifecycle_idempotencies_state_check`).

2. Confirm the actual constraint names by reading the result of `pg_dump` or by checking via `grep -n 'CHECK' foundation/persistence/postgres/migrations/001-schema.sql`. Use the exact names from `001-schema.sql`'s constraint declarations.

3. Create `foundation/persistence/postgres/migrations/002-host-agent-proxy.sql`:

```sql
-- Host-agent and proxy: per-spec 2026-05-24-host-agent-and-proxy-design.md.
--
-- Adds two new columns on rimsky_instances + extends the CHECK constraint
-- vocabulary on rimsky_lifecycle_idempotencies for the new "run_scope"
-- scope_kind and "run_scope_terminal" state value.

-- Add the two new columns.
ALTER TABLE rimsky_instances
    ADD COLUMN service_bindings JSONB,
    ADD COLUMN created_by_api_key_id UUID REFERENCES rimsky_api_keys(id);

-- Extend the scope_kind CHECK constraint. Drop the existing constraint and
-- recreate with the expanded value set. Constraint name must match what
-- Postgres auto-generated in 001-schema.sql — verify by reading the
-- CREATE TABLE for rimsky_lifecycle_idempotencies and adapting if the
-- auto-name differs from the default convention.
ALTER TABLE rimsky_lifecycle_idempotencies
    DROP CONSTRAINT rimsky_lifecycle_idempotencies_scope_kind_check,
    ADD CONSTRAINT rimsky_lifecycle_idempotencies_scope_kind_check
        CHECK (scope_kind IN ('template', 'instance', 'run_scope'));

-- Extend the state CHECK constraint with the new run_scope_terminal value.
-- IMPORTANT: read the actual state vocabulary from 001-schema.sql's CHECK
-- constraint AND from foundation/persistence/lifecycle_idempotency.go's
-- LifecycleIdempotencyState* constants. Copy the existing values
-- VERBATIM into the IN clause, then append 'run_scope_terminal'. As of the
-- current Go enum, the values are: 'registered', 'deployed', 'undeployed',
-- 'created' (no 'terminated' — verify against the current source before
-- writing the migration). The literal IN-list MUST exactly match the
-- existing constants + the new one.
ALTER TABLE rimsky_lifecycle_idempotencies
    DROP CONSTRAINT rimsky_lifecycle_idempotencies_state_check,
    ADD CONSTRAINT rimsky_lifecycle_idempotencies_state_check
        CHECK (state IN (<copy existing values from 001-schema.sql>, 'run_scope_terminal'));
```

4. Read `code:foundation/persistence/lifecycle_idempotency.go` lines 26-29 (the `LifecycleIdempotencyState*` constants) AND `code:foundation/persistence/postgres/migrations/001-schema.sql`'s CHECK constraint on the state column. Substitute the placeholder above with the actual values verbatim, plus `'run_scope_terminal'`.

**Verification:** `test -f foundation/persistence/postgres/migrations/002-host-agent-proxy.sql && grep -q 'service_bindings JSONB' foundation/persistence/postgres/migrations/002-host-agent-proxy.sql && grep -q 'created_by_api_key_id UUID' foundation/persistence/postgres/migrations/002-host-agent-proxy.sql && grep -q 'run_scope_terminal' foundation/persistence/postgres/migrations/002-host-agent-proxy.sql`.

### Task 9: SQLite migration 002 — table-rebuild for CHECK constraint update

**Files:** `foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql` (new)

**Steps:**

1. Read `foundation/persistence/sqlite/migrations/001-schema.sql`. Find the SQLite shapes of `rimsky_instances` and `rimsky_lifecycle_idempotencies`. Note: SQLite typically uses `TEXT` for UUIDs (with `default '00000000-...'` or just nullable) and `TEXT` or `BLOB` for JSON. Confirm against the existing column types in `001-schema.sql`.

2. **SQLite cannot DROP CONSTRAINT in place.** The migration must rebuild the `rimsky_lifecycle_idempotencies` table via the standard SQLite idiom:

```sql
-- Host-agent and proxy: parallel to postgres 002. Per spec
-- 2026-05-24-host-agent-and-proxy-design.md.

-- Add the two new columns on rimsky_instances (ALTER TABLE ADD COLUMN
-- works on SQLite even with NOT NULL if a default is supplied; both
-- columns are nullable here so no default needed).
ALTER TABLE rimsky_instances ADD COLUMN service_bindings <JSON-OR-BLOB>;
ALTER TABLE rimsky_instances ADD COLUMN created_by_api_key_id TEXT REFERENCES rimsky_api_keys(id);

-- Rebuild rimsky_lifecycle_idempotencies to extend the CHECK constraints.
-- Standard SQLite ALTER-CHECK idiom: create _new with new constraints,
-- copy data, drop old, rename _new → old.
CREATE TABLE rimsky_lifecycle_idempotencies_new (
    -- copy the exact column definitions from 001-schema.sql, with the
    -- two CHECK constraints extended:
    --   scope_kind: ('template', 'instance', 'run_scope')
    --   state: (existing values + 'run_scope_terminal')
    -- ... (full column list)
);

INSERT INTO rimsky_lifecycle_idempotencies_new
    SELECT * FROM rimsky_lifecycle_idempotencies;

DROP TABLE rimsky_lifecycle_idempotencies;

ALTER TABLE rimsky_lifecycle_idempotencies_new
    RENAME TO rimsky_lifecycle_idempotencies;

-- Recreate any indexes that 001-schema.sql declared on the old table.
-- (Indexes don't survive the DROP TABLE; copy CREATE INDEX statements
-- from 001 verbatim.)
```

3. Fill in the exact column definitions and indexes from `001-schema.sql`. Substitute `<JSON-OR-BLOB>` with whatever the file uses for other JSON-typed columns (search for `JSON` or `BLOB` in 001-schema.sql).

4. Verify against the SQLite migration runner — if it has special handling for foreign-key references, ensure the rebuild preserves them.

**Verification:** `test -f foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql && grep -q 'service_bindings' foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql && grep -q 'created_by_api_key_id' foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql && grep -q 'rimsky_lifecycle_idempotencies_new' foundation/persistence/sqlite/migrations/002-host-agent-proxy.sql`.

### Task 10: Extend `LifecycleIdempotencyScope*` and `LifecycleIdempotencyState*` Go enums

**Files:** `foundation/persistence/lifecycle_idempotency.go`

**Steps:**

1. Read the file. Existing constants:
   - `LifecycleIdempotencyScopeTemplate = "template"` (line 17)
   - `LifecycleIdempotencyScopeInstance = "instance"` (line 18)
   - `LifecycleIdempotencyState*` values (line 26+).

2. Add a third scope-kind constant in the same `const (...)` block:

```go
LifecycleIdempotencyScopeRunScope LifecycleIdempotencyScopeKind = "run_scope"
```

3. Add a new state constant in the same `const (...)` block as the other `LifecycleIdempotencyState*` values:

```go
LifecycleIdempotencyStateRunScopeTerminal LifecycleIdempotencyState = "run_scope_terminal"
```

4. Build: `cd foundation && go build ./... && cd ..`.

**Verification:** `cd foundation && grep -q 'LifecycleIdempotencyScopeRunScope' persistence/lifecycle_idempotency.go && grep -q 'LifecycleIdempotencyStateRunScopeTerminal' persistence/lifecycle_idempotency.go && go build ./... && cd ..`.

### Task 11: Extend `InstanceCreateInput` and `InstanceRow` with the two new fields

**Files:** `foundation/persistence/instances.go`

**Steps:**

1. Read the current `InstanceCreateInput` struct (around line 134) and `InstanceRow` struct (around line 25). Match existing field tag conventions (JSON tags use snake_case keys).

2. Add to both structs:

```go
// ServiceBindings is opaque JSONB carrying the per-instance late-bound
// service catalog. Empty for instances that don't use late-bound services.
ServiceBindings json.RawMessage `json:"service_bindings,omitempty"`

// CreatedByAPIKeyID is the api-key whose authenticated request created
// the instance. Nil for anonymous-mode-created instances.
CreatedByAPIKeyID *shared.UUID `json:"created_by_api_key_id,omitempty"`
```

(Position the fields together near other per-deployment knobs like `Params`. Verify imports include `encoding/json` and `github.com/fallguyconsulting/rimsky/foundation/shared`.)

3. Build: `cd foundation && go build ./... && cd ..`.

**Verification:** `cd foundation && grep -q 'ServiceBindings' persistence/instances.go && grep -q 'CreatedByAPIKeyID' persistence/instances.go && go build ./... && cd ..`.

### Task 12: Update Postgres + SQLite INSERT/SELECT SQL on `rimsky_instances`

**Files:** `foundation/persistence/postgres/instances.go`, `foundation/persistence/sqlite/instances.go`

**Steps:**

1. Read `foundation/persistence/postgres/instances.go`. Find the `Create` (or `Insert`) method. Find every `SELECT ... FROM rimsky_instances` query that hydrates `InstanceRow` (likely in `Get`, `List`, `ListTerminatedWithLifecycle*`, etc.).

2. Update the Postgres INSERT to include `service_bindings, created_by_api_key_id` columns and corresponding `$N` parameter placeholders. Update the parameter list. NULL handling: pass `nil` for missing `*shared.UUID` (pgx handles nullable UUIDs naturally); pass `nil` or `[]byte(nil)` for empty `json.RawMessage`.

3. Update every `SELECT` query to include the two new columns in the projection list. Update the corresponding `rows.Scan(...)` (or struct-scanner) call to read into `&row.ServiceBindings, &row.CreatedByAPIKeyID`.

4. Do the same for `foundation/persistence/sqlite/instances.go`.

5. Build and test foundation: `cd foundation && go build ./... && go test ./persistence/... -count=1 && cd ..`.

**Verification:** `cd foundation && go build ./... && go test ./persistence/... -count=1 && cd ..`.

### Task 13: Extend `SelectCandidatesRequest` and `SelectCandidates` admit-list clause

**Files:** `foundation/persistence/node_runs.go`, `foundation/persistence/postgres/queue.go`

**Steps:**

1. Read `code:foundation/persistence/node_runs.go::SelectCandidatesRequest` (line 75). Today carries `AcceptedExecutors`, `AcceptedStores`, `Limit` (or similar). Add two new fields:

```go
// LateBindExecutorProxy is the rimsky.yml-configured proxy peer name
// for the executor protocol (late_bind_service_proxies.executor).
// Empty string when no late-bind proxy is configured. Used by the
// admit-list extension to claim rows whose executor_name appears
// only in the instance's service_bindings.
LateBindExecutorProxy string

// LateBindClaimProducerProxy is the rimsky.yml-configured proxy peer
// name for the claim_producer protocol. Empty string when none.
LateBindClaimProducerProxy string
```

2. Read `code:foundation/persistence/postgres/queue.go::SelectCandidates` (line 172). The current WHERE clause has the form:

```sql
WHERE d.executor_name = ANY($2::text[])
   OR (d.executor_name IS NULL AND COALESCE(array_length(d.required_stores, 1), 0) > 0)
  AND d.required_stores <@ $1::text[]
```

(Read the actual SQL — adjust to match.)

3. Extend the WHERE to admit late-bound rows. The new parameters become `$3` (executor proxy name, may be empty) and `$4` (claim-producer proxy name, may be empty):

```sql
WHERE (
        d.executor_name = ANY($2::text[])
        OR (d.executor_name IS NULL AND COALESCE(array_length(d.required_stores, 1), 0) > 0)
        OR (
              $3 <> ''
              AND $3 = ANY($2::text[])
              AND i.service_bindings ? d.executor_name
        )
      )
  AND (
        d.required_stores <@ $1::text[]
        OR (
              $4 <> ''
              AND $4 = ANY($1::text[])
              AND (SELECT COALESCE(bool_and(i.service_bindings ? n), false) FROM unnest(d.required_stores) AS n)
        )
      )
```

(`i.service_bindings ?` is Postgres JSONB key-existence; combined with `unnest()` to test every name in `required_stores` against the JSONB keys.)

4. Pass `req.LateBindExecutorProxy` and `req.LateBindClaimProducerProxy` as `$3, $4` in the query invocation.

5. Build foundation and run persistence tests: `cd foundation && go build ./... && go test ./persistence/postgres/... -count=1 && cd ..`. Existing call sites that don't populate the two new fields will leave them empty (`""`), and the `<> ''` check makes the OR-branches inert — behavior is identical to today.

**Verification:** `cd foundation && go build ./... && go test ./persistence/... -count=1 && cd ..`.

---

## Pass 3: Dispatch resolution foundations — DispatchContext + Resolver + Registry + InstanceID threading

**Goal:** Introduce `DispatchContext`, extend `Resolver.Resolve` signature, add `LateBindResolver`, add `*locks.Registry.GetWithContext` via functional options, and thread `InstanceID` through `acquireOneLock` / `acquireClaim` / `acquireFanOutIfDeclared` and `AcquireSubClaimsInput`. Switch the dispatch sites to the new `GetWithContext` and the new `Resolve(name, ctx)`.
**Scope:** Tasks 14–20
**End state:** working
**Verification:** `go build ./... && go test ./runtime/... ./foundation/locks/... -count=1`

### Task 14: Add `DispatchContext` type + extend `Resolver.Resolve` signature

**Files:** `runtime/executor/resolver.go`

**Steps:**

1. Read the current file. Today: `Resolver.Resolve(name string) (Endpoint, bool)`; `StaticResolver` is a concrete impl.

2. Add the `DispatchContext` type at the top of the file under `package executor`:

```go
// DispatchContext carries instance/run-scope identity into resolver
// lookups. Named DispatchContext (rather than ResolveContext) to avoid
// the symbol clash with graph/attribute/substitution.go::ResolveContext,
// which is rimsky's existing substitution context.
type DispatchContext struct {
    InstanceID string  // empty for non-instance-scoped resolution
    RunScopeID string  // ditto
}
```

3. Change the `Resolver` interface:

```go
type Resolver interface {
    Resolve(name string, ctx DispatchContext) (Endpoint, bool)
    AcceptedNames() []string
}
```

4. Update `StaticResolver.Resolve` to accept the new parameter and ignore it:

```go
func (r *StaticResolver) Resolve(name string, _ DispatchContext) (Endpoint, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    e, ok := r.m[name]
    return e, ok
}
```

5. Find every caller of `Resolver.Resolve` and every implementation of `Resolver`: `rg 'Resolve\(' --include='*.go'` then filter. For each:
   - Implementations (test fakes, mocks): match the new signature, ignore the new arg.
   - Callers: pass `executor.DispatchContext{}` (zero value) at every site that doesn't yet have run context. Pass 4's tasks will replace dispatch sites with proper context.

6. Build: `go build ./...`.

**Verification:** `go build ./...`.

### Task 15: Add `LateBindResolver` chained after `StaticResolver`

**Files:** `runtime/executor/resolver.go`

**Steps:**

1. In `runtime/executor/resolver.go`, add a new resolver type after `StaticResolver`:

```go
// LateBindResolver chains after a static resolver. For names not in
// the static map, it consults a per-instance service_bindings lookup
// hook and a static late_bind_service_proxies map (loaded from
// rimsky.yml). If both produce a hit, it returns the proxy's endpoint
// (resolved via the underlying static resolver).
//
// The dispatch path attaches the original service name to the call
// context via the per-call gRPC interceptor (Pass 4); the proxy
// reads the header to route. LateBindResolver does not add any
// metadata to the returned Endpoint.
type LateBindResolver struct {
    static          Resolver
    lookupBindings  func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
    lateBindProxies map[string]string  // protocol → proxy service name
}

// NewLateBindResolver wraps a static resolver with late-bind fallback.
// When lookupBindings is nil or lateBindProxies is empty, the resolver
// behaves as a passthrough — the static resolver's results are
// returned unchanged.
func NewLateBindResolver(
    static Resolver,
    lookupBindings func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error),
    lateBindProxies map[string]string,
) *LateBindResolver {
    return &LateBindResolver{
        static:          static,
        lookupBindings:  lookupBindings,
        lateBindProxies: lateBindProxies,
    }
}

func (r *LateBindResolver) Resolve(name string, ctx DispatchContext) (Endpoint, bool) {
    if ep, ok := r.static.Resolve(name, ctx); ok {
        return ep, true
    }
    if ctx.InstanceID == "" {
        return Endpoint{}, false
    }
    if r.lookupBindings == nil || len(r.lateBindProxies) == 0 {
        return Endpoint{}, false
    }
    proxyName, ok := r.lateBindProxies["executor"]
    if !ok || proxyName == "" {
        return Endpoint{}, false
    }
    bindings, ok, err := r.lookupBindings(context.Background(), ctx.InstanceID)
    if err != nil || !ok {
        return Endpoint{}, false
    }
    if _, exists := bindings[name]; !exists {
        return Endpoint{}, false
    }
    return r.static.Resolve(proxyName, ctx)
}

func (r *LateBindResolver) AcceptedNames() []string {
    return r.static.AcceptedNames()
}
```

2. Add imports for `context` and `encoding/json` if not already present.

3. Build: `go build ./runtime/executor/...`.

**Verification:** `go build ./runtime/executor/...`.

### Task 16: Add `GetWithContext` + functional options to `*locks.Registry`

**Files:** `foundation/locks/registry.go`

**Steps:**

1. Read the file. Today: `Registry` is `{producers map[string]ClaimProducer}`; `NewRegistry()` is zero-arg.

2. Extend the struct with two private fields:

```go
type Registry struct {
    producers              map[string]ClaimProducer
    lookupInstanceBindings func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
    lateBindServiceProxies map[string]string
}
```

3. Add the `Option` type and option-applier functions:

```go
// Option configures a Registry at construction time.
type Option func(*Registry)

// WithLookupInstanceBindings supplies the persistence hook for
// late-bound claim-producer resolution. When nil (the default),
// the registry has no late-bind support.
func WithLookupInstanceBindings(fn func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)) Option {
    return func(r *Registry) { r.lookupInstanceBindings = fn }
}

// WithLateBindServiceProxies supplies the per-protocol proxy-name
// map (loaded from rimsky.yml's late_bind_service_proxies). When
// empty or nil, the registry has no late-bind support.
func WithLateBindServiceProxies(m map[string]string) Option {
    return func(r *Registry) { r.lateBindServiceProxies = m }
}
```

4. Change `NewRegistry` to accept options:

```go
func NewRegistry(opts ...Option) *Registry {
    r := &Registry{producers: make(map[string]ClaimProducer)}
    for _, opt := range opts {
        opt(r)
    }
    return r
}
```

5. Add the new context-aware method. Foundation cannot import `runtime/`, so use a plain `instanceID string` parameter rather than the `DispatchContext` type (with a `@diverged` comment recording the parallelism choice):

```go
// GetWithContext is the late-bind-aware sibling of Get. When the
// registry was constructed without late-bind options (or no instance
// context is supplied), it falls through to Get(name) — behavior is
// identical to today.
//
// @diverged: true
// @reason: parallels runtime/executor/resolver.go::Resolver.Resolve(name, DispatchContext)
//          but uses a plain instanceID arg instead of a DispatchContext type
//          to avoid foundation→runtime imports (banned by layer-purity).
func (r *Registry) GetWithContext(name string, instanceID string) (ClaimProducer, bool) {
    if p, ok := r.Get(name); ok {
        return p, true
    }
    if instanceID == "" {
        return nil, false
    }
    if r.lookupInstanceBindings == nil {
        return nil, false
    }
    proxyName, ok := r.lateBindServiceProxies["claim_producer"]
    if !ok || proxyName == "" {
        return nil, false
    }
    bindings, ok, err := r.lookupInstanceBindings(context.Background(), instanceID)
    if err != nil || !ok {
        return nil, false
    }
    if _, exists := bindings[name]; !exists {
        return nil, false
    }
    return r.Get(proxyName)
}
```

6. Add imports for `context` and `encoding/json`.

7. Build and test: `cd foundation && go build ./... && go test ./locks/... -count=1 && cd ..`. Existing zero-arg `NewRegistry()` call sites continue to compile.

**Verification:** `cd foundation && go build ./... && go test ./locks/... -count=1 && cd ..`.

### Task 17: Thread `instanceID` through `acquireOneLock` and `acquireClaim`

**Files:** `runtime/runner_acquire_named_locks.go`, `runtime/runner_acquire_claims.go`

**Steps:**

1. Read both files. `acquireClaim` is called from `acquireOneLock` at `code:runtime/runner_acquire_named_locks.go:59`.

2. Add an `instanceID shared.UUID` parameter to both function signatures. Place after `tx` to match the convention.

3. Inside both functions, when calling `args.StoreRegistry.Get(producerName)`, replace with:

```go
producer, ok := args.StoreRegistry.GetWithContext(producerName, instanceID.String())
```

4. Build will fail until Task 18 wires `tryAcquire` to pass `nd.InstanceID`. The next task's verification covers the merged state.

**Verification:** Deferred — Task 18's verification covers.

### Task 18: Update `tryAcquire` to pass `nd.InstanceID` to `acquireOneLock` and `acquireFanOutIfDeclared`

**Files:** `runtime/runner_acquire.go`, `runtime/runner_acquire_helpers.go`

**Steps:**

1. Read `runtime/runner_acquire.go::tryAcquire`. Confirm `nd` is fetched around line 424 via `args.Persist.Nodes().Get(ctx, cand.NodeID, tx)`.

2. Find each `acquireOneLock(...)` call inside `tryAcquire` and pass `nd.InstanceID` as the new parameter.

3. Read `runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared` (around line 36). Today: `(ctx, args, tx, *acquisition, persistence.Candidate, *node.TemplateNodeDef, []AcquiredLock, time.Duration)`. Add `instanceID shared.UUID` after `tx`.

4. Find the call to `acquireFanOutIfDeclared` in `tryAcquire` (around line 553) and pass `nd.InstanceID`.

5. **Sweep test callers** of `acquireFanOutIfDeclared`: `rg 'acquireFanOutIfDeclared\(' runtime/`. Specifically, `code:runtime/runner_acquire_helpers_test.go` has at least three call sites (lines 144, 192, 230 — verify). Each test invocation must pass `shared.UUID{}` (zero UUID) for the new `instanceID` parameter; behavior under `GetWithContext` is identical to today's `Get` when `instanceID == ""`.

6. Build: `go build ./runtime/...`.

**Verification:** `go build ./runtime/...`.

### Task 19: Add `InstanceID` field to `AcquireSubClaimsInput`; wire production + test callers

**Files:** `runtime/runner_subclaim.go`, `runtime/runner_acquire_helpers.go`, `runtime/runner_subclaim_test.go`

**Steps:**

1. Read `runtime/runner_subclaim.go`. Find `AcquireSubClaimsInput` (around line 45). Add:

```go
// InstanceID is sourced from the parent NodeRunRow's InstanceID,
// threaded through tryAcquire → acquireFanOutIfDeclared. Used by
// Registry.GetWithContext for late-bound claim-producer resolution.
InstanceID shared.UUID
```

2. In `runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared` (around line 89, the production caller that builds `AcquireSubClaimsInput`), populate the new field from the `instanceID` parameter threaded in by Task 18:

```go
input := AcquireSubClaimsInput{
    // ... existing fields
    InstanceID: instanceID,
}
```

3. Inside the body of `AcquireSubClaims` (find via `rg 'func.*AcquireSubClaims'`), replace any `args.StoreRegistry.Get(producerName)` calls with `args.StoreRegistry.GetWithContext(producerName, input.InstanceID.String())`.

4. Update test callers in `runner_subclaim_test.go` at lines 49, 72, 301. Each constructs an `AcquireSubClaimsInput` literal — add `InstanceID: shared.UUID{}` (zero UUID; `GetWithContext` returns false at step 2 of its semantics, so static-only fixtures are unaffected).

5. Build and test: `go build ./... && go test ./runtime/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... -count=1`.

### Task 20: Sweep remaining `Registry.Get` dispatch-path callers

**Files:** Run `rg 'StoreRegistry\.Get\(' runtime/` (and `rg '\.Get\(' foundation/locks/`) to find call sites.

**Steps:**

1. For each match, determine: is the caller in a dispatch-time path with `InstanceID` available?
   - Dispatch path → switch to `GetWithContext(name, instanceID.String())` (thread `instanceID` from the surrounding scope if needed).
   - Startup-time / sweep with no instance context → leave as bare `Get(name)`.

2. Briefly document each touched call site with a one-line comment if the switch reason isn't obvious from context.

3. Build and test: `go build ./... && go test ./runtime/... ./foundation/locks/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... ./foundation/locks/... -count=1`.

---

## Pass 4: Per-call header interceptor + claim-producer error_class translation

**Goal:** Install a gRPC client-side `UnaryClientInterceptor` + `StreamClientInterceptor` on the supervisor's dial of every peer service, attaching `x-rimsky-service-name` from the per-call context. Stamp the header at executor and claim-producer dispatch sites. Translate claim-producer gRPC status errors into a typed error carrying `error_class`, then route claim-producer failures through `applyErrorPolicy` so the existing `error_types:` chain consumes them.
**Scope:** Tasks 21–26
**End state:** working
**Verification:** `go build ./... && go test ./runtime/... -count=1`

### Task 21: Add the gRPC client-side interceptor

**Files:** `runtime/clientiface/service_name_interceptor.go` (new)

**Steps:**

1. Confirm `runtime/clientiface/` is the right home (check sibling files there). If `clientiface` is shared infrastructure for typed wire types and doesn't own gRPC plumbing, use `runtime/peer/` or `runtime/executor/` instead — pick the location nearest the existing gRPC dial code that already imports `google.golang.org/grpc`.

2. Define the interceptor pair + context helper:

```go
package <appropriate-package>

import (
    "context"
    "google.golang.org/grpc"
    "google.golang.org/grpc/metadata"
)

type serviceNameKey struct{}

// WithServiceName returns ctx with the given service name attached
// for the next outbound gRPC call. Returns ctx unchanged when name
// is empty (no-op on irrelevant call paths).
func WithServiceName(ctx context.Context, name string) context.Context {
    if name == "" {
        return ctx
    }
    return context.WithValue(ctx, serviceNameKey{}, name)
}

// ServiceNameUnaryInterceptor stamps x-rimsky-service-name from the
// per-call context onto outgoing unary RPCs. No-op when the context
// carries no service name. Hosted (non-proxy) services ignore the
// header; the host-agent-proxy reads it to route.
func ServiceNameUnaryInterceptor(
    ctx context.Context, method string, req, reply any,
    cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
    if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
        ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
    }
    return invoker(ctx, method, req, reply, cc, opts...)
}

// ServiceNameStreamInterceptor is the streaming-RPC equivalent. The
// interceptor fires once at stream creation and the metadata travels
// in the initial HTTP/2 headers; subsequent stream frames inherit
// the same call context (no per-frame handling needed for
// server-streaming RPCs like Executor.Execute).
func ServiceNameStreamInterceptor(
    ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
    method string, streamer grpc.Streamer, opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
    if name, ok := ctx.Value(serviceNameKey{}).(string); ok && name != "" {
        ctx = metadata.AppendToOutgoingContext(ctx, "x-rimsky-service-name", name)
    }
    return streamer(ctx, desc, cc, method, opts...)
}
```

3. Build: `go build ./runtime/...`.

**Verification:** `go build ./runtime/...`.

### Task 22: Install the interceptor on the executor and claim-producer dial sites

**Files:** `runtime/executor/client.go`, `runtime/peer/dial.go`

**Steps:**

1. Read `runtime/executor/client.go`. Find where `grpc.NewClient` (or `grpc.Dial`) is called for the executor gRPC connection. Add interceptors to the dial options:

```go
opts = append(opts,
    grpc.WithUnaryInterceptor(<pkg>.ServiceNameUnaryInterceptor),
    grpc.WithStreamInterceptor(<pkg>.ServiceNameStreamInterceptor),
)
```

(Substitute the package import based on Task 21's location.)

2. Read `runtime/peer/dial.go::Dial`. Add the same interceptors to its `grpc.NewClient` call.

3. For other peer-service clients (`lifecycle_client.go`, `publisher_client.go`, `validation_client.go`, `data_processing_client.go`): leave a `// TODO(host-agent-proxy v2): install ServiceName interceptor here when this protocol gains late-bind support` comment at each dial site. v1 only fronts executor and claim-producer.

4. Build: `go build ./runtime/...`.

**Verification:** `go build ./runtime/...`.

### Task 23: Stamp the service-name on executor dispatch contexts

**Files:** `runtime/runner_dispatch.go`

**Steps:**

1. Find every `Execute(...)` call on the executor client via `rg 'executor.*Execute\(' runtime/`. The primary dispatch site is around `runtime/runner_dispatch.go:206`; the executor name in scope there is `acq.Executor` (from `*acquisition`).

2. Before each `Execute(...)` call, stamp the name:

```go
ctx = <pkg>.WithServiceName(ctx, acq.Executor)
```

(Use the import path from Task 21.)

3. Build and test: `go build ./... && go test ./runtime/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... -count=1`.

### Task 24: Stamp the service-name on claim-producer dispatch contexts

**Files:** `runtime/runner_acquire_claims.go`, `runtime/auto_terminal_chain.go` (and other Commit/Abandon/Release call sites)

**Steps:**

1. Find every `producer.Open(...)`, `producer.Commit(...)`, `producer.Abandon(...)`, `producer.Release(...)` call via `rg 'producer\.(Open|Commit|Abandon|Release)\(' runtime/`.

2. At each site, the producer's name is in scope (local var or accessible via surrounding context). Stamp it on the context:

```go
ctx = <pkg>.WithServiceName(ctx, producerName)
err := producer.Open(ctx, ...)
```

3. Build and test: `go build ./... && go test ./runtime/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... -count=1`.

### Task 25: Translate claim-producer gRPC status → typed error carrying `error_class`

**Files:** `runtime/peer/client.go`

**Steps:**

1. Read `runtime/peer/client.go`. The `Open`/`Commit`/`Abandon`/`Release` methods today wrap gRPC errors as `fmt.Errorf("remote producer %q: Open: %w", ...)`.

2. Add the typed error and the gRPC-status helper to the same file (or a sibling `runtime/peer/errors.go`):

```go
import (
    "google.golang.org/grpc/status"
    "google.golang.org/genproto/googleapis/rpc/errdetails"
)

// ProducerCallError is returned by remote claim-producer calls when
// the underlying gRPC call failed. It carries the rimsky error_class
// extracted from the gRPC status's ErrorInfo detail (or "" if none).
// The error-policy chain at applyErrorPolicy reads ErrorClass to
// consult the template's error_types: chain.
type ProducerCallError struct {
    ProducerName string
    Method       string  // "Open", "Commit", etc.
    ErrorClass   string  // empty if no ErrorInfo on the gRPC status
    Underlying   error
}

func (e *ProducerCallError) Error() string {
    return fmt.Sprintf("remote producer %q: %s: %v", e.ProducerName, e.Method, e.Underlying)
}

func (e *ProducerCallError) Unwrap() error { return e.Underlying }

// extractErrorClass walks the gRPC status details for a
// google.rpc.ErrorInfo entry and returns its Reason as the
// rimsky error_class. Returns "" when no ErrorInfo is attached.
func extractErrorClass(err error) string {
    st, ok := status.FromError(err)
    if !ok {
        return ""
    }
    for _, d := range st.Details() {
        if info, ok := d.(*errdetails.ErrorInfo); ok {
            return info.Reason
        }
    }
    return ""
}
```

3. Update each method (`Open`, `Commit`, `Abandon`, `Release`) on `Client`:

```go
if err != nil {
    return nil, &ProducerCallError{
        ProducerName: c.name,
        Method:       "Open",  // adjust per method
        ErrorClass:   extractErrorClass(err),
        Underlying:   err,
    }
}
```

(Existing call-site error types — `(*OpenResponse, error)`, `error`, etc. — stay unchanged; the new typed error wraps the underlying error rather than replacing it.)

4. Build: `go build ./runtime/peer/...`.

**Verification:** `go build ./runtime/peer/...`.

### Task 26: Route claim-producer errors through `applyErrorPolicy`

**Files:** `runtime/runner_acquire_claims.go` (and Commit/Abandon/Release call sites)

**Steps:**

1. Read `code:runtime/runner_error_policy.go::applyErrorPolicy` (line 54). The actual signature is:

```go
func applyErrorPolicy(
    ctx context.Context, args RunArgs, acq *acquisition,
    errorClass string, payload map[string]any, tx persistence.Tx,
) (postCommitFn, error)
```

It takes `*acquisition`, not `cand` / `nodeDef`.

2. Read `code:runtime/runner_acquire_claims.go::acquireClaim`. Today the `producer.Open` failure path around line 103-105 returns `openResult` enum values from inside the acquisition tx; this happens BEFORE `*acquisition` is fully built in `tryAcquire`. Direct routing through `applyErrorPolicy` from inside `acquireClaim` is not feasible (no `acq` yet).

3. The cleanest landing point is `tryAcquire` in `runtime/runner_acquire.go`, AFTER it receives the failure signal from `acquireOneLock` (which propagates from `acquireClaim`). `tryAcquire` already has the partial `acquisition` it's been building.

4. Implementation steps:
   - In `acquireClaim`: when `producer.Open` returns a `*peer.ProducerCallError`, extract the `ErrorClass` from it and return a new sentinel + the error class up to `acquireOneLock`. Re-use the existing `openResult` enum if it has an "errored" variant, or add one.
   - In `acquireOneLock`: propagate the error class up to `tryAcquire`.
   - In `tryAcquire`: when the acquisition signal is "errored with class X", build the partial `acquisition` (matching the pattern at the existing `openResultUnavailable` handling around lines 504-526) and call `applyErrorPolicy(ctx, args, partialAcq, errorClass, payload, tx)`. Treat the return as a post-commit hook same as the existing acquire-failure path.

5. The exact code shape depends on the existing `openResult` enum; read it first via `rg 'type openResult|openResult\w+\s+=' runtime/`. The plan deliberately doesn't pre-write the exact diff because the existing acquire-failure path's shape determines the minimum disturbance.

6. Apply the analogous pattern at the `Commit`/`Abandon`/`Release` call sites — those are typically in `runtime/auto_terminal_chain.go` and the supervisor's terminal handling. Find each, identify the surrounding `*acquisition` (or build one), and route through `applyErrorPolicy`.

7. Build and test: `go build ./... && go test ./runtime/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... -count=1`.

---

## Pass 5: Control-api wiring — rimsky.yml field, AppDeps, template field, lifecyclePeersForSpec, instance creation plumbing

**Goal:** Add `late_bind_service_proxies` to `RimskyConfig`. Add `LateBindServiceProxies` to `AppDeps` + wire from config. Add `LateBindServices` field to `TemplateSpec` (in canonical bytes). Extend `lifecyclePeersForSpec` to include the late-bind proxy when templates declare `late_bind_services`. Plumb `service_bindings` and `created_by_api_key_id` through `createInstanceRequest` → `provisionArgs` → `InstanceCreateInput`. Extend `InstancePayload` + `dispatchInstanceEvent` to forward both fields. Update the instance-create call site to populate the payload.
**Scope:** Tasks 27–33
**End state:** working
**Verification:** `cd foundation && go test ./... -count=1 && cd .. && go test ./control/controlapi/... ./graph/... -count=1`

### Task 27: Add `LateBindServices` field to `TemplateSpec` (canonical-spec bytes)

**Files:** `foundation/spec/template.go` (or wherever `TemplateSpec` is declared — verify via `rg 'type TemplateSpec'`)

**Steps:**

1. Read the current `TemplateSpec` struct. Note the JCS canonicalization is pinned in `graph/template/canonical/jcs.go`; adding a field makes it participate in the canonical hash (preserving `concept:template`'s content-addressing invariant).

2. Add a new field at the top-level (placement matches the file's existing convention for top-level fields like `Nodes` / `Graphs`):

```go
// LateBindServices declares service names whose registration-time
// existence and schema checks are deferred to dispatch. Names in
// this list bypass the discovery-cache check and the
// expected_attributes_schema cross-check during template
// registration. At dispatch, the spawned binary's Capabilities
// provides the schema; the proxy validates resolved attribute
// values against it; mismatch → contract_mismatch error.
//
// Stored inside the canonical spec bytes — changes participate
// in the template hash (concept:template's content-addressing
// invariant is preserved).
LateBindServices []string `json:"late_bind_services,omitempty" yaml:"late_bind_services,omitempty"`
```

3. Build the foundation module: `cd foundation && go build ./... && cd ..`.

**Verification:** `cd foundation && go build ./... && cd ..`.

### Task 28: Add `late_bind_service_proxies` to `RimskyConfig`

**Files:** `control/config/stores.go` (or wherever `RimskyConfig` is declared — confirm via `rg 'type RimskyConfig'`)

**Steps:**

1. Find `RimskyConfig`. Add a new top-level field:

```go
// LateBindServiceProxies maps protocol name → proxy service name.
// e.g., {"executor": "host-agent-proxy", "claim_producer": "host-agent-proxy"}.
// Consumed by LateBindResolver (executor) and *locks.Registry.GetWithContext
// (claim-producer) for late-bind dispatch resolution; consumed by
// lifecyclePeersForSpec for late-bind-proxy fan-out subscription.
LateBindServiceProxies map[string]string `yaml:"late_bind_service_proxies"`
```

2. Build: `go build ./control/config/...`.

**Verification:** `go build ./control/config/...`.

### Task 29: Add `LateBindServiceProxies` to `AppDeps` + wire from rimsky.yml

**Files:** `control/controlapi/app.go`, `control/config/controlapi.go`

**Steps:**

1. Find `AppDeps` in `control/controlapi/app.go`. Add:

```go
// LateBindServiceProxies maps protocol → proxy service name. Populated
// from rimsky.yml's late_bind_service_proxies by StartControlAPI.
// Consumed by lifecyclePeersForSpec to know which proxy peer to add
// to the fan-out when a template declares late_bind_services.
LateBindServiceProxies map[string]string
```

2. Find `StartControlAPI` in `control/config/controlapi.go`. Where `AppDeps` is constructed, plumb the field through:

```go
deps := &controlapi.AppDeps{
    // ... existing fields
    LateBindServiceProxies: cfg.LateBindServiceProxies,
}
```

3. Build: `go build ./control/...`.

**Verification:** `go build ./control/...`.

### Task 30: Extend `lifecyclePeersForSpec` (exported as `LifecyclePeersForSpec`) to include late-bind proxy

**Files:** `control/controlapi/lifecycle.go`

**Steps:**

1. Read the current `lifecyclePeersForSpec(deps AppDeps, spec node.TemplateSpec) []string`. Today it discards `deps` and delegates to `peersReferencedBySpec(spec)`.

2. **Export the function** by renaming to `LifecyclePeersForSpec` (capital L) — the supervisor entrypoint in Pass 7 needs to call it from outside the package.

3. Extend the function body to consume `deps.LateBindServiceProxies`:

```go
func LifecyclePeersForSpec(deps AppDeps, spec node.TemplateSpec) []string {
    peers := peersReferencedBySpec(spec)
    if len(spec.LateBindServices) > 0 {
        seen := make(map[string]struct{}, len(peers))
        for _, p := range peers {
            seen[p] = struct{}{}
        }
        for _, proxyName := range deps.LateBindServiceProxies {
            if proxyName == "" {
                continue
            }
            if _, exists := seen[proxyName]; exists {
                continue
            }
            peers = append(peers, proxyName)
            seen[proxyName] = struct{}{}
        }
    }
    return peers
}
```

4. Find every caller of the old `lifecyclePeersForSpec` (lowercase) and rename to `LifecyclePeersForSpec`. Confirm the callers are `FanOutInstanceEvent` and (in Pass 6) the new `FanOutRunScopeEvent`. **`FanOutTemplateEvent` should NOT call the extended version** — the proxy doesn't care about template events, so template fan-out should use the non-extended `peersReferencedBySpec(spec)` directly. If `FanOutTemplateEvent` currently calls `lifecyclePeersForSpec`, switch it to call `peersReferencedBySpec` directly so the late-bind extension stays scoped to instance + run-scope fan-out.

5. Build: `go build ./control/controlapi/...`.

**Verification:** `go build ./control/controlapi/...`.

### Task 31: Plumb `service_bindings` + `created_by_api_key_id` through `createInstanceRequest`

**Files:** `control/controlapi/instances.go`

**Steps:**

1. Read `createInstanceRequest`. Add:

```go
// ServiceBindings carries the per-instance late-bound service catalog.
// Opaque JSON; shape per spec (`{<name>: {"path": "<binary-path>"}}`).
ServiceBindings json.RawMessage `json:"service_bindings,omitempty"`
```

2. Find `provisionArgs` (around line 763). Add:

```go
ServiceBindings   json.RawMessage
CreatedByAPIKeyID *shared.UUID
```

3. Find `provisionInstanceTx` (around line 792). Thread both new fields into the `persistence.InstanceCreateInput` it builds.

4. Find `handleCreateInstance` in `code:control/controlapi/instances.go`. The request variable is named `req` (not `r`). **Add `ident, _ := IdentityFromContextOK(req.Context())` near handler entry** (right after the JSON-decode of the request body, BEFORE the `deps.Persist.Transaction(...)` closure opens around line 300) — `ident` must be in scope at BOTH the `provisionArgs{...}` construction site AND the `FanOutInstanceEvent` payload-population site that Task 33 lands at line 415. Then populate both fields on `provisionArgs`:

```go
// At handler entry (after JSON decode, before tx opens):
ident, _ := IdentityFromContextOK(req.Context())

// Later, where provisionArgs is built:
args := provisionArgs{
    // ... existing fields
    ServiceBindings:   body.ServiceBindings,
    CreatedByAPIKeyID: ident.KeyID,  // *shared.UUID, nil for anonymous-mode
}
```

(Matches the pattern at `code:control/controlapi/auth_handlers.go:118-129`. The handler-scope `ident` variable is reused by Task 33 to populate the lifecycle payload.)

5. Build: `go build ./control/controlapi/...`.

**Verification:** `go build ./control/controlapi/...`.

### Task 32: Extend `InstancePayload` + `dispatchInstanceEvent` to forward the new fields

**Files:** `control/controlapi/lifecycle.go`

**Steps:**

1. Read `InstancePayload` (around line 75 — the Go struct dispatched to subscribers). Add:

```go
// ServiceBindings carries the per-instance late-bound service catalog
// (opaque JSON). Empty when the instance has no late-bound services.
ServiceBindings json.RawMessage

// OwnerAPIKeyID is the api-key that authenticated the create request.
// Nil for instances created under anonymous-mode.
OwnerAPIKeyID *shared.UUID
```

2. Read `dispatchInstanceEvent` (around line 307). It builds `locks.OnInstanceCreatedRequest{...}` (NOT a genv1 proto type — the alias from Task 6) when the event is `EventInstanceCreated`. Update that branch to set the two new fields on the Go request struct:

```go
case EventInstanceCreated:
    req := locks.OnInstanceCreatedRequest{
        InstanceID:      payload.InstanceID,
        TemplateHash:    payload.TemplateHash,
        InstanceKey:     payload.InstanceKey,
        Params:          payload.Params,
        ServiceBindings: payload.ServiceBindings,
        OwnerAPIKeyID:   uuidString(payload.OwnerAPIKeyID),
    }
    // ... existing dispatch
```

(The Go `OnInstanceCreatedRequest.OwnerAPIKeyID` is `string` per the proto's `string owner_api_key_id` choice — define a small `uuidString(*shared.UUID) string` helper that returns `""` for nil and `u.String()` otherwise.)

3. Build: `go build ./control/controlapi/...`.

**Verification:** `go build ./control/controlapi/...`.

### Task 33: Populate `InstancePayload` from the freshly-created instance row

**Files:** `control/controlapi/instances.go`

**Steps:**

1. Find the create call site that builds `InstancePayload` and calls `FanOutInstanceEvent` (around line 415). At that scope, the only available value is `respOut createInstanceResponse` (`code:control/controlapi/instances.go:125`) — there is NO `InstanceRow` in scope (the row lives inside `provisionInstanceTx` as a local `inst` and is not returned).

2. Pick one of these approaches:

   (a) **Extend `createInstanceResponse`** to carry the two new fields back from `provisionInstanceTx`:

   ```go
   type createInstanceResponse struct {
       // ... existing fields
       ServiceBindings json.RawMessage `json:"service_bindings,omitempty"`
       OwnerAPIKeyID   *shared.UUID    `json:"created_by_api_key_id,omitempty"`
   }
   ```

   Update `provisionInstanceTx` to populate them from the local `inst persistence.InstanceRow` (value, not pointer — `Instances().Create` returns the row by value per `code:foundation/persistence/instances.go#68`) before returning. Then at the handler scope, populate the payload from `respOut.ServiceBindings` and `respOut.OwnerAPIKeyID`.

   (b) **Populate the payload from values already known at the handler scope** — `body.ServiceBindings` (the unmarshalled request body) and `ident.KeyID` (destructured from `IdentityFromContextOK(r.Context())` at handler entry; see Task 31's plumbing). No `createInstanceResponse` extension needed.

   **Pick (b)** — the values are reliable at handler scope (the request body's `ServiceBindings` is what got stored; `ident.KeyID` is what was written to the column). It avoids extending the response surface. The payload populates as:

```go
payload := lifecycle.InstancePayload{
    // ... existing fields
    ServiceBindings: body.ServiceBindings,
    OwnerAPIKeyID:   ident.KeyID,  // *shared.UUID, nil for anonymous
}
```

(Confirm `body.ServiceBindings` and `ident` are the variable names actually in scope at the FanOut site by reading the surrounding code — if they're named differently, adapt.)

3. Build and test: `cd foundation && go test ./... -count=1 && cd .. && go test ./control/controlapi/... -count=1`.

**Verification:** `cd foundation && go test ./... -count=1 && cd .. && go test ./control/controlapi/... ./graph/... -count=1`.

---

## Pass 6: Main RunScope close + control-api `FanOutRunScopeEvent`

**Goal:** Add a control-api-side `FanOutRunScopeEvent` helper that mirrors the existing `FanOutInstanceEvent` shape. Insert main-scope close + `OnRunScopeTerminal` fan-out at `instance_terminator.go::tick`'s polling loop (sub-tick lag) and at the explicit-DELETE path in `instances.go` (synchronous). Each insertion site reads `inst.MainRunScopeID` directly (no `FindMainForInstance` lookup — the column is already projected).
**Scope:** Tasks 34–35
**End state:** working
**Verification:** `go build ./... && go test ./control/controlapi/... -count=1`

### Task 34: Add control-api `FanOutRunScopeEvent` helper

**Files:** `control/controlapi/lifecycle.go`

**Steps:**

1. Read the existing `FanOutInstanceEvent` helper (around line 211) as the template. Its idempotency pattern uses `deps.Persist.LifecycleIdempotency().Get(...)` → `Upsert(...)` (singular accessor; not `LifecycleIdempotencies`).

2. Add a sibling helper:

```go
// FanOutRunScopeEvent fires OnRunScopeTerminal to every lifecycle
// subscriber that matches the late-bind-extended peer filter
// (LifecyclePeersForSpec) for the instance's template. Synchronous,
// DB-idempotent via rimsky_lifecycle_idempotencies with
// scope_kind="run_scope" and state="run_scope_terminal".
//
// Per spec 2026-05-24-host-agent-and-proxy-design.md §"Reap" and
// §"Firing sites for OnRunScopeTerminal". Called by control-api's
// instance_terminator.tick + DELETE path (this file's Task 35) for
// main-scope close. Supervisor has a parallel runtime/-side helper
// (Pass 7) for sub-graph + fanout-partition scopes.
func FanOutRunScopeEvent(
    ctx context.Context,
    deps AppDeps,
    tplSpec node.TemplateSpec,
    runScopeID shared.UUID,
    terminalReason string,
    tx persistence.Tx,
) ([]string, map[string]error, error) {
    peers := LifecyclePeersForSpec(deps, tplSpec)
    peerNames := make([]string, 0, len(peers))
    perPeerErr := make(map[string]error)
    scopeID := runScopeID.String()

    for _, name := range peers {
        peerNames = append(peerNames, name)

        // Idempotency: skip if already delivered.
        existing, err := deps.Persist.LifecycleIdempotency().Get(
            ctx, name,
            persistence.LifecycleIdempotencyScopeRunScope,
            scopeID, tx,
        )
        if err != nil {
            return peerNames, perPeerErr, fmt.Errorf("FanOutRunScopeEvent: lifecycle row lookup for %q: %w", name, err)
        }
        if existing != nil && existing.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
            continue
        }

        sub, ok := deps.LifecycleSubs.Get(name)
        if !ok {
            continue
        }

        req := locks.OnRunScopeTerminalRequest{
            RunScopeID:     runScopeID.String(),
            TerminalReason: terminalReason,
        }
        if err := sub.OnRunScopeTerminal(ctx, req); err != nil {
            perPeerErr[name] = err
            // Match the existing FanOutInstanceEvent convention:
            // continue to next peer on subscriber error, surface via perPeerErr.
            continue
        }

        if err := deps.Persist.LifecycleIdempotency().Upsert(ctx,
            persistence.LifecycleIdempotencyRow{
                StoreRegistrationName: name,
                ScopeKind: persistence.LifecycleIdempotencyScopeRunScope,
                ScopeID:   scopeID,
                State:     persistence.LifecycleIdempotencyStateRunScopeTerminal,
            }, tx,
        ); err != nil {
            return peerNames, perPeerErr, fmt.Errorf("FanOutRunScopeEvent: upsert lifecycle row %q: %w", name, err)
        }
    }
    return peerNames, perPeerErr, nil
}
```

(Field name `StoreRegistrationName` verified against `code:foundation/persistence/lifecycle_idempotency.go::LifecycleIdempotencyRow`. The `withOptionalTx` helper at `code:control/controlapi/lifecycle.go::withOptionalTx` has signature `withOptionalTx(ctx, db, tx, func(ctx context.Context, tx persistence.Tx) error)` — both `ctx` and `tx` are passed into the callback. Rewrite the body above to wrap every `LifecycleIdempotency().Get`/`Upsert` call in `withOptionalTx` for nil-tx → open-fresh-tx semantics matching the sibling helper. The snippet shown above calls the persistence methods directly with `tx`; replace each with a `withOptionalTx(ctx, deps.Persist, tx, func(ctx context.Context, tx persistence.Tx) error { /* Get or Upsert here */ })` wrap.)

3. Build: `go build ./control/controlapi/...`.

**Verification:** `go build ./control/controlapi/...`.

### Task 35: Insert main-scope close + `FanOutRunScopeEvent` at both control-api close sites

**Files:** `control/controlapi/instance_terminator.go`, `control/controlapi/instances.go`

**Steps:**

1. Read `instance_terminator.go::tick`. Find the existing `FanOutInstanceEvent(EventInstanceTerminated)` call (around line 164). `terminatedRow` is in scope; it's a `persistence.InstanceRow` with `MainRunScopeID` already projected (no `FindMainForInstance` lookup needed).

2. The variable in scope is `inst persistence.InstanceRow` (value, not pointer; NOT `terminatedRow`) and `tpl *persistence.TemplateRow` (already loaded at line 137 in the existing tick code — DO NOT add a second `Templates().GetByHash` call; reuse the existing `tpl` variable). Insert *immediately before* the existing `FanOutInstanceEvent` call:

```go
// Close the instance's main run-scope and fire OnRunScopeTerminal
// before OnInstanceTerminated, so the host-agent-proxy can reap any
// spawned processes scoped to this main run-scope. Sub-tick lag
// (vs the row's terminal mark in graph/frame/engine.go) is acceptable
// for v1.
if inst.MainRunScopeID != (shared.UUID{}) {
    if err := deps.Persist.RunScopes().Close(ctx, nil, inst.MainRunScopeID); err != nil {
        slog.Warn("close main run-scope failed",
            "instance", inst.ID, "error", err)
    } else {
        // tpl is already loaded above (at the existing line 137 lookup) —
        // reuse it; no second Templates().GetByHash call needed.
        _, _, _ = FanOutRunScopeEvent(ctx, deps, tpl.Spec,
            inst.MainRunScopeID, "instance_terminated", nil)
    }
}
```

(`shared.UUID` is an alias for `github.com/google/uuid.UUID`, which has no `IsZero()` method — use `!= (shared.UUID{})` per repo convention. Verify the actual variable name for the instance row in tick — if it differs from `inst`, adapt; the requirement is that this snippet uses the existing in-scope row + template variables rather than re-fetching.)

3. Read `instances.go` around line 614 — the explicit-DELETE handler `handleDeleteInstance`. Find the equivalent `FanOutInstanceEvent` call. Insert the same snippet shape *immediately before* it, with one difference: in the DELETE path the in-scope variable is `inst *persistence.InstanceRow` (a pointer; `resolveInstance` returns `*persistence.InstanceRow`). The snippet's `inst.MainRunScopeID != (shared.UUID{})` and field access work unchanged because Go auto-dereferences for field access. The DELETE path runs synchronously in the request context; use the request variable's `Context()` and use the surrounding transaction if one is available.

4. The `terminalReason` differs per call site: use `"instance_terminated"` for the tick path and `"instance_deleted"` for the DELETE path. (No new column needed on `InstanceRow`; the strings are spec-internal and never persisted.)

5. Build and test: `go test ./control/controlapi/... -count=1`.

**Verification:** `go build ./... && go test ./control/controlapi/... -count=1`.

---

## Pass 7: Supervisor outbound lifecycle + admit-list wiring + Registry production wiring + validator bypass

**Goal:** Grow `StartSupervisor` to dial outbound `LifecycleSubscriber` subscribers (via the existing `dialLifecycleSubscribers` helper). Add a `LifecyclePeersForSpec` function-pointer field on `SupervisorConfig` + wire from the supervisor entrypoint. Add a supervisor-side `FanOutRunScopeEvent` helper at `runtime/lifecycle_fanout.go`. Call the helper at the two supervisor close sites (`subgraph_dispatch.go`, `auto_terminal_chain.go`) with the two-lookup `tplSpec` resolution chain. Wire `SelectCandidates` admit-list extension with real config values. Wire `NewRegistry` options + the bindings-lookup hook at `dialRemoteStores`. Extend `validatorHooksFor` to bypass late-bind names.
**Scope:** Tasks 36–40
**End state:** working
**Verification:** `go build ./... && go test ./runtime/... ./foundation/persistence/postgres/... ./control/controlapi/... -count=1`

### Task 36: Add `SupervisorConfig` fields + wire from `StartSupervisor` + supervisor entrypoint

**Files:** `control/config/supervisor.go`, `control/config/stores.go`, `cmd/rimsky-supervisor/main.go`

**Steps:**

1. Read `code:control/config/supervisor.go::SupervisorConfig`. Find the existing `ExpectedAttributesSchemaFor` function-pointer (used as model). Add three new fields:

```go
// LifecyclePeersForSpec returns the lifecycle peer names that should
// receive instance- and run-scope-keyed events for a given template
// spec. Production wiring supplies a closure that calls
// controlapi.LifecyclePeersForSpec with the rimsky.yml
// late_bind_service_proxies map baked in. Avoids runtime/ → control/
// import (denied by .golangci.yml's runtime-purity rule).
//
// Per spec 2026-05-24-host-agent-and-proxy-design.md.
LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string

// LifecycleSubs is the supervisor's outbound LifecycleSubscriber
// registry. Populated by StartSupervisor via dialLifecycleSubscribers
// (the same helper control-api uses). Used to fire OnRunScopeTerminal
// for sub-graph and fanout-partition scope closes.
LifecycleSubs *locks.LifecycleRegistry

// Executors mirrors the rimsky.yml executors: block. The supervisor
// needs it for DialLifecycleSubscribers (which walks the union of
// claim_producers: + executors: looking for protocols: [..., lifecycle_subscriber]).
// Existing supervisor wiring already takes Stores but not Executors;
// adding this field is a prerequisite for the DialLifecycleSubscribers call.
Executors ExecutorsConfig

// LateBindServiceProxies passes the rimsky.yml late-bind map through
// to the supervisor for use in SelectCandidatesRequest construction
// and Registry option wiring.
LateBindServiceProxies map[string]string
```

2. Read `code:control/config/stores.go::dialLifecycleSubscribers` (line 558). It's currently unexported. Export it by renaming to `DialLifecycleSubscribers`. Find all in-package callers and update them.

3. In `cmd/rimsky-supervisor/main.go`, populate the new `Executors` field on `SupervisorConfig` from the parsed rimsky.yml (the same source already feeding the control-api binary). This is the prerequisite for step 4.

4. Read `StartSupervisor` in the same file or nearby. After the existing `dialRemoteStores` call, add:

```go
lifecycleSubs, err := DialLifecycleSubscribers(ctx, cfg.Stores, cfg.Executors)
if err != nil {
    return nil, fmt.Errorf("StartSupervisor: dial lifecycle subscribers: %w", err)
}
// Store on the handle (or on the runtime args plumbed downstream).
// Pass into runtime.NewRunArgs (or whatever the runtime-init function is).
```

5. In `code:cmd/rimsky-supervisor/main.go`, where `SupervisorConfig` is built:

```go
// Closure that invokes controlapi.LifecyclePeersForSpec with the
// rimsky.yml late_bind_service_proxies baked in. Lives in main.go
// (control/ layer) so the supervisor doesn't import control/
// from within the runtime/.
peersForSpec := func(tplSpec node.TemplateSpec) []string {
    return controlapi.LifecyclePeersForSpec(
        controlapi.AppDeps{LateBindServiceProxies: cfg.LateBindServiceProxies},
        tplSpec,
    )
}

supCfg := config.SupervisorConfig{
    // ... existing fields
    Executors:              cfg.Executors,  // from step 3 above
    LifecyclePeersForSpec:  peersForSpec,
    LateBindServiceProxies: cfg.LateBindServiceProxies,
}
```

6. Build: `go build ./...`.

**Verification:** `go build ./...`.

### Task 37: Add supervisor's `FanOutRunScopeEvent` helper + call at runtime close sites

**Files:** `runtime/lifecycle_fanout.go` (new), `runtime/subgraph_dispatch.go`, `runtime/auto_terminal_chain.go`

**Steps:**

1. Create `runtime/lifecycle_fanout.go`. Mirror the shape of control-api's `FanOutRunScopeEvent` (Task 34) but with explicit parameters in place of `AppDeps` (no `AppDeps` import — runtime cannot import control):

```go
package runtime

import (
    "context"
    "fmt"

    "github.com/fallguyconsulting/rimsky/foundation/locks"
    "github.com/fallguyconsulting/rimsky/foundation/persistence"
    "github.com/fallguyconsulting/rimsky/foundation/shared"
    "github.com/fallguyconsulting/rimsky/graph/node"
)

// FanOutRunScopeEvent fires OnRunScopeTerminal to subscribers matching
// the peer filter for the given template. Called by the supervisor's
// sub-graph and fanout-partition close sites synchronously after the
// close commits. Idempotency-protected via rimsky_lifecycle_idempotencies
// (scope_kind="run_scope", state="run_scope_terminal").
//
// Parallel to control-api's FanOutRunScopeEvent (which handles main
// scopes). Each layer has its own LifecycleRegistry; the two close
// disjoint scope kinds, so no double-fire risk.
//
// Per spec 2026-05-24-host-agent-and-proxy-design.md.
func FanOutRunScopeEvent(
    ctx context.Context,
    persist persistence.Tables,
    lifecycleSubs *locks.LifecycleRegistry,
    peersForSpec func(tplSpec node.TemplateSpec) []string,
    tplSpec node.TemplateSpec,
    runScopeID shared.UUID,
    terminalReason string,
    tx persistence.Tx,
) error {
    if lifecycleSubs == nil || peersForSpec == nil {
        return nil  // no-op if supervisor wasn't wired with lifecycle outbound
    }
    peers := peersForSpec(tplSpec)
    scopeID := runScopeID.String()

    for _, name := range peers {
        existing, err := persist.LifecycleIdempotency().Get(
            ctx, name,
            persistence.LifecycleIdempotencyScopeRunScope,
            scopeID, tx,
        )
        if err != nil {
            return fmt.Errorf("FanOutRunScopeEvent: lifecycle row lookup for %q: %w", name, err)
        }
        if existing != nil && existing.State == persistence.LifecycleIdempotencyStateRunScopeTerminal {
            continue
        }

        sub, ok := lifecycleSubs.Get(name)
        if !ok {
            continue
        }

        req := locks.OnRunScopeTerminalRequest{
            RunScopeID:     runScopeID.String(),
            TerminalReason: terminalReason,
        }
        if err := sub.OnRunScopeTerminal(ctx, req); err != nil {
            // Non-fatal at the per-peer level; log and continue.
            // (Mirror FanOutInstanceEvent's behavior; perPeerErr collection
            // can be added if downstream code wants to surface it.)
            continue
        }

        if err := persist.LifecycleIdempotency().Upsert(ctx,
            persistence.LifecycleIdempotencyRow{
                StoreRegistrationName: name,
                ScopeKind: persistence.LifecycleIdempotencyScopeRunScope,
                ScopeID:   scopeID,
                State:     persistence.LifecycleIdempotencyStateRunScopeTerminal,
            }, tx,
        ); err != nil {
            return fmt.Errorf("FanOutRunScopeEvent: upsert lifecycle row %q: %w", name, err)
        }
    }
    return nil
}
```

2. Read `runtime/subgraph_dispatch.go`. The close site at line 260 is inside `CarryExitWriteback`, which takes `args PropagationArgs` (NOT `RunArgs`). `PropagationArgs` (`code:runtime/state_propagation.go:98`) currently has only `RunTree`, `RunScopes`, `ClaimHandles`, `Logger` — none of `Persist`, `LifecycleSubs`, `LifecyclePeersForSpec`. Two options:

   a. **Extend `PropagationArgs`** with `Persist persistence.Tables`, `LifecycleSubs *locks.LifecycleRegistry`, `LifecyclePeersForSpec func(node.TemplateSpec) []string`. Update every constructor (`rg 'PropagationArgs{' runtime/`) to populate them from the surrounding `RunArgs`. Then the close-site snippet works as written below.

   b. **Hoist the FanOut call up to the caller of `CarryExitWriteback`** (where `RunArgs` IS in scope). The caller already has `args.Persist` etc. Less invasive — extend the caller to inspect the close outcome and fire `FanOutRunScopeEvent` post-close.

   **Pick (a)** — it keeps the close + fan-out atomic at the close site (no risk of one without the other) and the `PropagationArgs` extension is the same shape as the `RunArgs` extension in step 4. Update every `PropagationArgs{...}` literal (find via `rg 'PropagationArgs{' runtime/`) to populate the three new fields from the surrounding `RunArgs` / `Config`.

   After (a) is wired, insert at the close site (using `args.RunScopes.Close(...)` — that's how the close is called via the existing field):

```go
// Resolve tplSpec via two-step lookup: instance → template.
inst, err := args.Persist.Instances().Get(ctx, exitScope.InstanceID, tx)
if err == nil {
    tpl, err := args.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
    if err == nil {
        _ = FanOutRunScopeEvent(ctx, args.Persist, args.LifecycleSubs,
            args.LifecyclePeersForSpec, tpl.Spec, exit.RunScopeID,
            "subgraph_exit", tx)
    }
}
```

3. Read `runtime/auto_terminal_chain.go`. Find the close call at line 158 (it uses a local `scopes` var alias for `args.Persist.RunScopes()`). Insert the same shape after the close, using `"fanout_partition_terminal"` as the terminal reason. Use the appropriate `args.*` accessors in this file — read the existing function's arg struct first; if it's also `PropagationArgs`, the extension from step 2 covers it.

4. Wire `LifecycleSubs` and `LifecyclePeersForSpec` into BOTH `runtime.Config` (`code:runtime/supervisor.go` around line 75) AND `RunArgs` (`code:runtime/runner.go` around line 101). The fields flow `runtime.Config → RunArgs` at the runner-init site (find via `rg 'RunArgs{' runtime/`). Plumb both from `StartSupervisor`'s newly-populated `cfg.LifecycleSubs` and `cfg.LifecyclePeersForSpec`. Then ensure those fields propagate further into `PropagationArgs` per step 2 (a) at every `PropagationArgs{...}` construction site.

5. Build and test: `go build ./... && go test ./runtime/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... -count=1`.

### Task 38: Wire `SelectCandidates` admit-list extension with real values

**Files:** Find `SelectCandidates(...)` callers via `rg 'SelectCandidates\(' --include='*.go'`. Likely `runtime/supervisor.go` and helpers.

**Steps:**

1. At each `Queue().SelectCandidates(ctx, tx, req)` caller, populate the new `LateBindExecutorProxy` and `LateBindClaimProducerProxy` fields on the `req` struct literal:

```go
req := persistence.SelectCandidatesRequest{
    // ... existing fields
    LateBindExecutorProxy:      sup.LateBindServiceProxies["executor"],
    LateBindClaimProducerProxy: sup.LateBindServiceProxies["claim_producer"],
}
```

2. Plumb `LateBindServiceProxies` from `SupervisorConfig` through to wherever the request is built. Existing flow for `AcceptedExecutors`/`AcceptedStores` is the model.

3. Build and test: `go build ./... && go test ./runtime/... ./foundation/persistence/postgres/... -count=1`.

**Verification:** `go build ./... && go test ./runtime/... ./foundation/persistence/postgres/... -count=1`.

### Task 39: Wire `NewRegistry` options at `dialRemoteStores`

**Files:** `control/config/stores.go`

**Steps:**

1. Read `code:control/config/stores.go::dialRemoteStores` (line 528). The function calls `locks.NewRegistry()` zero-arg.

2. Change the signature to accept the extra wiring inputs:

```go
func dialRemoteStores(
    ctx context.Context,
    cfg RemoteStoresConfig,
    persist persistence.Tables,
    lateBindServiceProxies map[string]string,
) (*locks.Registry, error) {
    // Bindings-lookup hook backed by the live persistence layer.
    // `shared.UUID` is an alias for `github.com/google/uuid.UUID`; use
    // uuid.Parse from the standard import (no shared.ParseUUID helper).
    lookupBindings := func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
        instID, err := uuid.Parse(instanceID)
        if err != nil {
            return nil, false, err
        }
        row, err := persist.Instances().Get(ctx, instID, nil)
        if err != nil {
            return nil, false, err
        }
        if row == nil || len(row.ServiceBindings) == 0 {
            return nil, false, nil
        }
        var bindings map[string]json.RawMessage
        if err := json.Unmarshal(row.ServiceBindings, &bindings); err != nil {
            return nil, false, err
        }
        return bindings, true, nil
    }
    reg := locks.NewRegistry(
        locks.WithLookupInstanceBindings(lookupBindings),
        locks.WithLateBindServiceProxies(lateBindServiceProxies),
    )
    // ... existing dial loop populating reg
    return reg, nil
}
```

3. Find every caller of `dialRemoteStores`: `rg 'dialRemoteStores\(' control/config/`. Update each to pass `persist` and `lateBindServiceProxies` from the surrounding scope (`StartSupervisor` has `cfg.LateBindServiceProxies` and the persistence handle in scope; `StartControlAPI` similarly).

4. Build: `go build ./control/config/... && go build ./...`.

**Verification:** `go build ./...`.

### Task 40: Extend `validatorHooksFor` to bypass late-bind names

**Files:** `control/controlapi/templates.go`

**Steps:**

1. Read `code:control/controlapi/templates.go::validatorHooksFor`. The function returns a struct (or sets of closures) carrying `ExecutorDeclared(name) bool` and `ExecutorExpectedAttributesSchema(name) ([]byte, bool)` hooks consumed by the template validator.

2. Modify the function (or the calling code in the registration handler — whichever is cleaner) to consult `spec.LateBindServices` before delegating to the existing discovery-cache-backed implementations:

```go
hooks := validatorHooksFor(deps, ...)  // existing call

// Wrap the existing hooks to short-circuit for late-bind names.
origExecutorDeclared := hooks.ExecutorDeclared
hooks.ExecutorDeclared = func(name string) bool {
    for _, ls := range spec.LateBindServices {
        if ls == name {
            return true  // bypass strict declared check
        }
    }
    return origExecutorDeclared(name)
}

origExecutorSchema := hooks.ExecutorExpectedAttributesSchema
hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
    for _, ls := range spec.LateBindServices {
        if ls == name {
            return nil, true  // "declared, no schema cross-check needed"
        }
    }
    return origExecutorSchema(name)
}
```

(The exact shape — wrap-in-place vs. modify-inside-function — depends on the existing hook API. Read the validator's consumption site to determine the cleaner edit.)

3. The same bypass applies analogously if there are `ClaimProducerDeclared`/`ClaimProducerExpectedAttributesSchema` hooks. Apply the same shape.

4. Build and test: `go test ./control/controlapi/... -count=1`.

**Verification:** `go build ./... && go test ./control/controlapi/... -count=1`.

---

## Pass 8: Proxy binary

**Goal:** Stand up `cmd/rimsky-host-agent-proxy/` — the supervisor-facing protocol handlers (Executor + ClaimProducer with full proxy logic; LifecycleSubscriber consumer-role; Publisher/Validation/DataProcessing UNIMPLEMENTED), the agent-facing `HostAgent.Connect` server, the spawn-lifecycle state machine, callback-URL rewriting, and the binding-cache (populated via OnInstanceCreated, with `GET /instances/{id}` fallback). Include unit tests.
**Scope:** Tasks 41–46
**End state:** working
**Verification:** `go build ./cmd/rimsky-host-agent-proxy/... && go test ./cmd/rimsky-host-agent-proxy/... -count=1`

### Task 41: Create proxy binary scaffold + config

**Files:** `cmd/rimsky-host-agent-proxy/main.go` (new), `cmd/rimsky-host-agent-proxy/config.go` (new)

**Steps:**

1. Model after `cmd/rimsky-supervisor/main.go` and the in-repo stub executor at `executors/stub/` (binary entrypoint under `executors/stub/cmd/`, gRPC `Executor` server in `executors/stub/stub.go`) for the binary structure (gRPC listener, signal-handled graceful shutdown, slog setup).

2. Create `config.go` with:

```go
package main

type Config struct {
    GRPCPort        int    // serves HostAgent.Connect AND the rimsky service protocols on the same port
    ControlAPIURL   string // for GET /instances/{id} cache-miss fallback
    ControlAPIToken string // api-key the proxy uses to call control-api as itself
    LogLevel        string
}

func LoadConfig() Config {
    // Env: RIMSKY_PROXY_GRPC_PORT (default 9090), RIMSKY_CONTROL_API_URL,
    // RIMSKY_CONTROL_API_TOKEN, RIMSKY_LOG_LEVEL.
}
```

3. Create `main.go`:

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "net"
    "os"
    "os/signal"
    "syscall"

    "google.golang.org/grpc"
    genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func main() {
    cfg := LoadConfig()
    slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
    slog.Info("rimsky-host-agent-proxy starting", "grpc_port", cfg.GRPCPort)

    state := newProxyState()  // Task 42
    grpcSrv := grpc.NewServer()

    // Agent-facing.
    genv1.RegisterHostAgentServer(grpcSrv, newAgentServer(state))  // Task 43

    // Supervisor-facing.
    genv1.RegisterExecutorServer(grpcSrv, newExecutorHandler(state, cfg))     // Task 44
    genv1.RegisterExecutorObservabilityServer(grpcSrv, newExecutorObsHandler())
    genv1.RegisterClaimProducerServer(grpcSrv, newClaimProducerHandler(state, cfg))  // Task 45
    genv1.RegisterClaimProducerObservabilityServer(grpcSrv, newClaimProducerObsHandler())
    genv1.RegisterLifecycleSubscriberServer(grpcSrv, newLifecycleHandler(state))  // Task 46

    // UNIMPLEMENTED registrations.
    genv1.RegisterPublisherServer(grpcSrv, newUnimplementedPublisher())
    genv1.RegisterValidationServer(grpcSrv, newUnimplementedValidation())
    genv1.RegisterDataProcessingServer(grpcSrv, newUnimplementedDataProcessing())

    lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
    if err != nil { slog.Error("listen", "error", err); os.Exit(1) }
    go func() {
        if err := grpcSrv.Serve(lis); err != nil { slog.Error("serve", "error", err) }
    }()

    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    <-sigs
    slog.Info("shutting down")
    grpcSrv.GracefulStop()
}
```

Leave constructor functions (`newAgentServer`, etc.) as stubs — Tasks 42–46 implement them.

4. Build: `go build ./cmd/rimsky-host-agent-proxy/...`.

**Verification:** `go build ./cmd/rimsky-host-agent-proxy/...`.

### Task 42: Implement the proxy state machine (agent connections + spawn registry + binding cache)

**Files:** `cmd/rimsky-host-agent-proxy/state.go` (new), `cmd/rimsky-host-agent-proxy/state_test.go` (new)

**Steps:**

1. Define the in-memory state shape:

```go
type proxyState struct {
    mu sync.RWMutex

    agents           map[string]*agentConnection           // api_key_id → connection
    spawns           map[string]*spawnState                // spawn_id → metadata
    runScopeBindings map[runScopeBindingKey]string         // (run_scope, name) → spawn_id
    instances        map[string]*instanceCacheEntry        // instance_id → cached binding catalog + owner + params
}

type agentConnection struct {
    apiKeyID             string
    agentLabel           string
    localCallbackBaseURL string

    sendCh        chan *genv1.ServerFrame
    closeOnce     sync.Once
    closed        chan struct{}

    // Pending acks keyed by spawn_id / forward_id.
    pendingSpawnMu sync.Mutex
    pendingSpawn   map[string]chan *genv1.SpawnAck
    pendingReap    map[string]chan *genv1.Reaped
    pendingHTTP    map[string]chan *genv1.LocalHttpResponse
    pendingStreams map[string]chan *genv1.DispatchFrame  // keyed by (spawn_id, stream_id)
}

type spawnState struct {
    spawnID          string
    agentAPIKeyID    string
    runScopeID       string
    bindingName      string
    capabilities     map[string][]byte
    originalCallback string  // for callback-URL un-rewriting when LocalHttpForward arrives
}

type runScopeBindingKey struct {
    runScopeID  string
    bindingName string
}

type instanceCacheEntry struct {
    serviceBindings map[string]bindingSpec
    ownerAPIKeyID   string
    params          map[string]any
    lastUpdated     time.Time
}

type bindingSpec struct {
    Path string `json:"path"`
}
```

2. Implement mutator methods with proper locking:
   - `registerAgent(apiKeyID, label, localCallbackBaseURL) → (*agentConnection, displacedPrior bool)`
   - `dropAgent(apiKeyID)`
   - `recordSpawn(spawnID, agentAPIKeyID, runScopeID, bindingName, capabilities, originalCallback)`
   - `lookupSpawn(spawnID) → (*spawnState, bool)`
   - `lookupSpawnByRunScopeBinding(runScopeID, bindingName) → (spawnID, bool)`
   - `dropSpawnsForRunScope(runScopeID) → []spawnID`
   - `cacheInstance(instanceID, serviceBindings, ownerAPIKeyID, params)`
   - `lookupInstance(instanceID) → (*instanceCacheEntry, bool)`

3. Write `state_test.go` covering: duplicate-register displaces prior; spawn dedup by run-scope; cache hit/miss; concurrent access safety.

4. Build and test: `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

**Verification:** `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

### Task 43: Implement the agent-facing `HostAgent.Connect` handler

**Files:** `cmd/rimsky-host-agent-proxy/agent_server.go` (new), `cmd/rimsky-host-agent-proxy/agent_server_test.go` (new)

**Steps:**

1. Implement `Connect(stream genv1.HostAgent_ConnectServer) error`:
   - First `ClientFrame` MUST be `Register` (else close with `InvalidArgument`).
   - Call `state.registerAgent(...)` (displacing any prior connection — send `RegisterAck{displaced_prior: true}` on the new connection; close the old `sendCh`).
   - Launch a goroutine to read from the stream: route incoming frames by oneof — `SpawnAck`/`Reaped`/`DispatchFrame`/`LocalHttpForward`/`Heartbeat` each go to their handler.
   - Launch a goroutine to read from `agentConnection.sendCh` and write to the stream.
   - On context cancellation or stream error: `state.dropAgent(apiKeyID)`, close all spawn-state for that connection (notify pending dispatches with `host_agent_disconnected`).

2. Test with an in-process bidi gRPC stream:
   - Register + RegisterAck happy path
   - Heartbeat → HeartbeatAck round-trip
   - Duplicate Register displaces prior (assert `displaced_prior: true` flag set)
   - Spawn → SpawnAck round-trip with pending-channel correlation
   - Stream close → dropAgent + pending dispatches notified

3. Build and test: `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

**Verification:** `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

### Task 44: Implement `Executor` + `ExecutorObservability` handlers

**Files:** `cmd/rimsky-host-agent-proxy/executor_handler.go` (new), `cmd/rimsky-host-agent-proxy/executor_handler_test.go` (new)

**Steps:**

1. Implement `Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error`. Follow the spec's §"Proxy on a supervisor's Executor.Execute(req) call":
   - Extract `x-rimsky-service-name` from `metadata.FromIncomingContext(stream.Context())`; if missing → return a single Heartbeat-then-StreamClose{Error, error_class: "binding_not_found"} (use the existing executor.proto event shape).
   - `entry, ok := state.lookupInstance(req.InstanceId)`. On miss: GET `cfg.ControlAPIURL + "/instances/" + req.InstanceId` with the proxy's `cfg.ControlAPIToken`; populate cache; if still missing → error.
   - If `entry.OwnerAPIKeyID == ""` → `host_agent_not_connected`.
   - `agent, ok := state.lookupAgent(entry.OwnerAPIKeyID)`. Missing → `host_agent_not_connected`.
   - `binding, ok := entry.ServiceBindings[name]`. Missing → `binding_not_found`.
   - `spawnID, ok := state.lookupSpawnByRunScopeBinding(req.RunScopeId, name)`. If missing, send `Spawn{spawn_id: newID, binding, cwd: entry.params["cwd"], run_scope_id, expected_protocols: ["executor"]}` via agent.sendCh. Await `SpawnAck` on the pending-channel (configurable timeout, default 30s). On failure → `spawn_failed`. On success, record spawn (including `originalCallback = req.CallbackUrl`).
   - Rewrite `req.CallbackUrl`: parse, replace host:port with the agent's `localCallbackBaseURL` (parsed similarly), serialize back. Path stays the same.
   - Serialize the rewritten `req` to bytes, wrap as `DispatchFrame{spawn_id, protocol: "executor", payload: bytes, kind: DATA, stream_id: newID}`, send via agent.sendCh.
   - Open a pending-stream channel for the `(spawn_id, stream_id)` pair. Read responses; each response `DispatchFrame.payload` is the serialized inner `ExecuteEvent`; deserialize and `stream.Send(...)` to the supervisor.
   - On terminal `StreamClose` event in the inner stream, complete the outer call.
   - On agent disconnect mid-stream: send one synthetic `StreamClose{Error, error_class: "host_agent_disconnected"}` and return.

2. Implement `ExecutorObservability.Capabilities`: return a `CapabilitiesResponse` with empty `declared_events`, empty `expected_attributes_schema`. The proxy itself has no fixed capability schema — it serves whatever name the supervisor dispatches.

3. Tests: stub an in-process spawned binary (a small gRPC server implementing `Executor.Execute` that returns Heartbeat + StreamClose{Success}); stub an `agentConnection`; exercise happy path; exercise each error class (missing name header, missing instance cache, owner-empty, agent missing, binding missing, spawn timeout, disconnect mid-stream).

4. Build and test: `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

**Verification:** `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

### Task 45: Implement `ClaimProducer` + `ClaimProducerObservability` handlers

**Files:** `cmd/rimsky-host-agent-proxy/claim_producer_handler.go` (new), `cmd/rimsky-host-agent-proxy/claim_producer_handler_test.go` (new)

**Steps:**

1. Implement `Open`, `Commit`, `Abandon`, `Release`. Same flow as Executor.Execute but unary:
   - Extract `x-rimsky-service-name` from metadata.
   - Resolve owner → agent connection.
   - Resolve binding.
   - Spawn (lazy) via the agent with `expected_protocols: ["claim_producer"]`.
   - Forward the unary RPC via `DispatchFrame{kind: DATA, stream_id: newID}`. Await one response frame on the pending-stream channel.
   - On any error: return a gRPC error status with `google.rpc.ErrorInfo{Reason: <error_class>}` attached via `status.WithDetails`. Use `codes.Internal` for proxy-side failures, `codes.FailedPrecondition` for missing-binding-style errors.

2. Implement `ClaimProducerObservability.Capabilities`: return a synthetic envelope advertising `realized_write_semantics: [SYNC, STAGED_ASYNC, BLOCKING_ASYNC, READ_ONLY]` (all four). The proxy is transport — it advertises the full envelope; per-claim realized semantics come from each spawned producer's `Open` response.

3. Tests covering happy path + each error class.

4. Build and test: `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

**Verification:** `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

### Task 46: Implement `LifecycleSubscriber` consumer-role + UNIMPLEMENTED handlers + LocalHttpForward routing

**Files:** `cmd/rimsky-host-agent-proxy/lifecycle_handler.go` (new), `cmd/rimsky-host-agent-proxy/http_forward.go` (new), `cmd/rimsky-host-agent-proxy/unimplemented_handlers.go` (new)

**Steps:**

1. `lifecycle_handler.go`: implement the 7 methods:
   - `OnInstanceCreated`: parse `req.ServiceBindings` JSON into `map[string]bindingSpec`; parse `req.Params` JSON into `map[string]any`; call `state.cacheInstance(req.InstanceId, bindings, req.OwnerApiKeyId, params)`. Return `&genv1.LifecycleAck{}`.
   - `OnRunScopeTerminal`: call `state.dropSpawnsForRunScope(req.RunScopeId)` → list of spawn-ids; for each, look up the spawn's `agentConnection` and send `Reap{spawn_id, sigterm_grace_seconds: 30}` via agent.sendCh; await `Reaped`. Return `&genv1.LifecycleAck{}` after all reaps complete (or timeout).
   - `OnTemplateRegistered`/`OnTemplateDeployed`/`OnTemplateUndeployed`/`OnTemplateDeregistered`/`OnInstanceTerminated`: return `&genv1.LifecycleAck{}` immediately (no-op).

2. `http_forward.go`: implement the agent-side `LocalHttpForward` handler (called from the agent_server's frame-router):
   - The frame carries `{forward_id, method, url, body, headers, spawn_id}`. The URL is what the spawned process posted to (the rewritten callback URL).
   - Look up `state.lookupSpawn(spawn_id) → spawn.originalCallback`. POST `body` to `spawn.originalCallback` with `headers`. Read response.
   - Wrap response as `LocalHttpResponse{forward_id, status, body, headers}`; send via `agent.sendCh` to the originating agent connection.

3. `unimplemented_handlers.go`: stub implementations for `Publisher`, `Validation`, `DataProcessing` that return `codes.Unimplemented` from every method.

4. Tests covering:
   - LifecycleSubscriber consumer path (cache pop on OnInstanceCreated; reap fan-out on OnRunScopeTerminal)
   - LocalHttpForward → upstream POST → LocalHttpResponse round-trip

5. Build and test: `go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

**Verification:** `go build ./cmd/rimsky-host-agent-proxy/... && go test ./cmd/rimsky-host-agent-proxy/... -count=1`.

---

## Pass 9: Host agent binary + CLI subcommand wrapper

**Goal:** Stand up `cmd/rimsky-host-agent/` and an importable `runtime/hostagent` package containing the daemon's main loop. The daemon dials the proxy, exec()s local binaries, forwards dispatch + local HTTP, and reaps on signal. Wire `rimsky agent {start, status, stop}` as a subcommand on the main `rimsky` CLI binary (`cmd/rimsky/main.go`).
**Scope:** Tasks 47–50
**End state:** working
**Verification:** `go build ./cmd/rimsky-host-agent/... && go test ./cmd/rimsky-host-agent/... ./runtime/hostagent/... ./control/cli/... -count=1`

### Task 47: Create the importable `runtime/hostagent` package + binary entrypoint

**Files:** `runtime/hostagent/run.go` (new), `runtime/hostagent/config.go` (new), `cmd/rimsky-host-agent/main.go` (new)

**Steps:**

1. Create `runtime/hostagent/config.go` with:

```go
package hostagent

type Config struct {
    RimskyURL         string        // proxy URL to dial
    APIKey            string        // auth
    ListenAddr        string        // "" → OS-assigned ephemeral on 127.0.0.1
    AllowPaths        []string      // glob patterns; empty → permissive
    AgentLabel        string        // default hostname-pid
    LogLevel          string
    HeartbeatInterval time.Duration // default 10s
    ReapGracePeriod   time.Duration // default 30s
}

func LoadConfigFromEnv() Config { ... }
```

2. Create `runtime/hostagent/run.go` with the daemon main loop:

```go
package hostagent

import (
    "context"
    "fmt"
    "log/slog"
    "net"
    "net/http"
    // ... gRPC imports

    genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// Run is the host-agent's main loop. Used by both cmd/rimsky-host-agent/main.go
// (the standalone binary) and the `rimsky agent start` CLI subcommand
// (which dispatches to the same code path).
func Run(ctx context.Context, cfg Config) error {
    // 1. Bind local HTTP listener (Task 48).
    // 2. Dial proxy with retry/backoff.
    // 3. Open HostAgent.Connect bidi stream.
    // 4. Send Register with local_callback_base_url derived from the
    //    bound listener address.
    // 5. Heartbeat goroutine.
    // 6. Frame-dispatch goroutine (Tasks 48-49 implement handlers).
    // 7. On stream close: reconnect with backoff; SIGKILL orphaned
    //    children on cfg.ReapGracePeriod.
    // 8. On ctx cancellation: shut down cleanly.
    return nil  // placeholder
}
```

3. Create `cmd/rimsky-host-agent/main.go`:

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/fallguyconsulting/rimsky/runtime/hostagent"
)

func main() {
    slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
    cfg := hostagent.LoadConfigFromEnv()
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    if err := hostagent.Run(ctx, cfg); err != nil {
        slog.Error("hostagent.Run", "error", err)
        os.Exit(1)
    }
}
```

4. Build: `go build ./cmd/rimsky-host-agent/... && go build ./runtime/hostagent/...`.

**Verification:** `go build ./cmd/rimsky-host-agent/... && go build ./runtime/hostagent/...`.

### Task 48: Implement local HTTP listener + forward-to-proxy

**Files:** `runtime/hostagent/local_http.go` (new), `runtime/hostagent/local_http_test.go` (new)

**Steps:**

1. Bind a `net/http` server on `cfg.ListenAddr` (default `127.0.0.1:0`; get OS-assigned port back via `listener.Addr().(*net.TCPAddr).Port`).

2. Define a catch-all handler that:
   - Reads method, URL, headers, body.
   - Generates a fresh `forward_id` (UUID).
   - Wraps as `LocalHttpForward{forward_id, method, url, body, headers}` (spawn_id can be inferred from the URL path's `ack_id` by the proxy — leave empty here).
   - Sends through the proxy stream.
   - Awaits matching `LocalHttpResponse` on a pending-channels map keyed by `forward_id` (timeout 30s).
   - Writes the response back to the spawned process.

3. Add tests using `httptest.NewServer` for the upstream.

4. Build and test: `go test ./runtime/hostagent/... -count=1`.

**Verification:** `go test ./runtime/hostagent/... -count=1`.

### Task 49: Implement spawn / exec / dispatch / reap

**Files:** `runtime/hostagent/spawn.go` (new), `runtime/hostagent/dispatch.go` (new), `runtime/hostagent/spawn_test.go` (new)

**Steps:**

1. `spawn.go`: implement `handleSpawn(ctx, *genv1.Spawn) → *genv1.SpawnAck`:
   - Validate `binding.path` against `cfg.AllowPaths` (`filepath.Match` against each glob; if any matches → allow; if AllowPaths is non-empty and none match → reject with `spawn_failed`).
   - `exec.Command(binding.path)` with `cmd.Dir = spawn.Cwd` (if non-empty) and `cmd.Env = os.Environ()` (inherit agent's env). Start.
   - Wait for child gRPC port. **The agent allocates the port and tells the child via env var; the child does NOT communicate any port back.** Concrete contract:
     - Before `exec.Command`, the agent picks a free TCP port (e.g., open a listener on `:0`, read the OS-assigned port, close immediately — accept the race) and sets `RIMSKY_AGENT_PORT=<port>` in the child's env via `cmd.Env = append(os.Environ(), fmt.Sprintf("RIMSKY_AGENT_PORT=%d", port))`.
     - The spawned binary is expected to read `RIMSKY_AGENT_PORT` and bind its gRPC server to that port on localhost. (This is a plan-invented contract for v1; document it in the spawned-binary contract for tests + downstream consumers.)
     - The agent polls `127.0.0.1:<port>` (TCP-dial with a short retry loop) until ready or `spawn.ReadyTimeoutSeconds` elapses.
     - Test fixture binaries honor `RIMSKY_AGENT_PORT`.
   - Poll-dial the child's port (retry loop, total bounded by `spawn.ReadyTimeoutSeconds`).
   - For each protocol in `spawn.ExpectedProtocols`: dial via gRPC, call the protocol's `Capabilities()` RPC (use the gen-v1 client stubs), serialize the response with `proto.Marshal`, store in `SpawnAck.Capabilities` map.
   - Register the live child in an in-memory `liveChildren` map keyed by spawn-id (track PID, gRPC conn, exit chan).
   - Return `SpawnAck{spawn_id, status: SPAWN_STATUS_READY, capabilities}`. On any failure: `SpawnAck{spawn_id, status: SPAWN_STATUS_FAILED, error: {class, message}}`.

2. `dispatch.go`: implement `handleDispatchFrame(*genv1.DispatchFrame) → []*genv1.DispatchFrame`:
   - Look up live child for `frame.SpawnId`.
   - Deserialize `frame.Payload` based on `frame.Protocol`. Forward via the gRPC client. For server-streaming `Executor.Execute`, open a stream, send the request, read responses one by one and stream them back as `DispatchFrame{kind: DATA, payload: serialized response}`. On terminal `StreamClose` event, send one final frame and close the stream.

3. `spawn.go`: implement `handleReap(ctx, *genv1.Reap) → *genv1.Reaped`:
   - Lookup child; `cmd.Process.Signal(syscall.SIGTERM)`.
   - After `reap.SigtermGraceSeconds`, `cmd.Process.Kill()`.
   - Wait for `cmd.Wait()`. Return `Reaped{spawn_id, clean: <exit-code-based-bool>}`.

4. Tests: spawn a tiny in-test stub binary (compiled inline or via `go test`-time `go build`); exercise spawn + Capabilities handshake + dispatch + reap.

5. Build and test: `go test ./runtime/hostagent/... -count=1`.

**Verification:** `go test ./runtime/hostagent/... -count=1`.

### Task 50: Wire `rimsky agent {start, status, stop}` subcommand

**Files:** `control/cli/agent.go` (new), `cmd/rimsky/main.go`

**Steps:**

1. Read `code:cmd/rimsky/main.go`. The dispatch is a switch on `os.Args[1]` (verified at lines 25+ — `case "version"`, `case "template"`, etc.). Each case calls into `control/cli/` package functions (e.g., `cli.RunAuth(os.Args[2:])`).

2. Create `control/cli/agent.go`. **Return `int` (process exit code), not `error`**, to match the prevailing convention at `code:cmd/rimsky/main.go` where every existing subcommand handler returns `int` and the main dispatch is `os.Exit(cli.RunXxx(os.Args[2:]))`:

```go
package cli

import (
    "context"
    "flag"
    "fmt"
    "os"
    "os/exec"
    "syscall"

    "github.com/fallguyconsulting/rimsky/runtime/hostagent"
)

func RunAgent(args []string) int {
    if len(args) == 0 {
        fmt.Fprintln(os.Stderr, "rimsky agent: subcommand required (start|status|stop)")
        return 2
    }
    switch args[0] {
    case "start":
        return runAgentStart(args[1:])
    case "status":
        return runAgentStatus(args[1:])
    case "stop":
        return runAgentStop(args[1:])
    default:
        fmt.Fprintf(os.Stderr, "rimsky agent: unknown subcommand %q\n", args[0])
        return 2
    }
}

func runAgentStart(args []string) int {
    fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
    allowPaths := fs.String("allow-paths", "", "comma-separated glob patterns for binary path validation")
    listen := fs.String("listen", "", "agent local listener addr (default 127.0.0.1:0)")
    foreground := fs.Bool("foreground", false, "run in foreground (don't daemonize)")
    if err := fs.Parse(args); err != nil {
        fmt.Fprintln(os.Stderr, err)
        return 2
    }
    cfg := hostagent.LoadConfigFromEnv()
    if *allowPaths != "" {
        cfg.AllowPaths = strings.Split(*allowPaths, ",")
    }
    if *listen != "" {
        cfg.ListenAddr = *listen
    }
    if *foreground {
        ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
        defer cancel()
        if err := hostagent.Run(ctx, cfg); err != nil {
            fmt.Fprintln(os.Stderr, err)
            return 1
        }
        return 0
    }
    // Daemonize: fork self with --foreground; parent writes PID file and exits.
    self, err := os.Executable()
    if err != nil {
        fmt.Fprintln(os.Stderr, err); return 1
    }
    forkArgs := append([]string{"agent", "start", "--foreground"}, args...)
    cmd := exec.Command(self, forkArgs...)
    cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
    if err := cmd.Start(); err != nil {
        fmt.Fprintln(os.Stderr, err); return 1
    }
    pidPath := filepath.Join(os.Getenv("HOME"), ".rimsky", "agent.pid")
    if err := os.MkdirAll(filepath.Dir(pidPath), 0700); err != nil {
        fmt.Fprintln(os.Stderr, err); return 1
    }
    if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
        fmt.Fprintln(os.Stderr, err); return 1
    }
    return 0
}

func runAgentStatus(args []string) int {
    // Read ~/.rimsky/agent.pid; check if process is alive (os.FindProcess + signal 0).
    // Print "running (pid N)" or "not running". Return 0 either way (status is informational).
}

func runAgentStop(args []string) int {
    // Read PID file, SIGTERM the process, wait briefly, remove PID file. Return 0 on success, 1 on error.
}
```

3. In `code:cmd/rimsky/main.go`, add to the dispatch switch (place alphabetically near `case "admin":`):

```go
case "agent":
    os.Exit(cli.RunAgent(os.Args[2:]))
```

(Match the existing `os.Exit(cli.RunXxx(...))` pattern; do NOT use `if err != nil { os.Exit(1) }` — the return value IS the exit code.)

4. Build and test: `go build ./... && go test ./control/cli/... ./cmd/rimsky-host-agent/... ./runtime/hostagent/... -count=1`.

**Verification:** `go build ./... && go test ./control/cli/... ./cmd/rimsky-host-agent/... ./runtime/hostagent/... -count=1`.

---

## Pass 10: CLI run extensions + auth login + Context.api_key + aliases + tests + design docs

**Goal:** Add the additive `rimsky run` flags + auto-start agent. Implement `rimsky auth login`. Extend `Context` with `api_key`. Add CLI-side alias resolution. Write scenario tests under `test/scenarios/`. Apply all design-doc edits. (Declaring/shipping the proxy in the reference deployment is out of scope — those assets live in the rimsky-docs repo.)
**Scope:** Tasks 51–58
**End state:** working
**Verification:** `go build ./... && go test ./control/cli/... ./test/scenarios/... -count=1 && make lint`

### Task 51: Extend `rimsky run` with new flags + auto-start-agent

**Files:** `control/cli/run.go`

**Steps:**

1. Read `code:control/cli/run.go::RunRun`. Today parses positional `<file>` + `--params <json>` + `--instance-key` + `--tag` + `--keep`/`--no-keep`.

2. Add new flags:
   - `--template <name>`: mutually exclusive with positional `<file>`. Error if both given.
   - `--param k=v`: repeatable; build into `map[string]any`; merge with `--params` (later wins, with repeated `--param k=v` flags applied last in their declaration order).
   - `--service <name>=<path>`: repeatable; collect into `map[string]bindingSpec{Path: path}`.

3. Auto-start-agent flow: if `--service` is supplied AND the agent isn't running locally, call `runAgentStart` equivalent inline before submitting. **v1 connection-state contract**: the check is **PID-existence only** (read `~/.rimsky/agent.pid`, send signal 0 to confirm process is alive). If the PID exists and is live, assume the agent is connected to the proxy — do not start a second daemon. Document this in `--service`'s help text. The "agent running but disconnected" case (agent process alive, proxy connection dropped) is handled by the proxy returning `host_agent_not_connected` for any dispatch — the operator policy retries (the spec's default policy for that error class). A future enhancement could dial the agent's local status endpoint to confirm connection-state before submitting; out of scope for v1.

4. Resolve `--service` aliases (Task 53).

5. Include `service_bindings` in the `POST /instances` request body when supplied.

6. Add unit tests for flag parsing, the additive shape, and the mutually-exclusive `--template`/`<file>` check.

7. Build and test: `go test ./control/cli/... -count=1`.

**Verification:** `go test ./control/cli/... -count=1`.

### Task 52: Add `rimsky auth login` verb + extend `Context` struct

**Files:** `control/cli/config.go`, `control/cli/auth_login.go` (new), `control/cli/auth.go` (or wherever the `auth` subcommand dispatch lives)

**Steps:**

1. Read `code:control/cli/config.go::Context`. Add an `APIKey` field with `yaml` tag:

```go
type Context struct {
    Endpoint string `yaml:"endpoint"`
    APIKey   string `yaml:"api_key,omitempty"`
}
```

Existing config files without `api_key` continue to load — YAML naturally tolerates missing keys; `omitempty` handles the serialization side.

2. Create `control/cli/auth_login.go`. **Return `int`** to match the existing auth sub-handler convention. Read `code:control/cli/auth.go::RunAuth` first — every existing sub-handler takes `(ctx context.Context, args []string) int` (the `ctx` is threaded from `RunAuth`):

```go
package cli

func RunAuthLogin(ctx context.Context, args []string) int {
    // Read current context's Endpoint as default.
    // Prompt for URL (override default if user provides).
    // Prompt for api-key with stdin password-style (golang.org/x/term.ReadPassword).
    // Optional: hit GET <url>/auth/status to verify the key works; fail with clear error.
    // Write the key into the current Context.APIKey field; SaveConfig().
    // Print confirmation.
    // Return 0 on success, 1 on error.
}
```

3. Find the `auth` subcommand dispatch in `code:control/cli/auth.go::RunAuth`. Add a `case "login":` arm matching the sibling-handler shape:

```go
case "login":
    return RunAuthLogin(ctx, rest)
```

(Use whatever local variable holds the args remainder — likely `rest` per `RunAuth`'s convention.)

4. Build and test: `go test ./control/cli/... -count=1`.

**Verification:** `go test ./control/cli/... -count=1`.

### Task 53: Add CLI-side `--service` alias resolution

**Files:** `control/cli/aliases.go` (new), `control/cli/run.go`

**Steps:**

1. Create `control/cli/aliases.go`:

```go
type aliasFile struct {
    Aliases map[string]string `yaml:"aliases"`
}

func LoadServiceAliases() map[string]string {
    merged := map[string]string{}
    // Load ~/.rimsky/aliases.yml (global).
    if home, err := os.UserHomeDir(); err == nil {
        load(filepath.Join(home, ".rimsky", "aliases.yml"), merged)
    }
    // Load .rimsky/aliases.yml (project-local) — overlay; later wins.
    load(".rimsky/aliases.yml", merged)
    return merged
}

func load(path string, into map[string]string) {
    data, err := os.ReadFile(path)
    if err != nil { return }  // missing is fine
    var f aliasFile
    if err := yaml.Unmarshal(data, &f); err != nil { return }
    for k, v := range f.Aliases {
        into[k] = v
    }
}
```

2. In `control/cli/run.go`'s `--service` handling: when the flag value contains `=`, treat as `name=path`. When bare (no `=`), look up in the alias map. Error clearly if no alias matches:

```go
parts := strings.SplitN(svcValue, "=", 2)
var name, path string
if len(parts) == 2 {
    name, path = parts[0], parts[1]
} else {
    name = parts[0]
    aliases := LoadServiceAliases()
    p, ok := aliases[name]
    if !ok {
        return fmt.Errorf("--service %q: no alias defined; use --service %s=<path>", name, name)
    }
    path = p
}
```

3. Add tests covering: explicit `name=path`, bare name with alias, bare name without alias (error).

4. Build and test: `go test ./control/cli/... -count=1`.

**Verification:** `go test ./control/cli/... -count=1`.

### Task 54: End-to-end scenario test — late-bound executor happy path

**Files:** `test/scenarios/host_agent_late_bind_executor_test.go` (new)

**Steps:**

1. Read an existing scenario test for the pattern (e.g., `test/scenarios/acquire_unavailable_pass_test.go`). The pattern uses `graph/scenario.Start` (or similar) to spin up an in-process runtime.

2. Write a scenario:
   - Register a template that has a node with `executor: codegen` and `late_bind_services: [codegen]`.
   - Spin up an in-process proxy (call `proxy.NewServer(state, cfg)` directly — bypass the binary; bind to a free port).
   - Spin up an in-process host-agent (call `hostagent.Run(ctx, cfg)` with `cfg.RimskyURL` pointing at the proxy's port).
   - Stub binary: a small in-process gRPC server implementing `Executor.Execute` that returns Heartbeat + StreamClose{Success}. Bind to a free port; expect the agent to dial it.
   - For the stub-binary approach to work in tests, the agent's spawn path needs to support "the binary is already running on a known port" — modify the test setup to register the stub binary's address into the agent's `liveChildren` map directly, bypassing `exec.Command`. Or: build the stub binary at test setup time via `go build` and exec it. Pick whichever is more idiomatic for the rimsky test suite (check how the in-repo stub executor at `executors/stub` and its `stubtest` helpers are stood up in existing scenario / conformance tests).
   - Create an instance with `service_bindings: {"codegen": {"path": "<stub-binary-path>"}}` (or whatever the stub-injection mechanism above requires).
   - Trigger a frame; assert dispatch completes via the proxy + agent path, the stub binary handles the Execute, and the run reaches terminal.

3. Build and test: `go test ./test/scenarios/host_agent_late_bind_executor_test.go -count=1`.

**Verification:** `go test ./test/scenarios/host_agent_late_bind_executor_test.go -count=1`.

### Task 55: End-to-end scenario tests — failure modes

**Files:** `test/scenarios/host_agent_failure_modes_test.go` (new)

**Steps:**

Add tests covering each failure mode (use the same in-process scaffold as Task 54):

- **Agent not connected:** instance with bindings, no agent dialed; assert dispatch returns `StreamClose{Error, error_class: "host_agent_not_connected"}`.
- **Missing binding:** instance with empty `service_bindings`; assert `binding_not_found`.
- **Spawn fails:** binding path points at a non-existent binary; assert `spawn_failed`.
- **Agent disconnect mid-dispatch:** drop the agent's stream mid-Execute; assert `host_agent_disconnected`.
- **Proxy restart with reconnect:** restart the in-process proxy; agents reconnect; subsequent runs succeed.

Build and test: `go test ./test/scenarios/host_agent_failure_modes_test.go -count=1`.

**Verification:** `go test ./test/scenarios/host_agent_failure_modes_test.go -count=1`.

### Task 56: Create the two new concept files + add to `concepts.md` TOC

**Files:** `.ok-planner/design/concepts/host-agent.md` (new), `.ok-planner/design/concepts/host-agent-proxy.md` (new), `.ok-planner/design/concepts.md`

**Steps:**

1. Create `.ok-planner/design/concepts/host-agent.md` with the content specified in the spec §"Design changes — New concepts" (frontmatter + `## What it is` / `## Purpose` / `## Boundaries` / `## Invariants` / `## Aliases and historical names` / `## Open within this concept` / `## Notes` sections, all per spec). The spec's New-concepts bodies are written path-free per the concept self-containment rule — transcribe them as-is and do not add any file paths, Go symbols, or proto/RPC citations.

2. Create `.ok-planner/design/concepts/host-agent-proxy.md` similarly.

3. Read `.ok-planner/design/concepts.md`. Per `.ok-planner/CLAUDE.md`, `concepts.md` is regenerated by `execute-plan`'s post-pass design-doc step from each concept's lead sentence whenever a plan touches `concepts/` — so hand-edits here are a fallback the regen normalizes. Manually insert two new entries (alphabetically) so the TOC is correct even before regen:

```markdown
- `host-agent` — Long-running daemon on a user's dev machine, bundled into the `rimsky` CLI binary, that authenticates outbound to a host-agent-proxy and serves spawn / dispatch / reap / local-HTTP-forward requests against locally-running binaries.
- `host-agent-proxy` — Rimsky-stack `concept:service` implementing the multi-protocol composition pattern; presents the executor, claim-producer, and lifecycle-subscriber protocols on the supervisor-facing side and maintains agent connections on the dev-facing side via a long-lived bidi-stream protocol.
```

**Verification:** `test -f .ok-planner/design/concepts/host-agent.md && test -f .ok-planner/design/concepts/host-agent-proxy.md && grep -q '\`host-agent\`' .ok-planner/design/concepts.md && grep -q '\`host-agent-proxy\`' .ok-planner/design/concepts.md`.

### Task 57: Apply concept mutations + tension addendum + create new tensions

**Files:** `.ok-planner/design/concepts/{executor,claim-producer,service,instance,rimsky-yml,template,lifecycle-subscriber,supervisor,error-policy,conformance,rimsky}.md`, `.ok-planner/design/tensions/callback-hostname-split.md`, `.ok-planner/design/tensions/unreachable-service-row-stall.md` (new), `.ok-planner/design/tensions/anonymous-mode-locks-out-late-bind.md` (new), `.ok-planner/design/tensions/internal-service-auth-unspeced.md` (new)

**Steps:**

1. Apply each concept mutation from the spec §"Design changes — Mutations to existing concepts", writing the concept-file text **path-free** per the concept self-containment rule (see the Self-containment note at the head of that spec section). The spec's citation hints orient you to *where* each change lands; do not transcribe file paths, Go symbols, proto/RPC method names, or `table:`/`cfg:` citations into the concept bodies. Apply each:
   - `executor.md`: append the Notes entry.
   - `claim-producer.md`: append the analogous Notes entry.
   - `service.md`: append the Notes entry.
   - `instance.md`: Boundaries — add `service_bindings` and `created_by_api_key_id`; Invariants — add the two new entries; Notes — reference the spec.
   - `rimsky-yml.md`: Boundaries — add the per-protocol `late_bind_service_proxies` map; Invariants — late-bound names resolve via the named proxy; Notes — reference the spec.
   - `template.md`: Invariants — add `late_bind_services` (canonical-bytes-stored); Notes — reference the spec.
   - `lifecycle-subscriber.md`: Invariants — method count 6→7 + relaxed firing-site invariant; Boundaries — supervisor is now a firer; Notes — rationale + peer-filter extension note.
   - `supervisor.md`: Boundaries — three additions; Invariants — extended filter clause; Notes — reference the spec.
   - `error-policy.md`: append the Notes entry listing new error_class values.
   - `conformance.md`: append the Notes entry about proxy conformance.
   - `rimsky.md`: extend "What it is", Boundaries, Invariants per spec; add Notes entry.

2. Append the `tensions/callback-hostname-split.md` addendum (two-point text from spec).

3. Create the three new tension files (`unreachable-service-row-stall.md`, `anonymous-mode-locks-out-late-bind.md`, `internal-service-auth-unspeced.md`) with the exact verbatim content from the spec's §"New tensions to catalog".

**Verification:** `grep -l 'Per spec 2026-05-24-host-agent-and-proxy-design' .ok-planner/design/concepts/ | wc -l` should report ≥ 11 (one per mutated file). Plus: `grep -q 'Per spec 2026-05-24-host-agent-and-proxy-design' .ok-planner/design/tensions/callback-hostname-split.md && for f in unreachable-service-row-stall anonymous-mode-locks-out-late-bind internal-service-auth-unspeced; do test -f ".ok-planner/design/tensions/$f.md"; done`.

### Task 58: Final integration check — full test + lint

**Files:** None (verification-only task)

**Steps:**

1. `go test ./... -count=1`.
2. `cd foundation && go test ./... -count=1 && cd ..`.
3. `cd protocols && go build ./... && cd ..`.
4. `make lint`.

**Verification:** `go test ./... -count=1 && cd foundation && go test ./... -count=1 && cd .. && cd protocols && go build ./... && cd .. && make lint`.

---

## Manual checks after completion

These cannot be automated and should be run by the user after the implementation is complete:

- **Multi-process integration.** Against a running multi-process deployment that includes the `rimsky-host-agent-proxy` service (the reference-deployment assets live in the rimsky-docs repo, out of scope for this plan), run a `rimsky auth login`, `rimsky run --template <name> --service codegen=<bin>` cycle; confirm the spawned binary runs on the host machine and the workflow completes.
- **CLI ergonomics smoke test.** Run `rimsky auth login` interactively against a deployed rimsky, `rimsky agent start`, `rimsky run --template my-workflow --param cwd=. --service codegen=./my-binary`. Confirm the full UX feels right and error messages are clear (no agent → `host_agent_not_connected`; missing binary → `spawn_failed`; bad path → clear CLI-side error).
- **Concept-doc check.** Confirm the new `host-agent` and `host-agent-proxy` concept files read correctly and follow the self-containment rule (no code paths or citations in the bodies). Public-docs propagation is handled separately in the rimsky-docs repo.
- **Anonymous-mode interaction.** Confirm anonymous-mode users get a clear error when attempting to use `--service` (per the new tension `anonymous-mode-locks-out-late-bind`); the error should mention that the deployment must be authenticated to use late-bound services.
