# Stores Redesign

Status: design proposal. All seven decision points worked through in discussion; ready for implementation.
Supersedes (when accepted): `node-graph-design.md` §3.4 (Userdata), §4 (Resources), §5 and §6 (parts of), §8 (Node contract); `architecture.md` §1.2 (Resource library), §3 (Package import rules — affected packages), §5 (Blessed invariants — additions), §8 (Storage tables); `protocol.md` (substantially reshaped); `executor-author-guide.md` (updated); `resource-author-guide.md` (replaced by `store-author-guide.md`).

## 0. Context

This redesign emerged from prototyping a real workload: a queue-driven content-authoring pipeline where many parallel worker instances claim items from a backlog, run an LLM agent against a filesystem-managed corpus, and commit results — all while sharing a global rate-limit budget. Walking through what *would* fit revealed the current `Resource` model fighting the underlying systems in general, not just for that case.

The fundamental mismatch: 100 markdown files are 100 paths in 1 tree, not 100 independent versioned things. The `Resource` abstraction treats each datum as a sole-writer-owned versioned unit, but real systems are shared backends with regions, locks, and commit semantics. The redesign restructures the abstraction to admit that.

Rimsky is v0; there is no production data to migrate. There is also no migration guide to write — the first consumer of rimsky is the first consumer, not a v0→v1 upgrade for existing operators. The proposal takes the clean break.

### 0.1 Three operations on state, kept distinct

The design conversation revealed that current rimsky conflates three operations under "rollback." The redesign keeps them separate, because they have different cost profiles and different applicability.

- **Atomicity** (universal, free): a partial write never becomes live. Either the new state is fully visible or it isn't. Filesystem: rename. Database: transaction. S3: write-then-rename. Every reasonable store provides this. What people often credit "rollback" with — "the bad commit didn't land" — is actually atomicity.
- **Discard** (per-attempt; mutually exclusive with resume): throw away in-progress, uncommitted work. Useful when an attempt errors and the partial state is invalid. Always available in sidecar/versioned modes. **Not available in direct-mode stores** — there is no sidecar to throw away; the writes are already on disk.
- **Resume** (per-attempt; mutually exclusive with discard): preserve in-progress work across executor death so the next dispatch continues from where the prior left off. Per-store-kind capability — filesystem, S3, git, append-log can offer it; databases generally can't.
- **Restore** (per committed state, opt-in): rewind live state to a previously-committed version. Genuinely opt-in; most stores don't have it. Versioned mode and git-kind stores do.

Discard and resume operate on uncommitted (sidecar) state and are mutually exclusive per attempt. Restore operates on committed state and is orthogonal to both.

### 0.2 Decisions made during design

Quick summary of design decisions accepted during discussion. Each is elaborated in the relevant section.

1. **All lock state in postgres**, not in stores. Hybrid eligibility: predicate-in-query for named locks; in-Go conflict-evaluation (via store-supplied `RegionsConflict` / `HasClaimableItem` callbacks) for region locks and claim availability. (§2.3, §10)
2. **Stores are global, name-resolved.** Operator-configured at deployment level; templates reference by name. Parallel mechanism to today's executor name resolution. (§2.1)
3. **Configuration-driven node shape uniformity.** No `kind` enum on nodes. A node is whatever its property combination makes it: pure-cascade, executor-driven, native (claim-only), or any composition. One omnibus runner handles all. (§9)
4. **Claim is part of the store contract, not queue-specific.** Lock acquisition has two flavors: caller-specified region, and store-picks-region (claim). Same downstream lifecycle. (§2.6)
5. **Multi-claim is supported from v1.** Per-store entry; metadata namespaced by store name in the node's attributes. (§2.6, §9)
6. **Claim payload is data; claim ref is bookkeeping.** Default is claim-and-forget (ack on commit, release on give_up). Long-lived claims opt-in via `hold: true`; rimsky tracks holders via `rimsky_claim_holders`; explicit `claim_resolutions` required at terminal nodes. (§2.6)
7. **Rimsky doesn't enqueue.** Writing to a queue / ring buffer / work-table is a store-external concern (an HTTP endpoint or admin operation), not a runner action. Rimsky's vocabulary is claim, lock, commit, resolve, release. (§3.5)
8. **Queues and ring buffers are configurations of one `claim_store` kind.** They differ only in default `on_commit` and `on_give_up` actions: queue defaults to `delete`, ring buffer defaults to `release_to_back`. (§3.5)
9. **Attributes** (the per-node, per-run typed data table) replace `outputs`, `inputs`, and `claim_metadata` as a unified concept. Schema declares the shape; `source:` directives identify rimsky-populated fields; sourceless fields are executor-populated. (§2.7)
10. **Userdata is truly opaque.** No `{{...}}` syntax inside it. Rimsky owns all `{{...}}` substitution into `attributes` source-directives. Executors do no substitution. (§2.7)

## 1. Motivation

The `Resource` abstraction in current rimsky treats each unit of data as an independently-versioned, sole-writer-owned thing. The reality of the underlying systems is different:

- 100 inline-jsonb resources are 100 rows in 1 table.
- 100 filesystem resources are 100 paths in 1 directory tree.
- 100 external-sql resources are 100 staging tables wrapping rows in caller-owned tables.

The fiction works for static reasoning ("only node X writes to resource Y"), but it costs us:

1. **Cross-resource atomicity is impossible** even when the underlying store could trivially do it (one transaction over two rows, one rename across two files).
2. **External-sql's per-resource staging tables proliferate** because we pretend each row deserves its own staging-and-swap, when postgres already has transactions.
3. **Lock granularity is hardcoded** at one-mutex-per-resource. No way to say "lock this subtree" or "this is append-only, no contention possible."
4. **Concurrency tags duplicate locking** at a different layer — three mechanisms (resource ownership, dispatch claim, concurrency tag) all doing variants of conflict management.
5. **Rollback is over-emphasized.** Most "rollback" in real systems is just atomicity ("the bad write never landed"). Genuine version-restoration is rare and is best opt-in.

The proposal: replace `Resource` with `Store`, expose conflict management as first-class primitives (locks, with claim as a sub-flavor), make the executor protocol transparent to the store's underlying shape, and unify per-run wiring as **attributes** with rimsky-owned substitution.

## 2. Core model

### 2.1 Store

