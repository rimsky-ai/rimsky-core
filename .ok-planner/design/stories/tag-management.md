---
story: tag-management
status: as-is
---

# Operator manages movable template-hash names

## Role

As an operator, I can create a movable name for a template hash, list and resolve current tag bindings, re-point a tag to a different template hash, and remove a tag I no longer need, so that I version a deployable name and roll forward or back without disrupting in-flight instances.

## Capability

Operator-driven tag lifecycle: bind, list, resolve, re-bind, delete a tag pointing at a template hash through the control-api or CLI.

## Business value

Operators version a deployable name and roll forward or back without disrupting in-flight instances; rebinds atomically redirect new instance creation without retroactively affecting existing instances.

## Acceptance

Through the control-api or `rimsky tag …` CLI, an operator binds a tag to a template hash; afterward, instance creation against the tag uses that hash. Re-binding the tag to a different hash atomically redirects subsequent instance creation to the new hash without affecting instances already created under the old binding. Deleting a tag makes the name no longer resolvable for new instances.

## Falsifier

Tag rebind isn't picked up by subsequent instance creation (resolves to the prior hash), OR tag deletion leaves the name still resolving.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
