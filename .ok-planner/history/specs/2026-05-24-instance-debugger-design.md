# Instance Debugger — Design

**Date:** 2026-05-24
**Topic:** Live-instance debugging via runtime-installed breakpoints with optional resume-time attribute mutation, plus soft-pause / resume / paused-on-create affordances on `concept:instance`. MCP-as-skin: the agent debugger pattern. Breakpoint hits are the only debugger-specific feedback surface; general event-log subscription is out of scope (deferred to a future telemetry feature). MCP push semantics (`resources/subscribe` + server-pushed notifications) are also out of v1 scope — the current MCP transport is stateless POST and adding push requires a separately-scoped transport upgrade. V1 ships with agent-side polling via `resources/read`.

**Origin sketches:** `.ok-planner/history/sketches/2026-05-20-instance-harness-sketch.md` (the matcher-overlay primitive of that sketch shipped earlier as `spec:2026-05-21-attribute-overrides-matcher-overlay-design`; this spec picks up the un-shipped event-tap and breakpoint primitives) and `.ok-planner/history/sketches/2026-05-19-control-mcp-subscribe-push.md` (the push-surface sketch; folds into the MCP `resources` integration here, polling-shaped in v1).

**Depends on:** `plan:2026-05-23-signal-taxonomy-and-policy-decoupling` Pass 1 (signal infrastructure + audit-emission wiring). The breakpoint surface uses signal type-paths to express outcome-based filters on `after_terminal` checkpoints; the spec assumes the signal package at `foundation/signal/` is in place before this spec's plan executes. The plan's first task is a gate that verifies the signal package's expected surface.

## 1. Context

The agent debugger is the v1 use case. An LLM agent operating rimsky via MCP wants to:

1. Create an instance with the option to hold dispatch until breakpoints are installed.
2. Install runtime breakpoints with a matcher predicate over dispatch identity (executor, node, graph, child_key, attribute values) and optionally a signal-type filter on terminal outcomes.
3. Discover breakpoint hits via MCP `resources/read` polling, each hit carrying a snapshot of the dispatch context (or terminal signal) plus the node-run row, the open wait-set, and the held claims.
4. Inspect the snapshot, optionally mutate the dispatch via a one-shot overlay fragment, and resume.
5. Pause / unpause running instances on demand.

Today the system has none of this. The closest existing primitives are:

- `attribute_overrides.by_match` (per `concept:attribute` and `spec:2026-05-21-attribute-overrides-matcher-overlay-design`) — equality-only 5-key matcher applied as L5 in the merge layering, set at instance-create only, no runtime mutation.
- `concept:parked-state` — executor-emitted hold state, not operator-injected.
- `GET /events` (per `concept:event-log`) — paginated polling, not push.
- The control-plane MCP at `code:control/controlapi/mcp/server.go::Server.ServeHTTP` — tools-only V1; no `resources/subscribe` capability.

This spec adds the minimum surface for agent-driven debugging, no more.

### What this spec does NOT do

- General event-log subscription / "watch this instance's events live." That's telemetry / logging, a separate concern with its own future spec. The debugger's only feedback surface is **breakpoint hits** (via `resources/read` polling).
- MCP `resources/subscribe` + `notifications/resources/updated` (server-pushed). The current MCP transport is stateless POST per JSON-RPC; push requires a streamable-HTTP transport upgrade with per-session state. Deferred to a future spec. V1 ships polling-via-`resources/read` and the resource shape is designed so push is purely additive when the transport lands.
- Webhook or SSE destinations for hit delivery. MCP-only in v1.
- Promote-a-hit-to-overlay (a "remember this overlay for future matching dispatches" ergonomic). Skipped for v1 per earlier discussion.
- Executor-telemetry concerns (token/cost capture, subagent depth, synthetic-blocker test mode) — these belong with the still-pending `.ok-planner/sketches/2026-05-07-agentic-telemetry.md`.
- Hard pause (preempt in-flight dispatches). Soft pause (stop claiming new dispatches; let in-flight run to terminal) is the only pause semantics. Hard-pause-at-next-checkpoint is expressible by composing soft pause with a `pause`-mode breakpoint with empty matcher.

## 2. Architecture overview

The deliverable is one new feature: an agent-debugger surface on `concept:control-api` accessible via MCP, with two primitives plus a pause / resume affordance.

```
┌────────────────────────────────────────────────────────────────────┐
│  control-api (rimsky-control-api binary)                           │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │  HTTP+JSON                                                   │  │
│  │  + POST /instances accepts {paused: bool} flag (new)         │  │
│  │  + POST /instances/{id}/pause (new)                          │  │
│  │  + POST /instances/{id}/resume (new)                         │  │
│  │  + POST /instances/{id}/breakpoints (new)                    │  │
│  │  + GET  /instances/{id}/breakpoints (new)                    │  │
│  │  + DELETE /instances/{id}/breakpoints/{bp_id} (new)          │  │
│  │  + POST /instances/{id}/breakpoints/{bp_id}/resume (new)     │  │
│  └──────────────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │  MCP — POST /mcp (extended from tools-only)                  │  │
│  │  + tools mirroring the HTTP additions above                  │  │
│  │  + resources/list (advertise URIs filtered by permission)    │  │
│  │  + resources/read (page hits by ?since=<seq>&limit=<n>)      │  │
│  │  (no resources/subscribe, no server-pushed notifications —   │  │
│  │   agent polls resources/read on its own cadence)             │  │
│  └──────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
         │                                                  ▲
         │  CRUD on breakpoints, hits                       │  agent polls resources/read;
         │                                                  │  handler runs a SELECT against
         ▼                                                  │  rimsky_breakpoint_hits
┌────────────────────────────────────────────────────────────────────┐
│  Persistence (postgres / sqlite — flattened to one schema file)    │
│  rimsky_instances.paused BOOLEAN (new column)                      │
│  rimsky_instance_breakpoints (new table)                           │
│  rimsky_breakpoint_hits (new table)                                │
└────────────────────────────────────────────────────────────────────┘
         ▲                                ▲
         │ checkpoint                     │ checkpoint
         │ (before_dispatch)              │ (after_terminal)
         │                                │
┌────────────────────────────────────────────────────────────────────┐
│  Supervisor (rimsky-supervisor binary)                             │
│  - candidate-selection skips paused instances                      │
│  - before_dispatch + after_terminal checkpoints query breakpoints; │
│    on match, write hit row, block runner (pause mode) or continue  │
│    (notify_only mode)                                              │
└────────────────────────────────────────────────────────────────────┘
```

Three primitives:

1. **Breakpoints** (§4) — runtime-installed pause-points with matcher grammar from `attribute_overrides.by_match` plus an optional `signal_type` prefix filter on `after_terminal` checkpoints. Two modes (`pause` blocks the runner; `notify_only` just records a hit row and continues). Resume optionally carries a one-shot overlay fragment that merges as L6 on top of the dispatch's existing L5 bag.

2. **Pause / resume / paused-on-create** (§5) — soft pause via a `paused BOOLEAN` column on `rimsky_instances`. Three routes (`POST /instances` gains a `paused: true` parameter; new `POST /instances/{id}/pause`; new `POST /instances/{id}/resume`).

3. **MCP `resources` read surface for breakpoint hits** (§6) — the MCP server extends from tools-only to tools + read-only resources. One resource family (`rimsky://instances/{id}/breakpoint-hits` and `rimsky://breakpoints/{bp_id}/hits`); agent polls `resources/read` with a `?since=<seq>` cursor to page through hits. No subscription / no server-push — push semantics await a future MCP transport upgrade.

Two extractions enable the work:

- **`foundation/matcher/`** (§7) — the matcher grammar, validator, and evaluator currently in `code:runtime/attribute_overrides.go::evaluateMatcher` and `code:control/controlapi/attribute_overrides.go::validateMatcherKeys` extract to a shared package. Both `attribute_overrides.by_match` and the new breakpoint feature call it.

- **MCP `resources` capability (read-only)** (§6) — the in-process MCP server at `code:control/controlapi/mcp/server.go::Server.ServeHTTP` gains `resources/list` and `resources/read` dispatch in its method switch. No new transport, no per-session state. Push (`resources/subscribe` + `notifications/resources/updated`) is left for a future spec.

## 3. Pre-spec cleanup: flatten migrations

Before any new schema, this spec's plan deletes the 14 numbered migration files in each of `foundation/persistence/postgres/migrations/` and `foundation/persistence/sqlite/migrations/`:

