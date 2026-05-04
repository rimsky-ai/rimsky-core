# Rules

## Pre-v1 — break freely
Rimsky is pre-v1. There is no production data to preserve and no consumer is locked into a particular schema. When a refactor would be cleaner without a migration path, take the clean path. Delete dead code rather than carrying it forward.

- Migrations in `core/migrations/` are still numbered and append-only — that's how the migration runner works, not a backwards-compat guarantee. If a schema needs rethinking before v1 ships, write a new migration that drops + recreates rather than threading a compat shim.
- No backwards-compat guarantees on the wire protocol, the YAML config shape, the event-log payloads, or the resource interface until v1 ships. If a change requires nuking a dev Postgres, say so explicitly.
- When v1 ships, replace this section with deployed-stage rules.

## After Code Changes — Required Final Step
You are NOT done with a task until you have completed ALL of the following. Do this before reporting completion.

### Verify the build
Run **every** check that could be affected by the change. This is mandatory, not optional.

- **Any Go change:** `go build ./... && go test ./... && make lint`
- **Proto changes (`proto/v1/*.proto`):** `make proto-gen` first, then the Go checks above.
- **Scenario or storage changes:** `go test ./test/scenarios/... ./core/storage/... -count=1` (these spin up real Postgres via testcontainers — Docker must be running).
- **Race-sensitive paths (queue, supervisor, scheduler):** add `-race`, e.g. `go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3`.
- **Reference-binary or deploy changes:** rebuild the Docker images touched by the change (`deploy/build-images.sh`) and bring up `deploy/docker-compose.yml` to verify the stack still reaches `/health`.
- **TypeScript executor (`executors/claude-agent/`):** `cd executors/claude-agent && npm install && npm test && npm run build`.
- **Conformance-relevant changes (protocol, executor surface):** `go run ./core/cmd/rimsky-conformance --endpoint <executor> --transport grpc` against the executors you touched.
- **If any check fails, fix it before moving on.** A passing test in one package does not guarantee others pass — interface changes, proto regenerations, and shared-type changes propagate across packages and across the Go ↔ TS boundary.

### Update documentation
1. **`docs/architecture.md`** — update when package structure, blessed invariants, or the three-collections boundary changes.
2. **`docs/specs/2026-05-04-service-protocol-contract.md`** — update when the wire contract changes. Authoritative source for all three service protocols. The proto sources at `protocols/proto/v1/*.proto` and the contract doc must stay in sync. (`docs/protocol.md` is now a one-page pointer to this contract.)
3. **`docs/node-graph-design.md`** — update when the conceptual model (states, messages, error actions, resource semantics) changes.
4. **`docs/operator-guide.md`** — update when env vars, YAML config, or deployment topology changes.
5. **`docs/executor-author-guide.md` / `docs/claim-producer-author-guide.md`** — update when the corresponding interface contract changes.
6. **Cold-read annotations** (`@source`, `@diverged`, `@agent-contract`, `@blessed-invariant`) — update when modifying annotated code.
7. **`CHANGELOG.md`** — append a bullet under `## Unreleased` describing the change and its rationale.
8. **`CLAUDE.md`** — only if the change affects something a future session would otherwise trip over (a new blessed invariant, a new gotcha, a new build step). Most changes don't need a CLAUDE.md update.
9. **Dead code** — remove anything the change has rendered unreachable.

## Fix Every Bug You Find
If you discover a bug, broken behavior, or incorrect code while working — even if it's unrelated to your current task — fix it. Do not log it for later. Do not defer it. Do not work around it. Do not describe it in a report and move on. Fix it, verify the fix, and document what you changed.

This applies to all work: feature development, code review, debugging, testing, auditing. "Low severity", "cosmetic", "architecture change required", "not in scope" — none of these are reasons to leave a bug unfixed. If the fix requires an architecture change, make the architecture change. If the root cause is unclear, debug until it is clear.

Do not use workarounds. If a function doesn't persist a field, fix the function — don't update the database directly. Workarounds mask bugs.

## Project-agnostic
Rimsky is a self-contained orchestration platform intended to be embedded by many consumers (as a Go module, as Docker images, or as a git submodule). No code, doc, comment, test fixture, or example may name or assume a specific consumer. Templates and examples must use generic, illustrative names (`project-alpha`, `analytics_production`, `items`, `category`). If a real consumer's terminology has leaked in, scrub it.

## Code Style
All new code must follow cold-read conventions (see `cold-read-cheatsheet.md` and the longer-form docs in `cold-read/`).

The Go-specific lint set is enforced by `.golangci.yml` (`make lint`): gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog` only — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. Cron parsing is `robfig/cron/v3`. Resist adding heavier alternatives (Viper, Cobra, Gin, Echo).

## Search Scoping
Exclude from file searches: `.git/`, `vendor/`, `bin/`, `tmp/`, `proto/v1/gen/` (generated), `executors/claude-agent/node_modules/`, `executors/claude-agent/dist/`, `coverage.out`, `coverage.html`.

## Writing & Analysis
- Save project-specific notes to project-local paths (`./CLAUDE.md`, `./docs/`, `./CHANGELOG.md`), not external memory.
- When writing analysis or design documents, cross-check the written output against your findings before finishing — don't omit sections discussed verbally.
- Design proposals go in `docs/` with a YYYY-MM-DD prefix (e.g. `docs/2026-04-25-stores-redesign.md`). Implementation execution logs go in `docs/history/` once the work lands.
