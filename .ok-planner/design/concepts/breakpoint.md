---
concept: breakpoint
---

# Breakpoint

## What it is

A breakpoint is a runtime-installed pause-point on a live `concept:instance`, identified by a stable identifier and bound to a matcher, a checkpoint position, an optional signal-type filter, a mode (pause vs notify-only), an overflow policy, and an optional self-deletion TTL. Persisted in a per-instance breakpoint ledger; hits in a separate hit ledger. The breakpoint's dimensions span: where in the supervisor's per-dispatch flow it fires (pre-dispatch vs post-terminal), whether it blocks the runner or only records, how to handle hits that arrive faster than they are drained, and how long the breakpoint itself lives.

## Purpose

Enable agent-driven debugging of live rimsky instances. The agent installs breakpoints at the dispatch points it cares about, optionally pauses execution, inspects the snapshot, and optionally mutates the dispatch via a one-shot overlay before resuming. An unresumed pause-mode hit also gates `concept:control-api`'s debug-override channel, which lets the agent apply persistent node-attribute writes and invalidations against the paused instance — a separate, persistent mutation path distinct from the one-shot resume overlay. This is the runtime-cooperative half of `concept:control-api`'s debugger surface; `concept:instance`'s paused/resume affordance is the other half (instance-level hold).

The debug-override channel is frame-scoped: both of its verbs resolve "the node's latest run" as the latest run within the instance's currently active frame only, never a run from any other frame. A node with no run at all in the active frame is a no-op; a node whose only runs belong to a different frame is refused outright, naming the frame mismatch, rather than silently paired with the active frame. Within the active frame, a node-run that has already reached a terminal state is never mutated in place — an attribute write targeting such a node instead lands on a freshly created invalidated run for that node, which carries the terminal run's attribute bag forward. A node-run merely paused mid-frame (held or parked, e.g. sitting at a breakpoint) is not terminal and remains a legal direct target for both verbs. When the in-frame run is dispatched and in flight (running, held, or parked), an attribute write both lands on that run's live bag and queues one operator-invalidate stale run behind it, whose creation-time snapshot carries the merged bag forward — so the value takes effect immediately in the paused run and again on the post-resume re-run (per `story:operator-invalidate-queues-during-flight`); a run still waiting in the queue (stale or pending) just receives the merge, with no extra row.

## Boundaries

Owns: the per-instance breakpoint ledger and the hit ledger (schema, CRUD, sweeps); breakpoint matcher evaluation and hit-recording at the pre-dispatch and post-terminal checkpoints; the resume-with-overlay merge — layer L6, applied after `concept:attribute`'s L5 override merge — that feeds the one-shot resume payload into the dispatch; the per-mode overflow policies and a queue-cap on unresumed hits.

Does NOT own: the matcher grammar itself (shared with `concept:attribute`'s by_match via the common foundation matcher package); template-baked pauses (none exist — `concept:parked-state` is executor-emitted, this concept is operator-injected at runtime); the audit-log emission for the API surface (covered by the existing auth audit event kinds per `concept:event-log`); hit *delivery* (`concept:control-api` owns it, exposing **both** the read-only MCP resource-listing and resource-read extension and a read-only REST route that surface hits — this concept owns the ledger, not the transport); the runner's checkpoint wiring — invoking evaluation at the pre-dispatch and post-terminal points in the dispatch flow — and the blocked-runner resume-polling loop (owned by `concept:supervisor`: this concept owns what happens when a checkpoint is evaluated, not where in the dispatch flow it is invoked from).

Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`, `concept:parked-state`.

## Invariants

- Only the supervisor creates hit rows; resume and TTL housekeeping sweeps by other roles may update or delete an existing hit row, but no non-supervisor role inserts one.
- Resume is idempotent on `hit_id`: replays return the original outcome unchanged.
- A signal-type filter is rejected on pre-dispatch breakpoints at registration (the filter only makes sense on post-terminal hits).
- Pause-mode hits combined with a silently-dropping overflow policy are rejected at registration (pause-mode hits cannot be silently dropped).
- Notify-only mode combined with a blocking overflow policy is rejected at registration (the policy contradicts the mode's non-blocking semantics).
- The L6 resume overlay applies only to the single dispatch that hit the breakpoint; it never persists into the instance's stored attribute-overrides.
- A resume overlay joins the dispatch's effective attribute bag the moment it is applied: when several breakpoints pause the same dispatch and resume in sequence, each later breakpoint's matcher evaluates against — and each later hit's snapshot records — the bag as amended by every earlier resume's overlay, so what the operator inspects at a pause is what the dispatch will actually run with.
- An L6 resume overlay on a post-terminal hit is rejected at the resume API as an invalid-overlay error — the dispatch the breakpoint observed has already committed, so the overlay can never feed back into the run; accepting it would silently no-op.
- Cascade-deletion of a breakpoint (the hit rows are deleted with their parent breakpoint) unblocks any paused runner waiting on a hit of that breakpoint, treating the missing-row case as auto-resume with no overlay.
- Pause-mode breakpoint evaluation fails closed: an infrastructure error while evaluating or persisting a pause-mode hit blocks the dispatch rather than silently skipping the pause — a pause is an operator-requested gate, and the dispatch does not proceed past it on error. After-terminal (observation-only) evaluation fails open: failures are logged, never blocking settlement.
- The pre-dispatch checkpoint fires exactly once per executor-invoking dispatch attempt — once, before the executor is invoked, and not again on any subsequent retry re-invocation of the executor — regardless of how the dispatch's attribute bag was sourced: a sealed bag built earlier per `concept:node-run`, then substitution and override application at dispatch. Every branch of the dispatcher's attribute-resolution path reaches the checkpoint before the executor invocation. Executor-less dispatch attempts (the pure-cascade and claim-acquired dispositions) skip the pre-dispatch checkpoint: with no executor invocation, the assembled bag cannot mutate after assembly, so there is nothing to gate or overlay pre-dispatch; those dispatches are observed at the post-terminal checkpoint only.
- The debug-override channel never pairs the instance's currently active frame with a `concept:run-scope` or node-run that belongs to a different frame — per `concept:frame`'s perfect frame isolation, a RunScope never spans frames, so a node's latest run from a prior or otherwise inactive frame is not a legal invalidation or attribute-write target; a request that would require such a cross-frame pairing is refused. Attribute writes additionally never mutate a terminal node-run row in place, even one that belongs to the active frame — the write is redirected onto a freshly created invalidated run instead, which carries the terminal run's attribute bag forward.

## Policy differences from `by_match`

The breakpoint matcher shares its grammar with `concept:attribute`'s `by_match` overrides, but the validator's used-executors cross-check is intentionally laxer on the breakpoint side:

- The attribute by-match overlay rejects an executor key that names an executor not referenced by any node in the template (the override is dead).
- Breakpoint matchers treat every declared executor as valid regardless of template usage, so an operator can install a breakpoint against any declared executor — including ones the current template doesn't dispatch to. This supports cross-template debugger habits (an operator who runs a debug session against many templates can carry one matcher pinned to a specific executor even on templates that happen not to use that executor; the breakpoint just doesn't fire).

The breakpoint matcher still enforces every other cross-check: declared node types, existing graphs, declared deployment-level executors, and the closed grammar.
