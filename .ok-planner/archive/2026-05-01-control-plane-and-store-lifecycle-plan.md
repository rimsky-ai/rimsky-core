# Control Plane & Store Lifecycle v1 — Implementation Plan

**Goal:** Implement the control-plane v1 surface and store lifecycle event protocol per `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`.

**Architecture:** Templates become content-addressed (hash-as-id, separate tags table) with a four-state lifecycle (registered/deployed/undeployed/[absent]). Six new lifecycle RPCs are added to the store-service protocol; rimsky fires them synchronously with progress-preserving retry tracked in a new `rimsky_store_lifecycle` table. Stores config and executors config unify into `rimsky.yml` (`RIMSKY_CONFIG`); control-api gains an `ExecutorDeclared` validation hook. Pre-v1: drop-and-recreate the affected schema; rename env vars without compat shims.

**Tech Stack:** Go 1.22+, Postgres 15, gRPC + protobuf v3, chi HTTP router, jackc/pgx/v5, gopkg.in/yaml.v3, RFC 8785 JCS via `github.com/cyberphone/json-canonicalization`.

**Source spec:** `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`. All section references in this plan (§N.M) are to that spec unless noted otherwise.

**Working directory throughout:** `/Users/patrick/Documents/projects/research/verantel/submodules/rimsky`

**Pre-v1 posture:** project rules at `.claude/rules/rules.md` permit drop-and-recreate migrations and breaking changes. Any existing dev databases will be nuked by the migration in Task 3; this is expected.

---

## File map

### New files

- `core/canonical/jcs.go` — RFC 8785 canonicalization wrapper + `CanonicalSpecHash`.
- `core/canonical/jcs_test.go` — unit tests for hashing determinism.
- `core/migrations/003-template-registry-and-lifecycle.sql` — schema migration.
- `core/storage/postgres/store_lifecycle.go` — DAO for `rimsky_store_lifecycle`.
- `core/storage/postgres/template_tags.go` — DAO for `rimsky_template_tags`.
- `core/controlapi/lifecycle.go` — lifecycle-event fan-out helper.
- `core/controlapi/lifecycle_test.go` — fan-out unit tests.
- `core/controlapi/tags.go` — tag CRUD HTTP handlers.
- `core/controlapi/instance_terminator.go` — background worker that fires `OnInstanceTerminated`.

### Modified files (Go)

- `proto/v1/store_service.proto` — six new RPCs; two new `OpenRequest` fields.
- `core/store/interface.go` — six new methods on `Store` interface; default-no-op embeddable struct.
- `core/store/types.go` — `OpenRequest`/`ClaimSpec` gain `TemplateID` and `InstanceID`.
- `core/store/remote/client.go` — gRPC client methods for the six lifecycle events; populate envelope on Open.
- `core/store/storetest/fake.go` — implements the six methods; records calls.
- `core/storage/postgres/templates.go` — rewrite for hash-keyed schema.
- `core/storage/postgres/instances.go` — `template_hash TEXT` + `instance_key`; FOR UPDATE locking.
- `core/storage/interfaces.go` (and friends) — extend backend interface for new DAOs.
- `core/controlapi/templates.go` — wrap body shape, add lifecycle endpoints, integrate hashing.
- `core/controlapi/instances.go` — touch-up per §2.2.
- `core/controlapi/app.go` — wire new routes; spawn the terminator worker.
- `core/controlapi/app_util.go` (existing) — share resolver helper.
- `core/node/template_validator.go` — `RegistryHooks` gains `ExecutorDeclared`.
- `core/config/stores.go` — extend YAML parsing to include `executors:`; rename internal types as needed.
- `core/config/supervisor.go` — drop `executors:` from supervisor YAML; consume from rimsky.yml.
- `core/config/controlapi.go` — pass executor name set to control-api hooks.
- `core/cmd/rimsky-control-api/main.go` — `RIMSKY_STORES_CONFIG` → `RIMSKY_CONFIG`.
- `core/cmd/rimsky-supervisor/main.go` — same rename; remove executors from supervisor-config parsing.
- `core/cmd/rimsky-scheduler/main.go` — same rename.
- `core/scheduler/scheduler.go` (or `core/frame/`) — set `rimsky_instances.terminated_at = now()` when terminal predicate flips.
- `core/migrations/embed.go` — registers `003-template-registry-and-lifecycle.sql`.
- `go.mod`, `go.sum` — vendor `github.com/cyberphone/json-canonicalization`.
- `stores/postgres/main.go`, `stores/filesystem/main.go`, `stores/stub/main.go` — implement six methods.
- `core/cmd/rimsky-executor-conformance/` — six new no-op-passing checks.

### Modified files (deploy)

- `deploy/stores.yml` → renamed to `deploy/rimsky.yml`; merge in executors block.
- `deploy/supervisor-config.yml` — remove `executors:` block.
- `deploy/docker-compose.yml` — env var renames, mount path updates.
- `deploy/kubernetes/rimsky-chart/values.yaml`, `templates/configmap-stores.yaml` (rename to configmap-rimsky.yaml) — track unified config.

### Modified files (tests)

- `core/storage/postgres/postgres_test.go` — schema round-trip, FK refusals.
- `core/controlapi/templates_test.go` — state machine transitions; tag movement; idempotent re-register.
- `core/controlapi/admin_routes_test.go` — wire any test that seeds rimsky_templates with hash-shape ids.
- `core/frame/producer_test.go`, `core/attributes/store_test.go`, `core/queue/postgres/queue_test.go` — adjust seed SQL for the new schema (template_hash TEXT vs UUID, etc.).
- `test/scenarios/lifecycle/` (new directory) — end-to-end lifecycle scenario tests.
- `test/smoke/setup.go` — load via `RIMSKY_CONFIG`.

### Modified docs

- `docs/architecture.md`, `docs/protocol.md`, `docs/operator-guide.md`, `docs/store-author-guide.md`, `docs/glossary.md`, `docs/2026-04-26-control-layer.md`, `docs/2026-05-01-auth-and-multitenancy.md`, `CHANGELOG.md`, `CLAUDE.md`.

---

## Task 1 — Vendor JCS library and add canonical hashing helper

**Files:** `go.mod`, `go.sum`, `core/canonical/jcs.go`, `core/canonical/jcs_test.go`.

### Steps

1. Add the JCS library dependency:
   ```bash
   go get github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer@latest
   ```
   Verify it appears in `go.mod`.

2. Create `core/canonical/jcs.go` with the canonical-hash helper:
   ```go
   // Package canonical provides deterministic canonicalization and content
   // hashing for template specs.
   //
   // RFC 8785 JSON Canonicalization Scheme (JCS) is used so that two
   // semantically-identical TemplateSpec values — regardless of map ordering,
   // whitespace, or non-essential string-escape variations — produce
   // byte-identical canonical bytes and, in turn, identical hashes.
   //
   // @blessed-invariant: the canonical-hash function is the registry's identity
   // function. Any change that alters output bytes for previously-registered
   // specs is a breaking change. The JCS library version is pinned in go.mod.
   package canonical

   import (
   	"crypto/sha256"
   	"encoding/hex"
   	"encoding/json"
   	"fmt"

   	jsoncanonicalizer "webpki.org/jsoncanonicalizer"

   	"github.com/fallguyconsulting/rimsky/core/node"
   )

   // CanonicalSpecHash returns the rimsky-side content hash of a TemplateSpec
   // in the form "sha256-<64-hex>". The spec is JSON-marshalled via
   // encoding/json, JCS-canonicalized, then SHA-256-hashed.
   func CanonicalSpecHash(spec node.TemplateSpec) (string, error) {
   	raw, err := json.Marshal(spec)
   	if err != nil {
   		return "", fmt.Errorf("marshal spec: %w", err)
   	}
   	canon, err := jsoncanonicalizer.Transform(raw)
   	if err != nil {
   		return "", fmt.Errorf("canonicalize spec: %w", err)
   	}
   	sum := sha256.Sum256(canon)
   	return "sha256-" + hex.EncodeToString(sum[:]), nil
   }
   ```

3. Create `core/canonical/jcs_test.go` with these test cases (covering the four invariants spec §7.3 calls out):
   - `TestCanonicalSpecHash_Deterministic`: hash the same spec twice; expect identical output.
   - `TestCanonicalSpecHash_KeyOrderInvariant`: build two specs that differ only in declared field order (use `node.TemplateSpec` directly with same data); expect identical hashes.
   - `TestCanonicalSpecHash_PrefixIsSha256`: assert output begins with `"sha256-"` and the suffix is 64 hex chars.
   - `TestCanonicalSpecHash_DistinctSpecs`: build two specs with one differing field; expect different hashes.
   - `TestCanonicalSpecHash_WhitespaceInvariant`: take a `node.TemplateSpec` and produce two raw JSON marshals — one compact, one indented (using `json.MarshalIndent`) — feed both into `jsoncanonicalizer.Transform` directly (bypassing the helper) and assert the canonicalized output bytes are identical. This validates JCS strips whitespace as expected by the helper.
   - `TestCanonicalSpecHash_NumberNormalization`: build two raw JSON byte slices that differ only in number representation (e.g., `{"x": 1.0}` vs `{"x": 1}`, or `{"x": 1e3}` vs `{"x": 1000}`); feed both through `jsoncanonicalizer.Transform`; assert the canonicalized bytes are identical. This validates JCS normalizes numbers as RFC 8785 specifies.

   Each test constructs a minimal `node.TemplateSpec` (using `Name`, `Version`, `FrameResolution`, and one trivial node) and exercises the hash. The whitespace / number-normalization tests can directly exercise `jsoncanonicalizer.Transform` rather than going through `CanonicalSpecHash`, since `encoding/json` always emits the same shape for a given Go value (so testing JSON-source variation requires byte-level inputs).

