# Resource Author Guide

This guide is for Go developers writing a new rimsky resource implementation.

In v1, resource implementations are Go-only. Resources are tightly coupled to
rimsky's storage layer — they share a transaction context with the version
registry, depend on the `storage.ResourceRegistry` surface, and need typed
access to `pgxpool.Pool` for storage-backed impls. Other languages are out of
scope for v1.

If you want to add a non-Go data-store adapter, the preferred path is a
tiny Go resource wrapper that talks to your external system over its native
protocol. Ask in the rimsky discussion forum before starting.

For operator context, see `operator-guide.md`. For the concept model, see
`node-graph-design.md`.

---

## 1. The interfaces

The whole surface lives in one file: `core/resource/interface.go`.

### 1.1 `resource.Resource`

```go
type Resource interface {
    Path() []string
    OwnerNodeID() shared.UUID

    CurrentVersion(ctx context.Context) (*Version, error)
    PreviousVersion(ctx context.Context) (*Version, error)
    ListVersions(ctx context.Context, limit int) ([]*Version, error)

    Commit(ctx context.Context, req CommitRequest) (*CommitResult, error)
    NoOpCommit(ctx context.Context) error
    RestoreVersion(ctx context.Context, target VersionRef) (*Version, error)
}
```

Method-by-method:

**`Path()`** — the resource path declared in the template (e.g.
`["discovery", "project-alpha"]`). Immutable; set at creation.

**`OwnerNodeID()`** — UUID of the node that owns this resource (from
`owns_resources[]`). Immutable; set at creation.

**`CurrentVersion(ctx)`** — returns the currently-active version row, or
`(nil, nil)` if the resource has never been committed. Typical
implementation: fetch the `rimsky_resources` row, follow its
`current_version_id` pointer, return the `rimsky_resource_versions` row
converted to `resource.Version`.

**`PreviousVersion(ctx)`** — returns the version the resource would revert
to on `RestoreVersion("previous")`. `(nil, nil)` if no prior version
exists.

**`ListVersions(ctx, limit)`** — newest-first list of versions, capped at
`limit`. Used by the control API's `/resources/:id/versions` endpoint.

**`Commit(ctx, req)`** — the core write path. §2.1 has details.

**`NoOpCommit(ctx)`** — records that an execution ran successfully but
produced no change (`changed=false`). Updates bookkeeping timestamps;
does NOT write a new version row.

**`RestoreVersion(ctx, target)`** — rollback. §2.2 has details.

### 1.2 `resource.Factory`

```go
type Factory interface {
    ConfigSchema() []byte
    Create(cfg Config, rules []QualityRuleSpec, reg Registry) (Resource, error)
}
```

**`ConfigSchema()`** returns a JSON Schema describing the `config:` block
the template author writes for your implementation. Template validation
rejects malformed configs at `POST /templates` time — the earlier the
better. Strictness pays off.

**`Create(cfg, rules, reg)`** is called once per resource per instance,
during `POST /instances`. At Create time you receive:

- `cfg`: the template's `config:` block, post-placeholder-resolution
  and post-schema-validation. Plus three reserved keys the instance
  factory injects: `_resource_id` (UUID string), `_path` (`[]string`),
  `_owner_node_id` (UUID string). **Use these; do not invent your own.**
- `rules`: the `quality_rules:` block from the template, bound at Create
  time. Rebinding at Commit time is a design error — it opens races where
  a rule config changes between runs.
- `reg`: a narrow `resource.Registry` interface. If your impl needs the
  richer `storage.ResourceRegistry` surface, accept it via a Factory
  struct field (as `inlinejsonb.Factory.StorageRegistry` does); `reg`
  then becomes a formally-accepted, ignored parameter.

### 1.3 `CommitRequest`, `CommitResult`, `Version`, `VersionRef`

```go
type CommitRequest struct {
    ProducedBy    shared.UUID   // node_id that produced this result
    Result        any           // JSON-serializable payload
    Changed       bool          // producer-declared verdict
    ChangeSummary string        // freeform description
}

type CommitResult struct {
    Accepted      bool
    Version       *Version                 // non-nil iff Accepted && Changed
    QualityErrors []qualityrule.Failure    // non-empty iff !Accepted
}

type Version struct {
    ID             shared.UUID
    ProducedByNode *shared.UUID
    Data           []byte   // JSON bytes for inline impls; nil for external
    DataRef        []byte   // ref for external impls; nil for inline
    ChangeSummary  string
    CommittedAt    time.Time
}

type VersionRef struct {
    Kind string     // "previous" | "id"
    ID   shared.UUID // only used when Kind=="id"
}
```

