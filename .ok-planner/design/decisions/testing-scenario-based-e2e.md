---
decision: testing-scenario-based-e2e
---

# Testing discipline

## Choice

End-to-end via the test group's scenarios directory + the services module's scenarios test directory driving the assembled product; persistence tests use an integration-test container helper to boot real backends. Harness wait helpers are poll-until-success: they block until the awaited state appears, and they report the expected-versus-observed state descriptively when the run is cut short. The suites run with no per-package time ceiling; hang detection lives in the test guard, which watches the runner's event stream and kills a run only when no test has completed for a long interval.

## Rationale

Real-stack integration tests against the load-bearing safety properties documented in the concept catalog. Unbounded waits keep the verdict a function of the code alone (see `decision:test-wallclock-lint-ratchet`), and a progress-based hang backstop preserves that property where an elapsed-time ceiling cannot: a per-package timeout is an aggregate budget covering every test in the package, so one test blocking longer under load consumes the budget belonging to the rest and the verdict becomes load-dependent. A correct suite emits completions continuously at any load, so a no-progress interval never binds; a hung run emits nothing and still dies loudly.

## Alternatives

- Deadline-bounded poll helpers that fail the test on expiry — rejected: the deadline is a verdict input, the exact idiom the testing rules ban.
- A per-package elapsed-time ceiling as the hang backstop — rejected: it is an aggregate budget sized to total runtime, so machine load changes which tests are killed.
