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
- **Reference-binary or deploy changes:** rebuild the touched core images with `make core-images` (and `make service-images` for bundled-service changes), then verify the stack via the testcontainers-based services harness under `lib/services/test/` (e.g. `go test ./lib/services/test/scenarios/... -count=1`), which boots `rimsky-all-in-one:latest` and drives a node to terminal.
- **Conformance-relevant changes (protocol, executor surface):** `go run ./cmd/rimsky conformance executor --endpoint <executor> --transport grpc` against the executors you touched.
- **If any check fails, fix it before moving on.** A passing test in one package does not guarantee others pass — interface changes, proto regenerations, and shared-type changes propagate across packages and across the Go ↔ TS boundary.

### Update documentation
1. **Citation tags** (`@concept:`, `@story:`, `@decision:`) — if you rename or move the design artifact a tag resolves to, update the citing call sites in the same change so `citation_resolution` stays green.
2. **`CLAUDE.md`** — only if the change affects something a future session would otherwise trip over (a new blessed invariant, a new gotcha, a new build step). Most changes don't need a CLAUDE.md update.
3. **Dead code** — remove anything the change has rendered unreachable.

## Fix Every Bug You Find
If you discover a bug, broken behavior, or incorrect code while working — even if it's unrelated to your current task — fix it. Do not log it for later. Do not defer it. Do not work around it. Do not describe it in a report and move on. Fix it, verify the fix, and document what you changed.

This applies to all work: feature development, code review, debugging, testing, auditing. "Low severity", "cosmetic", "architecture change required", "not in scope" — none of these are reasons to leave a bug unfixed. If the fix requires an architecture change, make the architecture change. If the root cause is unclear, debug until it is clear.

Do not use workarounds. If a function doesn't persist a field, fix the function — don't update the database directly. Workarounds mask bugs.

## Tests Are Deterministic — There Are No Flakes
A test that passes sometimes and fails sometimes is a defect, not a weather condition. It is either a bug in the test (it asserts on something nondeterministic) or a bug in the system (a real race the test caught intermittently). Both must be fixed at the root. **Never "tune" a test to make it pass** — bumping a deadline, adding a `sleep`, raising a retry count, or loosening an assertion hides which of the two bugs you have and guarantees the failure returns on a slower machine.

Rules:
- **Synchronize on the event, not on wall-clock time.** Block on the channel / `WaitGroup` / condition the code signals when the awaited thing happens. A test that waits for a real signal passes in 3ms or 3s identically.
- **No wall-clock constant in the pass/fail path — not even a "generous" one.** A per-assertion timeout that can fire on correct-but-slow execution is a load-dependent verdict, i.e. nondeterministic; raising 5s→30s only lengthens the fuse, and any finite value is an unprovable guess about the load ceiling ("why 30 and not 29?" has no answer — that is the tell). Block on the signal with a **bare receive** (`<-ch`) or **poll the condition in a loop that only exits on success** (small inter-poll sleep for CPU politeness is fine; a deadline that returns false/fails is not). A correct system always delivers the awaited signal, so blocking forever is safe and passes fast; a broken one never delivers and hangs.
- **The hang backstop measures progress, not elapsed time.** `go test -timeout` is banned as a backstop and every suite runs with `-timeout 0`. A per-package timeout is an *aggregate* budget covering every test in the package: it must be sized to total runtime, so one test blocking longer under load consumes the budget belonging to every other test, and the machine's load decides which arbitrary set gets killed. That makes it a verdict input, exactly like the per-assertion deadline banned above — the aggregate form was simply out of the lint's reach. The sanctioned backstop is `tools/gotest-guard.sh`, which watches the runner's JSON event stream and kills a run only when nothing has started, completed, or emitted output for a long interval (`RIMSKY_TEST_NO_PROGRESS_SECS`, default 20m). A correct suite emits continuously at any load, so the interval never binds; a hung run emits nothing and dies loudly. The guard reports that kill as an **inconclusive run** with its own exit code — never as a test failure, because it cannot tell a hung test from a saturated machine and must not pretend otherwise. Never put a hang backstop inside the test's verdict logic.
- **Concurrency caps bound real shared resources only.** `-p` / `-parallel` limits exist because the testcontainers-backed suites boot Postgres and rimsky stacks against one docker daemon (see `decision:parallel-cap-removal`); a cap must name the contention it guards and is never scheduling insurance for a racy test.
- **No `time.Sleep` to "let things settle"** in a test. Replace with an explicit synchronization point. No bare `time.Now()` in logic under test — inject the `Clock` abstraction the codebase already provides.
- **Isolate per-test state:** unique DB schema/namespace, `t.TempDir()`, OS-assigned ephemeral ports, no package-level mutable state shared across tests.
- **No `-race` in the suite, ever.** The suite's job is catching regressions in business logic, and its verdict must be a function of the code alone. The race detector is a *detector*, not a check: a report is real, but a green run proves nothing, so wiring it into a gate makes the gate's verdict probabilistic — the same defect as a flaky assertion, arriving from the other direction. Finding races is a separate discipline with its own tooling and its own cadence; it does not gate the build, and no target, CI job, or release chain may add `-race` back.
- Deterministic seeds for anything randomized (vary by index, never by wall-clock).
- When you touch a test that has ever flaked, fix its nondeterminism at the root as part of that change ("Fix Every Bug You Find" applies to flaky tests).

