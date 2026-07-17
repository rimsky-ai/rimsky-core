---
decision: scratch-protocol
status: as-is
aliases: []
---

# Scratch rides settling terminal outcomes only

## Choice

Add a bytes scratch field to the executor's execute request (carries the row's current scratch on dispatch) and to each of the three settling terminal outcome variants — success, error, park — that close out a dispatch with a terminal verdict. The await-async-callback outcome is excluded: it is a transient that hands the dispatch off to an out-of-band callback rather than settling, so scratch lands on whichever outcome the eventual async-callback posts (success, error, or park). The async-callback body's outcome oneof variants (success, error, park) gain the same field per the existing pattern. Scratch is also the carry-channel across a park-and-resume cycle: the parker writes to the Park outcome's scratch field, which the supervisor persists on the parked row's scratch slot; on time-wake the same row re-dispatches, so the resumed executor reads its scratch on the same dispatch (per `concept:parked-state`). There is no mid-dispatch scratch write channel: a terminal outcome (Success, Error, or Park) is the only way an executor hands scratch back to rimsky. A dedicated mid-dispatch checkpoint callback was considered and retired unused — a genuine long-running-checkpoint need is a fresh spec with a real consumer, not a channel kept live on speculation.

An empty terminal scratch on a settling outcome (zero-length bytes) is a **no-op against the dispatch row's persisted scratch**, not a clear-to-NULL command. The row's prior scratch state — set by an earlier terminal outcome (on this row, or copied forward onto a new row at recalculate enqueue), or simply never written — survives an empty terminal-attach; only a non-empty terminal scratch changes what the next dispatch sees.

## Rationale

Symmetric across all settling outcomes means executors get a uniform "save state for next dispatch" mechanism regardless of how they exit — trivial for both sync and async executors, since every executor eventually settles via one of the three terminal variants. The inertness rule (see `concept:inertness`) extends to scratch — rimsky never inspects it.

The empty-terminal-attach-is-a-no-op choice keeps the overwhelmingly common "executor doesn't use scratch" case free of an extra write per terminal, removes a race surface where the terminal transaction would surface a missing-row error against a row a concurrent path retired, and preserves whatever scratch state the row already carried.
