---
decision: host-daemon-port-assignment-no-handshake
---

# The host daemon assigns the child's port

## Choice

The host daemon picks a free local port, tells the spawned child which port to bind, and polls that port until the child's server answers, bounded by the spawn's ready-timeout. The child reports no port back. A binary that binds elsewhere fails the readiness poll and the spawn reports failure (see `concept:host-daemon`).

## Rationale

The daemon needs the port before the child is ready, because it starts polling immediately and dials the child for every dispatch afterwards. Assigning the port makes that knowledge unconditional and lets the daemon run several bindings on one machine without collision. A handshake back would need its own channel from a process that has not started serving yet, which is the readiness problem the port poll exists to solve. The binary carries the cost: a late-bound service reads the port the daemon gives it.

## Alternatives

- Let the child pick a port and report it back — rejected: it needs a second channel from a not-yet-serving child, and a child that never reports looks like a slow one.
- Fix a port per binding in configuration — rejected: two spawns on one machine collide, and concurrent bindings stop working.