## Structured Payloads Are Proto-Declared
Any structured JSON payload rimsky itself authors — event-log rows, cascade signal payloads — is declared as a message in `lib/protocols/proto/v1/events.proto` and built from the generated Go type. Never assemble one as a `map[string]any`. The payload-carrying fields (`persistence.EventAppendInput.Payload`, `signal.Signal.Payload`) take `eventpayload.Payload`, whose only constructor accepts a generated message, so a map literal does not compile — that is the check, not a habit.

The rule exists because the two halves drifted: fields were declared and never written, keys were written and never declared, and nothing caught either. Constructing from the generated type makes both unrepresentable.

- **Adding a field to a payload** means editing the proto, running `make proto-gen`, and setting it at the emit site. A field nobody sets is a field that should not be declared.
- **A payload whose shape belongs to someone else** — an executor's error data, a template author's opaque blob — is not given a rimsky shape. Declare it `google.protobuf.Struct` when the bytes must stay traversable (structurally inert, per `concept:inertness` — CEL payload predicates read these), or `bytes` when they are byte-opaque. Either way rimsky passes them through uninspected.
- **Reading a payload back** (a row from the store, a response DTO) goes through `.Map()`. Decoding is not construction; `eventpayload.Decoded` exists for that path only.
- **JSON types, not Go types.** A constructed payload holds what gets persisted: numbers are `float64`, timestamps are RFC3339 strings, repeated fields are `[]any`. Assert against that shape. 64-bit integer fields serialize as JSON strings per the protobuf JSON mapping — prefer a 32-bit field where the value is bounded and should read as a number.

## Project-agnostic
Rimsky is a self-contained orchestration platform intended to be embedded by many consumers (as a Go module, as Docker images, or as a git submodule). No code, doc, comment, test fixture, or example may name or assume a specific consumer. Templates and examples must use generic, illustrative names (`project-alpha`, `analytics_production`, `items`, `category`). If a real consumer's terminology has leaked in, scrub it.

## Code Style
All new code must follow Plumbline conventions — see `plumbline-cheatsheet.md` in this directory for the actionable rules (materialized by the [Plumbline plugin](https://github.com/fallguyconsulting/plumbline) via `/plumbline:affirm`).

The Go-specific lint set is enforced by `.golangci.yml` (`make lint`): gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive (without the `exported` rule). Logging is stdlib `log/slog` only — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. Cron parsing is `robfig/cron/v3`. Resist adding heavier alternatives (Viper, Cobra, Gin, Echo).

## Search Scoping
Exclude from file searches:`.ok-planner`, `.git/`, `vendor/`, `bin/`, `tmp/`, `lib/protocols/proto/v1/gen/` (generated), `coverage.out`, `coverage.html`.

## Writing & Analysis
- Save project-specific notes to project-local paths (e.g. `./CLAUDE.md`), not external memory.
- When writing analysis or design documents, cross-check the written output against your findings before finishing — don't omit sections discussed verbally.
- Design proposals go in `.ok-planner/sketches/` with a YYYY-MM-DD prefix (via `/sketch`); design questions go into the issue intake (`.ok-planner/issues/`, one file per question) to be resolved via `/plan-sprint`; ad-hoc working documents live in `.ok-planner/workbench/` with a YYYY-MM-DD prefix.
- When writing prose to a human in an interactive session — status updates, review findings, items surfaced into notes files — use the citation grammar in `.claude/rules/citation-grammar.md` to make artifact kinds explicit (code, tables, protos, concepts, invariants, etc.). The grammar applies to live agent ↔ user prose only; it is **not** a convention for source code, repo docs, or commit messages.
