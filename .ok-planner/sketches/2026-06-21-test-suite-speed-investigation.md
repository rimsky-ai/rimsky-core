# Test-suite speed — investigation sketch

**Date:** 2026-06-21
**Status:** Sketch (investigation only; no changes made, no authorization to build)

## Problem

The test suite is slow. We don't want to give up accuracy or invite flakes, but the wall-clock cost of running `make test-all` and `make test-race` (the release path) is high enough that it discourages frequent local runs and bloats CI. This sketch captures the findings of a read-only investigation and the opportunities ordered by leverage.

## Findings: where the time goes

### Testcontainers boot per-test, no pooling

The two big test homes both pay full container startup cost on every test.

- `code:test/support/scenario/harness.go::Start#81` is called by ~150 scenario tests. It calls `code:test/support/testpg/testpg.go::StartFreshPostgresDSN#24`, which runs `postgres:14-alpine` once per call and tears it down via `t.Cleanup`. There is no `TestMain`, no shared container, no template database.
- `code:lib/services/test/harness/rimsky.go::BringUpRimsky#220` boots a fresh `rimsky-all-in-one:latest` container per test, and per-test peer containers for sensors / stores / executors (`code:lib/services/test/harness/sensor_*.go`, `code:lib/services/test/harness/executor_*.go`, etc.).
- A sibling per-call factory exists in `code:lib/foundation/internal/pgtest/pgtest.go` for the foundation module.
- The only shared resource is a lazy Docker network (`code:lib/services/test/harness/rimsky.go#28` — `sharedNetworkOnceReapedByRyukAtProcessExit`).

A grep for `TestMain` over the whole tree finds **zero real `TestMain` functions**. There is no package-level setup amortization anywhere.

### CI runs cold

`file:.github/workflows/ci.yml` sets `cache: false` on `actions/setup-go@v5` in both the `go` and `lint` jobs. The Go build cache and module cache are not persisted across runs. The `ts-executor` job similarly has no npm cache (commented as "No npm cache because of the file: link").

The `go` job also runs `make build-all` and *then* `make test-all`. `go test ./...` does its own compilation, so the first step's output is thrown away.

There is no testcontainers image cache — `postgres:14-alpine`, `postgres:15-alpine`, and `rimsky-all-in-one:latest` are pulled fresh on every CI run.

### `-race -count=3` over packages that aren't all race-shaped

`file:Makefile#98-100` defines `test-race`:

```
go test -race -count=3 ./lib/runtime/... ./lib/graph/scheduler/...
cd lib/foundation && go test -race -count=3 ./persistence/postgres/... ./persistence/sqlite/...
```

`test-race` is required by the `release` target (`file:Makefile#302`). The persistence packages boot containers per test, so `-count=3` triples container boots there, and the race-detection rationale is thin for storage-shaped tests (the load-bearing race surface lives in the scheduler / runtime / queue layers, not in CRUD).

### `t.Parallel()` adoption is uneven

- `test/scenarios/...`: ~84% adoption (220/261 files). Good.
- `lib/services/test/scenarios`: ~86% (32/37 top-level). Good.
- `lib/runtime/...`: ~54% (30/56).
- `lib/control/...`: ~53% (27/51).
- `lib/graph/...`: ~32% (7/22).
- `lib/foundation/persistence/sqlite`: 1/17 — conspicuous.
- `lib/foundation/persistence/postgres`: 0/8 — conspicuous.

Sqlite tests use independent files, so parallel adoption is mechanical. Postgres tests already boot per-test containers, so isolation is already there.

### `-p 1` serializes services scenarios

`code:Makefile::test-integration` runs `RIMSKY_RUN_INTEGRATION_TESTS=1 go test -p 1 ./...`. The `-p 1` forces one test binary at a time across `lib/services`. Whether the constraint is load-bearing or historical is the question — within-package parallelism via `t.Parallel()` still works.

### Modules run sequentially, no CI sharding

`code:Makefile::test-all` walks the four Go modules with sequential `cd` statements: root → `lib/foundation` → `lib/protocols` → `lib/services` → `examples`. The CI `go` job runs the whole chain on one `ubuntu-latest` runner with no matrix.

## Opportunities, ordered by leverage

The first three compound on each other — pooling Postgres makes parallelization in the persistence packages safe, which makes narrowing `test-race` cheaper.

### 1. Postgres template-DB pool

Replace per-call `StartFreshPostgresDSN` with a `TestMain`-managed Postgres that:

1. Boots one `postgres:14-alpine` per package (or per test binary).
2. Runs migrations once into a template database (`rimsky_tmpl`).
3. For each test, executes `CREATE DATABASE rimsky_t_<rand> TEMPLATE rimsky_tmpl` — a sub-second clone.
4. Drops the per-test DB in `t.Cleanup`.

Same pattern applies to `code:lib/foundation/internal/pgtest/pgtest.go` and `code:lib/services/test/harness/postgres.go`.

Risk: none if each test still gets a fresh DB. Postgres template-DB cloning is atomic. Existing parallel scenarios already assume DB isolation.

Touch points: the three factories plus their `t.Cleanup` semantics. Callers don't change because the returned DSN signature is stable.

### 2. Rimsky-container reuse in services scenarios

Within a `lib/services/test/scenarios` package, reuse the all-in-one rimsky container via `TestMain`. Each test creates its own instance namespace (unique `instance_id` prefix or dedicated control-api token) rather than a fresh container.

Risk: harder than #1 because state lives across tests (`rimsky_node_runs`, etc.). Needs per-test namespace isolation that survives `t.Parallel()`. Worth doing after #1 lands.

