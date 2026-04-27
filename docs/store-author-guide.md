# Store Author Guide

This guide is for Go developers writing a new rimsky store implementation.

In v1, store implementations are Go-only. Stores are tightly coupled to
rimsky's lock acquisition path — they share a transaction context with the
supervisor's atomic-acquisition transaction (spec §13.3) and need typed
access to `pgxpool.Pool` for postgres-backed kinds. Other languages are out
of scope for v1.

If you want to add a non-Go data-store adapter, the preferred path is a
small Go store wrapper that talks to your external system over its native
protocol. Ask in the rimsky discussion forum before starting.

For operator context, see `operator-guide.md`. For the concept model, see
`node-graph-design.md`. The authoritative spec for the redesign is
`docs/specs/2026-04-25-stores-redesign-design.md` (sections referenced
inline below).

---

## 1. Vocabulary

A **store** is an operator-configured, named data backend (filesystem
directory, postgres queue table, etc.). Stores expose **regions** —
substrings of their namespace — and the supervisor takes **locks** on
regions before dispatching a node that claims to mutate them.

Two flavours of acquisition (spec §5.6):

- **Region lock** — caller specifies the region; store locks or fails.
- **Claim** — caller asks the store to pick from its eligible pool.

Both produce the same downstream artifact: a `LockHandle` referencing a row
in `rimsky_lock_holders`. Lock state lives **only** in postgres. Stores may
persist *data* state (e.g. `claim-store-postgres` flips an items-table row
to `'in_progress'` on claim acquisition), but the question "is anyone
holding lock X" is answered exclusively by `rimsky_lock_holders`. This is
a blessed invariant — see `core/store/interface.go`.

A store also declares a **mode** (spec §6):

- **Direct (v1)** — handle points at the live region; reads and writes
  happen in-place. No sidecar, no atomic-swap.
- **Sidecar (post-v1)** — handle points at a private workspace; commit
  applies it to live atomically.
- **Versioned (post-v1)** — sidecar mode + retained committed history +
  rollback.

v1 ships only direct mode. The interface is shaped to extend cleanly to
sidecar/versioned without breaking changes.

---

## 2. The interfaces

The whole surface lives in `core/store/`:

- `interface.go` — `Store`, `ClaimableStore`, `ResumableStore`,
  `Capabilities`, `LockSpec` and its three variants.
- `types.go` — `LockHandle`, `ClaimResult`, `ReleaseAction`, `CommitResult`,
  `NativeHandle` (sealed) and the v1 concrete handles.
- `tx.go` — `WithTx` / `TxFromContext` for tx plumbing.
- `registry.go` — `Factory`, `Registry`, `BuildAll`, `GetStore`.

### 2.1 `store.Capabilities`

```go
type Capabilities struct {
    SupportsRegionLock bool
    SupportsClaim      bool
    SupportsDiscard    bool
    SupportsResume     bool
    SupportsRestore    bool
}
```

Capabilities are read by the supervisor at dispatch time to decide whether
a node's required operations are serviceable. **A store implementation that
advertises a capability MUST also satisfy the corresponding sub-interface;
the supervisor type-asserts at dispatch time** (spec §8.5.1):

| capability        | required sub-interface |
| ----------------- | ---------------------- |
| `SupportsClaim`   | `ClaimableStore`       |
| `SupportsResume`  | `ResumableStore`       |
| `SupportsRestore` | (post-v1: `RestorableStore`) |

`SupportsAtomicMulti` and `KeepVersionsMax` are deliberately absent in v1;
both belong to deferred features and are not declared until those land
(see §6 Known limitations below).

### 2.2 `store.Store`

```go
type Store interface {
    Kind() string                          // "filesystem" | "claim_store"
    Name() string                          // operator-configured
    Capabilities() Capabilities

    LockEligible(ctx, spec) (bool, error)
    RegionsConflict(a, b any) bool         // PURE
    UnmarshalRegion(raw []byte) (any, error) // PURE

    AcquireLock(ctx, spec) (LockHandle, ClaimResult, error)
    OpenHandle(ctx, lh, resumed) (NativeHandle, error)
    Commit(ctx, lh) (CommitResult, error)
    ReleaseLock(ctx, lh, action) error
}
```

