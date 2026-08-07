---
issue: park-payload-carries-undeclared-keys
kind: human
category: inconsistent
artifacts:
  - concept:signal
  - concept:parked-state
status: repaired
opened: 2026-08-07T08:55:58Z
github: https://github.com/rimsky-ai/rimsky-core/issues/81
---

# The emitted transient/park payload carries two keys its struct does not declare

Question: should `TransientParkPayload` declare `scratch_size` / `scratch_spilled`, or should the emitter stop adding them?

Re-verified at HEAD: `parkTerminalSignal` (`lib/runtime/runner_terminal_park.go`) still added `scratch_size` and `scratch_spilled` to the payload map after building it from `TransientParkPayload`, and neither field was declared on the struct in `lib/foundation/signal/payloads.go`.

Rule that determined the fix: `concept:parked-state`'s Invariants already commit to these two fields as part of the park signal's payload — "The park audit signal's payload carries the scratch payload's byte size and a spilled-to-blob flag, never the scratch bytes; a zero-length scratch payload records size zero and spilled false." The emission code was already honoring this commitment; only the Go struct (and therefore the CEL schema binding used for `when:` predicate registration) was out of sync with it.

Fix: added `ScratchSize int` (`json:"scratch_size"`) and `ScratchSpilled bool` (`json:"scratch_spilled"`) to `TransientParkPayload` in `lib/foundation/signal/payloads.go`, and updated `parkTerminalSignal` to set them on the struct directly instead of mutating the payload map after `PayloadMap()`. No behavior change — the wire payload is identical; the schema now matches it.

Verified: `go build ./...` and `go test ./lib/foundation/signal/... ./lib/runtime/...` pass.
