---
decision: late-bound-services-direct-spawn
---

# Late-bound services under self-host are spawned directly on loopback ports

## Choice

Under the self-hosted ephemeral-run verb, a late-bound service binding (a service name bound to a local binary path) reuses the direct-spawn code path the compose one-shot uses: the host-agent spawn primitive launches each binding on a loopback gRPC port, and an ephemeral executor entry pointing at that loopback endpoint is synthesized into the run's synthetic config, so the self-hosted stack dials the spawned binaries as ordinary registered executors. Late-bound services are therefore out-of-process and loopback-reached — matching the compose one-shot semantically — NOT in-proc handlers; the in-proc path is reserved for bundled services. No separate host-agent daemon is auto-started and no host-agent-proxy config is required.

## Rationale

The host-agent package already exposes the spawn primitive as a callable unit, and the compose one-shot already uses it directly without the daemon. Reusing that path under the self-hosted run verb gives one code path for both self-host cases with no proxy plumbing.

## Alternatives

- Auto-start the host-agent daemon and synthesize a proxy entry into the ephemeral config — rejected: extra processes and config synthesis, duplicating the compose one-shot's simpler shape.
- Reject late-bound bindings under self-host — rejected: loses functionality the remote path has.
