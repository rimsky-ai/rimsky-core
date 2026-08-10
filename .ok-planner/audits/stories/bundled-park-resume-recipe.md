---
audit: bundled-park-resume-recipe
artifact: story:bundled-park-resume-recipe
determination: unsupported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# Park-then-resume runs, but nothing ships that an evaluator can run

Unsupported. The story promises a recipe an operator can copy and run; the tree
ships none. Of the 102 committed runnable files outside the planner estate — every
`.sh`, `.py`, `.md`, `.yml` and `.yaml` file the repository tracks — not one is a
script that drives a node through a park and its resumed completion. Eight
mention both parking and resuming, and all eight are prose: two service READMEs
and six release notes. The README, which does carry a first-steps walkthrough
naming a runnable onboarding template, names no park recipe. What the recipe
would demonstrate does work: driven by hand against a zero-config all-in-one
deployment and a rate-limited endpoint, the bundled outbound-HTTP executor parked
the node on a 429, the park resumed on its own retry schedule, and the same run
reached the endpoint again and settled successfully. The gap is the shipped
artifact, not the behaviour.
