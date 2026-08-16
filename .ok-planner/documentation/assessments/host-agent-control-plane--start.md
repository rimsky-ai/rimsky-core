---
assessment: host-agent-control-plane--start
subject: story:host-agent-control-plane
way: start
release: d977250c
outcome: held
warrant: experiment:host-agent-control-plane
---
# Starting the host-agent from the same CLI that drives the stack

Against a deployment paired with a `catalog:images/rimsky-host-agent-proxy`, `catalog:cli-verbs/rimsky agent status` first reported the agent not running. `catalog:cli-verbs/rimsky agent start` then returned success and named both the process id it started and the proxy it connected to, so the operator learns what was started and where it attached from the command itself. The agent ran on the host under its own state directory, and a subsequent status read confirmed it connected to that same proxy. No separate process manager or configuration step stood between the operator and a connected agent.

## Unverified remainder

None: the passing run demonstrates the way as promised.
