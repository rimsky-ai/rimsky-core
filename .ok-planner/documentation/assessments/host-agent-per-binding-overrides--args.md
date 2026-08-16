---
assessment: host-agent-per-binding-overrides--args
subject: story:host-agent-per-binding-overrides
way: args
release: d977250c
outcome: held
warrant: experiment:host-agent-per-binding-overrides
---
# Giving each late-bind binding its own command-line arguments

The two bindings in the same instance declared different argument vectors for the same binary, and each spawned child received exactly the vector its own binding declared — four elements against two. Both nodes settled fresh, and the children ran as separate processes, so the argument list is per binding rather than per agent or per deployment. A template author declaring several bindings for varied local binaries gets the invocation each one needs.

## Unverified remainder

None: the passing run demonstrates the way as promised.
