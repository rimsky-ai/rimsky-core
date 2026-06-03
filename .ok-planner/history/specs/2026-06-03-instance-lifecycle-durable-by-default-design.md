# Instance Lifecycle: Durable-by-Default + Frame-End Correctness + Trace Retention — Design Spec

**Date:** 2026-06-03
**Status:** Approved design (authorization to plan)
**Source sketch:** `.ok-planner/sketches/2026-06-03-instance-lifecycle-durable-by-default-sketch.md`

## Summary

Three coupled changes to instance lifecycle, surfaced together because they
share one root cause — the platform never wrote down precisely when a frame
*ends*, and the auto-terminate-on-drain behavior that grew on top of that
ambiguity contradicts the intended durable-by-default instance model:

1. **Durable-by-default instances.** An instance is durable by default: once
   created from a template, it lives until force-terminate. Auto-termination
   becomes an opt-in per-instance flag, `terminate_after_run`, with strict
   "terminate after the next frame ends" semantics. The current
   unconditional auto-terminate-on-drain — and the publisher-subscription
   coupling layered on top of it — are removed.

2. **Frame-end correctness.** A frame ends only when *all* its node_runs are
   resolved. A `parked` node_run (awaiting an async callback or snooze wake)
   is **not** resolved, so it holds its frame open. The current frame-end and
   instance-terminal predicates wrongly treat `parked` as "not contributing,"
   so a frame drains to completion — and an instance can terminate — while a
   node is parked. This is corrected in both drivers.

3. **Trace retention.** Durable-by-default removes the implicit cleanup that
   auto-terminate-then-delete used to perform (cascade-deleting an instance's
   frames, node_runs, and events). The execution trace — frames + node_runs +
   event logs — is brought under one coherent per-instance retention policy:
   a trailing time window, a most-recent-frames count cap, or the lesser of
   both. This also closes a pre-existing gap: the audit event log has no
   retention at all today.

## Motivation

An *instance* is the live, durable deployment of a template; durability is
what distinguishes it from a one-shot run. An instance runs whenever one of
its nodes is invalidated, each invalidation resolves in a frame, and a single
instance resolves many frames over its life. Nothing should terminate it on
its own.

Today, `MarkInstanceTerminatedIfDone` runs at every frame-end, for every
instance, and stamps `terminated_at` as soon as the instance's work settles —
making every instance behave like a batch job. This is longstanding
(control-plane v1) but was never written into the `instance` concept, which
documents force-terminate as the production path to terminal and never
mentions an on-drain path. A recent acceptance-coverage test (a real sensor
driving a node repeatedly) was the first to exercise a genuinely long-lived
instance and surfaced the conflict; the fix applied there coupled termination
to the presence of an active publisher-subscription — patching the symptom by
binding termination to sensors, exactly the coupling the durable model
forbids.

Pinning down "when does a frame end" exposed the parked-node defect, and
flipping the lifecycle default exposed the retention gap. All three are in
scope (a spec is a unit of work, not a single feature).

## The frame model (foundational)

This spec depends on a crisp statement of the frame lifecycle, which the
current `frame` concept states ambiguously (its "What it is" prose says a
frame ends "when no run remains stale or running" — omitting `parked` —
while its "Held frames" section says a parked node_run holds the frame open;
the two contradict, and the code followed the wrong half).

The authoritative model:

- A frame **begins** only when a node is invalidated. Invalidation happens
  for exactly two reasons: a direct operator/user invalidation, or the
  delivery of a message. Nothing else begins a frame.
- A frame **ends** only when every node_run in it is resolved — no node_run
  remains `stale`, `running`, or `parked`. No exceptions.
- A `parked` node_run holds its frame open (the frame stays `running`/held).
  Resuming a parked node — *park-wake*, via async callback or snooze timer —
  does **not** begin a frame; it resumes the still-running frame the parked
  node belongs to. "Begins a frame" is distinct from "causes a frame to
  execute."
- Frames are strictly sequential per instance (≤1 running frame, enforced by
  a partial-uniqueness index) and never cross or sub-divide instances.
