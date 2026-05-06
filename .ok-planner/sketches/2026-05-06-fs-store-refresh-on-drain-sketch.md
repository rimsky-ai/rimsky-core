# fs-store: pick-policy action vocabulary v2 + `refresh_on_drain` — Design Sketch

**Date:** 2026-05-06
**Status:** Sketch (not a spec; not authorization to build)

## Idea

Two related fs-store changes that together unblock **queue mode** for
the docs-pipeline (`/Users/patrick/Documents/projects/research/verantel/.ok-planner/sketches/2026-05-05-docs-corpus-rimsky-pipeline-sketch.md`)
and clean up the pick-policy action surface generally:

1. **Replace today's `on_commit_default: release_to_back | delete` +
   `delete_strategy` (proposed)** with a single named-action vocabulary
   that factors queue-entry-fate from folder-fate cleanly:

   - `pop` — queue entry consumed, folder kept in place
   - `pop_and_move` — queue entry consumed, folder renamed to a
     configured target root
   - `pop_and_delete` — queue entry consumed, folder `rm -rf`'d
   - `recycle` — queue entry returns to the queue tail, folder kept
     (today's `release_to_back`, renamed for symmetry)

   Same vocabulary applies to both `on_commit` and `on_give_up`.

2. **Add `refresh_on_drain: bool`** so that a pick policy in queue mode
   (any `pop*` commit action) automatically re-discovers the source
   root on the next `Open` after the queue empties. One sentinel file
   in `.fs-store/<policy>/` flips the right way at the right time.

Both changes are pure store-side. No proto change, no Rimsky-core
change. Independently shippable; both are needed for the docs-pipeline
queue mode (or, with `pop`, just `refresh_on_drain` is needed).

## Motivation

Today's pick-policy commit-action surface conflates two independent
dimensions:

1. **Queue-entry fate** — should this slot in the queue stay or go?
2. **Folder fate** — what happens to the folder on disk when this
   commit fires?

Today's `release_to_back` says "slot returns, folder untouched";
today's `delete` says "slot goes, folder also goes." There's no
expressed name for "slot goes, folder stays" — and that's the action
the docs-pipeline actually wants: each area-pass is a finite chunk of
work; the agent edits the folder in-place; the queue entry is
consumed; the folder remains for `git diff` review. With today's
vocabulary the operator has to either:

- Use `release_to_back` and rely on auto-discovery / sentinel sweeps
  to eventually drain (never reliably terminates), or
- Use `delete` and accept that `os.RemoveAll` destroys the agent's
  edits along with the folder.

The proposed-but-not-shipped `delete_strategy: move` was an attempt to
patch over this — it told the `delete` action to do `os.Rename`
instead of `os.RemoveAll`, preserving edits. That works but it's a
two-axis config (`delete + delete_strategy: move`) where one of the
axes is conditional on the other. The named-action vocabulary in this
sketch collapses both axes into a single field with four legal values.

A second observation: under today's design, "queue mode" (single-pass
with termination) is implicit in the choice of `delete` as the commit
action, and the handoff between drains requires operator intervention
(e.g., the docs-pipeline original sketch's `mv guidance guidance.queue`
pre-step). `refresh_on_drain` makes the drain-handoff automatic so the
operator can just `dev up` repeatedly without any pre-step.

## Action vocabulary v2

```yaml
pick_policies:
  "@docs":
    root: guidance
    folder_pattern: "^[a-z][a-z0-9_-]*$"
    on_commit: pop                              # ← new vocabulary
    on_give_up: pop_and_move                    # different action on failure
    give_up_move_target_root: guidance.failed   # required iff on_give_up == pop_and_move
    refresh_on_drain: true                      # ← new flag
    visibility_timeout_seconds: 1800
    sync_strategy: on_open
```

### Actions

| Action | Queue entry | Folder | Useful when |
|---|---|---|---|
| `pop` | consumed | kept | Workflow edits the folder in-place; operator reviews via `git diff`. |
| `pop_and_move` | consumed | renamed to `commit_move_target_root` (or `give_up_move_target_root` for the give-up path) | Stage / promote workflows; failure-triage areas. |
| `pop_and_delete` | consumed | `os.RemoveAll`'d | Process-then-discard (e.g., a one-shot scratch ingest). Destructive; few legit use cases. |
| `recycle` | returned to queue tail | kept | Ring mode (formerly `release_to_back`). |

### Required-iff-action config

- `on_commit: pop_and_move` requires `commit_move_target_root: <path>`
- `on_give_up: pop_and_move` requires `give_up_move_target_root: <path>`
- `pop_and_delete` and `pop` and `recycle` require no extra fields

The validator rejects the action without its target field, and rejects
target fields that don't pair with their action. Move targets are
validated at config-load to be on the same filesystem as `root` (else
the `os.Rename` won't be atomic; fail loudly per existing fs-store
discipline).

