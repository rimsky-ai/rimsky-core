# Store Author Guide

> **Status:** This guide is **v2-shaped and pending a full v3 rewrite.**
> Per `docs/history/2026-04-27-stores-redesign-v3-design.md` §13.1, store
> implementations are now standalone binaries that implement the
> rimsky store-service gRPC protocol
> (`proto/v1/store_service.proto`) — not in-process Go subpackages.
> The contract is:
>
> 1. Implement the 4 + 1 RPC handlers (`Open`, `Commit`, `Abandon`,
>    `Release` plus `Capabilities()`) in any language with
>    gRPC support; the standard reference impls under `stores/` are Go.
> 2. Define your own config schema (loaded from your binary's
>    config-file env var; rimsky never sees it).
> 3. Ship a binary + Dockerfile + `config-example.yml`.
> 4. Honor the **five store-author obligations** from spec §7.8:
>    - Sweep / TTL for orphan reclamation.
>    - Record `claim_id` before any state mutation in `Open` so the
>      sweep can identify orphans.
>    - All terminal verbs idempotent in `claim_id`.
>    - Do not depend on rimsky calling `Abandon` for orphan cleanup.
>    - Canonicalize `region` bytes such that byte-equal correctly
>      indicates conflict (per spec §7.7).
> 5. Auth-blind advisory: rimsky has no machinery for credentials,
>    encryption, or access control. Encrypt sensitive bytes before
>    handing them to rimsky if you need protection.
>
> The reference impls in `stores/filesystem/`, `stores/postgres/`,
> `stores/stub/` are the working examples until this guide is rewritten.
>
> **The text below is v2 reference material — do not follow it for new
> stores. The v3 spec is authoritative.**

---

This guide is for developers writing a new rimsky store implementation.
The v3 contract summarised in the Status banner above is authoritative;
the rest of this file is v2 reference material kept for context only.

For operator context, see `operator-guide.md`. For the concept model,
see `node-graph-design.md`. The vocabulary lives in `docs/glossary.md`
— read it first.

---

## 1. Vocabulary

A **store** is an operator-configured, named data backend (filesystem
directory, postgres database, future S3 / git / etc.). The store's two
primitives:

- **Claim** — a row in `rimsky_lock_holders` keyed by `(store_name,
  region_data, intent)`. The substrate-bound primitive. Held for the
  duration of one node execution (or longer, for held claims under
  inheritance). Halts node dispatch when conflicting claims are held on
  overlapping regions.
- **Named lock** — a row in `rimsky_lock_holders` keyed by `(lock_name,
  limit)`. Non-substrate. The supervisor handles named locks directly via
  `rimsky_lock_holders` without calling into any store; your `Store`
  implementation never sees them.

A claim is acquired via `Open(ClaimSpec)`; the spec carries `(StoreName,
Selector, Intent, Alias)`. Selector is opaque text the substrate parses;
the substrate decides what selectors mean. Recommended convention:
substrate-recognized special-form selectors (`@policy-name`) trigger
configured **pick policies** that pick an item per the policy's logic.

Lock state lives **only** in postgres. Stores may persist *data* state
(e.g. `core/store/postgres/` flips an items-table row to `'in_progress'`
at `Open` for pick-policy claims), but the question "is anyone holding
lock X" is answered exclusively by `rimsky_lock_holders` (blessed
invariant 9a — see `core/store/interface.go`). Stores **must not**
internally serialize on lock-shaped predicates either (blessed invariant
9b — the §9-strategy-2 reader-lease pattern is forbidden); see §6 below.

A store also declares a **`write_semantics`** (spec §8):

- **`direct`** — Writes hit live data. No staging area. r×rw on
  overlapping regions blocks (sync semantics). Default for v1 reference
  implementations.
- **`staged_blocking`** — Writes go to a substrate-private staging area;
  `Commit` does an atomic swap into live. r×rw on overlapping regions
  blocks. Sidecar mode for post-v1.
- **`staged_async`** — Writes go to a staging area; reads see a stable
  view of live state during writes. r×rw on overlapping regions does NOT
  block (async semantics). Honest support requires snapshot delegation or
  native MVCC pass-through.

