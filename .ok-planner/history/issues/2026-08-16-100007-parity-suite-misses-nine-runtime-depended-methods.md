---
issue: parity-suite-misses-nine-runtime-depended-methods
kind: audit
category: test
artifacts:
  - decision:parity-expansion
status: promoted
opened: 2026-08-16T10:00:07Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The cross-driver parity suite misses nine methods the runtime depends on

Two hand-mirrored persistence drivers are kept equivalent by a conformance suite that runs the same cases against both; a decision says it covers every queue, claim-handle and frame behaviour the runtime depends on. Of the 77 methods on those three interfaces, nine have no parity case, among them the frame-settlement method the graph frame engine calls — pinned only by a single-driver package test. The ruling adds the cases.

## Options

- Add parity cases for the nine, frame settlement first, and optionally a mechanical check that the suite's exercised set equals the interfaces' method set; cost: nine cases and, if taken, one enumeration test.
- Narrow the decision to a named subset; cost: accepts the drift risk the decision exists to prevent, for methods with live callers.

The ruling closes the coverage gap the decision promises is closed.

## Ruling

> Generated ruling (/verify-issues): Add cross-driver parity cases for the nine uncovered methods, the frame-settlement method first, and add the enumeration check so the decision's coverage claim is self-policing. Forced by the decision's own universal ("every … behavior the runtime depends on") over methods with live non-test callers. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
