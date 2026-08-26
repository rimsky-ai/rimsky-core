# Shared test stacks and a run-labeled sweep — Design Sketch

**Date:** 2026-08-25
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

A full `make test-all` boots about 90 `postgres:15-alpine` containers, a `rimsky-all-in-one` and a stub executor per services test, and one anonymous data volume per postgres container. A run that dies mid-flight leaves its containers and volumes on the daemon, because ryuk (testcontainers' reaper) is best-effort. This sketch proposes three changes. The services suite boots one stack per test-package process and provisions each test's templates and instances in situ. The core suite boots one postgres per `make` run instead of one per package. Every docker resource a run creates carries a label unique to that run, and the run sweeps by that label on every exit path, so nothing survives the run.

## Shape

### Why the count is what it is

Rimsky's executor, claim-producer, and publisher set is boot-time config: `executors:`, `claim_producers:`, `publishers:` in `rimsky.yml`. The control API exposes them read-only (`GET /executors`, `GET /claim-producers` in `lib/control/observability/handler.go`) and nothing reloads them. The services harness therefore renders a fresh `rimsky.yml` per test (`lib/services/test/harness/rimsky.go::renderRimskyYAML`) and boots a postgres (`startPostgresOnNetwork`), a rimsky (`runRimskyContainer`), and a stub executor for it. 42 `BringUpRimsky` call sites and 26 `StartFreshPostgres` call sites each boot a postgres. The core suite already has the right shape: `lib/foundation/pgpool/pgpool.go::Pool` boots one container per process under `sync.Once` and hands each test a database cloned from a migrated template. 24 packages reach it, so `make test-root` still boots 24 containers. Nothing calls `Pool.Close`.

### Part 1 — one stack per services test-package process

A package-level stack, booted once per process under `sync.Once`, the same pattern as `pgpool.Pool`:

```
package process
  └─ stack (sync.Once)
       ├─ network            (one per process; today: sharedNetworkName)
       ├─ postgres           (one per process; pgpool.Pool, template + per-test clone)
       ├─ stub executor(s)   (one per distinct behavior the package's tests need)
       └─ rimsky-all-in-one  (one per process; executors: = union of the package's names)
  └─ test A: DeployTemplate(name-A) → CreateInstance → … → t.Cleanup: DELETE /instances, DELETE /templates
  └─ test B: DeployTemplate(name-B) → …
```

- The harness gains `StackForPackage(t)` returning the shared `RimskyEndpoint`. `BringUpRimsky` stays as the name for a private stack and is called only by tests that need one.
- The union of service names is declared once per package, in a `TestMain` or a package-level `var stack = harness.NewPackageStack(harness.WithExecutor(...), ...)`. Each `WithExecutor(name, endpoint)` in the package moves into that declaration. A test that today declares `WithExecutor("stub", ep)` reads the name from the package stack instead.
- Each test isolates by identity instead of by container: it uses a per-test template name and instance key, and calls `DELETE /templates/{id}` and `DELETE /instances/{idOrKey}` in `t.Cleanup`. The routes exist (`lib/control/controlapi/templates.go`, `instances.go`, `tags.go`).
- Postgres under the shared rimsky is one database, not one per test. Per-test clones do not help here because the rimsky container holds one DSN. Tests isolate by template and instance identity.
- Tests that need a distinct boot posture keep a private stack and name the reason: `WithServiceAuthMTLS` (1 site), `WithContainerEnv` (4 sites), the `RimskyHandle.Restart` users, and the split-role stacks in `rimsky_split.go`.
- Estimated boots per `make test-services`: from ~68 postgres + ~68 rimsky + ~68 stub to one of each per package (14 packages import the harness) plus the private stacks above.

### Part 2 — one postgres per `make` run for the core suite

`pgpool.Pool.boot` honors `RIMSKY_TEST_PG_DSN`. When set, the pool skips the container boot, connects to the DSN as admin, creates its template database under a process-unique name, and clones from it as today. `make test-root` / `test-foundation` / `test-all` boot one `postgres:15-alpine` (through the same labeled helper as Part 3), export the DSN, run the packages, and remove the container in the sweep. With the variable unset the pool boots its own container, so plain `go test ./test/scenarios/...` still works. 24 boots become 1 under `make`.

The template name must be process-unique (`rimsky_tmpl_<pid>` or a random suffix) because 24 processes now share one server. The clone name already is (`nextCloneName`).

### Part 3 — run-scoped label and an owned sweep

```
label: org.rimsky.test-run=$RIMSKY_IMAGE_TAG        (the tag make test-all already mints)

creation sites → one helper (test/support/dockerlabel or similar)
  lib/foundation/pgpool/pgpool.go::runPostgresWithRetry
  lib/services/test/harness/boot_retry.go             (the harness's testcontainers.Run wrapper)
  lib/services/test/harness/rimsky.go::sharedNetworkName
  cmd/rimsky/cli/ctx_demo_test.go                     (direct testcontainers.Run today)

fitness test (test/plumbline/): greps for testcontainers.Run(, GenericContainer(, network.New(
  → fails on any call outside the helper

sweep (tools/gotestguard/main.go), on every exit path — pass, fail, inconclusive kill, own SIGINT:
  docker ps -aq     --filter label=org.rimsky.test-run=$TAG | xargs docker rm -fv
  docker network ls -q --filter label=org.rimsky.test-run=$TAG | xargs docker network rm
  docker volume ls -q  --filter label=org.rimsky.test-run=$TAG | xargs docker volume rm

Makefile: test-* targets run the same sweep as their last recipe line under trap
make reap-runs: removes every org.rimsky.test-run resource older than REAP_HOURS
                (sibling of reap-images; the path for a run whose guard was itself killed)
```

- The label's value is the tag each run already mints (`.ok-workspaces/bin/run-tag`). A concurrent workspace's run holds a different value, so the sweep cannot reach it.
- `-v` on `docker rm` removes the anonymous `/var/lib/postgresql/data` volume the postgres image declares; that volume is what accumulates today (984 dangling volumes, 28 GB).
- Ryuk stays on. It reaps the common case fast. The sweep is the guarantee.
- The guard's inconclusive-kill path today sends `SIGQUIT` then `SIGKILL` to the process group and exits; the sweep runs after the kill, before the exit code is returned.
- The fitness test is the mechanical check the plumbline cheatsheet asks for. A creation site that bypasses the helper fails the build, so an unlabeled resource cannot exist.

### Effect of the three parts

| | today | after |
| --- | --- | --- |
| postgres boots per `make test-all` | ~90 | ~15 (1 core + 14 services packages) |
| rimsky-all-in-one boots | ~68 | ~14 + private stacks |
| resources after a killed run | whatever ryuk missed | none (sweep) or none after `reap-runs` |
| anonymous volumes after any run | one per postgres container ryuk removed without `-v` | none |

## Open questions

- **Stub executor behavior per test.** `test/support/scenario/harness_util.go::StartStubExecutorWithSchema` and the services harness's `executor_stub.go` configure a stub's schema and mode at container start. A shared stack needs either one stub container per distinct configuration in the package, or a stub that selects behavior per request (by node type or node config). Which is smaller depends on how many distinct configurations the 14 packages use. This sketch does not count them.
- **`decision:testcontainers-go` says the test process owns container lifecycle.** Part 2's make-level postgres is close to that decision's rejected alternative ("externally provisioned databases"). The pool still owns isolation (template + clone) and still boots its own container under plain `go test`; only the `make` path shares. A sprint should decide whether that fits the decision or amends it.
- **`decision:parallel-cap-removal` justifies `-p 2` by concurrent stack boots.** With one stack per package the contention it names shrinks. Whether the cap stays at 2 or rises is a measurement question after Part 1 lands.
- **Postgres isolation inside the shared rimsky.** Tests that inspect rows directly (`pgdbtest.QueryForTest` users) must scope every query by instance or template identity once they share a database. This sketch does not check whether any test asserts on a table-wide count.
- **Where the sweep runs when the guard is not the entry point.** `make test-in-stack` and `make smoke-all` go through the guard; `test-docker` runs `go test` inside a container with a socket. That path needs its own sweep, or the target goes away.
- **Label on volumes.** testcontainers labels containers and networks it creates; anonymous volumes declared by an image carry only `com.docker.volume.anonymous`. `docker rm -v` on a labeled container removes them; a volume that outlives its container (daemon restart mid-run) may need the sweep to fall back to `docker volume prune --filter label=…` on volumes testcontainers created explicitly, and to a dangling-volume prune for the anonymous class.

## Risks / unknowns

- Isolation changes from a fresh stack per test to unique names plus cleanup. A test that leaks an instance (a `t.Fatal` before the cleanup registration) now affects its neighbors in the same process. The core scenario suites already work this way. The services suites do not yet.
- The union `executors:` block means a services test can no longer prove "rimsky rejects a node whose executor is unregistered" against the shared stack; those tests need a private stack or a name the package stack deliberately omits.
- To sweep on its own `SIGINT`, the guard must catch the signal, forward it, wait for the group to exit, then sweep. It must also cope when the docker daemon is what hung: the sweep must never block the exit indefinitely, so each `docker` call is bounded and prints its failure.
- Three canary.test processes (`test/scenarios/canary`, pids 78326, 78717, 80059 at the time of writing) have run for 3.5 days orphaned to launchd. They came from a bare `go test -run TestCanary…`, not through the guard, so nothing in this sketch kills them. They show that a process-level orphan is a separate class from a docker orphan.

## What this is not

- Not a change to rimsky's product surface: executors stay boot-time config. A runtime `POST /executors` would remove the need for per-package unions. It is a product feature with its own auth and consistency questions, outside this sketch.
- Not a migration of the scenario suites' `pgpool` pattern. That pattern is the model the services suite adopts.
- Not a change to `make reap-images`, which stays image-only. `reap-runs` is its sibling, not a replacement.