The substrate's max capability is exposed via `Factory.MaxWriteSemantics()`.
Operator config can downgrade (force `direct` on a `staged_blocking`-
capable store) but not upgrade — `BuildAll` rejects upgrades with a
clear error.

---

## 2. The interfaces

The whole surface lives in `core/store/`:

- `interface.go` — universal 5-verb `Store` interface; `Capabilities`
  (one field).
- `types.go` — `ClaimSpec`, `NamedLockSpec`, `Intent`, `WriteSemantics`,
  `ClaimResult` (with `Address` / `Payload` / `Region` as
  `json.RawMessage`).
- `conflict.go` — `ModeCoexists` helper (the spec §8.5 matrix).
- `tx.go` — `WithTx` / `TxFromContext` for tx plumbing.
- `registry.go` — `Factory`, `Registry`, `BuildAll`, `GetStore`.
- `lockholders.go` — `LockHoldersClient` postgres helpers shared by
  supervisor and scheduler.

### 2.1 `store.Capabilities`

```go
type Capabilities struct {
    WriteSemantics WriteSemantics
}
```

One field. Future capabilities can be added as struct fields without
breaking the interface. The dropped capability fields from the prior
spec — `SupportsRegionLock`, `SupportsClaim`, `SupportsResume`,
`SupportsRestore`, `SupportsDiscard`, `SupportsAtomicMulti`,
`KeepVersionsMax` — are all dissolved (spec §9.1).

### 2.2 `store.Store` (the 5-verb interface)

```go
type Store interface {
    Kind() string
    Name() string
    Capabilities() Capabilities

    RegionsConflict(a, b []byte) bool          // PURE (invariant 14)
    UnmarshalRegion(raw []byte) ([]byte, error) // PURE (invariant 14)

    Open(ctx context.Context, spec ClaimSpec) (ClaimResult, error)
    Commit(ctx context.Context, region []byte, address []byte, policyOverride string) error
    Abandon(ctx context.Context, region []byte, address []byte, policyOverride string) error
    Delete(ctx context.Context, region []byte) error
    Release(ctx context.Context, region []byte, address []byte) error
}
```

Method-by-method:

**`Kind()`** — canonical string the factory registers under. Operators
reference it via `kind:` in `stores.yml`.

**`Name()`** — operator-configured store name; matches the YAML key under
`stores.<name>`. Set at construction time.

**`Capabilities()`** — see §2.1; returns the store's `WriteSemantics`.

**`RegionsConflict(a, b []byte) bool`** — region-overlap predicate. Inputs
are the substrate-canonical bytes (whatever `UnmarshalRegion` produces).
Returns true if the two regions cannot both be held at once.

> **`@blessed-invariant: RegionsConflict and UnmarshalRegion are pure.`**
> No side effects, no external state read; deterministic on inputs (spec
> §21 invariant 14). The supervisor calls these inside the atomic
> acquisition transaction (§13.3) and inside hot eligibility loops;
> impurity here would corrupt acquisition correctness. The annotation
> lives in `core/store/interface.go`. Do not call out to a database, the
> filesystem, or any external system from these two methods.

**`UnmarshalRegion(raw []byte) ([]byte, error)`** — deserialises
`rimsky_lock_holders.region_data` JSONB into your store's canonical-bytes
form. The supervisor calls this on each existing-holder row before passing
the value to `RegionsConflict`. Same purity contract as above.

**`Open(ctx, spec) (ClaimResult, error)`** — produce a substrate-native
address for the executor and register whatever substrate-side state the
`(intent × write_semantics)` combination requires (staging area,
snapshot, MVCC transaction, or nothing). Called inside the supervisor's
atomic acquisition transaction (blessed invariant 15); use
`store.TxFromContext(ctx)` to obtain the open `*pgx.Tx` and route every
substrate-side DB mutation through it (see §3 below).

