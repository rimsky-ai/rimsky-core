# Stores Redesign — Implementation Spec

**Status:** spec, ready for planning
**Authoritative for:** the redesign work; the discursive companion lives at `docs/2026-04-25-stores-redesign.md` and is the design rationale, not the contract.
**Supersedes on landing:** `docs/node-graph-design.md` §3.4, §4, §5, §6, §8; `docs/architecture.md` §1.2, §3, §5, §8; `docs/protocol.md` (substantially); `docs/executor-author-guide.md`; `docs/resource-author-guide.md` (deleted, replaced by `docs/store-author-guide.md`).

---

## 1. Goal

Replace the current `Resource` abstraction with a `Store` abstraction that admits the underlying systems (filesystems, queues, databases) are shared backends with regions, locks, and commit semantics. Unify three current conflict-management mechanisms (resource ownership, dispatch claim, concurrency tags) under a single primitive (locks). Replace per-run inputs / outputs (proto `result` + resource versions) and claim metadata with a single typed `attributes` table. Make `userdata` truly opaque: rimsky never substitutes into it.

The redesign ships **end-to-end in one execution pass.** No backwards compatibility, no per-commit decomposition, no transitional support for old fields. Pre-v1; dev databases are nuked on adoption.

## 2. Non-goals (deliberately out of scope for this work)

- **Sidecar / versioned modes** for filesystem stores. Only `direct` mode ships.
- **S3 store**, **git store**. The conceptual model accommodates them; implementations land later.
- **Forge API integration** (PR creation for git store).
- **Multi-store atomic commit.** Each store commits independently.
- **Path-narrowing within a git branch** (regions are whole branches, when git ships).
- **Bulk enqueue / priority / visibility metadata** for claim stores.
- **Output merging across upstreams** beyond namespacing by upstream node name.
- **Database direct-mode store.** Attributes replace inline-jsonb resources; external-sql resources are dropped without a v1 replacement (their use case re-emerges when database stores ship).
- **Sidecar-mode store-level quality rules.** Deferred with sidecar mode.
- **Versioned-mode `Restore` API.** Deferred with versioned mode. The existing `RestoreVersion` plumbing (in `scheduler.InvalidateArgs`, `scheduler.InvalidateRequest`, `controlapi/nodes.go invalidateNodeRequest`, related event payloads, the `invalidateRestorePath` function in `core/scheduler/invalidate.go`) is **deleted** in this work — see §16 for the inventory.
- **Migrations.** Pre-v1, no production data: `core/migrations/001-initial.sql` is rewritten in place; `core/migrations/002-data-ref-jsonb.sql` is deleted. The dev DB (including `rimsky_migrations`) must be nuked before running new code.

## 3. Execution constraints

The work is executed end-to-end by autonomous subagents; the user is not present.

- **No interactive prompts.** Every decision in this spec is binding.
- **No remote-side actions.** No `git push`, no PR creation, no Docker-image push, no calls to external services beyond what already exists (testcontainers pulling `postgres:15`, `npm install` in `executors/claude-agent/`).
- **No destructive actions outside the working tree.** Subagents may delete files anywhere within the repo. They may not delete anything outside.
- **`make proto-gen`** is run when proto files change. `protoc` is assumed available; if not, the agent installs it locally without prompting.
- **Tests use Docker** (testcontainers-go for scenarios; docker-compose + testcontainers for the smoke fixture). Docker socket is assumed available.
- **`go test ./... -race -count=1` and `make lint` are mandatory final checks.** Both must pass.
- **`cd executors/claude-agent && npm install && npm test && npm run build`** must pass.

## 4. Motivation (brief)

The `Resource` abstraction treats each datum as a sole-writer-owned versioned unit. The reality:
- 100 markdown files are 100 paths in 1 tree.
- 100 inline-jsonb rows are 100 rows in 1 table.
- 100 external-sql resources are 100 staging tables wrapping rows in 1 caller-owned table.

Costs the redesign removes:

1. Cross-resource atomicity is impossible even when the underlying system trivially provides it.
2. Per-resource staging tables proliferate when `BEGIN; …; COMMIT` would suffice.
3. Lock granularity is hardcoded one-mutex-per-resource.
4. Concurrency tags duplicate locking at a different layer.

The redesign exposes locking, claiming, and committing as first-class primitives over named, operator-configured stores.

## 5. Vocabulary

### 5.1 Store

A **store** is a deployment-level data backend. Configured once by operators in YAML; loaded by control-api and each supervisor at startup.

**There is no `rimsky_stores` database table.** Stores are pure runtime objects built from process YAML config; each process has its own `Registry`. Templates reference stores by name; resolution mirrors today's executor name resolution.

A supervisor pool's config lists the stores it has access to; dispatch eligibility filters out nodes whose required stores aren't in the local pool — analogous to executor `accepted_executors` filtering today.

### 5.2 Region

A **region** is a portion of a store's namespace. v1 region grammars:

| Store kind     | Region grammar                                  |
|----------------|-------------------------------------------------|
| `filesystem`   | path globs (`section-a/**`, `shared/glossary.md`) |
| `claim_store`  | implicit per-claim (the store picks the region) |

(Database, S3, append-log, git regions are described in the discursive design doc; they ship post-v1.)

A node's template declares the regions it `write`s and `read`s on each referenced store.

### 5.3 Lock

A **lock** is a node's exclusivity claim on a named scope or a region within a store, held for the duration of one execution. Acquired before the node enters `running`; released when the node exits `running` (commit, give-up, or preserve-for-resume).

Locks unify three mechanisms today:
- Region exclusive lock = today's resource ownership.
- Counting semaphore on a named lock = today's concurrency tag with limit N.
- Mutex on a named lock = today's concurrency tag with limit 1.

**All lock state lives in postgres**, in `rimsky_lock_holders`. Stores never persist lock state. (Stores may persist *data* state — e.g. `claim-store-postgres` flips an items-table row to `in_progress` when claiming — but that is store data, not lock state. The "is anyone holding lock X" question is answered exclusively by `rimsky_lock_holders`.)

### 5.4 Handle

A **handle** is a native-shape reference to the locked region(s) or claim payload, passed to the executor:

| Store kind                | Handle                                              |
|---------------------------|-----------------------------------------------------|
| `filesystem` (direct)     | absolute directory path; POSIX ops work unmodified  |
| `claim_store`             | the claimed item's payload (read-only JSON)         |

The executor sees the underlying system in its native form. No special "rimsky-store" API. The lock and commit machinery are entirely behind the handle.

### 5.5 Sidecar

In `sidecar` and `versioned` modes (post-v1), a store's per-lock private workspace. **Direct-mode stores have no sidecar** — the handle points at the live region. Failed work in direct mode remains until overwritten.

### 5.6 Claim

A **claim** is the store-picks-region variant of lock acquisition. Two flavors:

1. **Specified-region lock**: caller declares the region; store locks or fails.
2. **Claim**: caller asks the store to pick a region from its eligible pool, lock it, and report the choice.

Both produce the same downstream artifact: a `LockHandle` with identical lifecycle. Only **who chose** differs.

**Multi-claim** is supported: a node may have multiple `stores: [{name: X, claim: true}]` entries from different stores. Each store's claim metadata is namespaced under that store's name in the node's attributes (§5.7).

#### 5.6.1 Payload vs. claim ref

Two things come back from claim acquisition:

- **Payload** — the data that was in the queue / pool item. User data; propagates freely once read.
- **Claim ref** — rimsky-internal bookkeeping (`rimsky_claim_holders` row) for held claims.

