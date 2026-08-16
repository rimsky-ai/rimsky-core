---
assessment: host-agent-control-plane--status
subject: story:host-agent-control-plane
way: status
release: d977250c
outcome: held
warrant: experiment:host-agent-control-plane
---
# Checking whether the agent is connected and what it is running

After the agent started, `catalog:cli-verbs/rimsky agent status` reported it connected, named the proxy it had attached to, gave the time since connecting, and reported no spawned children. An instance then bound a local binary through `catalog:template-keys/late_bind_services` and was woken, and status listed exactly one spawned child, naming the run-scope it belongs to, the binding path the operator declared, and the spawn id, with the child process alive under the process id it had written. The status verb therefore answers both questions an operator has — is it attached, and what is it running right now — without reading logs.

## Unverified remainder

None: the passing run demonstrates the way as promised.
