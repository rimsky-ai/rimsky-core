---
issue: publisher-readme-templatespec-cannot-register
kind: human
category: doc-drift
artifacts:
  - concept:publisher
  - concept:template
  - concept:signal
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:25Z
github: https://github.com/rimsky-ai/rimsky-core/issues/101
---

# The publisher example's only complete template cannot be registered

The publisher example is a copy-and-modify reference, and its README contains one
full worked template — the thing a reader copies. It doesn't register. Its
subscription entry uses two keys that aren't fields on a subscription, omits the
one key that is required, names a signal type outside the taxonomy the validator
accepts, and leaves out the message-type declaration the publisher block needs.

Four of those are independent rejections, so the snippet fails at the first hurdle
and a reader debugging it uncovers them one at a time. A registerable version
already exists in the example's own end-to-end test.

Four further claims in the same README were re-verified and are false:

- It says rimsky refuses a template whose publisher block names a kind the peer
  doesn't advertise. There is no such cross-check. Publisher validation confirms
  names are unique and non-empty and that the message type is declared — the
  peer's advertised kinds are never consulted.
- It documents an environment variable for overriding the example's port. The
  port is a literal in the source.
- It tells the reader the tests run without Docker. The end-to-end file carries
  no build tag and shares the package, so a plain package-wide test run boots
  containers and a full rimsky stack.
- It counts four acceptance legs; there are five.

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
