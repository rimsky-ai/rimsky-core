---
audit: service-spawn-flag
artifact: decision:service-spawn-flag
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095818-service-spawn-dispatch-path-has-no-e2e-test
---

# Compose-run mirrors the run verb's service-spawn flag

Unsupported: two of the decision's three claims hold by direct inspection — the flag shape is identical to the standalone run verb's, and the spawn primitive is the same single function called from all sites that spawn services this way. The third claim, that dispatch proceeds directly from the in-process supervisor with no proxy hop, is architecturally consistent but unexercised by any test: no test runs this verb's service-spawning form against a real or stub template and asserts a node reaches a terminal state via the spawned endpoint; the docker-based compose end-to-end tests that do reach terminal states exercise a different verb pair entirely.