The substrate detects whether this is a fresh acquisition or a resumed
one **internally by lookup against its own state, keyed by the lock-holder
identity**. There is no `resumed` flag on `Open`; resume is a universal
behaviour pattern, not a capability. The supervisor preserves the lock-
holder row across retries and calls `Open` again; the substrate handles
the resumed-vs-fresh branch.

For pick-policy claims (selectors the substrate recognizes as policy-
form), `Open` invokes the configured pick policy and returns the picked
item's address. The picked identifier becomes the `region_data` on the
lock-holder row. The address shape is **substrate-native bytes**, opaque
to Rimsky; the executor decodes per its substrate-specific knowledge.

**`Commit(ctx, region, address, policyOverride)`** — for regional `rw`
claims on `staged_*` substrates: atomically publish the staging area's
contents into the live region. For pick-policy claims: apply the
configured `on_commit` action (overridable via `policyOverride`). For
`direct`-mode regional `rw` claims: substrate no-op (writes already live;
`Commit` is a confirmation hook). `policyOverride` is meaningful only
for pick-policy claims; ignore it otherwise.

**`Abandon(ctx, region, address, policyOverride)`** — for regional `rw`
on `staged_*`: discard the staging area without publishing. For pick-
policy claims: apply the configured `on_give_up` action (overridable via
`policyOverride`). For `direct`-mode `rw`: degenerate — direct writes
cannot be undone; document this as an honest substrate limitation in
your store's README. Not called for read-only claims.

**`Delete(ctx, region)`** — remove the live region's data. A third
terminal action alongside `Commit` and `Abandon` for nodes whose intent
is deletion. Regional claims only (pick-policy claims express deletion
via `Commit` + `policyOverride = "delete"`).

**`Release(ctx, region, address)`** — tear down substrate-side read state
(snapshot, MVCC transaction) for a read claim. Fires only when the
substrate registered such state (relevant for `staged_async` substrates;
not exercised by any v1 store implementation). Lock-holder row deletion
suffices for read claims under `direct` / `staged_blocking`.

### 2.3 Verb-firing matrix per claim shape

| Claim shape | `write_semantics` | Verbs invoked at terminal |
|---|---|---|
| Regional `r` | `direct` / `staged_blocking` | None — lock-holder row deletion is sufficient |
| Regional `r` | `staged_async` | `Release(region, address)` |
| Regional `rw` | `direct` | `Commit` (no-op) or `Delete` or `Abandon` (degenerate) |
| Regional `rw` | `staged_*` | `Commit` (atomic swap) or `Abandon` or `Delete` |
| Pick-policy claim | (any) | `Commit(..., policyOverride)` or `Abandon(..., policyOverride)` |

For **held claims**, the supervisor's auto-terminal mechanism (v3 spec §4.10 invariant 13)
fires exactly one resolution at holding-subgraph completion. Aggregate
outcome — all-completed → `on_commit`; any-failed → `on_give_up` — drives
the verb. From the store implementation's perspective, the verb call is
indistinguishable from a non-held terminal; the supervisor handles the
subgraph-level coordination.

### 2.4 Specs

```go
type ClaimSpec struct {
    StoreName string  // name of the configured store
    Selector  string  // opaque text (post-substitution)
    Intent    Intent  // "r" | "rw"
    Alias     string  // per-claim name within node; defaults to StoreName
}

type Intent string
const (
    IntentRead      Intent = "r"
    IntentReadWrite Intent = "rw"
)

type NamedLockSpec struct {
    Name string  // operator-configured name
}
```

`ClaimSpec` and `NamedLockSpec` are **distinct types with no common
interface**. Two primitives, two types. The prior spec's `LockSpec`
discriminated-union, `RegionLockSpec` / `ClaimLockSpec`, `LockHandle`,
`NativeHandle` (sealed interface), `ClaimableStore` / `ResumableStore`
sub-interfaces, `HasPriorWork`, `OpenHandle`, `AcquireLock`, `ReleaseLock`,
`ReleaseAction` — all dissolve.

### 2.5 `ClaimResult` and address shape

