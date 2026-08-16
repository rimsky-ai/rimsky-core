---
audit: write-semantics
artifact: concept:write-semantics
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# The four write-semantics values, their coexistence sub-matrices, and the five invariants the concept claims

Supported. The enum carries exactly four concrete values plus a zero value, and the coexistence predicate reproduces each of the four sub-matrices the concept describes: the staged-asynchronous value alone conflicts only writer against writer, while the synchronous, blocking-asynchronous, and read-only values each coexist only reader against reader — and the predicate panics on any value outside the four, so the enum is closed where it matters. All five invariants hold. The operator-declared allowed set is checked against the producer's advertised envelope during the startup wiring of every remote claim producer, rejecting both a non-subset and an empty declaration before the process serves traffic. Every remote Open's realized value is checked twice per claim in the producer client — once against the advertised envelope and once against the operator's narrowing, each with its own error naming both sets — and the same guard rejects the wire zero value outright, so a producer returning UNKNOWN never reaches the supervisor's persistence step. The two claims that are obligations on producers rather than on rimsky are both instruments in the claim-producer conformance kit rather than assertions in prose: one scenario opens the same byte-equal scope twice and fails the producer when the realized values differ, which is byte-equal-scope uniformity; another opens a reader concurrently with an open writer on a byte-equal scope under the staged-asynchronous value and fails the producer when the reader is blocked, which is the reader-lease prohibition, and it carries its own test asserting that a reader-lease producer is failed rather than passed. The realized value is persisted per claim handle in both backends under the claimant guard, so the conflict-matrix input the concept says it owns is durable.
