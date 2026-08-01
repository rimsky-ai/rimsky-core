---
story: local-orchestrator-zero-config
status: as-is
---

# Local user runs an ad-hoc template with zero config

## Role

As a local user (a developer iterating on templates against files on my own machine), I can run an ad-hoc template through the rimsky all-in-one process with no rimsky.yml and no external service infrastructure for the bundled executors and claim producers the template references, so that I iterate locally without a docker/compose stack — driving real bundled services (real claude CLI spawn, real filesystem reads) end-to-end.

## Capability

The ephemeral-run verb, given a template file and no configured endpoint, self-hosts a full all-in-one stack inside the CLI process: synthetic config in a run directory, bundled executor and claim-producer handlers registered in-process, all three roles started, the template registered + deployed + instantiated against the local control-api, events streamed until the instance reaches terminal, then a clean teardown (see `decision:rimsky-run-self-hosts-templates`, `decision:bundled-registry-entrypoint`). Bundled services read their operator env vars from the CLI process's environment; unset means open defaults, and services whose required configuration is absent are skipped rather than blocking the run (see `decision:per-service-load-opts-from-env`).

## Business value

The iterate-on-a-template loop needs zero infrastructure: no rimsky.yml, no docker, no compose stack, no service containers. One binary, one command, real bundled services.

