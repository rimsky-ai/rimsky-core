---
audit: coverage-wildcard-asymmetry
artifact: decision:coverage-wildcard-asymmetry
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:46Z
---

# Wildcard subscriptions cover whole-pull reads; per-field subscriptions do not

Supported. `lib/graph/node/template_validator.go`'s `coverageMatch` (feeding `validateSubstitutionRefCoverage`, tagged `@decision: coverage-wildcard-asymmetry`) implements the asymmetry directly: a whole-pull ref (`ref.FieldPath == ""`) is covered only by an `attribute/*` subscription entry, never by a per-field `attribute/<x>/changed` entry; a per-field ref is covered by either its matching per-field entry or the wildcard. Three of the claim's four cells are exercised by tests in `lib/graph/node`: `TestSubstitutionCoverage_WholePullRefUncovered` proves a per-field subscription does not cover a whole-pull read (rejected, citing the decision in its assertion message); `TestCheckAttributeSource_BareFormPulls`'s "bare nodes attribute pull accepted" subtest proves the wildcard does cover a whole-pull read (accepted); and `TestCoverageCheck_SymmetryWithNodes` proves a matching per-field subscription covers a per-field read (accepted). The fourth cell (wildcard covering a per-field read) has no dedicated test but follows directly from the same `coverageMatch` branch already exercised by the whole-pull-accepted case.
