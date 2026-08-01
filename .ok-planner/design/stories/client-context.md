---
story: client-context
status: as-is
---

# Operator switches between control-api endpoints

## Story

As an operator on a dev machine, I can register multiple control-api endpoints in the `rimsky` CLI, switch between them, and inspect or remove them, so that I run commands against several deployments without flag plumbing.

Per-CLI context catalog: register, switch, inspect, remove control-api endpoints — subsequent CLI commands target the active context.

Operators run commands against several deployments without flag plumbing; switching is a one-step verb rather than a re-export of environment variables.
