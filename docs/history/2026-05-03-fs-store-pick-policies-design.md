# Standard filesystem store: pick-policy support

Date: 2026-05-03
Status: design

## Position relative to existing specs

This spec **extends** the standard filesystem store-service described in the
v3 stores-redesign spec (`docs/specs/2026-04-27-stores-redesign-v3-design.md`,
filesystem-store discussion around line 375). It is **not** an amendment
overlay (contrast with `docs/specs/2026-04-30-stores-protocol-cleanup-design.md`,
which amended specific v3 sections).

- The v3 spec describes the standard filesystem store as "concrete paths
  only, no globs." That phrase governs the **selector grammar**: rimsky
  passes selectors through to the store unparsed, and the store rejects
  any selector containing glob metacharacters. Auto-discovery in this
  spec runs entirely store-internally (the store reads the filesystem to
  enumerate its own queue contents); selectors that arrive at `Open` are
  still concrete — `@policy-name` keys (looked up in the configured
  policy table) or concrete relative paths (the existing regional path).
  No selector-grammar relaxation.
- This spec adds pick-policy support to the standard filesystem store-
  service alongside the existing regional path. When no `pick_policies`
  block is configured, behavior is identical to today's v3 standard.
- No v3 protocol surface is altered. No blessed invariant changes.
  `Capabilities()` continues to report `WriteSemantics: direct`. The four
  runtime verbs (`Open` / `Commit` / `Abandon` / `Release`) keep identical
  signatures and semantics. The startup-handshake `Capabilities()` verb is
  unchanged.
- Parallel structure to how the standard postgres store-service has always
  supported both regional and pick-policy modes — the standard fs store
  gains mode parity.

No rimsky-side code change is required. The supervisor's acquisition flow
already handles `OpenOutcome{Available: false}` (per v3 spec §4.7) and
already routes terminal verbs by claim_id; the new fs pick-policy path
just produces those outcomes through different store-internal logic.

## Motivation

The standard postgres store ships with pick-policies (items-table queues
keyed by `@policy-name` selectors). The standard filesystem store today is
regional-only — every selector resolves to a concrete path under the
configured root. Operators who want a queue or ring over a set of folders
must compose two stores (postgres for the queue, filesystem for the per-
folder region lock), even when the entire workload is filesystem-native
and pulling in postgres adds operational weight without fitting the use
case.

Common workloads that want a fs-native pick policy:

- **Document-rotation rings** — a set of folders under review, each cycled
  through periodically.
- **Drop-and-process queues** — operator drops a folder under a watched
  root; a workflow picks it up, processes it, deletes it.
- **Round-robin partitions** — N partitions under a watched root, M
  workers each picking a partition for exclusive work.

This spec adds the minimum surface to cover these patterns natively in
the standard fs store.

## Scope

| Element | v1 |
|---|---|
| Pick-policy actions on `on_commit_default` / `on_give_up_default` | `release_to_back`, `release_to_head`, `delete` |
| Auto-discovery of folders by reading the configured sub-root | yes |
| Items-insertion admin endpoint | not shipped (auto-discovery covers it) |
| Bump-to-head admin endpoint | shipped |
| Sync strategy | configurable: `on_open` (default) or `on_sweep` |
| Folder pattern filter | hardcoded skip-leading-dot + optional regex |
| `delete` semantics | `os.RemoveAll` of the underlying folder |
| Visibility-timeout sweep | shipped (parity with pg) |
| Lifecycle event handlers | no-ops (no per-template/per-instance state) |
| Multiple pick policies in one store-service | yes (each has its own sub-root + state directory) |

## Config schema

Operator-managed in the store-service's own config file (typically
`store-filesystem.yml`). Rimsky neither defines nor validates this file
(per v3 §6.3 — store-services own their own config schema).

