# Intent Dossier: breakpoint

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Breakpoint is the operator-injected sibling of executor-emitted parked-state — two distinct primitives serving different control directions (2026-05-24, instance-debugger, artifact). The v1 use case is an LLM agent driving rimsky via MCP.
- Exactly two supervisor checkpoints: before_dispatch (after the post-L5 merged bag, before Execute) and after_terminal (wired at the two callers of runApplyTerminal, after the terminal tx commits and before cascade walks) — the caller wiring preserves the no-tx-held-across-wait invariant (2026-05-24, artifact).
- Two modes: pause (blocks the supervisor runner until resumed; the default) and notify_only (records a hit row and continues) (2026-05-24, artifact).
- The matcher is the shared closed 5-key dispatch-identity grammar (node_type, executor, graph, child_key, attrs) in a shared foundation matcher package: equality-only, AND-joined, missing keys wildcard, empty matcher fires for every dispatch; signal_type is deliberately a separate field, not part of the grammar (2026-05-24, artifact).
- Hits live in their own table rimsky_breakpoint_hits (seq BIGSERIAL pagination cursor + id UUID resume identity, instance_id denormalized); cross-process resume signaling is DB polling at 250ms — binaries coordinate exclusively through the database (2026-05-24, artifact).
- Concept invariants: only the supervisor writes hits; resume is idempotent on hit_id; signal_type rejected on before_dispatch; the L6 resume overlay never persists into attribute_overrides (2026-05-24, artifact).
- Direct node targeting exists ONLY via the breakpoint/pause debug channel; there is no generic way to invalidate or re-run specific nodes (2026-06-15, 4c42fe5b, transcript).
- Breakpoints live in code at lib/runtime/breakpoint_*.go (2026-06-17, b31002b8, transcript).

## Required behaviors (open promises)

