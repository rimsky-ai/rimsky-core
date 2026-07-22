---
tension: frame-stuck-dangling-adjacent
category: vestigial
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - error-policy
  - frame
resolution:
  shape: reword-adjacent
  summary: |
    Dropped frame-stuck from error-policy.md Adjacent (the slug pointed
    at no concept file). The mechanism it referred to is the
    frame.stuck.observed slog warning, which lives in concepts/frame.md
    as part of the advisory frame_timeout mechanism. Prose updated to
    point at frame directly.
---

# `Adjacent: frame-stuck` in `error-policy` points at a non-existent concept

## What is muddy

`concepts/error-policy.md` names `Adjacent: frame-stuck`, but there is no `concepts/frame-stuck.md`. The notion the slug points at is the advisory `frame.stuck.observed` slog warning emitted when `last_progress_at` falls outside `frame_timeout_ms` — a mechanism that already lives in `concepts/frame.md` (frame_timeout, no-progress observation, purely advisory: no destructive action). It is not a separate noun.

## Why it matters

Cross-link integrity bug at final-approval. A reader following `Adjacent: frame-stuck` finds nothing and may believe a concept is missing rather than that the prose is over-pointing.

## Resolution candidates (do NOT pick)

- **Reword** the `Adjacent:` line in `concepts/error-policy.md` to point at `frame` (where the stuck-observation mechanism lives), and adjust the surrounding sentence if it implies `frame-stuck` is its own noun.

## Evidence

- `concepts/error-policy.md` Adjacent block.
- `concepts/frame.md` frame_timeout / `frame.stuck.observed` boundary.
- `review-notes.md` "Suspected-but-unconfirmed concepts" / "Unresolved issues".