```yaml
# store-filesystem.yml
root: /workspace                          # store main root (existing field)
write_semantics: direct                   # existing field; unchanged

pick_policies:
  "@docs-ring":
    root: documents                       # required when ≥1 policy is configured;
                                          # relative path under main root.
                                          # auto-discovers /workspace/documents/*
                                          # as queue items.
    folder_pattern: "^[a-z][a-z0-9-]*$"   # optional regex; default is "any name
                                          # not starting with `.`". Always-skip-
                                          # leading-dot is hardcoded in addition
                                          # to this regex.
    on_commit_default: release_to_back    # required. release_to_back |
                                          # release_to_head | delete
    on_give_up_default: release_to_back   # required. same vocabulary
    visibility_timeout_seconds: 1800      # required. > 5 × heartbeat_interval;
                                          # size to worst-case workflow duration.
    sync_strategy: on_open                # optional. on_open (default) | on_sweep

  "@reports-ring":
    root: reports/daily
    on_commit_default: release_to_back
    on_give_up_default: release_to_back
    visibility_timeout_seconds: 600

# Listen addresses (existing).
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
admin_port: 9120                          # required when any pick policy is
                                          # configured; unused otherwise.
sweep_interval_seconds: 60
```

### Validation at startup

Per policy:

- `root:` is a valid relative path (no `..`, not absolute).
- `<store-root>/<policy.root>/` exists, is a directory, is readable + writable
  by the store-service uid.
- `folder_pattern` (if present) compiles as a Go `regexp`.
- `on_commit_default`, `on_give_up_default` ∈ {`release_to_back`,
  `release_to_head`, `delete`}.
- `visibility_timeout_seconds` > 0.
- `sync_strategy` ∈ {`on_open`, `on_sweep`} or absent (defaults to `on_open`).

Two policies' `root:` paths are permitted to overlap. The cross-queue
concurrency story (see "Region-byte consistency" below) keeps overlap safe
at the cost of conflict-induced wasted cycles when both queues race for the
same folder; this is documented behavior, not a configuration error.

### Idempotent setup

At startup the store creates `<store-root>/.fs-store/<policy>/available/`
and `<store-root>/.fs-store/<policy>/in_progress/` per configured policy
via `os.MkdirAll`. Idempotent — harmless if directories already exist.

## Directory layout

State always lives at the main store root, namespaced by policy. Folders
live at the per-policy sub-root. Single state location regardless of how
many policies are configured or where their sub-roots are.

```
<store-root>/
├── .fs-store/                                     # all queue state
│   ├── docs-ring/
│   │   ├── available/
│   │   │   ├── area-a                             # mtime = queue position
│   │   │   ├── area-c
│   │   │   └── area-d
│   │   └── in_progress/
│   │       └── area-b.<claim_id>.<claimed_nanos>
│   └── reports-ring/
│       ├── available/
│       └── in_progress/
├── documents/                                     # docs-ring sub-root
│   ├── area-a/                                    # actual folders
│   ├── area-b/
│   ├── area-c/
│   └── area-d/
└── reports/
    └── daily/
        ├── 2026-05-01/
        ├── 2026-05-02/
        └── 2026-05-03/
```

### Sentinel filename grammar

- Available: `available/<folder>` — folder name verbatim.
- In-progress: `in_progress/<folder>.<claim_id>.<claimed_nanos>`.

In-progress filenames are parsed **from the right**: the rightmost `.`
separates `<claimed_nanos>` (digits only); the next `.` separates
`<claim_id>`; everything before is the folder name. This handles dotted
folder names (`my.docs`) without escaping.

**Dependency on rimsky-supplied claim_id format.** The parse-from-right
rule assumes `claim_id` contains no `.` characters. Today this holds:
`core/store/types.go` types `ClaimID` as a string, and the supervisor's
acquisition path generates the value via `uuid.New().String()` (see
`runner_acquire.go::acquireClaim`), which produces hyphen-separated
hex with no dots. If the rimsky-side claim_id format ever changes to
include `.`, the standard fs store implementation must add escaping in
the sentinel filename (e.g., `<folder>|<claim_id>|<claimed_nanos>` with
`|` as the separator). Implementations should keep the separator
choice localized to one place so a future change is mechanical.

