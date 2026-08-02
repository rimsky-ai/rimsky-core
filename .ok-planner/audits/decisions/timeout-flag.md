---
audit: timeout-flag
artifact: decision:timeout-flag
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:37Z
---

# Wall-clock --timeout flag defaults to unbounded

Supported. The full population of run-to-terminal verbs — the two CLI flows that register a `--timeout` `flag.DurationVar` (`rimsky run`, shared by both remote and self-host modes in `cmd/rimsky/cli/run.go`, and `rimsky compose run` in `cmd/rimsky/cli/compose/run.go`) — both default the flag to `0`, documented inline as "unbounded"/"0 = unbounded," with no other code path substituting a non-zero default. Both verbs' terminal-wait logic (`waitAndCleanup` in `run.go`, `waitOneShotToTerminal` in `compose/run.go`) treats `timeout <= 0` as "wait forever" (no timer armed), matching "absence means as long as it takes."
