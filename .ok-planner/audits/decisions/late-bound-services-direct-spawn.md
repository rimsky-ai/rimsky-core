---
audit: late-bound-services-direct-spawn
artifact: decision:late-bound-services-direct-spawn
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# Direct loopback spawning of late-bound services under the self-hosted run, read against the shared spawn path

Supported. The self-hosted ephemeral run and the compose one-shot call the identical service-spawning function — there is exactly one in the package — which resolves each binding to an absolute path (through the alias table when the flag names no path), hands it to the host-agent package's spawn primitive with the current environment, and records the child as an executor entry with the gRPC transport pointing at a loopback address and the port the primitive picked. That overlay is folded into the run's synthetic config before the stack starts, so the embedded stack dials the spawned binaries as ordinary registered executors: out-of-process and loopback-reached, never in-process, since the in-process transport appears only in the bundled registration path. Neither rejected alternative is present: nothing in the self-host path starts a host-agent daemon or synthesizes a proxy entry, and no code rejects late-bound bindings under self-host. A test spawns a stub binary through the self-hosted run flag and drives a node to terminal against it.
