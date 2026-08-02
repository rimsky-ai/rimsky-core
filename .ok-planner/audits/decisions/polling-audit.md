---
audit: polling-audit
artifact: decision:polling-audit
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:02Z
---

# Ordering-sensitive test waits block on the event-log tail instead of deadline-polling

Supported. `test/support/eventwait/eventwait.go`'s `WaitForEvent` (tagged `@decision: polling-audit`) is an unbounded loop that re-reads the durable event log on a fixed short interval and returns only once the matching event count appears, carrying no deadline or timeout in its own pass/fail logic — the only backstop is the suite-level `go test -timeout`. It is used by 6 scenario test files (`subscription_cascade_test.go`, `lifecycle_handlers_test.go`, `work_completed_test.go`, `parked_resume_spurious_cascade_test.go`, `retry_loop_cap_test.go`, `template_error_policy_e2e_test.go`) for exactly the ordering-sensitive assertions the decision describes, while the separate, permitted `require.Eventually` deadline-polling pattern (27 call sites across `test/scenarios`) is reserved for genuine outcome waits (e.g. a run count reaching a target), matching the decision's stated split.
