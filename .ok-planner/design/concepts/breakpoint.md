---
concept: breakpoint
status: as-is
aliases: []
---

# Breakpoint

## What it is

A breakpoint is a runtime-installed pause-point on a live `concept:instance`, identified by a stable identifier and bound to a matcher, a checkpoint position, an optional signal-type filter, a mode (pause vs notify-only), an overflow policy, and an optional self-deletion TTL. Persisted in a per-instance breakpoint ledger; hits in a separate hit ledger. The breakpoint's dimensions span: where in the supervisor's per-dispatch flow it fires (pre-dispatch vs post-terminal), whether it blocks the runner or only records, how to handle hits that arrive faster than they are drained, and how long the breakpoint itself lives.

## Purpose

Enable agent-driven debugging of live rimsky instances. The agent installs breakpoints at the dispatch points it cares about, optionally pauses execution, inspects the snapshot, and optionally mutates the dispatch via a one-shot overlay before resuming. This is the runtime-cooperative half of `concept:control-api`'s debugger surface; `concept:instance`'s paused/resume affordance is the other half (instance-level hold).

## Boundaries

Owns: the per-instance breakpoint ledger and the hit ledger (schema, CRUD, sweeps); the supervisor pre-dispatch and post-terminal checkpoint logic; the resume-with-overlay L6 merge; the per-mode overflow policies and a queue-cap on unresumed hits.

Does NOT own: the matcher grammar itself (shared with `concept:attribute`'s by_match via the common foundation matcher package); template-baked pauses (none exist — `concept:parked-state` is executor-emitted, this concept is operator-injected at runtime); the audit-log emission for the API surface (covered by the existing auth audit event kinds per `concept:event-log`); hit *delivery* (`concept:control-api` owns it, exposing **both** the read-only MCP resource-listing and resource-read extension and a read-only REST route that surface hits — this concept owns the ledger, not the transport).

Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`, `concept:parked-state`.

## Invariants

- Only the supervisor writes hit rows.
- Resume is idempotent on `hit_id`: replays return the original outcome unchanged.
- A signal-type filter is rejected on pre-dispatch breakpoints at registration (the filter only makes sense on post-terminal hits).
- Pause-mode hits combined with a silently-dropping overflow policy are rejected at registration (pause-mode hits cannot be silently dropped).
- Notify-only mode combined with a blocking overflow policy is rejected at registration (the policy contradicts the mode's non-blocking semantics).
- The L6 resume overlay applies only to the single dispatch that hit the breakpoint; it never persists into the instance's stored attribute-overrides.
- An L6 resume overlay on a post-terminal hit is rejected at the resume API as an invalid-overlay error — the dispatch the breakpoint observed has already committed, so the overlay can never feed back into the run; accepting it would silently no-op.
- Cascade-deletion of a breakpoint (the hit rows are deleted with their parent breakpoint) unblocks any paused runner waiting on a hit of that breakpoint, treating the missing-row case as auto-resume with no overlay.

## Policy differences from `by_match`

The breakpoint matcher shares its grammar with `concept:attribute`'s `by_match` overrides via the common foundation matcher package, but the validator's used-executors cross-check is intentionally laxer on the breakpoint side:

- The attribute by-match overlay rejects an executor key that names an executor not referenced by any node in the template (the override is dead). Implemented by passing a populated set of used-executor names to the matcher validator.
- Breakpoint matchers leave the used-executors set empty so an operator can install a breakpoint against any declared executor — including ones the current template doesn't dispatch to. This supports cross-template debugger habits (an operator who runs a debug session against many templates can carry one matcher pinned to a specific executor even on templates that happen not to use that executor; the breakpoint just doesn't fire).

The breakpoint matcher still enforces every other cross-check: declared node types, existing graphs, declared deployment-level executors, and the closed grammar. This is enforced by the control-api breakpoint matcher-refs check.
