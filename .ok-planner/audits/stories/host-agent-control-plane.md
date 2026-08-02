---
audit: host-agent-control-plane
artifact: story:host-agent-control-plane
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:12Z
---

# `rimsky agent {start,status,stop}` manages the daemon's lifecycle, reaping children on stop

Supported. `cmd/rimsky/cli/agent.go` implements the full `start` / `status` / `stop` subcommand surface: `start` daemonizes, polls the status file for a connected acknowledgment, and reports the pid; `status` reports connected/disconnected state plus any spawned children; `stop` sends SIGTERM then escalates to SIGKILL and removes the pid/status files. `test/scenarios/host_agent_control_plane_demo_test.go` proves this end to end twice: `TestHostAgentControlPlaneDemo` runs the shipped `examples/host-agent-control-plane-demo.sh` and asserts on the literal `started`/`connected`/`stopped` output lines, and `TestHostAgentControlPlaneDispatchReap` starts the daemon via the real CLI, dispatches a late-bound instance to spawn a real child process, stops the daemon, and asserts the daemon pid and the spawned child pid are both gone and that the child received a terminate signal (via a term-log side channel) — confirming the "children reaped" clause against the one children-set the daemon holds at stop time, not just the daemon's own exit.
