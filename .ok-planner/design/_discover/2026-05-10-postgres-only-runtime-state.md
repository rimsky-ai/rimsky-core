---
topic: postgres-only-runtime-state
kind: boundary
---

# Three runtime processes share state only through Postgres; the unified entrypoint spawns them as children

## Description

Rimsky's runtime consists of three independent binaries — `rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api` — plus a synchronously-run `rimsky-migrate` step. The unified `rimsky-entrypoint` spawns the three children concurrently after migrate completes (`cmd/rimsky-entrypoint/main.go:30,46-71`). The three processes do not import each other (enforced by `.golangci.yml` depguard rules + the package directory layout) and have no in-process IPC: every cross-process signal flows through database rows.

Each binary constructs its own `persistence.Driver` from the unified `rimsky.yml` and wires its own subset of components:

- **`rimsky-scheduler`** (`cmd/rimsky-scheduler/main.go`) — runs scheduler ticks, processes schedules, fires invalidates, sweeps stuck frames, sweeps parked nodes. Uses `modeling/scheduler/`.
- **`rimsky-supervisor`** (`cmd/rimsky-supervisor/main.go`) — runs the integration runner (acquisition tx, dispatch, terminal handling, auto-terminal). Uses `foundation/integration/`.
- **`rimsky-control-api`** (`cmd/rimsky-control-api/main.go`) — serves the chi HTTP routes (templates, instances, observability, admin). Uses `modeling/controlapi/`.

The unified `rimsky-entrypoint` (`cmd/rimsky-entrypoint/main.go`) spawns the three as plain `exec.Command` children and forwards SIGTERM/SIGINT. CLAUDE.md "Reference deployment & local stack" describes this as the single Docker image `rimsky/all` bundling all three under one PID-1 entrypoint.

The "memory" blob backend reject gate at `foundation/persistence/blob_config.go:115` is the code-level acknowledgment that the only cross-process channel is the database: that backend is rejected unless `RIMSKY_PROCESS_ROLE=unified` because "the per-process binaries (rimsky-scheduler, rimsky-supervisor, rimsky-control-api) cannot share state through an in-process map." Same restriction applies in principle to SQLite (CLAUDE.md "Non-obvious gotchas" notes SQLite + replicas > 1 silently splits state) but SQLite is not gate-rejected — it's marked dev-only with operator documentation.

The shape of every shared table is constrained by the three-process topology. `rimsky_worker_request` carries `claimed_by` (supervisor id), `frame_id`, `phase` because every cross-role question must materialize as a queryable row. `rimsky_claim_handle.holder_supervisor_id` exists for the claimant-guarded release invariant. `rimsky_supervisors` is the supervisor-registry table. Heartbeat columns are queryable timestamps, not in-memory channels.

Alternative considered: in-process orchestration with goroutines per role. Not chosen — running as separate processes lets operators scale supervisors independently of the control-api, lets the scheduler crash without taking the API down, and supports multi-replica supervisor pools. The unified entrypoint shows the project still supports colocated mode as a single Docker image for dev convenience without giving up the separation: each spawned child uses its own pgxpool.

## Code surface

- `cmd/rimsky-entrypoint/main.go` — entire file; spawn and signal forwarding.
- `cmd/rimsky-scheduler/main.go`, `cmd/rimsky-supervisor/main.go`, `cmd/rimsky-control-api/main.go` — three independent main packages.
- `cmd/rimsky-migrate/main.go` — synchronously-run migrate step.
- `foundation/persistence/driver.go` — `Driver` interface (each process constructs its own).
- `foundation/persistence/blob_config.go:103-130` — memory-backend reject gate.
- `deploy/rimsky-all.yml`, `deploy/Dockerfile.all` — unified-image config.

## Prose surface

- `CLAUDE.md` "Four Backend Processes" (zonebase parent doc) — analogous pattern but in the consumer.
- `CLAUDE.md` "Reference deployment & local stack" — unified-image story.
- `CLAUDE.md` "Non-obvious gotchas" — memory backend reject, SQLite-not-gated.
- `docs/humans/dashboard.md` — operator-facing description of multi-process topology.

## Adjacent topics

- `2026-05-10-three-go-module-split` — module split that complements the process split.
- `2026-05-10-blob-spill-pluggable-backends` — memory backend reject gate at the same boundary.
- `2026-05-10-sqlite-dev-only` — same multi-host argument applies to SQLite.
- `2026-05-10-advisory-locks-tick-and-migrate` — the cross-process coordination primitive.
- `2026-05-10-unified-rimsky-yml-config` — same config file consumed by all three.

## Observations

- The three processes can be scaled independently (more supervisors for executor-heavy load; one scheduler is canonical because of the advisory tick lock). A multi-supervisor deployment relies on `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` to ensure only one scheduler runs the tick at a time.
- `rimsky-migrate` is run synchronously by the entrypoint before the three runtime children start; it uses the migration session lock so concurrent runs across replicas are safe. The migrate binary exits 0 when done; the three children block on it.
- The unified image's PID-1 (`rimsky-entrypoint`) forwards SIGTERM but not all signals (per the file contents); a graceful shutdown depends on each child's signal handling.
- The "memory blob backend dev-only" rule is enforced; the "SQLite dev-only" rule is documented but not enforced. The asymmetry is called out in `2026-05-10-sqlite-dev-only` "Open question".
