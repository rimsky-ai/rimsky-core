---
issue: claim-binding-partition-request-dead-wire
kind: human
category: bug
artifacts:
  - concept:fan-out
  - concept:claim-producer
  - concept:validation
status: repaired
opened: 2026-08-07T09:45:25Z
github: https://github.com/rimsky-ai/rimsky-core/issues/85
---

# ClaimBinding.partition_request was dead wire: plumbed on both sides, populated by nothing

Re-verified on the current tree: `ValidateClaimBinding.PartitionRequest`
(`lib/runtime/clientiface/validation.go`) and `ClaimBinding.partition_request`
(`validation.proto`) were still carried end-to-end to the wire but never
populated — `runClaimProducerRoleChecks`
(`lib/runtime/validation_pipeline.go`), the only site that builds these
bindings, left the field zero-valued.

**Rule that determined the fix.** Not a new design question: the field's own
sibling fields on the same struct (`Selector`, `Data`) already establish the
live idiom for `ClaimBinding` — forward the raw, unresolved template literal
verbatim at registration time (substitution only happens later, at
acquisition, per `concept:fan-out`'s invariant that `fan_out.partition_request`
"is resolved through substitution at acquisition, not passed verbatim").
`concept:fan-out`'s acquisition-time claim lookup
(`lib/runtime/runner_acquire_helpers.go::resolveFanOutParentClaim`) also
already establishes that `nodeDef.FanOut.Claim` is matched against a claim's
*alias* (`AcquiredLock.Alias`, equivalently `NodeClaimProducerRef.AliasOf()`)
— the same key `runClaimProducerRoleChecks` already groups bindings by. One
compliant end state: populate `PartitionRequest` on the binding whose alias
is the node's declared fan-out claim, leaving it empty for every other claim
on the node (matching the historical proto comment's description, "empty
when the node does not fan out against this claim").

**What changed.** `lib/runtime/validation_pipeline.go::runClaimProducerRoleChecks`
now sets `binding.PartitionRequest = []byte(n.FanOut.PartitionRequest)` when
`n.FanOut != nil && n.FanOut.Claim == s.AliasOf()`. No proto change — the
wire field already existed on both sides.

**Verified.** `go build ./...` and `go test ./lib/runtime/...` pass; `make lint`
clean.
