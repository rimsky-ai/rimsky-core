---
story: clean-lint
status: as-is
---

# Maintainer verifies the codebase passes Plumbline's full enforcement

## Story

As a rimsky maintainer, I can verify that the codebase passes Plumbline's full enforcement with every check active, so that `decision:coding-style` accurately describes the active configuration.

Plumbline's lint, with every check active in the project's Plumbline configuration, reports the post-work tree clean. The configuration shows every check active.

The methodology recorded in `decision:coding-style` matches the active enforcement state of the codebase: contributors and agents reading the decision see the same set of checks the lint actually runs.
