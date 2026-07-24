# Stores: Pick-Policy Action Vocabulary v2 + fs-store `sync_strategy: on_drain` — Design Spec

**Date:** 2026-05-06
**Status:** Spec (approved by user; awaiting implementation plan)
**Predecessor sketch:** `.ok-planner/sketches/2026-05-06-fs-store-refresh-on-drain-sketch.md`

## 1. Goal

Replace the existing pick-policy commit/give-up action vocabulary with a
factored four-action set across **both** the bundled filesystem store
(`stores/filesystem/`) and the bundled postgres store
(`stores/postgres/`). Each store supports the subset of the action
vocabulary that's meaningful for its underlying mechanism. Additionally,
extend the filesystem store's `sync_strategy` with an `on_drain` value
that enables single-pass-then-refresh queue-mode workflows.

All changes are pure store-side; no `claim_producer.proto` change, no
Rimsky-core change.

The filesystem-store work unblocks **queue mode** for the docs-corpus
rimsky-pipeline sketch (`.../verantel/.ok-planner/sketches/2026-05-05-docs-corpus-rimsky-pipeline-sketch.md`)
and cleans up the action-config surface generally. Postgres-store
parity ensures the two bundled stores expose a consistent v2
vocabulary so operators don't need to learn two different sets of
action names. Composes with the reactive-loops + lifecycle-handlers
work shipped 2026-05-05 (commit `712f83a`).

## 2. Motivation

### 2.1 The conflated commit-action axis

Today's `OnCommitDefault` and `OnGiveUpDefault` fields take values from a
small enum (`release_to_back | release_to_head | delete`) that conflates
two independent dimensions:

1. **Queue-entry fate** — does the slot in the queue stay or go?
2. **Folder fate** — what happens to the folder on disk?

The existing vocabulary covers two of the four meaningful combinations:

- `release_to_back`: slot stays, folder stays
- `delete`: slot goes, folder goes

But it has no name for "slot goes, folder stays" — which is the action
the docs-pipeline actually wants. The previously-proposed
`delete_strategy: move | trash | remove` was an attempt to patch over
this by making `delete` configurable, producing a two-axis config where
one axis is conditional on the other. The named-action vocabulary
collapses both axes into a single field with four legal values that map
1:1 to the meaningful (entry × folder) combinations.

`release_to_head` (slot returns to front of queue, folder stays) is
unused in any in-tree config; it has the LIFO infinite-loop footgun
shape and we drop it.

### 2.2 The drain-handoff problem

Today's pick-policy returns `Unavailable` when the queue empties and
keeps returning `Unavailable` indefinitely. For queue-mode workflows
("process each item once, then terminate") the operator must manually
intervene between drains to re-prime the queue.

A `sync_strategy: on_drain` value resolves this by syncing only at
drain-pass boundaries (signaled by an internal `drained` sentinel),
producing exactly one `Unavailable` per drain pass and auto-refreshing
the queue on the next `Open` after that.

### 2.3 Why these belong in one spec

The vocabulary change is coupled across stores by parity: rolling out
the v2 vocabulary on the filesystem store while leaving the postgres
store on `release_to_back | delete | release_to_head` would expose
two different action vocabularies in the same deployment. Operators
configuring multiple stores would have to memorize the divergence.
The migration discipline (pre-v1 break-cleanly) is identical for
both, so doing them together is cheaper than two staggered cycles.

The fs-store `sync_strategy` and `drained` mechanism are coupled to
the v2 vocabulary by validator rules: `pop` requires
`sync_strategy != on_open` (else popped folders get re-added by sync
within a drain pass), and the natural pairing for queue mode is
`pop + sync_strategy: on_drain`. Specifying these separately would
require a temporary intermediate config shape that doesn't make sense.

## 3. Action vocabulary

### 3.1 The four actions

| Action | Queue entry fate | Underlying-resource fate |
|---|---|---|
| `pop` | consumed | kept in place (fs: folder stays on disk; pg: NOT SUPPORTED — see §3.2) |
| `pop_and_move` | consumed | renamed (fs: `os.Rename` to a configured target root; pg: NOT SUPPORTED) |
| `pop_and_delete` | consumed | destroyed (fs: `os.RemoveAll`; pg: row deleted — semantically equivalent to `pop` for pg, see §3.2) |
| `recycle` | returned to queue tail | kept in place |

Each action is identified by its name string in YAML. `pop_and_move` is
the only parameterized action; it carries a target-root path inline.

### 3.2 Per-store action support

Different stores implement different subsets of the vocabulary because
their underlying mechanics differ.

#### Filesystem store

The fs-store treats the queue entry (sentinel file under
`.fs-store/<policy>/available/`) and the underlying resource (the
folder under `<policy.root>/`) as separate concerns. All four actions
are meaningful and supported.

| fs-store support | Action |
|---|---|
| ✓ | `pop` |
| ✓ | `pop_and_move` (requires inline `target` path; see §3.5) |
| ✓ | `pop_and_delete` |
| ✓ | `recycle` |

#### Postgres store

The pg-store's items table holds rows that are simultaneously the
queue entry AND the underlying resource. There's no separate folder
to move/delete independently. Only the actions whose semantic
distinguishes "queue entry consumed" from "queue entry recycled"
apply.