Method-by-method:

**`Kind()`** — canonical string the factory registers under. Operators
reference it via `kind:` in `stores.yml`.

**`Name()`** — operator-configured store name; matches the YAML key under
`stores.<name>`. Set at construction time.

**`Capabilities()`** — see §2.1.

**`LockEligible(ctx, spec)`** — eligibility check used by the dispatch
eligibility evaluator (spec §13.2). For region locks, the supervisor
pre-screens against existing holders via `RegionsConflict` before this is
called; the implementation can rely on that. Return `false` if the store
cannot service this kind of spec at all (e.g. a `ClaimLockSpec` against a
filesystem store).

**`RegionsConflict(a, b any) bool`** — region-overlap predicate. Inputs
are the store's in-Go region type (whatever `UnmarshalRegion` produces).
Returns true if the two regions cannot both be held at once.

> **`@blessed-invariant: RegionsConflict and UnmarshalRegion are pure.`**
> No side effects, no external state read; deterministic on inputs (spec
> §18 invariant 14). The supervisor calls these inside the atomic
> acquisition transaction (§13.3) and inside hot eligibility loops;
> impurity here would corrupt acquisition correctness. The annotation
> lives in `core/store/interface.go`. Do not call out to a database, the
> filesystem, or any external system from these two methods.

**`UnmarshalRegion(raw []byte) (any, error)`** — deserialises
`rimsky_lock_holders.region_data` JSONB into your store's in-Go region
type. The supervisor calls this on each existing-holder row before
passing the value to `RegionsConflict`. Same purity contract as above.

**`AcquireLock(ctx, spec)`** — called inside the supervisor's atomic
acquisition transaction. For direct-mode filesystem this is a no-op
returning zero values; the supervisor inserts the lock-holder row
independently and fills in `LockHandle` from it. For claim stores this
performs the atomic items-table flip (`state='in_progress'`) and returns
the picked item's payload + ID via `ClaimResult`. Use
`store.TxFromContext(ctx)` to obtain the open `*pgx.Tx` and route every
DB mutation through it (see §3 below).

**`OpenHandle(ctx, lh, resumed)`** — constructs the executor-facing
`NativeHandle`. For resumable acquisitions the supervisor passes
`resumed=true`; the store may surface prior in-progress state. Direct-mode
stores ignore `resumed` (the live path is the same either way).

**`Commit(ctx, lh)`** — called after the executor signals
`Complete{changed: true}`. Direct-mode: no-op (writes already on disk).
Sidecar/versioned (post-v1): apply sidecar to live atomically. Returns
`CommitResult{Changed, ChangeSummary}`.

**`ReleaseLock(ctx, lh, action)`** — finalises the acquisition. Direct-
mode filesystem: no-op for all actions. Claim stores: invoke `on_commit`
or `on_give_up` policy depending on `action`. Sidecar/versioned (post-v1):
discard or preserve the sidecar.

`ReleaseAction` enum (`core/store/types.go`):

| value                  | meaning                                           |
| ---------------------- | ------------------------------------------------- |
| `ReleaseCommit`        | normal terminal-commit path                       |
| `ReleaseGiveUp`        | give-up policy fires (`on_give_up`)               |
| `ReleaseDiscard`       | sidecar discard / claim release-on-give_up        |
| `ReleasePreserveResume`| preserve in-progress state for resume on retry    |

### 2.3 Optional sub-interfaces

```go
type ClaimableStore interface {
    Store
    HasClaimableItem(ctx, criteria map[string]any) (bool, error)
    ReleaseClaimItem(ctx, claimID string, action string) error
}

type ResumableStore interface {
    Store
    HasPriorWork(ctx, spec LockSpec) (bool, error)
}
```

`HasClaimableItem` is the eligibility short-circuit; called by the
dispatch evaluator. Return `false` quickly when the pool is empty.