```
001-baseline.sql
002-tags.sql
003-per-run-attributes.sql
004-wait-set-drained-at.sql
005-attribute-overrides-rename.sql
006-attribute-overrides-match-counts.sql
007-run-scopes.sql
008-node-runs-run-scope-id.sql
009-claim-scope-rename.sql
010-instances-main-run-scope.sql
011-park-reason-collapse.sql
012-node-runs-prior-dispatch.sql
013-node-runs-settling-signal-type.sql
014-drop-last-outcome.sql
```

Note: file 014 drops the `last_outcome` column from `rimsky_node_runs` (per `plan:2026-05-23-signal-taxonomy-and-policy-decoupling` Pass 5). The consolidated `001-schema.sql` reflects the post-drop state — it does not declare `last_outcome` on `rimsky_node_runs`.

And writes a single replacement file `001-schema.sql` per backend, containing the full current schema as one consolidated bundle:

- All tables (`rimsky_supervisors`, `rimsky_templates`, `rimsky_instances`, `rimsky_nodes`, `rimsky_node_runs`, `rimsky_node_attributes`, `rimsky_node_events`, `rimsky_claim_handles`, `rimsky_claim_holders`, `rimsky_wait_set`, `rimsky_frames`, `rimsky_messages`, `rimsky_publisher_subscriptions`, `rimsky_lifecycle_idempotencies`, `rimsky_message_idempotencies`, `rimsky_events`, `rimsky_lineage`, `rimsky_run_scopes`, `rimsky_api_keys`, `rimsky_template_tags`). The `rimsky_migrations` bookkeeping table is bootstrapped by the migration runner itself and is not authored as a user-facing migration.
- All current indexes.
- All current CHECK constraints (including the 2-value `park_reason` collapse from migration 011, the `phase` enum extension, the `settling_signal_type` column from migration 013, and the `last_outcome` column dropped by migration 014, etc.).
- The new tables and column from this spec (§7 details): `rimsky_instance_breakpoints`, `rimsky_breakpoint_hits`, and `rimsky_instances.paused`.

This is a pre-v1 break-freely operation per `.claude/rules/rules.md`. Operators with existing dev databases drop and recreate — the consolidated `001-schema.sql` is not an upgrade path; it's a fresh-database baseline. The new filename `001-schema.sql` is distinct from the old `001-baseline.sql`, so fresh deployments apply it cleanly on first migrate. The migration runner at `code:foundation/persistence/migrations.go::Migrator.Run` continues to work unchanged; `@blessed-invariant 8` (session advisory lock on migrations) is preserved.

`embed.go` in each migration directory is updated to embed the new single file.

## 4. Breakpoints

### 4.1 Lifecycle

Breakpoints live in `rimsky_instance_breakpoints` and are created at runtime against a live (or paused-on-create) instance via control-API:

```
POST /instances/{instance_id}/breakpoints
  Content-Type: application/json
  {
    "matcher":         { /* the 5-key matcher predicate from foundation/matcher */ },
    "checkpoint":      "before_dispatch" | "after_terminal",
    "signal_type":     "terminal/error/*",         // optional; only valid when checkpoint = "after_terminal"
    "mode":            "pause" | "notify_only",    // default: "pause"
    "overflow_policy": "drop_oldest" | "block_dispatch" | "auto_resume_after_ttl",
                                                   // default: "drop_oldest" for notify_only, "block_dispatch" for pause
    "hit_ttl_seconds": 300,                        // only meaningful when overflow_policy = "auto_resume_after_ttl"
    "ttl_seconds":     3600                        // optional; auto-removal of the breakpoint itself
  }
  → 201 { "breakpoint_id": "<uuid>", "instance_id": "<uuid>", ...echoed... }
  → 400 ErrMatcherInvalid if matcher fails foundation/matcher's Validate
  → 400 if signal_type set on a before_dispatch breakpoint
  → 400 if signal_type fails foundation/signal/taxonomy::ValidateTypePath
  → 404 ErrInstanceNotFound if instance doesn't exist
```

```
GET /instances/{instance_id}/breakpoints
  → 200 { "breakpoints": [ { ...row projection... }, ... ] }
```

```
DELETE /instances/{instance_id}/breakpoints/{breakpoint_id}
  → 204 on success
  → 404 ErrBreakpointNotFound
```

The instance is identified separately from the breakpoint_id in resume / delete URIs for symmetry with the rest of the API (cf. `code:control/controlapi/messages.go` which has `/instances/{id}/messages` and `/messages/{id}` parallel surfaces).

### 4.2 Supervisor cooperation

At each of the two checkpoints, the supervisor's runner queries `rimsky_instance_breakpoints` for the current instance and evaluates each candidate matcher:

```
before_dispatch checkpoint — runs in code:runtime/runner_dispatch.go after
  applyAttributeOverrides has produced the post-L5 merged attribute bag,
  before the executor Execute call.

after_terminal checkpoint — wired at the two callers of runApplyTerminal:
    - code:runtime/runner.go      (the synchronous-terminal dispatch path)
    - code:runtime/callback.go    (the async-callback dispatch path)
  Both callers invoke runApplyTerminal, which owns the terminal-handler tx
  (calling applyTerminalComplete / applyTerminalError / applyTerminalPark /
  applyTerminalInfraError per the terminal kind). The after_terminal
  checkpoint fires after that tx commits and before cascade walks fire.
  Wiring at the callers rather than inside applyTerminal preserves the
  no-tx-held-across-wait invariant for pause-mode breakpoints.
```

