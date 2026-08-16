---
experiment: bundled-park-resume-recipe
commit: d977250c
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

Five checks, two failing.

The tree ships no such recipe. Of the 102 committed runnable files outside the
planner estate, none is a script that drives a park and its resume, and the
README names none. The demos the tree does ship are the onboarding, cascade-send,
client-context, frame-origin-audit and host-agent-control-plane walkthroughs;
none of them parks anything. The bundled HTTP-node executor's own README
describes parking in prose without a runnable sequence.

The behaviour a recipe would demonstrate does work on the bundled stack. The
bundled `http-node` executor parked the worker on the endpoint's 429, tagged
`rate_limited`; the park resumed on its own retry schedule; the same run reached
the endpoint a second time and settled at `terminal/success`.

RESULT: FAIL — park-then-resume runs on the bundled stack, but nothing ships that
an evaluator can copy and run to see it.
