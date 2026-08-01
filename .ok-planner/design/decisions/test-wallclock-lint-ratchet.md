---
decision: test-wallclock-lint-ratchet
status: as-is
---

# Wall-clock verdict idioms in tests are lint-gated with a ratchet

## Choice

A lint forbids wall-clock verdict idioms in test code: fail-on-timeout
selects, deadline-bounded poll loops that fail on expiry, and
deadline-polling helpers — including third-party ones such as
`require.Eventually`, whose deadline is a verdict input. Enforcement
is a one-way ratchet: a recorded baseline covers the pre-existing
backlog, the gate fails when the violation count increases, and the
baseline drains as touched files are fixed. A per-site suppression
marker exists for sleeps that are genuinely not verdict inputs
(fixture pacing), each carrying its justification at the site.

## Rationale

The testing rules already retired the dialect — any finite timeout is
a guess about machine load, so a deadline that fails a test is a
load-dependent verdict, not a verdict. Prose alone let roughly two
hundred sites accumulate. Gating now and draining later stops new
instances immediately without churning dozens of test files against
in-flight work.

## Alternatives

- Sweep every site and gate in one motion — rejected: maximal churn
  across ~76 files, colliding with in-flight work, to remove backlog
  the ratchet drains anyway as files get touched.
- Keep auditing periodically without a gate — rejected: leaves the
  rule permanently unenforced; the banned dialect re-enters with
  every new test.
- Loosen the rule to sanction generous documented timeouts —
  rejected: "why 30 and not 29?" has no answer; any finite bound is
  an unprovable load guess.
