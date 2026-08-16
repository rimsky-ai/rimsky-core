---
assessment: claude-agent--async-dispatch
subject: story:claude-agent
way: async-dispatch
release: d977250c
outcome: held
warrant: experiment:claude-agent
---
# Agent work is handed off and settled later, not held open

The audit drove a deployment of `catalog:images/rimsky-all-in-one` running the bundled agent executor against a stand-in agent speaking the same contract, on one template of seven nodes exercising each clause the story names. Twelve checks ran and none failed. Every agent node's work was handed off asynchronously and settled later by callback, so a long-running agent turn does not occupy a dispatch slot while it thinks. The executor advertised thirteen declared error classes over the control API, which is what lets an operator see the failure vocabulary of agent work before wiring anything to it.

## Unverified remainder

None: the passing run demonstrates the way as promised.
