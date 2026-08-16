---
audit: spawned-local-services
artifact: story:spawned-local-services
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:15:00Z
---

# A binary and one command: spawned for the run, gone when it returns

Supported. The whole story was driven as one command in a scrubbed process
environment with no rimsky variables, no docker, no compose stack, no service
installed on the machine and no hand-written configuration — just the CLI, a
template and a binary named on the command line. No process was running the
binary beforehand; the command exited successfully, and its transcript shows the
binary announcing itself, serving the node, and both of the instance's nodes
reaching terminal success. When the command returned, the process that served
the run was gone and no process was left running the binary or left over from
the command itself. A second invocation was served by a different process, which
was likewise gone when it returned. The same template run without naming the
binary did not run at all — the command exited non-zero at template registration
with a validation failure — so the binding is what supplies the service.
