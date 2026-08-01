---
decision: scratch-protocol
status: as-is
---

# Scratch rides settling terminal outcomes only

## Choice

The executor protocol carries scratch on the execute request (the row's current scratch hydrates every dispatch) and on the three settling terminal outcomes — success, error, park — in both the synchronous response and the async-callback body. The transient await-async-callback hand-off carries none: scratch lands on whichever settling outcome the eventual callback posts. A settling terminal outcome is the only channel by which an executor hands scratch back to rimsky. Park is the carry-channel across a park-and-resume cycle: the parked row persists the Park outcome's scratch, and the time-wake re-dispatch of the same row hydrates it back (per `concept:parked-state`).

An empty (zero-length) terminal scratch is a no-op against the dispatch row's persisted scratch, not a clear-to-NULL command: the row's prior scratch survives an empty terminal-attach, and only a non-empty terminal scratch changes what the next dispatch sees.

## Rationale

Symmetry across all settling outcomes gives executors one uniform "save state for next dispatch" mechanism regardless of how they exit — trivial for both sync and async executors, since every executor eventually settles via one of the three terminal variants. The inertness rule (see `concept:inertness`) extends to scratch — rimsky never inspects it.

Empty-terminal-attach-as-no-op keeps the overwhelmingly common "executor doesn't use scratch" case free of an extra write per terminal, removes a race surface where the terminal transaction would surface a missing-row error against a row a concurrent path retired, and preserves whatever scratch state the row already carried.

## Alternatives

- A dedicated mid-dispatch checkpoint callback — rejected: no real consumer; a genuine long-running-checkpoint need warrants its own design with an actual consumer, not a speculative channel kept live.
- Empty terminal scratch clears the persisted slot — rejected: costs an extra write on the common no-scratch case, opens a missing-row race against concurrently retired rows, and destroys carried state.