`ReleaseClaimItem` is called by the §5.6.4 last-released-wins branch when
a held claim resolves. The lock-holder row may already be deleted at this
point, so the call takes `claimID` directly. **Run inside the caller-
provided tx via `store.TxFromContext(ctx)`** — never the underlying pool.

`HasPriorWork` reports whether the store retains in-progress state for a
given lock spec from a prior dispatch attempt. The supervisor uses the
result to decide whether to pass `resumed=true` to `OpenHandle`.

### 2.4 `LockSpec` variants

```go
type NamedLockSpec struct { Name string; Mode LockMode; Limit int }
type RegionLockSpec struct { StoreName string; Region, ReadRegion any; Resumable bool }
type ClaimLockSpec  struct { StoreName string; Criteria map[string]any; Hold bool; OnCommit, OnGiveUp string; Resumable bool }
```

Named locks are not store-scoped (they are process-wide
mutex/counting semaphores) — the supervisor handles them directly via
`rimsky_lock_holders` without calling into any store. Your `Store`
implementation only sees `RegionLockSpec` and `ClaimLockSpec`.

### 2.5 Native handles

`NativeHandle` is a sealed interface (`core/store/types.go`); only types
declared in the `store` package can satisfy it. v1 ships:

```go
type FilesystemDirectHandle struct {
    Path         string
    WriteRegions []string
    ReadRegions  []string
}

type ClaimStoreHandle struct {
    Payload   any
    ClaimID   string
    StoreName string
}
```

A new store kind that wants its own handle shape adds the type to
`core/store/types.go` and a corresponding marker. Executors deserialise
the `handle` field of `ExecuteRequest.stores[<name>]` (spec §12.1) into
the kind-specific shape per their own concerns.

---

## 3. Transaction plumbing

The `core/store/` package exposes a tx-context helper used uniformly by
every store implementation that needs to participate in the supervisor's
outer transaction:

```go
ctx = store.WithTx(ctx, tx)         // supervisor side
tx, ok := store.TxFromContext(ctx)  // store side
```

A store with **no DB writes** (like `filesystem-direct`) is free to call
`TxFromContext` and ignore the returned tx. The supervisor still attaches
one so `AcquireLock`/`ReleaseLock` can be called uniformly.

A store with **DB writes** (like `claim-store-postgres`) **MUST** use the
tx for all its mutations — never the underlying pool — so atomicity with
the supervisor's lock-holder inserts is preserved. This is non-negotiable:
falling out of the outer tx breaks the §13.3 acquisition guarantee that
either both the lock-holder row and the items-table flip happen, or
neither does.

Read-only paths that run **outside** the atomic acquisition tx
(eligibility hints called from `HasClaimableItem`, the visibility-timeout
sweep) are free to use the pool directly.

---

## 4. Worked example: re-implementing the direct-mode filesystem store

This walks through `core/store/filesystem/` from scratch. It is the
simplest non-trivial store: no DB writes, region grammar is `[]string` of
path globs, all the interesting work is in `RegionsConflict`.

### 4.1 Package layout

```
core/store/filesystem/
  factory.go     — Factory{} + Build(name, cfg) → *Store
  store.go       — *Store + interface methods
  region.go      — pure RegionsConflict([]string, []string) bool helpers
  region_test.go — table-driven overlap tests
  store_test.go  — acquire / open / commit / release roundtrip
```

Imports are `core/store/` and stdlib only (per spec §8.1). No `pgx`, no
`pgxpool` — direct-mode filesystem has no DB writes.

### 4.2 The factory

```go
package filesystem

import (
    "fmt"

    "github.com/fallguy/rimsky/core/store"
)

type Factory struct{}

func (Factory) Kind() string { return "filesystem" }

func (Factory) Build(name string, cfg map[string]any) (store.Store, error) {
    mode, _ := cfg["mode"].(string)
    if mode != "direct" {
        return nil, fmt.Errorf("filesystem store %q: only mode=direct is supported in v1, got %q", name, mode)
    }
    root, _ := cfg["root"].(string)
    if root == "" {
        return nil, fmt.Errorf("filesystem store %q: missing 'root'", name)
    }
    return &Store{name: name, root: root}, nil
}
```

