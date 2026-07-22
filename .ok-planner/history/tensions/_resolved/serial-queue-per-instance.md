---
tension: serial-queue-per-instance
category: unclear
status: resolved
spec: 2026-06-14-message-schema-layer-design
affects:
  - frame
  - instance
resolution:
  summary: |
    The rename-the-mode resolution candidate is moot: there is no longer a
    mode name to rename, since the alternative frame-resolution mode
    (coalesce) retires. The substantive concern — per-instance ordering
    versus template-wide expectations — survives as a documented property
    of `concept:frame`: ordering is per-instance, and consumers needing
    template-wide ordering must coordinate above rimsky.
---

# `serial_queue` ordering is per-instance; readers expecting template-wide ordering will be surprised

## What is muddy

`frame_resolution: serial_queue` is described as "preserves ordering: each invalidate produces its own frame; frames run one at a time per instance." The "per instance" qualifier is load-bearing but easy to miss — a template author may reasonably expect template-wide ordering (all invalidates against any instance of this template run in arrival order).

## Why it matters

A consumer relying on serial ordering across instances will see independent ordering per instance and not realize. Two instances of the same template execute concurrently; their frames don't serialize against each other.

## Resolution

The rename-the-mode resolution candidate is moot: there is no longer a mode name to rename, since the alternative frame-resolution mode (`coalesce`) retires. The substantive concern — per-instance ordering versus template-wide expectations — survives as a documented property of `concept:frame`: ordering is per-instance, and consumers needing template-wide ordering must coordinate above rimsky.

## Evidence

- `_discover/2026-05-10-frame-resolution-model.md` Observations bullet 2.
- `docs/concepts/frame.md` "Common mistakes".
