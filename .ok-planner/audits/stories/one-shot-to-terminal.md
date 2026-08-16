---
audit: one-shot-to-terminal
artifact: story:one-shot-to-terminal
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:59:54Z
---

# One invocation stands a run up, drives it to terminal, and leaves nothing behind

Supported, including the two halves that are easy to assume rather than check.
The manifest declared two templates and two instances and was driven in a
scrubbed environment with an empty home directory, so no deployment was running
beforehand and none could have been addressed even if one had been. The single
invocation stood the stack up, applied the manifest, and reported both declared
instances reaching terminal before it returned — one succeeding, one failing,
which also shows it waits for real outcomes rather than for dispatch. That
nothing was left to tear down was settled by consequence: the control-api port
the run allocated, read out of the run's own transcript, refused connections once
the command returned. Six checks, none failing.
