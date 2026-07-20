---
decision: single-process-mode
status: as-is
---

# The all-in-one runs all three roles in one process

## Choice

The entrypoint's no-command path runs migrate synchronously, then registers the bundled executor and claim-producer handlers into the process's in-proc dispatch pool via the bundled registration entrypoint (see `decision:bundled-registry-entrypoint`), then starts all three roles (scheduler, supervisor, control-api) in-process via the existing library entry points, each on its configured port, with one signal-handled shutdown. A failure to construct any configured bundled handler aborts the boot before any role starts. The single-role path (explicit role command) keeps its per-role process behavior. A process-role env marker names the unified single-process mode. Its setters are the entrypoint's no-command all-in-one path, the compose one-shot, and the ephemeral-run verb in self-host mode — the three the marker's blob-config error text names — plus the conformance runner's in-memory blob backend, which sets the same marker to satisfy the memory-backend gate even though it runs neither the compose one-shot nor the three-role all-in-one stack (see `concept:replica`, `story:single-process-all-in-one`, `decision:rimsky-run-self-hosts-templates`).

## Rationale

The unified env marker and the memory-blob gate both promise a shared-process deployment; the role mains are thin wrappers over library calls, so the promised deployment is the cheap honest fix.

## Alternatives

Keep three spawned processes and remove the memory gate (rejected: leaves the unified marker meaningless and the memory backend useless even in dev).
