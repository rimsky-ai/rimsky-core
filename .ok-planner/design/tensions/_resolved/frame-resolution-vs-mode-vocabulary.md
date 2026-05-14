---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: frame-resolution-vs-mode-vocabulary
category: inconsistent
status: open
affects:
  - frame
---

# Template authors write `frame_resolution:`; the runtime column is `mode` — two names for one value across the same data flow

## What is muddy

The template-author surface declares the field as `frame_resolution:` (`modeling/node/template.go:35`, `TemplateSpec.FrameResolution`). The persisted column is `rimsky_frames.mode` (`foundation/persistence/postgres/migrations/002-frame-resolution.sql:13-25`). The Go type used internally is `FrameMode` (`foundation/persistence/frames.go:31-36`). The concept doc at `docs/concepts/frame.md` uses "mode" while the template doc uses "frame_resolution". The lookup helper bridges the two names: `LookupFrameMode` reads `t.spec->>'frame_resolution'` and returns it as `FrameMode`.

The two names refer to identical values (`coalesce | serial_queue`) flowing through the same JCS-canonicalized JSONB without transformation. A reader greping for `frame_resolution` in the persistence layer finds nothing; a reader greping for `mode` in the template doc finds nothing.

## Why it matters

This is a cold-read seam at one of the most-touched template fields. The split is mild but persistent: every conversation about frame policy has to decide which name to use, and operators reading the schema see "mode" while operators reading their YAML see "frame_resolution". Documentation has to translate at the boundary — the kind of friction that's individually small but accumulates across the codebase.

Adjacent to other naming-split tensions: `consumer-key-vs-instance-key`, `store-vs-claim-producer-vocabulary`, `yaml-stores-alias`, `yaml-write-semantics-alias`. The pattern is recurring; this one's resolution is consistent with whatever direction those resolve.

## Resolution candidates (do NOT pick)

- Rename the YAML field to `frame_mode:` (alias old name for back-compat under the pre-v1 "break freely" rule).
- Rename the schema column to `frame_resolution` (migration; pre-v1 allows it).
- Document the synonym in `docs/concepts/frame.md` "Aliases and historical names".

## Evidence

- `_discover/2026-05-10-frame-resolution-model.md` Observations bullet "template-time vs runtime vocabulary".
- `modeling/node/template.go:35` vs `foundation/persistence/postgres/migrations/002-frame-resolution.sql:13-25`.

