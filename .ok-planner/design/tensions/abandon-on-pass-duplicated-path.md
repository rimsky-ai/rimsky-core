---
tension: abandon-on-pass-duplicated-path
category: muddy-boundary
status: open
affects:
  - terminal-resolution
  - auto-terminal
  - lifecycle-handler
---

# `Producer.Abandon` on already-Open'd claims fires from two sites that do not route through `ResolveClaimHandleTerminal`

## What is muddy

`foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` is documented (lines 5-27) as the single audited site for the producer-verb-fire + claim-handle-delete sequence. Both the active-terminal path (`releaseLocksInTx` calling directly) and the held-terminal path (`CheckAndFireResolution` calling on aggregate completion) route through it.

But there are two pre-dispatch / handler-pass siblings that *don't*:

1. `foundation/integration/runner_lifecycle.go::handleAcquireUnavailable` (lines 76+) — when `tryAcquire` returns the `errAcquireUnavailable` sentinel and the `OnAcquireUnavailable` handler resolves to `pass` or `error`, the function calls `Producer.Abandon` directly on every already-Open'd claim. This is before any dispatch happens, so the active-terminal spine never runs.
2. `foundation/integration/runner_terminal_handlers.go::applyTerminalPass` (lines 79-121) — when an `OnExecutorBlocked` or `OnExecutorErrored` handler resolves to `pass`, the function calls `releaseLocksInTx(success=false)`, which dispatches per-lock; for the held branch this still routes through `CheckAndFireResolution` → `ResolveClaimHandleTerminal`, but the non-held active-claim branch calls `ResolveClaimHandleTerminal` itself, while the *direct* Abandon path historically lived alongside `handleOrphanedClaim` — and the comment at `runner_terminal_handlers.go:75-77` explicitly notes the duplication ("mirrors handleOrphanedClaim's Abandon-then-clear").

The "single audited site" framing breaks at the `OnAcquireUnavailable` pass/error case: no executor call was made, no dispatch row exists in the same state, so the unified engine's preconditions don't apply.

## Why it matters

The unified-engine narrative is load-bearing for `@blessed-invariant 13` ("Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven") and for the spec's §7.3 statement that the producer verb + claim-handle delete is a single audited sequence. A reader walking the spine for the first time sees `ResolveClaimHandleTerminal` as the bottom of the pipe and reasonably assumes "every producer.Abandon happens here" — only to discover that the pre-dispatch unavailability path and the `applyTerminalPass` early-out do their own direct calls.

If a future change adds telemetry, a metric, or an audit-event emit at `ResolveClaimHandleTerminal`, the pre-dispatch and pass paths silently miss it.

## Resolution candidates (do NOT pick)

- Refactor `handleAcquireUnavailable` to construct a synthetic `acq.Locks` slice and route through `releaseLocksInTx` → `ResolveClaimHandleTerminal`.
- Extract a smaller "abandon-already-opened-claim" helper that both sites call, with the same audit emit + delete sequence.
- Re-scope the unified-engine narrative to "all post-dispatch terminal paths" and document the pre-dispatch carve-out explicitly at `terminal_decision.go:5-27`.

## Evidence

- `_discover/terminal-resolution.md` Observations bullet "duplicated Abandon-on-pass-or-error logic".
- `foundation/integration/runner_lifecycle.go:32-150`.
- `foundation/integration/runner_terminal_handlers.go:75-121`.
- `foundation/integration/terminal_decision.go:5-27,110-135`.

