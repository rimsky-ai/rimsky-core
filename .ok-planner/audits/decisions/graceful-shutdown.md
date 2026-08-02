---
audit: graceful-shutdown
artifact: decision:graceful-shutdown
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095824-production-shutdown-grace-is-30s-not-5s
---

# Graceful shutdown with a hardcoded grace

Unsupported. Checked all three process-shutdown entrypoints in the repository. Only the command-line development-tooling path matches the decision closely: a polite signal, a hardcoded five-second grace, forceful termination of stragglers, and a second-interrupt escalator that exits immediately. The two paths that front actual deployments — the all-in-one image's entrypoint and the shared bootstrap for the three standalone role binaries — both hardcode a thirty-second grace instead of five and read at most one signal, with no second-interrupt escalation. Separately, the supervisor's own wait for in-flight dispatches to finish uses its own hardcoded thirty-second timeout and, on expiry, only logs a warning naming the still-active count rather than terminating or force-killing anything. The five-second, hard-kill, second-signal description holds only for the development path, not for the paths that carry production traffic.
