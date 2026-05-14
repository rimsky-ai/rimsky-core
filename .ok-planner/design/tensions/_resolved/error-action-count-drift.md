---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: error-action-count-drift
category: inconsistent
status: open
affects:
  - error-policy
  - lifecycle-handler
---

# Error-action count: 3 in CLAUDE.md "Vocabulary"; 5+1 in `docs/concepts/error-policy.md`

## What is muddy

The action vocabulary for `error_types:` entries is cited differently:

- CLAUDE.md "Vocabulary": "3 error actions (`retry`, `invalidate(targets)`, `give_up`)".
- `docs/concepts/error-policy.md`: 5+1 actions: `retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, `give_up`, `pass`.

The vocabulary list in CLAUDE.md is older; the concept doc is the current authority. `discard_then_retry` and `resume_then_retry` matter for held claims (the former releases, the latter preserves). `pass` is used in lifecycle-handler context.

## Why it matters

Template authors using the older list miss `discard_then_retry` / `resume_then_retry` and end up using `retry` everywhere, with held-claim semantics that may not match intent.

## Resolution candidates (do NOT pick)

- Update CLAUDE.md "Vocabulary" to match `docs/concepts/error-policy.md`.
- Single canonical action list referenced from both surfaces.
- Distinguish "error-types actions" vs "lifecycle-handler resolves" explicitly (CLAUDE.md collapses these; the concept doc splits them).

## Evidence

- `_discover/error-policy-retry-loop-cap.md` Observations bullet 1.
- CLAUDE.md "Vocabulary".
- `docs/concepts/error-policy.md`.

