# Rimsky Go Port — Plan D — Documentation

**Goal:** Author the full rimsky v1 documentation set: conceptual design, implementation-shape architecture, node-executor protocol reference, operator guide, executor-author guide, and resource-author guide. At the end of Plan D, rimsky is OSS-ready — every doc listed in spec §15 exists at its committed path, reads as a standalone reference, and collectively enables a first-time reader to deploy, operate, and extend rimsky without reading the code.

**End state after this plan:** six Markdown files under `rimsky-go/docs/` cover the full documentation surface. `node-graph-design.md` stands alone as a conceptual reference (not a diff against the TS predecessor's doc). `architecture.md` explains the package layout and the blessed invariants with pointers into the Go code. `protocol.md` is sufficient for a developer to implement their own executor in any language. Author guides each include a complete copy-pasteable reference implementation (Python HTTP-bridge executor; Go memory-only resource). Build + lint + tests remain green.

**Architecture:** Markdown only. No new Go code. No template authoring. No consumer-specific cutover work — consumer migration planning lives in the consumer's own documentation, per spec §1.4. Plan D is strictly about rimsky-project documentation.

**Reference documents:**
- Spec: `docs/specs/2026-04-23-rimsky-go-port-design.md` (especially §15 docs scope, §20 success criteria)
- Plans A, B, C (complete): `docs/plans/2026-04-23-rimsky-go-port-plan-{a,b,c}-*.md`
- Existing Go code + generated proto files (for cross-referencing in architecture + protocol docs)

---

## Phase 0 — Amendments (none)

No amendments to Plan A/B/C. Plan D is strictly documentation.

---

## Phase 1 — Conceptual design doc

### Task 1.1 — Author `rimsky-go/docs/node-graph-design.md`

Standalone conceptual reference for rimsky v1 Go. NOT a diff against the TS doc — reads alone.

Structure (derived from `docs/cell-graph-design.md` but rewritten with node terminology and the three-collections architecture):
- §1 Motivation
- §2 Core model — nodes, messages, resources
- §3 Nodes — "properties, not classes" framing; pure-cascade nodes; schedule property; executor property; userdata block
- §4 Resources — versioning, double-buffering, change verdict, access methods, pluggable implementations, rollback-as-resource-concern
- §5 Messages — `invalidate`, `recalculate`
- §6 Error model — per-node taxonomy, policy chains, retry/invalidate/give_up
- §7 Parameterization — userdata, instance params, placeholders
- §8 Node contract — full template shape
- §9 Lifecycle — registration, execution, pure-cascade execution
- §10 Design principles (carry forward the TS doc's 10 principles where they still apply, add "Executors are peers, not subsystems" and "Resources own rollback semantics")
- §11 Deferred items (per spec §2.2)
- §12 Glossary (with the cell→node rename applied per spec §21)

Write node-focused. "Cell" should appear only in a brief appendix acknowledging the TS predecessor — nowhere else.

Target length: 600–900 lines.

---

## Phase 2 — Implementation-shape docs

### Task 2.1 — `rimsky-go/docs/architecture.md`

Cover:
- Three-collection architecture (orchestrator, resources, executors)
- Package layout + import-graph rules (spec §4.1)
- Three long-running processes (scheduler, supervisor, control-api)
- Distribution model (Go module + reference binaries + Docker images)
- Blessed invariants (spec §17), with file + line pointers into the Go code
- Library entry points (`config.StartScheduler`, `config.StartSupervisor`, `config.StartControlAPI`)
- Where executors run (peer services), how they're referenced (supervisor-config name → endpoint)

Target length: 300–500 lines.

### Task 2.2 — `rimsky-go/docs/protocol.md`

Cover:
- Transports: gRPC canonical + HTTP+JSON bridge semantics
- `.proto` message reference rendered from `node_executor.proto` as readable Markdown
- The 5 event kinds: Heartbeat, Complete, Blocked, Errored, AsyncAccepted (terminal semantics — exactly one terminal per stream; stream close after terminal)
- Async-handoff pattern: why, when, the callback HTTP contract
- Userdata conventions: opaque to rimsky, executor-defined schema
- Conformance: how to run `rimsky-conformance` against your executor
- Versioning: `v1` compatibility commitments
- Auth: mTLS recommended; plain for dev

Include `curl` examples for the HTTP bridge and `grpcurl` pointers for gRPC.

Target length: 400–600 lines.

---

## Phase 3 — Author and operator guides

### Task 3.1 — `rimsky-go/docs/operator-guide.md`

Cover:
- 15-minute quickstart (docker compose up; curl health; deploy a first template)
- Docker Compose deployment (from `deploy/docker-compose.yml`)
- Helm/Kubernetes deployment (from `deploy/kubernetes/rimsky-chart`)
- Supervisor config reference (executors map, concurrency_limits, sql_connections, callback)
- Template authoring:
  - Schema walkthrough
  - Common patterns: pure-cascade fan-out, schedule-driven nodes, rollback policies
- Control API operations (templates, instances, operator overrides, event-log queries)
- Monitoring: `/health`, `/metrics`, key Postgres queries (running nodes, stuck dispatch rows, recent errors)
- Common failure modes and how to diagnose them

Include copy-pasteable curl snippets for every control API operation.

Target length: 400–700 lines.

### Task 3.2 — `rimsky-go/docs/executor-author-guide.md`

Written for someone authoring a new executor in any language.

Cover:
- The contract: implement `Execute(ExecuteRequest) returns (stream ExecuteEvent)`; emit ≥0 heartbeats + exactly 1 terminal; close stream
- Minimal Go example (point at `executors/http-node` as reference)
- Complete Python example (~80 lines, FastAPI HTTP+JSON bridge, stub-mode-compliant, copy-pasteable)
- Minimal TypeScript example (point at `executors/claude-agent` as reference)
- Async-handoff pattern (when to use, how to POST back to `callback_url`)
- Error classes (application-level Errored vs Blocked, infra-error mapping)
- Stub mode: why; env var `RIMSKY_EXECUTOR_STUB_MODE=1`; required for conformance CI
- Running the conformance suite
- Docker image conventions; supervisor config integration

Target length: 300–500 lines.

### Task 3.3 — `rimsky-go/docs/resource-author-guide.md`

Written for Go developers writing a new resource implementation (v1: Go only).

Cover:
- The `resource.Resource` interface (method-by-method walkthrough)
- The `resource.Factory` interface + JSON-schema config validation
- Quality-rule binding at `Factory.Create` time
- Commit flow (evaluate rules, `CommitVersion` or reject)
- Rollback semantics (`RestoreVersion("previous")` and `("id")`; `ErrRollbackUnsupported`)
- Version GC (`keep_versions`)
- Testing: use `core/scenario` harness + `core/internal/pgtest`; exercise commit + rollback + no-op
- Registration
- Reference: walk through `inline-jsonb` and `external-sql` as two contrasting examples

Include a complete "memory-only" reference implementation (~100 lines) as a pedagogical example.

Target length: 250–400 lines.

---

## Phase 4 — Definition of Done

### Task 4.1 — Gate verification

**Steps:**

1. `cd rimsky-go && go build ./... && go test ./... -count=1 && go vet ./... && golangci-lint run` — all exit 0 (no code changes expected in Plan D; just sanity-check the build/test state hasn't regressed).
2. Every doc from spec §15 exists at the correct path and is non-placeholder:
   - `rimsky-go/docs/node-graph-design.md`
   - `rimsky-go/docs/architecture.md`
   - `rimsky-go/docs/protocol.md`
   - `rimsky-go/docs/operator-guide.md`
   - `rimsky-go/docs/executor-author-guide.md`
   - `rimsky-go/docs/resource-author-guide.md`
3. Each doc reads end-to-end without obvious gaps or self-contradiction. Cross-references resolve.
4. The minimum-viable README at `rimsky-go/README.md` is refreshed from Plan A's stub to a real README (overview, quick start, docs links, contributing placeholder, license placeholder).
5. Append a Plan D entry to `rimsky-go/CHANGELOG.md`.
6. Append a final `plan_completed` entry for Plan D and an `all_plans_completed` entry to the execution log.

**Verification:** all gates green.

---

## Appendix — Subagent dispatch notes

**Parallelizable groups:**
- Phase 1 (conceptual design doc), Phase 2 (architecture + protocol), and Phase 3 (three author guides) are mostly independent and can be parallelized. Each doc is one subagent task.

**Critical-path tasks:**
- Task 1.1 (node-graph-design.md) — longest doc, highest care. Reviewer should verify the "no 'cell' outside the change-summary appendix" rule, the §3 "properties not classes" framing is clear, and the document stands alone for a reader with no TS-project context.
- Task 3.2's Python example — the copy-pasteable reference must actually pass `rimsky-conformance --require-stub-mode`. If it doesn't, external executor authors will blame rimsky.