The factory is deliberately strict: missing keys, wrong types, unknown
modes all fail at registry-build time. Operator misconfigurations should
crash the binary on startup — never at first dispatch.

### 4.3 Capability declaration

```go
func (s *Store) Capabilities() store.Capabilities {
    return store.Capabilities{
        SupportsRegionLock: true,
        SupportsClaim:      false,
        SupportsDiscard:    false,
        SupportsResume:     true,
        SupportsRestore:    false,
    }
}
```

`SupportsResume: true` means the live region carries any in-progress
writes from a prior dispatch — the executor sees the partial state on
retry and is responsible for noticing/redoing as appropriate. We do
**not** also implement `ResumableStore` with state, because the live
region is always usable; `HasPriorWork` simply returns `false` so the
supervisor never passes `resumed=true` to `OpenHandle`.

(That choice is a v1 simplification. A more sophisticated direct-mode
filesystem could expose markers under the live region and report them via
`HasPriorWork`. The interface is the same shape either way.)

### 4.4 The `RegionsConflict` purity contract

```go
func (s *Store) RegionsConflict(a, b any) bool {
    ga, okA := a.([]string)
    gb, okB := b.([]string)
    if !okA || !okB {
        return true // fail closed: never silently admit an unknown shape
    }
    return RegionsConflict(ga, gb) // package-level pure helper
}
```

Inputs of an unexpected type are treated as conflicting — the supervisor
must never silently admit an acquisition whose region we cannot
interpret. Same posture in `UnmarshalRegion`: malformed JSONB is an error,
not a silent default.

The package-level `RegionsConflict([]string, []string)` lives in
`region.go` and is the heart of the store. It iterates pairs of globs and
returns true if any pair overlaps under our extended `path/filepath.Match`
semantics (`**` is treated as "any path under the prefix"). The function
takes no `ctx`, reads no filesystem, calls no external system; it can be
unit-tested with table-driven cases at microsecond speed.

The blessed-invariant annotation lives at the function's doc comment.

### 4.5 `UnmarshalRegion`

```go
func (s *Store) UnmarshalRegion(raw []byte) (any, error) {
    var globs []string
    if err := json.Unmarshal(raw, &globs); err != nil {
        return nil, fmt.Errorf("filesystem store %q: unmarshal region: %w", s.name, err)
    }
    return globs, nil
}
```

The returned `any` is the same `[]string` re-typed. The supervisor passes
it straight to `RegionsConflict`. Symmetry between the runtime type and
the on-disk JSONB shape is the simplest path; resist serialising into a
struct that needs translation.

### 4.6 `AcquireLock` and `OpenHandle`

`AcquireLock` is a no-op:

```go
func (s *Store) AcquireLock(_ context.Context, _ store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
    return store.LockHandle{}, store.ClaimResult{}, nil
}
```

The supervisor inserts the `rimsky_lock_holders` row independently and
populates the LockHandle's ID + timestamps before calling `OpenHandle`.

`OpenHandle` constructs the native handle. Direct-mode filesystem needs
the resolved write/read regions for the `FilesystemDirectHandle` payload,
but `LockHandle` does not carry them. The pattern: the supervisor's runner
threads them through context via a package-private helper:

```go
func WithRegions(ctx context.Context, write, read []string) context.Context { ... }

func (s *Store) OpenHandle(ctx context.Context, _ store.LockHandle, _ bool) (store.NativeHandle, error) {
    write, read := regionsFromContext(ctx)
    return store.FilesystemDirectHandle{
        Path:         s.root,
        WriteRegions: write,
        ReadRegions:  read,
    }, nil
}
```

A new store kind that needs handle-time data the LockHandle doesn't carry
should follow the same pattern: package-private `WithFoo`/`fooFromContext`
helpers, threaded by the supervisor's runner. **Do not** add fields to
`LockHandle` for kind-specific data — it is the cross-store-kind shape
recorded by the row.