A **store** is a deployment-level data backend, configured once by operators. It corresponds to a real underlying system: a filesystem tree, a database, an S3 bucket, a queue/ring-buffer-backed workload table, an append-only log, a git repository. Many nodes write into one store, into different regions or via different claims.

Stores are **named** in the deployment configuration (orchestrator level — control-api owns the registry; supervisors read it). Templates reference stores by name; resolution is parallel to today's executor name resolution.

```yaml
stores:
  content:
    kind: filesystem
    mode: direct                   # direct | sidecar | versioned
    root: /workspace/content
  app-data:
    kind: postgres
    mode: direct
    dsn: "${APP_DB_URL}"
  archive:
    kind: s3
    mode: versioned
    bucket: my-archive
    sidecar_prefix: ".rimsky-store/"
  code-repo:
    kind: git
    mode: versioned                # git is natively versioned
    repo_url: "git@github.com:org/code.git"
    local_path: /workspace/repos/code
    branch_prefix: "rimsky/auto/"
    main_branch: main
    commit_strategy: open-pr
  topics-ring:
    kind: claim_store
    backend: postgres
    items_table: topics_ring
    on_commit_default:  release_to_back     # ring-buffer behavior
    on_give_up_default: release_to_back
```

Templates reference stores by name; they do not declare implementations or per-store-instance configuration. Operators own backends; template authors compose workflows over them.

Different supervisor pools can specialize: a pool with config entries for some stores only claims dispatch rows whose nodes' required stores are in that pool's config. Mirrors today's executor-pool specialization.

### 2.2 Region

A **region** is a portion of a store's namespace. Region grammar is per-store-kind:

| Store kind | Region grammar |
|---|---|
| filesystem | path globs (`section-a/**`, `shared/glossary.md`) |
| database | `{schema, table, predicate}` or `{table, row_id_set}` |
| s3 | key prefixes |
| append-only log | the whole log, or a partition key |
| git | branch name |
| claim_store (queue / ring buffer) | implicit per-claim; see §2.6 |

A node's template declares the regions it writes (`write`) and reads (`read`). The store enforces conflict semantics over these regions.

### 2.3 Lock

A **lock** is a node's exclusivity claim on a named scope (lock name) or a region (within a store), held for the duration of one execution. Acquired before the node enters `running`; held until the node exits `running` (commit, discard, or preserve-for-resume).

Locks are **the unified conflict-management primitive**, replacing concurrency tags, resource ownership, and dispatch-queue claim semantics:

- Per-region exclusive lock = today's resource ownership.
- Counting semaphore on a named lock = today's concurrency tag with limit N.
- Mutex on a named lock = today's concurrency tag with limit 1.

**All lock state lives in postgres** (`rimsky_lock_holders`), uniformly across stores and lock kinds. This gives one heartbeat/expiry/orphan-reap mechanism, one observability surface, and lets named-lock contention be evaluated by SQL predicate. Stores never store lock state themselves.

For region locks, stores supply pure conflict-evaluation logic via `Store.RegionsConflict(a, b)` — a function the supervisor calls when checking new acquisitions against existing holders. For claim availability, stores supply `Store.HasClaimableItem(criteria)`.

### 2.4 Handle

A **handle** is a native-shape reference to the locked region(s) (or claim payload), passed to the executor. The handle's form depends on the store's kind:

| Store kind | Handle |
|---|---|
| filesystem | a directory path the executor uses with normal POSIX ops |
| database | a connection string (or pgx pool) inside an open transaction |
| s3 | endpoint + prefix-scoped credentials |
| append-only log | a write-only file descriptor or stream URL |
| git | a worktree path the executor uses with normal POSIX ops; a normal `git` CLI works against it |
| claim_store | the claimed item's payload (read-only) |

**Transparency principle**: the executor sees the underlying system in its native form. No special "rimsky-store" API. Standard tools (CLI binaries, ORM libraries, SDKs) work unmodified. The sidecar, locking, staging, and commit machinery are entirely behind the handle.

### 2.5 Sidecar

A **sidecar** is the store's per-lock private workspace, used in `sidecar` and `versioned` modes. For a filesystem store this is a working copy of the locked region under `.rimsky-store/working/<lock-id>/`. For S3, a sidecar object prefix in the same bucket. For a database, the open transaction itself functions as a sidecar.

Direct-mode stores have no sidecar — the handle points directly at the live region.

### 2.6 Claim

A **claim** is the store-picks-region variant of lock acquisition. Lock acquisition has two flavors:

1. **Specified-region lock**: the caller declares the region; the store locks it (or fails / blocks). Filesystem, git, S3, single-database row locks.
2. **Claim**: the caller asks the store to *pick* a region from a pool of currently-eligible regions, lock it, and report what was picked. Queues, ring buffers, pools, work-stealing tables.

Both flavors produce the same downstream artifact: a `LockHandle` with identical commit/discard/release semantics. The only difference is **who chose the region**.

**Multi-claim is supported.** A node may have multiple store entries each with `claim: true` from different stores. Each claim's metadata is namespaced under the store's name in the node's attributes (§2.7). Per-claim `required: true|false` flag (default true) controls whether an empty claim no-op-commits the whole node or proceeds without that store's data.

#### 2.6.1 Payload vs. claim ref

Two distinct things come back from claim acquisition:

- **Payload** — the data that was in the queue entry (or pool item, or work-stealing row). User data; once read, it propagates freely as data.
- **Claim ref** — rimsky-internal bookkeeping that says "this item is in the in-progress state and someone needs to delete or release it eventually." Tracked in `rimsky_claim_holders`.

These are separate concerns. Whether downstream nodes propagate the payload (or any subset) is irrelevant to the claim's lifecycle. The claim's lifecycle is governed entirely by the bookkeeping: who's holding the ref, and what action they take when they release it.

#### 2.6.2 Default: claim-and-forget

By default, when the claiming node commits successfully, the claim resolves immediately (`on_commit` action; default `delete` for queues, `release_to_back` for ring buffers). On give-up, the claim resolves via `on_give_up` (default release back to queue / buffer).

This matches how most queue-driven systems work: process the message in one unit; ack on success; release on failure. Most workflows fit this.

#### 2.6.3 Opt-in: hold across the workflow

For workflows that want the claim to anchor a longer pipeline — "the queue item should stay in flight until the entire downstream chain completes" — the claim acquisition declares `hold: true`. Then:

