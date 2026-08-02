---
audit: one-shot-to-terminal
artifact: story:one-shot-to-terminal
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Operator drives a compose manifest to terminal in one invocation

Supported. `TestComposeRunOneShotTerminal_E2E` builds the real `rimsky` binary and a stub executor, then runs `rimsky compose run <manifest>` as a single subprocess against a bare manifest and a late-bound service flag — no separate stand-up step, no pre-provisioned rimsky infrastructure. The test asserts the process exits with the aggregate outcome (mixed success/failure), the spawned service process is gone after the invocation returns (nothing left running), and the run's own SQLite state database (materialized under the run's `.rimsky` artifact directory) shows both declared instances present and terminated. One process invocation both starts and finishes the run.
