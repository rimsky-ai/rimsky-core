---
issue: terminal-error-payload-two-shapes
kind: human
category: inconsistent
artifacts:
  - concept:signal
  - concept:breakpoint
  - decision:event-log-payload-shapes
status: repaired
opened: 2026-08-07T08:55:56Z
github: https://github.com/rimsky-ai/rimsky-core/issues/78
---

# The same terminal error is emitted in two different payload shapes, depending on which surface reads it

Question: an executor-emitted error's `payload` reached `terminal/error/<class>`'s `error_payload` wrapped in an extra `{"payload": ...}` layer on the real (audited/cascaded) emission path, but unwrapped on the breakpoint-only path — which shape is canonical?

Re-verified at HEAD: still reproduced exactly as filed. `readExecutorOutcome` (`lib/runtime/runner_dispatch.go`) and `parseAsyncCallback` (`lib/runtime/callback.go`) both wrapped the executor's raw error payload as `{"payload": <raw>}` before it reached `errorPolicySignal` → `BuildTerminalErrorSignal`, while `signal_for_terminal.go` (breakpoint-only path) separately unwrapped the same key.

Rule that determined the fix: `concept:signal`'s Payload-schemas section already commits to the canonical shape — "Where a signal's payload wraps an opaque sub-object whose wire carrier also names its own opaque field `payload`, the inner field is renamed with a domain prefix on the rimsky side — a rimsky-side rename only." `TerminalErrorPayload.ErrorPayload` (`lib/foundation/signal/payloads.go`) is exactly that rename (`payload` → `error_payload`); a second, undeclared nesting layer contradicts the "rename only" rule. The unwrapped shape (already produced by `signal_for_terminal.go`) is the compliant one; no commitment changed, only the real emission path was brought into line with the already-decided rename discipline.

Fix: `readExecutorOutcome` and `parseAsyncCallback` now store the executor's raw payload unwrapped on `terminalEvent.Payload`, matching how runtime-synthesized errors already behaved; `signal_for_terminal.go` no longer needs (or does) an unwrap step. Updated the one test that asserted the old wrapped shape (`lib/runtime/signal_for_terminal_test.go`) and removed the now-unneeded defensive double-unwrap in the e2e harness (`lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go`).

Verified: `go build ./...` and `go test ./lib/...` pass.
