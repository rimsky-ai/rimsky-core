# Subscription-cascade resolution + quality-of-life cycle — implementation plan

**Spec:** `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`
**Goal:** Decompose the overloaded `dependencies:` block into impactee-side `subscribes:` + `rimsky_wait_set` ledger; type `Park.reason` as an enum; ship an atomic-staging pattern doc + reference filesystem `ClaimProducer`.
**Architecture:** Foundation change in `foundation/cascade/` + `foundation/persistence/` (new wait-set table; cascade walk reframed as pessimistic-invalidate + settled-state drain). Template-validator rewrite in `graph/node/` (reject old shape, validate new shape, parse substitution refs into inverse-edge map). Eligibility predicate change in `foundation/persistence/postgres/nodes.go` + `foundation/persistence/sqlite/nodes.go` (the `ListReadyForDispatch` query joins against `rimsky_wait_set` instead of `dependencies`). Send-side `invalidate.targets` declarations retire across the lifecycle-handler family and `error_types`. `concept:on-event-handler` retires; ten concept docs mutate; two new concept docs land. Piece 2 (Park.reason typed) and Piece 3 (atomic-staging) are independent of Piece 1; tasks for them are interleaved at points where they don't conflict.
**Tech Stack:** Go 1.22+ (root + foundation + protocols modules tied by `go.work`); PostgreSQL 14+ via `jackc/pgx/v5`; SQLite via `modernc.org/sqlite`; protobuf via `protocols/proto/v1/`; TypeScript executor at `executors/claude-agent/` (Node 20+, vitest); operator dashboard `dashboards/rimsky-dashboard/` (TS / Vite). HTTP routing `go-chi/chi`; logging stdlib `log/slog` (JSON, field-structured).

---

## Orientation: how this codebase is structured

The implementer should read these once before starting:

- **`/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/CLAUDE.md`** — repo conventions, module layout (foundation / protocols / root), package import rules (`golangci.yml` enforces `pgx-isolation`, `foundation-purity`, `graph-purity`, `runtime-purity`), blessed invariants, gotchas, build commands.
- **`/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/.claude/rules/rules.md`** — pre-v1 break-freely policy, after-code-changes checklist, fix-every-bug rule.
- **`/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/.claude/rules/cold-read-cheatsheet.md`** — cold-read conventions (organize by feature, ~500-line file / ~100-line function, tracked duplication via `@source:`, etc.).
- **`/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/.claude/rules/citation-grammar.md`** — citation grammar for agent-to-user prose. Not for code or docs.
- **`/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/.ok-planner/design/concepts.md`** — concept catalog TOC. Read this and any concept docs the plan touches.
- **The spec** at `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md` is the authoritative source of truth for what this plan implements. Re-read sections of the spec as the corresponding plan task is reached; the plan does not re-derive the design decisions.

The repo has three Go modules tied by `go.work`:
- **`foundation/`** — `github.com/fallguyconsulting/rimsky/foundation` — cascade, persistence (Postgres + SQLite), claim/lock primitives, persistable row-type primitives (`foundation/spec/`).
- **`protocols/`** — `github.com/fallguyconsulting/rimsky/protocols` — gRPC interfaces + `.proto` sources + generated bindings.
- **Root module** `github.com/fallguyconsulting/rimsky` — `graph/`, `runtime/`, `control/`, `cmd/`, `stores/`, `executors/`, `dashboards/`.

Pre-v1 rule: no migration shims, no backwards-compat aliases. Break freely. When the migration shape would be cleaner without a shim, take the clean path.

Verification commands referenced throughout this plan:

- `make build-all` — `go build` across all three modules.
- `make test-all` — `go test` across all three modules.
- `make lint` — `golangci-lint` (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive).
- `make proto-gen` — regenerate proto bindings.
- `make tidy` — `go mod tidy` across all modules.
- `cd executors/claude-agent && npm test` — vitest for the TS executor.
- `cd executors/claude-agent && npm run build` — `tsc` for the TS executor.
- Test-scenario integration tests use testcontainers-go (Postgres in Docker); require a working Docker socket.

---

## Concept-doc consultations

