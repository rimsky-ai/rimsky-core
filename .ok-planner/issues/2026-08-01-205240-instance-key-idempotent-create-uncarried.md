---
issue: instance-key-idempotent-create-uncarried
kind: human
category: unspecified
artifacts:
  - concept:instance
status: open
opened: 2026-08-01T20:52:40Z
---

# Should concept:instance carry the per-template instance-key uniqueness and idempotent re-create commitment?

## Problem

The instance intent dossier records a long-standing, never-superseded commitment: instance-key uniqueness is per-template (`UNIQUE(template_hash, instance_key)`, NULL keys distinct), and re-creating with an existing key is idempotent — the create returns the existing row and ignores the request's flags. The code still honors both halves (`table:rimsky_instances` carries the unique constraint in `file:lib/foundation/persistence/postgres/migrations/001-initial.sql`; `code:lib/control/controlapi/instances.go` looks up by `(template_hash, instance_key)` and returns the existing row). The live `concept:instance` says only "`instance_key` is nullable; canonical identity is the UUID" — neither the per-template uniqueness scope nor the idempotent re-create semantics appear anywhere in the corpus, and downstream machinery (the compose driver's deterministic `compose:` keys) depends on the idempotent behavior. `concept:instance` is sprint-final-form, so the reconciliation pass may not fold the amendment itself.

## Candidates

- Amend `concept:instance` (in a future sprint delta) with an invariant carrying both halves: instance-key uniqueness is scoped per template hash, and create-with-existing-key is idempotent, returning the existing row and ignoring the request's flags.
- Leave the concept as-is, ruling the uniqueness scope and idempotent re-create an implementation detail below concept altitude (in which case the behavior is protected only by tests, not by the corpus).