4. Verify the canonical package builds and its tests pass:
   ```bash
   go build ./core/canonical/... && go test ./core/canonical/...
   ```

5. Run the full module build to make sure the dep resolves cleanly:
   ```bash
   go mod tidy && go build ./...
   ```

---

## Task 2 — Proto changes: six lifecycle RPCs and Open scope envelope

**Files:** `proto/v1/store_service.proto`, generated `proto/v1/gen/*.go`.

### Steps

1. Edit `proto/v1/store_service.proto`. Add the six lifecycle RPCs to the `StoreService` service block (after the existing `Capabilities` RPC, before `Open`):
   ```protobuf
   // Lifecycle events. Every store-service implements all six; stores that
   // don't care return success immediately. Per spec §4.1.
   rpc OnTemplateRegistered(OnTemplateRegisteredRequest)     returns (OnTemplateRegisteredResponse);
   rpc OnTemplateDeployed(OnTemplateDeployedRequest)         returns (OnTemplateDeployedResponse);
   rpc OnTemplateUndeployed(OnTemplateUndeployedRequest)     returns (OnTemplateUndeployedResponse);
   rpc OnTemplateDeregistered(OnTemplateDeregisteredRequest) returns (OnTemplateDeregisteredResponse);
   rpc OnInstanceCreated(OnInstanceCreatedRequest)           returns (OnInstanceCreatedResponse);
   rpc OnInstanceTerminated(OnInstanceTerminatedRequest)     returns (OnInstanceTerminatedResponse);
   ```

2. In the same file, add the message definitions (after the existing `ReleaseResponse`):
   ```protobuf
   // Template-scope lifecycle events. template_id is the content hash
   // ("sha256-<64-hex>"), opaque to rimsky.
   message OnTemplateRegisteredRequest    { string template_id = 1; }
   message OnTemplateRegisteredResponse   {}
   message OnTemplateDeployedRequest      { string template_id = 1; }
   message OnTemplateDeployedResponse     {}
   message OnTemplateUndeployedRequest    { string template_id = 1; }
   message OnTemplateUndeployedResponse   {}
   message OnTemplateDeregisteredRequest  { string template_id = 1; }
   message OnTemplateDeregisteredResponse {}

   // Instance-scope lifecycle events. instance_id is the rimsky-generated
   // instance UUID, opaque to rimsky.
   message OnInstanceCreatedRequest {
     string template_id = 1;
     string instance_id = 2;
   }
   message OnInstanceCreatedResponse {}
   message OnInstanceTerminatedRequest {
     string template_id = 1;
     string instance_id = 2;
   }
   message OnInstanceTerminatedResponse {}
   ```

3. Edit `OpenRequest` in the same file. Add two trailing fields:
   ```protobuf
   string template_id  = 6;   // content hash; opaque to rimsky.
   string instance_id  = 7;   // instance UUID; opaque to rimsky.
   ```

4. Regenerate the proto bindings:
   ```bash
   make proto-gen
   ```

5. Verify the regenerated code compiles:
   ```bash
   go build ./proto/v1/gen/...
   ```

---

## Task 3 — Schema migration: drop-and-recreate templates/instances; add tags and lifecycle tables

**Files:** `core/migrations/003-template-registry-and-lifecycle.sql`, `core/migrations/embed.go`, `core/migrations/runner_test.go`.

### Steps

1. Inspect the existing `core/migrations/embed.go` to see the registration pattern:
   ```bash
   cat core/migrations/embed.go
   ```

2. Create `core/migrations/003-template-registry-and-lifecycle.sql`:
   ```sql
   -- 003-template-registry-and-lifecycle.sql
   -- Control-plane v1 + store lifecycle protocol.
   -- Per docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md.
   --
   -- Pre-v1: drop and recreate templates/instances. Existing dev databases
   -- are nuked. CASCADE drops dependent rows in rimsky_nodes,
   -- rimsky_dispatch, rimsky_lock_holders, rimsky_claim_holders, rimsky_frames,
   -- rimsky_node_attributes, rimsky_schedules, rimsky_supervisor_heartbeats.

   DROP TABLE IF EXISTS rimsky_instances CASCADE;
   DROP TABLE IF EXISTS rimsky_templates CASCADE;

   CREATE TABLE rimsky_templates (
       id              TEXT        PRIMARY KEY,
       spec            JSONB       NOT NULL,
       state           TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
       registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
       source          TEXT        NOT NULL DEFAULT 'direct'
   );

   CREATE TABLE rimsky_template_tags (
       tag             TEXT        PRIMARY KEY,
       template_id     TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
       updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
   );

   CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);

   CREATE TABLE rimsky_instances (
       id             UUID        PRIMARY KEY,
       template_hash  TEXT        NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
       instance_key   TEXT,
       params         JSONB       NOT NULL DEFAULT '{}',
       created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
       -- terminated_at is the implementation choice for spec §10 open question 1
       -- (instance terminal-event detection mechanism). Set by the scheduler/frame
       -- terminal-predicate evaluation (Task 18); polled by the control-api
       -- terminator worker (Task 19) which fires OnInstanceTerminated.
       terminated_at  TIMESTAMPTZ,
       UNIQUE (template_hash, instance_key)
   );

   CREATE INDEX idx_rimsky_instances_terminated
       ON rimsky_instances (terminated_at)
       WHERE terminated_at IS NOT NULL;

   CREATE TABLE rimsky_store_lifecycle (
       store_registration_name TEXT        NOT NULL,
       scope_kind              TEXT        NOT NULL CHECK (scope_kind IN ('template','instance')),
       scope_id                TEXT        NOT NULL,
       state                   TEXT        NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
       last_event_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
       PRIMARY KEY (store_registration_name, scope_kind, scope_id)
   );

   CREATE INDEX idx_rimsky_store_lifecycle_scope
       ON rimsky_store_lifecycle (scope_kind, scope_id);
   ```

   Notes embedded in the SQL: per spec §1.2, §1.6, §2.1, §5.3. The `terminated_at` column is the at-least-once trigger for `OnInstanceTerminated` (Task 19).

3. Register the new migration in `core/migrations/embed.go`. Following the existing pattern (each migration file embedded with go:embed), add `003-template-registry-and-lifecycle.sql` to the embed declaration.

4. Inspect any existing references to the old schema in tests:
   ```bash
   grep -rn 'consumer_key\|template_id UUID\|rimsky_substores' core/ test/ stores/ 2>/dev/null
   ```
   Note the file paths returned — they will be updated in Tasks 11 and 17.

5. Update `core/migrations/runner_test.go` if it whitelists table names — extend the list with `rimsky_template_tags` and `rimsky_store_lifecycle`. The current list includes `rimsky_templates`; add the new tables.

6. Verify the migration runs against a fresh testcontainer Postgres:
   ```bash
   go test ./core/migrations/... -count=1 -run TestRunner
   ```

---

## Task 4 — Extend Store interface with six lifecycle methods + default no-op embeddable

**Files:** `core/store/interface.go`, `core/store/types.go`, `core/store/no_op.go` (new).

### Steps

1. Edit `core/store/types.go`. Extend `ClaimSpec` to carry the scope envelope:
   ```go
   // ClaimSpec is the post-substitution claim shape passed to Open. The
   // TemplateID and InstanceID fields carry the per-spec §4.2 scope envelope;
   // both are opaque strings rimsky never inspects (per blessed invariant 20).
   type ClaimSpec struct {
   	StoreName  string
   	Selector   string
   	Intent     Intent
   	Alias      string
   	TemplateID string   // content hash (template-scope)
   	InstanceID string   // instance UUID (instance-scope)
   }
   ```
   Existing call sites that construct `ClaimSpec` will need TemplateID/InstanceID populated; do that as part of Task 5 (remote client) and Task 12 (instance creation).

2. Edit `core/store/interface.go`. Append six new method signatures to the `Store` interface:
   ```go
   // Lifecycle events. Per spec §4.1 / §5. All stores implement all six;
   // stores that don't care return nil (success). Idempotent: calling the
   // same event for the same scope twice produces the same observable state.
   OnTemplateRegistered(ctx context.Context, templateID string) error
   OnTemplateDeployed(ctx context.Context, templateID string) error
   OnTemplateUndeployed(ctx context.Context, templateID string) error
   OnTemplateDeregistered(ctx context.Context, templateID string) error
   OnInstanceCreated(ctx context.Context, templateID, instanceID string) error
   OnInstanceTerminated(ctx context.Context, templateID, instanceID string) error
   ```

3. Create `core/store/no_op.go` with a default no-op embeddable struct:
   ```go
   package store

   import "context"

   // LifecycleNoop provides default no-op implementations of the six
   // lifecycle methods. Embed it in a Store implementation to opt out of
   // reacting to lifecycle events while remaining contract-conformant.
   //
   //   type MyStore struct {
   //   	store.LifecycleNoop
   //   	// ... runtime-verb impls ...
   //   }
   type LifecycleNoop struct{}

   func (LifecycleNoop) OnTemplateRegistered(ctx context.Context, templateID string) error {
   	return nil
   }
   func (LifecycleNoop) OnTemplateDeployed(ctx context.Context, templateID string) error {
   	return nil
   }
   func (LifecycleNoop) OnTemplateUndeployed(ctx context.Context, templateID string) error {
   	return nil
   }
   func (LifecycleNoop) OnTemplateDeregistered(ctx context.Context, templateID string) error {
   	return nil
   }
   func (LifecycleNoop) OnInstanceCreated(ctx context.Context, templateID, instanceID string) error {
   	return nil
   }
   func (LifecycleNoop) OnInstanceTerminated(ctx context.Context, templateID, instanceID string) error {
   	return nil
   }
   ```

