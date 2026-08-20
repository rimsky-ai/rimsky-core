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

The grace is hardcoded at three values by path: five seconds where the CLI
supervises child processes it spawned itself, ten seconds where a bundled
service drains its own serving surfaces, and thirty seconds on the deployed
core paths — the container entrypoint, the standalone role processes, the
host-agent proxy's two gRPC listeners, and the supervisor's wait for
in-flight dispatches to drain. Every path, without exception, treats a
second interrupt as escalation to hard exit: immediate hard kill,
best-effort close. One shared helper installs that escalation. An entry
point gets the escalation by calling that helper, so no entry point
implements it again.

An operator tunes one window: the host agent's wait for a spawned child to
exit. The operator sets it in the process environment. That window governs a
third-party binary, not a rimsky drain.

## Rationale

The three windows serve different populations. A CLI-spawned child is a
local process an operator is watching in a terminal; five seconds is long
enough for a well-behaved one to unwind and short enough that a misbehaving
one never holds the session. A bundled service drains in-flight RPCs
only it knows about. Ten seconds covers that drain and still returns before
an orchestrator's container stop expires. A deployed core process is draining
dispatches carrying real work whose loss costs more than the wait, so
thirty seconds gives the drain a genuine chance to complete. The host-agent
proxy takes that window too. It ships as one of the four distributed core
images, and the calls it drains are dispatches bound for a host agent, not a
bundled service's own RPCs.

None of the three is configurable, because the operator need a knob would
serve — "let me out now" — is answered by the second-signal escalation
instead. The escalation is universal rather than a property of one path,
because it is what makes a fixed grace tolerable. It works only in a process
that installs it, so one helper installs it in every process. The host
agent's child window is the exception: the operator, not rimsky, knows how
long a third-party binary needs to exit.

## Alternatives

- One hardcoded grace across every path — rejected: a five-second value
  cuts live deployed dispatches to serve a local-tooling responsiveness
  need, and a thirty-second value makes an operator wait on a local child
  that has already stopped mattering. The three populations genuinely differ.
- A configurable grace period for rimsky's own drains — rejected: a knob
  whose only real use is immediate exit, which the second-signal escalation
  already provides without configuration.
- Wait indefinitely for in-flight work to unwind — rejected: a single
  misbehaving executor blocks the operator's exit.
- Leave each entry point to install the escalation itself — rejected: a new
  process omits it without failing anything, and the omission surfaces only
  when an operator presses a second interrupt and nothing exits.