- The claim is registered in `rimsky_claim_holders` at commit time.
- Holding propagates implicitly through the dependency DAG: any node downstream of a holding source is part of the hold.
- At least one node in the holding subgraph must declare `claim_resolutions` to terminate the hold.
- Template-deploy validation checks: every "terminal" node (leaf within the holding subgraph) must declare resolution for the claim, or deploy fails with "claim leaked at terminal X."

```yaml
- type: claim-topic
  stores:
    - name: topics-ring
      claim: true
      hold: true
  ...

- type: review
  dependencies: [draft, scope, claim-topic]
  claim_resolutions:
    - source: claim-topic
      store:  topics-ring
      # uses store defaults: release_to_back on commit, release_to_back on give_up
```

#### 2.6.4 Reference counting and resolution semantics

`rimsky_claim_holders` rows track terminal-leaf nodes for held claims. As terminals commit:

- `delete` action: any holder deleting wins (atomic ack of the queue/buffer entry); other holders' rows become silently complete on their next commit.
- `release` action: decrements the holder count; the actual queue/buffer release fires only when count → 0 from releases (last-released-wins).
- A mixed delete + release scenario: delete wins regardless of order.

For linear chains (single terminal), there's exactly one row, exactly one resolution. For fan-out (multiple terminals), the count walks down as each completes.

Control-API endpoint `GET /claims/:id/holders` exposes current holders for debugging.

#### 2.6.5 Interface

The store's lock-acquisition method takes a `LockSpec` discriminated by kind:

```go
type LockSpec interface {
    Kind() string  // "region" | "claim"
}

type RegionLockSpec struct {
    Region StoreRegion  // caller-specified
}

type ClaimLockSpec struct {
    Criteria map[string]any  // optional filters (priority bucket, partition, etc.)
    Hold     bool             // false: claim-and-forget; true: register in claim_holders
    OnCommit string           // overrides store default
    OnGiveUp string           // overrides store default
}

type Store interface {
    AcquireLock(ctx, LockSpec) (LockHandle, ClaimResult, error)
    LockEligible(ctx, LockSpec) (bool, error)
    OpenHandle(ctx, LockHandle) (NativeHandle, error)
    ReleaseLock(ctx, LockHandle, action ReleaseAction) error
    // ... commit/discard etc. ...
}

type ClaimResult struct {
    ResolvedRegion any              // what the store picked (kind-specific)
    Payload        any              // user-data payload from the claimed item
    ClaimID        string           // rimsky-internal ID; tracked in claim_holders
}
```

For specified-region locks, `ClaimResult.Payload` is empty. For claims, payload carries whatever was in the claimed item.

### 2.7 Attributes

A node's **attributes** is a single per-run typed data table, schema-declared in the template, populated by a mix of rimsky-supplied sources and executor-emitted writes.

Attributes replace and unify what would otherwise be three concepts: per-run inputs, per-run outputs, and claim metadata. One schema, one persisted data table per node-run, one mechanism for downstream nodes to reference into it via `{{deps.<node>.<field>}}`.

#### 2.7.1 Schema and source directives

```yaml
- type: scope
  dependencies: [claim-topic]
  attributes:
    schema:
      type: object
      properties:
        area:        { type: string, source: "{{deps.claim-topic.area}}" }
        subtopic:    { type: string, source: "{{deps.claim-topic.subtopic}}" }
        scope_notes: { type: string }
      required: [area, subtopic, scope_notes]
```

Each property's schema may include a `source:` directive. Three populator types:

- **`source: "{{deps.<node>.<field>}}"`** — populated at dispatch from upstream node's attributes.
- **`source: "{{claim.<store>.<field>}}"`** — populated at dispatch from claim metadata when the node has a claim on that store.
- **`source: "{{params.<key>}}"`** — populated at dispatch from instance params.
- **No source directive** — populated by the executor (or supervisor for native nodes) during the run.

Substitution syntax is the same `{{...}}` form used today. **Rimsky owns all substitution.** Executors do no substitution.

#### 2.7.2 Userdata stays opaque

Userdata is purely executor configuration: model, system prompt reference, tool list, prompt-construction strategy. **No `{{...}}` syntax inside userdata.** Rimsky never parses or substitutes userdata.

```yaml
- type: scope
  attributes:
    schema:
      properties:
        area:        { type: string, source: "{{deps.claim-topic.area}}" }
        subtopic:    { type: string, source: "{{deps.claim-topic.subtopic}}" }
        scope_notes: { type: string }
  userdata:
    model: claude-sonnet-4-6
    system_prompt_ref: "scope-system.md"
    # no template syntax — opaque executor config
```

The executor reads from the attributes table to construct its prompt context. How it does so is the executor's concern (e.g., the claude-agent executor exposes attributes as MCP tool inputs, or dumps them as JSON in a system-prompt section, or whatever).

#### 2.7.3 Validation

Attributes schema is JSON Schema; validation happens at two points:

- **At dispatch** (after rimsky substitution): all source-directives must resolve. Failure raises `template_resolution_failed` and routes through the policy chain.
- **At commit** (after executor writes): the entire populated attributes object must validate against the schema. Required-but-empty fields fail. Invalid types fail. Failure raises `attributes_schema_failed` and routes through the policy chain.

#### 2.7.4 Writeback patterns

Two patterns for executors to populate sourceless fields:

- **Terminal-final** (default): the executor accumulates attribute writes internally; emits a final `complete{ attributes_delta: {...} }` terminal event. Rimsky merges, validates, persists. Simple; sufficient for short-running nodes.
- **Incremental-via-callback**: the executor sets fields one at a time via a callback URL (or its MCP equivalent). Rimsky persists each update immediately. Terminal event signals "done" without carrying data. Supports resumption naturally — partial attributes survive interruption.

The claude-agent executor uses the incremental pattern because resumption is its biggest win.

#### 2.7.5 Resumption-friendly

The attributes table IS the resumable state. If the executor is killed mid-run, whatever it has written so far is preserved (in the `rimsky_node_attributes` table). The next dispatch hands back the same attributes; the executor sees its prior partial state and continues.

This is independent of store sidecars / working copies — store-side resumption preserves writes-to-store; attributes-side resumption preserves the executor's structured state. Both compose; both opt-in via `resumable: true` on the relevant declarations.