Folder names with characters that filtering does not exclude (spaces,
unicode, etc.) are stored verbatim in sentinel filenames. The kernel
handles them; readers are not expected to parse the available-side
filenames as anything other than opaque identifiers (the file content is
not consulted).

## Algorithms

### Sync step

Reconciles the available-sentinel set against the on-disk folder set. Runs
at the start of every `Open` (when `sync_strategy: on_open`) or on every
sweep tick (when `sync_strategy: on_sweep`).

```
extant = readdir(<store-root>/<policy.root>/) filtered by:
         - not starting with `.`
         - matching policy.folder_pattern (if configured)
         - is a directory (lstat-confirmed)

available = readdir(<store-root>/.fs-store/<policy>/available/)

in_progress_folders = parse_folder_from_right(entry) for each entry in
                      readdir(<store-root>/.fs-store/<policy>/in_progress/)

tracked = available ∪ in_progress_folders

# Add brand-new folders.
for folder in extant - tracked:
    open(available/<folder>, O_CREAT|O_EXCL, 0644)
    # EEXIST means concurrent sync added it; ignore.

# Remove stale: folder gone from disk but still has an available sentinel.
for folder in available - extant:
    unlink(available/<folder>)
    # ENOENT means a concurrent pick claimed it; ignore.
```

In-progress entries for vanished folders are deliberately not cleaned up
in sync — the workflow will fail when the executor tries to read the
missing path, the failure flows through `on_give_up_default`, and the
sentinel resolves naturally. Cleaning them mid-claim would race with
terminal handlers.

### Open / pick algorithm

```
Open(claim_id, spec):
    if pp, ok := configured_pick_policies[spec.Selector]; ok:
        return openPickPolicy(claim_id, pp)
    return openRegional(spec.Selector)        # existing v3 path; unchanged

openPickPolicy(claim_id, policy):
    if policy.sync_strategy == on_open:
        run_sync(policy)

    entries = readdir(<store-root>/.fs-store/<policy>/available/)
    sort entries by:
        primary:   mtime ascending
        secondary: lexical (filename) ascending  # tiebreaker for bulk-add

    for entry in entries:
        new_name = "<entry>.<claim_id>.<now_nanos>"
        err = rename(available/<entry>, in_progress/<new_name>)
        if err == ENOENT:
            continue                  # another supervisor took it
        if err != nil:
            return err

        folder = entry
        sub_path = filepath.Join(policy.root, folder)
        abs_path = filepath.Join(<store-root>, sub_path)
        return OpenOutcome{
            Available: true,
            Result: ClaimResult{
                Address: json.Marshal(abs_path),
                Region:  json.Marshal(sub_path),
                Payload: json.Marshal({"folder": folder}),
            },
        }

    # Available exhausted — nothing to give right now.
    return OpenOutcome{Available: false}
```

The rename is the atomic claim. POSIX guarantees that two concurrent
`rename(src, dst)` calls with the same `src` produce exactly one success
and one ENOENT — no flock or other coordination needed.

### Commit / Abandon / action routing

```
Commit(claim_id, region, address):
    return applyPickAction(claim_id, on_commit_default)

Abandon(claim_id, region, address):
    return applyPickAction(claim_id, on_give_up_default)

applyPickAction(claim_id, action):
    (policy, entry, folder) = find_by_claim_id(claim_id)
    if policy == nil:
        return nil                    # no in_progress entry; idempotent no-op

    switch action:
    case release_to_back:
        utimensat(in_progress/<entry>, now)
        rename(in_progress/<entry>, available/<folder>)
            # ENOENT → no-op success (sweep or another terminal beat us)

    case release_to_head:
        utimensat(in_progress/<entry>, time(0))
        rename(in_progress/<entry>, available/<folder>)
            # ENOENT → no-op success

    case delete:
        os.RemoveAll(<store-root>/<policy.root>/<folder>)
        unlink(in_progress/<entry>)
            # ENOENT → no-op success
```

