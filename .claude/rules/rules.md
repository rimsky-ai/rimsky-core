# Rules

## Pre-v1 — break freely
Rimsky is pre-v1. There is no production data to preserve and no consumer is locked into a particular schema. When a refactor would be cleaner without a migration path, take the clean path. Delete dead code rather than carrying it forward.

- Migrations in `lib/foundation/persistence/{postgres,sqlite}/migrations/` are still numbered and append-only — that's how the migration runner works, not a backwards-compat guarantee. If a schema needs rethinking before v1 ships, write a new migration that drops + recreates rather than threading a compat shim.
- No backwards-compat guarantees on the wire protocol, the YAML config shape, the event-log payloads, or the resource interface until v1 ships. If a change requires nuking a dev Postgres, say so explicitly.
- When v1 ships, replace this section with deployed-stage rules.

## After Code Changes — Required Final Step
You are NOT done with a task until you have completed ALL of the following. Do this before reporting completion.

### Verify the build
Run **every** check that could be affected by the change. This is mandatory, not optional.

- **Any Go change:** `go build ./... && go test ./... && make lint`
- **Proto changes (`lib/protocols/proto/v1/*.proto`):** `make proto-gen` first, then the Go checks above.
- **Scenario or storage changes:** `go test ./test/scenarios/... ./lib/foundation/persistence/... -count=1` (these spin up real Postgres via testcontainers — Docker must be running).
- **Race-sensitive paths (queue, supervisor, scheduler):** add `-race`, e.g. `go test ./lib/foundation/persistence/postgres/... ./lib/runtime/... ./lib/graph/scheduler/... -race -count=3`.
- **Reference-binary or deploy changes:** rebuild the touched core images with `make core-images` (and `make service-images` for bundled-service changes), then verify the stack via the testcontainers-based services harness under `lib/services/test/` (e.g. `go test ./lib/services/test/scenarios/... -count=1`), which boots `rimsky-all-in-one:latest` and drives a node to terminal.
- **TypeScript executor (`lib/services/executors/claude-agent/`):** `cd lib/services/executors/claude-agent && npm install && npm test && npm run build`.
- **Conformance-relevant changes (protocol, executor surface):** `go run ./cmd/rimsky conformance executor --endpoint <executor> --transport grpc` against the executors you touched.
- **If any check fails, fix it before moving on.** A passing test in one package does not guarantee others pass — interface changes, proto regenerations, and shared-type changes propagate across packages and across the Go ↔ TS boundary.

### Update documentation
1. **Plumbline annotations** (`@source`, `@diverged`, `@agent-contract`, `@blessed-invariant`) — update when modifying annotated code.
2. **`CLAUDE.md`** — only if the change affects something a future session would otherwise trip over (a new blessed invariant, a new gotcha, a new build step). Most changes don't need a CLAUDE.md update.
3. **Dead code** — remove anything the change has rendered unreachable.

## Fix Every Bug You Find
If you discover a bug, broken behavior, or incorrect code while working — even if it's unrelated to your current task — fix it. Do not log it for later. Do not defer it. Do not work around it. Do not describe it in a report and move on. Fix it, verify the fix, and document what you changed.

This applies to all work: feature development, code review, debugging, testing, auditing. "Low severity", "cosmetic", "architecture change required", "not in scope" — none of these are reasons to leave a bug unfixed. If the fix requires an architecture change, make the architecture change. If the root cause is unclear, debug until it is clear.

Do not use workarounds. If a function doesn't persist a field, fix the function — don't update the database directly. Workarounds mask bugs.

## Project-agnostic
Rimsky is a self-contained orchestration platform intended to be embedded by many consumers (as a Go module, as Docker images, or as a git submodule). No code, doc, comment, test fixture, or example may name or assume a specific consumer. Templates and examples must use generic, illustrative names (`project-alpha`, `analytics_production`, `items`, `category`). If a real consumer's terminology has leaked in, scrub it.

## Code Style
All new code must follow Plumbline conventions — see `plumbline-cheatsheet.md` in this directory for the actionable rules (materialized by the [Plumbline plugin](https://github.com/fallguyconsulting/plumbline) via `/plumbline:affirm`).

The Go-specific lint set is enforced by `.golangci.yml` (`make lint`): gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog` only — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. Cron parsing is `robfig/cron/v3`. Resist adding heavier alternatives (Viper, Cobra, Gin, Echo).

## Search Scoping
Exclude from file searches:`.ok-planner`, `.git/`, `vendor/`, `bin/`, `tmp/`, `lib/protocols/proto/v1/gen/` (generated), `lib/services/executors/claude-agent/node_modules/`, `lib/services/executors/claude-agent/dist/`, `coverage.out`, `coverage.html`.

## Writing & Analysis
- Save project-specific notes to project-local paths (e.g. `./CLAUDE.md`), not external memory.
- When writing analysis or design documents, cross-check the written output against your findings before finishing — don't omit sections discussed verbally.
- Design proposals go in `.ok-planner/sketches/` with a YYYY-MM-DD prefix.
- When writing prose to a human in an interactive session — status updates, review findings, items surfaced into notes files — use the citation grammar in `.claude/rules/citation-grammar.md` to make artifact kinds explicit (code, tables, protos, concepts, invariants, etc.). The grammar applies to live agent ↔ user prose only; it is **not** a convention for source code, repo docs, or commit messages.