### Pairings — what the four actions × `refresh_on_drain` produce

| `on_commit` | `refresh_on_drain` | Behavior |
|---|---|---|
| `recycle` | irrelevant | Today's ring mode. Items cycle; queue never empties. |
| `pop` | `false` | Single-pass over a snapshot of the corpus; sticky `Unavailable` after drain; folders all still in `root/`. |
| `pop` | `true` | **docs-pipeline queue mode.** Each operator-triggered run does one pass; folders stay in `root/`; next operator run gets a fresh queue automatically. |
| `pop_and_move` | `false` | Single-pass with promote/archive; sticky `Unavailable` after drain; `root/` is empty (folders all moved out). |
| `pop_and_move` | `true` | Same as above; refresh just confirms emptiness. (Useless pairing — folders moved out, refresh finds nothing. Still legal, no warning.) |
| `pop_and_delete` | `false` | Single-pass with destruction; sticky `Unavailable`; folders gone. |
| `pop_and_delete` | `true` | Same caveat as `pop_and_move` + `refresh_on_drain: true`. |

`refresh_on_drain` is meaningful only with `pop` (where folders persist
between drains and refresh re-discovers them). With `pop_and_*`, folders
leave the source root, so refresh has nothing to find. Legal, just
inert. With `recycle`, the queue never empties, so the
empty-then-refresh path never triggers. Also inert.

## Configuration migration

Today's pick policies use:

```yaml
on_commit_default: release_to_back | delete
on_give_up_default: release_to_back | delete
```

V2:

```yaml
on_commit: recycle | pop | pop_and_move | pop_and_delete
on_give_up: recycle | pop | pop_and_move | pop_and_delete
commit_move_target_root: <path>      # iff on_commit == pop_and_move
give_up_move_target_root: <path>     # iff on_give_up == pop_and_move
refresh_on_drain: true | false       # default false
```

Pre-v1; no compat shim. The migration is mechanical:

| Old | New |
|---|---|
| `on_commit_default: release_to_back` | `on_commit: recycle` |
| `on_commit_default: delete` | `on_commit: pop_and_delete` |
| `on_give_up_default: release_to_back` | `on_give_up: recycle` |
| `on_give_up_default: delete` | `on_give_up: pop_and_delete` |

(There's no old config that maps to `pop` or `pop_and_move` — those
are the new actions the v2 vocabulary surfaces.)

The store rejects unknown action values and unknown action+target
combinations at config-load. Existing pick-policy YAML files in the
codebase (`quickstart/store-filesystem.yml`, the smoke fixture configs,
any in-tree examples) must be updated as part of the implementation.

## `refresh_on_drain` mechanism

A new sentinel file under each pick policy's state directory:

```
.fs-store/<policy>/
├── available/        # one sentinel file per available item
├── in_progress/      # one sentinel file per claimed item
└── drained           # ← present iff the last drain pass just ended
```

The `drained` file is empty (its existence is the signal). It's
created and removed by the pick-policy code; never touched by the
operator.

### Open sequence with `refresh_on_drain: true` and `on_commit: pop`

```
Open call:
  1. If sync_strategy == "on_open": run sync (existing).
  2. Try claim from available/ (existing rename-as-claim logic).
     ├─ Success: was this the last item in available/?
     │    ├─ Yes: write .fs-store/<policy>/drained atomically (O_EXCL).
     │    └─ No:  return Acquired.
     │  (Either way, return Acquired for this call.)
     └─ available/ empty:
          ├─ drained file present:
          │    Remove drained file. Return Unavailable.
          └─ drained file absent:
               Run sync (regardless of sync_strategy — auto-refresh).
               Try claim again.
               ├─ Success: same "was this the last?" check.
               └─ Still empty: write drained, return Unavailable.
```

Net effect: each drain pass produces exactly one `Unavailable` per
`Open` call. Stable. Race-tolerant via O_EXCL on the `drained` write
(double-write benign; missing-write impossible).

### Concurrency invariants

- `drained` exists ⇒ `available/` is empty AND the previous successful
  claim was the last item.
