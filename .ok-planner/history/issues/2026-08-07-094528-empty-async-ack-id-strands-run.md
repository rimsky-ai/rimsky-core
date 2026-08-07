---
issue: empty-async-ack-id-strands-run
kind: human
category: bug
artifacts:
  - concept:node-run
  - concept:orphan-reaper
  - decision:async-callback-outcome-oneof
status: repaired
opened: 2026-08-07T09:45:28Z
github: https://github.com/rimsky-ai/rimsky-core/issues/88
---

# An AwaitAsyncCallback with an empty async_ack_id stranded the run permanently

Re-verified on the current tree: the gap was exactly as filed.
`lib/runtime/runner_dispatch.go::readExecutorOutcome`'s `Outcome_AwaitAsync`
case returned the empty `async_ack_id` unconditionally; the caller's
`if asyncAck != ""` guard then skipped registration and fell through to
`runApplyTerminal` with `terminalKindAsyncAccepted`, which `applyTerminal`'s
`default:` arm rejects — leaving the node-run row claimed and `running` with
no ack id, invisible to `ListOrphanedClaims`'s
`async_ack_id IS NOT NULL` predicate.

**Rule that determined the fix.** Not a new design question — the same
function already carries the governing precedent one case above: the
`Outcome_Park` arm rejects a Park outcome missing its required `resume_at`
by settling it as `terminalKindErrored` /
`spec.ErrorClassExecutorProtocolViolation` rather than falling through.
`concept:terminal-resolution`'s wire-to-terminal-kind stage states the same
principle generally: "every dispatch path resolves to a terminal-kind even
when the wire contract is violated." An `AwaitAsyncCallback` outcome with a
missing required `async_ack_id` is the same class of malformed-required-field
violation as Park's missing `resume_at`, and the one idiom already used for
it in this exact function is the single compliant fix — not a synthesized
new error shape.

**What changed.** `lib/runtime/runner_dispatch.go::readExecutorOutcome`'s
`Outcome_AwaitAsync` case now rejects an empty `AsyncAckId` up front,
settling as `terminalKindErrored` /
`spec.ErrorClassExecutorProtocolViolation` (`reason:
"await_async_missing_ack_id"`), mirroring the Park arm. The run now settles
as a diagnosable error at the point of the violation instead of stranding
in `running` with no path back through the orphan reaper.

**Verified.** `go build ./...` and `go test ./lib/runtime/...` pass; `make
lint` clean.
