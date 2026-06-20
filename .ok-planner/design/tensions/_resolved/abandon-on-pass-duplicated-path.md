---
tension: abandon-on-pass-duplicated-path
category: muddy-boundary
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - terminal-resolution
  - auto-terminal
  - lifecycle-handler
resolution:
  shape: extract-shared-helper
  helper: foundation/integration/abandon_claim.go::abandonOpenedClaim
  doc-sweep:
    - concepts/auto-terminal.md (unified-engine-entry invariant reworded)
    - concepts/terminal-resolution.md (OnAcquireUnavailable paragraph + kind→verb table reworded)
  summary: |
    Extracted a narrow shared helper centralizing producer.Abandon on
    already-Open'd claims. Both the pre-dispatch carve-out
    (handleAcquireUnavailable.abandonPartialLocks) and the post-dispatch
    unified-engine Abandon branch (ResolveClaimHandleTerminal) now call
    the same helper. The doc-language inconsistency between
    auto-terminal.md and terminal-resolution.md is reconciled in the
    same change.
---

# `Producer.Abandon` on already-Open'd claims fires from two sites that do not route through `ResolveClaimHandleTerminal`

## What is muddy

Before this resolution: the pre-dispatch path (`handleAcquireUnavailable.abandonPartialLocks` in `runner_lifecycle.go`) called `producer.Abandon` directly, while the post-dispatch path (`ResolveClaimHandleTerminal` in `terminal_decision.go`) was framed as "the single audited site for producer-verb fire + claim-handle delete." That framing was almost-true — `ResolveClaimHandleTerminal` was indeed the single site that fired the post-dispatch verb and the claimant-guarded `rimsky_claim_handle` delete — but it gave the misleading impression that *every* `producer.Abandon` on an already-Open'd claim ran through that engine. The pre-dispatch carve-out did not. If a future change added telemetry, a metric, or an audit-event emit at the unified-engine site, the pre-dispatch path would silently miss it.

This tension is resolved by `2026-05-11-design-log-convergence`'s extraction of the shared `abandonOpenedClaim` helper in `foundation/integration/abandon_claim.go`. Both sites now call the same helper for the producer-Abandon step. The post-dispatch site continues to own the claimant-guarded delete in its caller-provided tx; the pre-dispatch site has no row to delete (the acquisition tx rolled back).

## Why it matters

The unified-engine narrative is load-bearing for the held-claim auto-terminal aggregate-outcome rule and for the spec's §7.3 statement that the producer verb + claim-handle delete is a single audited sequence. A reader walking the spine for the first time sees `ResolveClaimHandleTerminal` as the bottom of the pipe and reasonably assumes "every producer.Abandon happens here" — only to discover that the pre-dispatch unavailability path and the `applyTerminalPass` early-out do their own direct calls.

If a future change adds telemetry, a metric, or an audit-event emit at `ResolveClaimHandleTerminal`, the pre-dispatch and pass paths silently miss it.

## Resolution candidates (do NOT pick)

- Refactor `handleAcquireUnavailable` to construct a synthetic `acq.Locks` slice and route through `releaseLocksInTx` → `ResolveClaimHandleTerminal`.
- Extract a smaller "abandon-already-opened-claim" helper that both sites call, with the same audit emit + delete sequence.
- Re-scope the unified-engine narrative to "all post-dispatch terminal paths" and document the pre-dispatch carve-out explicitly at `terminal_decision.go:5-27`.

**Picked shape (refine-design step 5):** Extract a small "abandon-already-opened-claim" helper that both `handleAcquireUnavailable.abandonPartialLocks` and `ResolveClaimHandleTerminal`'s Abandon branch call. Same audit emit + delete sequence in one place; both sites become thin call-sites. The helper does not force the pre-dispatch path through the post-dispatch spine (no synthetic `acq.Locks` glue). Doc-language sweep (per the Additional context section): once the helper exists, reword the unified-engine-entry rule in `concepts/auto-terminal.md` to read "Unified `ResolveClaimHandleTerminal` (post-dispatch) and the shared `abandon-already-opened-claim` helper are the two audited sites for `Producer.Abandon` on already-Open'd claims; the pre-dispatch `OnAcquireUnavailable` carve-out routes through the helper but not through `ResolveClaimHandleTerminal` itself" (or equivalent). Reword `concepts/terminal-resolution.md` opening prose to match.

## Additional context (added during refine-design intake)

The body of "What is muddy" above over-states which sites bypass `ResolveClaimHandleTerminal`. The genuinely duplicated path is **only** the pre-dispatch `handleAcquireUnavailable.abandonPartialLocks` (which calls `lk.Store.Abandon` directly at `runner_lifecycle.go:75`). The post-dispatch `applyTerminalPass` **does** route through `releaseLocksInTx(success=false)` → `ResolveClaimHandleTerminal` for both held and non-held branches (`runner_terminal_release.go:137`). Reword the tension body during resolution to match.

There is a corresponding documentation-language inconsistency between two concepts:
- `concepts/auto-terminal.md`'s unified-engine-entry rule currently reads "Unified `ResolveClaimHandleTerminal` is also the entry point for orphan-reaper bail paths and error-policy `pass`/`error` resolutions on already-Open'd claims." This is correct for the post-dispatch `OnExecutorBlocked`/`OnExecutorErrored` `pass` path but silently wrong for the pre-dispatch `OnAcquireUnavailable` `pass`/`error` path.
- `concepts/terminal-resolution.md` opening prose notes the `OnAcquireUnavailable` carve-out correctly but does not state the post-dispatch `pass` site routes through the unified engine.

Once the structural resolution shape is picked (re-route the pre-dispatch path through the unified engine, vs. extract a shared helper, vs. rescope the engine's narrative to "post-dispatch only"), the doc-language sweep across both concepts follows from that decision. Both concept files must be updated as part of the same resolution.

## Evidence

- `_discover/terminal-resolution.md` Observations bullet "duplicated Abandon-on-pass-or-error logic".
- `foundation/integration/runner_lifecycle.go:32-150` (the actual pre-dispatch direct-Abandon site).
- `foundation/integration/runner_terminal_handlers.go:75-121` (post-dispatch; routes through `releaseLocksInTx`).
- `foundation/integration/runner_terminal_release.go:137`.
- `foundation/integration/terminal_decision.go:5-27,110-135`.
- `concepts/auto-terminal.md` Invariants block (unified-engine-entry rule).
- `concepts/terminal-resolution.md` opening prose + `OnAcquireUnavailable` paragraph.
- `review-notes.md` "Back-edge judgment calls" / "Tension `abandon-on-pass-duplicated-path` framing is partially imprecise" + "Inconsistency between `terminal-resolution` and `auto-terminal` on unified-engine entry".

