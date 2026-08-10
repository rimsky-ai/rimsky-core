---
audit: one-shot-to-terminal
artifact: story:one-shot-to-terminal
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# One invocation drives a compose manifest's declared instances to terminal

Supported. A single `rimsky compose run` in an environment scrubbed of every
rimsky variable, with an empty home directory and no rimsky running anywhere,
stood up its own stack, applied a two-instance manifest, and reported both
declared instances reaching terminal — one success, one failure — before
returning, in about two seconds; the control-api port the run had allocated
refused connections once the command returned, so nothing was left to tear
down. Both declared instances of the manifest, 2 of 2, reached terminal inside
the invocation.
