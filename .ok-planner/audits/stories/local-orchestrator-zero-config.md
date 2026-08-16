---
audit: local-orchestrator-zero-config
artifact: story:local-orchestrator-zero-config
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:50:41Z
---

# Ad-hoc template run from one binary and one command, with no standing infrastructure

Supported: the CLI binary alone drives an ad-hoc template to terminal with
nothing else running. Both template cases were run in a deliberately hostile
environment for the claim — a scrubbed process environment with no rimsky
variables, an empty home directory so no stored configuration or endpoint could
resolve, and a fresh working directory — with no docker, no compose stack, and no
external executor process. Each run booted its own stack, migrated a fresh local
database, registered and deployed the template, and drove an instance to terminal
before returning. That the bundled services are real and not stubs was settled by
a pass/fail pair rather than by a log line: the clean template exited zero with
both nodes at success, while the same template with one null in the checked field
exited non-zero with the node carrying the bundled verifier's own error class for
its own check — an outcome only the service's own check logic produces. Six
checks across two cases, none failing.
