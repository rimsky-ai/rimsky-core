# Layer Crystallization Implementation Plan

**Goal:** Reshape Rimsky into four crisply-layered concerns (foundation, modeling, service protocols, bundled services + examples) by producing three durable contract documents, splitting the Go module structure (foundation, protocols, root), settling vocabulary (region→scope; protocol-level "store"→"ClaimProducer"; foundation subsystem names), consolidating worker-request bookkeeping, unifying parallel-but-distinct internal mechanisms, and rewriting the user-facing documentation against the new structure.

**Architecture:** Three Go modules — `github.com/fallguy/rimsky/foundation` (cascade engine + lock manager + integration + persistence contract), `github.com/fallguy/rimsky/protocols` (ClaimProducer/Executor/LifecycleSubscriber Go interfaces and protobuf bindings, stdlib-only deps), and the root module `github.com/fallguy/rimsky` (modeling layer, cmd binaries, bundled service reference impls). Coordinated by `go.work`. `rimsky_dispatch` and `rimsky_lock_holders` collapse to `rimsky_worker_request` + `rimsky_claim_handle` with an `is_held` flag and a `phase` column expressing the active+held lifecycle.

**Tech Stack:** Go 1.25 (multi-module via go.work), Postgres (via pgx/v5; production driver) and SQLite (via modernc.org/sqlite; dev-only driver), gRPC + protobuf for service protocols, chi for HTTP, robfig/cron/v3 for cron parsing, golangci-lint with depguard. TypeScript / npm for `executors/claude-agent`. TypeScript / Vite / React for `dashboards/rimsky-dashboard` (untouched by this plan except for path-related references).

---

## Reference materials

The implementer must read these before starting:

- **Spec** — `docs/specs/2026-05-04-layer-crystallization-design.md`. Authoritative requirements for everything below. Sections referenced as `spec §N`.
- **Foundation contract draft** — `docs/specs/2026-05-03-foundation-contract-design.md`. Starting point for Task 1.
- **CLAUDE.md** at repo root — codebase orientation, package import rules, blessed invariants, build commands, gotchas. Many invariants are annotated `@blessed-invariant N` in code; spec §11 Phase 5 verification gates depend on them.
- **Cold-read style** — `.claude/rules/cold-read-cheatsheet.md`. One feature per file; max 2 levels of dir nesting per feature; ~500-line file / ~100-line function guidelines; max 3 nesting depth via early returns; tracked duplication via `@source` / `@diverged` annotations; `@agent-contract` / `@blessed-invariant` blocks for cross-cutting concerns.
- **Project rules** — `.claude/rules/rules.md`. Pre-v1 break-freely (drop+recreate migrations OK; no compat shims). Fix every bug found while working. Verify the build after every code change.

Key blessed invariants to preserve through the refactor (currently in CLAUDE.md; will be re-numbered in the post-Phase-1 foundation contract but the historical numbers carry through):

- **1.** State machine rejects illegal transitions (running→running under reason `dispatch_claimed` errors).
- **2.** Dispatch claim brackets the running window. (Post-Phase-5: the `phase` column on `rimsky_worker_request` does this.)
- **3.** Multi-handle acquisition uses deterministic sorted order.
- **4.** Claimant-guarded release on every claim-handle deletion and worker-request `claimed_by` nullification.
- **5.** Verify-before-run.
- **6.** Orphan cutoff is `5 × heartbeat_interval`.
- **7.** Advisory lock on the dispatch tick.
- **8.** Session advisory lock on migrations.
- **9a.** Lock state lives only in foundation persistence; service implementations do not persist lock state.
- **10.** Acquisition is atomic (worker-request claim + claim-handle inserts + address record-back commit together or not at all).
- **11.** *(modeling-layer)* Userdata is opaque to rimsky.
- **12.** *(modeling-layer)* Attributes validate twice (at dispatch post-substitution; at commit on executor writeback).
- **13.** Auto-terminal aggregate-outcome resolution is single, claim-handle-row-locked, aggregate-outcome-driven.
- **15.** Producer `Open` fires inside the foundation-side acquisition transaction.
- **20.** Claim content (address, payload, scope) is inert in foundation.

Current top-level repo layout (will change in Task 14+):

```
core/                # orchestrator (will dissolve into foundation/ + modeling/)
  attributes/        canonical/   cli/         cmd/
  config/            controlapi/  doc.go       executor/
  frame/             internal/    node/        observability/
  persistence/       qualityrule/ scenario/    scheduler/
  shared/            store/       supervisor/
proto/v1/            # protobuf source + generated bindings (moves to protocols/)
stores/              # bundled claim producers (filesystem, postgres, stub)
executors/           # bundled executors (claude-agent (TS), http-node (Go), stub)
test/                # cross-layer tests
deploy/              # docker-compose, helm chart, dockerfiles
docs/                # specs, plans, architecture, history
dashboards/          # rimsky-dashboard (TS/React)
go.mod               # single root module currently
Makefile
.golangci.yml
```

Settled sub-decisions (from spec §14, locked in by this plan):

| Sub-decision | Choice |
|---|---|
| Worker-request schema (spec §8.2) | Option I: single `rimsky_worker_request` table with `phase` column; `rimsky_claim_handle` is FK-cascade child. |
| Foundation `integration/` primary type | `Conductor`. |
| `persistence.Coordinator` rename | → `persistence.AdvisoryLocker`. |
| `rimsky_store_lifecycle` table rename | → `rimsky_lifecycle_idempotency` (Task 31). |
| Conformance binary names | Three binaries: `rimsky-conformance` (executor + lifecycle); `rimsky-claim-producer-conformance`; existing `rimsky-conformance-probe` retained. |
| Backwards-compat shim for protocols module | None. |
| `core/cmd/` → root `cmd/` | Yes (Task 14). |

---

## Tasks

Each task lists the files it touches, numbered steps with embedded code, and a final verification command. Run the verification at the end of each task before moving on. If a verification fails, fix and re-run before continuing. Do not commit; the user owns git.

### Task 1 — Write the finalized foundation contract

**Files:**
- New: `docs/specs/2026-05-04-foundation-contract.md`

**Steps:**

1. Read `docs/specs/2026-05-03-foundation-contract-design.md` end to end. This is the starting point.

2. Create `docs/specs/2026-05-04-foundation-contract.md` by copying the 2026-05-03 draft.

3. Update the metadata block at the top: change date to 2026-05-04; change Status to `Authoritative until v1`; remove the "Design draft" framing.

4. Apply the 8 deltas listed in spec §5:

   a. **Vocabulary update** — replace every occurrence of "region" / "Region" / "regions" with "scope" / "Scope" / "scopes" in the conflict-predicate sense. Specifically:
      - §4.1 "claim handle" field `region` → `scope`. The bullet listing the field set: `id, holder, region, address, payload, purpose` becomes `id, holder, scope, address, payload, purpose`.
      - §4.2 title "Region conflict" → "Scope conflict". Body text "two claim handles **conflict** iff their region bytes are byte-equal" → "iff their scope bytes are byte-equal".
      - §4.3 "byte-equal-region" → "byte-equal-scope" everywhere.
      - §6.1 SQL column `region_data BYTEA` → `scope_data BYTEA`.
      - All invariant references in §7 that mention region.
      - §8 "What is explicitly NOT in the foundation" — no change needed (no region references there).

   b. **Subsystem package names settled** — add a new sentence to §3 (or a new sub-section §3.0) stating: "The cascade engine, lock manager, and integration layer are realized as Go packages `foundation/cascade/`, `foundation/locks/`, and `foundation/integration/` respectively. Foundation persistence lives in `foundation/persistence/`." Update §11.4 to mark the open question as resolved.

   c. **Worker-request consolidation direction settled** — update §11.2 to mark the open question as resolved with: "Worker-request consolidation lands in spec `2026-05-04-layer-crystallization-design.md` §8 with Option I (single `rimsky_worker_request` table with `phase` column; `rimsky_claim_handle` as FK-cascade child carrying `is_held BOOLEAN`)."

   d. **Write-semantics location settled** — update §4.3 with: "The conflict predicate is evaluated against the (producer, scope-bytes) pair using the `realized_write_semantics` value carried on each claim handle. Per the byte-equal-scope uniformity invariant, all claim handles with the same (producer, scope-bytes) MUST have identical `realized_write_semantics`. The producer-declared envelope from `Capabilities()` constrains which `realized_write_semantics` values may be returned." Update §11.1 to mark resolved.

   e. **Module split settled** — update §11.5 to: "Foundation is its own Go module at `github.com/fallguy/rimsky/foundation`. Rationale and implementation details in spec `2026-05-04-layer-crystallization-design.md` §4.2."

   f. **Implementation-status section retired** — replace §10 (the current-code-locations dump) with a single short paragraph: "The code in `github.com/fallguy/rimsky/foundation` matches this contract. Cross-references from foundation packages to specific files are not maintained in this contract; see `@blessed-invariant N` annotations in source for current code locations."

   g. **Cross-references updated** — search the document for any `docs/specs/2026-04-2*.md` or `docs/specs/2026-05-01*.md` or `docs/specs/2026-05-02-persistence*.md` or `docs/specs/2026-05-02-rimsky-cli*.md` or `docs/specs/2026-05-03-fs-store*.md` — these have all moved to `docs/history/`. Update paths accordingly.

   h. **Driver interface set collapsed** — update §6.2 to list the three driver interfaces: `Cascade`, `WorkerRequests`, `AdvisoryLocker`. Remove `Locks` (collapsed into `WorkerRequests`). Replace `Coordinator` with `AdvisoryLocker` in this section. Add a short note: "The `WorkerRequests` interface owns both the worker-request rows and their child claim-handle rows; see §8.4 of `2026-05-04-layer-crystallization-design.md` for the methods."

5. Update §7 (foundation invariants catalog) to use the new vocabulary (scope) and to reflect the §6.2 driver interface change. The numbering preserves: 1, 2, 3, 4, 5, 6, 7, 8, 9a, 10, 13, 15, 20. (Modeling-layer invariants 9b, 11, 12 are noted as out-of-scope-here.)

6. Update §11 "Open questions": items 11.1, 11.2, 11.4, 11.5 are now closed (delete those sub-sections or replace with "Resolved — see §N"). 11.3 (lifecycle protocol split) is closed by the service-protocol contract being a separate document. Result: §11 should be empty or just say "All open questions from the 2026-05-03 draft are closed."

**Verification:**
```sh
test -f docs/specs/2026-05-04-foundation-contract.md && echo "exists"
grep -c '\bregion' docs/specs/2026-05-04-foundation-contract.md  # should be 0 (or only in literal "region" used in non-conflict-predicate sense, which there shouldn't be)
grep -c '\bscope' docs/specs/2026-05-04-foundation-contract.md  # should be > 10
grep -c 'AdvisoryLocker' docs/specs/2026-05-04-foundation-contract.md  # should be > 0
grep -c 'docs/specs/2026-04' docs/specs/2026-05-04-foundation-contract.md  # should be 0 (all migrated to docs/history/)
```

### Task 2 — Move the 2026-05-03 foundation contract draft to history

**Files:**
- Move: `docs/specs/2026-05-03-foundation-contract-design.md` → `docs/history/2026-05-03-foundation-contract-design.md`

**Steps:**

1. Run `git mv docs/specs/2026-05-03-foundation-contract-design.md docs/history/2026-05-03-foundation-contract-design.md`.

**Verification:**
```sh
test ! -f docs/specs/2026-05-03-foundation-contract-design.md && echo "moved"
test -f docs/history/2026-05-03-foundation-contract-design.md && echo "in history"
```

### Task 3 — Write the modeling-layer comprehensive contract

**Files:**
- New: `docs/specs/2026-05-04-modeling-layer-contract.md`

**Background:** This is the most substantial deliverable in Phase 1. It supersedes the archived per-subsystem design docs in `docs/history/` for content-authority purposes (those docs remain as design records). The contract covers 10 modeling subsystems per spec §6.1 with the structure specified in spec §6.2. The foundation/modeling boundary section (spec §6.3) defines the four predicates the modeling layer supplies to the foundation.

