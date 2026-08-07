---
issue: aggregation-policy-validate-unreachable
kind: human
category: bug
artifacts:
  - concept:fan-out
  - concept:error-policy
status: repaired
opened: 2026-08-07T08:49:15Z
github: https://github.com/rimsky-ai/rimsky-core/issues/67
---

# Should `AggregationPolicy.Validate` or `validateFanOut` be the one live check for a node's fan-out aggregation policy?

Re-verified on the current tree: `lib/foundation/spec/aggregation_policy.go::AggregationPolicy.Validate`
was still unreachable outside its own unit test, while
`lib/graph/node/template_validator_holds.go::validateFanOut` remained
the sole live registration-time check, with a different and weaker
rule set (in particular, never rejecting `max_failures` on a
non-`threshold` kind).

**Rule that determined the fix.** This is a duplicated-check DRY
violation, not a new design question: `rules.md`'s pre-v1 clause
("delete dead code rather than carrying it forward") plus the
Plumbline DRY rule ("semantically identical logic lives in ONE
place") together force deleting the unreachable duplicate rather than
promoting it. `validateFanOut`'s permissiveness on `max_failures` is
not a gap this issue's rules newly decide — the corpus (`fan-out.md`,
`error-policy.md`) is silent on that numeric knob, and the issue's own
evidence records that the external docs corpus was already corrected
to describe the live (permissive) behavior as canonical. So the one
forced, commitment-preserving move is removing the dead code, not
strengthening the live check.

**What changed.** Deleted `AggregationPolicy.Validate` (and its
now-pointless `TestAggregationPolicy_Validate` unit test) from
`lib/foundation/spec/aggregation_policy.go` /
`aggregation_policy_test.go`. `validateFanOut` in
`lib/graph/node/template_validator_holds.go` is now the sole
implementation of this check family; no behavior change for any
already-registering template. `TestAggregationPolicyYAMLBinds` (an
unrelated YAML-binding test) is untouched.

**Verified.** `go build ./...` and
`go test ./lib/foundation/spec/... ./lib/graph/node/...` both pass.
