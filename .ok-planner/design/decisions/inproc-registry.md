---
decision: inproc-registry
status: as-is
aliases: []
---

# Explicit in-process handler registry at supervisor startup

## Choice

An in-process registry (a map from executor identity to handler) constructed explicitly at supervisor startup. The supervisor's setup path imports each builtin handler package, constructs handler instances, and inserts them by canonical executor identity. The registry is passed into the executor client pool factory. Bundled utility executors live as builtin handlers under the runtime's executor package.

## Rationale

Explicit wiring is testable (the registry is constructible in tests with arbitrary handler sets) and the dependency graph stays visible — every utility handler the binary serves is an explicit import. Avoids init-time globals.

## Alternatives

Init-time self-registration via package-init functions against a global registry. Simpler API for handler authors but introduces hidden ordering and makes test isolation harder.
