---
issue: examples-readme-apache-claim-and-guarantee-table
kind: human
category: doc-drift
artifacts:
  - concept:module-layout
  - decision:licensing-dual-apache-agpl
  - decision:licensing-enforced-by-license-lint
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:31Z
github: https://github.com/rimsky-ai/rimsky-core/issues/108
---

# The examples module's entry README overstates its licensing guarantee

rimsky is dual-licensed: the protocol module is permissive, the core is copyleft.
The examples module's README tells readers the examples depend only on the
permissive module — a statement an integrator would reasonably rely on when
deciding whether copying an example carries a copyleft obligation.

The module's dependency manifest directly requires two copyleft modules. The
product code is clean — only the end-to-end test files reach them, so building an
example pulls nothing copyleft — but the guarantee as written is broader than
what holds, and the distinction between "the build is clean" and "the manifest
lists nothing" is exactly the one a compliance reader needs. The project's own
executor example already words this correctly, so the precise version exists in
the tree; the entry point is the imprecise one.

Two further defects in the same file, re-verified:

- It offers the examples as reference for streaming-server method signatures. No
  example implements a streaming RPC — the two matches in the tree are
  unimplemented stubs.
- Its table gives a cross-stack proof in the guarantee column for one directory.
  Six carry one. And one directory listed there has neither a demo script nor any
  test of its own; its driver lives outside the examples tree entirely, so the
  table implies a guarantee that isn't in the directory it names.

## Ruling

> Retired: the examples module is being removed in full, so the README this issue
> reports on ceases to exist and the drift it documents is dissolved rather than
> corrected. The documentation project maintains the cookbook that replaces it.
>
> Findings underneath these issues that concern rimsky rather than the examples
> were pulled out as their own issues before retirement — an unenforced publisher
> kind, an unproven ordering guarantee on terminal events, and a lifecycle
> callback that cannot refuse — so nothing about the platform is lost with the
> module.
