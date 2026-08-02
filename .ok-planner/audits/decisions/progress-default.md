---
audit: progress-default
artifact: decision:progress-default
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:37Z
---

# Default progress printer emits per-node lifecycle to stderr

Supported. `cmd/rimsky/cli/compose/progress.go`'s default printer (selected by `newProgressPrinter` when neither `--quiet`, `--verbose`, nor `--json` is set) implements exactly `InstanceStarting`, `NodeRunTerminal`, and `InstanceTerminal` as non-empty line emitters (`FrameTick` is a no-op), writing through a `bufio.Writer` that is flushed after every single line, satisfying "line-buffered." Both call sites (`cmd/rimsky/cli/compose/run.go`, `cmd/rimsky/cli/compose/template_run.go`) construct the printer against `os.Stderr`, and the printer is driven synchronously from the polling loop in `WaitForInstancesTerminal`, so lines are chronologically ordered as events settle. `progress_test.go`'s `TestDefaultPrinter_LineFlushed` and `TestLinePrinter_ProseSingleSource` exercise the flush and formatting behavior directly.
