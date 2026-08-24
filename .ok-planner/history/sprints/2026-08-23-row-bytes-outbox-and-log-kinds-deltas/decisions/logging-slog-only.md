---
decision: logging-slog-only
---

# Logging library

## Choice

Stdlib structured logging, written as JSON. Every rimsky-authored process installs the same JSON handler and takes its level from one shared environment variable, `RIMSKY_LOG_LEVEL`. That population is the three core roles, the entrypoint, the migrate binary, the CLI and the processes it launches, the host daemon, the host-daemon proxy, and every bundled service.

## Rationale

Minimize dependencies; production-ready stdlib. Verbosity is a deployment-wide setting, so one variable carries it. An operator raises the level once and every process follows, whether it runs as its own container or as a handler inside the all-in-one process. One handler shape keeps every process's output readable by the same parser. A fitness check enumerates the process entrypoints, so a new binary cannot ship with a level nothing can change.

## Alternatives

- Zap or Zerolog — rejected: a third-party dependency for what the standard library already provides.
- A per-service log-level variable in place of the shared one — rejected: an operator would set eleven variables to raise verbosity across one deployment.
- Let each process pin its own level — rejected: an operator could not turn up a service that is misbehaving.
