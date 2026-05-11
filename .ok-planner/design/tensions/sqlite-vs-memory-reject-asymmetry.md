---
tension: sqlite-vs-memory-reject-asymmetry
category: inconsistent
status: open
affects:
  - persistence-driver
  - blob-backend
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

- Add the symmetric reject gate: if `driver: sqlite` and `RIMSKY_PROCESS_ROLE != unified`, reject at startup.
- Make sqlite + replicas > 1 a deployment-time check (`deploy/rimsky-all.yml`).
- Default the unified image to postgres if `REPLICAS > 1`.

## Evidence

- `_discover/2026-05-10-sqlite-dev-only.md` Observations bullet 1.
- `_discover/2026-05-10-blob-spill-pluggable-backends.md` Observations bullet 1.
- CLAUDE.md "Non-obvious gotchas" — both topics.

