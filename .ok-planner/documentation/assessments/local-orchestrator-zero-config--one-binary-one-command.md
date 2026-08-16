---
assessment: local-orchestrator-zero-config--one-binary-one-command
subject: story:local-orchestrator-zero-config
way: one-binary-one-command
release: d977250c
outcome: held
warrant: experiment:local-orchestrator-zero-config
---
# Running an ad-hoc template from the CLI alone, with nothing else standing

The audit ran the CLI (`catalog:binaries/rimsky`) against two ad-hoc templates in an environment made deliberately hostile to the claim: a scrubbed process environment with no rimsky variables set, an empty home directory so no stored configuration or endpoint could resolve, a fresh working directory, no container runtime, no compose stack, and no external executor process. Each invocation booted its own stack, migrated a fresh local database, registered and deployed the template, and drove an instance to terminal before returning. That the bundled services doing the work are real and not stubs was settled by a pass/fail pair rather than a log line: the clean template exited zero with both nodes successful, while the same template with one null in the checked field exited non-zero with the node carrying `catalog:error-classes/verifier/check_failed` — an outcome only the bundled shape-check service's own check logic produces. Six checks across two cases, none failing.

## Unverified remainder

The loop was demonstrated on the bundled shape-check service over inline rows. It does not establish zero-config behaviour for bundled services that reach outside the machine, nor that every bundled service is reachable without configuration.
