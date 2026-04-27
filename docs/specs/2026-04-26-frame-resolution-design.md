# Frame Resolution Design Spec

**Date:** 2026-04-26
**Status:** design proposal — ready for plan + implementation
**Genesis:** the design proposal at `docs/history/2026-04-25-frame-resolution.md` (moved to history after this spec's implementation shipped; read first if context is needed — that doc captures the problem statement, the failed smoke test that surfaced the gap, the four-rule design space, and the rationale for the choices made in this spec). The design proposal answered "what is the problem and what's the design space"; this document answers "what shall we build."
**Foundation:** assumes the stores-redesign spec at `docs/specs/2026-04-25-stores-redesign-design.md` as the underlying contract. References to §X.Y in this spec are to that spec unless otherwise noted.

---

## 1. Vocabulary

This spec introduces and uses the following terms consistently. These are normative.

- **Frame** — a complete pass of the cascade engine over a graph instance. A frame begins with one or more invalidation sources and ends when every node in the frame's expected-node set has reached a terminal state (`fresh`, `failed`, or pruned).
- **Render** — the act of executing a frame. "Render frame N" = "execute frame N's cascade." Used as both noun (a render is happening) and verb (the engine renders frame N).
- **Frame queue** — under serial-queue (Rule 3a), the per-instance ordered list of frames waiting to render or in flight.
- **Coalesce** — Rule 1 behavior: invalidations during an in-flight render collapse into a single trailing frame whose source set is the union of all coalesced sources.
- **Serial queue** — Rule 3a behavior: each invalidation produces a distinct frame; frames render one at a time per instance, in arrival order.
- **Frame source** — the node(s) whose invalidation initiated a frame. Stored as `source_node_ids text[]` on `rimsky_frames`.
- **Frame expected-node set** — the set of nodes transitively reachable in the template's edge graph from the frame's source set. Computed at frame-start; not materialized as a separate table for v1 (derivable from template topology + source set).
- **Pruned** — a node in a frame's expected-node set that did not run because some upstream node committed `changed: false`, cutting the cascade. Pruned nodes have no `rimsky_dispatch` row for that `frame_id`.
- **Frame-resolution mode** — the per-template setting (`frame_resolution: coalesce | serial_queue`) that selects between Rule 1 and Rule 3a behavior.

Out of scope for this spec (deferred to a future spec):

- **Parallel buffered (Rule 3b)** — multiple concurrent frames per instance with per-frame attribute snapshots and topological gating. Schema is forward-compatible (see §10.6) but logic is post-v1.
- **Per-frame attribute snapshots** — no v1 storage primitive; under Rules 1 and 3a, frames don't overlap, so the live attribute IS the in-flight frame's attribute.
- **Operator-invalidate priority / queue-jumping** — every invalidate is FIFO under 3a or joins the pending-coalesce row under 1. If "force this through now" turns out to be a real operator need, it goes in a follow-up spec.
- **Frame back-pressure** — the queue is unbounded for v1. Pathological queue growth is a producer-side bug, not an engine concern. A bounded queue with an explicit overflow policy is a follow-up.

---

## 2. Why this exists

The existing rimsky cascade has no notion of a frame: invalidations propagate immediately and the cascade has no "complete this pass before another can start" gate. Under sustained invalidation pressure (e.g., the §19.2 stores-redesign smoke test: 100 force-fires on a scheduled source) the cascade live-locks — each new invalidation cancels the in-flight work, which never settles, and downstream commits drop on the floor. The smoke test's acceptance predicate (≥100 terminal commits) currently observes ~3.

The architectural fix is to introduce a frame as a first-class engine concept. Every cascade event in the system belongs to a frame; frames are scheduled, render, and complete; the engine guarantees the frame-resolution invariant:

> **The graph resolves to a consistent state between invalidations.**

Two resolution modes are supported. Each template declares which it uses. There is no default — declaration is mandatory.

- `frame_resolution: coalesce` — multiple invalidations during a render collapse to one trailing frame. Suited to data-freshness pipelines where rate-of-change >> rate-of-meaningful-recomputation.
- `frame_resolution: serial_queue` — each invalidation produces its own frame; frames render one at a time. Suited to event-driven pipelines where each invalidation represents a discrete unit of work (the smoke fixture's claim-driven workload is the canonical example).

Rule 2 (preemptive abort) is intentionally not supported. Under stores semantics, mid-render side effects (executor commits, store mutations, claim acquisitions) cannot be reliably rolled back, making "abort and restart" unsafe. Operators who need "kill the in-flight thing and run mine" are directed to wait-then-fire under serial_queue, or to coalesce-fire under coalesce — neither preempts running work. Rule 3b (parallel buffered) is post-v1 (see §10.6 for schema forward-compatibility).

---

## 3. Mode semantics

### 3.1 `frame_resolution: serial_queue`

Each invalidation event produces a distinct frame.

- **Source events:** scheduled fire (cron), operator invalidate (`POST /v1/nodes/{node_id}/invalidate`), admin force-fire (`POST /admin/scheduled-nodes/{node_id}/force-fire`), and any other producer of an invalidation. Cascade-internal message-passes are NOT source events — they are propagation within an existing frame.
- **Enqueue:** producer inserts a row in `rimsky_frames` with `state = 'queued'`, `mode = 'serial_queue'`, `source_node_ids = [{producer's target}]`, `queued_at = now()`. Enqueue is a single-row insert with no engine-side coordination.
- **No queue coalescing.** Two same-source invalidations queue two distinct frames. (If a producer wants to coalesce same-source events, it does so producer-side before enqueue. The schedule_ticker's existing no-backfill behavior, §14, is one such producer-side coalescer; that behavior is preserved.)
- **Dequeue and render:** scheduler tick (§4) selects the oldest `queued` frame for an instance, transitions it to `running`, sets `started_at = now()`, and initiates the cascade by setting `rimsky_nodes.state = 'stale'` AND `rimsky_nodes.frame_id = $frame_id` for each `source_node_id`. From there the cascade propagates as it does today, but every dispatch and every node-state transition carries the `frame_id`.
- **Frame-end:** detected when no node in this instance has non-terminal state for this `frame_id` (see §4.2). Engine transitions `state` to `completed` (or `failed`, if any expected-node ended `failed`), sets `ended_at = now()`. On the same tick, dequeues the next `queued` frame for the instance.
- **Per-instance ordering invariant:** at most one frame in `running` state per instance at any time. Enforced by a partial unique index on `rimsky_frames(instance_id) WHERE state = 'running'`. Multiple instances of the same template may have running frames concurrently — independent instances do not share a queue.

### 3.2 `frame_resolution: coalesce`

Invalidations during an in-flight render collapse into a single trailing frame.

- **Source events:** same as serial_queue.
- **Enqueue logic** (full mechanics in §13.1; summary here): producer calls `frame.EnqueueOrCoalesce`. The helper either inserts a new `queued` coalesce row (if none exists) or appends the source to the existing `queued` coalesce row's `source_node_ids` array. Producers never write directly to `rimsky_frames.state = 'running'`; the queued→running transition always happens in the scheduler tick (§4.3).
- **At most one queued frame per coalesce instance.** Enforced by partial unique index `uq_rimsky_frames_coalesce_queued` on `rimsky_frames(instance_id) WHERE state = 'queued' AND mode = 'coalesce'`. Serial-queue instances may accumulate many queued rows; coalesce instances have at most one.
- **Trailing-frame trigger:** at frame-end of the running frame (§4.1 step 1), the same scheduler tick checks for a queued coalesce row and transitions it to `running` (§4.1 step 3). No debounce window. Multiple invalidates that arrived during the running frame collapse into this one trailing render whose source set is the union of all coalesced targets.
- **First-invalidate latency.** Because both modes go through the same queued→running path, a coalesce instance with no in-flight render still waits one scheduler tick (≤1s default) between the first invalidate and the start of rendering. This unifies the engine's flow at the cost of one tick of latency on the first event — acceptable for the data-freshness use cases coalesce targets.
- **Frame-end:** identical to serial_queue's predicate (§4.2).

### 3.3 Behaviors common to both modes

- A frame cannot be aborted, killed, or cancelled by another invalidation. The kill-requested mechanism (`runner_dispatch.go::isKillRequested`, the `kill_requested` column on `rimsky_nodes`) is removed by this spec (see §11). Operator nudges queue or coalesce; they do not preempt.
- A frame can be marked `failed` only by the timeout reaper (§7) or by an expected-node entering `failed` state (which triggers frame-end with `state = 'failed'`).
- Mid-frame invalidations on nodes inside the in-flight frame's expected set are absorbed: under serial_queue they enqueue a new frame; under coalesce they join the pending row. **They do not affect the in-flight render** — the in-flight render reads the node-state and attributes that existed at frame-start, not the live state.
- The instance's frame_id "watermark" — the frame_id of the most recently completed frame — is implicit (max `frame_id` with `state IN ('completed','failed')` per instance). No dedicated column.

---

## 4. Engine: scheduler-owned

The scheduler tick (the same tick guarded by `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`, blessed-invariant 7) gains the frame-engine logic. No new process. The scheduler binary's package import surface gains nothing not already present.

### 4.1 Scheduler-tick frame logic

For each instance with at least one non-terminal `rimsky_frames` row, the scheduler:

1. **Detect frame-end.** For the running frame (if any), evaluate the frame-end predicate (§4.2). If true, transition the frame to `completed` or `failed`, set `ended_at`, run claim-resolution finalization (§8) inside the same tx as the state transition.

2. **Advance queue (serial_queue).** For instances with `frame_resolution = serial_queue` AND no running frame: select the oldest `queued` frame and transition it to `running` (see §4.3 for transition mechanics).

3. **Advance trailing (coalesce).** For instances with `frame_resolution = coalesce` AND no running frame AND a queued frame: transition the queued frame to `running`.

4. **Reap stuck frames.** For frames in `running` state with `started_at + frame_timeout_ms < now()` AND no `rimsky_dispatch` rows with `frame_id = F.frame_id AND claimed_by IS NOT NULL`: transition the frame to `failed` (treated as "frame wedged with no live work; abandon"). See §7.

5. **Reap orphan dispatches with stale frames.** If a `rimsky_dispatch` row has `claimed_by IS NOT NULL` but its `frame_id`'s frame row is in a terminal state (`completed`, `failed`), the dispatch is orphaned by frame collapse (should not happen under correct accounting; defensive sweep). Release the dispatch (claimant-guarded UPDATE per blessed-invariant 4), log, do not retry.

All five steps run inside the existing scheduler-tick advisory lock — only one scheduler replica drives an instance's frame transitions per tick.

### 4.2 Frame-end predicate

A running frame F is at end if and only if there are no nodes in this instance still in motion:

```sql
SELECT count(*) = 0 FROM rimsky_nodes
WHERE instance_id = F.instance_id
  AND state IN ('stale', 'running');
```

(Node states in rimsky are `fresh | stale | running | failed` — see CLAUDE.md and `core/node/state.go`. There is no separate `claimed` state; `claimed_by IS NOT NULL` on `rimsky_dispatch` together with `rimsky_nodes.state = 'running'` represents an in-flight execution.)

Rationale: under Rules 1+3a, frames don't overlap, so the live `rimsky_nodes` table IS the in-flight frame's per-node state. Any node in `stale` or `running` belongs to the running frame; if no such node exists, the frame is done.

If any expected node ended `failed`: `state` of the frame row → `failed`. Else: → `completed`. The scheduler reads the dispatch outcomes for this `frame_id` via `rimsky_dispatch.frame_id` join (the dispatch is the audit record of "this node ran or failed in this frame"; for `failed` outcomes, the supervisor leaves the dispatch row in place after marking `rimsky_nodes.state = 'failed'`).

Edge case: the frame just transitioned `queued → running` and its source-node `state = 'stale'` writes haven't committed yet. Mitigation: the queued→running transition AND the source-stale writes happen in the same tx (§4.3). If the tx commits, the source-stale rows are visible to the next read; if it rolls back, the frame stays `queued`.

Edge case: a node ended `failed` in a previous frame and its `rimsky_nodes.state = 'failed'`. The predicate excludes `failed` (terminal). Correct — a `failed` node from a prior frame doesn't keep new frames in flight. (Re-enqueueing after a failure is operator-driven via invalidate, which clears the failed state via the existing state-machine transition `failed → stale`.)

### 4.3 Frame-start mechanics (`queued → running`)

When the scheduler transitions a frame F from `queued` to `running`, all of these happen in one transaction:

1. `UPDATE rimsky_frames SET state = 'running', started_at = now() WHERE frame_id = F.frame_id AND state = 'queued'` (CAS — only succeeds if still queued; the row's `RETURNING` is checked).
2. For each `node_id` in `F.source_node_ids`:
   ```sql
   UPDATE rimsky_nodes
   SET state = 'stale',
       frame_id = F.frame_id,
       updated_at = now()
   WHERE instance_id = F.instance_id AND id = $node_id
     AND state IN ('fresh','failed');   -- the legal in-bounds invalidate transitions
   ```
3. The standard dispatch enqueue path the existing scheduler already uses for "this node became stale" runs for each source node — inserting a `rimsky_dispatch` row with `frame_id = F.frame_id` (see §10.2 for the new `frame_id` column on dispatch).

If step 1's CAS fails (another scheduler replica already transitioned the frame), the entire tx rolls back; that replica skips this frame for this tick. Blessed-invariant 7's per-tick advisory lock makes the race rare; this CAS is defense in depth.

If step 2 finds a source node not in `fresh|failed` (i.e., still `stale` or `running` from a prior frame the engine somehow believed had ended): the tx rolls back, the scheduler logs a structured warning, and the frame remains `queued` for the next tick to retry. This should be impossible under correct accounting (frame-start is gated on no-frame-in-flight) and indicates a bug — fail loud, not silently.

The cascade then propagates as it does today — supervisors pick up `stale` nodes via the dispatch queue, claim them, run the executor, commit. The only difference: every dispatch row carries `frame_id`.

### 4.4 Cascade propagation: how `frame_id` flows

When a node N commits successfully, the supervisor's commit logic (§13.5–13.7 of stores-redesign) marks N's children stale to propagate the cascade:

```sql
UPDATE rimsky_nodes
SET state = 'stale',
    frame_id = N.frame_id,
    updated_at = now()
WHERE instance_id = N.instance_id AND id = $child_id
  AND state = 'fresh';   -- only fresh children transition; non-fresh children are already in this frame
```

(If a child is already non-fresh — e.g., another upstream parent reached it first this frame — the UPDATE's `state = 'fresh'` predicate skips it. The cascade message is absorbed; the existing dispatch row already carries the correct `frame_id`.)

A new `rimsky_dispatch` row is enqueued for the child via the existing supervisor commit path; the dispatch insert reads `frame_id` from `rimsky_nodes` for the child node and writes it to the dispatch row.

Pruning: when a node N commits with `changed: false`, the cascade message-passes are skipped — N's children are NOT transitioned to `stale`. They remain `fresh` (or whatever previous state). The frame-end predicate (§4.2) does not see them as in-flight, so the frame ends without them.

Audit trail for pruning: `SELECT FROM rimsky_dispatch WHERE frame_id = $frame_id AND node_id = $child_id` — empty result + child reachable from frame source + an upstream parent committed `changed: false` in this frame = pruned. Operators reconstruct from these primitives. (For richer audit, `rimsky_events` already records cascade events with reasons; that table is unchanged by this spec.)

---

## 5. Configuration

### 5.1 Template grammar

Two fields are added at the top level of the template YAML:

- `frame_resolution` — **required**. One of `coalesce | serial_queue`.
- `frame_timeout_ms` — optional. Integer milliseconds. Default `600000` (10 minutes). Hard floor `60000` (60s). No upper limit.

Control-api rejects template uploads with HTTP 400 if `frame_resolution` is missing or not one of the allowed values, OR if `frame_timeout_ms` is set to a value below the hard floor.

```yaml
# Template YAML
name: my_template
frame_resolution: serial_queue   # required — coalesce | serial_queue
frame_timeout_ms: 600000         # optional — defaults to 600000 (10 minutes); minimum 60000

nodes:
  - id: source
    # ...
```

### 5.2 Template change semantics

If a template's `frame_resolution` is changed (via re-upload), instances created from the new template version use the new mode. Existing in-flight frames retain their `mode` (snapshotted on `rimsky_frames` at frame creation, §10.4) — a frame that started under coalesce finishes under coalesce even if the template was re-uploaded with serial_queue mid-render.

Pre-v1 — break freely. No template-versioning migration needed; if templates need rewriting, rewrite them.

### 5.3 Per-instance overrides

None. `frame_resolution` is per-template, period. If two instances of the same template need different modes, author two templates.

---

## 6. Held claims and frames

The §11.4 holding-subgraph DAG walk and §5.6.4 first-delete-wins / last-released-wins resolution algorithms are unchanged in their core mechanics. Frame integration:

- A held claim acquired by a frame's source node is held through the entire frame's render. The `rimsky_lock_holders` and `rimsky_claim_holders` rows persist while the frame is in flight.
- Resolution at terminal-leaves happens during the terminal-leaf's commit transaction, exactly as in stores-redesign §11.4 / §5.6.4. Frame-end (per §4.1 step 1) does NOT also run claim-resolution — it would double-resolve. Resolution is bound to the terminal-leaf commit, not to the frame transition.
- If a frame ends in `failed` state: any claims still held by failed nodes (or nodes that didn't reach their terminal-leaf) are released by the existing orphan-reap mechanism (§6 stores-redesign; held lock cutoff = 5 × heartbeat_interval, blessed-invariant 6). The held-claim items are returned to `available` state by the claim-store visibility-timeout sweep (§13.5 stores-redesign), not by frame-end logic.

Schema: `rimsky_lock_holders` and `rimsky_claim_holders` gain a `frame_id` column **as a non-key non-unique observability column**. The existing PKs (`(claim_id, holder_node_id)` etc.) stand. The `frame_id` column lets operators query "which frame held claim X?" and lets the post-v1 Rule 3b spec promote `frame_id` into the PK without a destructive migration.

---

## 7. Frame timeouts and stuck frames

### 7.1 Configuration

Per-template: `frame_timeout_ms` integer milliseconds, declared in the template YAML alongside `frame_resolution`. **Default: `600000` (10 minutes) if omitted.** Hard floor: `60000` (60s) — control-api rejects template uploads with smaller values. No upper limit.

The value is snapshotted onto each `rimsky_frames.frame_timeout_ms` row at queue-time, so a mid-flight template re-upload doesn't change in-flight frame timeouts.

### 7.2 Reaper logic

A running frame F is "stuck" if **all** of:

1. `F.started_at + (F.frame_timeout_ms × interval '1 ms') < now()`
2. No live executor work for this frame: `count(*) = 0 FROM rimsky_dispatch WHERE frame_id = F.frame_id AND claimed_by IS NOT NULL`. (A dispatch with `claimed_by IS NOT NULL` plus `rimsky_nodes.state = 'running'` for the same node is the in-flight signal under stores-redesign.)
3. The cascade is wedged: `count(*) > 0 FROM rimsky_nodes WHERE instance_id = F.instance_id AND state IN ('stale','running')` (there are still in-motion nodes; combined with (2), they are not actually being worked on — e.g., a `stale` node whose dispatch nobody claimed, or a `running` node whose supervisor died and orphan-reap hasn't yet acted).

When a stuck frame is detected:
- Inside one transaction:
  - Force all `rimsky_nodes` rows in this instance with `state IN ('stale','running')` to `state = 'failed'` so they don't keep subsequent frames blocked.
  - Transition `rimsky_frames.state = 'failed'`, set `ended_at = now()`.
- Log a structured event (via `rimsky_events`) with `frame_id`, `instance_id`, and the wedged node IDs. This is the operator's audit trail.
- The next scheduler tick advances the queue.

Note: because the per-dispatch heartbeat-loss + orphan-reap mechanism (stores-redesign §13.5) handles the case of a supervisor dying with a claimed dispatch, condition (2) becomes true on its own once orphan-reap runs. The frame-timeout reaper is a higher-level safety net for the case where orphan-reap unwinds a dispatch but the resulting `stale` node is never claimed (e.g., no supervisor accepts its `required_stores`, or a misconfigured template). Without this reaper, such a frame would block its instance forever.

### 7.3 What a stuck frame is NOT

- A frame whose dispatches are running and heartbeating: not stuck. The per-dispatch heartbeat timeout (§5.5 stores-redesign) handles per-node deadlocks. Frame-timeout is the higher-level safety net.
- A frame whose async-handoff callback hasn't arrived: not stuck. The dispatch is still in `running` state (per §12.4 stores-redesign — async handoff keeps the dispatch open). Step (2) of the predicate excludes this case.

---

## 8. Quality-rule failures and frame outcomes

A node that fails its commit-time quality-rule validation (§12.6 stores-redesign) transitions to `state = 'failed'`. This is unchanged.

Frame-end logic (§4.1 step 1) reads the `rimsky_dispatch` outcomes for this frame_id:

- All expected nodes either `completed` or pruned: frame state → `completed`.
- Any expected node `failed` (including quality-rule failures): frame state → `failed`.

A frame ending in `failed` does NOT prevent the next queued frame from running. The next frame starts with the same node-state the failed frame left behind — failed nodes stay failed until something invalidates them. If the next frame's source set re-invalidates the failed nodes, the existing `failed → stale` state-machine transition applies and they get another shot.

---

## 9. Async handoff (§12.4) across frames

An executor that returns `AsyncAccepted` keeps its dispatch open (`claimed_by IS NOT NULL`) until the callback arrives at `POST /v1/callback/{async_ack_id}`. The corresponding `rimsky_nodes` row remains in `state = 'running'` for the duration.

Under Rules 1+3a, frames don't overlap. So:

- Async dispatch issued in frame N → the source node is `running` with a still-claimed dispatch → frame N is in flight (predicate §4.2 sees the node in `running`).
- Frame N cannot end until the async dispatch resolves (callback arrives, or per-dispatch heartbeat-loss triggers orphan-reap, or `frame_timeout_ms` reaps the frame).
- Frame N+1 cannot start until frame N ends.

So async handoff naturally serializes frame execution. No frame-correlation logic needed — the `rimsky_dispatch` row already carries `frame_id` (see §10.2), and the callback resolution path in `core/supervisor/callback.go` doesn't need to be frame-aware.

---

## 10. Schema additions

### 10.1 New table: `rimsky_frames`

```sql
CREATE TABLE rimsky_frames (
    frame_id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID         NOT NULL REFERENCES rimsky_instances(instance_id),
    mode              TEXT         NOT NULL CHECK (mode IN ('coalesce','serial_queue')),
    state             TEXT         NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    source_node_ids   TEXT[]       NOT NULL CHECK (array_length(source_node_ids, 1) >= 1),
    queued_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    ended_at          TIMESTAMPTZ,
    frame_timeout_ms  BIGINT       NOT NULL,    -- snapshotted from instance config at queue-time
    CONSTRAINT chk_running_has_started CHECK (state != 'running' OR started_at IS NOT NULL),
    CONSTRAINT chk_terminal_has_ended  CHECK (state NOT IN ('completed','failed') OR ended_at IS NOT NULL)
);

CREATE INDEX idx_rimsky_frames_queued
    ON rimsky_frames (instance_id, queued_at)
    WHERE state = 'queued';

-- At most one running frame per instance (the per-instance-ordering invariant)
CREATE UNIQUE INDEX uq_rimsky_frames_running
    ON rimsky_frames (instance_id)
    WHERE state = 'running';

-- At most one queued frame per coalesce instance (the pending-coalesce row)
CREATE UNIQUE INDEX uq_rimsky_frames_coalesce_queued
    ON rimsky_frames (instance_id)
    WHERE state = 'queued' AND mode = 'coalesce';
```

### 10.2 Modified table: `rimsky_dispatch`

```sql
ALTER TABLE rimsky_dispatch
    ADD COLUMN frame_id UUID NOT NULL REFERENCES rimsky_frames(frame_id);

CREATE INDEX idx_rimsky_dispatch_frame
    ON rimsky_dispatch (frame_id);
CREATE INDEX idx_rimsky_dispatch_frame_claimed
    ON rimsky_dispatch (frame_id) WHERE claimed_by IS NOT NULL;
```

The stores-redesign `rimsky_dispatch` (§9.6 of that spec) has no `state` column — claimed-ness is encoded by `claimed_by IS NULL`. The frame-aware indexes above mirror the existing index pattern (`rimsky_dispatch_pending_idx` / `rimsky_dispatch_claimed_idx`). The dispatch row's `node_id`, `enqueued_at`, `claimed_by`, `claimed_at`, `last_heartbeat_at`, `required_stores` columns are unchanged.

### 10.3 Modified table: `rimsky_nodes`

`kill_requested` is removed (see §11). `frame_id` is added as the per-node "which frame is this node currently in motion for."

```sql
ALTER TABLE rimsky_nodes DROP COLUMN kill_requested;

ALTER TABLE rimsky_nodes
    ADD COLUMN frame_id UUID REFERENCES rimsky_frames(frame_id);
-- Nullable: a fresh node with no in-flight render has frame_id = NULL.
-- Set when state transitions fresh|failed → stale (frame-start or cascade message-pass).
-- Preserved across stale → running → fresh|failed transitions for the same frame.
-- Cleared (set NULL) when state transitions to 'fresh'.
-- Preserved on terminal 'failed' (so operators can see which frame the failure belongs to).

CREATE INDEX idx_rimsky_nodes_frame_state
    ON rimsky_nodes (frame_id, state)
    WHERE state IN ('stale','running');
```

The `rimsky_nodes` columns `id`, `instance_id`, `node_type`, `state`, `assigned_supervisor_id`, `last_heartbeat_at`, `updated_at`, etc., are unchanged.

### 10.4 Modified tables: `rimsky_lock_holders`, `rimsky_claim_holders`

```sql
ALTER TABLE rimsky_lock_holders   ADD COLUMN frame_id UUID;
ALTER TABLE rimsky_claim_holders  ADD COLUMN frame_id UUID;
-- Nullable, non-key. Observability + forward-compat for Rule 3b.
-- Existing PKs and unique indexes (rimsky_claim_holders_claim_node_idx, etc.) unchanged;
-- v1 logic does not key on frame_id.
-- Populated on hold creation by the supervisor; not read or enforced by core release/resolve paths.
```

### 10.5 Cascade-message table

The existing rimsky cascade is implicit — node-to-node message-passing happens via direct `rimsky_nodes.state` updates inside the supervisor's commit transaction (§13.5–13.7 stores-redesign). There is no `rimsky_messages` table. No schema change here.

### 10.6 Forward-compatibility for Rule 3b

Rule 3b would add:

- `rimsky_frame_node_attributes(frame_id, node_id, attributes JSONB, PRIMARY KEY (frame_id, node_id))` — per-frame attribute snapshots.
- Promote `frame_id` to the PK on `rimsky_lock_holders` (alongside `id`) and `rimsky_claim_holders` (replacing `rimsky_claim_holders_claim_node_idx` with `(claim_id, holder_node_id, frame_id)`).
- Drop the `uq_rimsky_frames_running` unique index (parallel running frames per instance).
- Add a topological-gating dispatch-eligibility check.

None of these require destructive migration of v1 data. The v1 schema is forward-compatible.

---

## 11. Removal of `kill_requested`

The current `kill_requested BOOLEAN` column on `rimsky_nodes` and the supervisor-side polling path (`core/supervisor/runner_dispatch.go::isKillRequested`, which reads `rimsky_nodes.kill_requested`) are deleted by this spec.

- `ALTER TABLE rimsky_nodes DROP COLUMN kill_requested` (in the same migration that adds `frame_id`; see §10.3).
- Remove `isKillRequested` from `core/supervisor/runner_dispatch.go` and its callers in the executor stream-recv loop.
- Remove the heartbeat-tick poll of `rimsky_nodes.kill_requested` in `core/supervisor/supervisor.go` (the `runLoop`'s heartbeat case, per stores-redesign §13.4 "the same tick polls `rimsky_nodes.kill_requested` for nodes assigned to this supervisor; if set, the runner signals the executor's cancel token / SIGTERM and drives the give-up path"). Replace with a no-op; in-flight nodes are not cancelled by operator action under the frame model.
- Remove the control-api `nodes.go` route logic that sets `kill_requested = true` on operator-originated invalidate reasons prefixed with `"operator"`. Operator invalidates now go through `frame.EnqueueOrCoalesce` (§13.2); they do not preempt running work.
- Update `CLAUDE.md`'s gotcha "Operator-originated invalidates set kill_requested=true" to "Operator-originated invalidates enqueue (serial_queue) or coalesce (coalesce mode) a new frame; in-flight work is never preempted."
- Update the package-doc comments in `core/supervisor/supervisor.go` and `core/supervisor/runner_dispatch.go` to remove references to kill-poll.

---

## 12. Migration

**Pre-v1; rules.md says break freely; no production data to preserve.**

A single migration (numbered next-after-the-stores-redesign-migration) does:

1. `CREATE TABLE rimsky_frames` and its indexes (§10.1).
2. `ALTER TABLE rimsky_dispatch ADD COLUMN frame_id …` (§10.2). Best-effort backfill of existing rows: assign a synthetic frame_id by inserting a `rimsky_frames` row in `failed` state per affected instance, or fail the migration loud (acceptable on dev DBs).
3. `ALTER TABLE rimsky_nodes DROP COLUMN kill_requested; ADD COLUMN frame_id …` (§10.3, §11).
4. `ALTER TABLE rimsky_lock_holders ADD COLUMN frame_id …` (§10.4).
5. `ALTER TABLE rimsky_claim_holders ADD COLUMN frame_id …` (§10.4).
6. Mark any non-terminal `rimsky_nodes` rows (`state IN ('stale','running')`) as `state = 'failed'` so existing in-flight cascades are abandoned cleanly rather than carrying NULL frame_id forward into the new model.

Acceptable to nuke dev DBs entirely if the migration runner refuses any of the above (rules.md "Pre-v1 — break freely").

Operator-side migration:

- Re-upload all templates with `frame_resolution: <coalesce|serial_queue>` declared. Templates without the field are rejected by control-api.
- Restart all three rimsky processes (scheduler, supervisor, control-api).

The migration is destructive to in-flight work but non-destructive to template definitions (other than requiring the new field).

---

## 13. Producer integration

### 13.1 Producer helper: `frame.EnqueueOrCoalesce`

All producers (schedule_ticker, control-api invalidate route, admin force-fire) call a single helper:

```go
// core/frame/producer.go
func EnqueueOrCoalesce(ctx context.Context, tx pgx.Tx,
    instanceID uuid.UUID, sourceNodeID uuid.UUID) (uuid.UUID, error)
```

The helper:

1. Reads the template's `frame_resolution` and `frame_timeout_ms` for the instance (joining `rimsky_instances → rimsky_templates`).
2. **`serial_queue` branch:** unconditionally `INSERT INTO rimsky_frames(instance_id, mode, state, source_node_ids, queued_at, frame_timeout_ms) VALUES (…, 'serial_queue', 'queued', ARRAY[sourceNodeID], now(), $timeout)`. Returns the new frame_id.
3. **`coalesce` branch:** in one statement (using `INSERT … ON CONFLICT (instance_id) WHERE state = 'queued' AND mode = 'coalesce' DO UPDATE` to match the partial unique index `uq_rimsky_frames_coalesce_queued` from §10.1):
   - If a `running` frame exists for this instance AND a `queued` coalesce row exists: append `sourceNodeID` to the queued row's `source_node_ids` (deduped).
   - If a `running` frame exists AND no `queued` coalesce row exists: insert a `queued` coalesce row with the source.
   - If no `running` frame exists: insert a `queued` coalesce row with the source. (The next scheduler tick will transition it to `running` per §4.1 step 3.)

Both modes therefore go through the same `queued → running` advancement on scheduler tick (§4.3). This means a coalesce instance has a one-tick latency between the first invalidate and the start of rendering — acceptable, and operationally simpler than special-casing "first invalidate goes straight to running."

Producers DO NOT directly write `rimsky_nodes.state = 'stale'`; that write happens inside the scheduler's frame-start tx (§4.3 step 2).

### 13.2 Schedule firing (`core/scheduler/schedule_ticker.go`)

When the schedule_ticker fires a scheduled node, it calls `frame.EnqueueOrCoalesce`. The existing no-backfill behavior (single trailing fire on outage, §14 stores-redesign) is preserved — the schedule_ticker emits one call per cron firing window, regardless of mode.

### 13.3 Operator invalidate (`POST /v1/nodes/{node_id}/invalidate`)

The control-api's nodes route calls `frame.EnqueueOrCoalesce` instead of writing directly to `rimsky_nodes.state`. No queue-jumping; operator invalidates are FIFO under serial_queue and coalesce-joining under coalesce.

### 13.4 Force-fire (`POST /admin/scheduled-nodes/{node_id}/force-fire`)

The admin handler currently runs `UPDATE rimsky_schedules SET next_fire_at = now() WHERE node_id = $1` and returns 204 (stores-redesign §16.1). Behaviour preserved — the schedule_ticker picks up the row at the next tick and calls `frame.EnqueueOrCoalesce`. No direct frame-engine call from the admin handler.

**Smoke-fixture interaction.** The §19.2 stores-redesign smoke fixture issues 100 sequential force-fires, waiting between each fire for the source node to transition `running → fresh` (or `failed`). Under `frame_resolution: serial_queue`, this wait is satisfied when the source's dispatch in frame N commits — but frame N is not yet ended (downstream nodes scope/draft/review are still rendering). The next force-fire enqueues frame N+1; frame N+1 stays `queued` while frame N is still in flight; advances on a later scheduler tick once frame N's downstream completes. The wait condition functions as a pacing throttle but does not bound queue depth — under sufficiently slow downstream work, multiple frames can accumulate `queued`. The acceptance predicate (≥100 terminal commits) holds because every fire produces exactly one frame, every frame has exactly one terminal-leaf commit (the review node), and serial-queue ordering guarantees all 100 frames complete before the test concludes.

### 13.5 Cascade message-pass

Internal cascade (a node committing → its children become `stale` in the same supervisor tx) is NOT a producer event. It propagates the existing frame's `frame_id` from `rimsky_nodes` directly (§4.4). No `frame.EnqueueOrCoalesce` call.

---

## 14. Supervisor integration

### 14.1 Dispatch claim (§13.3 stores-redesign)

When a supervisor claims a stale node, the existing acquisition transaction (stores-redesign §13.3) is augmented:

1. The dispatch SELECT reads `rimsky_dispatch.frame_id` along with the existing columns.
2. The dispatch insert (or update of `claimed_by`) preserves the existing `frame_id` value — `frame_id` was set at dispatch enqueue time (in the supervisor's commit tx for cascade dispatches, or in the scheduler's frame-start tx for source dispatches; see §4.3 / §4.4).
3. Inserts of `rimsky_lock_holders` rows include `frame_id` (observability column, §10.4) sourced from the claimed dispatch row.
4. The transaction continues to commit all of (claim dispatch, insert lock-holders, complete store mutations) or none — blessed-invariant 10 unchanged.

If a claimed dispatch's `frame_id` is NULL (defensive check; should be impossible), the supervisor logs a structured warning and bails as `frame_id_null`. This is a bug; fail loud.

### 14.2 Terminal commit (§13.5–13.7 stores-redesign)

When the supervisor commits a node terminal:

- Cascade message-passes to children propagate `frame_id` from this node's `rimsky_nodes.frame_id` (§4.4).
- For `completed` outcomes: `rimsky_nodes.state = 'fresh', frame_id = NULL` (the node is no longer in flight; the frame relationship is severed).
- For `failed` outcomes: `rimsky_nodes.state = 'failed', frame_id = <preserved>` — so operators can audit which frame the failure belongs to.

### 14.3 Async handoff resume

When the async-callback path (`core/supervisor/callback.go`) resolves a deferred dispatch, it inherits `frame_id` from the existing `rimsky_dispatch` row (no new lookup needed) and proceeds with the existing terminal-commit logic. No frame-aware code change in the callback path beyond ensuring the cascade message-pass uses the dispatch's `frame_id` (which it already will, via §4.4's update).

---

## 15. Removed code paths

This spec removes:

1. `rimsky_nodes.kill_requested` column (§10.3, §11).
2. `core/supervisor/runner_dispatch.go::isKillRequested` and its caller in the executor stream-recv loop.
3. The heartbeat-tick poll of `rimsky_nodes.kill_requested` in `core/supervisor/supervisor.go`'s `runLoop` (currently signals cancel_token / SIGTERM on assigned-running nodes per stores-redesign §13.4).
4. The `nodes.go` operator-invalidate path that sets `kill_requested = true` on reason-prefix `"operator"`. Replaced by the producer logic in §13.3 (call `frame.EnqueueOrCoalesce`).
5. Any docstring / CLAUDE.md / `docs/operator-guide.md` / `docs/architecture.md` text that asserts "operator invalidates kill in-flight work" — replaced by "operator invalidates enqueue (serial_queue) or coalesce (coalesce mode) a new frame; in-flight work is never preempted."

---

## 16. File inventory (informative; the plan will detail edits)

New files:

- `core/migrations/00X-frame-resolution.sql` — the migration in §12.
- `core/frame/frame.go` (or under `core/scheduler/frame.go`) — the frame-engine logic invoked by scheduler tick (§4).
- `core/frame/producer.go` — the `EnqueueOrCoalesce(ctx, instance_id, source_node_id) → frame_id` helper called by schedule_ticker, control-api/nodes.go, and force-fire.
- `test/scenarios/frame_resolution/` — frame-resolution scenario tests (§17).

Modified files:

- `core/scheduler/scheduler.go` — adds frame-engine logic (§4.1) to the tick loop, under the existing `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`.
- `core/scheduler/schedule_ticker.go` — schedule firing calls `frame.EnqueueOrCoalesce` (§13.2).
- `core/controlapi/nodes.go` — operator invalidate route calls `frame.EnqueueOrCoalesce` (§13.3); remove `kill_requested = true` write.
- `core/controlapi/admin_force_fire.go` — no logic change; the existing `UPDATE rimsky_schedules SET next_fire_at = now()` triggers the schedule_ticker on the next tick (§13.4).
- `core/supervisor/runner.go`, `runner_acquire.go`, `runner_dispatch.go`, `runner_terminal.go` — propagate `frame_id` through dispatch claims, executor calls, terminal commits, and cascade message-passes (§14); remove `isKillRequested` poll path (§11).
- `core/supervisor/supervisor.go` — remove `rimsky_nodes.kill_requested` poll from the heartbeat tick (§11). Update package doc.
- `core/supervisor/callback.go` — no logic change; doc note that `frame_id` rides the existing dispatch row.
- `core/queue/postgres/queue.go` — the canonical `rimsky_dispatch` SQL surface (per CLAUDE.md blessed-invariants 2/3/4/10). Adds `frame_id` to dispatch reads/writes; new index helpers; updates the orphan-reap and claim queries that gain a `frame_id` predicate where useful.
- `core/storage/postgres/nodes.go` (or wherever `rimsky_nodes` reads/writes live in the post-stores-redesign codebase — implementer reconciles) — `frame_id` column reads/writes; remove `kill_requested` column references.
- `core/store/lockholders.go` (the unified rimsky_lock_holders helper, per stores-redesign §16.2), `core/store/claimstorepg/holders.go` — `frame_id` observability column on insert (read paths unchanged).
- `core/node/template.go` and `core/node/template_validator.go` (the template-parsing and template-validation surface; the controlapi route is `core/controlapi/templates.go`) — parse `frame_resolution` and `frame_timeout_ms`; validate against allowed values; reject template uploads missing `frame_resolution` or with `frame_timeout_ms < 60000`.
- `CLAUDE.md` — gotchas section update per §21 item 4; new "Where to look first" entry for this spec.
- `docs/architecture.md`, `docs/operator-guide.md`, `docs/node-graph-design.md` — describe the frame model. (`docs/protocol.md` does not change — frames are supervisor-internal.)
- `CHANGELOG.md` — bullet under `## Unreleased`.
- `test/smoke/fixtures/template.yml` — add `frame_resolution: serial_queue` (also `frame_timeout_ms: 600000` if non-default).
- `test/smoke/stores_redesign_smoke_test.go` — no source change; the wait-for-source-fresh pacing remains; the acceptance predicate (≥100 terminal commits) remains.

---

## 17. Tests

### 17.1 Scenario tests (real Postgres via testcontainers)

In `test/scenarios/frame_resolution/`:

1. **`serial_queue_each_invalidate_one_frame_test.go`** — fire 10 invalidates rapidly under serial_queue; assert 10 frames queued, all 10 render, all 10 produce terminal commits.

2. **`coalesce_collapses_invalidates_test.go`** — fire 10 invalidates rapidly under coalesce; assert 2 frames render total (the first running frame, plus one trailing coalesce frame). Assert `source_node_ids` on the trailing frame contains all distinct sources from the 9 mid-render invalidates.

3. **`frame_in_flight_blocks_next_serial_queue_test.go`** — start frame N (slow executor, 5s), fire invalidate while it runs; assert frame N+1 is `queued` not `running` while N is in flight. After N completes, assert N+1 transitions to `running` on the next scheduler tick.

4. **`frame_in_flight_pending_coalesce_test.go`** — same as above but with coalesce; assert exactly one `queued` row exists at any time during the running frame, regardless of how many invalidates fire (enforced by `uq_rimsky_frames_coalesce_queued`).

5. **`frame_end_after_async_callback_test.go`** — node returns AsyncAccepted; frame stays in flight (predicate §4.2 sees the node in `running`); callback arrives; frame ends; queued frame advances. Assert frame_id matches between dispatch, callback, and frame row.

6. **`frame_timeout_reaper_test.go`** — frame with no live work but a wedged `stale` `rimsky_nodes` row (simulated by inserting state='stale' with frame_id but no supervisor accepting its required_stores). Assert reaper transitions frame to `failed` after `frame_timeout_ms`. Assert wedged node row transitions to `state='failed'`. Assert the next queued frame proceeds on the following tick.

7. **`pruned_node_does_not_block_frame_end_test.go`** — node committing `changed: false` halts cascade to a downstream branch. Assert frame ends without those downstream nodes ever entering `stale`. Assert `rimsky_dispatch` has no rows for the pruned nodes for this `frame_id`.

8. **`held_claim_resolution_at_frame_end_test.go`** — held claim acquired by frame source; terminal-leaf commits and resolves the claim within the frame. Assert `rimsky_claim_holders.frame_id` is set to the frame's id (observability). Assert resolution happens at terminal-leaf commit (not at frame-end — i.e., the §5.6.4 algorithm fires once, in the terminal-leaf's tx).

9. **`failed_node_marks_frame_failed_test.go`** — expected-set node enters `failed` (executor error or quality-rule violation per §12.6 stores-redesign); frame ends with `state = 'failed'`; next queued frame proceeds. Assert `rimsky_nodes.frame_id` is preserved on the failed node.

10. **`template_missing_frame_resolution_rejected_test.go`** — control-api template-upload route; submit template without `frame_resolution`; assert HTTP 400 with an error mentioning the field. Then submit with an invalid value (e.g., `"abort"`); assert 400.

11. **`per_instance_ordering_invariant_test.go`** — concurrent invalidates from multiple producers (force-fire + operator invalidate concurrently). Assert at most one `running` frame per instance at all times — enforced by `uq_rimsky_frames_running`. The test attempts to insert two `running` rows directly via SQL and asserts the second fails with a unique-violation.

12. **`frame_start_atomicity_test.go`** (covers blessed invariant 18) — concurrent scheduler ticks attempt the queued→running CAS for the same frame. Assert only one succeeds (the other's tx rolls back). Assert that on the success path, `rimsky_frames.state = 'running'`, `rimsky_frames.started_at IS NOT NULL`, AND every `source_node_id` has `rimsky_nodes.state = 'stale'` with the matching `frame_id` — all visible from a single read after commit.

13. **`no_null_frame_id_on_in_flight_dispatch_test.go`** (covers blessed invariant 19) — drive a render through to a mid-flight state. Query `SELECT count(*) FROM rimsky_dispatch WHERE frame_id IS NULL`; assert 0. Then query `SELECT count(*) FROM rimsky_nodes WHERE state IN ('stale','running') AND frame_id IS NULL`; assert 0.

### 17.2 Unit tests

- `core/frame/producer_test.go` — `EnqueueOrCoalesce` semantics for both modes, with table-driven cases covering all of §13.1's coalesce branches (no running frame, running with no queued, running with queued) plus the unconditional serial_queue insert.
- `core/scheduler/frame_test.go` — frame-end predicate, queued→running transition, stuck-frame reaper. Use stub stores; no real Postgres needed.
- `core/node/template_validator_test.go` — `frame_resolution` parsing, validation, missing-field rejection, `frame_timeout_ms` floor check.

### 17.3 Smoke test (§19.2 stores-redesign)

`test/smoke/stores_redesign_smoke_test.go` — no source change. Add `frame_resolution: serial_queue` to `test/smoke/fixtures/template.yml`. Run the existing test; assertion is still ≥100 terminal commits over 100 force-fires. Should pass cleanly under the new model.

---

## 18. Invariants (annotated `@blessed-invariant` in source)

This spec adds five new blessed invariants, augmenting the 14 from stores-redesign:

15. **Frame-resolution mode is mandatory and per-template.** Control-api rejects template uploads missing `frame_resolution`; the field is one of `coalesce | serial_queue`. (`core/node/template_validator.go`, `core/controlapi/templates.go`) — verified by §17.1 test 10.

16. **Per-instance ordering: at most one `running` frame per instance.** Enforced by `uq_rimsky_frames_running`. (`core/migrations/00X-frame-resolution.sql`) — verified by §17.1 test 11.

17. **At most one `queued` coalesce frame per instance.** The pending-coalesce row. Enforced by `uq_rimsky_frames_coalesce_queued`. (Same migration.) — verified by §17.1 test 4.

18. **Frame-start atomicity.** Queued→running transition AND source-node `state = 'stale', frame_id = $frame_id` writes happen in one transaction. (`core/frame/frame.go`) — verified by §17.1 test 12.

19. **`frame_id` flows with cascade.** No `rimsky_dispatch` row has `frame_id IS NULL`. No `rimsky_nodes` row in state `stale` or `running` has `frame_id IS NULL`. (`core/supervisor/runner.go`, `core/supervisor/runner_acquire.go`, `core/scheduler/frame.go`) — verified by §17.1 test 13.

---

## 19. Acceptance criteria

The implementation is complete when:

1. All scenario tests in §17.1 pass.
2. All unit tests in §17.2 pass.
3. The §19.2 stores-redesign smoke test passes — 100 force-fires produce ≥100 terminal commits — with `frame_resolution: serial_queue` declared on the smoke template.
4. `go build ./...`, `go test ./...`, `make lint` clean.
5. Race-mode runs of frame-relevant packages pass: `go test ./core/scheduler/... ./core/supervisor/... ./core/frame/... ./test/scenarios/frame_resolution/... -race -count=3`.
6. Documentation updated per `rules.md` "After Code Changes" checklist:
   - `docs/architecture.md` — describes the frame engine and scheduler ownership.
   - `docs/protocol.md` — no wire change; note that supervisor-internal `frame_id` exists.
   - `docs/node-graph-design.md` — adds the frame concept to the conceptual model.
   - `docs/operator-guide.md` — describes `frame_resolution` template field, frame-state observability via the new table, frame-timeout config.
   - `CLAUDE.md` — gotchas section updated (kill_requested removed; frame model added).
   - `CHANGELOG.md` — entry under `## Unreleased`.
7. The Helm chart and docker-compose reference deployments come up cleanly post-migration.

---

## 20. Out of scope (recap)

- Rule 2 (preemptive abort) — not supported in v1; rationale in §2.
- Rule 3b (parallel buffered) — post-v1; schema is forward-compatible (§10.6).
- Operator queue-jumping / priority — post-v1.
- Bounded queue with overflow policy — post-v1.
- Producer-side coalescing beyond what schedule_ticker already does (no-backfill) — post-v1; the schedule_ticker behavior is preserved as-is.
- Per-frame attribute snapshots — only relevant for Rule 3b; not in v1.
- Operator-driven "abort the current frame" — explicitly not supported; would require side-effect rollback we cannot guarantee.

---

## 21. Open items for the implementing agent

These are minor surfaces the implementer verifies during implementation; they are not deferred design decisions:

1. The migration's number is the next free integer after the most recent stores-redesign migration in `core/migrations/`. The implementer reads the directory and picks the next number.
2. Any existing `test/scenarios/` test that relied on `kill_requested`-driven preemption (e.g., operator-invalidate-cancels-running-node scenarios) is updated to assert the new behaviour: operator invalidate enqueues/coalesces a frame; in-flight work continues to completion. The implementer scans `test/scenarios/` and `core/supervisor/*_test.go` for `kill_requested` references and updates each.
3. The control-api error message format for a missing or invalid `frame_resolution` field follows whatever validation-error pattern the existing template-upload path uses (single-error vs multi-error response); the implementer matches the existing pattern.
4. The CLAUDE.md gotchas section is updated to remove the kill_requested gotcha and add a new gotcha covering the frame model. Exact wording is the implementer's call; the substance is "operator invalidates do not preempt; they enqueue or coalesce a frame; the kill_requested column is gone."

End of spec.
