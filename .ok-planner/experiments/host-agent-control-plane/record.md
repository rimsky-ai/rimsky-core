---
experiment: host-agent-control-plane
commit: d977250c
---

# Starting, inspecting and stopping the host-agent from the rimsky CLI

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`, both from the
tree's own image tag, on one docker network, plus the `rimsky` CLI binary built
from this tree. The agent runs on the host under its own state directory. The
late-bound binary is the local service built for
host-agent-late-bind-all-protocols; the binding hands it a pid file to write and
a delay to hold its execution for, so the run can inspect the agent while a
child is live. The agent is started with `RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1`,
which a child using the shipped peer-auth helper needs for the loopback
enrolment endpoint the agent hands it. Re-run unchanged at this tree.

## What was observed

Before starting, `rimsky agent status` reported the agent not running. `rimsky
agent start` returned 0 and named the pid it started and the proxy it connected
to. `rimsky agent status` then reported `connected`, the same proxy, the time
since connecting, and `spawned children: none`.

An instance bound the local binary and was woken. Status then listed exactly one
spawned child, naming the run-scope it belongs to, the binding path the operator
declared, and the spawn id. The child process was alive at that moment under the
pid it had written.

`rimsky agent stop` returned 0 and reported the agent stopped. The child process
was gone afterwards, and no process anywhere on the machine still held the bound
binary open. `rimsky agent status` reported not running again, and a second
`rimsky agent stop` returned 0 rather than an error.

RESULT: PASS