- The agent-debugger flow: create an instance with dispatch held until breakpoints are installed; install breakpoints with matcher + optional signal-type filter; discover hits via MCP resources/read polling with a full dispatch-context snapshot; optionally mutate via a one-shot overlay on resume; pause/unpause running instances (2026-05-24, instance-debugger, artifact).
- Hit snapshot content: seq, hit/breakpoint/instance/node_run/frame IDs, checkpoint and mode, dispatch identity with the full post-L5 merged attribute bag, terminal signal (after_terminal only), a node-run projection, and held-claims / open-wait-set summaries only — claim content stays opaque even to the debugger (2026-05-24, artifact).
- Unresumed-hit queue capped at 100 with overflow_policy drop_oldest (notify_only only, increments visible dropped_count) / block_dispatch (pause only) / auto_resume_after_ttl (either); pause+drop_oldest and notify_only+block_dispatch rejected 400 at create — pause-mode hits can never be silently dropped (2026-05-24, artifact).
- Resume is idempotent on hit_id (second call → 200 with first_resume: false and the original outcome, no new state); resuming a hit whose breakpoint was deleted → 404; deleting a breakpoint cascade-deletes hits and a runner blocked on one detects the row's disappearance and proceeds without overlay — must not deadlock, scenario-covered (2026-05-24, artifact).
- Resume-overlay validation is both-layers: shape + pre-merge validation against the dispatch's effective schema at control-api (400 ErrResumeOverlayInvalid), then supervisor-side defense-in-depth re-validation routed through template_validation_failed; the snapshot carries effective_schema and an absent field logs Warn and defers to the supervisor gate (2026-05-24, artifact).
- Per-iteration block contract for multiple matching pause breakpoints: hit N resumes before hit N+1 is written; matchers evaluate against a snapshot of the post-L5 bag captured at function entry so a later matcher never observes an earlier hit's L6 overlay (2026-05-24, artifact).
- Two-level TTLs: ttl_seconds auto-removes the breakpoint (expires_at; NULL = instance-lifetime); hit_ttl_seconds (default 300) bounds unresumed hits under auto_resume_after_ttl; housekeeping piggybacks on the orphan-reaper tick (SweepExpired, AutoResumeStale with structured WARN); an auto-resumed dispatch is indistinguishable from resume-without-overlay (2026-05-24, artifact).
- signal_type filter: after_terminal only (400 on before_dispatch), validated at registration against the signal taxonomy, trailing-* wildcards only, prefix-matched against the terminal type-path (2026-05-24, artifact); the create API rejects retired event/<name> and event/* paths with 400 via signal.ValidateSubscriptionType (2026-06-17, b31002b8, transcript).
- Empty-string child_key is rejected at the grammar level (matching non-fan-out dispatches = omit the key) (2026-05-24, artifact).
- Async-callback after_terminal contexts carry no GraphName/child_key, so breakpoints intended to fire there must leave graph and child_key absent (2026-05-24, instance-debugger-divergences, artifact).
- GET /instances/{idOrKey}/breakpoint-hits over REST under breakpoint:read, mirroring the MCP resource's {hits, next_since, truncated} shape with ?since=&limit=; deliberately HTTP-only (no second MCP GET tool) (2026-05-28, quality-of-life-features, artifact).
- MCP resources: two coexisting URI families (instance-scoped and breakpoint-scoped hits), ?since=<seq> cursor, ascending seq, server-enforced limit, request-carried cursor with no server session state; the rimsky:// URI scheme parsed in exactly one file (2026-05-24, artifact).
- `rimsky instance status` and `rimsky watch` are client-side aggregators over existing read endpoints including pending breakpoint hits; watch shows events, hits, and the terminal line in one timestamp-ordered feed, never source-grouped (2026-05-28; 2026-06-06, comprehensive-gap-closure, artifact).
- The before_dispatch checkpoint fires exactly once per dispatch attempt regardless of how the attribute bag was sourced (sealed/snapshot, schema-less, or full substitution) (2026-06-21, 10cf843b, transcript).
- The debug-override channel: single endpoint POST /instances/{id}/debug/override with discriminated actions invalidate_node and set_attribute, applied synchronously in the request tx; legal ONLY when the instance is explicitly paused or an unresumed pause-mode hit is blocking a runner; refused with 409 outside the gate (never silently no-oped); guarded by a dedicated permission scope not on standard operator keys; every use emits a debug.override.applied audit event; overrides do not persist beyond the running frame (2026-06-14, bfc9febb, transcript).
- Soft pause is the only pause semantics: stop claiming new dispatches, let in-flight run to terminal; paused-candidate selection is a WHERE paused = false filter; hard-pause-at-next-checkpoint is composed from soft pause + an empty-matcher pause breakpoint (2026-05-24, artifact).
- No new event kinds for the debugger's audit surface: API audit rows reuse auth.access_attempted / auth.access_denied with breakpoint_id / hit_id / instance_id in the existing payload envelope (2026-05-24, artifact).

## Intentional absences

- MCP push (resources/subscribe, server-pushed updates) — out of v1; polling via resources/read, with the resource shape designed so push is purely additive (2026-05-24, artifact).
- Webhook / SSE destinations for hit delivery — declined for v1; MCP (+ the REST route) only (2026-05-24; 2026-05-28, artifact).
- Promote-a-hit-to-overlay ergonomic — skipped for v1 (2026-05-24, artifact).
- Hard pause (preempting in-flight dispatches) as a primitive — rejected outright (2026-05-24, artifact).
- General live event-log subscription in the debugger — telemetry is a separate concern; breakpoint hits are the debugger's only feedback surface (2026-05-24, artifact).
- An on_acquire_failed checkpoint — pre-dispatch failures (e.g. acquisition-unavailable) are NOT caught by after_terminal (the checkpoint names a lifecycle position, not a signal type); breaking on them is out of v1 scope (2026-05-24, artifact).
- A general operator verb to re-run or invalidate arbitrary nodes — the operator-invalidate route's retirement is intentional; "re-running arbitrary nodes breaks the model. debug feature (with permission) only" (2026-06-15, 4c42fe5b, transcript). node/reset's wake half retires with the synthetic-envelope purge (2026-06-15, 4c42fe5b, transcript).
- Events streaming (SSE on GET /events) — out of v1 scope; live-tail is a UX improvement, not a capability gap (2026-06-18, 9fb55f08, transcript).
- Parked frames and stuck-past-timeout frames as debug-override gate states — those are degraded states the override would paper over; the gate is paused-or-breakpoint only (2026-06-14, bfc9febb, transcript, superseding the same session's earlier four-state gate).

## Corrections and restorations (drift-fight record)

- The planned partial index WHERE expires_at IS NULL OR expires_at > NOW() is invalid Postgres (NOW() is STABLE); shipped idx uses WHERE expires_at IS NULL with time filtering at query sites (2026-05-24, instance-debugger-divergences, artifact).
- concepts.md TOC never gained the breakpoint entry even though concepts/breakpoint.md shipped — generator bug left visible (2026-05-24, instance-debugger-divergences, artifact).
- `rimsky watch` double-printed hits (drained both /events and the pending-hits route) — reworked to drain /events alone; the pending-hits route deliberately remains as a point-in-time status surface, not dead code (2026-06-06, comprehensive-gap-closure-divergences, artifact).
- Plan tasks wrongly marked N/A because literal directory paths didn't exist — breakpoints do exist (lib/runtime/breakpoint_*.go); hunts must ground on symbols, not paths (2026-06-17, b31002b8, transcript).

## Superseded / historical

- "Breakpoint hits introduce no new event kind strings / live only in their own table" (2026-05-24) — partially superseded by the 2026-06-06 artifact adding breakpoint.hit rows to the unified event log; see Conflicts.
- Earlier four-state debug-override gate (stuck, parked, paused, breakpoint) — narrowed the same session to paused + unresumed pause-mode hit only (2026-06-14, bfc9febb, transcript).
- The "instance breakpoint" sibling primitive parked as a future sketch in the matcher-overlay spec (2026-05-21) — delivered as this concept by the 2026-05-24 instance-debugger spec.

## Conflicts needing human ruling

- breakpoint.hit on the event log: the 2026-06-06 artifact promises breakpoint.hit rows appended co-transactionally to /events (GET /events?kind=breakpoint.hit) and records the watch rework as relying on exactly that ("breakpoint.hit now lands on /events"); the later, higher-tier 2026-06-18 transcript (9fb55f08) rules "breakpoint.hit emission onto the event-log" out of v1 scope with the poll-based /events endpoint and the pollable hits route as the accepted fallback. Strict precedence favors the transcript, but it declares out-of-scope something the artifact tier records as already shipped and load-bearing for `rimsky watch`. A human must rule whether event-log hit emission is required, merely tolerated, or to be removed.
