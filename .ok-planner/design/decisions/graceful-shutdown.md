---
decision: graceful-shutdown
---

# Graceful shutdown with a hardcoded grace per path

## Choice

On interrupt, terminate, run-timeout expiry, or natural completion,
shutdown is polite-then-forceful: new dispatches stop first, in-flight
dispatches and spawned children receive a polite terminate signal, and
anything still running when the grace expires stops holding the shutdown —
spawned child processes are hard-killed, and in-flight dispatches the
supervisor is still waiting on are left to their own timeouts while
shutdown proceeds — before the remaining surfaces close and the process
exits (the most-recent-run pointer updating per `decision:artifact-layout`
on the way out).

The grace is hardcoded at two values by path: five seconds where the CLI
supervises child processes it spawned itself, and thirty seconds on the
deployed paths — the container entrypoint, the standalone role processes,
and the supervisor's wait for in-flight dispatches to drain. Every path,
without exception, treats a second interrupt as escalation to hard exit:
immediate hard kill, best-effort close.

## Rationale

The two windows serve different populations. A CLI-spawned child is a local
process an operator is watching in a terminal; five seconds is long enough
for a well-behaved one to unwind and short enough that a misbehaving one
never holds the session. A deployed process is draining dispatches carrying
real work whose loss costs more than the wait, so thirty seconds gives the
drain a genuine chance to complete.

Neither window is configurable, because the operator need a knob would
serve — "let me out now" — is answered by the second-signal escalation
instead. That is why the escalation is universal rather than a property of
one path: it is the escape hatch that makes fixed graces tolerable.

## Alternatives

- One hardcoded grace across every path — rejected: a five-second value
  cuts live deployed dispatches to serve a local-tooling responsiveness
  need, and a thirty-second value makes an operator wait on a local child
  that has already stopped mattering. The two populations genuinely differ.
- A configurable grace period — rejected: a knob whose only real use is
  immediate exit, which the second-signal escalation already provides
  without configuration.
- Wait indefinitely for in-flight work to unwind — rejected: a single
  misbehaving executor blocks the operator's exit.
