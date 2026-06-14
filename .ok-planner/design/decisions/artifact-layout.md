---
decision: artifact-layout
status: adopted
---

# artifact-layout

## Choice

Per-run directory at `<root>/.rimsky/runs/<timestamp>-<name>/` containing `state.db` and `blobs/`. `<root>/.rimsky/latest` is a symlink to the most-recent run directory.

## Rationale

A single folder per run is the natural archive-and-ship unit. The `latest` symlink covers the common "open the last one" case without timestamp-parsing.