### 4.7 `Commit` and `ReleaseLock`

Both are no-ops in direct mode:

```go
func (s *Store) Commit(_ context.Context, _ store.LockHandle) (store.CommitResult, error) {
    return store.CommitResult{Changed: true}, nil
}

func (s *Store) ReleaseLock(_ context.Context, _ store.LockHandle, _ store.ReleaseAction) error {
    return nil
}
```

The `Changed: true` return is a placeholder — the executor signals
`Complete{changed: ...}` via a separate path before the supervisor calls
`Commit`; a sidecar/versioned mode (post-v1) computes the value here.
For direct mode it is unused.

### 4.8 Registration

```go
// in core/cmd/rimsky-supervisor/main.go (and -scheduler, -control-api)
storeFactories := []store.Factory{
    filesystem.Factory{},
    claimstorepg.Factory{Pool: pool},
    // myimpl.Factory{},   // ← add yours here
}

reg := store.NewRegistry()
for _, f := range storeFactories { reg.Register(f) }
stores, err := reg.BuildAll(storesCfg)
```

**Do not** register from `init()`. Registration needs the dependencies
(`*pgxpool.Pool`, named connection pools, etc.) wired explicitly at
process startup. Explicit registration is idiomatic rimsky.

Once registered, operators reference your store in `stores.yml`:

```yaml
stores:
  scratch:
    kind: filesystem
    mode: direct
    root: /var/lib/rimsky/scratch
```

And nodes reference it in templates via `stores: [{name: scratch, ...}]`.

---

## 5. Testing

Stores live under `core/store/<kind>/` and are tested with co-located
`*_test.go` files.

### 5.1 Pure region tests

`RegionsConflict` is a pure function; table-driven tests are the bulk of
your coverage. See `core/store/filesystem/region_test.go` for the
pattern: input pairs, expected overlap, ~50 cases covering literal-vs-
literal, literal-vs-glob, glob-vs-glob, `**` extension, and disjoint
subtrees.

### 5.2 Roundtrip tests with no DB

For direct-mode filesystem the store is a thin shell — most roundtrip
tests construct a `*Store` directly, call `AcquireLock` / `OpenHandle` /
`Commit` / `ReleaseLock`, and assert the returned native-handle shape.
No postgres needed.

### 5.3 Integration tests with real Postgres

For DB-coupled kinds (like `claim-store-postgres`), use the `pgtest`
harness:

```go
import "github.com/fallguy/rimsky/core/internal/pgtest"

func TestMyStore_Integration(t *testing.T) {
    ctx := context.Background()
    pool, teardown := pgtest.StartPostgres(ctx, t)
    t.Cleanup(teardown)

    // Create the operator-owned items table the store expects (spec §9.10)
    // ... construct your Factory, Build, AcquireLock inside a tx, assert ...
}
```

`pgtest.StartPostgres` spins up a throwaway Postgres container, applies
all rimsky migrations, and returns a ready pool. Test isolation is via
fresh containers; no test pollutes another. These tests require a working
Docker socket and pull the postgres image — they are not unit-test fast.

### 5.4 Scenario tests

The `core/scenario/` harness drives full end-to-end scenarios — template
deploy, instance create, executor dispatch (against stubbed executors),
store acquisition, commit, event assertions. Once your store is
registered as a factory, it plugs into scenario tests automatically via
its YAML `kind`.

The harness's default test fixture uses the in-process `core/store/stub/`
store, which implements `Store`, `ClaimableStore`, and `ResumableStore`
with configurable in-memory state. A new store kind does not need to
modify the stub — it brings its own fixtures.

---

## 6. Known limitations

These are accepted in v1 (spec §20) and may bite you in subtle ways.
Document them in your store's README so operators understand the
guarantees.

### 6.1 Multi-store atomic commit is not provided

