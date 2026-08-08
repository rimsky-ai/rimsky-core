---
issue: validation-readme-misdescribes-when-validator-runs
kind: human
category: doc-drift
artifacts:
  - concept:validation
  - story:validation-author
  - decision:doc-accuracy-gates
status: retired
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:55:30Z
github: https://github.com/rimsky-ai/rimsky-core/issues/107
---

# The validation example understates when a validator gets called

A validation service is an external peer rimsky consults when a template is
registered. Its example README says the pipeline walks the template's nodes and
calls the validator for each one it references.

It does more than that. There are three independent walks: one per node, one per
publisher entry, and — for a validator advertising the lifecycle-subscriber role
— one against every registered validator client, with no filter for whether the
template references it at all.

That last one is what makes this worth correcting rather than tidying. An
implementer sizing a handler off "walks nodes only" builds for the templates that
name their service. A validator advertising the lifecycle-subscriber role is
called for every template registered anywhere in the deployment — a different
order of traffic, and different assumptions about what the handler can afford to
do per call.

Two smaller claims in the same file, both re-verified:

- It says the pipeline calls the producer's capability check and caches the
  result. There is no cache — each check dials a fresh short-lived connection and
  closes it.
- Its list of what the example ships omits the Dockerfile. Both the build target
  and the example's own end-to-end test depend on it, so a reader reconstructing
  the directory from the list cannot run the test the README tells them to run.

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