### 2.8 Templated fields

`{{...}}` substitution applies to attributes source-directives (described above) and to a few other rimsky-owned fields:

- `stores[*].read` and `stores[*].write` region declarations — substituted at dispatch before lock acquisition.
- `locks[*].name` — substituted at dispatch before eligibility evaluation.
- Anything else rimsky parses and acts on directly.

Two phases of resolution:

- **Instantiation-time** (`{params.x}` single brace) — at `POST /instances`. Stable across the instance's life.
- **Dispatch-time** (`{{deps.<node>.<field>}}` / `{{claim.<store>.<field>}}` / `{{params.<key>}}` double brace) — fresh per run, resolved against current attributes / claim metadata / instance params.

Visual distinction matches the timing distinction; the supervisor uses brace count to know which phase to substitute in.

## 3. The three modes

Stores offer one of three modes, picked at deployment time:

### 3.1 Direct

- Lock + handle to live region.
- Atomicity at whatever granularity the underlying system offers (per-file via rename for filesystem; per-statement-or-transaction for SQL; per-PUT for S3).
- **Resumption is trivial**: in-progress work simply persists in the live region. Next dispatch picks up where it left off.
- **No discard**: nothing to throw away. Failed work remains until overwritten.
- **No restoration**: rollback to prior committed state is unavailable; use external mechanisms (git, backups).

Best fit: content-authoring workflows where individual file writes are atomic and external version control (git, operated outside rimsky) provides history.

### 3.2 Sidecar

- Lock + handle to a private workspace (working copy).
- On `complete{changed: true}`: store atomically applies sidecar to live.
- On `complete{changed: false}`: store discards sidecar.
- On `blocked` / `errored` with `discard_then_retry`: store discards sidecar.
- On `blocked` / `errored` with `resume_then_retry`: store preserves sidecar for next dispatch.
- **Atomicity at lock-region granularity** — multi-file or multi-row commits land all-or-nothing.
- **No restoration**: prior committed versions are not retained.

Best fit: workflows that need lock-region atomicity but don't need version history.

### 3.3 Versioned

- Sidecar mode + retained committed history.
- `Restore(target)` swaps live region to a prior committed version (within the retention window).
- Implementation varies by store: filesystem keeps `.previous` plus a delta log; database keeps audit-log entries replayable in reverse; S3 keeps prior objects under a versioning prefix.
- **For large stores where snapshotting is impractical** (multi-TB databases, big filesystems), versioned mode uses delta-only retention rather than full copies.

Best fit: critical data where operator-driven rollback to known-good states is required.

### 3.4 Git as a store

Git deserves its own subsection because it composes the modes and capabilities differently. Native versioning, atomic commit, history-based restoration, and durable working trees all come from git itself.

**Region = branch.** Confirmed during design: the natural and correct unit of isolation in a git store is the branch, not the path. Two locks on different branches never conflict at write time — conflict, if any, surfaces at merge time, which is a separate phase. A region is `branch: rimsky/auto/work-item-1`; that's the lock. Path-narrowing within a branch is not part of v1.

**Handle = a worktree path.** Lock acquisition runs `git worktree add <sidecar>/worktrees/<lock-id> <branch>`. Standard tooling — file editors, language servers, the `git` CLI itself — works unmodified.

**Commit strategies**, configured per store or overridden per node:

| Strategy | What happens on `complete{changed: true}` |
|---|---|
| `local-only` | `git commit` on the worktree's branch. Branch is local-only; operator pushes manually. |
| `push` | Commit + `git push origin <branch>`. CI takes over from there. |
| `open-pr` | Commit + push + create a PR via the configured forge API. For human-in-loop review workflows. |
| `merge-locally` | Commit + fast-forward `main_branch` to include the new commit. Branch is then deleted. **Should be deployment-flag-guarded** (e.g. `allow_merge_locally: true` at the store-config level) — automatic merges into the canonical branch with no human review is high-blast-radius. |

**Resumption is rich.** A worktree persists across executor death. Next dispatch hands back the same worktree. Agent sees uncommitted dirty state, possibly iterative commits already on the branch, full git log.

**Discard is `git worktree remove --force` plus `git branch -D`.** Configurable to keep the branch via `on_discard: keep_branch`.

**Restoration is `git checkout <sha>` or `git revert <sha>`.** Full history always available.

**Quality rules become git pre-commit hooks** (or equivalent integrated check). Store-level rules in the store's `quality_rules` config; the implementation runs them as `git diff --cached`-driven validators before `git commit`.

**Drawbacks**: git operations are slower than raw filesystem ops; worktrees are finite (mitigation: pool/reuse); push contention requires fetch+rebase+retry; auth (SSH keys / tokens) is operator-managed; branch GC needed if `keep_branch: true`.

This is the obvious store kind for the agentic-coding workflow class.

### 3.5 Claim stores (queues, ring buffers, work tables)

A `claim_store` holds a backlog of items and hands them out via claim (§2.6). The configuration determines its policy.

**Items**: each is a JSON payload with a store-assigned ID. Order may be FIFO, priority-based, round-robin, or any other policy the implementation chooses.

**Operations** (rimsky-side):
- **Claim** — atomic acquisition of the next available item per the store's selection policy.
- **Acknowledge / Delete** — mark item permanently complete (item removed from store).
- **Release** — return item to store for re-acquisition (semantics vary by policy: queue → return to head; ring buffer → return to back; work-stealing → mark `pending` again).

**Operations NOT in rimsky's vocabulary**: enqueue / append / item-creation. These are store-external — a store's own HTTP/admin endpoint, used by operators or by executors that want to push work. Rimsky doesn't drive them.

**Configurations**:

- **Queue** (FIFO + delete-on-success):
  ```yaml
  inbound-queue:
    kind: claim_store
    backend: postgres
    items_table: inbound_queue
    on_commit_default:  delete
    on_give_up_default: release_to_head
  ```

- **Ring buffer** (cyclic, never delete):
  ```yaml
  topics-ring:
    kind: claim_store
    backend: postgres
    items_table: topics_ring
    on_commit_default:  release_to_back
    on_give_up_default: release_to_back
  ```

- **Work-stealing table**: similar to queue but with custom selection criteria (priority, partition, etc.).

