# Reintegrate rimsky-services under lib/services/ Implementation Plan

**Spec:** none
**Goal:** Pull the carved-out `../rimsky-services` repo back into rimsky-core as a fourth Go module at `lib/services/`, and back out its three external couplings (the published `protocols` Go-module pin, the `@rimsky-ai/protocols` npm package, and the Docker Hub / ghcr images).
**Architecture:** The bundled services (stores, sensors, subscribers, executors) re-enter as a self-contained Go module `github.com/rimsky-ai/rimsky-core/lib/services`, tied into the existing `go.work` alongside the root, foundation, and protocols modules. The module requires only `lib/protocols` (via a local `replace`), so the module graph itself enforces consumption-side isolation; the existing `consumption-side-isolation` depguard rule is retained as defense-in-depth. The TS `claude-agent` executor consumes the in-tree `lib/protocols` npm package via a `file:` dependency. Per-service Docker images build from the monorepo root via `go.work`, driven by a new `make service-images` target; the integration harness consumes locally-built image tags instead of Docker Hub / ghcr.
**Tech Stack:** Go 1.25 multi-module workspace (`go.work`), `golangci-lint` (depguard), the in-repo `tools/license-check` linter (`licensing.yml`), Docker multi-stage builds, npm/TypeScript (claude-agent), testcontainers-go (integration harness).

---

## Context the implementer must have

This plan reintegrates a sibling repo. Read this section before starting.

### The source

`../rimsky-services` (relative to the rimsky-core repo root) is a clean, separately-committed Git repo carved out of rimsky-core. Its tree:

```
stores/{filesystem,postgres}/{cmd,server,store,lifecycle}/   # claim-producer stores
stores/shared/sql-checks/                                    # shared helper
sensors/{sensor-cron,sensor-http,sensor-object-store,sensor-webhook}/
subscribers/openlineage/
executors/{http-node,verifier-http,verifier-shape-checks}/   # Go executors
executors/claude-agent/                                      # TS/npm workspace
internal/ops/ops.go                                          # 30-line slog setup
test/{harness,scenarios,smoke,stubexecutor}/                 # out-of-process integration tests
deploy/build-images.sh                                       # per-service image build script
<repo-level files: go.mod, go.sum, CLAUDE.md, README.md, CHANGELOG.md,
 .golangci.yml, .gitignore, .dockerignore, .github/, .claude/, cold-read/,
 COPYING.md, COPYRIGHT, LICENSE.agpl, NOTICE, CLA.md, TRADEMARKS.md,
 .DS_Store, openlineage (a ~15MB stray build-artifact binary)>
```

Each service has a co-located Dockerfile (`stores/filesystem/Dockerfile.filesystem`, etc.). The services repo's Go module path is `github.com/rimsky-ai/rimsky-services`; it imports rimsky-core's protocol surface as the **published** module `github.com/rimsky-ai/rimsky-core/protocols` (61 Go files).

### The three couplings being backed out