A node writing to two stores commits independently per store. There is no
two-phase-commit machinery in v1. If the first `Store.Commit` succeeds
and the second fails, the supervisor records a failure but the first
store has already accepted the write.

In direct mode this is usually fine — the writes already landed before
`Commit` is called, and `Commit` is a no-op. The hazard is for sidecar/
versioned modes (post-v1), where `Commit` actually mutates live state.

If your store needs cross-store atomicity, the answer in v1 is "use a
single store" — for example, one postgres store transactionally writing
to multiple tables, behind one `Store` interface. The `SupportsAtomicMulti`
capability flag is reserved for the post-v1 design that solves this
properly.

### 6.2 Direct-mode store-level quality rules are warned-and-ignored

Templates can declare `quality_rules:` at both the node level and the
store level (spec §15). For direct-mode stores, store-level rules are
**accepted in YAML but warned-and-ignored** at runtime: rejection is
awkward when the bytes have already landed on disk. v1 supports
node-level `must_match_regex` for filesystem stores (the node-level rule
runs at supervisor commit time, before the supervisor records success);
store-level rules wait on sidecar mode to land.

If your store is direct mode, document this limitation. Operators who
need rejecting validation should use node-level rules in the meantime.

### 6.3 Region-overlap detection is per-store-kind

There is no cross-kind overlap reasoning. v1 ships only filesystem
path-glob overlap. A new store kind brings its own region grammar and
its own pure overlap predicate; rimsky does not try to relate
`["**/foo"]` to `{"sql_table": "bar"}`.

### 6.4 `HasClaimableItem` is TOCTOU

For claim stores, the eligibility hint may go stale between the
evaluator's check and the actual `AcquireLock`. `AcquireLock` re-validates
atomically (FOR UPDATE SKIP LOCKED), so there is no correctness issue —
just an occasional wasted candidate slot. Do not try to "fix" the race
with caching; the items-table is the authoritative source.

### 6.5 `claim-store-postgres` items table is operator-owned

Rimsky verifies the table's column shape at registry-build time but does
not create it. A new claim-store backend should follow the same rule:
verify, don't create. Operators populate items via direct SQL or via the
admin endpoint (`POST /admin/claim-stores/:name/items`).

### 6.6 `stores.yml` divergence between processes is silently corrected

control-api and supervisors each build their own `Registry` from process
YAML at startup. If a supervisor is missing a store its peers know about,
that supervisor simply doesn't claim relevant work; dispatch eligibility
fails the supervisor's match. No alarm fires. A new store kind inherits
this property automatically.

---

## 7. Checklist

Before shipping your store implementation:

- [ ] `Factory.Build()` rejects missing or wrong-typed config keys with
      clear errors at registry-build time.
- [ ] `Capabilities()` is honest: every `true` flag has the corresponding
      sub-interface implemented (`ClaimableStore`, `ResumableStore`).
- [ ] `RegionsConflict` and `UnmarshalRegion` are pure — no side effects,
      no external state. Annotate the doc comment with `@blessed-invariant`.
- [ ] Unknown region shapes in `RegionsConflict` fail closed (return
      `true` rather than silently admitting an acquisition).
- [ ] `AcquireLock` uses `store.TxFromContext(ctx)` for any DB writes;
      never the pool directly.
- [ ] `ReleaseClaimItem` (if `ClaimableStore`) does the same.
- [ ] Read-only eligibility hints (`HasClaimableItem`) may use the pool;
      they run outside the atomic acquisition tx.
- [ ] Pure region tests cover literal-literal, literal-glob, glob-glob,
      and disjoint-subtree cases.
- [ ] Integration tests with `pgtest.StartPostgres` cover the real DB
      path (if applicable).
- [ ] Scenario tests exercise the impl end-to-end via `stores.yml` +
      template + instance (if applicable).
- [ ] `Factory{}` is registered explicitly from the three reference
      binaries' `main()` (scheduler, supervisor, control-api), not from
      `init()`.
- [ ] README documents the store's YAML config keys, region grammar, and
      any v1 limitations from §6 that apply to your kind.
