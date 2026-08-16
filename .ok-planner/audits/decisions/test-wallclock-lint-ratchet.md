---
audit: test-wallclock-lint-ratchet
artifact: decision:test-wallclock-lint-ratchet
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:49:58Z
---

# The wall-clock-idiom lint, its one-way ratchet, and its justified suppression marker

Supported. A dedicated scanner walks the four top-level code directories, classifies a file as test code by test-suffix or by sitting under a test-shaped directory, and carries detectors for each of the three idiom classes the decision names: fail-on-timeout selects, deadline-bounded poll loops in both their loop forms, and deadline-polling assertion helpers — the third-party detector covers all six `Eventually`/`Never` variants of the assertion library the project uses, which is the case the decision calls out by name. The ratchet is one-way in both directions and enforced by an ordinary test in the pin-test package, which runs under the root module's suite: the gate fails when a file's count exceeds its recorded baseline, and it also fails when a file's count falls below it, forcing the drained backlog to be locked in. The recording tool itself refuses to write an increased per-file count unless deliberately overridden. The committed baseline records 234 sites across 115 files. The per-site suppression marker exists and is only honoured when the site carries non-empty justification text after it, on the violating line or the line above.