- A RunScope is **contained** within its frame — the stack of node_runs
  backing subgraph and recursion within that one frame. Every frame contains
  at least one RunScope; RunScopes never cross a frame or instance boundary.
  (The 2026-05-22 frame note's phrase "a frame spans multiple RunScopes" is
  the source of the confusion: read "contains," not "spans.")

The park-timeout sweep is the safety valve that keeps a held frame from
hanging forever: on park-timeout it transitions `parked → failed`, which
resolves the node and lets the frame end normally.

## Goals

- Instances are durable by default; only `terminate_after_run` instances
  self-terminate, and only after their next frame ends.
- Frame-end and instance-termination never fire while a node_run is `parked`.
- The execution trace is retained under one coherent, operator-tunable policy
  and cannot grow without bound on a long-lived durable instance.
- The `frame`, `instance`, and `event-log` concept docs state these
  semantics precisely.

## Non-goals

- No change to force-terminate or delete semantics (`handleTerminateInstance`
  stamps `terminated_at` unconditionally; delete reaps a terminal instance
  and frees the instance key). Those stay as-is.
- No redesign of frames, node_runs, the `paused` state, the publisher/sensor
  protocol, or held-durable claim release (the asset-delete and
  instance-termination release paths are unchanged; durable-committed claims
  remain retention-exempt — they are the asset surface, and durable-by-default
  is precisely what makes a cross-frame claim meaningful).
- No template-level default for the flag (per-instance only); a template
  default can be added later if a real need appears.

---

## Design

### 1. The `terminate_after_run` flag

A per-instance boolean, sourced from the instance-create request, default
`false`. The name deliberately avoids `auto_terminate` / `auto-terminal`,
which already names an unrelated concept (the held-claim Commit/Abandon
resolution mechanism) — reusing it would overload a load-bearing word.

**Wire + thread-through** (each step mirrors the existing `paused` flag):

- `createInstanceRequest` (`lib/control/controlapi/instances.go`): add
  `TerminateAfterRun bool \`json:"terminate_after_run,omitempty"\``.
- `provisionArgs` (same file): add `TerminateAfterRun bool`; set it from
  `body.TerminateAfterRun` in `handleCreateInstance` and pass to
  `provisionInstanceTx`.
- `InstanceCreateInput` (`lib/foundation/persistence/instances.go`): add
  `TerminateAfterRun bool`; the postgres + sqlite `Create` INSERTs add the
  `terminate_after_run` column to their column list and bind the value.
- `InstanceRow` (same file): add `TerminateAfterRun bool \`json:"terminate_after_run"\``;
  both drivers' `scanInstance` / `instanceCols` read the new column.
- `instanceItem` + `toInstanceItem` (`lib/control/controlapi/instances.go`):
  surface `terminate_after_run` on the GET/list projections.

Idempotent re-create (same `template_hash` + `instance_key`) ignores the flag
on the request and returns the existing row's value, exactly as `paused`
behaves.

### 2. Strict terminal semantics

`terminate_after_run` means **run at most one more time, then terminate** —
the instance terminates after the next frame *ends*, regardless of whether
other frames are queued. (That another frame is queued at that instant is
arbitrary; the strict meaning is the useful one now, and richer modes —
drain-to-quiet, count-based — can be added later behind the same flag or a
renamed successor. See the `instance` concept Notes.)

Because the terminal check runs only at frame-end (from `transitionFrameEnd`
in `lib/graph/frame/engine.go`), and a frame now ends only when all its
node_runs are resolved (§3), the strict semantics fall out cleanly: at a real
frame-end the instance has completed exactly one run, so termination is
correct by construction and can never fire mid-run or while parked.

`MarkInstanceTerminatedIfDone` (postgres `lib/foundation/persistence/postgres/frames.go`,
sqlite `lib/foundation/persistence/sqlite/frames.go`) is rewritten to:

- **Gate on** `terminate_after_run = true` (durable instances are never
  touched).
