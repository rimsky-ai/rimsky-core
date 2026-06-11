---
story: rimsky-deployment-bootstrap
status: as-is
---

# Entrypoint role selection + migrate discipline

## Role

As an operator deploying rimsky to a stack, I can run the bundled `rimsky-entrypoint` with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit env-var override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.

## Capability

`rimsky-entrypoint` role-selection + migration discipline: no command spawns all roles, a known role command spawns just that role, an unknown command exits non-zero. Migrate runs exactly once per deployment with `RIMSKY_ENTRYPOINT_MIGRATE` override available.

## Business value

Operators choose any deployment topology (all-in-one or three-role split) and the schema reaches the right state deterministically — no races, no silent skips, no manual one-shot orchestration when not wanted.

## Acceptance

Running `rimsky-entrypoint` with no command starts all three role binaries and runs migrations once before any of them start; running it with a single role command (`rimsky-scheduler` / `rimsky-supervisor` / `rimsky-control-api`) starts only that role and runs migrations only when the role is `rimsky-control-api` (so a three-container split migrates exactly once, not three racing runs); running it with an unknown command or multiple args exits non-zero with a clear error. `RIMSKY_ENTRYPOINT_MIGRATE=1` forces migrate; `=0` skips it.

## Falsifier

Migrations race when the three-container split fires three simultaneous `rimsky-entrypoint` processes, OR a three-container split never migrates, OR an unknown command silently spawns the all-in-one path.

## Proof

Executable proof.
