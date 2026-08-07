---
issue: allow-paths-env-var-absent
kind: audit
category: config-surface
artifacts:
  - concept:host-agent
status: promoted
opened: 2026-08-06T06:49:16Z
sprint: 2026-08-06-ruled-intake-drain.md
---

# Spawn confinement is flag-only, so env-configured host agents always run unconfined

The host agent can confine which binaries it will spawn to an
operator-supplied path allowlist — but only via the `--allow-paths`
CLI flag (`cmd/rimsky/cli/agent.go:62`). Every other knob on the same
startup path has an environment-variable override
(`lib/runtime/hostagent/config.go:33-58` reads env equivalents for
seven of the eight flags); the security-relevant one is the exception.
The standalone `rimsky-host-agent` binary defines no flags at all and
reads config purely from the environment, and an empty allowlist
accepts every spawn path (`lib/runtime/hostagent/spawn.go:259`). A
container or service-unit deployment therefore always runs with spawn
confinement wide open, with no way to close it short of a wrapper
script that re-launches through the CLI flag.

The corpus commits that spawn paths "may optionally be bounded by an
operator-supplied path-glob allowlist" (`concept:host-agent`) without
naming which surface carries the bound; per-flag env parity is the de
facto convention of the very function this knob is missing from.

## Options

- Add `RIMSKY_AGENT_ALLOW_PATHS` (comma-separated globs) to the env
  loader, matching the existing convention; unset stays open. Cost:
  none beyond the change — it closes the parity gap and changes no
  default.
- Declare flag-only confinement intended and document it. Cost:
  env-only deployments remain unconfineable by design.
- Require an explicit opt-in to run unconfined in env-only mode.
  Cost: a posture change that breaks zero-config expectations for
  existing deployments.

The ruling decides whether env-configured deployments can confine
spawns.

## Ruling

> Recommended ruling (/verify-issues): add `RIMSKY_AGENT_ALLOW_PATHS`,
> comma-separated path globs, following the env-override convention
> the rest of the agent's config already uses; the default when unset
> stays open.
>
> Rationale: the concept promises an operator-supplied bound and the
> surrounding code promises env parity — the gap reads as an
> omission, not a choice, and the opt-in option is a posture change
> nothing has asked for. The flip case: a record that confinement was
> deliberately scoped to interactively-launched agents would turn
> this into documentation of a flag-only surface instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
