---
story: mandatory-instantiation-gate
status: as-is
---

# Instance create validates value constraints

## Role

As an operator creating an instance from a deployed template, I can trust that rimsky validates statically-knowable attribute config against every referenced service's schema — including value constraints, not just shape — and refuses the create with a clear error if anything is statically misconfigured, so that bad config is caught at create time rather than as a mid-run dispatch failure.

## Capability

Mandatory instantiation gate: rimsky validates statically-knowable attribute config (including value constraints, not just shape) against every referenced service's schema before persisting the instance.

## Business value

Bad config is caught at create time with a clear naming of the offending attribute, not mid-run as a dispatch failure that wastes runtime resources and produces a confusing error trail.

## Acceptance

With a template referencing an executor whose schema declares value constraints (e.g., a `minimum: 0` on a numeric attribute), creating an instance whose attributes violate the constraint (e.g., a negative value) is refused with a clear validation error naming the offending attribute and the violated constraint; the instance is not persisted and nothing runs. A well-formed instance of the same template succeeds.

## Falsifier

Value-constraint violation slips through, OR the rejection cites only a shape error rather than the value-constraint violation.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
