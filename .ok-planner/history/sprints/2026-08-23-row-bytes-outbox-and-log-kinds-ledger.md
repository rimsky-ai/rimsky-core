# Ledger: 2026-08-23-row-bytes-outbox-and-log-kinds

Stages reviewed: 1–7. Fix-only rounds: 1 of 3 used; the round closed every line. Code complete. The certification gate then ran six fixer batches to a clean exit; its record is the completion report's `## Certification ledger`. Builders 1–3 retired after Stages 1, 2, 3. Builder 4 retired after the Stage 4 fix round (392k tokens). Builder 5 retired after the Stage 5 fix round (429k tokens). Builder 6 retired after the Stage 6 fix round (451k tokens). Reviewer 1 retired after Stage 3. Reviewer 2 retired after Stage 6 (430k tokens). Reviewer 3 retired after fix-only round 1 with an empty ledger. Builders 1–8 retired.

## Open findings

(none — code complete)

## Closed findings

- S1-1 … S1-7, S2-1 … S2-4, S3-1 … S3-3, S4-1 … S4-5, S5-1 … S5-7, S6-1 … S6-2, S7-1 … S7-4 — closed in their fix rounds.

## Open claimed forks

(none — F1, F2, F3 are in the report's `## Divergences`)

## Reviewer observations (not findings)

- `DeliverStagedLifecycleRow` performs the subscriber RPC inside the transaction that holds the per-scope advisory lock. The shape predates the sprint.
- `stageInstanceTerminatedInTx` and `StageRunScopeTerminal` fail when the template row is missing. `handleDeleteTemplate` makes that unreachable. Nothing records the dependency.
- `decision:mcp-http-parity` promises route parity. The parity test checks per action, so `producer-outbox` (pre-existing) and the new `lifecycle-outbox` route are unreachable over MCP. No sprint item asks for it.
- `runtime.FlushProducerVerbOutbox` hardcodes `DefaultServiceDeliveryStallAfter`. Every caller is a test.
- The lifecycle drain reads its pending summary in one transaction and marks or clears in another. Two roles can race to one spurious `stalled`/`recovered` pair when a service's last row drains at that instant. It resolves on the next pass.
- The proto rename changes the gRPC full method name to `/rimsky.v1.HostDaemon/Connect`. Both ends ship in this tree. No divergence names it as the sprint's largest outward break.
- `docs/env-vars.md` carried two stale rows forward mechanically (S5-2, S5-3). A pass over its "Default" column against the code catches any other.
- `.ok-plumbline/config.json` declares no `tests` array, so `/events` classes `test/support/composestub/main.go`, `lib/services/test/stubexecutor/main.go`, and `test/support/claim_producers/stub/server/server.go` as product paths. Predates the sprint.
- D48's subsystem set has 62 first segments. `CONDUCTOR` names a noun the concept catalog lacks. `PROCESS.ROLE.FAILED` and `ENTRYPOINT.ROLE.FAILED` are one event seen from two places. Vocabulary questions for a later `/audit`.
- `loggerNames` in the log-kind scan is a tree-wide set of identifier names, not types. The sensors' `s.logger.Warn(...)` sites are read as logger calls only because other packages declare a `logger shared.Logger`. Rename that field and the sites leave the scan without a violation.
- `cmd/rimsky/cli/compose/shutdown_test.go:269` — the `awaited.Until` labelled "the child to survive the drain's SIGTERM" returns on the first poll. The verdict rests on the post-`Drain` check. Only the wait's stated reason is false.
- `lib/protocols/conformance/executor/await_terminal_test.go:314` — `cancel()` may land before `AwaitTerminal` starts awaiting. The verdict is deterministic either way.
- `lib/services/executors/claude-agent/agentrun.go:325,703,713` — three real-clock reads stay in product code outside the timeout loop. No test verdict reads them.
