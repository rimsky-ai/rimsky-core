---
decision: service-spawn-flag
status: adopted
---

# service-spawn-flag

## Choice

The compose-run verb accepts a per-service spawn flag mapping a service name to a local binary (with bare-name aliases), mirroring the standalone run verb's service-spawn flag. The verb spawns binaries directly using the same exec-and-ready-poll mechanism the host-agent uses, registers each spawned endpoint in the synthetic unified config's executors block, and dispatches to the spawned port directly via the in-process supervisor. The host-agent proxy chain (per `concept:host-agent-proxy`) is not used here because the supervisor is in-process and dials the spawned endpoint directly (see `concept:host-agent`, `concept:supervisor`).

## Rationale

The spawning primitive (port-pick + ready-environment injection + ready-poll + child-process supervision) is a shared primitive called by both the host-agent and the compose-run verb (see `concept:host-agent`). The familiar flag shape on the verb means consumers and operators don't relearn the spawn surface.
