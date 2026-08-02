---
audit: verifier-http
artifact: story:verifier-http
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:05Z
---

# Template author validates via external check service using the bundled verifier-http executor

Supported. `lib/services/executors/verifier-http/executor.go::Execute` POSTs `attributes.url` with the configured `body`, treats any 2xx response (or an explicit `expected_status` set) as terminal success with a `verifier_pass`/`verifier_status` delta, and treats a non-matching status as a terminal error whose class is `verifier/check_failed` or, when the upstream body carries a class field, `verifier/check_failed/<upstream_class>` — matching the story's 2xx-success / 4xx-5xx-error-with-upstream-class routing. `test/scenarios/verifier_http_e2e_test.go::TestVerifierHttpCrossStack` cross-stack-builds the real binary and exercises all three legs against a live upstream: a 2xx leg asserting fresh/success with the configured body echoed byte-for-byte, a 4xx leg asserting a failed terminal typed `terminal/error/verifier/check_failed/rate_limited` (upstream class threaded through), and a 5xx leg asserting a failed terminal typed `terminal/error/verifier/check_failed`.
