---
issue: spec-selftarget-dead-code
kind: human
category: bug
artifacts:
  - decision:pre-v1-pure-removal-for-retired-surfaces
status: repaired
opened: 2026-08-07T08:49:16Z
github: https://github.com/rimsky-ai/rimsky-core/issues/68
---

# Is `spec.SelfTarget` a deliberate reservation of the `self` token, or dead code that should go?

Re-verified on the current tree: `lib/foundation/spec/template.go`'s
`SelfTarget = "self"` and its re-export in
`lib/graph/node/template.go` were still the only two occurrences of
the symbol anywhere in the tree — no reader.

**Rule that determined the fix.** `decision:pre-v1-pure-removal-for-retired-surfaces`'s
Choice is unconditional: "Retired DSL surfaces are removed from the
code entirely... No detection rule, no migration error string, no
parser case that names the old shape." A zero-reader constant naming
a retired reactive-loops surface is exactly what that Choice forbids
keeping around, so there is exactly one compliant end state: delete
it. Nothing else in the corpus reserves the `self` token for a future
use.

**What changed.** Removed the `SelfTarget` constant from
`lib/foundation/spec/template.go` and its re-export from
`lib/graph/node/template.go`. No other code, test, or fixture
referenced either symbol.

**Verified.** `go build ./...` passes.
