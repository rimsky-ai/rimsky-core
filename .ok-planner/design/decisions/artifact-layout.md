---
decision: artifact-layout
---

# artifact-layout

## Choice

A per-run directory under a stable per-root parent, named by timestamp plus run name, holding the run's state database. A pointer entry at the parent level resolves to the most-recent run directory. The state database stays openable with widely available tooling for its format — no rimsky-specific reader is required to inspect an artifact.

## Rationale

A single folder per run is the natural archive-and-ship unit, and under the embedded-file backend the one database file is the whole record of the run. The pointer entry covers the common "open the last one" case without timestamp-parsing. Third-party readability is the artifact's operational value — the operator who inherits a run must be able to open it with standard tooling — so openness is a binding constraint on future storage changes, not a byproduct of today's driver choices.

## Alternatives

- One shared state database across all runs — rejected: loses the copy-one-folder archive-and-ship unit and couples every run's lifecycle to one file.
- Flat per-run files keyed by run id in a single directory — rejected: a run's state database and its configuration file no longer travel together.
- A compressed or encrypted rimsky-specific artifact encoding opened through a bundled reader — rejected: trades third-party post-mortem inspection away for storage convenience.
