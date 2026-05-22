# executors/stub

A **test-double Executor** for rimsky's executor protocol.

## What this is

A `stub` in this directory is a **test double** in the Meszaros sense: a
deterministic, scripted implementation of the Executor gRPC service used to
exercise rimsky's supervisor against canned outcomes. It is the executor
analog of a fake/mock in unit testing — not a skeleton template, not an
"implement this later" placeholder, not a copy-paste starting point.

If you are writing a real Executor for a real workload, **do not start
from `stub`**. Look at the reference implementations:

- **`executors/http-node/`** (Go) — invokes user-provided HTTP endpoints
  per node type.
- **`executors/claude-agent/`** (TypeScript) — runs Claude Code CLI as the
  executor, with full async-handoff and writeback support.

Those two demonstrate the wire shape, the heartbeat cadence, the async
callback bridge, observability emissions, and the realistic terminal
flow. `stub` deliberately omits most of that machinery because tests
don't need it.

## Three primary uses

1. **`executors/stub/cmd/`** — a standalone gRPC binary
   (`rimsky-executor-stub`) used by the quickstart and smoke-deployment
   compose stacks as a no-op executor. Combined with
   `RIMSKY_EXECUTOR_STUB_MODE=1`, it returns immediate-success outcomes
   keyed by `node_type` via `StubAttributesFor` — useful for end-to-end
   stack tests that exercise the supervisor + control-api + persistence
   path without actually doing executor-side work.

2. **`executors/stub/stubtest/`** — the in-process wrapper used by
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

3. **`rimsky-executor-conformance`** — when invoked with
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
| `.Error(class, payload)` | `StreamClose.Error` (use `class="executor_blocked"` for the executor-blocked path) |
| `.Park(reason, reasonNote, payload, resumeAt, sessionToken)` | `StreamClose.Park` |
| `.AwaitAsyncCallback(ackID, expectedMs)` | `StreamClose.AwaitAsyncCallback` |

Plus modifiers:

- `.Heartbeats(n)` — emit N extra heartbeats before the terminal.
- `.Delay(d)` — sleep `d` before each event.
- `.EmitNamedEvent(name, payload)` — emit a `NamedEvent` before the
  terminal (multiple calls accumulate in order).

`EnableStubMode()` short-circuits every Execute call to an
immediate-success outcome with `attributes_delta = StubAttributesFor(node_type)`.