These are independent. Payload propagation is irrelevant to claim lifecycle; claim lifecycle is governed by the bookkeeping (who's holding, what action they take when they release).

#### 5.6.2 Claim-and-forget (default)

When the claiming node commits successfully, the claim resolves immediately:
- Queues default `on_commit: delete` (ack).
- Ring buffers default `on_commit: release_to_back`.

On give-up, the claim resolves via `on_give_up`:
- Queues default `on_give_up: release_to_head`.
- Ring buffers default `on_give_up: release_to_back`.

#### 5.6.3 Hold (opt-in)

For workflows where the claim should anchor a longer pipeline, the claim acquisition declares `hold: true`:

- The claim is registered in `rimsky_claim_holders` at commit time of the claiming-source node.
- Holding propagates implicitly through the dependency DAG: any node downstream of a holding source participates in the hold.
- "Terminal-leaf within the holding subgraph" means: a node whose template is downstream of the holding source and which has no further downstream nodes that also depend (transitively) on the holding source.
- At least one such terminal-leaf must declare `claim_resolutions` for the held claim. Template-deploy validation enforces this (algorithm in §11.4).

#### 5.6.4 Reference counting and resolution algorithm

`rimsky_claim_holders` carries one row per (held-claim, terminal-leaf-node) — inserted at commit of the claiming-source node, one per terminal-leaf identified by the template-deploy DAG walk (§11.4).

When a terminal-leaf node commits or gives up, the supervisor runs the following resolution algorithm in **the same DB transaction** that releases the lock-holder row for the terminal node (§13.6).

`rimsky_claim_holders` carries an `actual_action TEXT` column (§9.9.3) populated when the row transitions to `completed`. It records what the terminal actually did — `'delete'`, `'release_to_back'`, or `'release_to_head'` — picked from `on_commit` (terminal committed) or `on_give_up` (terminal gave up). This makes the "did anyone delete?" check unambiguous.

```
For each (claim_id, store_name) where the terminal node is a holder:
  Begin within the outer release transaction.
  R := SELECT row FROM rimsky_claim_holders
       WHERE claim_id = ? AND holder_node_id = ? AND state = 'active'
       FOR UPDATE
  IF R is null:
    // No-op; this terminal isn't holding the claim (already resolved, or not a holder).
    CONTINUE

  action := if terminal_outcome = commit then R.on_commit else R.on_give_up

  // Mark this row completed, recording what we actually did.
  UPDATE rimsky_claim_holders
    SET state = 'completed', completed_at = now(), actual_action = action
    WHERE id = R.id

  IF action = 'delete':
    // First-delete-wins (within this frame). Check if a sibling already deleted.
    PRIOR_DELETE := SELECT 1 FROM rimsky_claim_holders
                    WHERE claim_id = ? AND state = 'completed' AND actual_action = 'delete'
                      AND id != R.id
                      AND frame_id IS NOT DISTINCT FROM R.frame_id
                    LIMIT 1
    IF PRIOR_DELETE is null:
      // We're the first; perform the items-table delete and mark same-frame siblings completed.
      DELETE FROM <items_table_for_store> WHERE item_id = R.claim_id
      UPDATE rimsky_claim_holders
        SET state = 'completed', completed_at = now(), actual_action = 'delete_won'
        WHERE claim_id = ? AND state = 'active' AND id != R.id
          AND frame_id IS NOT DISTINCT FROM R.frame_id
    // ELSE: a sibling already deleted; our row is just bookkeeping. No items-table action.

  ELSE  // action ∈ {'release_to_back', 'release_to_head'}
    // Last-released-wins (within this frame). Only fire the store-side release
    // when ALL same-frame siblings are completed AND none of them deleted.
    ACTIVE_COUNT  := SELECT count(*) FROM rimsky_claim_holders
                     WHERE claim_id = ? AND state = 'active'
                       AND frame_id IS NOT DISTINCT FROM R.frame_id
    DELETE_COUNT  := SELECT count(*) FROM rimsky_claim_holders
                     WHERE claim_id = ? AND state = 'completed'
                       AND actual_action IN ('delete', 'delete_won')
                       AND frame_id IS NOT DISTINCT FROM R.frame_id
    IF ACTIVE_COUNT = 0 AND DELETE_COUNT = 0:
      // ctx_with_tx wraps the outer transaction via store.WithTx (§8.4.1).
      store.ReleaseClaimItem(ctx_with_tx, claim_id, action)  // sets items_table row state='available',
                                                              // claim_token=NULL, repositions per action
```

Two refinements vs. an "obvious" reading of the algorithm, both required for ring-buffer claim stores that recycle `claim_id` across cycles:

- The "current row" SELECT filters `state='active'`. Historical 'completed' rows for the same `(claim_id, holder_node_id)` pair coexist with the live row (the unique index is partial on `state='active'`, see §9.9.3); an unfiltered SELECT would error with "more than one row" when prior cycles' completed rows are still in the table.
- The sibling-count predicates scope by `frame_id IS NOT DISTINCT FROM R.frame_id`. Without that scoping, a prior cycle's completed 'delete' / 'delete_won' row on the same reused `claim_id` would falsely match the "did anyone delete?" predicate for a fresh cycle and suppress the legitimate release. v1 frame_id is observability-only at the schema level (§10.4 of the frame-resolution spec — "v1 logic does not key on frame_id") — that constraint applies to index keying, not to in-tx correctness scoping.

The `'delete_won'` sentinel marks the sibling rows that were collapsed by a winning delete (so a later release sweep doesn't try to fire its own release path). The semantics: once any sibling resolves with `delete`, the claim is gone and no release fires regardless of count.

The whole sub-flow runs in the outer transaction; failure rolls back lock release. The `items_table` updates are done by the store, which is given the open transaction handle (§13.6 / `store.TxFromContext` per §8.4.1).

For linear chains (one terminal): one row, one resolution. For fan-out (N terminals): N rows; the count walks down. Mixed delete + release: the delete branch wins regardless of order; subsequent releases see `DELETE_COUNT > 0` and skip the store-side release.

Control-API exposes `GET /claims/:claim_id/holders` for debugging.

### 5.7 Attributes

A node's **attributes** is a single per-run typed data object, schema-declared in the template, persisted in `rimsky_node_attributes`. Attributes replace and unify per-run inputs (today's `deps_data` / `reads_data` proto fields), per-run outputs (today's `Complete.result` + resource version writes), and claim metadata.

Each schema property may carry a `source:` directive. Source kinds:

- `source: "{{deps.<node>.<field>}}"` — populated at dispatch from the named upstream node's attributes.
- `source: "{{claim.<store>.<field>}}"` — populated at dispatch from the claim payload of `<store>` on this node. The payload field is `payload.<...>` so the full directive is e.g. `"{{claim.<store>.payload.area}}"`.
- `source: "{{params.<key>}}"` — populated at dispatch from instance params.
- *(no `source:`)* — populated by the executor (or supervisor for native nodes) during the run.

**Rimsky owns all `{{...}}` substitution.** Executors do no substitution.

Field provenance (source-driven vs. executor-populated) is determined at retry / resume time by inspecting the in-memory schema: a property declaration with a `source:` directive is source-driven; a property declaration without one is executor-populated. This is the only fact the runtime reads about a property's source-status.

#### 5.7.1 Validation

Attributes use JSON Schema draft-07 (matches what `github.com/santhosh-tekuri/jsonschema/v5` parses by default; library is added as a dependency). Validation runs at two points:

- **At dispatch**, after substitution: every required source-directive must resolve. Failure raises `template_resolution_failed`, routed through the node's policy chain.
- **At commit**, after the executor's writes are merged: the populated attributes object must validate against the schema. Failure raises `attributes_schema_failed`, routed through the policy chain.

#### 5.7.2 Writeback patterns

- **Terminal-final** (default for short-running executors): executor accumulates writes in-process; emits `Complete{ attributes_delta: {...} }` as the terminal event. Supervisor merges into `rimsky_node_attributes.data`, validates, persists.
- **Incremental-via-callback**: executor calls `POST {callback_url}/v1/attributes/{node_id}` per field-write (or batch). Supervisor merges and persists each call. Terminal `Complete` carries no `attributes_delta`. Survives executor death; partial state preserved automatically.

The TS `claude-agent` executor uses incremental writeback.

#### 5.7.3 Resumption interaction

`rimsky_node_attributes.data` is the resumable state for the executor.

- `resumable: true` + `resume_then_retry`: source-driven fields **repopulated** at dispatch (upstream may have changed); executor-populated fields **preserved** verbatim.
- `resumable: true` + `discard_then_retry`: source-driven fields repopulated; executor-populated fields **cleared**.
- `resumable: false` + retry of any kind: source-driven fields repopulated; executor-populated fields cleared.

Default is `resumable: false`. `run_attempt` is incremented on every retry (whether source or executor fields cleared) and is exposed to the executor in the dispatch handle for visibility (`ExecuteRequest.run_attempt`).

### 5.8 Userdata

Userdata is purely executor configuration: model, system-prompt reference, tool list, prompt-construction strategy. **Rimsky never parses, substitutes, or validates userdata.** Substitution syntax (`{{...}}`) inside a userdata value is treated as literal bytes by rimsky; if an executor wants to interpret it, that's the executor's choice.

The executor reads from the dispatch's `attributes` object to get per-run data. How it composes that with `userdata` is the executor's concern (e.g., `claude-agent` exposes attributes as MCP tool inputs; `http-node` puts attributes in the request body).

## 6. Modes

A store declares one of three modes at deployment time. v1 ships only `direct`.

### 6.1 Direct (v1)

- Lock acquisition + handle to the live region.
- Atomicity at the underlying primitive's granularity (filesystem: per-file rename; postgres: per-statement / per-transaction).
- Resumption is trivial: in-progress work is in the live region; next dispatch picks up where the prior left off.
- No sidecar to discard. Failed work persists until overwritten. `discard_then_retry` is effectively `keep_then_retry` for direct-mode stores; templates against direct-mode stores must understand this. Documented as a known limitation in `store-author-guide.md`.

### 6.2 Sidecar (post-v1)

- Lock + handle to a private working copy.
- On `Complete{changed: true}`: store atomically applies sidecar to live.
- On `Complete{changed: false}`: store discards sidecar.
- On `discard_then_retry`: sidecar discarded.
- On `resume_then_retry`: sidecar preserved.

### 6.3 Versioned (post-v1)

- Sidecar mode + retained committed history + `Restore(target)`.

## 7. Claim stores (v1)

A `claim_store` holds a backlog of items and hands them out via claim. Configuration determines policy.

### 7.1 Items

Each item has a store-assigned ID and a JSON payload. Order may be FIFO, priority-based, or store-policy.

### 7.2 Operations (rimsky-side)

- **Claim** — atomic acquisition of the next eligible item.
- **Acknowledge / Delete** — mark item permanently complete (item removed).
- **Release** — return item to store for re-acquisition (`release_to_head` or `release_to_back`).

### 7.3 Operations NOT in rimsky's vocabulary

Enqueue / append / item-creation. These are store-external — a store's own HTTP/admin endpoint, used by operators or by external producers. Rimsky does not push items into claim stores.

For `claim-store-postgres`, the items table is a postgres table with a documented schema (§9.10). Operators populate it via direct SQL or via `POST /admin/claim-stores/:name/items` exposed by `core/controlapi`. The control-api endpoint is admin-only, gated by the existing admin auth header (re-uses today's `X-Rimsky-Admin-Token` mechanism unchanged).

### 7.4 Configurations

- **Queue**:
  ```yaml
  inbound:
    kind: claim_store
    backend: postgres
    items_table: inbound_items
    on_commit_default:  delete
    on_give_up_default: release_to_head
    visibility_timeout_seconds: 300
  ```

- **Ring buffer**:
  ```yaml
  topics-ring:
    kind: claim_store
    backend: postgres
    items_table: topics_items
    on_commit_default:  release_to_back
    on_give_up_default: release_to_back
    visibility_timeout_seconds: 300
  ```

### 7.5 Capabilities

- `SupportsRegionLock`: false
- `SupportsClaim`: true
- `SupportsDiscard`: true (semantics: store-side release-on-give_up; not a sidecar discard)
- `SupportsResume`: true (claim ref preserved; same item re-handed)
- `SupportsRestore`: false

The terminology overload of "discard" (sidecar-discard vs. claim-release-on-give_up) is real. The `Capabilities.SupportsDiscard` flag means "this store accepts a `ReleaseAction` of `discard` and does the right thing for its kind." For claim stores, that "right thing" is the configured `on_give_up`. For sidecar/versioned stores it's literal sidecar discard. Direct-mode filesystem stores set `SupportsDiscard: false` since they have no equivalent.

### 7.6 Pacing

Per-claim-store concurrency caps are **not** a claim-store property. Cap parallel claims via a counting semaphore on a named lock the claim-nodes share:

```yaml
locks:
  - { name: "topics-ring:concurrent-claims", mode: counting, limit: 5 }
```

### 7.7 Visibility timeout vs. heartbeat

`claim-store-postgres` honours `visibility_timeout_seconds` only as a backstop. Rimsky's heartbeat is authoritative: the supervisor heartbeat tick extends the lock-holder row's `expires_at`; the orphan reaper (§13.5) releases the lock-holder when expired and the orphan-claim release path resets the items-table row to `state='available'`.

Visibility timeout default is `300s` (≥ `2 × 5 × heartbeat_interval`) so the heartbeat-driven release fires first under healthy conditions.

**Sweep ownership.** The visibility-timeout sweep is owned by the **scheduler** process (alongside the existing dispatch-claim sweep and the new lock-holder sweep, all under the same `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`). Cadence: once per scheduler tick. For each `claim-store-postgres` store in the scheduler's local registry, the sweep runs:

```sql
UPDATE <items_table>
   SET state = 'available', claim_token = NULL, claimed_at = NULL
 WHERE state = 'in_progress'
   AND claimed_at < now() - (<visibility_timeout_seconds> * interval '1 second')
   AND NOT EXISTS (
         SELECT 1 FROM rimsky_lock_holders
          WHERE store_name = <this_store_name>
            AND claim_id = <items_table>.item_id::text
       )
```

The `NOT EXISTS` guarantees the heartbeat path always runs first: as long as a lock-holder row exists for the claim, the store-side sweep skips the row.

## 8. Capabilities and Store interface

### 8.1 Package layout

- **`core/store/`** — interfaces, types, registry, transaction-context helpers. Imports `core/shared/` and `pgx/v5` (for the transaction-context helpers in §8.4.1; `pgx.Tx` is the only `pgx` symbol leaked through this package's public surface).
- **`core/store/filesystem/`** — direct-mode filesystem store. Imports `core/store/` and stdlib.
- **`core/store/claimstorepg/`** — claim-store-postgres. Imports `core/store/`, `core/shared/`, `pgx/v5`.
- **`core/attributes/`** — substitution engine, JSON Schema validation, callback handler. Imports `core/shared/` and `pgx/v5`. Does **not** import `core/node/` (the substitution grammar is owned here, not in `node/`).
- **`core/node/`** — template parsing imports `core/attributes/` for the substitution-grammar types (e.g. `attributes.Schema`).

### 8.2 Capabilities (Go)

```go
package store

type Capabilities struct {
    SupportsRegionLock  bool
    SupportsClaim       bool
    SupportsDiscard     bool
    SupportsResume      bool
    SupportsRestore     bool
}
```

Notably absent from v1: `SupportsAtomicMulti`, `KeepVersionsMax`. Both belong to deferred features and are not declared until those land.

### 8.3 Lock specs

```go
package store

type LockMode string

const (
    LockModeMutex    LockMode = "mutex"
    LockModeCounting LockMode = "counting"
)

type LockSpec interface {
    Kind() string  // "named" | "region" | "claim"
}

type NamedLockSpec struct {
    Name  string
    Mode  LockMode
    Limit int  // for counting; >=1; ignored for mutex
}

func (NamedLockSpec) Kind() string { return "named" }

type RegionLockSpec struct {
    StoreName string
    Region    any         // store-kind-specific (e.g. []string of globs for filesystem)
    Resumable bool
}

func (RegionLockSpec) Kind() string { return "region" }

type ClaimLockSpec struct {
    StoreName string
    Criteria  map[string]any  // optional filters; nil for any item
    Hold      bool
    OnCommit  string          // overrides store default
    OnGiveUp  string          // overrides store default
    Resumable bool
}

func (ClaimLockSpec) Kind() string { return "claim" }
```

### 8.4 Lock handle and results

```go
package store

type LockHandle struct {
    ID             string  // FK target == rimsky_lock_holders.id (UUID stringified)
    Kind           string  // "named" | "region" | "claim"
    StoreName      string  // empty for named locks
    HolderNodeID   string
    SupervisorID   string  // matches rimsky_supervisors.id (TEXT)
    AcquiredAt     time.Time
    ExpiresAt      time.Time
}

type ClaimResult struct {
    ResolvedRegion any     // store-kind-specific; nil for non-claim acquisitions
    Payload        any     // user-data payload from the claimed item; nil for non-claim acquisitions
    ClaimID        string  // store-assigned item identifier; FK target for rimsky_claim_holders.claim_id
}

type ReleaseAction string

const (
    ReleaseCommit         ReleaseAction = "commit"
    ReleaseDiscard        ReleaseAction = "discard"
    ReleaseGiveUp         ReleaseAction = "give_up"
    ReleasePreserveResume ReleaseAction = "preserve_for_resume"
)

type CommitResult struct {
    Changed       bool
    ChangeSummary string
}
```

`LockHandle.HolderNodeID` and `SupervisorID` are convenience fields populated from the inserted lock-holder row; the supervisor uses them for log/event annotation. They are not load-bearing for correctness (the SQL row is the authoritative source).

### 8.4.1 Transaction plumbing

The `core/store/` package exposes a tx-context helper used uniformly by every store implementation that needs to participate in the supervisor's outer transaction:

```go
package store

type txKey struct{}

// WithTx attaches an open *pgx.Tx to the context. Used by the supervisor's
// runner before calling Store methods that may need to write inside the same tx.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
    return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext returns the *pgx.Tx attached via WithTx, or (nil, false) if
// none is present. Stores that have no DB writes (e.g. filesystem-direct)
// may ignore the tx; the supervisor still attaches one so AcquireLock /
// ReleaseLock can be called uniformly.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
    tx, ok := ctx.Value(txKey{}).(pgx.Tx)
    return tx, ok
}
```

A store with no DB writes (like `filesystem-direct`) is free to call `TxFromContext` and ignore the returned tx. A store with DB writes (like `claim-store-postgres`) **must** use the tx for all its mutations — never the underlying pool — so atomicity with the supervisor's lock-holder inserts is preserved.

### 8.5 Store interface

```go
package store

type Store interface {
    Kind() string  // canonical: "filesystem" | "claim_store"
    Name() string  // operator-configured; matches stores.<name> in YAML

    Capabilities() Capabilities

    // Eligibility check used by the dispatch eligibility evaluator.
    // For region locks, the supervisor calls this after pre-loading existing
    // holders for the store; the implementation can rely on the caller
    // having already screened against existing holders via RegionsConflict.
    LockEligible(ctx context.Context, spec LockSpec) (bool, error)

    // Region-overlap predicate; called by the supervisor when comparing a
    // candidate region acquisition against an existing holder for this store.
    // Returns true if the two regions conflict (cannot both be held at once).
    // MUST BE PURE: no side effects, no state read, deterministic on inputs.
    // (Annotated @blessed-invariant in the store interface file.)
    RegionsConflict(a, b any) bool

    // UnmarshalRegion deserialises region_data JSONB into the store's
    // in-Go region type. The supervisor calls this on each existing-holder
    // row before passing to RegionsConflict.
    UnmarshalRegion(raw []byte) (any, error)

    // AcquireLock is called inside the supervisor's atomic acquisition
    // transaction (§13.3). For direct-mode filesystem this is a no-op and
    // returns ClaimResult{} unchanged. For claim_store this performs the
    // atomic items-table flip (state='in_progress') and returns the picked
    // item's payload + ID. The store is given the open *pgx.Tx via ctx
    // (key store.txKey) so its writes participate in the same transaction.
    AcquireLock(ctx context.Context, spec LockSpec) (LockHandle, ClaimResult, error)

    // OpenHandle constructs the executor-facing handle. For resumable acquisitions
    // the supervisor passes resumed=true; the store may surface prior in-progress
    // state (e.g. an open sidecar workspace). For direct-mode filesystem,
    // resumed is ignored and the live path is returned in either case.
    OpenHandle(ctx context.Context, lh LockHandle, resumed bool) (NativeHandle, error)

    // Commit is called after the executor signals Complete{changed: true}.
    // For direct-mode filesystem this is a no-op (writes already on disk;
    // returns CommitResult{Changed: true}). For sidecar/versioned modes
    // (post-v1) this applies the sidecar to live atomically.
    Commit(ctx context.Context, lh LockHandle) (CommitResult, error)

    // ReleaseLock honours the action: claim stores invoke their on_commit /
    // on_give_up policy; sidecar/versioned stores discard the sidecar (for
    // give_up/discard) or keep it (for preserve_for_resume); direct-mode
    // filesystem is a no-op for all actions.
    ReleaseLock(ctx context.Context, lh LockHandle, action ReleaseAction) error
}
```

#### 8.5.1 Optional capability sub-interfaces

A store implementation that advertises a capability MUST also satisfy the corresponding sub-interface; the supervisor type-asserts at dispatch time. v1 needs:

```go
package store

type ClaimableStore interface {
    Store
    HasClaimableItem(ctx context.Context, criteria map[string]any) (bool, error)
    // ReleaseClaimItem performs the items-table reposition for the given claim
    // ID, per the supplied action ('release_to_back' | 'release_to_head').
    // Called by the §5.6.4 last-released-wins branch; the lock-holder row may
    // already be deleted at this point, so the call takes claim_id directly
    // rather than a LockHandle. Runs inside the caller-provided tx via
    // store.TxFromContext(ctx).
    ReleaseClaimItem(ctx context.Context, claimID string, action string) error
}

type ResumableStore interface {
    Store
    HasPriorWork(ctx context.Context, spec LockSpec) (bool, error)
}
```

`DiscardableStore.Discard` is **not** a separate method in v1 — `ReleaseLock(handle, ReleaseDiscard)` covers the use case. (For sidecar mode post-v1, the same `ReleaseLock` path branches on action.)

`RestorableStore` is post-v1; not declared in this work.

### 8.6 Native handle

`NativeHandle` is a sealed interface; concrete types per store kind. v1:

```go
package store

type NativeHandle interface{ nativeHandleMarker() }

type FilesystemDirectHandle struct {
    Path         string
    WriteRegions []string
    ReadRegions  []string
}

func (FilesystemDirectHandle) nativeHandleMarker() {}

type ClaimStoreHandle struct {
    Payload   any
    ClaimID   string
    StoreName string
}

func (ClaimStoreHandle) nativeHandleMarker() {}
```

The executor protocol serialises these into the `handle` field of `ExecuteRequest.stores[<name>]` (§12.1) as `google.protobuf.Struct`; the executor unmarshals into kind-specific shapes per its own concerns.

### 8.7 Factory + registry

```go
package store

type Factory interface {
    Kind() string
    Build(name string, cfg map[string]any) (Store, error)
}

type Registry struct { ... }

func (r *Registry) Register(f Factory)
func (r *Registry) BuildAll(cfg StoresConfig) (map[string]Store, error)
func (r *Registry) GetStore(name string) (Store, bool)

type StoresConfig struct {
    Stores map[string]map[string]any  // top-level "stores" map from YAML
}
```

`StoresConfig.Stores[name]["kind"]` selects the factory; remaining keys are passed to `Factory.Build`.

The control-api and each supervisor build their own `Registry` from process YAML at startup (`stores.yml` path via env var `RIMSKY_STORES_CONFIG`, default `/etc/rimsky/stores.yml`). Stores are not exchanged across processes — coordination is via postgres lock-holder rows.

## 9. Schema

`core/migrations/001-initial.sql` is **rewritten in place** to the end-state schema below. (See §16.3 for the full deletion list, including `002-data-ref-jsonb.sql`.)

The migration runner code in `core/migrations/runner.go` is preserved unchanged. Existing dev databases must be fully nuked before running the new code (since `rimsky_migrations` would think `001-initial.sql` is already applied, and the rewritten content won't re-run). CHANGELOG entry + operator-guide update document this explicitly.

The full end-state schema:

### 9.1 `rimsky_migrations` (preserved verbatim)

```sql
CREATE TABLE IF NOT EXISTS rimsky_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 9.2 `rimsky_templates` (preserved verbatim)

```sql
CREATE TABLE IF NOT EXISTS rimsky_templates (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    version     TEXT NOT NULL,
    spec        JSONB NOT NULL,
    deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, version)
);
```

The `spec` JSONB now carries the new template shape (`stores`, `locks`, `attributes`, `claim_resolutions`); template-deploy validation runs on the new shape.

### 9.3 `rimsky_instances` (preserved verbatim)

```sql
CREATE TABLE IF NOT EXISTS rimsky_instances (
    id           UUID PRIMARY KEY,
    template_id  UUID NOT NULL REFERENCES rimsky_templates(id),
    consumer_key TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (template_id, consumer_key)
);
```

### 9.4 `rimsky_nodes` (modified — `concurrency_tags` removed; everything else preserved)

```sql
CREATE TABLE IF NOT EXISTS rimsky_nodes (
    id                    UUID PRIMARY KEY,
    instance_id           UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_type             TEXT NOT NULL,
    executor              TEXT,                      -- supervisor executor name; null for native nodes
    schedule_cron         TEXT,
    state                 TEXT NOT NULL,             -- fresh|stale|running|failed
    dependencies          UUID[] NOT NULL,
    current_error_class   TEXT,
    retry_counter         INT NOT NULL DEFAULT 0,
    action_index          INT NOT NULL DEFAULT 0,
    last_heartbeat_at     TIMESTAMPTZ,
    assigned_supervisor_id TEXT,                     -- TEXT, matches rimsky_supervisors.id
    kill_requested        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_nodes_state_updated_at_idx ON rimsky_nodes (state, updated_at);
CREATE INDEX IF NOT EXISTS rimsky_nodes_instance_id_node_type_idx ON rimsky_nodes (instance_id, node_type);
```

`concurrency_tags TEXT[]` is dropped. Per-node concurrency control now lives in `locks: [...]` template declarations enforced via `rimsky_lock_holders`.

### 9.5 `rimsky_supervisors` (preserved verbatim — TEXT id retained)

```sql
CREATE TABLE IF NOT EXISTS rimsky_supervisors (
    id                  TEXT PRIMARY KEY,
    accepted_executors  TEXT[] NOT NULL,
    concurrency         INT NOT NULL,
    callback_host       TEXT,
    callback_port       INT,
    last_heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_node_count   INT NOT NULL DEFAULT 0,
    registered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rimsky_supervisors_last_heartbeat_at_idx ON rimsky_supervisors (last_heartbeat_at);
```

A new column **is** added to track the local store registry for the pool-specialization predicate (§14.2):

```sql
-- column is part of the rewritten 001-initial.sql, not a separate migration:
accepted_stores TEXT[] NOT NULL DEFAULT '{}'
```

`core/storage/postgres/supervisors.go` is updated so the Upsert SQL writes both `accepted_executors` and `accepted_stores`, and the scan reads both.

### 9.6 `rimsky_dispatch` (modified — `concurrency_tags` removed; `required_stores` added; `claimed_by` remains TEXT)

```sql
CREATE TABLE IF NOT EXISTS rimsky_dispatch (
    id                UUID PRIMARY KEY,
    node_id           UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    executor_name     TEXT,                          -- nullable for native nodes
    required_stores   TEXT[] NOT NULL DEFAULT '{}',  -- denormalized at enqueue time
    enqueued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_by        TEXT,                          -- supervisor id (TEXT); null until claimed
    claimed_at        TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,                   -- new column for the dispatch-claim sweep
    UNIQUE (node_id)
);
CREATE INDEX IF NOT EXISTS rimsky_dispatch_pending_idx       ON rimsky_dispatch (enqueued_at) WHERE claimed_by IS NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_claimed_idx       ON rimsky_dispatch (claimed_by, claimed_at) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS rimsky_dispatch_heartbeat_idx     ON rimsky_dispatch (last_heartbeat_at) WHERE claimed_by IS NOT NULL;
```

Changes vs. today's schema:
- `concurrency_tags TEXT[]` dropped (replaced by named-lock predicate at claim time).
- `executor_name` becomes nullable to accommodate native (claim-only) nodes.
- `required_stores TEXT[]` added — populated at enqueue time from the template's per-node-type `nodeRequiredStores(node_type)`. Used by §14.2's pool-specialization predicate.
- `last_heartbeat_at` added. The existing dispatch-claim sweep (`ListOrphanedClaims` in `core/queue/postgres/queue.go`) currently reads `rimsky_dispatch.claimed_at`; in the redesign the sweep predicate **switches to `last_heartbeat_at`** so claim age tracks heartbeat liveness rather than initial-claim time. This is part of the §16.2 `core/queue/postgres/queue.go` rewrite.

### 9.7 `rimsky_schedules` (preserved verbatim)

```sql
CREATE TABLE IF NOT EXISTS rimsky_schedules (
    node_id        UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    cron_expr      TEXT NOT NULL,
    next_fire_at   TIMESTAMPTZ NOT NULL,
    last_fired_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS rimsky_schedules_next_fire_at_idx ON rimsky_schedules (next_fire_at);
```

### 9.8 `rimsky_events` (preserved verbatim; payload kinds extended)

```sql
CREATE TABLE IF NOT EXISTS rimsky_events (
    id          BIGSERIAL PRIMARY KEY,
    instance_id UUID REFERENCES rimsky_instances(id) ON DELETE CASCADE,
    node_id     UUID REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

New event kinds (also added to `proto/v1/events.proto` as typed payloads):
- `lock_acquired`, `lock_released`, `lock_orphan_reaped`
- `attributes_substituted`, `attributes_committed`, `attributes_validation_failed`
- `claim_acquired`, `claim_held`, `claim_resolved` (carries `action`, `claim_id`, `store_name`)
- `template_resolution_failed`

Removed event kinds (associated payload messages dropped from `events.proto`):
- `commit` (per-resource commit; superseded by `attributes_committed`)
- `pure_cascade_commit` (the commit semantics fold into attributes)

Preserved event kinds: `state_transition`, `error`, `work_started`, `work_completed`, `no_op_commit`, `quality_rule_failed`, `heartbeat_lost`, `operator_override`, `orphaned_claim_released`, `orphaned_claim_lost_race`, `work_rejected`, `schedule_fired`, `unresolved_executor`, `schedule_dispatch_failed`, `message_emitted`, `message_received`.

### 9.9 New tables

#### 9.9.1 `rimsky_node_attributes`

```sql
CREATE TABLE IF NOT EXISTS rimsky_node_attributes (
    node_id     UUID PRIMARY KEY REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    run_attempt INT NOT NULL DEFAULT 0,
    data        JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Lifecycle:
- Created lazily on first dispatch of a node.
- `data` populated at dispatch with source-directive substitutions; updated by executor writeback (incremental or terminal-final).
- On retry: `run_attempt` increments; `data` cleared per §5.7.3 (source-driven fields repopulated from upstream / claim / params; executor-populated fields cleared unless `resumable: true` + `resume_then_retry`).
- On commit: validated against the template's schema; persisted.
- On invalidate: row preserved (audit trail).
- On instance delete: row CASCADE-deletes via `node_id` FK.

#### 9.9.2 `rimsky_lock_holders`

```sql
CREATE TABLE IF NOT EXISTS rimsky_lock_holders (
    id                   UUID PRIMARY KEY,
    lock_kind            TEXT NOT NULL,           -- 'named' | 'region' | 'claim'
    lock_name            TEXT,                    -- non-null for kind='named'
    store_name           TEXT,                    -- non-null for kind in ('region','claim')
    region_data          JSONB,                   -- non-null for kind='region'
    claim_id             TEXT,                    -- non-null for kind='claim'
    holder_supervisor_id TEXT NOT NULL,           -- TEXT, FK target rimsky_supervisors.id
    holder_node_id       UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    claimed_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at           TIMESTAMPTZ NOT NULL,
    CONSTRAINT lock_kind_fields CHECK (
        (lock_kind = 'named' AND lock_name IS NOT NULL AND store_name IS NULL AND region_data IS NULL AND claim_id IS NULL) OR
        (lock_kind = 'region' AND lock_name IS NULL AND store_name IS NOT NULL AND region_data IS NOT NULL AND claim_id IS NULL) OR
        (lock_kind = 'claim'  AND lock_name IS NULL AND store_name IS NOT NULL AND region_data IS NULL AND claim_id IS NOT NULL)
    )
);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_named_idx      ON rimsky_lock_holders (lock_name) WHERE lock_kind = 'named';
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_store_idx      ON rimsky_lock_holders (store_name) WHERE lock_kind IN ('region','claim');
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_supervisor_idx ON rimsky_lock_holders (holder_supervisor_id);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_expires_idx    ON rimsky_lock_holders (expires_at);
CREATE INDEX IF NOT EXISTS rimsky_lock_holders_node_idx       ON rimsky_lock_holders (holder_node_id);
```

Lifecycle:
- Inserted atomically with dispatch claim (§13.3).
- `last_heartbeat_at` and `expires_at` extended on each supervisor heartbeat tick.
- Removed on `ReleaseLock` (regardless of action), claimant-guarded.
- Orphan-reaped at `5 × heartbeat_interval` (§13.5).

#### 9.9.3 `rimsky_claim_holders`

```sql
CREATE TABLE IF NOT EXISTS rimsky_claim_holders (
    id              UUID PRIMARY KEY,
    claim_id        TEXT NOT NULL,
    store_name      TEXT NOT NULL,
    holder_node_id  UUID NOT NULL REFERENCES rimsky_nodes(id) ON DELETE CASCADE,
    on_commit       TEXT NOT NULL,                -- declared: 'delete' | 'release_to_back' | 'release_to_head'
    on_give_up      TEXT NOT NULL,                -- declared: same vocabulary
    actual_action   TEXT,                         -- recorded at completion: 'delete' | 'release_to_back' | 'release_to_head' | 'delete_won'
    state           TEXT NOT NULL DEFAULT 'active', -- 'active' | 'completed'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
-- Per-active-cycle uniqueness: ring-buffer claim stores reuse `claim_id`
-- (= `item_id`) across cycles, so once a holder transitions to
-- 'completed' a fresh 'active' row must be insertable for the next cycle.
-- The partial WHERE clause enforces "one ACTIVE holder per (claim, leaf)
-- at a time" while permitting historical 'completed' rows to coexist.
CREATE UNIQUE INDEX IF NOT EXISTS rimsky_claim_holders_claim_node_idx
    ON rimsky_claim_holders (claim_id, holder_node_id) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS rimsky_claim_holders_active_idx ON rimsky_claim_holders (claim_id) WHERE state = 'active';
```

`actual_action` is null while `state='active'`; populated when the row transitions to `'completed'` per the §5.6.4 algorithm. The `'delete_won'` value marks a sibling row that was collapsed by another sibling's winning delete.

Lifecycle:
- Inserted at commit of the claiming-source node (when `hold: true`), one row per (claim_id, terminal-leaf-node) per the §11.4 DAG walk.
- `state` flips `'active' → 'completed'` per the §5.6.4 algorithm.
- On instance delete: rows CASCADE-delete via `holder_node_id` FK.

### 9.10 `claim-store-postgres` items table contract

`claim-store-postgres` operates over an operator-owned items table. The required schema:

```sql
CREATE TABLE <items_table> (
    item_id     UUID PRIMARY KEY,
    payload     JSONB NOT NULL,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    state       TEXT NOT NULL DEFAULT 'available',  -- 'available' | 'in_progress' | 'dead_letter'
    claim_token UUID,                               -- non-null when state='in_progress'
    claimed_at  TIMESTAMPTZ                         -- non-null when state='in_progress'
);
CREATE INDEX <items_table>_available_idx   ON <items_table> (enqueued_at) WHERE state = 'available';
CREATE INDEX <items_table>_in_progress_idx ON <items_table> (claim_token) WHERE state = 'in_progress';
```

Operators create this table out-of-band. The `claim-store-postgres` factory verifies the table exists and has the expected columns at registry build time (`Build`); missing or malformed → startup fail-fast with a clear error.

The `dead_letter` state is set by the supervisor when a node hits `give_up` and the claim's `on_give_up = delete` (representing "this item is poisoned"). Operators inspect via `SELECT … WHERE state='dead_letter'`; manual SQL flips back to `available` for retry. No automated re-enqueue in v1.

### 9.11 Tables removed

- `rimsky_resources` — DROP. Not present in the rewritten `001-initial.sql`.
- `rimsky_resource_versions` — DROP. Not present in the rewritten `001-initial.sql`.

## 10. Substitution & attributes resolution

### 10.1 Two phases of resolution

| Syntax                  | Phase          | When                | Resolved against                                               |
|-------------------------|----------------|---------------------|----------------------------------------------------------------|
| `{params.x}`            | instantiation  | `POST /instances`   | `rimsky_instances.params` (single pass; baked into node config at instantiation time)               |
| `{{deps.<n>.<f>}}`      | dispatch       | each run            | `rimsky_node_attributes.data` of upstream `<n>`                                                     |
| `{{claim.<store>.<f>}}` | dispatch       | each run            | claim payload of `<store>` for this node (§5.7 path: `payload.<...>`)                               |
| `{{params.<key>}}`      | dispatch       | each run            | `rimsky_instances.params` (re-read on each dispatch; same data source as `{params.x}`, no divergence) |

Brace count is the disambiguator: single-brace = instantiation; double-brace = dispatch.

### 10.2 Where substitution applies

Rimsky parses and substitutes in:

- `attributes.schema.properties.*.source` directives.
- `stores[*].read` and `stores[*].write` region declarations (every entry in the array is substituted).
- `locks[*].name`.

Rimsky **does not** substitute in:

- `userdata` (any depth, any value, any phase).
- `claim_resolutions[*].source` and `claim_resolutions[*].store` — these are raw node-name and store-name references, resolved by string match against the template, not by substitution.

### 10.3 Substitution rules

- Single pass; no recursion. A substitution result containing `{{...}}` is treated as literal text.
- Required attribute schema fields (per JSON Schema `required`) whose `source` fails to resolve raise `template_resolution_failed`.
- Optional attribute fields whose `source` fails to resolve are **omitted** from `data`; they remain absent unless the executor writes them.
- Region or lock-name substitution failure on any required component raises `template_resolution_failed`.
- An empty resolved value (`""` or `null`) for a region pattern is **rejected** with `template_resolution_failed` to avoid grant-everything globs by accident.

### 10.4 `template_resolution_failed` policy

A new error class. Routed through the node's policy chain like any other error class. Default chain: `[ {give_up} ]`. Templates may override.

### 10.5 Substitution-time race handling

Dispatch eligibility (§13.1) requires upstream nodes to be `fresh`; substitution reads each `{{deps.<n>.<f>}}` from `rimsky_node_attributes.data` of `<n>`. Between eligibility and substitution another supervisor or operator action can invalidate the dispatch row. The verify-before-run check (§13.3 step 4 — preserves the existing `orphaned_claim_lost_race` behaviour) catches this: re-reading `rimsky_dispatch.claimed_by` after lock acquisition surfaces the race. The transaction has already inserted lock-holder rows; if verify fails, the supervisor does the rollback path: `DELETE` lock-holder rows (claimant-guarded), update node state per the orphan-claim-lost-race handler, and bail.

## 11. Templated fields summary and template-deploy validation

### 11.1 What rimsky parses

| Field                                        | Phase                      |
|----------------------------------------------|----------------------------|
| `attributes.schema.properties.*.source`      | dispatch                   |
| `stores[*].read`, `stores[*].write`          | dispatch                   |
| `locks[*].name`                              | dispatch                   |
| any field with `{params.x}` (single brace)   | instantiation              |

### 11.2 What rimsky does not parse

- `userdata` (any depth).
- `claim_resolutions[*].source` (raw node-name string match).
- `claim_resolutions[*].store` (raw store-name string match).

### 11.3 Removed template fields

- `owns_resources` — replaced by `stores: [{name, write: [...]}]`.
- `reads_resources` — replaced by `stores: [{name, read: [...]}]`.
- `concurrency_tags` — replaced by `locks: [{name, mode: counting, limit: N}]`.

Templates using removed fields fail at template-deploy with a clear error message naming the new equivalent.

### 11.4 Held-claim resolution validation algorithm

Run during template-deploy validation. Pseudocode:

```
For each node N in template.nodes that declares any store entry with hold: true:
  For each such store entry S = (store_name, claim: true, hold: true):
    holding_subgraph = { D in template.nodes | D depends transitively on N }
                       ∪ { N }                  // include N itself; N may also resolve

    leaves = { L in holding_subgraph |
                 no D in holding_subgraph depends on L (L has no descendants in the subgraph) }

    resolvers = { L in leaves |
                    L.claim_resolutions contains an entry with source=N and store=S.store_name }

    IF resolvers ⊊ leaves:
      missing = leaves \ resolvers
      raise TemplateValidationError(
        "claim leaked at terminal(s) %s for held claim %s on store %s",
        missing, source=N, store=S.store_name)
```

A leaf with no `claim_resolutions` entry covering `(source=N, store=S.store_name)` fails the deploy. (A leaf may declare resolutions for multiple held claims; each held claim is validated independently.)

### 11.5 Worked example (generic)

```yaml
nodes:
  # Pure-infra node: no executor, just claims from a ring buffer and
  # populates attributes from the claim payload.
  - type: claim-topic
    schedule: "* * * * *"
    stores:
      - { name: topics-ring, claim: true, hold: true }
    attributes:
      schema:
        type: object
        properties:
          area:     { type: string, source: "{{claim.topics-ring.payload.area}}" }
          subtopic: { type: string, source: "{{claim.topics-ring.payload.subtopic}}" }
        required: [area, subtopic]
    locks:
      - { name: "topics-ring:concurrent-claims", mode: counting, limit: 5 }

  - type: scope
    dependencies: [claim-topic]
    executor: claude-agent
    attributes:
      schema:
        type: object
        properties:
          area:        { type: string, source: "{{deps.claim-topic.area}}" }
          subtopic:    { type: string, source: "{{deps.claim-topic.subtopic}}" }
          scope_notes: { type: string }
        required: [scope_notes]
    userdata:
      model: claude-sonnet-4-6
      system_prompt_ref: "scope-system.md"
    locks:
      - { name: model-budget, mode: counting, limit: 50 }
    error_types:
      review_rejected:
        policy:
          - { action: discard_then_retry, count: 2 }
          - { action: invalidate, targets: [scope] }
          - { action: give_up }

  - type: draft
    dependencies: [claim-topic, scope]
    executor: claude-agent
    attributes:
      schema:
        type: object
        properties:
          area:        { type: string, source: "{{deps.claim-topic.area}}" }
          subtopic:    { type: string, source: "{{deps.claim-topic.subtopic}}" }
          scope_notes: { type: string, source: "{{deps.scope.scope_notes}}" }
        required: [area, subtopic, scope_notes]
    stores:
      - name: content
        write: ["items/{{deps.claim-topic.area}}/{{deps.claim-topic.subtopic}}.md"]
        read:  ["items/**", "shared/**"]
    userdata:
      model: claude-sonnet-4-6
      system_prompt_ref: "draft-system.md"
    quality_rules:
      - { type: must_match_regex, target: write, pattern: "^---\\n", severity: error }
    locks:
      - { name: model-budget, mode: counting, limit: 50 }

  - type: review
    dependencies: [claim-topic, scope, draft]
    executor: claude-agent
    attributes:
      schema:
        properties:
          accepted: { type: boolean }
          notes:    { type: string }
        required: [accepted]
    stores:
      - name: content
        read: ["items/{{deps.claim-topic.area}}/{{deps.claim-topic.subtopic}}.md"]
    claim_resolutions:
      - source: claim-topic
        store:  topics-ring
        # uses store defaults: release_to_back / release_to_back
    userdata:
      model: claude-sonnet-4-6
      system_prompt_ref: "review-system.md"
    locks:
      - { name: model-budget, mode: counting, limit: 50 }
```

(`model-budget` limit is illustrative — in the smoke fixture it's set high to allow throughput.)

## 12. Executor protocol

`proto/v1/node_executor.proto` is updated. Clean break, no dual-protocol support.

### 12.1 `ExecuteRequest` (rewritten)

```proto
message ExecuteRequest {
  string node_id = 1;
  string instance_id = 2;
  string node_type = 3;

  // Opaque per-node config from the template. Rimsky never interprets this;
  // only the executor does. NEVER substituted.
  google.protobuf.Struct userdata = 4;

  // Per-run typed attributes. Source-directive fields are pre-populated by
  // rimsky at dispatch; sourceless fields are populated by the executor
  // (terminal-final via attributes_delta on Complete, or incremental via
  // POST {callback_url}/v1/attributes/{node_id}).
  google.protobuf.Struct attributes = 5;

  // The declared JSON Schema for the node's attributes. For executor reference;
  // rimsky validates at dispatch (substitution) and at commit (writeback) regardless.
  google.protobuf.Struct attributes_schema = 6;

  // Handles for each store the node references. Keyed by store-config name.
  map<string, StoreHandle> stores = 7;

  string callback_url = 8;
  string cancel_token = 9;

  // True iff the dispatch is a resumed retry (resumable: true + resume_then_retry).
  // The executor may use this to short-circuit re-running already-completed work.
  bool resumed = 10;

  // Increments on every retry. Exposed for executor visibility / idempotency.
  int32 run_attempt = 11;
}

message StoreHandle {
  string kind = 1;                              // "filesystem" | "claim_store"
  google.protobuf.Struct handle = 2;            // kind-specific
  repeated string write_regions = 3;
  repeated string read_regions  = 4;
  bool   resumed = 5;                           // store-side prior work exists
}
```

Removed vs. current proto:
- `deps_data` (folded into `attributes`).
- `reads_data` (folded into `attributes`).
- `instance_params` (folded into `attributes` via `source: "{{params.x}}"` directives).

### 12.2 Terminal events (rewritten)

```proto
message ExecuteEvent {
  oneof event {
    Heartbeat heartbeat = 1;
    Complete complete = 2;
    Blocked blocked = 3;
    Errored errored = 4;
    AsyncAccepted async_accepted = 5;
  }
}

message Heartbeat {
  int64 timestamp_ms = 1;
  string note = 2;
}

message Complete {
  bool changed = 1;
  string change_summary = 2;
  // Optional: terminal-final attribute writeback. Empty for the
  // incremental-via-callback pattern. Replaces the old `result` field.
  google.protobuf.Struct attributes_delta = 3;
}

message Blocked {
  string reason = 1;
  google.protobuf.Struct context = 2;
}

message Errored {
  string error_class = 1;
  google.protobuf.Struct payload = 2;
}

message AsyncAccepted {
  string async_ack_id = 1;
  int64 expected_completion_ms = 2;
}
```

Removed: `Complete.result` (replaced by `attributes_delta` semantically).

### 12.3 HTTP+JSON bridge

Mirror of the gRPC shape, message bodies keyed `type` (per the existing chi-route convention preserved unchanged).

### 12.4 Async handoff

Unchanged transport: `AsyncAccepted` → executor calls back to `POST {callback_url}/v1/callback/{async_ack_id}` with `TerminalEvent` JSON. The lock-holder rows and `rimsky_node_attributes` row persist across the async period (the supervisor doesn't release them until the callback arrives or orphan-reap fires). Body keying preserved.

### 12.5 Incremental attributes callback (new)

`POST {callback_url}/v1/attributes/{node_id}` with body:

```json
{ "delta": { "<field>": <value>, ... } }
```

Supervisor merges into `rimsky_node_attributes.data`, persists, returns `204 No Content`. Auth is via the supervisor-issued `cancel_token` in the `Authorization` header (matches the existing async-callback auth pattern).

### 12.6 Supervisor-side actions per terminal event

| Terminal event                                 | Direct mode                                                                | Sidecar/Versioned (post-v1)                |
|------------------------------------------------|----------------------------------------------------------------------------|--------------------------------------------|
| `Complete{changed: true}`                      | `Commit` (no-op for direct), validate attributes, `ReleaseLock(commit)`    | sidecar applied + lock released            |
| `Complete{changed: false}`                     | `ReleaseLock(commit)`; `attributes` validated only if executor wrote any   | sidecar discarded + lock released          |
| `Blocked` / `Errored` + `discard_then_retry`   | `ReleaseLock(give_up)` (in-flight writes already on disk)                  | sidecar discarded + lock released          |
| `Blocked` / `Errored` + `resume_then_retry`    | `ReleaseLock(preserve_for_resume)` (sidecar IS the live tree)              | sidecar preserved + lock released          |
| `Blocked` / `Errored` + `give_up`              | `ReleaseLock(give_up)`                                                     | sidecar discarded + lock released          |
| `Errored` + `invalidate(targets)`              | `ReleaseLock(give_up)` + invalidate targets                                | same                                       |

For claim stores, `ReleaseLock(commit)` honours the claim's `on_commit`; `ReleaseLock(give_up)` honours `on_give_up`. For held claims, the action is processed via the §5.6.4 algorithm.

## 13. Locks & dispatch queue

### 13.1 Dispatch eligibility

A node is dispatch-eligible when:

1. All declared dependencies are `fresh`.
2. All required attribute source-directives can resolve (upstream attributes / claim payloads / params exist).
3. All required locks are acquirable (per §13.2).
4. All required stores are in the local supervisor's `accepted_stores` (§14.2).
5. The node's `executor` is in the local supervisor's `accepted_executors`, or the node has no `executor` (native, claim-only).

### 13.2 Hybrid lock-eligibility (all in-Go; serialization happens in §13.3)

All eligibility checks are evaluated in Go after the candidate dispatch row is selected. The dispatch SELECT is **not** parameterized by per-row required-lock data; lock specs come from the in-memory template registry by `node_type`.

- **Named locks**: in-Go count check. For each `locks: [{name, mode, limit}]`:
  - Read `count(*)` from `rimsky_lock_holders` for the lock name.
  - Compare against `limit` (or `0` for mutex).
  This is a **best-effort hint**; the authoritative serialization is the per-name advisory lock taken in §13.3 step 3b.
- **Region locks**: in-Go evaluation. The supervisor loads existing `rimsky_lock_holders` rows for the same `store_name`, calls `store.UnmarshalRegion(row.region_data)` then `store.RegionsConflict(newRegion, existingRegion)` for each. Bails if any conflict. Authoritative serialization comes from re-checking inside the acquisition tx (§13.3 step 3d).
- **Claim availability**: in-Go via `store.HasClaimableItem(criteria)`. Best-effort: a `true` result does not guarantee the subsequent `AcquireLock` will succeed (TOCTOU races are normal). The atomic `AcquireLock` re-validates inside the tx and returns a typed empty-result if the pool is empty by then.

### 13.3 Atomic acquisition

The atomic-acquisition transaction is **owned by `core/supervisor/runner.go`**. `core/queue/postgres/queue.go` exposes building-block SQL helpers (the dispatch SELECT, the lock-holder INSERT, etc.); the runner orchestrates the transaction by calling them inside a single `pgx.Tx`. This locates blessed invariant 10 (§18) on `runner.go` for source-annotation purposes.

The runner runs **one transaction** end-to-end:

```
BEGIN tx
  -- Step 1: candidate selection. Pool-specialization predicates inlined; per-spec
  -- lock eligibility is evaluated in step 2 in Go (§13.2).
  candidate := SELECT row FROM rimsky_dispatch
                 WHERE claimed_by IS NULL
                   AND required_stores <@ $supervisor_accepted_stores
                   AND (executor_name = ANY($supervisor_accepted_executors) OR executor_name IS NULL)
                 ORDER BY enqueued_at
                 FOR UPDATE SKIP LOCKED
                 LIMIT 1
  IF candidate IS NULL: ROLLBACK and yield (no eligible work).

  -- Step 2: in-Go pre-check for ALL lock specs (named, region, claim) per §13.2.
  -- These are advisory; the authoritative serialization comes from the advisory
  -- locks + re-checks in step 3.
  For each lock spec on candidate node:
    IF !lockSpecEligibleHint(spec): ROLLBACK and try next dispatch row.
  -- (Implementation note: the runner reads the candidate's node_type, looks up
  -- spec list from the in-memory template registry, evaluates each per §13.2.)

  -- Step 3a: rebind path for preserve-for-resume.
  For each region/claim spec where a prior `rimsky_lock_holders` row exists for
  (holder_node_id = candidate.node_id, store_name, holder_supervisor_id = $supervisor_id, expires_at > now()):
    Treat as "rebound". Skip AcquireLock. Reuse the existing row.
    UPDATE rimsky_lock_holders SET last_heartbeat_at = now(),
                                   expires_at = now() + (5 * heartbeat_interval * interval '1 second')
      WHERE id = <existing_row_id>
    SET resumed = true for this lock spec's ExecuteRequest field.
  Named locks are NEVER rebound — always re-acquired through the path below.

  -- Step 3b: per-named-lock advisory + recount.
  For each named lock the node requires, in §13.7 sort order:
    SELECT pg_advisory_xact_lock(hashtext('rimsky_lock:' || lock_name))   -- serializes
    Re-check count: SELECT count(*) FROM rimsky_lock_holders WHERE lock_name = ?
    IF count >= limit (or > 0 for mutex): ROLLBACK and yield.

  -- Step 3c: claim the dispatch row. Claimant-guarded re-check.
  rows := UPDATE rimsky_dispatch
            SET claimed_by = $supervisor_id, claimed_at = now(), last_heartbeat_at = now()
          WHERE id = $candidate_id AND claimed_by IS NULL
          RETURNING 1
  IF rows = 0: ROLLBACK (someone else won; lost the row between SELECT FOR UPDATE
                          release and our UPDATE — should not happen given SKIP LOCKED
                          inside the same tx, but the guard is the invariant).

  -- Step 3d: per-region-lock re-evaluation.
  For each region lock, re-load existing holders for the same store and re-evaluate
  RegionsConflict. If a new conflict surfaced: ROLLBACK and yield.

  -- Step 3e: AcquireLock + insert per spec, in §13.7 sort order.
  For each lock spec NOT already rebound in step 3a:
    Call store.AcquireLock(ctx_with_tx, spec).  -- store's writes share this tx
    IF error or empty ClaimResult: ROLLBACK and yield.
    INSERT INTO rimsky_lock_holders (id, lock_kind, lock_name, store_name, region_data,
                                      claim_id, holder_supervisor_id, holder_node_id,
                                      claimed_at, last_heartbeat_at, expires_at)
      VALUES (..., now(), now(), now() + (5 * heartbeat_interval * interval '1 second'))

COMMIT  -- pg_advisory_xact_locks released automatically on commit.

-- Step 4: Verify-before-run (separate read; not in tx). Bail to orphan-handler on mismatch.
SELECT claimed_by FROM rimsky_dispatch WHERE id = $candidate_id
IF claimed_by != $supervisor_id:
  Run orphan-claim-lost-race handler:
    For each lock-holder row this attempt inserted:
      Best-effort store.ReleaseLock(give_up) outside any tx.
      DELETE FROM rimsky_lock_holders WHERE id = ? AND holder_supervisor_id = $supervisor_id
  Bail.

-- Step 4.5: Transition node to running state. Done in its own short tx:
BEGIN tx2
  UPDATE rimsky_nodes
     SET state = 'running', state_reason = 'dispatch_claimed',
         assigned_supervisor_id = $supervisor_id,
         last_heartbeat_at = now(),
         updated_at = now()
   WHERE id = $candidate.node_id AND state = 'fresh'
  RETURNING 1
COMMIT tx2
-- The state machine (§blessed-invariant 1) rejects illegal transitions; if the
-- UPDATE returns 0 rows because state moved out of 'fresh' between commit-of-tx
-- and now (e.g. operator invalidate), bail to the orphan handler.

-- Step 5: Open native handles.
For each acquired (or rebound) lock:
  store.OpenHandle(ctx, lh, resumed)

-- Step 6: Hand off to executor or native runner.
```

The whole acquisition is one `pgx.Tx`; advisory locks, the dispatch UPDATE, AcquireLock store mutations, and lock-holder inserts all commit or roll back together. `FOR UPDATE SKIP LOCKED` plus the claimant-guarded UPDATE in step 3c prevents two supervisors from succeeding on the same row.

For claim acquisitions, the items-table flip is `state='available' → 'in_progress'`, `claim_token = <new uuid>`, `claimed_at = now()`. The store's claim-pick SQL is:

```sql
UPDATE <items_table>
   SET state = 'in_progress', claim_token = $1, claimed_at = now()
 WHERE item_id = (
       SELECT item_id FROM <items_table>
        WHERE state = 'available'
          AND (<criteria-derived predicate> OR true)
        ORDER BY enqueued_at
          FOR UPDATE SKIP LOCKED
        LIMIT 1
       )
RETURNING item_id, payload
```

`FOR UPDATE SKIP LOCKED` is essential: two concurrent acquirers in their own transactions are routed to different rows rather than blocking on each other. The whole acquisition tx (advisory locks + lock-holder inserts + items-table flip) commits or rolls back atomically.

### 13.4 Heartbeat

Each supervisor's heartbeat tick (interval: `heartbeat_interval`, default `5s` — preserved from current value):

```sql
BEGIN;
UPDATE rimsky_supervisors SET last_heartbeat_at = now() WHERE id = $1;
UPDATE rimsky_dispatch SET last_heartbeat_at = now() WHERE claimed_by = $1;
UPDATE rimsky_lock_holders
   SET last_heartbeat_at = now(), expires_at = now() + ($2 * interval '1 second')
 WHERE holder_supervisor_id = $1
   AND holder_node_id IN (
         SELECT id FROM rimsky_nodes WHERE assigned_supervisor_id = $1 AND state = 'running'
       );
UPDATE rimsky_nodes SET last_heartbeat_at = now() WHERE assigned_supervisor_id = $1 AND state = 'running';
COMMIT;
```

(`$2` is `5 × heartbeat_interval_seconds`.)

The `holder_node_id IN (running nodes)` filter ensures **preserve-for-resume rows are not refreshed** — those rows are tied to nodes that have transitioned out of `running` (to `stale`). Without the filter the resume-grace cutoff (§13.6) would never fire.

The same tick polls `rimsky_nodes.kill_requested` for nodes assigned to this supervisor; if set, the runner signals the executor's cancel token / SIGTERM and drives the give-up path. Behaviour identical to today's `kill_requested` handling.

### 13.5 Orphan reap

The scheduler tick (under `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`) sweeps four cases:

1. **Dispatch-claim sweep** (semantics preserved; predicate column changed): `rimsky_dispatch` rows where `last_heartbeat_at < now() - 5 × heartbeat_interval` AND `claimed_by IS NOT NULL` → clear `claimed_by`, `claimed_at`, `last_heartbeat_at`, claimant-guarded on `claimed_by`. The current implementation reads `claimed_at`; the redesign switches to `last_heartbeat_at` so claim age tracks heartbeat liveness rather than initial-claim time.

2. **Lock-holder sweep** (new): `rimsky_lock_holders` rows where `expires_at < now()` → for each row, in one transaction:
   - For `lock_kind = 'claim'`: call the store's `ReleaseLock(tx, lh, ReleaseGiveUp)` so the items-table row goes back to `state='available'`. If the lock-holder is still tied to a held claim (i.e. an `active` `rimsky_claim_holders` row exists), run the §5.6.4 resolution algorithm with `actual_action = on_give_up`.
   - For `lock_kind = 'region'` or `'named'`: nothing store-side (no items-table involvement).
   - `DELETE` the lock-holder row, claimant-guarded on `holder_supervisor_id`.
   - Emit `lock_orphan_reaped` event.

3. **Claim-holder GC** (new): `rimsky_claim_holders` rows whose `holder_node_id`'s current node state is `failed` or `fresh` AND whose `state` is still `'active'` → these are stale "leaked" holders (a terminal node finished but the resolution algorithm didn't fire — most plausibly because of an earlier orphaned dispatch). Run the §5.6.4 algorithm with `actual_action = on_give_up`. After this, the row is `'completed'` and the items-table row is reset for the claim's case.

4. **Visibility-timeout sweep** (new, per §7.7): for each `claim-store-postgres` store in the scheduler's local registry, run the SQL in §7.7 to reset items whose `claimed_at + visibility_timeout < now()` AND for which no `rimsky_lock_holders` row exists.

The 5× cutoff matches the existing dispatch-claim invariant.

### 13.6 Release

When a node's runner reaches a terminal, in one transaction:

```
BEGIN tx
  store.ReleaseLock(tx, handle, action)            // store-side cleanup; for claim stores
                                                    // this runs the on_commit / on_give_up
                                                    // policy via items-table mutations
  IF lock_kind = 'claim' AND a held-claim row exists:
    Run §5.6.4 resolution algorithm in tx          // first-delete-wins / last-released-wins;
                                                    // updates rimsky_claim_holders.actual_action

  IF action = ReleasePreserveResume AND lock_kind IN ('region','claim'):
    // Preserve the lock-holder row and the store's prior in-progress state.
    // expires_at is the sweep predicate (§13.5 step 2). Set it `resume_grace`
    // into the future. last_heartbeat_at is left as-is (never refreshed by
    // the heartbeat tick once the node leaves 'running' — see §13.4 filter).
    UPDATE rimsky_lock_holders
      SET expires_at = now() + (resume_grace * interval '1 second')
      WHERE id = handle.id AND holder_supervisor_id = supervisor_id
  ELSE:
    DELETE FROM rimsky_lock_holders
      WHERE id = handle.id AND holder_supervisor_id = supervisor_id   -- claimant-guarded

  UPDATE rimsky_nodes SET state = <fresh|stale|failed>, ... WHERE id = node_id
COMMIT
```

`resume_grace` is configured (default `1800s` = 30min). If the node isn't re-dispatched within the grace window the lock-holder row is reaped by §13.5 step 2 (predicate: `expires_at < now()`), which calls `store.ReleaseLock(tx, lh, ReleaseGiveUp)` and runs §5.6.4 with `actual_action = on_give_up` for held claims — recovering the items-table row.

The store gets the open `*pgx.Tx` via context (`store.TxFromContext` per §8.4.1).

**Rebind path on next dispatch (same supervisor).** When the same supervisor returns to dispatch the node within the grace window, §13.3 step 3a finds the existing `rimsky_lock_holders` row (matching `holder_node_id`, `store_name`, `holder_supervisor_id`, and `expires_at > now()`), reuses it, updates `last_heartbeat_at + expires_at` to the running-node value, and skips `Store.AcquireLock`. The lock spec is marked `resumed=true` so `Store.OpenHandle` is called with `resumed=true`. Named locks are NEVER rebound — they go through the standard advisory-lock + count + insert path each time.

**Rebind path on next dispatch (different supervisor).** Not supported. A different supervisor's §13.3 step 3a finds no row owned by it; the standard fresh-acquisition path runs. The orphan reap (firing within `resume_grace`) clears the prior supervisor's row and resets the items-table; the new supervisor sees a fresh `state='available'` row (or no claim available). The executor sees a non-resumed dispatch on the new supervisor. This is documented as a known limitation in `store-author-guide.md`.

### 13.7 Sort-order invariant for multi-lock acquisition

Locks are sorted by `(lock_kind, sort_key)`:
- `named`: sort_key = `lock_name`
- `region`: sort_key = `store_name + ":" + canonical_json(region)` where `canonical_json` is JSON with sorted keys
- `claim`: sort_key = `store_name`

Sorted insert prevents deadlock under concurrent contention on overlapping lock sets. Preserves and generalises the existing per-tag-locks-acquired-in-sorted-order invariant.

## 14. Configuration

### 14.1 `stores.yml`

Loaded by control-api and each supervisor at startup from `RIMSKY_STORES_CONFIG` (default `/etc/rimsky/stores.yml`).

Schema:

```yaml
stores:
  <name>:
    kind: filesystem | claim_store
    # kind-specific config follows
    ...
```

Filesystem direct-mode:

```yaml
stores:
  content:
    kind: filesystem
    mode: direct
    root: /workspace/content
```

Claim-store-postgres:

```yaml
stores:
  inbound:
    kind: claim_store
    backend: postgres
    items_table: inbound_items
    on_commit_default:  delete
    on_give_up_default: release_to_head
    visibility_timeout_seconds: 300
```

### 14.2 Supervisor pool specialization

Each supervisor process declares the stores it has access to via `stores.yml`. On supervisor registration the supervisor writes `accepted_stores TEXT[]` into `rimsky_supervisors` (§9.5).

Each `rimsky_dispatch` row carries a denormalized `required_stores TEXT[]` populated at enqueue time from the template's `nodeRequiredStores(node_type)`. Dispatch eligibility (§13.1 step 4) filters with an SQL predicate on the dispatch SELECT:

```sql
... WHERE rimsky_dispatch.required_stores <@ $supervisor_accepted_stores ...
```

`<@` is "array contained by" — the row is eligible only if every required store is in the supervisor's accepted set. Empty `required_stores` (the common case for nodes with no store references) trivially passes.

`required_stores` is set on each `Enqueue` call. `core/queue/interface.go`'s new `Enqueue` signature accepts `requiredStores []string` and writes it into the row.

### 14.3 Existing config preserved

`RIMSKY_SUPERVISOR_CONFIG` (callback advertise host, etc.) preserved as-is. The new `stores.yml` is loaded separately to keep store config from bloating the supervisor config.

## 15. Quality rules

Two layers; node-level evaluated by supervisor before commit, store-level evaluated by store during commit. v1 supports node-level `must_match_regex` for filesystem stores.

Direct-mode store-level rules are accepted in YAML but warned-and-ignored in v1 (rejection is awkward when bytes already landed). Documented limitation.

## 16. Files modified, added, and deleted

This is the executing agent's reference inventory. The plan must touch all of these.

### 16.1 Added (new files)

- `core/store/interface.go` — `Store`, `LockSpec`, `LockHandle`, `Capabilities`, `ReleaseAction`, `LockMode`, `ClaimResult`, `CommitResult`, `NativeHandle`, factory + registry types.
- `core/store/registry.go` — `Registry` implementation, `BuildAll`, `GetStore`, `Register`.
- `core/store/types.go` — concrete `NativeHandle` types (`FilesystemDirectHandle`, `ClaimStoreHandle`).
- `core/store/lockholders.go` — postgres helpers for `rimsky_lock_holders` insert / delete / heartbeat-extend / sweep (used by the supervisor and scheduler; lives in `core/store/` because it's the unified mechanism, not in `core/queue/postgres`).
- `core/store/filesystem/` — direct-mode filesystem store: `factory.go`, `store.go`, `region.go` (path-glob conflict logic), `region_test.go`, `store_test.go`.
- `core/store/claimstorepg/` — claim-store-postgres: `factory.go`, `store.go`, `acquire.go`, `release.go`, `holders.go` (claim-holder reference-counted resolution algorithm — §5.6.4), `*_test.go` files for each.
- `core/store/stub/` — in-process stub store implementing `Store`, `ClaimableStore`, and `ResumableStore` with configurable in-memory state (region table, claim queue, capability flags). Used by the migrated `test/scenarios/*` tests that exercise runner / state-machine semantics without needing real filesystem or postgres-claim-store setup. Files: `factory.go`, `store.go`, `store_test.go`. Default test fixtures in `core/scenario/harness.go` use this store.
- `core/attributes/` — substitution engine + JSON Schema validation:
  - `substitution.go` (one-pass `{{...}}` substitution)
  - `validate.go` (JSON Schema validation via `github.com/santhosh-tekuri/jsonschema/v5`)
  - `callback.go` (HTTP handler for `POST /v1/attributes/{node_id}`)
  - `store.go` (postgres helpers for `rimsky_node_attributes` row CRUD)
  - `*_test.go`
- `core/migrations/001-initial.sql` — **rewritten in place** to the §9 end-state schema.
- `proto/v1/node_executor.proto` — rewritten per §12.
- `proto/v1/events.proto` — extended with new event payloads (§9.8) and pruned of removed kinds.
- `deploy/stores.yml` — default store config used by docker-compose: `content` (filesystem direct), `topics-ring` (claim-store-postgres).
- `executors/claude-agent/src/attributes-tools.ts` (or similar; place per existing TS layout) — MCP tool wrappers for read / set on attributes.
- `test/scenarios/stores/`, `test/scenarios/locks/`, `test/scenarios/attributes/`, `test/scenarios/claim_stores/` — new scenario buckets (cases per §19.1).
- `test/smoke/` — new bucket: `setup.go` (stack bring-up via testcontainers; mirrors `deploy/docker-compose.yml`), `stores_redesign_smoke_test.go` (acceptance fixture per §19.2).
- `docs/store-author-guide.md` — replaces `docs/resource-author-guide.md`.

### 16.2 Modified (heavy changes)

- `core/node/template.go` — remove `OwnsResources`, `ReadsResources`, `ResourceDef`, `ReadResourceDef`, `ConcurrencyTags`. Add `Stores []NodeStoreRef`, `Locks []NodeLockRef`, `Attributes NodeAttributesDef`, `ClaimResolutions []ClaimResolutionRef`. Move userdata field type if needed (preserved as `Userdata map[string]any`).
- `core/node/template_validator.go` — drop `validateOwnsResources`, `validateConcurrencyTags`. Add `validateStores`, `validateLocks`, `validateAttributesSchema`, `validateClaimResolutions` (the §11.4 DAG walk).
- `core/node/template_validator_test.go` — rewrite to cover the new validators; drop resource/concurrency-tag tests.
- `core/queue/interface.go` — remove `concurrency_tags` from `EnqueueRequest`, `ClaimNext`, etc. Add `requiredLocks []store.LockSpec` and `requiredStores []string` to claim eligibility inputs.
- `core/queue/postgres/queue.go` — rewrite the dispatch query: drop `concurrency_tags` predicate; add hybrid lock-eligibility per §13.2; atomic dispatch-claim + lock-holder insert + store `AcquireLock` per §13.3.
- `core/queue/postgres/queue_test.go` — rewrite.
- `core/storage/interfaces.go` — remove `ResourceRegistry`, `ResourceDataStore` interfaces, `Resources()` / `ResourceData()` accessors on `StorageBackend`. Add `LockHolders()`, `NodeAttributes()`, `ClaimHolders()` accessors. Remove `ConcurrencyTags` from `NodeRow`.
- `core/storage/postgres/backend.go` — remove resource accessors; add new ones for the three new tables.
- `core/storage/postgres/nodes.go` — remove `concurrency_tags` from inserts/reads; preserve all other columns.
- (No standalone `core/storage/postgres/dispatch.go` exists today; dispatch SQL lives in `core/queue/postgres/queue.go`. The `concurrency_tags` removal, `executor_name` nullability, `required_stores` addition, and `last_heartbeat_at` addition are folded into the `core/queue/postgres/queue.go` rewrite.)
- `core/storage/postgres/supervisors.go` — extend Upsert SQL to write `accepted_stores`; extend the row scan to read it back.
- `core/storage/postgres/lock_holders.go` (new) — implements the new accessor.
- `core/storage/postgres/node_attributes.go` (new) — implements the new accessor.
- `core/storage/postgres/claim_holders.go` (new) — implements the new accessor.
- `core/scheduler/scheduler.go` — drop the existing `n.ConcurrencyTags` references in `sweepHeartbeatLost` and `sweepReady` (the new `Enqueue` signature no longer takes tags). Add the four sweeps from §13.5: dispatch-claim sweep predicate switches to `last_heartbeat_at`; new `lockHolderSweep`, `claimHolderGC`, `visibilityTimeoutSweep`. Preserves `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`.
- `core/scheduler/recalculate.go` — drop the `n.ConcurrencyTags` reference at the existing `Enqueue` call site; align with the new `Enqueue` signature (`node_id`, `executor`, `required_stores`, `enqueued_at`).
- `core/scheduler/invalidate.go` — **delete `invalidateRestorePath` entirely** along with the `RestoreVersion` field on `InvalidateArgs` / `InvalidateRequest`. Versioned-mode restore is post-v1; the redesign drops the restore plumbing in this work. The remaining invalidate logic (cascade, kill_requested propagation, message-emit) is preserved.
- `core/scheduler/pure_cascade.go` — update the empty-executor sweep policy: today nodes with `executor = ""` short-circuit to skip enqueue. The redesign distinguishes "pure cascade" (no executor, no claim) from "native claim-only" (no executor, has claim store). Pure-cascade behaviour unchanged. Native claim-only nodes are now enqueued and run by the omnibus runner per §17.1 step 4b. Update the file to differentiate based on the in-memory template's `stores: [{claim:true}]` declarations.
- `core/supervisor/runner.go` — rewrite as the omnibus runner per §17.1.
- `core/supervisor/supervisor.go` — drop `core/resource` import; rewire registry plumbing to use `core/store`.
- `core/supervisor/commit.go` — drop `core/resource` import; rewrite the commit path per §17.1 step 6 (Store.Commit + Store.ReleaseLock + §5.6.4 held-claim resolution in one tx; delete the resource.CommitVersion / resource.RestoreVersion call sites).
- `core/supervisor/on_error.go` — adapt to new error classes (`template_resolution_failed`, `attributes_schema_failed`); drop concurrency-tag refs.
- `core/supervisor/terminal_outcome.go` — rewrite per §12.6 (commit / give_up / preserve-for-resume mappings to ReleaseAction).
- `core/supervisor/commit_test.go` — rewrite to test the new commit path (Store.Commit + ReleaseLock + claim-holder resolution).
- `core/supervisor/callback_test.go` — drop `core/resource` import; update to assert against the new attributes-callback path.
- `core/supervisor/runner_test.go` — drop `core/resource` import; rewrite assertions for the new omnibus runner.
- `core/supervisor/supervisor_test.go` — drop `core/resource` import; update wiring to use the new `core/store` registry.
- The heartbeat tick lives inside `core/supervisor/supervisor.go` (`runLoop`'s heartbeat case). Extend it in place per §13.4 to update `rimsky_lock_holders` (gated by `holder_node_id IN running-nodes`) and `rimsky_nodes.last_heartbeat_at` — no file rename.
- `core/controlapi/app.go` — drop `registerResourcesRoutes`. Add `registerClaimsRoutes` (`GET /claims/:claim_id/holders`), `registerAdminClaimStoresRoutes` (`POST /admin/claim-stores/:name/items`), and `registerAdminScheduleRoutes` (`POST /admin/scheduled-nodes/:node_id/force-fire` — used by the smoke fixture per §19.2).
- `core/controlapi/instances.go` — drop concurrency-tag handling on instance create; integrate attributes substitution.
- `core/controlapi/templates.go` — drop concurrency-tag refs; integrate new template validation.
- `core/controlapi/nodes.go` — drop concurrency-tag refs **and** drop the `RestoreVersion` field on `invalidateNodeRequest` plus all related JSON encoding/decoding (versioned-mode restore is post-v1).
- `core/controlapi/app_test.go` — rewrite to drop resource refs; add tests for new routes.
- `core/controlapi/admin_force_fire.go` (new) — handler for `POST /admin/scheduled-nodes/:node_id/force-fire`. The handler runs `UPDATE rimsky_schedules SET next_fire_at = now() WHERE node_id = $1` and returns `204` immediately. It does **not** wait for the cascade to complete; callers (e.g. the smoke fixture) poll for completion separately via the events table or node state. This keeps the endpoint cross-process safe (no in-process scheduler dependency). The scheduler's regular tick picks the row up at the next tick.
- `core/scenario/harness.go` — rewrite end-to-end (current ~412 lines):
  - Drop all `core/resource/...` imports; add `core/store/...` imports.
  - Replace `factories.Register("inline-jsonb", ...)` wiring (current line ~82-83) with `core/store/stub/` registration as the default test store.
  - Delete `getResourceForOwner` and adjacent resource-helper methods (current lines ~181-206).
  - Rewrite `templateSpecToJSON` (current lines ~308-412): every `concurrency_tags`, `owns_resources`, `reads_resources`, `restore_version` key is replaced with the new template grammar (`stores`, `locks`, `attributes`, `claim_resolutions`). Add helpers for constructing the new grammar from Go test inputs.
  - Wire up the attributes substitution + JSON Schema validation paths (call into `core/attributes/`).
  - Wire up the `rimsky_lock_holders` orphan-reap goroutine into the in-process scheduler the harness starts.
- `core/scenario/harness_test.go`, `core/scenario/harness_util.go` — adjust to the new harness signature; update or remove resource-specific helpers.
- `core/cmd/rimsky-supervisor/main.go` — drop resource registry wiring; add `RIMSKY_STORES_CONFIG` loading and store registry build.
- `core/cmd/rimsky-control-api/main.go` — same.
- `core/cmd/rimsky-scheduler/main.go` — add `RIMSKY_STORES_CONFIG` loading and store registry build (the scheduler now needs the registry for the §13.5 step 4 visibility-timeout sweep over `claim-store-postgres` instances and for the schedule-cron tick that drives the dispatch-queue sweep).
- `core/config/supervisor.go` — drop resource fields (`GetResource`, `ResourceFactories`); add `StoreFactories`, `Stores` map.
- `core/config/controlapi.go` — same.
- `core/config/scheduler.go` — add `StoreFactories`, `Stores` map.
- `core/node/policy.go` — drop the `RestoreVersion` field on `ErrorActionDef` / `PolicyAction` (versioned-mode restore is post-v1; the field is part of the template grammar and must go to keep the template parser strict).
- `core/node/state.go` — drop the `ReasonRestoreVersion` constant and the state-machine branch keying off it; the state machine continues to enforce the existing transitions for everything else (preserves blessed invariant 1).
- `core/node/state_test.go` — drop tests exercising `ReasonRestoreVersion`.
- `core/shared/types.go` — drop `ConcurrencyTag` type if defined here.
- `executors/claude-agent/src/server.ts` — receive `attributes` in `Execute` requests; expose to MCP as read+set tools; persist via incremental `POST /v1/attributes/{node_id}` callback. Drop the result-passing path. Userdata surfaced opaquely to the agent.
- `executors/claude-agent/src/server.test.ts` — rewrite to validate against new protocol; preserve the async-handoff end-to-end test.
- `executors/claude-agent/src/internal-mcp-tools.ts` — drop the result-write MCP tool; add read-attribute and set-attribute tools that hit the supervisor's `/v1/attributes/{node_id}` callback. The set-attribute tool batches per-call deltas; the read-attribute tool returns the live `attributes` object as the executor saw it on dispatch.
- `executors/claude-agent/src/internal-mcp-server.ts` — wire up the new tools; drop the old result-write tool.
- `executors/claude-agent/src/agent-run.ts` — update the agent run loop to consume `attributes` (instead of `result`); ensure the final `Complete` event omits `result` and optionally carries `attributes_delta` for terminal-final pattern (or empty for incremental pattern).
- `executors/claude-agent/src/cli-runner.ts` — adjust local-run scaffolding to mock the new attributes shape.
- `executors/claude-agent/src/http-bridge.ts` — adjust the HTTP+JSON serialization to match the new `ExecuteRequest`/`TerminalEvent` shapes.
- All `executors/claude-agent/src/*.test.ts` files (search across the directory): update mocks and assertions to the new protocol.
- `executors/http-node/server.go` (the file housing the Go HTTP serving logic; `main.go` is the binary entrypoint and may also need a small touchup) — receive `attributes` in the request body; expose to the target endpoint. Userdata opaque.
- `executors/stub/stub.go` (Go test fixture) — accept the new `ExecuteRequest` shape; in stub mode, return an immediate `Complete{changed: true, attributes_delta: {...}}` synthesized from a simple map of `node_type → field defaults`.
- `core/cmd/rimsky-executor-conformance/main.go` — update the CLI shell to surface the new protocol shape (drop result-passing flags, etc.).
- `conformance/` (root-level package) — `runner.go` and all scenario files in `conformance/scenarios/` (`result_serialization.go`, `async_handoff.go`, `terminal_is_last.go`, `execute_happy_path.go`, and any siblings) — rewrite each scenario against the new ExecuteRequest / TerminalEvent shapes. Particularly: replace `Complete.GetResult()` reads with `Complete.GetAttributesDelta()`; update assertions; drop scenarios that exercise the removed `result` semantics if they have no analog under attributes.
- `deploy/docker-compose.yml` — add `RIMSKY_STORES_CONFIG=/etc/rimsky/stores.yml` and a volume mount for `deploy/stores.yml`. Add a one-shot init container (or shell-init step) that creates the `topics_items` items table the smoke test populates.
- `deploy/Dockerfile.go-base`, `Dockerfile.http-node`, `Dockerfile.claude-agent` — adjust if any path changes in build steps.
- `deploy/build-images.sh` — preserved if no path changes.
- All `test/scenarios/*_test.go` migrated per §19.1 (the existing tests are rewritten, two are deleted).
- `test/scenarios/scenarios_util_test.go` — preserved structurally; updated only for the new harness signatures.
- `core/scenario/harness.go` test helpers updated for the new harness shape (covered above).
- Other `RestoreVersion` cleanup call sites: `core/scheduler/invalidate.go` (handled above), `core/scheduler/recalculate.go` (drop the `restore_version` plumbing), `core/scheduler/messages.go` if such a file exists (drop), `proto/v1/events.proto` (any RestoreVersion-bearing payloads removed). Search-replace for `RestoreVersion` and `restore_version` across the repo to catch all sites.

### 16.3 Deleted

- `core/resource/` — entire package: `interface.go`, `register.go`, `errors.go`, `inlinejsonb/` (factory + resource + tests), `externalsql/` (factory + resource + tests + scenario_test).
- `core/storage/postgres/resources.go`.
- `core/storage/postgres/resource_data.go`.
- `core/storage/postgres/postgres_test.go` resource-specific tests (split: keep non-resource tests, delete resource tests).
- `core/controlapi/resources.go`.
- `core/migrations/002-data-ref-jsonb.sql`.
- The `invalidateRestorePath` function in `core/scheduler/invalidate.go` (and any helpers it owns; the file as a whole is preserved minus this function and the `RestoreVersion` field on `InvalidateArgs` / `InvalidateRequest`).
- All `RestoreVersion` / `restore_version` fields, JSON tags, and event payloads across the repo (versioned-mode restore is post-v1).
- `test/scenarios/double_buffering_test.go` (sidecar mode is post-v1).
- `test/scenarios/rollback_via_restore_version_test.go` (versioned mode is post-v1).
- `docs/resource-author-guide.md`.

### 16.4 Doc updates

The executing agents update these docs in the same work (per project rules):

- **`docs/architecture.md`** — §1.2 (Resource library → Store library), §3 (import rules: `core/store/` allowed importers; `core/resource/` removed), §5 (blessed invariants additions per §18 below), §8 (storage tables enumerated per §9).
- **`docs/protocol.md`** — full rewrite per §12. Authoritative source remains `proto/v1/node_executor.proto`.
- **`docs/node-graph-design.md`** — §3.4 (userdata is opaque), §4 (resources → stores), §6 (error model: add `template_resolution_failed`, `attributes_schema_failed`), §7 (parameterization: §10.1 two-phase), §8 (node contract).
- **`docs/operator-guide.md`** — `stores.yml` config; running the smoke test; nuking dev DB; new env vars; admin endpoints (`POST /admin/claim-stores/:name/items`).
- **`docs/executor-author-guide.md`** — receiving attributes; opaque userdata; incremental writeback callback; per-store handles; protocol message shapes.
- **`docs/store-author-guide.md`** — new doc replacing `resource-author-guide.md`. Worked example: implementing a custom store; capability declaration; `RegionsConflict` purity contract; `UnmarshalRegion`; transaction semantics for stores that mutate during `AcquireLock`.
- **`docs/2026-04-25-stores-redesign.md`** — moved to `docs/history/` once implementation lands. The spec in `docs/specs/2026-04-25-stores-redesign-design.md` (this file) is the contract.
- **`CHANGELOG.md`** — full rewrite of `## Unreleased` with the redesign as a single entry; explicit "BREAKING: nuke dev DB before upgrade".
- **`CLAUDE.md`** — update package import rules (resource → store), blessed invariants list (additions per §18), gotchas (stores-config YAML; userdata is opaque; held-claim resolution algorithm).

## 17. Omnibus runner

`core/supervisor/runner.go` becomes the single configuration-driven flow.

### 17.1 Algorithm

```
1. Claim eligible dispatch row + insert lock-holder rows + store-side AcquireLocks
   (atomic per §13.3). Verify-before-run.
2. Open native handles via Store.OpenHandle(ctx, lh, resumed) for each lock.
3. Resolve attribute source-directives:
   - Read upstream rimsky_node_attributes.data for {{deps.<n>.<f>}}.
   - Read claim payloads from ClaimResults.Payload for {{claim.<store>.<f>}}.
   - Read instance params for {{params.<key>}}.
   - Substitute into the per-property source directive; merge into rimsky_node_attributes.data.
   - Validate: every required source resolved. Otherwise raise template_resolution_failed and route through policy chain.
4. Determine dispatch path:
   a. node.executor != "": send ExecuteRequest to the executor (gRPC or HTTP+JSON).
   b. node.executor == "" AND node has at least one claim acquired: native (claim-only) — emit synthetic Complete{changed: true} (claim succeeded; no additional executor work).
   c. node.executor == "" AND no claim: pure-cascade — emit synthetic Complete based on dependency-message-driven recalc (preserves existing pure-cascade semantics).
5. Heartbeat loop:
   - Extend dispatch + lock-holder + node expiry timestamps every heartbeat_interval.
   - Poll rimsky_nodes.kill_requested; on true, signal cancel_token / SIGTERM and short-circuit to give-up.
6. On terminal event (Complete | Blocked | Errored | AsyncAccepted):
   - For AsyncAccepted: hold the dispatch + locks open until callback; pre-register the async-ack handler (existing path preserved).
   - For Complete{changed: true}:
       a. Validate executor-populated attributes against schema; raise attributes_schema_failed on mismatch.
       b. Run node-level quality_rules.
       c. Begin tx:
          For each acquired lock (in §13.7 sort order):
            store.Commit(tx, handle).
            store.ReleaseLock(tx, handle, ReleaseCommit).
            IF lock.Kind == "claim" AND a rimsky_claim_holders row exists for (claim_id, this_node_id):
              Run §5.6.4 resolution algorithm in tx.
            Delete rimsky_lock_holders row (claimant-guarded). [Or preserve per §13.6 for ReleasePreserveResume.]
          Update rimsky_nodes.state = 'fresh'.
          Persist final rimsky_node_attributes.data.
          Commit tx.
       d. Emit attributes_committed and lock_released events.
   - For Complete{changed: false}: as above but skip schema validation if no executor writes; ReleaseLock(commit) is still called (commits are no-op for direct mode).
   - For Blocked / Errored:
       Per the policy chain action:
         discard_then_retry → ReleaseLock(give_up); state='stale'; re-enqueue.
         resume_then_retry  → ReleaseLock(preserve_for_resume); state='stale'; re-enqueue with resumed=true.
         give_up            → ReleaseLock(give_up); state='failed'.
         invalidate(targets) → ReleaseLock(give_up); state='failed'; invalidate targets.
       Held-claim accounting runs per §5.6.4 with the appropriate action.
```

### 17.2 Capability assertions in the runner

When the runner needs a capability beyond the base `Store` interface, it type-asserts:

```go
// Resumable open
if resumed {
    rs, ok := s.(store.ResumableStore)
    if !ok || !s.Capabilities().SupportsResume {
        return errors.New("resume requested for non-resumable store " + s.Name())
    }
    has, err := rs.HasPriorWork(ctx, spec)
    ...
}

// Claim-availability eligibility
cs, ok := s.(store.ClaimableStore)
if !ok {
    return false, fmt.Errorf("claim spec on non-claimable store %s", s.Name())
}
return cs.HasClaimableItem(ctx, spec.Criteria)
```

The supervisor's runner is the only call site that needs these assertions.

## 18. Blessed invariants

Existing invariants preserved (with adjustments noted):

1. **State machine rejects illegal transitions.** Unchanged. (`core/node/state.go`)
2. **Dispatch claim brackets the running window.** Unchanged. (`core/queue/postgres/queue.go`)
3. **Multi-lock acquisition uses deterministic sorted order.** Generalised: all locks (named, region, claim) acquired in the §13.7 sort order. (`core/supervisor/runner.go` after the redesign — the queue no longer holds tag-limit logic; the runner orchestrates atomic acquisition.)
4. **Claimant-guarded release.** Generalised: every `DELETE FROM rimsky_lock_holders` and every `UPDATE rimsky_dispatch SET claimed_by = NULL` is `AND … = supervisor_id`. (`core/queue/postgres/queue.go`, `core/supervisor/runner.go`, `core/scheduler/scheduler.go`)
5. **Verify-before-run.** Unchanged. (`core/supervisor/runner.go`)
6. **Orphan-claim cutoff is `5 × heartbeat_interval`.** Generalised: same cutoff for `rimsky_lock_holders` orphan reap. (`core/scheduler/scheduler.go`)
7. **Advisory lock on scheduler tick.** Unchanged. (`core/scheduler/scheduler.go`)
8. **Session advisory lock on migrations.** Unchanged. (`core/migrations/runner.go`)

New invariants (annotated `@blessed-invariant` in the source):

9. **Lock state lives only in postgres.** No store implementation persists lock state. (`core/store/interface.go`, on the `Store` interface comment.)
10. **Lock acquisition is atomic with dispatch claim.** The transaction in §13.3 step 3 either claims dispatch and inserts all required `rimsky_lock_holders` rows AND completes all store `AcquireLock` mutations, or none of these. (`core/supervisor/runner.go` on the acquisition function; `core/queue/postgres/queue.go` on the dispatch SQL.)
11. **Userdata is opaque to rimsky.** No code path inspects, parses, substitutes, or validates `userdata`. (`core/attributes/substitution.go` and the `ExecuteRequest.userdata` proto comment.)
12. **Attributes validate twice: at dispatch (substitution) and at commit (executor writeback).** Both gates mandatory. (`core/attributes/validate.go`.)
13. **First-delete-wins, last-released-wins for held claims.** Implemented in §5.6.4. (`core/store/claimstorepg/holders.go`.)
14. **`RegionsConflict` and `UnmarshalRegion` are pure.** No side effects, no external state read; deterministic on inputs. (`core/store/interface.go`.)

Each new invariant has a scenario test (§19.1).

## 19. Test surface

### 19.1 Scenario tests (testcontainers-go, real postgres)

Existing `test/scenarios/*` tests are migrated to the new mechanisms where the underlying behaviour is preserved. Two are deleted because their underlying mechanism is post-v1:

- `double_buffering_test.go` — DELETE (sidecar mode is post-v1).
- `rollback_via_restore_version_test.go` — DELETE (versioned mode is post-v1).

Existing scenarios migrated:

- `cascade_invalidate_test.go`, `fan_out_pattern_test.go`, `pure_cascade_test.go`, `happy_path_executor_test.go`, `agentic_executor_async_handoff_test.go`, `executor_blocked_test.go`, `give_up_test.go`, `heartbeat_loss_reenqueue_test.go`, `no_op_commit_test.go` (rewritten to assert `Commit` returns `Changed: false` and no `attributes_committed` event), `orphaned_claim_test.go`, `scheduled_node_test.go`, `state_machine_same_state_rejected_test.go`, `unresolved_executor_test.go`, `verify_before_run_race_test.go` (uses the new harness with stub store; verifies `orphaned_claim_lost_race` path) — rewritten to use attributes for data flow, locks for concurrency caps, and stores for persistence.
- `concurrency_tag_limit_test.go` — renamed `named_lock_counting_test.go`; semantics preserved.

New scenarios under new buckets:

**`test/scenarios/stores/`:**
- `filesystem_direct_write_test.go` — write succeeds; lock released after commit.
- `filesystem_direct_disjoint_regions_test.go` — two nodes with disjoint write globs run concurrently.
- `filesystem_direct_overlapping_regions_test.go` — two nodes with overlapping write globs serialise.
- `filesystem_direct_read_concurrent_with_write_test.go` — read-on-X concurrent with write-on-X serialises (v1: read locks block on write locks, documented).
- `store_pool_specialization_test.go` — supervisor without store X never claims dispatch rows requiring X.

**`test/scenarios/locks/`:**
- `named_lock_mutex_test.go` — limit=1 blocks second acquirer.
- (`named_lock_counting_test.go` is the renamed `concurrency_tag_limit_test.go`, listed above as a migrated scenario; counted once. It lives in `test/scenarios/locks/` after rename.)
- `region_lock_conflict_test.go` — two supervisors race on overlapping regions; only one wins via `RegionsConflict`.
- `lock_atomic_acquisition_test.go` — dispatch claim and lock-holder inserts land atomically; on `AcquireLock` failure the transaction rolls back leaving no orphan.
- `lock_heartbeat_extends_expiry_test.go` — heartbeat tick updates `expires_at`.
- `lock_orphan_reap_test.go` — supervisor goes silent; lock-holder rows reaped at 5× cutoff; locks become available.
- `lock_sorted_acquisition_no_deadlock_test.go` — node requiring two named locks; multiple supervisors; no deadlock under contention.
- `lock_claimant_guarded_release_test.go` — release attempted with wrong `holder_supervisor_id` is a no-op.

**`test/scenarios/attributes/`:**
- `attributes_substitution_from_deps_test.go` — `{{deps.x.field}}` resolves at dispatch.
- `attributes_substitution_from_claim_test.go` — `{{claim.<store>.payload.field}}` resolves.
- `attributes_substitution_from_params_test.go` — `{{params.x}}` resolves.
- `attributes_required_missing_template_resolution_failed_test.go` — required field missing → `template_resolution_failed` with policy routing.
- `attributes_optional_missing_omitted_test.go` — optional source missing → field absent, not failure.
- `attributes_schema_validation_at_commit_test.go` — type mismatch → `attributes_schema_failed`.
- `attributes_incremental_writeback_test.go` — multiple `POST /v1/attributes/{node_id}` calls accumulate.
- `attributes_terminal_final_writeback_test.go` — `attributes_delta` on `Complete` merges correctly.
- `attributes_resumable_preserve_test.go` — `resumable: true` + `resume_then_retry` preserves executor-populated fields.
- `attributes_resumable_false_clears_test.go` — `resumable: false` + retry clears executor-populated fields.
- `attributes_substitution_race_lost_test.go` — between eligibility and substitution an upstream is invalidated; verify-before-run catches the race; node bails as `orphaned_claim_lost_race`.
- `userdata_opaque_test.go` — userdata containing `{{...}}` arrives at the executor verbatim.

**`test/scenarios/claim_stores/`:**
- `queue_claim_fifo_test.go` — FIFO selection; payload carried.
- `claim_empty_no_dispatch_test.go` — empty claim store → no dispatch.
- `claim_concurrent_supervisors_atomic_test.go` — N supervisors, one item, only one wins.
- `queue_on_commit_delete_test.go` — successful commit acks atomically.
- `queue_on_give_up_release_to_head_test.go` — give-up returns to head.
- `ring_buffer_release_to_back_test.go` — successful commit returns to back.
- `claim_hold_linear_chain_test.go` — `hold: true` + linear terminal; resolution at terminal commit.
- `claim_hold_fan_out_first_delete_wins_test.go` — 2 terminals, one delete + one release: delete wins regardless of order.
- `claim_hold_fan_out_release_count_test.go` — 2 release terminals: actual release fires when count → 0.
- `claim_resolutions_missing_template_deploy_fails_test.go` — held claim with no resolution at any terminal → template-deploy validation rejects per §11.4.
- `claim_resumption_test.go` — executor crash mid-run; claim ref preserved; same payload re-handed on next dispatch.
- `multi_claim_test.go` — node with two claim-store entries from different stores; both populated in attributes; both resolved per their respective `on_commit`.

### 19.2 Smoke fixture (acceptance criterion)

`test/smoke/stores_redesign_smoke_test.go` runs the full deployment in-process via testcontainers-go for postgres + Go-built scheduler / supervisor / control-api / stub claude-agent / stub http-node.

`test/smoke/setup.go` exposes `BringUpStack(t)` which:
1. Starts postgres (testcontainers), runs the migration runner against the rewritten `001-initial.sql`.
2. Creates the `topics_items` items table (per §9.10 schema). Done via direct SQL inside `BringUpStack`; this table is operator-owned (rimsky does not manage it).
3. Starts scheduler, supervisor, control-api as in-process Go services on ephemeral ports.
4. Loads `test/smoke/fixtures/stores.yml` declaring `content` (filesystem direct, root pointing at `t.TempDir()`) and `topics-ring` (claim-store-postgres, ring-buffer defaults).
5. Configures stub executors: `RIMSKY_EXECUTOR_STUB_MODE=1`. The stub returns `Complete{changed: true, attributes_delta: {<defaults>}}` keyed by `node_type` from a small fixture map: `scope → {scope_notes: "stub"}`, `draft → {}` (writes a fixed string to its `write` region), `review → {accepted: true}`.
6. Returns handles (`*ControlAPI`, `*pgxpool.Pool`, etc.).

The test:

1. Calls `BringUpStack(t)`.
2. Bulk-inserts 100 items into `topics_items` with payloads `{"area": "A_<n>", "subtopic": "S_<n>"}` (n = 1..100).
3. Deploys the §11.5 template via control-api (`POST /templates`), then creates one instance (`POST /instances`).
4. Sets `model-budget` lock limit high (`limit: 50`) in the deployed template so executor parallelism is unconstrained.
5. **Drives 100 source-node fires via the force-fire admin endpoint plus a fast scheduler tick.**
   - `BringUpStack` configures the in-process scheduler with a tick interval of `50ms` (overrides the production default; smoke-test only).
   - For `n` in 1..100: `POST /admin/scheduled-nodes/{claim-topic-node-id}/force-fire`. The handler updates `rimsky_schedules.next_fire_at = now()` and returns `204` immediately (§16.1).
   - **Between fire `n` and fire `n+1`**, the test waits for the source node to transition through `running → fresh` (or `failed`) — observed by polling `rimsky_nodes.state` every 50ms with a per-fire timeout of `5s` (see "Wall-clock structure" below). This guarantees one source-node run per force-fire (no dropped fires from coalescing).
   - The 100 calls thus run sequentially; with a 50ms scheduler tick and stub executor, each fire's full cycle is sub-second under the happy path.
6. Once all 100 force-fires complete (the loop above exits), poll every 250ms (timeout 300s) for the downstream-cascade steady-state:
   - `count(rimsky_events WHERE kind='work_completed' AND payload->>'node_type'='review') >= 100`
   - AND `count(rimsky_dispatch WHERE claimed_by IS NOT NULL) = 0`
   - AND `count(rimsky_lock_holders) = 0`
   - AND `count(rimsky_claim_holders WHERE state='active') = 0`

Then assert (no further polling needed):
- `SELECT count(*) FROM topics_items WHERE state != 'available'` → 0 (all items returned to ring buffer).
- `SELECT count(*) FROM topics_items WHERE state = 'available'` → 100 (ring buffer never deletes).
- control-api HTTP health endpoint returns 200.

Wall-clock structure:
- **Phase 1 (force-fires, sequential):** 100 × (per-fire wait). Per-fire timeout is **5s** — happy path is sub-second; hitting 5s indicates a real bug, not slowness. **Fail-fast: the first per-fire timeout terminates the test immediately**, so phase 1 wall-clock is bounded by the time it takes to hit one bug, not by the cumulative `5s × 100` arithmetic. Phase 1 happy-path: ≤ 30s total.
- **Phase 2 (cascade drain, polling):** 300s budget for downstream nodes (scope, draft, review) to drain, given `model-budget` permits 50-wide parallelism.

The Go test sets `-timeout 10m` (`go test -timeout 10m ./test/smoke/...`); the 10m global budget covers the happy path with margin. Per-fire timeout is enforced via `context.WithTimeout(ctx, 5*time.Second)` per call. Phase 1 + Phase 2 happy-path total ≤ 5 minutes wall-clock.

**Out of v1 acceptance**: a "kill supervisor mid-run" resume test is **not written** in this work. (Resume semantics are exercised at the unit-test level in `test/scenarios/attributes/attributes_resumable_preserve_test.go` and `test/scenarios/claim_stores/claim_resumption_test.go`; a full-stack mid-run kill is left for follow-up.)

### 19.3 Required final checks

Per project rules, all of these must pass:

- `go build ./...`
- `go test ./... -count=1`
- `go test ./test/scenarios/... -count=1` (testcontainers)
- `go test ./test/smoke/... -count=1` (the smoke fixture above)
- `go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3`
- `make lint`
- `make proto-gen` then `git diff --exit-code proto/v1/gen/` (regenerated files match check-in)
- `cd executors/claude-agent && npm install && npm test && npm run build`
- `deploy/build-images.sh` succeeds.
- `docker compose -f deploy/docker-compose.yml up -d` followed by `curl -fsS http://localhost:8080/health` returns 200.

## 20. Risks and accepted limitations

These are accepted; the agents do not try to solve them in this work.

1. **Multi-store atomic commit is not provided.** A node writing to two stores commits independently per store. Documented in `store-author-guide.md`.
2. **Direct-mode store-level quality rules** are warned-and-ignored.
3. **Region-overlap detection** is per-store-kind; v1 ships only filesystem path-glob overlap.
4. **`HasClaimableItem` TOCTOU**: the eligibility hint may be stale; `AcquireLock` re-validates atomically. No correctness issue, just an occasional wasted candidate.
5. **Dead-letter handling** is manual (operator SQL flips `state='dead_letter' → 'available'`).
6. **Attribute history beyond last-run** is via the events log; no per-attempt history table.
7. **Output merging across upstreams** is namespaced by upstream node name; conflicts are last-write-wins in source-directive declaration order.
8. **Stretch-test flakiness**: the supervisor-kill-mid-run resume test is allowed to skip if flaky — see §19.2.
9. **`claim-store-postgres` items table is operator-owned**; rimsky verifies but does not create.
10. **`stores.yml` divergence between control-api and supervisors** is silently corrected at dispatch eligibility (a supervisor without the store simply doesn't claim relevant work).

## 21. Glossary

- **Store** — operator-configured, named data backend.
- **Region** — a portion of a store's namespace.
- **Lock** — a held exclusivity claim; uniform across named, region, claim flavours.
- **Lock holder** — a row in `rimsky_lock_holders`. Authoritative source of "who holds lock X."
- **Claim holder** — a row in `rimsky_claim_holders`. Tracks (held-claim, terminal-leaf-node) pairs for held claims.
- **Handle** — native-shape reference to locked region(s) or claim payload.
- **Sidecar** — store-private workspace for non-direct modes (post-v1).
- **Claim** — the store-picks-region flavour of lock acquisition.
- **Payload** — user data carried by a claimed item.
- **Claim ref** — rimsky-internal bookkeeping for a held claim; identical to "claim holder."
- **Attributes** — per-node-run typed JSON object; replaces inputs / outputs / claim_metadata.
- **Userdata** — opaque-to-rimsky executor configuration block.
- **Source-directive** — `source: "{{...}}"` annotation on an attribute schema field.
- **Direct mode** — store mode where the handle points at the live region; no sidecar.
- **Hold** — opt-in claim flag; claim survives past the claiming node's commit.
- **Terminal-leaf** — a node in a holding subgraph with no further descendants in that subgraph; resolves the held claim.
- **Native handle** — store-kind-specific handle struct (e.g. `FilesystemDirectHandle`).
- **Acquisition transaction** — the postgres transaction inside which dispatch claim, lock-holder inserts, and store-side `AcquireLock` mutations all happen atomically.

## 22. Pointer to the discursive design

The companion at `docs/2026-04-25-stores-redesign.md` (moved to `docs/history/` once implementation lands) carries the rationale: why these decisions were made, what alternatives were considered, what the prototyping revealed. This spec is the contract; the companion is the why.
