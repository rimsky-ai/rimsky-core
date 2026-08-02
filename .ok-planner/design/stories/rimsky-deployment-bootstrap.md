---
story: rimsky-deployment-bootstrap
---

# Entrypoint role selection + migrate discipline

## Story

As an operator deploying rimsky to a stack, I can run the bundled multi-role entrypoint with no command to launch all three roles together for dev (or as a single role for multi-process production), and trust that database migrations run exactly once per deployment regardless of role split — never racing across roles, never silently skipped — with an explicit environment-variable override for one-shot init containers, so that the deployment topology is whatever I choose and the schema arrives at the right state deterministically.