`find_by_claim_id` linearly scans every configured policy's `in_progress/`
looking for `*.<claim_id>.*`. The protocol carries no policy hint with
the claim_id, so the store has no faster lookup. Claim_ids are rimsky-
supplied UUIDs unique across all acquisitions; in correct operation at
most one policy's `in_progress/` will contain a match. The implementation
returns the first match found and is not required to detect or report
duplicates (mirrors the pg store's `findPolicyForClaim` at
`stores/postgres/store/store.go:315`). Mirrors the pg store's deliberate
choice not to maintain an in-memory map (`stores/postgres/store/store.go`
comment: "An earlier draft kept a claim_id → item_id map but no consumer
ever read it; removed to eliminate drift"). For workloads with a small
in-flight set (the typical case), this is trivial; at very high concurrency
operators should partition into multiple smaller policies.

`Release` for direct-mode fs is a no-op (existing v3 behavior; no read
state to tear down). Pick-policy claims do not register read state at
Open, so the no-op continues to apply.

### Claimant-guard and idempotency

Between `find_by_claim_id` resolving the entry name and the action handler
firing, a sweep tick or a duplicated terminal call could move the entry.
Every action handler treats `ENOENT` on its primary mutation (the rename
or the unlink) as success. This is the fs equivalent of pg's
"`AND claim_token = $1` filter, zero rows affected on mismatch":
duplicated terminal RPCs and races with visibility-timeout reclamation are
both benign.

For the `delete` action specifically, the order is **`RemoveAll` first,
then `unlink`**. Rationale:

- If `RemoveAll` succeeds and `unlink` fails (rare; ENOSPC, permission), the
  in_progress sentinel persists. Visibility-timeout eventually reclaims
  it back to `available/`. On the next sync, the folder is in
  `available - extant` (sentinel exists, folder gone), so sync unlinks
  the orphan sentinel. Net recovery: one wasted visibility-timeout
  window, then quiet cleanup. Eventually consistent.
- If we had ordered them as `unlink` first and `RemoveAll` failed, the
  sentinel would be gone but the folder would still be on disk. The next
  sync would re-add the folder to the queue, and the failed-deletion folder
  would get re-processed indefinitely. Worse failure mode.

### Sweep loop

```
every sweep_interval_seconds:
    for each policy:
        if policy.sync_strategy == on_sweep:
            run_sync(policy)
        for entry in readdir(.fs-store/<policy>/in_progress/):
            (folder, claim_id, claimed_nanos) = parse_from_right(entry)
            if (now - claimed_nanos) > policy.visibility_timeout:
                rename(in_progress/<entry>, available/<folder>)
                    # ENOENT → another tick or terminal beat us; ignore
```

The sweep is purely store-internal — it does not consult `rimsky_lock_holders`
or fire any rimsky-side bookkeeping. Per v3 spec §7.5, the rimsky-side
orphan reaper runs independently and is the canonical authority for "is
anyone holding lock X."

## Region-byte consistency

Pick-policy claim on folder F under policy with `root: <sub-root>` produces:

```
Region = json.Marshal("<sub-root>/<F>")
```

Regional claim with selector `<sub-root>/<F>` produces (via existing path-
cleaning in `openRegional`):

```
Region = json.Marshal("<sub-root>/<F>")
```

These are **byte-equal**, which makes the rimsky-side conflict predicate
in `rimsky_lock_holders` (byte-equal `region_data`) detect overlap across:

1. **Two pick-policy claims on the same folder** (two policies with overlapping
   roots, both auto-discover the folder, both attempt to pick it). The
   second-acquiring supervisor's lock-holder INSERT conflicts; the
   acquisition tx rolls back; the supervisor compensates by calling
   `Abandon`, which routes through `on_give_up_default`. For a ring policy
   (`release_to_back`), the second claim's sentinel cycles to the tail of
   its own queue. The conflict is observable to operators only as a
   wasted-cycle in the metrics; correctness is preserved.

2. **Pick-policy claim followed by a regional claim on the same folder**.
   The regional claim's lock-holder INSERT conflicts byte-equal; the
   regional supervisor blocks until the pick-policy claim terminates.

3. **Two regional claims on the same folder** (existing v3 behavior;
   unchanged).

This invariant is load-bearing for the cross-queue and queue-vs-regional
concurrency stories. Implementation must preserve byte-equal canonicalization
between the two paths.

## Admin endpoint: bump-to-head

URL pattern follows the pg store's v3 admin convention (`/admin/<verb>/{selector}`,
per `stores/postgres/store/admin.go` route `/admin/items/{selector}`):