| pg-store support | Action |
|---|---|
| ✓ | `pop` (deletes the row from the items table; the row IS the resource) |
| ✗ | `pop_and_move` — REJECTED at config-load. No folder concept. |
| ✗ | `pop_and_delete` — REJECTED at config-load. Equivalent to `pop` for pg; the redundancy would mislead. |
| ✓ | `recycle` (re-marks the row available; today's `release_to_back`) |

The pg-store validator surfaces a clear error when an unsupported
action is configured: `"postgres store: pick_policies[<sel>]: action
<name> not supported by postgres store; supported actions are pop,
recycle"`.

### 3.3 Identical vocabulary for both `on_commit` and `on_give_up`

Both fields take values from the same action set (subject to the
per-store support matrix in §3.2). The two fields are configured
independently — operators can mix-and-match (e.g., `on_commit: pop`
for success, `on_give_up: pop_and_move` for failures to a triage
area, on fs-store).

### 3.4 Both fields are required

No defaults. Configs that omit either field are rejected at
config-load with a clear error message. Pre-v1 cleanup discipline:
the actions differ enough in observable behavior that picking one
without thinking is a config bug. The validator forces an explicit
choice.

### 3.5 YAML shape: inline parameterized action

Bare strings for non-parameterized actions; one-key map for
`pop_and_move` with its target root:

```yaml
pick_policies:
  "@docs":
    root: guidance
    folder_pattern: "^[a-z][a-z0-9_-]*$"
    on_commit:
      pop_and_move: guidance.failed
    on_give_up: recycle
    sync_strategy: on_drain
    visibility_timeout_seconds: 1800
```

Co-locating action with its parameter eliminates the
"specified action but forgot the target field" failure mode that a
flat-fields shape would create. The YAML grammar is small:

```ebnf
action     = bare_action | parameterized_action
bare_action = "pop" | "pop_and_delete" | "recycle"
parameterized_action = { "pop_and_move": <path> }
```

The parser accepts a string xor a one-key map for `on_commit` and
`on_give_up` fields and rejects all other shapes. Specifically:

- A bare string must be exactly one of `pop`, `pop_and_delete`,
  or `recycle`. Any other string is rejected as an unknown action.
- A one-key map's only legal key is `pop_and_move`; the value must
  be a non-empty string (a relative path resolved per §6.4).
- A null value, a number, an empty map (`{}`), a multi-key map, a
  sequence, a nested map, or any other YAML shape for an `on_commit`
  / `on_give_up` field is rejected with a parse-level error
  identifying the offending field.

## 4. `sync_strategy` enum (filesystem store only)

> **Scope:** This section applies to the filesystem store's pick
> policies only. The postgres store does not have or need a
> `sync_strategy` field — its items table is the source of truth and
> there is no auto-discovery to schedule.

### 4.1 Values

| Value | Sync runs when | Use cases |
|---|---|---|
| `on_open` | every `Open` call | Ring mode (`recycle`) with live folder discovery |
| `on_drain` | only when queue is empty AND `drained` sentinel is absent (start of new pass) | Queue mode (`pop`) with auto-refresh between passes |
| `explicit` | only via admin endpoint trigger | Operator-controlled batch ingest |
| `never` | only at startup (initial population) | Static corpus, no in-flight changes |

### 4.2 Default

If `sync_strategy` is omitted, default to `on_open`. (This matches
today's behavior — empty/missing is treated as `on_open` already.)

## 5. The `drained` sentinel mechanism (filesystem store only)

> **Scope:** This section applies to the filesystem store's pick
> policies only. The postgres store has no analogous mechanism (it
> does not support `sync_strategy: on_drain`).

This section specifies the implementation of `sync_strategy: on_drain`.

### 5.1 State files

Existing per-policy state directory layout under
`<store-root>/.fs-store/<policy>/`:

```
.fs-store/<policy>/
├── available/        # one sentinel file per available item (existing)
├── in_progress/      # one sentinel file per claimed item (existing)
└── drained           # NEW — empty file; presence is the signal
```

`drained` is created and removed by the pick-policy code only. It is
empty (its existence is the signal). It is never touched by the
operator.

### 5.2 When `drained` is written

`drained` is written **only** when the policy's `sync_strategy` is
`on_drain`. Under `on_open`, `explicit`, and `never`, the sentinel is
never created — those strategies do not use the drain-pass-boundary
mechanism, and writing the sentinel under them would be dead state.

Under `sync_strategy: on_drain`: when an `Open` call's
rename-as-claim succeeds AND the resulting `available/` directory is
empty (the claimed item was the last available), the pick-policy
creates `drained` via:

```go
f, err := os.OpenFile(filepath.Join(state, "drained"),
    os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
if err == nil {
    _ = f.Close()
}
// EEXIST is benign: a concurrent Open already wrote it.
```

The "is `available/` empty?" check is performed by reading the
directory after the rename succeeds; this preserves today's
lockless rename-as-claim model.

The other place `drained` may be written is the corpus-empty case in
§5.4 step 3, also under `on_drain` only.

### 5.3 When `drained` is removed

`drained` is removed in three cases. All three apply only when
`sync_strategy: on_drain` (the only strategy that ever writes
`drained` in the first place); under other strategies these code
paths are unreachable.

1. **`Open` returns Unavailable due to drained-state.** When the
   on-drain Open sequence (§5.4 step 1) finds `available/` empty AND
   `drained` present, it removes `drained` and returns Unavailable.
   This is the load-bearing pass-boundary signal.
2. **Visibility-timeout sweep returns items to `available/`.**
   When `sweep.go` reaps a stale `in_progress/` sentinel and returns
   it to `available/`, it also removes `drained` if present. This
   prevents a subsequent on-drain Open from seeing stale-`drained`
   alongside non-empty `available/`.
3. **Defense-in-depth in `runSync`.** Whenever `runSync` adds new
   items to `available/` (the "Add brand-new folders" branch), it
   also removes `drained` if present. Under `on_drain` this is
   redundant with case #1 (which already cleared `drained` before
   `runSync` was called), but it costs one `os.Remove` and prevents
   a stale-`drained` footgun if a future code path calls `runSync`
   from somewhere other than the §5.4 sequence.

Under `on_open`, `explicit`, and `never`, none of these cases apply
(`drained` is never written, so removing it is a no-op).

### 5.4 Open sequence with `sync_strategy: on_drain` and `on_commit: pop`

```
Open call:
  1. If available/ is empty AND drained file is present:
       Remove drained file. Return Unavailable. (Done.)
  2. If available/ is empty AND drained file is absent:
       Run runSync. (Auto-refresh trigger.)
  3. Try claim from available/ (existing rename-as-claim logic).
     - On success: was this the last item in available/?
         - Yes: write drained atomically (O_EXCL).
         - No:  do nothing extra.
       Return Acquired.
     - On empty available/ after sync produced nothing:
       Write drained, return Unavailable.
       (This handles the "corpus is empty" startup case.)
```

### 5.5 Open sequence with `sync_strategy: on_open`

Identical to today's behavior. No `drained` writes or reads occur
under this strategy (per §5.2):

```
Open call:
  1. Run runSync.
  2. Try claim from available/.
     - On success: return Acquired.
     - On empty: return Unavailable.
```

Sync runs every Open and re-discovers folders that haven't been
moved/deleted, so the queue effectively doesn't drain for `recycle`,
`pop_and_move`, or `pop_and_delete`. The `pop` action is rejected
with this strategy (§6.1), so the "queue would never drain because
sync re-adds popped folders" failure mode is impossible.

### 5.6 Open sequence with `sync_strategy: explicit` or `never`

```
Open call:
  1. Try claim from available/.
     - On success: return Acquired.
     - On empty: return Unavailable.
```

No sync trigger. No `drained` writes or reads (per §5.2 these
strategies don't use the sentinel). The queue is whatever was
populated at startup or by the most recent admin trigger
(`explicit`); under `never`, the queue is whatever was populated at
startup and stays that way until the store restarts.

### 5.7 Net effect

Each `sync_strategy: on_drain` drain pass produces exactly one
`Unavailable` per `Open` call:

```
T = drain_passes_completed
N(t) = items in folder root at start of drain pass t
Open calls per pass t = N(t) + 1
                         (N Acquired + 1 Unavailable that clears drained)
```

Between drain passes (after `drained` is consumed), the next `Open`
finds `drained` absent and runs `runSync`, repopulating `available/`
from current folder state. The cycle is stable.

## 6. Validator rules

Each store has its own validator, but the rules below cover both. Each
validator runs at store startup (config-load time) and rejects invalid
configurations with errors that name the offending policy and the
specific issue.

### 6.1 Rejected at config-load (both stores, shared rules)

| Combination | Reason |
|---|---|
| `on_commit` field missing | Required field |
| `on_give_up` field missing | Required field |
| Action name not in `{pop, pop_and_move, pop_and_delete, recycle}` | Unknown action |
| `pop_and_move` value not a one-key map with a string path | Malformed parameterized action |
| `release_to_back`, `release_to_head`, or bare `delete` as an action | Old vocabulary; pre-v1 break-cleanly |
| `OnCommitDefault` / `OnGiveUpDefault` field names | Old field names; pre-v1 break-cleanly |

### 6.1a Rejected at config-load (filesystem-store only)

| Combination | Reason |
|---|---|
| `pop_and_move` target on a different filesystem from `root` | `os.Rename` is not atomic across filesystems |
| `pop + sync_strategy: on_open` | Queue would never drain (sync re-adds popped folders within a pass) |
| `sync_strategy` not in `{on_open, on_drain, explicit, never}` | Includes the dropped `on_sweep` value, which becomes "unknown sync strategy" |

### 6.1b Rejected at config-load (postgres-store only)

| Combination | Reason |
|---|---|
| `pop_and_move` action used | Not supported by postgres store (no folder concept) |
| `pop_and_delete` action used | Not supported by postgres store (semantically equivalent to `pop`; redundant name would mislead) |

### 6.2 Warned at config-load (filesystem-store only)

| Combination | Reason |
|---|---|
| `recycle + sync_strategy: on_drain` | Queue never empties; `on_drain` never fires. Legal but inert. |

Warnings are logged via package-level `slog` at `slog.LevelWarn`.
Config still loads; the store starts.

### 6.2a Validator return type

Both stores' validator implementations return a `ValidationResult`
struct rather than just an `error`. Production callers (the `New`
constructor in each store) consult the struct: errors fail the
constructor; warnings are logged via package-level `slog`. Tests
inspect the returned struct directly to assert on warning content.

```go
type ValidationResult struct {
    Errors   []error
    Warnings []string
}
```

This shape avoids needing an injectable `*slog.Logger` field on the
`Store` struct (neither store has one today), and keeps warnings
testable as data.

### 6.3 Cross-filesystem validation (filesystem-store only)

For `pop_and_move`, the validator must compare the underlying
filesystem device-id of the policy `root` and the action's target
root. Procedure:

1. Resolve both paths to absolute paths via `filepath.Abs`.
2. Resolve any symlinks via `filepath.EvalSymlinks` so device-id
   comparison reflects the actual storage location, not a link's
   parent directory.
3. Call `os.Stat` (which uses `syscall.Stat_t` on POSIX) on each
   resolved path; compare `stat.Sys().(*syscall.Stat_t).Dev`.

If the device IDs differ, fail config-load with:

```
filesystem store: pick policy "@docs": pop_and_move target "guidance.failed"
is on a different filesystem than the policy root "guidance"; os.Rename
across filesystems is not atomic, refusing to load.
```

`EvalSymlinks` is critical: without it, a symlink crossing filesystems
would slip through (the validator would compare the link's containing
directory's device-id, not the actual destination's).

### 6.4 Pop-and-move target path resolution (filesystem-store only)

Target roots are resolved relative to the **store's top-level `root`**
(the same way each policy's `root` field is resolved). For example,
if the store's top-level root is `/var/lib/rimsky-store/content` and a
policy declares `root: guidance` and `pop_and_move: guidance.failed`,
the validator resolves the target as
`/var/lib/rimsky-store/content/guidance.failed` — a sibling of the
policy's `root`, not a subdirectory of it.

The target directory must exist at config-load (validator does
`os.Stat`); the validator does NOT create missing target directories
on the operator's behalf.

## 7. Migration

### 7.1 Pre-v1 break-cleanly discipline

No compat shim, no auto-translate. Configs using the old vocabulary
fail at config-load with a clear error message that names the new
vocabulary mapping. Operators do a one-time YAML rewrite per the
table below.

### 7.2 Mapping table (filesystem store)

| Old | V2 |
|---|---|
| `on_commit_default: release_to_back` | `on_commit: recycle` |
| `on_commit_default: delete` | `on_commit: pop_and_delete` |
| `on_commit_default: release_to_head` | (no longer supported; remove) |
| `on_give_up_default: release_to_back` | `on_give_up: recycle` |
| `on_give_up_default: delete` | `on_give_up: pop_and_delete` |
| `on_give_up_default: release_to_head` | (no longer supported; remove) |

The new actions `pop` and `pop_and_move` have no old-vocabulary
equivalent; operators wanting them write them fresh.

### 7.2a Mapping table (postgres store)

| Old | V2 |
|---|---|
| `on_commit_default: release_to_back` | `on_commit: recycle` |
| `on_commit_default: delete` | `on_commit: pop` (the row is consumed; pg has no separate folder concept) |
| `on_commit_default: release_to_head` | (no longer supported; remove) |
| `on_give_up_default: release_to_back` | `on_give_up: recycle` |
| `on_give_up_default: delete` | `on_give_up: pop` |
| `on_give_up_default: release_to_head` | (no longer supported; remove) |

The fs-store-only actions `pop_and_move` and `pop_and_delete` are not
in the postgres mapping table — the validator rejects them per §6.1b.

### 7.3 In-tree config files to update

The implementation must update every in-tree config and test fixture
that uses the old vocabulary. Concrete list (subject to discovery
during the implementation plan):

**Filesystem-store configs and tests:**
- `deploy/store-filesystem.yml`
- `modeling/cli/embedded/deploy/store-filesystem.yml`
- `stores/filesystem/config-example.yml`
- `stores/filesystem/store/admin_test.go`
- `stores/filesystem/store/pick_policy_test.go`
- `stores/filesystem/store/store_test.go`
- `test/smoke/setup.go`
- `test/scenarios/*.go` test fixtures (multiple files; e.g.,
  `acquire_unavailable_pass_test.go`,
  `held_claim_acquirer_blocked_pass_test.go`,
  `reactive_loop_self_invalidate_in_frame_test.go`, etc.)
- `test/scenarios/stores/*.go` test fixtures (multiple files; e.g.,
  `fs_pick_policy_basic_test.go`, `fs_cross_queue_concurrency_test.go`,
  `fs_pick_vs_scope_concurrency_test.go`)

**Postgres-store configs and tests:**
- `deploy/store-postgres.yml`
- `stores/postgres/config-example.yml`
- `stores/postgres/store/store_test.go`
- Any `test/scenarios/stores/pg_*_test.go` files

**Docs:**
- Any docs in `docs/concepts/`, `docs/protocols/`, or `docs/humans/`
  that reference `release_to_back` / `delete` as action values

(The `PickPolicy` Go struct rename and type changes are listed
separately in §13 — those are implementation work, not migration of
operator-facing config files.)

### 7.4 Database / persistence migration

None required. The pick-policy state directory layout (`available/`,
`in_progress/`, plus the new `drained` sentinel) is unchanged in
shape; the new `drained` file is created on demand by the running
store.

## 8. Common patterns

The spec includes this section so a future operator/reader doesn't
have to derive the implications of action × strategy combinations
from first principles. Documentation target: `docs/concepts/` (a new
or extended `claim-producer-fs-store.md` page; postgres equivalents
get their own page).

### 8.1 Ring mode with live folder discovery (filesystem)

```yaml
on_commit: recycle
on_give_up: recycle
sync_strategy: on_open
```

Cycles indefinitely through whatever folders match `folder_pattern` in
`root`. New folders added externally are picked up on the next `Open`.
Removed folders' sentinels are cleared by `runSync` (and the
visibility-timeout sweep for any in-progress claims). Never returns
Unavailable.

### 8.2 Queue mode with auto-refresh between passes

```yaml
on_commit: pop
on_give_up:
  pop_and_move: guidance.failed
sync_strategy: on_drain
```

Each drain pass processes the corpus once. Successful commits leave
folders in place; failed commits triage to `guidance.failed`. After
the queue empties, returns one Unavailable per drain pass and
auto-refreshes the queue from `root` on the next `Open` after that.
This is the docs-pipeline workflow.

### 8.3 Stage / promote workflow

```yaml
on_commit:
  pop_and_move: guidance
on_give_up:
  pop_and_move: guidance.failed
sync_strategy: on_open
```

Operator stages items in `root` (e.g., `guidance.queue`); successful
commits move them to `guidance` (canonical home); failures move to
`guidance.failed`. Queue empties as items move out of `root`.

### 8.4 One-shot ingest with destruction

```yaml
on_commit: pop_and_delete
on_give_up:
  pop_and_move: guidance.failed
sync_strategy: on_drain
```

Items are processed and destroyed; failures triage. Auto-refresh
between passes if any new items appear in `root`.

### 8.5 Static queue with operator-controlled refresh (filesystem)

```yaml
on_commit: pop
on_give_up: pop
sync_strategy: explicit
```

After the initial sync at startup, the queue does not auto-refresh.
Operator calls the admin endpoint to trigger a sync when they want
new folders picked up. Queue eventually drains; returns sticky
Unavailable until admin trigger.

### 8.6 Postgres ring mode

```yaml
items_table: docs_ring
on_commit: recycle
on_give_up: recycle
visibility_timeout_seconds: 1800
```

Items in the table are claimed and recycled in FIFO order. Ring
behavior cycles forever as long as items exist in the table; new
items inserted via the postgres-store admin endpoint join the ring
on the next claim. Never returns Unavailable while items exist.

### 8.7 Postgres queue mode

```yaml
items_table: docs_queue
on_commit: pop
on_give_up: pop
visibility_timeout_seconds: 1800
```

Items are popped (rows deleted) on commit. Queue drains as items are
processed. Returns Unavailable when the table is empty. Operator or
external producer inserts new rows via the postgres-store admin
endpoint to refill.

## 9. Concurrency, sweep, and corner cases (filesystem-store)

> **Scope:** §§9.1–9.6 below describe filesystem-store internals.
> Postgres-store concurrency concerns (claim-token discipline, idempotent
> terminal actions, items-table locking) are unchanged from today; this
> spec touches only the action-vocabulary surface, not the underlying
> claim-token machinery. See §14 for the postgres-store implementation
> notes.

### 9.1 Race between concurrent claims and `drained` write

Two concurrent `Open`s both successfully claim items via rename, both
read `available/` empty afterward, both attempt to write `drained`
with `O_EXCL`. The second write fails with `EEXIST`; the call ignores
that error. Net effect: `drained` is written once, both calls return
Acquired. Correct.

### 9.2 Race between `drained` removal and concurrent `Open`s

Two concurrent `Open`s both find `available/` empty AND `drained`
present. Both attempt `os.Remove("drained")`. One succeeds; the
other gets `ENOENT`, treated as "already removed by the other call,
fine." Both return Unavailable. Correct.

### 9.3 Visibility-timeout sweep returning items mid-pass

When the sweep reaps a stale `in_progress/` sentinel and returns it
to `available/`, the sweep code calls a new helper to remove
`drained` if present (per §5.3 case #2). Subsequent `Open`s see
`available/` non-empty and proceed with claim attempts; no spurious
Unavailable.

### 9.4 Empty corpus on startup

First `Open` on an empty corpus, `sync_strategy: on_drain`:
1. `available/` empty, `drained` absent → run sync.
2. Sync produces no items (extant is empty).
3. `available/` still empty → write `drained`, return Unavailable.

Second `Open`:
1. `available/` empty, `drained` present → remove `drained`, return
   Unavailable.

Third `Open`: same as first. The cycle oscillates writing-then-
removing the sentinel. Operationally harmless (filesystem ops are
fast); cosmetically chatty. Not optimized in v1.

### 9.5 Folder removed mid-claim

A folder in `in_progress/` whose underlying directory is removed
externally remains as a stale `in_progress/` sentinel until the
visibility-timeout sweep reaps it. On the next `runSync` after that,
the sentinel is gone (sweep moved it to `available/`) and the
corresponding `available/` sentinel is cleared by sync's
"remove stale: folder gone from disk" branch.

### 9.6 Cross-pass `on_give_up: pop` with deterministic failure

If a folder fails permanently and `on_give_up: pop` is configured,
`runSync` (running at the next pass boundary) re-discovers the
folder and re-adds it to `available/`. The next pass would re-attempt
the same folder. For permanent failures, operators should prefer
`on_give_up: pop_and_move` (move to a triage area; the folder no
longer matches `folder_pattern` in `root`, so sync doesn't re-add).

The store does not enforce any retry-budget across passes; budget
enforcement is the responsibility of Rimsky's `error_types` policy
chain at the node level. The pick-policy is the simpler "what
happens to this slot and folder" abstraction.

## 10. Test plan

Existing `pick_policy_test.go` (filesystem) and `store_test.go`
(postgres) cover today's behavior. New tests required, organized
per-store.

### 10.0 Filesystem-store tests

(Sections 10.1–10.6 below; this is a section header.)

### 10.1 Action-vocabulary tests

- `TestAction_Pop_FolderStays` — verify pop removes queue entry,
  folder remains on disk, sentinel state consistent.
- `TestAction_PopAndMove_FolderRenamed` — verify folder moved to the
  configured target; rename atomic.
- `TestAction_PopAndMove_GiveUpUsesGiveUpTarget` — verify
  `on_give_up: { pop_and_move: ... }` honors its own target,
  independent of the commit target.
- `TestAction_PopAndDelete_FolderGone` — verify `os.RemoveAll`
  behavior matches today's `delete` action.
- `TestAction_Recycle_QueueCycles` — verify sentinel returns to
  `available/` with fresh mtime; semantically same as today's
  `release_to_back`.
- `TestAction_RejectsCrossFilesystemTarget` — config-load fails when
  `pop_and_move` target is on a different filesystem.
- `TestAction_RejectsMissingTargetDirectory` — config-load fails when
  `pop_and_move` target directory doesn't exist.

### 10.2 `sync_strategy: on_drain` tests

- `TestOnDrain_SinglePass` — drain N items, verify exactly one
  Unavailable, then verify next Open re-acquires after refresh.
- `TestOnDrain_EmptyCorpus` — no folders, verify oscillating
  Unavailable / Unavailable behavior.
- `TestOnDrain_SweepClearsDrained` — drain, simulate sweep returning
  an item, verify next Open Acquires (no spurious Unavailable).
- `TestOnDrain_RaceUnderConcurrentOpens` — `t.Parallel`, M Opens
  concurrently against an N-folder corpus; verify
  (acquired count + unavailable count) == M and sentinel state is
  consistent.

### 10.3 Validator tests

- `TestValidator_RejectsOldNames` — `release_to_back` / `delete` /
  `release_to_head` in `on_commit` are rejected with a clear error
  pointing at the new vocabulary.
- `TestValidator_RejectsMissingFields` — config without `on_commit`
  or without `on_give_up` is rejected.
- `TestValidator_RejectsPopOnOpen` — `pop + sync_strategy: on_open`
  is rejected.
- `TestValidator_WarnsRecycleOnDrain` — `recycle + sync_strategy:
  on_drain` produces a `slog.Warn` log line; config loads. The
  store accepts an injectable `*slog.Logger` (the existing
  `Store` struct has a logger field); the test injects a logger
  backed by a custom `slog.Handler` that captures records into
  a slice, then asserts the slice contains a record with
  `Level == slog.LevelWarn` and a message keyed on the inert
  pairing. (Standard Go pattern; no exotic test infrastructure.)
- `TestValidator_RejectsUnknownAction` — `on_commit: nonsense`
  fails.
- `TestValidator_RejectsMalformedParameterizedAction` —
  `on_commit: { pop_and_move: }` (missing target) or
  `on_commit: { pop_and_move: target1, pop_and_move: target2 }`
  (multi-key) fail.

### 10.4 Migration regression tests

- `TestMigration_OldFieldNames` — `OnCommitDefault` (old field name)
  in YAML loads as a parser error pointing at `on_commit`.

### 10.5 Common-pattern integration tests

One end-to-end scenario test per pattern in §8:

- `TestPattern_RingMode_LiveDiscovery`
- `TestPattern_QueueMode_AutoRefresh`
- `TestPattern_StagePromote`
- `TestPattern_OneShotIngest`
- `TestPattern_StaticQueue_ExplicitRefresh`

These exercise the full Open→Commit cycle through the store, not just
the pick-policy code.

### 10.6 Lint / vet / race coverage (filesystem)

All tests run under `-race`. The pick-policy concurrent paths
(`drained` write and `available/` claim race) get
`-race -count=20` runs to flush flakes.

### 10.7 Postgres-store tests

These exercise the postgres store's pick-policy action surface against
real postgres via testcontainers (per existing `pgtest` discipline).

- `TestPGAction_Pop_RowDeleted` — verify pop deletes the row from the
  items table; subsequent claims don't re-pick it.
- `TestPGAction_Recycle_RowReturnsToQueue` — verify recycle re-marks
  the row available with a fresh claim_after timestamp.
- `TestPGValidator_RejectsPopAndMove` — config-load fails when
  `on_commit: pop_and_move` is configured for a postgres pick policy
  with a clear error pointing at the supported actions.
- `TestPGValidator_RejectsPopAndDelete` — config-load fails for
  `pop_and_delete`.
- `TestPGValidator_RejectsOldNames` — `release_to_back`,
  `release_to_head`, bare `delete` rejected with new-vocabulary
  error message.
- `TestPGValidator_RejectsMissingFields` — missing `on_commit` or
  `on_give_up` rejected.
- `TestPGMigration_OldFieldNames` — `OnCommitDefault` (old field
  name) in YAML produces a parser error pointing at `on_commit`.

### 10.8 Cross-store consistency tests

- `TestSharedVocab_FsAndPgUseSameNames` — meta-test that asserts the
  fs-store and pg-store action constants use the same string values
  for `pop` and `recycle`. Single source of truth (a shared `Action`
  type defined in a common location, see §13) prevents string drift
  between the two stores.

## 11. Risks and unknowns

### 11.1 `os.Rename` cross-fs failure modes

The validator uses `filepath.EvalSymlinks` + `os.Stat` (per §6.3) to
detect cross-filesystem `pop_and_move` targets at config-load,
including the symlink-crossing-filesystem case. The
symlink-resolution step is what makes this robust; without it, a
symlink at `root` or at the target whose actual storage is on a
different filesystem would slip through.

`os.Rename` on a directory that contains open file handles inside it
is allowed on Linux and macOS (the platforms Rimsky targets). The
rename succeeds; existing file handles continue to reference the same
inodes through their new paths.

### 11.2 `pop_and_move` target collisions

If the `pop_and_move` target directory already contains a folder
with the same name as the one being moved, `os.Rename` will fail with
`EEXIST` on POSIX. The pick-policy should surface this clearly via
the existing fs-store error path (returns the error from
`applyPickAction`; the supervisor logs it).

Operators can avoid by ensuring the target is a clean staging area
or by having the workflow remove the target folder before the
commit. The store does NOT auto-rename / dedup target collisions;
that policy belongs upstream.

### 11.3 Concurrency on `drained` write under high churn

The `O_EXCL` write race is benign (per §9.1), but high-churn
workflows (many concurrent `Open` calls) might generate a noticeable
volume of `EEXIST` log lines if not handled silently. The
implementation must NOT log the `EEXIST` case as a warning; it's
expected and benign.

### 11.4 `runSync` performance under large corpora

`runSync` reads the entire `extant`, `available/`, and `in_progress/`
directories on every invocation. With `sync_strategy: on_open`, this
runs per `Open` call. For large corpora (10k+ folders) this could
become a bottleneck. Out of scope for v1; document as a known
limitation. Mitigation paths (caching, inotify) are deferred.

### 11.5 Sweep + `drained` ordering

The "remove `drained` whenever items return to `available/`" rule
must apply to both the sweep loop (case #2 in §5.3) and `runSync`
(case #3). The implementation must touch both code paths; missing
either creates a stale `drained` that produces a spurious
Unavailable next Open.

## 12. What this is not / out of scope

- **Not a per-call action override.** Today's commit RPC accepts no
  override; the policy decides. A per-call override (commit-this-
  claim with `pop_and_move` even though the policy default is
  `recycle`) is a wire-protocol change to `claim_producer.proto` and
  belongs in a separate spec if needed.
- **Not a per-instance or per-frame queue mechanism.** The drain pass
  is store-global per pick policy. Two concurrent instances against
  the same policy share the drain. Per-instance isolation would
  require the fs-store to opt into `LifecycleSubscriber` and
  maintain per-`instance_id` queue state — separate spec.
- **Not a Rimsky-core change.** No proto change, no Rimsky binary
  change, no schema change. Strictly store-side (filesystem and
  postgres stores; the stub store has no pick-policy concept and is
  unaffected).
- **Not a queue-persistence overhaul.** The fs-store remains
  filesystem-rooted; `drained` is one new sentinel file, not a new
  storage layer. The pg-store's items table schema is unchanged.
- **Not a token-budget, rate-limit, or admission-control primitive.**
- **Not a `cwd_from_store` change.** That feature lives on the
  executor side (`agent-run.ts`); this spec doesn't touch it.
- **Not changes to `release_to_head`.** The action is dropped; configs
  that use it are rejected at load. No replacement; if a future
  workflow needs LIFO semantics it gets its own spec.

## 13. Implementation surface (for the plan)

### 13.1 Shared / cross-store

- A new common location for the `Action` tagged-union type and its
  YAML unmarshal (per §3.5 grammar), referenced by both stores.
  Candidate location: `stores/common/action/` (a new sub-package) so
  fs-store and pg-store can both import it without depending on each
  other. The four action names are defined as constants here. This
  ensures string parity (per §10.8) and one source of truth for the
  shape of `pop_and_move`'s parameter.
- The `ValidationResult` struct (per §6.2a) lives alongside the
  `Action` type in `stores/common/action/` (or similar) since both
  stores' validators return it.

### 13.2 Filesystem store

- `stores/filesystem/store/store.go` — `PickPolicy` struct: rename
  `OnCommitDefault` → `OnCommit` with the new tagged-union type;
  rename `OnGiveUpDefault` → `OnGiveUp`; replace the existing
  string-typed `SyncStrategy` field with a typed enum (still string
  internally is fine; the validator enforces the four values).
  `validatePickPolicy` becomes the fs-store validator returning
  `ValidationResult`.
- `stores/filesystem/store/pick_policy.go` — `runSync`,
  `openPickPolicy`, `applyPickAction`: new action-name vocabulary;
  `drained` sentinel write/remove; `removeDrainedIfPresent` helper;
  per-strategy Open sequence dispatch.
- `stores/filesystem/store/sweep.go` — remove `on_sweep` handling
  (no longer in the enum); call `removeDrainedIfPresent` after the
  sweep returns items to `available/`.
- `stores/filesystem/store/store_test.go`,
  `stores/filesystem/store/pick_policy_test.go`,
  `stores/filesystem/store/admin_test.go` — update existing tests
  to use the new vocabulary.
- New test files for the §10.1–10.6 test cases.
- `stores/filesystem/cmd/main.go` — `yamlPickPolicy` struct: rename
  fields (`OnCommitDefault` → `OnCommit`); accept the inline
  parameterized YAML for actions; update the doc-comment YAML
  example.
- `stores/filesystem/server/observability.go` — line 306
  references the old field names; rename.

### 13.3 Postgres store

- `stores/postgres/store/store.go` — `PickPolicy` struct: rename
  `OnCommitDefault` → `OnCommit` with the tagged-union type;
  rename `OnGiveUpDefault` → `OnGiveUp`. Keep the existing fields
  (`ItemsTable`, `VisibilityTimeout`); the postgres store has no
  `SyncStrategy`, no `Root`, no `FolderPattern`. Add a postgres
  validator returning `ValidationResult`.
- `stores/postgres/store/store.go::applyPickAction` — switch from
  the old action vocabulary to the new (only `pop` and `recycle` are
  legal here; `pop_and_move` and `pop_and_delete` are rejected by
  the validator and so should never reach this code path — defensive
  rejection here too).
- `stores/postgres/store/store.go::validPickAction` — accept only
  `pop` and `recycle` action constants from the shared package.
- `stores/postgres/cmd/main.go` — `yamlPickPolicy` struct: rename
  fields; accept the inline parameterized YAML for actions; update
  the doc-comment YAML example.
- `stores/postgres/server/observability.go` — lines 306–307 and
  316–317 reference the old field names; rename.
- `stores/postgres/store/store_test.go` — update existing tests.
- New test files for the §10.7 test cases.

### 13.4 In-tree configs and cross-package tests

Per §7.3.

### 13.5 Docs

- `docs/concepts/` — new or updated pages documenting the action
  vocabulary (shared concept), the fs-store sync strategies and
  `drained` mechanism, and the §8 common patterns. Postgres-store
  page (existing or new) gets the §8.6–§8.7 patterns.

The plan also covers each store's validator implementation, the
cross-fs validation helper (fs-store only), and the slog-warn for
`recycle + on_drain` (fs-store only).

## 14. Postgres-store implementation notes

This section captures the postgres-store-specific implementation
considerations that differ from the filesystem store.

### 14.1 Action implementations

The postgres store's `applyPickAction` already handles the three old
actions (`delete`, `release_to_back`, `release_to_head`) via SQL
against the configured `items_table`. The migration:

- **`pop`** — replaces the existing `delete` branch. Same SQL: delete
  the row from the items table where `claim_token = $claimID`. The
  rename is purely cosmetic on the action name; the SQL is unchanged.
- **`recycle`** — replaces the existing `release_to_back` branch.
  Same SQL: `UPDATE items_table SET claim_token = NULL,
  claim_after = NOW() WHERE claim_token = $claimID` (or whatever the
  existing release_to_back SQL is — see §14.3).
- **`pop_and_move`** — rejected at config-load (§6.1b). If a config
  somehow slips through, `applyPickAction` returns
  `errors.New("postgres store: pop_and_move not supported")` as a
  defensive fallback; the operator should never reach this path.
- **`pop_and_delete`** — same defensive rejection.
- **`release_to_head`** — removed entirely. The existing SQL branch
  is deleted; the validator rejects the old action name from configs.

### 14.2 No `sync_strategy`, no `drained`, no cross-fs check

The postgres store has no auto-discovery mechanism (the items table
is the source of truth; rows are inserted by the admin endpoint or
external producers). It has no `SyncStrategy` field today and gets no
new one. There is no `drained` sentinel concept (no equivalent to
"the queue was just drained; refresh the source") because the source
of truth is a SQL table the store doesn't itself populate. Cross-fs
validation is meaningless without `pop_and_move`.

### 14.3 Verify existing SQL before mapping

The plan must read the postgres-store's current `applyPickAction`
implementation (in `stores/postgres/store/store.go` around the
`switch action` block at ~line 340) to confirm:

- The exact SQL for `delete` (what becomes `pop`).
- The exact SQL for `release_to_back` (what becomes `recycle`).
- The exact SQL for `release_to_head` (which is deleted entirely).

If the implementer discovers the existing SQL has any subtlety
(e.g., conditional logic on `visibility_timeout`, claim-token
filtering for idempotence) the rename preserves it verbatim — only
the case-label string changes.

### 14.4 Validator returns ValidationResult

The pg-store's `New` constructor today returns
`(*Store, error)`. After this work, the validator portion still
surfaces errors via the constructor's error return; warnings (if
any are added — currently the pg-store has no warning conditions)
are logged via package-level `slog`. The pg-store's
`ValidationResult` may have an empty `Warnings` slice in v2; the
struct is included for shape parity with fs-store and to leave room
for future per-store warnings.

### 14.5 Test discipline

The pg-store's tests use `pgtest` (testcontainers-backed real
postgres). The §10.7 tests follow the same pattern. The
cross-store consistency test (§10.8 `TestSharedVocab_FsAndPgUseSameNames`)
lives in `stores/common/action/` (alongside the shared
`Action` type) and tests the constants directly, not the
testcontainers path.
