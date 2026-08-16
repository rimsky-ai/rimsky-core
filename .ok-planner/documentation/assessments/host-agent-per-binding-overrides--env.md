---
assessment: host-agent-per-binding-overrides--env
subject: story:host-agent-per-binding-overrides
way: env
release: d977250c
outcome: held
warrant: experiment:host-agent-per-binding-overrides
---
# Giving each late-bind binding its own environment variables

One template declared two late-bound services under `catalog:template-keys/late_bind_services` and one node each, and the instance bound both to the same binary under different configuration. Both nodes settled fresh, and each spawned child reported back exactly its own binding's environment variable value and label — one reporting the first binding's values, the other the second's. The two ran as separate processes, so neither binding's environment leaked into the other. A template author can therefore run the same binary twice in one instance under different environments without global configuration.

## Unverified remainder

None: the passing run demonstrates the way as promised.
