# Pass 4 / P3a — scenario test audit

Working notes for the repo-reorganization plan
(`.ok-planner/plans/2026-05-24-repo-reorganization.md`, Tasks 21–24).

Generated from:

```
grep -rln 'fallguyconsulting/rimsky/stores' --include="*.go" . | grep -v '^./stores/'
ls test/scenarios/stores/*.go
ls test/scenarios/atomic_staging/*.go
ls test/smoke/*.go
```

Plus per-file content inspection (file naming is a heuristic; file
content is the truth, per Task 23).

---

## Category 1 — REWRITE-TO-STUB

Tests that exercise rimsky's generic behaviour (cascade, scheduler,
supervisor, lifecycle, runtime, locks) and only incidentally need a
backing claim-producer store. The stub-store is sufficient.

**Outcome of Task 22: no rewrites required.** Every test in this
category was already authored against `pkg:stores/stub` from the
start — no in-tree generic test imports `pkg:stores/filesystem` or
`pkg:stores/postgres`.

The full set (every file uses
`pkg:stores/stub/{testfixture,store,dataprocessing}` only — no
filesystem/postgres imports):

- `test/scenarios/acquire_pass_invalidate_emit_test.go`
- `test/scenarios/acquire_unavailable_error_routing_test.go`
- `test/scenarios/acquire_unavailable_error_types_test.go`
- `test/scenarios/acquire_unavailable_pass_test.go`
- `test/scenarios/acquire_unavailable_retry_default_test.go`
- `test/scenarios/asset/durable_lifetime_across_run_completion_test.go`
- `test/scenarios/asset/staging_then_swap_with_co_holders_test.go`
- `test/scenarios/attribute_overrides_match_overlay_fanout_e2e_test.go`
- `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go`
- `test/scenarios/fanout_callback_determinism_e2e_test.go`
- `test/scenarios/fanout_child_error_retry_e2e_test.go`
- `test/scenarios/fanout_strict_cascade_e2e_test.go`
- `test/scenarios/fanout_success_cascade_e2e_test.go`
- `test/scenarios/held_claim_acquirer_blocked_pass_test.go`
- `test/scenarios/held_claim_acquirer_passes_test.go`
- `test/scenarios/held_claim_mixed_upstream_test.go`
- `test/scenarios/lifecycle/lifecycle_e2e_test.go`
- `test/scenarios/locks/atomic_acquisition_test.go`
- `test/scenarios/locks/claim_scope_conflict_race_test.go`
- `test/scenarios/locks/node_run_phase_test.go`
- `test/scenarios/parked_lifecycle_test.go`
- `test/scenarios/run_tree/candidate_handle_threaded_test.go`
- `test/scenarios/stores/scope_claim_test.go`
- `test/scenarios/stores/scope_envelope_test.go`

`test/scenarios/stores/open_rollback_test.go` and
`test/scenarios/stores/placeholder_test.go` carry no stores import at
all (the former is a `t.Skip` doc-pointer; the latter is a package
banner). They stay in rimsky as generic-rimsky-behaviour scaffolding,
no rewrite required.

---

## Category 2 — MOVE-TO-RIMSKY-SERVICES

Store-specific tests whose subject-matter is the filesystem or
postgres bundled-store implementation itself. These move with the
production stores in Pass 5 (Task 29).

Each entry is content-confirmed (Task 23) rather than relying on
filename alone.

### Pass-4 parking treatment

Files in this category that depend on the production-store packages
(or on the production-shape `Config` types the testfixture used to
expose) are parked behind a `//go:build rimskyservices` build tag.
The tag excludes them from the default `go build` and `go test`
runs, satisfying `make test-all` while keeping the source in place
for Pass 5 to relocate. Pass 5 strips the tag when each file lands
in `../rimsky-services`.

Files left **un-tagged** still compile against the in-tree pg-store
and continue to run pre-Pass-5; Pass 5 moves them with the rest of
the set.

### Filesystem-specific

- **`test/scenarios/stores/fs_cross_queue_concurrency_test.go`** —
  imports `pkg:stores/filesystem/{testfixture,store}` and
  `pkg:stores/common/action`. Drives the filesystem store's pick
  policy across two concurrent queues; pins fs-specific FIFO + cross-
  queue isolation behaviour. **Tagged `rimskyservices` in Pass 4.**
- **`test/scenarios/stores/fs_pick_policy_basic_test.go`** — fs-only
  pick-policy basics (pop / pop-and-move / pop-and-delete / recycle
  semantics against on-disk folders). **Tagged in Pass 4.**
- **`test/scenarios/stores/fs_pick_vs_scope_concurrency_test.go`** —
  exercises the filesystem store's interplay between pick-policy
  queues and scope-claim contention; both surfaces are fs-specific.
  **Tagged in Pass 4.**

### Postgres-specific

- **`test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go`**
  — imports `pkg:stores/postgres/testfixture`. Drives the fused
  postgres-store verifier role through atomic-staging commit-then-
  abandon transitions against a real Postgres testcontainer.
  **Tagged in Pass 4** (stub-store has no executor surface).
- **`test/scenarios/atomic_staging/pg_verifier_conformance_test.go`**
  — imports `pkg:stores/postgres/testfixture`. Conformance-style
  assertions on the postgres-store verifier wire shape. **Tagged in
  Pass 4.**
