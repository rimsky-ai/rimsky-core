---
issue: story-rimsky-health-check-dependency-semantics-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rimsky-health-check
  - concept:control-api
status: verified
opened: 2026-08-01T22:32:20Z
---

# The health probe's success semantics need a home — and its story overstates what it checks

The control API exposes an unauthenticated health probe for load balancers and orchestrators. Its story promises in prose that the probe returns non-success when a critical dependency is down; the format rules force the story down to its sentence, so that mapping needs a home first.

Re-verification narrows the claim: the probe returns success unless a database transaction fails — persistence connectivity is the only dependency actually checked; nothing probes executors, producers, or publishers (`code:lib/control/controlapi/health.go::handleHealth`, pinned by the end-to-end test carrying the story's annotation). So the story's "critical dependency" wording overstates the implemented contract. The control-api concept already carries a sibling bullet about this exact route — every operation is auth-gated except the health probe (`concept:control-api`) — making it the natural home; a decision would require fabricating a rationale/tradeoff the code and history don't record, which the authoring rules forbid. Extending a concept's invariants is a sprint-level act.

## Options

- Extend the control-api concept's health-probe bullet with the semantics — success unless persistence is unavailable, non-success then — and reduce the story, tightening "critical dependency" to what is implemented.
- Rule the semantics below corpus altitude — leaves external orchestrator config (what does a 200 mean?) resting on story prose scheduled for deletion.

The ruling confirms the rule-forced homing; the accompanying narrowing (persistence, not "any critical dependency") is intent-level and is named here so the owner sees it rather than inheriting it silently.

## Ruling

> Generated ruling (/verify-issues): extend the control-api concept's health-probe bullet to state the mapping — the probe succeeds unless persistence is unavailable, and returns non-success then — and reduce the story to its sentence with the promise stated as persistence availability, not "critical dependency." The one-home principle and the ban on fabricated decision rationale leave the concept bullet as the only compliant home, and honesty about the implemented check forces the narrower wording.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