**Source material to consult while writing** (these are now in `docs/history/`):
- `docs/history/2026-04-26-frame-resolution-design.md` — Frames subsystem.
- `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md` — Control-plane API + lifecycle event firing model.
- `docs/history/2026-04-27-stores-redesign-v3-design.md` (§13.3 in particular) — claim-content inertness; substitution-leaf-extraction.
- `docs/history/2026-05-02-rimsky-cli-and-compose-design.md` — CLI + compose.
- `docs/history/2026-05-02-persistence-pluggable-and-unified-image-design.md` — pluggable persistence patterns (foundation-relevant; what's modeling-relevant is the modeling-layer driver protocol).

**Steps:**

1. Create `docs/specs/2026-05-04-modeling-layer-contract.md` with this header:

   ```markdown
   # Modeling-Layer Contract

   **Status:** Authoritative until v1, 2026-05-04.
   **Scope:** Comprehensive contract for Rimsky's modeling layer. Defines templates, instances, frames, schedules, attributes, control-plane API, public vocabularies, YAML config shape, modeling persistence contract, and CLI shape.
   **Authority:** Single source of truth for the modeling layer. Supersedes the archived per-subsystem design docs in `docs/history/` (preserved as design records).
   **Layer position:** Modeling sits above the foundation (`foundation/` module per `2026-05-04-foundation-contract.md`) and consumes the service protocols (`protocols/` module per `2026-05-04-service-protocol-contract.md`) for control-plane lifecycle events.
   ```

2. Add `## 1. Purpose` section: 2–3 paragraphs framing what the modeling layer is, what problem it solves (giving operators a coherent abstraction over the foundation primitives), and the four-layer model context. Reference `2026-05-04-foundation-contract.md` for the foundation; reference the service-protocol contract for service interactions.

3. Add `## 2. Foundation/modeling boundary` section (spec §6.3). Document the four predicates the modeling layer supplies to the foundation:

   ```markdown
   The modeling layer programs the foundation through four predicates:

   1. **Cascade target predicate.** Given a node and the executor-supplied `changed: bool`, computes the set of dependent nodes to receive an invalidate signal. Default policy: `changed=true` → all direct dependents marked `has_value=false`; `changed=false` → propagation halts at this node.
   2. **Holding-subgraph completion predicate.** Given a claim handle, computes whether the subgraph holding it has reached terminal across all members. Foundation invariant 13 depends on this returning true at exactly one moment per held subgraph.
   3. **Aggregate-outcome predicate.** Given a holding subgraph at completion, computes commit-vs-abandon. Default: any-failed → abandon; all-completed → commit.
   4. **Coexistence predicate.** Given a pair of byte-equal-scope claim handles with announced `WriteSemantics`, computes whether they may coexist. Default: identical semantics on both sides → coexist iff the semantics permits (e.g., `staged_async` is read-shared); differing semantics → fail at acquisition (the byte-equal-scope uniformity invariant on `realized_write_semantics` makes this impossible in practice — the predicate exists for defense in depth).

   These four predicates are the totality of the foundation's "read me at decision points" surface. The foundation has no other knowledge of modeling semantics.
   ```

4. Add `## 3. Templates` section. Cover (each as a sub-section):
   - 3.1 Purpose & scope — content-addressed reusable graph shapes.
   - 3.2 Content-addressing — `sha256-<64-hex>` over RFC 8785 JCS-canonicalized spec bytes (the `core/canonical` package; will move to `modeling/template/canonical/` in Task 14). Hash bytes are not pinned across pre-v1 changes.
   - 3.3 Registration — `POST /templates` accepts a spec; computes hash; idempotent (re-registering same spec returns same hash). Tags table separately. Tag movement does not migrate live instances.
   - 3.4 Tags — `compose:<project>:<...>` is reserved for `rimsky-cli compose`. Manual `rimsky-cli template register --tag compose:foo:bar` is rejected client-side. Manual `curl POST /tags` against the same prefix is NOT rejected by control-api (server-side enforcement is a v1 open question; record this in §3.4 along with the operator-guidance to pick distinct compose project names).
   - 3.5 Persistence schema — `rimsky_templates` table (columns: `id TEXT PRIMARY KEY`, `spec JSONB`, `created_at TIMESTAMPTZ`); `rimsky_template_tags` table (columns: `tag TEXT`, `template_hash TEXT REFERENCES rimsky_templates(id)`, `created_at TIMESTAMPTZ`, primary key on `tag`). Indexes on `template_hash`.
   - 3.6 Invariants — list any. Numbered.
   - 3.7 Out of scope — template hash bytes are not pinned (pre-v1); no template versioning beyond movable tags; no template parameterization beyond the attributes mechanism.

5. Add `## 4. Instances` section:
   - 4.1 Purpose & scope.
   - 4.2 Instance lifecycle — `POST /instances` (body `{template, instance_key?, params}`) creates; instance is bound to `template_hash` at creation (FK to `rimsky_templates.id`); instance terminator goroutine in control-api polls `rimsky_instances.terminated_at` and fires `OnInstanceTerminated`.
   - 4.3 Instance-key namespace — nullable; `compose:<project>:<...>` reserved for compose CLI; uniqueness scope per-template.
   - 4.4 Persistence schema — `rimsky_instances` (columns: `id UUID PRIMARY KEY`, `template_hash TEXT NOT NULL REFERENCES rimsky_templates(id)`, `instance_key TEXT`, `params JSONB`, `created_at TIMESTAMPTZ`, `terminated_at TIMESTAMPTZ`).
   - 4.5 Invariants — bind-at-creation; `instance_key` uniqueness within a template scope; terminator fires exactly once.
   - 4.6 Out of scope — no live re-bind to a different `template_hash`; no instance migration.

6. Add `## 5. Frames` section:
   - 5.1 Purpose & scope — frame-resolution model; frames are the unit of cascade resolution.
   - 5.2 Resolution modes — `coalesce` (operator-originated invalidates fold into a pending coalesce row) and `serial_queue` (operator-originated invalidates enqueue a new frame). Templates declare `frame_resolution: coalesce | serial_queue` (required field; control-api rejects without it).
   - 5.3 At-most-one-running enforcement — unique constraint `uq_rimsky_frames_running` on `(instance_id) WHERE state = 'running'`.
   - 5.4 Frame-end SQL predicate — "no `rimsky_nodes` rows in state `stale` or `running` for this instance" — evaluated on every scheduler tick by `frame.RunTick`.
   - 5.5 Cascade-tick relationship — each running frame iterates the cascade engine until frame-end; frame transitions then advance.
   - 5.6 Foundation worker-request lifecycle relationship — every `rimsky_worker_request` row carries `frame_id NOT NULL`; every non-fresh `rimsky_nodes` row carries `frame_id`.
   - 5.7 Persistence schema — `rimsky_frames` (columns: `id UUID PRIMARY KEY`, `instance_id UUID NOT NULL`, `state TEXT NOT NULL`, `resolution TEXT NOT NULL`, `created_at TIMESTAMPTZ`, ended_at TIMESTAMPTZ; unique `uq_rimsky_frames_running`).
   - 5.8 Invariants — at-most-one-running; frame-end is SQL-predicate-driven; in-flight work always runs to terminal (operator-originated invalidates do NOT preempt; the `kill_requested` column was retired).
   - 5.9 Out of scope — no kill-poll path; no in-flight cancellation.

7. Add `## 6. Schedules` section:
   - 6.1 Purpose & scope — cron-driven invalidation.
   - 6.2 Cron parsing — `robfig/cron/v3` semantics (5-field standard cron + optional descriptor).
   - 6.3 Advancement — schedules advance from `row.NextFireAt`, not `clock.Now()`. Missed fires are NOT backfilled.
   - 6.4 Admin force-fire — `POST /admin/scheduled-nodes/{node_id}/force-fire` bypasses cron; updates `rimsky_schedules.next_fire_at = now()`; returns 204 without waiting for cascade. Admin-only route.
   - 6.5 Persistence schema — `rimsky_schedules` (columns: `node_id UUID PRIMARY KEY`, `cron_expr TEXT`, `next_fire_at TIMESTAMPTZ`, `last_fire_at TIMESTAMPTZ`).
   - 6.6 Invariants — advancement-from-row-not-now; no backfill.
   - 6.7 Out of scope — no replay; no backfill.

8. Add `## 7. Attributes` section:
   - 7.1 Purpose & scope — typed inputs to executors with substitution from claim content.
   - 7.2 Schema language — JSON Schema with the `properties[*].source` extension.
   - 7.3 Substitution engine — `{{...}}` directive grammar (syntax: `{{<source-name>.<json-path>}}` typically); the substitution happens at dispatch time (post-claim-acquisition), expanding leaf paths from acquired claim payload/address.
   - 7.4 Validation — twice (modeling-layer invariant 12): at dispatch (post-substitution; before executor call); at commit (executor writeback; against the post-substitution schema).
   - 7.5 Userdata-is-opaque — modeling-layer invariant 11. `userdata` (a sibling field at the dispatch input level) is NEVER inspected, parsed, substituted, or validated by Rimsky. Identical-looking text in `userdata` reaches the executor verbatim.
   - 7.6 Substitution-leaf-extraction — the only sanctioned introspection site for claim content (foundation invariant 20) is `attribute/substitution.go::walkPath` (will be at this path after Task 14). Lazy-unmarshals into a transient `map[string]any` only at leaf-extraction call time. No other code path reads claim payload/address/scope.
   - 7.7 Persistence — attributes schemas are part of template specs (no separate table).
   - 7.8 Invariants — modeling-layer 11 and 12.
   - 7.9 Out of scope — no template-level substitution beyond `properties[*].source`; no userdata substitution.

9. Add `## 8. Control-plane API` section:
   - 8.1 Purpose & scope — operator and CLI HTTP surface.
   - 8.2 Routes (full list with method + path + brief description). Cover at minimum: `/health`, `/templates` (GET, POST), `/templates/{hash}`, `/tags`, `/tags/{tag}`, `/instances` (GET, POST), `/instances/{idOrKey}`, `/instances/{id}/terminate`, `/admin/scheduled-nodes/{node_id}/force-fire`, `/v1/observability/*` (existing observability surface — reference the dashboard/observability spec for details). Consult `docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md` for canonical route list.
   - 8.3 Admin route distinction — `/admin/*` paths require admin auth (currently implicit; v1 will add explicit auth).
   - 8.4 Lifecycle event firing model — control-api fires lifecycle events synchronously at template/instance state transitions; idempotency tracked in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle` per Task 31). Every subscribed peer is RPC'd; failures are retried per the existing retry policy. Reference the service-protocol contract (`2026-05-04-service-protocol-contract.md` §3) for the LifecycleSubscriber surface.
   - 8.5 Versioning — bare paths (no `/v1/` prefix on most routes; `/v1/observability/*` is the exception). Rolling upgrades are operator-managed. Endpoints used by both versions work; endpoints only on one return 404 / 405.
   - 8.6 Invariants — admin route boundary; lifecycle idempotency.
   - 8.7 Out of scope — no auth (v1 concern); no rate limiting (v1 concern).

10. Add `## 9. Public vocabularies` section:
    - 9.1 State vocabulary — the four user-facing names `fresh`/`stale`/`running`/`failed` are the modeling-layer presentation of the foundation's two-bit-plus-flag state space (see foundation contract §3.1). Document the mapping table.
    - 9.2 Message vocabulary — `invalidate` (cascade signal) and `recalculate` (per-node action issued by the dispatch loop). `invalidate` is the only graph-level message; `recalculate` is internal to the modeling layer's interaction with foundation dispatch.
    - 9.3 Error-action vocabulary — `retry` / `invalidate(targets)` / `give_up` are the chosen surface over the foundation's parameterized failure-terminal primitive (auto_recovers + cascade_targets; see foundation contract §3.3). Document the mapping:
       - `retry` → `auto_recovers=true, cascade_targets={}`.
       - `invalidate(targets)` → `auto_recovers=true, cascade_targets=targets`.
       - `give_up` → `auto_recovers=false, cascade_targets={}`.
    - 9.4 Vocabulary stability — these names are stable until v1; renames are deliberate (require successor design doc).

11. Add `## 10. YAML config shape` section per spec §6.1.8 / §10. Document the `rimsky.yml` shape with Option II:

    ```yaml
    persistence:
      driver: postgres                    # or 'sqlite' (dev-only)
      postgres:
        url: postgresql://...
      sqlite:
        path: /var/lib/rimsky/state.db

    claim_producers:
      - name: items-pg
        endpoint: localhost:7001
        protocols: [claim_producer, lifecycle_subscriber]   # default: [claim_producer]
        write_semantics_envelope: [staged_async]            # operator-declared; ⊆ producer-declared at handshake
        # ... per-producer config

    executors:
      - name: claude-agent
        endpoint: localhost:7100
        protocols: [executor]
        # ...

    named_locks:
      - name: api-rate-limit
        mode: counting
        capacity: 5
    ```

    Rules:
    - Each peer entry's `protocols` field defaults to a single-element list matching the block name (`claim_producer` for entries under `claim_producers:`; `executor` for entries under `executors:`).
    - There is no separate `lifecycle_subscribers:` block. A peer that implements LifecycleSubscriber declares it via the `protocols` list on its primary block.
    - Operator-declared `write_semantics_envelope` (a set) MUST be a subset of the producer-declared envelope returned by `Capabilities()`. Startup fails fast if not.
    - Validation rules: every template-referenced producer name MUST be declared; every named lock referenced in a template MUST be declared.

    Add a sub-section 10.2 documenting how `RIMSKY_CONFIG` is loaded by all four rimsky binaries (rimsky-control-api, rimsky-supervisor, rimsky-scheduler, rimsky-migrate) per the persistence-pluggable spec.

12. Add `## 11. Modeling persistence contract` section:
    - 11.1 Tables owned by modeling: `rimsky_templates`, `rimsky_template_tags`, `rimsky_instances`, `rimsky_schedules`, `rimsky_frames`, `rimsky_events`, `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle`), `rimsky_nodes` (boundary case — node-state lives on top of foundation node-state; document the split in 11.3 below).
    - 11.2 Driver interface set scoped to modeling tables only: `Templates`, `Instances`, `Schedules`, `Frames`, `Events`, `LifecycleIdempotency`, `NodeMeta` (modeling-side node metadata; the foundation-side node-state lives in foundation persistence).
    - 11.3 Boundary with foundation persistence — `rimsky_nodes` is split: foundation owns `has_value`, `has_outstanding_request`, `auto_recovers` columns; modeling owns `frame_id`, `template_node_id` (the spec-side identifier within the template), and any other modeling correlation columns. Implementation: a single `rimsky_nodes` table with columns owned per-layer; migrations must distinguish.
    - 11.4 Migrations — modeling migrations live alongside foundation migrations in the migration runner; ordering ensures foundation migrations precede modeling migrations that depend on them.

13. Add `## 12. CLI shape` section:
    - 12.1 Purpose — `rimsky-cli` is a thin client to the control-plane API.
    - 12.2 Commands — list each: `template register`, `template get`, `template list`, `tag set`, `tag get`, `instance create`, `instance get`, `instance list`, `instance terminate`, `admin force-fire`, `compose up`, `compose down`, etc.
    - 12.3 Versioning — bare paths; no client-side server-version check; rolling upgrades operator-managed.
    - 12.4 Compose subcommand — `rimsky-cli compose` owns the `compose:<project>:<...>` namespace for tags and instance keys; client-side validation rejects manual usage of that prefix elsewhere.
    - 12.5 Out of scope — no auth flow yet (v1 concern); no plugin system.

14. Add `## 13. Vocabulary mapping (modeling ↔ foundation)` section. Brief table linking each user-facing name (`fresh`, `stale`, `running`, `failed`, `invalidate`, `recalculate`, `retry`, `invalidate(targets)`, `give_up`, `frame`, `template`, `instance`, `schedule`, `attributes`, `userdata`) to the foundation primitive it presents (or "modeling-only — no foundation correlate" for the latter set).

15. Add `## 14. Open questions` section for any modeling-layer concerns that remain unresolved at v1. At minimum: server-side enforcement of `compose:` reserved-prefix on tags (currently CLI-only). Other items as you encounter while writing.

16. Add `## 15. Out of scope` listing what's deliberately not in this contract: foundation concerns (covered in foundation contract); service protocol surface (covered in service-protocol contract); bundled service implementations (covered in `docs/claim-producer-author-guide.md` and `docs/executor-author-guide.md`); dashboard / observability (separate spec).

**Verification:**
```sh
test -f docs/specs/2026-05-04-modeling-layer-contract.md
wc -l docs/specs/2026-05-04-modeling-layer-contract.md  # expect > 500 lines
for h in '## 1.' '## 2.' '## 3.' '## 4.' '## 5.' '## 6.' '## 7.' '## 8.' '## 9.' '## 10.' '## 11.' '## 12.' '## 13.' '## 14.' '## 15.'; do
  grep -qF "$h" docs/specs/2026-05-04-modeling-layer-contract.md || echo "MISSING: $h"
done
```
The loop should print nothing.

### Task 4 — Write the service-protocol contract

**Files:**
- New: `docs/specs/2026-05-04-service-protocol-contract.md`

**Steps:**

1. Create `docs/specs/2026-05-04-service-protocol-contract.md` with header:

   ```markdown
   # Service-Protocol Contract

   **Status:** Authoritative until v1, 2026-05-04.
   **Scope:** Three Rimsky service protocols — `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Wire shapes, Go interfaces, capability handshakes, conformance requirements.
   **Authority:** Single source of truth for the service protocol surface. Supersedes the archived stores-redesign-v3 spec (`docs/history/2026-04-27-stores-redesign-v3-design.md`), the cleanup overlay (`docs/history/2026-04-30-stores-protocol-cleanup-design.md`), and the control-plane-and-store-lifecycle spec's service-protocol content (`docs/history/2026-05-01-control-plane-and-store-lifecycle-design.md`).
   **Layer position:** The protocols module (`github.com/fallguy/rimsky/protocols`) carries Go interfaces and protobuf bindings; foundation calls a subset (claim verbs, executor dispatch); modeling calls a subset (lifecycle hooks).
   ```

2. Add `## 1. Overview` section explaining the three protocols, the cross-cutting layer position, and the principle that any binary may implement zero, one, or multiple protocols (declared per peer in `rimsky.yml` per modeling-layer contract §10).

3. Add `## 2. ClaimProducer` section per spec §7.1. Include:
   - 2.1 Purpose & scope — produces claim handles; reconciles them at terminal.
   - 2.2 Wire surface — methods `Open`, `Commit`, `Abandon`, `Release`, `Capabilities`. Each with proto signature.
   - 2.3 Go interface — full Go signature in `protocols/claimproducer/claimproducer.go`.
   - 2.4 Types:
     ```go
     type OpenRequest struct { ClaimID uuid.UUID; Spec ClaimSpec }
     type ClaimSpec struct { /* ... opaque to Rimsky except scope substitution-leaf paths */ }
     type ClaimResult struct {
       Address               json.RawMessage
       Payload               json.RawMessage
       Scope                 json.RawMessage  // canonicalized scope bytes
       RealizedWriteSemantics WriteSemantics  // NEW: per-claim
     }
     type CapabilitiesResult struct {
       WriteSemanticsEnvelope []WriteSemantics  // NEW: set of permissible values
     }
     type WriteSemantics int
     const ( SyncWrite WriteSemantics = iota; StagedAsync; BlockingAsync; ReadOnly )
     ```
   - 2.5 Invariants:
     - **9b** *(no internal serialization on lock-shaped predicates)* — verbatim from foundation invariant catalog.
     - **20** *(claim content is inert in foundation — address/payload/scope are opaque bytes)* — applies to producer too: producer must not assume Rimsky inspects content.
     - **NEW: write-semantics uniformity per (producer, scope-bytes)** — across the lifetime of a producer, two `Open` calls returning byte-equal scope MUST return the same `RealizedWriteSemantics`. Producers enforce. Foundation relies on this for the conflict predicate.
     - **NEW: envelope conformance** — `RealizedWriteSemantics` returned by `Open` MUST be a member of the `WriteSemanticsEnvelope` returned by `Capabilities`.
   - 2.6 Conformance — the `rimsky-claim-producer-conformance` binary verifies all of the above against any binary claiming to implement ClaimProducer. Include a list of test categories: handshake, Open/Commit/Abandon/Release round-trip, write-semantics uniformity, envelope conformance, idempotency under retry.
   - 2.7 Removed from this protocol — the six lifecycle hooks (now in LifecycleSubscriber).
   - 2.8 Out of scope — store-internal queue semantics (e.g., postgres items-table) are not visible at the protocol level.

4. Add `## 3. LifecycleSubscriber` section per spec §7.2. Include:
   - 3.1 Purpose & scope — a service hooks into control-plane lifecycle events.
   - 3.2 Wire surface — six methods: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. Each with proto signature.
   - 3.3 Go interface — full Go signature in `protocols/lifecycle/lifecycle.go`.
   - 3.4 Implementation pattern — return `nil` from any method the binary doesn't react to. Binaries that don't react at all simply don't implement the service.
   - 3.5 Idempotency — control-api tracks idempotency in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle` in Task 31). Each event keyed by (peer-name, event-type, object-id).
   - 3.6 Conformance — `rimsky-conformance` binary's `--check-lifecycle` mode (combined with executor conformance into one binary per the binary-name decision).
   - 3.7 Out of scope — bidirectional events from peer back to Rimsky (peer can't initiate); cross-peer event ordering guarantees.

5. Add `## 4. Executor` section per spec §7.3:
   - 4.1 Purpose & scope — runs nodes given inputs.
   - 4.2 Wire surface — `Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`. Each with proto signature.
   - 4.3 Go interface.
   - 4.4 Async-callback path — wire requirement: executor POSTs `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`). The supervisor's chi route binds this exactly. Diagram the request/response sequence for both sync and async terminal cases.
   - 4.5 Capabilities response — includes `http_bridge_url` for dashboard discoverability.
   - 4.6 Userdata-is-opaque — modeling-layer invariant 11 re-asserted: executors MUST receive `userdata` verbatim (no substitution); rimsky doesn't introspect.
   - 4.7 Conformance — `rimsky-conformance --check-executor` mode.
   - 4.8 Out of scope — observability protocol (separate spec); execution semantics (executor-internal).

6. Add `## 5. Capability handshake protocol` section. Document the startup flow: control-api/supervisor probes each declared peer's `Capabilities()` per protocol it claims to implement; equality-checks operator-declared properties against producer-declared envelope; fails fast on mismatch.

7. Add `## 6. Conformance binaries` section. Three binaries per the binary-name sub-decision:
   - `rimsky-conformance` — covers Executor and LifecycleSubscriber. Flags: `--endpoint`, `--transport grpc|http+json`, `--check-executor`, `--check-lifecycle`, `--retention-test-seconds`, `--require-stub-mode`.
   - `rimsky-claim-producer-conformance` — renamed from `rimsky-store-conformance`. Covers ClaimProducer.
   - `rimsky-conformance-probe` — utility helper, retained as-is.

8. Add `## 7. Migration & vocabulary` section noting that this contract:
   - Renames `Store` → `ClaimProducer` at the protocol level.
   - Renames `region` → `scope` everywhere on the wire.
   - Splits lifecycle hooks out of `Store` into the new `LifecycleSubscriber` service.
   - Adds `RealizedWriteSemantics` to `ClaimResult`.
   - Adds `WriteSemanticsEnvelope` to `CapabilitiesResult`, replacing the single `WriteSemantics` field.

9. Add `## 8. Out of scope`. List: dashboard observability protocols (separate spec); bundled-service implementations; YAML config shape (covered in modeling contract §10).

**Verification:**
```sh
test -f docs/specs/2026-05-04-service-protocol-contract.md
for h in '## 1.' '## 2.' '## 3.' '## 4.' '## 5.' '## 6.' '## 7.' '## 8.'; do
  grep -qF "$h" docs/specs/2026-05-04-service-protocol-contract.md || echo "MISSING: $h"
done
grep -c 'ClaimProducer' docs/specs/2026-05-04-service-protocol-contract.md  # expect > 5
grep -c 'LifecycleSubscriber' docs/specs/2026-05-04-service-protocol-contract.md  # expect > 5
grep -c 'RealizedWriteSemantics' docs/specs/2026-05-04-service-protocol-contract.md  # expect >= 2
```

### Task 5 — Update CHANGELOG with Phase 1 contracts entry

**Files:**
- Edit: `CHANGELOG.md`

**Steps:**

