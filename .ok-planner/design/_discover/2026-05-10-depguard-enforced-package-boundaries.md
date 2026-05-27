---
topic: depguard-enforced-package-boundaries
kind: boundary
---

# Depguard enforces pgx-isolation and foundation-internal-isolation at lint time

## Description

Module boundaries declared by `go.mod` only protect against importing a package's exported surface from outside the module. They don't prevent code inside the root module from reaching into `pgx` or `foundation/internal/` directly, which would erode the persistence-driver abstraction and foundation-private invariants. Rimsky's `.golangci.yml` configures two depguard rules to close those leaks.

**`pgx-isolation`** (`.golangci.yml:14-30`) denies imports of `github.com/jackc/pgx/v5` (and `pgxpool`, `pgconn`) everywhere except an explicit allow-list:

- `foundation/persistence/postgres/` — the postgres driver impl.
- `foundation/internal/pgtest/` — driver-internal test fixtures (testcontainers-backed).
- `cmd/` — every binary main package (legitimate connection construction).
- `modeling/internal/pgtest/` — modeling-side pgx-using integration tests.
- `modeling/scenario/` — the scenario test harness.
- `stores/` — bundled producer reference impls (the postgres store needs pgx).
- `test/smoke/` — smoke fixture.

The denial message: "pgx is allowed only in [allow-list]. Use the persistence interfaces."

**`foundation-internal-isolation`** (`.golangci.yml:35-50`) denies any non-`foundation/` package from importing `github.com/rimsky-ai/rimsky-core/foundation/internal`. The denial message: "foundation/internal/ is private to the foundation module. Use the public foundation packages."

Both rules ship in the lint set `make lint` runs. CLAUDE.md's "Package import rules" section calls them out as "non-negotiable" and lists the allow-lists explicitly.

The rules' value is twofold: (a) the modeling and integration layers can only see Postgres through `persistence.Driver` / `persistence.AdvisoryLocker` / `persistence.Queue` interfaces, which keeps the persistence-pluggable design (`@blessed-invariant`s 7, 8, 10 all rely on advisory-lock helpers exposed via the interface); (b) the second concrete persistence driver (sqlite) is in fact present at `foundation/persistence/sqlite/`, validating the abstraction. A second prod-grade driver (CockroachDB, planetscale, …) plugs in at the same interface.

Adding a new package that needs raw pgx requires editing `.golangci.yml`'s allow-list explicitly — a visible review-time decision rather than a silent type assertion that would slip through.

## Code surface

- `.golangci.yml:14-30` — `pgx-isolation` rule.
- `.golangci.yml:35-50` — `foundation-internal-isolation` rule.
- `foundation/persistence/driver.go` — the interface families that modeling code uses instead.
- `foundation/internal/pgtest/` — example test-fixture package using pgx legitimately.
- `foundation/persistence/postgres/` — the only driver allowed to import pgx.

## Prose surface

- `CLAUDE.md` "Package import rules (enforced; violations break the build)" — full enumeration.
- `.claude/rules/cold-read-cheatsheet.md` "Dependencies" — max 2 files of import depth.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — foundation as a separate module.
- `.ok-planner/specs/2026-05-04-modeling-layer-contract.md` — modeling reads only the interfaces.

## Adjacent topics

- `2026-05-10-three-go-module-split` — depguard is one of two enforcement mechanisms (module boundary + lint).
- `2026-05-10-sqlite-dev-only` — sqlite is the second driver the abstraction protects.
- `2026-05-10-stdlib-slog-and-minimal-deps` — minimal-deps discipline.

## Observations

- The allow-list mixes layers: `cmd/` is a wide allowance (every binary's main package) while `modeling/internal/pgtest/` is narrow (only this one subdir of modeling). A new modeling-side test that wants pgx would need to be placed under the internal pgtest path or the allow-list extended.
- `stores/` is allow-listed as a whole; the bundled stores ship as separate binaries (`stores/postgres/main.go`) and the postgres store legitimately needs pgx. But the rule doesn't distinguish "stores binary main packages" from "stores/internal helper that should not need pgx" — the unit of allow is the directory prefix.
- `foundation-internal-isolation` is named after the directory `foundation/internal/`, which contains the pgtest fixture. The rule's scope grows automatically as more code is added under `foundation/internal/`.
- There is no symmetric rule for `modeling/internal/`: modeling-internal-internal-isolation does not exist, only the pgtest-targeted pgx isolation. So `modeling/internal/` is not formally a private surface in the way `foundation/internal/` is.