The store's selection policy and persistence backend are independent. `backend: postgres` is the v1 reference; SQS, Redis, etc. ship later.

**Resumption** is configurable per claim_store. Default: claim is preserved across executor death (item stays in the in-progress state, claim_token still valid); next dispatch picks up the same item. Visibility timeout backstops in case rimsky's own bookkeeping fails.

**Capabilities**:

- `SupportsRegionLock`: false
- `SupportsClaim`: true
- `SupportsDiscard`: true (Release)
- `SupportsResume`: true (claim preservation)
- `SupportsRestore`: false

**Pacing via locks**: a claim_store's per-store rate limit is a counting semaphore on a named lock the claim-nodes share — `locks: [{name: "topics-ring:concurrent-claims", mode: counting, limit: 5}]`. Caps how many parallel claims-in-flight; orthogonal to backlog size.

**Drawbacks**:
- **Item sizing**: payloads are small JSON. Large items belong in another store with the claim payload carrying a reference.
- **Visibility timeout vs. heartbeat**: rimsky's heartbeat handles supervisor death; the store's visibility timeout is a backstop. Need careful alignment so they don't interfere.
- **Dead-letter handling**: items that fail `max_retries` move to a dead-letter sub-state; operators inspect, edit, re-enqueue manually.

## 4. Required vs. optional capabilities

A store implementation declares its capabilities. The required core is small:

```go
type Store interface {
    Kind() string
    AcquireLock(ctx, LockSpec) (LockHandle, ClaimResult, error)
    LockEligible(ctx, LockSpec) (bool, error)
    ReleaseLock(ctx, LockHandle, action ReleaseAction) error
    OpenHandle(ctx, LockHandle) (NativeHandle, error)
    Capabilities() Capabilities

    // Region-overlap check, called by supervisor when evaluating new acquisitions
    // against existing holders for this store.
    RegionsConflict(a, b any) bool
}

type Capabilities struct {
    SupportsRegionLock   bool
    SupportsClaim        bool
    SupportsDiscard      bool
    SupportsResume       bool
    SupportsRestore      bool
    SupportsAtomicMulti  bool
    KeepVersionsMax      int
}
```

Optional methods are conditional on capabilities:

```go
type DiscardableStore interface {
    Store
    Discard(ctx, LockHandle) error
}

type ResumableStore interface {
    Store
    HasPriorWork(ctx, LockSpec) (bool, error)
    OpenHandleResume(ctx, LockHandle) (NativeHandle, error)
}

type RestorableStore interface {
    Store
    Restore(ctx, RegionSpec, target VersionRef) error
    ListVersions(ctx, RegionSpec, limit int) ([]Version, error)
}

type ClaimableStore interface {
    Store
    HasClaimableItem(ctx, criteria map[string]any) (bool, error)
}
```

Templates that declare features against a store missing the relevant capability fail at template-deploy validation.

## 5. Transparency principle in detail

The handle is the entire executor-facing API of the store. Once the executor has the handle, the store is invisible.

**Filesystem store** (direct mode):
1. Supervisor calls `store.AcquireLock(["section-a/**"])` → `LockHandle`.
2. Supervisor calls `store.OpenHandle(LockHandle)` → `{path: "/workspace/content"}`.
3. Supervisor invokes executor with `stores.content.handle.path`.
4. Executor reads, writes, deletes files. Each write is atomic via filesystem primitive.
5. Executor signals `complete{changed: true}`.
6. Supervisor calls `store.ReleaseLock(LockHandle)`. (No commit: live tree was the working surface.)

**Filesystem store** (sidecar mode):
1. Supervisor calls `store.AcquireLock(["section-a/**"])` → `LockHandle`.
2. Supervisor calls `store.OpenHandle(LockHandle)` → `{path: ".../working/abc123/section-a"}`.
3. Sidecar is a working copy of the live region (reflink/hardlink/copy).
4. Executor writes into that path; sees own writes overlaid on original region's content.
5. Executor signals `complete{changed: true}`.
6. Supervisor calls `store.Commit(LockHandle)`: store applies delta atomically.
7. Supervisor calls `store.ReleaseLock(LockHandle)`.

**Database store** (direct mode):
1. Supervisor calls `store.AcquireLock(...)` → opens a transaction.
2. Supervisor calls `store.OpenHandle(LockHandle)` → connection inside that transaction.
3. Executor runs SQL against it.
4. Executor signals `complete{changed: true}`.
5. Supervisor calls `store.Commit(LockHandle)`: `COMMIT`.
6. Supervisor calls `store.ReleaseLock(LockHandle)`.

**Claim store** (queue / ring buffer):
1. Supervisor calls `store.AcquireLock(ClaimLockSpec{...})` → `LockHandle`, `ClaimResult{Payload: {...}, ClaimID}`.
2. Supervisor populates the node's attributes from claim metadata sources.
3. Optional executor: receives attributes; reads payload, possibly writes back to attributes.
4. Executor signals `complete`.
5. Supervisor commits per the claim's `on_commit` action (delete, release, etc.); releases lock.

The executor never knows whether the store is in direct, sidecar, or versioned mode, or whether it's a queue or a ring buffer. The handle behaves identically.

## 6. Resumption

Resumption is a per-store-kind capability, plus a parallel attributes-side capability:

| Store kind | Resumable? |
|---|---|
| filesystem (direct) | yes — work is in the live tree |
| filesystem (sidecar/versioned) | yes — sidecar working copy persists |
| database (direct) | no — transactions die with the connection |
| s3 (sidecar/versioned) | yes — sidecar prefix persists |
| append-only log | yes — partial entries accumulate |
| git | yes — worktree persists with uncommitted edits and any iterative commits |
| claim_store | yes — claim ref preserved; same payload re-handed |

Plus: **attributes resumption** — the executor's structured progress in `rimsky_node_attributes` survives interruption. Independent of store-side resumption; both compose.

When a node transitions `running → stale` (kill, supervisor crash, infra error) and the template declares `resumable: true`:

1. Next dispatch: supervisor queries `store.HasPriorWork(spec)` for store-side; reads `rimsky_node_attributes` for attributes-side.
2. If prior work exists: supervisor calls `store.OpenHandleResume(LockHandle)`; populates ExecuteRequest with `resumed: true` and prior attributes.
3. Else: fresh sidecar / fresh transaction; empty attributes (other than source-populated fields).

