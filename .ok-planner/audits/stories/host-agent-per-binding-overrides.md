---
audit: host-agent-per-binding-overrides
artifact: story:host-agent-per-binding-overrides
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:05:00Z
---

# Each late-bind binding runs its binary under its own environment, arguments, directory and timeout

Supported. All four per-binding settings the story names were driven against a
deployment with a connected agent. One template declared two late-bound services
bound to the same binary under different configuration: both nodes settled
fresh, and each spawned child reported back exactly its own binding's
environment variable and label, its own argument vector (four elements against
two), and its own working directory, running as two separate processes, so
neither binding's configuration leaked into the other. The spawn timeout was
exercised in both directions on a binary that holds its startup for twenty
seconds: with a two-second timeout declared the node settled failed carrying the
agent's own spawn error, and with a sixty-second timeout declared and nothing
else changed the same binary spawned, served the dispatch, and the node settled
fresh.
