---
story: bundled-park-resume-recipe
status: as-is
---

# Operator demonstrates park-then-resume on the bundled stack

## Role

As an operator evaluating rimsky's parking behavior, I can run a
self-contained, copy-runnable recipe on the bundled stack that drives a node
through a real park and its resumed completion, so that I can see
park-then-resume work end to end before wiring a real rate-limited upstream.

## Capability

A bundled park-then-resume recipe that is self-contained — everything it needs
ships with the stack, no external endpoint — and that induces its park through
the production parking path, not a conformance probe.

## Business value

Operators and template authors can observe rimsky's most temporal behavior —
park, timed wake, re-dispatch, settle — on a clean checkout, without standing
up an external service to trigger it.

## Acceptance

An operator runs the recipe against the bundled stack on a clean checkout: the
driven node enters the parked state with a near-term resume time; the
supervisor wakes it at that time and re-dispatches; the run settles
successfully. All of it is observable through the stack's ordinary surfaces,
and nothing outside the checkout is involved.

## Falsifier

The recipe requires an endpoint that does not ship with the stack, OR the
induced park travels a stub or probe path rather than the production parking
mechanism, OR the parked node never resumes to a successful settlement.

## Proof

Executable proof — an end-to-end exercise on the bundled stack exhibiting the
parked state and the subsequent resumed success.