**Default is `resumable: false`** for safety. Resuming half-completed work is opt-in: some node logic isn't safe to resume halfway. Templates with idempotent or naturally-resumable work opt in.

## 7. Quality rules: store-level + node-level

Two layers, both useful:

- **Store-level rules**: configured with the store. Apply to every commit through that store. Universal invariants.
- **Node-level rules**: in the template. Apply to this node's writes only.

Examples for filesystem store:

```yaml
stores:
  content:
    kind: filesystem
    mode: direct
    quality_rules:
      - { type: valid_utf8, severity: error }
      - { type: max_size_bytes, value: 1048576, severity: error }
```

```yaml
nodes:
  - type: draft-section
    quality_rules:
      - { type: must_match_regex, target: write, pattern: "^---\\n", severity: error }
```

The supervisor evaluates node-level rules before calling commit. The store evaluates store-level rules during commit. Either rejection routes through the node's policy chain as `quality_rule_failed`.

For direct-mode stores, "rejection" is awkward (the bytes are already on disk). Direct-mode stores either offer no quality rules or evaluate them on each individual write through a wrapped handle. Sidecar and versioned modes evaluate at commit-time as described.

## 8. Executor protocol

`ExecuteRequest`:

```
{
    instance_id, node_id,
    userdata: {...},                       // opaque, verbatim, no substitution
    attributes: {...},                     // structured; source-fields pre-populated
    attributes_schema: {...},              // for executor reference
    stores: {                              // handles
        <store_name>: {
            kind: "filesystem" | "database" | "s3" | "git" | "claim_store" | ...,
            handle: <kind-specific>,
            write_regions: [...],          // resolved
            read_regions: [...],           // resolved
            resumed: bool
        }
    },
    callback_url, cancel_token,
    resumed: bool                          // attributes have prior executor state
}
```

Note absent fields:
- No `deps_outputs` — folded into `attributes`.
- No `claim_metadata` — folded into `attributes` (via `source: "{{claim.<store>.<field>}}"` directives).
- No `result` field on terminal events — the executor's output IS its writes (through store handles, into attributes).

Terminal events:

```
{ kind: "complete", changed: true,  change_summary: "...", attributes_delta: {...} }
{ kind: "complete", changed: false }
{ kind: "blocked", reason, context }
{ kind: "errored", error_class, payload }
```

`attributes_delta` is the executor's final writeback for the terminal-final pattern (§2.7.4). Executors using the incremental pattern via callback don't include it; the callback persists each update separately.

Supervisor-side actions driven by terminal events:

| Terminal event | Direct mode | Sidecar/Versioned mode |
|---|---|---|
| `complete{changed: true}` | release lock | commit, release lock |
| `complete{changed: false}` | release lock | discard, release lock |
| `blocked` / `errored` + `discard_then_retry` | release lock (in-flight writes already on disk) | discard, release lock |
| `blocked` / `errored` + `resume_then_retry` | release lock; sidecar IS the live tree | preserve sidecar, release lock |
| `blocked` / `errored` + `give_up` | release lock | discard, release lock |

For claim stores, `release lock` honors the claim's `on_commit` / `on_give_up` action (delete, release_to_back, etc.). For held claims, ack-or-release follows §2.6.4 (first-delete-wins; release on count → 0).

For direct-mode stores with `discard_then_retry`, there's no sidecar to discard, so the policy is effectively `keep_then_retry`. Templates against direct-mode stores should be aware.

## 9. Template syntax

A worked example showing all the new fields:

```yaml
nodes:
  # Pure-infra node: no executor, just claims from a ring buffer and
  # populates attributes from the claim payload.
  - type: claim-topic
    schedule: "*/30 * * * * *"
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

  # Processing node parameterized by the claim's attributes.
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
      - { name: model-budget, mode: counting, limit: 1 }
    error_types:
      review_rejected:
        policy:
          - { action: discard_then_retry, count: 2 }
          - { action: invalidate, targets: [scope] }
          - { action: give_up }

  # Writer node — region declaration is parameterized.
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
        resumable: true
    userdata:
      model: claude-sonnet-4-6
      system_prompt_ref: "draft-system.md"
    quality_rules:
      - { type: must_match_regex, target: write, pattern: "^---\\n", severity: error }
    locks:
      - { name: model-budget, mode: counting, limit: 1 }

  # Terminal node — declares claim resolution.
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
        # uses store defaults: release_to_back on commit, release_to_back on give_up
    userdata:
      model: claude-sonnet-4-6
      system_prompt_ref: "review-system.md"
    locks:
      - { name: model-budget, mode: counting, limit: 1 }
```

What's gone from old templates: `owns_resources`, `reads_resources`, `concurrency_tags`. What's new: `stores` (with claim/hold/write/read), `locks`, `attributes` (with `source:` directives), `claim_resolutions`. `userdata` stays opaque executor configuration.

## 10. Locks and the dispatch queue

A node is **dispatch-eligible** when:

1. All declared dependencies are `fresh`.
2. All required source-directives in `attributes` resolve (upstream attributes / claim metadata / params exist).
3. All required locks are acquirable (per the hybrid eligibility check below).

### 10.1 Hybrid lock-eligibility

All lock state lives in `rimsky_lock_holders`:

```sql
CREATE TABLE rimsky_lock_holders (
  id              UUID PRIMARY KEY,
  lock_kind       TEXT NOT NULL,           -- 'named' | 'region' | 'claim'
  lock_name       TEXT,                    -- for named locks
  store_id        UUID,                    -- for region/claim locks
  region_data     JSONB,                   -- for region locks (kind-specific)
  claim_id        TEXT,                    -- for claim locks (rimsky-internal)
  holder_supervisor_id UUID NOT NULL,
  holder_node_id  UUID NOT NULL,
  claimed_at      TIMESTAMPTZ NOT NULL,
  last_heartbeat_at TIMESTAMPTZ NOT NULL,
  expires_at      TIMESTAMPTZ NOT NULL
);
```

Eligibility evaluation is hybrid:

