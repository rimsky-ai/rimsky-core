# Nil-tx self-deadlock audit

**Goal:** Make the `rimsky/all` unified image work end-to-end on the SQLite driver. The quickstart should be able to use `driver: sqlite` and run a real cascade. (Today the quickstart is forced to postgres because SQLite hangs.)

**Problem:** The codebase has many call sites that open a `Persist.Transaction(...)` and inside the callback call another persistence method passing `nil` for its tx parameter. With the SQLite driver's `MaxOpenConns=1` (`foundation/persistence/sqlite/driver.go:30`), the inner call needs a fresh connection from the pool, the pool's only connection is held by the outer tx, the inner call blocks forever waiting, the outer tx can never commit. **Hard self-deadlock**, surfaces as either a hung HTTP request or a `SQLITE_BUSY` from a *different* process whose `BEGIN IMMEDIATE` waits 5 s for the writer slot the deadlocked process is holding.

The bug is masked on postgres because the default pool size is much larger — the inner call gets a different connection and succeeds.

**Two confirmed instances already fixed** (in the commits leading into this work):

1. `modeling/controlapi/lifecycle.go::FanOutTemplateEvent` + `FanOutInstanceEvent` — every lifecycle Get/Upsert/Delete inside the loop took `nil`. Fix added an explicit `tx persistence.Tx` parameter and threaded it through; deploy/undeploy/deregister callers pass their open tx; register/instance-create/terminate callers pass `nil` (correct — those run outside a tx).
2. `foundation/integration/runner_acquire.go` — `tryAcquire` and `insertHeldClaimHoldersAtAcquire` did `Nodes().Get(..., nil)`, `Instances().Get(..., nil)`, `Nodes().ListByInstance(..., nil)` from inside the open acquisition tx. Fix: pass `tx` through.

**Sites that the next iteration of the diagnostic surfaced but did NOT fix**, all of which appear to be self-deadlock-prone (called from inside a Persist.Transaction in their respective code paths — verify before fixing):

- `foundation/integration/runner_locks.go:136,140,144,154,216,220,259` — `loadInheritedClaimsForNode`, `loadDepsAttributes`, `lookupTemplate`. Called from `buildLockSpecs` which is called from `tryAcquire` (inside its tx).
- `foundation/integration/runner_terminal.go:187,216,309,579` — terminal-handler reads from inside the terminal tx.
- `foundation/integration/conductor.go:69,181` — conductor's stale-heartbeat sweep and ready-for-dispatch list. Need to check whether either runs inside a tx.
- `foundation/integration/runner.go:360,365` — `MergeDelta` is the design-intentional one (see "MaxOpenConns=1 is load-bearing" below). The `360` site (`Nodes.NodeAttributes().Get`) is probably the deadlock-prone one.
- `foundation/integration/orphan_reaper.go:92` — orphan reaper opens a tx; check what it calls inside.
- A pass through `modeling/scheduler/` and `modeling/frame/` is also warranted — the scheduler tick chain (`scheduler.go:256` → `frame.RunTick`) opens many transactions.

**Mechanical fix pattern.** For each call site:

1. Identify whether the function is called from inside a `Persist.Transaction(...)` callback (look up the call graph).
2. If yes: thread an explicit `tx persistence.Tx` parameter through the function and pass it to the inner persistence calls. Lifecycle has the established pattern — see `lifecycle.go::FanOutTemplateEvent` for the signature shape and the docstring comment explaining why.
3. If no (function only ever called from outside a tx): leave `nil`. Add a docstring comment noting the assumption so the next reader doesn't get nervous.
4. If sometimes inside, sometimes outside: parameterize. Callers must pass either the open tx or `nil`.

**Verification approach:**

