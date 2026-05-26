---
tension: serial-queue-per-instance
category: unclear
status: open
affects:
  - frame
  - instance
---

# `serial_queue` ordering is per-instance; readers expecting template-wide ordering will be surprised

## What is muddy

`frame_resolution: serial_queue` is described as "preserves ordering: each invalidate produces its own frame; frames run one at a time per instance." The "per instance" qualifier is load-bearing but easy to miss — a template author may reasonably expect template-wide ordering (all invalidates against any instance of this template run in arrival order).

## Why it matters

A consumer relying on serial ordering across instances will see independent ordering per instance and not realize. Two instances of the same template execute concurrently; their frames don't serialize against each other.

## Resolution candidates (do NOT pick)

- State prominently in the frame concept's definition that serial-queue ordering is per-instance, so template authors do not assume template-wide serialization (see `concept:frame`, `concept:instance`).
- Add a template-level "serialize across instances" resolution mode, which would require a frame queue scoped to the whole deployment rather than to a single instance.
- Rename the serial-queue mode to make its per-instance scope explicit in the mode name itself.

## Evidence

- `_discover/2026-05-10-frame-resolution-model.md` Observations bullet 2.
- `docs/concepts/frame.md` "Common mistakes".