---

## 2. Commit and rollback

### 2.1 Commit flow

```
   executor returns Complete(result, changed)
            │
            ▼
   supervisor calls Resource.Commit(CommitRequest)
            │
            ▼
   marshal Result to JSON bytes (reject here if not serializable)
            │
            ▼
   evaluate quality rules against (newData, prevData)
            │          │
            │          ├── error-severity failure  →  return {Accepted:false, QualityErrors:...}
            │          └── warning-severity        →  log + continue
            ▼
   req.Changed == false  →  NoOpCommit; return {Accepted:true, Version:nil}
            │
            ▼
   CommitVersion → write new rimsky_resource_versions row, advance current pointer
            │
            ▼
   GCOldVersions(keep_versions)
            │
            ▼
   return {Accepted:true, Version: <new>}
```

Reference: `core/resource/inlinejsonb/resource.go`'s `Commit`.

Key invariants your impl must preserve:

1. **Quality errors short-circuit the write.** Do not persist a new version
   row when `QualityErrors` is non-empty.
2. **Warnings do not block.** Severity-`warning` rule failures are logged
   (today) or emitted as events (future), but never block a commit.
3. **No-op is visible.** `NoOpCommit` must update enough bookkeeping (e.g.
   a `last_noop_at` timestamp, or an event) that operators can tell a
   successful-no-change run from nothing-happened.
4. **GC runs after the commit, not before.** `keep_versions=2` means
   current + previous are retained; older rows may be dropped.

### 2.2 Rollback semantics

`RestoreVersion` supports two targets:

- `VersionRef{Kind: "previous"}` — swap the current and previous
  pointers. Always supported if a previous version exists.
- `VersionRef{Kind: "id", ID: <uuid>}` — restore a specific historical
  version. Supported only if that version has not been GC'd past
  `keep_versions`. For external-sql, it also requires that the staging
  tables for that version still exist.

If your implementation cannot honor a restore, wrap
`resource.ErrRollbackUnsupported`:

```go
return nil, fmt.Errorf("myimpl: restore: %w", errors.Join(err, resource.ErrRollbackUnsupported))
```

The supervisor checks `errors.Is(err, resource.ErrRollbackUnsupported)` and
treats it as a policy failure rather than an infrastructure error — the
operator may have to intervene manually.

### 2.3 Version garbage collection

Each resource carries a `keep_versions` setting (from `retention.keep_versions`
in the template, default 2). After every successful Commit, call
`storage.ResourceRegistry.GCOldVersions(resourceID, keepVersions)` — the
storage layer deletes oldest-first until at most `keep_versions` rows
remain. GC is advisory, not hard: losing GC due to a crash just leaves a
few extra rows until the next Commit.

---

## 3. Quality rules

Rules are declared in the template:

```yaml
quality_rules:
  - type: row_count_ratio
    config: { min_ratio: 0.5 }
    severity: error
  - type: no_nulls
    config: { fields: [zone_code, geometry] }
    severity: error
```

The factory receives them as `[]qualityrule.Spec` at Create time. Inside
Commit, call `qualityrule.EvaluateAll(ctx, rules, EvalInput{NewData, PreviousData})`
— the shared evaluator returns `(errors, warnings, nil)` on success.

Rule types are registered in `core/qualityrule/`. You don't add them; you
consume them. If a template references a rule type your implementation
doesn't want to support (some rules only make sense for tabular data, some
for documents), reject it at factory Create with a clear error.

---

## 4. Testing

Resources live under `core/resource/<impl>/` and are tested with
co-located `*_test.go` files.

### 4.1 Unit tests with an in-memory fake

See `core/resource/inlinejsonb/resource_test.go`'s `fakeResourceRegistry`
for a complete in-memory `storage.ResourceRegistry` fake you can crib —
roughly 150 lines, one mutex, a few maps. This is the preferred unit-test
harness for:

- commit + re-commit with increasing versions
- no-op commits leave pointers stable
- `keep_versions` GC actually drops old rows
- quality-error commits do NOT persist
- rollback via `VersionRef{Kind: "previous"}`

### 4.2 Integration tests with real Postgres

For storage-coupled impls (like `external-sql`), use the `pgtest` harness:

```go
import "github.com/fallguy/rimsky/core/internal/pgtest"

func TestMyResource_Integration(t *testing.T) {
    ctx := context.Background()
    pool, teardown := pgtest.StartPostgres(ctx, t)
    t.Cleanup(teardown)

    // ... construct your Factory, Create, Commit, assert ...
}
```

