---
story: rimsky-deployment-bootstrap
status: as-is
---

# Entrypoint role selection + migrate discipline

## Role

As an operator deploying rimsky to a stack, I can run the bundled multi-role entrypoint with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit environment-variable override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.

## Capability

Bundled multi-role entrypoint role-selection + migration discipline (see `concept:rimsky`): no command spawns all roles, a known role command spawns just that role, an unknown command exits non-zero. Migrate runs exactly once per deployment with an explicit migrate-mode environment-variable override available.

## Business value

Operators choose any deployment topology (all-in-one or three-role split) and the schema reaches the right state deterministically — no races, no silent skips, no manual one-shot orchestration when not wanted.

## Acceptance

Running the bundled multi-role entrypoint with no command starts all three role binaries and runs migrations once before any of them start; running it with a single role command (scheduler, supervisor, or control-api) starts only that role and runs migrations only when the role is control-api (so a three-container split migrates exactly once, not three racing runs); running it with an unknown command or multiple arguments exits non-zero with a clear error. The migrate-mode override has a force-on setting that forces migrate and a force-off setting that skips it.

## Falsifier

Migrations race when the three-container split fires three simultaneous entrypoint processes, OR a three-container split never migrates, OR an unknown command silently spawns the all-in-one path.

## Proof

Executable proof.