```go
type ClaimResult struct {
    Address json.RawMessage  // substrate-native pointer
    Payload json.RawMessage  // substrate-supplied data captured at acquisition
    Region  json.RawMessage  // substrate's identifier (resolved selector OR pick)
}
```

All three fields are `json.RawMessage` — opaque bytes from Rimsky's
perspective per **blessed invariant 20** (claim content is inert).
Rimsky reads claim content by named-field path **only** at substitution-
leaf extraction; never logs, validates, transforms, or otherwise
introspects.

**Address shape recommendation:** substrate-native bytes appropriate to
your kind. The executor decodes per its substrate-specific knowledge of
the store's `kind`. For `filesystem`, an address is naturally a path; for
`postgres`, a row locator; for S3, a bucket / key reference. There is no
"rimsky-canonical" address shape — pick what your substrate's executors
need and document it in your store's README.

### 2.6 Pick policies (substrate-side)

Pick policies are **substrate-side configuration**, not a Rimsky-protocol-
level capability. The substrate recognizes special-form selectors
(recommended convention: `@policy-name`) and dispatches internally to the
configured policy's pick logic. Multiple pick policies per store are
supported.

The store's `pick_policies` config block (read by `Factory.Build`) lists
the named policies the store implements. Each entry is keyed by the
recognized selector form (e.g., `@review-queue`) and carries substrate-
specific configuration (item path, ordering, `on_commit_default`,
`on_give_up_default`, visibility timeout, etc.). The schema is substrate-
defined; Rimsky does not standardize it. Document your store's
`pick_policies` schema in its README; the operator-guide cross-references
that documentation.

A pick policy is implemented entirely inside the store. There is no
Rimsky-side interface for "give me an item"; `Open(ClaimSpec)` is the
only entry point, and the substrate dispatches based on the resolved
selector text.

---

## 3. Transaction plumbing

The `core/store/` package exposes a tx-context helper used uniformly by
every store implementation that needs to participate in the supervisor's
outer transaction:

```go
ctx = store.WithTx(ctx, tx)         // supervisor side
tx, ok := store.TxFromContext(ctx)  // store side
```

A store with **no DB writes** (like the direct-mode filesystem store) is
free to call `TxFromContext` and ignore the returned tx. The supervisor
still attaches one so `Open` can be called uniformly.

A store with **DB writes** (like the postgres store at
`core/store/postgres/`) **MUST** use the tx for all its mutations —
never the underlying pool — so atomicity with the supervisor's
lock-holder inserts is preserved (blessed invariant 15). This is
non-negotiable: falling out of the outer tx breaks the §13.3 acquisition
guarantee that either both the lock-holder row and the substrate-side
state flip happen, or neither does.

Read-only paths that run **outside** the atomic acquisition tx (the
visibility-timeout sweep) are free to use the pool directly.

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
        WriteSemantics: store.WriteSemanticsDirect,
    }
}

