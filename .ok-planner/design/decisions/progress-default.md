---
decision: progress-default
status: adopted
---

# Default progress output is per-node lifecycle

## Choice

Per-node lifecycle: one line per instance creation, one per node-run terminal, one per instance terminal. Output goes to stderr, chronologically ordered, line-buffered.

## Rationale

Per-node terminals are the granularity operators read live; deeper-frequency events (frame ticks, claim openings) become noise in the common case.

## Alternatives

- Emit every lifecycle event by default (frame ticks, claim openings) — rejected: noise drowns the per-node terminals operators actually read.
- Silent by default — rejected: a live run producing no output reads as a hang.
