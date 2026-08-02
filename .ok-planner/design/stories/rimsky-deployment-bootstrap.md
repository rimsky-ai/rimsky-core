---
story: rimsky-deployment-bootstrap
---

# Entrypoint role selection + migrate discipline

## Story

As an operator deploying rimsky to a stack, I can run the bundled multi-role entrypoint with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit environment-variable override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.

Bundled multi-role entrypoint role-selection + migration discipline (see `concept:rimsky`): no command spawns all roles, a known role command spawns just that role, an unknown command exits non-zero. Migrate runs exactly once per deployment with an explicit migrate-mode environment-variable override available.

Operators choose any deployment topology (all-in-one or three-role split) and the schema reaches the right state deterministically — no races, no silent skips, no manual one-shot orchestration when not wanted.
