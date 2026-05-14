# Control Plane & Store Lifecycle v1 — Design

## Status

- Spec, 2026-05-01.
- Outcome of the 2026-05-01 brainstorm covering scope (b) of the design notes (`docs/2026-04-26-control-layer.md` and `docs/2026-05-01-auth-and-multitenancy.md`).
- Foundational dependencies:
  - `docs/specs/2026-04-27-stores-redesign-v3-design.md` — the v3 store contract this spec extends.
  - `docs/specs/2026-04-30-stores-protocol-cleanup-design.md` — the cleanup overlay that produced the current 4-verbs + Capabilities baseline.
  - `docs/2026-04-26-control-layer.md` — the strategic design notes for §1 (lifecycle events) and §2 (registry shape).
  - `docs/2026-05-01-auth-and-multitenancy.md` — auth-blind v1 stance; substrate-supported multi-tenancy is a downstream consequence of §5 below.
- Companion follow-on: a separate brainstorm/spec will cover `rimsky-cli` and `rimsky-compose.yml` once this surface is shipped. That work is out of scope here (§11.1).

## Context

Rimsky's runtime (scheduler + supervisor + executor protocol) is well-defined. The control-api's surface is partly shipped (template/instance endpoints exist; stores config is loaded by all three processes) and partly missing (no template-state machine, no store-side lifecycle events, no content-addressed registry, no executor-validation hook on template registration).

This spec lands the control-plane v1 in five coupled changes:

1. **Template registry overhaul** — content-addressed identity, movable tags, four-state lifecycle, deterministic content hashing.
2. **Instance endpoint touch-up** — content-pinned template binding, generic terminology, state-machine enforcement.
3. **Unified deployment config** — `rimsky.yml` (`RIMSKY_CONFIG`) supersedes `RIMSKY_STORES_CONFIG`; declares stores, named_locks, and executors in one place; all three processes load it; control-api gains an `ExecutorDeclared` validation hook.
4. **Store lifecycle event protocol** — six new RPCs on `StoreService` (`OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`) plus a scope envelope (`template_id`, `instance_id`) on `Open`. Provisioning policy lives entirely in store-service config; templates declare nothing about scope or provisioning.
5. **Bookkeeping** — a `rimsky_store_lifecycle` table tracking last-acked event delivery state per (store, scope) pair.

The architectural backbone:

- **Templates think in stores, not substores.** A template references store names; the operator decides whether each store-service is shared, per-template, per-instance, per-tenant, or anything else. The same template runs against any deployment shape.
- **Rimsky is fully inert about provisioning.** Lifecycle events carry only scope IDs (`template_id`, `instance_id`); store-services hold all policy. Rimsky never inspects template substore declarations because there are none.
- **All stores implement all six lifecycle methods.** No subscription model. Stores that don't care about an event return success immediately. Capabilities stays single-field (`write_semantics`).
- **Lifecycle events are synchronous.** Every transition fires its events to all relevant stores; the API endpoint blocks until all stores ack or any store errors. Caller-driven retry; idempotent re-fire gated by `rimsky_store_lifecycle`.
- **Templates are content-addressed.** Canonical identity is `sha256-<hex>` over RFC 8785 JCS-canonicalized spec JSON. Tags are movable aliases over those identities (Docker model). Re-registering the same spec is a cheap no-op.

What this spec does not cover (see §11):

- CLI surface, `rimsky-cli` commands, `rimsky-compose.yml`.
- Auth, principal field, policy hook, ACLs.
- Audit logging.
- Package manager / OCI distribution.
- Quotas.
- Sub-graph composability.

---

## 1. Template registry

### 1.1 Identity model

Every template has exactly one canonical identity: a content hash of its spec, computed at registration time. Tags are operator-managed aliases pointing at hashes; many tags can point at one hash; one tag points at exactly one hash; tags can be moved to a different hash via an explicit operation.

Tag-vs-hash disambiguation: a token of the form `^sha256-[0-9a-f]{64}$` is a hash; anything else is a tag. Tag identifiers must satisfy `^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$` (rejects hash-shaped tags by construction; permits version-shaped tags like `ingest@1.2.0`).

Re-registering a spec whose hash already exists in `rimsky_templates` is idempotent: no row insertion, no event fired. Tags supplied alongside re-registration are added/moved as if they were tag-management calls.

### 1.2 Schema

```sql
CREATE TABLE rimsky_templates (
    id              TEXT PRIMARY KEY,                                   -- 'sha256-<64-hex>'
    spec            JSONB NOT NULL,                                     -- canonical normalized spec
    state           TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed')),
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    source          TEXT NOT NULL DEFAULT 'direct'                      -- 'direct' | future package-manager values
);

CREATE TABLE rimsky_template_tags (
    tag             TEXT PRIMARY KEY,
    template_id     TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rimsky_template_tags_template_id ON rimsky_template_tags(template_id);
```

Notes:

- The `spec` column holds the JSONB representation that produced the hash. JSONB normalization (key reordering, whitespace stripping) by Postgres is fine — the hash is computed before insertion against the canonicalized text and stored separately as the `id`. Postgres-side re-canonicalization does not affect identity.
- `state` is the *template's* lifecycle state (registry view). The per-store delivery state lives in `rimsky_store_lifecycle` (§5).
- `source` is forward-compatible metadata for the package manager (when it lands). Default is `'direct'`. v1 records but does not consult.
- `ON DELETE RESTRICT` on the FK forces explicit cleanup ordering: a template can only be deleted when no tags reference it. Tag deletion does not cascade to the template; only operator-driven template deletion (after the last tag is removed) actually deletes the template row.

### 1.3 Content hashing

Canonicalization: RFC 8785 JSON Canonicalization Scheme (JCS). Sorts object keys lexicographically by code point, normalizes JSON numbers to their shortest unambiguous representation, normalizes string escapes to the minimal-escape form. The library `github.com/cyberphone/json-canonicalization` is vendored into the repo.

What is hashed: the JSON form of the validated `node.TemplateSpec` only. Specifically:

- The `templateDeployRequest` is decoded and converted to `node.TemplateSpec` via the existing `toTemplateSpec` mapper.
- `node.ApplyFrameResolutionDefaults` is applied (so the persisted spec carries resolved defaults).
- The defaulted spec is marshalled to JSON via stdlib `encoding/json`.
- The marshalled bytes are canonicalized via JCS.
- The canonicalized bytes are hashed with SHA-256.
- The hex-encoded result is prefixed with `sha256-` to produce the `id`.

