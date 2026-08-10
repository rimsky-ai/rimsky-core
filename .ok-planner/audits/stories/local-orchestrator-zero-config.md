---
audit: local-orchestrator-zero-config
artifact: story:local-orchestrator-zero-config
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# An ad-hoc template run with one binary, one command, and no configuration

Supported. Two `rimsky run <template>` invocations under `env -i` with an empty
home directory and no docker involved each booted an in-process stack against a
fresh local database and drove the template to terminal: the clean case exited 0
with every node at `terminal/success`, and the case whose rows violate the
declared check exited 1 with the node at
`terminal/error/verifier/check_failed/no_nulls`. That error class is the bundled
shape-check service's own, produced by its own check logic, so the run exercised
a real bundled service rather than a stub; of the three credential-free bundled
executors the boot registers in-process, the template named one and used it.
