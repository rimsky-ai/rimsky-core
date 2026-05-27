# executors/stub

A **test-double Executor** for rimsky's executor protocol.

## What this is

A `stub` in this directory is a **test double** in the Meszaros sense: a
deterministic, scripted implementation of the Executor gRPC service used to
exercise rimsky's supervisor against canned outcomes. It is the executor
analog of a fake/mock in unit testing — not a skeleton template, not an
"implement this later" placeholder, not a copy-paste starting point.

If you are writing a real Executor for a real workload, **do not start
from `stub`** — it deliberately omits the heartbeat cadence, the async
callback bridge, observability emissions, and the realistic terminal
flow that a real executor needs, because tests don't need them.
Implement the Executor gRPC service directly against the protocol
(`protocols/proto/v1/executor.proto`); `stub` is a test double, not a
starting point.

## Two primary uses

1. **`executors/stub/stubtest/`** — the in-process wrapper used by
   scenario tests under `test/scenarios/`. Tests script per-node-type
   behavior on the shared `Stub` instance:

   ```go
   h.Stub.WhenType("worker").
       Success(map[string]any{"k": "v"}, true, "applied")
   h.Stub.WhenType("blocked-worker").
       Error("executor_blocked", map[string]any{"reason": "waiting"})
   h.Stub.WhenType("snoozer").
       Park(genv1.ParkReason_PARK_REASON_RETRY_BACKOFF, "rate_limit", payload, resumeAt, sessionToken)
   h.Stub.WhenType("async-worker").
       AwaitAsyncCallback("ack-1", 5000)
   ```

   The stub records every `ExecuteRequest` it sees in `Observed()` so
   tests can assert on what the supervisor wired through (attributes,
   store handles, callback URL).

2. **`rimsky-executor-conformance`** — when invoked with
   `--require-stub-mode`, the conformance probe runs against a stub-mode
   target as a known-good baseline for protocol-shape checks. The stub
   is the protocol's reference implementation in the same sense that
   `rimsky-conformance-probe` is the executor's reference client.

## Scripting DSL surface

`Stub.WhenType(type)` returns a `TypeBuilder` that produces one of four
terminal outcomes on the wire (mirroring the executor protocol's
`StreamClose` oneof):

| DSL method | Wire outcome |
|---|---|
| `.Success(result, changed, summary)` | `StreamClose.Success` |
| `.Error(class, payload)` | `StreamClose.Error` (use `class="executor_blocked"` for the executor-blocked path; the stub auto-prefixes single-segment classes with `stub/` so the wire-level class becomes `stub/executor_blocked` per `concept:signal` hierarchical convention) |
| `.Park(reason, reasonNote, payload, resumeAt, sessionToken)` | `StreamClose.Park` |
| `.AwaitAsyncCallback(ackID, expectedMs)` | `StreamClose.AwaitAsyncCallback` |

Plus modifiers:

- `.Heartbeats(n)` — emit N extra heartbeats before the terminal.
- `.Delay(d)` — sleep `d` before each event.
- `.EmitNamedEvent(name, payload)` — emit a `NamedEvent` before the
  terminal (multiple calls accumulate in order).

`EnableStubMode()` short-circuits every Execute call to an
immediate-success outcome with `attributes_delta = StubAttributesFor(node_type)`.