Plan tasks below will mutate or retire concept docs and add new ones. The full list (from the spec's `## Concept impacts` section):

- New: `.ok-planner/design/concepts/subscription.md`, `.ok-planner/design/concepts/wait-set.md`.
- Mutate in place (Notes entry appended; Boundaries/Invariants edits as called out per task): `cascade`, `invalidate`, `node`, `lifecycle-handler`, `error-policy`, `named-event`, `frame`, `last-outcome`, `parked-state`, `executor`, `claim-producer` (light).
- Retire to `concepts/_retired/`: `on-event-handler`.

Resolved tensions to land under `.ok-planner/design/tensions/_resolved/`:
- `dependency-overloaded-bundle.md`
- `subscription-implies-cascade-dependency.md`
- `rimsky-not-a-dag-vocabulary.md`
- `send-vs-subscribe-asymmetry.md`

---

# Tasks

## T1. Schema migration: extend baseline (pre-v1 baseline-update) — Postgres

**Files:**
- `foundation/persistence/postgres/migrations/001-baseline.sql`

**Steps:**

1. Open the file. Locate the `CREATE TABLE IF NOT EXISTS rimsky_node_runs` block (around line 128). Find the line declaring `parked_reason TEXT,`. Add a new line immediately after it:
   ```sql
       parked_reason_note                   TEXT,
   ```
   (15 characters of indentation matching siblings, NO trailing comma if it's the new last column before `wake_reason`; place this BEFORE the `wake_reason` line to keep ordering monotonic.)

2. Add a new top-level table definition immediately AFTER the `rimsky_node_runs` table block (i.e., before the `rimsky_schedules` table). Insert:
   ```sql
   -- Per-frame wait-set ledger driving dispatch eligibility under the
   -- subscription-cascade model. Cascade walks insert rows when a sender
   -- transitions out of a settled state (the "pessimistic invalidate");
   -- the drain rule deletes rows where sender_node_id = S in bulk when
   -- the sender reaches any settled state (fresh / failed / parked).
   -- Eligibility predicate: a stale node is dispatch-eligible iff no
   -- wait-set rows exist for it in the current frame.
   --
   -- subscription_scope distinguishes per-node ('direct') from
   -- cross-cutting ('instance') subscriptions so a receiver subscribed
   -- to a sender via BOTH a direct and a cross-cutting subscription
   -- gets two distinct rows that both must drain.
   --
   -- ON DELETE CASCADE from rimsky_frames(frame_id) cleans up stale
   -- wait-set rows when a frame closes.
   CREATE TABLE IF NOT EXISTS rimsky_wait_set (
       frame_id            UUID        NOT NULL REFERENCES rimsky_frames(frame_id) ON DELETE CASCADE,
       receiver_node_id    UUID        NOT NULL REFERENCES rimsky_nodes(id)        ON DELETE CASCADE,
       sender_node_id      UUID        NOT NULL REFERENCES rimsky_nodes(id)        ON DELETE CASCADE,
       topic_kind          TEXT        NOT NULL CHECK (topic_kind IN ('state','attribute','event')),
       subscription_scope  TEXT        NOT NULL CHECK (subscription_scope IN ('direct','instance')),
       topic_filter        JSONB,
       inserted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       PRIMARY KEY (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope)
   );
   CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_receiver ON rimsky_wait_set (frame_id, receiver_node_id);
   CREATE INDEX IF NOT EXISTS idx_rimsky_wait_set_sender   ON rimsky_wait_set (frame_id, sender_node_id);
   ```

**Verification:**
- `cd foundation && go build ./...` — passes (no Go change yet, but builds embedded migration).
- `grep -n "rimsky_wait_set\|parked_reason_note" foundation/persistence/postgres/migrations/001-baseline.sql` — both names appear.

## T2. Schema migration: extend baseline — SQLite

**Files:**
- `foundation/persistence/sqlite/migrations/001-baseline.sql`

**Steps:**

1. Apply the same two edits as T1 against the SQLite baseline. Notes for SQLite syntax:
   - `TIMESTAMPTZ` → `TIMESTAMP` (SQLite has no `TIMESTAMPTZ`).
   - `JSONB` → `TEXT` (SQLite stores JSON in TEXT columns).
   - `NOW()` → `CURRENT_TIMESTAMP`.
   - `CHECK` constraints work identically.
   - `CREATE INDEX IF NOT EXISTS` works identically.
2. The `parked_reason_note TEXT,` insertion at the equivalent location.
3. Insert the `rimsky_wait_set` table immediately before the `rimsky_schedules` table.

**Verification:**
- `grep -n "rimsky_wait_set\|parked_reason_note" foundation/persistence/sqlite/migrations/001-baseline.sql` — both names appear.

## T3. Spec types: update `TemplateNodeDef` and handler types

**Files:**
- `foundation/spec/template.go`

**Steps:**

1. In `TemplateNodeDef`, REMOVE the `Dependencies []string` field. (Pre-v1 break-freely; no compat alias.)
2. REMOVE the `OnEvent map[string]EventHandler` field and its block comment.
3. ADD a new field `Subscribes []SubscriptionEntry` with:
   ```go
   	// Subscribes declares the node's reactive surface. Each entry names an
   	// upstream node (or instance: true for cross-cutting) plus a topic kind
   	// (state | attribute | event) with optional filters and a frame
   	// modifier. Plus implicit subscriptions inferred by the template
   	// validator from substitution refs in Attributes (see
   	// graph/node/subscription_edges.go). Per spec
   	// .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
   	// Piece 1.
   	Subscribes []SubscriptionEntry `yaml:"subscribes,omitempty" json:"subscribes,omitempty"`
   ```
4. REMOVE the `EventHandler` struct entirely (its only consumer was the deleted `OnEvent` map).
5. In `OnAcquireUnavailableHandler`, REMOVE the `Invalidate *HandlerInvalidate` field. Keep `Resolve` and `ErrorClass`.
6. In `OnExecutorCompleteHandler`, REMOVE its `Invalidate` field (find the struct definition further down in the file; remove the `Invalidate *HandlerInvalidate` line, keep its `Resolve` semantics).
7. In `OnExecutorTerminalHandler` (used by `OnExecutorErrored`), REMOVE its `Invalidate` field. Keep `Resolve` / `ErrorClass`.
8. In `PolicyAction` (used by `ErrorTypePolicy.Policy`), REMOVE the `"invalidate"` value from its accepted `Action` set. Locate the type definition; if `Action` is a string field with documented values in a comment, update the comment to drop `invalidate`. If the field is an enum-like with `const`s, remove the constant.
9. The `HandlerInvalidate` type may still be referenced by external code. Keep it for now; T10 will retire it once all callsites are gone.

**Verification:**
- `cd foundation && go build ./spec/...` — passes (this module is self-contained).
- `grep -n "OnEvent\|Dependencies " foundation/spec/template.go` — no occurrences in `TemplateNodeDef`.

## T4. Spec types: add `SubscriptionEntry`

**Files:**
- New: `foundation/spec/subscription.go`

**Steps:**

1. Create the new file with package `spec` and the following content:
   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package spec

   // SubscriptionEntry declares one impactee-side reactive coupling.
   // See .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
   // Piece 1 (subscription-cascade model resolution).
   //
   //	@concept: subscription
   type SubscriptionEntry struct {
   	// Node names the upstream node-type (template-relative). Mutually
   	// exclusive with Instance.
   	Node string `yaml:"node,omitempty" json:"node,omitempty"`

   	// Instance=true makes this a cross-cutting subscription: fires on
   	// the topic match across every node in the instance. Mutually
   	// exclusive with Node.
   	Instance bool `yaml:"instance,omitempty" json:"instance,omitempty"`

   	// On is the topic kind: "state" | "attribute" | "event".
   	On string `yaml:"on" json:"on"`

   	// When narrows a state subscription to a specific node-state
   	// ("fresh" | "stale" | "running" | "failed" | "parked"). Empty
   	// means "any state transition." Only meaningful when On == "state".
   	When string `yaml:"when,omitempty" json:"when,omitempty"`

   	// Outcome narrows a state subscription further to a last_outcome
   	// value ("fresh_changed" | "fresh_unchanged" | "passed" |
   	// "pure_cascade" | "failed"). Only meaningful when On == "state"
   	// AND When != "".
   	Outcome string `yaml:"outcome,omitempty" json:"outcome,omitempty"`

   	// ErrorClass narrows a state subscription further to a specific
   	// error_class string. Only meaningful when On == "state" AND
   	// When == "failed".
   	ErrorClass string `yaml:"error_class,omitempty" json:"error_class,omitempty"`

   	// Reason narrows a state subscription further to a specific
   	// ParkReason. Lower-snake-case form (matching storage/CLI/Prometheus
   	// surface). Only meaningful when On == "state" AND When == "parked".
   	Reason string `yaml:"reason,omitempty" json:"reason,omitempty"`

   	// Name is required for On == "event" (the named-event name).
   	// Optional for On == "attribute" (specific attribute key; absent
   	// means "any attribute change"). Unused for On == "state".
   	Name string `yaml:"name,omitempty" json:"name,omitempty"`

   	// Frame is "in" | "next". Empty defaults to "in" for per-node
   	// subscriptions and "next" for cross-cutting (Instance=true).
   	Frame string `yaml:"frame,omitempty" json:"frame,omitempty"`
   }

   // Topic-kind constants for SubscriptionEntry.On.
   const (
   	TopicKindState     = "state"
   	TopicKindAttribute = "attribute"
   	TopicKindEvent     = "event"
   )

   // Subscription-scope constants used by the wait-set persistence layer.
   const (
   	SubscriptionScopeDirect   = "direct"
   	SubscriptionScopeInstance = "instance"
   )
   ```

**Verification:**
- `cd foundation && go build ./spec/...` — passes.
- `grep -n "SubscriptionEntry" foundation/spec/subscription.go` — type appears.

## T5. Spec types: row-storage shape for subscriptions

**Files:**
- `foundation/spec/template.go` (top-level constant block area)

**Steps:**

1. The state-machine values (`fresh`, `stale`, `running`, `failed`, `parked`) and `last_outcome` values are referenced by subscription filters. Look for existing constants in `foundation/spec/template.go` and `foundation/cascade/state.go`; if state-name constants don't exist yet in `foundation/spec/`, add them at the top of `subscription.go` (extend T4's file):
   ```go
   // Node-state values valid as SubscriptionEntry.When for On=="state".
   // Mirrors the foundation/cascade state machine; redeclared here so the
   // template validator can range-check without importing cascade
   // (foundation-internal-isolation depguard).
   const (
   	NodeStateFresh   = "fresh"
   	NodeStateStale   = "stale"
   	NodeStateRunning = "running"
   	NodeStateFailed  = "failed"
   	NodeStateParked  = "parked"
   )
   ```
   Check first whether these constants already exist somewhere usable in `foundation/spec/` — if so, use the existing names rather than duplicating.

**Verification:**
- `cd foundation && go build ./spec/...` — passes.

## T6. Wait-set persistence interface

**Files:**
- New: `foundation/persistence/wait_set.go`

**Steps:**

1. Create the file:
   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
   // repo root, or http://www.apache.org/licenses/LICENSE-2.0.

   package persistence

   import (
   	"context"
   	"encoding/json"

   	"github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   // WaitSetRow is one row of rimsky_wait_set.
   //
   //	@concept: wait-set
   type WaitSetRow struct {
   	FrameID            shared.UUID
   	ReceiverNodeID     shared.UUID
   	SenderNodeID       shared.UUID
   	TopicKind          string // "state" | "attribute" | "event"
   	SubscriptionScope  string // "direct" | "instance"
   	TopicFilter        json.RawMessage // nullable; carried for observability
   }

   // WaitSetTable is the persistence-layer access surface for
   // rimsky_wait_set, the per-frame ledger that drives dispatch
   // eligibility under the subscription-cascade model.
   //
   //	@concept: wait-set
   type WaitSetTable interface {
   	// Insert adds one wait-set row. Idempotent under the table's PK
   	// (frame_id, receiver, sender, topic_kind, subscription_scope) —
   	// duplicate inserts within the same transaction are dropped via
   	// ON CONFLICT DO NOTHING.
   	Insert(ctx context.Context, row WaitSetRow, tx Tx) error

   	// DeleteBySender bulk-deletes every wait-set row where
   	// (frame_id, sender_node_id) match. Drains the sender from every
   	// receiver's wait-set in one statement. Called by the cascade walk
   	// when a sender reaches any settled state (fresh / failed / parked).
   	DeleteBySender(ctx context.Context, frameID, senderID shared.UUID, tx Tx) error

   	// ListForReceiver returns the wait-set rows currently gating the
   	// receiver. Used by /admin/diagnostics/wait-sets for stuck-frame
   	// debugging.
   	ListForReceiver(ctx context.Context, frameID, receiverID shared.UUID, tx Tx) ([]WaitSetRow, error)

   	// ListForFrame returns every wait-set row in a frame. Used by
   	// /admin/diagnostics/wait-sets without a receiver filter.
   	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)
   }
   ```

2. In `foundation/persistence/database.go` (or wherever the `Tables` umbrella interface and `Database` interface live — locate via `grep -rn "type Tables interface" foundation/persistence/`), add a `WaitSet() WaitSetTable` accessor to the `Tables` interface and update the umbrella implementation.

   To find: `grep -n "ClaimHandle\|ClaimHolder\|Nodes()" foundation/persistence/database.go` to find the existing accessor pattern; mirror it.

**Verification:**
- `cd foundation && go build ./persistence/...` — fails until T7 and T8 land (the Postgres + SQLite impls), so this is a partial-build step. Move to T7.

## T7. Wait-set persistence: Postgres impl

**Files:**
- New: `foundation/persistence/postgres/wait_set.go`

**Steps:**

1. Create the file with `package postgres` and the four methods. Mirror the structure of `foundation/persistence/postgres/nodes.go` (the `nodesImpl` shape — read it first to match the codebase pattern):
   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
   // repo root, or http://www.apache.org/licenses/LICENSE-2.0.

   package postgres

   import (
   	"context"
   	"encoding/json"
   	"fmt"

   	"github.com/fallguyconsulting/rimsky/foundation/persistence"
   	"github.com/fallguyconsulting/rimsky/foundation/shared"
   )

   type waitSetImpl struct {
   	*pgStore
   }

   func (s *waitSetImpl) Insert(ctx context.Context, row persistence.WaitSetRow, tx persistence.Tx) error {
   	ex := s.q(tx)
   	_, err := ex.Exec(ctx,
   		`INSERT INTO rimsky_wait_set
   		   (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter)
   		 VALUES ($1, $2, $3, $4, $5, $6)
   		 ON CONFLICT (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope)
   		 DO NOTHING`,
   		row.FrameID, row.ReceiverNodeID, row.SenderNodeID,
   		row.TopicKind, row.SubscriptionScope, row.TopicFilter)
   	if err != nil {
   		return fmt.Errorf("rimsky_wait_set insert: %w", err)
   	}
   	return nil
   }

   func (s *waitSetImpl) DeleteBySender(ctx context.Context, frameID, senderID shared.UUID, tx persistence.Tx) error {
   	ex := s.q(tx)
   	_, err := ex.Exec(ctx,
   		`DELETE FROM rimsky_wait_set
   		  WHERE frame_id = $1 AND sender_node_id = $2`,
   		frameID, senderID)
   	if err != nil {
   		return fmt.Errorf("rimsky_wait_set delete by sender: %w", err)
   	}
   	return nil
   }

   func (s *waitSetImpl) ListForReceiver(ctx context.Context, frameID, receiverID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
   	ex := s.q(tx)
   	rows, err := ex.Query(ctx,
   		`SELECT frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter
   		   FROM rimsky_wait_set
   		  WHERE frame_id = $1 AND receiver_node_id = $2`,
   		frameID, receiverID)
   	if err != nil {
   		return nil, fmt.Errorf("rimsky_wait_set list for receiver: %w", err)
   	}
   	defer rows.Close()
   	return collectWaitSet(rows)
   }

   func (s *waitSetImpl) ListForFrame(ctx context.Context, frameID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
   	ex := s.q(tx)
   	rows, err := ex.Query(ctx,
   		`SELECT frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter
   		   FROM rimsky_wait_set
   		  WHERE frame_id = $1`,
   		frameID)
   	if err != nil {
   		return nil, fmt.Errorf("rimsky_wait_set list for frame: %w", err)
   	}
   	defer rows.Close()
   	return collectWaitSet(rows)
   }

   func collectWaitSet(rows pgx.Rows) ([]persistence.WaitSetRow, error) {
   	out := []persistence.WaitSetRow{}
   	for rows.Next() {
   		var w persistence.WaitSetRow
   		var filter []byte
   		if err := rows.Scan(&w.FrameID, &w.ReceiverNodeID, &w.SenderNodeID,
   			&w.TopicKind, &w.SubscriptionScope, &filter); err != nil {
   			return nil, err
   		}
   		if filter != nil {
   			w.TopicFilter = json.RawMessage(filter)
   		}
   		out = append(out, w)
   	}
   	return out, rows.Err()
   }
   ```
   (Replace `pgx.Rows` with whatever the codebase actually imports; check `foundation/persistence/postgres/nodes.go`'s imports for the right alias.)

2. Wire `waitSetImpl` into the postgres `Tables` accessor — find where the postgres adapter constructs its `Tables` value (likely in `foundation/persistence/postgres/database.go` or similar; grep for `func.*Nodes()` to locate the pattern) and add `WaitSet()` returning a `*waitSetImpl{pgStore: store}`.

**Verification:**
- `cd foundation && go build ./persistence/postgres/...` — passes after both this file and the `Tables` wiring are in.

## T8. Wait-set persistence: SQLite impl

**Files:**
- New: `foundation/persistence/sqlite/wait_set.go`

**Steps:**

1. Mirror T7 against the SQLite driver. Notable SQLite differences (read `foundation/persistence/sqlite/nodes.go` for the codebase pattern):
   - Param binding uses `?` placeholders, not `$N`.
   - `ON CONFLICT (...) DO NOTHING` is supported by `modernc.org/sqlite`.
   - The query executor type and Rows abstraction differ; match the existing `sqlite` package shape.
2. Wire into the SQLite `Tables` accessor (mirror T7 step 2).

**Verification:**
- `cd foundation && go build ./persistence/sqlite/...` — passes.

## T9. Wait-set persistence: conformance fixture

**Files:**
- New: `foundation/persistence/conformance/wait_set.go`

**Steps:**

1. Create a conformance test file at the path above, mirroring the structure of an existing conformance fixture (e.g. `foundation/persistence/conformance/nodes_mark_stale_for_cascade.go`).
2. Test the four `WaitSetTable` methods against both adapters:
   - `Insert` once; `ListForReceiver` returns one row; PK conflict on duplicate `Insert` is a no-op.
   - `DeleteBySender` removes all rows for `(frame, sender)`; `ListForReceiver` returns empty.
   - `ListForFrame` returns the full set.
3. Include the new fixture in the conformance dispatcher (find via `grep -rn "MarkStaleForCascade\|conformance fixtures" foundation/persistence/conformance/`).

**Verification:**
- `cd foundation && go test -run TestWaitSet ./persistence/postgres/... -count=1` — passes (requires Docker for testcontainers).
- `cd foundation && go test -run TestWaitSet ./persistence/sqlite/... -count=1` — passes.

## T10. Retire `HandlerInvalidate` from foundation/spec

**Files:**
- `foundation/spec/template.go`

**Steps:**

1. Verify no remaining callsites in `foundation/spec/` reference `HandlerInvalidate`. (At this point T3 removed all consumer fields; only the type definition should remain.)
2. Delete the `HandlerInvalidate` struct definition and any documentation comments referencing it.
3. Also delete the per-emit-frame constants `FrameIn` / `FrameNext` if they are defined only for handler-invalidate use. If `Frame` is reused by subscription entries, keep `FrameIn` / `FrameNext` constants and note they're now subscription-frame values.

**Verification:**
- `cd foundation && go build ./...` — passes.
- `grep -rn "HandlerInvalidate" foundation/` — no matches.

## T11. Substitution grammar: rename `deps.X.Y` → `nodes.X.attribute.Y`

**Files:**
- `graph/attribute/substitution.go`

**Steps:**

1. Locate `directivePattern` and the parsing logic that splits the inside of `{{...}}` into source-kind + path. Find the branch that handles `deps.<node>.<field>...` — likely a switch on the first path token.
2. Replace the `deps.` token recognition with a parser for `nodes.<node>.attribute.<field>...` and `nodes.<node>.event.<event_name>.<path>...`. The existing event-substitution branch should already parse `nodes.<emitter>.event.<event_name>.<path>` — verify, and unify the dispatch so:
   - `nodes.<X>.attribute.<key>` resolves through the existing `Deps` map (rename the path traversal to walk under `attribute.<key>` instead of `<key>` directly).
   - `nodes.<X>.event.<name>.<path>` continues to resolve through `EventLookup`.
3. Remove all references to the `deps.` directive prefix. The error message for `ErrMissingSource` should cite the `nodes.X.attribute.Y` shape.
4. Update the package doc-comment that references `deps.X.Y` (`graph/attribute/doc.go` if present; search via `grep -rn "deps\." graph/attribute/`).
5. Search for tests in the package that exercise the `deps.` form and update them to `nodes.X.attribute.Y` (`grep -n "deps\." graph/attribute/*_test.go`).

**Verification:**
- `go test ./graph/attribute/... -count=1` — passes.
- `grep -rn "deps\." graph/attribute/` — no remaining occurrences except possibly in module-internal log-line text.

## T12. Substitution grammar: update `runtime/runner_locks.go` to populate `Deps` map under new keys

**Files:**
- `runtime/runner_locks.go`
- `runtime/runner_dispatch.go`

**Steps:**

1. In `runtime/runner_locks.go::loadDepsAttributes` (around line 220), the function loads each upstream node's `rimsky_node_attributes.data` into a map keyed by `depNode.NodeType`. The map is consumed by `ResolveContext.Deps`. Under T11's grammar, `Deps[X]` is now read as `nodes.X.attribute.<field>` — the map keying by node-type is unchanged, but the variable name and comment should reflect the new grammar. Rename `loadDepsAttributes` → `loadSubscribedNodeAttributes`; update its doc comment to say it loads attribute data for every upstream node referenced via substitution refs and explicit subscriptions.
2. In `runtime/runner_dispatch.go::loadDepsAttributesByID` (around line 572), do the equivalent rename: `loadDepsAttributesByID` → `loadSubscribedNodeAttributesByID`. Update the doc comment.
3. The set of nodes whose attributes get loaded was previously `nd.Dependencies`. Under the new model, it must be the set of nodes referenced in the receiver's substitution refs PLUS explicit `subscribes:` entries naming nodes. For now, source this from the inverse-edge map per template (computed in T16); for this task, place a `TODO: replaced by subscription-edge lookup in T16` and continue using a function parameter for the node-type set so the call sites in T16 can pass it in. Concretely, change the signatures to accept `subscribedNodeIDs []shared.UUID` instead of pulling `nd.Dependencies` internally.
4. Update the callers in the same package to pass the new parameter; for now they can pass the `nd.Dependencies`-equivalent — T16/T17 will fix this when the subscription edge map lands.

**Verification:**
- `cd .. && go build ./runtime/...` (from the project root) — passes.

## T13. Foundation cascade: state machine constants for subscription topics

**Files:**
- `foundation/cascade/state.go`

**Steps:**

1. Review the state-machine transitions. The subscription-cascade resolution does not change the state values (`fresh`, `stale`, `running`, `failed`, `parked`) or the legal transitions. No code change in this file beyond verifying the constants exposed for use by subscription validation.
2. Add a doc-comment block at the top of `state.go` (just above the package declaration block) noting that the state values are referenced by `SubscriptionEntry.When` filters in `foundation/spec/subscription.go`. No code change.

**Verification:**
- `cd foundation && go build ./cascade/...` — passes.

## T14. Subscription-edge inverse map (template registration)

**Files:**
- New: `graph/node/subscription_edges.go`

**Steps:**

1. Create a new file under `graph/node/` (the package that owns the template validator). Define:
   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // Subscription-edge inverse map. Computed at template registration
   // by the validator (template_validator.go::validateSubscribes plus the
   // substitution-ref auto-subscribe inference). Cached on the template
   // row for cascade-walk lookup at runtime.
   //
   //	@concept: subscription
   //	@concept: wait-set
   package node

   import (
   	"github.com/fallguyconsulting/rimsky/foundation/spec"
   )

   // SubscriptionEdge is one entry in the inverse map: from a sender's
   // node-type, the list of (receiver_node_type, topic_kind, topic_filter)
   // that the cascade walk should match against the sender's transition.
   type SubscriptionEdge struct {
   	ReceiverNodeType  string
   	TopicKind         string
   	SubscriptionScope string // "direct" | "instance"
   	Filter            SubscriptionFilter
   	Frame             string // "in" | "next"
   }

   // SubscriptionFilter captures the optional filter dimensions of a
   // SubscriptionEntry that the cascade walk evaluates against a sender's
   // transition.
   type SubscriptionFilter struct {
   	When       string // "" | node-state
   	Outcome    string // "" | last_outcome
   	ErrorClass string // "" | error_class
   	Reason     string // "" | snake_case ParkReason
   	Name       string // "" | attribute key OR event name
   }

   // SubscriptionEdgeMap is keyed by sender node-type. The empty key ""
   // holds cross-cutting (instance: true) subscriptions.
   type SubscriptionEdgeMap map[string][]SubscriptionEdge

   // BuildSubscriptionEdges walks every node's Subscribes block plus the
   // substitution refs parsed from its attribute schema, and produces the
   // inverse map. Called by the template validator at registration.
   //
   // The substitution refs are passed in as parallel arrays per receiver
   // node-type (parsed by extractSubstitutionRefs below). Explicit entries
   // are unioned with implicit entries; duplicate (sender, topic_kind,
   // filter, frame) tuples are de-duplicated.
   func BuildSubscriptionEdges(
   	spec spec.TemplateSpec,
   	substitutionRefs map[string][]substitutionRef,
   ) SubscriptionEdgeMap {
   	out := SubscriptionEdgeMap{}
   	for _, n := range spec.Nodes {
   		receiverType := n.Type
   		// Explicit subscriptions.
   		for _, s := range n.Subscribes {
   			edge := edgeFromSubscription(s, receiverType)
   			senderKey := s.Node // empty for cross-cutting
   			out[senderKey] = appendUniqueEdge(out[senderKey], edge)
   		}
   		// Implicit subscriptions from substitution refs.
   		for _, ref := range substitutionRefs[receiverType] {
   			edge := edgeFromSubstitutionRef(ref, receiverType)
   			out[ref.SenderNodeType] = appendUniqueEdge(out[ref.SenderNodeType], edge)
   		}
   	}
   	return out
   }

   // substitutionRef is one parsed `{{nodes.X.attribute.Y}}` or
   // `{{nodes.X.event.Z.<path>}}` directive in a node's attribute schema.
   type substitutionRef struct {
   	SenderNodeType string
   	TopicKind      string // "attribute" | "event"
   	Name           string // attribute key or event name
   }

   // extractSubstitutionRefs scans every node's NodeAttributesDef.Schema
   // recursively, walks every `source:` string in the JSON-Schema tree,
   // parses the {{...}} directives in each source string, and returns
   // a map from receiver-node-type to the list of refs the receiver
   // depends on. Returns an empty map if no refs found.
   func extractSubstitutionRefs(t spec.TemplateSpec) map[string][]substitutionRef {
   	// Implementation: recursive descent over Schema (a map[string]any).
   	// Look for keys named "source" with string values. For each,
   	// regex-match every {{...}} occurrence (mirror
   	// graph/attribute/substitution.go::directivePattern).
   	// For each directive that matches nodes.<X>.attribute.<Y> or
   	// nodes.<X>.event.<Y>[.<path>], emit a substitutionRef.
   	// Skip claim.* and params.* directives — they don't auto-subscribe.
   	// Skip nodes.<self>.* (a node's own substitution refs do not gate
   	// itself).
   	// TODO when implementing: pull the directive regex from substitution.go
   	// rather than duplicating; this is shared parsing logic.
   	// ...
   }

   func edgeFromSubscription(s spec.SubscriptionEntry, receiverType string) SubscriptionEdge {
   	scope := spec.SubscriptionScopeDirect
   	if s.Instance {
   		scope = spec.SubscriptionScopeInstance
   	}
   	frame := s.Frame
   	if frame == "" {
   		if s.Instance {
   			frame = "next"
   		} else {
   			frame = "in"
   		}
   	}
   	return SubscriptionEdge{
   		ReceiverNodeType:  receiverType,
   		TopicKind:         s.On,
   		SubscriptionScope: scope,
   		Filter: SubscriptionFilter{
   			When: s.When, Outcome: s.Outcome,
   			ErrorClass: s.ErrorClass, Reason: s.Reason,
   			Name: s.Name,
   		},
   		Frame: frame,
   	}
   }

   func edgeFromSubstitutionRef(ref substitutionRef, receiverType string) SubscriptionEdge {
   	return SubscriptionEdge{
   		ReceiverNodeType:  receiverType,
   		TopicKind:         ref.TopicKind,
   		SubscriptionScope: spec.SubscriptionScopeDirect,
   		Filter:            SubscriptionFilter{Name: ref.Name},
   		Frame:             "in",
   	}
   }

   func appendUniqueEdge(edges []SubscriptionEdge, e SubscriptionEdge) []SubscriptionEdge {
   	for _, existing := range edges {
   		if existing == e {
   			return edges
   		}
   	}
   	return append(edges, e)
   }
   ```

2. Implement `extractSubstitutionRefs` by recursive descent over the JSON-Schema map. Look for any `"source"` string-typed value at any depth; extract `{{...}}` directives via the same regex pattern used in `graph/attribute/substitution.go::directivePattern`. For each directive, parse the prefix:
   - `nodes.<node_name>.attribute.<key>` → `{SenderNodeType: <node_name>, TopicKind: "attribute", Name: <key>}`.
   - `nodes.<node_name>.event.<name>.<path>` → `{SenderNodeType: <node_name>, TopicKind: "event", Name: <name>}`.
   - Skip `claim.*`, `params.*`, and self-references.

**Verification:**
- `go build ./graph/node/...` — passes.

## T15. Subscription-edge unit tests

**Files:**
- New: `graph/node/subscription_edges_test.go`

**Steps:**

1. Add table-driven tests for `BuildSubscriptionEdges` covering:
   - Empty template → empty map.
   - One node with explicit `subscribes:` entries → map keyed by sender with the right `(receiver, topic_kind, filter, frame)` tuples.
   - Cross-cutting `instance: true` → key is empty string.
   - Substitution refs in attribute schema → implicit subscriptions appear.
   - Union of explicit + implicit dedupes correctly.
   - `Frame` defaults to `in` for per-node, `next` for cross-cutting; explicit `frame:` overrides.

**Verification:**
- `go test ./graph/node/... -run TestBuildSubscriptionEdges -count=1 -v` — all cases pass.

## T16. Template validator: reject `dependencies:` and old shape; validate `subscribes:`

**Files:**
- `graph/node/template_validator.go`

**Steps:**

1. Locate `validateDependencies` (around line 202). Replace its body so that any non-empty `n.Dependencies` produces a validation error:
   ```go
   func validateDependencies(n TemplateNodeDef, base string, _ map[string]int, res *ValidationResult) {
   	if len(n.Dependencies) == 0 {
   		return
   	}
   	res.Errors = append(res.Errors, ValidationError{
   		Path: base + ".dependencies",
   		Msg:  "`dependencies:` retired; declare reactive coupling under `subscribes:` and read upstream data via substitution refs (`{{nodes.<node>.attribute.<key>}}`). See .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md Piece 1 migration table.",
   	})
   }
   ```
   This validator stays in place during the transition window so any leftover `dependencies:` in unmigrated templates surfaces as a clear error. **After T54 verifies no `dependencies:` remain in test/scenario/doc fixtures**, REMOVE this validator and the `Dependencies` field reference. (Plan task T54 handles the removal.)

2. Add a new validator `validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, hooks RegistryHooks, res *ValidationResult)`:
   ```go
   func validateSubscribes(n TemplateNodeDef, base string, declared map[string]int, hooks RegistryHooks, res *ValidationResult) {
   	for i, s := range n.Subscribes {
   		sbase := fmt.Sprintf("%s.subscribes[%d]", base, i)
   		// Mutual exclusion: Node and Instance.
   		if s.Node == "" && !s.Instance {
   			res.Errors = append(res.Errors, ValidationError{
   				Path: sbase,
   				Msg:  "must declare either `node:` or `instance: true`",
   			})
   			continue
   		}
   		if s.Node != "" && s.Instance {
   			res.Errors = append(res.Errors, ValidationError{
   				Path: sbase,
   				Msg:  "`node:` and `instance: true` are mutually exclusive",
   			})
   			continue
   		}
   		// `node:` must reference a declared node-type in this template.
   		if s.Node != "" {
   			if _, ok := declared[s.Node]; !ok {
   				res.Errors = append(res.Errors, ValidationError{
   					Path: sbase + ".node",
   					Msg:  fmt.Sprintf("subscription `node: %q` does not reference a declared node", s.Node),
   				})
   				continue
   			}
   		}
   		// `on:` is required and must be a known topic kind.
   		switch s.On {
   		case spec.TopicKindState, spec.TopicKindAttribute, spec.TopicKindEvent:
   		default:
   			res.Errors = append(res.Errors, ValidationError{
   				Path: sbase + ".on",
   				Msg:  fmt.Sprintf("`on:` must be one of %q | %q | %q, got %q",
   					spec.TopicKindState, spec.TopicKindAttribute, spec.TopicKindEvent, s.On),
   			})
   			continue
   		}
   		// On == "event" requires `name:`.
   		if s.On == spec.TopicKindEvent && s.Name == "" {
   			res.Errors = append(res.Errors, ValidationError{
   				Path: sbase + ".name",
   				Msg:  "`on: event` requires `name:`",
   			})
   		}
   		// On != "state" forbids state-only filters.
   		if s.On != spec.TopicKindState {
   			if s.When != "" || s.Outcome != "" || s.ErrorClass != "" || s.Reason != "" {
   				res.Errors = append(res.Errors, ValidationError{
   					Path: sbase,
   					Msg:  "state-only filters (when/outcome/error_class/reason) require `on: state`",
   				})
   			}
   		}
   		// `when:` must be a valid node state.
   		if s.When != "" {
   			switch s.When {
   			case spec.NodeStateFresh, spec.NodeStateStale, spec.NodeStateRunning, spec.NodeStateFailed, spec.NodeStateParked:
   			default:
   				res.Errors = append(res.Errors, ValidationError{
   					Path: sbase + ".when",
   					Msg:  fmt.Sprintf("`when: %q` is not a valid node state", s.When),
   				})
   			}
   		}
   		// `frame:` must be in | next | "".
   		switch s.Frame {
   		case "", FrameIn, FrameNext:
   		default:
   			res.Errors = append(res.Errors, ValidationError{
   				Path: sbase + ".frame",
   				Msg:  fmt.Sprintf("`frame:` must be empty | %q | %q, got %q", FrameIn, FrameNext, s.Frame),
   			})
   		}
   		// Cross-checks against upstream executor capabilities (silent-skip if unreachable).
   		if s.Node != "" && s.On == spec.TopicKindEvent && hooks.ExecutorDeclaredEvents != nil {
   			// Find the upstream node's executor.
   			// (Locate by node type in spec.Nodes — pass spec in to the validator if it's not already accessible.)
   			// If executor declared, cross-check that s.Name appears in declared_events.
   			// Mirror the existing on_event validator's silent-skip semantics.
   		}
   	}
   }
   ```
   Wire `validateSubscribes` into the top-level `validate(n TemplateNodeDef, ...)` dispatcher next to `validateDependencies`.

3. Remove `validateOnEvent` (around line 871-940). The `on_event:` map no longer exists on `TemplateNodeDef`. Delete the function and its call site in the top-level dispatch.

4. Update `validateErrorTypes` (around line 213) to REJECT `action: invalidate` entries — under the new model, error-policy actions are `retry | give_up | pass`. Add an error message citing the migration table.

5. Update `validateOnAcquireUnavailable` / `validateOnExecutorComplete` / `validateOnExecutorErrored` (search for these in the file) to remove validation of the now-deleted `Invalidate` field. Keep `resolve` and `error_class` checks.

6. Wire `BuildSubscriptionEdges` into the validator's post-validation phase. After all per-node validators pass, call:
   ```go
   refs := extractSubstitutionRefs(spec)
   edgeMap := BuildSubscriptionEdges(spec, refs)
   // Store edgeMap on the ValidationResult or on the template row for runtime use.
   ```
   The storage shape on the template row needs to be persisted alongside the template's canonical bytes. **Option A:** add a `SubscriptionEdges json.RawMessage` column to `rimsky_templates`. **Option B:** derive the map on first use at the cache layer. The simpler choice is Option B given pre-v1 — compute at registration, cache in-memory keyed by template_hash. Document the choice in a comment; do not add a new column.

**Verification:**
- `go build ./graph/node/...` — passes.
- `go test ./graph/node/... -run TestValidate -count=1 -v` — existing validator tests will FAIL because they use `Dependencies` / `OnEvent`. T22 fixes those tests.

## T17. Template validator: substitution-ref name cross-check

**Files:**
- `graph/node/template_validator.go`

**Steps:**

1. After `extractSubstitutionRefs` runs (in T16's wiring), iterate the result: for each ref `(receiver, sender_node_type, topic_kind, name)`:
   - If `sender_node_type` is not a declared node in the template → validation error.
   - If `topic_kind == "attribute"` AND `name != ""` → check that the sender's `Attributes.Schema` has a `properties.<name>` entry. If not → validation error.
   - If `topic_kind == "event"` → cross-check against the sender's executor's `Capabilities.declared_events` via `hooks.ExecutorDeclaredEvents` (silent-skip when unreachable, mirroring today's `validateOnEvent` behavior).

**Verification:**
- `go build ./graph/node/...` — passes.

## T18. Cascade walk: pessimistic invalidate + settled-state drain

**Files:**
- `runtime/cascade_invalidate.go`
- `runtime/runner_terminal.go`

**Steps:**

1. In `runtime/cascade_invalidate.go`, the function that walks cascade downstream from an invalidated node currently uses `MarkStaleForCascade` plus the target list from `Nodes().ListDependents(...)`. The new walk uses the subscription-edge map cached at template registration. Replace the dependent-list lookup with a call into a new helper that resolves the cascade from the sender via the per-template subscription-edge map:
   ```go
   // After the sender's state transition is committed in tx, walk the
   // subscription-edge map for the sender's node-type, plus the
   // cross-cutting entries under key "":
   //   1. For "in-cascade" edges (Frame == "in"): mark each receiver
   //      stale + insert wait-set row in the current frame.
   //   2. For "next-cascade" edges (Frame == "next"): queue the
   //      stale-mark + wait-set insert for the next frame (see T20).
   // The "pessimistic invalidate" rule: ALL subscription edges from
   // the sender (regardless of filter compatibility with the actual
   // transition) trigger the stale-mark and wait-set insert. The
   // settled-state drain (below) handles the "filter didn't actually
   // match" case via idempotent re-fire.
   ```

2. In `runtime/runner_terminal.go::cascadeChildrenStaleInTx` (around line 273), the function today walks `Nodes().ListDependents` and stale-marks each. Rewrite to:
   - Look up the subscription-edge map for the sender's template + node-type.
   - For each `direct` scope edge with `Frame: "in"`: resolve the receiver-node-id from the receiver node-type via instance node lookup; `MarkStaleForCascade(receiver, frame_id)`; `WaitSet().Insert(...)`.
   - For each `instance` scope edge: same, but the receiver may be any node-type in the instance; iterate the instance's nodes matching the receiver-type filter.
   - For each `Frame: "next"` edge: see T20.

3. Add a new function `drainWaitSetOnSettled(ctx, args, tx, acq)` called wherever the sender reaches a settled state (`fresh`, `failed`, `parked`). It calls `WaitSet().DeleteBySender(frame_id, sender_id, tx)`. Wire this into:
   - Successful terminal-complete path (after `cascadeChildrenStaleInTx`).
   - Errored terminal path (where the node transitions to `failed`).
   - Park-terminal path (where the node transitions to `parked`).

4. The function previously named `cascadeChildrenStaleInTx` should be renamed `cascadeSubscribersStaleInTx` to reflect the new semantics; update all call sites in `runtime/`.

**Verification:**
- `go build ./runtime/...` — passes.
- `go test ./runtime/... -run TestCascade -count=1` — existing tests will fail because they use the dependencies-based cascade. T48 fixes scenario tests.

## T19. Cascade walk: drain on park / failed terminal

**Files:**
- `runtime/runner_terminal_park.go`
- `runtime/runner_terminal.go` (the failed branch — search for the `failed` state transition handling)

**Steps:**

1. In `runtime/runner_terminal_park.go`, after the node transitions to `parked` and the `cascadeChildrenStaleInTx` walk runs, call `drainWaitSetOnSettled(...)`. (T18 already added this — verify the call site in the park terminal handler.)
2. In `runtime/runner_terminal.go` failed-branch, same.

**Verification:**
- `go build ./runtime/...` — passes.
- `grep -n "drainWaitSetOnSettled\|WaitSet().DeleteBySender" runtime/` — three call sites (terminal-success, terminal-failed, terminal-park).

## T20. Frame:next deferred-invalidate queue carries subscription edges

**Files:**
- `graph/scheduler/scheduler.go` (or wherever the `frame: next` deferred-invalidate mechanism lives today — search for "frame: next" or `FrameNext` references in `runtime/` + `graph/scheduler/`)

**Steps:**

1. Today's `frame: next` invalidate discipline (per CLAUDE.md "Per-emit invalidate frame discipline ... Default empty → next") works for operator-API, error_types policy, lifecycle-handler invalidates. Locate the implementation. Likely a deferred-queue mechanism in the scheduler that runs at next-frame-open.
2. Extend the queue's payload shape to include `(receiver_node_id, sender_node_id, topic_kind, topic_filter, subscription_scope)` so subscription-cascade walks with `Frame: "next"` can use the same queue.
3. At next-frame-open, the queue is consumed: for each entry, mark the receiver stale in the new frame and insert a wait-set row with the new frame's `frame_id`.

**Verification:**
- `go build ./graph/scheduler/...` — passes.
- `go build ./runtime/...` — passes.

## T21. SweepReady eligibility predicate change

**Files:**
- `foundation/persistence/postgres/nodes.go`
- `foundation/persistence/sqlite/nodes.go`

**Steps:**

1. In `foundation/persistence/postgres/nodes.go::ListReadyForDispatch` (around line 136), the query joins against `n.dependencies` to verify all deps are fresh. Replace with the new wait-set-based predicate. The new query:
   ```sql
   SELECT ` + nodeCols + ` FROM rimsky_nodes n
    WHERE n.executor IS NOT NULL AND n.executor <> ''
      AND n.state = 'stale'
      AND n.id NOT IN (
        SELECT receiver_node_id FROM rimsky_wait_set
         WHERE frame_id = n.frame_id
      )
      AND NOT EXISTS (
        SELECT 1 FROM rimsky_node_runs x WHERE x.node_id = n.id
      )
    ORDER BY n.created_at ASC
   ```
   Note: the eligibility check filters by `frame_id = n.frame_id`, which assumes `rimsky_nodes` carries the current frame_id. Verify against the schema (line 99 of `001-baseline.sql` confirms `rimsky_nodes.frame_id` exists).
2. Apply the same change to `foundation/persistence/postgres/nodes.go::ListPureCascadeReady` (around line 165) — pure-cascade nodes also gate on the wait-set predicate.
3. Mirror both changes in `foundation/persistence/sqlite/nodes.go` (SQL is portable).
4. Remove the now-unused `dependencies` column reference if no other queries use it. (Likely still used elsewhere; do not drop the column.)

**Verification:**
- `cd foundation && go build ./persistence/...` — passes.
- `cd foundation && go test -run TestListReadyForDispatch ./persistence/postgres/... -count=1` — passes (test may need updating; T22 covers it).

## T22. Persistence-layer test updates

**Files:**
- `foundation/persistence/conformance/observability.go`
- `foundation/persistence/conformance/nodes_*.go`
- Any persistence test files that construct templates with `Dependencies:`

**Steps:**

1. Grep for `Dependencies:` and `dependencies:` under `foundation/persistence/`:
   ```sh
   rg 'Dependencies:' foundation/persistence/
   ```
2. Update each fixture's template construction to use `Subscribes` rather than `Dependencies`. For dispatch-eligibility tests specifically:
   - Where the test previously asserted "node A is ready iff dep B is fresh", change the setup to insert a wait-set row gating A on B; resolve B; assert A is now ready.

**Verification:**
- `cd foundation && go test ./persistence/conformance/... -count=1` — passes for SQLite.
- `cd foundation && go test ./persistence/postgres/... -count=1` — passes against Docker testcontainer.

## T23. Drop `Nodes.dependencies` column reads from runtime

**Files:**
- `foundation/persistence/postgres/nodes.go`
- `foundation/persistence/sqlite/nodes.go`
- `foundation/persistence/nodes.go`

**Steps:**

1. The `NodeRow` struct in `foundation/persistence/nodes.go` carries `Dependencies []shared.UUID` populated from the `dependencies` column. Under the new model the runtime no longer reads this for cascade purposes, but it may still be useful for retrospective debugging. **Pre-v1 break-freely:** remove the `Dependencies` field from `NodeRow` and from the SELECT column list in `nodeCols` (both adapters).
2. Drop the `dependencies` column reads from `Nodes().Get`, `Nodes().List`, etc. The column stays in the schema (don't drop it; keep for now as inert) but no code reads it.
3. Find and fix every caller in `runtime/` that reads `.Dependencies`. After T12's rename, only legitimate uses should remain (e.g. `loadSubscribedNodeAttributesByID` was using `nd.Dependencies` as a placeholder).
4. Replace the placeholder reads with a function that, given a node, returns the set of subscribed-to sender node IDs computed from the subscription-edge map. Implement this helper in `runtime/subscription_loaders.go` (new file):
   ```go
   // resolveSubscribedSenders returns the set of sender node-ids that
   // a receiver is subscribed to (either explicitly via Subscribes or
   // implicitly via substitution refs). Used to populate the Deps map
   // for substitution at dispatch.
   func resolveSubscribedSenders(ctx context.Context, args RunArgs, receiverNodeID shared.UUID, tx persistence.Tx) ([]shared.UUID, error) {
   	// Look up the receiver's template via instance.
   	// Read the cached subscription-edge map.
   	// Filter to edges where receiver matches our node-type.
   	// Translate sender_node_type back to sender_node_id via instance
   	// node lookup (Nodes().ListByInstanceAndType or similar).
   	// Return the set.
   }
   ```

**Verification:**
- `go build ./...` (from repo root) — passes.
- `grep -rn "\.Dependencies" runtime/ foundation/persistence/` — no remaining references in production code.

## T24. Drop `Nodes.dependencies` actual deletion from schema (pre-v1)

**Files:**
- `foundation/persistence/postgres/migrations/001-baseline.sql`
- `foundation/persistence/sqlite/migrations/001-baseline.sql`

**Steps:**

1. Locate the `rimsky_nodes` table definition. Remove the `dependencies UUID[] NOT NULL DEFAULT '{}'` column (or equivalent — verify exact line via grep) and any index that references it.
2. SQLite equivalent (likely `dependencies TEXT NOT NULL DEFAULT '[]'` or a serialized form).

**Verification:**
- `cd foundation && go build ./...` — passes.
- `grep -n "dependencies" foundation/persistence/postgres/migrations/001-baseline.sql` — no occurrences in the `rimsky_nodes` block.

## T25. Drop dependency-related node accessors

**Files:**
- `foundation/persistence/nodes.go`
- `foundation/persistence/postgres/nodes.go`
- `foundation/persistence/sqlite/nodes.go`

**Steps:**

1. Find any methods related to listing dependents or dependencies (`ListDependents`, `ListDependencies`, etc.) and remove them if they have no remaining callers post-T18.
2. Update the `Nodes` interface in `foundation/persistence/nodes.go` accordingly.

**Verification:**
- `go build ./...` — passes.

## T26. Proto change: `Park.reason` → `ParkReason` enum + `reason_note`

**Files:**
- `protocols/proto/v1/executor.proto`

**Steps:**

1. Locate the `message Park` block (around line 179 of `executor.proto`).
2. Above it, add the new enum definition:
   ```protobuf
   // ParkReason categorizes why an executor parked a node. Storage form
   // (col:rimsky_node_runs.parked_reason) is lower_snake_case derived
   // from the enum symbol (e.g. PARK_REASON_AWAITING_HUMAN →
   // awaiting_human). The same form is used on the diagnostics
   // endpoint, the rimsky-cli `parked list --reason=` flag, and the
   // Prometheus rimsky_parked_nodes_by_reason gauge label.
   //
   //	@concept: parked-state
   enum ParkReason {
     PARK_REASON_UNSPECIFIED    = 0;
     PARK_REASON_TIME_WAIT      = 1;
     PARK_REASON_SIGNAL_WAIT    = 2;
     PARK_REASON_AWAITING_HUMAN = 3;
     PARK_REASON_RETRY_BACKOFF  = 4;
   }
   ```
3. Rewrite the `message Park` block:
   ```protobuf
   message Park {
     // Typed reason. Stored as lower_snake_case text.
     ParkReason reason = 1;
     // Free-form human annotation. Inert in rimsky.
     string reason_note = 5;
     // Inert payload bytes; passed back via ResumeContext on resume.
     bytes payload = 2;
     // Optional wall-clock time at which SweepParkedNodes wakes the node.
     google.protobuf.Timestamp resume_at = 3;
     // Inert session token; passed back via ResumeContext on resume.
     string session_token = 4;
   }
   ```
   Note that field 1 changes wire type (length-delimited string → varint enum). Pre-v1 break-freely; the spec's Piece 2 section explicitly authorizes this.

**Verification:**
- `make proto-gen` — completes without error.
- `grep -n "ParkReason\|reason_note" protocols/proto/v1/gen/executor.pb.go` — type appears.

## T27. Proto bindings regeneration

**Files:**
- `protocols/proto/v1/gen/executor.pb.go` (and any other generated bindings)

**Steps:**

1. Run `make proto-gen`. This regenerates `protocols/proto/v1/gen/*.pb.go` from the `.proto` source.
2. Inspect the diff to confirm `Park.Reason` is now `ParkReason` (the Go enum type) and `Park.ReasonNote` (string) exists.

**Verification:**
- `cd protocols && go build ./...` — passes.

## T28. Runtime: park terminal handling uses enum

**Files:**
- `runtime/runner_terminal_park.go`
- `runtime/runner_dispatch.go`
- `runtime/callback.go`

**Steps:**

1. In `runtime/runner_terminal_park.go`, locate the type / struct carrying park terminal data (`ParkReason` field; see line 42 + 100). The struct field type was previously `string`; the Go proto generates `ParkReason` as a typed enum (an alias over `int32`). Update the local Go type to be the proto's `ParkReason`, and translate at the persistence boundary to `lower_snake_case` text:
   ```go
   import genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"

   // parkReasonStorageForm converts the proto enum to the snake_case
   // text stored in col:rimsky_node_runs.parked_reason.
   func parkReasonStorageForm(r genv1.ParkReason) string {
   	// Drop the PARK_REASON_ prefix and lower-case.
   	s := r.String()
   	const prefix = "PARK_REASON_"
   	if strings.HasPrefix(s, prefix) {
   		s = s[len(prefix):]
   	}
   	return strings.ToLower(s)
   }
   ```
2. Update the persistence write to call `parkReasonStorageForm(t.ParkReason)` and also write `t.ReasonNote` to `parked_reason_note`.
3. In `runtime/runner_dispatch.go::oneEvent` (or the equivalent — `grep -n "ParkReason" runtime/runner_dispatch.go` to find it), update the local `ParkReason string` field to `ParkReason genv1.ParkReason` and add a `ParkReasonNote string` field. Update the conversion at the wire boundary accordingly.
4. In `runtime/callback.go`, do the same for the async-callback `Park` body.

**Verification:**
- `go build ./runtime/...` — passes.

## T29. Persistence: `parked_reason_note` accessors

**Files:**
- `foundation/persistence/postgres/queue_park.go`
- `foundation/persistence/sqlite/queue_park.go`
- `foundation/persistence/node_runs.go`

**Steps:**

1. In `queue_park.go` (both adapters), every INSERT / UPDATE / SELECT that touches `parked_reason` should also touch `parked_reason_note`. Mirror the existing `parked_reason` handling. The new column is nullable, so `NULL`-safe handling at the Go side (`sql.NullString` or `*string`).
2. Update the row-type in `foundation/persistence/node_runs.go` to include `ParkedReasonNote *string` or `ParkedReasonNote sql.NullString`.

**Verification:**
- `cd foundation && go build ./persistence/...` — passes.
- `cd foundation && go test ./persistence/postgres/... -run TestParkedReason -count=1` — passes (test may need updating; T30 covers).

## T30. Persistence: parked-reason tests use enum form

**Files:**
- `foundation/persistence/postgres/queue_park_test.go` (find via grep)
- Any other test file exercising the `parked_reason` column

**Steps:**

1. Update tests to write storage-form snake_case strings (`"awaiting_human"`) and read them back.
2. Add test coverage for `parked_reason_note`.

**Verification:**
- `cd foundation && go test ./persistence/... -count=1` — passes.

## T31. Stub executor: emit enum

**Files:**
- `executors/stub/stub.go`

**Steps:**

1. Locate the `Park` emission (around line 324). Replace any free-form-string reason emission with the typed enum:
   ```go
   park := &genv1.Park{
   	Reason:     genv1.ParkReason_PARK_REASON_UNSPECIFIED, // or per the stub's TypeBuilder DSL
   	ReasonNote: "", // optional
   	// ... existing payload, resume_at, session_token
   }
   ```
2. Check `executors/stub/stubtest/` (the TypeBuilder DSL helper directory) for any `Park()` builder method. If it accepts a string reason, update it to accept `genv1.ParkReason` + optional `string` for note. Provide a default mapping if necessary so existing callers don't break in obvious ways.

**Verification:**
- `cd executors/stub && go build ./...` — passes.
- `go test ./executors/stub/... -count=1` — passes.

## T32. Subscription composition: `reason:` filter wiring

**Files:**
- `graph/node/template_validator.go`
- `runtime/cascade_invalidate.go` (or wherever subscription edges are evaluated)

**Steps:**

1. The `SubscriptionEntry.Reason` filter is already validated in T16 (it's only meaningful when `When == "parked"`). At cascade-walk-match time, the engine evaluates the filter against the sender's transition. For a sender parking with reason `awaiting_human`, the engine compares `parked_reason` against the subscription's `Reason` filter (case-insensitive, snake_case).
2. The pessimistic-invalidate rule (T18) inserts wait-set rows regardless of filter compatibility, so the `Reason` filter actually plays no role in invalidate-time gating. It's purely a discriminator at observation time. Verify the engine doesn't conditionally insert based on filter mismatch.
3. Document the `reason:` filter's actual semantics in `graph/node/subscription_edges.go`: it's a passive observation filter, not an invalidate gate. Update the file header comment.

**Verification:**
- `go build ./graph/node/... ./runtime/...` — passes.

## T33. CLI: `rimsky-cli parked list` subcommand

**Files:**
- New: `control/cli/parked.go`
- `control/cli/main.go` (or wherever subcommands are registered — search via `grep -rn "register\|cmd\." control/cli/`)

**Steps:**

1. Create `control/cli/parked.go` following the structure of an existing thin-client subcommand (mirror `control/cli/nodes.go` or `control/cli/health.go`).
2. Add the `parked` subcommand with one verb `list`:
   ```sh
   rimsky-cli parked list [--reason=<kind>] [--older-than=<dur>] [--instance=<uuid>]
   ```
3. Implementation: parse flags, build the HTTP query URL against `/admin/diagnostics/parked-nodes`, GET the JSON, format as a table with columns `instance`, `node_id`, `parked_at`, `resume_at`, `reason`, `reason_note`.
4. Add `parked` to the subcommand registration in the CLI's main entrypoint.

**Verification:**
- `go build ./cmd/rimsky-cli/...` — passes.
- `go test ./control/cli/... -count=1` — passes.

## T34. ControlAPI: `parked-nodes?reason=` enum validation

**Files:**
- `control/controlapi/admin_diagnostics.go`

**Steps:**

1. In `handleAdminParkedNodes` (around line 153), the `reason` query param is currently a free string passed through. Add validation: if non-empty, must be one of the known snake_case forms (`time_wait`, `signal_wait`, `awaiting_human`, `retry_backoff`, `unspecified`). Unknown values return HTTP 400 with the list of valid options.
2. The list comes from the proto's `ParkReason` enum. Use the `genv1.ParkReason_name` map and convert each value via the snake_case helper from T28.

**Verification:**
- `go build ./control/controlapi/...` — passes.
- `go test -run TestHandleAdminParkedNodes ./control/controlapi/... -count=1` — passes (add a 400-case test).

## T35. ControlAPI: `/admin/diagnostics/wait-sets` endpoint

**Files:**
- New: `control/controlapi/admin_waitset.go`
- `control/controlapi/admin_diagnostics.go` (to register the route)

**Steps:**

1. Create the new file following the structure of `admin_diagnostics.go`:
   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   package controlapi

   import (
   	"context"
   	"net/http"

   	"github.com/google/uuid"
   	"github.com/fallguyconsulting/rimsky/foundation/persistence"
   )

   // WaitSetEntry is one wait-set row surfaced via /admin/diagnostics/wait-sets.
   type WaitSetEntry struct {
   	FrameID            uuid.UUID `json:"frame_id"`
   	ReceiverNodeID     uuid.UUID `json:"receiver_node_id"`
   	SenderNodeID       uuid.UUID `json:"sender_node_id"`
   	TopicKind          string    `json:"topic_kind"`
   	SubscriptionScope  string    `json:"subscription_scope"`
   	TopicFilter        any       `json:"topic_filter,omitempty"`
   }

   type WaitSetResponse struct {
   	WaitSet []WaitSetEntry `json:"wait_set"`
   }

   func handleAdminWaitSets(deps AppDeps) http.HandlerFunc {
   	return func(w http.ResponseWriter, req *http.Request) {
   		frameStr := req.URL.Query().Get("frame")
   		nodeStr := req.URL.Query().Get("node")
   		if frameStr == "" {
   			writeError(w, errBadRequest("missing required ?frame= query param"))
   			return
   		}
   		frameID, err := uuid.Parse(frameStr)
   		if err != nil {
   			writeError(w, errBadRequest("invalid frame id"))
   			return
   		}
   		var receiver *uuid.UUID
   		if nodeStr != "" {
   			rid, err := uuid.Parse(nodeStr)
   			if err != nil {
   				writeError(w, errBadRequest("invalid node id"))
   				return
   			}
   			receiver = &rid
   		}
   		out := WaitSetResponse{WaitSet: []WaitSetEntry{}}
   		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
   			var rows []persistence.WaitSetRow
   			var err error
   			if receiver != nil {
   				rows, err = deps.Persist.Tables().WaitSet().ListForReceiver(ctx, frameID, *receiver, tx)
   			} else {
   				rows, err = deps.Persist.Tables().WaitSet().ListForFrame(ctx, frameID, tx)
   			}
   			if err != nil {
   				return err
   			}
   			for _, r := range rows {
   				entry := WaitSetEntry{
   					FrameID:           r.FrameID,
   					ReceiverNodeID:    r.ReceiverNodeID,
   					SenderNodeID:      r.SenderNodeID,
   					TopicKind:         r.TopicKind,
   					SubscriptionScope: r.SubscriptionScope,
   				}
   				if len(r.TopicFilter) > 0 {
   					var f any
   					_ = json.Unmarshal(r.TopicFilter, &f)
   					entry.TopicFilter = f
   				}
   				out.WaitSet = append(out.WaitSet, entry)
   			}
   			return nil
   		})
   		if err != nil {
   			writeError(w, err)
   			return
   		}
   		writeJSON(w, http.StatusOK, out)
   	}
   }
   ```

2. In `admin_diagnostics.go::routes(...)`, register: `r.Get("/admin/diagnostics/wait-sets", handleAdminWaitSets(deps))`.

**Verification:**
- `go build ./control/controlapi/...` — passes.
- `go test -run TestHandleAdminWaitSets ./control/controlapi/... -count=1` — write a fixture test and verify it passes.

## T36. claude-agent: `report_park` MCP tool

**Files:**
- `executors/claude-agent/src/internal-mcp-tools.ts`
- `executors/claude-agent/src/internal-mcp-server.ts`
- `executors/claude-agent/src/server.ts`
- `executors/claude-agent/src/agent-run.ts`

**Steps:**

1. In `internal-mcp-tools.ts`, alongside the existing `report_complete` and `report_blocked` definitions, add a `report_park` tool descriptor:
   ```ts
   {
     name: "report_park",
     description: "Park the dispatch. The supervisor pauses the node until resume_at elapses or an invalidate wakes it.",
     inputSchema: {
       type: "object",
       properties: {
         reason: {
           type: "string",
           enum: ["time_wait", "signal_wait", "awaiting_human", "retry_backoff"],
           description: "Typed park reason. The agent must pick one.",
         },
         reason_note: {
           type: "string",
           description: "Optional human-readable annotation.",
         },
         resume_at: {
           type: "string",
           format: "date-time",
           description: "Optional ISO 8601 timestamp at which to wake. Absent means signal-only.",
         },
       },
       required: ["reason"],
     },
   }
   ```
2. In `internal-mcp-server.ts`, add the handler. Pattern follows `report_complete` and `report_blocked`. On invocation:
   - Validate `reason` against the allowed set (reject `unspecified`).
   - Resolve the per-dispatch outcome promise with `{ kind: "park_requested", reason: <enum-typed-string>, reasonNote: <string>, payload: new Uint8Array(), resumeAt: <Date>, sessionToken: runId }`.
3. In `agent-run.ts`, the outcome promise type already supports `kind: "park_requested"` (per the rate-limit detection path). Extend the discriminated-union type with `reason` and `reasonNote` fields. The agent-emitted park and the rate-limit-detected park both flow through the same outcome resolution.
4. In `server.ts::handleOutcome` (the path that translates the outcome into a gRPC `Park` terminal), pass through `reason` and `reasonNote` from the outcome to the proto message.
5. Update the agent-run.ts rate-limit path to set `reason: "time_wait"` and `reasonNote: "claude rate-limit detected; resume at ${signalRL.resumeAt}"` (or similar).

**Verification:**
- `cd executors/claude-agent && npm install && npm run build` — passes.

## T37. claude-agent: tests for `report_park`

**Files:**
- `executors/claude-agent/src/server.test.ts`

**Steps:**

1. Add tests covering:
   - `report_park` with a valid `reason` resolves the outcome promise with a `park_requested` shape.
   - `report_park` with `reason: "unspecified"` is rejected at the MCP layer (400 / schema validation failure).
   - `report_park` and rate-limit-detection coexist: whichever resolves first wins.
   - The translated Park gRPC message carries the typed enum (`ParkReason_PARK_REASON_AWAITING_HUMAN` for input `"awaiting_human"`) and the `reason_note` string.

**Verification:**
- `cd executors/claude-agent && npm test` — all tests pass.

## T38. Concept doc: `subscription.md` (new)

**Files:**
- New: `.ok-planner/design/concepts/subscription.md`

**Steps:**

1. Create the file following the template established by other concept docs (read `.ok-planner/design/concepts/cascade.md` for an example). Frontmatter:
   ```yaml
   ---
   concept: subscription
   status: as-is
   aliases: []
   references:
     - ../../specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
   ---
   ```
2. Sections required: `# Subscription`, `## What it is`, `## Purpose`, `## Boundaries`, `## Invariants`, `## Aliases and historical names`, `## Open within this concept`.
3. Body content (draft from the spec):
   - **What it is:** an impactee-side declaration of "fire me when this upstream topic transitions." Three topic kinds: `state`, `attribute`, `event`. Scope: per-node (`node:`) or cross-cutting (`instance: true`). Optional filters (`when`, `outcome`, `error_class`, `reason`, `name`). Frame modifier (`in` | `next`). Substitution refs in attribute schemas auto-subscribe.
   - **Purpose:** decouples reactive coupling from compound `dependencies:` declarations. Read access, cascade subscription, and eligibility gating become independent primitives. Send-side `invalidate.targets` retire.
   - **Boundaries:** owns the per-template inverse-edge map (computed at registration), the topic taxonomy, and the auto-subscribe rule. Does NOT own: the cascade walk itself (lives in `cascade`); the wait-set ledger (lives in `wait-set`); the eligibility predicate (lives in the persistence-layer SweepReady query).
   - **Invariants:** subscriptions validate against upstream's declared output topology at registration when the upstream executor is reachable via the observability handshake (silent-skip otherwise, mirroring today's `validateOnEvent` semantics). Substitution refs auto-subscribe — no orphan reads.

**Verification:**
- File exists at the named path with required sections.
- `cat .ok-planner/design/concepts/subscription.md | head -20` shows the frontmatter.

## T39. Concept doc: `wait-set.md` (new)

**Files:**
- New: `.ok-planner/design/concepts/wait-set.md`

**Steps:**

1. Mirror T38's structure. Frontmatter slug `wait-set`. Reference the same spec path.
2. Body:
   - **What it is:** per-frame ledger (`table:rimsky_wait_set`) that records "receiver R is waiting for sender S in frame F under (topic_kind, subscription_scope, topic_filter)." Cascade walks insert rows; settled-state drain deletes them.
   - **Purpose:** derives dispatch eligibility from cascade history without requiring a pre-declared dependency list. Decouples cascade semantics from eligibility semantics.
   - **Boundaries:** owns the per-frame ledger schema, the insert-on-cascade-walk rule, the bulk-delete-on-settle rule, the eligibility predicate. Does NOT own: subscription declaration (lives in `subscription`); cascade walk logic (lives in `cascade`); frame lifecycle (lives in `frame`).
   - **Invariants:** rows live only within their `frame_id`'s scope (ON DELETE CASCADE from `rimsky_frames`); a stale receiver is eligible iff its wait-set is empty for the current frame; bulk-delete on sender resolution covers every topic kind uniformly (idempotent re-fire when filter didn't actually match).

**Verification:**
- File exists.

## T40. Concept doc retirement: `on-event-handler.md`

**Files:**
- `.ok-planner/design/concepts/on-event-handler.md`
- New directory: `.ok-planner/design/concepts/_retired/`

**Steps:**

1. Create `.ok-planner/design/concepts/_retired/` directory.
2. Move `.ok-planner/design/concepts/on-event-handler.md` to `.ok-planner/design/concepts/_retired/on-event-handler.md`.
3. Edit the moved file: at the top, after the frontmatter, add a tombstone block:
   ```markdown
   > **Retired** by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The `on_event:` map disappears; subscription-to-event (`subscribes: [{node: X, on: event, name: Y}]`) replaces both the substitution path (already supported via `{{nodes.X.event.Y}}`) and the invalidate-downstream path (the receiver's subscription wakes it automatically via cascade). See the spec's Piece 1 migration table for the rewrite shape.
   ```

**Verification:**
- `ls .ok-planner/design/concepts/_retired/on-event-handler.md` exists.
- `ls .ok-planner/design/concepts/on-event-handler.md` does NOT exist.

## T41. Concept-doc mutations: cascade, invalidate, node, lifecycle-handler, error-policy

**Files:**
- `.ok-planner/design/concepts/cascade.md`
- `.ok-planner/design/concepts/invalidate.md`
- `.ok-planner/design/concepts/node.md`
- `.ok-planner/design/concepts/lifecycle-handler.md`
- `.ok-planner/design/concepts/error-policy.md`

**Steps:**

For each file:

1. **`cascade.md`**: Boundaries section — add language: "The cascade walk's downstream traversal is driven by the per-template subscription-edge inverse map (see `concept:subscription`), not by a static dependency graph. Wait-set rows are inserted on every cascade-walk match (pessimistic invalidate); the bulk-delete-on-settled-state rule (see `concept:wait-set`) drains them as senders resolve." Invariants section — add: "Eligibility = state=stale AND wait-set is empty for the current frame (predicate evaluated in the persistence-layer SweepReady query)." Notes section — append entry citing the spec.

2. **`invalidate.md`**: emitter list updated. The current text likely names "operator API, error-types policy, lifecycle handler" as emitters. Update to: "operator API, scheduler tick, cascade walk from subscription-edge matches. The error-types policy's `action: invalidate` and lifecycle-handler `invalidate.targets:` are retired; their effects are now declared as receiver-side subscriptions." Notes entry cites the spec.

3. **`node.md`**: Boundaries — `dependencies:` retired; `subscribes:` introduced (`see concept:subscription`); substitution refs auto-subscribe. Invariants stay otherwise. Notes entry.

4. **`lifecycle-handler.md`**: Boundaries — the three lifecycle slots (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored`) lose their `invalidate.targets:` clauses; `resolve` and `error_class` stay. The cross-reference to `concept:on-event-handler` is dropped (that concept is retired). The concept reduces to "three lifecycle slots with `resolve` + `error_class`." Notes entry.

5. **`error-policy.md`**: Boundaries — `action: invalidate` retires; `action: retry | give_up | pass` stay. The retry-loop cap stays. Notes entry.

**Verification:**
- `grep -n "Notes" .ok-planner/design/concepts/cascade.md .ok-planner/design/concepts/invalidate.md .ok-planner/design/concepts/node.md .ok-planner/design/concepts/lifecycle-handler.md .ok-planner/design/concepts/error-policy.md` — each file has a Notes section with a new entry.

## T42. Concept-doc mutations: named-event, frame, last-outcome, parked-state, executor, claim-producer

**Files:**
- `.ok-planner/design/concepts/named-event.md`
- `.ok-planner/design/concepts/frame.md`
- `.ok-planner/design/concepts/last-outcome.md`
- `.ok-planner/design/concepts/parked-state.md`
- `.ok-planner/design/concepts/executor.md`
- `.ok-planner/design/concepts/claim-producer.md`

**Steps:**

For each file:

1. **`named-event.md`**: Boundaries — consumption paths updated. The two paths today are substitution + on_event-handler-invalidate; under the new model: substitution (unchanged) + subscription-to-event (`subscribes: [{node, on: event, name}]`). The `concept:on-event-handler` reference is dropped. Notes entry.

2. **`frame.md`**: Notes entry: "`rimsky_wait_set` rows are cascade-deleted on frame close via `ON DELETE CASCADE` from `rimsky_frames(frame_id)`."

3. **`last-outcome.md`**: Notes entry: "Values become filter predicates on `state` subscriptions (`outcome:` filter). Subscription validation cross-checks `outcome:` filter against the enum."

4. **`parked-state.md`**: Notes entry: "`parked_reason` is now typed (proto enum `ParkReason`); the column stores the snake_case form. New `parked_reason_note` column carries the free-form human annotation. See spec Piece 2."

5. **`executor.md`**: Notes entry: "`Park.reason` typed as `ParkReason` enum on the wire; new `reason_note` field carries human annotation. The Notes section already references the prior Snooze→Park rename; this entry sits alongside it."

6. **`claim-producer.md`**: Notes entry: "Atomic-staging pattern documented at `docs/agents/examples/atomic-staging.md` with a reference filesystem implementation under `examples/atomic-staging-fs-producer/`. Pattern is producer-side discipline; no rimsky-level surface change."

**Verification:**
- `grep -l "2026-05-14" .ok-planner/design/concepts/*.md` — at least eleven files cite the spec.

## T43. Concept catalog TOC regeneration

**Files:**
- `.ok-planner/design/concepts.md`

**Steps:**

1. The file is auto-generated per the file header. Locate the generator: probably an ok-planner skill or a script under the marketplace tree. If a generator exists at the project, run it. Otherwise:
2. Manually edit the TOC: remove the `on-event-handler` entry; add entries for `subscription` and `wait-set` in alphabetical order. Use the one-sentence definitions from each new concept doc's "What it is" section's opening sentence.

**Verification:**
- `grep -n "subscription\|wait-set" .ok-planner/design/concepts.md` — both appear.
- `grep -n "on-event-handler" .ok-planner/design/concepts.md` — does not appear in the main list (it's retired).

## T44. Resolved tensions: write four files

**Files:**
- New: `.ok-planner/design/tensions/_resolved/dependency-overloaded-bundle.md`
- New: `.ok-planner/design/tensions/_resolved/subscription-implies-cascade-dependency.md`
- New: `.ok-planner/design/tensions/_resolved/rimsky-not-a-dag-vocabulary.md`
- New: `.ok-planner/design/tensions/_resolved/send-vs-subscribe-asymmetry.md`

**Steps:**

1. Look at an existing resolved tension under `.ok-planner/design/tensions/_resolved/` for the template shape (`grep -l "resolution" .ok-planner/design/tensions/_resolved/ | head -1`).
2. For each new file, write:
   ```markdown
   ---
   tension: <slug>
   category: <category>
   status: resolved
   affects:
     - subscription
     - cascade
     - node
     - wait-set
   ---

   # <Human title>

   ## What was muddy

   <One paragraph from the spec's Tensions resolved section.>

   ## Why it mattered

   <One paragraph.>

   ## Resolution

   Resolved by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. <One paragraph summarizing the resolution from the spec.>
   ```
3. Specifically:
   - `dependency-overloaded-bundle.md` — the `dependencies:` block bundled read-access + cascade-subscription + eligibility-gate; decomposed into substitution refs + subscriptions + wait-set.
   - `subscription-implies-cascade-dependency.md` — attribute substitution required `dependencies:`, event substitution didn't; both now auto-subscribe.
   - `rimsky-not-a-dag-vocabulary.md` — surface vocabulary treated rimsky as a DAG; resolution acknowledges reactive node graph with bidirectional message flow.
   - `send-vs-subscribe-asymmetry.md` — push-style `invalidate.targets` coexisted with pull-style `dependencies:`; send-style retired across the lifecycle-handler family.

**Verification:**
- All four files exist under `.ok-planner/design/tensions/_resolved/`.

## T45. Scenario tests: pattern-based migration plan

**Files:**
- Every `*.go` test file under `test/scenarios/`
- `graph/scenario/harness.go`
- `test/smoke/fixtures/template.yml`

**Pattern for migration** (the implementer applies this pattern to every affected file; the verification is the full `go test` pass):

For each file containing `Dependencies:` or `OnEvent:` in template construction, or `Invalidate.Targets` in handler declarations, or `action: invalidate` in error-types blocks:

1. **`Dependencies: []string{"X"}`** → REMOVE the field; add a `Subscribes: []spec.SubscriptionEntry{...}` field with the receiver-side subscriptions. Use substitution refs in `Attributes.Schema` where the receiver reads upstream data (which auto-subscribes via T14's parser).
2. **`OnEvent: map[string]spec.EventHandler{"X": {Invalidate: ...}}`** on emitter A → ADD a `Subscribes: [{Node: "A", On: "event", Name: "X"}]` to each impactee node B.
3. **`OnExecutorComplete.Invalidate.Targets = []string{"B"}`** on A → ADD `Subscribes: [{Node: "A", On: "state", When: "fresh"}]` to B.
4. **`OnExecutorErrored.Invalidate.Targets = []string{"B"}`** on A → ADD `Subscribes: [{Node: "A", On: "state", When: "failed"}]` to B (with optional `ErrorClass` filter).
5. **`OnAcquireUnavailable.Invalidate.Targets = []string{"B"}`** on A → ADD `Subscribes: [{Node: "A", On: "state", When: "failed", ErrorClass: "acquire_unavailable"}]` to B (modulo error-class naming used in the codebase — grep for the existing string).
6. **`error_types: { class: { policy: [{action: invalidate, targets: [B]}] } }`** on A → REMOVE the `action: invalidate` entry; ADD `Subscribes: [{Node: "A", On: "state", When: "failed", ErrorClass: <class>}]` to B (or `instance: true` cross-cutting if it should fire for any node's error of that class).
7. **`{{deps.X.Y}}`** substitution → REPLACE with `{{nodes.X.attribute.Y}}`.
8. **`Dependencies: []string{}` (empty)** → REMOVE the field entirely; if the test then expects "no upstream gating," the new model gives the same behavior (empty wait-set = eligible immediately).

The migration is mechanical per-file. The implementer should apply the pattern, then run the test, then iterate on any failures.

**Files to migrate** (grep result from `rg -l 'Dependencies:|OnEvent:|deps\.|invalidate.*targets' test/ graph/scenario/ test/smoke/fixtures/`):

The implementer runs the grep first to enumerate the precise list, then applies the pattern to each.

**Verification:**
- After ALL files in the list are migrated:
  - `rg 'Dependencies:|OnEvent:|deps\.|action: invalidate' test/ graph/scenario/ test/smoke/fixtures/` — no remaining occurrences.
  - `go test ./test/scenarios/... -count=1` — passes (requires Docker for testcontainers).

## T46. Scenario test: new wait-set scenario coverage

**Files:**
- New: `test/scenarios/subscription_cascade_test.go`

**Steps:**

1. Add new scenario tests covering the wait-set semantics not previously exercised:
   - **Multiple invalidator drain**: receiver R with substitution refs to A, B, C. All three are invalidated in one frame. R's wait-set has three rows. R dispatches only after A AND B AND C resolve. Verify wait-set is empty at receiver's dispatch.
   - **Conditional fan-in (the dissolved barrier problem)**: `intake → spine` always; `optional_check_1` conditionally invalidated by intake. `finalize` reads from spine + both optional checks. In a frame where only `optional_check_1` was invalidated, `finalize`'s wait-set contains spine + optional_check_1 (not optional_check_2). `finalize` dispatches after both resolve.
   - **Cross-cutting subscription**: a cleanup-on-failure node with `subscribes: [{instance: true, on: state, when: failed, error_class: rate_limited, frame: next}]`. When any node in the instance fails with `rate_limited`, the cleanup node fires in the next frame.
   - **Frame-end cascade cleanup**: assert that `rimsky_wait_set` rows for the closed frame are gone post-close (via ON DELETE CASCADE).
   - **`frame: next` loop convergence**: two mutually-subscribed nodes with `frame: next` modifiers; verify the cycle defers across frames and the instance eventually converges.
   - **Eligibility respects multiple senders (regression test for the "single-invalidator assumption" bug class)**: a receiver with 5 subscribed senders; first 4 resolve; receiver is still gated by sender 5; sender 5 resolves; receiver dispatches.

2. Mirror the structure of existing scenario tests under `test/scenarios/` (e.g. `cascade_invalidate_test.go`).

**Verification:**
- `go test -run TestSubscriptionCascade ./test/scenarios/ -count=1` — all sub-tests pass.

## T47. Smoke fixture template migration

**Files:**
- `test/smoke/fixtures/template.yml`

**Steps:**

1. Apply the T45 pattern to the YAML smoke fixture:
   - `dependencies: [claim-topic]` on `scope` → REMOVE; `{{deps.claim-topic.area}}` is in `scope.attributes` and auto-subscribes via the new substitution grammar `{{nodes.claim-topic.attribute.area}}`.
   - `dependencies: [claim-topic, scope]` on `draft` → REMOVE; substitution refs auto-subscribe.
   - `dependencies: [claim-topic, scope, draft]` on `review` → REMOVE.
   - All `{{deps.X.Y}}` → `{{nodes.X.attribute.Y}}` (also in the `selector:` strings — check those too).
   - `error_types: { review_rejected: { policy: [{action: discard_then_retry, count: 2}, {action: invalidate, targets: [scope]}, {action: give_up}] } }` on `scope` → REMOVE the `action: invalidate` entry. The previous semantics ("invalidate self on retry exhaustion") are reframed as: scope subscribes to its own `state, when: failed, error_class: review_rejected` and... actually self-invalidate-on-error retires entirely under the new model (per the spec's "self-invalidate retires" rule). The smoke fixture's intent — "retry twice then give up" — survives because the retry policy already handles it: keep `[{action: discard_then_retry, count: 2}, {action: give_up}]`. The middle `invalidate` entry was redundant given the retry cap.

2. Update the smoke test code itself (`stores_redesign_smoke_test.go` or whatever the smoke test entry point is) if it programmatically constructs the equivalent template with the old fields.

**Verification:**
- `grep -n "dependencies\|deps\.\|action: invalidate" test/smoke/fixtures/template.yml` — no occurrences.
- The smoke test still passes the YAML→JSON conversion at deploy time (run: `go test ./test/smoke/... -count=1`).

## T48. Runtime tests: cascade + named-event handler retirement

**Files:**
- `runtime/cascade_invalidate_test.go`
- `runtime/runner_named_events.go`
- Any `runtime/` tests touching `fireOnEventHandler` or `on_event` dispatch

**Steps:**

1. In `runtime/runner_named_events.go`, the `fireOnEventHandler` function (around line 124) reads `acq.NodeDef.OnEvent` which no longer exists. Delete the function. Update `processNamedEvents` (around line 39) to remove the call site.
2. The remaining purpose of `processNamedEvents` is persistence-only: write each NamedEvent to `rimsky_node_events`. The cascade walk picks up the emission via the new mechanism: when the emitter terminals (transitions out of running), the cascade walk evaluates `on: event` subscription edges against the emitter's emitted events in this run. Add a new function `walkEventSubscriptionsForEmitter(ctx, args, tx, acq, eventNames []string)` that, for each event name the emitter emitted in this run, walks subscription edges with `topic_kind = "event"` and `name = <event>` from the emitter, inserts wait-set rows + stale-marks. Call this from `processNamedEvents` after persistence completes.
3. Update `runtime/cascade_invalidate_test.go` — rewrite tests to assert wait-set behavior rather than the old cascade-from-dependencies behavior. The implementer should remove tests that exercised retired behaviors and add tests for the new semantics (mirroring the patterns in T46).

**Verification:**
- `go build ./runtime/...` — passes.
- `go test ./runtime/... -count=1` — passes.
- `grep -n "fireOnEventHandler\|OnEvent" runtime/` — no occurrences in production code.

## T49. Atomic-staging: pattern doc

**Files:**
- New: `docs/agents/examples/atomic-staging.md`

**Steps:**

1. Create the file (Markdown, citing-grammar does NOT apply to public docs). Structure:
   - **`# Atomic-staging pattern for custom ClaimProducers`**
   - **`## Why this pattern exists`** — paragraph from spec Piece 3 "What this is" + "Why this is worth a worked example."
   - **`## The pattern`** — 4-verb mapping. For each verb (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`), one subsection covering the responsibilities + a per-substrate example (filesystem POSIX rename, Postgres schema swap, S3 prefix copy, Iceberg branch fast-forward).
   - **`## Atomicity caveats by substrate`** — substrate-by-substrate honest accounting (atomic on POSIX rename + Iceberg + Postgres single-tx; non-atomic on S3 copy+delete; substrate-dependent on BigQuery; incoherent on streaming substrates).
   - **`## Held-subgraph integration`** — explains how `inherits: [{claim: <alias>}]` lets verify nodes work against staging; all-success → Commit; any-failure → Abandon.
   - **`## Concurrent stagers and orphan handling`** — rimsky's claim-handle gate serializes byte-equal scope; the producer needs a sweep loop for leaked staging from crashed runs; example sweep cadence + TTL.
   - **`## Worked example: filesystem`** — pointer to `examples/atomic-staging-fs-producer/`; brief overview of the binary's structure and the example template.
2. Cross-reference `docs/concepts/claim-producer-fs-store.md` (the bundled `pop_and_move` shape — related but queue-shaped, not staging-shaped).

**Verification:**
- File exists.

## T50. Atomic-staging: reference producer binary structure

**Files:**
- New: `examples/atomic-staging-fs-producer/cmd/main.go`
- New: `examples/atomic-staging-fs-producer/server/server.go`
- New: `examples/atomic-staging-fs-producer/store/store.go`
- New: `examples/atomic-staging-fs-producer/sweep/sweep.go`
- New: `examples/atomic-staging-fs-producer/README.md`
- New: `examples/atomic-staging-fs-producer/template.yaml`

**Steps:**

1. The new `examples/` directory does not exist yet at the repo root. Create it.
2. Initialize the Go module for the example:
   ```sh
   mkdir -p examples/atomic-staging-fs-producer
   cd examples/atomic-staging-fs-producer
   ```
   The example does not need its own `go.mod` if it lives within the root Go workspace and depends only on the protocols module. Verify the workspace layout via `cat go.work`; if needed, add `./examples/atomic-staging-fs-producer` to the workspace.
3. `cmd/main.go`: `package main` with a `func main()` that:
   - Reads config from env (`RIMSKY_ATOMIC_STAGING_ROOT`, `RIMSKY_POSTGRES_DSN`, `RIMSKY_LISTEN_ADDR`).
   - Constructs a `*store.Store` rooted at the configured directory.
   - Constructs a `*server.Server` wrapping the store.
   - Constructs a sweep loop running every `RIMSKY_SWEEP_INTERVAL` (default 5m) with a `staging_ttl` (default 24h).
   - Registers the server as a gRPC ClaimProducer service and serves on `RIMSKY_LISTEN_ADDR`.
4. `server/server.go`: implements `genv1.ClaimProducerServer` (the gRPC interface from `protocols/proto/v1/gen/`). Four methods + `Capabilities`. Each method delegates to the store with translation between the proto wire shape and the store's Go types.
5. `store/store.go`: the four-verb logic.
   - `Open(scope, claim_id, intent)`: creates `staging/<scope>/<claim_id>/`. Records `(claim_id, staging_path, canonical_path)` in a small SQLite file `producer_state.db` (use `modernc.org/sqlite`) so the sweep loop can find leaked dirs.
   - `Commit(claim_id)`: two-rename atomic swap. `mv canonical/<scope> canonical/<scope>._old`; `mv staging/<scope>/<claim_id> canonical/<scope>`; `rm -rf canonical/<scope>._old`.
   - `Abandon(claim_id)`: `rm -rf staging/<scope>/<claim_id>`.
   - `Release(claim_id)`: no-op for `r`; equivalent to `Abandon` for `rw` that never committed.
   - `Capabilities()`: declares `protocols: [claim_producer]`, `write_semantics_envelope: [staged_async]`, scope-conflict matrix.
6. `sweep/sweep.go`: periodic loop. Reads the `rimsky_claim_handles` table over Postgres (via `RIMSKY_POSTGRES_DSN`). For each entry in the store's `producer_state.db` whose `claim_id` isn't in the live handle set AND was created more than `staging_ttl` ago, calls `Abandon` to drop it.
7. `template.yaml`: worked example template using the new subscription syntax:
   ```yaml
   name: atomic-staging-example
   version: "1.0"
   frame_resolution_mode: serial_queue
   nodes:
     - type: stage-data
       executor: http-node
       stores:
         - { name: atomic-staging-fs, alias: target, selector: my-scope, intent: rw }
       userdata: { url: "http://my-loader.internal:8080/load" }
       attributes:
         schema:
           type: object
           properties:
             staging_path: { type: string, source: "{{claim.target.address}}" }
           required: [staging_path]

     - type: verify-staged
       executor: http-node
       inherits:
         - { claim: target }
       userdata: { url: "http://my-checks.internal:8080/verify-shape" }
       attributes:
         schema:
           type: object
           properties:
             staging_path: { type: string, source: "{{nodes.stage-data.attribute.staging_path}}" }
           required: [staging_path]

     - type: verify-staged-domain
       executor: http-node
       inherits:
         - { claim: target }
       userdata: { url: "http://my-checks.internal:8080/verify-domain" }
       attributes:
         schema:
           type: object
           properties:
             staging_path: { type: string, source: "{{nodes.stage-data.attribute.staging_path}}" }
           required: [staging_path]
   ```
8. `README.md`: how to run, what it demonstrates. Pointers to the pattern doc + spec.

**Verification:**
- `go build ./examples/atomic-staging-fs-producer/...` — passes (with workspace updated to include the example).
- `cd examples/atomic-staging-fs-producer && go test ./... -count=1` — passes if unit tests are added; otherwise build-only.

## T51. Atomic-staging: conformance run

**Files:**
- No new files; uses `cmd/rimsky-claim-producer-conformance`

**Steps:**

1. The conformance probe binary exercises any ClaimProducer endpoint against rimsky's protocol expectations. Run it against the new example:
   ```sh
   go run ./cmd/rimsky-claim-producer-conformance --endpoint localhost:8090 --transport grpc
   ```
2. Conformance: the binary boots the example producer (or assumes it's running on the endpoint) and runs the standard suite — Open / Commit / Abandon / Release lifecycle, byte-equal scope conflict, Capabilities() response shape.

**Verification:**
- Manual: requires the producer to be running locally. Skip in automated test sweep; include as a manual check in the final section.

## T52. Sweep-loop unit test

**Files:**
- New: `examples/atomic-staging-fs-producer/sweep/sweep_test.go`

**Steps:**

1. Mock the `rimsky_claim_handles` query (use an in-memory table fixture or stub the DB).
2. Test cases:
   - Alive handle's staging directory preserved.
   - Old leaked staging (claim_id not in live set, mtime > TTL) dropped.
   - Recent leaked staging (claim_id not in live set, mtime < TTL) preserved.

**Verification:**
- `cd examples/atomic-staging-fs-producer && go test ./sweep/... -count=1` — passes.

## T53. Dashboard parked-reason view (TS — bounded scope)

**Files:**
- `dashboards/rimsky-dashboard/src/...` (TypeScript components — the dashboard layout is in this directory)

**Steps:**

1. Read the dashboard's current parked-nodes view (likely a component under `dashboards/rimsky-dashboard/src/`). If there is no parked-nodes view today, the dashboard scope is to ADD one.
2. The view consumes `/admin/diagnostics/parked-nodes?reason=<kind>` (existing endpoint). Group by `reason`. Render `awaiting_human` rows with operator-attention styling (high contrast, visible icon); other reasons render uniformly.
3. The dashboard's frontend technology is TypeScript / Vite. Tests are written in vitest if the dashboard has any (check `dashboards/rimsky-dashboard/tests/`).

**Verification:**
- `cd dashboards/rimsky-dashboard && npm install && npm run build` — passes.
- `cd dashboards/rimsky-dashboard && npm test` — passes (if tests exist).

## T54. Final cleanup: remove `dependencies:` rejection validator

**Files:**
- `graph/node/template_validator.go`
- `foundation/spec/template.go`

**Steps:**

1. After T45 + T47 are confirmed by grep ("no remaining `dependencies:` occurrences anywhere in the codebase"), REMOVE the `validateDependencies` function added in T16. The `Dependencies` field is already removed from `TemplateNodeDef` (T3), so any new template author trying to use `dependencies:` will get a YAML-level unknown-field error from the parser, which is sufficient.
2. Remove the call site from the validator dispatch.

**Verification:**
- `rg 'dependencies:' foundation/ graph/ runtime/ control/ test/ executors/ docs/` — only matches inside concept-doc body text describing retirement (acceptable).
- `go build ./...` — passes.
- `make lint` — passes.

## T55. Doc snippets: migrate `docs/concepts/` + `docs/agents/examples/`

**Files:**
- `docs/concepts/*.md`
- `docs/agents/examples/*.md`

**Steps:**

1. For each file under `docs/concepts/` and `docs/agents/examples/` that contains template YAML snippets:
   - Replace `dependencies: [...]` with `subscribes: [...]` (using the migration pattern from T45).
   - Replace `{{deps.X.Y}}` with `{{nodes.X.attribute.Y}}`.
   - Remove send-side `invalidate.targets:` clauses; document the receiver-side subscription form instead.
2. Update prose where it describes "dependencies" as a concept; reframe as "subscriptions" per the new model.

**Verification:**
- `rg 'dependencies:|deps\.\|invalidate.*targets|action: invalidate' docs/concepts/ docs/agents/examples/` — no matches (or only matches inside historical-context paragraphs that are acceptable).

## T56. Full build + test sweep

**Files:**
- All

**Steps:**

1. From the repo root:
   ```sh
   make build-all
   make test-all
   make lint
   ```
2. Plus the TS executor:
   ```sh
   cd executors/claude-agent && npm install && npm test && npm run build
   ```
3. Plus race tests on the cascade-walk + wait-set discipline:
   ```sh
   go test ./foundation/persistence/postgres/... ./runtime/... ./graph/scheduler/... -race -count=3
   ```

**Verification:**
- All commands pass.

## T57. Update CHANGELOG + feature-index

**Files:**
- `CHANGELOG.md`
- `feature-index.md` (if it exists at the repo root)

**Steps:**

1. Append a `## Unreleased` entry to `CHANGELOG.md` describing the cycle:
   - **Subscription-cascade resolution**: `dependencies:` retires; `subscribes:` introduced; substitution refs auto-subscribe; new `rimsky_wait_set` ledger drives eligibility; lifecycle-handler `invalidate.targets` and `error_types: action: invalidate` retired; `on_event:` map retired; substitution grammar `deps.X.Y` → `nodes.X.attribute.Y`.
   - **`parked_reason` typed**: `Park.reason` → `ParkReason` enum; new `reason_note` field; new `parked_reason_note` column; new `rimsky-cli parked list` subcommand; new `/admin/diagnostics/wait-sets` endpoint; new `report_park` MCP tool on claude-agent.
   - **Atomic-staging pattern**: pattern doc + reference filesystem producer under `examples/atomic-staging-fs-producer/`.
2. Update `feature-index.md` (if present at repo root for the rimsky submodule itself — check first) with entries for the new features.

**Verification:**
- `git diff CHANGELOG.md` shows the new entry.

---

## Manual checks after completion

These are tasks the implementer cannot verify autonomously; the user runs them after the implementation + automated tests are clean:

- **Dashboard visual verification** (T53): bring up the dashboard with `cd dashboards/rimsky-dashboard && npm run dev`, point a browser at it, park a node via the stub executor, confirm the parked-nodes view groups by reason and `awaiting_human` rows are visually distinct.
- **Conformance run for the reference producer** (T51): start the producer (`go run ./examples/atomic-staging-fs-producer/cmd`) and run `go run ./cmd/rimsky-claim-producer-conformance --endpoint localhost:8090 --transport grpc` against it. Confirm conformance passes.
- **End-to-end smoke against the deploy compose**: `docker compose -f deploy/docker-compose.yml up -d`, wait for `/health`, post a template using the new subscription syntax, create an instance, observe cascade behavior via the dashboard. Confirm no regressions in the smoke fixture.
- **CHANGELOG / feature-index review**: skim the entries added in T57 and confirm they accurately reflect the user-facing surface.