`pgtest.StartPostgres` spins up a throwaway Postgres 14 container, applies
all rimsky migrations, and returns a ready pool. Test isolation is via
fresh containers; no test pollutes another.

### 4.3 Scenario tests

The `core/scenario/` harness drives full end-to-end scenarios — template
deploy, instance create, executor dispatch (against stubbed executors),
resource commit, event assertions. Once your resource is registered as a
factory, it plugs into scenario tests automatically via its
`implementation` name.

---

## 5. Registration

Resource implementations register themselves by calling
`resource.RegisterFactory(name, Factory{...})`. The typical pattern:

```go
package myresource

import "github.com/fallguy/rimsky/core/resource"

func Register(storage storage.ResourceRegistry) {
    resource.RegisterFactory("my-impl", Factory{StorageRegistry: storage})
}
```

Then the consumer wires registration from its `main`:

```go
// in core/cmd/rimsky-control-api/main.go (or wherever the process is bootstrapped)
inlinejsonb.Register(storageBackend.Resources())
externalsql.Register(storageBackend.Resources(), sqlConnections)
myresource.Register(storageBackend.Resources())
```

**Do not** register from `init()`. Registration needs the `storage.ResourceRegistry`
and possibly other config (named SQL pools), which aren't available at
package-init time. Explicit registration at process startup is idiomatic
rimsky.

Once registered, templates reference your implementation by its registered
name:

```yaml
owns_resources:
  - path: ["scratch", "{consumer_key}"]
    implementation: my-impl
    config:
      # whatever your ConfigSchema() says
```

---

## 6. Reference implementations

Two ship with rimsky; they illustrate opposite ends of the design space.

### 6.1 `inline-jsonb`

`core/resource/inlinejsonb/` — data lives directly in
`rimsky_resource_versions.data` (JSONB). Suitable for small-to-medium
results (up to ~1MB per version) where you want versioning + rollback + GC
for free, no external storage to manage. Used by rimsky's own operational
resources (discovery results, agent artifacts, intermediate transforms).

Commit path: marshal → quality eval → `CommitVersion` → `GCOldVersions`.
Rollback: delegate to `storage.ResourceRegistry.RestoreVersion` which swaps
pointers. GC: `keep_versions` rows retained.

### 6.2 `external-sql`

