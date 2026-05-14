---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: yaml-write-semantics-alias
category: inconsistent
status: open
affects:
  - rimsky-yml
  - write-semantics
---

# YAML `write_semantics:` (single value) is a legacy alias for `write_semantics_envelope:` (list)

## What is muddy

Per-producer write-semantics config has two YAML shapes:

- `write_semantics_envelope: [SYNC, STAGED_ASYNC]` — current required name, a SET.
- `write_semantics: SYNC` — legacy single-value form, accepted as a one-element envelope shortcut.

CLAUDE.md "Non-obvious gotchas" describes this. The legacy spelling is a pre-v1 transition affordance.

## Why it matters

Config drift; a sample showing `write_semantics:` may mislead a reader to think single-value is the canonical form. Two configs may express the same thing differently.

## Resolution candidates (do NOT pick)

- Remove the legacy alias.
- Lint mixed usage.
- Keep both with a deprecation warning.

## Evidence

- `_discover/2026-05-10-write-semantics-envelope-handshake.md` Description.
- `_discover/2026-05-10-unified-rimsky-yml-config.md` Description.
- CLAUDE.md "Non-obvious gotchas".

