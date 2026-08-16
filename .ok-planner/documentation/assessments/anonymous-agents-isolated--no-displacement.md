---
assessment: anonymous-agents-isolated--no-displacement
subject: story:anonymous-agents-isolated
way: no-displacement
release: d977250c
outcome: held
warrant: experiment:anonymous-agents-isolated
---
# A second developer's agent joins without knocking the first one off

The audit brought up an anonymous-mode deployment with no keys minted and one `catalog:images/rimsky-host-agent-proxy`, then started two host agents on the machine with `catalog:cli-verbs/rimsky agent start`, each with its own state directory, its own identity and its own routing label. Both agents connected to the same proxy and both stayed connected: the second registration did not displace the first. `catalog:cli-verbs/rimsky agent status` reported both agents connected before the work and again after it. Two developers can therefore hold agent connections against one shared anonymous deployment at the same time.

## Unverified remainder

The run held two agents against one proxy; it does not establish a ceiling on how many agents one proxy admits.
