---
audit: artifact-layout
artifact: decision:artifact-layout
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# Per-run directory with colocated state db, blob store, and a latest pointer

Supported. `EnsureRunDir` (`cmd/rimsky/cli/compose/artifact.go`) creates one
directory per run under a stable `.rimsky/runs` parent, named
`<timestamp>-<run-name>` with a numeric collision suffix on reuse, and
creates a `blobs/` subdirectory inside it; `WriteSyntheticRimskyYAML` points
the SQLite driver at `state.db` and the filesystem blob backend at `blobs/`
inside that same directory, so the two stores travel together. Both formats
are stock: SQLite opens with any SQLite tool and the blob store is plain
files. `UpdateLatestSymlink` maintains a `.rimsky/latest` symlink resolving
to the most-recent run directory via an atomic swap. Coverage: `EnsureRunDir`
has tests for first-claim, filename-collision retry, and 12-way concurrent
distinct claims; `UpdateLatestSymlink` has tests for initial install,
overwrite, stale-tmp sweeping, and 8-writer/8-reader concurrent stress
asserting no reader ever observes a missing or invalid target — all in
`cmd/rimsky/cli/compose/artifact_test.go`.