- **`test/scenarios/atomic_staging/pg_verifier_test.go`** — pure
  wire-shape test on `genv1.ExecuteEvent` / `StreamClose` envelopes;
  no stores import. Compiles fine post-Pass-4. **Un-tagged**, moves
  with its siblings in Pass 5 for cohesion.
- **`test/scenarios/bundled_executor_vocab_test.go`** — imports
  `pkg:stores/postgres/{server,store}` plus
  `pkg:executors/{http-node,verifier-http,verifier-shape-checks}/errorclasses`.
  Asserts hierarchical error-class vocabularies for the bundled
  postgres-store executor role and the three bundled http /
  verifier executors. Every subject in this file is a production-
  side bundled deliverable that moves to rimsky-services. **Un-
  tagged in Pass 4** — it doesn't touch the testfixture and still
  compiles against the in-tree pg-store packages; Pass 5 moves it.

### Smoke fixture (entire `test/smoke/` directory)

`test/smoke/setup.go` is the shared `BringUpStack` harness. It wires
loopback filesystem + postgres store-services with concrete
`fsfixture.Config{Root: ...}` and `pgsfixture.Config{...PickPolicies...}`
configuration. Every test in the package depends on the harness.

- **`test/smoke/setup.go`** — fs + pg fixture wiring; testcontainers
  Postgres bring-up; pg pick-policy and items-table provisioning.
  Store-specific by construction. **Tagged in Pass 4.**
- **`test/smoke/data_platform_smoke_test.go`** — imports
  `pkg:stores/stub/{server,dataprocessing,store}` for the stub-side
  data-platform extension; also leans on `BringUpStack`. **Tagged
  in Pass 4** (cannot compile without `setup.go`'s symbols).
- **`test/smoke/stores_redesign_smoke_test.go`** — drives the
  fs+pg-backed §11.5 four-node template through 100 invalidations.
  **Tagged in Pass 4.**
- **`test/smoke/auth_smoke_test.go`** — exercises the auth-key
  lifecycle against the live `BringUpStack`. Generic in subject but
  bound to the fs+pg harness; **Tagged in Pass 4** so the package
  compiles consistently.
- **`test/smoke/observability_smoke_test.go`** — observability
  endpoints against `BringUpStack`. **Tagged in Pass 4** for the
  same reason.

Note for Pass 5: the smoke directory could be split (auth /
observability are not store-specific in subject and could be
recreated against a stub-only harness in rimsky), but the cheapest
move is to relocate the whole `test/smoke/` package alongside the
production stores in rimsky-services.

---

## Category 3 — MOVE-TO-RIMSKY-DOCS

Handled by Pass 7 (Task 39), not Pass 4. Listed here so the move-set
is in one place.

The four atomic-staging example tests all import
`pkg:github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store`:

- `test/scenarios/atomic_staging/abandon_on_any_failure_test.go`
- `test/scenarios/atomic_staging/commit_on_all_success_test.go`
- `test/scenarios/atomic_staging/concurrent_staging_test.go`
- `test/scenarios/atomic_staging/sub_stage_verifier_failure_test.go`

These accompany `examples/atomic-staging-fs-producer/` to
`../rimsky-docs/examples/atomic-staging-fs-producer/scenarios/`.

---

## Out-of-scope / stays in rimsky unchanged

The two conformance-binary `main_test.go` files import
`pkg:stores/stub/{store,testfixture}`. The stub-store stays in
rimsky as test infrastructure, so these tests need no changes:

- `cmd/rimsky-claim-producer-conformance/main_test.go`
- `cmd/rimsky-data-processing-conformance/main_test.go`

---

## Category counts

| Category | Count |
| --- | --- |
| REWRITE-TO-STUB (already uses stub — no rewrite needed) | 24 |
| REWRITE-TO-STUB (no stores import; stay as-is) | 2 |
| MOVE-TO-RIMSKY-SERVICES (tagged `rimskyservices` in Pass 4) | 9 (3 fs-pick + 2 pg-verifier + 4 smoke + setup.go × 1; smoke total 5) |
| MOVE-TO-RIMSKY-SERVICES (un-tagged; still compiles) | 2 (`pg_verifier_test.go`, `bundled_executor_vocab_test.go`) |
| MOVE-TO-RIMSKY-DOCS | 4 |
| Out-of-scope (rimsky stub-conformance tests) | 2 |

Total files surveyed: 43 (matches the union of the inventory
queries above).

---

## Pass-5 hand-off notes

For each MOVE-TO-RIMSKY-SERVICES file, Pass 5 should:

1. Remove the `//go:build rimskyservices` line and its accompanying
   "parked" comment block.
2. Update the file's `github.com/rimsky-ai/rimsky-core/...`
   imports to `github.com/rimsky-ai/rimsky-services/...`
   where the imported package moves with the production stores.
3. Move the file to the corresponding path under
   `../rimsky-services/test/...`.

The two un-tagged MOVE-TO-RIMSKY-SERVICES files
(`pg_verifier_test.go`, `bundled_executor_vocab_test.go`) just need
steps 2 and 3 — no tag to strip.

The two testfixture packages (`stores/filesystem/testfixture`,
`stores/postgres/testfixture`) stay in rimsky and are now
implemented as thin wrappers around `pkg:stores/stub`. The Config
shapes are intentionally preserved (a local `PickPolicy` struct on
each fixture) so that whatever testfixture rimsky-services chooses
to ship can drop in with the same signature.
