---
audit: bundled-recipes-production-paths
artifact: decision:bundled-recipes-production-paths
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# The park-then-resume recipe induces its park through production parking machinery, not a test hook

Supported. Both instances of the bundled park-then-resume recipe — the copy-runnable `examples/park-resume-demo.sh` and the Go end-to-end test `examples/park-resume/main_e2e_test.go` — drive the bundled `http-node` executor at a real HTTP endpoint that answers a real 429 with `Retry-After`; the resulting park is produced by the executor's ordinary production status-code handling in `lib/services/executors/http-node/server.go` (`resp.StatusCode == http.StatusTooManyRequests` → `parkedOutcome(parseRetryAfter(...))`), which is unconditional production code. The executor's separate synthetic "park probe" path (`executeParkProbe`) is gated behind a distinct stub-mode flag that neither recipe enables, so the alternative the decision names as rejected is demonstrably not what either recipe exercises.
