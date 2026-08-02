---
decision: single-process-mode
---

# The all-in-one runs all three roles in one process

## Choice

The entrypoint's no-command path runs all three roles (scheduler, supervisor, control-api) in one process via the existing library entry points, each on its configured port, with one signal-handled shutdown; the bundled executor and claim-producer handlers register into the process's in-proc dispatch pool via the bundled registration entrypoint (see `decision:bundled-registry-entrypoint`), and a failure to construct any configured bundled handler aborts the boot before any role starts. The single-role path (explicit role command) keeps its per-role process behavior. A process-role env marker names the unified single-process mode; it is set exactly by the paths that genuinely run rimsky in one shared process — including the conformance runner, whose in-memory blob backend depends on it — and it is what gates the memory blob backend (see `concept:replica`, `story:single-process-all-in-one`, `decision:rimsky-run-self-hosts-templates`).

## Rationale

The unified env marker and the memory-blob gate both promise a shared-process deployment; the role mains are thin wrappers over library calls, so the promised deployment is the cheap honest fix.

## Alternatives

- Keep three spawned processes and remove the memory gate — rejected: leaves the unified marker meaningless and the memory backend useless even in dev.