- **Drop** the `rimsky_publisher_subscriptions` clause entirely (reverting the
  coverage plan's symptom fix — termination is independent of sensors).
- **Drop** the queued/running-frames clause (this is the strict-A change:
  termination does not wait for queued frames to drain).
- **Keep and extend** the in-flight node_run guard so it also excludes
  `parked` rows — a defensive restatement of the §3 invariant at the instance
  level, so termination can never fire while any node_run is unresolved
  (`stale`, `running`, or `parked`).

Resulting predicate (postgres form; sqlite mirrors):

```sql
UPDATE rimsky_instances i
SET terminated_at = now()
WHERE i.id = $1
  AND i.terminated_at IS NULL
  AND i.terminate_after_run = true
  AND NOT EXISTS (
      SELECT 1 FROM rimsky_node_runs r
      JOIN rimsky_nodes n ON n.id = r.node_id
      WHERE n.instance_id = i.id
        AND (
             (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
          OR r.phase = 'parked'
          OR r.state = 'parked'
        )
  )
```

**Orphaned queued frames.** Under strict-A, an instance can terminate while a
frame is still `queued` (a message arrived mid-run). That queued frame must
never be promoted against a terminated instance. `ListQueuedFramesReadyToStart`
(both drivers) adds a `NOT EXISTS`/join guard excluding instances with
`terminated_at IS NOT NULL`. The orphaned frame row is cleaned up by the
instance's eventual delete (cascade) and by trace retention (§4); it simply
never runs.

### 3. Frame-end correctness (parked holds the frame)

A `parked` node_run carries `phase='parked'`, `state='parked'` (set by
`applyTerminalPark`; the wake path resets `phase='parked'→'pending'`,
`state='parked'→'stale'`). The frame-end detector
`ListRunningFramesNoPendingNodes` (both drivers) currently selects a running
frame when no node_run is `phase IN ('pending','active','held') AND state IN
('stale','running')` — which excludes `parked`, so a frame drains to
`completed` while a node is parked.

Fix: a `parked` node_run keeps the frame open. The `NOT EXISTS` predicate
gains the same parked-aware clause used in §2:

```sql
SELECT f.frame_id, f.instance_id
FROM rimsky_frames f
WHERE f.state = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM rimsky_node_runs r
      WHERE r.frame_id = f.frame_id
        AND (
             (r.phase IN ('pending','active','held') AND r.state IN ('stale','running'))
          OR r.phase = 'parked'
          OR r.state = 'parked'
        )
  )
```

Consequences:

- The frame stays `running`/held until the parked node resumes (callback /
  snooze) or the park-timeout sweep fails it — both of which resolve the node
  and let the frame end normally.
- The async park-wake path (`wakeParkedNode`) uses the node's recorded
  `frame_id`; with this fix that frame is still `running` when the wake lands,
  so park-wake genuinely "resumes a running frame" as the model states (today
  it can reference an already-`completed` frame).
- The held-frames diagnostic (`CountHeldFrames`, `f.state='running' AND
  d.phase='parked'`) becomes consistent with the frame-end rule rather than
  racing it.

**Predicate audit.** The implementer must audit every frame / node-run
"is there still work" predicate for parked-consistency, since several exist
with subtly different phase/state sets (e.g. the stuck-frame warning query and
the queued-advance path). Any predicate whose intent is "does this frame still
have unresolved work" must count `parked`; any whose intent is "is there
work eligible to dispatch *now*" must not. Each call site is reviewed and the
intended set documented inline.

### 4. Trace retention

"Audit" is the full per-instance execution trace: frames + their node_runs +
the event logs. These are retained as one thing under one policy, replacing
three inconsistent behaviors today (frame rows kept forever "for
observability"; node_runs capped at most-recent-N frames; audit events never
reaped at all).

**Policy.** Per instance, retain the trace by the **lesser of**:

- a trailing **time window** (`cfg:retention.trace_trailing`, a duration), and
- a most-recent-frames **count cap** (`cfg:retention.recent_frames_kept`, the
  existing knob, repurposed from a node-run-only cap to a whole-trace cap).

A frame (and its node_runs) is reaped when it is older than the time window
**or** beyond the most-recent count cap — whichever retains less. If only one
knob is set, that one applies; if neither is set, no structural reaping
occurs. In-flight frames (`queued`, `running`, including parked-held) are
always exempt — nothing live is ever reaped.

**Structural rows** (`rimsky_frames` + `rimsky_node_runs`): the retention
sweep computes the per-instance reap-set of terminal frames (older-than-window
OR beyond-count) and deletes those frame rows; their node_runs go via the
existing `ON DELETE CASCADE`. This subsumes today's run-only prune
(`PruneOldRunsForRetention`), which is replaced by a frame-row-deleting sweep.

**Event logs** are time-keyed, not frame-keyed, so they are retained by the
time window alone:

- `rimsky_events` (audit log) has `occurred_at` and FKs to `instance_id` +
  `node_id` (both `ON DELETE CASCADE`); it carries no `frame_id`, so it cannot
  dangle on a frame reap. Reap by `occurred_at < now - trace_trailing`.
- `rimsky_node_events` (named events) has `emitted_at` (and a non-FK
  `frame_id`). Reap by `emitted_at < now - trace_trailing`.

The count-cap dimension applies only to structural rows; event logs age out by
time only. A frame trimmed early by the count cap leaves its audit narrative
in place for the rest of the window — acceptable, since audit events reference
surviving `node_id`/`instance_id` rows, never a frame FK.

**Plumbing:**

- `RetentionConfig` (`lib/runtime/retention_sweeps.go`): add `TraceTrailing
  time.Duration`; keep `RecentFramesKept int`. `<= 0` / zero disables each
  dimension.
- Config loader (`lib/control/config/stores.go`): add `TraceTrailing
  *time.Duration \`yaml:"trace_trailing"\`` to the `yamlRetention` struct, a
  `defaultRetentionTraceTrailing` const (proposed `30 * 24 * time.Hour`,
  alongside the existing `defaultRetentionRecentFramesKept = 100` and
  `defaultRetentionLineageTrailing`), and the `parseRetention` mapping +
  non-negative validation (mirroring the existing `recent_frames_kept` /
  `lineage_trailing` handling). Both defaults are finalizable in the plan.
- Replace `FrameTable.PruneOldRunsForRetention` with a frame-reaping method
  (e.g. `PruneTraceForRetention(ctx, recentFramesKept, cutoff)`) implemented
  in both drivers, deleting terminal frame rows by the lesser-of predicate and
  cascading their node_runs.
- Add a time-based retention method to the event accessors. `EventTable`
  (`lib/foundation/persistence/events.go`) today has no delete/retention
  method (`Append`, `List`, `LastTerminalByNodes` only); add
  `DeleteOlderThan(ctx, cutoff)` to it (over `occurred_at`) and an equivalent
  on the named-event accessor (over `emitted_at`), mirroring
  `LineageTable.DeleteOlderThan`.
- `SweepRunTreeRetention` is reshaped (or joined by a sibling sweep) to drive
  all three deletes from one cutoff + cap, logging counts per table.

## Components & data flow

```
POST /instances {terminate_after_run}
  → createInstanceRequest.TerminateAfterRun
  → provisionArgs.TerminateAfterRun
  → InstanceCreateInput.TerminateAfterRun
  → INSERT rimsky_instances(... , terminate_after_run)
  → InstanceRow.TerminateAfterRun → instanceItem (GET/list)

frame-engine tick:
  ListRunningFramesNoPendingNodes  (parked-aware) → frame ends only when all node_runs resolved
  transitionFrameEnd → MarkInstanceTerminatedIfDone (terminate_after_run gated, parked-aware, no PS/queued clause)
  runAdvanceQueued → ListQueuedFramesReadyToStart (skips terminated instances)

retention sweep tick:
  PruneTraceForRetention(recent_frames_kept, now-trace_trailing)  → frames (+node_runs cascade)
  Events().DeleteOlderThan(now-trace_trailing)                    → rimsky_events
  NodeEvents().DeleteOlderThan(now-trace_trailing)                → rimsky_node_events
```

## Schema / migration

New numbered migration `005-instance-terminate-after-run.sql` in both
`lib/foundation/persistence/{postgres,sqlite}/migrations/` (next number after
`004`; pre-v1, plain additive — no compat shim):

- Postgres: `ALTER TABLE rimsky_instances ADD COLUMN terminate_after_run
  boolean NOT NULL DEFAULT false;`
- SQLite: the same `ADD COLUMN` (SQLite permits `NOT NULL` with a literal
  `DEFAULT`), matching the storage form the `paused` column uses in the
  sqlite schema.

No migration is needed for the predicate or retention changes — they are
query/code-level. Existing frame/node_run/event tables already carry the
columns the new predicates and sweeps read (`phase`, `state`, `ended_at`,
`occurred_at`, `emitted_at`).

## Error handling

- A terminated instance continues to reject further messages
  (`lib/control/controlapi/messages.go`, `errInstanceTerminated`) — unchanged.
  Under durable-by-default this path fires far less often (only after explicit
  force-terminate or a `terminate_after_run` run), which is the intended
  behavior.
- Retention deletes are best-effort sweeps in their own short transactions;
  a failure logs and is retried on the next tick (mirroring the existing
  lineage / claim-handle sweeps). In-flight exemption guarantees a sweep can
  never delete live work even under contention.
- The parked-aware predicates are strictly more conservative (they retain a
  frame / withhold termination in more cases), so they cannot newly terminate
  or end-frame anything the old predicates left alone.

## Testing strategy

Behavioral gates (see Acceptance scenarios) plus targeted coverage:

- **Predicate unit/conformance tests** in both drivers: a frame with only a
  parked node_run stays `running`; `MarkInstanceTerminatedIfDone` is a no-op
  for a durable instance and for a `terminate_after_run` instance with a
  parked node, and fires for a `terminate_after_run` instance whose frame
  fully resolved.
- **Retention tests** in both drivers: lesser-of-both reaping; in-flight
  (including parked-held) frames exempt; audit/named events reaped by time;
  no dangling references after a count-cap trim.
- **Blast-radius rework.** Many existing scenario/integration tests assert an
  instance reaches terminal after its work (the old default). Each is updated
  to either set `terminate_after_run: true` (to keep asserting termination) or
  assert durability (the new default). The acceptance-coverage sensor-cascade
  test is reworked: under durable-by-default the sensor instance stays alive
  with no flag and no publisher-subscription coupling, so it asserts that, not
  the carve-out. The publisher-subscription clause removal is part of this
  pass, carrying its dependent test updates with it.
- Race-sensitive predicate paths (frame engine, queue) run with `-race`.

## Acceptance scenarios

1. **Durable across frames (headline).** Instantiate a template (no flag)
   whose node is driven by a real bundled sensor/publisher. The sensor fires
   repeatedly; each fire is a message that invalidates the node → a frame runs
   a real executor → the node re-resolves. After the first frame settles,
   `terminated_at` stays NULL and the instance processes the 2nd…Nth fire.
   *Observable:* the instance stays active and the node re-runs on every fire,
   with no publisher-subscription coupling in the terminal predicate.

2. **Opt-in `terminate_after_run`.** Create with `terminate_after_run: true`
   → one frame runs the graph (real executor) → at that frame-end
   `terminated_at` is set → a subsequent `POST /instances/{id}/messages` is
   rejected. *Observable:* exactly one run, then terminal, replay rejected.

3. **Parked holds the frame.** A node parks awaiting a real async callback.
   While parked the frame stays `running`/held and `terminated_at` stays NULL
   **even with `terminate_after_run` set**. The real callback POSTs back → the
   node resumes, resolves, the frame ends → only then does `terminate_after_run`
   fire. *Observable:* no premature frame completion or termination while
   parked.

4. **Trace retention.** A live durable instance accumulates frames + node_runs
   + events across many runs; after the window/cap, the old trace is reaped
   (frames + node_runs cascade; audit + named events by time) while in-flight
   frames and the most-recent trace survive. *Observable:* old trace rows gone,
   recent + in-flight intact, no dangling events.

---

## Design changes

- **Concept: mutate `concepts/instance.md` in place.** The existing Invariants
  list has one combined termination invariant (the "An instance is terminal
  exactly when its terminal timestamp is set … force-terminate … the instance
  key is freed for reuse only by the subsequent row delete" bullet). Leave that
  bullet's sentences unchanged (force-terminate is still the unconditional
  production path to terminal; delete still frees the instance key), and **add
  two new invariant bullets** alongside it: (a) "An instance is durable by
  default: it self-terminates only when created with `terminate_after_run =
  true`, and then only after its next frame ends (strict 'run at most once
  more' semantics). The default (`terminate_after_run = false`) never
  self-terminates." (b) "Termination is independent of `concept:sensor` /
  `concept:publisher-subscription` and of node presence — the termination
  decision reads nothing about subscriptions or nodes." Append a Notes entry:
  `2026-06-03 — Durable-by-default lifecycle + opt-in terminate_after_run
  (strict: terminate after the next frame ends, regardless of queued frames).
  Replaces the longstanding unconditional auto-terminate-on-drain and removes
  the publisher-subscription coupling. Richer termination modes (drain-to-quiet,
  count-based) are deliberately deferred behind the same flag or a renamed
  successor. Per spec:2026-06-03-instance-lifecycle-durable-by-default.`

- **Concept: mutate `concepts/frame.md` in place.** In "What it is," replace
  the frame-end sentence with: "It ends only when every node_run in the frame
  is resolved — no node_run remains `stale`, `running`, or `parked`. A
  `parked` node_run holds its frame open; the frame does not end while any
  node is parked." Replace the existing frame-begin clause ("A frame begins
  when a node receives an invalidate (in-frame cascade walk) OR when pending
  boundary-crossing messages get delivered") with: "A frame *begins* only when
  a node is invalidated — a direct operator/user invalidation, or message
  delivery (see Message delivery below). Resuming a parked node — park-wake,
  via async callback or snooze timer — does not begin a frame; it resumes the
  still-running frame the parked node belongs to. 'Begins a frame' is distinct
  from 'causes a frame to execute.'" In the Held-frames section, append: "A
  held frame is precisely a running frame with a `parked` (or
  acquisition-pending) node_run; because a parked node_run holds its frame
  open, the held-frames diagnostic and the frame-end rule agree." Add a Notes
  entry: `2026-06-03 — Frame-end definition corrected to include parked
  node_runs as unresolved (a parked node holds its frame open), resolving the
  prior What-it-is vs Held-frames contradiction; clarified that park-wake
  resumes rather than begins a frame, and that a RunScope is *contained*
  within its frame (read the 2026-05-22 note's "span multiple RunScopes" as
  "contain"). Per spec:2026-06-03-instance-lifecycle-durable-by-default.`

- **Concept: mutate `concepts/event-log.md` in place.** Two existing pieces of
  body text assert the opposite of the new retention model and must be
  replaced, not merely supplemented:
  - In Invariants, replace the bullet "No built-in retention; operator-managed
    retention is required." with: "Audit rows are reaped under the shared
    trailing trace-retention window (the same per-instance window that bounds
    frames and node_runs), in addition to cascade-removal on instance delete;
    within the window the log is append-only."
  - In Boundaries, in the "Does NOT own" list, replace "retention policy
    (operator-managed)" with "the trace-retention window value (a shared
    per-instance bound that also governs frames and node_runs, applied here as
    a reaping cutoff)".
  Leave the durability invariant (writes never silently dropped) and all other
  body text unchanged. Append a Notes entry: `2026-06-03 — Audit log brought
  under the shared trace-retention window; previously had no built-in retention
  (reaped only by instance-delete cascade). Replaced the "No built-in
  retention" invariant and the "retention policy (operator-managed)" boundary.
  Part of the durable-by-default trace-retention model. Per
  spec:2026-06-03-instance-lifecycle-durable-by-default.`
