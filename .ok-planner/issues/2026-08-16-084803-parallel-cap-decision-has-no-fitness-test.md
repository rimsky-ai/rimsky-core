---
issue: parallel-cap-decision-has-no-fitness-test
kind: audit
category: test
artifacts:
  - decision:config-enforced-fitness-tests
  - decision:parallel-cap-removal
status: verified
opened: 2026-08-16T08:48:03Z
---

# The parallel-cap decision is enforced only by Makefile flags with no fitness test

A decision says test parallelism caps exist only for suites that share a real resource (the docker daemon), never as scheduling insurance; the Makefile carries the caps on three module targets and none on the fourth, which matches. Another decision says any decision enforced solely by a configuration surface — the Makefile named among them — must be proved by an annotated fitness test. No test proves this one; the only reference is a bare prose mention in a Makefile comment, exactly the unpoliced shape the fitness-test decision warns against. The ruling adds the test.

## Options

- Add a fitness test that reads the Makefile's test recipes and asserts the caps where they belong and their absence where they don't, annotated with the decision, and drop the bare comment reference; cost: none beyond the test.
- Carve test-runner flags out of the fitness-test decision; cost: contradicts that decision's explicit naming of the Makefile with no stated reason.

The ruling adds the missing proof.

## Ruling

> Generated ruling (/verify-issues): Add a grouped fitness test over the Makefile's test recipes — the docker-backed module suites carry the parallelism cap, the protocols suite carries none — annotated with the parallel-cap decision, and remove the bare decision reference from the Makefile comment. Forced by the fitness-test decision, whose Choice already names the Makefile as an in-scope configuration surface. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
