---
issue: executor-readme-three-defects
kind: human
category: doc-drift
artifacts:
  - concept:error-policy
  - story:executor-protocol
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:26Z
github: https://github.com/rimsky-ai/rimsky-core/issues/102
---

# The executor example's README hands the reader a test command that passes without testing

The executor example is the primary onramp for anyone writing a service against
rimsky's executor protocol. Its README gives a command to run the end-to-end
test, and the test-name filter in that command matches nothing. Go reports
success and exits zero. The reader concludes the example works, having run
no tests at all.

Five more defects sit in the same file, all re-verified against the current tree:

- It describes a validator "mode" setting with two values. No such configuration
  key exists anywhere in the tree; the validation runs unconditionally.
- It says error-class names are range-checked so a typo can't silently no-op.
  An unknown class produces a warning, not an error — the policy still registers,
  and the warning text says so.
- It counts five end-to-end legs and describes a fifth that doesn't exist. There
  are three.
- Its list of in-process tests omits one of the six.
- It names a helper on the wrong package.

One of the originally filed defects has since been fixed: the error-policy
snippet's key is now correct in the tree. That one needs nothing.

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
