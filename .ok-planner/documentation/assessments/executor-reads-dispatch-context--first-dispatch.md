---
assessment: executor-reads-dispatch-context--first-dispatch
subject: story:executor-reads-dispatch-context
way: first-dispatch
release: d977250c
outcome: held
warrant: experiment:executor-reads-dispatch-context
---
# Reading, from inside the agent script, that this dispatch has no predecessor

A stand-in agent binary driven by the bundled `catalog:bundled-services/claude-agent (executor)` read its dispatch context and wrote what it read into the node's output attributes. On a plain node the script read a dispatch id equal to the one rimsky recorded for that run, a non-empty run-scope id, and a null predecessor with a null predecessor disposition. The script therefore knows it is the first attempt at this work without inferring it from indirect signals such as an empty attribute bag. Eight checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
