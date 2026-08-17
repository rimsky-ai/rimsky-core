---
issue: lifecycle-conformance-suite-lacks-its-own-package
kind: audit
category: conflicting
artifacts:
  - concept:conformance
  - decision:conformance-suite-per-protocol
status: promoted
opened: 2026-08-16T08:59:35Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The lifecycle-subscriber conformance suite lives inside the executor suite's package

The conformance library proves a third-party service implements a rimsky protocol; the conformance concept says it carries one sub-package per protocol, and the per-protocol decision's reason is that an implementer of one protocol certifies without carrying the others. Seven suites have their own package; the lifecycle-subscriber suite is one file inside the executor package, so certifying only lifecycle means compiling the executor suite and its fixtures. The CLI subcommand already exists and is unaffected — only the packaging is wrong. The ruling moves the file.

## Options

- Move the lifecycle suite into its own sub-package and repoint the CLI's subcommand; cost: none beyond the move.
- Bless the exception in prose; cost: contradicts the decision's own rationale.

The ruling makes the eighth suite match the other seven.

## Ruling

> Generated ruling (/verify-issues): Give the lifecycle-subscriber conformance suite its own sub-package beside the other seven and point the existing subcommand at it. Forced by the conformance concept ("one sub-package per protocol") and the per-protocol decision's rationale. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
