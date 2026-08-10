---
audit: operator-onboarding
artifact: story:operator-onboarding
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# A newcomer copies the shipped example and watches it finish

Supported. The tree ships an onboarding template and a walkthrough script beside
it, and the README's first-steps section names the template by path, so a
newcomer has something to copy and a page telling them to. Copied out of the tree
into a scratch directory and run against a zero-config all-in-one deployment, one
verb registered, deployed and instantiated the copy and printed the instance id,
the watch verb returned zero once the instance reached a terminal state, and the
instance's node reported `terminal/success`. The copied walkthrough script, which
wraps the same two verbs, ran end to end and exited zero. Nothing in the
walkthrough asked the newcomer to write or edit a template.

## Compliance

The body names the delivery surface ("run a single CLI verb"), which the story
rules place in `decisions/` rather than in a story. Compliant text: "As a new
operator with no prior rimsky experience, I can copy a shipped example workflow,
run it against my local stack in one step, and watch the resulting instance run
to completion, so that I learn the dev loop end-to-end without writing a template
from scratch."
