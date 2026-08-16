---
assessment: host-agent-per-binding-overrides--working-directory
subject: story:host-agent-per-binding-overrides
way: working-directory
release: d977250c
outcome: held
warrant: experiment:host-agent-per-binding-overrides
---
# Giving each late-bind binding its own working directory

Each of the two bindings declared its own working directory for the same binary, and each spawned child reported back the directory its own binding named. The children ran as separate processes and neither adopted the other's directory, so a binary that resolves relative paths behaves per binding. Both nodes settled fresh under those directories.

## Unverified remainder

None: the passing run demonstrates the way as promised.