1. Add a new section under `## Unreleased`:

   ```markdown
   ### Docs — Layer crystallization Phase 1: contracts

   - **Foundation contract finalized.** New `docs/specs/2026-05-04-foundation-contract.md`
     supersedes the 2026-05-03 draft (moved to `docs/history/`). Vocabulary
     updated (region → scope); subsystem package names settled (`cascade`,
     `locks`, `integration`); driver interface set collapsed
     (`Cascade`, `WorkerRequests`, `AdvisoryLocker`); module split commitment
     locked in.
   - **Modeling-layer comprehensive contract.** New
     `docs/specs/2026-05-04-modeling-layer-contract.md`. Single source of
     truth for templates, instances, frames, schedules, attributes,
     control-plane API, public vocabularies, YAML config shape, modeling
     persistence contract, and CLI shape. Supersedes content from the
     archived per-subsystem design docs in `docs/history/`.
   - **Service-protocol contract.** New
     `docs/specs/2026-05-04-service-protocol-contract.md`. Defines
     `ClaimProducer` (renamed from `Store`), `Executor`, and
     `LifecycleSubscriber`. Adds `RealizedWriteSemantics` per claim and
     `WriteSemanticsEnvelope` at handshake. Supersedes service-protocol
     content from the archived stores-redesign-v3 + cleanup overlay +
     control-plane-and-store-lifecycle docs.
   ```

**Verification:**
```sh
grep -q 'Layer crystallization Phase 1' CHANGELOG.md
```

### Task 6 — Create `foundation/go.mod` and skeleton directory structure

**Files:**
- New: `foundation/go.mod`
- New: `foundation/cascade/.gitkeep`
- New: `foundation/locks/.gitkeep`
- New: `foundation/integration/.gitkeep`
- New: `foundation/persistence/.gitkeep`
- New: `foundation/persistence/postgres/.gitkeep`
- New: `foundation/persistence/sqlite/.gitkeep`
- New: `foundation/shared/.gitkeep`
- New: `foundation/internal/.gitkeep`

**Steps:**

1. Create `foundation/go.mod`:
   ```
   module github.com/fallguy/rimsky/foundation

   go 1.25

   require (
   	github.com/google/uuid v1.6.0
   	github.com/jackc/pgx/v5 v5.9.2
   )
   ```
   The implementer will copy the actual full `require` and `require ( indirect )` blocks from the root `go.mod` matching what foundation packages use after the move. For now, include just the obvious ones; `go mod tidy` will fix the rest later.

2. Create the directory skeleton:
   ```sh
   mkdir -p foundation/cascade foundation/locks foundation/integration \
            foundation/persistence/postgres foundation/persistence/sqlite \
            foundation/shared foundation/internal
   touch foundation/cascade/.gitkeep foundation/locks/.gitkeep \
         foundation/integration/.gitkeep foundation/persistence/.gitkeep \
         foundation/persistence/postgres/.gitkeep foundation/persistence/sqlite/.gitkeep \
         foundation/shared/.gitkeep foundation/internal/.gitkeep
   ```

**Verification:**
```sh
test -f foundation/go.mod
test -d foundation/cascade && test -d foundation/locks && test -d foundation/integration
test -d foundation/persistence/postgres && test -d foundation/persistence/sqlite
test -d foundation/shared && test -d foundation/internal
```

### Task 7 — Create `protocols/go.mod` and skeleton

**Files:**
- New: `protocols/go.mod`
- New: `protocols/claimproducer/.gitkeep`
- New: `protocols/executor/.gitkeep`
- New: `protocols/lifecycle/.gitkeep`
- New: `protocols/proto/.gitkeep`

**Steps:**

1. Create `protocols/go.mod` with stdlib + grpc + protobuf only:
   ```
   module github.com/fallguy/rimsky/protocols

   go 1.25

   require (
   	google.golang.org/grpc v1.69.0
   	google.golang.org/protobuf v1.36.0
   )
   ```
   Match versions to whatever the root go.mod currently uses.

2. Create directories and `.gitkeep` files:
   ```sh
   mkdir -p protocols/claimproducer protocols/executor protocols/lifecycle protocols/proto
   touch protocols/claimproducer/.gitkeep protocols/executor/.gitkeep protocols/lifecycle/.gitkeep protocols/proto/.gitkeep
   ```

**Verification:**
```sh
test -f protocols/go.mod
test -d protocols/claimproducer && test -d protocols/executor && test -d protocols/lifecycle && test -d protocols/proto
```

### Task 8 — Create `go.work` for dev ergonomics

**Files:**
- New: `go.work`

**Steps:**

1. Create `go.work` at the repo root:
   ```
   go 1.25

   use (
   	.
   	./foundation
   	./protocols
   )
   ```

2. Run `go work sync` to populate `go.work.sum`.

**Verification:**
```sh
test -f go.work
go work sync && echo "go.work valid"
```

### Task 9 — Move foundation persistence code

**Files:**
- Move: `core/persistence/driver.go` → `foundation/persistence/driver.go`
- Move: `core/persistence/queue.go` → `foundation/persistence/queue.go` (will be reorganized in Phase 5)
- Move: `core/persistence/store.go` → `foundation/persistence/lock_holders.go` (rename)
- Move: `core/persistence/types.go` → `foundation/persistence/types.go`
- Move: `core/persistence/migrations.go` → `foundation/persistence/migrations.go`
- Move: `core/persistence/open.go` → `foundation/persistence/open.go`
- Move: `core/persistence/open_test.go` → `foundation/persistence/open_test.go`
- Move: `core/persistence/postgres/driver.go` → `foundation/persistence/postgres/driver.go`
- Move: `core/persistence/postgres/queue.go` → `foundation/persistence/postgres/queue.go`
- Move: `core/persistence/postgres/lock_holders.go` → `foundation/persistence/postgres/lock_holders.go`
- Move: `core/persistence/postgres/coordinator.go` → `foundation/persistence/postgres/advisory_locker.go` (rename)
- Move: `core/persistence/sqlite/driver.go` → `foundation/persistence/sqlite/driver.go`
- Move: `core/persistence/sqlite/queue.go` → `foundation/persistence/sqlite/queue.go`
- Move: `core/persistence/sqlite/lock_holders.go` → `foundation/persistence/sqlite/lock_holders.go`
- Move: `core/persistence/sqlite/coordinator.go` → `foundation/persistence/sqlite/advisory_locker.go` (rename)
- Other postgres/sqlite files (events, frames, instances, queue, schedules, store_lifecycle, templates) STAY in `core/persistence/postgres/` and `core/persistence/sqlite/` — these are modeling-layer tables and will move to `modeling/persistence/` in Task 13.

**Steps:**

1. List all current `core/persistence/postgres/*.go` files:
   ```sh
   ls core/persistence/postgres/
   ```
   Expected: `driver.go`, `queue.go`, `lock_holders.go`, `coordinator.go` (foundation-relevant); `events.go`, `frames.go`, `instances.go`, `schedules.go`, `store_lifecycle.go`, `templates.go`, `template_tags.go`, `nodes.go` (modeling-relevant — STAY for now).

2. Use `git mv` to move each foundation file:
   ```sh
   git mv core/persistence/driver.go foundation/persistence/driver.go
   git mv core/persistence/queue.go foundation/persistence/queue.go
   git mv core/persistence/store.go foundation/persistence/lock_holders.go
   git mv core/persistence/types.go foundation/persistence/types.go
   git mv core/persistence/migrations.go foundation/persistence/migrations.go
   git mv core/persistence/open.go foundation/persistence/open.go
   git mv core/persistence/open_test.go foundation/persistence/open_test.go
   git mv core/persistence/postgres/driver.go foundation/persistence/postgres/driver.go
   git mv core/persistence/postgres/queue.go foundation/persistence/postgres/queue.go
   git mv core/persistence/postgres/lock_holders.go foundation/persistence/postgres/lock_holders.go
   git mv core/persistence/postgres/coordinator.go foundation/persistence/postgres/advisory_locker.go
   git mv core/persistence/sqlite/driver.go foundation/persistence/sqlite/driver.go
   git mv core/persistence/sqlite/queue.go foundation/persistence/sqlite/queue.go
   git mv core/persistence/sqlite/lock_holders.go foundation/persistence/sqlite/lock_holders.go
   git mv core/persistence/sqlite/coordinator.go foundation/persistence/sqlite/advisory_locker.go
   ```

3. Update package declarations: every moved file's `package persistence` / `package postgres` / `package sqlite` declaration is correct as-is (the package names match the directory). Do NOT rename `package` lines unless the directory name changes.

4. Update import paths inside the moved files. Every file that previously imported `github.com/fallguy/rimsky/core/persistence/...` now needs to import `github.com/fallguy/rimsky/foundation/persistence/...`. Use:
   ```sh
   grep -rln 'github.com/fallguy/rimsky/core/persistence' foundation/ | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/core/persistence|github.com/fallguy/rimsky/foundation/persistence|g' "$f"
   done
   ```

5. Inside the moved files, rename Go identifiers `Coordinator` → `AdvisoryLocker` ONLY where it refers to the advisory-lock helper interface and impl. The interface declaration is in `foundation/persistence/driver.go` (or wherever `type Coordinator interface` lives). Apply:
   ```sh
   grep -rln '\bCoordinator\b' foundation/persistence/ | while read f; do
     # Capture context first; only rename in interface/method/struct declarations.
     # Manual review may be needed; the implementer should grep for 'Coordinator' first
     # and confirm each instance refers to the advisory-lock helper, not some other
     # Coordinator type from a different package.
     true
   done
   grep -rn '\bCoordinator\b' foundation/persistence/  # implementer reads each match and decides
   ```
   Apply renames manually for safety. Expected sites: `type Coordinator interface { ... }` → `type AdvisoryLocker interface { ... }`; struct that implements it; `coordinator` field/parameter names → `advisoryLocker`.

**Verification:**
```sh
ls foundation/persistence/
ls foundation/persistence/postgres/
ls foundation/persistence/sqlite/
# foundation/persistence should now contain: driver.go, queue.go, lock_holders.go, types.go, migrations.go, open.go, open_test.go (and the empty .gitkeep)
test -f foundation/persistence/driver.go
test -f foundation/persistence/postgres/advisory_locker.go && echo "renamed"
! grep -rn 'github.com/fallguy/rimsky/core/persistence' foundation/  # should print nothing
```

**Build state after this task: BROKEN.** This is expected and intentional — modeling and bundled-services callers still reference the old `core/persistence/...` paths; the build will not compile until Task 12 ("Restore buildable state") completes the import-path updates and rename cascade. Do NOT run `go build` and treat its failure as a Task 9 failure. Move on to Task 10.

### Task 10 — Move foundation cascade / supervisor / scheduler logic

**Background:** The cascade engine, integration layer, and lock manager logic currently lives spread across `core/scheduler/`, `core/supervisor/`, `core/node/`, `core/message/`, and `core/store/`. Spec §10 of the foundation-contract draft notes this. The post-Phase-2 layout puts the foundation-only parts into `foundation/cascade/`, `foundation/locks/`, `foundation/integration/`, and the modeling-only parts into `modeling/`.

The split is:

