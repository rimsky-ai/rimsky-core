---
audit: host-agent-control-plane
artifact: story:host-agent-control-plane
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Starting, inspecting and stopping the agent, with its children reaped

Supported, measured against a containerised deployment and its proxy with the
agent running on the host. All three verbs answered: status reported the agent
not running before it started; start returned 0 naming the pid and the proxy it
connected to; status then reported connected, the same proxy, the time since
connecting, and no spawned children. With one dispatch in flight, status listed
exactly one child naming its run-scope, the binding path the operator declared
and its spawn id, and that child was a live process on the machine. Stop
returned 0, the child process was gone afterwards, no process anywhere still
held the bound binary open, status reported not running again, and a second stop
returned 0 rather than an error.

## Compliance

The story authoring rules put the delivery surface in a decision, not in the
story. This body names it twice: "through the host-agent control-plane CLI
surface" in the capability, and "from the same CLI that drives the rimsky stack"
as the whole benefit. The compliant text states the need without the surface —
"As an operator running rimsky-dispatched workflows on a dev machine, I can
start the host-agent locally, check whether it is connected and what it is
running, and stop it with its spawned children reaped, so that I can see and end
the agent's work without hunting for processes it left behind."