### 3. `t.Parallel()` in `lib/foundation/persistence/sqlite` and `…/postgres`

Sqlite: mechanical add — each test has its own file. Postgres: safe once #1 lands (each test has its own cloned DB).

### 4. Narrow `test-race`

Keep `-race -count=3` on `./lib/runtime/...` and `./lib/graph/scheduler/...`. Drop the persistence packages from the `-count=3` slice — they still get `-race -count=1` coverage under `test-all`. The race-detection rationale lives in the scheduler / queue / runtime layers; the persistence layer's race surface is mostly contention against the underlying driver, not Go data races.

### 5. Restore CI Go build / module cache

`cache: false` is set on both `setup-go` invocations in `code:.github/workflows/ci.yml#37,75`. Investigation found no evidence the choice was deliberated:

- The workflow shipped with `cache: false` from its first commit (`77bad641`, 2026-06-02). There is no prior commit where caching was enabled and then turned off after a failure.
- Every other non-obvious choice in the file carries an inline rationale (the `go-version-file` pin, the `GOLANGCI_LINT_VERSION` v1 pin, the npm-cache-skip with its `file:`-link rationale, the `make build-all` over `go build ./...`). `cache: false` carries none.
- The `setup-go` cache covers `~/.cache/go-build` and `~/go/pkg/mod` only. It does not cache `rimsky:latest`, `rimsky-all-in-one:latest`, `rimsky-claim-producer-filesystem:latest` (those are built inline in the same job), and it does not cache `postgres:14-alpine` (testcontainers pulls it from Docker Hub at test time). A Docker-side image-staleness incident, if one occurred, cannot have been caused or fixed by toggling this flag.
- No filed issue mentions CI caching or Go-cache staleness.
- No Makefile target legitimately poisons the Go build cache: `make proto-gen` is not in the CI test chain, `make tidy` is not in `test-all`, and Go's cache is content-addressed (a stale compiled artifact cannot survive a source change).

The repo has five `go.sum` files (`./go.sum`, `./examples/go.sum`, `./lib/foundation/go.sum`, `./lib/protocols/go.sum`, `./lib/services/go.sum`). `actions/setup-go@v5`'s default cache key uses `**/go.sum`, which hashes all of them. The recommended change is to enable caching with an explicit `cache-dependency-path` so the multi-module key is unambiguous:

```yaml
- uses: actions/setup-go@v5
  with:
    go-version-file: go.mod
    cache: true
    cache-dependency-path: |
      go.sum
      lib/foundation/go.sum
      lib/protocols/go.sum
      lib/services/go.sum
      examples/go.sum
```

Risk surface is small: if a stale cache ever does cause a failure, the symptom is loud (compile error or wrong-binary behavior), the rollback is one workflow edit (`cache: false`), and Go's content-addressed cache makes the "stale binary running" failure mode genuinely rare. Apply the same change to the `lint` job.

### 6. Drop `make build-all` from the CI `go` job

`make test-all` recompiles everything via `go test`. Removing the prior `make build-all` removes a redundant full compile. (Or invert: keep `build-all` and have `test-all` use the cached binaries — less natural in Go.)

### 7. Prewarm testcontainers images in CI

Add a CI step to `docker pull postgres:14-alpine && docker pull postgres:15-alpine && docker pull rimsky-all-in-one:latest` before `make test-all`, ideally backed by a GitHub Actions layer cache. Eliminates fresh pulls inside testcontainers' first-test latency.

### 8. Shard the CI `go` job by Go module

Matrix the four modules (root / `lib/foundation` / `lib/protocols` / `lib/services`) as parallel jobs. Modules are independent under `go.work`; the race slice runs in whichever job owns the package.

### 9. Revisit `-p 1` in `test-integration`

If the constraint is testcontainers-name collision avoidance, raising `-p` works once unique names + ryuk handle cleanup. If it's host-resource bound (Docker daemon under load), `-p 2` may be the safe ceiling. Worth a focused experiment.

### 10. Layer-cache the inline Dockerfile builds in CI

The `go` job builds three Dockerfiles inline (`Dockerfile.rimsky`, `Dockerfile.all-in-one`, `Dockerfile.filesystem`) before `make test-all`. Switch to `docker/build-push-action` with `cache-from`/`cache-to` or a buildx registry cache so layer rebuilds aren't from scratch.

## Risks to avoid

- **Sharing containers across `t.Parallel()` tests without DB or namespace isolation.** This is the flake risk that buys speed at the cost of accuracy. The template-DB pattern in #1 keeps isolation while pooling.
- **Dropping `-race` from runtime / scheduler.** Load-bearing race detection — the original `-race -count=3` exists for a reason on those packages. Narrowing the scope (#4) is fine; removing it is not.
- **Skipping testcontainer-backed tests in CI.** They're the integration coverage. Caching their dependencies (#5, #7, #10) is the lever; skipping them is not.

## Suggested first slice

If this becomes a plan, #1 is the natural entry point: it's contained to three files (`code:test/support/testpg/testpg.go`, `code:lib/foundation/internal/pgtest/pgtest.go`, `code:lib/services/test/harness/postgres.go`), it's the highest single-lever item, and it unblocks #3 and #4. CI-side changes (#5, #6, #7, #8, #10) are independent and can ship in parallel.

## Out of scope

- Cutting test coverage. The goal is the same accuracy at less wall-clock, not less coverage.
- Changing the scenario test programming model (`scenario.Start` API). Pooling sits beneath the public surface.
- Replacing testcontainers-go with a different harness. Not necessary to capture the wins above.
- The TS executor (`lib/services/executors/claude-agent`). Vitest already runs single-process with in-process fakes; it isn't the bottleneck.
