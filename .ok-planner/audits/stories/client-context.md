---
audit: client-context
artifact: story:client-context
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# Multiple control-api endpoints registered, switched, inspected, and removed

Supported. Against two independently booted deployments each seeded with a
distinct template, both endpoints were registered, listed with their addresses,
and the current one marked; switching the current context re-targeted a
subsequent command that named no endpoint at all, which returned the first
deployment's template hash before the switch and only the second's after it; and
removing the non-current entry left the other listed. All 5 of the context verbs
the story implies — add, list, current, use, remove — were exercised.

## Compliance

The body names the delivery surface ("in the `rimsky` CLI", "without flag
plumbing"), which the story rules place in `decisions/` rather than in a story.
Compliant text: "As an operator on a dev machine, I can register multiple
control-api endpoints, switch between them, and inspect or remove them, so that
I run commands against several deployments without naming an endpoint on each
one."
