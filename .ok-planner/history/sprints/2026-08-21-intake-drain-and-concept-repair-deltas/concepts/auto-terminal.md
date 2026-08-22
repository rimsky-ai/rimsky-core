---
concept: auto-terminal
aliases:
  - held-claim resolution
---

# Auto-terminal

## What it is

Auto-terminal is the mechanism that fires a producer's terminal disposition — commit or abandon — exactly once at the end of a held claim's holding subgraph, from the aggregate of its holders' outcomes. It delegates to the unified claim-handle terminal-resolution engine, and it settles the handle, the producer's terminal verb, and the holders' transitions at one atomic point. The verb is a notification of a disposition already recorded: a delivery failure retries and never rewrites the disposition.

Auto-terminal is also the deferred cascade-fire moment for downstream subscribers that are not members of the holding subgraph. A held node-run fires the cascade walker as soon as it becomes held, filtered to the holding subgraph's own members, which is how members coordinate with each other while the claim is held. Auto-terminal is the moment the non-member cascade fires, at the same atomic point the handle is promoted. On commit the held holders become fresh and the walker carries the success signal to non-members; on abandon the held holders fail and the walker carries the abandoned error-class signal (see `decision:held-claim-poison-propagation`, `decision:terminal-error-abandoned-as-error-class`).

For a handle with expected fan-out children, the aggregate outcome comes from the child's chosen aggregation policy rather than a plain any-failed or all-completed rule, so a set of children that all completed can still resolve to abandon (see `concept:fan-out`).

Both holder transitions apply to leaf holders alone. Auto-terminal never stamps a held holder run that is an aggregating parent in the run tree: the handle promotion and the verb enqueue proceed as usual, but that parent's settled state and settling signal come only from the run-tree aggregation over its children, which fires the parent's own settlement cascade. The aggregation verdict is the single authoritative verdict for such a parent, and the claim-side abandoned signal never appears on it.

A verify-before-run check that loses a race bails, and that bail routes through the same engine under its own source kind. There the engine deletes the row rather than promoting it, because a bailed acquisition is unwound rather than resolved. One path stays outside the engine — the pre-dispatch path where the claim was never available, whose rows the acquisition transaction's rollback already removed, leaving only the shared abandon to fire.

## Purpose

A held claim outlives the node-run that acquired it, so something must decide when to release it. Auto-terminal puts that decision in one place and drives it from a deterministic predicate over the states of the handle's holder rows, instead of leaving each holder to guess whether it is the last one out.

## Boundaries

Auto-terminal owns the aggregate-outcome computation, the enqueue of the producer's terminal verb, and the promotion of the handle row to committed or abandoned — with the ownership-bail and never-acquired paths deleting the row instead of promoting it. It does not own how a holder reaches its own terminal, which belongs to `concept:error-policy` and to the handling of a clean executor completion. It does not own what the verb does on the producer's side, which belongs to `concept:claim-producer`. It does not own the active, non-held branch of terminal resolution, which routes through the same engine and belongs to `concept:terminal-resolution`. Auto-terminal keeps firing across a park, because a park ends a dispatch and not a run.

see also: `claim-handle`, `claim-producer`, `parked-state`, `terminal-resolution`, `error-policy`, `fan-out`

## Aliases

- held-claim resolution
