---
issue: stale-recovery-disposition-sync-path-unrealized
kind: audit
category: decision-drift
artifacts:
  - decision:prior-stale-recovery-rename
status: repaired
opened: 2026-08-02T09:58:09Z
---

# Does `stale_recovery` cover sync RPC-broken dispatches, or only the async quiet-period/max-runtime sweep?

Only the async sweep (`SweepExecutorDeadlines`) ever stamps `stale_recovery`; every sync-dispatch error path (dial failure, resolve failure, cancellation) stamps `retry_after_error` instead, and always has — `concept:node-run` already documented this async-only reality accurately. `decision:prior-stale-recovery-rename`'s Choice and the `PRIOR_STALE_RECOVERY` proto comment were the stale artifacts, both claiming a sync-RPC-broken half of the mechanism that was never realized in code.

Rule that determined the fix: outcome-2 corpus-side repair — the code and the counterpart artifact (`concept:node-run`) already agreed on the commitment; only the decision's prose and the proto comment had the polarity wrong, with no behavior or wire-shape change involved.

Changed: `.ok-planner/design/decisions/prior-stale-recovery-rename.md` (Choice + Rationale narrowed to the async sweep only, cross-referencing `concept:node-run`) and the `PRIOR_STALE_RECOVERY` comment in `lib/protocols/proto/v1/executor.proto` (regenerated via `make proto-gen`).

Verified: `go build ./...`, `go test ./lib/protocols/... ./lib/runtime/...` all pass.
