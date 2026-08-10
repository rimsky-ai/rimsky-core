---
audit: spawned-local-services
artifact: story:spawned-local-services
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A declared local binary, spawned for one run and gone when it returns

Supported, measured by two invocations of one command inside a scrubbed process
environment with an empty home directory, a fresh working directory, no docker,
no service installed and no configuration file written by hand. The command
declared the local binary as the run's service, exited 0, and its transcript
carried the binary announcing itself, serving the node's one execution, and both
of the instance's nodes reaching terminal success. When the command returned the
process that served the run was gone, no process was running the binary, and no
rimsky process was left behind. The second invocation was served by a different
pid, itself gone when that command returned. The same template run without the
service declaration exited non-zero at template registration, so the declaration
is what supplies the service rather than anything already installed.