**Out of scope: pre-dispatch failures.** Acquisition-unavailable and other failures that occur before a dispatch reaches `runApplyTerminal` produce canonical signals (per `concept:signal`'s `terminal/error/acquire/unavailable`, post-`spec:2026-05-23-signal-taxonomy-and-policy-decoupling`) via `code:runtime/on_error.go::OnError`. They are **not** caught by `after_terminal` breakpoints. The checkpoint name describes the supervisor lifecycle position (after `runApplyTerminal` returns), not the signal type — acquire-failures emit outside that path. Operators wanting to break on pre-dispatch failures would need a future `on_acquire_failed` checkpoint or equivalent; out of v1 scope. A breakpoint with `signal_type: "terminal/error/*"` at `after_terminal` catches dispatch-time errors only.

Evaluation logic (pseudocode in either checkpoint):

```go
matches := findMatchingBreakpoints(ctx, instanceID, checkpoint, dispatchCtx)
                                  // queries rimsky_instance_breakpoints filtered
                                  // by checkpoint, then in-memory filters via
                                  // matcher.Evaluate + signal_type prefix check
                                  // (after_terminal only).

const QueueCap = 100

for _, bp := range matches {
    // Pre-write overflow handling on the queue cap (100 unresumed hits per breakpoint).
    // Behavior is policy-specific:
    for {
        unresumed, _ := persist.BreakpointHits().UnresumedCount(ctx, bp.ID, tx)
        if unresumed < QueueCap {
            break  // room to write
        }
        switch bp.OverflowPolicy {
        case "drop_oldest":
            // notify_only only — synchronously evict oldest, increment counter, proceed.
            persist.BreakpointHits().DropOldest(ctx, bp.ID, QueueCap-1, tx)
            persist.Breakpoints().IncrementDropped(ctx, bp.ID, tx)
        case "block_dispatch":
            // pause only — block until something drains. The supervisor polls at the
            // same 250ms cadence used for resume-detection. UnresumedCount filters by
            // `resumed_at IS NULL`, so resumed hits don't count against the cap — the
            // queue moves as soon as any blocked hit gets a resume call. No reaper
            // dependency for correctness here.
            sleep(250 * time.Millisecond)
            continue
        case "auto_resume_after_ttl":
            // Either mode — block until the sweeper auto-resumes the oldest stale hit
            // (TTL-bounded). Polls at the same cadence; never silently drops a pause-
            // mode hit.
            sleep(250 * time.Millisecond)
            continue
        }
    }

    hitID, _, err := persist.BreakpointHits().Create(ctx, hitRow(bp, dispatchCtx, snapshot), tx)
    if err != nil { return err }
    if bp.Mode == "notify_only" {
        continue  // no block; supervisor proceeds to the next match or out of the loop
    }
    if err := waitForResume(ctx, hitID); err != nil { return err }
                              // polls hit row at 250ms cadence until resumed_at != NULL
    hit, err := persist.BreakpointHits().Get(ctx, hitID, tx)
    if err != nil { return err }
    if hit.ResumeOverlay != nil {
        merged := shared.DeepMergeJSON(dispatchCtx.MergedAttributes, hit.ResumeOverlay)
        // Defense-in-depth re-validate. On failure surface as template_validation_failed
        // routed through concept:error-policy (control-api's resume API already pre-validated;
        // this is the runtime gate for schema drift between resume time and dispatch time).
        if err := attributes.Validate(executorSchema, merged, attributes.PhaseDispatch); err != nil {
            return wrapAsTemplateValidationFailed(err)
        }
        dispatchCtx.MergedAttributes = merged
    }
}
```

When the breakpoint loop completes, the supervisor proceeds with the (possibly mutated) dispatch context. The L6 overlay is per-hit, never persists into `rimsky_instances.attribute_overrides`.

### 4.3 The one cross-process arrow

Control-api writes the resume row; the supervisor's blocked runner needs to see it. We use polling for symmetry with the existing claim-ledger and reaper patterns — the blocked runner queries `rimsky_breakpoint_hits.resumed_at` for its hit on a tight loop at 250ms cadence. The latency is on the resume → continue path only, which is not on the hot path. The simpler shape avoids introducing a new IPC channel between rimsky's binaries (which today coordinate exclusively through the DB).

For paused-instance candidate-selection, no cross-process notification is needed at all — the supervisor's poll-and-claim cycle already runs continuously; pausing just adds a `WHERE paused = false` filter.

### 4.4 Matcher grammar

Identical to the closed 5-key grammar from `attribute_overrides.by_match` (per `spec:2026-05-21-attribute-overrides-matcher-overlay-design`), extracted to `foundation/matcher/` (§7). Equality-only, AND-joined across present keys, missing keys are wildcards, empty matcher fires for every dispatch.

```jsonc
{
  "node_type": "<template node name>",      // optional
  "executor":  "<executor name>",           // optional
  "graph":     "main" | "<sub-graph name>", // optional
  "child_key": "<value>",                   // optional; empty for non-fan-out
  "attrs":     { "<dotted.path>": <primitive>, ... } // optional
}
```

### 4.5 Signal-type filter

Separate field on the breakpoint row, not part of the matcher grammar:

- `signal_type` — optional string; prefix-match against the terminal signal's type-path per `concept:signal`.
- Only valid when `checkpoint = "after_terminal"`. Rejected with 400 on `before_dispatch` (no signal exists yet).
- Validated at registration via `code:foundation/signal/taxonomy.go::ValidateTypePath` (or `ValidateSubscriptionType` for the trailing-`*` admitted form). Trailing-`*` admitted (matches the signal package's invariant: wildcard syntax is trailing-`*` only).
- Evaluated by breakpoint-specific code in the supervisor at the `after_terminal` checkpoint, against the terminal signal that was just emitted. The `code:foundation/signal/types.go::TypePath.HasPrefix` helper handles the comparison.

### 4.6 Snapshot payload

Written verbatim into `rimsky_breakpoint_hits.snapshot` and surfaced via `resources/read`:

```jsonc
{
  "seq":           <int64>,        // monotonic cursor for resources/read pagination
  "hit_id":        "<uuid>",       // stable identity for the resume API
  "breakpoint_id": "<uuid>",
  "instance_id":   "<uuid>",
  "node_run_id":   "<uuid>",
  "frame_id":      "<uuid>",
  "checkpoint":    "before_dispatch" | "after_terminal",
  "mode":          "pause" | "notify_only",
  "hit_at":        "<iso8601>",

  "dispatch_context": {
    "executor":           "...",
    "node_type":          "...",
    "graph":              "...",
    "child_key":          "...",
    "merged_attributes":  { /* post-L5 attribute bag */ }
  },

  // Present only for checkpoint = "after_terminal":
  "terminal_signal": {
    "type":    "terminal/error/...",
    "payload": { /* per concept:signal payload schema */ }
  },

  "node_run":      { /* projection of rimsky_node_runs row at hit time */ },
  "held_claims":   [
    { "claim_handle_id": "<uuid>", "scope_summary": "<short string>", "alias": "..." },
    ...
  ],
  "open_wait_set": [
    { "sender_node_id": "<uuid>", "topic": "...", "name": "..." },
    ...
  ]
}
```

Per `concept:inertness`, `held_claims` and `open_wait_set` are summaries — IDs, types, counts. Claim content (scope / address / payload) stays opaque even at the debugger; the agent gets IDs to query through normal channels if it wants to dig deeper.

`merged_attributes` IS included in full because the matcher grammar already treats attribute values as visible (per the sanctioned read site annotation on `code:runtime/attribute_overrides.go::evaluateMatcher`).

### 4.7 Resume API

```
POST /instances/{instance_id}/breakpoints/{breakpoint_id}/resume
  Content-Type: application/json
  {
    "hit_id":  "<uuid>",
    "overlay": { /* attribute fragment; optional */ }
  }
  → 200 { "resumed": true, "first_resume": true }
        // first_resume=true on the original call, false on idempotent replay
  → 400 ErrResumeOverlayInvalid if overlay shape-invalid OR fails resume-time JSON Schema validation
  → 404 ErrBreakpointHitNotFound if hit_id not in rimsky_breakpoint_hits
        // also covers the cascade-deleted-via-breakpoint-delete case: the FK ON DELETE CASCADE
        // removes hits when their parent breakpoint is deleted, so an orphan-hit resume sees 404.
```

Idempotency: a second resume call on an already-resumed hit returns 200 OK with `first_resume: false` and the original outcome — replay-safe. No new state is written; the original `resume_overlay`, `resumed_by_key`, and `resumed_at` are preserved.

Resume-time validation discipline (per the decided "both layers" approach, mirroring the existing `attribute_overrides` discipline):

1. **Shape check.** The overlay must satisfy `foundation/matcher`'s overlay-fragment shape rules (same as `by_match`'s overlay field — opaque to rimsky except for top-level type checks).
2. **Pre-merge validation against the dispatch's effective schema.** Read the post-L5 bag from `hit.snapshot.dispatch_context.merged_attributes`. Compute `DeepMergeJSON(merged_attributes, overlay)`. Run `attributes.Validate` against the effective attribute schema (executor's declared schema + the template's L1+L2 merge). Failure → `ErrResumeOverlayInvalid` (400).
3. **Persist.** Write `resume_overlay = <fragment>` and `resumed_at = NOW()` on the hit row. Single tx.
4. **Defense-in-depth at supervisor.** When the blocked runner sees `resumed_at` set, it re-validates the merged bag against the executor's raw schema (the existing pattern at `code:runtime/runner_dispatch.go`). Failure routes through `template_validation_failed` per `concept:error-policy`.

Resume body's `overlay` can add new attribute keys, override existing keys, or both — bounded by whatever the executor's JSON Schema allows (e.g., `additionalProperties: false` would reject new keys at the JSON Schema validation step). The breakpoint feature doesn't separately constrain this.

### 4.8 Production-safety: hit-queue overflow

Each breakpoint row carries an `overflow_policy` controlling what happens when the per-breakpoint unresumed-hit queue reaches the cap of 100 rows. The three policies have distinct semantics:

- **`drop_oldest`** (allowed on `notify_only` only; default for `notify_only`): when the cap is reached, the supervisor synchronously evicts the oldest unresumed hit via `BreakpointHitTable.DropOldest` (deleting the row) and increments the breakpoint's `dropped_count BIGINT`. New hit row is then created normally. Agent notifications carry the cumulative `dropped_count` so the agent knows it has missed hits. Production-safe: the instance keeps running and the queue stays bounded.

- **`block_dispatch`** (allowed on `pause` only; default for `pause`): the queue cap is enforced by the per-hit block — each pause-mode hit blocks the dispatch until resumed, so the cap is reached only if 100 distinct dispatches simultaneously hit the breakpoint and none have been resumed. In that case the 101st matching dispatch also blocks (waiting for any prior hit to resume before it gets its own row). No silent drops; in the worst case the instance stalls until the agent drains hits.

- **`auto_resume_after_ttl`** (allowed on both `pause` and `notify_only`): each hit auto-resumes (`resume_overlay = NULL`, `resumed_by_key = "<sweeper>"`) if it sits unresumed past `hit_ttl_seconds` (default 300s). Combined with `pause` mode: the dispatch blocks but is guaranteed to proceed within the TTL window. Combined with `notify_only` mode: the hit row is reaped within the TTL window even if no agent ever reads it (prevents `rimsky_breakpoint_hits` from growing unboundedly on an abandoned subscription). When the queue cap is reached under this policy, the supervisor blocks the new hit-write and waits for the sweeper to auto-resume the oldest stale hit (no silent drops; the TTL bounds the wait — worst case the dispatch waits one full `hit_ttl_seconds` before proceeding).

The `(mode, overflow_policy)` combinations are validated at create-time:
- `pause` + `drop_oldest` → rejected with 400 (pause-mode hits cannot be silently dropped; the dispatch is paused waiting on them).
- `notify_only` + `block_dispatch` → rejected with 400 (notify-only's whole point is non-blocking; the block_dispatch policy contradicts it).
- All other combinations are admitted.

Sweep operation handles both auto-deletion of expired breakpoints (via `ttl_seconds`) and auto-resume of stale hits (via `hit_ttl_seconds`) — piggybacking on the existing `concept:orphan-reaper` cadence (no new ticker, no new goroutine).

## 5. Pause / resume / paused-on-create

### 5.1 Wire shape

`POST /instances` accepts an optional `paused` flag in the body:

```jsonc
{
  "template": "...",
  "instance_key": "...",
  "params": { ... },
  "attribute_overrides": { ... },
  "frame_delivery_mode": "...",
  "paused": true                    // new; default false
}
```

When `paused: true`, the instance row is created with `paused = true` in `rimsky_instances`. The supervisor's candidate-selection query gains `AND paused = false` so paused instances are never picked up for dispatch.

```
POST /instances/{instance_id}/pause
  → 200 { "paused": true }
  → 404 ErrInstanceNotFound
  → 409 ErrInstanceAlreadyPaused (idempotent surface — surfaces the typo of pausing twice)
```

```
POST /instances/{instance_id}/resume
  → 200 { "resumed": true }
  → 404 ErrInstanceNotFound
  → 409 ErrInstanceNotPaused (mirror of pause's 409)
```

### 5.2 Soft-pause semantics

Pausing sets `paused = true`. In-flight dispatches **run to terminal naturally** — their cascades fire, their auto-terminals complete, their lineage records are written. New dispatches are not claimed because of the candidate-selection filter. The instance drains to a quiet state and stays there.

Resume sets `paused = false`. Supervisor's next tick picks up eligible work; pending messages (which accumulated during the pause) are delivered per the instance's `frame_delivery_mode`.

Hard-pause-at-next-checkpoint (preempting in-flight at the supervisor's checkpoints) is not a separate primitive. The agent expresses it as: `instance_pause` (stop claiming) + install a `pause`-mode breakpoint with empty matcher (catch every dispatch at the next checkpoint). The two compose cleanly.

### 5.3 No effect on external publishers

Messages from publishers / sensors targeting a paused instance write to `rimsky_messages` normally (per `concept:message`). They accumulate as pending. First dispatch after resume drains them per `frame_delivery_mode`. The pause is for debugger reasons; it doesn't silence external producers.

### 5.4 Idempotency interaction

`POST /instances` idempotency uses `(template_hash, instance_key)` as the dedup key, per `code:control/controlapi/instances.go::handleCreateInstance` (the FOR-UPDATE-locked existing-row resolution). It is NOT the universal `Idempotency-Key` HTTP header (that header is the `POST /instances/{id}/messages` pattern per `concept:message`).

The `paused` flag is a creation parameter, not part of the dedup key. Same `(template_hash, instance_key)` with different `paused` values on the second request returns the existing instance unchanged — the `paused` flag from the second request is ignored (consistent with how `attribute_overrides` and `params` are treated on the existing dedup path). This matches the existing behavior for other creation-time parameters and preserves the idempotency contract.

If the operator wants to pause an instance that already exists and is running, they call `POST /instances/{id}/pause` — not a second instance-create with `paused: true`.

## 6. MCP integration

### 6.1 Capability extension

The in-process MCP server at `code:control/controlapi/mcp/server.go::Server.ServeHTTP` is currently a stateless POST handler exposing `initialize`, `tools/list`, and `tools/call` only. This spec extends the dispatch switch to also support a polling-shaped subset of MCP's `resources` capability:

- `resources/list` — enumerate canonical resource URIs the requesting key has permission to read, filtered by `breakpoint:read`.
- `resources/read` — read recent hits for a URI, paginated by a `?since=<seq>` cursor.

The MCP capability handshake at `initialize` advertises `resources: { subscribe: false, listChanged: false }` (per the MCP spec's optional capability flags). Tools-only clients ignoring the new capability continue to work unchanged.

**Push semantics are explicitly out of v1 scope.** `resources/subscribe` and `notifications/resources/updated` are NOT in this spec. The current MCP transport is stateless POST per JSON-RPC; server-pushed notifications would require upgrading to MCP's streamable-HTTP transport with per-session state, which is a separately-scoped piece of infrastructure work. For v1 the agent polls `resources/read` on its own cadence (a debugger session is short-lived and polling every 2–5 seconds is fine for the use case). A future spec can land the transport upgrade and add `resources/subscribe` + push notifications on top of this surface — the resource URI scheme, the read shape, and the pagination cursor are designed so that the upgrade is purely additive.

### 6.2 Subscribable resources

Two canonical URI shapes:

```
rimsky://instances/{instance_id}/breakpoint-hits
rimsky://breakpoints/{breakpoint_id}/hits
```

- **Instance-scoped** (`rimsky://instances/{id}/breakpoint-hits`) — agent polls once per debugger session; each `resources/read` returns hits past the cursor across all breakpoints on that instance.
- **Breakpoint-scoped** (`rimsky://breakpoints/{bp_id}/hits`) — agent polls per breakpoint after the create call returns its `breakpoint_id`.

Both URI shapes coexist; reading one doesn't preclude the other. `resources/list` advertises the instance-scoped form per instance the requesting key has `breakpoint:read` for. The breakpoint-scoped form is constructed by the agent.

URIs accept query parameters:

- `?since=<seq>` — return only hits with `seq > <since>`, ordered ascending by `seq`. The cursor is the `seq BIGSERIAL` column on `rimsky_breakpoint_hits` (see §7.2; matches the `id BIGSERIAL` cursor pattern on `rimsky_events`). Same shape on both URI families. Agent records the highest `seq` it has seen and uses it as the cursor for the next poll.
- `?limit=<n>` — bound the page size; server enforces a maximum (default 500).

### 6.3 Tool catalog additions

Tools mirror the HTTP endpoints, per the existing MCP-as-skin pattern at `code:control/controlapi/mcp_route.go::builtinSchemas`:

| Tool name | Wraps |
|---|---|
| `instance_create` | `POST /instances` — gains the `paused` parameter |
| `instance_pause` | `POST /instances/{id}/pause` |
| `instance_resume` | `POST /instances/{id}/resume` |
| `breakpoint_create` | `POST /instances/{id}/breakpoints` |
| `breakpoint_list` | `GET /instances/{id}/breakpoints` |
| `breakpoint_delete` | `DELETE /instances/{id}/breakpoints/{bp_id}` |
| `breakpoint_resume_hit` | `POST /instances/{id}/breakpoints/{bp_id}/resume` |

New `Action` entries land in `code:control/controlapi/actions.go::v1Actions`. The MCP tool catalog is computed from that registry per the existing pattern; new actions auto-expose as tools.

### 6.4 `resources/read` semantics

A single `resources/read` call returns a page of hits past the cursor:

```sql
-- instance-scoped:
SELECT * FROM rimsky_breakpoint_hits
  WHERE instance_id = $1 AND seq > $since
  ORDER BY seq ASC
  LIMIT $limit;

-- breakpoint-scoped:
SELECT * FROM rimsky_breakpoint_hits
  WHERE breakpoint_id = $1 AND seq > $since
  ORDER BY seq ASC
  LIMIT $limit;
```

The response body shape:

```jsonc
{
  "contents": [
    {
      "uri": "rimsky://instances/{id}/breakpoint-hits?since=<since>",
      "mimeType": "application/x-rimsky-breakpoint-hits+json",
      "text": "{\"hits\": [...], \"next_since\": <seq>, \"truncated\": <bool>}"
    }
  ]
}
```

`hits` contains the full snapshot payload per §4.6 for each row, ordered ascending by `seq`. `next_since` is the highest `seq` in the page (or the request's `since` if the page is empty); `truncated` is true if the result hit the `limit`. Agents record `next_since` as their cursor and call again to drain when `truncated` is true.

### 6.5 Polling cadence (agent-side)

Polling is entirely agent-controlled. The agent decides how often to call `resources/read` based on its own latency budget. For a debugger session this is typically 1–5 seconds; for an audit walk it might be once per minute. The server has no per-session state to maintain, no live tail to keep open, no cleanup on disconnect — the next request just queries with the next cursor.

Polling cost on the server is bounded by the agent's chosen interval and the per-query LIMIT cap. The indexes in §7.2 (`idx_bp_hits_breakpoint_seq` and `idx_bp_hits_instance_seq`) cover both query shapes.

### 6.6 No session lifecycle

Because the MCP server is stateless POST, there is no session lifecycle to manage. Each `resources/read` is a self-contained request carrying its own cursor. Reconnect is implicit — the agent's next request is identical to its prior request would have been, modulo the cursor it's accumulated.

### 6.7 Auth integration

Reads gate under `breakpoint:read` (covered by `*:read` wildcard for agent-supervisor); writes gate under `breakpoint:create`, `breakpoint:resume`, `breakpoint:delete`, `instance:pause`, `instance:resume`. See §8.

MCP `resources/*` and tool-call paths re-enter the chi router via `code:control/controlapi/mcp/catalog.go::Catalog.Invoke`, going through the same `gateByAction` middleware as HTTP requests. Audit rows record `protocol_skin: "mcp"`.

## 7. Shared matcher package and persistence

### 7.1 `foundation/matcher/` package

Extract from current locations:

- `code:runtime/attribute_overrides.go::evaluateMatcher`, `matcherAllowedKeys`, `walkAttrPath`, `primitiveEqual`.
- `code:control/controlapi/attribute_overrides.go::validateMatcherKeys`.

Layout:

```
foundation/matcher/
  matcher.go       // Matcher type, Evaluate, Context, primitive helpers
  validate.go      // Validate, ValidationRefs
  matcher_test.go
  validate_test.go
```

Per `concept:module-layout` import boundaries: the package lives under `foundation/` with imports only from `foundation/shared` (for `Logger`) and stdlib. No dependencies on `runtime/`, `control/`, or persistence types.

Public API:

```go
package matcher

// Matcher is the 5-key dispatch-identity predicate, equality only, AND across
// present keys. Missing keys are wildcards; empty matcher fires for every dispatch.
type Matcher map[string]any

// Context is the dispatch context the matcher evaluates against.
type Context struct {
    Executor     string
    NodeType     string
    Graph        string
    ChildKey     string
    AttributeBag map[string]any // post-L5 merged attributes per concept:attribute
}

// Evaluate returns true if matcher fires on ctx.
//
// The attrs.<path> branch is the inertness-sanctioned attribute-value read site
// (preserved from the @concept: inertness annotation in
// runtime/attribute_overrides.go::evaluateMatcher).
func Evaluate(m Matcher, ctx Context, logger shared.Logger) bool

// ValidationRefs supplies the reference name-sets the validator
// cross-checks against.
type ValidationRefs struct {
    NodeTypes  map[string]struct{}
    Executors  map[string]struct{}
    GraphNames map[string]struct{}
}

// Validate enforces the matcher's shape at registration time. Returns nil on
// success or a wrapped foundation/shared error with rejection reason.
func Validate(m Matcher, refs ValidationRefs) error
```

The closed key set (`node_type`, `executor`, `graph`, `child_key`, `attrs`) lives as a package-private map; `Validate` rejects unknown keys; `Evaluate` defensively skips entries with unknown keys per the existing `code:runtime/attribute_overrides.go::evaluateMatcher` discipline.

**`child_key` rule inherited.** The existing by_match validator at `code:control/controlapi/attribute_overrides.go::validateMatcherKeys` (around the `child_key` case) rejects `child_key: ""` because an empty string would silently match every non-fan-out dispatch (which carry `childKey == ""` per `concept:fan-out`). This rejection is part of the shared matcher's grammar — both `by_match` and breakpoint matchers reject empty `child_key`. Breakpoints that need to match non-fan-out dispatches simply omit `child_key` entirely (missing keys are wildcards); breakpoints that need to target a specific fan-out partition supply a non-empty `child_key`. The rule is grammar-level, not context-dependent.

Caller updates:

- `code:runtime/runner_dispatch.go::applyAttributeOverrides` calls `matcher.Evaluate` (in place of inline `evaluateMatcher`).
- `code:control/controlapi/attribute_overrides.go::validateMatcherKeys` collapses to `matcher.Validate` plus the `by_match`-specific wire-shape checks (top-level list shape, per-entry `{matcher, overlay}` envelope) which stay where they are.
- New breakpoint code (`runtime/breakpoint_eval.go`, `control/controlapi/breakpoints.go`) calls `matcher.Validate` at registration and `matcher.Evaluate` at supervisor checkpoint time.

The breakpoint's `signal_type` filter is **not** part of `foundation/matcher`. Breakpoint-specific code consumes signal type-paths via `code:foundation/signal/taxonomy.go::ValidateTypePath` and `code:foundation/signal/types.go::TypePath.HasPrefix`. Matcher package stays pure to dispatch identity.

### 7.2 New tables and column

`rimsky_instances` gains one column:

```sql
ALTER TABLE rimsky_instances
  ADD COLUMN paused BOOLEAN NOT NULL DEFAULT false;
```

(But this lives in the consolidated `001-schema.sql` per §3, not as a separate ALTER.)

`rimsky_instance_breakpoints`:

```sql
CREATE TABLE rimsky_instance_breakpoints (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instance_id      UUID NOT NULL REFERENCES rimsky_instances(id) ON DELETE CASCADE,
  matcher          JSONB NOT NULL,
  checkpoint       TEXT NOT NULL
                   CHECK (checkpoint IN ('before_dispatch','after_terminal')),
  signal_type      TEXT,  -- nullable; required NULL when checkpoint='before_dispatch' (enforced at API)
  mode             TEXT NOT NULL DEFAULT 'pause'
                   CHECK (mode IN ('pause','notify_only')),
  overflow_policy  TEXT NOT NULL
                   CHECK (overflow_policy IN ('drop_oldest','block_dispatch','auto_resume_after_ttl')),
  hit_ttl_seconds  INT NOT NULL DEFAULT 300,
  ttl_seconds      INT,
  dropped_count    BIGINT NOT NULL DEFAULT 0,
  created_by_key   TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at       TIMESTAMPTZ  -- materialized at create; NULL = instance-lifetime
);

CREATE INDEX idx_breakpoints_instance_active
  ON rimsky_instance_breakpoints (instance_id)
  WHERE expires_at IS NULL OR expires_at > NOW();

CREATE INDEX idx_breakpoints_expires
  ON rimsky_instance_breakpoints (expires_at)
  WHERE expires_at IS NOT NULL;
```

`rimsky_breakpoint_hits`:

```sql
CREATE TABLE rimsky_breakpoint_hits (
  seq             BIGSERIAL PRIMARY KEY,         -- monotonic cursor for resources/read pagination
  id              UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),  -- stable identity for the resume API
  breakpoint_id   UUID NOT NULL REFERENCES rimsky_instance_breakpoints(id) ON DELETE CASCADE,
  instance_id     UUID NOT NULL REFERENCES rimsky_instances(id),  -- denormalized for instance-scoped poll
  node_run_id     UUID,
  frame_id        UUID,
  checkpoint      TEXT NOT NULL,
  mode            TEXT NOT NULL,                 -- snapshotted from breakpoint at hit time
  snapshot        JSONB NOT NULL,
  hit_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resumed_at      TIMESTAMPTZ,                   -- nullable; set on resume (manual or auto)
  resumed_by_key  TEXT,                          -- nullable; api-key id that called resume
  resume_overlay  JSONB                          -- nullable; L6 fragment from resume body
);

CREATE INDEX idx_bp_hits_breakpoint_unresumed
  ON rimsky_breakpoint_hits (breakpoint_id, hit_at)
  WHERE resumed_at IS NULL;

CREATE INDEX idx_bp_hits_instance_seq
  ON rimsky_breakpoint_hits (instance_id, seq);

CREATE INDEX idx_bp_hits_breakpoint_seq
  ON rimsky_breakpoint_hits (breakpoint_id, seq);
```

Two identity columns intentionally: `seq BIGSERIAL` is the monotonic cursor for `resources/read` pagination (matching the `id BIGSERIAL` pattern on `rimsky_events`); `id UUID` is the stable identity referenced by the resume API (`POST /breakpoints/{bp_id}/resume` body's `hit_id`). UUID-only would break cursor ordering (`gen_random_uuid()` is v4 random, not monotonic); BIGSERIAL-only would force the API to expose integer hit IDs, inconsistent with the rest of the rimsky control-plane surface (which uses UUIDs for cross-process references). Both columns are cheap.

`instance_id` is denormalized intentionally — the agent's `resources/read` on instance-scoped URIs queries by `(instance_id, seq)`; the index supports it without a join. Pre-v1 break-freely permits the denormalization with no compat shim.

Both Postgres and SQLite carry the same definitions, modulo Postgres-specific syntax (`gen_random_uuid()` vs SQLite's UUID generation, `JSONB` vs SQLite's `JSON`/`TEXT`, etc., per the existing backend-specific shapes in the consolidated `001-schema.sql`).

### 7.3 Persistence interfaces

`foundation/persistence` adds two table accessors:

```go
// Following the existing persistence-interface convention in
// foundation/persistence/instances.go::InstanceTable: parameters
// take shared.UUID, not the bare uuid.UUID library type.

type BreakpointTable interface {
    Create(ctx context.Context, bp BreakpointRow, tx Tx) (shared.UUID, error)
    Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointRow, error)
    ListForInstance(ctx context.Context, instanceID shared.UUID, includeExpired bool, tx Tx) ([]BreakpointRow, error)
    Delete(ctx context.Context, id shared.UUID, tx Tx) error
    IncrementDropped(ctx context.Context, id shared.UUID, tx Tx) error
    SweepExpired(ctx context.Context, now time.Time, tx Tx) (int, error)
}

type BreakpointHitTable interface {
    Create(ctx context.Context, hit BreakpointHitRow, tx Tx) (shared.UUID, int64, error)  // returns id, seq
    Get(ctx context.Context, id shared.UUID, tx Tx) (*BreakpointHitRow, error)
    ListSinceForInstance(ctx context.Context, instanceID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
    ListSinceForBreakpoint(ctx context.Context, bpID shared.UUID, sinceSeq int64, limit int, tx Tx) ([]BreakpointHitRow, error)
    ListUnresumedForBreakpoint(ctx context.Context, bpID shared.UUID, tx Tx) ([]BreakpointHitRow, error)
    Resume(ctx context.Context, id shared.UUID, byKey string, overlay map[string]any, tx Tx) error
    AutoResumeStale(ctx context.Context, now time.Time, tx Tx) (int, error)
    DropOldest(ctx context.Context, bpID shared.UUID, keepCount int, tx Tx) (int, error)
    UnresumedCount(ctx context.Context, bpID shared.UUID, tx Tx) (int, error)
}
```

Implementations land at:

- `foundation/persistence/postgres/breakpoints.go`
- `foundation/persistence/postgres/breakpoint_hits.go`
- `foundation/persistence/sqlite/breakpoints.go`
- `foundation/persistence/sqlite/breakpoint_hits.go`

Hung off `persistence.Database.Tables()` per the existing accessor pattern (cf. `Instances()`, `Events()`).

### 7.4 Reaper integration

The existing `concept:orphan-reaper` cadence gains two new operations on each tick:

- `BreakpointTable.SweepExpired(ctx, now, tx)` — deletes breakpoints past `expires_at`.
- `BreakpointHitTable.AutoResumeStale(ctx, now, tx)` — auto-resumes hits past their TTL (when the breakpoint's `overflow_policy = 'auto_resume_after_ttl'`).
- `BreakpointHitTable.DropOldest(ctx, bpID, keepCount, tx)` — invoked synchronously by the supervisor's checkpoint evaluator when overflow occurs on a `drop_oldest` policy (not by the reaper).

These piggyback on the existing reaper tick (no new ticker, no new goroutine).

## 8. Auth surface

| Action | Bound route(s) / MCP shape | Default role-template membership |
|---|---|---|
| `instance:create` | `POST /instances` (existing; gains `paused` flag as parameter, no new verb) | unchanged: `admin`, `operator`, `agent-supervisor` (existing membership) |
| `instance:pause` | `POST /instances/{id}/pause` (new) | `debug-operator` (new); `admin` via `*:*` |
| `instance:resume` | `POST /instances/{id}/resume` (new) | `debug-operator`; `admin` |
| `breakpoint:read` | `GET /instances/{id}/breakpoints`; MCP `resources/list` and `resources/read` on `rimsky://instances/{id}/breakpoint-hits` and `rimsky://breakpoints/{bp_id}/hits` | `agent-supervisor` via existing `*:read`; `debug-operator`; `admin` |
| `breakpoint:create` | `POST /instances/{id}/breakpoints` | `debug-operator`; `admin` |
| `breakpoint:resume` | `POST /instances/{id}/breakpoints/{bp_id}/resume` | `debug-operator`; `admin` |
| `breakpoint:delete` | `DELETE /instances/{id}/breakpoints/{bp_id}` | `debug-operator`; `admin` |

New `Action` entries land in `code:control/controlapi/actions.go::v1Actions` per the existing canonical-registry pattern.

New role-template at `code:control/cli/roles/debug-operator.json`:

```json
{
  "name": "debug-operator",
  "description": "Permission bundle for live-instance debugging: pause/resume instances and install/inspect/resume/delete runtime breakpoints. High-risk in production — grant only to operators or agent keys that need to halt or mutate live dispatches.",
  "permissions": [
    { "action": "*:read" },
    { "action": "instance:pause" },
    { "action": "instance:resume" },
    { "action": "breakpoint:create" },
    { "action": "breakpoint:resume" },
    { "action": "breakpoint:delete" }
  ]
}
```

`agent-supervisor`'s existing permission set is unchanged. Agents with `agent-supervisor` keys can read breakpoint hits via `*:read`, but cannot create, resume, or delete breakpoints, nor pause / resume instances. Operators promote an agent to debugger authority by minting a new key with the `debug-operator` role (or by extending the agent's existing key with the additional verbs, per the existing `concept:permission` grammar).

Audit rows (per `concept:event-log` `auth.*` event kinds) record `protocol_skin: "http"` or `"mcp"` for each action invocation; payload includes `breakpoint_id` / `hit_id` / `instance_id` as relevant. No new audit event kinds.

## 9. Error handling

New error classes in `foundation/shared/errors.go`:

| Class | HTTP status | When |
|---|---|---|
| `ErrBreakpointNotFound` | 404 | Breakpoint id not in `rimsky_instance_breakpoints` |
| `ErrBreakpointHitNotFound` | 404 | Hit id not in `rimsky_breakpoint_hits` |
| (idempotent replay on already-resumed hit) | 200 | Hit already has `resumed_at != NULL`; response carries `first_resume: false` plus the original outcome. Not a separate error class. |
| `ErrMatcherInvalid` | 400 | Wrapped from `matcher.Validate` failure; payload includes offending key + reason |
| `ErrResumeOverlayInvalid` | 400 | Wrapped from resume-time JSON Schema validation failure; payload includes the schema error path |
| `ErrInstanceNotPaused` | 409 | Resume against an instance not in paused state |
| `ErrInstanceAlreadyPaused` | 409 | Pause against an already-paused instance |

`code:control/controlapi/app.go::writeError` handles HTTP translation per existing patterns. MCP error responses translate the same wrapped errors per the existing MCP error envelope handler.

Failure modes routed through existing surfaces (no new error class):

- A resume overlay that passes the pre-merge validation but fails the supervisor-side defense-in-depth re-validation surfaces as `template_validation_failed` per `concept:error-policy`. The dispatch terminates with that error class; existing `error_types:` routing applies. (This path covers schema drift between resume time and dispatch time — rare but possible if the template's effective schema is mutated mid-flight, which itself is uncommon.)
- `paused: true` on instance-create with a key that already exists for a different paused value: standard idempotency-conflict per `concept:instance` discipline.

Auto-resume of a stale hit (when `overflow_policy = 'auto_resume_after_ttl'` fires) emits a structured WARN log with `breakpoint_id`, `hit_id`, and time-since-hit. No API surface; sweep operation only. The eventual dispatch (which now proceeds with `resume_overlay = NULL`) is indistinguishable from a normal resume-without-overlay.

## 10. Testing strategy

### 10.1 Unit tests

- **`foundation/matcher/matcher_test.go`** — full grammar coverage migrated from `runtime/attribute_overrides_test.go`: all 5 keys, primitive types, nested attr paths, empty matcher, unknown-key defensive skip, primitive-equality coercion across `int` / `int64` / `float64` / `json.Number`.
- **`foundation/matcher/validate_test.go`** — registration-time validation: unknown key rejection, ordinal-key rejection (the existing `dispatch_index` / `nth_child` / `partition_index` / `seq` rejections preserved), cross-reference checks against node names, executor names, graph names.
- **`runtime/breakpoint_eval_test.go`** — supervisor-side breakpoint evaluation: per-checkpoint match dispatch, signal_type prefix filter on `after_terminal`, overflow_policy branching, mode branching (pause blocks, notify_only doesn't), the polling-for-resume loop, the L6 overlay merge.
- **`runtime/runner_dispatch_test.go` extension** — paused-instance candidate-selection filter (the new `AND paused = false` clause is exercised against test fixtures with paused and unpaused rows).
- **`control/controlapi/breakpoints_test.go`** — request validation per endpoint: matcher shape, signal_type-vs-checkpoint discipline, mode/policy/ttl enum acceptance, permission gates.
- **`control/controlapi/instances_test.go` extension** — `paused: true` instance-create accepted; idempotency-key + paused-value matrix; pause / resume idempotency error semantics.
- **`control/controlapi/attribute_overrides_test.go` adjustment** — existing tests continue to pass after the matcher validator is collapsed onto `matcher.Validate`; the by_match-specific wire-shape checks (top-level list shape, per-entry `{matcher, overlay}` envelope) are exercised separately.

### 10.2 Scenario tests

Under `test/scenarios/breakpoints/`:

- **Pause-and-resume happy path.** Install pause-mode breakpoint → dispatch matches → hit fires → resume without overlay → dispatch proceeds to terminal.
- **Resume-with-overlay.** Install pause-mode breakpoint → hit fires → resume with overlay that mutates an attribute → merged bag includes the mutation → terminal reflects it.
- **Resume-with-invalid-overlay.** Install pause-mode breakpoint → hit fires → resume with overlay that fails JSON Schema → 400 ErrResumeOverlayInvalid → hit stays paused → second resume with valid overlay succeeds.
- **Notify-only mode.** Install notify_only breakpoint → dispatch matches → hit fires → dispatch continues without waiting → terminal arrives → hit row still recorded → agent's next `resources/read` poll returns the hit.
- **Multi-breakpoint match.** Two pause-mode breakpoints with overlapping matchers → both match the same dispatch → per-iteration block contract: hit 1 is written and the supervisor blocks on `waitForResume`; after hit 1 resumes, hit 2 is written and the supervisor blocks again; only after the second resume does the dispatch proceed. The agent sees one hit per `resources/read` poll, not both at once. Breakpoint matchers are evaluated against a snapshot of the post-L5 attribute bag captured at function entry so iteration N+1's matcher does not observe iteration N's L6 resume overlay (spec §4.4).
- **Paused-on-create + install + release.** Create instance with `paused: true` → install pause-mode breakpoint → call `POST /instances/{id}/resume` → first dispatch hits breakpoint → confirm no dispatch fired before the release.
- **Soft instance pause.** Start instance → dispatch in flight → call `instance_pause` → in-flight dispatch runs to terminal naturally → confirm no new dispatch claimed → call `instance_resume` → next dispatch claimed normally.
- **Concurrent-frame correctness.** Frame A hits pause-mode breakpoint → frame B (different node, no match) keeps running → frame B reaches terminal while A is paused → A's resume releases → A's terminal arrives → both terminals visible in event log; lineage records correct.
- **Hit-queue overflow `drop_oldest`.** notify_only breakpoint with empty matcher → 150 dispatches → 50 hits dropped → `dropped_count = 50` on the breakpoint row → first 100 hits still queryable.
- **Hit auto-resume via TTL.** Pause-mode breakpoint with `overflow_policy = auto_resume_after_ttl` and `hit_ttl_seconds = 1` → hit fires → no resume call → sweeper fires within 2 seconds → dispatch proceeds (with no overlay) → terminal arrives.
- **Signal-type filter on after_terminal.** Install after_terminal breakpoint with `signal_type = 'terminal/error/*'` → dispatch succeeds (terminal/success) → no hit → dispatch fails (terminal/error/some_class) → hit fires.
- **Breakpoint expiry.** Install breakpoint with `ttl_seconds = 1` → sweep at t=2s → breakpoint deleted → subsequent matching dispatches don't hit.
- **Orphan hit on breakpoint deletion.** Install pause-mode breakpoint → dispatch hits → before resume, delete the breakpoint (cascade-deletes the hit per FK) → supervisor's polling loop sees the hit row gone → unblocks → dispatch proceeds without overlay (treated as auto-resume). Test verifies this path doesn't deadlock.

### 10.3 MCP scenario tests

Under `test/scenarios/mcp_resources/`:

- `resources/list` returns subscribable breakpoint-hit URIs filtered by the requesting key's permission grant (a key without `breakpoint:read` for an instance sees no URI for that instance).
- `resources/read` on `rimsky://instances/{id}/breakpoint-hits` returns the most recent N hits (paginated by the server's default limit), with full snapshot payload per §4.6.
- `resources/read` with `?since=<seq>` returns only hits with `seq > <since>`, ordered ascending; `next_since` in the response advances the cursor.
- Polling pattern (agent calls `resources/read` repeatedly with the accumulated `next_since` cursor) reliably surfaces every hit exactly once, in insertion order.
- A second `resources/read` against the same URI from a fresh MCP request — no session state — returns the same shape (`since`-cursor is request-carried, not server-tracked).

### 10.4 Conformance

No new conformance surfaces — breakpoints are control-plane only, not on any service protocol. Existing executor / claim-producer / publisher / lifecycle-subscriber conformance suites are unaffected.

### 10.5 Persistence consolidation tests

- Migration runner against a fresh database boots from `001-schema.sql` cleanly and reaches the same end-state as the previous 13-migration series (verified by schema-introspection test that compares column / index / constraint sets).
- Migration runner against a database whose `rimsky_migrations` table contains stale rows (pointing at `001-baseline.sql` through `013-*`) but otherwise empty (no actual schema objects from the old migrations) applies `001-schema.sql` cleanly — the orphan `rimsky_migrations` rows do not block the new file (no filename match). Test fixture: empty schema + pre-seeded `rimsky_migrations` rows. Actual stale-data upgrade (a populated dev DB carrying old schema and old `rimsky_migrations` rows) is explicitly NOT a supported path; operators drop and recreate per §3. The test guards the orphan-row case so that ephemeral CI environments with leftover migration state from prior runs still apply the new baseline.

## 11. Separation of concerns

The debugger spans persistence, runtime cooperation, and two transport adapters (HTTP and MCP). Three layers, no cross-layer awareness:

**Layer 1 — Core debugger domain.** Transport-neutral. No code here knows that an HTTP route exists, that MCP exists, or what URI scheme clients use.
- `foundation/matcher/` — matcher grammar, evaluator, validator. Shared with `attribute_overrides.by_match`.
- `foundation/persistence/breakpoints.go` — row types and table interfaces (`BreakpointTable`, `BreakpointHitTable`); typed-constant enums for `Mode` / `Checkpoint` / `OverflowPolicy`.
- `foundation/persistence/{postgres,sqlite}/breakpoints*.go` — backend storage impls.
- `runtime/breakpoint_eval.go` — supervisor cooperation, `EvaluateBreakpoints`, queue-overflow, `waitForResume`.
- `runtime/breakpoint_resume.go` — `ValidateAndPersistResume`: hit-resume validation discipline (read snapshot → merge overlay → validate against effective schema → persist). The transport-shaped resume handler delegates here so all transports share one entry point.
- `runtime/breakpoint_snapshot.go` — snapshot building from the acquisition.
- `runtime/signal_for_terminal.go` — signal envelope construction.

Layer 1 imports only `foundation/` primitives. Pure domain.

**Layer 2 — HTTP transport adapter.** Knows HTTP+JSON only.
- `control/controlapi/breakpoints.go` — six route handlers + the paused-on-create flag's handler updates. Each handler parses JSON, validates transport shape, calls into Layer 1 (`matcher.Validate`, persistence accessors, `runtime.ValidateAndPersistResume`), translates errors. No domain logic.
- `control/controlapi/instances.go` (extension) — `paused` flag + `POST /instances/{idOrKey}/pause`/`/resume`.

Layer 2 imports `foundation/matcher`, `foundation/persistence`, `foundation/shared`, and the runtime helpers. It does **not** import the MCP package, does **not** speak MCP wire shapes.

**Layer 3 — MCP transport adapter.** Knows the MCP wire only.
- `control/controlapi/mcp/server.go` — generic `ResourceCatalog` interface added alongside the existing `ToolCatalog`. No breakpoint-specific code.
- `control/controlapi/mcp_resources.go` (new) — `breakpointResourceCatalog`: the ONLY place the `rimsky://...` URI scheme is parsed; gates by permission; calls `BreakpointHitTable.ListSinceForInstance` / `ListSinceForBreakpoint`.

The MCP **tools** for create / list / delete / resume-hit are auto-derived from `v1Actions` and dispatch back through the chi router — they reuse Layer 2's HTTP handlers verbatim. So MCP tool surface ≡ HTTP surface; no parallel implementation. The MCP **resources** surface is new and lives entirely in Layer 3.

**Two properties that fall out of this layering:**

- *The breakpoint URI scheme lives in one file.* `control/controlapi/mcp_resources.go` is the only code site that knows `rimsky://instances/{uuid}/breakpoint-hits` exists. Layer 1 doesn't. Layer 2 doesn't. Adding a future SSE or webhook adapter is "write a new Layer 3 file"; nothing downstream changes.
- *Resume semantics live in one runtime helper.* `runtime.ValidateAndPersistResume` is the single domain entry point. The HTTP handler calls it; an eventual MCP-shaped resume (if push lands and resume becomes a notification-driven primitive) would call the same function. Validation discipline, merge logic, and persistence shape stay in Layer 1.

**Anti-patterns this layering explicitly prevents:**

- HTTP handlers reaching into the supervisor's runtime checkpoint code: not possible — `EvaluateBreakpoints` is called only by the supervisor's runner_dispatch.go and the two runApplyTerminal callers.
- MCP code parsing breakpoint matcher grammar: not possible — matcher validation runs inside the HTTP handler (which MCP tools dispatch through) and inside the runtime evaluator. MCP doesn't see matchers; it sees opaque JSONB in hit snapshots and persistence-shaped responses.
- Persistence accessors leaking transport vocabulary: not possible — the persistence interfaces (`BreakpointTable`, `BreakpointHitTable`) take `shared.UUID`, primitive types, and typed-constant enums. No URI strings, no HTTP request types, no MCP envelope types cross into `foundation/persistence/`.

## 12. Design changes

### 11.1 New concepts

- **Create `concepts/breakpoint.md`**. Definition: a runtime-installed pause-point on a live instance, identified by UUID and bound to `(matcher, checkpoint, signal_type?, mode, overflow_policy, ttl_seconds?)`. Persisted in `rimsky_instance_breakpoints`; hits in `rimsky_breakpoint_hits`. Purpose: enable agent-driven debugging of live instances — pause, inspect, optionally mutate via a one-shot overlay, resume. Boundaries: owns the breakpoint and hit tables, the `before_dispatch` / `after_terminal` supervisor checkpoints, the resume-with-overlay merge, the overflow policies. Does NOT own the matcher grammar (shared with `concept:attribute`'s `by_match` via `foundation/matcher`); does NOT own template-baked pauses (none exist — `concept:parked-state` is executor-emitted; this concept is operator-injected at runtime). Invariants: only the supervisor writes hits; resume is idempotent on `hit_id`; `signal_type` is rejected on `before_dispatch` breakpoints; the L6 resume overlay never persists into `attribute_overrides`. Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`.

### 11.2 Mutated concepts

- **Mutate `concepts/control-api.md`**. Update the "What it is" subsection: MCP capability extends from tools-only to tools + read-only resources (specifically `resources/list` and `resources/read`; no `resources/subscribe` and no server-pushed notifications in v1 — those require an MCP transport upgrade deferred to a future spec). Update the HTTP routes list to include `POST /instances/{id}/pause`, `POST /instances/{id}/resume`, `POST /instances/{id}/breakpoints`, `GET /instances/{id}/breakpoints`, `DELETE /instances/{id}/breakpoints/{bp_id}`, `POST /instances/{id}/breakpoints/{bp_id}/resume`. Append a Notes entry: `2026-05-24 — MCP capability extends from tools-only to tools + read-only resources per spec 2026-05-24-instance-debugger-design. resources/list and resources/read added to the dispatch switch; push (resources/subscribe + notifications/resources/updated) deferred to a future transport-upgrade spec. New /instances/{id}/pause and /resume routes added. New /instances/{id}/breakpoints/* routes added.`

- **Mutate `concepts/instance.md`**. Boundaries section: add "paused state column" to the Owns list. Invariants section: add "Candidate selection by the supervisor skips paused instances (the candidate query filter includes `AND paused = false`)." Append a Notes entry: `2026-05-24 — Adds rimsky_instances.paused BOOLEAN column and the corresponding pause / resume / paused-on-create surface per spec 2026-05-24-instance-debugger-design. Soft-pause semantics: in-flight dispatches run to terminal; new claims are held until resume.`

- **Mutate `concepts/supervisor.md`**. Boundaries section: add "breakpoint checkpoint evaluation at before_dispatch and after_terminal; blocked-runner polling for resume." Invariants section: add "Candidate selection skips paused instances and dispatches matching pause-mode breakpoints with unresumed hits." Append a Notes entry: `2026-05-24 — Adds breakpoint checkpoint cooperation per spec 2026-05-24-instance-debugger-design. Pause-mode breakpoints block the runner until resume; notify_only breakpoints emit a hit row and continue. Pause-mode block uses polling (200-250ms) on rimsky_breakpoint_hits.resumed_at; no cross-process IPC bus.`

- **Mutate `concepts/signal.md`**. Append a Notes entry: `2026-05-24 — concept:breakpoint consumes signal type-paths via the signal_type filter on after_terminal breakpoints (prefix-only, trailing-* wildcards, validated via foundation/signal/taxonomy.go::ValidateTypePath). No taxonomy change; concept:signal is read-only consumer.`

- **Mutate `concepts/attribute.md`**. Append a Notes entry: `2026-05-24 — Matcher grammar (the closed 5-key dispatch-identity predicate from by_match) extracts to foundation/matcher/ per spec 2026-05-24-instance-debugger-design. concept:breakpoint reuses the package. by_match wire shape, semantics, and merge layering unchanged.`

- **Mutate `concepts/persistence-database.md`**. Append a Notes entry: `2026-05-24 — Migration history flattened per spec 2026-05-24-instance-debugger-design. The 14 numbered migrations (001-baseline through 014-drop-last-outcome) are deleted and replaced with a single consolidated 001-schema.sql per backend reflecting current schema state plus the new breakpoint tables and rimsky_instances.paused column. Pre-v1 break-freely operation; existing dev databases drop and recreate. Adds BreakpointTable and BreakpointHitTable accessors on Tables().`

- **Mutate `concepts/role-template.md`**. Update the "V1 ships" list to include `debug-operator`. Append a Notes entry: `2026-05-24 — Adds debug-operator role-template per spec 2026-05-24-instance-debugger-design. Bundles *:read, instance:pause, instance:resume, breakpoint:create, breakpoint:resume, breakpoint:delete. High-risk in production; grant explicitly. agent-supervisor unchanged.`

- **Mutate `concepts/permission.md`**. Append a Notes entry: `2026-05-24 — Adds breakpoint:* and instance:pause / instance:resume action verbs to the canonical registry per spec 2026-05-24-instance-debugger-design. breakpoint:read covered by *:read wildcard; the four writes (create, resume, delete, instance:pause, instance:resume) require explicit grant via the new debug-operator role-template.`

- **Mutate `concepts/parked-state.md`**. Append a Notes entry: `2026-05-24 — concept:breakpoint is the operator-injected sibling to executor-emitted parked-state. Breakpoint pause-mode blocks the runner at supervisor checkpoints; parked-state is the executor's own hold via Park terminal. The two are distinct primitives serving different control directions; per spec 2026-05-24-instance-debugger-design.`

- **Mutate `concepts/inertness.md`**. The current sanctioned-read-site enumeration (cross-cutting Boundaries / Sanctioned read sites subsection) cites `evaluateMatcher` at `code:runtime/attribute_overrides.go` for attribute-value matcher predicates. This spec moves that function to the new `foundation/matcher/` package as `code:foundation/matcher/matcher.go::Evaluate` (the `attrs.<path>` branch). Update the enumerated sanctioned-site list to point at the new location; the discipline (primitive-equality only, no logging, no formatting of values) is preserved verbatim in the extracted code. Append a Notes entry: `2026-05-24 — Matcher evaluator extracted to foundation/matcher/ per spec 2026-05-24-instance-debugger-design. The sanctioned attribute-value read site for matcher predicates is now code:foundation/matcher/matcher.go::Evaluate (attrs.<path> branch). by_match in runtime/attribute_overrides.go::applyAttributeOverrides delegates to the shared package; the inertness discipline is unchanged.`

### 11.3 Tensions

No tension changes. `tension:events-kind-no-enum` is unaffected. Two cases worth being explicit about:

- **Breakpoint hits** live in their own table (`rimsky_breakpoint_hits`), not in `rimsky_events`. No new `kind` strings on `rimsky_events.kind` come from the hits themselves.
- **API audit rows** for the new debugger endpoints DO write to `rimsky_events` via the existing `auth.access_attempted` / `auth.access_denied` event kinds added by `spec:2026-05-15-control-plane-mcp-and-auth-design` (per `concept:event-log`'s "Auth event kinds" subsection). No new audit `kind` strings are introduced; the `payload` JSONB carries the new fields (`breakpoint_id`, `hit_id`, `instance_id`) inside the existing envelope shape.

Net: the tension's "kind typos produce events no consumer finds" failure mode is unchanged by this spec.
