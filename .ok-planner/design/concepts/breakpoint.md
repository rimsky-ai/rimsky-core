---
concept: breakpoint
status: as-is
aliases: []
references:
  - ../../specs/2026-05-24-instance-debugger-design.md
---

# Breakpoint

## What it is

A breakpoint is a runtime-installed pause-point on a live `concept:instance`, identified by UUID and bound to a `(matcher, checkpoint, signal_type?, mode, overflow_policy, ttl_seconds?)` tuple. Persisted in `table:rimsky_instance_breakpoints`; hits in `table:rimsky_breakpoint_hits`.

- `matcher` — closed five-key predicate from `concept:attribute`'s by_match shape (shared via `code:foundation/matcher/`); see `concept:attribute` §Matcher grammar.
- `checkpoint` — `before_dispatch` or `after_terminal`; identifies where in the supervisor's per-dispatch flow the breakpoint fires.
- `signal_type` — optional prefix-match against `concept:signal` type-paths (valid only for `after_terminal`); the operator's way to express "break only on terminal/error/*" etc.
- `mode` — `pause` (block the runner until resume) or `notify_only` (record the hit and continue).
- `overflow_policy` — `drop_oldest` (notify_only-only; default), `block_dispatch` (pause-only; default), or `auto_resume_after_ttl` (per-hit timeout).
- `ttl_seconds` — optional auto-deletion of the breakpoint itself; instance-lifetime when NULL.

## Purpose

Enable agent-driven debugging of live rimsky instances. The agent installs breakpoints at the dispatch points it cares about, optionally pauses execution, inspects the snapshot, and optionally mutates the dispatch via a one-shot overlay before resuming. This is the runtime-cooperative half of `concept:control-api`'s debugger surface; `concept:instance`'s paused/resume affordance is the other half (instance-level hold).

## Boundaries

Owns: `table:rimsky_instance_breakpoints` and `table:rimsky_breakpoint_hits` (schema, CRUD, sweeps); the `before_dispatch` and `after_terminal` supervisor checkpoint logic in `code:runtime/breakpoint_eval.go`; the resume-with-overlay L6 merge; the per-mode overflow policies and the queue-cap (100 unresumed hits per breakpoint).

Does NOT own: the matcher grammar itself (shared with `concept:attribute`'s by_match via `code:foundation/matcher/`); template-baked pauses (none exist — `concept:parked-state` is executor-emitted, this concept is operator-injected at runtime); the audit-log emission for the API surface (covered by the existing `auth.*` event kinds per `concept:event-log`); the MCP transport for hit delivery (`concept:control-api` owns the read-only `resources/list` / `resources/read` extension that surfaces hits).

Adjacent: `concept:supervisor`, `concept:control-api`, `concept:attribute`, `concept:instance`, `concept:signal`, `concept:permission`, `concept:parked-state`.

## Invariants

- Only the supervisor writes hit rows (`@blessed-invariant` candidate).
- Resume is idempotent on `hit_id`: replays return the original outcome unchanged.
- `signal_type` is rejected on `before_dispatch` breakpoints at registration.
- `mode=pause + overflow_policy=drop_oldest` is rejected at registration (pause-mode hits cannot be silently dropped).
- `mode=notify_only + overflow_policy=block_dispatch` is rejected at registration (the policy contradicts the mode's non-blocking semantics).
- The L6 resume overlay applies only to the single dispatch that hit the breakpoint; it never persists into `col:rimsky_instances.attribute_overrides`.
- An L6 resume overlay on an `after_terminal` hit is rejected at the resume API with `ErrResumeOverlayInvalid` — the dispatch the breakpoint observed has already committed, so the overlay can never feed back into the run; accepting it would silently no-op.
- Cascade-deletion of a breakpoint (via ON DELETE CASCADE on `rimsky_breakpoint_hits.breakpoint_id`) unblocks any paused runner waiting on a hit of that breakpoint, treating the missing-row case as auto-resume with no overlay.

## Policy differences from `by_match`

The breakpoint matcher shares its grammar with `concept:attribute`'s `by_match` overrides via `code:foundation/matcher/`, but the validator's `UsedExecutors` cross-check is intentionally laxer on the breakpoint side:

- `by_match` rejects an `executor:` key that names an executor not referenced by any node in the template (the override is dead). Implemented by passing a populated `matcher.ValidationRefs.UsedExecutors`.
- Breakpoint matchers leave `UsedExecutors` nil so an operator can install a breakpoint against any declared executor — including ones the current template doesn't dispatch to. This supports cross-template debugger habits (an operator who runs a debug session against many templates can carry one `executor: claude-agent` matcher even on templates that happen not to use it; the breakpoint just doesn't fire).

The breakpoint matcher still enforces every other cross-check: `node_type` must be declared, `graph` must exist (or be `main`), `executor` must be a declared deployment-level executor name, and the closed five-key grammar applies. The site is `code:control/controlapi/breakpoints.go::breakpointMatcherRefs`.

## Aliases and historical names

None.

## Open within this concept

(none yet)

## Notes

- 2026-05-24 — Introduced per spec `.ok-planner/specs/2026-05-24-instance-debugger-design.md`.