`core/resource/externalsql/` — data lives in a consumer-owned SQL table;
rimsky manages versioning pointers and a staging-table + atomic-swap
pattern for commits. Suitable for large tabular payloads where you want the
data to be directly queryable by downstream systems (not buried inside
rimsky's JSONB).

Extra wrinkles not present in inline-jsonb:

- Probes the target table at Factory.Create (`SELECT 1 FROM schema.table LIMIT 0`)
  so configuration errors surface early.
- Validates identifier safety (schema/table/column names may not contain
  double quotes).
- Accepts a named `pgxpool.Pool` via the `connection_ref` template field;
  named pools come from the supervisor's `sql_connections:` config.
- Rollback requires that the `__previous` table still exists; if GC has
  dropped it, returns `ErrRollbackUnsupported`.

Both impls share the same pattern: all storage-layer calls go through a
`storage.ResourceRegistry`. That's the interface contract — your impl is a
small adapter between "what the data looks like" and "where it lives."

---

## 7. Complete memory-only example

Here is a complete, ~100-line resource implementation backed by a process-
local map. It is pedagogical only — it does not survive process restarts,
so it's useless in production. Use it as a starting point for unit tests,
for a scratch in-process cache resource, or as a template for your own
real implementation.

```go
// Package memresource is a pedagogical rimsky resource backed by a
// process-local map. Versions are lost on process restart.
package memresource

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/google/uuid"

    "github.com/fallguy/rimsky/core/qualityrule"
    "github.com/fallguy/rimsky/core/resource"
    "github.com/fallguy/rimsky/core/shared"
)

// Factory implements resource.Factory.
type Factory struct{}

var configSchema = []byte(`{"type":"object","additionalProperties":false}`)

func (f Factory) ConfigSchema() []byte { return configSchema }

func (f Factory) Create(cfg resource.Config, rules []qualityrule.Spec, _ resource.Registry) (resource.Resource, error) {
    rid, _ := cfg["_resource_id"].(string)
    path, _ := cfg["_path"].([]string)
    own, _ := cfg["_owner_node_id"].(string)
    if rid == "" || own == "" || len(path) == 0 {
        return nil, fmt.Errorf("memresource: missing reserved cfg keys")
    }
    resID, err := uuid.Parse(rid)
    if err != nil { return nil, err }
    ownerID, err := uuid.Parse(own)
    if err != nil { return nil, err }
    return &memResource{
        resourceID:  resID,
        path:        append([]string(nil), path...),
        ownerNodeID: ownerID,
        rules:       rules,
    }, nil
}

type memResource struct {
    mu          sync.Mutex
    resourceID  shared.UUID
    path        []string
    ownerNodeID shared.UUID
    rules       []qualityrule.Spec
    versions    []*resource.Version // newest last
    current     int                 // index; -1 if none
}

func (r *memResource) Path() []string         { return r.path }
func (r *memResource) OwnerNodeID() shared.UUID { return r.ownerNodeID }

func (r *memResource) CurrentVersion(_ context.Context) (*resource.Version, error) {
    r.mu.Lock(); defer r.mu.Unlock()
    if r.current < 0 { return nil, nil }
    v := *r.versions[r.current]; return &v, nil
}

func (r *memResource) PreviousVersion(_ context.Context) (*resource.Version, error) {
    r.mu.Lock(); defer r.mu.Unlock()
    if r.current < 1 { return nil, nil }
    v := *r.versions[r.current-1]; return &v, nil
}

func (r *memResource) ListVersions(_ context.Context, limit int) ([]*resource.Version, error) {
    r.mu.Lock(); defer r.mu.Unlock()
    out := make([]*resource.Version, 0, len(r.versions))
    for i := len(r.versions) - 1; i >= 0; i-- {
        out = append(out, r.versions[i])
        if limit > 0 && len(out) >= limit { break }
    }
    return out, nil
}

func (r *memResource) Commit(ctx context.Context, req resource.CommitRequest) (*resource.CommitResult, error) {
    errs, _, err := qualityrule.EvaluateAll(ctx, r.rules, qualityrule.EvalInput{NewData: req.Result})
    if err != nil { return nil, err }
    if len(errs) > 0 { return &resource.CommitResult{Accepted: false, QualityErrors: errs}, nil }
    if !req.Changed { return &resource.CommitResult{Accepted: true}, nil }

    r.mu.Lock(); defer r.mu.Unlock()
    v := &resource.Version{
        ID: uuid.New(), ProducedByNode: &req.ProducedBy,
        ChangeSummary: req.ChangeSummary, CommittedAt: time.Now(),
    }
    r.versions = append(r.versions, v); r.current = len(r.versions) - 1
    return &resource.CommitResult{Accepted: true, Version: v}, nil
}

func (r *memResource) NoOpCommit(_ context.Context) error { return nil }

func (r *memResource) RestoreVersion(_ context.Context, target resource.VersionRef) (*resource.Version, error) {
    r.mu.Lock(); defer r.mu.Unlock()
    switch target.Kind {
    case "previous":
        if r.current < 1 { return nil, resource.ErrRollbackUnsupported }
        r.current--
        v := *r.versions[r.current]; return &v, nil
    case "id":
        for i, v := range r.versions {
            if v.ID == target.ID { r.current = i; cp := *v; return &cp, nil }
        }
        return nil, resource.ErrRollbackUnsupported
    }
    return nil, fmt.Errorf("memresource: unknown VersionRef.Kind %q", target.Kind)
}

// Register the factory.
func Register() { resource.RegisterFactory("mem", Factory{}) }
```

Drop it into `core/resource/memresource/resource.go`, add a co-located
`resource_test.go` that exercises commit → commit → rollback → no-op, call
`memresource.Register()` from your process's main, and reference it in a
template as `implementation: mem`.

---

## 8. Checklist

Before shipping your resource implementation:

- [ ] `Factory.ConfigSchema()` returns a strict JSON Schema.
- [ ] `Factory.Create()` rejects missing reserved keys (`_resource_id`,
      `_path`, `_owner_node_id`) with clear errors.
- [ ] `Commit` runs quality rules before persistence; error-severity
      failures short-circuit.
- [ ] `Commit` with `Changed=false` calls `NoOpCommit` instead of
      writing a version row.
- [ ] `RestoreVersion` wraps `ErrRollbackUnsupported` on unsupported
      targets.
- [ ] Unit tests with in-memory fake registry cover commit / no-op /
      rollback.
- [ ] Integration tests with `pgtest.StartPostgres` cover the real
      storage path (if applicable).
- [ ] Scenario tests exercise the impl end-to-end via template +
      instance (if applicable).
- [ ] `Register(...)` is called explicitly from process `main`, not
      `init()`.
- [ ] Documentation for the template config block (README or doc
      comment on `ConfigSchema`).
