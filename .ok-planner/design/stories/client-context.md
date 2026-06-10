---
story: client-context
status: as-is
---

# Operator switches between control-api endpoints

## Role

As an operator on a dev machine, I can register multiple control-api endpoints in the `rimsky` CLI, switch between them, and inspect or remove them, so that I run commands against several deployments without flag plumbing.

## Capability

Per-CLI context catalog: register, switch, inspect, remove control-api endpoints — subsequent CLI commands target the active context.

## Business value

Operators run commands against several deployments without flag plumbing; switching is a one-step verb rather than a re-export of environment variables.

## Acceptance

The operator registers a context naming a control-api endpoint, switches the active context to it, and from that point CLI commands hit that endpoint; switching to a different registered context redirects subsequent commands. Inspecting the active context names the current endpoint; removing a context makes it no longer switchable to.

## Falsifier

Switched context isn't picked up by the next command, OR removed context still resolves.

## Proof

Demo — a runnable script walking through register / switch / use / remove, with two real local control-api endpoints to make the switch observable.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
