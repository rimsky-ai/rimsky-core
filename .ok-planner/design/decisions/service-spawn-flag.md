---
decision: service-spawn-flag
---

# Compose-run mirrors the run verb's service-spawn flag

## Choice

The compose-run verb accepts the same per-service spawn flag shape as the standalone run verb, mapping a service name to a local binary. It spawns binaries through the same exec-and-ready-poll primitive the host-agent uses and dispatches to each spawned endpoint directly from the in-process supervisor; the host-agent proxy chain (per `concept:host-agent-proxy`) is not in the path (see `concept:host-agent`, `concept:supervisor`).

## Rationale

The spawning primitive (port-pick + ready-environment injection + ready-poll + child-process supervision) is one shared primitive called by both the host-agent and the compose-run verb (see `concept:host-agent`). The familiar flag shape means consumers and operators don't relearn the spawn surface.

## Alternatives

- Route compose-run's spawned services through the host-agent proxy chain — rejected: the supervisor is in-process and can dial the spawned endpoint directly; the proxy hop buys nothing here.
- A compose-specific spawn flag shape — rejected: operators would relearn a surface the standalone run verb already established.
