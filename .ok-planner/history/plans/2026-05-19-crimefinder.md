# Crimefinder Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-19-crimefinder-design.md`
**Goal:** Build crimefinder — a code-review-as-rimsky-graph tool consisting of a custom executor (host process), a custom claim-producer (containerized), template YAML, and a thin CLI wrapper — landing under `apps/crimefinder/` in the rimsky repo.
**Architecture:** Two TypeScript services + template YAML + CLI. The producer (containerized, bind-mounts the host repo) implements `concept:claim-producer` gRPC plus a tool-specific `CrimefinderState` typed gRPC service. The executor (host process) implements `concept:executor` gRPC and spawns Claude CLI as a subprocess, hosting an internal MCP server that exposes `review_*` gates. The gates delegate to producer-typed RPCs for all state mutations. Rimsky orchestrates fan-out, claim lifecycle, retries, and cascade.
**Tech Stack:** TypeScript (Node 20+), `@grpc/grpc-js`, `@grpc/proto-loader`, `@modelcontextprotocol/sdk`, `fastify`, `pino`, `zod`, `vitest`, `testcontainers` (for scenario tests).

---

## Decision context (carried over from the spec)

The implementer starts a fresh session and has not read the spec. Before any task, read `.ok-planner/specs/2026-05-19-crimefinder-design.md` end-to-end. The points below are the load-bearing decisions the spec makes; this plan implements them but does not re-litigate them.

**Service shape:**
- **Producer is one process speaking two gRPC services** on the same listener: the standard `rimsky.v1.ClaimProducer` (Capabilities / Open / Commit / Abandon / Release / SplitScope / ScopesConflict) AND a crimefinder-internal `crimefinder.v1.CrimefinderState` service (AppendFinding / QueryFindings / UpdateFindingStatus / AppendCoverage / RunTests / CommitFix / DeferFinding / SkipZone / RequestHelp / AggregateFindings). The typed service's endpoint is carried in the `ClaimResult.address` bytes the producer hands back from `Open`.
- **Executor is one process speaking one gRPC service** (`rimsky.v1.Executor`) and hosting an internal HTTP+JSON-RPC MCP server on a loopback port that the spawned Claude CLI subprocess dials. Async-callback handoff pattern (mirror `dir:executors/claude-agent/`).

**Findings live in the repo, not in rimsky Postgres.** `.crimefinder/findings.jsonl`, `.crimefinder/coverage.jsonl`, `.crimefinder/passes.jsonl` are git-tracked append-only JSONL. Rimsky Postgres holds only orchestration state (`rimsky_node_runs`, `rimsky_claim_handles`, `rimsky_events`).

**Atomicity:** `review_commit_fix` flows as commit-then-append-then-recovery-scan, all inside the producer's commit-mutex. The git commit's message footer carries `Resolves: <finding_id>`; if the JSONL append fails post-commit, recovery scan on producer startup walks `git log` and reconstructs missing rows.

**Class-5b auto-routing in `AppendFinding`:** when a class-1-4 finding's `concept_slug` references a `concepts/<slug>.md` file whose `Boundaries:` or `Invariants:` sections the description does not contain a contiguous 8-token verbatim quote from, the row is rewritten with `class:"5b"` and `auto_rerouted:true` before insertion.

**Zone partitioning:** `SplitScope` lifts the algorithm from `code:../../../crimefinder/src/features/zones/partition.ts::partitionIntoZones`. Sub-claims have logical zone scope (file lists); `ScopesConflict` returns `conflict:true` only when sub-paths overlap.

**Iteration:** `iter_num` is NOT carried via template substitution. The producer tracks per-pass iteration count internally and returns it in the `@unresolved-class-1-4:pass_id=X` claim payload. The template statically declares three `fix-iter-N` nodes.

**Template grammar note:** the spec uses `claims:` in YAML examples per the design docs. The current rimsky parser still uses `stores:` (`code:foundation/spec/template.go::TemplateNodeDef.Stores`). The template YAML file in this plan **emits `stores:`** (matching the parser); other plan prose may reference `claims:` per the design-doc vocabulary — both refer to the same template directive.

**Substitution grammar:** rimsky's substitution accepts only `{{nodes.X.attribute.Y}}`, `{{nodes.X.event.E.Y}}`, `{{claim.A.address|payload.Y|scope}}`, and `{{params.Y}}` — **no `{{userdata.*}}` or `{{instance.*}}`** per `code:graph/attribute/substitution.go`.

**Deployment:** rimsky stack + crimefinder-producer in Docker; crimefinder-executor + CLI on the host. Bind-mount the host repo at identical absolute paths so producer (in container) and Claude CLI (host) agree on file paths.

**Auth (executor):** `env:ANTHROPIC_API_KEY` (production, written to 0600 temp settings file with `apiKeyHelper`) OR `env:CLAUDE_CODE_OAUTH_TOKEN` (dev, env passthrough). Pattern lifted from `dir:executors/claude-agent/src/cli-env.ts`.

**Prototype lineage:** Several files are ports of `dir:/Users/patrick/Documents/projects/research/crimefinder/src/` (the prototype). They carry `@source:` annotations and (where divergent) `@diverged:` markers. The prototype is read-only.

---

## File map

Every file the plan creates or modifies:

### Top-level scaffolding (under `apps/crimefinder/`)
- `apps/crimefinder/package.json` (new) — workspace root, declares scripts and dependency unification across executor/producer/cli
- `apps/crimefinder/tsconfig.base.json` (new) — shared TS config
- `apps/crimefinder/.gitignore` (new) — excludes `node_modules/`, `dist/`, `.crimefinder/` (in any descendant test repo)
- `apps/crimefinder/CHANGELOG.md` (new)
- `apps/crimefinder/CLAUDE.md` (new)
- `apps/crimefinder/README.md` (new)
- `apps/crimefinder/feature-index.md` (new)
- `apps/crimefinder/cold-read/README.md` (new) — pointer to `cold-read/` at rimsky root

### Shared types package (`apps/crimefinder/shared/`)
- `apps/crimefinder/shared/package.json`
- `apps/crimefinder/shared/tsconfig.json`
- `apps/crimefinder/shared/src/index.ts` — re-exports
- `apps/crimefinder/shared/src/jsonl-rows.ts` — finding / status_update / tension_confirmation / help_request / coverage / pass row schemas (zod)
- `apps/crimefinder/shared/src/gate-io.ts` — gate input/output types
- `apps/crimefinder/shared/src/error-classes.ts` — error class taxonomy (gate-level + executor-level)
- `apps/crimefinder/shared/src/scope-addresses.ts` — typed scope address shapes
- `apps/crimefinder/shared/src/named-events.ts` — named-event payload shapes
- `apps/crimefinder/shared/src/ids.ts` — base32 ID generation (lifted from prototype `../crimefinder/src/shared/ids.ts`)
- `apps/crimefinder/shared/src/fingerprint.ts` — fingerprint normalization
- co-located `*_test.ts` for each file above

### Producer (`apps/crimefinder/producer/`)
- `apps/crimefinder/producer/package.json`
- `apps/crimefinder/producer/tsconfig.json`
- `apps/crimefinder/producer/src/proto-loader.ts` — load ClaimProducer + CrimefinderState protos
- `apps/crimefinder/producer/src/main.ts` — entry point
- `apps/crimefinder/producer/src/server.ts` — gRPC server bootstrap
- `apps/crimefinder/producer/src/capabilities.ts` — Capabilities handler
- `apps/crimefinder/producer/src/jsonl-store.ts` — append-and-query for findings / coverage / passes
- `apps/crimefinder/producer/src/jsonl-mutex.ts` — single-writer mutex
- `apps/crimefinder/producer/src/git-ops.ts` — git add / commit / status / log
- `apps/crimefinder/producer/src/zones/partition.ts` — `@source:` lift
- `apps/crimefinder/producer/src/zones/coverage.ts` — `@source:` lift
- `apps/crimefinder/producer/src/dedup/group.ts` — `@source:` lift
- `apps/crimefinder/producer/src/dedup/resolve.ts` — `@source:` lift
- `apps/crimefinder/producer/src/claim-producer/open.ts` — Open dispatch by selector
- `apps/crimefinder/producer/src/claim-producer/commit.ts` — Commit handler
- `apps/crimefinder/producer/src/claim-producer/abandon.ts` — Abandon handler
- `apps/crimefinder/producer/src/claim-producer/release.ts` — Release handler
- `apps/crimefinder/producer/src/claim-producer/split-scope.ts` — SplitScope by partition_request kind
- `apps/crimefinder/producer/src/claim-producer/scopes-conflict.ts` — non-overlapping sub-path detector
- `apps/crimefinder/producer/src/scopes/pass-state.ts` — `@pass-state:new` Open handler
- `apps/crimefinder/producer/src/scopes/context-scan.ts` — `@context-scan:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/source-tree.ts` — `@source-tree:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/aggregate-findings.ts` — `@aggregate-findings:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/dedup-grouping.ts` — `@dedup-grouping:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/class-split.ts` — `@class-split:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/unresolved-class-1-4.ts` — `@unresolved-class-1-4:pass_id=X` Open handler with iteration counter
- `apps/crimefinder/producer/src/scopes/fix-partition.ts` — `@fix-partition:pass_id=X&iter_num=N` Open handler
- `apps/crimefinder/producer/src/scopes/re-review-partition.ts` — `@re-review-partition:...` Open handler
- `apps/crimefinder/producer/src/scopes/iter-aggregate.ts` — `@iter-aggregate:...` Open handler
- `apps/crimefinder/producer/src/scopes/class-5-finalize.ts` — `@class-5-finalize:pass_id=X` Open handler
- `apps/crimefinder/producer/src/scopes/report.ts` — `@report:pass_id=X` Open handler
- `apps/crimefinder/producer/src/state/append-finding.ts` — CrimefinderState.AppendFinding (with class-5b auto-routing)
- `apps/crimefinder/producer/src/state/query-findings.ts` — CrimefinderState.QueryFindings
- `apps/crimefinder/producer/src/state/update-status.ts` — CrimefinderState.UpdateFindingStatus
- `apps/crimefinder/producer/src/state/append-coverage.ts` — CrimefinderState.AppendCoverage
- `apps/crimefinder/producer/src/state/run-tests.ts` — CrimefinderState.RunTests (with mtime cache)
- `apps/crimefinder/producer/src/state/commit-fix.ts` — CrimefinderState.CommitFix (atomic)
- `apps/crimefinder/producer/src/state/defer-finding.ts` — CrimefinderState.DeferFinding
- `apps/crimefinder/producer/src/state/skip-zone.ts` — CrimefinderState.SkipZone
- `apps/crimefinder/producer/src/state/request-help.ts` — CrimefinderState.RequestHelp
- `apps/crimefinder/producer/src/state/aggregate-findings.ts` — CrimefinderState.AggregateFindings
- `apps/crimefinder/producer/src/state/class-5b-rule.ts` — server-side concept-citation rule
- `apps/crimefinder/producer/src/state/iteration-counter.ts` — per-pass iter_num tracker
- `apps/crimefinder/producer/src/state/session-tokens.ts` — one-time bearer token registry
- `apps/crimefinder/producer/src/state/test-cache.ts` — RunTests result cache keyed by tree mtime
- `apps/crimefinder/producer/src/recovery/startup-scan.ts` — walk git log on boot, append missing JSONL rows
- `apps/crimefinder/producer/src/config.ts` — reads `.crimefinder/config.yml` from the bind-mounted repo
- `apps/crimefinder/producer/src/concepts/parser.ts` — extracts Boundaries:/Invariants: sections from `concepts/<slug>.md`
- `apps/crimefinder/producer/src/concepts/scanner.ts` — scans `@concept:` annotations
- `apps/crimefinder/producer/src/health.ts` — HTTP `/health`
- co-located `*_test.ts` for each file above

### Executor (`apps/crimefinder/executor/`)
- `apps/crimefinder/executor/package.json`
- `apps/crimefinder/executor/tsconfig.json`
- `apps/crimefinder/executor/src/proto-loader.ts` — load Executor + ExecutorObservability protos
- `apps/crimefinder/executor/src/main.ts` — entry point
- `apps/crimefinder/executor/src/server.ts` — gRPC server bootstrap, async-callback handoff
- `apps/crimefinder/executor/src/capabilities.ts` — declared_events list and Capabilities response
- `apps/crimefinder/executor/src/userdata-schema.ts` — JSON Schema for userdata
- `apps/crimefinder/executor/src/agent-run.ts` — orchestration: spawn Claude CLI, run MCP server, wait, callback
- `apps/crimefinder/executor/src/cli-runner.ts` — subprocess spawn (lifted from claude-agent shape)
- `apps/crimefinder/executor/src/cli-env.ts` — auth precedence (lifted from `executors/claude-agent/src/cli-env.ts`)
- `apps/crimefinder/executor/src/silence-watch.ts` — silence detector
- `apps/crimefinder/executor/src/internal-mcp-server.ts` — loopback HTTP-JSON-RPC server for gates
- `apps/crimefinder/executor/src/internal-mcp-tools.ts` — tool definitions and zod input schemas
- `apps/crimefinder/executor/src/gates/review-context.ts`
- `apps/crimefinder/executor/src/gates/review-finding.ts`
- `apps/crimefinder/executor/src/gates/review-coverage.ts`
- `apps/crimefinder/executor/src/gates/review-complete.ts`
- `apps/crimefinder/executor/src/gates/review-run-tests.ts`
- `apps/crimefinder/executor/src/gates/review-commit-fix.ts`
- `apps/crimefinder/executor/src/gates/review-defer.ts`
- `apps/crimefinder/executor/src/gates/review-skip-zone.ts`
- `apps/crimefinder/executor/src/gates/review-request-help.ts`
- `apps/crimefinder/executor/src/state-client.ts` — gRPC client for CrimefinderState
- `apps/crimefinder/executor/src/prompt-loader.ts` — reads `userdata.system_prompt` and `userdata.user_prompt_template` from the dispatched node; falls back to bundled defaults only if a template doesn't supply them
- (prompts themselves live in `apps/crimefinder/templates/prompts/` — see "Templates" section below — and are referenced from the template via the new `source_file:` resolution that landed in spec `2026-05-19-multi-instance-template-ergonomics-design.md`)
- `apps/crimefinder/executor/src/stub-mode.ts`
- `apps/crimefinder/executor/src/token-registry.ts` — per-run token for MCP auth (lifted shape from claude-agent)
- `apps/crimefinder/executor/src/observability.ts` — pino logger config
- co-located `*_test.ts` for each file above

### Proto (`apps/crimefinder/proto/`)
- `apps/crimefinder/proto/v1/crimefinder_state.proto` — typed-data service definition

### CLI (`apps/crimefinder/cli/`)
- `apps/crimefinder/cli/package.json`
- `apps/crimefinder/cli/tsconfig.json`
- `apps/crimefinder/cli/src/main.ts` — entry point, argv dispatch
- `apps/crimefinder/cli/src/commands/pass.ts` — `crimefinder pass`
- `apps/crimefinder/cli/src/commands/status.ts` — `crimefinder status`
- `apps/crimefinder/cli/src/commands/up.ts` — `crimefinder up`
- `apps/crimefinder/cli/src/commands/down.ts` — `crimefinder down`
- `apps/crimefinder/cli/src/rimsky-cli.ts` — thin wrapper to invoke `rimsky template register|deploy|instance create`
- co-located `*_test.ts`

### Templates (`apps/crimefinder/templates/`)
- `apps/crimefinder/templates/code-review-pass.yml`
- `apps/crimefinder/templates/validate.mjs` — node-uniqueness + sub-graph encapsulation checker (described in T47)
- `apps/crimefinder/templates/prompts/review-zone.system.md`
- `apps/crimefinder/templates/prompts/review-zone.user.md`
- `apps/crimefinder/templates/prompts/fix.system.md`
- `apps/crimefinder/templates/prompts/fix.user.md`
- `apps/crimefinder/templates/prompts/re-review.system.md`
- `apps/crimefinder/templates/prompts/re-review.user.md`
- `apps/crimefinder/templates/prompts/dedup.system.md`
- `apps/crimefinder/templates/prompts/dedup.user.md`

### Deploy (`apps/crimefinder/deploy/`)
- `apps/crimefinder/deploy/Dockerfile.producer`
- `apps/crimefinder/deploy/docker-compose.fragment.yml` — service block to merge into consumer compose
- `apps/crimefinder/deploy/rimsky.yml.fragment` — `executors:` and `claim_producers:` entries for consumer rimsky.yml

### Tests (`apps/crimefinder/test/`)
- `apps/crimefinder/test/scenarios/harness.ts` — testcontainers-driven harness
- `apps/crimefinder/test/scenarios/full-pass-stub.test.ts`
- `apps/crimefinder/test/scenarios/multi-zone-concurrency.test.ts`
- `apps/crimefinder/test/scenarios/fix-cycle-iteration.test.ts`
- `apps/crimefinder/test/scenarios/crash-recovery.test.ts`
- `apps/crimefinder/test/scenarios/cross-zone-finding.test.ts`
- `apps/crimefinder/test/scenarios/rediscovery-dedup.test.ts`
- `apps/crimefinder/test/scenarios/tension-confirmation.test.ts`
- `apps/crimefinder/test/scenarios/class-5b-routing.test.ts`
- `apps/crimefinder/test/scenarios/coverage-threshold.test.ts`
- `apps/crimefinder/test/scenarios/fixtures/` — small repo fixtures with intentional findings
- `apps/crimefinder/test/e2e/smoke.test.ts` — gated (skipped by default)

---

## Tasks

The implementer should work through tasks in order. Each task has explicit numbered steps. Verification at the end of each task is a command to run and read.

Throughout the plan, "the prototype" refers to `/Users/patrick/Documents/projects/research/crimefinder/`. Read it but do not modify it.

---

### T1. Bootstrap the `apps/crimefinder/` subfolder

**Files:**
- `apps/crimefinder/package.json`
- `apps/crimefinder/tsconfig.base.json`
- `apps/crimefinder/.gitignore`

**Steps:**

1. Create the directory: `mkdir -p apps/crimefinder`.

2. Write `apps/crimefinder/package.json`:

   ```json
   {
     "name": "@crimefinder/workspace",
     "version": "0.1.0",
     "private": true,
     "license": "Apache-2.0",
     "type": "module",
     "workspaces": [
       "shared",
       "proto",
       "producer",
       "executor",
       "cli",
       "test"
     ],
     "scripts": {
       "build": "npm run build --workspaces --if-present",
       "test": "npm run test --workspaces --if-present",
       "lint": "npm run lint --workspaces --if-present",
       "typecheck": "npm run typecheck --workspaces --if-present"
     },
     "devDependencies": {
       "typescript": "^5.3.2",
       "vitest": "^1.0.4",
       "@types/node": "^20.10.0",
       "eslint": "^8.55.0",
       "@typescript-eslint/parser": "^6.13.1",
       "@typescript-eslint/eslint-plugin": "^6.13.1"
     }
   }
   ```

3. Write `apps/crimefinder/tsconfig.base.json` (mirrors `executors/claude-agent/tsconfig.json` with workspace adjustments):

   ```json
   {
     "compilerOptions": {
       "target": "ES2022",
       "module": "NodeNext",
       "moduleResolution": "NodeNext",
       "lib": ["ES2022"],
       "types": ["node"],
       "strict": true,
       "noImplicitAny": true,
       "esModuleInterop": true,
       "forceConsistentCasingInFileNames": true,
       "skipLibCheck": true,
       "declaration": true,
       "sourceMap": true,
       "resolveJsonModule": true
     }
   }
   ```

4. Write `apps/crimefinder/.gitignore`:

   ```
   node_modules/
   dist/
   *.tsbuildinfo
   .DS_Store
   coverage/
   ```

5. Create the workspace subdirectories: `mkdir -p apps/crimefinder/{shared,proto,producer,executor,cli,templates,deploy,test,cold-read}`.

6. Write **stub `package.json` files for every workspace member** so that the workspace root's `npm install` (run in T2 step 5) doesn't fail with `EWORKSPACES: workspace not found`. Each stub gets fleshed out in its dedicated task.

   `apps/crimefinder/shared/package.json` (stub; T2 replaces with full content):
   ```json
   { "name": "@crimefinder/shared", "version": "0.1.0", "private": true, "type": "module" }
   ```

   `apps/crimefinder/proto/package.json` (stub; T9 replaces):
   ```json
   { "name": "@crimefinder/proto", "version": "0.1.0", "private": true }
   ```

   `apps/crimefinder/producer/package.json` (stub; T10 replaces):
   ```json
   { "name": "@crimefinder/producer", "version": "0.1.0", "private": true, "type": "module" }
   ```

   `apps/crimefinder/executor/package.json` (stub; T35 replaces):
   ```json
   { "name": "@crimefinder/executor", "version": "0.1.0", "private": true, "type": "module" }
   ```

   `apps/crimefinder/cli/package.json` (stub; T46 replaces):
   ```json
   { "name": "@crimefinder/cli", "version": "0.1.0", "private": true, "type": "module" }
   ```

   `apps/crimefinder/test/package.json` (stub; T48 replaces):
   ```json
   { "name": "@crimefinder/test", "version": "0.1.0", "private": true, "type": "module" }
   ```

