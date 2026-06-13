---
story: operator-onboarding
status: as-is
---

# New operator runs first dev-loop end-to-end

## Role

As a new operator with no prior rimsky experience, I can copy a shipped example workflow, run a single CLI verb against my local stack, and watch the resulting instance run to completion, so that I learn the dev loop end-to-end without writing a template from scratch.

## Capability

Onboarding dev-loop: copy a shipped example templatespec, run `rimsky run <file>` against an all-in-one stack, observe instance progress to terminal.

## Business value

A new operator with no prior rimsky experience learns the dev loop end-to-end without writing a template from scratch — through a single CLI verb against a real shipped example.

## Acceptance

An operator without prior template-writing experience copies a shipped example templatespec, runs `rimsky run <file>` against a running all-in-one stack, observes the command print an instance ID and exit cleanly, can look the instance up through the standard list/get surfaces, and watches it progress to a terminal state through the real supervisor. A second assertion confirms the documented `rimsky run` invocation succeeds as written.

## Falsifier

The shipped example isn't a real runnable templatespec (would need modification to run), OR `rimsky run` is a stub that prints a fake ID without driving register + deploy + instantiate.

## Proof

Demo — a runnable shell sequence published as the first-steps walkthrough.
