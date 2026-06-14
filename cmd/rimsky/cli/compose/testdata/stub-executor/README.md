# stub-executor

Minimal late-bound gRPC executor used by the Pass 6 acceptance proofs
for `rimsky compose run`. Not a deployable service; not exported as a
test-support package.

## Purpose

The compose-run verb's `--service <name>=<path>` flag spawns a binary
through `lib/runtime/hostagent.SpawnService`. That helper picks a free
localhost port, sets `RIMSKY_AGENT_PORT=<port>` in the child's
environment, and poll-dials `127.0.0.1:<port>` until the child binds.
This stub satisfies that contract with the simplest possible Executor
gRPC implementation, so the acceptance demos and proof tests can drive
a real compose-run end to end without the heaviness of a full
executor.

## Behavior

The stub branches on the dispatch request's resolved attributes:

- default — emit a single `Success` terminal and close the stream.
- `outcome=fail` — emit a single `Error` terminal with
  `error_class=stub/failed`.
- `delay_ms=<n>` — `time.Sleep` for `<n>` milliseconds (capped at
  60_000) before emitting the terminal. Used by STORY-live-progress
  to interleave a fast and slow instance.

`Capabilities` reports a permissive open attribute schema
(`{"type":"object"}`) so attribute-bearing nodes pass the dispatch-
time attribute-surface gate, plus `stub/failed` in
`declared_error_classes` so a template's `error_types:` policy can
reference the class without registration-time rejection.

## How tests invoke it

Tests build the binary at runtime via `go build -o <tmpdir>/stub-executor
./cmd/rimsky/cli/compose/testdata/stub-executor`, then pass it to
`rimsky compose run --service stub=<path>`. The compose-run verb spawns
the binary, merges its endpoint into the synthetic `rimsky.yml` under
the `stub` executor name, and drives the manifest's nodes through it.

Demo shell scripts under `examples/compose/` follow the same pattern.

## Related fixtures

- `lib/runtime/hostagent/testdata/stub-service/main.go` — HTTP-only
  port-binding fixture for the SpawnService primitive's unit tests
  (no protocol, no executor surface).
- `lib/services/test/stubexecutor/main.go` — image-shipping stub for
  the services-harness integration tests (always-success or
  always-error gated by env vars, not per-dispatch attributes).
- `test/support/executors/stub` — Go-callable test double for the
  in-process scenario harness (not a binary).
