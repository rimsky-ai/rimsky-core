---
audit: test-wallclock-lint-ratchet
artifact: decision:test-wallclock-lint-ratchet
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:46Z
---

# Wall-clock verdict idioms are lint-gated with a draining baseline ratchet

Supported. `tools/wallclock-lint/scan/scan.go` detects the banned idioms named in the decision — `require`/`assert` `.Eventually`/`.Never` family calls, `case <-time.After(` fail-on-timeout selects, and `for time.Now().Before(...)`/`for time.Since(...)` deadline-bounded poll loops — across all test code in the `cmd`, `lib`, `test`, `tools`, and `examples` roots. `test/plumbline/wallclock_ratchet_test.go` is the ratchet: it compares the live scan against `tools/wallclock-lint/baseline.json` (124 files, 264 recorded violations) per file, failing when a file's count rises above its baseline and failing again when a file's count falls below its recorded baseline without the baseline being regenerated — a genuine one-way drain, checked by reading both branches of the comparison. A per-site suppression, `//nolint:testwallclock`, is recognized by the scanner only when followed by non-empty justification text, matching the "each carrying its justification at the site" requirement. This test runs under the ordinary `go test ./...` sweep the project's build gate already requires.
