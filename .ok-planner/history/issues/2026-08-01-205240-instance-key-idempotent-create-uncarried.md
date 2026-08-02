---
issue: instance-key-idempotent-create-uncarried
kind: human
category: unspecified
artifacts:
  - concept:instance
status: promoted
sprint: 2026-08-01-intent-ingestion.md
opened: 2026-08-01T20:52:40Z
---

# Should the instance concept commit to per-template key uniqueness and idempotent re-create?

An instance (a running deployment of a node-graph template) can carry an optional caller-chosen `instance_key`. Two behaviors hang off that key today, both enforced by code but stated nowhere in the design corpus: the key is unique *per template* — the same string is legal under two different templates (`UNIQUE (template_hash, instance_key)` in the initial migration) — and creating an instance whose key already exists is idempotent: the control API returns the existing row and ignores the rest of the request (`code:lib/control/controlapi/instances.go`).

This isn't incidental plumbing. The compose driver (the `rimsky compose` deployment path) derives deterministic `compose:` keys precisely so that re-running a manifest is safe — it leans on idempotent re-create to make "apply this manifest again" a no-op rather than a duplicate-instance factory. The live `concept:instance` says only that the key is nullable and the UUID is canonical identity; if a future change scoped uniqueness globally or made repeat-create an error, no artifact would object, but every re-run of a compose manifest would break.

The behavior is settled and depended-upon; the question is whether it becomes a design commitment or stays protected only by the migration's constraint and tests.

## Options

- Amend the instance concept with an invariant carrying both halves: uniqueness scoped per template hash, and create-with-existing-key returning the existing row, request flags ignored. Cost: an intent-level concept amendment — sprint work.
- Rule it implementation detail below concept altitude. Cost: a load-bearing contract of the compose re-run story has no design-level protection.

The ruling decides whether these two key behaviors become invariants of the instance concept.

## Ruling

> Recommended ruling (/verify-issues): Commit both halves as an
> invariant on the instance concept — key uniqueness is per
> template, and re-creating with an existing key idempotently
> returns the existing instance.
>
> Rationale: the corpus's own bar for an invariant is a property
> other machinery is entitled to lean on, and the compose driver
> already leans on this one — leaving it below corpus altitude (the
> second option) protects a depended-upon contract with nothing but
> a schema constraint nobody promised to keep. The concept already
> speaks to the key's nullability, so the amendment completes a
> commitment it half-states. Flip case: if compose reconciliation
> ever moves off instance keys onto UUID bookkeeping of its own,
> the external dependent disappears and the detail could
> legitimately stay code-level.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