- **Named locks** (counting semaphores, mutexes by name): SQL predicate in the dispatch query. Counts holders for the same `lock_name` against the configured `limit`. Fast under contention; predicate-in-query at scale.
- **Region locks**: in-Go conflict evaluation. The supervisor loads existing holders for the relevant store from `rimsky_lock_holders`, calls `store.RegionsConflict(newRegion, existingRegion)` for each, bails if any conflict. N is small (region locks are usually disjoint by construction); the cost is bounded.
- **Claim availability**: in-Go check via `store.HasClaimableItem(criteria)`. Returns true/false to indicate whether the store has at least one claimable item right now.

### 10.2 Acquisition flow

Lock acquisition is atomic with dispatch claim. The supervisor's runner:

1. Selects a candidate dispatch row via `SELECT FOR UPDATE SKIP LOCKED`, with the named-lock predicate inlined.
2. For each region/claim lock the node requires, calls the in-Go eligibility check. If any fails, releases the candidate and tries the next.
3. Once all locks are eligible: in one transaction, claims the dispatch row (sets `claimed_by`) AND inserts holder rows in `rimsky_lock_holders`.
4. Verifies claim ownership (verify-before-run, mirrors today's invariant).
5. Proceeds to the runner.

### 10.3 Orphan reap

Lock holders have heartbeat / expires_at columns. The scheduler's tick includes a sweep parallel to the existing dispatch-claim sweep:

- Find rows where `last_heartbeat_at` < `now - 5 × heartbeat_timeout`.
- Delete the rows (claimant-guarded — match on `holder_supervisor_id` to prevent races with revived supervisors).
- Locks become eligible again; their dispatch rows return to the pool.

Generous orphan cutoff (5×) per the existing blessed invariant on dispatch claims.

### 10.4 Why this matters

This unifies three previously-separate mechanisms (resource ownership, dispatch claim, concurrency tag) into one. The cost is real schema commitment and a new sweep loop. The benefit is one mechanism with one set of invariants, observable via one table.

## 11. Implementation sequence (six commits)

Six commits on the current branch, each leaving the codebase building and tests passing. Rimsky is v0 with no external consumers; we don't need PR overhead — the sequence is for working-tree discipline (each commit is a coherent unit of progress) and for the explicit hard break in commit 4.

### Commit 1: Add `core/store/` package + new schema (additive only)

- New schema migrations: `rimsky_stores`, `rimsky_lock_holders`, `rimsky_node_attributes`, `rimsky_claim_holders`. Tables exist but unused.
- New `core/store/` package: `Store` interface, `LockSpec`, `ClaimResult`, factory registry.
- First implementations: filesystem direct-mode store, claim-store (postgres-backed). Unit tests + scenario tests for each.
- New doc: `store-author-guide.md`.
- No changes to template parser, supervisor runner, scheduler, or executors. Old code paths untouched.

### Commit 2: Lock state lives in postgres

- Move existing `concurrency_tag` enforcement to use `rimsky_lock_holders` internally. Behavior preserved; mechanism changes.
- Add hybrid lock-eligibility flow: named-locks predicate-in-query, region/claim post-filter using stores from commit 1.
- Add lock-orphan-reap to the scheduler tick, parallel to the existing dispatch-claim sweep.
- Update `architecture.md` §5 (blessed invariants — additions).

### Commit 3: Template parser + attributes machinery

- Template parser accepts new fields: `stores`, `locks`, `attributes` (with `source:`), `claim`, `hold`, `claim_resolutions`.
- Old fields (`owns_resources`, `reads_resources`, `concurrency_tags`) accepted with deprecation warnings during this transitional period.
- Source-substitution machinery: at dispatch, resolve all `{{deps.x}}` / `{{claim.x}}` / `{{params.x}}` against relevant inputs, populate `rimsky_node_attributes`.
- Schema validation against `attributes.schema` on commit.
- Claim-hold tracking via `rimsky_claim_holders`.
- `claim_resolutions` actions wired to commit + give_up flows.
- Update `node-graph-design.md` §3.4, §4, §8 to describe new vocabulary.

The supervisor's runner still uses the old executor protocol at this point; stores and attributes are populated but not yet handed to executors.

### Commit 4: Executor protocol changes (the hard break)

- Update `proto/v1/node_executor.proto`: add `stores`, `attributes`, `attributes_schema` to `ExecuteRequest`; remove `deps_data` / `reads_data`; update terminal events (drop `result`; add optional `attributes_delta` on `complete`).
- Update reference executors:
  - `claude-agent`: receive `attributes` in spawn, expose as MCP tools (read + set), persist back via callback. Drop the result-passing MCP callback. Surface `userdata` opaquely as before.
  - `http-node`: receive `attributes` in the request body, allow target endpoint to specify how to use them.
- Update conformance suite (`cmd/rimsky-conformance/`) to validate against the new protocol.
- Update `protocol.md` and `executor-author-guide.md`.
- Old protocol path removed; old executors that haven't been updated will fail.

### Commit 5: Omnibus runner

- Supervisor's runner becomes the single configuration-driven flow.
- Branches: has executor → executor RPC; no executor + has claim store → native path; pure-cascade unchanged.
- `claim_resolutions` actions execute on commit / give_up.
- Resumable mode handed through (both store-side and attributes-side).
- Tests exercise "fully loaded" combinations: claim + executor + multi-store + parameterized regions + hold + resolution.

### Commit 6: Cleanup and the explicit break

- Remove `core/resource/` package entirely.
- Reject `owns_resources` / `reads_resources` / `concurrency_tags` at template-deploy with a clear migration error.
- Drop `rimsky_resources`, `rimsky_resource_versions` schema tables.
- Update remaining docs: `architecture.md` (replace §1.2; clean up §3 import rules; update §8 storage tables); `operator-guide.md` (rewrite templates section, monitoring queries); delete `resource-author-guide.md`.
- Move this redesign proposal to `docs/history/` once implementation completes.

After commit 6, the legacy paths are gone and the redesign is canonical.

## 12. Open questions

Remaining unsettled questions worth flagging before implementation. None blocks commit 1.

1. **Multi-store atomic commit.** A node writing to two stores still commits independently per store. Cross-store atomicity is impossible. Acceptable per discussion (same as today, just more honest). Worth a note in `store-author-guide.md`.
2. **Region-overlap detection** for region locks beyond path globs. Trivial for filesystem and S3; harder for SQL predicates. Likely answer: database stores offer table-level or row-id-set locks initially, not arbitrary-predicate regions.
3. **Handle lifetime under async handoff.** `claude-agent` uses `AsyncAccepted` and reports terminal asynchronously. Lock and sidecar must persist across the async period — same treatment as today's dispatch claim.
4. **Store-level invariants on direct-mode stores.** Filesystem direct mode can't reject writes after they happen. Either no commit-time quality rules in direct mode, or kernel-level enforcement (FUSE, AppArmor) — practical answer: skip for v1, document as a known limitation.
5. **Read-region semantics.** `read` declaration is an access grant, not a freshness gate. Dependencies on other nodes provide freshness gating. Worth being explicit in `node-graph-design.md`.
6. **Git store push contention.** Two locks pushing to the same remote branch (rare, since branches are the lock unit) race. Initial answer: fetch+rebase+retry with bounded attempts, then `git_push_contention` error class.
7. **Git store branch GC.** A blessed `rimsky-git-gc` background task pruning stale branches. Designed but not implemented in v1.
8. **Forge API integration for `open-pr` strategy.** Pluggable forge interface; ships with GitHub support first.
9. **Output merging across multiple upstreams** when downstream attributes pull from multiple sources with overlapping field names. Namespacing by upstream node type (`{{deps.<node>.<field>}}`) handles most cases; "whichever is freshest" semantics not supported in v1.
10. **Attribute history beyond last-run.** Last-run-wins. Audit trail via `rimsky_events`. Richer history mechanism deferred.
11. **Visibility-timeout vs. heartbeat alignment** for claim stores. Two abandon-detection mechanisms (rimsky's heartbeat and the store's visibility timeout). Need a clear story about which is authoritative.
12. **Store name resolution across supervisor pools.** A pool's config declares which stores it has access to; dispatch eligibility filters to nodes whose required stores are in the pool. Mirrors `accepted_executors` for executor pools. Mechanically the same.

## 13. What this gives us

- **Templates get shorter and more declarative.** Backends are operator concerns; templates are workflow concerns.
- **The protocol simplifies.** No `result` field, no per-resource userdata interpretation, no MCP callback for committing structured results.
- **Existing tools work unmodified** through the transparency principle. Standard CLIs, ORMs, SDKs operate on handles in their native form.
- **Resumption is first-class** at two layers: store-side (sidecars persist) and attributes-side (executor's structured progress survives).
- **One conflict-management mechanism (locks)** replaces three (resource ownership, concurrency tags, dispatch claim).
- **One per-run typed data table (attributes)** replaces three (inputs, outputs, claim metadata) and removes the executor-side substitution requirement.
- **The "resource" fiction goes away.** Stores admit they're shared backends with regions, locks, and commit semantics.
- **First-class git store** opens up agentic-coding workflow class — branch-per-work-item, automatic PR generation, full history-based restoration.
- **First-class claim stores** (queues, ring buffers, work tables) make worker-pool patterns expressible without ad-hoc machinery.

## 14. Picking up where we left off

When the next session begins, this is the state:

### Settled
- All seven decision points (§0.2) are accepted.
- The template syntax in §9 is canonical for the redesign.
- Branch-as-region for git stores is settled; path-narrowing within branch is not v1.
- Rimsky doesn't enqueue; queues and ring buffers are configurations of one `claim_store` kind.
- All lock state lives in postgres (`rimsky_lock_holders`); hybrid eligibility check.
- Attributes replace outputs / inputs / claim metadata; rimsky owns all `{{...}}` substitution; userdata is truly opaque.
- Configuration-driven node shape uniformity; one omnibus runner.
- Multi-claim from v1; hold + claim_resolutions for long-lived claims; first-delete-wins, last-released-wins.

### Start here
- **Commit 1 first.** New schema, new `core/store/` package, filesystem direct-mode, claim_store. Additive only. Get the foundation in place; everything else depends on it.
- The dispatch-queue lock-eligibility predicate (§10) is the most architecturally significant piece and is worked through in detail. Implementation should follow that section closely.
- **Validation target**: a non-trivial real workload running end-to-end against a deployed rimsky stack. The redesign has succeeded when a real consumer pipeline — not the contrived test harness — ships on it.

### Watch out for
- The transparency principle is the doc's load-bearing claim. If executors end up needing special "rimsky-store" code, something is wrong — back up and reconsider.
- Lock-state-in-postgres is uniform; resist temptation to push state back into stores for "performance" before measuring.
- Userdata-is-opaque is the principle that lets executors stay simple. Don't slip back into rimsky-substituting userdata; if a use case seems to need it, the answer is probably an attribute, not a userdata template.

### What's deliberately not in v1
- Sidecar / versioned modes for filesystem (only direct mode in v1).
- S3 store (after foundation lands).
- Git store (after foundation lands; design is in §3.4).
- Forge API integration for `open-pr`.
- Branch GC for git store.
- Multi-store atomic commit.
- Path-narrowing-within-branch for git regions.
- Bulk enqueue, priority/visibility metadata for claim stores.
- Output merging across upstreams beyond namespacing.

## 15. Implementation prerequisites

Concrete artifacts to have in place before commit 1 begins:

1. **Updated proto file** — `proto/v1/node_executor.proto` draft with the new ExecuteRequest shape (lands in commit 4 but worth drafting alongside commit 1 to keep the target visible).
2. **Schema migration files** — `core/migrations/` numbered files for new tables (`rimsky_stores`, `rimsky_lock_holders`, `rimsky_node_attributes`, `rimsky_claim_holders`).
3. **`store-author-guide.md` skeleton** — at least the interface contract and a worked example (the filesystem direct-mode store) so external implementers have a template.
4. **Scenario test scaffolding** — new buckets for `test/scenarios/stores/`, `test/scenarios/locks/`, `test/scenarios/attributes/`, `test/scenarios/claim_stores/`. Each starts empty; later commits add to the relevant bucket.
5. **Conformance suite update plan** — a writeup of what new conformance cases need to exist (lands with commit 4 protocol changes, but worth thinking through alongside commit 1).
6. **Documentation map** — explicit list of which doc sections move/change in which commit. Commit 1 owns the new `store-author-guide.md`; commit 3 owns `node-graph-design.md` updates; commit 4 owns `protocol.md` and `executor-author-guide.md`; commit 6 owns `architecture.md` cleanup, `operator-guide.md` rewrite, and `resource-author-guide.md` deletion.

This is the design. Implementation can begin.
