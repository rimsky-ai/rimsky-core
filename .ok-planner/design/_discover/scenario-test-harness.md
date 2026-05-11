---
topic: scenario-test-harness
kind: discipline
---

# Scenario tests drive the supervisor through `modeling/scenario.Start` against pre-launched producer peers on ephemeral ports

## Description

Rimsky's blessed invariants are mostly load-bearing safety properties (no double-execute, claimant-guarded release, verify-before-run, atomic acquisition). Each has a regression test that exercises it. The tests live in `test/scenarios/` and use a harness in `modeling/scenario/` to drive the supervisor through realistic flows.

`modeling/scenario/harness.go` is the entry point. CLAUDE.md "Blessed invariants" guidance: "When adding new invariant coverage, drive the supervisor through `modeling/scenario.Start` against pre-launched producer-services on ephemeral ports (the smoke fixture in `test/smoke/setup.go` is the reference example)."

The harness's job:

1. Spin up a real Postgres via testcontainers (`foundation/internal/pgtest/` + `modeling/internal/pgtest/` — both pgx-allowed).
2. Run the migrate step against it.
3. Launch the producer peer-services (filesystem, postgres, stub) on ephemeral gRPC ports.
4. Launch the executor peer-services on ephemeral ports.
5. Start a supervisor, scheduler, and control-api wired to the test Postgres.
6. Return a handle the test can use to drive operations (create templates, instances, force-fire schedules, observe events).

The fixture is reused across `test/scenarios/`, `test/smoke/`, and `cmd/rimsky-conformance` (for conformance runs). Each scenario boots its own Postgres container; tests are not unit-test fast.

`test/scenarios/locks/` is the regression backstop for the locking invariants (claimant-guarded release, scope conflict, named-lock atomicity, deterministic ordering). `test/scenarios/stores/`, `test/scenarios/claim_stores/` exercise producer-side scenarios. `test/scenarios/frame_resolution/` covers frame semantics. `test/scenarios/lifecycle/` covers the LifecycleSubscriber protocol.

CLAUDE.md "Build & test" notes:

```sh
go test ./test/scenarios/ -run TestVerifyBeforeRunRace -v
go test ./test/scenarios/... -count=5 -race      # flake hunt
```

The `-count=5 -race` invocation is the canonical flake-hunt for invariant tests; race-sensitive paths (queue, supervisor, scheduler) merit it.

Per `.claude/rules/rules.md` "Verify the build":

> Race-sensitive paths (queue, supervisor, scheduler): add `-race`, e.g. `go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3`.

(The path names refer to the pre-Phase-5 directories; in the current structure these are `foundation/persistence/postgres/queue.go`, `foundation/integration/`, `modeling/scheduler/`.)

The scenario harness keeps a clean separation: scenario tests use the public modeling-layer API (`scenario.Start` + control-api HTTP calls) and the public peer-protocols (gRPC). They do not reach into foundation internals; that's enforced by depguard (`foundation-internal-isolation`).

The smoke fixture (`test/smoke/setup.go`) is the reference example: it bootstraps a complete deployment and drives 100 sequential force-fires through `/admin/scheduled-nodes/{node_id}/force-fire` (CLAUDE.md "Non-obvious gotchas"). It's the closest test to "what an operator's CI would do."

## Code surface

- `modeling/scenario/harness.go` — entry point.
- `modeling/scenario/harness_util.go` — utility helpers.
- `modeling/scenario/harness_test.go` — self-tests.
- `test/scenarios/locks/` — locking-invariant tests.
- `test/scenarios/stores/`, `test/scenarios/claim_stores/`, `test/scenarios/frame_resolution/`, `test/scenarios/lifecycle/`.
- `test/smoke/setup.go` — smoke fixture.
- `foundation/internal/pgtest/` — pgx-allowed test fixture.
- `modeling/internal/pgtest/` — pgx-allowed test fixture.

## Prose surface

- `CLAUDE.md` "Build & test" — scenario test commands.
- `CLAUDE.md` "Blessed invariants" — scenario tests as regression backstops.
- `.claude/rules/rules.md` "Verify the build" — race-sensitive path discipline.

## Adjacent topics

- `2026-05-10-conformance-test-binaries` — different but related: conformance binaries are standalone whereas scenarios are Go tests.
- `2026-05-10-depguard-enforced-package-boundaries` — scenario tests use the modeling-layer API; pgtest is the only foundation-internal allowance.
- `2026-05-10-postgres-only-runtime-state` — scenarios spin up real Postgres because that's what production uses.

## Observations

- Scenario tests are slow (each boots a Postgres container) but cover real-world race conditions that unit tests can't. The `-race -count=N` flake hunt is the canonical way to verify a new invariant test isn't flaky.
- The pgtest fixtures (`foundation/internal/pgtest/`, `modeling/internal/pgtest/`) are the only foundation-internal-allowed surfaces for testing; everything else routes through the public `persistence.Driver` interface.
- The smoke fixture's 100 sequential force-fires exercise the cron-no-backfill rule's complement: it forces fires deterministically to verify the scheduler-tick path with high pressure.
- `modeling/scenario` is at modeling layer, not test layer; it's importable code (with a `_test.go` self-test) so scenario tests in `test/scenarios/` can import it. Its location in `modeling/` (not `test/`) is what makes it usable by both internal scenario tests and external tooling.
