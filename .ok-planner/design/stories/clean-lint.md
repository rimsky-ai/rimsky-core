---
story: clean-lint
status: as-is
---

# Maintainer verifies the codebase passes Plumbline's full enforcement

## Role

As a rimsky maintainer, I can verify that the codebase passes Plumbline's full enforcement with every check active, so that `decision:coding-style` accurately describes the active configuration.

## Capability

Plumbline's lint, with every check active in the project's Plumbline configuration, reports the post-work tree clean. The configuration shows every check active.

## Business value

The methodology recorded in `decision:coding-style` matches the active enforcement state of the codebase: contributors and agents reading the decision see the same set of checks the lint actually runs.

## Acceptance

The maintainer runs Plumbline's lint against the post-work tree → the lint reports the codebase clean, and the project's Plumbline configuration shows every check active.

## Falsifier

The maintainer runs Plumbline's lint and either sees any violation reported, or finds any check inactive in the project's Plumbline configuration.

## Proof

Executable — a script that runs Plumbline's lint against the post-work tree, asserts the lint reports clean, and asserts the project's Plumbline configuration has every check active.
