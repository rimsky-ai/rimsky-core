---
topic: stdlib-slog-and-minimal-deps
kind: discipline
---

# Stdlib `log/slog` + chi + pgx + robfig/cron; resist heavier alternatives

## Description

The Go dependency budget across rimsky's three Go modules is deliberately constrained to a small set of approved libraries. The list (visible in `go.mod`, `foundation/go.mod`, `protocols/go.mod`):

- **Logging**: `log/slog` (stdlib), JSON handler. Every logger across the codebase is `slog.Default()` or a per-component `*slog.Logger`. No Zap, no Zerolog.
- **HTTP routing**: `github.com/go-chi/chi/v5` (root go.mod). No Gin, no Echo, no Fiber.
- **Postgres**: `github.com/jackc/pgx/v5` (root + foundation go.mod). Allow-listed by depguard. No `database/sql` adapters, no ORM.
- **SQLite**: `modernc.org/sqlite` (pure-Go, no CGO). The CGO-free property is load-bearing for cross-compilation.
- **Cron**: `github.com/robfig/cron/v3`.
- **JSON canonicalization**: `github.com/cyberphone/json-canonicalization` (JCS for content-addressed templates).
- **JSON Schema**: `github.com/santhosh-tekuri/jsonschema/v5` (draft-07 default).
- **Testing**: `testify` + `testcontainers-go` for integration; vanilla `testing` elsewhere.
- **Merging**: hand-rolled `modeling/shared/jsonmerge.go` (no third-party merge lib).

The **protocols module** has an even tighter budget: `grpc`, `protobuf`, `uuid`, stdlib. That's it. External implementers of the three protocols import only this module and transitively pull only those.

The **foundation module** adds `pgx` and `uuid` to protocols' set. No JSON schema, no JCS, no testing libraries beyond stdlib `testing` + foundation's own pgtest fixtures.

The **root module** pulls in the heavier libs (jsonschema, robfig/cron, jcs, testcontainers). This is the layer where modeling, control-api, and the cmd binaries live.

`.claude/rules/rules.md` makes the rule explicit: "Logging is stdlib `log/slog` only — no Zap, no Zerolog. HTTP routing is `go-chi/chi`. Postgres is `jackc/pgx/v5`. Cron parsing is `robfig/cron/v3`. Resist adding heavier alternatives (Viper, Cobra, Gin, Echo)." CLAUDE.md "Code style" repeats this.

The pattern is consistent enough to be a project rule. Inferred from the absence of common heavier alternatives: no Cobra/Viper (flag-library imports are absent), no Gin/Echo (chi is the only HTTP router), no ORM (raw SQL via pgx). New code MUST use `slog`; mixing in a different structured logger would create a churn-inducing discrepancy.

Pure-Go SQLite (`modernc.org/sqlite`) is what makes the cross-compilation story work without CGO — important for the Docker images, particularly the alpine-based runtime images. A CGO-backed SQLite would force the build to install a C toolchain in every image.

## Code surface

- `go.mod` (root), `foundation/go.mod`, `protocols/go.mod` — three dependency files.
- `cmd/rimsky-entrypoint/main.go:36` — slog setup site.
- `.golangci.yml:14-30` — depguard allow-list for pgx.

## Prose surface

- `.claude/rules/rules.md` "Code Style" — explicit list.
- `CLAUDE.md` "Code style" — restates the list.
- `cold-read/cold-read-style-guide.md` — feature-first conventions including minimal-deps.

## Adjacent topics

- `2026-05-10-three-go-module-split` — the budget is per-module.
- `2026-05-10-depguard-enforced-package-boundaries` — pgx is the only pinned external dependency under depguard.
- `2026-05-10-sqlite-dev-only` — modernc.org/sqlite enables the cross-compile story.

## Observations

- The protocols module's `grpc + protobuf + uuid` budget is tight enough that an external implementer importing it transitively pulls fewer than 10 third-party libraries. This is deliberate so that a custom claim-producer or executor doesn't carry rimsky's full dependency tree.
- The hand-rolled `modeling/shared/jsonmerge.go` (no third-party merge lib) is one example of "prefer stdlib + a small hand-rolled helper over a library." A future code review will catch attempts to introduce a merge library.
- The dependency budget is enforced by social convention (rules file + code review) plus the depguard rule for pgx. Other dependencies (Zap, Cobra, etc.) are not lint-rejected; their absence is the rule's evidence.
- `cyberphone/json-canonicalization` is the JCS impl for template hashing (`2026-05-10-content-addressed-templates`). Its version is pinned because changing canonicalization bytes would change every existing template hash.