```
POST /admin/bump-to-head/{selector}
Content-Type: application/json
Body: {"folder": "<folder-name>"}
```

Selector path-param accepts the raw `@policy-name` form or its
percent-encoded `%40policy-name` form.

Responses:

| Code | Meaning |
|---|---|
| 204 No Content | bumped successfully |
| 400 Bad Request | invalid body, unknown selector, folder name violates pattern |
| 404 Not Found | folder isn't currently in this policy's available/ set |
| 409 Conflict | folder is currently in_progress (can't bump while held) |
| 500 Internal Server Error | filesystem error |

Implementation:

1. Selector must match a configured policy.
2. Folder name must match `policy.folder_pattern` and not start with `.`.
3. `<store-root>/<policy.root>/<folder>` must exist as a directory (else 404).
4. Attempt `utimensat(available/<folder>, time(0))` to set the mtime to
   epoch so the sentinel sorts to the head on the next pick. On success
   → return 204.
5. If `utimensat` returns ENOENT, the available sentinel isn't there.
   Two coherent reasons: a concurrent pick just claimed it (in_progress
   now has the entry), or sync hasn't enqueued the folder yet. Resolve
   by checking `in_progress/<folder>.*.*`:
   - present → 409 Conflict (folder is held)
   - absent → 404 Not Found (folder exists on disk but sync hasn't
     picked it up yet; operator can retry)

The single-mutation design — `utimensat` first, recheck `in_progress/`
only on ENOENT — eliminates pre-mutation existence checks and their
attendant race windows. Whether a concurrent pick fired before, during,
or after the `utimensat` call, the post-ENOENT recheck of `in_progress/`
correctly distinguishes "raced and now held" (409) from "genuinely not
in queue" (404). No code path returns 500 for a concurrent-pick race.

If `utimensat` fires before a concurrent pick, the next pick reads the
bumped mtime and sorts the folder to the head — bump succeeded, return
204 stands.

## Lifecycle event handlers

The standard fs store implements all six v1 control-plane lifecycle methods
(per `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`
§4.1):

- `OnTemplateRegistered`
- `OnTemplateDeployed`
- `OnTemplateUndeployed`
- `OnTemplateDeregistered`
- `OnInstanceCreated`
- `OnInstanceTerminated`

The fs store has no per-template or per-instance state — pick-policy state
is keyed solely by configured policy name, decoupled from template/instance
identity. All six handlers return `nil` immediately. Mirrors today's
regional-only fs store behavior; no change.

## Crash recovery state-transition table

| Crash point | On-disk state | Recovery |
|---|---|---|
| Mid-sync (some `O_CREAT`s done, some not) | Partial available sentinels | Next sync picks up; `O_CREAT \| O_EXCL` is idempotent. |
| After `rename(available → in_progress)`, before rimsky-side `rimsky_lock_holders` INSERT commits | in_progress entry exists; no rimsky lock-holder row | Visibility-timeout sweep reclaims back to available. |
| After lock-holder INSERT, before executor runs | in_progress + lock-holder row | Rimsky-side orphan reaper drops the lock-holder if the supervisor dies; supervisor's bookkeeping calls `Abandon` → `on_give_up_default`. |
| `delete` mid-flight: after `os.RemoveAll`, before `unlink(in_progress sentinel)` | Folder gone; sentinel persists | Visibility-timeout reclaims sentinel back to available; next sync notices folder is gone and unlinks the available sentinel. Eventually consistent. |
| Two sweeps concurrently see the same expired in_progress entry | Both attempt the same rename | First wins; second gets ENOENT; no-op. |
| Two terminal calls with the same claim_id race | Both find the same sentinel; both attempt rename/unlink | First wins; second gets ENOENT; no-op. |
| Concurrent sync + concurrent terminal | Sync touches available/, terminal touches in_progress/ | Different directories, different files; no contention. |

## Operational constraints

- **Visibility-timeout sizing.** `visibility_timeout > 5 × heartbeat_interval`
  AND longer than the worst-case workflow duration (including all
  inheriting nodes in the holding subgraph). If the sweep reclaims a
  sentinel while the supervisor still believes it holds the claim, the
  next pick attempt will conflict via byte-equal region match in
  `rimsky_lock_holders` (the rimsky-side row hasn't expired yet) and the
  second supervisor will `Abandon`-recycle. Self-healing but wastes a cycle.

- **Sub-root stability.** Operators changing a policy's `root:` config requires
  restarting the store-service. The state directory at `<store-root>/.fs-store/
  <policy>/` survives the restart; the sync step at next startup reconciles
  the available set against the (new) sub-root's folder list.

- **Filesystem.** Designed for local POSIX filesystems with reliable rename
  atomicity. NFSv4 is supported in practice; NFSv3 has known fuzzy rename
  semantics under heavy concurrency and is not recommended. SMB is not
  supported.

- **Folder-name charset.** No restrictions beyond the kernel's (no `/`, no
  `\0`). Names with `.` are handled by the parse-from-right rule. Names
  with newlines or other control characters are technically supported by
  the kernel and the rename-based algorithm but should be avoided by
  operator convention.

- **Scale.** `readdir` cost is O(N) per sync; `find_by_claim_id` cost is
  O(P × I) where P = policies and I = in-flight per policy. For ring
  workloads at human cadence this is trivial. At >10k folders per policy
  on slow storage, switch `sync_strategy` to `on_sweep` to amortize the
  readdir cost; partition into multiple smaller policies if a single
  queue's cardinality keeps growing.

## Testing strategy

| Layer | Coverage | Location |
|---|---|---|
| Unit | Sync round-trip (folders added/removed, sentinels reconcile correctly) | `stores/filesystem/store/store_test.go` |
| Unit | Concurrent pick: M goroutines × N folders, exactly N successes, M−N `Unavailable` | same |
| Unit | Each action handler (`release_to_back`, `release_to_head`, `delete`) — verify on-disk state after terminal | same |
| Unit | Visibility-timeout sweep reclaims expired in_progress sentinel | same |
| Unit | Terminal idempotency: duplicate Commit with same claim_id is a no-op | same |
| Unit | Selector dispatch: `@policy` keys hit pick-policy path; non-`@` selectors hit regional path | same |
| Unit | Region-byte equality: pick-policy region for folder F == regional region for path `<sub-root>/F` | same |
| Unit | Folder pattern filtering: matching + non-matching folders dropped under root, only matching get sentinels | same |
| Unit | Multi-policy with overlapping roots and disjoint patterns: each policy has its own state, no cross-talk | same |
| Unit | Bump-to-head: insert N folders, bump one, next pick selects the bumped folder | same |
| Integration | End-to-end through gRPC: loopback fs store-service via `stores/filesystem/testfixture`, supervisor acquires `@policy` via wire, executor runs, terminal fires, queue cycles | new file under `test/scenarios/stores/` |
| Integration | Cross-queue concurrency: two pick-policy claims on overlapping folders; loser conflicts via region match and recycles | same |
| Integration | Pick-vs-regional concurrency: pick-policy claim on F holds; regional claim on F blocks until commit | same |

The "M goroutines × N folders" unit test is the reference test for rename-
as-claim atomicity. Run with `-race` and `-count=10` to catch any subtle
ordering issues that escape a single run. Same shape as the existing pg
store's concurrent-pick test, just with rename in place of
`FOR UPDATE SKIP LOCKED`.

## Documentation updates that ship with the implementation

- `docs/operator-guide.md`: new "fs store pick policies" subsection paralleling
  the existing pg pick-policy section. Show the YAML, the directory layout,
  the operational constraints.
- `docs/glossary.md`: extend the pick-policy entry to note fs supports pick
  policies in addition to pg; extend `release_to_back` / `release_to_head` /
  `delete` action entries to note fs as a supporting store.
- `CHANGELOG.md`: entry under Unreleased describing the new pick-policy
  support and the auto-discovery semantics.
- `docs/store-author-guide.md`: no change required (pick policies are
  store-internal vocabulary; the author guide does not standardize them).

## Future extensions (deliberately not in v1)

- **`move_to: <path>` action.** A non-destructive alternative to `delete`
  that relocates the folder instead of removing it. `trash` (move to
  `.fs-store/trash/<timestamp>-<folder>`), `archive` (move to a
  timestamp-prefixed archive area), and similar patterns become special
  cases of `move_to`. Adds an action variant; trivial implementation.

- **Items-insertion admin endpoint.** Operator force-adds a folder to the
  queue independent of disk state. v1 explicitly does not ship this because
  auto-discovery covers the realistic insertion paths. If a use case appears
  for "queue this folder before it exists on disk," the endpoint can be
  added as a parallel surface to the pg store's existing items endpoint.

- **mtime-based sync short-circuit.** A third `sync_strategy` value
  (`hybrid`) that stats the watched root and skips the readdir when the
  root's mtime hasn't changed since last sync. Adds a stat per Open at
  the cost of relying on directory-mtime semantics that vary across
  filesystems (Linux ext4: reliable; NFS / FUSE: variable). Defer until
  someone has a workload where the readdir cost matters and the mtime
  semantics on their filesystem are trustworthy.

- **Per-policy concurrency limits.** A policy-level cap on simultaneous
  in-flight claims (similar to a counting semaphore). Today, this is
  expressible at the rimsky template layer via named locks; the store
  doesn't need to enforce it.

- **Priority field on folders.** A separate sort key beyond mtime (e.g.,
  a `<priority-int>` prefix in the sentinel filename, sorted before
  mtime). Today, `release_to_head` / `bump_to_head` cover urgent-folder
  cases without a separate priority axis. Add only if a real workload
  needs both stable mtime ordering and priority overrides.

## Decisions log

Locked during the brainstorm pass that produced this spec:

- **Rename-based atomic claim, not flock.** POSIX rename is reliable across
  more filesystems and adds no contention point.
- **State at the main root** (`<store-root>/.fs-store/<policy>/`), not per
  sub-root. Single state location per store-service, easier to audit
  and back up separately from data.
- **Region bytes pinned byte-equal between pick-policy and regional paths.**
  Load-bearing for cross-queue and queue-vs-regional concurrency stories.
- **Three actions** (`release_to_back`, `release_to_head`, `delete`). The
  action vocabulary matches the pg store; the per-action **mechanics**
  differ where the underlying primitive does. Specifically:
  - `release_to_back`: pg bumps the items-table `sequence` via
    `nextval`; fs sets the sentinel's mtime to `now`. Both push to the
    tail of their respective sort orders.
  - `release_to_head`: pg increments the items-table `priority` by 1
    (relative-to-current-priority head bump within the priority axis);
    fs sets the sentinel's mtime to `time(0)` (absolute head). The fs
    semantic is strictly stronger — a head-bumped fs sentinel sorts
    ahead of every non-head-bumped sibling, whereas the pg version
    only outranks siblings at the previous priority level. Operator-
    guide language must distinguish the two.
  - `delete`: pg runs `DELETE FROM <items_table>`; fs runs
    `os.RemoveAll(<store-root>/<sub-root>/<folder>)`. Both consume
    the queued item.
- **No items-insertion endpoint.** Auto-discovery is the insertion mechanism.
- **`sync_strategy` configurable per-policy** with `on_open` default.
- **Multi-policy supported** with per-policy sub-roots; overlapping roots
  permitted (with the documented wasted-cycle cost on conflict).
- **Lifecycle handlers all return nil.** No per-template/per-instance state.
- **Spec is an extension of v3, not an amendment overlay.** No v3 protocol
  surface or invariant changes.
