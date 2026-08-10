---
audit: live-progress
artifact: story:live-progress
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# Per-node lifecycle is visible while the run is still in flight

Supported. In a one-shot run over two instances — one settling at once, one
blocked eight seconds on an upstream fetch — the progress stream, timestamped as
each line reached the terminal, delivered the quick instance's per-node outcome
at +1s and its instance summary at +2s, nine seconds before the command returned
at +11s with the lagging instance's own node line. Both instances, 2 of 2,
produced per-node lines at the moment their work settled rather than batched at
exit, which is what lets a watcher tell outstanding work from a hang.
