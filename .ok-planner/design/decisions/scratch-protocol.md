---
decision: scratch-protocol
status: as-is
aliases: []
---

# Scratch on the executor wire and as a mid-dispatch callback

## Choice

Add a bytes scratch field to the executor's execute request (carries the row's current scratch on dispatch) and to each of the three settling terminal outcome variants — success, error, park — that close out a dispatch with a terminal verdict. The await-async-callback outcome is excluded: it is a transient that hands the dispatch off to an out-of-band callback rather than settling, so scratch lands on whichever outcome the eventual async-callback posts (success, error, or park). The async-callback body's outcome oneof variants (success, error, park) gain the same field per the existing pattern. For mid-dispatch checkpointing: add a new executor-protocol scratch HTTP callback, paralleling the attributes incremental-writeback callback path; the body is the opaque scratch bytes. Out-of-process executors post directly; the claude-agent executor wraps the callback as an in-server tool surface on its internal MCP server, mirroring how it wraps the attributes callback as a similar tool surface; in-process executor handlers call the runtime-side writeback helper directly without going over the wire.

An empty terminal scratch on a settling outcome (zero-length bytes) is a **no-op against the dispatch row's persisted scratch**, not a clear-to-NULL command. The row's prior scratch state — set by a mid-dispatch callback, a recovery-time copy from the predecessor row, or simply never written — survives an empty terminal-attach. The executor's mechanism for actively clearing scratch is to post an empty body to the mid-dispatch scratch callback (or, for in-process handlers, call the runtime-helper write with a zero-length slice mid-dispatch); the terminal outcome's scratch field is a "save state for next dispatch" lane, not a "reset" lane.

## Rationale

Symmetric across all settling outcomes means executors get a uniform "save state for next dispatch" mechanism regardless of how they exit. The mid-dispatch callback (with claude-agent's in-server tool-surface wrapper for its CLI surface, and the runtime-helper direct call for in-process handlers) covers long-running executors that want to checkpoint without terminating; trivial for sync executors that can attach at the terminal outcome. The inertness invariant (`@blessed-invariant 21` / `concept:inertness`) extends to scratch — rimsky never inspects.

The empty-terminal-attach-is-a-no-op choice keeps the overwhelmingly common "executor doesn't use scratch" case free of an extra write per terminal, removes a race surface where the terminal transaction would surface a missing-row error against a row a concurrent path retired, and preserves any mid-dispatch checkpoint the executor wrote earlier in the same dispatch. The "clear" lane is the mid-dispatch callback with an empty body — symmetric with the "set" lane.