- Whenever `available/` transitions from empty to non-empty (via
  sync, via sweep returning items, via `recycle` action — though
  `recycle` and `refresh_on_drain` don't usefully co-exist), remove
  `drained` if present.
- Implementation: one-line addition to `runSync` (remove `drained` if
  any new items were placed into `available/`) and to `sweep.go`
  (remove `drained` after returning items from `in_progress/` back
  to `available/`).

### Empty-corpus startup

First Open on an empty corpus:
1. Sync runs → `available/` stays empty.
2. Try claim → empty.
3. `drained` absent → run sync again (idempotent, still empty).
4. Try claim → empty.
5. Write `drained`, return Unavailable.

Second Open (still empty corpus):
1. Sync → empty.
2. Try claim → empty.
3. `drained` present → remove it, return Unavailable.

Third Open: same as first. Sequence oscillates writing-then-removing
the sentinel. Cosmetically chatty but operationally harmless.

If we care: short-circuit by checking "did sync produce anything new?"
before considering writing a fresh `drained`. Defer to v2.

## What this collapses (relative to the docs-pipeline original sketch)

The docs-pipeline original sketch (`docs-corpus-rimsky-pipeline-sketch.md`)
listed two Rimsky-side dependencies:

- **#1: `delete_strategy: move`** — proposed two-axis `delete +
  delete_strategy: move` config to make queue mode preserve agent
  edits. **Subsumed entirely** by the action-vocabulary v2:
  `pop_and_move` is `delete + delete_strategy: move`; `pop_and_delete`
  is today's `delete`; new actions `pop` and `recycle` cover the rest
  of the cross-product.
- **#2: Reactive Loops + Lifecycle Handlers** — shipped 2026-05-05
  (commit `712f83a`).

So with v2, the docs-pipeline's queue-mode pick policy becomes:

```yaml
"@docs":
  root: guidance
  folder_pattern: "^[a-z][a-z0-9_-]*$"
  on_commit: pop                          # folder stays; agent edits preserved
  on_give_up: pop                         # let next pass retry the failed folder
  refresh_on_drain: true                  # auto-refresh queue between operator runs
  visibility_timeout_seconds: 1800
  sync_strategy: on_open
```

Operator workflow:

```sh
cd docs-pipeline
rimsky-cli dev up           # creates instance → drain pass → terminates on Unavailable
rimsky-cli dev down
git diff ../agent-content/platform/guidance/
# Review and commit. Run again if more polishing wanted.
```

No `mv guidance guidance.queue` pre-step. No `guidance.failed/` triage
area (unless the operator wants one — set `on_give_up: pop_and_move`
with `give_up_move_target_root: guidance.failed`). Corpus stays
in place; agent edits land in canonical paths.

## What this does not do

- **Does not change today's behavior under the default `refresh_on_drain:
  false` for any policy.** Strict opt-in.
- **Does not change the wire protocol.** No `Open` RPC field added.
- **Does not introduce per-instance scoping.** `refresh_on_drain` is
  store-global per pick policy. Two concurrent instances against the
  same policy share the drain.
- **Does not provide lifecycle subscription.** `LifecycleSubscriber` is
  available; the fs-store doesn't need it for this work.

## Considered alternatives

### Per-instance queue via `OnInstanceCreated` lifecycle subscription

The fs-store opts into `LifecycleSubscriber` and snapshots the corpus
into a per-`instance_id` queue file on `OnInstanceCreated`.

**Pros:** per-instance isolation; cleanly tracks runs via Rimsky's
instance lifecycle.
**Cons:** new protocol surface for the fs-store (currently doesn't
implement `LifecycleSubscriber`); per-instance manifest lifecycle and
abnormal-termination cleanup; conditional `instance_id` switching in
the pick-policy code path.

**Verdict:** strictly more capable but more complex. Defer until a
workflow needs per-instance isolation against a single pick policy.
Single-operator workflows (docs-pipeline) don't need it.

### Frame-scoped lifecycle event (`OnFrameStarted`)

A 7th method on `LifecycleSubscriber`. Store reloads queue when a new
frame starts.

**Cons:** real new proto method; wrong granularity for both `frame: in`
(fires once per drain — same as `refresh_on_drain` but heavier) and
`frame: next` (fires per iteration, defeating the drain).

**Verdict:** skip.

### Frame ID in `Open` RPC

Add `frame_id` to `claim_producer.proto::OpenRequest`. Store
auto-detects frame transitions.

**Cons:** real proto change; same wrong-granularity problem; per-frame
queues don't fit the docs-pipeline model.

**Verdict:** skip.

### Keep today's two-axis design (`delete + delete_strategy: move`)

Add only `delete_strategy: move | trash | remove` and `refresh_on_drain`
without renaming the action vocabulary.

**Pros:** smaller config-shape diff. Less migration churn.
**Cons:** the conditional-on-other-field shape (`delete_strategy` is
meaningful only when `on_commit_default == delete`) is exactly the
leak this sketch is trying to fix. Adding `refresh_on_drain` on top
makes the config three-axis where two are inter-dependent. The
named-action vocabulary is one axis with four legal values. Cleaner.

**Verdict:** rejected. We're pre-v1; no compat constraint; do the
clean change once.

## Open questions

- **Naming: `recycle` vs keeping `release_to_back`.** `recycle` matches
  the named-mode pattern and is shorter. `release_to_back` is more
  descriptive about queue ordering ("returns to the back of the
  queue") but verbose. I lean `recycle`. Open for the brainstorm.
- **Should the validator reject `pop_and_move` + `refresh_on_drain:
  true`?** The pairing is legal but inert (folders moved out, refresh
  finds nothing). Could warn at config-load. Probably not worth a
  hard reject — operators may want it as a "single-pass with archive,
  but next pass auto-refreshes if any folders are added externally
  later" pattern. Document as no-op-but-legal.
- **What if `on_commit == pop` is combined with no `refresh_on_drain`?**
  Drain happens; sticky Unavailable; manual reset. Useful for
  workflows where the operator wants a one-shot pass with no
  auto-restart. Already legal under v2.
- **Per-call action override.** Today's commit RPC could carry an
  override (commit-this-claim with `pop_and_move` even though the
  policy default is `recycle`). Out of scope for v2; document as
  future work.
- **Does the admin HTTP API expose action vocabulary?** Add a
  validating-config endpoint and an "explain this policy" endpoint
  to ease operator debugging. Out of scope for v2.

## Risks / unknowns

- **Renaming `release_to_back` → `recycle` is a breaking config
  change.** Pre-v1, acceptable. Catch references in:
  `quickstart/store-filesystem.yml`, `deploy/store-filesystem.yml`
  (if exists), smoke-test fixtures, in-tree examples,
  `stores/filesystem/store/types.go` (config struct field), tests.
- **Cross-filesystem move on `pop_and_move`.** `os.Rename` requires
  same filesystem. Validate at config-load that
  `commit_move_target_root` and `give_up_move_target_root` are on the
  same fs as `root`. Fail loudly on startup, not at runtime.
- **Concurrent claims racing on the last item + `drained` write.** Two
  Opens both succeed via rename, both check `available/` empty, both
  attempt O_EXCL `drained` write. Second EEXISTs, harmless. Both
  return Acquired. Already covered in mechanism §.
- **Visibility-timeout sweep returning items to `available/` after
  `drained` was set.** Handled by removing `drained` whenever
  `available/` transitions empty → non-empty (one-line addition to
  `sweep.go`).

## Test plan (sketch)

Existing `pick_policy_test.go` covers today's behavior. Add:

- Action-vocabulary tests:
  - `TestAction_Pop_FolderStays` — verify pop removes queue entry,
    folder remains on disk, sentinel state consistent.
  - `TestAction_PopAndMove_FolderRenamed` — verify folder moved to
    `commit_move_target_root`; rename atomic.
  - `TestAction_PopAndMove_FolderMoved_GiveUp` — verify
    `give_up_move_target_root` honored on give-up.
  - `TestAction_PopAndDelete_FolderGone` — verify `os.RemoveAll`
    behavior matches today's `delete`.
  - `TestAction_Recycle_QueueCycles` — verify sentinel returns to
    `available/` with fresh mtime; semantically same as today's
    `release_to_back`.
  - `TestAction_RejectMoveActionWithoutTarget` — config-load rejects
    `pop_and_move` without `commit_move_target_root` (or
    `give_up_move_target_root` for give-up).
  - `TestAction_RejectMoveTargetCrossFilesystem` — config-load fails
    when target is on a different filesystem.

- `refresh_on_drain` tests:
  - `TestRefreshOnDrain_SinglePass` — opt in, drain N items, verify
    exactly one Unavailable, then verify next Open re-acquires.
  - `TestRefreshOnDrain_EmptyCorpus` — opt in, no folders, verify
    oscillating Unavailable / Unavailable behavior.
  - `TestRefreshOnDrain_SweepClearsDrained` — opt in, drain, simulate
    sweep returning an item, verify next Open Acquires (no spurious
    Unavailable).
  - `TestRefreshOnDrain_RaceUnderConcurrentOpens` — `t.Parallel`,
    M Opens concurrently against an N-folder corpus; verify
    (acquired count + unavailable count) == M and sentinel state is
    consistent.
  - `TestRefreshOnDrain_DefaultOff_PreservesTodaysBehavior` —
    regression: with `refresh_on_drain: false`, behavior identical to
    today's sticky-Unavailable.

- Migration tests:
  - `TestConfigLoad_RejectsOldNames` — `release_to_back` /
    plain `delete` in `on_commit` are rejected with a clear error
    pointing at the new vocabulary. (Pre-v1; no auto-translate.)

## What this is not

- **Not a queue persistence overhaul.** fs-store stays
  filesystem-rooted; this is one new sentinel file plus action-name
  vocabulary, not a new storage layer.
- **Not a per-instance / per-frame scoping mechanism.** That's the
  deferred lifecycle-subscription path.
- **Not a replacement for `recycle`.** Ring mode still has its place
  (perpetual cycling without termination).
- **Not a Rimsky-core change.** Strictly fs-store. The reactive-loops
  + lifecycle-handlers shipped 2026-05-05 stays the same.
- **Not a token-budget or rate-limit primitive.**
