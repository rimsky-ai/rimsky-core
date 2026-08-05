---
issue: no-inducible-park-on-the-bundled-stack
kind: human
category: developer-experience
artifacts:
  - story:bundled-park-resume-recipe
  - concept:parked-state
  - story:http-node
  - decision:bundled-recipes-production-paths
status: answered
opened: 2026-07-22T10:19:11Z
github: https://github.com/rimsky-ai/rimsky-core/issues/39
---

# Can a park-then-resume journey now be driven end to end against the bundled stack?

Question: the filed Problem said nothing in the bundled stack could induce a real park, so no clean-state, copy-runnable park-then-resume walkthrough was possible.

Answer: it now can, closing the gap `story:bundled-park-resume-recipe` promises. Commit `2ef58038` ("Bundled park-then-resume recipe (closes gh#39)") added a rate-limit-once endpoint under `examples/park-resume/` (built by `make test-images`), a copy-runnable `examples/park-resume-demo.sh`, and an e2e proof (`examples/park-resume/main_e2e_test.go`, `TestParkThenResumeOnBundledStackE2E`) that drives the bundled http-node through a real `429` → `transient/park` → timed wake → `terminal/success` settlement, asserting on both audit events by name and failing loudly ("the 429 dispatch did not travel the production parking path" / "the parked run never resumed to a successful settlement") if either is missing. This follows `decision:bundled-recipes-production-paths`'s Choice — induce the demonstrated state through the production parking path, never a synthetic probe or test hook — verbatim. Nothing here is left for a sprint to carry.