**Verification:**
```
ls apps/crimefinder/ && cat apps/crimefinder/package.json | node -e "JSON.parse(require('fs').readFileSync(0))" && echo OK
```
Expect the directory listing plus `OK`. JSON parse failure means the package.json is malformed.

---

### T2. Shared package skeleton

**Files:**
- `apps/crimefinder/shared/package.json`
- `apps/crimefinder/shared/tsconfig.json`
- `apps/crimefinder/shared/src/index.ts`

**Steps:**

1. Write `apps/crimefinder/shared/package.json`:

   ```json
   {
     "name": "@crimefinder/shared",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "main": "./dist/index.js",
     "types": "./dist/index.d.ts",
     "scripts": {
       "build": "tsc",
       "test": "vitest run",
       "typecheck": "tsc --noEmit",
       "lint": "eslint src"
     },
     "dependencies": {
       "zod": "^3.22.4"
     }
   }
   ```

2. Write `apps/crimefinder/shared/tsconfig.json`:

   ```json
   {
     "extends": "../tsconfig.base.json",
     "compilerOptions": {
       "outDir": "./dist",
       "rootDir": "./src"
     },
     "include": ["src/**/*.ts"],
     "exclude": ["dist", "node_modules", "**/*_test.ts"]
   }
   ```

3. Create the src directory: `mkdir -p apps/crimefinder/shared/src`.

4. Write a placeholder `apps/crimefinder/shared/src/index.ts`:

   ```ts
   // Re-exports for shared types. Populated in subsequent tasks.
   export {};
   ```

5. Install dependencies: `cd apps/crimefinder && npm install` (or `npm install --workspaces`). This populates the workspace `node_modules`.

**Verification:**
```
cd apps/crimefinder/shared && npx tsc --noEmit
```
Expect zero output and exit code 0.

---

### T3. Shared: ID generation

**Files:**
- `apps/crimefinder/shared/src/ids.ts`
- `apps/crimefinder/shared/src/ids_test.ts`

**Background:** The prototype has TWO source files contributing to crimefinder's ID generators. `generateIssueId`, `generateNonce`, `generateHuntId`, `generateSessionId` live in `code:/Users/patrick/Documents/projects/research/crimefinder/src/shared/ids.ts`. `generateZoneId` lives in `code:/Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition.ts:31`. We consolidate both into one `shared/src/ids.ts` in crimefinder, with `@source:` annotations citing both origin paths.

**Steps:**

1. Read both prototype sources:
   - `cat /Users/patrick/Documents/projects/research/crimefinder/src/shared/ids.ts`
   - `sed -n '25,40p' /Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition.ts`

2. Write `apps/crimefinder/shared/src/ids.ts`. Required exports:

   - `generatePassId(): string` — returns `"p_" + <24 base32 chars random>`.
   - `generateFindingId(): string` — returns `"f_" + <24 base32 chars random>`.
   - `generateZoneId(label: string): string` — returns `"z_" + <first 12 chars of base32(sha256(label))>` (deterministic). **Lifted from the prototype's `partition.ts`, consolidated here for cohesion with the other ID generators.**
   - `generateRowId(): string` — returns 24 random base32 chars (for status_update / coverage / help_request rows).
   - `generateSessionToken(): string` — returns 32 random base32 chars (for producer-issued bearer tokens).

   Base32 alphabet: RFC 4648 lower-case (`abcdefghijklmnopqrstuvwxyz234567`). Use `node:crypto`'s `randomBytes` for entropy. For the deterministic zone ID, use `crypto.createHash('sha256').update(label).digest()` and base32-encode the first 8 bytes (gives ~13 chars; slice to 12).

   Prepend the `@source:` annotation:

   ```ts
   /**
    * @source: /Users/patrick/Documents/projects/research/crimefinder/src/shared/ids.ts
    *          /Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition.ts (for generateZoneId)
    * @diverged: true
    * @reason: consolidated generateZoneId (originally in partition.ts) into
    *          the shared ID-generator module; extended ID set (added pass /
    *          row / session-token prefixes); base32 charset normalized to
    *          RFC4648 lower-case.
    */
   ```

   T12 (the partition.ts lift) will import `generateZoneId` from this module rather than re-implementing it.

3. Write `apps/crimefinder/shared/src/ids_test.ts` with vitest tests:
   - `generatePassId()` returns 26-char string starting with `p_`.
   - `generateFindingId()` returns 26-char string starting with `f_`.
   - `generateZoneId(label)` is deterministic (same label → same ID across calls).
   - `generateZoneId("a") !== generateZoneId("b")`.
   - Generated IDs from `generateRowId()` are unique across 1000 calls.

4. Add `apps/crimefinder/shared/vitest.config.ts` (vitest's default `include` is `**/*.test.ts` with a dot; we use `*_test.ts` per the cold-read convention, so an explicit `include` is REQUIRED — without it, `npm run test` reports zero tests and silently passes):

   ```ts
   import { defineConfig } from "vitest/config";
   export default defineConfig({
     test: { include: ["src/**/*_test.ts"] },
   });
   ```

   The same `vitest.config.ts` (with appropriate `testTimeout` adjustments where noted) gets added to every workspace that runs tests: `producer/` (T10), `executor/` (T35), `cli/` (T46), `test/` (T48). Each of those task lists includes adding this file as an explicit step.

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run
```
All ids tests pass.

---

### T4. Shared: fingerprint normalization

**Files:**
- `apps/crimefinder/shared/src/fingerprint.ts`
- `apps/crimefinder/shared/src/fingerprint_test.ts`

**Steps:**

1. Write `apps/crimefinder/shared/src/fingerprint.ts`. Required export:

   ```ts
   export function computeFingerprint(args: {
     file: string;
     symbol?: string;
     description: string;
   }): string;
   ```

   The function:
   - Normalizes the description by: (a) lowercasing, (b) stripping `*`, `_`, and backticks, (c) regex-replacing contiguous digit runs with `<num>`, (d) regex-replacing `0x[0-9a-f]+` with `<hex>`, (e) regex-replacing UUIDs (`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`) with `<uuid>`, (f) collapsing whitespace runs to a single space, (g) trimming.
   - Builds the canonical input: `file + "|" + (symbol or "") + "|" + normalizedDescription`.
   - Returns `"sha256:" + hex(sha256(canonicalInput))`.

2. Write `apps/crimefinder/shared/src/fingerprint_test.ts`:
   - Round-trip: same inputs → same fingerprint.
   - Drift case: changing line numbers in description does NOT change fingerprint (since digits are normalized to `<num>`).
   - Different files → different fingerprints.
   - Different symbols → different fingerprints (case-sensitive).
   - Description with only emphasis differences (e.g. `**bug**` vs `bug`) → same fingerprint.
   - Description with different hex addresses → same fingerprint.
   - Description with different UUIDs → same fingerprint.

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run src/fingerprint_test.ts
```
All fingerprint tests pass.

---

### T5. Shared: error-class taxonomy

**Files:**
- `apps/crimefinder/shared/src/error-classes.ts`
- `apps/crimefinder/shared/src/error-classes_test.ts`

**Steps:**

1. Write `apps/crimefinder/shared/src/error-classes.ts`. Required exports:

   ```ts
   // Gate-level error classes (returned in MCP error envelopes to Claude CLI).
   export const GATE_ERROR_CLASSES = [
     "finding_not_found",
     "finding_already_resolved",
     "working_tree_clean",
     "working_tree_changes_out_of_scope",
     "tests_not_recent",
     "tests_failed",
     "test_command_not_configured",
     "coverage_below_threshold",
     "unresolved_findings_in_flight",
     "concept_citation_missing",
     "commit_failed",
     "tension_already_cataloged",
   ] as const;
   export type GateErrorClass = (typeof GATE_ERROR_CLASSES)[number];

   // Executor-level error classes (Error{error_class: ...} on the
   // executor protocol terminal; consumed by template error_types: keys).
   export const EXECUTOR_ERROR_CLASSES = [
     "silence_timeout",
     "tool_error",
     "commit_failed",
     "tests_failed",
   ] as const;
   export type ExecutorErrorClass = (typeof EXECUTOR_ERROR_CLASSES)[number];

   export interface GateErrorEnvelope {
     code: number; // always -32000 (MCP application error)
     message: string;
     data: {
       crimefinder_error_class: GateErrorClass;
       retryable: boolean;
       [k: string]: unknown; // class-specific extras
     };
   }

   export function makeGateError(
     cls: GateErrorClass,
     message: string,
     retryable: boolean,
     extras?: Record<string, unknown>,
   ): GateErrorEnvelope;
   ```

2. Write tests verifying the envelope shape, retryable flag, extras propagation.

3. Update `apps/crimefinder/shared/src/index.ts` to re-export ids, fingerprint, error-classes (and subsequent shared modules; keep updating as they land).

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run src/error-classes_test.ts && npx tsc --noEmit
```

---

### T6. Shared: JSONL row schemas

**Files:**
- `apps/crimefinder/shared/src/jsonl-rows.ts`
- `apps/crimefinder/shared/src/jsonl-rows_test.ts`

**Steps:**

1. Write `apps/crimefinder/shared/src/jsonl-rows.ts`. Define zod schemas and TypeScript types for each row kind (full schemas in spec §"Data formats" → "`.crimefinder/findings.jsonl`" / coverage / passes). Required exports:

   ```ts
   export const FindingRowSchema = z.object({ /* kind:"finding" shape */ });
   export const StatusUpdateRowSchema = z.object({ /* kind:"status_update" */ });
   export const TensionConfirmationRowSchema = z.object({ /* kind:"tension_confirmation" */ });
   export const HelpRequestRowSchema = z.object({ /* kind:"help_request" */ });
   export const FindingsRowSchema = z.discriminatedUnion("kind", [
     FindingRowSchema, StatusUpdateRowSchema,
     TensionConfirmationRowSchema, HelpRequestRowSchema,
   ]);

   export const CoverageRowSchema = z.object({ /* coverage row */ });

   export const PassStartedRowSchema = z.object({ /* kind:"pass_started" */ });
   export const PassFinishedRowSchema = z.object({ /* kind:"pass_finished" */ });
   export const PassesRowSchema = z.discriminatedUnion("kind", [
     PassStartedRowSchema, PassFinishedRowSchema,
   ]);

   export type FindingRow = z.infer<typeof FindingRowSchema>;
   // ... and so on for each schema
   ```

   Refer to the spec's "Data formats" section for every field's name, type, and nullability. Pay special attention to:
   - `class: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4), z.literal("5a"), z.literal("5b")])`.
   - `status` enum on status_update: `"fixing" | "fixed" | "deferred" | "duplicate-of" | "void" | "queued-to-spec" | "resolved-via-spec"`.
   - All timestamps as ISO8601 strings (`z.string().datetime({ offset: true })`).
   - `nullable()` for optional line numbers.

2. Write tests round-tripping each row kind: build a valid object, parse it through the schema, assert equality.

3. Add tests for invalid inputs (wrong `kind`, missing required fields, bad enum value); assert `parse()` throws.

4. Add a helper `parseFindingsLine(line: string): FindingsRow` that JSON.parses then schema-validates.

5. Add a helper `serializeFindingsRow(row: FindingsRow): string` that emits one-line JSON (no trailing newline; the caller adds it during file write).

6. Update `shared/src/index.ts` to re-export the new module.

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run src/jsonl-rows_test.ts
```

---

### T7. Shared: gate I/O types

**Files:**
- `apps/crimefinder/shared/src/gate-io.ts`
- `apps/crimefinder/shared/src/gate-io_test.ts`

**Steps:**

1. Write `apps/crimefinder/shared/src/gate-io.ts`. One zod schema per gate input AND output, mapped to the spec's "Gate vocabulary" table:

   ```ts
   export const ReviewContextInputSchema = z.object({});
   export const ReviewContextOutputSchema = z.discriminatedUnion("role", [
     z.object({
       role: z.literal("review-zone"),
       pass_id: z.string(),
       zone_id: z.string(),
       /* ... full spec shape ... */
     }),
     z.object({
       role: z.literal("fix-cycle"),
       /* ... full spec shape ... */
     }),
   ]);

   export const ReviewFindingInputSchema = z.object({
     class: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4), z.literal("5a"), z.literal("5b")]),
     file: z.string(),
     line_start: z.number().int().nullable().optional(),
     line_end: z.number().int().nullable().optional(),
     symbol: z.string().optional(),
     description: z.string(),
     concept_slug: z.string().nullable().optional(),
     tension_slug: z.string().nullable().optional(),
     confidence: z.enum(["high", "low"]),
   });
   export const ReviewFindingOutputSchema = z.object({
     finding_id: z.string(),
     effective_class: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4), z.literal("5a"), z.literal("5b")]),
     auto_rerouted: z.boolean(),
   });

   /* ... and so on for every gate in the spec table:
      review_coverage, review_complete, review_run_tests,
      review_commit_fix, review_defer, review_skip_zone,
      review_request_help. */
   ```

2. Write tests that round-trip a representative valid input/output for each gate, and reject invalid inputs (wrong enum, missing required, etc.).

3. Update `shared/src/index.ts`.

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run src/gate-io_test.ts
```

---

### T8. Shared: scope addresses and named-event payloads

**Files:**
- `apps/crimefinder/shared/src/scope-addresses.ts`
- `apps/crimefinder/shared/src/scope-addresses_test.ts`
- `apps/crimefinder/shared/src/named-events.ts`
- `apps/crimefinder/shared/src/named-events_test.ts`

**Steps:**

1. Write `apps/crimefinder/shared/src/scope-addresses.ts`. Two zod-discriminated address shapes per the spec's "Scope kinds and address shapes":

   ```ts
   export const SourceTreeZoneAddressSchema = z.object({
     kind: z.literal("source-tree-zone"),
     pass_id: z.string(),
     zone_id: z.string(),
     zone_label: z.string(),
     zone_files: z.array(z.string()),
     repo_root_path: z.string(),
     state_endpoint_url: z.string(),
     session_token: z.string(),
   });
   export const PassStateAddressSchema = z.object({
     kind: z.literal("pass-state"),
     pass_id: z.string(),
     state_endpoint_url: z.string(),
     session_token: z.string(),
   });
   export const ScopeAddressSchema = z.discriminatedUnion("kind", [
     SourceTreeZoneAddressSchema, PassStateAddressSchema,
   ]);

   export function encodeAddress(a: ScopeAddress): Uint8Array;
   export function decodeAddress(bytes: Uint8Array): ScopeAddress;
   ```

   `encodeAddress` serializes to JSON bytes; `decodeAddress` parses JSON bytes then runs the zod validator. Producer-side returns bytes from gRPC handlers; executor-side consumes bytes from the dispatch.

2. Write tests for round-tripping each kind, and rejecting malformed bytes.

3. Write `apps/crimefinder/shared/src/named-events.ts`. Twelve event names per spec; constant wrapper + per-event `data` shape:

   ```ts
   export const NAMED_EVENT_NAMES = [
     "pass_opened", "pass_closed",
     "zone_started", "zone_completed", "zone_skipped",
     "finding_emitted", "finding_resolved", "finding_deferred",
     "finding_dedup_marked",
     "tests_ran", "commit_failed", "help_requested",
   ] as const;
   export type NamedEventName = (typeof NAMED_EVENT_NAMES)[number];

   export const NamedEventEnvelopeSchema = z.object({
     event: z.enum(NAMED_EVENT_NAMES),
     pass_id: z.string(),
     zone_id: z.string().optional(),
     session_id: z.string().optional(),
     ts: z.string().datetime({ offset: true }),
     data: z.record(z.unknown()),
   });
   ```

   Plus per-event `data` Zod schemas (look up spec for each event's `data` fields). Helper to build envelopes given the event name + data.

4. Tests round-trip each event kind.

5. Update `shared/src/index.ts` and run `npx tsc --noEmit && npx vitest run` from the shared directory.

**Verification:**
```
cd apps/crimefinder/shared && npx tsc --noEmit && npx vitest run
```
All shared tests pass. Type-check is clean.

---

### T8a. Shared: class codec

**Files:**
- `apps/crimefinder/shared/src/class-codec.ts`
- `apps/crimefinder/shared/src/class-codec_test.ts`

**Background:** Finding `class` is the union `1 | 2 | 3 | 4 | "5a" | "5b"` in TypeScript, but proto wire format requires a single primitive type. Both the producer and the executor encode/decode through this shared codec to stay in lockstep.

**Steps:**

1. Write `class-codec.ts`:

   ```ts
   export type FindingClass = 1 | 2 | 3 | 4 | "5a" | "5b";
   const VALID_WIRE = new Set(["1", "2", "3", "4", "5a", "5b"]);
   export function encodeClass(c: FindingClass): string {
     return typeof c === "number" ? String(c) : c;
   }
   export function decodeClass(s: string): FindingClass {
     if (!VALID_WIRE.has(s)) throw new Error(`invalid wire class: ${JSON.stringify(s)}`);
     if (s === "5a" || s === "5b") return s;
     return Number(s) as 1 | 2 | 3 | 4;
   }
   export function isFindingClass(value: unknown): value is FindingClass {
     return value === 1 || value === 2 || value === 3 || value === 4 || value === "5a" || value === "5b";
   }
   ```

2. Tests: round-trip every value; reject invalid strings (`"0"`, `"5"`, `"5c"`, `""`); reject non-string inputs to `decodeClass`.

3. Update `shared/src/index.ts` to re-export. T9's proto definition will reference this codec in implementation prose. T28's `append-finding.ts` and `query-findings.ts` use `encodeClass`/`decodeClass` at every wire boundary.

**Verification:**
```
cd apps/crimefinder/shared && npx vitest run src/class-codec_test.ts
```

---

### T9. Proto package: crimefinder_state.proto

**Files:**
- `apps/crimefinder/proto/v1/crimefinder_state.proto`
- `apps/crimefinder/proto/package.json` (no TS source; serves as a workspace anchor for the .proto)
- `apps/crimefinder/proto/README.md`

**Steps:**

1. Write `apps/crimefinder/proto/v1/crimefinder_state.proto`. Define the typed-data service per spec §"Producer surface" → "Two protocols, one process" → `CrimefinderState`. Include:

   - Package declaration: `package crimefinder.v1;`.
   - `service CrimefinderState { rpc AppendFinding(...); rpc QueryFindings(...); rpc UpdateFindingStatus(...); rpc AppendCoverage(...); rpc RunTests(...); rpc CommitFix(...); rpc DeferFinding(...); rpc SkipZone(...); rpc RequestHelp(...); rpc AggregateFindings(...); }`.
   - Per-RPC `Request` and `Response` messages with field names matching the gate I/O zod schemas. For `class` (which is union 1|2|3|4|"5a"|"5b"), use `string` (always serialize as `"1"`, `"5a"`, etc.) for proto-side simplicity.
   - Every Request carries `session_token: string` (the bearer issued via the claim address).
   - Per-RPC Response carries the success-case fields; failures flow as gRPC error status codes plus structured details (use `google.rpc.ErrorInfo` if needed; otherwise `string error_class = N` on the response with an `oneof` like `OpenResponse`).

2. Write `apps/crimefinder/proto/package.json` (no build needed; just declares the workspace member):

   ```json
   {
     "name": "@crimefinder/proto",
     "version": "0.1.0",
     "private": true,
     "scripts": { "test": "echo no-op", "build": "echo no-op", "typecheck": "echo no-op" }
   }
   ```

3. Write `apps/crimefinder/proto/README.md`:

   ```markdown
   # crimefinder proto

   Wire protocol for the crimefinder-producer's typed-data gRPC service
   (`crimefinder.v1.CrimefinderState`). Loaded at runtime via
   `@grpc/proto-loader` by both the producer (server) and the executor
   (client). The rimsky.v1.ClaimProducer protocol is also implemented
   by the producer; that .proto lives at the rimsky repo root under
   `protocols/proto/v1/claim_producer.proto` and is loaded directly
   from there.
   ```

**Verification:**
```
ls apps/crimefinder/proto/v1/crimefinder_state.proto && head -5 apps/crimefinder/proto/v1/crimefinder_state.proto
```
File exists with expected content.

---

### T10. Producer package skeleton + proto-loader

**Files:**
- `apps/crimefinder/producer/package.json`
- `apps/crimefinder/producer/tsconfig.json`
- `apps/crimefinder/producer/src/proto-loader.ts`
- `apps/crimefinder/producer/src/proto-loader_test.ts`

**Steps:**

1. Write `apps/crimefinder/producer/package.json`:

   ```json
   {
     "name": "@crimefinder/producer",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "main": "./dist/main.js",
     "bin": { "crimefinder-producer": "./dist/main.js" },
     "scripts": {
       "build": "tsc",
       "test": "vitest run",
       "typecheck": "tsc --noEmit",
       "lint": "eslint src",
       "start": "node dist/main.js"
     },
     "dependencies": {
       "@crimefinder/shared": "*",
       "@grpc/grpc-js": "^1.10.0",
       "@grpc/proto-loader": "^0.7.10",
       "fastify": "^4.29.1",
       "pino": "^8.17.0",
       "yaml": "^2.3.4",
       "zod": "^3.22.4"
     }
   }
   ```

2. Write `apps/crimefinder/producer/tsconfig.json` (mirrors shared's tsconfig with appropriate paths). Also write `apps/crimefinder/producer/vitest.config.ts` mirroring T3 step 4's shape (`include: ["src/**/*_test.ts"]`) — without this, `vitest run` from the producer directory finds zero tests and silently passes.

3. Write `apps/crimefinder/producer/src/proto-loader.ts`. Loads both proto files. Mirror `code:executors/claude-agent/src/proto-loader.ts::resolveProtoPath` (three-candidate list shape) but apply two adjustments:

   **a. For `claim_producer.proto` (lives at rimsky repo root `protocols/proto/v1/`).** Crimefinder's producer is one directory deeper than claude-agent (`apps/crimefinder/producer/` vs `executors/claude-agent/`), so the candidate list needs ONE additional `..` level. Also, the Dockerfile in T34 copies `protocols/` to `/app/protocols/`, so the in-container case is `../../protocols/proto/v1/` from `/app/producer/dist/`. Candidates (in order):

   ```ts
   const claimProducerCandidates = [
     resolve(here, `../../../../protocols/proto/v1/${filename}`), // rimsky-root layout: producer/{src,dist}/ → rimsky/protocols/
     resolve(here, `../../../protocols/proto/v1/${filename}`),    // unusual layout (extra repo nesting); defensive
     resolve(here, `../../protocols/proto/v1/${filename}`),       // container layout: /app/producer/dist/ → /app/protocols/
     resolve(here, `../protocols/proto/v1/${filename}`),          // defensive fallback
   ];
   ```

   **b. For `crimefinder_state.proto` (lives at `apps/crimefinder/proto/v1/`).** From `apps/crimefinder/producer/src/proto-loader.ts` it's `../../proto/v1/`. From the built `producer/dist/proto-loader.js` it's also `../../proto/v1/`. In container at `/app/producer/dist/`, the Dockerfile copies `proto/` to `/app/proto/`, so the path is `../../proto/v1/` there as well. Candidates:

   ```ts
   const crimefinderStateCandidates = [
     resolve(here, `../../proto/v1/${filename}`),    // src/dist/container — all converge
     resolve(here, `../../../proto/v1/${filename}`), // defensive
   ];
   ```

   Use the same `existsSync` walk as claude-agent's `resolveProtoPath`. Throw with the full candidate list if none match — surfacing path resolution failures immediately is preferable to silent loading from the wrong file.

   Exports:
   ```ts
   export interface ProducerPackage {
     rimsky: { v1: { ClaimProducer: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition } } };
     crimefinder: { v1: { CrimefinderState: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition } } };
   }
   export function loadProducerProtos(): ProducerPackage;
   ```

4. Write `proto-loader_test.ts`: assert `loadProducerProtos().rimsky.v1.ClaimProducer.service` and `.crimefinder.v1.CrimefinderState.service` both exist and have non-empty `methods` maps.

5. Run `npm install` from the workspace root to wire `@crimefinder/shared` into the producer's `node_modules`.

**Verification:**
```
cd apps/crimefinder/producer && npx tsc --noEmit && npx vitest run src/proto-loader_test.ts
```

---

### T11. Producer: JSONL store with single-writer mutex

**Files:**
- `apps/crimefinder/producer/src/jsonl-mutex.ts`
- `apps/crimefinder/producer/src/jsonl-mutex_test.ts`
- `apps/crimefinder/producer/src/jsonl-store.ts`
- `apps/crimefinder/producer/src/jsonl-store_test.ts`

**Steps:**

1. Write `apps/crimefinder/producer/src/jsonl-mutex.ts`. Implement an in-process promise-chain mutex:

   ```ts
   export class JsonlMutex {
     private queue: Promise<void> = Promise.resolve();
     async withLock<T>(fn: () => Promise<T>): Promise<T> {
       const prev = this.queue;
       let release: () => void;
       const next = new Promise<void>((r) => (release = r));
       this.queue = prev.then(() => next);
       await prev;
       try { return await fn(); }
       finally { release!(); }
     }
   }
   ```

2. Write `jsonl-mutex_test.ts`: kick off 100 concurrent `withLock` calls, each writing a unique number to an in-memory array; assert order matches call order (or any total order — only one writer in the critical section at a time).

3. Write `apps/crimefinder/producer/src/jsonl-store.ts`. Exports:

   ```ts
   export class JsonlStore {
     constructor(private repoRoot: string, private logger: Logger) {}

     async appendFinding(row: FindingRow | StatusUpdateRow | TensionConfirmationRow | HelpRequestRow): Promise<void>;
     async appendCoverage(row: CoverageRow): Promise<void>;
     async appendPassStarted(row: PassStartedRow): Promise<void>;
     async appendPassFinished(row: PassFinishedRow): Promise<void>;

     async readFindings(): Promise<(FindingsRow)[]>;
     async readCoverage(): Promise<CoverageRow[]>;
     async readPasses(): Promise<PassesRow[]>;

     async ensureDir(): Promise<void>; // mkdir .crimefinder/ if absent
   }
   ```

   Each `append*` method:
   - Acquires the appropriate per-file mutex (one mutex per file path).
   - Validates the row via the matching shared zod schema before write.
   - Writes one line + `\n` via `fs.promises.appendFile`.

   `read*` methods are read-only (no mutex needed for read — readers see what's been flushed); on each line: trim, skip blank, JSON.parse, schema-validate. Skip malformed lines with a `logger.warn` rather than throwing — a JSONL file may be mid-append.

4. Write `jsonl-store_test.ts` with a tmp directory:
   - Append a finding; read it back; assert equality.
   - Append 50 findings concurrently via `Promise.all`; read; assert all 50 are present with no corruption (each line parses cleanly).
   - Append a status_update; read; assert both finding and status_update return from `readFindings()`.
   - Malformed line in the middle: write a manual corrupt JSON line via `fs.appendFile`; assert `readFindings()` returns the valid rows and logs a warning.

5. Update `producer/tsconfig.json` `paths:` (if needed) so `@crimefinder/shared` resolves.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/jsonl-mutex_test.ts src/jsonl-store_test.ts
```

