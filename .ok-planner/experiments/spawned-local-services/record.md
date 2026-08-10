---
experiment: spawned-local-services
commit: PENDING
---

# A binary and one command: spawned for the run, gone when it returns

## What it ran against

The `rimsky` CLI binary built from this tree, invoked as `rimsky run
<template> --service devsvc=<path>` inside a scrubbed process environment
(`env -i`, an empty `HOME`, no rimsky variables) and a fresh working directory.
No docker, no compose stack, no service installed on the machine, no
configuration file written by hand. The bound binary is the local service built
for host-agent-late-bind-all-protocols; it logs the run-scope it was called for
and its own pid, and the command's transcript carries that log because the
spawned process writes to the command's own error stream.

## What was observed

No process was running the binary before the command. The command exited 0. Its
transcript shows the binary announcing itself, serving one Execute call for the
node, and both of the instance's nodes reaching `terminal/success`. Nothing in
the run named an installed service: the only executor the node could reach was
the one the flag bound.

When the command returned, the process that served the run was gone, no process
anywhere was running the binary, and no rimsky process was left behind. A second
invocation of the same command was served by a different pid, and that process
was gone when it returned too.

The same template run without the `--service` flag did not run at all: the
command exited non-zero at template registration with a validation failure, so
the binding is what supplies the service.

RESULT: PASS
