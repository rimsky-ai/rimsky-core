---
tension: terminal-event-overloaded
category: overloaded
status: open
affects:
  - executor
  - parked-state
---

# "Terminal event" covers five wire variants with three different parallel semantics

## What is muddy

The executor protocol declares "terminal events" as `Complete | Blocked | Errored | AsyncAccepted | ParkRequested`. But "terminal" doesn't mean the same thing for each:

- **`Complete` / `Errored` / `Blocked`** — always terminal at both the stream level and the logical level.
- **`AsyncAccepted`** — stream-terminal (closes the gRPC stream) but logical-non-terminal (the final outcome arrives later via async-callback).
- **`ParkRequested`** — terminal-but-resumable (transitions to a hold state, future re-dispatch).

A reader counting "terminal events" gets different numbers depending on what counts as terminal.

## Why it matters

A new contributor adding a sixth variant has to decide which parallel semantics it falls under, and the existing language doesn't tabulate the matrix. Bug-class: misclassifying `AsyncAccepted` as logical-terminal leaks the callback registry.

## Resolution candidates (do NOT pick)

- Rename the wire-level term ("stream-closing events"?) and reserve "terminal" for logical-terminal.
- Tabulate the three parallel semantics in `docs/concepts/executor.md`.
- Split `AsyncAccepted` and `ParkRequested` into a separate "yield" variant set.

## Evidence

- `_discover/2026-05-10-executor-streamed-execute.md` Observations bullet 1.
- `protocols/proto/v1/executor.proto:131-208`.

