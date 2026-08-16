---
audit: bundled-park-resume-recipe
artifact: story:bundled-park-resume-recipe
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:10:09Z
---

# No shipped recipe drives park-then-resume, though the behaviour itself works

Unsupported. The story promises an evaluator something to copy and run; the tree
ships no such thing. Every committed file outside the planner estate was
enumerated — 1917 of them, 102 of them runnable scripts or manifests — and none
drives a node through a park and its resume. The demos the tree does ship are the
onboarding, cascade-send, client-context, frame-origin-audit and
host-agent-control-plane walkthroughs, none of which parks anything; the README's
first-steps walkthrough is the onboarding demo; there is no docs, examples or
recipes directory, no build target that runs one, and the bundled HTTP-node
executor's README describes parking in prose without a runnable sequence. The
failure is the recipe, not the behaviour: the run drove park-then-resume itself
on the bundled stack, where the HTTP-node executor parked a worker on a real
rate-limited answer tagged as such, the park resumed on its own schedule, and the
same run reached the upstream a second time and succeeded. An evaluator can
therefore reach the behaviour only by authoring the template, a rate-limited
endpoint and the wiring themselves, which is what the story exists to spare them.
