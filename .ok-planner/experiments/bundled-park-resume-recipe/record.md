---
experiment: bundled-park-resume-recipe
commit: PENDING
---

# What an evaluator can copy and run to see park-then-resume

## What it ran against

`way-shipped-recipe.py` does two things. First it enumerates what the tree ships:
every committed `.sh`, `.py`, `.md`, `.yml` and `.yaml` file outside the planner
estate, and looks for a runnable one that drives a park and its resume. Then it
drives park-then-resume itself on the bundled stack, to establish what a recipe
would have to reach: a docker network carrying `rate-limited-endpoint.py` with
`RETRY_AFTER=2` and a `rimsky-all-in-one` container with
`RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` set to the network's subnet, running
a template whose `http-node` worker calls that endpoint.

## What was observed

The tree ships no such recipe. Of the 102 committed runnable files outside the
planner estate, none is a script that drives a park and its resume; eight files
mention both parking and resuming, and all eight are prose — the http-node
executor's README, the stub executor's README, and six release notes. The README
names no park recipe.

The behaviour a recipe would demonstrate does work on the bundled stack. The
bundled `http-node` executor parked the worker on the endpoint's 429, tagged
`rate_limited`; the park resumed on its own retry schedule; the same run reached
the endpoint a second time and settled at `terminal/success`.

Three checks pass, two fail.

RESULT: FAIL — park-then-resume runs on the bundled stack, but nothing ships that
an evaluator can copy and run to see it.
