---
decision: service-spawn-flag
status: adopted
---

# service-spawn-flag

## Choice

`rimsky compose run` accepts `--service <name>=<path>` (and bare `<name>` aliases), mirroring the `rimsky run` flag. The verb spawns binaries directly using the same exec-and-ready-poll mechanism the host-agent daemon uses, registers each spawned endpoint in the synthetic unified config's executors block, and dispatches to the spawned port directly via the in-process supervisor. The host-agent's proxy chain (per `concept:host-agent-proxy`) is not used here because supervisor is in-process and dials the spawned endpoint directly.

## Rationale

The spawning primitive (port-pick + env-var injection + ready-poll + child-process supervision) is a shared helper called by both the host-agent daemon and the verb. The familiar flag shape on the verb means consumers and operators don't relearn the spawn surface.