func (Factory) MaxWriteSemantics() store.WriteSemantics {
    return store.WriteSemanticsDirect  // direct-mode filesystem is the v1 shape
}
```

The single capability field is the operator-effective `write_semantics`
(after registry-build downgrade application). The factory exposes the
substrate's max via `MaxWriteSemantics`; the registry rejects upgrade
attempts at `BuildAll`.

For a direct-mode filesystem store there is no resume-vs-fresh
distinction at the protocol layer — `Open` returns the live path either
way; the executor sees whatever partial state earlier attempts left in
the live region. There is no `resumed` flag and no `HasPriorWork`
sub-interface; both dissolved.

### 4.4 The `RegionsConflict` purity contract

```go
func (s *Store) RegionsConflict(a, b []byte) bool {
    return regionsConflictGlobs(a, b)  // package-level pure helper
}
```

The supervisor passes the canonical bytes from `UnmarshalRegion`; the
package-level helper unpacks them into `[]string` glob lists internally
and returns true if any pair overlaps under the extended
`path/filepath.Match` semantics (`**` = "any path under the prefix"). The
function takes no `ctx`, reads no filesystem, calls no external system;
it can be unit-tested with table-driven cases at microsecond speed.

The blessed-invariant annotation lives at the function's doc comment in
`core/store/interface.go`.

### 4.5 `UnmarshalRegion`

```go
func (s *Store) UnmarshalRegion(raw []byte) ([]byte, error) {
    var globs []string
    if err := json.Unmarshal(raw, &globs); err != nil {
        return nil, fmt.Errorf("filesystem store %q: unmarshal region: %w", s.name, err)
    }
    // Re-marshal in canonical form so RegionsConflict gets stable bytes.
    return json.Marshal(globs)
}
```

The returned bytes are the canonical form `RegionsConflict` operates on.
Symmetry between the on-disk JSONB shape and the canonical bytes is the
simplest path; resist serialising into a struct that needs translation.

### 4.6 `Open`

`Open` produces the executor-visible address. Direct-mode filesystem
constructs an absolute path under the configured root, joining the
resolved selector glob's literal prefix:

```go
func (s *Store) Open(ctx context.Context, spec store.ClaimSpec) (store.ClaimResult, error) {
    // selector text is opaque to Rimsky; substrate parses
    path, err := s.resolvePath(spec.Selector)
    if err != nil {
        return store.ClaimResult{}, err
    }
    addrJSON, _ := json.Marshal(map[string]string{"path": path})
    regJSON, _  := json.Marshal([]string{spec.Selector})
    return store.ClaimResult{
        Address: addrJSON,
        Payload: nil,        // direct-mode filesystem has no per-claim payload
        Region:  regJSON,
    }, nil
}
```

Inside a substrate that maintains DB state (e.g. the postgres store's
items-table flip), `Open` would also call `store.TxFromContext(ctx)` and
route the `UPDATE … SET state='in_progress'` through the supervisor's
open `*pgx.Tx` — see §3.

### 4.7 `Commit`, `Abandon`, `Delete`, `Release`

For a direct-mode filesystem store:

```go
func (s *Store) Commit(_ context.Context, _, _ []byte, _ string) error {
    return nil  // no-op; writes already on disk
}

func (s *Store) Abandon(_ context.Context, _, _ []byte, _ string) error {
    // Degenerate for direct-mode rw — direct writes cannot be undone.
    // README documents this honest substrate limitation.
    return nil
}

func (s *Store) Delete(_ context.Context, region []byte) error {
    // Remove the live region's data per the kind's rules.
    return s.removePath(region)
}

func (s *Store) Release(_ context.Context, _, _ []byte) error {
    return nil  // no read-side state to tear down for direct mode
}
```

For `staged_*` modes (post-v1), `Commit` would atomically swap the
substrate's staging area into the live region (filesystem rename, SQL
`ALTER TABLE` swap, S3 manifest pointer flip, etc.); `Abandon` would
discard the staging area. **Substrate-side fences are brief and applied
at commit** — the dominant pattern is staging + atomic swap.

Run-spanning substrate locks (open transactions held across an executor's
whole run) are an **anti-pattern**: they duplicate the orchestrator's
claim machinery and burn substrate resources. The orchestrator already
holds the claim across the executor's run via `rimsky_lock_holders`; the
substrate doesn't need to layer its own.

### 4.8 Registration

```go
// in core/cmd/rimsky-supervisor/main.go (and -scheduler, -control-api)
storeFactories := []store.Factory{
    filesystem.Factory{},
    pgstore.Factory{Pool: pool},          // core/store/postgres
    // myimpl.Factory{},                  // ← add yours here
}

reg := store.NewRegistry()
for _, f := range storeFactories { reg.Register(f) }
stores, err := reg.BuildAll(storesCfg)    // enforces MaxWriteSemantics ceiling
```

**Do not** register from `init()`. Registration needs the dependencies
(`*pgxpool.Pool`, named connection pools, etc.) wired explicitly at
process startup. Explicit registration is idiomatic rimsky.

Once registered, operators reference your store in `stores.yml`:

```yaml
stores:
  scratch:
    kind: filesystem
    write_semantics: direct
    root: /var/lib/rimsky/scratch
