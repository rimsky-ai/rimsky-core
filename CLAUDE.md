# CLAUDE.md

Rimsky is a project-agnostic reactive node-graph orchestration platform. This file is a pointer index — per rimsky's own "After Code Changes" rule, keep CLAUDE.md lean and only update it when something a future session would otherwise trip over has changed.

## Where to look first

**Architecture, concepts, invariants** — `.ok-planner/design/concepts.md` is the TOC; per-noun definitions, boundaries, and invariants live under `.ok-planner/design/concepts/<slug>.md`. The concept catalog is the durable design surface, updated alongside code through `/execute-plan` runs, and is the authoritative source for ownership and invariants. Start with `concepts/module-layout.md` for the four-module / four-layer split (`protocols/` + `foundation/` + the opt-in test-only `testpg/` + root with `graph/` → `runtime/` → `control/`).

**Enforced import boundaries** — `.golangci.yml` `depguard` block (`pgx-isolation`, `foundation-internal-isolation`, `protocols-purity`, `foundation-purity`, `graph-purity`, `runtime-purity`, `consumption-side-isolation`). If lint and prose disagree, lint wins. `go.work` ties the four Go modules together (root, `foundation`, `protocols`, `testpg`).

**Load-bearing safety properties** — `grep -rn '@blessed-invariant' .` in source. Each annotation carries the invariant plus the code site that enforces it; scenario tests under `test/scenarios/` exercise them.

**Concept-to-code links** — `grep -rn '@concept:' .` in source.

**Open / unresolved design questions** — `.ok-planner/design/tensions/`.

**Build commands** — `Makefile`. Standard targets: `make build-all`, `make test-all`, `make lint`, `make tidy`, `make proto-gen`. Scenario and storage tests under `test/scenarios/...` and `foundation/persistence/...` use testcontainers-go and require a working Docker socket.

**Public docs** — not part of this repo. This tree carries no docs sources, no docs-lint tooling, and no docs gate.

**Image builds** — Dockerfiles live in `dockerfiles/`. `make core-images` builds the four distributed images: `rimsky` (all role binaries + `rimsky-entrypoint` under one image — role by container command, backend by config — `dockerfiles/Dockerfile.rimsky`), `rimsky-all-in-one` (the `rimsky` image plus baked zero-config SQLite defaults so it runs out of the box for local dev — built `FROM rimsky:$(VERSION)`, so it must follow the `rimsky` build — `dockerfiles/Dockerfile.all-in-one`, baking `dockerfiles/all-in-one.{rimsky,supervisor-config}.yml`), `rimsky-host-agent-proxy` (`dockerfiles/Dockerfile.go-base`), and `rimsky-conformance` (the bundled protocol conformance runners — `dockerfiles/Dockerfile.conformance`). The CLI ships as a binary (`make cli`), not an image.

**Recent changes** — `git log`. This repo keeps no CHANGELOG; design rationale lives in the concept catalog (above) and `.ok-planner/` history.

**Workflow scratch (do not cite, do not refresh)** — `.ok-planner/{specs,plans,sketches,history}/`. Per `.ok-planner/CLAUDE.md` these are historical records of how work was planned, not living docs of the codebase. This includes dated `YYYY-MM-DD-*-contract.md` and `YYYY-MM-DD-*-design.md` files under `specs/` — they are archive material even if prior CLAUDE.md text framed them as authoritative.

## Cross-cutting gotchas

These don't have a natural concept-doc home and would trip a fresh session.

- **Supervisor callback hostname.** The supervisor binds its async-callback HTTP listener on `0.0.0.0`, but executors need a reachable hostname to dial back. Set `callback.advertise_host` in YAML or `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` env to a hostname the executors can reach the supervisor at. Empty → executors can't reach back.
- **Async-callback body key.** An external executor must POST to `${callback_url}/v1/callback/{async_ack_id}` with the body keyed `type` (not `kind`) — enforced by the Go supervisor's chi route.
- **Dropped `POST /sensors/{watch_id}/observations` route.** The 2026-05-17 publisher-protocol unification collapsed the sensor-observation path into the universal `POST /instances/{id}/messages` endpoint with `sender_kind: "publisher"` + `publisher_subscription_id` capability. The old route is GONE — a v1 deployment fronting v0-pointing external publishers returns 404 with no other diagnostic. Operators upgrading from v0 must repoint publishers at `/instances/{id}/messages` and pass the new envelope shape; see `concept:publisher-subscription` for the wire shape.
- **Universal `Idempotency-Key` header on `POST /instances/{id}/messages`.** Every publisher message-emit MUST carry the `Idempotency-Key` HTTP header. Rimsky dedups via `table:rimsky_message_idempotencies` and returns the original `message_id` with `200 OK` on replay (a fresh insert returns `201 Created`). Status-code distinction is operator-visible. Sweep TTL is controlled by `cfg:messages.idempotency_ttl_seconds`.
- **Late-bound binaries must read `RIMSKY_AGENT_PORT`.** When `rimsky-host-agent` spawns a local binary as a late-bound service (`rimsky run --service <name>=<path>`), it picks a free port, sets `RIMSKY_AGENT_PORT` in the child's environment, and poll-dials `127.0.0.1:<port>` until the child's gRPC server is up (bounded by the Spawn's ready-timeout). The spawned binary MUST read `RIMSKY_AGENT_PORT` and bind its gRPC server there — there is no port handshake back to the agent. A binary that ignores the env var or binds elsewhere fails the readiness poll and the dispatch returns `spawn_failed`. The proxy binary reads `RIMSKY_PROXY_GRPC_PORT` (default 9090), `RIMSKY_CONTROL_API_URL`, and `RIMSKY_CONTROL_API_TOKEN` (the cache-miss `GET /instances/{id}` fallback). See `concept:host-agent` / `concept:host-agent-proxy` for the architecture.

## Code style

Follow `.claude/rules/cold-read-cheatsheet.md`: organize by feature; ~500-line file / ~100-line function guidelines; max 3 nesting levels via early returns; prefer tracked duplication with `@source:` / `@diverged:` over hidden coupling; `@agent-contract` / `@blessed-invariant` blocks for stable cross-cutting concerns.

Go specifics enforced by `.golangci.yml`: gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive. Logging is stdlib `log/slog` only — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. SQLite is `modernc.org/sqlite` (pure-Go, no CGO). Cron is `robfig/cron/v3`.