1. Reproduce the hang on SQLite first. Use the quickstart with `persistence: driver: sqlite` (revert `quickstart/rimsky.yml`'s persistence block). Run register → deploy → instance-create. The known smoking gun: `instance create` succeeds but the dispatched node never leaves `stale` because the supervisor's `tryAcquire` self-deadlocks; the scheduler's next tick errors with `SQLITE_BUSY` because the writer slot is held.
2. Enable the diagnostic timing logs by setting the rimsky processes' slog level to Debug (or temporarily promote them to Info while iterating). The logs include per-step elapsed_ms in `handleDeployTemplate*` and `FanOutTemplateEvent`; add similar to any function you suspect.
3. After each batch of fixes: rebuild rimsky/all, recreate the SQLite-backed quickstart stack from a fresh volume, and rerun the cascade. The end-to-end success criterion: both nodes in `quickstart/example-template.yml` reach `fresh`; the instance terminates; no `SQLITE_BUSY` warnings in the supervisor or scheduler log.
4. Switch `quickstart/rimsky.yml` to `driver: sqlite` and update the README + compose-file comment to drop the postgres-vs-sqlite explanation.

**Tests to add along the way:**

- Each fix site should grow a scenario test that exercises the path under SQLite. The existing scenarios in `test/scenarios/` mostly drive postgres via testcontainers; add a SQLite variant of the basic happy-path and held-claim cascades to lock the no-deadlock guarantee in.
- A unit test that wraps the persistence layer with a `MaxOpenConns=1` SQLite pool and asserts that calling each public persistence method from inside a `Persist.Transaction` succeeds. That's the structural guard against new instances of this bug.

**Related but separate concerns** (do NOT roll into this audit; capture as separate work):

- **`_txlock=immediate` blanket policy** (`foundation/persistence/sqlite/driver.go:102`). The driver sets every transaction to `BEGIN IMMEDIATE`, which takes the SQLite writer slot even for read-only operations. The reader-concurrency benefit of WAL is wasted in practice. The reason cited (`coordinator's no-op named/scope locks`) suggests a specific load-bearing path that needs `BEGIN IMMEDIATE`; the rest could be `BEGIN DEFERRED`. Worth revisiting once the deadlock audit lands and we have a known-good baseline. Likely needs driver-level work: thread `sql.TxOptions{ReadOnly: true}` through `Persist.Transaction(...)` and use that to pick BEGIN mode.
- **`MaxOpenConns=1` is load-bearing** (`foundation/persistence/sqlite/driver.go:30`). The driver's design uses pool size 1 as an implicit serialization primitive for callers that pass `nil` for tx (specifically `node_attributes.MergeDelta` per the comment, and likely others). The structural fix is option (C) from the diagnostic conversation: make `tx` a required parameter on every persistence method and remove the `nil`-tx code path entirely. That eliminates the bug class but is a much larger refactor than the audit. Pre-v1 break-freely makes it tractable; this audit deliberately stays scoped to the surface-level fixes.

**Estimating scope is forbidden** per project rules; the audit is bounded by the file/line list above plus whatever the call-graph traversal surfaces. A handler with one fixer subagent + one reviewer subagent following the lifecycle-and-runner-acquire-fix pattern should be able to drive it to clean.

**Reference commits** (for fresh-context grounding):

- `5ce6ea2 docs: build public-documentation surface + lint suite`
- `2ac2f04 quickstart: one-command Rimsky stack with bundled stub executor`
- `5195f6d quickstart: postgres backend, store-stub config, distroless volume perms`
- The two confirmed-fix commits land alongside this doc.

**Reference lines for the fix pattern:**

- Signature change: `modeling/controlapi/lifecycle.go::FanOutTemplateEvent` (added `tx persistence.Tx` parameter, docstring explains why).
- Inside-tx caller: `modeling/controlapi/templates.go::handleDeployTemplateState` (passes `tx` from the enclosing `Persist.Transaction` callback).
- Outside-tx caller: `modeling/controlapi/templates.go::handleDeployTemplate` (the register handler — passes `nil` because it runs the fan-out before the tx opens).
- Threading through helpers: `foundation/integration/runner_acquire.go::tryAcquire` (the tx parameter is already on the function signature; the fix was just changing `nil` → `tx` at the call sites).
