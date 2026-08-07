---
issue: runtime-minted-classes-absent-from-synthesized-list
kind: human
category: enforcement-gap
artifacts:
  - concept:error-policy
  - concept:executor
status: repaired
opened: 2026-08-07T09:45:30Z
github: https://github.com/rimsky-ai/rimsky-core/issues/90
---

# Does declaring an `error_types:` policy for `executor_protocol_violation` or `abandoned` draw a spurious "undeclared vocabulary" warning?

Re-verified on the current tree: yes. `executor_protocol_violation` is
minted at two sites (`lib/runtime/runner_dispatch.go` — an undeclared
tag and a Park missing `resume_at`) and `abandoned` is minted at two
sites (`lib/runtime/runner_terminal.go`, `lib/runtime/held_cascade_defer.go`
— the held-claim auto-terminal abandon path), but neither was in
`lib/foundation/spec/enums.go::RuntimeSynthesizedErrorClasses`, so a
template routing on either class through `error_types:` drew the
"not in any declared vocabulary" warning from
`lib/graph/node/template_validator.go`'s registration check.

**Rule that determined the fix.** `concept:error-policy`'s own
invariant already commits to the fix: error-class keys are
range-checked "against the union of the declared vocabularies a key
may legitimately come from: the node's executor's declared error
classes, the runtime-synthesized classes... and the declared error
classes of every claim producer." Both classes are runtime-synthesized
by construction (minted by the runtime, not any executor or
producer), so the corpus already requires them to be in that union;
their absence was an implementation gap, not an open design question.
`decision:terminal-error-abandoned-as-error-class` additionally
confirms `abandoned` is a first-class, intentionally-routable error
class.

**What changed.** Added `ErrorClassExecutorProtocolViolation` and
`ErrorClassAbandoned` named constants to
`lib/foundation/spec/enums.go`, appended both to
`RuntimeSynthesizedErrorClasses`, and repointed the four runtime mint
sites (`lib/runtime/runner_dispatch.go` ×2,
`lib/runtime/runner_terminal.go` ×2,
`lib/runtime/held_cascade_defer.go` ×1) from raw string literals to
the new constants, matching the existing pattern for the other six
synthesized classes. Added
`TestValidateErrorTypes_AcceptsRuntimeMintedExecutorProtocolViolationAndAbandoned`
to `lib/graph/node/template_validator_error_types_test.go` (mirrors
the existing `executor_sync_timeout` regression test) to pin the fix.

**Verified.** `go build ./...` and
`go test ./lib/foundation/spec/... ./lib/graph/node/... ./lib/runtime/...`
all pass.