---

### T12. Producer: zones partition (lifted)

**Files:**
- `apps/crimefinder/producer/src/zones/partition.ts`
- `apps/crimefinder/producer/src/zones/partition_test.ts`
- `apps/crimefinder/producer/src/zones/coverage.ts`
- `apps/crimefinder/producer/src/zones/coverage_test.ts`

**Steps:**

1. Read the prototype: `cat /Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition.ts` and `cat /Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition_test.ts`.

2. Write `apps/crimefinder/producer/src/zones/partition.ts`. Port the prototype with these changes:
   - Use `@crimefinder/shared`'s `generateZoneId` rather than the prototype's local hash function (they're equivalent).
   - Replace any direct prototype-side imports with `@crimefinder/shared` equivalents.
   - Prepend `@source:` annotation:
     ```ts
     /**
      * @source: /Users/patrick/Documents/projects/research/crimefinder/src/features/zones/partition.ts
      * @diverged: false
      */
     ```

   Public API:
   ```ts
   export interface Zone { id: string; label: string; files: string[]; }
   export function partitionIntoZones(opts: {
     projectRoot: string;
     targetPath?: string;
     maxFilesPerZone?: number;
     smallGroupThreshold?: number;
     ignorePatterns?: string[];
   }): Zone[];
   ```

3. Port `partition_test.ts`: same test cases as the prototype, with paths adjusted.

4. Read and port `coverage.ts` and `coverage_test.ts` from the prototype's `src/features/zones/`. The prototype operates on SQLite; our version operates on the JSONL coverage store. Port the *partitioning math* (mapping files to zones, computing per-zone coverage %) but rewrite the storage layer to read from `JsonlStore`. Mark `@source:` + `@diverged: true` with `@reason: storage swapped from SQLite to .crimefinder/coverage.jsonl`.

   Public API:
   ```ts
   export interface ZoneCoverageSummary {
     zoneId: string; zoneLabel: string;
     totalFiles: number; filesChecked: number;
     coveragePercent: number; sessionsSpent: number;
   }
   export async function computeZoneCoverage(
     coverageRows: CoverageRow[],
     huntId: string,
     zones: Zone[],
   ): Promise<ZoneCoverageSummary[]>;
   export function mapFileToZone(filePath: string, zones: Zone[]): Zone | null;
   ```

5. Tests cover: zones partition matches prototype; coverage % computed correctly given an array of CoverageRow.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/zones/
```

---

### T13. Producer: dedup (lifted)

**Files:**
- `apps/crimefinder/producer/src/dedup/group.ts`
- `apps/crimefinder/producer/src/dedup/group_test.ts`
- `apps/crimefinder/producer/src/dedup/resolve.ts`
- `apps/crimefinder/producer/src/dedup/resolve_test.ts`

**Steps:**

1. Read prototype: `/Users/patrick/Documents/projects/research/crimefinder/src/features/dedup/group.ts` and `resolve.ts`.

2. Port `group.ts`. Public API:
   ```ts
   export function groupFindingsByFile(findings: FindingRow[]): Map<string, string[]>;
   // returns file → [finding_id, ...]
   export interface FileGroup { file: string; findingIds: string[]; }
   export function batchFileGroups(groups: Map<string, string[]>, findings: FindingRow[]): FileGroup[][];
   // batches small groups together for fewer dedup-agent invocations
   ```

   Prepend `@source:` + `@diverged: true`, `@reason: input type changed from prototype's Issue to FindingRow`.

3. Port `resolve.ts`. Public API:
   ```ts
   export interface DedupGroup { survivorId: string; duplicateIds: string[]; }
   export interface DedupResult { duplicateGroups: DedupGroup[]; }
   export function applyDedupResults(
     allResults: DedupResult[],
   ): Array<{ findingId: string; duplicateOf: string }>;
   // returns list of status-update intents; caller writes to JSONL
   ```

4. Port tests. They should remain mostly the same shape; adjust types and dedup-result format.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/dedup/
```

---

### T14. Producer: git ops

**Files:**
- `apps/crimefinder/producer/src/git-ops.ts`
- `apps/crimefinder/producer/src/git-ops_test.ts`

**Steps:**

1. Write `apps/crimefinder/producer/src/git-ops.ts`. Functions:

   ```ts
   export interface GitOps {
     status(repoRoot: string): Promise<{ paths: string[]; clean: boolean }>;
     // calls `git status --porcelain`; parses output into changed paths

     mtime(repoRoot: string): Promise<number>;
     // returns max mtime across tracked files in working tree (for test cache invalidation)

     add(repoRoot: string, paths: string[]): Promise<void>;
     // git add <paths>

     commit(repoRoot: string, message: string): Promise<string>;
     // git commit -m <message>; returns the SHA (parse from `git rev-parse HEAD`)
     // on git failure (e.g. pre-commit hook), throws GitCommitError with stderr captured

     log(repoRoot: string, sinceSha?: string): Promise<Array<{ sha: string; subject: string; body: string }>>;
     // walks recent commits; used by recovery scan to find Resolves: footers

     repoRoot(cwd: string): Promise<string>;
     // git rev-parse --show-toplevel
   }

   export class GitCommitError extends Error {
     constructor(public stderr: string) { super(`git commit failed: ${stderr}`); }
   }

   export function createGitOps(execFile: typeof import("node:child_process").execFile = childProcess.execFile): GitOps;
   ```

   Use `node:child_process.execFile` with `shell: false` (avoid shell injection). Set `cwd` to `repoRoot`. Capture stdout and stderr.

2. Write tests using a tmp git repo. Use `execFile` to run `git init`, `git add`, etc., then exercise each function:
   - `status` on a clean repo returns `{ paths: [], clean: true }`.
   - After `fs.writeFile` to a tracked file, status returns the path with `clean: false`.
   - `commit` on a dirty repo returns a SHA, and `log` includes that SHA.
   - `commit` fails when there are no staged changes; assert `GitCommitError`.
   - `commit` fails when a pre-commit hook returns non-zero (write a hook that exits 1); assert `GitCommitError` with stderr captured.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/git-ops_test.ts
```

---

### T15. Producer: concepts parser and scanner

**Files:**
- `apps/crimefinder/producer/src/concepts/parser.ts`
- `apps/crimefinder/producer/src/concepts/parser_test.ts`
- `apps/crimefinder/producer/src/concepts/scanner.ts`
- `apps/crimefinder/producer/src/concepts/scanner_test.ts`

**Steps:**

1. Write `parser.ts`. Read a `concepts/<slug>.md` file and extract two named sections:

   ```ts
   export interface ConceptDoc {
     slug: string;
     filePath: string;
     content: string;
     boundaries: string;   // text of the "## Boundaries" section (empty string if missing)
     invariants: string;   // text of the "## Invariants" section (empty string if missing)
   }

   export async function readConcept(filePath: string): Promise<ConceptDoc>;
   ```

   Algorithm: read file; locate `## Boundaries` heading; capture text until next `## ` heading (or EOF); same for `## Invariants`. Trim. If the heading isn't present, the section is `""`.

2. Tests: fixture files with various heading orderings (boundaries first, invariants first, missing one, missing both); assert correct extraction.

3. Write `scanner.ts`. Recursively grep for `@concept: <slug>` annotations in source files under a given root:

   ```ts
   export interface ConceptAnnotation { file: string; line: number; slug: string; }
   export async function scanConceptAnnotations(opts: {
     repoRoot: string;
     marker?: string;          // default "@concept:"
     ignorePatterns?: string[]; // default ["node_modules", ".git", "dist", "build"]
   }): Promise<ConceptAnnotation[]>;
   ```

4. Tests: tmp dir with files containing `@concept:` markers; assert all are found with correct file:line.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/concepts/
```

---

### T16. Producer: class-5b auto-routing rule

**Files:**
- `apps/crimefinder/producer/src/state/class-5b-rule.ts`
- `apps/crimefinder/producer/src/state/class-5b-rule_test.ts`

**Steps:**

1. Write `class-5b-rule.ts`:

   ```ts
   export function shouldRerouteToClass5b(args: {
     description: string;
     conceptBoundaries: string;
     conceptInvariants: string;
     minTokenRun?: number; // default 8
   }): boolean;
   ```

   Algorithm:
   - Tokenize each input string: lowercase, strip Markdown emphasis (`*`, `_`, backticks), split on whitespace, drop tokens shorter than 4 characters, drop punctuation-only tokens.
   - Build a token sequence for `description`, for `boundaries`, for `invariants`.
   - Return `false` if either `boundaries` or `invariants` contains a contiguous subsequence of length ≥ `minTokenRun` (default 8) of `description`'s token sequence — i.e., the description quoted at least 8 consecutive load-bearing tokens.
   - Else return `true` (no verbatim quote → reroute to 5b).

2. Tests:
   - Description that contains 8 consecutive tokens from `Boundaries:` → `false` (no reroute).
   - Description that contains only 7 consecutive tokens → `true`.
   - Description with all the right words but in a different order → `true` (sequence matters).
   - Both `boundaries` and `invariants` empty → `true` (no quote possible).
   - Case insensitive: uppercase description tokens still match lowercase concept tokens.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/class-5b-rule_test.ts
```

---

### T17. Producer: per-pass iteration counter (durable)

**Files:**
- `apps/crimefinder/producer/src/state/iteration-counter.ts`
- `apps/crimefinder/producer/src/state/iteration-counter_test.ts`

**Background:** The counter must survive producer crashes mid-pass, because the iter_num is embedded in selectors (`@fix-partition:pass_id=X&iter_num=N`) and partition caches. Rebuilding from "count how many `@unresolved-class-1-4` Opens" requires a durable record. We persist iter advances as `iter_marker` rows in `.crimefinder/passes.jsonl` — small, durable, mergeable like the other rows. (This requires extending the passes-row schema; see step 1 of T6 if not already done — return there and add `IterMarkerRowSchema` to the discriminated union before T17.)

**Steps:**

1. If not already done, extend `shared/src/jsonl-rows.ts` (T6) to include:

   ```ts
   export const IterMarkerRowSchema = z.object({
     kind: z.literal("iter_marker"),
     id: z.string(),
     ts: z.string().datetime({ offset: true }),
     pass_id: z.string(),
     iter_num: z.number().int().positive(),
   });
   export type IterMarkerRow = z.infer<typeof IterMarkerRowSchema>;
   // Update the passes-row discriminated union to include IterMarkerRowSchema.
   ```

   Also extend `JsonlStore` (T11) with `appendIterMarker(row: IterMarkerRow)` and update `readPasses()` to return iter_marker rows alongside pass_started / pass_finished.

2. Write `iteration-counter.ts`:

   ```ts
   export class IterationCounter {
     private byPass = new Map<string, number>();
     constructor(private store: JsonlStore, private logger: Logger) {}

     // On boot, read all iter_marker rows and rebuild in-memory state.
     async restore(): Promise<void> {
       const passes = await this.store.readPasses();
       for (const row of passes) {
         if (row.kind === "iter_marker") {
           const prev = this.byPass.get(row.pass_id) ?? 0;
           if (row.iter_num > prev) this.byPass.set(row.pass_id, row.iter_num);
         }
       }
     }

     // Atomically advance: append a durable iter_marker AND update in-memory.
     async nextFor(passId: string): Promise<number> {
       const next = (this.byPass.get(passId) ?? 0) + 1;
       await this.store.appendIterMarker({
         kind: "iter_marker",
         id: generateRowId(),
         ts: new Date().toISOString(),
         pass_id: passId,
         iter_num: next,
       });
       this.byPass.set(passId, next);
       return next;
     }

     currentFor(passId: string): number {
       return this.byPass.get(passId) ?? 0;
     }
   }
   ```

   Note: `nextFor` writes BEFORE updating in-memory. On producer crash between the JSONL append and any downstream observation, the next boot's `restore()` will see the marker and replay the correct counter — and any node that observed the previous iter_num is still consistent with the durable JSONL state.

3. Tests:
   - `nextFor` writes an iter_marker row AND returns the incremented number.
   - Two `IterationCounter` instances sharing a JsonlStore + restore between operations show consistent counters (simulates crash recovery).
   - Different `pass_ids` are independent.
   - `restore()` is idempotent (replaying with no new markers keeps the same state).

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/iteration-counter_test.ts
```

---

### T18. Producer: session-token registry

**Files:**
- `apps/crimefinder/producer/src/state/session-tokens.ts`
- `apps/crimefinder/producer/src/state/session-tokens_test.ts`

**Steps:**

1. Write `session-tokens.ts`. Maps `session_token` → claim metadata (pass_id, claim_handle_id, optional zone_id). Issued on `Open`, validated on every typed-API call, released on `Release`:

   ```ts
   export interface TokenMetadata {
     passId: string;
     claimHandleId: string;
     zoneId?: string;
     role?: "review-zone" | "fix-cycle" | "dedup";
     issuedAt: number;
   }
   export class SessionTokenRegistry {
     private tokens = new Map<string, TokenMetadata>();
     issue(meta: TokenMetadata): string;  // generates token + stores
     validate(token: string): TokenMetadata | null;
     revoke(token: string): void;
   }
   ```

   Use `generateSessionToken()` from `@crimefinder/shared`.

2. Tests: issue → validate returns metadata; revoke → validate returns null; multiple tokens are isolated.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/session-tokens_test.ts
```

---

### T19. Producer: RunTests with cache

**Files:**
- `apps/crimefinder/producer/src/state/test-cache.ts`
- `apps/crimefinder/producer/src/state/test-cache_test.ts`
- `apps/crimefinder/producer/src/state/run-tests.ts`
- `apps/crimefinder/producer/src/state/run-tests_test.ts`

**Steps:**

1. Write `test-cache.ts`:

   ```ts
   export interface TestResult {
     exitCode: number;
     stdoutTail: string;
     stderrTail: string;
     ranAt: string;             // ISO8601
     treeMtimeAtRun: number;    // ms epoch
     commandSha: string;        // sha256 of the command + cwd + relevant env
   }
   export class TestCache {
     private byPass = new Map<string, TestResult>();
     get(passId: string, currentMtime: number, commandSha: string): TestResult | null;
     // Returns cached result IFF currentMtime <= treeMtimeAtRun AND commandSha matches.
     set(passId: string, result: TestResult): void;
   }
   ```

2. Tests: cache hit when mtime didn't advance; cache miss when mtime advanced; cache miss when commandSha differs.

3. Write `run-tests.ts`. Combines `GitOps.mtime`, the test command from config, and TestCache:

   ```ts
   export interface RunTestsArgs {
     passId: string;
     repoRoot: string;
     command: string;     // e.g. "go test ./..."
     timeoutMs: number;   // from cfg:tests.timeout_seconds * 1000
   }
   export async function runTests(
     args: RunTestsArgs,
     deps: { git: GitOps; cache: TestCache; execFile: ExecFileFn; mutex: TestRunMutex },
   ): Promise<TestResult>;
   ```

   - Compute `commandSha` from `command + repoRoot`.
   - Read `currentMtime = git.mtime(repoRoot)`.
   - Acquire `TestRunMutex` (one mutex globally; tests don't parallelize across passes).
   - Inside the mutex: check cache; if hit, return cached.
   - Otherwise: shell out via `execFile` with `shell: false`. Tokenize the command yourself (split on whitespace; the first token is the program, the rest are args — this prevents shell injection but limits command syntax; if config has shell metacharacters, this will fail, which is acceptable for v1).
   - Capture last 200 lines of stdout and stderr.
   - Build `TestResult`; cache it; release mutex; return.
   - On `execFile` timeout, return `{ exitCode: -1, stderrTail: "TIMEOUT", ... }` and emit a `tests_failed` named-event (the executor wraps this gate's result and emits the event).

4. `TestRunMutex` is a `JsonlMutex`-style mutex; reuse the class.

5. Tests:
   - First call runs the command; second call (same mtime) returns cached.
   - File modified between calls (advance mtime by touching a tracked file); second call re-runs.
   - Different command (config change) → re-runs.
   - Timeout case: command sleeps longer than `timeoutMs`; result is `exitCode: -1`.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/test-cache_test.ts src/state/run-tests_test.ts
```

---

### T20. Producer: config reader

**Files:**
- `apps/crimefinder/producer/src/config.ts`
- `apps/crimefinder/producer/src/config_test.ts`

**Steps:**

1. Write `config.ts`. Reads `${repoRoot}/.crimefinder/config.yml`:

   ```ts
   export const ConfigSchema = z.object({
     tests: z.object({
       command: z.string(),
       timeout_seconds: z.number().int().positive().default(600),
       cwd: z.string().default("."),
     }).optional(),
     require_tests_before_commit: z.boolean().default(false),
     coverage: z.object({
       threshold_pct: z.number().min(0).max(100).default(80),
       on_below_threshold: z.enum(["require_skip", "warn", "allow"]).default("require_skip"),
     }).default({}),
     partitioning: z.object({
       max_files_per_zone: z.number().int().positive().default(50),
       small_group_threshold: z.number().int().positive().default(10),
       additional_ignore_patterns: z.array(z.string()).default([]),
     }).default({}),
     allowed_tools: z.array(z.string()).default([
       "Read", "Glob", "Grep", "Edit", "Write",
       "mcp__crimefinder__review_*",
     ]),
     design_docs: z.object({
       concepts_dir: z.string().default(".ok-planner/design/concepts"),
       tensions_dir: z.string().default(".ok-planner/design/tensions"),
       annotation_marker: z.string().default("@concept:"),
     }).optional(),
   });
   export type CrimefinderConfig = z.infer<typeof ConfigSchema>;
   export async function readConfig(repoRoot: string): Promise<CrimefinderConfig>;
   // Returns defaults if file is missing; throws if file is malformed YAML or fails schema.
   ```

2. Tests:
   - Missing file → returns defaults.
   - Minimal file (just `tests.command`) → returns merged defaults.
   - Bad YAML → throws.
   - Schema violation (e.g. `coverage.threshold_pct: 200`) → throws.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/config_test.ts
```

---

### T21. Producer: scope handlers — pass-state, context-scan, source-tree

**Files:**
- `apps/crimefinder/producer/src/scopes/pass-state.ts`
- `apps/crimefinder/producer/src/scopes/pass-state_test.ts`
- `apps/crimefinder/producer/src/scopes/context-scan.ts`
- `apps/crimefinder/producer/src/scopes/context-scan_test.ts`
- `apps/crimefinder/producer/src/scopes/source-tree.ts`
- `apps/crimefinder/producer/src/scopes/source-tree_test.ts`

**Steps:**

1. Each scope handler exports an `open` function with signature:

   ```ts
   export interface OpenContext {
     selector: string;          // post-substitution selector from rimsky
     claimId: string;           // rimsky-generated
     repoRoot: string;
     store: JsonlStore;
     tokens: SessionTokenRegistry;
     iterCounter: IterationCounter;
     stateEndpointUrl: string;  // producer's typed-service URL
     logger: Logger;
   }
   export interface OpenResult {
     address: Uint8Array;
     payload: Uint8Array;
     scope: Uint8Array;
   }
   export async function openPassState(ctx: OpenContext): Promise<OpenResult>;
   ```

2. `pass-state.ts` for selector `@pass-state:new&mission=<urlencoded>&trigger=<urlencoded>`:
   - Parse `mission` and `trigger` from the selector's query string (URL-decode). The template uses `{{params.mission}}` and `{{params.trigger}}` substitution at registration time so these come through as literal strings in the resolved selector when the producer's `Open` fires. Document this URL-encoded-selector convention in a code comment — `OpenRequest` (per `proto:claim_producer.proto`) carries `selector`, `claim_id`, `producer_name`, `intent`, `alias`, `template_id`, `instance_id` only; instance-level params do not flow to the producer as a separate field, so encoding them in the selector is the supported path.
   - Generate `passId` via `generatePassId()`.
   - Write a `pass_started` row to `passes.jsonl` carrying `mission`, `trigger`, `template_hash` (from `OpenRequest.template_id`), and `params_hash` (compute as `sha256(JSON.stringify({pass_id: passId, mission, trigger}))`).
   - Issue a session token bound to `{passId, claimId}`.
   - Build the address as `PassStateAddressSchema` JSON bytes: `{kind:"pass-state", pass_id, state_endpoint_url, session_token}`.
   - Build the payload bytes (returned to substitution) as JSON: `{pass_id: <passId>}`. The substitution `{{claim.pass-state.payload.pass_id}}` will pick this up.
   - Build the scope bytes: `{kind:"pass-state", pass_id}` (used by other scope handlers that match on scope to discover the active pass).
   - Return the `OpenResult`.

3. `context-scan.ts` for `@context-scan:pass_id=<id>`:
   - Parse `pass_id` from the selector.
   - Walk the repo for design-doc files (per `cfg:design_docs.concepts_dir` / `tensions_dir`). Read each.
   - Run the `@concept:` annotation scanner.
   - Build `context_manifest` payload: `{claude_md_present: bool, rules_files: string[], concepts: [{slug, path}, ...], tensions: [{slug, path}, ...], concept_annotations: [{file, line, slug}, ...]}`.
   - No session token issued (this is a read-only deterministic scope; consuming nodes don't call CrimefinderState).
   - Address: empty bytes (no typed-service interaction).
   - Scope: `{kind:"context-scan", pass_id}`.

4. `source-tree.ts` for `@source-tree:pass_id=<id>`:
   - Parse `pass_id`.
   - Issue a session token bound to `{passId, claimId}` (the eventual fan-out child runs will inherit this address; the executor uses it to call CrimefinderState).
   - Run `partitionIntoZones({ projectRoot: repoRoot, ...config.partitioning })`. Cache the zone plan against `pass_id` in an in-memory map (so SplitScope, which fires next on the same claim, returns the same partitioning).
   - Build address (this is the parent's address; the fan-out parent isn't usually called typed-service-wise, but provide a valid PassState-shaped address as fallback): `{kind:"pass-state", pass_id, state_endpoint_url, session_token}`.
   - Build payload: `{zone_count: zones.length}`.
   - Build scope: `{kind:"source-tree", pass_id}`.

5. Tests: for each handler, a happy-path test asserting payload/address shapes. Use a tmp dir with a tiny tree fixture for `source-tree.ts`.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/scopes/pass-state_test.ts src/scopes/context-scan_test.ts src/scopes/source-tree_test.ts
```

---

### T22. Producer: scope handlers — aggregate, dedup, class-split

**Files:**
- `apps/crimefinder/producer/src/scopes/aggregate-findings.ts`
- `apps/crimefinder/producer/src/scopes/aggregate-findings_test.ts`
- `apps/crimefinder/producer/src/scopes/dedup-grouping.ts`
- `apps/crimefinder/producer/src/scopes/dedup-grouping_test.ts`
- `apps/crimefinder/producer/src/scopes/class-split.ts`
- `apps/crimefinder/producer/src/scopes/class-split_test.ts`

**Steps:**

1. `aggregate-findings.ts` for `@aggregate-findings:pass_id=<id>`:
   - Read all findings from JSONL.
   - Filter to those with `pass_id` matching.
   - Materialize each finding's current status by scanning status_update rows.
   - Compute: `class_1_4_remaining: number`, `class_5: FindingRow[]`, `dedup_file_groups: Array<{file, findingIds}>`.
   - Payload: `{class_1_4_remaining, class_5, dedup_file_groups}`.

2. `dedup-grouping.ts` for `@dedup-grouping:pass_id=<id>`:
   - Read findings; compute file groups via `groupFindingsByFile`.
   - Batch into chunks via `batchFileGroups`.
   - Issue session tokens for each batch (the fan-out children will use them).
   - Build SplitScope's source data: an array of `{batchIndex, fileGroups: [{file, findingIds}, ...]}`. Stored in the in-memory partition cache keyed by `pass_id`.
   - Payload: `{batch_count}`. Address: PassState-shaped (the dedup leaf children call CrimefinderState).

3. `class-split.ts` for `@class-split:pass_id=<id>`:
   - Reads findings; post-dedup state means rows with `status:"duplicate-of"` are filtered out.
   - Compute: `class_1_4_remaining: bool`, `class_5_findings: FindingRow[]`.
   - Payload: those two fields. No fan-out (downstream is class-5-finalize and fix-iter-1).

4. Each test exercises a happy-path JSONL fixture.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/scopes/aggregate-findings_test.ts src/scopes/dedup-grouping_test.ts src/scopes/class-split_test.ts
```

---

### T23. Producer: scope handlers — unresolved, fix-partition, re-review-partition, iter-aggregate

**Files:**
- `apps/crimefinder/producer/src/scopes/unresolved-class-1-4.ts`
- `apps/crimefinder/producer/src/scopes/unresolved-class-1-4_test.ts`
- `apps/crimefinder/producer/src/scopes/fix-partition.ts`
- `apps/crimefinder/producer/src/scopes/fix-partition_test.ts`
- `apps/crimefinder/producer/src/scopes/re-review-partition.ts`
- `apps/crimefinder/producer/src/scopes/re-review-partition_test.ts`
- `apps/crimefinder/producer/src/scopes/iter-aggregate.ts`
- `apps/crimefinder/producer/src/scopes/iter-aggregate_test.ts`

**Steps:**

1. `unresolved-class-1-4.ts` for `@unresolved-class-1-4:pass_id=<id>`:
   - Compute the next iter_num via `IterationCounter.nextFor(passId)`.
   - Read findings; materialize statuses; identify findings with `effective_class in {1,2,3,4}` and `status in {"open","fixing"}`.
   - Group by zone (look up each finding's zone via `mapFileToZone` against the cached zone plan for `pass_id`).
   - Payload: `{iter_num, affected_zones: string[], skipped: affected_zones.length === 0}`.

2. `fix-partition.ts` for `@fix-partition:pass_id=<id>&iter_num=<n>`:
   - Parse `pass_id` and `iter_num` from selector.
   - Read findings; identify unresolved class-1-4 by zone, scoped to zones in `affected_zones` from the prior `@unresolved-class-1-4` call (which we cached).
   - For each affected zone, prepare a sub-scope descriptor with the zone's files and assigned findings.
   - Cache the partition under `(pass_id, iter_num, "fix")` so SplitScope returns the same set.
   - Payload: `{iter_num, affected_zones_count}`. Address: PassState-shaped (fix-zone children call CrimefinderState for run_tests/commit_fix gates).

3. `re-review-partition.ts` for `@re-review-partition:...`: similar; partitions same affected zones for a re-review pass.

4. `iter-aggregate.ts` for `@iter-aggregate:pass_id=<id>&iter_num=<n>`:
   - Re-evaluate: any unresolved class-1-4 findings remaining for this pass? Count `findings_resolved_this_iter` (status_updates with `status:"fixed"` and `by_pass=<passId>` since the prior iter-aggregate).
   - Payload: `{more_work_needed: bool, findings_resolved_this_iter: number}`.

5. Tests: each handler tested against a JSONL fixture covering "first iteration, work present", "second iteration after fix", "no work remaining".

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/scopes/unresolved-class-1-4_test.ts src/scopes/fix-partition_test.ts src/scopes/re-review-partition_test.ts src/scopes/iter-aggregate_test.ts
```

---

### T24. Producer: scope handlers — class-5-finalize, report

**Files:**
- `apps/crimefinder/producer/src/scopes/class-5-finalize.ts`
- `apps/crimefinder/producer/src/scopes/class-5-finalize_test.ts`
- `apps/crimefinder/producer/src/scopes/report.ts`
- `apps/crimefinder/producer/src/scopes/report_test.ts`

**Steps:**

1. `class-5-finalize.ts` for `@class-5-finalize:pass_id=<id>`:
   - Read findings; for any `class:5a` or `class:5b` row where current status materializes to `status:open` already → no-op. For any that materialize to a non-`open` status, do not change them (deferred / queued-to-spec are valid terminal states).
   - Payload: `{class_5_open: number, class_5_resolved: number, class_5_deferred: number, class_5_queued_to_spec: number}`.

2. `report.ts` for `@report:pass_id=<id>`:
   - Compute pass summary (zones planned/completed/skipped, findings_emitted, findings_resolved, etc.) by reading findings/coverage/passes JSONL.
   - Write a `pass_finished` row to passes.jsonl.
   - Payload: the same summary as JSON.

3. Tests: report writes correct row; finalize is no-op on already-open class-5.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/scopes/class-5-finalize_test.ts src/scopes/report_test.ts
```

---

### T25. Producer: SplitScope dispatcher

**Files:**
- `apps/crimefinder/producer/src/claim-producer/split-scope.ts`
- `apps/crimefinder/producer/src/claim-producer/split-scope_test.ts`

**Steps:**

1. Write `split-scope.ts`. Exports:

   ```ts
   export async function splitScope(args: {
     parentClaimHandleId: string;
     parentScope: Uint8Array;       // the parent's scope bytes (from Open)
     partitionRequest: Uint8Array;  // producer-interpreted bytes
     ctx: OpenContext;
   }): Promise<Array<{ scopeData: Uint8Array; partitionKey: string; producerMetadata: Uint8Array }>>;
   ```

2. Parse `partitionRequest` as JSON; switch on `kind`:
   - `"source-tree-partition"`: read cached partition for `pass_id`; emit one sub-scope per zone. `scopeData` for each zone is a `SourceTreeZoneAddressSchema` JSON serialization (used by child Open). Wait — `scope_data` is the persistent scope identity (`@blessed-invariant 4` requires byte-equal scope = same identity), not the address. Re-read concept: `scope` (opaque bytes that ClaimProducer.Open returns to identify what was acquired). For our zone sub-scope, the scope identity is `{kind:"source-tree-zone", pass_id, zone_id, zone_files}` JSON-bytes — enough to make two byte-equal zones conflict via ScopesConflict, and to recover the zone identity from cold storage.
   - `"dedup-partition"`: emit one sub-scope per dedup batch.
   - `"fix-partition"`: emit one sub-scope per affected zone (from cached partition for `(pass_id, iter_num, "fix")`).
   - `"re-review-partition"`: similar.

3. Tests: each `kind` returns the expected sub-scope count and well-formed bytes.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/claim-producer/split-scope_test.ts
```

---

### T26. Producer: ScopesConflict

**Files:**
- `apps/crimefinder/producer/src/claim-producer/scopes-conflict.ts`
- `apps/crimefinder/producer/src/claim-producer/scopes-conflict_test.ts`

**Steps:**

1. Write `scopes-conflict.ts`:

   ```ts
   export function scopesConflict(scopeA: Uint8Array, scopeB: Uint8Array): boolean;
   ```

   Algorithm:
   - JSON.parse both; if either fails to parse, fall back to byte-equal.
   - If both have `kind === "source-tree-zone"` AND same `pass_id`: return `conflict = (fileSet(a) ∩ fileSet(b)).size > 0`. Non-overlapping zones do not conflict.
   - If both have `kind === "pass-state"` AND same `pass_id`: return `true` (pass-state is single-holder; co-holdership is at handler level, not via new claims).
   - Otherwise: byte-equal default.

2. Tests:
   - Disjoint zones → `false`.
   - Overlapping zones → `true`.
   - Same-pass pass-state → `true`.
   - Different-pass pass-state → `false`.
   - Malformed bytes → byte-equal fallback (same bytes → `true`).

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/claim-producer/scopes-conflict_test.ts
```

---

### T27. Producer: ClaimProducer wire handlers (Open/Commit/Abandon/Release/Capabilities)

**Files:**
- `apps/crimefinder/producer/src/claim-producer/open.ts`
- `apps/crimefinder/producer/src/claim-producer/commit.ts`
- `apps/crimefinder/producer/src/claim-producer/abandon.ts`
- `apps/crimefinder/producer/src/claim-producer/release.ts`
- `apps/crimefinder/producer/src/capabilities.ts`
- co-located `*_test.ts`

**Steps:**

1. `open.ts`. Dispatches by selector prefix:

   ```ts
   export async function handleOpen(req: OpenRequest, ctx: OpenContext): Promise<OpenResponse>;
   ```

   - Parse the selector. Switch on prefix:
     - `@pass-state:` → `openPassState`
     - `@context-scan:` → `openContextScan`
     - `@source-tree:` → `openSourceTree`
     - `@aggregate-findings:` → `openAggregateFindings`
     - `@dedup-grouping:` → `openDedupGrouping`
     - `@class-split:` → `openClassSplit`
     - `@unresolved-class-1-4:` → `openUnresolvedClass14`
     - `@fix-partition:` → `openFixPartition`
     - `@re-review-partition:` → `openReReviewPartition`
     - `@iter-aggregate:` → `openIterAggregate`
     - `@class-5-finalize:` → `openClass5Finalize`
     - `@report:` → `openReport`
     - For fan-out child opens (where rimsky passes the producer-canonicalized `scope_data` from SplitScope back via Open): detect by the scope being a `source-tree-zone` shape (no `@`-prefixed selector). Build the OpenResult by parsing `scope_data` and constructing a `SourceTreeZoneAddressSchema` address with a fresh session_token.
   - Wrap the per-scope handler's `OpenResult` into the protocol's `OpenResponse` shape with `Acquired{address, payload, scope, realized_write_semantics: WRITE_SEMANTICS_SYNC}`.
   - Unknown selector → return `Unavailable{}` and log a warning.

2. `commit.ts`, `abandon.ts`, `release.ts`: side-effect-free for v1. `release` revokes the session token bound to the claim (look up via `claimId` → token map; SessionTokenRegistry needs an inverse index). `commit` and `abandon` are no-ops (the producer's state is already durable in JSONL).

3. `capabilities.ts`:

   ```ts
   export function buildCapabilitiesResponse(): CapabilitiesResponse {
     return {
       write_semantics_allowed: ["WRITE_SEMANTICS_SYNC"],
       supports_split_scope: true,
       supports_scopes_conflict: true,
       protocols: ["claim_producer"],
       validation_supported_roles: [],
     };
   }
   ```

4. Tests for each: well-shaped request → well-shaped response. Open with bad selector → Unavailable + warn.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/claim-producer/ src/capabilities_test.ts
```

---

### T28. Producer: CrimefinderState wire — state mutation handlers

**Files:**
- `apps/crimefinder/producer/src/state/append-finding.ts`
- `apps/crimefinder/producer/src/state/append-finding_test.ts`
- `apps/crimefinder/producer/src/state/query-findings.ts`
- `apps/crimefinder/producer/src/state/query-findings_test.ts`
- `apps/crimefinder/producer/src/state/update-status.ts`
- `apps/crimefinder/producer/src/state/update-status_test.ts`
- `apps/crimefinder/producer/src/state/append-coverage.ts`
- `apps/crimefinder/producer/src/state/append-coverage_test.ts`
- `apps/crimefinder/producer/src/state/defer-finding.ts`
- `apps/crimefinder/producer/src/state/defer-finding_test.ts`
- `apps/crimefinder/producer/src/state/skip-zone.ts`
- `apps/crimefinder/producer/src/state/skip-zone_test.ts`
- `apps/crimefinder/producer/src/state/request-help.ts`
- `apps/crimefinder/producer/src/state/request-help_test.ts`
- `apps/crimefinder/producer/src/state/aggregate-findings.ts`
- `apps/crimefinder/producer/src/state/aggregate-findings_test.ts`

**Steps:**

1. Each handler takes a typed-request matching its proto definition + dependency injection of `JsonlStore`, `SessionTokenRegistry`, `Logger`, and (where relevant) `IterationCounter`, `TestCache`, `GitOps`.

2. Every handler starts by validating the `session_token`. Reject with `UNAUTHENTICATED` gRPC status if invalid.

3. **`append-finding.ts`**:
   - Validate input via `ReviewFindingInputSchema`.
   - Compute `fingerprint = computeFingerprint({file, symbol, description})`.
   - Look for an existing finding with the same fingerprint in the same pass — if found, return the existing finding's id with `effective_class: existing.class` and `auto_rerouted: false`. This handles re-discovery during fix-cycle re-reviews.
   - If `class in {1,2,3,4}` and `concept_slug` set:
     - Read the concept file via `readConcept(concepts_dir + slug + ".md")`. If file is missing, treat as no auto-route (the agent referenced a slug we can't verify; allow the original class but log a warning).
     - Call `shouldRerouteToClass5b({description, conceptBoundaries, conceptInvariants})`. If `true`, set `effective_class = "5b"`, `auto_rerouted = true`.
   - If `tension_slug` set and the tension file exists under `tensions_dir` (and is NOT under `_resolved/`):
     - Write a `tension_confirmation` row instead of a `finding` row. Return `{finding_id: <new id>, effective_class: <original>, auto_rerouted: false}` but also note in the response (or via a separate error_extras field) that this was a tension confirmation; the executor uses this to return `tension_already_cataloged` ALONGSIDE success.
   - Otherwise: build a `FindingRow` with `generateFindingId()`, write to JSONL, return.

4. **`query-findings.ts`**: read JSONL, materialize statuses, return rows matching the filter (by `pass_id`, by `class`, by `status`, etc.).

5. **`update-status.ts`**: append a `status_update` row. Validate `status` enum and required fields per `status` (e.g. `reason` required for `deferred`).

6. **`append-coverage.ts`**: append one `coverage` row per file in `files_read`.

7. **`defer-finding.ts`**: validate finding exists and is `open` or `fixing`. If `status:fixed`, return error `finding_already_resolved`. Otherwise append `status_update` row with `status:"deferred"`, `reason: <input.reason>`.

8. **`skip-zone.ts`**: append to passes.jsonl (or a separate skip-record row that ties into pass summary). Validate session is in review-zone role.

9. **`request-help.ts`**: append `help_request` row to findings.jsonl. Return `help_id`.

10. **`aggregate-findings.ts`**: same as the scope handler's logic but exposed as a typed RPC for the executor to call mid-session if needed. Optional; the spec doesn't require it as a gate but having it on the typed service surface is symmetrical.

11. Tests per handler. Cover happy paths and each documented error path (`finding_not_found`, `finding_already_resolved`, invalid input, missing token).

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/append-finding_test.ts src/state/query-findings_test.ts src/state/update-status_test.ts src/state/append-coverage_test.ts src/state/defer-finding_test.ts src/state/skip-zone_test.ts src/state/request-help_test.ts src/state/aggregate-findings_test.ts
```

---

### T29. Producer: CrimefinderState — RunTests handler

**Files:**
- `apps/crimefinder/producer/src/state/run-tests-handler.ts`
- `apps/crimefinder/producer/src/state/run-tests-handler_test.ts`

**Steps:**

1. Write `run-tests-handler.ts`. This is the gRPC wrapper around `runTests` from T19:

   ```ts
   export async function handleRunTests(
     req: { session_token: string },
     deps: { tokens, runTestsFn, config, repoRoot },
   ): Promise<{ exit_code: number; output_excerpt: string; ran_at: string; cached: boolean }>;
   ```

   - Validate token. Look up `passId` from the token.
   - If `config.tests` is missing: throw with gRPC status carrying `test_command_not_configured`.
   - Call `runTests({passId, repoRoot, command: config.tests.command, timeoutMs: config.tests.timeout_seconds * 1000})`.
   - Map TestResult to the response shape; concatenate `stdoutTail + "---STDERR---\n" + stderrTail` into `output_excerpt` (cap at 4 KB).

2. Tests: exercise the wrapper with mocked `runTestsFn`.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/run-tests-handler_test.ts
```

---

### T30. Producer: CrimefinderState — CommitFix atomic flow

**Files:**
- `apps/crimefinder/producer/src/state/commit-fix.ts`
- `apps/crimefinder/producer/src/state/commit-fix_test.ts`
- `apps/crimefinder/producer/src/state/commit-mutex.ts`

**Steps:**

1. Write `commit-mutex.ts` — a `JsonlMutex`-style mutex specifically for commits. One global mutex per producer process (serializes commits across all passes).

2. Write `commit-fix.ts`:

   ```ts
   export interface CommitFixArgs {
     session_token: string;
     finding_id: string;
     fix_description: string;
     commit_message: string;
   }
   export async function handleCommitFix(
     args: CommitFixArgs,
     deps: {
       tokens: SessionTokenRegistry;
       store: JsonlStore;
       git: GitOps;
       commitMutex: CommitMutex;
       testCache: TestCache;
       config: CrimefinderConfig;
       repoRoot: string;
       logger: Logger;
     },
   ): Promise<{ commit_sha: string; finding_status: "fixed" }>;
   ```

   Flow per spec:
   1. Validate `session_token`; extract `passId`.
   2. Acquire `commitMutex`.
   3. Read findings; locate `finding_id`; materialize its status.
   4. If finding doesn't exist → throw `finding_not_found`.
   5. If status is not `open`/`fixing` → throw `finding_already_resolved`.
   6. `git.status(repoRoot)` → if `clean`, throw `working_tree_clean`.
   7. Filter changed paths by the finding's `file` (path-prefix match — accept changes anywhere in the repo if the finding's file is directory-shaped; for v1 the simple rule is exact-prefix match, where the finding's file is a path under which changes count). If no changes overlap → throw `working_tree_changes_out_of_scope`.
   8. If `config.require_tests_before_commit`:
      - Look up cached test result for `passId`. If absent or `treeMtimeAtRun < git.mtime(repoRoot)` → throw `tests_not_recent`.
      - If `exit_code !== 0` → throw `tests_failed`.
   9. `git.add(repoRoot, [filteredPaths])`.
   10. Construct commit message: `<commit_message>\n\nResolves: <finding_id>`.
   11. `git.commit(repoRoot, fullMessage)` → returns `sha`. On `GitCommitError`, release mutex and throw `commit_failed` (extras: `stderr`).
   12. Append `status_update` row: `{kind:"status_update", id: generateRowId(), ts: <now>, ref: finding_id, status:"fixed", by_pass: passId, by_session: <session_id from token>, resolved_at_commit: sha}`.
   13. Release mutex; return `{commit_sha: sha, finding_status: "fixed"}`.

3. Tests cover each error path and the happy path. Use a tmp git repo. Mock `git.commit` for some tests; use real git for the integration paths.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/state/commit-fix_test.ts
```

---

### T31. Producer: Recovery scan

**Files:**
- `apps/crimefinder/producer/src/recovery/startup-scan.ts`
- `apps/crimefinder/producer/src/recovery/startup-scan_test.ts`

**Steps:**

1. Write `startup-scan.ts`. Runs once per producer boot:

   ```ts
   export async function runStartupRecovery(deps: {
     store: JsonlStore;
     git: GitOps;
     iterCounter: IterationCounter;
     repoRoot: string;
     logger: Logger;
   }): Promise<{
     reconstructedRowsAppended: number;
     iterationCountersRebuilt: number;
   }>;
   ```

   Steps:
   1. Read findings JSONL fully.
   2. Materialize current status per finding.
   3. Read recent commits via `git.log(repoRoot)` (limit to last N=500 commits or since the last `pass_finished` row's commit, whichever is older).
   4. For each commit body containing `Resolves: f_<id>`: if the referenced finding has no `status_update` row with `resolved_at_commit:<sha>`, append a corrective row: `{kind:"status_update", id: generateRowId(), ts: <commit timestamp via git show>, ref: finding_id, status:"fixed", by_pass: <inferred from finding's pass_id>, by_session: "recovery-scan", resolved_at_commit: sha, note:"reconstructed by startup recovery"}`. Count.
   5. Rebuild `IterationCounter` state by calling `counter.restore()` (T17). The counter reads all `iter_marker` rows from passes.jsonl and reconstructs the in-memory map. No additional reconstruction logic is needed in the recovery scan itself; the durable iter_marker rows are the source of truth.

2. Tests:
   - Fixture: commit with `Resolves: f_xyz` exists in `git log`, no matching `status_update` in JSONL → recovery adds the row.
   - Fixture: same commit but `status_update` already present → no action.
   - Fixture: malformed `Resolves:` footer → skipped.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/recovery/startup-scan_test.ts
```

---

### T32. Producer: health endpoint

**Files:**
- `apps/crimefinder/producer/src/health.ts`
- `apps/crimefinder/producer/src/health_test.ts`

**Steps:**

1. Write `health.ts`. A small Fastify app exposing `GET /health`:

   ```ts
   export interface HealthOptions {
     repoRoot: string;
     port: number;
     git: GitOps;
     store: JsonlStore;
     logger: Logger;
   }
   export async function startHealthServer(opts: HealthOptions): Promise<{ shutdown(): Promise<void> }>;
   ```

   `GET /health` returns 200 with `{status:"ok"}` iff:
   - `${repoRoot}/.crimefinder/` is writable (mkdir-if-missing succeeds).
   - `git.status(repoRoot)` succeeds (proves bind-mount and git binary are working).
   - Otherwise 503 with a JSON error body.

2. Tests using Fastify's inject API.

**Verification:**
```
cd apps/crimefinder/producer && npx vitest run src/health_test.ts
```

---

### T33. Producer: gRPC server bootstrap + main.ts

**Files:**
- `apps/crimefinder/producer/src/server.ts`
- `apps/crimefinder/producer/src/server_test.ts`
- `apps/crimefinder/producer/src/main.ts`

**Steps:**

1. Write `server.ts`:

   ```ts
   export interface ServerConfig {
     host: string;
     port: number;
     repoRoot: string;
     stateEndpointUrl: string;     // advertised in pass-state addresses
     logger: Logger;
   }
   export interface RunningServer {
     address: string;
     shutdown(): Promise<void>;
   }
   export async function startGrpcServer(cfg: ServerConfig): Promise<RunningServer>;
   ```

   Inside:
   - `const pkg = loadProducerProtos();`
   - Construct the shared dependencies once: `JsonlStore`, `SessionTokenRegistry`, `IterationCounter`, `TestCache`, `CommitMutex`, `GitOps`, `Config`.
   - Run `runStartupRecovery` before accepting connections.
   - Create a `new grpc.Server()`.
   - Register `pkg.rimsky.v1.ClaimProducer.service` with handlers:
     - `Capabilities`: returns `buildCapabilitiesResponse()`.
     - `Open`: calls `handleOpen`.
     - `Commit`, `Abandon`, `Release`: per T27.
     - `SplitScope`: calls `splitScope`.
     - `ScopesConflict`: calls `scopesConflict`.
   - Register `pkg.crimefinder.v1.CrimefinderState.service` with handlers from T28-T30.
   - `bindAsync(host:port, grpc.ServerCredentials.createInsecure())`.
   - Return shutdown handle.

2. `server_test.ts`: spin up the server on a random port; assert each RPC is reachable and returns well-shaped responses for trivial inputs.

3. Write `main.ts`. Reads env vars, constructs config, starts the server and the health endpoint, handles SIGTERM:

   ```ts
   import { startGrpcServer } from "./server.js";
   import { startHealthServer } from "./health.js";
   import pino from "pino";

   const env = {
     host: process.env.CRIMEFINDER_PRODUCER_HOST ?? "0.0.0.0",
     grpcPort: Number(process.env.CRIMEFINDER_PRODUCER_PORT_GRPC ?? "9100"),
     httpPort: Number(process.env.CRIMEFINDER_PRODUCER_PORT_HTTP ?? "9101"),
     repoRoot: process.env.CRIMEFINDER_PRODUCER_REPO_ROOT,
     stateEndpointUrl: process.env.CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL,
     logLevel: process.env.LOG_LEVEL ?? "info",
   };
   if (!env.repoRoot) { console.error("CRIMEFINDER_PRODUCER_REPO_ROOT required"); process.exit(2); }
   if (!env.stateEndpointUrl) { console.error("CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL required"); process.exit(2); }

   const logger = pino({ level: env.logLevel });
   const grpc = await startGrpcServer({ host: env.host, port: env.grpcPort, repoRoot: env.repoRoot, stateEndpointUrl: env.stateEndpointUrl, logger });
   const health = await startHealthServer({ repoRoot: env.repoRoot, port: env.httpPort, /* git, store via DI */ logger });

   const shutdown = async () => {
     await Promise.all([grpc.shutdown(), health.shutdown()]);
     process.exit(0);
   };
   process.on("SIGTERM", shutdown);
   process.on("SIGINT", shutdown);
   ```

**Verification:**
```
cd apps/crimefinder/producer && npx tsc --noEmit && npx vitest run src/server_test.ts
```

---

### T34. Producer: Dockerfile and compose fragment

**Files:**
- `apps/crimefinder/deploy/Dockerfile.producer`
- `apps/crimefinder/deploy/docker-compose.fragment.yml`
- `apps/crimefinder/deploy/rimsky.yml.fragment`

**Steps:**

1. Write `Dockerfile.producer` (model after `deploy/Dockerfile.claude-agent`):

   ```Dockerfile
   FROM node:20-alpine AS builder
   WORKDIR /build
   COPY apps/crimefinder/package.json apps/crimefinder/package-lock.json* ./
   COPY apps/crimefinder/shared/package.json ./shared/
   COPY apps/crimefinder/proto/package.json ./proto/
   COPY apps/crimefinder/producer/package.json ./producer/
   RUN npm ci || npm install
   COPY apps/crimefinder/ ./
   # Bring proto files into the image so proto-loader can find them at runtime.
   COPY protocols/proto/v1/ /build/protocols/proto/v1/
   RUN npm run build --workspaces --if-present

   FROM node:20-alpine
   RUN apk add --no-cache tini git
   WORKDIR /app
   COPY --from=builder /build/shared ./shared
   COPY --from=builder /build/proto ./proto
   COPY --from=builder /build/producer ./producer
   COPY --from=builder /build/node_modules ./node_modules
   COPY --from=builder /build/protocols ./protocols
   ENV NODE_ENV=production
   USER node
   EXPOSE 9100 9101
   ENTRYPOINT ["/sbin/tini", "--", "node", "producer/dist/main.js"]
   ```

2. Write `docker-compose.fragment.yml` (consumer merges into their compose):

   ```yaml
   services:
     crimefinder-producer:
       image: crimefinder/producer:latest
       environment:
         CRIMEFINDER_PRODUCER_HOST: "0.0.0.0"
         CRIMEFINDER_PRODUCER_PORT_GRPC: "9100"
         CRIMEFINDER_PRODUCER_PORT_HTTP: "9101"
         CRIMEFINDER_PRODUCER_REPO_ROOT: "/repo"
         # Consumer SHOULD override this to the host-reachable URL of
         # the same producer port (typically host.docker.internal:7081
         # when host-side executors dial back; or
         # crimefinder-producer:9100 when other containers dial).
         # The address bytes returned by Open carry this URL verbatim
         # to whichever holder needs it.
         CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL: "crimefinder-producer:9100"
       volumes:
         # Bind-mount the host repo at the SAME absolute path on host
         # and container so Claude CLI's host-side edits and the
         # producer's container-side git ops agree on file paths.
         - "${REPO_ROOT}:${REPO_ROOT}"
       ports:
         - "7081:9100"   # gRPC for host-executor → producer
         - "7082:9101"   # HTTP /health for host-side checks
       expose:
         - "9100"
         - "9101"
       healthcheck:
         test: ["CMD", "wget", "-q", "-O-", "http://localhost:9101/health"]
         interval: 5s
         timeout: 3s
         retries: 10
   ```

3. Write `rimsky.yml.fragment`:

   ```yaml
   # Crimefinder service registrations. Consumer merges into their rimsky.yml.
   executors:
     crimefinder:
       transport: grpc
       endpoint: "host.docker.internal:7071"
       tls: off
       protocols: [executor]

   claim_producers:
     crimefinder:
       endpoint: "grpc://crimefinder-producer:9100"
       protocols: [claim_producer]
       write_semantics_allowed: [sync]
   ```

4. Add a small parser-validator at `apps/crimefinder/deploy/validate-yaml.mjs`:

   ```js
   import fs from "node:fs";
   import yaml from "yaml";
   const paths = process.argv.slice(2);
   if (paths.length === 0) { console.error("usage: validate-yaml.mjs <file>..."); process.exit(2); }
   let failed = false;
   for (const p of paths) {
     try {
       yaml.parse(fs.readFileSync(p, "utf-8"));
       console.log(`${p}: OK`);
     } catch (e) {
       console.error(`${p}: ${e.message}`);
       failed = true;
     }
   }
   process.exit(failed ? 1 : 0);
   ```

   This relies on `yaml` already being a dependency of `@crimefinder/producer` (added in T10) — run the script from the producer directory so `node_modules/yaml` resolves.

**Verification:**
```
docker build -f apps/crimefinder/deploy/Dockerfile.producer -t crimefinder/producer:latest . && \
  cd apps/crimefinder/producer && node ../deploy/validate-yaml.mjs ../deploy/docker-compose.fragment.yml ../deploy/rimsky.yml.fragment
```
Docker build succeeds (exits 0). YAML validator prints `<path>: OK` for both files and exits 0. Either failure halts the verification.

---

### T35. Executor: package skeleton + proto-loader

**Files:**
- `apps/crimefinder/executor/package.json`
- `apps/crimefinder/executor/tsconfig.json`
- `apps/crimefinder/executor/src/proto-loader.ts`
- `apps/crimefinder/executor/src/proto-loader_test.ts`

**Steps:**

1. Write `apps/crimefinder/executor/package.json` (mirror claude-agent's package.json shape):

   ```json
   {
     "name": "@crimefinder/executor",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "main": "./dist/main.js",
     "bin": { "crimefinder-executor": "./dist/main.js" },
     "scripts": {
       "build": "tsc",
       "test": "vitest run",
       "typecheck": "tsc --noEmit",
       "lint": "eslint src",
       "start": "node dist/main.js"
     },
     "dependencies": {
       "@crimefinder/shared": "*",
       "@grpc/grpc-js": "^1.10.0",
       "@grpc/proto-loader": "^0.7.10",
       "@modelcontextprotocol/sdk": "^1.27.1",
       "fastify": "^4.29.1",
       "pino": "^8.17.0",
       "zod": "^3.22.4"
     }
   }
   ```

2. Write `executor/tsconfig.json` (parallel to shared and producer). Also write `apps/crimefinder/executor/vitest.config.ts` with `include: ["src/**/*_test.ts"]` — required for test discovery (see T3 step 4).

3. Write `executor/src/proto-loader.ts`. Loads three proto files. Apply the same one-level-deeper + container-aware adjustment as T10:

   **a. `executor.proto` and `executor_observability.proto` (lives at rimsky repo root `protocols/proto/v1/`).** Candidates from `apps/crimefinder/executor/src/proto-loader.ts`:

   ```ts
   const protocolsCandidates = [
     resolve(here, `../../../../protocols/proto/v1/${filename}`), // rimsky-root layout: executor/{src,dist}/ → rimsky/protocols/
     resolve(here, `../../../protocols/proto/v1/${filename}`),    // unusual layout; defensive
     resolve(here, `../../protocols/proto/v1/${filename}`),       // defensive (executor primarily runs as host process)
     resolve(here, `../protocols/proto/v1/${filename}`),          // defensive fallback
   ];
   ```

   **b. `crimefinder_state.proto` (lives at `apps/crimefinder/proto/v1/`).** Candidates from `executor/src/proto-loader.ts`:

   ```ts
   const crimefinderStateCandidates = [
     resolve(here, `../../proto/v1/${filename}`),    // standard: executor/{src,dist}/ → apps/crimefinder/proto/
     resolve(here, `../../../proto/v1/${filename}`), // defensive
   ];
   ```

   Use the same `existsSync` walk and throw-with-candidate-list error shape as claude-agent's `resolveProtoPath`. Note: the executor primarily runs as a host process (outside Docker) launched from the rimsky repo root, so `here` resolves under `apps/crimefinder/executor/{src,dist}/` and the first candidate hits.

   ```ts
   export interface ExecutorPackage {
     rimsky: { v1: {
       Executor: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
       ExecutorObservability: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
     } };
     crimefinder: { v1: {
       CrimefinderState: grpc.ServiceClientConstructor & { service: grpc.ServiceDefinition };
     } };
   }
   export function loadExecutorProtos(): ExecutorPackage;
   ```

4. Tests assert services load with non-empty method maps.

**Verification:**
```
cd apps/crimefinder/executor && npx tsc --noEmit && npx vitest run src/proto-loader_test.ts
```

---

### T36. Executor: state-client (gRPC client to producer)

**Files:**
- `apps/crimefinder/executor/src/state-client.ts`
- `apps/crimefinder/executor/src/state-client_test.ts`

**Steps:**

1. Write `state-client.ts`. Wraps the `CrimefinderState` gRPC client; one method per typed RPC. Constructor takes endpoint URL + session token; every method automatically includes the token in the request.

   ```ts
   export interface StateClientConfig {
     endpoint: string;       // e.g. "localhost:7081"
     sessionToken: string;
     logger: Logger;
   }
   export class StateClient {
     constructor(cfg: StateClientConfig);
     appendFinding(req: ReviewFindingInput): Promise<ReviewFindingOutput>;
     queryFindings(req: { /* ... */ }): Promise<{ findings: FindingsRow[] }>;
     updateFindingStatus(req: { /* ... */ }): Promise<{ success: boolean }>;
     appendCoverage(req: { files_read: string[] }): Promise<{ recorded_count: number }>;
     runTests(): Promise<{ exit_code: number; output_excerpt: string; ran_at: string; cached: boolean }>;
     commitFix(req: { finding_id: string; fix_description: string; commit_message: string }): Promise<{ commit_sha: string; finding_status: "fixed" }>;
     deferFinding(req: { finding_id: string; reason: string }): Promise<{ finding_id: string; finding_status: "deferred" }>;
     skipZone(req: { reason: string }): Promise<{ zone_id: string; skipped: true }>;
     requestHelp(req: { question: string; blocker_finding_id?: string }): Promise<{ help_id: string }>;
     aggregateFindings(req: { /* ... */ }): Promise<{ /* ... */ }>;
     close(): void;
   }
   ```

   Internally:
   - `const pkg = loadExecutorProtos();`
   - `this.client = new pkg.crimefinder.v1.CrimefinderState(endpoint, grpc.credentials.createInsecure());`
   - Each method wraps the unary call in a Promise; on gRPC error, maps `error.code` + `error.metadata` into a `GateErrorEnvelope` and throws a typed error.

2. Tests using a stub gRPC server (or against the real producer in a scenario test fixture — but for unit tests, mock the channel).

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/state-client_test.ts
```

---

### T37. Executor: internal MCP server

**Files:**
- `apps/crimefinder/executor/src/internal-mcp-server.ts`
- `apps/crimefinder/executor/src/internal-mcp-server_test.ts`
- `apps/crimefinder/executor/src/internal-mcp-tools.ts`
- `apps/crimefinder/executor/src/internal-mcp-tools_test.ts`
- `apps/crimefinder/executor/src/token-registry.ts`
- `apps/crimefinder/executor/src/token-registry_test.ts`

**Steps:**

1. Read `executors/claude-agent/src/internal-mcp-server.ts` and `internal-mcp-tools.ts` — these are the reference patterns. The crimefinder executor's MCP surface is the `review_*` gate set.

2. Write `token-registry.ts`. Per-run bearer token issued at agent spawn, validated on every MCP request:

   ```ts
   export class McpTokenRegistry {
     private tokens = new Map<string, { runId: string; issuedAt: number }>();
     issue(runId: string): string;
     validate(token: string): { runId: string } | null;
     revoke(token: string): void;
   }
   ```

3. Write `internal-mcp-tools.ts`. Zod schemas + tool definitions per gate. Mirror claude-agent's structure:

   ```ts
   export const TOOL_DEFINITIONS: ToolDefinition[] = [
     { name: "review_context", description: "...", inputSchema: { /* token-only */ } },
     { name: "review_finding", description: "...", inputSchema: { /* full input */ } },
     /* ... and so on for all 9 gates */
   ];

   export const ReviewContextInput = z.object({ token: z.string() });
   export const ReviewFindingInput = z.object({ token: z.string(), /* ... */ });
   /* ... */
   ```

4. Write `internal-mcp-server.ts`. Fastify-based loopback HTTP+JSON-RPC server (port chosen at startup; advertised to Claude CLI via `--mcp-config`):

   ```ts
   export interface CallbackServerHandle {
     port: number;
     baseUrl: string;       // e.g. "http://127.0.0.1:54321"
     mcpConfigPath: string; // path to a temp JSON file holding the MCP config
     shutdown(): Promise<void>;
   }
   export interface McpServerOptions {
     host: string;          // typically 127.0.0.1
     logger: Logger;
     // The gate dispatcher: invoked when the agent calls a tool.
     dispatch: (toolName: string, input: unknown, ctx: { token: string }) => Promise<unknown>;
   }
   export async function startCallbackServer(opts: McpServerOptions): Promise<CallbackServerHandle>;
   ```

   Internal flow:
   - Bind on `host:0` to get a random port.
   - For each request:
     1. Parse JSON-RPC envelope.
     2. Validate `Authorization: Bearer <token>` matches a registered token.
     3. Match tool name; if not in TOOL_DEFINITIONS → `-32601 method not found`.
     4. Validate input via the matching zod schema; on failure → `-32602 invalid params`.
     5. Call `dispatch(toolName, validatedInput, {token})`.
     6. If `dispatch` throws a `GateErrorEnvelope` → return that envelope verbatim.
     7. Otherwise → return `{result: <output>}`.
   - Write a temp MCP config file referencing this server's URL and the per-run token; return the path.

5. Tests cover: tool registration, auth, schema validation, dispatch routing, error envelope passthrough.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/token-registry_test.ts src/internal-mcp-tools_test.ts src/internal-mcp-server_test.ts
```

---

### T38. Executor: gate handlers

**Files:**
- `apps/crimefinder/executor/src/gates/review-context.ts`
- `apps/crimefinder/executor/src/gates/review-finding.ts`
- `apps/crimefinder/executor/src/gates/review-coverage.ts`
- `apps/crimefinder/executor/src/gates/review-complete.ts`
- `apps/crimefinder/executor/src/gates/review-run-tests.ts`
- `apps/crimefinder/executor/src/gates/review-commit-fix.ts`
- `apps/crimefinder/executor/src/gates/review-defer.ts`
- `apps/crimefinder/executor/src/gates/review-skip-zone.ts`
- `apps/crimefinder/executor/src/gates/review-request-help.ts`
- co-located `*_test.ts` for each

**Steps:**

1. Each gate is a thin function that translates the MCP-side input to a `StateClient` call:

   ```ts
   export async function reviewFinding(
     input: ReviewFindingInputFromAgent,
     deps: { stateClient: StateClient; emitNamedEvent: NamedEventEmitter; logger: Logger },
   ): Promise<ReviewFindingOutputToAgent>;
   ```

2. Per-gate logic:

   - **`review-context`**: calls `stateClient.queryFindings({pass_id, zone_id, kind: "open-in-zone"})` plus reads role-specific data the producer surfaces via its claim payload (which the executor cached at dispatch time from the address). Build the `ContextPayload` (role-polymorphic) per spec. The role comes from `userdata.mission` passed at dispatch.
   - **`review-finding`**: calls `stateClient.appendFinding(input)`. Emits `finding_emitted` named-event on success. If response indicates `auto_rerouted:true` → return success but include `crimefinder_error_class:"concept_citation_missing"` in a sibling field per spec. If response indicates tension-confirmation routing → also return success with `tension_already_cataloged`.
   - **`review-coverage`**: calls `stateClient.appendCoverage`. No event.
   - **`review-complete`**: queries findings for any `status:fixing` in this session; if present → throw `unresolved_findings_in_flight`. Otherwise calls `stateClient.updateFindingStatus` is N/A here — review-complete just emits `zone_completed` named-event and returns `{findings_recorded, coverage_pct}` computed from queryFindings + appendCoverage state.
   - **`review-run-tests`**: calls `stateClient.runTests()`. Emits `tests_ran` event.
   - **`review-commit-fix`**: calls `stateClient.commitFix`. Emits `finding_resolved` event with `commit_sha` and `iter_num`. On `commit_failed` from the producer → re-throw with `crimefinder_error_class:"commit_failed"`.
   - **`review-defer`**: calls `stateClient.deferFinding`. Emits `finding_deferred`.
   - **`review-skip-zone`**: calls `stateClient.skipZone`. Emits `zone_skipped`.
   - **`review-request-help`**: calls `stateClient.requestHelp`. Emits `help_requested`.

3. The `NamedEventEmitter` is a function the executor's agent-run pipeline passes in; it appends events to a per-run buffer that gets included in the final async-callback POST.

4. Tests per gate: mock `stateClient`, exercise the gate, assert the right call shape AND the right named-event emission.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/gates/
```

---

### T39. Executor: prompt loader (template-supplied prompts via userdata)

**Files:**
- `apps/crimefinder/executor/src/prompt-loader.ts`
- `apps/crimefinder/executor/src/prompt-loader_test.ts`

**Background:** Prompts are NOT hard-coded in the executor. The template's per-node `userdata` carries `system_prompt: <string>` and `user_prompt_template: <string>` (the same userdata fields claude-agent already reads); the template author populates them via the `source_file:` references that landed in spec `2026-05-19-multi-instance-template-ergonomics-design.md`, pointing at Markdown files under `templates/prompts/`. The rimsky CLI resolves `source_file:` references to file content at `rimsky template register` time, so by the time `userdata` reaches the executor it carries the already-inlined prompt text.

The actual prompt content lives in T47's deliverables (the eight `.md` files under `templates/prompts/`) — those are part of the template package and customizable per consumer-repo.

**Steps:**

1. Write `prompt-loader.ts`:

   ```ts
   import type { Logger } from "pino";

   export interface ResolvedPrompts {
     systemPrompt: string;
     userPrompt: string;
   }
   export interface PromptLoaderInput {
     mission: string;          // userdata.mission, e.g. "review-zone"
     systemPromptFromUserdata?: string;
     userPromptTemplateFromUserdata?: string;
     // Whitespace-trim only; no markdown rendering, no executor-side templating.
   }
   export function loadPrompts(input: PromptLoaderInput, logger: Logger): ResolvedPrompts;
   ```

   Behavior:
   - If both `systemPromptFromUserdata` and `userPromptTemplateFromUserdata` are non-empty strings, return them verbatim (trimmed). This is the expected hot path — every consumer who registers the bundled template gets prompts from `source_file:` resolution.
   - If either is missing or empty, log a `warn` (`{event: "prompt_missing_from_userdata", mission, missing: "system" | "user" | "both"}`) and fall back to a minimal bundled default keyed on `mission`. The bundled defaults are intentionally terse — they exist as a safety net so the executor never spawns Claude CLI with empty prompts. A consumer using the unmodified bundled template never hits the fallback path.
   - Trim trailing whitespace; do NOT interpolate variables (the prompts are passed verbatim to the Claude CLI subprocess; the agent obtains structured context from the `review_context` MCP gate, not from prompt-template interpolation).

2. Bundled fallback prompts are minimal — six- to ten-line strings stored as TS constants in `prompt-loader.ts`. They cover review-zone, fix-cycle, re-review, and dedup. Each fallback explicitly cites that the rich prompt belongs in the template's `userdata.system_prompt` / `userdata.user_prompt_template` and instructs the agent to call `review_context` for mission detail.

3. Tests:
   - Hot path: both prompts supplied → returns them verbatim (trimmed).
   - Missing system: logs warn, returns fallback system + supplied user.
   - Missing both: logs warn, returns full fallback for the mission.
   - Unknown mission with no userdata: logs warn AND fails with a typed error — "unknown mission `<value>`" — because we cannot safely guess.
   - Trim verification: trailing whitespace on supplied prompts is stripped.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/prompt-loader_test.ts
```

---

### T40. Executor: cli-runner + cli-env (lifted)

**Files:**
- `apps/crimefinder/executor/src/cli-env.ts`
- `apps/crimefinder/executor/src/cli-env_test.ts`
- `apps/crimefinder/executor/src/cli-runner.ts`
- `apps/crimefinder/executor/src/cli-runner_test.ts`

**Steps:**

1. Read `executors/claude-agent/src/cli-env.ts` end-to-end. The file's main export is `buildCliEnv(config: CliAuthConfig): CliEnvResult`. Port verbatim to `apps/crimefinder/executor/src/cli-env.ts` with `@source:` annotation:

   ```ts
   /**
    * @source: executors/claude-agent/src/cli-env.ts
    * @diverged: false
    */
   ```

   Exports the same `buildCliEnv(config: CliAuthConfig): CliEnvResult` function and its `CliAuthConfig` / `CliEnvResult` types verbatim. `buildCliEnv` synchronously produces the env map plus any temp-file cleanup handle the API-key-helper path requires; do NOT rename it to `prepareAuthEnv` or change the return shape.

2. Read `executors/claude-agent/src/cli-runner.ts`. This is more divergent (claude-agent has logic for `cwd_from_store`, attribute writeback, etc.). Crimefinder's cli-runner just needs:
   - Spawn `claude` binary with `--mcp-config <path>`, `--allowedTools <list>`, working directory, system prompt, user prompt.
   - Stream stdout/stderr.
   - Return final outcome (exit code + final text response).

   Port a stripped-down version; mark `@source:` + `@diverged: true`, `@reason: removed cwd_from_store and attribute-writeback paths; crimefinder uses a fixed cwd (the repo root) and routes attributes via review-finding gate instead`.

3. Tests as in claude-agent's `cli-runner_test.ts` — spawn `node -e "..."` as a stand-in for `claude` to exercise the runner without Anthropic credentials.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/cli-env_test.ts src/cli-runner_test.ts
```

---

### T41. Executor: silence-watch

**Files:**
- `apps/crimefinder/executor/src/silence-watch.ts`
- `apps/crimefinder/executor/src/silence-watch_test.ts`

**Steps:**

1. Write `silence-watch.ts`. A timer that fires `silence_timeout` if no stdout/MCP activity in N ms. Reset on every byte of stdout AND every MCP tool call.

   ```ts
   export class SilenceWatch {
     constructor(opts: { timeoutMs: number; onTimeout: () => void; logger: Logger });
     touch(): void;     // reset timer
     stop(): void;      // clear timer
   }
   ```

2. Tests with `vi.useFakeTimers()`: advance time; assert `onTimeout` fires; assert `touch()` resets.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/silence-watch_test.ts
```

---

### T42. Executor: stub mode

**Files:**
- `apps/crimefinder/executor/src/stub-mode.ts`
- `apps/crimefinder/executor/src/stub-mode_test.ts`

**Steps:**

1. Write `stub-mode.ts`. When `env:CRIMEFINDER_EXECUTOR_STUB_MODE=1`, the executor does NOT spawn Claude CLI; instead it reads canned outcomes from `userdata.stub_outcome` and returns immediately:

   ```ts
   // Terminal variants match the AsyncCallbackBody oneof from T44 so the
   // stub's output flows through the same path as a real agent run.
   export type StubTerminal =
     | { variant: "success"; attributes_delta?: Record<string, unknown>; changed?: boolean; change_summary?: string | null }
     | { variant: "error"; error_class: string; payload?: unknown }
     | { variant: "park"; reason: string; reason_note?: string; resume_at?: string };

   export interface StubOutcome {
     gates_to_call: Array<{ name: string; input: unknown }>;
     terminal: StubTerminal;
     delay_ms?: number;
   }
   export const StubOutcomeSchema: z.ZodSchema<StubOutcome>;

   export async function runStubAgent(args: {
     userdata: Record<string, unknown>;
     dispatch: (toolName: string, input: unknown) => Promise<unknown>;
     logger: Logger;
   }): Promise<{ outcome: AgentOutcome }>;
   ```

   Behavior: parses `userdata.stub_outcome` via the schema; if missing, returns a default canned outcome (`{variant:"success", attributes_delta:{stub:true}}` after 50ms — mirror claude-agent). If present, calls each gate in `gates_to_call` order via `dispatch`, then returns the specified terminal mapped onto the `AgentOutcome` shape from T43.

2. Tests cover: default outcome, custom outcomes with multiple gate calls, error terminals.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/stub-mode_test.ts
```

---

### T43. Executor: agent-run orchestration

**Files:**
- `apps/crimefinder/executor/src/agent-run.ts`
- `apps/crimefinder/executor/src/agent-run_test.ts`

**Steps:**

1. Write `agent-run.ts`. Top-level orchestration per dispatch:

   ```ts
   export interface AgentRunArgs {
     dispatchId: string;
     userdata: { mission: string; stub_outcome?: unknown; [k: string]: unknown };
     stores: Array<{ alias: string; address: Uint8Array }>; // from ExecuteRequest
     callbackUrl: string;        // supervisor-supplied
     silenceTimeoutMs: number;
     stubMode: boolean;
     cliAuth?: CliAuthConfig;
     logger: Logger;
   }
   // AgentOutcome carries the typed pre-wire outcome. The terminal
   // variant (success / error / park) and the events list map directly
   // onto the AsyncCallbackBody proto in T44; do NOT introduce a
   // {kind, terminal} envelope here that doesn't exist on the wire.
   export type AgentOutcome = {
     events: Array<{ name: string; payload: Uint8Array }>;
   } & (
     | { variant: "success"; attributesDelta?: Record<string, unknown>; changed?: boolean; changeSummary?: string | null }
     | { variant: "error"; errorClass: ExecutorErrorClass; payload?: Uint8Array }
     | { variant: "park"; reason: string; reasonNote?: string; resumeAt?: string }
   );
   export async function runAgent(args: AgentRunArgs): Promise<AgentOutcome>;
   ```

2. Flow:
   1. Decode each store address via `decodeAddress`. Identify the `pass-state` address (or a `source-tree-zone` address); extract `state_endpoint_url` and `session_token`.
   2. Construct `StateClient` with those.
   3. Build the dispatch function for the MCP server: maps tool name → gate handler from T38, passing the StateClient and a per-run named-event buffer.
   4. Start the internal MCP server; pass the dispatch function.
   5. Resolve prompts via `loadPrompts({mission: userdata.mission, systemPromptFromUserdata: userdata.system_prompt, userPromptTemplateFromUserdata: userdata.user_prompt_template}, logger)` from T39. The bundled template populates `userdata.system_prompt` / `userdata.user_prompt_template` via `source_file:` references to `templates/prompts/*.md`; the loader returns them verbatim. The agent obtains structured context (zone files, concept docs, assigned findings, etc.) via the `review_context` MCP gate during the session, NOT via prompt-template interpolation in this layer.
   6. If `stubMode`: call `runStubAgent` and skip Claude CLI.
   7. Else: spawn Claude CLI via `cli-runner`, with `--mcp-config <mcpServer.mcpConfigPath>`, `--allowedTools <whitelist>`, and the resolved prompts. Start `SilenceWatch`. Stream output. On terminal (CLI exits), tear down.
   8. Build `AgentOutcome` (per the wire shape defined in step 1's `AgentOutcome` type) including the collected `events: [{name, payload}]` list.

3. Tests in stub mode exercise the full happy path and error paths.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/agent-run_test.ts
```

---

### T44. Executor: gRPC server with async-callback handoff

**Files:**
- `apps/crimefinder/executor/src/server.ts`
- `apps/crimefinder/executor/src/server_test.ts`

**Steps:**

1. Read `executors/claude-agent/src/server.ts` (`outcomeToCallbackBody` around line 480 and `buildCallbackUrl` around line 466) and `protocols/proto/v1/executor.proto` (`AsyncCallbackBody`, around lines 251-271). Port the same async-callback shape. Key points:
   - On `Execute(ExecuteRequest)`:
     1. Mint a `callbackAckId` (or use the supervisor's `dispatch_id`).
     2. Send one `Heartbeat`.
     3. Send `StreamClose{AwaitAsyncCallback{ack_id, callback_url}}` (the request's `callback_url`).
     4. Close the stream.
   - In the background: call `runAgent(args)` with the request's stores/userdata/callback_url.
   - POST the final outcome. **Body shape is the wire `AsyncCallbackBody`, NOT a `{ack_id, terminal: ...}` wrapper.** The supervisor extracts `ack_id` from the URL path (per `buildCallbackUrl`: `${callback_url}/v1/callback/${async_ack_id}`), and the body carries:

     ```jsonc
     {
       "events": [ /* repeated NamedEvent: { name: string, payload: bytes } */ ],
       // exactly one of these three keys is set (the oneof outcome):
       "success": { /* attributes_delta etc. per proto */ } ,
       // OR
       "error":   { "error_class": "<string>", "payload": <bytes-or-null> },
       // OR
       "park":    { "reason": "<string>", "reason_note": "<string?>", "resume_at": "<string?>" }
     }
     ```

   See `code:executors/claude-agent/src/server.ts::outcomeToCallbackBody` for the canonical mapping. The legacy `{type: ...}` discriminator and the invented `{terminal: ...}` wrapper are NOT accepted by the supervisor. NamedEvents are top-level on the body, not nested inside the outcome variant.

2. Tests as in claude-agent's `server_test.ts`: spin up the server with a stub CallbackServer + fake post; assert the async-callback POST shape on completion.

**Verification:**
```
cd apps/crimefinder/executor && npx vitest run src/server_test.ts
```

---

### T45. Executor: capabilities + observability + main.ts

**Files:**
- `apps/crimefinder/executor/src/capabilities.ts`
- `apps/crimefinder/executor/src/userdata-schema.ts`
- `apps/crimefinder/executor/src/observability.ts`
- `apps/crimefinder/executor/src/main.ts`

**Steps:**

1. Write `userdata-schema.ts`. JSON Schema for executor userdata, matching the spec's "Mission carrier" section AND the userdata fields claude-agent already reads (`executors/claude-agent/src/userdata-schema.ts:44-45`). Required top-level keys: `mission` (enum: `review-zone`, `fix-cycle`, `dedup`, `re-review`). Optional top-level keys: `system_prompt` (string), `user_prompt_template` (string), `stub_outcome`, `model`, `assigned_findings` (for fix-cycle role). The `system_prompt` and `user_prompt_template` fields are the SAME names claude-agent uses and the SAME names T39's prompt loader and T43's agent-run read from — keep them in lockstep; do not introduce `_override` suffixes. Export the schema and `declaredEvents` (12-name list from `@crimefinder/shared`'s NAMED_EVENT_NAMES).

2. Write `capabilities.ts`. Returns `ExecutorObservability.Capabilities`:

   ```ts
   export function buildCapabilitiesResponse(httpBridgeUrl: string | undefined) {
     return {
       userdata_schema: userdataSchemaBytes(),
       declared_events: declaredEvents,
       http_bridge_url: httpBridgeUrl ?? "",
     };
   }
   ```

3. Write `observability.ts`. Pino logger factory + an Observability ledger interface (lifted from claude-agent shape, simplified):

   ```ts
   export interface Observability {
     stepStarted(dispatchId: string): void;
     stepCompleted(dispatchId: string): void;
     stepFailed(dispatchId: string, errorClass: string): void;
   }
   export function createLogger(opts: { level: string }): Logger;
   ```

4. Write `main.ts`. Reads env vars, starts gRPC server. Env vars per spec / claude-agent precedent:

   ```ts
   const env = {
     host: process.env.CRIMEFINDER_EXECUTOR_HOST ?? "127.0.0.1",
     grpcPort: Number(process.env.CRIMEFINDER_EXECUTOR_PORT_GRPC ?? "7071"),
     silenceTimeoutMs: Number(process.env.CRIMEFINDER_EXECUTOR_SILENCE_MS ?? "120000"),
     callbackHost: process.env.CRIMEFINDER_EXECUTOR_CALLBACK_HOST ?? "127.0.0.1",
     stubMode: process.env.CRIMEFINDER_EXECUTOR_STUB_MODE === "1",
     anthropicApiKey: process.env.ANTHROPIC_API_KEY,
     claudeOauthToken: process.env.CLAUDE_CODE_OAUTH_TOKEN,
     logLevel: process.env.LOG_LEVEL ?? "info",
   };
   if (!env.stubMode && !env.anthropicApiKey && !env.claudeOauthToken) {
     console.error("ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN required (non-stub mode)");
     process.exit(2);
   }
   // ... build cliAuth, start server, handle SIGTERM
   ```

**Verification:**
```
cd apps/crimefinder/executor && npx tsc --noEmit
```
Then start the executor briefly: `node dist/main.js &; sleep 1; kill %1` (after `npm run build`). Should print a startup log line indicating gRPC bind, then exit clean.

---

### T46. CLI: pass / status / up / down

**Files:**
- `apps/crimefinder/cli/package.json`
- `apps/crimefinder/cli/tsconfig.json`
- `apps/crimefinder/cli/src/main.ts`
- `apps/crimefinder/cli/src/commands/pass.ts`
- `apps/crimefinder/cli/src/commands/status.ts`
- `apps/crimefinder/cli/src/commands/up.ts`
- `apps/crimefinder/cli/src/commands/down.ts`
- `apps/crimefinder/cli/src/rimsky-cli.ts`
- co-located `*_test.ts`

**Steps:**

1. Write `package.json` with `"bin": {"crimefinder": "./dist/main.js"}`. Also write `apps/crimefinder/cli/tsconfig.json` and `apps/crimefinder/cli/vitest.config.ts` (the latter with `include: ["src/**/*_test.ts"]` per T3 step 4).

2. Write `main.ts`. Reads `process.argv`, dispatches by first non-flag arg:

   ```ts
   const cmd = process.argv[2];
   switch (cmd) {
     case "pass": exit(await runPass(process.argv.slice(3))); break;
     case "status": exit(await runStatus(process.argv.slice(3))); break;
     case "up": exit(await runUp(process.argv.slice(3))); break;
     case "down": exit(await runDown(process.argv.slice(3))); break;
     default: printUsage(); process.exit(cmd ? 2 : 0);
   }
   ```

3. Write `rimsky-cli.ts`. Thin subprocess wrappers:

   ```ts
   export async function rimskyTemplateRegister(yamlPath: string, opts: { tag?: string }): Promise<{ template_hash: string }>;
   export async function rimskyTemplateDeploy(hashOrTag: string): Promise<void>;
   export async function rimskyInstanceCreate(template: string, params: Record<string, unknown>): Promise<{ instance_id: string }>;
   export async function rimskyInstanceGet(instanceId: string): Promise<unknown>;
   ```

   Each function runs `execFile("rimsky", [verb, ...args])`, parses the JSON output (`--format json`), and returns. Surface stderr to the caller's stderr on non-zero exit.

4. Write each command:

   - **`pass.ts`**: parses `--mission`, `--repo` (default cwd). Registers the template YAML if not yet registered (uses a fixed tag `@crimefinder-code-review-pass`). Creates an instance with params `{repo_root: <abs>, mission}`. Streams `rimskyInstanceGet` periodically until the instance is terminal; prints summary.

   - **`status.ts`**: parses `--repo`. Reads `.crimefinder/passes.jsonl`; aggregates open findings from `.crimefinder/findings.jsonl`; prints a status table.

   - **`up.ts`**: shell out to `docker compose <-f flags> up -d` AND start the host executor as a child process. Configuration is taken entirely from CLI flags + env vars + sensible defaults — no `.crimefinder/runtime.yml` indirection:
     - `--compose-file <path>` (repeatable; default `./docker-compose.yml` in the current working directory).
     - `--executor-path <path>` (default `apps/crimefinder/executor/dist/main.js`, resolved relative to the rimsky repo root via `git rev-parse --show-toplevel`).
     - `--executor-port <n>` (default 7071; flows through to `env:CRIMEFINDER_EXECUTOR_PORT_GRPC` on the spawned executor).
     - On start, write the executor's PID to `.crimefinder/runtime/executor.pid` in the repo (mkdir as needed). This file is read by `down`.

   - **`down.ts`**: shell out to `docker compose <-f flags> down` (same `--compose-file` flag set) and read `.crimefinder/runtime/executor.pid`; if present, send SIGTERM to the PID. Remove the pid file. If the PID file is absent, the executor isn't tracked; print a warning and continue with `docker compose down` only.

5. Tests per command using subprocess mocks.

**Verification:**
```
cd apps/crimefinder/cli && npx tsc --noEmit && npx vitest run
```

---

### T47. Template YAML and bundled prompt files

**Files:**
- `apps/crimefinder/templates/code-review-pass.yml`
- `apps/crimefinder/templates/validate.mjs` (template structural validator)
- `apps/crimefinder/templates/prompts/review-zone.system.md`
- `apps/crimefinder/templates/prompts/review-zone.user.md`
- `apps/crimefinder/templates/prompts/fix.system.md`
- `apps/crimefinder/templates/prompts/fix.user.md`
- `apps/crimefinder/templates/prompts/re-review.system.md`
- `apps/crimefinder/templates/prompts/re-review.user.md`
- `apps/crimefinder/templates/prompts/dedup.system.md`
- `apps/crimefinder/templates/prompts/dedup.user.md`

**Background:** The template incorporates two ergonomic features that landed in spec `2026-05-19-multi-instance-template-ergonomics-design.md`:

- **`source_file:` references** (Item 2 of that spec): anywhere a string position exists in the template, the YAML may carry `{source_file: <relative-path>}`. The `rimsky template register` CLI resolves these at register time before the wire call. Crimefinder uses this to keep prompts in editable Markdown files, customizable per consumer-repo. Paths are relative to the template YAML's directory.
- **Node-level `tags:`** (Item 4 of that spec): every crimefinder node carries `tags: [crimefinder, <role>]` so operators can filter the rimsky dashboard / events surface to crimefinder-only or per-role views.

**Steps:**

0. Write the prompt files at `apps/crimefinder/templates/prompts/`. Eight files total, four missions × {system, user}. Each system prompt establishes the agent's role and points at the MCP gates available. Each user prompt opens the task and points the agent at `review_context` for structured detail. The prompts are intentionally short (10-40 lines each) — they set up the work, but the actual structured context (zone files, concept docs, assigned findings) reaches the agent via the `review_context` MCP gate during the session. Initial content can be drafted from the prototype's `code:/Users/patrick/Documents/projects/research/crimefinder/src/templates/prompts/hunt-zone-system.md.hbs` as a starting point, adapted per mission:

   - `review-zone.system.md`: "You are a code-review agent assigned to one zone of a repository. Use the `review_context` tool to load your zone's files, concept docs, and open tensions. Emit findings via `review_finding`; report coverage incrementally via `review_coverage`; call `review_complete` when done. ..."
   - `fix.system.md`: "You are a fix-cycle agent assigned one or more open findings to resolve. Use `review_context` to load assigned findings and concept docs. Edit files via `Edit`/`Write`. Run tests via `review_run_tests`. Commit each fix via `review_commit_fix` (the only path to a git commit). ..."
   - `re-review.system.md`: similar to review-zone but scoped to zones touched in this iteration; emphasize "verify the fixes didn't introduce regressions" framing.
   - `dedup.system.md`: "You are a deduplication agent. The producer has grouped findings by file; for each group, identify rows describing the same underlying problem and call `review_finding` updates ..." (Note: dedup currently routes through `dedup-batch` gate; if a dedicated gate doesn't exist, the dedup mission uses `query_findings` and `update_finding_status` via the StateClient via the available gate vocabulary — verify the prompt does not promise tools that aren't in the `--allowedTools` whitelist.)

   Each `.user.md` is a short opener (3-10 lines) that prompts the first move (e.g. "Call `review_context` now and begin.").

1. Write the template per spec §"Template and graph topology". Use `stores:` (matching the rimsky parser, per the drift note in spec §"Subfolder layout"). Use the `subscribes:` directive for explicit ordering and rely on `{{nodes.X.attribute.Y}}` substitution-ref inference for implicit subscriptions. Every node carries `tags:`. Every executor-dispatching node's `userdata` carries `system_prompt` and `user_prompt_template` via `source_file:` references to the `prompts/` directory.

2. Template structure (every node fully spelled out):

   ```yaml
   name: code-review-pass
   version: "1.0"
   frame_resolution_mode: serial_queue

   nodes:
     - type: open-pass
       tags: [crimefinder, setup]
       stores:
         - name: pass-state
           selector: "@pass-state:new&mission={{params.mission}}&trigger={{params.trigger}}"
           intent: rw
           lifetime: subgraph
       attributes:
         schema:
           type: object
           properties:
             pass_id:
               type: string
               source: "{{claim.pass-state.payload.pass_id}}"
           required: [pass_id]

     - type: discover-context
       tags: [crimefinder, setup]
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: context-scan
           selector: "@context-scan:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: r
       attributes:
         schema:
           type: object
           properties:
             context_manifest:
               type: object
               source: "{{claim.context-scan.payload}}"

     - type: review-fan-out
       tags: [crimefinder, fan-out, review]
       executor: crimefinder
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: source-tree
           selector: "@source-tree:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: r
       fan_out:
         claim: source-tree
         partition_request: '{"kind":"source-tree-partition","pass_id":"{{nodes.open-pass.attribute.pass_id}}","ignore_patterns_from_config":true}'
         error_policy: { kind: best_effort }
       userdata:
         mission: "review-zone"
         system_prompt:        { source_file: prompts/review-zone.system.md }
         user_prompt_template: { source_file: prompts/review-zone.user.md }
       error_types:
         silence_timeout: { policy: [{ action: retry, count: 1 }, { action: pass }] }
         tool_error:      { policy: [{ action: retry, count: 2 }, { action: pass }] }

     - type: aggregate-initial
       tags: [crimefinder, aggregate]
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: agg
           selector: "@aggregate-findings:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: r
       subscribes:
         - { node: review-fan-out, on: state }
       attributes:
         schema:
           type: object
           properties:
             class_1_4_remaining: { type: integer, source: "{{claim.agg.payload.class_1_4_remaining}}" }
             class_5: { type: array, source: "{{claim.agg.payload.class_5}}" }
             dedup_file_groups: { type: array, source: "{{claim.agg.payload.dedup_file_groups}}" }

     - type: dedup-fan-out
       tags: [crimefinder, fan-out, dedup]
       executor: crimefinder
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: dedup-grouping
           selector: "@dedup-grouping:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: rw
       fan_out:
         claim: dedup-grouping
         partition_request: '{"kind":"dedup-partition","pass_id":"{{nodes.open-pass.attribute.pass_id}}","file_groups":{{nodes.aggregate-initial.attribute.dedup_file_groups}}}'
         error_policy: { kind: best_effort }
       userdata:
         mission: "dedup"
         system_prompt:        { source_file: prompts/dedup.system.md }
         user_prompt_template: { source_file: prompts/dedup.user.md }
       error_types:
         silence_timeout: { policy: [{ action: pass }] }
         tool_error:      { policy: [{ action: retry, count: 1 }, { action: pass }] }

     - type: class-split
       tags: [crimefinder, aggregate]
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: split
           selector: "@class-split:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: r
       subscribes:
         - { node: dedup-fan-out, on: state }
       attributes:
         schema:
           type: object
           properties:
             class_1_4_remaining: { type: boolean, source: "{{claim.split.payload.class_1_4_remaining}}" }
             class_5_findings:    { type: array,   source: "{{claim.split.payload.class_5_findings}}" }

     - type: fix-iter-1
       tags: [crimefinder, fix-cycle]
       delegate: fix-iteration
       holds:
         pass-state: { from: open-pass }
       subscribes:
         - { node: class-split, on: state }

     - type: fix-iter-2
       tags: [crimefinder, fix-cycle]
       delegate: fix-iteration
       holds:
         pass-state: { from: open-pass }
       subscribes:
         - { node: fix-iter-1, on: state }

     - type: fix-iter-3
       tags: [crimefinder, fix-cycle]
       delegate: fix-iteration
       holds:
         pass-state: { from: open-pass }
       subscribes:
         - { node: fix-iter-2, on: state }

     - type: class-5-finalize
       tags: [crimefinder, aggregate]
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: c5
           selector: "@class-5-finalize:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: r
       subscribes:
         - { node: fix-iter-3, on: state }
       attributes:
         schema:
           type: object
           properties:
             class_5_open:           { type: integer, source: "{{claim.c5.payload.class_5_open}}" }
             class_5_resolved:       { type: integer, source: "{{claim.c5.payload.class_5_resolved}}" }
             class_5_deferred:       { type: integer, source: "{{claim.c5.payload.class_5_deferred}}" }
             class_5_queued_to_spec: { type: integer, source: "{{claim.c5.payload.class_5_queued_to_spec}}" }

     - type: report
       tags: [crimefinder, report]
       holds:
         pass-state: { from: open-pass }
       stores:
         - name: rpt
           selector: "@report:pass_id={{nodes.open-pass.attribute.pass_id}}"
           intent: rw
       subscribes:
         - { node: class-5-finalize, on: state }
       attributes:
         schema:
           type: object
           properties:
             summary: { type: object, source: "{{claim.rpt.payload}}" }

   graphs:
     - name: fix-iteration
       entry: iter-guard
       exit: iter-aggregate
       nodes:
         - type: iter-guard
           tags: [crimefinder, fix-cycle, iter-guard]
           stores:
             - name: unresolved-check
               selector: "@unresolved-class-1-4:pass_id={{nodes.open-pass.attribute.pass_id}}"
               intent: r
           attributes:
             schema:
               type: object
               properties:
                 pass_id:
                   type: string
                   source: "{{nodes.open-pass.attribute.pass_id}}"
                 iter_num:
                   type: integer
                   source: "{{claim.unresolved-check.payload.iter_num}}"
                 affected_zones:
                   type: array
                   source: "{{claim.unresolved-check.payload.affected_zones}}"
                 skipped:
                   type: boolean
                   source: "{{claim.unresolved-check.payload.skipped}}"

         - type: fix-fan-out
           tags: [crimefinder, fix-cycle, fan-out, fix]
           executor: crimefinder
           holds:
             pass-state: { from: iter-guard }
           stores:
             - name: fix-partition
               selector: "@fix-partition:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
               intent: rw
           fan_out:
             claim: fix-partition
             partition_request: '{"kind":"fix-partition","pass_id":"{{nodes.iter-guard.attribute.pass_id}}","iter_num":{{nodes.iter-guard.attribute.iter_num}},"affected_zones":{{nodes.iter-guard.attribute.affected_zones}}}'
             error_policy: { kind: best_effort }
           userdata:
             mission: "fix-cycle"
             system_prompt:        { source_file: prompts/fix.system.md }
             user_prompt_template: { source_file: prompts/fix.user.md }
           error_types:
             silence_timeout: { policy: [{ action: retry, count: 1 }, { action: pass }] }
             tests_failed:    { policy: [{ action: pass }] }
             commit_failed:   { policy: [{ action: pass }] }
             tool_error:      { policy: [{ action: retry, count: 1 }, { action: pass }] }

         - type: re-review-affected
           tags: [crimefinder, fix-cycle, fan-out, re-review]
           executor: crimefinder
           holds:
             pass-state: { from: iter-guard }
           stores:
             - name: re-review-partition
               selector: "@re-review-partition:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
               intent: r
           fan_out:
             claim: re-review-partition
             partition_request: '{"kind":"re-review-partition","pass_id":"{{nodes.iter-guard.attribute.pass_id}}","iter_num":{{nodes.iter-guard.attribute.iter_num}},"affected_zones":{{nodes.iter-guard.attribute.affected_zones}}}'
             error_policy: { kind: best_effort }
           userdata:
             mission: "re-review"
             system_prompt:        { source_file: prompts/re-review.system.md }
             user_prompt_template: { source_file: prompts/re-review.user.md }
           subscribes:
             - { node: fix-fan-out, on: state }
           error_types:
             silence_timeout: { policy: [{ action: retry, count: 1 }, { action: pass }] }
             tool_error:      { policy: [{ action: retry, count: 2 }, { action: pass }] }

         - type: iter-aggregate
           tags: [crimefinder, fix-cycle, iter-aggregate]
           holds:
             pass-state: { from: iter-guard }
           stores:
             - name: iter-result
               selector: "@iter-aggregate:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
               intent: r
           subscribes:
             - { node: re-review-affected, on: state }
           attributes:
             schema:
               type: object
               properties:
                 more_work_needed:             { type: boolean, source: "{{claim.iter-result.payload.more_work_needed}}" }
                 findings_resolved_this_iter:  { type: integer, source: "{{claim.iter-result.payload.findings_resolved_this_iter}}" }
   ```

3. Add a build-time check: a small node script that parses the YAML and asserts (a) every node has a unique `type`, (b) every `subscribes:` target refers to a sibling node in the same graph, (c) sub-graph internal nodes only reference `iter-guard` or other sub-graph internals in `holds:` and `subscribes:` (the `attributes.source: "{{nodes.open-pass.attribute.pass_id}}"` on iter-guard is allowed because after `concept:delegation` absorption it sits in main alongside open-pass).

   Script: `apps/crimefinder/templates/validate.mjs`:

   ```js
   import fs from "node:fs";
   import yaml from "yaml";
   const doc = yaml.parse(fs.readFileSync(process.argv[2], "utf-8"));
   const errors = [];

   // Cross-graph node-type uniqueness. Per
   // graph/node/template_validator_graphs.go::flatten, rimsky rejects
   // duplicate types across main + every sub-graph at registration.
   const typeOrigin = new Map(); // type → first-seen graph name
   const flag = (graphName, type) => {
     const prior = typeOrigin.get(type);
     if (prior) {
       errors.push(`duplicate node type "${type}": appears in both ${prior} and ${graphName}`);
     } else {
       typeOrigin.set(type, graphName);
     }
   };
   for (const n of doc.nodes ?? []) flag("main", n.type);
   for (const g of doc.graphs ?? []) {
     for (const n of g.nodes ?? []) flag(g.name, n.type);
   }

   // Operator-metadata invariant: every crimefinder node carries tags.
   // Enforces the "every node tagged" promise in T47's background.
   const checkTags = (graphName, n) => {
     if (!Array.isArray(n.tags) || n.tags.length === 0) {
       errors.push(`${graphName}: node ${n.type} missing tags:`);
     }
   };
   for (const n of doc.nodes ?? []) checkTags("main", n);
   for (const g of doc.graphs ?? []) {
     for (const n of g.nodes ?? []) checkTags(`sub-graph ${g.name}`, n);
   }

   // Sub-graph encapsulation: holds:/subscribes: must reference local nodes.
   for (const g of doc.graphs ?? []) {
     if (!g.entry || !g.exit) errors.push(`sub-graph ${g.name} missing entry/exit`);
     const internalTypes = new Set((g.nodes ?? []).map(n => n.type));
     internalTypes.add(g.entry);
     for (const n of g.nodes ?? []) {
       for (const sub of n.subscribes ?? []) {
         if (sub.node && !internalTypes.has(sub.node)) {
           errors.push(`sub-graph ${g.name}: node ${n.type} subscribes to non-internal node ${sub.node}`);
         }
       }
       for (const [alias, h] of Object.entries(n.holds ?? {})) {
         if (h.from && !internalTypes.has(h.from)) {
           errors.push(`sub-graph ${g.name}: node ${n.type} holds ${alias} from non-internal node ${h.from}`);
         }
       }
     }
   }

   if (errors.length) {
     errors.forEach(e => console.error(e));
     process.exit(1);
   }
   console.log("template OK");
   ```

**Verification:**
```
cd apps/crimefinder && node templates/validate.mjs templates/code-review-pass.yml
```
Expect `template OK` and exit code 0. Any structural drift surfaces here without needing rimsky-control-api up.

---

### T48. Test harness for scenarios

**Files:**
- `apps/crimefinder/test/package.json`
- `apps/crimefinder/test/tsconfig.json`
- `apps/crimefinder/test/scenarios/harness.ts`
- `apps/crimefinder/test/scenarios/fixtures/tiny-repo/` (a test fixture)

**Steps:**

1. Write `test/tsconfig.json` (parallel to other workspaces) and `test/vitest.config.ts` with `include: ["{scenarios,e2e}/**/*.test.ts"]` (this workspace uses `*.test.ts` — vitest's default — for scenario/e2e files, distinct from the unit-test convention used elsewhere; scenarios are slower and benefit from the default-include treatment plus `testTimeout: 300_000`).

   ```ts
   import { defineConfig } from "vitest/config";
   export default defineConfig({
     test: {
       include: ["scenarios/**/*.test.ts", "e2e/**/*.test.ts"],
       testTimeout: 300_000,
       hookTimeout: 60_000,
     },
   });
   ```

2. Write `test/package.json`:

   ```json
   {
     "name": "@crimefinder/test",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "scripts": {
       "test": "vitest run --no-file-parallelism",
       "test:scenarios": "vitest run scenarios",
       "test:e2e": "CRIMEFINDER_E2E=1 vitest run e2e",
       "typecheck": "tsc --noEmit"
     },
     "dependencies": {
       "@crimefinder/shared": "*",
       "testcontainers": "^10.7.0",
       "@grpc/grpc-js": "^1.10.0",
       "@grpc/proto-loader": "^0.7.10",
       "zod": "^3.22.4"
     }
   }
   ```

3. Write `harness.ts`. Spins up:
   - A tmp git repo populated from a fixture directory.
   - A rimsky stack via testcontainers: postgres + rimsky-supervisor + rimsky-control-api + crimefinder-producer (built locally via the Dockerfile from T34).
   - A crimefinder-executor process on a chosen host port, configured with `CRIMEFINDER_EXECUTOR_STUB_MODE=1`.
   - Helpers: `registerTemplate(yamlPath)`, `createInstance(templateHash, params)`, `waitForInstanceTerminal(instanceId, timeoutMs)`, `readJsonl(repoRoot, fileName)`, `teardown()`.

   ```ts
   export interface ScenarioHarness {
     repoRoot: string;          // bind-mounted into producer
     controlApiUrl: string;     // host-mapped
     templateHash: string;
     registerTemplate(yamlPath: string): Promise<string>;
     createInstance(templateHash: string, params: Record<string, unknown>): Promise<string>;
     waitForInstanceTerminal(instanceId: string, timeoutMs?: number): Promise<{ state: string; finished_at?: string }>;
     readFindings(): Promise<FindingsRow[]>;
     readPasses(): Promise<PassesRow[]>;
     readCoverage(): Promise<CoverageRow[]>;
     pumpStubGate(toolName: string, input: unknown, opts: { passId: string; zoneId?: string }): Promise<unknown>;
     teardown(): Promise<void>;
   }
   export async function setupHarness(opts: {
     fixtureDir: string;
   }): Promise<ScenarioHarness>;
   ```

4. The harness's stub-executor receives dispatches with `userdata.stub_outcome` carrying the scripted gate sequence per scenario. Each scenario test composes the stub_outcome to drive the agent through a specific path.

5. Build a fixture: `apps/crimefinder/test/scenarios/fixtures/tiny-repo/` with a few files (e.g., `src/foo.ts`, `src/bar.ts`, `README.md`) and a `.crimefinder/config.yml`. Optionally a tiny `.ok-planner/design/concepts/example.md` for class-5b auto-routing tests.

6. Tests against the harness itself: bring it up, register the template, tear down, assert no leaked containers.

**Verification:**
```
cd apps/crimefinder/test && npx tsc --noEmit
# Full harness start requires Docker; scenario tests exercise it.
```

---

### T49. Scenario test: full pass with stub executor

**Files:**
- `apps/crimefinder/test/scenarios/full-pass-stub.test.ts`

**Steps:**

1. Test:
   - Bring up the harness with `tiny-repo` fixture (3 files, no findings to emit).
   - Stub-outcome: each zone session calls `review_context`, then `review_coverage([files...])`, then `review_complete`.
   - Aggregator: zero findings.
   - Fix iterations: each iter-guard returns `skipped:true` (no work).
   - Report writes summary.
   - Assert: `passes.jsonl` has one `pass_started` + one `pass_finished` row. `findings.jsonl` is empty. `coverage.jsonl` has rows for all 3 files.

**Verification:**
```
cd apps/crimefinder/test && CRIMEFINDER_E2E=0 npx vitest run scenarios/full-pass-stub.test.ts
```

---

### T50. Scenario test: multi-zone concurrency

**Files:**
- `apps/crimefinder/test/scenarios/multi-zone-concurrency.test.ts`
- `apps/crimefinder/test/scenarios/fixtures/multi-zone-repo/` (50+ files spread across 4 directories so partitioning produces ≥3 zones)

**Steps:**

1. Test:
   - Harness with `multi-zone-repo`.
   - Stub-outcome: each zone session emits 5 findings concurrently (parallel `review_finding` calls).
   - Assert: every emitted finding lands in JSONL exactly once. No corrupt lines. Coverage covers all files. Pass summary records expected counts.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/multi-zone-concurrency.test.ts
```

---

### T51. Scenario test: fix-cycle iteration

**Files:**
- `apps/crimefinder/test/scenarios/fix-cycle-iteration.test.ts`

**Steps:**

1. Test:
   - Harness with `tiny-repo`.
   - Stub-outcome (review-zone): emit 1 class-1 finding.
   - Stub-outcome (fix-cycle iter 1): `review_run_tests` → success, `review_commit_fix(finding_id)` → success.
   - Stub-outcome (re-review iter 1): no new findings.
   - iter-aggregate: `more_work_needed:false`.
   - iter-2 and iter-3: `iter-guard.skipped:true` (no work).
   - Assert: `iter_num:1` in iter-aggregate's stored payload; commit landed in git; JSONL has `status:fixed` row.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/fix-cycle-iteration.test.ts
```

---

### T52. Scenario test: crash recovery

**Files:**
- `apps/crimefinder/test/scenarios/crash-recovery.test.ts`

**Steps:**

1. Test:
   - Manually invoke the producer's `commit-fix` flow up to step 11 (git commit succeeds), then kill the producer container before it appends the JSONL row.
   - Restart the producer (testcontainers `restart()`).
   - Assert: the producer's startup recovery appended the missing `status_update` row referencing the commit SHA from `Resolves:` footer.

   To stage this: implementer can simulate via a producer build flag that skips the JSONL append (gated by `env:CRIMEFINDER_PRODUCER_TEST_SKIP_JSONL=1` only readable in tests), OR by direct file manipulation: do a real commit-fix flow, then delete the trailing `status_update` row from findings.jsonl, then restart the producer.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/crash-recovery.test.ts
```

---

### T53. Scenario test: cross-zone finding

**Files:**
- `apps/crimefinder/test/scenarios/cross-zone-finding.test.ts`

**Steps:**

1. Stub a review-zone session that emits a finding citing a file in a different zone.

2. Assert: finding row's `file` matches the actual file path; `zone_id` matches the zone where the file lives (not the dispatching zone); `originating_zone_id` matches the dispatching zone.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/cross-zone-finding.test.ts
```

---

### T54. Scenario test: re-discovery dedup

**Files:**
- `apps/crimefinder/test/scenarios/rediscovery-dedup.test.ts`

**Steps:**

1. Run a full pass that emits 2 findings.
2. Run a second pass against the same repo (no changes between passes). Stub the agent to emit the SAME 2 findings (same fingerprint).
3. Assert: the second pass produces zero new `kind:"finding"` rows in `findings.jsonl` (T28 step 3's fingerprint-dedup logic returns the existing `finding_id` from pass 1 for each re-emit). Specifically: count rows where `kind === "finding"` AND `pass_id === <second pass id>` — assert `=== 0`. The total `kind:"finding"` count across the JSONL remains `2` (the original from pass 1).

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/rediscovery-dedup.test.ts
```

---

### T55. Scenario test: tension confirmation

**Files:**
- `apps/crimefinder/test/scenarios/tension-confirmation.test.ts`
- Fixture extension: `apps/crimefinder/test/scenarios/fixtures/tiny-repo/.ok-planner/design/tensions/example-tension.md`

**Steps:**

1. Stub: agent emits `review_finding` with `tension_slug:"example-tension"`.
2. Assert: JSONL row is `kind:"tension_confirmation"`, not `kind:"finding"`.
3. Assert: gate response carries `tension_already_cataloged` alongside success.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/tension-confirmation.test.ts
```

---

### T56. Scenario test: class-5b auto-routing

**Files:**
- `apps/crimefinder/test/scenarios/class-5b-routing.test.ts`
- Fixture extension: `apps/crimefinder/test/scenarios/fixtures/tiny-repo/.ok-planner/design/concepts/example-concept.md` with explicit `## Boundaries\n...` section containing a recognizable phrase.

**Steps:**

1. Stub agent emits class-1 finding with `concept_slug:"example-concept"` and `description:"the example concept is wrong here"` (no quote from Boundaries).
2. Assert: JSONL row has `effective_class:"5b"`, `auto_rerouted:true`.
3. Run a second variant: description includes 10 contiguous tokens from the Boundaries section.
4. Assert: JSONL row has `effective_class:1`, `auto_rerouted:false`.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/class-5b-routing.test.ts
```

---

### T57. Scenario test: coverage threshold

**Files:**
- `apps/crimefinder/test/scenarios/coverage-threshold.test.ts`

**Steps:**

1. Stub a review-zone session that reports only 1 of N files in coverage, with N large enough to fall below `cfg:coverage.threshold_pct`. Call `review_complete` WITHOUT `review_skip_zone`.
2. Assert: gate returns `coverage_below_threshold` error.
3. Re-run with `review_skip_zone` called before `review_complete`.
4. Assert: gate accepts; pass progresses with the zone marked skipped.

**Verification:**
```
cd apps/crimefinder/test && npx vitest run scenarios/coverage-threshold.test.ts
```

---

### T58. E2E smoke (gated, manual)

**Files:**
- `apps/crimefinder/test/e2e/smoke.test.ts`

**Steps:**

1. Test guarded by `env:CRIMEFINDER_E2E=1`; otherwise it `it.skip`s.

2. When enabled:
   - Brings up the harness with a small fixture repo with one intentional class-1 finding (a function clearly missing error handling).
   - Disables stub mode; uses real Claude CLI (requires `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` in env).
   - Runs a full pass.
   - Asserts: at least one finding emitted; if `require_tests_before_commit:false`, optionally asserts at least one `review_commit_fix` succeeded.

3. Document in the file's top comment that this test costs API credits and is gated.

**Verification (gated; do not run on every CI):**
```
cd apps/crimefinder/test && CRIMEFINDER_E2E=1 npx vitest run e2e/smoke.test.ts
```
This step is **not** part of the automated plan execution; it appears in "Manual checks after completion" below.

---

### T59. Top-level files: CHANGELOG, CLAUDE.md, README, feature-index, cold-read pointer

**Files:**
- `apps/crimefinder/CHANGELOG.md`
- `apps/crimefinder/CLAUDE.md`
- `apps/crimefinder/README.md`
- `apps/crimefinder/feature-index.md`
- `apps/crimefinder/cold-read/README.md`

**Steps:**

1. Write `CHANGELOG.md`:

   ```markdown
   # Crimefinder Changelog

   ## Unreleased

   - Initial implementation per spec `.ok-planner/specs/2026-05-19-crimefinder-design.md`.
     Custom rimsky executor + claim-producer + template YAML + CLI wrapper.
     Read-and-fix review pass with atomic commit-fix gate, class-5b auto-routing,
     and concept-doc-aware classification.
   ```

2. Write `CLAUDE.md`. Crimefinder-specific guidance pointing at the spec, the cold-read conventions, and the prototype lineage. Don't duplicate rimsky's CLAUDE.md.

   ```markdown
   # CLAUDE.md (crimefinder)

   ## What this is

   Crimefinder is a code-review tool that consumes rimsky's orchestration
   primitives. It ships a custom executor, a custom claim-producer, template
   YAML, and a thin CLI wrapper. It does not modify rimsky itself.

   ## Where to look first

   - Architecture and contracts: `.ok-planner/specs/2026-05-19-crimefinder-design.md`.
   - Cold-read style: `../cold-read/cold-read-cheatsheet.md` (inherited from
     rimsky root; crimefinder follows the same conventions).
   - Prototype lineage: `/Users/patrick/Documents/projects/research/crimefinder/`
     is the original sketch; ported code carries `@source:` annotations.

   ## After Code Changes

   1. Run `npm run typecheck && npm test` from `apps/crimefinder/`.
   2. Update `apps/crimefinder/CHANGELOG.md`.
   3. Update `apps/crimefinder/feature-index.md` if a feature was added or removed.
   4. Update `@source:` annotations if you modified prototype-ported code.
   ```

3. Write `README.md`. Public-facing overview: what crimefinder does, how to bring it up, how to run a pass, how to read findings.

4. Write `feature-index.md`. Map of features → owning files:

   ```markdown
   # Crimefinder Feature Index

   | Feature | Files |
   |---|---|
   | Gate vocabulary (executor side) | `executor/src/gates/`, `executor/src/internal-mcp-server.ts`, `executor/src/internal-mcp-tools.ts` |
   | Class-5b auto-routing | `producer/src/state/class-5b-rule.ts`, `producer/src/state/append-finding.ts` |
   | Atomic commit-fix | `producer/src/state/commit-fix.ts`, `producer/src/state/commit-mutex.ts`, `producer/src/git-ops.ts` |
   | Zone partitioning | `producer/src/zones/partition.ts`, `producer/src/claim-producer/split-scope.ts` |
   | JSONL substrate | `producer/src/jsonl-store.ts`, `producer/src/jsonl-mutex.ts`, `shared/src/jsonl-rows.ts` |
   | Fingerprinting / dedup | `shared/src/fingerprint.ts`, `producer/src/dedup/` |
   | Recovery scan | `producer/src/recovery/startup-scan.ts` |
   | Concept-doc parsing | `producer/src/concepts/` |
   | Template + sub-graph | `templates/code-review-pass.yml` |
   | Host executor wiring | `executor/src/server.ts`, `executor/src/agent-run.ts` |
   | CLI wrapper | `cli/src/` |
   ```

5. Write `cold-read/README.md`. Pointer to rimsky root:

   ```markdown
   # cold-read (pointer)

   Crimefinder follows the rimsky root cold-read conventions at
   `../../../.claude/rules/cold-read-cheatsheet.md` (or the longer-form
   docs under `cold-read/` at the rimsky root). This directory exists
   to host crimefinder-specific divergences if any emerge; for now it
   only contains this pointer.
   ```

**Verification:**
```
ls apps/crimefinder/{CHANGELOG.md,CLAUDE.md,README.md,feature-index.md,cold-read/README.md}
```

---

### T60. `@source:` annotation audit

**Files:** All files lifted from the prototype.

**Steps:**

1. Run `rg "@source:" apps/crimefinder/` and assert every prototype-lifted file has an annotation citing its origin path.

2. The files expected to have `@source:`:
   - `apps/crimefinder/shared/src/ids.ts`
   - `apps/crimefinder/producer/src/zones/partition.ts`
   - `apps/crimefinder/producer/src/zones/coverage.ts`
   - `apps/crimefinder/producer/src/dedup/group.ts`
   - `apps/crimefinder/producer/src/dedup/resolve.ts`
   - `apps/crimefinder/executor/src/cli-env.ts`
   - `apps/crimefinder/executor/src/cli-runner.ts`
   - Their co-located `*_test.ts` files where tests are direct ports.

3. Diverged files additionally carry `@diverged: true` + `@reason: ...`. Confirm each cites a reason that matches the actual divergence (the build/recipe at the start of each lift task includes the expected reason).

4. Verify no NON-prototype-lifted files carry `@source:` (would be spurious).

**Verification:**
```
rg "@source:" apps/crimefinder/ | sort && \
  rg -L "@source:" apps/crimefinder/shared/src/ids.ts apps/crimefinder/producer/src/zones/partition.ts apps/crimefinder/producer/src/zones/coverage.ts apps/crimefinder/producer/src/dedup/group.ts apps/crimefinder/producer/src/dedup/resolve.ts apps/crimefinder/executor/src/cli-env.ts apps/crimefinder/executor/src/cli-runner.ts
```
First command lists annotations; second command's `-L` (files-without-match) should produce zero output.

---

### T61. Full build + type-check + test sweep

**Steps:**

1. From the workspace root: `cd apps/crimefinder && npm install && npm run typecheck`.
2. Run all unit tests: `npm run test`.
3. Run scenario tests (requires Docker): `cd test && npx vitest run scenarios/`.

**Verification:**
```
cd apps/crimefinder && npm run typecheck && npm run test
cd apps/crimefinder/test && npx vitest run scenarios/
```
All must pass.

---

## Manual checks after completion

These are not part of the automated execution. The user runs them after the plan finishes and code review is clean.

1. **End-to-end smoke with real Claude CLI.**
   - Ensure `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` is set in the environment.
   - Bring up the consumer rimsky stack with `apps/crimefinder/deploy/docker-compose.fragment.yml` merged into the consumer compose file.
   - Start the host executor: `cd apps/crimefinder/executor && node dist/main.js &`.
   - Run a review pass against a small test repo: `crimefinder pass --repo /path/to/test-repo --mission "convergence pass"`.
   - Inspect `.crimefinder/findings.jsonl` and `.crimefinder/passes.jsonl` in the test repo. Confirm at least one finding emitted, and that the pass row is `pass_finished`.
   - Run `crimefinder status --repo /path/to/test-repo` and confirm the table renders.

2. **Operator dashboard view of named-events.** With rimsky-dashboard running (`http://localhost:8090`), navigate to the events view and confirm crimefinder's named events appear (`pass_opened`, `finding_emitted`, etc.).

3. **Verify host-to-container connectivity.** With the host executor running, the supervisor should successfully dispatch a review pass to it. Visible failure modes: supervisor logs show `host.docker.internal:7071` unreachable (Linux without `--add-host`), or supervisor's `callback.advertise_host` is unset and executor's POST-back fails.

4. **Verify atomic commit semantics.** Open `.crimefinder/findings.jsonl` and `git log -p .crimefinder/findings.jsonl` for the test repo. Each `status:"fixed"` row should appear in the same commit as the code change it resolves.

