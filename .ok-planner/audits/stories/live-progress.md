---
audit: live-progress
artifact: story:live-progress
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:37Z
---

# Operator sees per-node lifecycle live during a one-shot run

Supported. Both run-to-terminal verbs (`rimsky compose run`, `rimsky run` self-host mode) drive a `ProgressPrinter` synchronously from `WaitForInstancesTerminal`'s polling loop, which emits an instance-starting line before waiting, a node-run-terminal line the moment each node settles, and an instance-terminal line when the instance goes idle — all to stderr as the run progresses, not batched at the end. `cmd/rimsky/cli/compose/wait_test.go`'s `TestWaitForInstancesTerminal_CallsPrinter` exercises this call sequence against a fake client with nodes settling across multiple poll ticks, confirming the printer sees per-node events as they happen rather than only a final rollup.