- **Pure foundation (move to `foundation/cascade/`):** node-state machine logic from `core/node/state.go`; cascade signal emission and propagation predicates (the parts that don't reference frames/instances/templates).
- **Pure foundation (move to `foundation/locks/`):** scope conflict primitives from `core/store/conflict.go` (the `RegionsByteEqual` / `ModeCoexists` helpers); claim-handle types currently in `core/store/types.go`.
- **Pure foundation (move to `foundation/integration/`):** acquisition tx code from `core/supervisor/runner_acquire.go`; auto-terminal from `core/supervisor/auto_terminal.go`; verify-before-run logic from `core/supervisor/runner.go`. Plus the gRPC client to ClaimProducer impls from `core/store/remote/`.
- **Mixed (split):** `core/scheduler/scheduler.go` has both foundation tick logic (advisory lock; orphan reaper; eligibility scan) and modeling logic (frame ticks; schedule ticks). Split: foundation parts go to `foundation/integration/conductor.go` and `foundation/integration/orphan_reaper.go`; modeling parts stay in `core/scheduler/` for now and move to `modeling/` in Task 13.
- **Modeling (do NOT move yet):** `core/frame/`, `core/controlapi/`, `core/attributes/`, `core/canonical/`, `core/scheduler/invalidate.go` (operator-originated invalidates are modeling), `core/scheduler/sweep_locks.go` (modeling-side sweep logic? — implementer review carefully), `core/cli/`, `core/observability/`, `core/qualityrule/`, `core/executor/`. These move to `modeling/` in Task 13.

**Files:**
- Move (foundation/cascade/): files from `core/node/`, parts of `core/scheduler/scheduler.go`.
- Move (foundation/locks/): `core/store/conflict.go`, `core/store/types.go`, parts of `core/persistence/postgres/lock_holders.go` (already moved in Task 9 — just needs the helpers).
- Move (foundation/integration/): `core/supervisor/runner_acquire.go`, `core/supervisor/auto_terminal.go`, `core/supervisor/runner.go`, `core/supervisor/runner_dispatch.go`, `core/store/remote/*.go`, parts of `core/scheduler/scheduler.go` (orphan reaper + tick).
- Move (foundation/shared/): `core/shared/types.go` (foundation-relevant subset; keep modeling types in `core/shared/`).

**Steps:**

1. Read each candidate file end-to-end before moving. Identify foundation-only vs modeling-only sections. The package import rules in CLAUDE.md are the guide: anything that references `template`, `instance`, `frame`, `schedule`, or `attributes` is modeling; anything that's purely state machine, claim handle, cascade signal, or worker-request is foundation.

2. For mixed files (notably `core/scheduler/scheduler.go`), split into two files:
   - `foundation/integration/conductor.go` — the tick advisory lock; the eligibility scan and dispatch; the verify-before-run integration.
   - `foundation/integration/orphan_reaper.go` — the 5×heartbeat orphan reap (single mechanism — see Task 41 for full unification).
   - Keep modeling-only logic in `core/scheduler/` for now; it moves in Task 13.

3. Move foundation files via `git mv` per the file list above. Use the same import-path-update sed loop as Task 9.

4. Rename the integration primary type to `Conductor`. Currently the supervisor's "Runner" type is the closest thing; rename `Runner` → `Conductor` in `foundation/integration/conductor.go`. All call sites within `foundation/` update; modeling-side callers (still in `core/`) will be fixed in Task 12 when modeling code moves.

5. For `core/store/`, move the foundation-relevant subset:
   - `core/store/conflict.go` → `foundation/locks/conflict.go`
   - `core/store/types.go` → `foundation/locks/types.go` (the `ClaimHandle`, `ClaimSpec`, `ClaimResult` types — but the `ClaimResult` in foundation is the *foundation-internal* representation; the wire-protocol `ClaimResult` lives in `protocols/claimproducer/`. Phase 4 reconciles. For now, keep the existing types and add a TODO comment.)
   - `core/store/remote/` → `foundation/integration/remote/` (the gRPC client to ClaimProducer impls).
   - `core/store/storetest/` → keep in place or move to a foundation testing util — the implementer's call. Default: move to `foundation/locks/storetest/`.
   - `core/store/registry.go` (the simple name → Store registry) — this is modeling-glue per spec §4.3 ("the registry becomes a modeling-layer concern"). Move to `modeling/config/` in Task 13.

**Verification:**
```sh
test -f foundation/integration/conductor.go
test -f foundation/integration/orphan_reaper.go
test -f foundation/locks/conflict.go
test -f foundation/locks/types.go
ls foundation/integration/remote/  # should have files
! grep -rn 'github.com/fallguy/rimsky/core/store' foundation/  # foundation should not reference core/store paths
! grep -rn 'github.com/fallguy/rimsky/core/persistence' foundation/  # already verified in Task 9
```

**Build state after this task: BROKEN.** Same expectation as Task 9. Do NOT run `go build`. Task 12 is the buildable gate.

### Task 11 — Move proto sources to protocols module

**Files:**
- Move: `proto/v1/node_executor.proto` → `protocols/proto/v1/executor.proto`
- Move: `proto/v1/store_service.proto` → `protocols/proto/v1/claim_producer.proto`
- Move: `proto/v1/events.proto` → `protocols/proto/v1/events.proto`
- Move: `proto/v1/executor_observability.proto` → `protocols/proto/v1/executor_observability.proto`
- Move: `proto/v1/store_observability.proto` → `protocols/proto/v1/store_observability.proto`
- Move: `proto/v1/gen/` → `protocols/proto/v1/gen/`

**Steps:**

1. `git mv` the files:
   ```sh
   mkdir -p protocols/proto/v1
   git mv proto/v1/node_executor.proto protocols/proto/v1/executor.proto
   git mv proto/v1/store_service.proto protocols/proto/v1/claim_producer.proto
   git mv proto/v1/events.proto protocols/proto/v1/events.proto
   git mv proto/v1/executor_observability.proto protocols/proto/v1/executor_observability.proto
   git mv proto/v1/store_observability.proto protocols/proto/v1/store_observability.proto
   git mv proto/v1/gen protocols/proto/v1/gen
   rmdir proto/v1 proto || true  # remove empty parents
   ```

2. Update the `option go_package` line in each `.proto` file to reflect the new module path. Example for `executor.proto`:
   ```diff
   - option go_package = "github.com/fallguy/rimsky/proto/v1/gen;rimsky_v1";
   + option go_package = "github.com/fallguy/rimsky/protocols/proto/v1/gen;rimsky_v1";
   ```
   Apply to all five `.proto` files.

3. Update `Makefile` `proto-gen` target paths from `proto/v1/` to `protocols/proto/v1/`:
   ```
   # in Makefile, find the proto-gen target and replace
   ```
   The implementer reads the current Makefile and updates the affected paths.

4. Run `make proto-gen` to regenerate bindings under the new path.

5. Update import paths in all Go files that previously imported the proto bindings:
   ```sh
   grep -rln 'github.com/fallguy/rimsky/proto/v1/gen' . \
     --include='*.go' | grep -v 'protocols/proto/' | grep -v 'docs/history/' \
     | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/proto/v1/gen|github.com/fallguy/rimsky/protocols/proto/v1/gen|g' "$f"
   done
   ```

   In the TS executor (`executors/claude-agent/`), proto bindings are TypeScript-generated; check `executors/claude-agent/src/grpc/` or wherever TS proto bindings are generated. Update path references in TS code accordingly. The TS package shouldn't be affected by the Go module reorg per se, only by proto path changes; the path references in TS-generated code may need regeneration.

**Verification:**
```sh
test -d protocols/proto/v1
test -f protocols/proto/v1/executor.proto
test -f protocols/proto/v1/claim_producer.proto
test ! -d proto  # old dir gone
make proto-gen  # regenerates without error
! grep -rn 'github.com/fallguy/rimsky/proto/v1' . \
  --include='*.go' --exclude-dir=docs/history --exclude-dir=protocols
```

**Build state after this task: still BROKEN.** `make proto-gen` runs through codegen tools and does not require the surrounding code to compile, so it can succeed even while the build is broken. Task 12 is still the buildable gate.

### Task 12 — Restore buildable state: update modeling-side imports + rename cascade

**Goal:** End this task with `go build ./... && go test ./... -count=1 && make lint` all clean. This is the catchall task that resolves the deferred work from Tasks 9–11. After Task 12, the working tree compiles.

**Files:**
- Many. Every file in `core/`, `stores/`, `executors/`, `test/` that imports a foundation-moved package or references a foundation-renamed type.
- Move: `core/store/registry.go` → `modeling/config/registry.go` (creating `modeling/config/` if needed).

**Steps:**

1. Globally rewrite the four import paths the foundation move broke:
   ```sh
   # 1a. core/persistence → foundation/persistence
   grep -rln 'github.com/fallguy/rimsky/core/persistence' core/ stores/ executors/ test/ \
     --include='*.go' | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/core/persistence|github.com/fallguy/rimsky/foundation/persistence|g' "$f"
   done

   # 1b. core/store types/conflict → foundation/locks
   grep -rln '"github.com/fallguy/rimsky/core/store"' core/ stores/ executors/ test/ \
     --include='*.go' | while read f; do
     sed -i '' 's|"github.com/fallguy/rimsky/core/store"|"github.com/fallguy/rimsky/foundation/locks"|g' "$f"
   done

   # 1c. core/store/remote → foundation/integration/remote
   grep -rln 'github.com/fallguy/rimsky/core/store/remote' core/ stores/ executors/ test/ \
     --include='*.go' | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/core/store/remote|github.com/fallguy/rimsky/foundation/integration/remote|g' "$f"
   done

   # 1d. core/store/storetest → foundation/locks/storetest (if Task 10 moved it there)
   grep -rln 'github.com/fallguy/rimsky/core/store/storetest' . \
     --include='*.go' --exclude-dir=docs/history | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/core/store/storetest|github.com/fallguy/rimsky/foundation/locks/storetest|g' "$f"
   done
   ```

2. Move `core/store/registry.go` to its modeling-glue home now (rather than waiting for Task 13). This prevents `core/store/` from being a stranded broken package with a registry but no types. Create `modeling/config/` if it doesn't exist:
   ```sh
   mkdir -p modeling/config
   git mv core/store/registry.go modeling/config/registry.go
   ```
   Open `modeling/config/registry.go`. Update its package declaration to `package config`. Update its import paths to use `foundation/locks` for the type it tracks (the old `Store` interface, which is in `foundation/locks/types.go` post-Task-10; will be renamed `ClaimProducer` in Task 23). Update the type the registry stores from `*store.Store` to `*locks.ClaimProducer` (or whatever the temporary name is — match the moved types.go).

3. Update foundation-renamed-type call sites. The Task 9 / Task 10 renames cascaded these required call-site fixes:

   a. **`Coordinator` interface → `AdvisoryLocker`.** Find every call site:
      ```sh
      grep -rn '\b[Pp]ersistence\.Coordinator\b\|\bcoordinator\.Lock\b\|\.coord\.Try' . \
        --include='*.go' --exclude-dir=docs --exclude-dir=foundation
      ```
      For each match: rename `Coordinator` → `AdvisoryLocker` in the call expression; rename local variable names like `coord` → `advisoryLocker` only when context makes the rename clear (otherwise leave the local name and just fix the imported type reference).

   b. **`Runner` (foundation/integration primary type) → `Conductor`.** If Task 10 renamed the type but not call sites, fix here:
      ```sh
      grep -rn 'integration\.Runner\b\|integration\.NewRunner\b' . --include='*.go'
      ```
      Rename `Runner` → `Conductor` and `NewRunner` → `NewConductor` at each call site.

   c. **Note:** `RegionsByteEqual` (→ `ScopesByteEqual`) and `core/store.Store` (→ `ClaimProducer`) renames are deferred to Phase 3 (Task 18) and Phase 4 (Tasks 20–23) respectively. For Task 12, leave these names as-is — the moved files in foundation still use them under the old names. They'll rename cleanly under their planned tasks.

4. Run the full pipeline and resolve any remaining errors:
   ```sh
   go build ./...
   ```
   Expected residual error classes after step 3:
   - `core/store` package missing — already moved away. Any leftover `import "github.com/fallguy/rimsky/core/store"` in code outside `core/store/` is a missed substitution; re-run the step-1 sed against the affected files.
   - Generated code referencing old paths — `make proto-gen` to regenerate.
   - Test fixtures referencing `core/internal/pgtest`. If pgtest moved, update the import path; if not yet moved, leave for Task 13.

   When an error class is encountered that does NOT match the catalog above, STOP and re-read this task plus the foundation contract before improvising. A foreign error class likely means a Task 10 split decision was made differently than the plan assumes.

5. Once `go build ./...` is clean, run tests and lint:
   ```sh
   go test ./... -count=1
   make lint
   ```
   Resolve any failures. Lint failures are typically depguard violations (file in a package that newly violates the `pgx-isolation` rule because the path changed) — these require updating `.golangci.yml` (not yet done; Task 14 does the canonical update). For now, add temporary depguard exceptions if needed; remove in Task 14.

**Verification:**
```sh
go build ./...   # must exit 0
go test ./... -count=1   # must exit 0
make lint   # must exit 0
test -f modeling/config/registry.go && echo "registry moved"
! ls core/store 2>/dev/null  # core/store directory should be gone
```

All four checks must pass. If any fail, the task is not done.

### Task 13 — Move modeling subdirectories to `modeling/`

**Goal:** Relocate the modeling-layer subdirectories from `core/` to `modeling/`. Build will be broken until Task 13e completes the import-path updates.

**Files:**
- Move: `core/attributes/` → `modeling/attribute/`
- Move: `core/canonical/` → `modeling/template/canonical/`
- Move: `core/controlapi/` → `modeling/controlapi/`
- Move: `core/frame/` → `modeling/frame/`
- Move: `core/observability/` → `modeling/observability/`
- Move: `core/qualityrule/` → `modeling/qualityrule/`
- Move: `core/executor/` → `modeling/executor/`
- Move: `core/cli/` → `modeling/cli/`
- Move: `core/config/` → `modeling/config/` (merge with the `modeling/config/` created in Task 12 for the registry)

**Steps:**

1. Create `modeling/` and the `modeling/template/` parent if missing.

2. Move each subdirectory via `git mv`:
   ```sh
   mkdir -p modeling/template
   git mv core/attributes modeling/attribute
   git mv core/canonical modeling/template/canonical
   git mv core/controlapi modeling/controlapi
   git mv core/frame modeling/frame
   git mv core/observability modeling/observability
   git mv core/qualityrule modeling/qualityrule
   git mv core/executor modeling/executor
   git mv core/cli modeling/cli
   ```

3. The `core/config/` move needs care — `modeling/config/` already exists with `registry.go` from Task 12. Merge by moving each file individually:
   ```sh
   for f in core/config/*; do
     git mv "$f" modeling/config/
   done
   rmdir core/config 2>/dev/null
   ```

**Verification:**
```sh
test ! -d core/attributes && test -d modeling/attribute
test ! -d core/controlapi && test -d modeling/controlapi
test ! -d core/frame && test -d modeling/frame
test ! -d core/observability && test -d modeling/observability
test ! -d core/qualityrule && test -d modeling/qualityrule
test ! -d core/executor && test -d modeling/executor
test ! -d core/cli && test -d modeling/cli
test ! -d core/config && test -d modeling/config
test -d modeling/template/canonical
```

**Build state after this task: BROKEN.** Task 13e is the buildable gate for Phase 2's modeling moves.

### Task 13b — Split `core/scheduler/` into modeling and foundation parts

**Goal:** `core/scheduler/scheduler.go` contains both foundation-relevant tick logic (advisory lock acquisition; orphan reaper trigger; eligibility scan) and modeling-relevant tick logic (frame ticks; schedule ticks). Task 10 already moved the foundation portion to `foundation/integration/conductor.go`. This task moves the modeling-relevant remainder to `modeling/scheduler/`.

**Files:**
- Move: `core/scheduler/invalidate.go` → `modeling/scheduler/invalidate.go`
- Move: `core/scheduler/invalidate_test.go` → `modeling/scheduler/invalidate_test.go`
- Move: `core/scheduler/sweep_locks.go` → `modeling/scheduler/sweep_locks.go`
- Move: `core/scheduler/pure_cascade_test.go` → `modeling/scheduler/pure_cascade_test.go`
- Move: any remaining `core/scheduler/*.go` files that are modeling-relevant (frame-tick wiring, schedule-tick wiring) → `modeling/scheduler/`.

**Steps:**

1. List remaining `core/scheduler/*.go` files:
   ```sh
   ls core/scheduler/
   ```

2. For each file, decide foundation vs modeling. Default rules:
   - Files referencing templates, instances, frames, schedules → modeling.
   - Files purely about advisory-lock-tick + orphan reaper trigger + eligibility scan → foundation (already moved in Task 10).
   - `invalidate.go` (operator-originated invalidate handling) → modeling.
   - `sweep_locks.go` → modeling (it's a sweep over modeling-side claim-holder records that runs alongside the foundation orphan reaper; reread to confirm).

3. `git mv` each modeling file:
   ```sh
   mkdir -p modeling/scheduler
   git mv core/scheduler/invalidate.go modeling/scheduler/invalidate.go
   git mv core/scheduler/invalidate_test.go modeling/scheduler/invalidate_test.go
   git mv core/scheduler/sweep_locks.go modeling/scheduler/sweep_locks.go
   git mv core/scheduler/pure_cascade_test.go modeling/scheduler/pure_cascade_test.go
   # any remaining files that are modeling
   ```

4. After all modeling files are moved, `core/scheduler/` should be empty (the foundation portion went to `foundation/integration/` in Task 10):
   ```sh
   ls core/scheduler/  # empty
   rmdir core/scheduler 2>/dev/null
   ```

5. Each moved file keeps `package scheduler` declaration (the leaf directory name still matches).

**Verification:**
```sh
test ! -d core/scheduler
test -d modeling/scheduler
ls modeling/scheduler/*.go  # should list the moved files
```

**Build state after this task: BROKEN.** Task 13e is the gate.

### Task 13c — Move modeling persistence files

**Files:**
- Move: `core/persistence/postgres/{events,frames,instances,nodes,schedules,store_lifecycle,template_tags,templates}.go` → `modeling/persistence/postgres/`.
- Move: `core/persistence/sqlite/{events,frames,instances,schedules,store_lifecycle,templates}.go` → `modeling/persistence/sqlite/`.
- Move: `core/persistence/conformance/` → `modeling/persistence/conformance/`.

**Steps:**

1. Create target directories:
   ```sh
   mkdir -p modeling/persistence/postgres modeling/persistence/sqlite
   ```

2. Move modeling postgres files:
   ```sh
   for f in events frames instances nodes schedules store_lifecycle template_tags templates; do
     [ -f core/persistence/postgres/${f}.go ] && git mv core/persistence/postgres/${f}.go modeling/persistence/postgres/
   done
   ```

3. Move modeling sqlite files:
   ```sh
   for f in events frames instances schedules store_lifecycle templates; do
     [ -f core/persistence/sqlite/${f}.go ] && git mv core/persistence/sqlite/${f}.go modeling/persistence/sqlite/
   done
   ```

4. Move conformance:
   ```sh
   [ -d core/persistence/conformance ] && git mv core/persistence/conformance modeling/persistence/conformance
   ```

5. Verify `core/persistence/postgres/` and `core/persistence/sqlite/` are now empty (foundation portion moved in Task 9; modeling portion moved here):
   ```sh
   ls core/persistence/postgres/  # empty
   ls core/persistence/sqlite/  # empty
   rmdir core/persistence/postgres core/persistence/sqlite core/persistence 2>/dev/null
   ```

**Verification:**
```sh
test ! -d core/persistence
test -d modeling/persistence/postgres && test -d modeling/persistence/sqlite
ls modeling/persistence/postgres/*.go | wc -l  # should be > 5
```

**Build state after this task: BROKEN.** Task 13e is the gate.

### Task 13d — Flatten `core/cmd/` to root `cmd/`; move shared and internal

**Files:**
- Move: `core/cmd/*` → `cmd/*` (eight binary directories).
- Move: `core/shared/` → `modeling/shared/` (entire directory; foundation has its own `foundation/shared/` already from Task 10).
- Move: `core/internal/` → `modeling/internal/`.

**Steps:**

1. Move cmd:
   ```sh
   git mv core/cmd cmd
   ```

2. Move shared (all of it goes to modeling — the foundation already has `foundation/shared/` from Task 10):
   ```sh
   git mv core/shared modeling/shared
   ```

3. Move internal:
   ```sh
   git mv core/internal modeling/internal
   ```

4. Verify `core/` is now empty:
   ```sh
   find core -type f 2>/dev/null | head -5  # empty
   rmdir core 2>/dev/null
   ```

**Verification:**
```sh
test ! -d core
test -d cmd
test -d modeling/shared
test -d modeling/internal
ls cmd/  # should list the binary directories
```

**Build state after this task: BROKEN.** Task 13e is the gate.

### Task 13e — Update import paths globally; restore buildable state

**Goal:** Resolve every broken import that the Tasks 13–13d moves introduced. After this task, `go build ./...` is clean again.

**Files:**
- Many: every Go file that imports a previously-`core/...` path that is now under `modeling/` or `cmd/`.
- Edit: `Dockerfile.cli`, `deploy/Dockerfile.all`, `executors/http-node/Dockerfile`, and any other Dockerfile that builds a binary from `./core/cmd/...`.

**Steps:**

1. Update import paths globally:
   ```sh
   grep -rln 'github.com/fallguy/rimsky/core/' . \
     --include='*.go' --exclude-dir=docs --exclude-dir=foundation --exclude-dir=protocols \
     | while read f; do
     sed -i '' 's|github.com/fallguy/rimsky/core/cmd|github.com/fallguy/rimsky/cmd|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/attributes|github.com/fallguy/rimsky/modeling/attribute|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/canonical|github.com/fallguy/rimsky/modeling/template/canonical|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/controlapi|github.com/fallguy/rimsky/modeling/controlapi|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/frame|github.com/fallguy/rimsky/modeling/frame|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/observability|github.com/fallguy/rimsky/modeling/observability|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/qualityrule|github.com/fallguy/rimsky/modeling/qualityrule|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/executor|github.com/fallguy/rimsky/modeling/executor|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/cli|github.com/fallguy/rimsky/modeling/cli|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/config|github.com/fallguy/rimsky/modeling/config|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/scheduler|github.com/fallguy/rimsky/modeling/scheduler|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/shared|github.com/fallguy/rimsky/modeling/shared|g' "$f"
     sed -i '' 's|github.com/fallguy/rimsky/core/internal|github.com/fallguy/rimsky/modeling/internal|g' "$f"
   done
   ```

2. Update Dockerfile build paths:
   ```sh
   grep -rln './core/cmd/' . --include='Dockerfile*' \
     | while read f; do
     sed -i '' 's|./core/cmd/|./cmd/|g' "$f"
   done
   ```

3. Run the full build pipeline. Resolve remaining errors:
   ```sh
   go build ./...
   ```
   Expected residual error classes:
   - A `core/...` import slipped through. Re-run the step-1 sed against the affected file.
   - A package declaration mismatch (file says `package canonical` but is in `modeling/template/canonical/`, which is fine — `package canonical` matches the leaf dir). If a mismatch occurs, edit the file.
   - A test fixture under `modeling/internal/pgtest/` referenced by a test in `foundation/persistence/postgres/`. Foundation persistence tests should use a foundation-internal pgtest, not modeling's. If this surfaces, decide: copy the helper into `foundation/internal/pgtest/`, or leave the modeling one for modeling tests and add a separate one in foundation. Default: copy to foundation, leave modeling's in place.

4. Tests and lint:
   ```sh
   go test ./... -count=1
   make lint
   ```

**Verification:**
```sh
go build ./...   # exit 0
go test ./... -count=1   # exit 0
make lint   # exit 0
! grep -rn 'github.com/fallguy/rimsky/core/' . \
  --include='*.go' --exclude-dir=docs --exclude-dir=foundation --exclude-dir=protocols
test ! -d core
```

All five checks must pass.

### Task 14 — Update Makefile and `.golangci.yml` for the multi-module layout

**Files:**
- Edit: `Makefile`
- Edit: `.golangci.yml`

**Steps:**

1. Update `Makefile` proto-gen target paths (already partially done in Task 11; verify completeness).

2. Update build targets that reference `./core/...`:
   - Find every `./core/...` in the Makefile.
   - Replace with appropriate post-Task-13 path: `./cmd/...`, `./modeling/...`, `./foundation/...`.

3. Add multi-module helpers if useful:
   ```makefile
   .PHONY: test-all
   test-all:
   	go test ./... ./foundation/... ./protocols/...

   .PHONY: build-all
   build-all:
   	go build ./... ./foundation/... ./protocols/...
   ```

4. Update `.golangci.yml`:
   - Change depguard `pgx-isolation` rule paths:
     ```yaml
     depguard:
       rules:
         pgx-isolation:
           list-mode: lax
           files:
             - "$all"
             - "!**/foundation/persistence/postgres/**"
             - "!**/modeling/persistence/postgres/**"
             - "!**/cmd/**"
             - "!**/foundation/internal/pgtest/**"
             - "!**/modeling/internal/pgtest/**"
             - "!**/scenario/**"
             - "!**/stores/**"
             - "!**/test/smoke/**"
           deny:
             - pkg: "github.com/jackc/pgx/v5"
               desc: "pgx is allowed only in foundation/persistence/postgres/, modeling/persistence/postgres/, cmd/, internal/pgtest/, scenario/, stores/, and test/smoke/. Use the persistence interfaces."
             - pkg: "github.com/jackc/pgx/v5/pgxpool"
               desc: "see pgx isolation rule above"
             - pkg: "github.com/jackc/pgx/v5/pgconn"
               desc: "see pgx isolation rule above"
     ```
   - Add a new depguard rule that prevents the modeling layer from importing `foundation/internal/`:
     ```yaml
     foundation-internal-isolation:
       list-mode: lax
       files:
         - "$all"
         - "!**/foundation/**"
       deny:
         - pkg: "github.com/fallguy/rimsky/foundation/internal"
           desc: "foundation/internal/ is private to the foundation module. Use the public foundation packages."
     ```

**Verification:**
```sh
make lint
make build-all  # if added
make proto-gen
```

### Task 15 — Update CLAUDE.md and CHANGELOG for Phase 2 module split

**Files:**
- Edit: `CLAUDE.md`
- Edit: `CHANGELOG.md`

**Steps:**

1. Update `CLAUDE.md`:
   - "What this repo is" — describe the three-module layout.
   - "Package import rules" section — completely rewrite. The new rules:
     ```markdown
     - `foundation/` — own Go module (`github.com/fallguy/rimsky/foundation`). Cascade engine + lock manager + integration + foundation persistence. Depends on `protocols` + stdlib.
     - `protocols/` — own Go module (`github.com/fallguy/rimsky/protocols`). ClaimProducer / Executor / LifecycleSubscriber Go interfaces + protobuf bindings. Stdlib + grpc + protobuf only.
     - Root module — modeling layer + cmd binaries + bundled service reference impls. Depends on foundation + protocols.
     - foundation/internal/ — private to foundation; modeling and bundled services CANNOT import.
     - depguard enforces pgx isolation and foundation-internal isolation; see .golangci.yml.
     ```
   - "Where to look first" section — replace per-subsystem doc references with the three contracts:
     ```markdown
     - Foundation: `docs/specs/2026-05-04-foundation-contract.md`
     - Modeling: `docs/specs/2026-05-04-modeling-layer-contract.md`
     - Service protocols: `docs/specs/2026-05-04-service-protocol-contract.md`
     ```
     (Full CLAUDE.md rewrite happens in Task 50; this task only does the import-rules section + the contract pointers.)

2. Add CHANGELOG entry under `## Unreleased`:
   ```markdown
   ### Refactor — Layer crystallization Phase 2: module split (γ)

   - **Three Go modules established.** `github.com/fallguy/rimsky/foundation`,
     `github.com/fallguy/rimsky/protocols`, and the root `github.com/fallguy/rimsky`.
     Coordinated by `go.work`. The `foundation` module owns cascade + locks +
     integration + foundation persistence; the `protocols` module owns the
     three service-protocol Go interfaces and protobuf bindings (stdlib +
     grpc/protobuf only deps); the root owns modeling + cmd binaries +
     bundled service reference impls.
   - **`core/` directory dissolved.** Contents migrated to `foundation/`,
     `modeling/`, `cmd/`, or stayed at root per the four-layer model.
   - **`proto/v1/` migrated to `protocols/proto/v1/`.** `option go_package`
     updated; bindings regenerated. Two proto files renamed:
     `node_executor.proto` → `executor.proto`; `store_service.proto` →
     `claim_producer.proto`.
   - **`persistence.Coordinator` renamed `persistence.AdvisoryLocker`.**
     Frees the `Coordinator` name space for `foundation/integration/Conductor`.
   - **`.golangci.yml` depguard rules updated** for new paths; new
     `foundation-internal-isolation` rule prevents modeling/services from
     reaching into foundation/internal/.
   - **No semantic code changes.** Renames, moves, depguard updates only.
   ```

**Verification:**
```sh
grep -q 'foundation/internal' CLAUDE.md
grep -q 'Layer crystallization Phase 2' CHANGELOG.md
go build ./... && go test ./... -count=1 && make lint  # full pipeline still clean
```

### Task 16 — Region → scope rename: SQL columns and migrations

**Files:**
- Edit: every `*.sql` migration in `foundation/persistence/migrations/` (or wherever migrations live now).
- Edit: `foundation/persistence/postgres/lock_holders.go` and `foundation/persistence/sqlite/lock_holders.go` — column references.

**Steps:**

1. Find migrations referencing `region_data`:
   ```sh
   grep -rln 'region_data' foundation/persistence/ modeling/persistence/
   ```

2. In the latest migration that creates `rimsky_lock_holders` (will be migration NNN_*.sql), change column `region_data BYTEA` → `scope_data BYTEA`. If multiple migrations touch this column, pick the canonical creation site and update; intermediate migrations that ALTER may need updates too.

3. Pre-v1 break-freely: it's acceptable to add a new migration that does `ALTER TABLE rimsky_lock_holders RENAME COLUMN region_data TO scope_data;` and update the column references in the canonical CREATE migration to match. Implementer's call between rewriting the CREATE in place vs. an ALTER migration. Default: rewrite the CREATE in place (cleaner under pre-v1 break-freely).

4. Update the same column reference in any indexes or constraints that name `region_data`.

5. In `foundation/persistence/postgres/lock_holders.go` and `foundation/persistence/sqlite/lock_holders.go`, rename:
   - SQL string literals: `region_data` → `scope_data`.
   - Go struct field: `RegionData []byte` → `ScopeData []byte`.
   - Any function names involving "Region" referring to scope: `LookupByRegion` → `LookupByScope`, `RegionConflictPredicate` → `ScopeConflictPredicate`, etc.

**Verification:**
```sh
! grep -rn 'region_data' foundation/ modeling/ stores/ executors/ test/  # zero results
grep -rn 'scope_data' foundation/persistence/  # multiple results
```

### Task 17 — Region → scope rename: proto fields

**Files:**
- Edit: `protocols/proto/v1/claim_producer.proto`
- Edit: any other `.proto` files referencing `region` in the conflict-predicate sense.
- Regen: `protocols/proto/v1/gen/`

**Steps:**

1. In each `.proto` file, find `region` field names that mean "scope":
   ```sh
   grep -rn '\bregion\b' protocols/proto/v1/
   ```

2. Rename:
   - `bytes region = N;` → `bytes scope = N;` (preserve field number for wire compat is moot in pre-v1; just rename).
   - `RegionConflict` message → `ScopeConflict` message.
   - All field references and message types.

3. Run `make proto-gen`.

4. Update any Go code that references the old proto fields. Common sites: `Region` → `Scope`, `GetRegion()` → `GetScope()`. Apply via grep:
   ```sh
   grep -rln '\bRegion\b\|\.GetRegion\(' . --include='*.go' \
     --exclude-dir=docs --exclude-dir=protocols/proto/v1/gen \
     | while read f; do
     # CAREFUL: this grep might match unrelated 'Region' uses (e.g., AWS region).
     # Implementer reads each match and decides.
     true
   done
   grep -rn '\bRegion\b\|\.GetRegion\(' . --include='*.go' \
     --exclude-dir=docs --exclude-dir=protocols/proto/v1/gen | head -50
   ```
   Apply renames manually per match.

**Verification:**
```sh
make proto-gen
go build ./...
! grep -rn '\bregion\b' protocols/proto/v1/*.proto  # zero in source protos
! grep -rn 'GetRegion\(' . --include='*.go' --exclude-dir=docs --exclude-dir=protocols/proto/v1/gen
```

### Task 18 — Region → scope rename: Go identifiers across all packages

**Files:**
- Many: every Go file that uses `Region` / `region` in the conflict-predicate sense.

**Steps:**

1. Get the full list:
   ```sh
   grep -rln '\b[Rr]egion\b' . --include='*.go' \
     --exclude-dir=docs --exclude-dir=protocols/proto/v1/gen \
     | sort -u
   ```

2. For each file:
   - Read it.
   - Rename all conflict-predicate-sense `Region`/`region` occurrences to `Scope`/`scope`.
   - Skip any unrelated `region` (e.g., a comment about "this region of code", an AWS region in deploy code, etc.). Use judgement.
   - Common renames: `RegionData` → `ScopeData`; `RegionConflict` → `ScopeConflict`; `RegionsByteEqual` → `ScopesByteEqual`; `WithRegion(...)` → `WithScope(...)`; `region []byte` → `scope []byte`.

3. Update tests:
   - Rename test names containing "Region" to "Scope".
   - Rename test fixtures that name regions to scopes.

4. Update doc comments referencing region in the conflict-predicate sense.

**Verification:**
```sh
go build ./...
go test ./... -count=1
make lint
# Region references that remain should be unrelated to conflict-predicate:
grep -rn '\bRegion\b' . --include='*.go' \
  --exclude-dir=docs --exclude-dir=protocols/proto/v1/gen | head -20
# Implementer reviews remaining matches; should be zero conflict-predicate-sense ones.
```

### Task 19 — Region → scope rename: docs and CHANGELOG

**Files:**
- Edit: `docs/specs/2026-05-04-foundation-contract.md` (already done in Task 1 with vocabulary update — verify).
- Edit: `docs/specs/2026-05-04-service-protocol-contract.md` (already uses `scope` per Task 4 — verify).
- Edit: `docs/specs/2026-05-04-modeling-layer-contract.md` (verify uses `scope`).
- Edit: any other doc files in `docs/` that reference `region` in the conflict-predicate sense.

**Steps:**

1. Grep:
   ```sh
   grep -rln '\b[Rr]egion\b' docs/ --include='*.md' --exclude-dir=docs/history
   ```

2. For each match, read and rename if conflict-predicate-sense. Skip if unrelated (e.g., "regional access", "geographic region"). Note: the foundation contract Task 1 already did this for `2026-05-04-foundation-contract.md`; this task catches anything else.

3. Add CHANGELOG entry:
   ```markdown
   ### Refactor — Layer crystallization Phase 3: region → scope rename

   - **`region` → `scope` everywhere on the wire and in foundation
     internals.** Proto field `bytes region` → `bytes scope`; SQL column
     `region_data` → `scope_data`; Go struct field `RegionData` →
     `ScopeData`; helper `RegionsByteEqual` → `ScopesByteEqual`. The
     §7.7 byte-equal-region invariant is now byte-equal-scope. Foundation
     contract, modeling-layer contract, and service-protocol contract all
     use the new vocabulary. Pre-v1 dev-DB-nuke applies; no data
     migration shim.
   ```

**Verification:**
```sh
go build ./... && go test ./... -count=1 && make lint
grep -q 'Layer crystallization Phase 3' CHANGELOG.md
```

### Task 20 — Define new `protocols/claimproducer/` interface

**Files:**
- New: `protocols/claimproducer/claimproducer.go`
- New: `protocols/claimproducer/types.go`
- New: `protocols/claimproducer/doc.go`

**Steps:**

1. Create `protocols/claimproducer/doc.go`:
   ```go
   // Package claimproducer defines the ClaimProducer service protocol.
   //
   // A ClaimProducer is a service that produces claim handles for
   // Rimsky's lock manager. The protocol surface is four runtime verbs
   // (Open, Commit, Abandon, Release) plus one startup handshake
   // (Capabilities). See docs/specs/2026-05-04-service-protocol-contract.md
   // for the authoritative spec.
   package claimproducer
   ```

2. Create `protocols/claimproducer/types.go`:
   ```go
   package claimproducer

   import (
   	"encoding/json"

   	"github.com/google/uuid"
   )

   // WriteSemantics declares how a claim handle's writes coexist with
   // concurrent claims on byte-equal scopes. See spec §2.4.
   type WriteSemantics int

   const (
   	WriteSemanticsUnknown WriteSemantics = iota
   	WriteSemanticsSync
   	WriteSemanticsStagedAsync
   	WriteSemanticsBlockingAsync
   	WriteSemanticsReadOnly
   )

   // OpenRequest is the request for the Open verb.
   type OpenRequest struct {
   	ClaimID uuid.UUID
   	Spec    json.RawMessage // opaque to Rimsky; producer-defined shape
   }

   // ClaimResult is the response from Open.
   //
   // Address, Payload, and Scope are inert in Rimsky (foundation invariant 20):
   // Rimsky reads them only at substitution-leaf extraction. RealizedWriteSemantics
   // declares the per-claim semantics; must be a member of the producer's
   // CapabilitiesResult.WriteSemanticsEnvelope; must be uniform across
   // byte-equal-scope claims (uniformity invariant in spec §2.5).
   type ClaimResult struct {
   	Address                json.RawMessage
   	Payload                json.RawMessage
   	Scope                  json.RawMessage
   	RealizedWriteSemantics WriteSemantics
   }

   // CapabilitiesResult is the response from Capabilities.
   type CapabilitiesResult struct {
   	WriteSemanticsEnvelope []WriteSemantics // permissible values; singleton common
   }
   ```

3. Create `protocols/claimproducer/claimproducer.go`:
   ```go
   package claimproducer

   import (
   	"context"

   	"github.com/google/uuid"
   )

   // ClaimProducer is the Go interface for the ClaimProducer service
   // protocol. See spec §2 for wire shapes and invariants.
   //
   // @blessed-invariant 9b: ClaimProducer implementations MUST NOT
   // internally serialize on lock-shaped predicates. The reader-lease
   // serialization pattern is forbidden for staged_async; honest support
   // requires snapshot delegation or native MVCC pass-through.
   type ClaimProducer interface {
   	Open(ctx context.Context, req OpenRequest) (ClaimResult, error)
   	Commit(ctx context.Context, claimID uuid.UUID) error
   	Abandon(ctx context.Context, claimID uuid.UUID) error
   	Release(ctx context.Context, claimID uuid.UUID) error
   	Capabilities(ctx context.Context) (CapabilitiesResult, error)
   }
   ```

4. Update the `protocols/proto/v1/claim_producer.proto` file (already renamed in Task 11). Currently it defines `service Store`; rename to `service ClaimProducer`. Rename `OpenRequest` (already exists), add `realized_write_semantics` field to `ClaimResult` proto message:
   ```protobuf
   message ClaimResult {
     bytes address = 1;
     bytes payload = 2;
     bytes scope = 3;  // renamed from region in Task 17
     WriteSemantics realized_write_semantics = 4;  // NEW
   }
   message CapabilitiesResult {
     repeated WriteSemantics write_semantics_envelope = 1;  // NEW; replaces single field
   }
   enum WriteSemantics {
     WRITE_SEMANTICS_UNKNOWN = 0;
     WRITE_SEMANTICS_SYNC = 1;
     WRITE_SEMANTICS_STAGED_ASYNC = 2;
     WRITE_SEMANTICS_BLOCKING_ASYNC = 3;
     WRITE_SEMANTICS_READ_ONLY = 4;
   }
   ```

5. Run `make proto-gen` to regenerate.

**Verification:**
```sh
test -f protocols/claimproducer/claimproducer.go
go build ./protocols/...
make proto-gen
grep -q 'service ClaimProducer' protocols/proto/v1/claim_producer.proto
grep -q 'realized_write_semantics' protocols/proto/v1/claim_producer.proto
grep -q 'write_semantics_envelope' protocols/proto/v1/claim_producer.proto
```

### Task 21 — Define new `protocols/lifecycle/` service

**Files:**
- New: `protocols/lifecycle/lifecycle.go`
- New: `protocols/lifecycle/types.go`
- New: `protocols/lifecycle/doc.go`
- Edit: `protocols/proto/v1/claim_producer.proto` — REMOVE the six lifecycle methods from the `ClaimProducer` service.
- New: `protocols/proto/v1/lifecycle.proto` — define `LifecycleSubscriber` service with the six methods.

**Steps:**

1. Create `protocols/proto/v1/lifecycle.proto`:
   ```protobuf
   syntax = "proto3";
   package rimsky.v1;
   option go_package = "github.com/fallguy/rimsky/protocols/proto/v1/gen;rimsky_v1";

   service LifecycleSubscriber {
     rpc OnTemplateRegistered(OnTemplateRegisteredRequest) returns (LifecycleAck);
     rpc OnTemplateDeployed(OnTemplateDeployedRequest) returns (LifecycleAck);
     rpc OnTemplateUndeployed(OnTemplateUndeployedRequest) returns (LifecycleAck);
     rpc OnTemplateDeregistered(OnTemplateDeregisteredRequest) returns (LifecycleAck);
     rpc OnInstanceCreated(OnInstanceCreatedRequest) returns (LifecycleAck);
     rpc OnInstanceTerminated(OnInstanceTerminatedRequest) returns (LifecycleAck);
   }

   message OnTemplateRegisteredRequest {
     string template_hash = 1;
     bytes spec = 2;  // canonical JCS-canonicalized template spec
   }
   message OnTemplateDeployedRequest {
     string template_hash = 1;
     repeated string tags = 2;
   }
   message OnTemplateUndeployedRequest {
     string template_hash = 1;
   }
   message OnTemplateDeregisteredRequest {
     string template_hash = 1;
   }
   message OnInstanceCreatedRequest {
     string instance_id = 1;
     string template_hash = 2;
     string instance_key = 3;  // may be empty
     bytes params = 4;
   }
   message OnInstanceTerminatedRequest {
     string instance_id = 1;
     string template_hash = 2;
     int64 terminated_at_unix_ms = 3;
   }
   message LifecycleAck {
     // empty; reserved for future use
   }
   ```

2. Remove the six methods from `protocols/proto/v1/claim_producer.proto` if present. (They might already be absent from the renamed `claim_producer.proto`; Task 11 only moved the proto file. Some history in `core/store/registry.go` and `core/controlapi/lifecycle.go` may reference them. Confirm: in the current `proto/v1/store_service.proto` (now `claim_producer.proto`), are the lifecycle methods present? If so, remove.)

3. Run `make proto-gen` to generate bindings for the new lifecycle proto.

4. Create `protocols/lifecycle/doc.go`:
   ```go
   // Package lifecycle defines the LifecycleSubscriber service protocol.
   //
   // A LifecycleSubscriber is a service that hooks into Rimsky's
   // control-plane lifecycle events: template state transitions and
   // instance state transitions. See spec §3 for the wire shape.
   //
   // Implementer pattern: return nil from any method the binary doesn't
   // react to. Binaries that don't react to any event simply don't
   // implement the service.
   package lifecycle
   ```

5. Create `protocols/lifecycle/types.go`:
   ```go
   package lifecycle

   import "encoding/json"

   type OnTemplateRegisteredRequest struct {
   	TemplateHash string
   	Spec         json.RawMessage
   }
   type OnTemplateDeployedRequest struct {
   	TemplateHash string
   	Tags         []string
   }
   type OnTemplateUndeployedRequest struct {
   	TemplateHash string
   }
   type OnTemplateDeregisteredRequest struct {
   	TemplateHash string
   }
   type OnInstanceCreatedRequest struct {
   	InstanceID   string
   	TemplateHash string
   	InstanceKey  string
   	Params       json.RawMessage
   }
   type OnInstanceTerminatedRequest struct {
   	InstanceID         string
   	TemplateHash       string
   	TerminatedAtUnixMs int64
   }
   ```

6. Create `protocols/lifecycle/lifecycle.go`:
   ```go
   package lifecycle

   import "context"

   // LifecycleSubscriber is the Go interface for the LifecycleSubscriber
   // service protocol. Implementations return nil from methods they
   // don't react to.
   type LifecycleSubscriber interface {
   	OnTemplateRegistered(ctx context.Context, req OnTemplateRegisteredRequest) error
   	OnTemplateDeployed(ctx context.Context, req OnTemplateDeployedRequest) error
   	OnTemplateUndeployed(ctx context.Context, req OnTemplateUndeployedRequest) error
   	OnTemplateDeregistered(ctx context.Context, req OnTemplateDeregisteredRequest) error
   	OnInstanceCreated(ctx context.Context, req OnInstanceCreatedRequest) error
   	OnInstanceTerminated(ctx context.Context, req OnInstanceTerminatedRequest) error
   }
   ```

**Verification:**
```sh
test -f protocols/lifecycle/lifecycle.go
test -f protocols/proto/v1/lifecycle.proto
make proto-gen
go build ./protocols/...
grep -q 'service LifecycleSubscriber' protocols/proto/v1/lifecycle.proto
! grep -q 'OnTemplateRegistered' protocols/proto/v1/claim_producer.proto  # not in claimproducer anymore
```

### Task 22 — Define new `protocols/executor/` interface (mostly rename)

**Files:**
- New: `protocols/executor/executor.go`
- New: `protocols/executor/types.go`
- New: `protocols/executor/doc.go`

**Steps:**

1. Create `protocols/executor/doc.go` describing the package.

2. Create `protocols/executor/types.go` with `ExecuteRequest`, `ExecuteResponse`, terminal-outcome types, etc. — mirror the proto messages in `protocols/proto/v1/executor.proto`.

3. Create `protocols/executor/executor.go` with the `Executor` Go interface (gRPC + HTTP+JSON bridge — match current `core/executor/` interface shape).

4. Update `protocols/proto/v1/executor.proto` if needed: confirm `Capabilities` includes `http_bridge_url` field.

**Verification:**
```sh
test -f protocols/executor/executor.go
go build ./protocols/...
```

### Task 23 — Update foundation/integration to call ClaimProducer (renamed from Store)

**Files:**
- Edit: `foundation/integration/conductor.go`
- Edit: `foundation/integration/remote/*.go` (the gRPC client; rename internal types as needed)
- Edit: `foundation/integration/runner_acquire.go` (or wherever acquisition lives)
- Edit: `foundation/integration/auto_terminal.go`

**Steps:**

1. Update foundation/integration/ to import `github.com/fallguy/rimsky/protocols/claimproducer` instead of the old `core/store` interface.

2. Rename Go-level uses: `Store` → `ClaimProducer`, `store.Store` → `claimproducer.ClaimProducer`, etc.

3. Update remote/ gRPC client to use the new generated bindings.

4. Update `Open` call sites to read `RealizedWriteSemantics` from `ClaimResult` and store it in the foundation lock-holder row (will be `rimsky_claim_handle.realized_write_semantics` post-Phase-5; for now, store in `rimsky_lock_holders` if the column exists or add it).

**Verification:**
```sh
go build ./foundation/...
! grep -rn '\bStore\b' foundation/integration/ --include='*.go'  # should be zero (renamed to ClaimProducer)
```

### Task 24 — Rename `Store` interface and types in bundled stores: filesystem

**Files:**
- Edit: `stores/filesystem/store/store.go`
- Edit: `stores/filesystem/server/server.go`
- Edit: `stores/filesystem/cmd/main.go`
- Edit: `stores/filesystem/config-example.yml`

**Steps:**

1. In `stores/filesystem/store/store.go`, rename the implementation from `Store` to a name that's clearer in context. The file lives under `stores/filesystem/store/` so the package name is `store` and the type can stay `Store` (it's still a "store" colloquially per the layered rename — services-layer term survives). The protocol-level interface it implements is now `ClaimProducer`. Update the type's documentation comment to say "filesystem-backed ClaimProducer implementation."

2. Update method signatures: the methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`) match the `ClaimProducer` interface — verify.

3. In `stores/filesystem/server/server.go`, the gRPC server registration switches from `proto.RegisterStoreServer` to `proto.RegisterClaimProducerServer` (the new name post-Task 17 proto rename). Apply.

4. Add the new `Capabilities` shape: return `WriteSemanticsEnvelope` (a list, even if singleton). The filesystem store typically returns `[ReadOnly, Sync]` or similar — check current behavior.

5. Update `Open` to return `RealizedWriteSemantics` per claim. For filesystem, the semantic is determined by the pick policy or the open mode; pick the right value per claim. Add tests covering the uniformity invariant: two `Open` calls returning byte-equal `scope` MUST return identical `RealizedWriteSemantics`.

6. Update `stores/filesystem/cmd/main.go` to wire the renamed gRPC server.

7. Update `stores/filesystem/config-example.yml` if it shows old YAML key names.

**Verification:**
```sh
go build ./stores/filesystem/...
go test ./stores/filesystem/... -count=1
```

### Task 25 — Rename for postgres store

**Files:**
- Edit: `stores/postgres/store/store.go`
- Edit: `stores/postgres/server/server.go`
- Edit: `stores/postgres/cmd/main.go`
- Edit: `stores/postgres/config-example.yml` (if exists)

**Steps:** Mirror Task 24 for the postgres store. The `pgsstore` package's `Store` type can keep that name; the gRPC service registration changes.

Update `Capabilities` to return `WriteSemanticsEnvelope`. Postgres store's items-table queue semantics typically declares `staged_async`; declare singleton envelope `[StagedAsync]` unless multiple modes are supported.

Update `Open` to return `RealizedWriteSemantics` per claim — for an items-queue producer, all claims have the same semantics (singleton envelope means no per-claim variation), so the return value is just the single declared value.

**Verification:**
```sh
go build ./stores/postgres/...
go test ./stores/postgres/... -count=1
```

### Task 26 — Rename for stub store

**Files:**
- Edit: `stores/stub/server/server.go`
- Edit: `stores/stub/server/observability.go`
- Edit: `stores/stub/server/observability_test.go`

**Steps:** Mirror Tasks 24-25 for the stub store. The stub typically has minimal logic; rename gRPC service registration; declare a fixed envelope (e.g., `[Sync]`).

**Verification:**
```sh
go build ./stores/stub/...
go test ./stores/stub/... -count=1
```

### Task 27 — Implement LifecycleSubscriber in bundled stores

**Files:**
- Edit: `stores/filesystem/server/server.go` — add LifecycleSubscriber server registration and method stubs.
- Edit: `stores/postgres/server/server.go` — same.
- Edit: `stores/stub/server/server.go` — same (typically no-op stubs returning nil).

**Steps:**

1. For each store binary, conditionally register a `LifecycleSubscriber` gRPC server based on whether the binary's config declares it implements `lifecycle_subscriber`. (The default for `stores/*` is `claim_producer` only; add a CLI flag or config field to opt in.)

2. For stores that need to react to lifecycle events (e.g., postgres store needs to bootstrap a schema on `OnTemplateDeployed`), implement the relevant methods. For now, default all six to no-op `return nil` and let operators add reactions per-store as needed.

3. Move the existing lifecycle implementation code (currently embedded in the `Store` impl as the six lifecycle methods) into a new file `stores/<kind>/lifecycle/lifecycle.go` that implements `protocols/lifecycle/LifecycleSubscriber`. Wire it up in the gRPC server registration.

**Verification:**
```sh
go build ./stores/...
go test ./stores/... -count=1
# Each store binary can be started with --enable-lifecycle and the LifecycleSubscriber gRPC service is registered.
```

### Task 28 — Update YAML config schema and parser to Option II

**Files:**
- Edit: `modeling/config/parser.go` (or wherever rimsky.yml is parsed)
- Edit: `modeling/config/types.go` (struct definitions)
- Edit: `deploy/rimsky.yml` (reference config)
- Edit: `deploy/store-filesystem.yml`, `deploy/store-postgres.yml` (per-store reference configs)
- Edit: `deploy/docker-compose.yml` if it has inline rimsky.yml-shaped content

**Steps:**

1. Modify the `rimsky.yml` parser:
   - Block `stores:` is now `claim_producers:`.
   - Each entry under `claim_producers:` and `executors:` gains optional `protocols: [...]` field. Default for `claim_producers:` is `[claim_producer]`; default for `executors:` is `[executor]`. Implementer-allowed values: `claim_producer`, `executor`, `lifecycle_subscriber`.
   - Each entry under `claim_producers:` gains required `write_semantics_envelope: [...]` field listing operator-declared permissible WriteSemantics values. Validation: must be a subset of producer-declared envelope returned by `Capabilities()`.
   - The previous `write_semantics: <single-value>` field is removed; conversion: a single value becomes a singleton envelope.

2. Update `deploy/rimsky.yml`:
   ```yaml
   persistence:
     driver: postgres
     postgres:
       url: postgresql://...

   claim_producers:
     - name: items-pg
       endpoint: store-postgres:7001
       protocols: [claim_producer, lifecycle_subscriber]
       write_semantics_envelope: [staged_async]

     - name: blob-fs
       endpoint: store-filesystem:7002
       protocols: [claim_producer]
       write_semantics_envelope: [sync, read_only]

   executors:
     - name: claude-agent
       endpoint: claude-agent:7100
       protocols: [executor]

     - name: http-node
       endpoint: http-node:7101
       protocols: [executor]

   named_locks:
     - name: api-rate-limit
       mode: counting
       capacity: 5
   ```

3. Update startup validation: control-api / supervisor / scheduler all probe `Capabilities()` per protocol per peer; equality-check `write_semantics_envelope` ⊆ producer-declared; fail fast on mismatch.

4. Update the per-store config files (`deploy/store-postgres.yml`, etc.) to use new YAML key names where applicable. These are the store-side configs (not the top-level rimsky.yml), so changes are minimal — mostly just the items-table or filesystem-path config they already had.

**Verification:**
```sh
go build ./modeling/config/...
go test ./modeling/config/... -count=1
```

If `modeling/config/` has no test that loads `deploy/rimsky.yml` end-to-end, add one as part of this task: write `modeling/config/parser_deploy_yaml_test.go` that calls the parser on `deploy/rimsky.yml` and asserts the parsed shape matches the new schema (top-level keys present; each `claim_producers:` entry has `protocols:` and `write_semantics_envelope:`). The test gates the verification.

### Task 29 — Update conformance binaries: rename and split

**Files:**
- Move: `cmd/rimsky-store-conformance/` → `cmd/rimsky-claim-producer-conformance/` (rename the binary directory and Go files inside).
- Edit: `cmd/rimsky-conformance/main.go` — restructure to support `--check-executor`, `--check-lifecycle`, and reorganize per the new protocols.
- New: under `cmd/rimsky-conformance/` add a `lifecycle_check.go` file implementing the LifecycleSubscriber conformance.
- New: under `cmd/rimsky-claim-producer-conformance/` add per-claim WriteSemantics conformance: envelope conformance, uniformity-per-(producer,scope).

**Steps:**

1. Rename the binary directory:
   ```sh
   git mv cmd/rimsky-store-conformance cmd/rimsky-claim-producer-conformance
   ```

2. Update `cmd/rimsky-claim-producer-conformance/main.go`:
   - Change binary name in usage strings.
   - Update flag descriptions.
   - Add envelope-conformance check: drives `Capabilities()`, then drives `Open()`, asserts that every returned `RealizedWriteSemantics` is a member of the envelope.
   - Add uniformity-per-(producer,scope) check: drives `Open()` twice with payloads/specs that produce byte-equal scope; asserts identical `RealizedWriteSemantics` returned.

3. Update `cmd/rimsky-conformance/main.go`:
   - Add `--check-lifecycle` flag.
   - Add `cmd/rimsky-conformance/lifecycle_check.go` that drives a stub-mode dispatch through control-api, then asserts the LifecycleSubscriber receives expected events with idempotency-key uniqueness.

4. Update Makefile / Dockerfile binary references that mention `rimsky-store-conformance`.

5. Update CHANGELOG entry on the conformance changes (rolled into the Phase 4 entry — see Task 33).

**Verification:**
```sh
go build ./cmd/rimsky-conformance/... ./cmd/rimsky-claim-producer-conformance/...
test -d cmd/rimsky-claim-producer-conformance && test ! -d cmd/rimsky-store-conformance
go test ./cmd/rimsky-conformance/... ./cmd/rimsky-claim-producer-conformance/... -count=1
```

The conformance binaries should have unit tests that run the conformance logic against the bundled stub services using the in-process `testfixture.Start` pattern (per CLAUDE.md: `stores/<kind>/testfixture.Start` exists for the loopback gRPC fixture). If such tests do not yet exist, add them as part of this task — the existing scenario suite provides patterns to copy. Do NOT verify by spinning up real services on hostnames the agent cannot reach.

### Task 30 — Update bundled executors for ClaimProducer rename and proto changes

**Files:**
- Edit: `executors/http-node/server.go` and related Go files
- Edit: `executors/stub/stub.go`
- Edit: `executors/claude-agent/src/main.ts`, `executors/claude-agent/src/server.ts`, related TS files

**Steps:**

1. Update Go executors (`http-node`, `stub`):
   - Update gRPC client imports if they call out to ClaimProducers (most executors don't, but the test fixture might).
   - Update proto bindings imports to the new `protocols/proto/v1/gen` path (already done in Task 11; verify here).

2. Update TS executor (`claude-agent`):
   - Regenerate or update TS proto bindings to match `claim_producer.proto` and `lifecycle.proto`.
   - Update gRPC service references in TS code from `Store` to `ClaimProducer` if any.
   - The async-callback path is a Rimsky concern; verify the executor still POSTs to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type`.

3. Update tests in each executor.

**Verification:**
```sh
go build ./executors/http-node/... ./executors/stub/...
go test ./executors/http-node/... ./executors/stub/... -count=1

cd executors/claude-agent
npm install
npm test
npm run build
cd -
```

### Task 31 — Rename `rimsky_store_lifecycle` → `rimsky_lifecycle_idempotency`

**Files:**
- Edit: a migration file (or create a new one) under `modeling/persistence/migrations/` that drops `rimsky_store_lifecycle` and creates `rimsky_lifecycle_idempotency` (pre-v1 break-freely).
- Edit: `modeling/persistence/postgres/store_lifecycle.go` (or whatever the modeling postgres impl file is) — rename file to `lifecycle_idempotency.go`, update SQL.
- Edit: `modeling/persistence/sqlite/store_lifecycle.go` — same treatment.
- Edit: `modeling/controlapi/lifecycle.go` and `modeling/controlapi/instance_terminator.go` and any other code that references the table name.

**Steps:**

1. Rename the file:
   ```sh
   git mv modeling/persistence/postgres/store_lifecycle.go modeling/persistence/postgres/lifecycle_idempotency.go
   git mv modeling/persistence/sqlite/store_lifecycle.go modeling/persistence/sqlite/lifecycle_idempotency.go
   ```

2. In each file, rename:
   - SQL strings: `rimsky_store_lifecycle` → `rimsky_lifecycle_idempotency`.
   - Function names: `StoreLifecycle*` → `LifecycleIdempotency*`.
   - Struct types accordingly.

3. Update the migration: pre-v1 break-freely permits adding a migration that drops the old table and creates the new. Or rewrite the canonical CREATE migration in place.

4. Update all callers (greppable):
   ```sh
   grep -rn 'rimsky_store_lifecycle\|StoreLifecycle' . --include='*.go' --exclude-dir=docs/history
   ```
   Apply renames.

**Verification:**
```sh
go build ./...
go test ./modeling/... -count=1
! grep -rn 'rimsky_store_lifecycle' . --include='*.go' --include='*.sql' --exclude-dir=docs/history
```

### Task 32 — Add LifecycleSubscriber wire-up in control-api

**Files:**
- Edit: `modeling/controlapi/lifecycle.go`
- Edit: `modeling/controlapi/instance_terminator.go`
- Edit: `modeling/controlapi/app.go` — startup probing of LifecycleSubscriber peers.
- Edit: `modeling/config/parser.go` — read `protocols:` field per peer.

**Steps:**

1. In control-api startup, iterate the parsed `claim_producers:` and `executors:` blocks; for any peer whose `protocols:` list contains `lifecycle_subscriber`, dial the peer's endpoint and probe the LifecycleSubscriber `Capabilities()` (or just verify it speaks the gRPC service via reflection).

2. Maintain a list of subscribed peers internally. When a lifecycle event fires (template registered/deployed/etc.), iterate the subscribed peers and RPC each. Idempotency tracked per-peer in `rimsky_lifecycle_idempotency`.

3. Update `instance_terminator.go` to fire `OnInstanceTerminated` via the LifecycleSubscriber service (currently fires via the bundled `Store.OnInstanceTerminated` method; switch to the new protocol).

4. Failure handling: per existing retry policy (retry on transient errors; record failure on permanent errors).

**Verification:**
```sh
go build ./modeling/controlapi/...
go test ./modeling/controlapi/... -count=1
```

### Task 33 — Update CHANGELOG with Phase 4 entry

**Files:**
- Edit: `CHANGELOG.md`

**Steps:**

1. Add CHANGELOG entry:
   ```markdown
   ### Refactor — Layer crystallization Phase 4: ClaimProducer rename + LifecycleSubscriber split + write-semantics envelope

   - **`Store` interface renamed to `ClaimProducer`** at the protocol layer.
     `protocols/claimproducer/` carries the Go interface; `service Store`
     in proto becomes `service ClaimProducer`. Bundled-services-layer term
     "store" survives for data-backed colloquial (filesystem store,
     postgres store, stub store).
   - **`LifecycleSubscriber` extracted as its own service** in
     `protocols/lifecycle/`. Six methods (`OnTemplateRegistered/Deployed/
     Undeployed/Deregistered`, `OnInstanceCreated/Terminated`) moved out
     of the bundled-into-Store pattern. Implementers return nil from
     methods they don't react to. Binaries declare which protocols they
     implement via `protocols:` field per peer in `rimsky.yml`.
   - **Write-semantics envelope added.** `Capabilities()` returns
     `WriteSemanticsEnvelope` (set of permissible values); `Open` returns
     `RealizedWriteSemantics` per claim. Operator declares
     `write_semantics_envelope: [...]` per peer in YAML; startup validation
     enforces operator envelope ⊆ producer envelope. Uniformity invariant:
     two `Open` calls returning byte-equal `scope` MUST return identical
     `RealizedWriteSemantics`.
   - **Conformance suites split.** `rimsky-store-conformance` renamed
     `rimsky-claim-producer-conformance`. `rimsky-conformance` covers
     executor + lifecycle (new `--check-lifecycle` mode). New per-claim
     WriteSemantics conformance (envelope + uniformity).
   - **YAML config shape updated** to Option II: `stores:` block renamed
     `claim_producers:`; entries gain optional `protocols:` list (defaults
     to a single-element list matching the block name); singular
     `write_semantics:` field replaced by required
     `write_semantics_envelope:` set.
   - **`rimsky_store_lifecycle` table renamed `rimsky_lifecycle_idempotency`**
     for accuracy; pre-v1 dev-DB-nuke applies.
   ```

**Verification:**
```sh
grep -q 'Layer crystallization Phase 4' CHANGELOG.md
go build ./... && go test ./... -count=1 && make lint
cd executors/claude-agent && npm test && npm run build && cd -
```

### Task 34 — Schema migration: drop `rimsky_dispatch` + `rimsky_lock_holders`, create `rimsky_worker_request` + `rimsky_claim_handle`

**Files:**
- New: a migration file under `foundation/persistence/migrations/` (e.g., `NNN_worker_request_consolidation.sql`) — naming convention follows the existing migrations.
- Edit: existing `rimsky_dispatch` and `rimsky_lock_holders` migrations may be rewritten in place under pre-v1 break-freely OR dropped and replaced by the new migration.

**Decision:** Rewrite in place. Pre-v1 break-freely lets us produce a clean migration history with one migration that creates the consolidated tables, dropping the previous separate ones. The implementer's call: in-place vs successor migration. Default — successor migration that drops old + creates new.

**Steps:**

1. Create the new migration `NNN_worker_request_consolidation.sql`:
   ```sql
   -- Drop legacy split structure.
   DROP TABLE IF EXISTS rimsky_lock_holders CASCADE;
   DROP TABLE IF EXISTS rimsky_dispatch CASCADE;

   -- Consolidated worker-request table.
   CREATE TABLE rimsky_worker_request (
     id UUID PRIMARY KEY,
     node_id UUID NOT NULL,                  -- FK to rimsky_nodes (modeling)
     frame_id UUID NOT NULL,                 -- FK to rimsky_frames (modeling)
     claimed_by TEXT,                        -- NULL = unclaimed; supervisor_id = active claim
     heartbeat_at TIMESTAMPTZ,
     phase TEXT NOT NULL,                    -- 'pending' | 'active' | 'held' | 'completed'
     active_terminal_at TIMESTAMPTZ,         -- when active phase ended
     created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_rimsky_worker_request_claimed_by ON rimsky_worker_request(claimed_by);
   CREATE INDEX idx_rimsky_worker_request_phase ON rimsky_worker_request(phase);
   CREATE INDEX idx_rimsky_worker_request_frame ON rimsky_worker_request(frame_id);

   -- Claim handles, FK-cascade child of worker-request.
   CREATE TABLE rimsky_claim_handle (
     id UUID PRIMARY KEY,
     worker_request_id UUID NOT NULL REFERENCES rimsky_worker_request(id) ON DELETE CASCADE,
     holder TEXT NOT NULL,                   -- supervisor_id
     scope_data BYTEA NOT NULL,              -- canonicalized scope bytes (renamed from region_data in Phase 3)
     address JSONB,
     payload JSONB,
     purpose TEXT NOT NULL,                  -- producer-name / acquisition path
     realized_write_semantics TEXT NOT NULL,
     is_held BOOLEAN NOT NULL,               -- true = persists into held phase
     created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   CREATE INDEX idx_rimsky_claim_handle_worker_request ON rimsky_claim_handle(worker_request_id);
   CREATE INDEX idx_rimsky_claim_handle_scope ON rimsky_claim_handle USING hash (scope_data);
   CREATE INDEX idx_rimsky_claim_handle_holder ON rimsky_claim_handle(holder);
   ```

2. Update modeling-layer FK references:
   - `rimsky_claim_holders` (modeling-side held-claim tracking? — verify) currently has `lock_holder_id UUID REFERENCES rimsky_lock_holders(id)`. Update to `claim_handle_id UUID REFERENCES rimsky_claim_handle(id)`. Add a migration that renames the column and updates the FK.
   - Any other modeling tables referencing `rimsky_dispatch.id` — update to `rimsky_worker_request.id`.

3. SQLite equivalent: write the same migration in SQLite syntax under `foundation/persistence/sqlite/migrations/`. Note SQLite doesn't support `BYTEA` (use `BLOB`), `JSONB` (use `TEXT` or `BLOB`), or `CASCADE` on FK in the same way (set `PRAGMA foreign_keys = ON;` and use `ON DELETE CASCADE`).

**Verification:**
```sh
go build ./foundation/persistence/... ./modeling/persistence/...
go test ./foundation/persistence/... ./modeling/persistence/... -count=1
```

Migration verification runs via the existing testcontainers-go infrastructure under `foundation/internal/pgtest/` (or wherever the postgres test helper landed in Task 13e). If no test currently exercises a fresh-DB migration end-to-end, add one as part of this task: `foundation/persistence/postgres/migration_test.go` that spins up a Postgres testcontainer, runs all migrations, and asserts the post-migration schema includes `rimsky_worker_request` and `rimsky_claim_handle` (via `pg_catalog` queries) and does NOT include `rimsky_dispatch` or `rimsky_lock_holders`. The same test in `foundation/persistence/sqlite/migration_test.go` for SQLite. Do NOT verify by reaching out to a real Postgres on `$TEST_DB_URL`.

### Task 35 — Update `WorkerRequests` driver interface (collapse Locks)

**Files:**
- Edit: `foundation/persistence/driver.go` (or wherever interfaces are declared)
- Edit: `foundation/persistence/postgres/queue.go` and `lock_holders.go` (consolidate)
- Edit: `foundation/persistence/sqlite/queue.go` and `lock_holders.go` (consolidate)

**Steps:**

1. In `foundation/persistence/driver.go` (or types.go, wherever the interface set is declared), define the consolidated `WorkerRequests` interface per spec §8.4:

   ```go
   type WorkerRequests interface {
       Insert(ctx context.Context, spec WorkerRequestSpec) (uuid.UUID, error)
       Claim(ctx context.Context, id uuid.UUID, supervisorID string) error
       Heartbeat(ctx context.Context, id uuid.UUID, supervisorID string) error
       EnterHeldPhase(ctx context.Context, id uuid.UUID) error
       Complete(ctx context.Context, id uuid.UUID, supervisorID string) error
       ReapOrphans(ctx context.Context, cutoff time.Duration) ([]uuid.UUID, error)

       InsertClaimHandles(ctx context.Context, workerRequestID uuid.UUID, handles []ClaimHandleSpec) error
       ReleaseClaimHandle(ctx context.Context, handleID uuid.UUID, holder string) error
       ListHandlesForResolution(ctx context.Context, workerRequestID uuid.UUID) ([]ClaimHandle, error)
   }
   ```

2. Remove the `Locks` interface entirely.

3. Update `foundation/persistence/postgres/` to implement the new consolidated interface. Combine the previous `queue.go` + `lock_holders.go` logic into one file (`worker_requests.go`) or keep two files in the same package, both implementing the `WorkerRequests` interface methods. Recommended: single file `worker_requests.go` for cohesion (~200-400 lines OK; cold-read guideline is ~500 lines).

4. Update SQLite same way.

5. Update all callers in `foundation/integration/` (the conductor, runner_acquire, auto_terminal) to use the consolidated interface.

**Verification:**
```sh
go build ./foundation/...
go test ./foundation/... -count=1
! grep -rn '\bLocks\b' foundation/persistence/ --include='*.go'  # interface gone
grep -q 'WorkerRequests' foundation/persistence/driver.go
```

### Task 36 — Update integration code for active-phase + held-phase lifecycle

**Files:**
- Edit: `foundation/integration/runner_acquire.go` (acquisition tx)
- Edit: `foundation/integration/auto_terminal.go` (auto-terminal resolution)
- Edit: `foundation/integration/conductor.go` (orphan reaper)

**Steps:**

1. **Acquisition** (`runner_acquire.go`): the atomic transaction now creates a `rimsky_worker_request` row with `phase='pending'`, claims it (`phase='active'`, `claimed_by=supervisor_id`), inserts claim handles via `InsertClaimHandles` (which call `Open` per producer between INSERT and COMMIT to populate `address`/`payload`/`realized_write_semantics`), and commits. Foundation invariant 10 (atomicity) and 15 (Open inside acquisition tx) preserved.

2. **Executor terminal handling**: when an executor terminal arrives (success or failure), the conductor:
   - Calls `WorkerRequests.Complete` if the worker-request has NO held claims (all claim-handle rows have `is_held=false`). This deletes the worker-request row and cascades the claim-handle rows; for each claim, the appropriate verb (`Commit` on success, `Abandon`/`Release` on failure) is called on the producer before deletion.
   - Calls `WorkerRequests.EnterHeldPhase` if the worker-request has any `is_held=true` claim handles. This advances `phase` to `held` and clears `claimed_by`. The held claim handles persist for auto-terminal.

3. **Auto-terminal** (`auto_terminal.go`): on every tick, the conductor calls `WorkerRequests.ListHandlesForResolution` for each `phase='held'` worker-request, applies the holding-subgraph completion predicate (modeling-supplied), and if complete, computes aggregate outcome and fires the appropriate verb on the producer for each held claim, then completes the worker-request (`phase='completed'`, deletion via cascade).

4. **Orphan reaper** (`conductor.go`): `WorkerRequests.ReapOrphans(cutoff=5*heartbeat_interval)` reaps:
   - `phase='active'` worker-requests with stale heartbeat → revert to `phase='pending'`, clear `claimed_by`. The claim handles persist (uncommitted in producer; producer's TTL handles cleanup).
   - `phase='held'` worker-requests don't have heartbeats — they're not reaped on the worker-request side; held-claim cleanup via the auto-terminal mechanism above.

5. The integration code's correctness against foundation invariants 3, 4, 5, 6, 10, 13, 15 is verified by the scenario tests. The substantive new tests covering active-phase + held-phase lifecycles are added in Task 38 — that's where invariant coverage for the consolidated schema lands. This task's verification covers compile + existing scenarios still passing.

**Verification:**
```sh
go build ./foundation/...
go test ./foundation/... -count=1
go test ./test/scenarios/... -count=1  # existing scenarios still pass; Task 38 adds new coverage
```

### Task 37 — Update modeling-layer references to repointed FKs

**Files:**
- Edit: `modeling/persistence/postgres/nodes.go` (or wherever node-state queries live).
- Edit: `modeling/persistence/postgres/instances.go`, etc.
- Edit: `modeling/scheduler/invalidate.go` and frame-related code.

**Steps:**

1. Find all SQL references to `rimsky_dispatch` and `rimsky_lock_holders` in modeling code:
   ```sh
   grep -rn 'rimsky_dispatch\|rimsky_lock_holders' modeling/ --include='*.go' --include='*.sql'
   ```

2. Update each to use `rimsky_worker_request` and `rimsky_claim_handle` respectively. Where applicable, join through the new schema (e.g., a query that previously joined dispatch and lock_holders by `dispatch_id` now joins worker_request and claim_handle by `worker_request_id`).

3. Update modeling-layer code that examines worker-request phase: e.g., the frame engine's frame-end SQL predicate ("no rimsky_nodes rows in state stale or running") now needs to know that "running" corresponds to `worker_request.phase='active'`.

**Verification:**
```sh
go build ./...
go test ./... -count=1
go test ./test/scenarios/... -count=1
```

### Task 38 — Add held-claim sub-design tests in `test/scenarios/locks/`

**Files:**
- Edit / new: `test/scenarios/locks/active_only_test.go` (active-phase-only lifecycle)
- New: `test/scenarios/locks/active_held_test.go` (active+held lifecycle)
- New: `test/scenarios/locks/orphan_active_test.go` (orphan reap during active phase)
- New: `test/scenarios/locks/orphan_held_test.go` (orphan reap during held phase — worker-request not reaped, claim handles persist; auto-terminal still fires)

**Steps:**

1. The `test/scenarios/locks/` directory currently contains compile-passing placeholders per the v3 cutover. Replace them with substantive scenarios that drive a worker-request through its full lifecycle:

2. `active_only_test.go`: drive a worker-request that acquires non-held claims, executes, terminates, asserts: (a) Commit fired on success, (b) claim-handle rows deleted, (c) worker-request row deleted, (d) no orphans.

3. `active_held_test.go`: drive a worker-request that acquires held claims, terminates, asserts: (a) worker-request enters `phase='held'`, (b) claim handles persist, (c) when the holding subgraph completes, auto-terminal fires Commit, (d) claim handles deleted, (e) worker-request row deleted.

4. `orphan_active_test.go`: simulate a supervisor crash during active phase (kill the supervisor, wait > 5×heartbeat, verify reaper resets `phase='pending'` and clears `claimed_by`).

5. `orphan_held_test.go`: simulate a crash during held phase (the worker-request is in `phase='held'` with `claimed_by=NULL`; auto-terminal should still fire correctly without worker-request-side reaping).

6. All tests use the existing testcontainers-go infrastructure (`foundation/persistence/postgres/...` test helpers, possibly renamed from `core/internal/pgtest`).

**Verification:**
```sh
go test ./test/scenarios/locks/... -count=1
```

### Task 39 — Update CHANGELOG with Phase 5 entry

**Files:**
- Edit: `CHANGELOG.md`

**Steps:**

1. Add CHANGELOG entry:
   ```markdown
   ### Refactor — Layer crystallization Phase 5: worker-request consolidation

   - **`rimsky_dispatch` and `rimsky_lock_holders` consolidated** into
     `rimsky_worker_request` and `rimsky_claim_handle`. Worker-request
     lifecycle has up to two phases: active (work running) and held
     (claims persist past work terminal until holding subgraph completes).
     `phase` column on the parent table; `is_held` column on the child
     table; FK-cascade delete on parent removal. Pre-v1 dev-DB-nuke
     applies.
   - **`Locks` driver interface collapsed into `WorkerRequests`.** The
     foundation persistence contract now publishes three driver
     interfaces: `Cascade`, `WorkerRequests`, `AdvisoryLocker`.
   - **Active-phase + held-phase lifecycle wired** in
     `foundation/integration/`. Acquisition tx, executor terminal, and
     auto-terminal all operate on the consolidated schema. Foundation
     invariants 3, 4, 5, 6, 10, 13, 15 all preserved.
   - **`test/scenarios/locks/` populated** with active-only,
     active-held, orphan-during-active, and orphan-during-held scenarios.
   ```

**Verification:**
```sh
grep -q 'Layer crystallization Phase 5' CHANGELOG.md
go build ./... && go test ./... -count=1 && make lint
go test ./test/scenarios/... -count=3 -race  # flake hunt the consolidated path
```

### Task 40 — Unify orphan reaper into single mechanism

**Files:**
- Edit: `foundation/integration/orphan_reaper.go`
- Edit: `foundation/integration/conductor.go`

**Steps:**

1. The current code has two reapers historically: one for `rimsky_dispatch` and one for `rimsky_lock_holders`. Post-Task 35/36 these are already unified at the table level (claim handles cascade-delete with worker-request, so reaping just the parent suffices for the active-phase case).

2. Confirm the unified reaper is in `foundation/integration/orphan_reaper.go` with a single method that:
   - Selects `phase IN ('active')` worker-requests with stale heartbeat (not seen in > `5 × heartbeat_interval`).
   - Per row, atomically: `UPDATE rimsky_worker_request SET phase='pending', claimed_by=NULL WHERE id = $1 AND claimed_by = $2` (claimant-guarded; foundation invariant 4).
   - Returns the list of reaped IDs for logging/metrics.

3. Held-phase rows: NOT reaped at the worker-request level. Auto-terminal handles their resolution; their claim-handle children have their own TTL via the producer's own state cleanup (foundation contract §4.5).

4. Remove any `// TODO: unify with the other reaper` comments that may exist.

**Verification:**
```sh
go build ./foundation/...
go test ./foundation/... ./test/scenarios/... -count=1
! grep -rn 'TODO: unify with the other reaper' foundation/ --include='*.go'
# Behavioral equivalence: scenario tests covering orphan reap should still pass with same outcomes.
```

### Task 41 — Unify terminal-decision into single engine

**Files:**
- Edit: `foundation/integration/terminal_decision.go` (new — extracted from auto_terminal.go and the executor-terminal handler in supervisor)
- Edit: `foundation/integration/auto_terminal.go` (slim to call the unified engine)
- Edit: wherever ApplyTerminalOutcome currently lives (likely `foundation/integration/runner.go` — slim to call the unified engine)

**Steps:**

1. The two pre-Phase-6 mechanisms are:
   - Auto-terminal: in `foundation/integration/auto_terminal.go`, function `CheckAndFireResolution` (or whatever the post-Task-10 name is) — fires Commit/Abandon based on aggregate outcome of held-claim subgraph completion.
   - Executor-terminal: the supervisor's terminal handler that processes the executor's terminal response — applies node-state transition, fires Commit/Abandon for non-held claims, deletes worker-request.

   Both share core sub-steps: SELECT FOR UPDATE; iterate claim handles; fire producer verb per is_held + outcome; delete worker-request (cascades claim handles); apply node-state transition; emit cascade signal.

2. Define the unified engine in `foundation/integration/terminal_decision.go`:
   ```go
   package integration

   import (
       "context"

       "github.com/google/uuid"
       "github.com/fallguy/rimsky/foundation/persistence"
       "github.com/fallguy/rimsky/protocols/claimproducer"
   )

   // Engine resolves worker-request terminals. One method covers both
   // the active-phase executor terminal and the held-phase auto-terminal
   // — parameterized by source.
   type Engine struct {
       persist   persistence.WorkerRequests
       producers ProducerRegistry  // name → claimproducer.ClaimProducer client
       cascade   CascadeApplier    // applies node-state transitions + cascade signals
   }

   func NewEngine(p persistence.WorkerRequests, prods ProducerRegistry, c CascadeApplier) *Engine {
       return &Engine{persist: p, producers: prods, cascade: c}
   }

   type TerminalDecision struct {
       WorkerRequestID uuid.UUID
       SupervisorID    string         // claimant guard for invariant 4
       Source          TerminalSource // ActiveTerminal | HeldTerminal
       Outcome         AggregateOutcome
       NodeOutcome     NodeOutcome    // node-state transition to apply
       CascadeTargets  []uuid.UUID    // for invalidate cascade
   }

   type TerminalSource int
   const (
       ActiveTerminal TerminalSource = iota
       HeldTerminal
   )

   type AggregateOutcome int
   const (
       Commit AggregateOutcome = iota
       Abandon
   )

   func (e *Engine) ResolveTerminal(ctx context.Context, td TerminalDecision) error {
       // See spec §8.4 and foundation contract §5.5 for the steps.
       // Brief summary:
       //   1. SELECT FOR UPDATE on rimsky_worker_request by ID; verify claimed_by matches td.SupervisorID for ActiveTerminal.
       //   2. Call e.persist.ListHandlesForResolution(td.WorkerRequestID) — returns []ClaimHandle.
       //   3. For each handle: e.producers[handle.PurposeProducer].Commit/Abandon based on outcome and is_held.
       //   4. Delete worker-request row (cascades child claim handles).
       //   5. e.cascade.Apply(td.NodeOutcome, td.CascadeTargets) — node-state transition + invalidate cascade.
       // Claimant-guarded throughout (foundation invariant 4); single-fire (invariant 13).
   }
   ```

   ProducerRegistry, CascadeApplier, and NodeOutcome are existing types or are introduced as part of this task — implementer chooses naming consistent with surrounding code.

3. Update `auto_terminal.go::CheckAndFireResolution` to: detect held-subgraph completion → construct `TerminalDecision{Source: HeldTerminal, ...}` → call `Engine.ResolveTerminal`. The function now contains the detection logic only; resolution is delegated.

4. Update the executor-terminal handler (in `foundation/integration/runner.go` or wherever it lives post-Task-10) similarly: parse executor terminal → construct `TerminalDecision{Source: ActiveTerminal, ...}` → call `Engine.ResolveTerminal`.

5. The two old direct-resolve code paths can now be deleted. Their tests stay; they exercise the unified engine through the same call surface.

6. Verify behavioral equivalence: scenarios that previously exercised auto-terminal or executor-terminal still pass with identical outcomes through the unified engine. The existing scenario suite + the held-claim tests added in Task 38 are the gate.

**Verification:**
```sh
go build ./foundation/...
go test ./foundation/... ./test/scenarios/... -count=3
# Behavioral equivalence: every scenario test that previously exercised auto-terminal or executor-terminal still passes; outcomes identical.
```

### Task 42 — Update CHANGELOG with Phase 6 entry

**Files:**
- Edit: `CHANGELOG.md`

**Steps:**

1. Add entry:
   ```markdown
   ### Refactor — Layer crystallization Phase 6: reaper + terminal-decision unification

   - **Single orphan reaper.** Replaces the historical pair (one for
     dispatch, one for lock-holders) with one mechanism that reaps stale
     active-phase worker-requests. Held-phase rows are auto-terminal
     concern; their claim-handle children clean up via producer TTL per
     foundation contract §4.5.
   - **Single terminal-decision engine.** `Engine.ResolveTerminal`
     replaces `ApplyTerminalOutcome` and `CheckAndFireResolution` as
     parallel mechanisms. Parameterized by phase (active vs held).
     Claimant-guarded; single-fire; invariants 4, 13 preserved.
   ```

**Verification:**
```sh
grep -q 'Layer crystallization Phase 6' CHANGELOG.md
go build ./... && go test ./... -count=1 && make lint
```

### Task 43 — Rewrite CLAUDE.md

**Files:**
- Edit: `CLAUDE.md` (full rewrite)

**Steps:**

1. Read the current `CLAUDE.md`. Note structure: "What this repo is", "Package import rules", "Blessed invariants", "Build & test", "Reference deployment", "Non-obvious gotchas", "Where to look first", "Code style".

2. Rewrite each section against the post-Phase-6 layered architecture:

   - **What this repo is**: Three Go modules (foundation, protocols, root); modeling layer in root; bundled service reference impls in `stores/` and `executors/`; dashboard in `dashboards/`.

   - **Package import rules**: Foundation and protocols are own modules; depguard enforces pgx isolation and foundation/internal/ isolation. Foundation depends on protocols + stdlib; modeling depends on both; bundled services depend on protocols.

   - **Blessed invariants**: Same numbering preserved (1, 2, 3, 4, 5, 6, 7, 8, 9a, 9b, 10, 11, 12, 13, 15, 20). Re-list each with current code location post-refactor (e.g., `foundation/cascade/state.go` for invariant 1; `foundation/integration/conductor.go` for invariant 7; etc.). Note that 11 and 12 are modeling-layer; 9b is service-protocol-layer; 20 is foundation but enforced across boundaries.

   - **Build & test**: `go build ./...` (root); `go build ./foundation/... ./protocols/...` (per module); `make test-all`; `make proto-gen`; `make lint`.

   - **Reference deployment**: `deploy/docker-compose.yml` brings up the unified stack; new YAML uses `claim_producers:` block.

   - **Non-obvious gotchas**: Update each gotcha to use new vocabulary (scope; ClaimProducer; worker-request phases). Drop gotchas that no longer apply (e.g., the v3 cutover transitional language). Add new gotchas if any:
     - "Foundation `internal/` is private; depguard enforces."
     - "Worker-request phase column drives lifecycle; respect active vs held distinction in any new scheduling code."
     - "Held-claim handles outlive their worker-request's active phase; orphan reaper handles only active-phase rows."

   - **Where to look first**: Pointer to the three contracts:
     ```markdown
     - Foundation: `docs/specs/2026-05-04-foundation-contract.md`
     - Modeling: `docs/specs/2026-05-04-modeling-layer-contract.md`
     - Service protocols: `docs/specs/2026-05-04-service-protocol-contract.md`
     - Operating: `docs/operator-guide.md`
     - Writing a claim producer: `docs/claim-producer-author-guide.md`
     - Writing an executor: `docs/executor-author-guide.md`
     ```

   - **Code style**: Reference cold-read-cheatsheet; same as before.

3. Remove all references to historical specs (the v3 stores doc, the cleanup overlay, etc.) — they're now in `docs/history/` and the contracts have superseded them.

**Verification:**
```sh
test -f CLAUDE.md
! grep -q 'docs/specs/2026-04' CLAUDE.md  # no historical paths
! grep -q 'v3 cutover' CLAUDE.md  # no transitional language
grep -q 'foundation/' CLAUDE.md
grep -q 'protocols/' CLAUDE.md
grep -q 'modeling/' CLAUDE.md
grep -q '2026-05-04-foundation-contract' CLAUDE.md
grep -q '2026-05-04-modeling-layer-contract' CLAUDE.md
grep -q '2026-05-04-service-protocol-contract' CLAUDE.md
```

### Task 44 — Rewrite docs/architecture.md

**Files:**
- Edit: `docs/architecture.md`

**Steps:**

1. Rewrite to present the four-layer model with the architectural diagram from spec §4.1.

2. Document the three modules and their dependencies.

3. Reference the three contracts as authoritative for each layer.

4. Update the "where to look first" and any cross-references.

5. Remove all references to historical specs.

**Verification:**
```sh
test -f docs/architecture.md
! grep -q 'docs/specs/2026-04' docs/architecture.md
grep -q 'foundation' docs/architecture.md
grep -q 'protocols' docs/architecture.md
```

### Task 45 — Rewrite docs/operator-guide.md

**Files:**
- Edit: `docs/operator-guide.md`

**Steps:**

1. Update YAML examples to Option II shape (`claim_producers:` block; `protocols:` per peer; `write_semantics_envelope:` per producer).

2. Update vocabulary: `scope`, `claim producer`, `worker request`. The "store" colloquialism survives where talking about data-backed ones.

3. Reference the new contracts.

4. Update CORS section (added in dashboard round-3) — verify still accurate post-refactor.

**Verification:**
```sh
grep -q 'claim_producers:' docs/operator-guide.md
grep -q 'write_semantics_envelope' docs/operator-guide.md
! grep -q 'docs/specs/2026-04' docs/operator-guide.md
```

### Task 46 — Rewrite docs/glossary.md

**Files:**
- Edit: `docs/glossary.md`

**Steps:**

1. Add new entries: `scope`, `claim producer`, `worker request`, `active phase`, `held phase`, `realized write semantics`, `write semantics envelope`, `lifecycle subscriber`.

2. Mark deprecated terms: `region` (now `scope`), `Store` (protocol-level — now `ClaimProducer`).

3. Update existing entries that referenced old vocabulary.

4. Add the four-layer model summary.

**Verification:**
```sh
grep -q '^## .scope' docs/glossary.md  # entry exists
grep -q 'claim producer' docs/glossary.md
```

### Task 47 — Retire or rewrite docs/protocol.md

**Files:**
- Edit or delete: `docs/protocol.md`

**Steps:**

1. The current `docs/protocol.md` documents the wire protocol; the new `2026-05-04-service-protocol-contract.md` is authoritative.

2. Enumerate referrers before deciding the approach:
   ```sh
   grep -rn 'docs/protocol\.md' . --exclude-dir=docs/history --exclude-dir=.git
   ```
   List the result. If many docs/code paths reference `docs/protocol.md`, rewrite as pointer (preserves links). If few or none, delete and update those references.

3. Default: rewrite as pointer. Replace contents with:
   ```markdown
   # Wire Protocol

   This document is retained as a pointer. Wire-protocol authority moved to
   the service-protocol contract:

   `docs/specs/2026-05-04-service-protocol-contract.md`
   ```
   Then update any referring docs to point at the contract directly (so future link-rot can be caught).

**Verification:**
```sh
test -f docs/protocol.md  # if rewritten as pointer
grep -q '2026-05-04-service-protocol-contract' docs/protocol.md
```

### Task 48 — Rewrite docs/executor-author-guide.md

**Files:**
- Edit: `docs/executor-author-guide.md`

**Steps:**

1. Update for the new module layout: external authors import `github.com/fallguy/rimsky/protocols/executor` only.

2. Reference the service-protocol contract.

3. Update YAML examples.

4. Document the async-callback path (POST `${callback_url}/v1/callback/{async_ack_id}` body keyed `type`).

**Verification:**
```sh
grep -q 'protocols/executor' docs/executor-author-guide.md
```

### Task 49 — Rename and rewrite docs/store-author-guide.md

**Files:**
- Move: `docs/store-author-guide.md` → `docs/claim-producer-author-guide.md`
- Edit: the moved file.

**Steps:**

1. `git mv docs/store-author-guide.md docs/claim-producer-author-guide.md`.

2. Rewrite the body:
   - Title: "Writing a Claim Producer"
   - Reference: `docs/specs/2026-05-04-service-protocol-contract.md`
   - Import path: `github.com/fallguy/rimsky/protocols/claimproducer` (and `protocols/lifecycle` if implementing both).
   - YAML config: `claim_producers:` block; `protocols:` field; `write_semantics_envelope:`.
   - Conformance: run `rimsky-claim-producer-conformance --endpoint <yourservice>:7000`.
   - Note: "store" is the colloquial term for data-backed producers (filesystem, postgres, etc.); the protocol-level term is "claim producer."

**Verification:**
```sh
test -f docs/claim-producer-author-guide.md
test ! -f docs/store-author-guide.md
grep -q 'protocols/claimproducer' docs/claim-producer-author-guide.md
```

### Task 50 — Update docs/node-graph-design.md

**Files:**
- Edit: `docs/node-graph-design.md`

**Steps:**

1. Update to reflect the foundation/modeling vocabulary distinction:
   - Public-facing 4 states / 2 messages / 3 error actions stay (modeling-layer presentation).
   - Add a sub-section: "Under the hood — foundation primitives": brief mapping from the 4-state vocabulary to the 2-bit-plus-flag foundation state space; mapping from the 3 error actions to the foundation's parameterized failure-terminal (auto_recovers + cascade_targets).

2. Update terminology: `region` → `scope`; `store` → "claim producer" at the protocol level.

3. Reference the foundation and modeling contracts.

**Verification:**
```sh
grep -q 'foundation primitives' docs/node-graph-design.md
grep -q 'scope' docs/node-graph-design.md
```

### Task 51 — Update Helm chart in `deploy/kubernetes/rimsky-chart/`

**Files:**
- Edit: `deploy/kubernetes/rimsky-chart/values.yaml`
- Edit: `deploy/kubernetes/rimsky-chart/templates/configmap-rimsky.yaml`
- Edit: `deploy/kubernetes/rimsky-chart/templates/*.yaml` as needed

**Steps:**

1. The Helm chart is known-stale per CLAUDE.md ("Polish T6"). Bring env-var names current with the binaries:
   - `RIMSKY_CONFIG` for all four rimsky binaries.
   - YAML configmap matches the new `claim_producers:` shape.

2. Add Service entries for any newly-renamed binaries (e.g., `rimsky-claim-producer-conformance` if shipping in the chart).

3. Update Deployment specs that reference `core/cmd/...` paths to `cmd/...`.

**Verification:**

If `helm` is available in the environment, run:
```sh
command -v helm && helm lint deploy/kubernetes/rimsky-chart && \
  helm template deploy/kubernetes/rimsky-chart > /tmp/rendered.yaml && \
  grep -q 'claim_producers' /tmp/rendered.yaml
```

If `helm` is NOT available, fall back to file-level checks (no rendering):
```sh
# Every YAML file in the chart parses as YAML.
python3 -c "
import sys, yaml, glob
for f in glob.glob('deploy/kubernetes/rimsky-chart/**/*.yaml', recursive=True):
    try:
        list(yaml.safe_load_all(open(f)))
    except Exception as e:
        print(f'{f}: {e}'); sys.exit(1)
print('all chart YAML parses')
"

# The new YAML key 'claim_producers' appears in the configmap template.
grep -rq 'claim_producers' deploy/kubernetes/rimsky-chart/

# No stale env-var references.
! grep -rn 'RIMSKY_DB_URL' deploy/kubernetes/rimsky-chart/  # gone in spec
! grep -rn '\bcore/cmd/' deploy/kubernetes/rimsky-chart/    # cmd flattened
```

The full helm-rendering check is in the "Manual checks after completion" section since it requires `helm` and human inspection of rendered manifests.

### Task 52 — Final verification: full build, full tests, lint, smoke

**Files:** None (verification only).

**Steps:**

1. Run the full verification pipeline:
   ```sh
   go build ./...
   go test ./... -count=1
   make lint
   make proto-gen  # idempotent — should produce no diff if all is well
   git diff --stat protocols/proto/v1/gen/  # should be empty after proto-gen
   ```

2. Run scenario tests with `-race`:
   ```sh
   go test ./test/scenarios/... -count=3 -race
   ```

3. Run the smoke test:
   ```sh
   go test ./test/smoke/... -count=1
   ```

4. TS executor:
   ```sh
   cd executors/claude-agent && npm install && npm test && npm run build && cd -
   ```

5. Dashboard:
   ```sh
   cd dashboards/rimsky-dashboard && npm install && npm test && npm run build && cd -
   ```

6. Cross-reference sanity check on docs:
   ```sh
   # Every docs/<not-history>/*.md path reference resolves to an existing file.
   for f in $(find docs -name '*.md' -not -path 'docs/history/*'); do
     grep -oE 'docs/[a-zA-Z0-9_/-]+\.md' "$f" | sort -u | while read ref; do
       test -f "$ref" || echo "$f references missing: $ref"
     done
   done
   # Output should be empty.
   ```

**Verification:**

All commands above succeed. The cross-reference check produces no output.

### Task 53 — Final CHANGELOG entry for Phase 7 + summary

**Files:**
- Edit: `CHANGELOG.md`

**Steps:**

1. Add CHANGELOG entry:
   ```markdown
   ### Refactor — Layer crystallization Phase 7: documentation rewrite

   - **`CLAUDE.md` rewritten** for the four-layer model. Three contracts
     are now the authoritative references; transitional language about
     past redesigns removed.
   - **`docs/architecture.md` rewritten** to present the four-layer
     model with the architectural diagram.
   - **`docs/operator-guide.md` rewritten** with new YAML key shape
     (`claim_producers:`, `protocols:`, `write_semantics_envelope:`).
   - **`docs/glossary.md` rewritten** with new vocabulary (scope, claim
     producer, worker request, active phase, held phase, realized write
     semantics, write semantics envelope, lifecycle subscriber).
   - **`docs/protocol.md` retired** as a one-page pointer to
     `docs/specs/2026-05-04-service-protocol-contract.md`.
   - **`docs/executor-author-guide.md` rewritten** for the
     `protocols/executor` import path and new wire shape.
   - **`docs/store-author-guide.md` renamed** to
     `docs/claim-producer-author-guide.md`; rewritten.
   - **`docs/node-graph-design.md` updated** to clarify the
     foundation/modeling vocabulary distinction (the 4 states / 2 messages
     / 3 error actions are modeling-layer presentations of foundation
     primitives).
   - **Helm chart** (`deploy/kubernetes/rimsky-chart/`) brought current
     with the new binary paths and YAML schema.
   ```

2. Add a top-level summary entry under `## Unreleased`:
   ```markdown
   ### Summary — Layer crystallization end-to-end

   The 7-phase architectural reshape produced three durable contract
   documents (foundation, modeling, service-protocol), enforced the
   foundation/protocols boundaries via Go modules (γ: `foundation`,
   `protocols`, root), settled vocabulary (scope, claim producer,
   conductor, advisory locker), consolidated `rimsky_dispatch` +
   `rimsky_lock_holders` into `rimsky_worker_request` +
   `rimsky_claim_handle` with active-phase + held-phase lifecycle, and
   rewrote user-facing docs against the new structure. Future product
   work (services, examples, agentic-workflow primitives) builds on a
   stable foundation and modeling layer that don't need re-litigating.
   ```

**Verification:**
```sh
grep -q 'Layer crystallization Phase 7' CHANGELOG.md
grep -q 'Summary — Layer crystallization' CHANGELOG.md
```

---

## Manual checks after completion

These items require human judgment or environments the implementer cannot reach. Run them after the automated tasks above are complete:

1. **Operator can run `docker compose -f deploy/docker-compose.yml up -d`** with the new YAML shape and reach `/health` on `:8080`. Then drive a stub-mode dispatch through `rimsky-cli template register` + `rimsky-cli instance create` and verify it terminates.

2. **Read the new `docs/operator-guide.md`** front-to-back. Confirm: a new operator could deploy, configure, register a template, run a dispatch, and observe outputs without consulting any historical docs.

3. **Read the three contracts** (`docs/specs/2026-05-04-foundation-contract.md`, `docs/specs/2026-05-04-modeling-layer-contract.md`, `docs/specs/2026-05-04-service-protocol-contract.md`) front-to-back. Confirm they are internally consistent, externally consistent with each other, and match the implementation. If a section drifted during implementation, update the contract.

4. **Walk the dashboard** at `http://localhost:8081` (or wherever the dashboard runs). Confirm: instances list renders; cascade graph renders; trace events appear; claim ledger shows expected events. The dashboard's path references should still work post-refactor (the dashboard was not in the spec scope but its server-side proxy targets renamed paths).

5. **Sanity check on Helm chart**: `helm template deploy/kubernetes/rimsky-chart > /tmp/rendered.yaml`; visually inspect the output for any stale env vars or paths.

6. **Run `rimsky-conformance --check-executor` and `rimsky-claim-producer-conformance`** against each bundled service (`stores/{filesystem,postgres,stub}`, `executors/{http-node,claude-agent,stub}`). Confirm clean passes.
