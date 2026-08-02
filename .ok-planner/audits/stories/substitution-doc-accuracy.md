---
audit: substitution-doc-accuracy
artifact: story:substitution-doc-accuracy
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:40Z
---

# The substitution grammar's documented source-kind list matches the resolver's actual dispatch set

Supported. `lib/graph/attribute/substitution_doc_accuracy_test.go::TestSubstitutionDocMatchesResolverDispatchSet` parses `substitution.go`'s package doc comment and its `resolveDirectiveValueRaw` dispatch switch via `go/ast`/`go/parser` (not by trusting either side's prose) and fails if the two source-kind sets — the documented `{{<kind>.…}}` bullets and the switch's case-clause string literals — differ in either direction. Reading the current file directly confirms both sides list the same six kinds today (`claim`, `params`, `nodes`, `messages`, `child`, `env`), so the mechanical cross-check the story requires both exists and currently passes.
