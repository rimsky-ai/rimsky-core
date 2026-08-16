---
audit: progress-default
artifact: decision:progress-default
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The default progress printer's three line kinds, its stream, and its buffering

Supported. With no progress flag set, both one-shot paths construct the default printer over standard error, and that printer emits exactly three line kinds — one when an instance starts being tracked, one per node-run terminal carrying the outcome and its optional reason, and one per instance terminal carrying the outcome and node count — while its frame-tick method is a deliberate no-op, so deeper-frequency events stay out of the default stream. Every line goes through one mutex-guarded buffered writer that flushes on each line, which gives both the chronological ordering and the line buffering the choice names. Neither rejected shape exists: the default is not silent, and it does not emit the finer-grained events. Tests cover the per-line flush and the emitted prose.
