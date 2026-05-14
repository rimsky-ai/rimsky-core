---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: lock-holder-vs-claim-handle-legacy
category: vestigial
status: open
affects:
  - claim-handle
  - held-claim
  - worker-request
---

# Phase-5 schema rename (`rimsky_lock_holders` → `rimsky_claim_handle`; `lock_holder_id` → `claim_handle_id`) leaves residue in prose

## What is muddy

The Phase-5 layer-crystallization consolidation renamed:

- `rimsky_dispatch` → `rimsky_worker_request`.
- `rimsky_lock_holders` → `rimsky_claim_handle`.
- `lock_holder_id` FK → `claim_handle_id`.

The schema-level renames are complete. But the legacy names persist in:

- Older sketches under `.ok-planner/history/`.
- Some pre-Phase-5 design docs and the rules file (which still cites `core/queue/...`, `core/supervisor/...`, `core/scheduler/...` paths from a pre-Phase-5 directory layout — `_discover/scenario-test-harness.md` notes this).
- Possibly a stale Go-side comment or test fixture name.

## Why it matters

A new contributor reading older design docs builds a mental model with stale names. Cross-references between current code and historical design require translation.

## Resolution candidates (do NOT pick)

- Sweep `.claude/rules/rules.md` "Verify the build" to use post-Phase-5 paths.
- Leave `.ok-planner/history/` as-is (historical) but add a "see also: current names" prefix.
- Add a one-page rename map in the docs.

## Evidence

- `_discover/2026-05-10-worker-request-phase-lifecycle.md` Description.
- `_discover/scenario-test-harness.md` Description "path names refer to the pre-Phase-5 directories" para.

