---
decision: testing-scenario-based-e2e
---

# Testing discipline

## Choice

End-to-end via the test group's scenarios directory + the services
module's scenarios test directory driving the assembled product;
persistence tests use an integration-test container helper to boot
real backends. Harness wait helpers are poll-until-success: they
block until the awaited state appears, with the suite-level timeout
as the only time-based backstop, and they report the expected-versus-
observed state descriptively on that exit path.

## Rationale

Real-stack integration tests against the load-bearing safety
properties documented in the concept catalog. Unbounded waits keep
the verdict load-independent (see
`decision:test-wallclock-lint-ratchet`); descriptive suite-timeout
exits preserve the diagnosability that bounded helpers' failure
messages used to provide.

## Alternatives

- Deadline-bounded poll helpers that fail the test on expiry —
  rejected: the deadline is a verdict input, the exact idiom the
  testing rules ban.