What is *not* hashed: tags, `source`, `registered_at`, request envelope fields outside the spec body. Two registrations of identical spec content under different tag/source values produce the same `id`.

Determinism is contractual: any change to canonical JSON output (Go's `encoding/json` ordering, JCS library upgrades) that would change hashes for previously-registered specs must be treated as a breaking change. The vendored JCS library version is pinned in `go.mod`.

### 1.4 State machine

Template lifecycle states and legal transitions:

```
[absent]
   │ POST /v1/templates                              → registered
   ▼
registered  ──── POST /v1/templates/.../deploy ────→  deployed
   │                                                    │
   │   ┌──────── POST /v1/templates/.../undeploy ───────┘
   │   ▼
   │ undeployed ─── POST /v1/templates/.../deploy ──→  deployed (cycle continues)
   │   │
   │   │
   ▼   ▼
   DELETE /v1/templates/...                         → [absent]
```

Invariants:

- A template is `deployed` if and only if `OnTemplateDeployed` has acked from every store its nodes reference and `OnTemplateUndeployed` has not yet been acked since.
- A template can transition `registered → deployed → undeployed → deployed → undeployed → …` repeatedly. Each `deploy`/`undeploy` is a discrete event cycle from each store's perspective.
- A template can be deregistered (`DELETE`) from either `registered` or `undeployed` state, and only when no instances reference it. Deregister from `deployed` is refused (HTTP 409); the operator must undeploy first. Deregister with live instances is refused (HTTP 409 with the offending instance IDs).
- A template can be deleted via tag deletion only when (a) the deleted tag is the last one pointing at it, AND (b) the template is in `registered` or `undeployed` state, AND (c) no instances reference it. Deletion of a non-final tag does not delete the template.
- Re-deploy from `deployed` is a no-op (200, no events fired). Re-undeploy from `undeployed` likewise. Re-register the same content hash likewise.

There is no explicit `undeployed → registered` transition needed; both states are valid points from which to deregister or to re-deploy.

#### 1.4.1 Why six events (not four)

The original design notes (`docs/2026-04-26-control-layer.md` §1.1) enumerated four events. The 2026-05-01 brainstorm expanded to six (added `OnTemplateRegistered` and `OnTemplateDeregistered`) so that the lifecycle vocabulary mirrors the operator-facing transitions exactly: register/deregister are registry-membership events; deploy/undeploy are activation events. The semantic distinction matters for store-services that want to participate in registry-membership without doing per-deploy work — e.g., a catalog-style store-service that indexes templates for discovery on `OnTemplateRegistered` (cheap; no resource allocation) and does its real work on `OnTemplateDeployed`. Most store-services collapse these and treat `OnTemplateRegistered` as a no-op; the events stay distinct so the option exists.

### 1.5 API endpoints

The request body for `POST /v1/templates` wraps the existing template-spec body shape so that registration metadata (tag, source) lives outside the spec — a clean separation since only the spec contributes to the content hash:

```
{
  "spec":  { ...existing templateDeployRequest fields... },
  "tag":   "ingest@1.2.0",          // optional; tag identifier per §1.1
  "source": "direct"                 // optional; default 'direct'
}
```

The endpoints:

```
POST   /v1/templates
       body: { spec, tag?, source? }
       → 201 { template_id (hash), tag (if any), state: 'registered' }
       Validates spec (existing node.ValidateTemplate plus new ExecutorDeclared hook).
       Computes hash. Order:
         1. If a row with this hash already exists in rimsky_templates:
            short-circuit. No re-validation of lifecycle rows; the existence of
            the rimsky_templates row guarantees prior fan-out completed (since
            the row is only inserted after all OnTemplateRegistered acks per
            §5.4). Tag (if supplied) is created or moved as a separate operation.
            Return 200.
         2. If no row exists: fire OnTemplateRegistered to every distinct store
            the template's nodes reference. Per §5.2, skip stores already at
            'registered' for this hash in rimsky_store_lifecycle (allows retry
            after partial-failure to resume from where it left off without
            re-firing already-acked stores).
         3. After all stores ack: INSERT the rimsky_templates row at
            state='registered'. Tag (if supplied) is created or moved.
         4. Return 201.
       On any store-ack failure between steps 2 and 3: abort with HTTP 5xx
       listing the failed stores. The rimsky_templates row is NOT inserted; the
       rimsky_store_lifecycle rows for stores that did ack remain (idempotent
       for retry). The caller retries the same request; rimsky finds the
       partial lifecycle progress and resumes.

GET    /v1/templates
       query: ?tag= (substring filter), ?state=, ?cursor=, ?limit=
       → 200 { templates: [...], next_cursor }
       Each item: { template_id, tags, state, registered_at, source }.

GET    /v1/templates/{tag_or_hash}
       → 200 { template_id, tags: [...], state, registered_at, source, spec }
       Resolves tag to hash if needed; 404 if neither tag nor hash matches.

POST   /v1/templates/{tag_or_hash}/deploy
       → 200 { template_id, state: 'deployed' }
       Refused (409) unless current state is 'registered' or already 'deployed'.
       Fires OnTemplateDeployed to every distinct store the template's nodes
       reference. Synchronous; all-or-nothing; idempotent.

POST   /v1/templates/{tag_or_hash}/undeploy
       → 200 { template_id, state: 'undeployed' }
       Refused (409) unless current state is 'deployed' or already 'undeployed'.
       Refused (409) if any instances bound to this template are not in a
       terminal state. Fires OnTemplateUndeployed.

DELETE /v1/templates/{tag_or_hash}
       → 200 { deleted: true }
       Tag form: if other tags still point at the same hash, the response is
       { deleted: true, tag_only: true }. Only when removing the last tag (or
       when called via hash with no tags) is the template row deleted, and
       only if state ∈ {registered, undeployed} and no instances reference it.
       Fires OnTemplateDeregistered prior to the row deletion.

POST   /v1/tags
       body: { tag, template: tag_or_hash }
       → 201 { tag, template_id }
       Creates a new tag pointing at the resolved template's hash. Refused (409)
       if the tag already exists.

GET    /v1/tags
       query: ?cursor=, ?limit=
       → 200 { tags: [{tag, template_id, updated_at}], next_cursor }

PUT    /v1/tags/{tag}
       body: { template: tag_or_hash }
       → 200 { tag, template_id }
       Moves an existing tag to point at the resolved template's hash.

DELETE /v1/tags/{tag}
       → 200 { deleted: true }
       Removes the tag-to-hash mapping. Does not delete the underlying template
       (use DELETE /v1/templates/{hash} for that).
```

Path resolution: `{tag_or_hash}` is a hash if the value matches `^sha256-[0-9a-f]{64}$`; otherwise it is a tag. Both match the same routes; the handler dispatches.

### 1.6 GC and retention

V1 does not GC templates. Operators delete templates explicitly. A future TTL-sweeper concept (mentioned in `docs/2026-04-26-control-layer.md` §2.5) is deferred; spec session for that later.

`rimsky_store_lifecycle` rows for `terminated` instances and `deregistered` templates are deleted immediately when the corresponding template/instance row is deleted (no historical retention). Audit-log shape lands with `docs/2026-05-01-auth-and-multitenancy.md` §2 and is out of scope here.

#### 1.6.1 Orphan lifecycle rows from aborted registration

If `POST /v1/templates` aborts mid-fan-out (some stores acked `OnTemplateRegistered`, the request failed before all acks), the `rimsky_templates` row is not inserted but `rimsky_store_lifecycle` rows exist for the partially-acked stores. These rows are tolerated:

- Re-submitting the same registration request resumes from where it left off; on full success, the template row is inserted normally.
- If the operator gives up (never re-submits), the rows persist as harmless orphans (they reference a `scope_id` for which no template row will ever exist).

A v1 cleanup mechanism is not specified. Orphans accumulate proportionally to abandoned registrations, which is operator behavior and will be small in practice. A future operator endpoint or sweeper to scrub orphan lifecycle rows for nonexistent template hashes is a candidate for a later spec.

---

## 2. Instances (touched up)

### 2.1 Schema

```sql
CREATE TABLE rimsky_instances (
    id              UUID PRIMARY KEY,
    template_hash   TEXT NOT NULL REFERENCES rimsky_templates(id) ON DELETE RESTRICT,
    instance_key    TEXT,                                       -- nullable; renamed from consumer_key
    params          JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (template_hash, instance_key)
);
```

Changes from existing:

- `template_id UUID` → `template_hash TEXT` (FK to the new hash-keyed `rimsky_templates`).
- `consumer_key NOT NULL` → `instance_key TEXT` (nullable). Postgres's default unique-constraint semantics treat NULLs as distinct, so multiple rows with `(template_hash=X, instance_key=NULL)` coexist (the constraint formally applies but does not collide on NULL). This is the desired behavior — it lets multiple keyless instances of the same template exist while still enforcing uniqueness when an `instance_key` is supplied.
- The unique constraint is `UNIQUE (template_hash, instance_key)`.

Other tables in the schema referencing `rimsky_templates(id) UUID` are updated to reference the hash text instead (none currently; verify during migration). Tables referencing `rimsky_instances(id)` are unchanged (instance ID stays UUID).

### 2.2 API touch-up

```
POST   /v1/instances
       body: { template: tag_or_hash, params, instance_key? }
       → 201 { instance_id, template_hash, ... }
       Refused (409) if the resolved template is not in state 'deployed'.
       Resolves tag to hash at creation; instance is content-pinned —
       moving the tag later does not migrate the instance to a new hash.
```

Order of operations to make `instance_key`-keyed retries truly idempotent and to close the deploy-state TOCTOU window against concurrent undeploy:

1. Begin a database transaction. `SELECT ... FOR UPDATE` on the `rimsky_templates` row for the resolved `template_hash`. This locks the template row against concurrent state changes (deploy / undeploy / deregister) for the duration of the instance-creation transaction.
2. Verify the template is in `state='deployed'`. If not, abort with HTTP 409.
3. Validate `params` against the spec stored at `rimsky_templates.spec` for the resolved `template_hash` (not the tag's current target — see §2.3 on content pinning).
4. Generate a fresh instance UUID. INSERT the `rimsky_instances` row. If `instance_key` is supplied and a row already exists with `(template_hash, instance_key)`, the INSERT fails on the unique constraint; the handler looks up the existing row, reuses its `instance_id`, and proceeds to step 5 (idempotent resume).
5. COMMIT the transaction. The instance row is now durably present; the template row's deploy-state lock is released.
6. Fire `OnInstanceCreated` to every distinct store the template's nodes reference. Per §5.2, skip stores already at `created` for this `instance_id` (idempotent retry).
7. After all stores ack: return 201.

The `SELECT ... FOR UPDATE` at step 1 is the rimsky-side enforcement of the §1.4 invariant "instances are only created against deployed templates." A concurrent `POST /v1/templates/{tag}/undeploy` arriving during steps 2–5 blocks on the row lock until the instance INSERT commits; the undeploy then proceeds, but the just-committed instance row prevents the undeploy from completing (per the §1.5 undeploy validation: refused if any non-terminal instances reference the template).

The lock serializes concurrent instance creations against the same template briefly (lock held only for steps 1–5; step 6's event-firing happens after lock release). For v1 this is fine; if profiling shows contention, the implementation can switch to `FOR NO KEY UPDATE` (weaker; still prevents concurrent state-changing transactions) or split the validation and INSERT into separate small transactions with optimistic retry.

Failure semantics: on any store-ack failure at step 6, the handler returns HTTP 5xx with the failed stores listed. The `rimsky_instances` row was durably committed at step 5 and remains; partially-acked `rimsky_store_lifecycle` rows remain. The caller retries the same request (with the same `instance_key`); rimsky finds the existing row, resumes firing only not-yet-acked stores, and returns 200 (or 5xx if it fails again).

Concurrent `POST /v1/instances` with the same `(template_hash, instance_key)` resolve cleanly: one wins the INSERT, the other gets the unique-constraint conflict, looks up the existing row, and proceeds against the same `instance_id`. Both callers see the same final state.

Calls without `instance_key` are not idempotent: each generates a new UUID. Caller is responsible for retry-driven dedup if it cares (typically via providing `instance_key`).

Existing instance routes (terminate, query state, etc.) are touched only where they reference the renamed fields. No semantic change beyond the state-validation tightening and the rename.

### 2.3 Tag movement and live instances

If tag `T` points at hash `A`, instance `X` is created bound to `A`. The operator later moves `T` to point at hash `B`. Instance `X` remains bound to `A` (its `template_hash` column does not change). New instances created via `T` get `B`. This mirrors Docker exactly — `docker run image:tag` resolves the tag once at run-time; the running container is bound to the image_id.

This is a deliberate design property: tag movement does not migrate live workloads. Operators wanting workloads to follow a tag terminate and re-instantiate.

### 2.4 Instance terminal events

`OnInstanceTerminated` fires when an instance reaches a terminal state. The exact trigger condition is:

- All `rimsky_nodes` rows for the instance are in `fresh` or `failed` state (no `stale`, no `running`), AND
- No `rimsky_dispatch` rows are claimed for the instance, AND
- No frame is in `running` state for the instance.

The spec does not prescribe the exact mechanism by which `OnInstanceTerminated` is fired (scheduler tick log, dedicated column on `rimsky_instances`, watcher on node-state transitions). Implementation chooses the cheapest correct mechanism. The contract is **at-least-once delivery** with handler-side idempotency (the same idempotency requirement that applies to all the other lifecycle events per §5.4): the implementation may legitimately re-fire `OnInstanceTerminated` after a control-api restart or supervisor crash, and store-services must tolerate the re-fire as a no-op once they have already processed the terminal transition for that `instance_id`.

The implementation tracks delivery state via `rimsky_store_lifecycle` rows for `(store_registration_name, 'instance', instance_id)`: rows present at `state='created'` with the underlying instance in terminal mode are candidates for `OnInstanceTerminated`; on successful ack, the row is deleted. A re-fire after restart finds the row still present and re-attempts the event; the store-service no-ops on duplicate.

This is at-least-once because exactly-once across a distributed boundary requires either coordinated transactions or a separate consensus mechanism — neither is justified by the use case here. Idempotent handlers cost nothing and remove the failure mode.

---

## 3. Unified deployment config: `rimsky.yml`

### 3.1 File shape

```yaml
# rimsky.yml — the deployment shape config. Loaded by all three rimsky
# processes (rimsky-control-api, rimsky-supervisor, rimsky-scheduler) at
# startup. Per-process tuning lives in per-process config files alongside.

stores:
  pg_workspace:
    endpoint: "grpc://store-postgres:9101"
    capabilities:
      write_semantics: direct
  fs_artifacts:
    endpoint: "grpc://store-filesystem:9100"
    capabilities:
      write_semantics: direct

named_locks:
  "pg_workspace:concurrent-claims": { limit: 5 }
  model-budget:                     { limit: 50 }

executors:
  claude-agent:
    transport: grpc
    endpoint: "claude-agent:9090"
    tls: off
  http-node:
    transport: grpc
    endpoint: "http-node:9091"
    tls: off
```

The three blocks supersede the prior split:

- `stores` — was the top-level shape of `RIMSKY_STORES_CONFIG`.
- `named_locks` — was the second top-level block of `RIMSKY_STORES_CONFIG`.
- `executors` — was inside `RIMSKY_SUPERVISOR_CONFIG`.

### 3.2 Env var

`RIMSKY_CONFIG` replaces `RIMSKY_STORES_CONFIG`. All three rimsky binaries read it at startup. The default path is `/etc/rimsky/rimsky.yml`.

`RIMSKY_STORES_CONFIG` is removed (pre-v1, no compat shim). The reference docker-compose stack and Helm chart are updated.

### 3.3 Per-process tuning configs

The supervisor still has `RIMSKY_SUPERVISOR_CONFIG` for tuning knobs, minus the `executors:` block which moves to `rimsky.yml`:

```yaml
# supervisor-config.yml — per-process tuning. NOT the deployment shape.
postgres_url: "${RIMSKY_DB_URL}"
concurrency: 8
heartbeat_interval_ms: 5000
claim_poll_interval_ms: 1000
callback:
  host: 0.0.0.0
  port: 9100
  advertise_host: ${CALLBACK_ADVERTISE_HOST:-}
```

Scheduler and control-api have their own per-process configs (postgres_url, listen ports, tick intervals). Unchanged in shape; unchanged in env-var names.

### 3.4 Validation hooks on template registration

The control-api's existing `node.ValidateTemplate` is called via `validatorHooksFor(deps)`. The hooks are extended:

- `StoreDeclared` (existing) — checked against the unified config's `stores:` block.
- `NamedLockDeclared` (existing) — checked against `named_locks:`.
- **`ExecutorDeclared` (NEW)** — checked against `executors:`. Templates referencing an undeclared executor are rejected at registration time (HTTP 400 with `validation_errors`), not deferred to runtime.

This closes the gap noted in the brainstorm: today's control-api silently accepts templates with undeclared executor names; failure surfaces only when the supervisor tries to dispatch.

### 3.5 Startup validation

All three rimsky processes parse `rimsky.yml` at startup and:

- Dial each `stores:` entry; run the `Capabilities()` handshake; validate strict equality against the operator-declared `write_semantics`. Failure → fail-fast at startup. (Existing behavior, unchanged.)
- Cache the executor-name set; control-api uses it for `ExecutorDeclared` validation. Supervisor uses the full `executors:` block to wire its dispatch clients. Scheduler reads but does not use directly (loads for symmetry; future use).

No `Capabilities.LifecycleEvents` validation: the protocol requires every store to implement all six lifecycle methods (§4.4).

---

## 4. Wire protocol

### 4.1 Six new lifecycle RPCs on `StoreService`

Added to `proto/v1/store_service.proto`:

```protobuf
service StoreService {
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);

  // NEW lifecycle events. Every store-service implements all six;
  // stores that don't care return success immediately.
  rpc OnTemplateRegistered(OnTemplateRegisteredRequest) returns (OnTemplateRegisteredResponse);
  rpc OnTemplateDeployed(OnTemplateDeployedRequest)     returns (OnTemplateDeployedResponse);
  rpc OnTemplateUndeployed(OnTemplateUndeployedRequest) returns (OnTemplateUndeployedResponse);
  rpc OnTemplateDeregistered(OnTemplateDeregisteredRequest) returns (OnTemplateDeregisteredResponse);
  rpc OnInstanceCreated(OnInstanceCreatedRequest)       returns (OnInstanceCreatedResponse);
  rpc OnInstanceTerminated(OnInstanceTerminatedRequest) returns (OnInstanceTerminatedResponse);

  // Existing 4 runtime verbs, unchanged in semantics.
  rpc Open(OpenRequest)         returns (OpenResponse);
  rpc Commit(CommitRequest)     returns (CommitResponse);
  rpc Abandon(AbandonRequest)   returns (AbandonResponse);
  rpc Release(ReleaseRequest)   returns (ReleaseResponse);
}

// Template-scope events. template_id is the content hash, opaque to rimsky.
message OnTemplateRegisteredRequest    { string template_id = 1; }
message OnTemplateRegisteredResponse   {}
message OnTemplateDeployedRequest      { string template_id = 1; }
message OnTemplateDeployedResponse     {}
message OnTemplateUndeployedRequest    { string template_id = 1; }
message OnTemplateUndeployedResponse   {}
message OnTemplateDeregisteredRequest  { string template_id = 1; }
message OnTemplateDeregisteredResponse {}

// Instance-scope events. instance_id is the instance UUID, opaque to rimsky.
message OnInstanceCreatedRequest       {
  string template_id = 1;
  string instance_id = 2;
}
message OnInstanceCreatedResponse      {}
message OnInstanceTerminatedRequest    {
  string template_id = 1;
  string instance_id = 2;
}
message OnInstanceTerminatedResponse   {}
```

Failure semantics: stores return gRPC error status codes for failure (matching the existing 4 runtime verbs). Empty response messages signal success.

### 4.2 `OpenRequest` scope envelope

Two new fields added to the existing `OpenRequest`:

```protobuf
message OpenRequest {
  string claim_id     = 1;
  string store_name   = 2;
  string selector     = 3;
  string intent       = 4;
  string alias        = 5;
  string template_id  = 6;   // NEW: content hash, opaque to rimsky
  string instance_id  = 7;   // NEW: instance UUID, opaque to rimsky
}
```

The supervisor populates both from the dispatch row's instance → template lookup. Store-services that don't care about the envelope ignore the fields. Store-services that do per-template or per-instance namespacing internally use the envelope to resolve the runtime call to the right namespace.

The envelope is rimsky-inert per the auth-blind / project-agnostic philosophy: rimsky does not introspect, validate, transform, log, or trace these fields beyond passing them on the wire. Same convention as `userdata` (blessed invariant 11) and claim content (blessed invariant 20).

### 4.3 HTTP+JSON bridge

The HTTP+JSON bridge (per v3 spec §5.2) gains six new endpoints mirroring the gRPC methods. JSON request/response shapes match the proto messages. Path naming follows the existing convention (`/v1/store/Open` → `/v1/store/OnTemplateRegistered`, etc.).

### 4.4 Capabilities unchanged

`CapabilityStruct` stays single-field:

```protobuf
message CapabilityStruct {
  string write_semantics = 1;
}
```

No `lifecycle_events` field. No subscription model. Every store implements all six lifecycle methods; the default for stores that don't care is a one-line no-op handler returning success.

The store-author guide documents the no-op pattern explicitly so authors don't accidentally implement state-mutating side effects in handlers they don't intend to use.

### 4.5 Non-changes to existing 4 runtime verbs

`Commit`, `Abandon`, and `Release` are unchanged in shape and semantics. They do not gain a scope envelope; the `claim_id` already serves to correlate with the store-internal state opened in `Open`, and the store has the envelope cached internally from `Open` if it needs it.

---

## 5. Lifecycle event semantics

### 5.1 Event fan-out

When a transition fires (template register, deploy, undeploy, deregister; instance create, terminate), the control-api computes the **distinct** set of store registration names referenced anywhere in the template's `nodes[].stores[]` block. It then RPCs the corresponding event to each.

Deduplication: a template with three nodes all referencing `pg_workspace` produces one event to `pg_workspace`, not three. Deduplication is by the `name` field of `nodes[].stores[]`.

A template referencing zero stores (rare but legal) fires zero events for that transition.

### 5.2 Idempotency

Before each RPC, the control-api consults `rimsky_store_lifecycle` (§5.3) for the `(store_registration_name, scope_kind, scope_id)` row. If the row's `state` is already at the target for this transition (e.g., `deployed` for an `OnTemplateDeployed` call), the RPC is skipped and treated as a no-op.

This makes all transitions idempotent. Re-deploy on `deployed` template → control-api finds all rows already `deployed` → returns 200 with zero events fired.

### 5.3 Bookkeeping schema: `rimsky_store_lifecycle`

```sql
CREATE TABLE rimsky_store_lifecycle (
    store_registration_name TEXT NOT NULL,
    scope_kind              TEXT NOT NULL CHECK (scope_kind IN ('template','instance')),
    scope_id                TEXT NOT NULL,                       -- template hash or instance UUID (text form)
    state                   TEXT NOT NULL CHECK (state IN ('registered','deployed','undeployed','created')),
    last_event_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (store_registration_name, scope_kind, scope_id)
);
```

State values by scope:

- `scope_kind = 'template'`: `state ∈ {'registered','deployed','undeployed'}`. (No `'deregistered'` value persisted — the row is deleted when the template is deregistered.)
- `scope_kind = 'instance'`: `state = 'created'` always. (No `'terminated'` value persisted — the row is deleted when the instance terminates.)

Combinations not in the cross-product (e.g., `scope_kind='instance'` with `state='deployed'`) are not constrained at the database level since the CHECK constraint is per-column; the application enforces the combination invariants. The flat single-column CHECK is intentional — Postgres CHECK constraints don't support per-row scope-conditional state vocabularies elegantly, and the application owns the state-machine logic anyway.

Row lifecycle:

- Inserted at first successful event ack for the (store, scope) pair (e.g., `state='registered'` after `OnTemplateRegistered` acks).
- Updated on subsequent successful event acks.
- Deleted on terminal events (`OnTemplateDeregistered` acked → row deleted; `OnInstanceTerminated` acked → row deleted).

### 5.4 Failure semantics

Each lifecycle-transition API endpoint is **synchronous and progress-preserving per transition**: events fire one by one; on failure, the lifecycle rows for stores that already acked persist so the next retry resumes from where the failed attempt stopped. The endpoint returns success only when the entire fan-out has acked. Two ordering patterns apply:

**Register, deploy, undeploy, deregister (template-scope events):**

1. Validate the transition is legal given the current `rimsky_templates` state.
2. Compute the deduplicated store set.
3. For each store, look up `rimsky_store_lifecycle`; skip if already at target.
4. RPC the event. On success: UPSERT `rimsky_store_lifecycle` to the new state. On failure: abort with HTTP 5xx, listing which store(s) failed.
5. After all stores ack: update `rimsky_templates.state` (or, for register, INSERT the row; for deregister, DELETE the row and its lifecycle rows). Return 200 / 201.

The `rimsky_templates.state` column reflects the LAST FULLY COMPLETED transition. A partial-failure transition leaves the column at its prior value; lifecycle rows track per-store progress. Retry consults lifecycle rows (step 3) and resumes from where the failed attempt left off. This makes the column the source of truth for "what state is this template actually in" and lets the API endpoint distinguish "transition in progress (retry to complete)" from "transition complete."

**Instance create:** (steps below summarize the §2.2 detailed flow)

1. Inside a database transaction with `SELECT ... FOR UPDATE` on the template row (per §2.2): validate template is in `state='deployed'`.
2. Validate `params` against the spec stored at `rimsky_templates.spec` for the resolved `template_hash` (per §2.3, validation uses the pinned hash, not the tag's current target if a tag was used to look up the template).
3. INSERT `rimsky_instances` row up front (generates UUID; idempotent against existing `(template_hash, instance_key)`).
4. COMMIT the transaction. The instance row is durable; the template-row lock is released.
5. Fire `OnInstanceCreated` to every distinct store; per §5.2, skip stores already at `created` for this `instance_id`.
6. After all stores ack, return 201.

The `rimsky_instances` row is durably committed at step 4 — before any events fire — regardless of whether subsequent events succeed. Partial-failure at step 5 leaves the row in place; retry resumes firing missing events. Concurrent requests with the same `instance_key` resolve via the unique constraint at step 3 inside the locked transaction.

**Instance terminate:**

Trigger condition per §2.4. At-least-once delivery; idempotent store-side handlers. `rimsky_store_lifecycle` row deleted on successful ack; re-fire after restart finds the row still present and re-attempts.

---

Caller-driven retry: on partial failure, the caller (operator or automation) retries the same endpoint. Rimsky's idempotency check (step 3) skips already-acked stores; only not-yet-acked stores get the event re-fired.

No 2PC, no rollback orchestration. Per-store atomicity is each store-service's concern (existing v3 spec §7.8 obligation). Rimsky's atomicity guarantee is at the (store, scope) level: either the row in `rimsky_store_lifecycle` reflects a successful ack, or it doesn't.

The wire protocol contract for stores: lifecycle-event handlers must be idempotent. Calling `OnTemplateDeployed` twice for the same `template_id` must produce the same observable state as calling it once. This matches the existing requirement on the 4 runtime verbs.

### 5.5 No async, no fire-and-forget

Lifecycle events are synchronous. There is no event queue, no background fan-out worker, no eventual-consistency story. If a store is slow, the deploy is slow. Operators see real failures, not silent "events queued."

This is a deliberate v1 stance. Async / batched / queued event delivery may land in a later spec if operational needs surface; v1 defaults to honest synchronous calls.

### 5.6 Ordering

Within a single API request, events fire to all stores in dictionary order of store registration name (deterministic for testability). Across requests, the natural lifecycle ordering is enforced by the API state-machine validation (§1.4): `OnTemplateDeployed` cannot fire before `OnTemplateRegistered` for the same template because the API endpoint refuses to deploy an unregistered template. Stores do not need to enforce ordering on their side.

---

## 6. Validation rules

### 6.1 Template registration validation

A template registration request fails (HTTP 400) if:

- The spec is malformed (existing `node.ValidateTemplate` errors).
- Any `nodes[].stores[].name` references a store not declared in `rimsky.yml`'s `stores:` block (existing `StoreDeclared`).
- Any `nodes[].locks[].name` references a named lock not declared (existing `NamedLockDeclared`).
- **Any `nodes[].executor` references an executor not declared** (NEW: `ExecutorDeclared`).
- The optional `tag` field is supplied and is malformed (does not match the tag regex in §1.1) or matches a hash-shape (rejected by tag regex).

### 6.2 Tag operations validation

- `POST /v1/tags`: tag must be valid (regex); template must resolve. Refused (409) if tag already exists.
- `PUT /v1/tags/{tag}`: tag must already exist; new template must resolve.
- `DELETE /v1/tags/{tag}`: tag must exist.
- Tag identifiers cannot be created in hash form (regex precludes).

### 6.3 Lifecycle transition validation

State-machine enforcement per §1.4. Endpoints return HTTP 409 for illegal transitions with a clear error message naming the current state and the disallowed transition.

### 6.4 Instance creation validation

- Template must exist and be in `state='deployed'` (HTTP 409 otherwise).
- `instance_key`, when provided, must be unique within `(template_hash, instance_key)`.
- `params` are validated against the template's `params_schema` (existing behavior).

### 6.5 Hash collision

Two registrations with the same content hash collide on the primary key. The control-api detects this case before insertion: if the hash already exists, the registration is treated as idempotent (no insertion, no event firing). The existing tag (if supplied) is created or moved as a separate operation.

This means: re-registering the same spec under a new tag is cheap. Different content under the same tag (via `PUT /v1/tags/{tag}`) is an explicit operation; it never happens implicitly.

### 6.6 Per-process startup validation

Each rimsky process at startup:

1. Loads `rimsky.yml`. Fails fast on parse error.
2. Dials each `stores:` entry; runs `Capabilities()` handshake; fails fast on unreachable / mismatched-capabilities store. (Existing behavior, unchanged.)
3. (Supervisor only) Validates the `executors:` block syntactically (each entry has `transport`, `endpoint`). **Does not dial, does not DNS-resolve** at startup — preserves the existing supervisor's lazy-dial-on-dispatch behavior so an executor outage (or unstarted executor container in a docker-compose deploy) during supervisor restart does not lock the entire supervisor out of starting. DNS resolution and dial happen at first dispatch to a given executor; failures surface as dispatch errors, not startup errors.
4. (Control-api only) Caches the store / named-lock / executor name sets for template-validation hooks.

`rimsky.yml` is operator-managed and expected to be deployed identically across all three rimsky processes in a deployment. The spec does not enforce cross-process consistency at runtime; if the operator updates the file out-of-band on different processes, mismatch can result in register-passes / dispatch-fails (a template registered against control-api's set of executors but referencing one the supervisor doesn't know about). This is operator-responsibility, same as today; rolling configuration updates are expected to roll all three processes coherently.

---

## 7. Affected code

### 7.1 New / changed files

- `proto/v1/store_service.proto` — six new RPCs; two new fields on `OpenRequest`.
- `core/migrations/003-template-registry-and-lifecycle.sql` — schema changes per §1.2, §2.1, §5.3.
- `core/migrations/embed.go` — registers the new migration.
- `core/storage/postgres/templates.go` — rewrite for hash-keyed schema; tag operations.
- `core/storage/postgres/instances.go` (existing) — swap `template_id UUID` → `template_hash TEXT`; rename `consumer_key`.
- `core/storage/postgres/store_lifecycle.go` (NEW) — DAO for `rimsky_store_lifecycle`.
- `core/controlapi/templates.go` — rewrite request handlers; integrate hashing; lifecycle-transition endpoints; tag endpoints.
- `core/controlapi/instances.go` (existing) — touch-up per §2.2.
- `core/controlapi/tags.go` (NEW) — tag CRUD endpoints.
- `core/controlapi/lifecycle.go` (NEW) — fan-out logic for firing lifecycle events; idempotency check against `rimsky_store_lifecycle`.
- `core/store/remote/` — gRPC client methods for the six lifecycle events.
- `core/store/interface.go` — `Store` interface gains six lifecycle methods (default no-op available via embeddable struct).
- `core/store/storetest/` — test fake implements the six methods.
- `core/config/stores.go` (existing) — extend to parse the unified `rimsky.yml`; preserve the existing stores/named_locks shape inside it; add `executors:` block parsing.
- `core/cmd/rimsky-control-api/main.go`, `core/cmd/rimsky-supervisor/main.go`, `core/cmd/rimsky-scheduler/main.go` — env var rename `RIMSKY_STORES_CONFIG` → `RIMSKY_CONFIG`.
- `core/cmd/rimsky-supervisor/main.go` — remove the `executors:` parsing from `RIMSKY_SUPERVISOR_CONFIG`; pull from `rimsky.yml`.
- `core/node/validate.go` — `RegistryHooks` gains `ExecutorDeclared`.
- `core/canonical/jcs.go` (NEW) — wrapper around the vendored JCS library; `CanonicalSpecHash(spec node.TemplateSpec) (string, error)`.
- `go.mod` — vendors `github.com/cyberphone/json-canonicalization`.
- `stores/postgres/main.go`, `stores/filesystem/main.go`, `stores/stub/main.go` — implement the six lifecycle methods (postgres and stub demonstrate the no-op pattern; filesystem ditto for v1).
- `executors/claude-agent/`, `executors/http-node/`, `executors/stub/` — unchanged (lifecycle events are store-only).

### 7.2 Deploy / reference assets

- `deploy/stores.yml` → renamed to `deploy/rimsky.yml`; `executors:` block merged in from `deploy/supervisor-config.yml`.
- `deploy/supervisor-config.yml` — `executors:` block removed; tuning knobs unchanged.
- `deploy/docker-compose.yml` — env var renames; mounts the new file under `/etc/rimsky/rimsky.yml` for all three rimsky containers.
- `deploy/kubernetes/rimsky-chart/` — known stale per CLAUDE.md; updated as part of this work.

### 7.3 Tests

- Unit tests on the JCS canonicalization wrapper (`core/canonical/jcs_test.go`): byte-exact output for representative specs; key-reordering invariance; whitespace invariance; number-normalization invariance.
- Unit tests on the template state machine (`core/controlapi/templates_test.go`): all valid transitions; all illegal transitions return 409; tag movement does not migrate instances.
- Unit tests on lifecycle-event fan-out and idempotency (`core/controlapi/lifecycle_test.go`): deduplication; partial-failure retry; skip-if-already-acked.
- Integration tests under `core/storage/postgres/` for schema migration: round-trip insert/read for `rimsky_templates`, `rimsky_template_tags`, `rimsky_store_lifecycle`; FK refusals.
- Scenario tests under `test/scenarios/lifecycle/` exercising end-to-end flows: register → deploy → instantiate → terminate → undeploy → deregister, with assertions on `rimsky_store_lifecycle` row deltas at each step. The fake store (`storetest`) records event invocations.
- Conformance suite (`core/cmd/rimsky-executor-conformance/`) gains six no-op-passing checks: a conforming store accepts each lifecycle event without error and produces the empty response.
- Smoke fixture (`test/smoke/`) — at minimum, validate that the new env var works end-to-end (bring up the stack via the unified `rimsky.yml`).

### 7.4 Documentation

The doc sweep updates:

- `docs/architecture.md` — package structure note for `core/canonical/`, `core/controlapi/lifecycle.go`, `rimsky_store_lifecycle`; pointers to this spec.
- `docs/protocol.md` — six new lifecycle RPCs; `OpenRequest` scope envelope; HTTP+JSON bridge mappings.
- `docs/operator-guide.md` — `rimsky.yml` shape and rename; new template lifecycle endpoints; tag operations; deploy-vs-instantiate distinction.
- `docs/store-author-guide.md` — six new methods stores must implement; default no-op pattern; idempotency requirement.
- `docs/glossary.md` — refresh "template," "instance," "tag," "deploy" / "undeploy" / "register" / "deregister"; remove any stale "substore" v2 vocabulary; add "lifecycle event," "scope envelope," "content hash."
- `docs/2026-04-26-control-layer.md` — § cross-references back to this spec; align terminology; the §1 narrative may need light edits to match the spec's exact event names and bookkeeping schema.
- `docs/2026-05-01-auth-and-multitenancy.md` — § cross-references; the substrate-supported multi-tenancy section's references to `declared_metadata` should be updated to reflect option α (no template-side metadata).
- `CHANGELOG.md` — Unreleased bullet describing this batch.
- `CLAUDE.md` — new gotcha entries: lifecycle events fire from control-api (not supervisor); `rimsky.yml` is the unified deployment config; `RIMSKY_STORES_CONFIG` is gone; instance binds to template_hash at creation; tag movement does not migrate live instances.

---

## 8. Compatibility & migration

### 8.1 Pre-v1 breakage policy

Per `.claude/rules/rules.md`, rimsky is pre-v1. There is no production data to preserve and no consumer is locked into a particular schema. This spec takes the clean path on every breaking change:

- Drop and recreate `rimsky_templates`, `rimsky_template_tags`, `rimsky_instances`, `rimsky_store_lifecycle` as a single migration step.
- Rename `RIMSKY_STORES_CONFIG` → `RIMSKY_CONFIG` with no fallback.
- Add six new RPCs to `StoreService`; require all stores to implement them. Existing store-services in this repo (`stores/postgres`, `stores/filesystem`, `stores/stub`) get updated as part of the implementation.
- Existing dev databases get nuked. Fresh start; no compat shim.

### 8.2 Migration ordering

The schema migration runs before any code that reads/writes the new tables. Per the existing migration runner pattern (`core/migrations/runner.go`), a session-level advisory lock is held for the migration batch.

The new migration `003-template-registry-and-lifecycle.sql` does:

1. `DROP TABLE rimsky_instances`.
2. `DROP TABLE rimsky_templates`.
3. Re-create `rimsky_templates` with the new schema.
4. Create `rimsky_template_tags`.
5. Re-create `rimsky_instances` with the new schema.
6. Create `rimsky_store_lifecycle`.

Other tables that referenced the old `rimsky_templates(id) UUID` are checked at migration time and updated to reference `TEXT` if any. Audit needed during implementation.

### 8.3 Wire compatibility

A control-api running this spec talks to store-services that implement the six new lifecycle RPCs. Older store-services without those methods return gRPC `Unimplemented` and the corresponding lifecycle-transition endpoints fail. Deployment-time coordination required: rolling out this spec means rolling out the updated store-services in the same release. Pre-v1, that's the operator's concern.

---

## 9. Out of scope (explicit)

This spec deliberately does **not** cover:

- **CLI surface and `rimsky-compose.yml`.** A separate brainstorm produces a follow-on spec for `rimsky-cli register/run/instantiate/rm/tag/compose`. That spec builds on the API surface defined here and does not modify it.
- **Auth / principal field / policy hook / ACLs.** Per `docs/2026-05-01-auth-and-multitenancy.md` §1, v1 is per-project deployment with no rimsky-side auth. The `source` column on `rimsky_templates` is forward-compat; no `principal` column.
- **Audit logging.** Deferred. Lands with auth-doc §2. The `rimsky_store_lifecycle` table is bookkeeping (current state only), not audit history.
- **Package manager / OCI distribution / signed packages / conformance attestations.** Per `docs/2026-04-26-package-manager.md`, package install delegates to `POST /v1/templates` with extra metadata. The `source` column will record `'package_manager'` when that lands; v1 does not consume the field.
- **Sub-graph composability.** Deferred per package-manager doc §10.
- **Quotas.** Deferred per control-layer doc §1.8.
- **Frame resolution.** Already shipped per `docs/specs/2026-04-26-frame-resolution-design.md`.
- **TTL / GC of templates.** Deferred. V1 templates persist until explicit deletion.
- **Async / batched / queued lifecycle event delivery.** Deferred. V1 is synchronous fan-out per §5.5.
- **`Capabilities.LifecycleEvents` subscription.** Explicitly rejected during brainstorm. All stores implement all six lifecycle methods.
- **Per-template substore declarations.** Explicitly rejected during brainstorm (option α). Templates declare nothing about scope or provisioning; that policy lives in store-config.
- **Tenant identity in the scope envelope.** Tenant routing, when an operator wants it, flows through the existing opaque `userdata` (per blessed invariant 11) or operator-wired per-tenant store-service registrations. No `tenant_id` field on the envelope. Deferred per auth-doc §3.

---

## 10. Open questions

Issues surfaced in design that are deliberately left for the implementation phase to settle:

1. **Instance terminal-event detection mechanism** (§2.4). The spec commits to at-least-once delivery with idempotent store-side handlers. The exact mechanism (scheduler tick log polling for terminal instances, dedicated column on `rimsky_instances`, watcher on `rimsky_nodes` state transitions) is the implementation's choice — at-least-once relaxes the constraint enough that any correct mechanism works.

2. **Default no-op interface for `Store`** (§7.1). The Go interface gains six methods; an embeddable struct providing no-op implementations would let store-author code stay short. Implementation chooses whether to ship one.

3. **Concurrency on lifecycle-event firing.** Within a single API request, events fire to multiple stores. Implementation choice: serial (simpler, slower for templates with many stores) vs. fan-out goroutines with a wait-group (faster, modestly more complex). Either is correct; serial is recommended for v1 for simplicity, parallel fan-out can land later if profiling shows it matters.

4. **Doc-sweep depth for `2026-04-26-control-layer.md`'s §1 narrative.** That section was reframed during the 2026-05-01 conversation but predates this spec. Implementation phase decides whether to leave the narrative as-is (with cross-references back to this spec) or rewrite it in light of the spec's exact terminology. Either is acceptable; cross-references are sufficient for a future reader.

5. **Orphan lifecycle-row cleanup** (§1.6.1). The spec tolerates orphan rows from aborted registrations; a cleanup endpoint or sweeper is deferred to a later spec. Implementation phase confirms orphans don't block other operations; if they do, a small cleanup is added in this batch.

---

## 11. Summary of decisions

For quick reference:

| # | Decision | Rationale |
|---|---|---|
| 1 | Hash-as-id for `rimsky_templates`, tags as separate table | Docker model; pre-v1 makes migration cost zero |
| 2 | RFC 8785 JCS canonicalization, sha256, vendored library, hash spec only | Deterministic, language-agnostic, library-supported |
| 3 | Four template-level events + two instance-level events | Mirrors register / deploy / undeploy / deregister + create / terminate lifecycle |
| 4 | Templates declare nothing about provisioning (option α) | Templates think in stores; provisioning is store-config-side |
| 5 | All stores implement all six lifecycle methods | No subscription complexity; no-op handlers cheap |
| 6 | Lifecycle events synchronous, all-or-nothing per transition, caller-driven retry | Honest failure surface; idempotency via `rimsky_store_lifecycle` |
| 7 | Tags first-class on `/v1/tags`; inline in template GET response; no nested write surface | One canonical write path; avoids `/templates/<tag>/tags/<tag>` ambiguity |
| 8 | Unified `rimsky.yml` (`RIMSKY_CONFIG`) for stores + named_locks + executors | One coherent deployment-shape surface |
| 9 | `consumer_key` → `instance_key`, `template_id UUID` → `template_hash TEXT` | Generic terminology; aligns with hash-as-id |
| 10 | Instance binds to template hash at creation; tag movement does not migrate live instances | Mirrors Docker; predictable; operator-explicit migration via terminate + re-instantiate |
| 11 | `Capabilities` stays single-field (`write_semantics`) | No `lifecycle_events` subscription; matches "all stores implement all" |
