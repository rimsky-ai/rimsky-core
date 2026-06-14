---
decision: progress-default
status: adopted
---

# progress-default

## Choice

Per-node lifecycle: one line per instance creation, one per node-run terminal, one per instance terminal. Output goes to stderr, chronologically ordered, line-buffered.

## Rationale

Per-node terminals are the granularity operators read live; deeper-frequency events (frame ticks, claim openings) become noise in the common case.
