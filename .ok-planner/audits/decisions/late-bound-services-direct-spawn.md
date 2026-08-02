---
audit: late-bound-services-direct-spawn
artifact: decision:late-bound-services-direct-spawn
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095818-service-spawn-dispatch-path-has-no-e2e-test
---

# Late-bound services under self-host are spawned directly on loopback ports

Unsupported: the structural claim holds by direct inspection — the self-hosted run verb reuses the same spawn primitive and synthesizes the same kind of executor entry the compose one-shot uses, with no proxy client anywhere in the self-host path — but no test drives an actual dispatch through it. Every test found either covers the pieces in isolation (the spawn primitive's own process lifecycle, flag and error paths, config-overlay merging) or reaches a terminal state using a bundled in-process executor rather than a spawned late-bound binding. The parallel remote/proxied path this decision explicitly mirrors is proven end to end; this composed path is not.
