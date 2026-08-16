---
audit: host-agent-control-plane
artifact: story:host-agent-control-plane
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:05:00Z
---

# An operator starts, inspects and stops the host-agent, and its children are reaped

Supported. All three lifecycle acts the story names were driven against a
deployment with an agent proxy. Before starting, status reported the agent not
running; starting it returned success and named the pid it started and the proxy
it connected to; status then reported it connected, named that same proxy, gave
the time since connecting, and reported no spawned children. An instance then
bound a local binary and was woken, and status listed exactly one spawned child
naming the run-scope it belongs to, the binding path the operator declared, and
the spawn id, with the child process alive under the pid it had written.
Stopping the agent returned success and reported it stopped; the child process
was gone afterwards and no process anywhere on the machine still held the bound
binary open; status reported not running again, and stopping an
already-stopped agent returned success rather than an error.

## Compliance

The body names the delivery surface, which belongs to a decision: "through the host-agent control-plane CLI surface", and the benefit clause is about that surface rather than the need ("so that I manage the agent's lifecycle from the same CLI that drives the rimsky stack"); compliant text would say the operator can start the agent, see whether it is connected and what it is running, and stop it so that its children are reaped, so that they can bring the agent up and down as they work without leaving orphaned processes behind.
