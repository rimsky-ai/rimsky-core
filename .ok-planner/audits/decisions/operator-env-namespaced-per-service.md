---
audit: operator-env-namespaced-per-service
artifact: decision:operator-env-namespaced-per-service
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095802-executor-host-port-envs-inconsistently-prefixed
---

# New bundled-service operator env vars are namespaced RIMSKY_<SERVICE>_*; generic per-executor knobs stay unprefixed

Unsupported. Checked all four bundled executors' operator environment variables: three of the four carry a per-service prefix on their host and port variables, contradicting the claim that generic per-executor knobs stay unprefixed; only one of the four matches the decision as written on this point. Every other checked category of operator variable — state, backend, and egress variables across the four sensors, the allowlist variables, stub-mode, and declared-tags — does carry the claimed prefix or lack of prefix correctly.
