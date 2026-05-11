---
tension: userdata-schema-as-opacity-exception
category: unspecified
status: open
affects:
  - opacity
  - userdata
  - observability
  - executor
---

# `userdata_schema` validation is a sanctioned opacity exception but not explicitly named in CLAUDE.md

## What is muddy

`@blessed-invariant 11` says userdata is opaque: no inspection, no validation beyond the executor-side schema check. But:

- The executor reports `userdata_schema` via `ExecutorObservability.Capabilities`.
- Rimsky validates userdata at template registration AND at dispatch post-merge/post-substitution against that schema.

This means rimsky DOES read userdata-adjacent metadata (the schema), and DOES structurally check the userdata against the schema. The reasoning (per `_discover/2026-05-10-observability-optional-protocols.md` Description): "schema validation is a structural check (does the JSON match the schema's keyword constraints) and does not 'inspect' the bytes the way logging or substitution would."

This is consistent with opacity but is a non-obvious distinction. CLAUDE.md "Non-obvious gotchas" doesn't surface it; the carve-out is implicit.

## Why it matters

A future contributor adding "validate userdata against `expected_size` field" might think it's allowed because schema validation already happens. The boundary is "structural validation only, not content inspection" — but that's nowhere written explicitly.

## Resolution candidates (do NOT pick)

- Add a sub-invariant under `@blessed-invariant 11` explicitly carving out structural schema validation.
- Move the validator to the executor side (rimsky forwards bytes; executor validates against its own schema).
- Document the carve-out in `docs/concepts/userdata.md`.

## Evidence

- `_discover/2026-05-10-observability-optional-protocols.md` Description "userdata_schema placement" para.
- `_discover/conformance-probe-stub-mode-handshake.md` Description final para.

