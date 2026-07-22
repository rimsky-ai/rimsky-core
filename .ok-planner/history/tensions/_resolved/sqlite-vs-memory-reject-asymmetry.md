---
tension: sqlite-vs-memory-reject-asymmetry
category: inconsistent
status: resolved
affects:
  - persistence-database
  - blob-backend
  - advisory-lock
resolution:
  summary: |
    No startup gate for SQLite. Instead of gating, the SQLite driver is
    made safe for multiple processes sharing one local database file:
    its read-then-write operations are transactional (immediate-mode
    transactions hold the writer slot, giving cross-process atomicity),
    and the scheduler-tick and migration locks are file-lock-based, so
    their exclusion holds across processes sharing the file. The
    platform defaults to Postgres outside the all-in-one deployment,
    and an operator overriding to SQLite is presumed to have chosen
    deliberately — gating a deliberate config choice is not this
    platform's policy. The memory blob backend remains gated to the
    single-process mode because cross-process in-memory state is broken
    by physics, not policy. The asymmetry is thereby justified and
    recorded rather than accidental. Separate database files per
    process and network filesystems remain physically unsupportable
    and undetectable in-process.
---

# `memory` blob backend is startup-rejected outside unified mode; SQLite + replicas > 1 is NOT — same broken-by-construction semantics

## What is muddy

Two cross-process configurations break by construction in the same way (per-process binaries can't share in-process state):

- **`memory` blob backend** — actively gate-rejected at startup in `foundation/persistence/blob_config.go:115` unless `RIMSKY_PROCESS_ROLE=unified`. Operator gets a fail-fast error.
- **SQLite + replicas > 1** — NOT gate-rejected. The unified image defaults to `driver: sqlite`; if replicas > 1 each process gets an independent SQLite file. Silently broken state split. Documented in CLAUDE.md "Non-obvious gotchas" but no code gate.

`_discover/2026-05-10-sqlite-dev-only.md` Observations bullet 1 explicitly calls this out: "no startup gate rejects this configuration today, parallel to the (enforced) memory-blob-backend rejection."

## Why it matters

An operator deploying the unified image with replicas > 1 silently splits state across replicas — broken; nothing fails fast. Same operator-facing risk class as memory blob, but with no safety net.

## Resolution candidates (do NOT pick)

- Add a symmetric startup gate that rejects the SQLite driver outside the unified single-process role.
- Add a deployment-time check that rejects SQLite under multiple replicas.
- Default the unified image to Postgres when scaled beyond one replica.

## Evidence

- `_discover/2026-05-10-sqlite-dev-only.md` Observations bullet 1.
- `_discover/2026-05-10-blob-spill-pluggable-backends.md` Observations bullet 1.
- CLAUDE.md "Non-obvious gotchas" — both topics.

