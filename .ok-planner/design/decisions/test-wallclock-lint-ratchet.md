---
decision: test-wallclock-lint-ratchet
---

# Wall-clock verdict idioms are lint-forbidden in test code

## Choice

A lint forbids wall-clock verdict idioms in test code: fail-on-timeout
selects, deadline-bounded poll loops that fail on expiry, and
deadline-polling helpers — including third-party ones such as
`require.Eventually`, whose deadline is a verdict input. The gate
fails on any violation. Its recorded baseline is empty. Every wait the
lint admits carries a class marker per `decision:polling-audit`. A
per-site suppression marker exists for sleeps that are genuinely not
verdict inputs (fixture pacing); each carries its justification at
the site.

## Rationale

The testing rules already retired the dialect — any finite timeout is
a guess about machine load, so a deadline that fails a test is a
load-dependent verdict, not a verdict. Prose alone let roughly two
hundred sites accumulate. A ratchet stopped new instances while the
backlog stood. One sweep drained the backlog once the class marker
made every site classifiable. An empty baseline keeps the gate
absolute, so the banned dialect cannot re-enter with a new test.

## Alternatives

- Keep the ratchet with a standing baseline — rejected: a standing
  backlog is a standing excuse, and the class marker made the sweep
  mechanical rather than judgment-heavy.
- Keep auditing periodically without a gate — rejected: leaves the
  rule permanently unenforced; the banned dialect re-enters with
  every new test.
- Loosen the rule to sanction generous documented timeouts —
  rejected: "why 30 and not 29?" has no answer; any finite bound is
  an unprovable load guess.
