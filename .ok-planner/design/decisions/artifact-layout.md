---
decision: artifact-layout
status: adopted
---

# artifact-layout

## Choice

A per-run directory under a stable per-root parent, named by timestamp plus run name, holding the run's state database and its blob store side by side. A pointer entry at the parent level resolves to the most-recent run directory.

## Rationale

A single folder per run is the natural archive-and-ship unit. The `latest` symlink covers the common "open the last one" case without timestamp-parsing.