1. **Go module pin** — `github.com/rimsky-ai/rimsky-core/protocols v0.1.0`, resolved from the module proxy. Backed out by joining `go.work` and a local `replace`, plus rewriting imports to the in-tree path `github.com/rimsky-ai/rimsky-core/lib/protocols` (the reorg moved protocols under `lib/`).
2. **npm `@rimsky-ai/protocols@0.1.0`** — used only by `lib/services/executors/claude-agent/src/proto-loader.ts`, which imports `{ protoDir, protoPath }`. The package source already lives in-tree at `lib/protocols/` (its `package.json` name is `@rimsky-ai/protocols`; `lib/protocols/index.js` exports `protoDir`/`protoPath`). Backed out by changing the dependency to `file:../../../../lib/protocols`. **No change to `proto-loader.ts` is needed** — the import specifier `@rimsky-ai/protocols` still resolves, now to the local package.
3. **Docker images** — `test/harness/rimsky.go` pulls `rimskyai/rimsky-all-in-one:latest` from Docker Hub; `test/harness/store_filesystem.go` references `ghcr.io/rimsky-ai/rimsky-services/store-filesystem:latest`. Backed out by repointing to locally-built tags (`rimsky-all-in-one:latest`, built by core's `make core-images`; `rimsky-store-filesystem:latest`, built by the new `make service-images`).

### Key facts about the target repo (rimsky-core)

- Module paths: root = `github.com/rimsky-ai/rimsky-core`; protocols = `github.com/rimsky-ai/rimsky-core/lib/protocols`; foundation = `github.com/rimsky-ai/rimsky-core/lib/foundation`.
- `go.work` currently lists: `.`, `./lib/foundation`, `./lib/protocols`.
- `make build-all` / `test-all` / `lint` iterate modules with explicit `cd lib/foundation` / `cd lib/protocols` blocks (see `Makefile`). A nested module dir (one with its own `go.mod`) is **excluded** from a parent module's `./...`, so a freshly-copied-but-unwired `lib/services` does not break the root build.
- `lib/protocols/index.js` exports `protoDir` (abs path to `proto/v1/`) and `protoPath(file)`. `lib/protocols/package.json` name is `@rimsky-ai/protocols`, version `0.1.0`, with `files` shipping `proto/**/*.proto` + `index.js` + `index.d.ts`.
- `.golangci.yml` `consumption-side-isolation` rule already exists with root-anchored globs (`stores/**`, `sensors/**`, `subscribers/**`, `executors/**`) and denies `lib/foundation`, `lib/graph`, `lib/runtime`, `lib/control`, `cmd`. Its comment says the globs "no longer have local matches" — this becomes false after the move.
- `licensing.yml` classifies `lib/protocols/` as apache, everything else rimsky ships (`lib/foundation/`, `lib/graph/`, `lib/control/`, `lib/runtime/`, `cmd/`, `test/`, `tools/`) as agpl. The `tools/license-check` walker scans `.go .ts .tsx .proto .sql .sh`. Existence-checking applies only to `apache`/`agpl` entries, **not** `exempt` (so gitignored `node_modules`/`dist` are safe to exempt even when absent).
- `tools/license-check` `make license-stamp` reconciles per-file headers to match each file's classification (adds missing, replaces wrong-kind); `make license-lint` verifies classification + import direction.
- Core's per-service Docker build pattern (see `dockerfiles/Dockerfile.rimsky`, `dockerfiles/Dockerfile.go-base`): copy `go.work go.work.sum`, then each module's `go.mod`/`go.sum`, `go mod download`, `COPY . .`, build. Core has **no** `.dockerignore` yet.

### Decisions already made (do not re-litigate)

- **Plain copy, not git subtree.** The services repo has only 3 commits (a flat snapshot + 2 mechanical import refactors); the real history is in core's own past. Copy the files; the lineage is captured in the eventual commit message (see "Manual checks").
- **`lib/services` is its own Go module** (not folded into root). Mirrors the foundation/protocols pattern, isolates the heavy dependency set (testcontainers, moby), and makes the module graph enforce isolation.
- **Tests stay inside the services module** at `lib/services/test/...` (they cannot live in core's root `test/` — different module, and they must not reach core internals). This also avoids any collision with core's existing `test/scenarios/stores/`.
- **Per-service Dockerfiles stay co-located** with each service under `lib/services/`; the build loop moves into the `Makefile`.
- **Licensing:** the Go side of `lib/services/` is AGPL. This is a deliberate relicensing of the production services from their current Apache headers (the bundled services are an internal engineering effort; downstream consumers who need a permissive license implement against `lib/protocols` themselves). The services-repo `README.md` claims "AGPL throughout, except ops and claude-agent," but the actual headers tell a different story: ~87 Go files across `stores/`, `sensors/`, `subscribers/openlineage/`, the three Go executors, and `internal/ops/` carry Apache headers, while only the `test/` subtree (~20 files) is already AGPL. Under the new classification, `make license-stamp` rewrites every one of those 87 Apache headers to AGPL. Code bodies are unchanged; only the per-file license headers change. The single Go Apache island remains `lib/protocols`. The `claude-agent` TS workspace **stays Apache** (a separate reference deliverable, outside the Go import graph).

---

## Pass 1: Copy the services tree in and strip repo-level cruft

**Goal:** Land the services source under `lib/services/`, keeping only the parts that belong in the monorepo. The `lib/services` module is intentionally not yet buildable (wrong imports, not in `go.work`); the root build stays green because a nested module is excluded from `./...`.
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `make build-all && make lint`

### Task 1: Copy the services tree into `lib/services/`

**Files:** new `lib/services/` (copied from `../rimsky-services/`)

**Steps:**
1. From the rimsky-core repo root, create the directory and copy the source trees and module manifests:
   ```
   mkdir -p lib/services
   cp -R ../rimsky-services/stores       lib/services/stores
   cp -R ../rimsky-services/sensors      lib/services/sensors
   cp -R ../rimsky-services/subscribers  lib/services/subscribers
   cp -R ../rimsky-services/executors    lib/services/executors
   cp -R ../rimsky-services/internal     lib/services/internal
   cp -R ../rimsky-services/test         lib/services/test
   cp ../rimsky-services/go.mod          lib/services/go.mod
   cp ../rimsky-services/go.sum          lib/services/go.sum
   ```
2. Remove any copied build artifacts that should never be tracked:
   ```
   rm -rf lib/services/executors/claude-agent/node_modules
   rm -rf lib/services/executors/claude-agent/dist
   find lib/services -name .DS_Store -delete
   ```
3. Run `git add -A` to checkpoint, then `ls lib/services` and confirm `stores sensors subscribers executors internal test go.mod go.sum` are present and no `node_modules`/`dist`/`.DS_Store` remain.

**Verification:** `ls lib/services/stores/filesystem/cmd/main.go && ls lib/services/executors/claude-agent/src/proto-loader.ts && ! test -e lib/services/executors/claude-agent/node_modules && echo OK`

### Task 2: Verify the root build is unaffected by the unwired module

**Files:** none (verification only)

**Steps:**
1. Confirm `lib/services/go.mod` still has the **old** module path (it will be fixed in Pass 2): `grep '^module' lib/services/go.mod` should print `module github.com/rimsky-ai/rimsky-services`.
2. Confirm the root/foundation/protocols modules still build and lint clean despite the new nested module sitting unwired.

**Verification:** `make build-all && make lint`

---

## Pass 2: Make `lib/services` a buildable in-tree module

**Goal:** Repoint the module path, rewrite imports onto the in-tree `lib/protocols`, wire the module into `go.work`, and get it building and vetting clean.
**Scope:** Tasks 3–6
**End state:** working
**Verification:** `cd lib/services && go build ./... && go vet ./... && cd ../.. && make build-all`

### Task 3: Repoint the services `go.mod`

**Files:** `lib/services/go.mod`

**Steps:**
1. Read `lib/services/go.mod`.
2. Change the module path line to:
   ```
   module github.com/rimsky-ai/rimsky-core/lib/services
   ```
3. Remove the published-protocols requirement line `github.com/rimsky-ai/rimsky-core/protocols v0.1.0` from the `require` block.
4. Add a requirement for the in-tree protocols module and a local `replace`. Add to the `require` block:
   ```
   github.com/rimsky-ai/rimsky-core/lib/protocols v0.0.0
   ```
   and add at the end of the file:
   ```
   replace github.com/rimsky-ai/rimsky-core/lib/protocols => ../protocols
   ```
   (Mirror the form in `lib/foundation/go.mod`, which declares the same `replace`.)

**Verification:** `grep -E 'module github.com/rimsky-ai/rimsky-core/lib/services|lib/protocols => ../protocols' lib/services/go.mod`

### Task 4: Rewrite Go imports across `lib/services`

**Files:** all `*.go` under `lib/services/`

**Steps:**
1. Rewrite the protocols import path (the published path → the in-tree `lib/` path):
   ```
   grep -rl 'github.com/rimsky-ai/rimsky-core/protocols/' --include='*.go' lib/services | \
     xargs perl -pi -e 's{github\.com/rimsky-ai/rimsky-core/protocols/}{github.com/rimsky-ai/rimsky-core/lib/protocols/}g'
   ```
2. Rewrite the services self-import path (the old module path → the new in-tree path):
   ```
   grep -rl 'github.com/rimsky-ai/rimsky-services/' --include='*.go' lib/services | \
     xargs perl -pi -e 's{github\.com/rimsky-ai/rimsky-services/}{github.com/rimsky-ai/rimsky-core/lib/services/}g'
   ```
3. Confirm no stale references remain:
   ```
   grep -rn 'rimsky-ai/rimsky-core/protocols/\|rimsky-ai/rimsky-services' --include='*.go' lib/services
   ```
   This must print nothing.

**Verification:** `! grep -rqn 'rimsky-ai/rimsky-core/protocols/\|rimsky-ai/rimsky-services' --include='*.go' lib/services && echo OK`

### Task 5: Add the module to the workspace

**Files:** `go.work`

**Steps:**
1. Read `go.work`.
2. Add `./lib/services` to the `use (...)` block, after `./lib/protocols`:
   ```
   use (
       .
       ./lib/foundation
       ./lib/protocols
       ./lib/services
   )
   ```

**Verification:** `grep './lib/services' go.work`

### Task 6: Tidy and build the services module

**Files:** `lib/services/go.mod`, `lib/services/go.sum`, `go.work.sum`

**Steps:**
1. Regenerate the module's checksums against the local protocols replace: `cd lib/services && go mod tidy`.
2. Sync the workspace checksum file: from the repo root, `go work sync`.
3. Build and vet the services module (vet compiles test files too, without running them — so no Docker is needed): `cd lib/services && go build ./... && go vet ./...`.
4. If the build surfaces a string-literal path (not an import) referencing an old path — e.g. a `testdata` path or a Dockerfile-context string in Go — fix it to the new location. (The reorg precedent: one test had a hardcoded path string missed by import rewrites.)
5. Confirm the root modules still build: from the repo root, `make build-all`.

**Verification:** `cd lib/services && go build ./... && go vet ./... && cd ../.. && make build-all`

---

## Pass 3: Back out the npm dependency (claude-agent)

**Goal:** Point the TS executor at the in-tree `lib/protocols` package instead of the published npm package, and confirm its toolchain passes.
**Scope:** Task 7
**End state:** working
**Verification:** `cd lib/services/executors/claude-agent && npm install && npm run build && npm test`

### Task 7: Switch `@rimsky-ai/protocols` to a local `file:` dependency

**Files:** `lib/services/executors/claude-agent/package.json`, `lib/services/executors/claude-agent/package-lock.json`

**Steps:**
1. Read `lib/services/executors/claude-agent/package.json`.
2. Change the dependency line from `"@rimsky-ai/protocols": "0.1.0"` to:
   ```
   "@rimsky-ai/protocols": "file:../../../../lib/protocols"
   ```
   (Relative path from `lib/services/executors/claude-agent/` up four levels to the repo root, then into `lib/protocols`.)
3. Regenerate the lockfile and install (this also creates the `node_modules/@rimsky-ai/protocols` link to the local package): `cd lib/services/executors/claude-agent && npm install`.
4. Confirm the local package linked correctly: `node -e "const p=require('@rimsky-ai/protocols'); console.log(p.protoPath ? 'ok' : 'missing')"` from that directory should print `ok` — **or**, if the package is ESM-only and `require` fails, instead confirm the symlink target: `readlink node_modules/@rimsky-ai/protocols` (or `ls -la node_modules/@rimsky-ai/protocols`) resolves into `lib/protocols`.
5. Do **not** edit `src/proto-loader.ts` — its `@rimsky-ai/protocols` import now resolves to the local package unchanged.
6. Build and test the workspace: `npm run build && npm test`.

**Verification:** `cd lib/services/executors/claude-agent && npm run build && npm test`

---

## Pass 4: Wire the module into the repo's tooling (build, lint, license, gitignore)

**Goal:** Teach `make`, `golangci-lint`, the license linter, and `.gitignore` about the new module so all the standard gates cover it.
**Scope:** Tasks 8–12
**End state:** working
**Verification:** `make build-all && make lint && make license-lint && cd lib/services && go vet ./...`

### Task 8: Extend the multi-module Make targets

**Files:** `Makefile`

**Steps:**
1. Read `Makefile`.
2. In the `build-all` target, append a services block after the protocols block:
   ```
   	cd lib/services && go build ./...
   ```
3. In the `test-all` target, append:
   ```
   	cd lib/services && go test ./...
   ```
   (This target already requires Docker for core's own testcontainer tests; the services integration tests join it. The autonomous pass gates do not run `test-all`.)
4. In the `lint` target, append:
   ```
   	cd lib/services && golangci-lint run
   ```
5. In the `lint-docker` target's chained `sh -c '...'`, append a services block mirroring the protocols one:
   ```
   	  cd /src/lib/services && PATH=/go/bin:$$PATH golangci-lint run --timeout 5m
   ```
6. Update the comment above `test-all`/`build-all` ("exercise every Go module ... root + lib/foundation + lib/protocols") to include `lib/services`.

**Verification:** `make build-all` (the services block runs and succeeds)

### Task 9: Add the `service-images` Make target

**Files:** `Makefile`

**Steps:**
1. Add a new `service-images` target (and add it to the `.PHONY` line) that builds each bundled-service image from the **repo-root** build context, referencing the co-located Dockerfiles under `lib/services/` and tagging with a `rimsky-` prefix + `$(VERSION)` + `latest`. Model the docker invocations on `core-images`. The full image set and their Dockerfile paths:

   | Local image tag base | Dockerfile |
   | --- | --- |
   | `rimsky-store-filesystem` | `lib/services/stores/filesystem/Dockerfile.filesystem` |
   | `rimsky-store-postgres` | `lib/services/stores/postgres/Dockerfile.postgres` |
   | `rimsky-sensor-cron` | `lib/services/sensors/sensor-cron/Dockerfile.sensor-cron` |
   | `rimsky-sensor-http` | `lib/services/sensors/sensor-http/Dockerfile.sensor-http` |
   | `rimsky-sensor-object-store` | `lib/services/sensors/sensor-object-store/Dockerfile.sensor-object-store` |
   | `rimsky-sensor-webhook` | `lib/services/sensors/sensor-webhook/Dockerfile.sensor-webhook` |
   | `rimsky-subscriber-openlineage` | `lib/services/subscribers/openlineage/Dockerfile.openlineage` |
   | `rimsky-executor-http-node` | `lib/services/executors/http-node/Dockerfile.http-node` |
   | `rimsky-executor-verifier-http` | `lib/services/executors/verifier-http/Dockerfile.verifier-http` |
   | `rimsky-executor-verifier-shape-checks` | `lib/services/executors/verifier-shape-checks/Dockerfile.verifier-shape-checks` |
   | `rimsky-executor-claude-agent` | `lib/services/executors/claude-agent/Dockerfile` |

   Each line takes the form:
   ```
   	docker build -f <dockerfile> -t rimsky-<name>:$(VERSION) -t rimsky-<name>:latest .
   ```
   Add a short comment block above the target explaining it builds the bundled-service images from the monorepo via `go.work`, with the build context at the repo root.
2. This target is **not** verified by a docker build in this pass (Docker is an external environment); it is exercised in "Manual checks after completion." Confirm only that the Makefile parses: `make -n service-images` prints the docker commands without error.

**Verification:** `make -n service-images`

### Task 10: Update the depguard rules for the relocated services

**Files:** `.golangci.yml`

**Steps:**
1. Read the `consumption-side-isolation` and `pgx-isolation` blocks in `.golangci.yml`.
2. In `consumption-side-isolation`, replace its `files:` list so it matches the relocated services regardless of how golangci-lint resolves paths when linting the `lib/services` module (module-relative vs. workspace-relative vs. absolute). Use:
   ```
   files:
     - "**/lib/services/**"
     - "stores/**"
     - "sensors/**"
     - "subscribers/**"
     - "executors/**"
   ```
   Drop the now-irrelevant `!stores/stub/**` and `!executors/stub/**` exclusions (the stub test doubles live in core's `test/support/`, never under `lib/services/`). The `stores/**` etc. globs still will not false-match core's `test/scenarios/stores/` (root-anchored; core's path is `test/scenarios/stores/`).
3. Rewrite the comment above the `consumption-side-isolation` rule: it is no longer a dormant "defensive guard against re-bundling with no local matches." State that `lib/services/` is the home of the bundled consumption-side services, that the module graph already prevents importing core internals (the services module requires only `lib/protocols`), and that this depguard rule is retained as defense-in-depth at the package level.
4. Leave the `consumption-side-isolation` `deny:` list as-is (it already denies `lib/foundation`, `lib/graph`, `lib/runtime`, `lib/control`, `cmd`).
5. In `pgx-isolation`, add one more exemption to its `files:` list so the atomic-staging integration test (which directly imports `github.com/jackc/pgx/v5/pgxpool` to drive a real Postgres) doesn't trip the rule. Append after the existing `!**/test/smoke/**` line:
   ```
   - "!**/lib/services/test/scenarios/**"
   ```
   Then extend the `desc:` on the `pkg: "github.com/jackc/pgx/v5"` deny entry to mention the new exemption alongside the existing ones (e.g. append `, and lib/services/test/scenarios/`).

**Verification:** `cd lib/services && golangci-lint run` (clean) and `make lint` (root + all modules clean)

### Task 11: Classify the new files in `licensing.yml` and reconcile headers

**Files:** `licensing.yml`, every Apache-headed Go file under `lib/services/` outside the claude-agent subtree (~87 files, rewritten by `make license-stamp`), `lib/services/internal/ops/ops.go` (package doc)

**Steps:**
1. Read `licensing.yml`.
2. Under `apache:`, add the TS executor (a separate Apache reference deliverable, outside the Go import graph):
   ```
   - lib/services/executors/claude-agent/   # TS executor reference impl; independently Apache-licensed
   ```
3. Under `agpl:`, add the services module (everything else it ships):
   ```
   - lib/services/
   ```
   Longest-prefix-match-wins means `lib/services/` → AGPL while `lib/services/executors/claude-agent/` → Apache. Per the user's licensing decision (the bundled services are an internal engineering effort; users who need a permissive license implement against `lib/protocols` themselves), every Go file under `lib/services/` outside the claude-agent subtree is classified AGPL.
4. Under `exempt:`, add the claude-agent build trees (so the walker never scans them; safe even when absent — exempt entries are not existence-checked):
   ```
   - lib/services/executors/claude-agent/node_modules/
   - lib/services/executors/claude-agent/dist/
   ```
5. Reconcile per-file headers to match the new classification: `make license-stamp`. **Scope note:** this is a substantial rewrite. The services repo's actual licensing differs from its README's claim — the production Go files (all of `stores/`, `sensors/`, `subscribers/openlineage/`, the three Go executors, and `internal/ops/`) carry Apache headers (~87 files), while only the `test/` subtree (~20 files) is already AGPL. Under the new agpl classification, `license-stamp` will replace the Apache header on every one of those 87 files with the AGPL header. The code bodies are not touched; only the per-file license header changes.
6. The package doc comment at the top of `lib/services/internal/ops/ops.go` is doubly stale after relicensing: it still describes the file as "the rimsky-services-local replacement for the former rimsky-internal sdk/go/ops" and references "rimsky's internal/ops" (which was deleted in the root-folder reorg). Replace the stale paragraph with a one-line description appropriate to the file's new home, e.g. "Package ops bundles the small slog-setup helper that every bundled-service binary in lib/services calls during startup."
7. Verify: `make license-lint` must report zero violations (correct headers + no Apache→AGPL import edge).

**Verification:** `make license-lint`

### Task 12: Re-add the services build-artifact patterns to `.gitignore`

**Files:** `.gitignore`

**Steps:**
1. Read `.gitignore`. It currently has a note that the bundled-services patterns "moved to sibling repos" and "no longer apply here."
2. Replace that stale note with live patterns for the relocated module:
   ```
   # Bundled-service node/TS build trees (lib/services/executors/claude-agent)
   lib/services/executors/*/node_modules/
   lib/services/executors/*/dist/

   # Stray ad-hoc service binaries (built without -o at a module root).
   # `/openlineage` is already present above (predates the carve-out); the
   # subscriber binary is named that by `go build .` in subscribers/openlineage/.
   /store-filesystem
   /store-postgres
   /sensor-cron
   /sensor-http
   /sensor-object-store
   /sensor-webhook
   /http-node
   /verifier-http
   /verifier-shape-checks
   ```
   Do **not** add `/subscriber-openlineage` — the actual binary `go build .` produces in `subscribers/openlineage/` is `openlineage` (the package name), and `/openlineage` is already in the existing `.gitignore` (it was left in place from the pre-carve era and remains accurate).
3. Confirm nothing under `lib/services` is currently staged that these patterns should exclude (`git status --porcelain lib/services | grep -E 'node_modules|/dist/'` prints nothing).

**Verification:** `git check-ignore lib/services/executors/claude-agent/node_modules/x lib/services/executors/claude-agent/dist/x` (both paths are reported as ignored)

---

## Pass 5: Rewrite Dockerfiles + repoint the harness onto local images

**Goal:** Make every service image build from the monorepo root (against in-tree `lib/protocols` via `go.work`), and make the integration harness consume locally-built image tags instead of Docker Hub / ghcr.
**Scope:** Tasks 13–17
**End state:** working
**Verification:** `cd lib/services && go build ./... && go vet ./...` plus the grep checks in Task 17

### Task 13: Rewrite the Go service Dockerfiles for the monorepo build context

**Files:** the 10 Go-service Dockerfiles + the stub-executor Dockerfile under `lib/services/`:
`stores/filesystem/Dockerfile.filesystem`, `stores/postgres/Dockerfile.postgres`, `sensors/sensor-cron/Dockerfile.sensor-cron`, `sensors/sensor-http/Dockerfile.sensor-http`, `sensors/sensor-object-store/Dockerfile.sensor-object-store`, `sensors/sensor-webhook/Dockerfile.sensor-webhook`, `subscribers/openlineage/Dockerfile.openlineage`, `executors/http-node/Dockerfile.http-node`, `executors/verifier-http/Dockerfile.verifier-http`, `executors/verifier-shape-checks/Dockerfile.verifier-shape-checks`, `test/stubexecutor/Dockerfile.stubexecutor`.

**Steps:**
1. For each Dockerfile, read it, then replace **only the builder stage's preamble** — the lines from the `COPY go.mod go.sum ./` (and any `RUN go mod download`) through `COPY . .` — with the monorepo manifest-copy pattern, and prefix the existing `go build` line with `cd lib/services && `. Leave the build-target path (e.g. `./stores/filesystem/cmd`), the `-o /out/...` name, and the entire final/runtime stage **exactly as they are** (the build target is already relative to the services module root, which is now `lib/services`).

   The builder-stage preamble becomes (header comment updated to say the build context is the rimsky-core repo root and the protocols module is resolved in-tree via `go.work`):
   ```dockerfile
   # syntax=docker/dockerfile:1.7
   #
   # Build context: the rimsky-core repo root. The bundled services build
   # against the in-tree lib/protocols module via go.work — no module proxy,
   # no published-tag pin.
   FROM golang:1.25-alpine AS builder
   WORKDIR /src
   COPY go.work go.work.sum ./
   COPY go.mod go.sum ./
   COPY lib/foundation/go.mod lib/foundation/go.sum ./lib/foundation/
   COPY lib/protocols/go.mod lib/protocols/go.sum ./lib/protocols/
   COPY lib/services/go.mod lib/services/go.sum ./lib/services/
   RUN cd lib/services && go mod download
   COPY . .
   RUN cd lib/services && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/<KEEP> ./<KEEP-target>
   ```
   where `<KEEP>` and `<KEEP-target>` are copied verbatim from the existing `go build` line in that Dockerfile.
2. After editing all of them, confirm each references the new preamble: `grep -L 'COPY lib/services/go.mod' lib/services/stores/*/Dockerfile.* lib/services/sensors/*/Dockerfile.* lib/services/subscribers/*/Dockerfile.* lib/services/executors/http-node/Dockerfile.* lib/services/executors/verifier-*/Dockerfile.* lib/services/test/stubexecutor/Dockerfile.*` should print nothing (every Go Dockerfile got the new line).

**Verification:** `grep -rl 'cd lib/services && CGO_ENABLED' lib/services --include='Dockerfile*' | wc -l` reports `11`

### Task 14: Rewrite the claude-agent Dockerfile for the monorepo context

**Files:** `lib/services/executors/claude-agent/Dockerfile`

**Steps:**
1. Read the Dockerfile. The build context becomes the rimsky-core repo root, and the build must preserve the repo's directory layout so the `file:` dependency (`../../../../lib/protocols`) resolves at install time.
2. Replace the builder stage with:
   ```dockerfile
   # syntax=docker/dockerfile:1.7
   #
   # Build context: the rimsky-core repo root. The executor resolves its protos
   # from the in-tree lib/protocols package (a file: dependency in
   # package.json), so the build must preserve the repo layout for npm to
   # resolve it.
   FROM node:20-alpine AS builder
   WORKDIR /build
   COPY lib/protocols/ ./lib/protocols/
   COPY lib/services/executors/claude-agent/package.json lib/services/executors/claude-agent/package-lock.json* ./lib/services/executors/claude-agent/
   WORKDIR /build/lib/services/executors/claude-agent
   RUN npm ci || npm install
   WORKDIR /build
   COPY lib/services/executors/claude-agent/ ./lib/services/executors/claude-agent/
   WORKDIR /build/lib/services/executors/claude-agent
   RUN npm run build
   ```
3. In the runtime stage, update the two `COPY --from=builder` paths to the new location:
   ```dockerfile
   COPY --from=builder /build/lib/services/executors/claude-agent/dist ./dist
   COPY --from=builder /build/lib/services/executors/claude-agent/node_modules ./node_modules
   ```
   Leave the rest of the runtime stage (the `@anthropic-ai/claude-code` global install, `tini`, `EXPOSE`, `ENTRYPOINT`) unchanged.

**Verification:** `grep -E 'COPY lib/protocols/|/build/lib/services/executors/claude-agent/dist' lib/services/executors/claude-agent/Dockerfile`

### Task 15: Point the harness at the locally-built all-in-one + fs-store images

**Files:** `lib/services/test/harness/rimsky.go`, `lib/services/test/harness/rimsky_test.go`, `lib/services/test/harness/store_filesystem.go`

**Steps:**
1. In `rimsky.go`, change the constant `rimskyAllImage = "rimskyai/rimsky-all-in-one:latest"` to `rimskyAllImage = "rimsky-all-in-one:latest"`. Update the surrounding doc comments (package doc and the bring-up comment) that reference pulling `rimskyai/rimsky-all-in-one:latest` "from Docker Hub" — they now reference the locally-built `rimsky-all-in-one:latest` image (built by `make core-images`).
2. In `rimsky_test.go`, line 16's doc comment also names `` `rimskyai/rimsky-all-in-one:latest` ``. Update it to `` `rimsky-all-in-one:latest` `` to match the const (this also satisfies Task 17's grep guard, which scans the whole `lib/services` tree for `rimskyai/` references).
3. In `store_filesystem.go`, change `storeFilesystemImage = "ghcr.io/rimsky-ai/rimsky-services/store-filesystem:latest"` to `storeFilesystemImage = "rimsky-store-filesystem:latest"`, and update the const's doc comment (it currently says "Built by `./deploy/build-images.sh` in the rimsky-services checkout") to "Built by `make service-images`."

**Verification:** `grep -n 'rimsky-all-in-one:latest\|rimsky-store-filesystem:latest' lib/services/test/harness/rimsky.go lib/services/test/harness/rimsky_test.go lib/services/test/harness/store_filesystem.go`

### Task 16: Repoint the stub-executor build context to the monorepo root

**Files:** `lib/services/test/harness/executor_stub.go`

**Steps:**
1. Read `executor_stub.go`. It builds the stub via `testcontainers.FromDockerfile{ Context: repoRoot(), Dockerfile: "test/stubexecutor/Dockerfile.stubexecutor", ... }`.
2. Find the `repoRoot()` helper (grep `func repoRoot` under `lib/services/test/harness/`). It must now resolve to the **rimsky-core** repo root (the directory containing `go.work`), so the Docker build context includes `lib/protocols` and `lib/services`. If `repoRoot()` currently walks up to the first `go.mod`, change it to walk up to the directory containing `go.work` (the workspace root). If it uses a fixed number of `..` segments, recompute for the new depth (`lib/services/test/harness/` → repo root is four levels up).
3. Update the `Dockerfile:` field to the path relative to that root: `"lib/services/test/stubexecutor/Dockerfile.stubexecutor"`.
4. Update the `Repo:` field in the same `FromDockerfile` struct from `"rimsky-services-test/stubexecutor"` to `"rimsky-test/stubexecutor"` (the image is now built within rimsky-core; the `rimsky-services-test/` prefix references a repo that no longer exists). This is the locally-built tag the harness reuses across test runs.
5. The stub-executor Dockerfile itself was already rewritten in Task 13 to `cd lib/services && go build ... ./test/stubexecutor`.

**Verification:** `cd lib/services && go build ./... && go vet ./...` (the harness compiles with the new context wiring)

### Task 17: Add a repo `.dockerignore` and confirm no external image references remain

**Files:** new `.dockerignore` (repo root)

**Steps:**
1. Create `.dockerignore` at the repo root so the now-larger build contexts (which `COPY . .`) stay lean and never copy host-only trees:
   ```
   .git
   **/node_modules
   **/dist
   .ok-planner
   bin
   tmp
   *.log
   coverage.out
   coverage.html
   ```
2. Confirm no Docker Hub / ghcr image references survive anywhere under `lib/services`:
   ```
   grep -rn 'rimskyai/\|ghcr.io/rimsky' lib/services
   ```
   This must print nothing (the only former references were the two harness constants and their doc comments, all repointed in Task 15).

**Verification:** `! grep -rqn 'rimskyai/\|ghcr.io/rimsky' lib/services && test -f .dockerignore && echo OK`

---

## Pass 6: Update the design concept, CLAUDE.md, and feature-index

**Goal:** Bring the durable docs into line with the four-module reality. (Design-doc edits ride in the same plan as the code per the workflow.)
**Scope:** Tasks 18–21
**End state:** working
**Verification:** `make build-all && make lint && make license-lint`

### Task 18: Update the `module-layout` concept

**Files:** `.ok-planner/design/concepts/module-layout.md`

**Steps:**
1. Read the concept. It currently describes a **three-module** workspace (protocols, foundation, root). Edit it to describe **four** modules, adding the services module. Keep the body **self-contained** — no file paths, no `pkg:`/`code:` citations, no quoted lint config. Use the existing prose register ("the protocols module," "the bundled services," "the lib group").
2. In "What it is," change "ties three modules into one build" to "four modules," and add a bullet describing the **services module** (under the lib group): the bundled consumption-side service implementations rimsky ships as images — claim-producer stores, sensors, subscribers, and executors. It depends only on the protocols module (and carries its own heavier integration-test dependency set); it is never imported back into the layered packages. Its isolation from the rimsky-internal layers is now enforced twice: by the module graph (it requires only the protocols module) and, as defense-in-depth, by the consumption-side-isolation lint rule.
3. In "Boundaries," change "the three-module workspace (protocols, foundation, root)" to "the four-module workspace (protocols, foundation, services, root)," and note that the layout owns the bundled-services module home under the lib group.
4. In the "Invariants" entry for consumption-side-isolation: it currently says the rule was a defensive guard with no local matches after the bundled deliverables moved out. Rewrite it: the bundled services now live in-tree (the services module); the rule actively denies them from importing any rimsky-internal layer, and the module graph reinforces the same boundary. Remove the now-obsolete sentence about the in-repo stub exemptions being part of this rule (the stubs are test-support, never under the services module).
5. In "Licensing boundary": note that the bundled services are AGPL like the rest of what rimsky ships, with one carve-out — the TypeScript executor reference implementation is independently Apache-licensed and sits outside the Go import graph, so the single Go Apache island remains the protocols module.
6. Append a Notes entry:
   ```
   - 2026-05-27 (spec: none — services reintegration): the bundled consumption-side services (stores, sensors, subscribers, executors), carved out on 2026-05-24, were pulled back in as a fourth workspace module under the lib group. They build against the in-tree protocols module via the workspace; the former published-module pin, the npm protocols-package dependency, and the external image references were all removed. Consumption-side-isolation is now enforced both by the module graph and the lint rule. The production Go services were relicensed Apache to AGPL on reintegration (an internal engineering effort, not a permissive deliverable; downstream consumers seeking permissive code implement against the protocols module themselves); the TS executor reference implementation stays Apache as a separate deliverable outside the Go import graph; the single Go Apache island remains the protocols module.
   ```

**Verification:** `! grep -nE '\.go|/[a-z]+/[a-z]+\.|pkg:|code:' .ok-planner/design/concepts/module-layout.md` returns no path-like citations introduced into the prose body (manual scan acceptable); `make build-all`

### Task 19: Update the concepts TOC line for `module-layout`

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Read the `module-layout` bullet in `.ok-planner/design/concepts.md`.
2. Update its one-sentence definition from "ties three modules into one build" to "four modules" (matching the concept edit). (The execute-plan run also regenerates this TOC at the end; this keeps it correct in the interim.)

**Verification:** `grep 'module-layout' .ok-planner/design/concepts.md`

### Task 20: Update `CLAUDE.md`

**Files:** `CLAUDE.md`

**Steps:**
1. Read `CLAUDE.md`. Update the "Where to look first" → module-layout pointer: the workspace now ties **four** modules (root, `lib/foundation`, `lib/protocols`, `lib/services`); `lib/services` holds the bundled consumption-side services (stores, sensors, subscribers, executors) shipped as images, isolated from core internals.
2. Add a cross-cutting gotcha under "Cross-cutting gotchas": the integration harness under `lib/services/test/` drives a real rimsky stack via testcontainers and consumes **locally-built** images — run `make core-images` (for `rimsky-all-in-one:latest`) and `make service-images` (for the `rimsky-store-filesystem:latest` peer image, etc.) before `make test-all` or the services scenario/smoke tests; nothing is pulled from a registry.
3. In the build-commands area, note that `make build-all`/`test-all`/`lint` now also cover the `lib/services` module, and `make service-images` builds the bundled-service images.

**Verification:** `grep -n 'lib/services' CLAUDE.md`

### Task 21: Add a bundled-services section to `feature-index.md`

**Files:** `feature-index.md`

**Steps:**
1. Read `feature-index.md` (it indexes features by layer in terse tables).
2. Add a new section `## Bundled services (lib/services/)` with a short intro (these are consumption-side services shipped as images; depend only on the protocols module; isolated from core internals) and a table with one row per service group (stores filesystem, stores postgres, sensor-cron, sensor-http, sensor-object-store, sensor-webhook, subscriber-openlineage, executor http-node, executor verifier-http, executor verifier-shape-checks, executor claude-agent), each with its `lib/services/...` path and a one-line purpose. Keep entries terse, matching the file's existing style.

**Verification:** `grep -n 'Bundled services' feature-index.md && make lint`

---

## Manual checks after completion

These require a Docker daemon (and, for the image builds, several minutes) and are not part of the autonomous run. Run them after the implementation and review are complete:

1. **Build all images locally:** `make core-images && make service-images`. Confirm `docker images | grep -E 'rimsky-all-in-one|rimsky-store-filesystem|rimsky-sensor-|rimsky-subscriber-|rimsky-executor-'` lists the expected tags.
2. **Run the full multi-module test suite:** `make test-all` (covers the `lib/services` integration scenarios/smoke, which spin up `rimsky-all-in-one:latest` + the peer images + the stub executor via testcontainers). Requires step 1 first.
3. **Run the smoke suite:** `make smoke-all`.
4. **Conformance (optional):** for any executor touched, `go run ./cmd/rimsky conformance executor --endpoint <built-image-endpoint> --transport grpc`.
5. **Commit message lineage** (when you commit — the run does not commit): record the provenance so the deep history stays discoverable, e.g.:
   > Reintegrate bundled services under lib/services/. Re-imported from rimsky-services @ `9990b6c`. These services last lived in-tree at `stores/ sensors/ subscribers/ executors/` before carve-out `c1ce756` (2026-05-24); pre-carve history: `git log c1ce756^ -- <path>`.