4. Verify the package compiles (it'll fail to compile until Tasks 5–7 update implementations, but check at least syntactically):
   ```bash
   go build ./core/store/
   ```
   Compilation will error on missing methods in `core/store/remote/` and `core/store/storetest/` — this is expected and is fixed in Tasks 5–6.

---

## Task 5 — Update remote gRPC client: lifecycle methods + Open envelope

**Files:** `core/store/remote/client.go`.

### Steps

1. Read the current `core/store/remote/client.go`:
   ```bash
   cat core/store/remote/client.go
   ```
   Identify the existing method set and how the gRPC proto types are imported.

2. Add the six lifecycle methods to the gRPC client. Each forwards to the corresponding generated method on `genv1.StoreServiceClient`. Pattern:
   ```go
   func (c *Client) OnTemplateRegistered(ctx context.Context, templateID string) error {
   	_, err := c.svc.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{TemplateId: templateID})
   	return err
   }
   ```
   Implement all six similarly. The two instance-scope methods take both `templateID` and `instanceID` and populate both proto fields.

3. Update the existing `Open` method to populate `template_id` and `instance_id` from the `ClaimSpec`:
   ```go
   req := &genv1.OpenRequest{
   	ClaimId:    string(claimID),
   	StoreName:  spec.StoreName,
   	Selector:   spec.Selector,
   	Intent:     string(spec.Intent),
   	Alias:      spec.Alias,
   	TemplateId: spec.TemplateID,
   	InstanceId: spec.InstanceID,
   }
   ```

4. Verify the package compiles:
   ```bash
   go build ./core/store/remote/...
   ```

---

## Task 5a — Populate scope envelope on the supervisor's ClaimSpec construction

**Files:** `core/supervisor/runner_locks.go` (and any other ClaimSpec construction sites under `core/supervisor/`).

Per spec §4.2: "The supervisor populates both [template_id, instance_id] from the dispatch row's instance → template lookup." The proto change (Task 2), the `ClaimSpec` extension (Task 4), and the remote client (Task 5) carry the fields end-to-end, but the supervisor must populate them at the construction site or they reach the wire as empty strings.

### Steps

1. Locate the construction sites:
   ```bash
   grep -rn 'store\.ClaimSpec{' core/supervisor/
   ```
   The primary site is `core/supervisor/runner_locks.go:110` (compute claim specs from the dispatch row's resolved nodes).

2. Trace where `template_hash` and `instance_id` are available in the supervisor's flow. The supervisor reads dispatch rows; each dispatch row carries `instance_id` and (transitively, via the existing instance → template join in the queue queries) `template_hash`. If the existing query already joins to fetch instance / template metadata, extend the row struct to include `template_hash` (renamed from any prior `template_id`). If not, extend the queue/dispatch query in `core/queue/postgres/queue.go` (or its callers) to include `i.template_hash AS template_hash` in the SELECT.

3. At each `store.ClaimSpec{}` construction site, populate the new fields:
   ```go
   store.ClaimSpec{
   	StoreName:  ...,
   	Selector:   ...,
   	Intent:     ...,
   	Alias:      ...,
   	TemplateID: row.TemplateHash,
   	InstanceID: row.InstanceID.String(),
   }
   ```

4. Inspect `core/queue/postgres/queue.go` and any callers in `core/supervisor/runner_*.go` for places that pre-existed where `template_id`-as-UUID columns were SELECTed; rename to `template_hash`. The migration in Task 3 already renames the column; this step propagates the type change to the Go side.

5. Verify build and supervisor tests:
   ```bash
   go build ./core/supervisor/... ./core/queue/...
   go test ./core/supervisor/... ./core/queue/postgres/... -count=1
   ```

---

## Task 6 — Update storetest.Fake with the six lifecycle methods

**Files:** `core/store/storetest/fake.go`.

### Steps

1. Add tracking for lifecycle calls on the existing `FakeCall` struct (extend `Verb` to allow lifecycle verb names; add an optional `TemplateID` and `InstanceID` field on the struct).

2. Implement the six lifecycle methods on `*Fake`. Pattern:
   ```go
   func (f *Fake) OnTemplateRegistered(ctx context.Context, templateID string) error {
   	f.mu.Lock()
   	defer f.mu.Unlock()
   	if f.ErrorFunc != nil {
   		if err := f.ErrorFunc("OnTemplateRegistered", store.ClaimID("")); err != nil {
   			return err
   		}
   	}
   	f.calls = append(f.calls, FakeCall{Verb: "OnTemplateRegistered", TemplateID: templateID})
   	return nil
   }
   ```
   Implement all six identically (template-scope use only TemplateID; instance-scope use both).

3. Verify storetest builds and any existing storetest tests still pass:
   ```bash
   go build ./core/store/storetest/... && go test ./core/store/...
   ```
   The `storetest` package itself should now satisfy the `Store` interface; if existing in-tree consumers of `*Fake` fail to typecheck, that's the symptom of an incomplete interface impl — fix until the package compiles.

---

## Task 7 — Update standalone store implementations: postgres, filesystem, stub

**Files:** `stores/postgres/main.go` (or its server entry point), `stores/filesystem/main.go`, `stores/stub/main.go`, plus per-binary internals as needed.

### Steps

1. For each of the three store binaries, locate the gRPC server-side `StoreServiceServer` implementation:
   ```bash
   grep -rn 'StoreServiceServer\|UnimplementedStoreServiceServer' stores/
   ```

2. Add no-op handlers for each of the six lifecycle methods on the server impl. Each returns `(&genv1.OnXxxResponse{}, nil)` with no side effects. Document the no-op pattern with a one-line comment per method:
   ```go
   // OnTemplateRegistered no-op: this store does not maintain template
   // metadata; lifecycle is operator-config driven.
   func (s *server) OnTemplateRegistered(ctx context.Context, req *genv1.OnTemplateRegisteredRequest) (*genv1.OnTemplateRegisteredResponse, error) {
   	return &genv1.OnTemplateRegisteredResponse{}, nil
   }
   ```

3. For each store, run its build and tests:
   ```bash
   go build ./stores/postgres/... ./stores/filesystem/... ./stores/stub/...
   go test ./stores/... -count=1
   ```

---

## Task 7a — Add the six lifecycle endpoints to the HTTP+JSON bridge

**Files:** `stores/internal/bridge/bridge.go`, `stores/internal/bridge/bridge_test.go`.

Per spec §4.3: "The HTTP+JSON bridge (per v3 spec §5.2) gains six new endpoints mirroring the gRPC methods. JSON request/response shapes match the proto messages. Path naming follows the existing convention (`/v1/store/Open` → `/v1/store/OnTemplateRegistered`, etc.)."

The bridge is shared infrastructure used by every store-service binary (filesystem, postgres, stub). Updating the bridge once propagates the new endpoints to all stores via their existing mounts.

### Steps

1. Inspect the existing bridge to learn the registration pattern:
   ```bash
   cat stores/internal/bridge/bridge.go
   ```

2. Add six new HTTP handler functions, one per lifecycle event. Each:
   - Decodes the JSON request body into the corresponding generated proto message (`genv1.OnTemplateRegisteredRequest`, etc.).
   - Calls the corresponding method on the wrapped `genv1.StoreServiceServer` implementation.
   - Encodes the response message back as JSON (empty object `{}` for the empty response messages).
   - Returns gRPC status codes mapped to HTTP status codes per the existing convention.

3. Mount the new routes on the existing chi router at:
   ```
   POST /v1/store/OnTemplateRegistered
   POST /v1/store/OnTemplateDeployed
   POST /v1/store/OnTemplateUndeployed
   POST /v1/store/OnTemplateDeregistered
   POST /v1/store/OnInstanceCreated
   POST /v1/store/OnInstanceTerminated
   ```
   Match the path-prefix convention used by the existing `/v1/store/Open`, `/v1/store/Commit`, etc.

4. Update `stores/internal/bridge/bridge_test.go` (if it exists) with one round-trip test per new endpoint: send the JSON request, assert the empty response, assert the underlying `StoreServiceServer` method was invoked with the right arguments. If no bridge test file exists today, create one with at least one test that exercises an `OnTemplateDeployed` round-trip.

5. Verify build and tests:
   ```bash
   go build ./stores/internal/bridge/...
   go test ./stores/internal/bridge/... -count=1
   ```

6. Verify the three store binaries that mount the bridge still build (no source change needed in those binaries; the bridge is shared):
   ```bash
   go build ./stores/postgres/... ./stores/filesystem/... ./stores/stub/...
   ```

---

## Task 8 — Unified rimsky.yml config: extend stores.yml parsing with executors

**Files:** `core/config/stores.go`.

### Steps

1. Read the current `core/config/stores.go` to understand the existing YAML wrapper struct:
   ```bash
   cat core/config/stores.go
   ```

2. Rename the loader and extend the schema. Add a new function `LoadRimskyConfigYAML(path string) (RimskyConfig, error)`. Keep `LoadStoresConfigYAML` callers happy by providing it as a thin wrapper that returns the stores+namedlocks subset, or remove call sites and update them in Task 9.

   Define the new top-level type:
   ```go
   // RimskyConfig is the parsed rimsky.yml — the unified deployment-shape
   // config loaded by all three rimsky processes. Per spec §3.1.
   type RimskyConfig struct {
   	Stores     RemoteStoresConfig
   	NamedLocks store.NamedLocksConfig
   	Executors  ExecutorsConfig
   }

   // ExecutorsConfig is the parsed `executors:` block.
   type ExecutorsConfig struct {
   	Executors map[string]ExecutorEntry
   }

   // ExecutorEntry per spec §3.1.
   type ExecutorEntry struct {
   	Transport string // "grpc"
   	Endpoint  string // "claude-agent:9090"
   	TLS       bool
   }
   ```

3. Implement `LoadRimskyConfigYAML(path string) (RimskyConfig, error)` that:
   - Reads the file, expands env vars (matching existing pattern with `os.ExpandEnv`).
   - Parses three top-level YAML blocks: `stores`, `named_locks`, `executors`.
   - Returns a populated `RimskyConfig`.
   - Returns a clear error if the file is missing.
   - Validates that each executor entry has a non-empty `transport` and `endpoint`; rejects with a descriptive error if not.

4. Add a `Validate()` method on `ExecutorsConfig`:
   ```go
   func (c ExecutorsConfig) Validate() error {
   	for name, e := range c.Executors {
   		if e.Transport == "" {
   			return fmt.Errorf("executor %q: transport required", name)
   		}
   		if e.Endpoint == "" {
   			return fmt.Errorf("executor %q: endpoint required", name)
   		}
   	}
   	return nil
   }
   ```

5. Add an `ExecutorDeclared(name string) bool` helper for use by control-api validation hooks:
   ```go
   func (c ExecutorsConfig) ExecutorDeclared(name string) bool {
   	_, ok := c.Executors[name]
   	return ok
   }
   ```

6. Verify the config package builds:
   ```bash
   go build ./core/config/...
   ```
   Existing call sites that referenced `LoadStoresConfigYAML` will fail compile — fix those call sites in Task 9 and 10. For now, leave the old function as a deprecated alias if necessary to keep this task atomic; otherwise remove it and let downstream tasks fix the breakage.

---

## Task 9 — Wire RIMSKY_CONFIG into the three cmd binaries

**Files:** `core/cmd/rimsky-control-api/main.go`, `core/cmd/rimsky-supervisor/main.go`, `core/cmd/rimsky-scheduler/main.go`.

### Steps

1. For each cmd file, replace the `RIMSKY_STORES_CONFIG` env var with `RIMSKY_CONFIG`:
   ```bash
   grep -rn 'RIMSKY_STORES_CONFIG\|defaultStoresConfigPath' core/cmd/
   ```

2. In each main.go, change:
   - The env-var name read from `os.Getenv` to `RIMSKY_CONFIG`.
   - The default path to `/etc/rimsky/rimsky.yml`.
   - The loader call from `LoadStoresConfigYAML` to `LoadRimskyConfigYAML` (returning the unified struct).
   - The downstream code to consume `cfg.Stores`, `cfg.NamedLocks`, `cfg.Executors`.
   - Documentation comments at the top of each file referring to the old env var/path.

3. In `rimsky-supervisor/main.go`, also remove the `executors:` block parsing from the supervisor-config loader. The supervisor consumes `cfg.Executors` from the unified config instead. Keep `RIMSKY_SUPERVISOR_CONFIG` for the per-process tuning knobs (concurrency, callback, heartbeat). Build the executor `Resolver` from `cfg.Executors`. **After load, call `cfg.Executors.Validate()` and fail-fast on error** (per spec §6.6 step 3 — syntactic validation only; no DNS/dial).

4. In `rimsky-control-api/main.go`, pass `cfg.Executors.ExecutorDeclared` into the validator hooks (Task 10 wires it into the hook struct; here, prepare the dependency).

5. Verify all three binaries build:
   ```bash
   go build ./core/cmd/rimsky-control-api/... ./core/cmd/rimsky-supervisor/... ./core/cmd/rimsky-scheduler/...
   ```

---

## Task 10 — Add ExecutorDeclared validation hook

**Files:** `core/node/template_validator.go`, `core/node/template_validator_test.go`, `core/controlapi/templates.go` (just the validatorHooksFor func), `core/config/controlapi.go` (or wherever `AppDeps` is constructed).

### Steps

1. In `core/node/template_validator.go`, add `ExecutorDeclared` to `RegistryHooks`:
   ```go
   type RegistryHooks struct {
   	StoreDeclared     func(name string) bool
   	NamedLockDeclared func(name string) bool
   	ExecutorDeclared  func(name string) bool
   }
   ```

2. In `ValidateTemplate`, add a per-node check: if `node.Executor != ""` and `hooks.ExecutorDeclared != nil`, require it returns true; otherwise emit a validation error like `executor "claude-agent" not declared`. Follow the existing pattern in `validateStores`.

3. Add a positive and negative test case in `core/node/template_validator_test.go`:
   - `TestValidateTemplate_ExecutorDeclared_OK`: spec with one executor reference; hook returns true; expect Ok().
   - `TestValidateTemplate_ExecutorDeclared_Missing`: same spec; hook returns false; expect a validation error mentioning the executor name.

4. In `core/controlapi/templates.go`, extend `validatorHooksFor(deps AppDeps)` to wire `hooks.ExecutorDeclared = deps.Executors.ExecutorDeclared` (or an equivalent helper on whatever type holds the executor set in AppDeps).

5. In `core/config/controlapi.go` (or the equivalent control-api wiring), add an `Executors ExecutorsConfig` field to the control-api config, populate it from `RimskyConfig.Executors` in `core/cmd/rimsky-control-api/main.go`, and thread it into `AppDeps`.

6. Verify the validator and controlapi packages build and tests pass:
   ```bash
   go test ./core/node/... ./core/config/... ./core/controlapi/...
   ```

---

## Task 11 — Templates DAO: rewrite for hash-keyed schema

**Files:** `core/storage/postgres/templates.go`, `core/storage/postgres/template_tags.go` (new), `core/storage/interfaces.go`.

### Steps

1. Inspect the existing `core/storage/postgres/templates.go` and the storage backend interface:
   ```bash
   cat core/storage/postgres/templates.go
   grep -n 'Templates()' core/storage/interfaces.go
   ```

2. Define new DAO types in `core/storage/postgres/templates.go`. The `Template` row struct becomes:
   ```go
   type Template struct {
   	ID            string             // hash, "sha256-..."
   	Spec          node.TemplateSpec  // existing JSON-decoded spec
   	State         string             // "registered" | "deployed" | "undeployed"
   	RegisteredAt  time.Time
   	Source        string             // "direct" | future package-manager values
   }
   ```

3. Replace the existing `Deploy(ctx, spec, tx) (TemplateSummary, error)` with explicit registry CRUD. The new DAO surface (interface in `core/storage/`):
   ```go
   type TemplatesDAO interface {
   	Insert(ctx context.Context, t Template, tx *Tx) error
   	GetByHash(ctx context.Context, hash string, tx *Tx) (*Template, error)
   	List(ctx context.Context, filter TemplateListFilter, page ListPagination, tx *Tx) (TemplatePage, error)
   	UpdateState(ctx context.Context, hash, newState string, tx *Tx) error
   	DeleteByHash(ctx context.Context, hash string, tx *Tx) error
   	LockForUpdate(ctx context.Context, hash string, tx *Tx) (*Template, error)  // SELECT … FOR UPDATE per §2.2
   }
   ```
   Update the Postgres impl methods accordingly. `LockForUpdate` runs `SELECT … FROM rimsky_templates WHERE id = $1 FOR UPDATE` and returns the row.

4. Create `core/storage/postgres/template_tags.go` with a tags DAO:
   ```go
   type TemplateTag struct {
   	Tag         string
   	TemplateID  string
   	UpdatedAt   time.Time
   }

   type TemplateTagsDAO interface {
   	Upsert(ctx context.Context, tag, templateID string, tx *Tx) error
   	Get(ctx context.Context, tag string, tx *Tx) (*TemplateTag, error)
   	ListByTemplate(ctx context.Context, templateID string, tx *Tx) ([]TemplateTag, error)
   	Delete(ctx context.Context, tag string, tx *Tx) (deleted bool, err error)
   	List(ctx context.Context, page ListPagination, tx *Tx) (TemplateTagPage, error)
   	CountByTemplate(ctx context.Context, templateID string, tx *Tx) (int, error)
   }
   ```
   Implement the Postgres-backed methods.

5. Extend `core/storage/interfaces.go`'s `StorageBackend` interface (or equivalent) with `TemplateTags() TemplateTagsDAO` and update `Templates() TemplatesDAO` to the new interface.

6. Update existing call sites that referenced the old `Deploy`/`Get(uuid)` signature. Likely call sites:
   - `core/controlapi/templates.go` (rewritten in Task 14).
   - `core/frame/producer.go`, `core/frame/producer_test.go`.
   - `core/attributes/store_test.go`.
   - `core/queue/postgres/queue_test.go`.
   - `core/controlapi/admin_routes_test.go`.

   For each, swap UUID lookups to hash lookups; update SQL test seeds to use TEXT hashes (e.g., `sha256-<hex>` or a fixed test sentinel like `sha256-aaa...`).

7. Verify storage builds and the Postgres tests pass (testcontainers required):
   ```bash
   go build ./core/storage/... && go test ./core/storage/postgres/... -count=1
   ```

8. Add FK-refusal integration tests in `core/storage/postgres/postgres_test.go` (or a new sibling file) covering the `ON DELETE RESTRICT` constraints introduced by Task 3:
   - `TestTemplateDelete_RefusedWithTagReferences`: insert template, insert tag pointing at it, attempt template delete via direct SQL `DELETE FROM rimsky_templates WHERE id = ...`; expect a Postgres FK error.
   - `TestTemplateDelete_RefusedWithInstanceReferences`: insert template, insert instance referencing it, attempt template delete; expect FK error.
   - `TestTemplateTagDelete_DoesNotCascadeToTemplate`: insert template, two tags; delete one tag; verify template row still exists.

9. Re-run storage tests after adding the FK-refusal cases:
   ```bash
   go test ./core/storage/postgres/... -count=1
   ```

---

## Task 12 — Instances DAO: template_hash + instance_key + terminated_at

**Files:** `core/storage/postgres/instances.go`.

### Steps

1. Update the `Instance` row struct to:
   ```go
   type Instance struct {
   	ID            uuid.UUID
   	TemplateHash  string
   	InstanceKey   *string  // nullable
   	Params        map[string]any
   	CreatedAt     time.Time
   	TerminatedAt  *time.Time  // nullable; set when instance reaches terminal state
   }
   ```

2. Update DAO methods to use the new column names:
   - `Insert`: writes `template_hash`, `instance_key` (nullable), `params`. UUID generated by caller.
   - `GetByID`: returns `Instance` per the new struct.
   - `ListByTemplate(hash string, …)`: replaces any list-by-template_id helper.
   - `MarkTerminated(ctx, instanceID, tx)`: `UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1 AND terminated_at IS NULL`. Idempotent (only sets once).
   - `ListTerminatedWithLifecycleRows(ctx, limit, tx)`: returns instances with `terminated_at IS NOT NULL` that still have at least one matching `rimsky_store_lifecycle` row at `(scope_kind='instance', scope_id=instance_id::text)`. Used by the terminator worker (Task 19).
   - `CountActiveByTemplate(ctx, templateHash, tx)`: returns the count of `rimsky_instances` rows with `template_hash = $1 AND terminated_at IS NULL`. Used by undeploy/deregister validation.

3. Update existing call sites (frame producer, supervisor, etc.):
   ```bash
   grep -rn 'consumer_key\|instances.Insert\|InstanceKey' core/
   ```
   Adjust each call site to use the renamed field/parameter.

4. Verify storage tests pass:
   ```bash
   go test ./core/storage/postgres/... -count=1
   ```

---

## Task 13 — rimsky_store_lifecycle DAO

**Files:** `core/storage/postgres/store_lifecycle.go` (new), `core/storage/interfaces.go`.

### Steps

1. Create `core/storage/postgres/store_lifecycle.go`:
   ```go
   package postgres

   import (
   	"context"
   	"time"
   	// ...
   )

   // StoreLifecycleRow is one (store, scope) bookkeeping entry.
   type StoreLifecycleRow struct {
   	StoreRegistrationName string
   	ScopeKind             string  // "template" | "instance"
   	ScopeID               string  // template hash or instance UUID (text form)
   	State                 string  // see spec §5.3
   	LastEventAt           time.Time
   }

   type StoreLifecycleDAO interface {
   	Get(ctx context.Context, storeName, scopeKind, scopeID string, tx *Tx) (*StoreLifecycleRow, error)
   	Upsert(ctx context.Context, row StoreLifecycleRow, tx *Tx) error
   	Delete(ctx context.Context, storeName, scopeKind, scopeID string, tx *Tx) error
   	DeleteByScope(ctx context.Context, scopeKind, scopeID string, tx *Tx) error  // delete all rows for a scope
   	ListByScope(ctx context.Context, scopeKind, scopeID string, tx *Tx) ([]StoreLifecycleRow, error)
   }
   ```

2. Implement the Postgres-backed methods. The Upsert query is:
   ```sql
   INSERT INTO rimsky_store_lifecycle (store_registration_name, scope_kind, scope_id, state, last_event_at)
   VALUES ($1, $2, $3, $4, now())
   ON CONFLICT (store_registration_name, scope_kind, scope_id)
   DO UPDATE SET state = EXCLUDED.state, last_event_at = now()
   ```

3. Add `StoreLifecycle() StoreLifecycleDAO` to the storage backend interface and the Postgres backend struct.

4. Verify build and tests:
   ```bash
   go build ./core/storage/... && go test ./core/storage/postgres/... -count=1
   ```

---

## Task 14 — Lifecycle fan-out helper

**Files:** `core/controlapi/lifecycle.go` (new), `core/controlapi/lifecycle_test.go` (new).

### Steps

1. Create `core/controlapi/lifecycle.go` with a fan-out helper used by all template-state-transition endpoints:
   ```go
   package controlapi

   // Lifecycle event kinds.
   type LifecycleEvent int

   const (
   	EventTemplateRegistered LifecycleEvent = iota
   	EventTemplateDeployed
   	EventTemplateUndeployed
   	EventTemplateDeregistered
   	EventInstanceCreated
   	EventInstanceTerminated
   )

   // FanOutTemplateEvent fires `event` to every distinct store referenced by
   // the template's nodes, idempotent against the rimsky_store_lifecycle table.
   //
   // Returns the deduped store list, the per-store error map (empty on full
   // success), and an aggregate error for the operation.
   //
   // Per spec §5.4 (template events): for each store, look up the lifecycle
   // row; skip if state already at target; otherwise RPC the event; on
   // success UPSERT the row to the new state; on failure, accumulate.
   func (a *App) FanOutTemplateEvent(
   	ctx context.Context,
   	event LifecycleEvent,
   	templateHash string,
   	spec node.TemplateSpec,
   ) (storeNames []string, perStoreErr map[string]error, err error)

   // FanOutInstanceEvent fires `event` (must be EventInstanceCreated or
   // EventInstanceTerminated) similarly.
   func (a *App) FanOutInstanceEvent(
   	ctx context.Context,
   	event LifecycleEvent,
   	templateHash, instanceID string,
   	spec node.TemplateSpec,
   ) (storeNames []string, perStoreErr map[string]error, err error)
   ```

2. Implement the helpers. Outline:
   - Compute the deduped store-name set from `spec.Nodes[].Stores[].Name` (use a `map[string]struct{}`).
   - Sort the names lexicographically (per spec §5.6: deterministic order for testability).
   - For each name:
     - Look up the rimsky_store_lifecycle row for `(name, scope_kind, scope_id)`.
     - If row state already matches the target state for `event`: skip.
     - Else: dial the store via the registry and call the appropriate gRPC method.
     - On success: UPSERT row to new state. (For deregister/terminate: DELETE row.)
     - On failure: collect into per-store error map; abort iteration.
   - Return.

   The `targetStateFor(event)` mapping:
   - `EventTemplateRegistered` → `"registered"`
   - `EventTemplateDeployed` → `"deployed"`
   - `EventTemplateUndeployed` → `"undeployed"`
   - `EventTemplateDeregistered` → row deleted (no terminal state value)
   - `EventInstanceCreated` → `"created"`
   - `EventInstanceTerminated` → row deleted

3. Create `core/controlapi/lifecycle_test.go` with unit tests using `storetest.Fake`:
   - `TestFanOut_DeduplicatesStores`: spec has 3 nodes referencing the same store; expect exactly 1 RPC.
   - `TestFanOut_SortedDeterministically`: spec references stores `b`, `a`, `c`; expect call order `a`, `b`, `c`.
   - `TestFanOut_SkipsAlreadyAtTarget`: pre-populate lifecycle row at target state; expect zero RPCs.
   - `TestFanOut_PartialFailureAborts`: fake stores: first OK, second errors; expect first row written, second error returned, third (if any) not called.
   - `TestFanOut_DeleteOnDeregister`: pre-populate row; fire OnTemplateDeregistered; expect row deleted.

4. Verify the lifecycle helper builds and tests pass:
   ```bash
   go test ./core/controlapi/ -run TestFanOut -count=1
   ```

---

## Task 15 — Rewrite controlapi/templates.go: registry endpoints + lifecycle integration

**Files:** `core/controlapi/templates.go`, `core/controlapi/templates_test.go`.

### Steps

1. Update the request shape. The new `templateRegisterRequest`:
   ```go
   type templateRegisterRequest struct {
   	Spec   templateSpecJSON `json:"spec"`
   	Tag    string           `json:"tag,omitempty"`
   	Source string           `json:"source,omitempty"` // default 'direct'
   }
   ```
   `templateSpecJSON` is the existing `templateDeployRequest` body shape, renamed/moved here so the spec body lives nested inside `"spec"`.

2. Implement the new `POST /v1/templates` handler per §1.5 and §6.5:
   1. Decode body.
   2. Convert to `node.TemplateSpec` via the existing `toTemplateSpec` mapper.
   3. Run `node.ValidateTemplate(&spec, validatorHooksFor(deps))` (now including `ExecutorDeclared`). Reject 400 on errors.
   4. Apply `node.ApplyFrameResolutionDefaults(&spec)`.
   5. Compute hash via `canonical.CanonicalSpecHash(spec)`.
   6. Validate `tag` against the regex (Task 16 has the regex helper); reject 400 if invalid.
   7. Open a tx. `GetByHash(hash)`. If row exists: short-circuit per §1.5 step 1; if `tag` provided, upsert tag pointing at hash; commit; return 200 with the hash.
   8. If not exists: call `FanOutTemplateEvent(EventTemplateRegistered, hash, spec)`. On per-store error: rollback (do nothing — no rows to roll back yet), return 5xx with the per-store error map.
   9. On full success: open a tx, `Insert(Template{ID: hash, Spec: spec, State: "registered", Source: source})`, `Upsert(tag, hash)` if `tag` provided, commit. Return 201.

3. Implement `GET /v1/templates`:
   - Query params: `?tag=` substring filter, `?state=`, `?cursor=`, `?limit=`.
   - Calls `Templates().List(filter, page, nil)`; for each row also fetches `TemplateTags().ListByTemplate(id)`.
   - Returns the list shape per §1.5.

4. Implement `GET /v1/templates/{tag_or_hash}`:
   - `resolveTagOrHash(ctx, value)` helper: if `value` matches `^sha256-[0-9a-f]{64}$`, return as-is; else look up in `rimsky_template_tags`. 404 if neither.
   - Returns the template + tags + spec.

5. Implement `POST /v1/templates/{tag_or_hash}/deploy`:
   1. Resolve to hash.
   2. Open tx, `LockForUpdate(hash)`.
   3. If state == "deployed": return 200 (idempotent no-op).
   4. If state != "registered" and != "undeployed": return 409. (Per spec §1.4 invariants and the state diagram, deploy is legal from `registered` OR `undeployed`. The spec §1.5 prose phrasing "registered or already 'deployed'" is internally inconsistent with §1.4 — follow §1.4.)
   5. Get spec from row.
   6. Release tx (don't hold the lock during fan-out — the fan-out is the slow part).
   7. Call `FanOutTemplateEvent(EventTemplateDeployed, hash, spec)`. On error: 5xx.
   8. On success: open tx, `UpdateState(hash, "deployed")`, commit. Return 200.

6. Implement `POST /v1/templates/{tag_or_hash}/undeploy` similarly:
   1. Resolve to hash.
   2. Open tx, `LockForUpdate(hash)`.
   3. If state == "undeployed": idempotent 200.
   4. If state != "deployed": 409.
   5. Check `instances.CountActiveByTemplate(hash)`. If > 0: 409 with the active instance count.
   6. Release tx. Fan-out `EventTemplateUndeployed`. On error: 5xx.
   7. On success: open tx, `UpdateState(hash, "undeployed")`, commit. Return 200.

7. Implement `DELETE /v1/templates/{tag_or_hash}`:
   - Tag form: resolve tag to hash; if other tags still point at this hash: delete just the tag, return `{"deleted": true, "tag_only": true}`.
   - When the tag being deleted is the last one (or the call is via direct hash with no tags pointing at it):
     1. Refuse 409 if `state` is `deployed`.
     2. Refuse 409 if `instances.CountActiveByTemplate(hash) > 0`.
     3. Fan-out `EventTemplateDeregistered`. On error: 5xx. (Note: the lifecycle rows get deleted as part of fan-out.)
     4. Open tx; delete remaining tag (if any); delete template row. Commit. Return 200.

8. Update existing tests in `core/controlapi/templates_test.go` to match the new shapes. Add new tests:
   - `TestRegister_HashCollisionIdempotent`: register same spec twice; second returns 200 with same hash.
   - `TestRegister_TagAttachment`: register with tag; verify both rows.
   - `TestDeploy_FromRegistered`: state machine forward.
   - `TestDeploy_AlreadyDeployedIsIdempotent`: 200, no-op.
   - `TestUndeploy_RefusedWithLiveInstances`: seed an instance; undeploy returns 409.
   - `TestDeleteTemplate_RefusedWhenDeployed`: 409.
   - `TestDeleteTemplate_TagOnly`: two tags point at hash; deleting one removes only the tag.
   - `TestDeleteTemplate_LastTag`: deleting the last tag deletes the template (state must allow).
   - `TestRegister_RejectsHashShapedTag`: tag of form `sha256-<hex>` → 400.

9. Verify the controlapi package builds and tests pass:
   ```bash
   go test ./core/controlapi/... -count=1
   ```

---

## Task 16 — Tags HTTP handlers

**Files:** `core/controlapi/tags.go`, `core/controlapi/tags_test.go`, `core/controlapi/app.go`.

### Steps

1. Create `core/controlapi/tags.go`:
   ```go
   package controlapi

   import "regexp"

   // tagPattern is the canonical tag identifier shape per spec §1.1.
   // Disallows hash-shape (which is rejected by anchor + first-char class).
   var tagPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)

   func validTag(s string) bool { return tagPattern.MatchString(s) }
   ```

2. Implement the four tag CRUD handlers and route registration:
   - `POST /v1/tags` body: `{tag, template: tag_or_hash}` → resolve template; insert tag; 201 or 409 on conflict.
   - `GET /v1/tags?cursor=&limit=` → paginated list.
   - `PUT /v1/tags/{tag}` body: `{template: tag_or_hash}` → 404 if tag missing; otherwise upsert pointing at new hash; 200.
   - `DELETE /v1/tags/{tag}` → delete row if present (only the tag-row, not the template); 200.

3. Add tests in `core/controlapi/tags_test.go`:
   - `TestCreateTag_ValidIdentifier`.
   - `TestCreateTag_RejectsHashShape`.
   - `TestCreateTag_DuplicateConflicts`.
   - `TestMoveTag_404IfMissing`.
   - `TestDeleteTag_DoesNotDeleteTemplate`.

4. Wire the tag routes in `core/controlapi/app.go`:
   ```go
   r.Post("/tags",        handleCreateTag(deps))
   r.Get("/tags",         handleListTags(deps))
   r.Put("/tags/{tag}",   handleMoveTag(deps))
   r.Delete("/tags/{tag}", handleDeleteTag(deps))
   ```

5. Verify build and tests pass:
   ```bash
   go test ./core/controlapi/... -count=1
   ```

---

## Task 17 — Touch-up controlapi/instances.go

**Files:** `core/controlapi/instances.go`, `core/controlapi/instances_test.go`.

### Steps

1. Update the request body shape per §2.2:
   ```go
   type instanceCreateRequest struct {
   	Template    string         `json:"template"`     // tag_or_hash
   	Params      map[string]any `json:"params"`
   	InstanceKey *string        `json:"instance_key,omitempty"`
   }
   ```

2. Rewrite the create handler per §2.2 step ordering:
   1. Decode body.
   2. Resolve `Template` to `template_hash` (via `resolveTagOrHash` from Task 15).
   3. Open a tx with `BEGIN`. Call `Templates().LockForUpdate(hash)`. 404 if not found.
   4. If `state != "deployed"`: rollback, return 409.
   5. Decode `Spec` from the row's stored JSONB and validate `params` against `Spec.ParamsSchema` per existing validation logic.
   6. Generate a fresh UUID. Try `Instances().Insert(Instance{ID: uuid, TemplateHash: hash, InstanceKey: req.InstanceKey, Params: req.Params})`. On unique-constraint conflict: lookup existing instance via `(template_hash, instance_key)` and reuse its UUID.
   7. Commit the tx.
   8. Call `FanOutInstanceEvent(EventInstanceCreated, hash, instance_id, spec)`. On error: 5xx (instance row remains; retry-safe per §2.2).
   9. On success: 201 with `{instance_id, template_hash, ...}`.

3. Update existing instance routes (terminate, get, list) to use `template_hash` instead of `template_id`. The current schema reference in queries needs `JOIN rimsky_templates t ON t.id = i.template_hash`.

4. Update existing tests; add:
   - `TestInstanceCreate_RefusedWhenTemplateNotDeployed`: 409.
   - `TestInstanceCreate_IdempotentWithInstanceKey`: same key → same instance_id.
   - `TestInstanceCreate_ContentPinned`: create instance against tag; move tag; verify instance still bound to original hash.
   - `TestInstanceCreate_FiresOnInstanceCreated`: assert fake-store call recorded.

5. Verify build and tests pass:
   ```bash
   go test ./core/controlapi/... -count=1
   ```

---

## Task 18 — Wire instance terminal-state detection in scheduler/frame

**Files:** `core/scheduler/scheduler.go` (or `core/frame/runtick.go`).

### Steps

1. Locate the existing per-tick frame-end evaluation:
   ```bash
   grep -rn 'frame.RunTick\|terminal\|stale.*running' core/scheduler/ core/frame/
   ```

2. Identify the SQL or Go logic that flags a frame as terminal. At the same evaluation site, when a frame transitions to terminal AND no other frames remain `running` for the instance AND no `stale` or `running` rows exist in `rimsky_nodes` for the instance AND no claimed dispatch rows reference the instance, set `rimsky_instances.terminated_at = now()` for that instance via the new `Instances().MarkTerminated(ctx, instanceID, tx)` DAO method (Task 12). The DAO method's `WHERE terminated_at IS NULL` clause makes it idempotent.

3. Add a unit test in `core/frame/` (or wherever the predicate lives) verifying that an instance with all-terminal nodes receives `terminated_at` set, and a re-evaluation does not move the timestamp.

4. Verify scheduler/frame tests pass:
   ```bash
   go test ./core/scheduler/... ./core/frame/... -count=1
   ```

---

## Task 19 — Instance terminator background worker

**Files:** `core/controlapi/instance_terminator.go` (new), `core/controlapi/instance_terminator_test.go` (new), `core/controlapi/app.go`.

### Steps

1. Create `core/controlapi/instance_terminator.go`:
   ```go
   package controlapi

   import (
   	"context"
   	"log/slog"
   	"time"
   )

   // InstanceTerminator polls for instances marked terminated_at IS NOT NULL
   // that still have outstanding rimsky_store_lifecycle rows at scope='instance';
   // for each, fires OnInstanceTerminated to the relevant stores; on success,
   // deletes the lifecycle row.
   //
   // Per spec §2.4: at-least-once delivery; re-fire is OK because store handlers
   // are idempotent.
   type InstanceTerminator struct {
   	app          *App
   	pollInterval time.Duration
   	logger       *slog.Logger
   	stop         chan struct{}
   	done         chan struct{}
   }

   func NewInstanceTerminator(app *App, pollInterval time.Duration) *InstanceTerminator {
   	return &InstanceTerminator{
   		app:          app,
   		pollInterval: pollInterval,
   		logger:       slog.Default(),
   		stop:         make(chan struct{}),
   		done:         make(chan struct{}),
   	}
   }

   func (t *InstanceTerminator) Run(ctx context.Context) {
   	defer close(t.done)
   	ticker := time.NewTicker(t.pollInterval)
   	defer ticker.Stop()
   	for {
   		select {
   		case <-ctx.Done():
   			return
   		case <-t.stop:
   			return
   		case <-ticker.C:
   			t.tick(ctx)
   		}
   	}
   }

   func (t *InstanceTerminator) Stop() {
   	close(t.stop)
   	<-t.done
   }

   func (t *InstanceTerminator) tick(ctx context.Context) {
   	// 1. Query rimsky_instances WHERE terminated_at IS NOT NULL AND
   	//    EXISTS (SELECT 1 FROM rimsky_store_lifecycle WHERE
   	//        scope_kind='instance' AND scope_id=id::text);
   	//    LIMIT N (e.g., 100).
   	// 2. For each, look up the template's spec.
   	// 3. Call FanOutInstanceEvent(EventInstanceTerminated, template_hash,
   	//    instance_id, spec). FanOut handles per-store skip-if-already-deleted
   	//    (but for instance terminate the row is deleted on ack, so the next
   	//    poll won't re-send to acked stores).
   	// 4. Log per-store errors at WARN; the next tick retries.
   }
   ```

2. Implement `tick` against the new DAO method `Instances().ListTerminatedWithLifecycleRows(ctx, limit, nil)` from Task 12. Use limit=100 to bound per-tick work.

3. Add unit tests in `core/controlapi/instance_terminator_test.go`:
   - `TestTick_FiresOnInstanceTerminatedForTerminalInstance`: seed terminated instance + lifecycle row + fake store; run one tick; verify call recorded and row deleted.
   - `TestTick_NoOpWhenNoTerminalInstances`: empty DB; tick does nothing.
   - `TestTick_RetriesAfterFailure`: fake store errors first tick; lifecycle row remains; second tick succeeds and row is deleted.

4. Wire the terminator into the control-api app lifecycle in `core/controlapi/app.go`:
   - Construct on startup: `terminator := NewInstanceTerminator(app, 2*time.Second)`.
   - Spawn a goroutine: `go terminator.Run(ctx)`.
   - Stop on app shutdown.

5. Verify build and tests pass:
   ```bash
   go test ./core/controlapi/... -count=1
   ```

---

## Task 20 — Add lifecycle conformance checks to rimsky-executor-conformance

**Files:** `core/cmd/rimsky-executor-conformance/`.

### Steps

1. Inspect existing conformance check structure:
   ```bash
   ls core/cmd/rimsky-executor-conformance/
   cat core/cmd/rimsky-executor-conformance/main.go
   ```

2. Add six new conformance checks, one per lifecycle event. Each check:
   - Sends the lifecycle RPC to the configured store endpoint with a synthetic `template_id` (a fake hash like `"sha256-" + strings.Repeat("a", 64)`) and, for instance-scope events, a synthetic `instance_id`.
   - Asserts the call returns no error and an empty response message.
   - Reports pass/fail.

3. Add the checks to the conformance run order. They should run after Capabilities (existing) and before the runtime-verb checks. Pre-existing `--require-stub-mode` semantics apply unchanged.

4. Verify the conformance binary builds:
   ```bash
   go build ./core/cmd/rimsky-executor-conformance/...
   ```

5. Add a unit test in `core/cmd/rimsky-executor-conformance/` (or its existing test file) that brings up an in-process stub store via `stores/stub/testfixture.Start` (which returns a dialer and ephemeral port), runs the conformance suite programmatically against it, and asserts all six new lifecycle checks pass. This replaces an end-to-end shell-based smoke run for autonomous executability — no manual port wiring, no background processes, no `sleep`. Pattern:

   ```go
   func TestConformance_LifecycleEvents(t *testing.T) {
   	addr := testfixture.Start(t)              // returns "localhost:NNNN"
   	results := conformance.Run(t.Context(), conformance.Config{
   		Endpoint:  addr,
   		Transport: "grpc",
   	})
   	for _, r := range results {
   		if !r.Passed {
   			t.Errorf("conformance check %s failed: %s", r.Name, r.Reason)
   		}
   	}
   }
   ```

   If the conformance binary's main package does not currently expose `conformance.Run`, refactor the relevant entrypoint into an exported function that the test can call. The existing `--require-stub-mode` semantics remain.

6. Verify the conformance test passes:
   ```bash
   go test ./core/cmd/rimsky-executor-conformance/... -count=1
   ```

---

## Task 21 — End-to-end scenario test

**Files:** `test/scenarios/lifecycle/lifecycle_e2e_test.go` (new).

### Steps

1. Inspect the existing scenario test pattern:
   ```bash
   ls test/scenarios/
   cat test/scenarios/state_machine_same_state_rejected_test.go 2>/dev/null || cat test/scenarios/verify_before_run_race_test.go 2>/dev/null
   ```
   Identify how scenario tests construct `core/scenario.Start` and pre-launched store-services.

2. Create `test/scenarios/lifecycle/lifecycle_e2e_test.go`. The test exercises the full register → deploy → instantiate → terminate → undeploy → deregister cycle:
   - Bring up testcontainer Postgres.
   - Run all migrations.
   - Start a stub-mode store-service via `stores/stub/testfixture.Start` on an ephemeral port. The store records lifecycle event invocations.
   - Start a control-api `App` against the DB and the store registry containing the stub.
   - HTTP-POST register a template → assert OnTemplateRegistered called once.
   - HTTP-POST deploy → assert OnTemplateDeployed called once; row state='deployed'.
   - HTTP-POST instantiate → assert OnInstanceCreated called once; lifecycle row inserted.
   - Drive the instance to terminal (synthesize node states / drain frames).
   - Wait for the terminator tick → assert OnInstanceTerminated called; lifecycle row deleted.
   - HTTP-POST undeploy → assert OnTemplateUndeployed called; row state='undeployed'.
   - HTTP-DELETE → assert OnTemplateDeregistered called; rimsky_templates row gone; all lifecycle rows for this template gone.

3. Verify the scenario test runs:
   ```bash
   go test ./test/scenarios/lifecycle/... -count=1 -v
   ```

---

## Task 22 — Update rimsky.yml + supervisor-config + docker-compose + Helm chart

**Files:** `deploy/rimsky.yml` (rename of `deploy/stores.yml`), `deploy/supervisor-config.yml`, `deploy/docker-compose.yml`, `deploy/kubernetes/rimsky-chart/values.yaml`, `deploy/kubernetes/rimsky-chart/templates/configmap-stores.yaml` → `configmap-rimsky.yaml`.

### Steps

1. Rename and extend the stores config file:
   ```bash
   git mv deploy/stores.yml deploy/rimsky.yml
   ```
   Edit `deploy/rimsky.yml` to add the `executors:` block at the bottom, copying the executor entries from `deploy/supervisor-config.yml`. Update header comments to describe the unified shape per spec §3.1.

2. Edit `deploy/supervisor-config.yml`:
   - Remove the `executors:` block entirely.
   - Update the header comment to describe the file's narrowed scope (per-process supervisor tuning).

3. Edit `deploy/docker-compose.yml`:
   - For all three rimsky services (control-api, supervisor, scheduler), replace `RIMSKY_STORES_CONFIG=/etc/rimsky/stores.yml` with `RIMSKY_CONFIG=/etc/rimsky/rimsky.yml`.
   - Update the volume mount mapping `./stores.yml:/etc/rimsky/stores.yml` to `./rimsky.yml:/etc/rimsky/rimsky.yml`.
   - Verify the supervisor container still mounts `supervisor-config.yml`.

4. Edit `deploy/kubernetes/rimsky-chart/`:
   ```bash
   ls deploy/kubernetes/rimsky-chart/templates/
   ```
   Rename `configmap-stores.yaml` → `configmap-rimsky.yaml`. Update its `data:` key from `stores.yml` to `rimsky.yml` and its mount path. Update `values.yaml` defaults to include the executors block. Update any deployment/statefulset templates that reference `RIMSKY_STORES_CONFIG`.

5. Verify the docker-compose build still works:
   ```bash
   docker compose -f deploy/docker-compose.yml config
   ```
   This validates the compose file syntactically (it does not start the stack).

6. Build all three rimsky Docker images:
   ```bash
   docker compose -f deploy/docker-compose.yml build rimsky-control-api rimsky-supervisor rimsky-scheduler
   ```

---

## Task 23 — Update smoke fixture

**Files:** `test/smoke/setup.go`.

### Steps

1. Inspect:
   ```bash
   cat test/smoke/setup.go | head -80
   ```

2. Update any `RIMSKY_STORES_CONFIG` references to `RIMSKY_CONFIG`. Update default file paths (`stores.yml` → `rimsky.yml`). If the smoke fixture starts the rimsky processes inline (not via docker compose), update the YAML loaded to include the unified shape.

3. Update any in-memory store-service registration to satisfy the new `Store` interface (likely already covered by Task 6's storetest changes if smoke uses `storetest.Fake`; but smoke may have its own fake — if so, embed `store.LifecycleNoop`).

4. Verify the smoke test compiles:
   ```bash
   go build ./test/smoke/...
   ```
   If smoke is gated behind a build tag (e.g., `//go:build smoke`), build with that tag.

---

## Task 24 — Run full Go check + lint

**Files:** none — verification step.

### Steps

1. Run a full module build:
   ```bash
   go build ./...
   ```
   Expect zero errors.

2. Run `go mod tidy`:
   ```bash
   make tidy
   ```

3. Run `go vet`:
   ```bash
   go vet ./...
   ```

4. Run `make lint`:
   ```bash
   make lint
   ```
   Fix any reported issues.

5. Run the full test suite (sequentially; some scenario tests are heavy):
   ```bash
   go test ./...
   ```

6. Run race-sensitive packages with `-race`:
   ```bash
   go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... ./core/controlapi/... -race -count=3
   ```

---

## Task 25 — Update docs/protocol.md

**Files:** `docs/protocol.md`.

### Steps

1. Inspect the current shape:
   ```bash
   head -80 docs/protocol.md
   grep -n 'Capabilities\|Open\|Commit\|Abandon\|Release' docs/protocol.md
   ```

2. Add a new section documenting the six lifecycle RPCs after the runtime-verbs section. Each method gets:
   - Wire signature (request and response messages).
   - Semantic description.
   - Idempotency requirement.
   - HTTP+JSON bridge path.

3. Add a paragraph documenting the `OpenRequest` `template_id` and `instance_id` fields, noting they are opaque to rimsky and rimsky-inert.

4. Update any references to "4 verbs" or "the four runtime verbs" to clarify that the protocol now has 4 runtime verbs + 6 lifecycle event verbs + Capabilities.

5. Verify the doc builds (no special build for markdown; just inspection):
   ```bash
   wc -l docs/protocol.md
   ```

---

## Task 26 — Update docs/operator-guide.md

**Files:** `docs/operator-guide.md`.

### Steps

1. Add a `rimsky.yml` section describing the unified shape per spec §3.1. Replace any `stores.yml` references with `rimsky.yml`.

2. Add subsections documenting:
   - `RIMSKY_CONFIG` env var (was `RIMSKY_STORES_CONFIG`).
   - The four-state template lifecycle (registered → deployed → undeployed → deregistered) and the corresponding API endpoints.
   - Tag operations (`POST /v1/tags`, `PUT /v1/tags/{tag}`, etc.).
   - Deploy-vs-instantiate distinction: a template must be deployed before any instance can be created.
   - Instance content-pinning: tag movement does not migrate live instances.

3. Remove any references to `consumer_key` (now `instance_key`) and `template_id UUID` (now `template_hash TEXT` in API context).

---

## Task 27 — Update docs/store-author-guide.md

**Files:** `docs/store-author-guide.md`.

### Steps

1. Add a section "Lifecycle events" after the runtime-verbs section. Document:
   - The six methods every store-service must implement.
   - The default no-op pattern (return success immediately) for stores that don't react.
   - The idempotency requirement: each method must be safe to call twice with the same scope IDs.
   - A code example using `store.LifecycleNoop` embedded in a Go store impl.

2. Document the `Open` scope envelope (`template_id`, `instance_id`) and that store-services may use it for namespace routing or ignore it.

---

## Task 28 — Update docs/glossary.md

**Files:** `docs/glossary.md`.

### Steps

1. Refresh entries for "template," "instance," "tag," "deploy," "undeploy," "register," "deregister."

2. Remove any "substore" v2 vocabulary (the v3 redesign already mostly handled this — verify no straggling references).

3. Add new entries:
   - "lifecycle event" — the six store-protocol RPCs fired at template/instance state transitions.
   - "scope envelope" — the `(template_id, instance_id)` pair on `OpenRequest`.
   - "content hash" — the canonical SHA-256 of an RFC 8785 JCS-canonicalized template spec; serves as the template's identity.

---

## Task 29 — Update docs/architecture.md, docs/2026-04-26-control-layer.md, docs/2026-05-01-auth-and-multitenancy.md

**Files:** `docs/architecture.md`, `docs/2026-04-26-control-layer.md`, `docs/2026-05-01-auth-and-multitenancy.md`.

### Steps

1. `docs/architecture.md`: Add a one-line mention of the new packages (`core/canonical/`, `core/controlapi/lifecycle.go`, `rimsky_store_lifecycle` table). Add a pointer to the spec.

2. `docs/2026-04-26-control-layer.md`: Add forward references to this spec at the top of §1 and §2 (since the spec lands their normative content). Light edits to align terminology with the spec's exact event names where the doc had differed.

3. `docs/2026-05-01-auth-and-multitenancy.md`: Update the `declared_metadata` references in §3.1 to reflect option α (no template-side metadata; tenant routing flows through userdata or operator-wired per-tenant store-service registrations).

---

## Task 30 — Update CHANGELOG.md and CLAUDE.md

**Files:** `CHANGELOG.md`, `CLAUDE.md`.

### Steps

1. `CHANGELOG.md`: Append a bullet under `## Unreleased`:
   ```
   - **Control-plane v1 + store lifecycle protocol.** Templates are now content-addressed (`rimsky_templates.id` is `sha256-<64-hex>` over RFC 8785 JCS-canonicalized spec); tags are movable aliases in `rimsky_template_tags`. Four-state template lifecycle (registered/deployed/undeployed/deregistered). Six new RPCs on `StoreService` (`OnTemplateRegistered`/`Deployed`/`Undeployed`/`Deregistered` + `OnInstanceCreated`/`Terminated`); all stores implement all six. `OpenRequest` gains `template_id` and `instance_id` fields. Bookkeeping in `rimsky_store_lifecycle`. Unified `rimsky.yml` (`RIMSKY_CONFIG`) replaces `RIMSKY_STORES_CONFIG`; declares stores, named_locks, and executors in one place. Control-api gains `ExecutorDeclared` validation hook. Per `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`. Pre-v1: drop+recreate of `rimsky_templates`/`rimsky_instances`; existing dev DBs nuked.
   ```

2. `CLAUDE.md`: Add gotcha entries under the existing "Non-obvious gotchas" section:
   - "**`RIMSKY_CONFIG` is the unified deployment-shape config.** Replaces `RIMSKY_STORES_CONFIG`. Loaded by control-api, supervisor, and scheduler. Declares stores, named_locks, and executors; per-process tuning still lives in per-process configs (e.g., `RIMSKY_SUPERVISOR_CONFIG`)."
   - "**Templates are content-addressed.** `rimsky_templates.id` is `sha256-<64-hex>` over JCS-canonicalized spec. Tags in `rimsky_template_tags` are movable aliases. Re-registering the same spec is a cheap no-op. Tag movement does not migrate live instances — instance is bound to the resolved hash at creation."
   - "**Lifecycle events fire from control-api, not supervisor.** The six events (`OnTemplateRegistered/Deployed/Undeployed/Deregistered/OnInstanceCreated/Terminated`) are RPCed synchronously by the control-api at state transitions. The supervisor never fires lifecycle events. Instance-terminated events fire from a control-api background terminator goroutine that polls `rimsky_instances.terminated_at`."
   - "**All stores implement all six lifecycle methods.** No subscription model in `Capabilities` — stores that don't care embed `store.LifecycleNoop` (default no-op)."
   - "**Instances bind to `template_hash` at creation.** Old `template_id UUID` is gone; FK is now `template_hash TEXT`. `consumer_key` renamed to `instance_key` and is nullable."

3. Verify the docs files have no broken cross-references:
   ```bash
   grep -rn 'stores\.yml\|RIMSKY_STORES_CONFIG\|consumer_key\|substores' docs/ CLAUDE.md CHANGELOG.md 2>/dev/null
   ```
   Each remaining hit is either a deliberate historical reference (already in older spec docs) or a missed update. Audit each and fix.

---

## Task 31 — Final verification pass

**Files:** none — verification step.

### Steps

1. Build everything:
   ```bash
   go build ./...
   ```

2. Run the full test suite:
   ```bash
   go test ./...
   ```

3. Run lint:
   ```bash
   make lint
   ```

4. Run race-sensitive package tests:
   ```bash
   go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... ./core/controlapi/... -race -count=3
   ```

5. Run scenario tests including the new lifecycle scenario:
   ```bash
   go test ./test/scenarios/... -count=1
   ```

6. Build all three rimsky Docker images:
   ```bash
   docker compose -f deploy/docker-compose.yml build rimsky-control-api rimsky-supervisor rimsky-scheduler
   ```

7. Build the standalone store-service Docker images touched by Task 7:
   ```bash
   docker compose -f deploy/docker-compose.yml build store-postgres store-filesystem
   ```

8. Tidy and verify:
   ```bash
   make tidy
   go build ./...
   ```

9. Confirm no straggling old-vocabulary references:
   ```bash
   grep -rn 'RIMSKY_STORES_CONFIG\|consumer_key\b' core/ test/ stores/ executors/ deploy/ 2>/dev/null | grep -v '^\(./\)\?docs/specs/2026-04'
   ```
   Output should be empty (or only contain matches in archived spec docs that intentionally retain old vocabulary as historical context).

---

## Manual checks after completion

The following items cannot be expressed as automated commands and are for the user to run after this plan completes:

1. **Visual review of the updated docs.** Skim `docs/operator-guide.md`, `docs/store-author-guide.md`, `docs/glossary.md`, and the CHANGELOG entry for clarity and accuracy. The spec text is the source of truth; doc updates should match.

2. **Bring up the docker-compose stack and exercise the smoke endpoint.** The plan builds the Docker images; the user should run:
   ```bash
   docker compose -f deploy/docker-compose.yml up -d
   curl http://localhost:8080/health
   docker compose -f deploy/docker-compose.yml down -v
   ```
   Confirm the stack reaches healthy and that the unified `rimsky.yml` is mounted correctly into all three rimsky containers.

3. **Helm-chart lint.** If the operator uses Helm, run `helm lint deploy/kubernetes/rimsky-chart/` against the updated chart and confirm it passes. (Beyond the scope of this plan since Helm is operator-side tooling.)

4. **Cross-deployment coordination check.** Per spec §6.6, `rimsky.yml` is operator-managed and expected to roll uniformly across all three rimsky processes. Ops should verify their deployment pipeline rolls them coherently (no operator-facing change here, just a behavioral note).
