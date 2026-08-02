---
audit: audit-artifact
artifact: story:audit-artifact
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# Operator inspects a completed one-shot run's durable record without re-running

Supported. `rimsky compose run` (and `rimsky run`) never delete the per-run
directory after the process exits, and the scenario test
`TestComposeRunOneShotTerminal_E2E` in `test/scenarios/` runs a real mixed
success/failure compose, then — after the process has exited — opens the
run's `state.db` with `database/sql` and asserts the failing instance's
terminal error event, its error-class payload, and its outcome attribute are
all readable from the artifact by hand, alongside instance and node-run
counts proving the successful instance is distinguishable too. A second test,
`TestComposeRunOneShotTerminal_QueryableWithStockSqlite3CLI`, re-opens the
same `state.db` with the stock `sqlite3` CLI (no rimsky-specific reader) and
confirms both `rimsky_instances` and the `rimsky_events` log are queryable.
Both tests are written explicitly as this story's falsifier.
