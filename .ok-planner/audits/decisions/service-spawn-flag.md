---
audit: service-spawn-flag
artifact: decision:service-spawn-flag
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The shared service-spawn flag shape, the shared spawn primitive, and the absent proxy hop

Supported. Both verbs register the same repeatable service flag taking a service name mapped to a local binary path, with the bare-name alias form on both, and the compose one-shot's spawner is the same function the self-hosted ephemeral run calls. That function delegates to the host-agent package's spawn primitive — the one the agent itself uses for late-bound children — which picks a free port, injects it into the child's environment, polls until the child answers, and supervises the process. Each spawned binary is then written into the run's synthetic config as a plain gRPC executor entry addressed at its loopback port, so the in-process supervisor dials it directly. The proxy chain is genuinely out of the path: the synthetic config has no late-bind-proxy block at all, and nothing in the one-shot paths starts an agent or a proxy. Neither rejected alternative is present.