```

And nodes reference it in templates via
`stores: [{name: scratch, selector: "<glob>", intent: rw, alias: <alias>}]`.

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
tests construct a `*Store` directly, call `Open` / `Commit` / `Abandon` /
`Delete` / `Release`, and assert the returned `ClaimResult` shape. No
postgres needed.

### 5.3 Integration tests with real Postgres

For DB-coupled kinds (like the postgres store), use the `pgtest`
harness:

```go
import "github.com/fallguy/rimsky/core/internal/pgtest"

func TestMyStore_Integration(t *testing.T) {
    ctx := context.Background()
    pool, teardown := pgtest.StartPostgres(ctx, t)
    t.Cleanup(teardown)

    // Create the operator-owned items table the store expects (per v3 spec §13.1's items-table provisioning workflow)
    // ... construct your Factory, Build, Open inside a tx, assert ...
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
store, which implements the 5-verb `Store` interface with configurable
in-memory state. A new store kind does not need to modify the stub — it
brings its own fixtures.

---

## 6. Known limitations and store-author guidance

These are accepted in v1 (carried forward from v3 spec §14 / §15) and may bite you in subtle ways.
Document them in your store's README so operators understand the
guarantees.

### 6.1 Store-side serialization on lock-shaped predicates is forbidden

Blessed invariant 9b: a store implementation **does not internally
serialize on lock-shaped predicates**. The §9-strategy-2 reader-lease
serialization pattern (substrate tracks active read leases; writers
block at the substrate boundary to wait them out) is **not a valid
implementation choice for `staged_async`**. Honest support requires:

- **Snapshot delegation** — substrate creates a stable view materialized
  at `Open(read)`; writers operate on the live region; the read view
  remains consistent until `Release`. (Filesystem with COW snapshots, S3
  with manifest pinning, etc.)
- **Native MVCC pass-through** — substrate opens a snapshot transaction
  at `Open(read)` (e.g. postgres `BEGIN ISOLATION LEVEL REPEATABLE READ`
  + `SET TRANSACTION SNAPSHOT`), holds it for the executor's run, ends
  it at `Release`. Writers go through their own `rw` claims; the orchestrator
  enforces no `w×w` overlap, and the substrate handles `r×w` non-blocking
  via MVCC.

A substrate that cannot honestly provide stable reads during writes
declares `staged_blocking` (or `direct`) and lets the scheduler do the
serialization. **Honest `write_semantics` reporting** is a load-bearing
contract — operators rely on it to reason about read-during-write
behavior, and incorrect reporting silently corrupts that reasoning.

### 6.2 Substrate-side fences are brief; run-spanning locks are anti-patterns

The dominant commit pattern is staging + atomic swap (filesystem rename,
SQL `ALTER TABLE` swap, S3 manifest pointer flip, Redis `RENAME`, git
merge). Hold substrate-side state only as long as needed for atomicity
at the swap moment.

**Run-spanning substrate locks** (open transactions held across an
executor's whole run; long-lived advisory locks; reader-leases that span
multiple `Open` / `Release` cycles) duplicate the orchestrator's claim
machinery (`rimsky_lock_holders` + heartbeat + orphan reap) and burn
substrate resources. Don't.

### 6.3 Resumed-vs-fresh detection lives entirely inside the substrate

There is no `resumed` flag at the protocol layer. The supervisor preserves
the lock-holder row across retries and calls `Open` again with the same
`ClaimSpec`; the substrate detects resumed-vs-fresh by lookup against its
own state, keyed by the lock-holder identity (e.g., `claim_token` on the
items-table row). Resume is universal — a behaviour pattern, not a
capability flag.

### 6.4 Multi-store atomic commit is not provided

A node writing to two stores commits independently per store. There is no
two-phase-commit machinery in v1. If the first `Store.Commit` succeeds
and the second fails, the supervisor records a failure but the first
store has already accepted the write.

If your store needs cross-store atomicity, the answer in v1 is "use a
single store" — for example, one postgres store transactionally writing
to multiple tables, behind one `Store` interface.

### 6.5 Region-overlap detection is per-store-kind

There is no cross-kind overlap reasoning. A new store kind brings its
own region grammar and its own pure overlap predicate; Rimsky does not
try to relate `["**/foo"]` to `{"sql_table": "bar"}`.

### 6.6 Postgres pick-policy items tables are operator-owned

Rimsky verifies each pick policy's items-table shape at registry-build
time but does not create it. A new postgres-backed pick-policy
implementation should follow the same rule: verify, don't create.
Operators populate items via direct SQL or via the admin endpoint
(`POST /admin/stores/:name/pick-policies/:selector/items`).

### 6.7 `stores.yml` divergence between processes is silently corrected

control-api and supervisors each build their own `Registry` from process
YAML at startup. If a supervisor is missing a store its peers know about,
that supervisor simply doesn't claim relevant work; dispatch eligibility
fails the supervisor's match. No alarm fires. A new store kind inherits
this property automatically.

### 6.8 `Abandon` is degenerate for `direct`-mode regional `rw` claims

Direct writes can't be undone. Document this as an honest substrate
limitation in your README — templates that require effective `discard`
semantics on `direct` stores are misconfigured. The operator's options:
declare the store `staged_blocking` (and absorb the staging cost) or
restructure the workflow.

---

## 7. Checklist

Before shipping your store implementation:

- [ ] `Factory.Build()` rejects missing or wrong-typed config keys with
      clear errors at registry-build time.
- [ ] `Factory.MaxWriteSemantics()` accurately reports the substrate's
      ceiling — operators can downgrade but never upgrade past it.
- [ ] `Capabilities()` reports the operator-effective `WriteSemantics`
      (post-downgrade) honestly. **No store-side serialization on lock-
      shaped predicates** — see §6.1 / blessed invariant 9b.
- [ ] `RegionsConflict` and `UnmarshalRegion` are pure — no side effects,
      no external state. Annotate the doc comment with `@blessed-invariant`.
- [ ] Unknown region bytes in `RegionsConflict` fail closed (return
      `true` rather than silently admitting an acquisition).
- [ ] `Open` uses `store.TxFromContext(ctx)` for any DB writes; never
      the pool directly. Substrate-side state mutations participate in the
      supervisor's atomic acquisition transaction (blessed invariant 15).
- [ ] `Open` detects resumed-vs-fresh internally by lookup against its
      own state, keyed by lock-holder identity. There is no `resumed`
      flag at the protocol layer.
- [ ] Substrate-side fences are brief and applied at commit (atomic-swap
      pattern). No run-spanning substrate locks (§6.2).
- [ ] Pick policies (if any) are wired entirely substrate-side: the
      `pick_policies` config block is parsed by the factory; selectors
      matching configured policy forms dispatch internally from `Open`.
- [ ] `Address` shape in `ClaimResult` is substrate-native bytes,
      documented in your README so executors can decode.
- [ ] Pure region tests cover the kind's grammar comprehensively.
- [ ] Integration tests with `pgtest.StartPostgres` cover the real DB
      path (if applicable).
- [ ] Scenario tests exercise the impl end-to-end via `stores.yml` +
      template + instance (if applicable).
- [ ] `Factory{}` is registered explicitly from the three reference
      binaries' `main()` (scheduler, supervisor, control-api), not from
      `init()`.
- [ ] README documents the store's YAML config keys, region grammar, the
      address shape, the `pick_policies` schema (if any), the substrate's
      max `write_semantics`, and any v1 limitations from §6 that apply to
      your kind.

---

## Lifecycle events (control-plane v1)

Per `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`. Every
store-service implements six lifecycle methods alongside the four runtime
verbs and `Capabilities`. Stores that don't react to lifecycle events
just return `nil` from each method; the in-tree filesystem and stub
stores follow this pattern (see `stores/filesystem/store/store.go` and
`stores/stub/store/store.go`).

```go
func (s *MyStore) OnTemplateRegistered(ctx context.Context, templateID string) error {
    return nil
}
// ... and similarly for the other five.
```

The six methods:

- `OnTemplateRegistered(ctx, template_id) error`
- `OnTemplateDeployed(ctx, template_id) error`
- `OnTemplateUndeployed(ctx, template_id) error`
- `OnTemplateDeregistered(ctx, template_id) error`
- `OnInstanceCreated(ctx, template_id, instance_id) error`
- `OnInstanceTerminated(ctx, template_id, instance_id) error`

`template_id` is the content hash (`sha256-<64-hex>`); `instance_id` is the
rimsky-generated instance UUID. Both are opaque strings — the store may use
them for namespace routing, audit log entries, or trace correlation.

### Idempotency contract

Each method must be safe to call twice with the same scope IDs. The control-
api may re-fire an event after a partial failure or restart; the second call
must produce the same observable state as the first.

### `Open` scope envelope

`OpenRequest` carries two new fields populated from the dispatch row's
instance → template lookup:

- `template_id` — content hash.
- `instance_id` — instance UUID.

Stores can ignore these fields without affecting protocol conformance; they
exist for stores that want per-template or per-instance namespacing.


## Observability protocol (optional)

Per `docs/specs/2026-05-02-dashboard-and-observability-design.md` §3,
stores MAY implement `proto/v1/store_observability.proto` to expose
per-claim views and optional admin views to dashboards. The dispatch
protocol is unchanged.

The service exposes five RPCs:

- `GetCapabilities` — declares supported sub-features and admin views.
- `GetClaim(claim_id)` — snapshot of one claim's state, history, and
  store-chosen `address`/`payload`/`region`.
- `StreamClaim(claim_id)` — replay-then-live stream, closes with a
  `category: "claim_terminal"` marker.
- `ListClaims` — optional paginated browse.
- `GetAdminView(view_name, params)` — store-internal admin surface.

### Capabilities-only "no observability" pattern

Mirrors the executor pattern: return zero/false flags from
`GetCapabilities`, return `Unimplemented` from the other RPCs. See
`stores/stub/server/observability.go` for the reference Go impl.

### Standard vocabulary

Per spec §3.3: `claim_opened`, `claim_committed`, `claim_abandoned`,
`claim_released`, `conflict_detected`, `log`, plus free-form strings.
Required `attributes` keys per category are listed in spec §3.3.

### `address` / `payload` / `region` exposure

Distinct from the inert-claim invariant in Rimsky core: the store's
own observability surface is free to expose whatever it wants in
`ClaimDetail.address`/`payload`/`region` (it MAY be null, redacted,
partial, or fully rendered). This is the store's call. Rimsky never
asks the store for these fields and never reads them from a
core-side code path; the inert-claim invariant in Rimsky core is
preserved.

### Retention + streaming semantics

Mirror the executor protocol exactly. Eviction returns
`ClaimDetail{state: UNKNOWN, ...}`.

### Admin views

Optional. Stores that want to expose store-internal admin surfaces
(postgres pick-policy queue depth, items table contents; filesystem
mount roots; etc.) declare them in
`StoreObservabilityCapabilities.admin_views` and serve them via
`GetAdminView`. v1 dashboard renders `render_hint` values `table` and
`raw_json`; richer hints (`chart`, `tree`) can be added without
breaking the contract — older dashboards fall back to `raw_json` for
unknown hints.

Reference admin-view implementations:

- `stores/filesystem/server/observability.go` — `pick_policies`
  (per-policy queue depth) + `policy_items` (one row per item).
- `stores/postgres/server/observability.go` — `pick_policies` +
  `items_queue` (queued vs in-progress count per items table).

### Custom UI hook

Same shape as the executor protocol. Marker enumeration for the store
protocol: `{claim_id}`, `{store_name}`.

### Capabilities probe (`--check-observability`)

`rimsky-store-conformance --check-observability` calls
`GetCapabilities`, validates the proto shape, and round-trips each
declared `admin_views` entry that has no required parameters. See
spec §6 for the full probe contract.

Reference: `proto/v1/store_observability.proto`,
`stores/filesystem/server/observability.go`,
`stores/postgres/server/observability.go`,
`stores/stub/server/observability.go`.
