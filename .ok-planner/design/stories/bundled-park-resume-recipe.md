---
story: bundled-park-resume-recipe
---

# Operator demonstrates park-then-resume on the bundled stack

## Story

As an operator evaluating rimsky's parking behavior, I can run a
self-contained, copy-runnable recipe on the bundled stack that drives a node
through a real park and its resumed completion, so that I can see
park-then-resume work end to end before wiring a real rate-limited upstream.

A bundled park-then-resume recipe that is self-contained — everything it needs
ships with the stack, no external endpoint — and that induces its park through
the production parking path, not a conformance probe.

Operators and template authors can observe rimsky's most temporal behavior —
park, timed wake, re-dispatch, settle — on a clean checkout, without standing
up an external service to trigger it.
