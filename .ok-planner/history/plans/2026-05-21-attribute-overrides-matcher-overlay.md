# Matcher Overlay for attribute_overrides — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`
**Goal:** Add a third routing dimension (`by_match`) to `attribute_overrides` — an ordered list of `{matcher, overlay}` entries evaluated against dispatch-time identity, with a per-entry match counter persisted on the instance row for unused-entry observability.
**Architecture:** New `by_match` key inside the existing `col:rimsky_instances.attribute_overrides` JSONB blob (no separate column for the overlays themselves). New JSONB column `col:rimsky_instances.attribute_overrides_match_counts` for the per-entry counter. The runtime extends `applyAttributeOverrides` to return matched-entry indices, which the supervisor batches into a short dedicated transaction calling a new `IncrementAttributeOverrideMatchCounts` persistence method. The validator at instance-create extends to accept `by_match` and cross-checks matcher key values against the locked template.
**Tech Stack:** Go (1.22+); PostgreSQL JSONB + SQLite TEXT (JSON); pgx/v5; modernc.org/sqlite; existing `code:foundation/shared/jsonmerge.go::DeepMergeJSON`.

---

## Preflight assumptions

The spec assumes the userdata-collapse work has landed before this plan begins. As of the plan-writing pass, these are already in the working tree (some uncommitted):

- `col:rimsky_instances.attribute_overrides` exists (was `userdata_overrides`); JSONB on Postgres, TEXT/JSON on SQLite; `NOT NULL DEFAULT '{}'`.
- `runtime/attribute_overrides.go` exists with `applyAttributeOverrides(resolved, overrides, executor, nodeName, logger)`.
- `control/controlapi/attribute_overrides.go` exists with `validateAttributeOverrides(overrides, templateNodes, executors)`.
- `foundation/persistence/instances.go::InstanceRow.AttributeOverrides` is a `map[string]any`.
- `foundation/persistence/conformance/instances_attribute_overrides.go` is the cross-driver test surface.
- Concept docs at `.ok-planner/design/concepts/{attribute,instance,inertness}.md` carry the post-collapse shape.

If any of those preconditions are unmet, **stop** and surface to the user. The plan does not bridge a partial userdata-collapse state.

---

## Task 1: Add postgres migration for the match-counts column

**Files:**
- `foundation/persistence/postgres/migrations/006-attribute-overrides-match-counts.sql` (new)

**Steps:**

1. Create the file `foundation/persistence/postgres/migrations/006-attribute-overrides-match-counts.sql` with exactly this content:

   ```sql
   -- =====  rimsky_instances.attribute_overrides_match_counts  =====
   -- Per-entry match counter for instance.attribute_overrides.by_match.
   -- Array of int64 indexed by by_match entry position; incremented
   -- synchronously by the supervisor on each matcher hit. Empty array
   -- ('[]') for instances with no by_match entries. Per spec
   -- .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
   -- §"Persistence".
   ALTER TABLE rimsky_instances
       ADD COLUMN attribute_overrides_match_counts JSONB NOT NULL DEFAULT '[]'::jsonb;
   ```

2. Verify the file exists and lints as valid SQL syntax (the embed.FS picks it up automatically; no embed.go change needed).

**Verification:**
```
test -f foundation/persistence/postgres/migrations/006-attribute-overrides-match-counts.sql && \
  head -1 foundation/persistence/postgres/migrations/006-attribute-overrides-match-counts.sql
```
Expect the first line to be the `-- =====  rimsky_instances.attribute_overrides_match_counts  =====` header.

---

## Task 2: Add sqlite migration for the match-counts column

**Files:**
- `foundation/persistence/sqlite/migrations/006-attribute-overrides-match-counts.sql` (new)

**Steps:**

1. Create the file `foundation/persistence/sqlite/migrations/006-attribute-overrides-match-counts.sql` with exactly this content:

   ```sql
   -- =====  rimsky_instances.attribute_overrides_match_counts  =====
   -- SQLite parallel of postgres migration 006. JSON stored as TEXT;
   -- semantics match. Per spec
   -- .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
   -- §"Persistence".
   ALTER TABLE rimsky_instances
       ADD COLUMN attribute_overrides_match_counts TEXT NOT NULL DEFAULT '[]';
   ```

**Verification:**
```
test -f foundation/persistence/sqlite/migrations/006-attribute-overrides-match-counts.sql && \
  head -1 foundation/persistence/sqlite/migrations/006-attribute-overrides-match-counts.sql
```

---

## Task 3: Add the field to InstanceRow + InstanceCreateInput

**Files:**
- `foundation/persistence/instances.go` (modify)

**Steps:**

1. Open `foundation/persistence/instances.go`. The existing `InstanceRow` struct is at line 29 and `InstanceCreateInput` at line 68.

2. Add a new field to `InstanceRow` after `AttributeOverrides`:

   ```go
   // AttributeOverridesMatchCounts is the per-entry match counter for
   // AttributeOverrides.by_match. Indexed by by_match entry position;
   // each int64 counts how many dispatches matched that entry. Length
   // equals len(AttributeOverrides["by_match"]); empty for instances
   // with no by_match entries. Read via GET /instances/{id}; written by
   // the supervisor's IncrementAttributeOverrideMatchCounts call after
   // applyAttributeOverrides returns matched indices.
   //
   // @concept: attribute (L5 matcher overlay)
   AttributeOverridesMatchCounts []int64 `json:"attribute_overrides_match_counts,omitempty"`
   ```

3. Add the same field to `InstanceCreateInput` after `AttributeOverrides`:

   ```go
   // AttributeOverridesMatchCounts is the initial counter array,
   // typically a zero-filled slice of length len(by_match). The control-
   // API handler initialises this from the request body's by_match
   // length; the persistence layer persists it verbatim.
   AttributeOverridesMatchCounts []int64
   ```

**Verification:**
```
go build ./foundation/persistence/...
```
Expect a clean build. (Other packages will not yet compile because the postgres/sqlite implementations don't write the new column; Task 4 / 5 address that.)

---

## Task 4: Add a new method to InstanceTable interface

**Files:**
- `foundation/persistence/instances.go` (modify)

**Steps:**

1. Open `foundation/persistence/instances.go`. The `InstanceTable` interface is at line 41.

2. Add this method declaration at the bottom of the interface (just before the closing brace), preserving the existing convention of `tx Tx` as the last parameter:

   ```go
   // IncrementAttributeOverrideMatchCounts atomically increments the
   // counter at each of the given by_match entry positions on the
   // instance's attribute_overrides_match_counts column. Out-of-range
   // indices (≥ array length) are silently ignored — the column's
   // length is fixed at create-time by the request body's by_match
   // length, so an out-of-range index signals an upstream wiring bug
   // and a WARN is the recovery surface, not a hard error.
   //
   // tx is required (non-nil); the backend's q(tx) accessor panics on
   // nil tx per the package's universal convention. Dispatch-path
   // callers wrap with args.Persist.Transaction(...) to open a short
   // dedicated tx (the dispatch-row write has already committed via
   // transitionToRunning before this is invoked, so the increment
   // runs in its own separate tx, not nested with anything).
   //
   // Per spec
   // .ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md
   // §"Persistence API".
   IncrementAttributeOverrideMatchCounts(ctx context.Context, instanceID shared.UUID, indices []int, tx Tx) error
   ```

   (The interface's existing methods use `shared.UUID` for the ID type — see `MarkTerminated` at line 51.)

**Verification:**
```
go build ./foundation/persistence/...
```
Expect a build error from the postgres and sqlite implementations not satisfying the interface — that is the expected red. Task 5 fixes it.

---

## Task 5: Implement IncrementAttributeOverrideMatchCounts on postgres

**Files:**
- `foundation/persistence/postgres/instances.go` (modify)

**Steps:**

1. Open `foundation/persistence/postgres/instances.go`. The `instanceCols` constant at line 29 currently reads `id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, created_at, terminated_at`. Add the new column at the end:

   ```go
   const instanceCols = `id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, created_at, terminated_at, attribute_overrides_match_counts`
   ```

2. Update the `Create` method (starts at line 34). Inside the function, after the existing `overridesBytes, err := json.Marshal(in.AttributeOverrides)` (around line 46), marshal the new field:

   ```go
   if in.AttributeOverridesMatchCounts == nil {
       in.AttributeOverridesMatchCounts = []int64{}
   }
   matchCountsBytes, err := json.Marshal(in.AttributeOverridesMatchCounts)
   if err != nil {
       return persistence.InstanceRow{}, fmt.Errorf("instances.create: marshal attribute_overrides_match_counts: %w", err)
   }
   ```

3. In the same `Create` method, update the `INSERT` statement (currently at line 63) to include the new column and parameter. Replace:

   ```go
   row := ex.QueryRow(ctx,
       `INSERT INTO rimsky_instances (id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode)
        VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'coalesce'))
        RETURNING `+instanceCols,
       id, in.TemplateHash, in.InstanceKey, paramsBytes, overridesBytes, deliveryMode,
   )
   ```

   with:

   ```go
   row := ex.QueryRow(ctx,
       `INSERT INTO rimsky_instances (id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, attribute_overrides_match_counts)
        VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'coalesce'), $7)
        RETURNING `+instanceCols,
       id, in.TemplateHash, in.InstanceKey, paramsBytes, overridesBytes, deliveryMode, matchCountsBytes,
   )
   ```

4. Update `scanInstance` (starts around line 307). Add a local variable for the new column scan and decode it:

   - Add `matchCounts []byte` to the local var block.
   - Add `&matchCounts` to the end of the `sc.Scan(...)` argument list.
   - After the `ov := ... unmarshal attribute_overrides` block, add:

     ```go
     mc := []int64{}
     if len(matchCounts) > 0 {
         if err := json.Unmarshal(matchCounts, &mc); err != nil {
             return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides_match_counts: %w", err)
         }
     }
     ```

   - Add the field to the returned `InstanceRow` literal:

     ```go
     AttributeOverridesMatchCounts: mc,
     ```

5. Add the new method at the bottom of the file (after `CountByActive`):

   ```go
   // IncrementAttributeOverrideMatchCounts builds a single UPDATE that
   // chains jsonb_set calls per index. Each chained step reads the prior
   // step's output and increments the int at that position by 1. Out-
   // of-range indices are silently no-ops (jsonb_set with create_missing
   // = false leaves the array unchanged when the path doesn't exist).
   //
   // tx must be non-nil — s.q(tx) panics on nil per the package's
   // universal convention. Callers wrap with args.Persist.Transaction.
   func (s *instancesImpl) IncrementAttributeOverrideMatchCounts(
       ctx context.Context,
       instanceID foundationshared.UUID,
       indices []int,
       tx persistence.Tx,
   ) error {
       if len(indices) == 0 {
           return nil
       }
       ex := s.q(tx)
       // Build a chained jsonb_set expression. $1 is instanceID; $2..$N
       // are the per-index args (as text — jsonb_set takes a text[] path).
       // Example shape for indices = [0, 2]:
       //   jsonb_set(
       //     jsonb_set(attribute_overrides_match_counts,
       //               ARRAY[$2::text],
       //               to_jsonb(coalesce((attribute_overrides_match_counts->>$2::text)::int, 0) + 1)),
       //     ARRAY[$3::text],
       //     to_jsonb(coalesce(... )::int, 0) + 1))
       setExpr := "attribute_overrides_match_counts"
       args := []any{instanceID}
       for i, idx := range indices {
           argPos := i + 2 // $2, $3, ...
           setExpr = fmt.Sprintf(
               "jsonb_set(%s, ARRAY[$%d::text], to_jsonb(coalesce((%s->>$%d::text)::int, 0) + 1))",
               setExpr, argPos, setExpr, argPos,
           )
           args = append(args, fmt.Sprintf("%d", idx))
       }
       query := fmt.Sprintf(
           `UPDATE rimsky_instances SET attribute_overrides_match_counts = %s WHERE id = $1`,
           setExpr,
       )
       if _, err := ex.Exec(ctx, query, args...); err != nil {
           return fmt.Errorf("instances.incrementAttributeOverrideMatchCounts: %w", err)
       }
       return nil
   }
   ```

   The accessor `s.q(tx)` is the same one used throughout the file (see `Create` at line 35, `Get` at line 81). The `querier` type returned by `q(tx)` has `Exec(ctx, sql, args...)` per `code:foundation/persistence/postgres/backend.go`.

**Verification:**
```
go build ./foundation/persistence/postgres/...
```

---

## Task 6: Implement IncrementAttributeOverrideMatchCounts on sqlite

**Files:**
- `foundation/persistence/sqlite/instances.go` (modify)

**Steps:**

1. Open `foundation/persistence/sqlite/instances.go`. The `instanceCols` constant at line 23 needs the new column appended (same shape as the postgres edit in Task 5, step 1):

   ```go
   const instanceCols = `id, template_hash, instance_key, params, attribute_overrides, frame_delivery_mode, created_at, terminated_at, attribute_overrides_match_counts`
   ```

2. Update the `Create` method to marshal + insert the new column. Mirror the structure of Task 5 step 2/3 but in the SQLite idiom:

   - Marshal `in.AttributeOverridesMatchCounts` to `matchCountsBytes` (same as postgres).
   - Update the INSERT to include the new column and a `?` placeholder.

3. Update `scanInstance` (SQLite's version uses string scans, NOT `[]byte`): add `var matchCountsStr string` to the scan-locals; append `&matchCountsStr` to the `Scan(...)` argument list; after the existing overrides unmarshal block, decode the new column:

   ```go
   mc := []int64{}
   if matchCountsStr != "" {
       if err := json.Unmarshal([]byte(matchCountsStr), &mc); err != nil {
           return persistence.InstanceRow{}, fmt.Errorf("unmarshal attribute_overrides_match_counts: %w", err)
       }
   }
   ```

   Then add `AttributeOverridesMatchCounts: mc,` to the returned `InstanceRow` literal.

4. Add the new method at the bottom of the file:

   ```go
   // IncrementAttributeOverrideMatchCounts chains json_set calls per
   // index using the $[i] path syntax. SQLite's BEGIN IMMEDIATE
   // (when wrapped via Tables.Transaction) serialises transactions
   // naturally.
   //
   // tx must be non-nil — s.q(tx) panics on nil per the package's
   // universal convention. Callers wrap with args.Persist.Transaction.
   func (s *instancesImpl) IncrementAttributeOverrideMatchCounts(
       ctx context.Context,
       instanceID foundationshared.UUID,
       indices []int,
       tx persistence.Tx,
   ) error {
       if len(indices) == 0 {
           return nil
       }
       setExpr := "attribute_overrides_match_counts"
       for _, idx := range indices {
           path := fmt.Sprintf("$[%d]", idx)
           // json_set(<prev>, '$[i]', coalesce(json_extract(<prev>, '$[i]'), 0) + 1)
           setExpr = fmt.Sprintf(
               "json_set(%s, '%s', coalesce(json_extract(%s, '%s'), 0) + 1)",
               setExpr, path, setExpr, path,
           )
       }
       query := fmt.Sprintf(
           `UPDATE rimsky_instances SET attribute_overrides_match_counts = %s WHERE id = ?`,
           setExpr,
       )
       if _, err := s.q(tx).ExecContext(ctx, query, instanceID.String()); err != nil {
           return fmt.Errorf("instances.incrementAttributeOverrideMatchCounts: %w", err)
       }
       return nil
   }
   ```

   The accessor `s.q(tx)` matches the existing pattern in the file (see `Create` at line 25, `Get` at line 70). The returned object has `ExecContext` and `QueryRowContext` methods per the package's existing usage.

**Verification:**
```
go build ./foundation/persistence/sqlite/...
```

---

## Task 7: Write conformance test for the new field round-trip

**Files:**
- `foundation/persistence/conformance/instances_attribute_overrides.go` (modify)
- `foundation/persistence/conformance/conformance.go` (modify — register the new subtest)

**Steps:**

1. Open `foundation/persistence/conformance/instances_attribute_overrides.go`. After `testInstancesAttributeOverridesDefaultsEmpty`, add a new function:

   ```go
   // testInstancesAttributeOverridesMatchCountsRoundTrip verifies that
   // the AttributeOverridesMatchCounts field survives Create + Get
   // round-trip with an explicit non-zero array.
   func testInstancesAttributeOverridesMatchCountsRoundTrip(t *testing.T, d persistence.Database) {
       t.Helper()
       defer d.Close()
       ctx := context.Background()
       if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
           t.Fatalf("migrate: %v", err)
       }
       store := d.Tables()

       tmpl := "sha256-" + uuid.NewString()
       id := uuid.New()
       initial := []int64{0, 0, 0, 0}

       if err := inTx(ctx, store, func(tx persistence.Tx) error {
           if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
               ID: tmpl,
               Spec: spec.TemplateSpec{
                   Name: "match-counts", Version: "1",
                   FrameResolutionMode: spec.FrameResolutionSerialQueue,
                   FrameTimeoutMs:      600000,
                   Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
               },
               State: persistence.TemplateStateRegistered, Source: "direct",
           }, tx); err != nil {
               return err
           }
           _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
               ID:                            id,
               TemplateHash:                  tmpl,
               Params:                        map[string]any{},
               AttributeOverridesMatchCounts: initial,
           }, tx)
           return err
       }); err != nil {
           t.Fatalf("Create: %v", err)
       }

       var got *persistence.InstanceRow
       if err := inTx(ctx, store, func(tx persistence.Tx) error {
           r, err := store.Instances().Get(ctx, id, tx)
           got = r
           return err
       }); err != nil {
           t.Fatalf("Get: %v", err)
       }
       if got == nil {
           t.Fatalf("Get returned nil")
       }
       if !reflect.DeepEqual(got.AttributeOverridesMatchCounts, initial) {
           t.Fatalf("counts mismatch: got %#v want %#v", got.AttributeOverridesMatchCounts, initial)
       }
   }
   ```

2. In `foundation/persistence/conformance/conformance.go`, find the `Suite` function and add a new `t.Run` line alongside the existing `InstancesAttributeOverridesRoundTrip`:

   ```go
   t.Run("InstancesAttributeOverridesMatchCountsRoundTrip", func(t *testing.T) { testInstancesAttributeOverridesMatchCountsRoundTrip(t, factory(t)) })
   ```

   Place it after the existing `InstancesAttributeOverridesMigrationBackfill` registration.

**Verification:**
```
go test ./foundation/persistence/postgres/... -run InstancesAttributeOverridesMatchCountsRoundTrip -count=1
go test ./foundation/persistence/sqlite/...   -run InstancesAttributeOverridesMatchCountsRoundTrip -count=1
```
Both backends must pass.

---

## Task 8: Write conformance test for IncrementAttributeOverrideMatchCounts — happy path

**Files:**
- `foundation/persistence/conformance/instances_attribute_overrides.go` (modify)
- `foundation/persistence/conformance/conformance.go` (modify)

**Steps:**

1. Add a new function to `instances_attribute_overrides.go`:

   ```go
   // testInstancesIncrementAttributeOverrideMatchCounts verifies the
   // basic increment path: starting from [0, 0, 0], incrementing
   // indices [0, 2] yields [1, 0, 1]; a second call incrementing
   // [0, 0, 1] (duplicate) yields [3, 1, 1] (each index in the slice
   // counts as a separate increment? — pin the semantics by spec:
   // duplicates inside one call increment that many times).
   func testInstancesIncrementAttributeOverrideMatchCounts(t *testing.T, d persistence.Database) {
       // ... full implementation following the round-trip test's
       // setup pattern. Verify counts after two increment calls.
   }
   ```

   **Decision to pin**: a duplicate index inside one `indices` slice increments the counter at that index by 1 *per occurrence* — i.e. `Increment(id, [0, 0])` brings index 0 from 0 to 2. This matches both backends' natural behavior (jsonb_set chained is left-fold, json_set chained is left-fold; each chained step reads the prior step's output and increments by 1). Encode this in the test.

2. Register the new subtest in `conformance.go`:

   ```go
   t.Run("InstancesIncrementAttributeOverrideMatchCounts", func(t *testing.T) { testInstancesIncrementAttributeOverrideMatchCounts(t, factory(t)) })
   ```

**Verification:**
```
go test ./foundation/persistence/postgres/... -run InstancesIncrementAttributeOverrideMatchCounts -count=1
go test ./foundation/persistence/sqlite/...   -run InstancesIncrementAttributeOverrideMatchCounts -count=1
```

---

## Task 9: Write conformance test for IncrementAttributeOverrideMatchCounts — concurrency

**Files:**
- `foundation/persistence/conformance/instances_attribute_overrides.go` (modify)
- `foundation/persistence/conformance/conformance.go` (modify)

**Steps:**

1. Add a function `testInstancesIncrementAttributeOverrideMatchCountsConcurrent` that:

   - Creates an instance with `AttributeOverridesMatchCounts: []int64{0, 0, 0}`.
   - Spawns N goroutines (N=20). Each goroutine calls `store.Transaction(ctx, func(ctx, tx) error { return store.Instances().IncrementAttributeOverrideMatchCounts(ctx, id, []int{0}, tx) })` — each goroutine opens its own short tx via the package's standard `Transaction` wrapper. (Per `code:foundation/persistence/postgres/backend.go::q`, every Table method requires an explicit non-nil tx; the `Transaction` wrapper is the only way to get one.)
   - Waits via `sync.WaitGroup`.
   - Reads back and asserts the final value at index 0 equals exactly 20 (monotonic, no lost updates).
   - Repeats with `[]int{2}` to confirm targeted indices don't bleed (final state should be `[20, 0, 20]`).

2. Register in `conformance.go`:

   ```go
   t.Run("InstancesIncrementAttributeOverrideMatchCountsConcurrent", func(t *testing.T) { testInstancesIncrementAttributeOverrideMatchCountsConcurrent(t, factory(t)) })
   ```

**Verification:**
```
go test -race -count=3 ./foundation/persistence/postgres/... -run InstancesIncrementAttributeOverrideMatchCountsConcurrent
go test -race -count=3 ./foundation/persistence/sqlite/...   -run InstancesIncrementAttributeOverrideMatchCountsConcurrent
```
Both must pass with no race detector hits. The final counter must equal the number of increment calls.

---

## Task 10: Add GraphName + ChildKey fields to acquisition struct

**Files:**
- `runtime/runner_acquire.go` (modify)

**Steps:**

1. Open `runtime/runner_acquire.go`. The `acquisition` struct is at line 74. Add two new fields after `Executor string` (around line 79):

   ```go
   // GraphName is the name of the template's graph this dispatch
   // belongs to — "main" (spec.MainGraphName) for main-graph dispatches
   // and the sub-graph name for internal-sub-graph dispatches. For
   // entry-absorbed dispatches (where a sub-graph's entry node shares
   // runtime identity with the calling node per concept:delegation),
   // the outer graph wins — the row's declared template location.
   //
   // Derived at acquisition time by consulting the bound template's
   // Graphs list (spec.TemplateSpec.Graphs) and finding the GraphSpec
   // whose Nodes contains NodeType. Legacy flat-Nodes templates with
   // an empty Graphs list resolve to "main".
   //
   // Consumed by applyAttributeOverrides (L5 matcher evaluation).
   //
   // @concept: attribute (L5 matcher overlay)
   GraphName string

   // ChildKey is the producer-emitted partition key for fan-out child
   // dispatches (set at sub-claim acquisition per concept:fan-out).
   // Empty string for non-fan-out dispatches. Already carried on the
   // run-tree row; threaded onto the acquisition so the dispatch path
   // can pass it to applyAttributeOverrides without re-fetching.
   //
   // Consumed by applyAttributeOverrides (L5 matcher evaluation).
   //
   // @concept: attribute (L5 matcher overlay)
   ChildKey string
   ```

**Verification:**
```
go build ./runtime/...
```
Existing tests still build. The fields default to empty strings; existing call sites that don't populate them will compile without changes.

---

## Task 11: Populate acquisition.GraphName at all production dispatch sites

**Files:**
- `runtime/runner_acquire.go` (modify)
- `runtime/callback.go` (modify)
- `runtime/runner_dispatch_test.go` (modify — test fixture)
- `runtime/subgraph_caller_lineage_test.go` (modify — test fixture)

**Steps:**

1. Identify the production `acquisition{}` construction sites in `runtime/runner_acquire.go`. They are at lines **420** (unavailable branch) and **444** (happy path). Both happen inside `tryAcquire`, where the bound template is in scope as `tmpl` of type `*node.TemplateSpec` (per `code:runtime/runner_locks.go::lookupTemplate` at line 386 — it returns `*node.TemplateSpec`, where `node.TemplateSpec` is a type alias for `spec.TemplateSpec`). The accessor is therefore **`tmpl.Graphs`**, not `tmpl.Spec.Graphs`.

   Just before each `acquisition{...}` literal (lines 420 and 444), compute the graph name:
   ```go
   graphName := lookupGraphName(tmpl.Graphs, nd.NodeType)
   ```
   Inside each literal, add the field:
   ```go
   GraphName: graphName,
   ChildKey:  childKey, // populated by Task 12; "" until then
   ```
   (`tmpl` and `nd` are the local-variable names in `tryAcquire`'s scope as of plan writing; both literals at lines 420 and 444 reference them as `nd.NodeType`, `nd.Executor`, etc., so the in-scope names are stable.)

2. Add the helper `lookupGraphName` near the existing helpers in `runner_acquire.go`:

   ```go
   // lookupGraphName returns the GraphSpec.Name whose Nodes contains
   // nodeType. Falls back to spec.MainGraphName when no GraphSpec
   // contains the type or when the template carries no sub-graphs
   // (legacy flat-Nodes templates with empty Graphs list).
   //
   // For entry-absorbed dispatches the outer-graph resolution is
   // automatic: the calling node is declared in the outer graph's
   // Nodes list, so the lookup finds the outer GraphSpec. Sub-graph
   // *internal* nodes are declared inside the sub-graph's Nodes list,
   // so the lookup naturally returns the sub-graph name for them.
   //
   // @concept: attribute (L5 matcher overlay's graph-key derivation)
   func lookupGraphName(graphs []spec.GraphSpec, nodeType string) string {
       for _, g := range graphs {
           for _, n := range g.Nodes {
               if n.Type == nodeType {
                   return g.Name
               }
           }
       }
       return spec.MainGraphName
   }
   ```

   Add the import if not already present:
   ```go
   import spec "github.com/fallguyconsulting/rimsky/foundation/spec"
   ```

3. The third production `acquisition{}` site is `code:runtime/callback.go:380` (`&acquisition{...}` inside the callback path, reconstructing an acquisition for resume-callback `applyTerminal`). This path does NOT call `applyAttributeOverrides` — it goes straight to `applyTerminal` with already-resolved attributes (`ac.ResolvedAttributes`). `AsyncContext` (`code:runtime/runner.go:292`) carries neither `GraphName` nor `ChildKey`, and extending it is out of scope for this plan. Populate the new fields with empty-string literals and a comment noting they're inert at this site:

   ```go
   acq := &acquisition{
       // ... existing fields ...
       GraphName: "", // resume-callback path doesn't run applyAttributeOverrides
       ChildKey:  "", // (see callback.go::applyTerminal); fields inert here
   }
   ```

4. The two test-fixture acquisition sites — `runtime/runner_dispatch_test.go:344` and `runtime/subgraph_caller_lineage_test.go:180` — must also accept the new fields. Add `GraphName: "main"` and `ChildKey: ""` to both literals. The existing tests exercise L3/L4 paths and don't invoke the L5 matcher overlay, so the main-graph default preserves their semantics; no test logic needs adjusting.

**Verification:**
```
go build ./runtime/...
go test ./runtime/... -count=1
```
Production + test acquisitions compile; existing tests pass.

---

## Task 12: Populate acquisition.ChildKey at the dispatch sites

**Files:**
- `runtime/runner_acquire.go` (modify)

**Steps:**

1. The two production acquisition construction sites (lines 420 and 444 — same sites touched in Task 11) need `ChildKey` populated. The child key for a dispatched run lives on `rimsky_node_runs.child_key` (set at sub-claim acquisition time). Read it from the run-tree row that `tryAcquire` already fetches around line 466 (`rt.GetByID(ctx, tx, cand.DispatchID)`):

   ```go
   var childKey string
   if rt := args.Persist.RunTree(); rt != nil {
       if row, err := rt.GetByID(ctx, tx, cand.DispatchID); err == nil && row != nil {
           childKey = row.ChildKey
       }
       // (a GetByID failure is already logged elsewhere — see the
       // existing ParentRunID handling around line 467.)
   }
   ```

   Compute `childKey` before the `acquisition{...}` literal and include `ChildKey: childKey` in the struct (and in the unavailable-branch literal at line 420, which currently does NOT fetch the run-tree row — for the unavailable branch, `ChildKey: ""` is fine since the merge function will run anyway when the node ultimately succeeds on retry; the unavailable handler doesn't call `applyAttributeOverrides`).

   For the happy-path literal at line 444, the existing `RunTree().GetByID` call below (line 466) currently re-fetches the row only for `ParentRunID`. Move that fetch above the `acquisition{}` literal so a single fetch supplies both `ParentRunID` and `ChildKey`, and the literal can include both fields. This is a small structural reorder, not a new fetch.

2. For non-fan-out dispatches (where no sub-claim was acquired), `rimsky_node_runs.child_key` is `""` already — the column is `NOT NULL DEFAULT ''` per the persistence schema. The same code reads `""` for them naturally; no special-casing needed.

3. The test-fixture acquisition sites from Task 11 step 4 already include `ChildKey: ""` per that step's guidance.

**Verification:**
```
go build ./runtime/...
go test ./runtime/... -count=1
```

---

## Task 13: Extend applyAttributeOverrides signature — accept graph + childKey, return matched indices

**Files:**
- `runtime/attribute_overrides.go` (modify)

**Steps:**

1. Open `runtime/attribute_overrides.go`. The current signature (lines 44-50) is:

   ```go
   func applyAttributeOverrides(
       resolved map[string]any,
       overrides map[string]any,
       executor string,
       nodeName string,
       logger shared.Logger,
   ) map[string]any
   ```

2. Replace with the new signature:

   ```go
   func applyAttributeOverrides(
       resolved  map[string]any, // post-substitution + post-static-default bag
       overrides map[string]any, // single blob: by_executor + by_node + by_match
       executor  string,
       nodeName  string,
       graph     string, // "main" (spec.MainGraphName) or sub-graph name
       childKey  string, // "" for non-fan-out dispatches
       logger    shared.Logger,
   ) (merged map[string]any, matched []int)
   ```

3. Update the doc comment block above the function to describe:
   - The new `by_match` (L5) layer.
   - The dispatch-context parameters (graph, childKey).
   - The matcher reads from the post-L4 snapshot (NOT the running merged bag).
   - The `(merged, matched)` return shape and what `matched` is for.

4. Update the function body to:

   a. Keep the existing L3 + L4 fold logic unchanged (steps 1-3 of the spec's `## Dispatch evaluation` Evaluation loop).
   b. After the L3 + L4 folds, snapshot the bag:

      ```go
      var matcherCtx map[string]any
      if m, ok := merged.(map[string]any); ok {
          matcherCtx = m
      } else {
          matcherCtx = map[string]any{}
      }
      ```

   c. Walk `by_match`:

      ```go
      entries, _ := lookupMatchList(overrides) // new helper (Task 14)
      for i, entry := range entries {
          matcher, _ := entry["matcher"].(map[string]any)
          overlay, _ := entry["overlay"].(map[string]any)
          if evaluateMatcher(matcher, executor, nodeName, graph, childKey, matcherCtx) {
              merged = shared.DeepMergeJSON(merged, overlay)
              matched = append(matched, i)
          }
      }
      ```

   d. Adjust the existing return-paths to return `(merged, nil)` for the no-overrides early-out (L5 has no matches if no overrides exist).

5. Update existing call patterns inside the function body to thread `matched` through correctly. Two return paths exist today (line 53-57 the empty-overrides path; line 67-79 the post-merge path). Both should return `(merged, matched)` — `matched` is nil/empty when no by_match entries exist or none matched.

**Verification:**
```
go build ./runtime/...
```
Expect a build error at the call site in `runner_dispatch.go:422` — that's the next task.

---

## Task 14: Add lookupMatchList helper

**Files:**
- `runtime/attribute_overrides.go` (modify)

**Steps:**

1. Add the helper near `lookupFragment` (currently at line 96 of `attribute_overrides.go`):

   ```go
   // lookupMatchList returns overrides["by_match"] coerced to
   // []map[string]any. Returns (nil, false) for any miss or shape
   // mismatch — the validator at instance-create rejects malformed
   // shapes, but this runtime helper degrades gracefully on bad data.
   func lookupMatchList(overrides map[string]any) ([]map[string]any, bool) {
       raw, ok := overrides["by_match"]
       if !ok {
           return nil, false
       }
       list, ok := raw.([]any)
       if !ok {
           return nil, false
       }
       out := make([]map[string]any, 0, len(list))
       for _, item := range list {
           m, ok := item.(map[string]any)
           if !ok {
               return nil, false
           }
           out = append(out, m)
       }
       return out, true
   }
   ```

**Verification:**
```
go build ./runtime/...
```
(Existing call-site build error from Task 13 still present; Task 17 fixes.)

---

## Task 15: Add evaluateMatcher helper

**Files:**
- `runtime/attribute_overrides.go` (modify)

**Steps:**

1. Add `evaluateMatcher` near the other helpers:

   ```go
   // evaluateMatcher returns true if matcher's predicate matches the
   // dispatch context. AND across all present matcher keys. Missing
   // matcher keys are wildcards. Empty matcher ({}) matches every
   // dispatch.
   //
   // Per concept:inertness, this is a sanctioned read site for
   // attribute values. The read is primitive-equality only; values
   // are never logged, formatted, or included in error messages.
   //
   // @concept: inertness (sanctioned read site for attribute values)
   func evaluateMatcher(
       matcher map[string]any,
       executor, nodeName, graph, childKey string,
       bag map[string]any,
   ) bool {
       if len(matcher) == 0 {
           return true // empty matcher matches every dispatch
       }
       if v, ok := matcher["node_type"]; ok {
           s, _ := v.(string)
           if s != nodeName {
               return false
           }
       }
       if v, ok := matcher["executor"]; ok {
           s, _ := v.(string)
           if s != executor {
               return false
           }
       }
       if v, ok := matcher["graph"]; ok {
           s, _ := v.(string)
           if s != graph {
               return false
           }
       }
       if v, ok := matcher["child_key"]; ok {
           s, _ := v.(string)
           if s != childKey {
               return false
           }
       }
       if v, ok := matcher["attrs"]; ok {
           attrsMatcher, _ := v.(map[string]any)
           for path, want := range attrsMatcher {
               got, found := walkAttrPath(bag, path)
               if !found {
                   return false
               }
               if !primitiveEqual(got, want) {
                   return false
               }
           }
       }
       return true
   }

   // walkAttrPath walks a dotted path through bag and returns the
   // leaf value plus whether the path resolved. Returns (nil, false)
   // for any non-map intermediate (the matcher only addresses
   // primitive leaves under composites).
   func walkAttrPath(bag map[string]any, path string) (any, bool) {
       cur := any(bag)
       parts := strings.Split(path, ".")
       for _, p := range parts {
           m, ok := cur.(map[string]any)
           if !ok {
               return nil, false
           }
           v, exists := m[p]
           if !exists {
               return nil, false
           }
           cur = v
       }
       return cur, true
   }

   // primitiveEqual compares two values for equality, returning false
   // when either side is non-primitive. Type-coerces JSON numbers
   // (float64 vs int) because matcher values come through JSON
   // unmarshal as float64 and bag values from substitution may be
   // typed otherwise.
   func primitiveEqual(a, b any) bool {
       switch av := a.(type) {
       case string:
           bv, ok := b.(string)
           return ok && av == bv
       case bool:
           bv, ok := b.(bool)
           return ok && av == bv
       case float64:
           switch bv := b.(type) {
           case float64:
               return av == bv
           case int:
               return av == float64(bv)
           case int64:
               return av == float64(bv)
           }
           return false
       case int:
           bf, ok := b.(float64)
           return ok && float64(av) == bf
       case int64:
           bf, ok := b.(float64)
           return ok && float64(av) == bf
       }
       return false
   }
   ```

2. Add the `strings` import to the file if not already present.

**Verification:**
```
go build ./runtime/...
```

---

## Task 16: Update existing test call sites + write unit tests for the L5 path

**Files:**
- `runtime/attribute_overrides_test.go` (modify)

**Steps:**

1. The Task 13 signature change breaks the existing 14 `applyAttributeOverrides(...)` call sites in this test file. Confirm with:
   ```
   grep -c "applyAttributeOverrides(" runtime/attribute_overrides_test.go
   ```
   Expect 14. Each call today has 5 arguments — `(resolved, ov, "claude-agent", "area-pass", logger)` — and assigns to a single `got` variable. Update each to:
   ```go
   got, _ := applyAttributeOverrides(resolved, ov, "claude-agent", "area-pass", "main", "", logger)
   ```
   The two new arguments are `graph` ("main" is fine for L3/L4 tests that don't exercise L5) and `childKey` (""). The second return value is `matched []int`; pre-existing tests don't assert on it, so `_` is appropriate. Apply this rewrite to every call site.

   After the rewrites, run:
   ```
   go test ./runtime/... -run TestApplyAttributeOverrides -count=1
   ```
   All existing subtests still pass.

2. Add a new top-level `TestApplyAttributeOverrides_ByMatch` test grouping with subtests for each of the spec's bullets:

   ```go
   func TestApplyAttributeOverrides_ByMatch(t *testing.T) {
       logger := shared.SilentLogger{}

       t.Run("empty by_match is a no-op", func(t *testing.T) {
           resolved := map[string]any{"k": "v"}
           ov := map[string]any{"by_match": []any{}}
           got, matched := applyAttributeOverrides(resolved, ov, "e", "n", "main", "", logger)
           if !reflect.DeepEqual(got, resolved) {
               t.Fatalf("got %#v want %#v", got, resolved)
           }
           if len(matched) != 0 {
               t.Fatalf("matched should be empty: %#v", matched)
           }
       })

       t.Run("single matcher node_type only", func(t *testing.T) {
           resolved := map[string]any{}
           ov := map[string]any{
               "by_match": []any{
                   map[string]any{
                       "matcher": map[string]any{"node_type": "fix"},
                       "overlay": map[string]any{"x": "applied"},
                   },
               },
           }
           // Matches:
           got, matched := applyAttributeOverrides(resolved, ov, "e", "fix", "main", "", logger)
           if got["x"] != "applied" {
               t.Fatalf("overlay not applied: %#v", got)
           }
           if !reflect.DeepEqual(matched, []int{0}) {
               t.Fatalf("matched = %#v", matched)
           }
           // Doesn't match:
           got2, matched2 := applyAttributeOverrides(resolved, ov, "e", "other", "main", "", logger)
           if _, exists := got2["x"]; exists {
               t.Fatalf("overlay incorrectly applied: %#v", got2)
           }
           if len(matched2) != 0 {
               t.Fatalf("matched2 should be empty: %#v", matched2)
           }
       })

       t.Run("multiple matcher keys AND together", func(t *testing.T) {
           // node_type AND attrs.iter_num=1 — both must match.
       })

       t.Run("empty matcher matches every dispatch", func(t *testing.T) {
           // matcher: {} should fire for every dispatch.
       })

       t.Run("declaration order — later wins on conflict", func(t *testing.T) {
           // Two entries match the same dispatch; later overlay wins
           // on overlapping path; non-conflicting paths from both apply.
       })

       t.Run("child_key matching", func(t *testing.T) {
           // Empty childKey doesn't match a matcher specifying child_key.
       })

       t.Run("graph matching", func(t *testing.T) {
           // main vs sub-graph names.
       })

       t.Run("attrs.<path> equality on primitives", func(t *testing.T) {
           // string/number/bool match; missing path → no match; non-primitive resolved → no match.
       })

       t.Run("matcher reads from post-L4 bag", func(t *testing.T) {
           // L3 override sets attrs.iter_num=1; matcher attrs: {iter_num: 1} fires.
       })

       t.Run("matcher reads from post-L4 snapshot, not running L5", func(t *testing.T) {
           // Two entries: first sets attrs.flag=true; second has matcher attrs: {flag: true}.
           // The second's matcher should NOT fire — it reads from the post-L4 snapshot,
           // not from the running merged bag including L5 folds.
       })

       t.Run("non-mutation invariant", func(t *testing.T) {
           // resolved and overrides unchanged after the call.
       })
   }
   ```

   Fill in each `// ...` body following the patterns of the existing L3/L4 tests in the same file.

**Verification:**
```
go test ./runtime/... -run TestApplyAttributeOverrides_ByMatch -count=1 -v
```
All subtests must pass.

---

## Task 17: Update the call site in runner_dispatch.go

**Files:**
- `runtime/runner_dispatch.go` (modify)

**Steps:**

1. The existing call at line 422 is:

   ```go
   resolved = applyAttributeOverrides(resolved, acq.InstanceAttributeOverrides, acq.Executor, acq.NodeType, args.Logger)
   acq.MergedAttributes = resolved
   ```

   Replace with:

   ```go
   merged, matched := applyAttributeOverrides(
       resolved,
       acq.InstanceAttributeOverrides,
       acq.Executor,
       acq.NodeType,
       acq.GraphName,
       acq.ChildKey,
       args.Logger,
   )
   resolved = merged
   acq.MergedAttributes = resolved
   if len(matched) > 0 {
       // Open a short dedicated tx for the counter increment. The
       // dispatch row already committed via transitionToRunning, so this
       // tx is separate from any other dispatch-path tx (the persistence
       // package's q(tx) accessor panics on nil tx — see
       // foundation/persistence/postgres/backend.go:125).
       err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
           return args.Persist.Instances().IncrementAttributeOverrideMatchCounts(ctx, acq.InstanceID, matched, tx)
       })
       if err != nil && args.Logger != nil {
           args.Logger.Warn("instance.attribute_overrides_counter_increment_failed",
               "instance_id", acq.InstanceID.String(),
               "matched_indices", matched,
               "error", err.Error())
           // Counter loss is observability degradation, not dispatch
           // failure — the dispatch row already committed and the run
           // continues. Per spec §"Error handling".
       }
   }
   ```

   The wrapper opens a single short tx for the batched increment. Adding the `persistence` import to `runner_dispatch.go` is not new — the file already imports it via `args.Persist.*` calls elsewhere in the same function.

**Verification:**
```
go build ./runtime/...
go test ./runtime/... -count=1
```
The whole runtime package builds and tests pass.

---

## Task 18: Extend validateAttributeOverrides — add by_match top-level key + signature change

**Files:**
- `control/controlapi/attribute_overrides.go` (modify)

**Steps:**

1. Open `control/controlapi/attribute_overrides.go`. The current signature (line 48) is:

   ```go
   func validateAttributeOverrides(
       overrides map[string]any,
       templateNodes []nodepkg.TemplateNodeDef,
       executors map[string]ExecutorEntry,
   ) error
   ```

   Add a fourth parameter `templateGraphs []spec.GraphSpec`:

   ```go
   func validateAttributeOverrides(
       overrides map[string]any,
       templateNodes []nodepkg.TemplateNodeDef,
       templateGraphs []spec.GraphSpec,
       executors map[string]ExecutorEntry,
   ) error
   ```

   Add the import: `spec "github.com/fallguyconsulting/rimsky/foundation/spec"`.

2. In the top-level-keys check (lines 56-60), add `"by_match"` to the allowed set:

   ```go
   for k := range overrides {
       if k != "by_executor" && k != "by_node" && k != "by_match" {
           return wrapInvalidf("attribute_overrides: unknown top-level key (allowed: by_executor, by_node, by_match); got %q", k)
       }
   }
   ```

3. After the existing `by_node` validation block (ends around line 107), add `by_match` validation. It calls a new helper `validateMatchEntries` (Task 19):

   ```go
   if raw, ok := overrides["by_match"]; ok {
       list, ok := raw.([]any)
       if !ok {
           return wrapInvalid("attribute_overrides.by_match must be an array")
       }
       if err := validateMatchEntries(list, templateNodes, templateGraphs, executors); err != nil {
           return err
       }
   }
   ```

**Verification:**
```
go build ./control/controlapi/...
```
Expect a build error at the single call site in `instances.go:239` — Task 21 addresses.

---

## Task 19: Add validateMatchEntries + validateMatcherKeys helpers

**Files:**
- `control/controlapi/attribute_overrides.go` (modify)

**Steps:**

1. Below `validateAttributeOverrides`, add:

   ```go
   // validateMatchEntries validates each by_match entry's shape and
   // matcher cross-checks against the locked template + declared
   // executors. Returns wrapped errAttributeOverridesInvalid on any
   // failure.
   func validateMatchEntries(
       list []any,
       templateNodes []nodepkg.TemplateNodeDef,
       templateGraphs []spec.GraphSpec,
       executors map[string]ExecutorEntry,
   ) error {
       // Build name sets once.
       nodeNames := make(map[string]struct{}, len(templateNodes))
       usedExecutors := make(map[string]struct{}, len(templateNodes))
       for _, n := range templateNodes {
           nodeNames[n.Type] = struct{}{}
           if n.Executor != "" {
               usedExecutors[n.Executor] = struct{}{}
           }
       }
       graphNames := make(map[string]struct{}, len(templateGraphs)+1)
       graphNames[spec.MainGraphName] = struct{}{} // "main" always valid
       for _, g := range templateGraphs {
           graphNames[g.Name] = struct{}{}
       }
       legacyFlat := len(templateGraphs) == 0

       for i, item := range list {
           entry, ok := item.(map[string]any)
           if !ok {
               return wrapInvalidf("attribute_overrides.by_match[%d] must be an object", i)
           }
           // Reject any keys other than matcher + overlay on the entry.
           for k := range entry {
               if k != "matcher" && k != "overlay" {
                   return wrapInvalidf("attribute_overrides.by_match[%d]: unknown entry key (allowed: matcher, overlay); got %q", i, k)
               }
           }
           matcher, hasMatcher := entry["matcher"].(map[string]any)
           if !hasMatcher {
               // Allow matcher to be entirely absent only as an explicit
               // empty object — require the key for clarity. Accept a
               // missing key as well by treating it the same as {}.
               if rawM, present := entry["matcher"]; present && rawM != nil {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher must be an object", i)
               }
               matcher = map[string]any{}
           }
           overlay, hasOverlay := entry["overlay"].(map[string]any)
           if !hasOverlay {
               return wrapInvalidf("attribute_overrides.by_match[%d].overlay must be an object", i)
           }
           _ = overlay // shape-only check; fragment values are inert
           if err := validateMatcherKeys(i, matcher, nodeNames, usedExecutors, executors, graphNames, legacyFlat); err != nil {
               return err
           }
       }
       return nil
   }

   // validateMatcherKeys enforces the matcher grammar's key set,
   // per-key cross-checks, and ordinal-shaped-key rejection.
   func validateMatcherKeys(
       entryIdx int,
       matcher map[string]any,
       nodeNames, usedExecutors map[string]struct{},
       executors map[string]ExecutorEntry,
       graphNames map[string]struct{},
       legacyFlat bool,
   ) error {
       allowed := map[string]struct{}{
           "node_type": {}, "executor": {}, "graph": {}, "child_key": {}, "attrs": {},
       }
       // Loud rejection vocabulary — ordinal-shaped keys that the spec
       // explicitly forbids. Map keys to the redirect message.
       ordinalRejects := map[string]string{
           "dispatch_index":  "use child_key or attrs.<path> as the matcher anchor; ordinal addressing is not supported",
           "nth_child":       "use child_key or attrs.<path>; ordinal addressing is not supported",
           "partition_index": "use child_key directly; partition_index is not exposed in the matcher grammar",
           "seq":             "use child_key or attrs.<path>; sequence addressing is not supported",
       }
       for k, v := range matcher {
           if msg, isOrdinal := ordinalRejects[k]; isOrdinal {
               return wrapInvalidf("attribute_overrides.by_match[%d].matcher: %s (offending key %q)", entryIdx, msg, k)
           }
           if _, ok := allowed[k]; !ok {
               return wrapInvalidf("attribute_overrides.by_match[%d].matcher: unknown matcher key %q (allowed: node_type, executor, graph, child_key, attrs)", entryIdx, k)
           }
           // Per-key shape + cross-check:
           switch k {
           case "node_type":
               s, ok := v.(string)
               if !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.node_type must be a string", entryIdx)
               }
               if _, found := nodeNames[s]; !found {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.node_type: unknown node %q", entryIdx, s)
               }
           case "executor":
               s, ok := v.(string)
               if !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.executor must be a string", entryIdx)
               }
               if _, declared := executors[s]; !declared {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.executor: unknown executor name %q", entryIdx, s)
               }
               if _, used := usedExecutors[s]; !used {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.executor: executor not referenced by any template node: %q", entryIdx, s)
               }
           case "graph":
               s, ok := v.(string)
               if !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.graph must be a string", entryIdx)
               }
               if legacyFlat {
                   if s != spec.MainGraphName {
                       return wrapInvalidf("attribute_overrides.by_match[%d].matcher.graph: template has no declared sub-graphs; only \"main\" is valid (got %q)", entryIdx, s)
                   }
               } else if _, ok := graphNames[s]; !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.graph: unknown graph %q (must be \"main\" or a declared sub-graph name)", entryIdx, s)
               }
           case "child_key":
               if _, ok := v.(string); !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.child_key must be a string", entryIdx)
               }
               // No cross-check — opaque per concept:fan-out.
           case "attrs":
               attrs, ok := v.(map[string]any)
               if !ok {
                   return wrapInvalidf("attribute_overrides.by_match[%d].matcher.attrs must be an object", entryIdx)
               }
               for path, primValue := range attrs {
                   if !isPrimitive(primValue) {
                       return wrapInvalidf("attribute_overrides.by_match[%d].matcher.attrs[%q]: must be a primitive (string / number / bool); composites use a dotted path instead", entryIdx, path)
                   }
               }
           }
       }
       return nil
   }

   func isPrimitive(v any) bool {
       switch v.(type) {
       case string, bool, float64, int, int64, json.Number:
           return true
       case nil:
           return false // explicit null is not a useful matcher predicate
       }
       return false
   }
   ```

2. Add the import: `"encoding/json"` if needed for `json.Number`.

**Verification:**
```
go build ./control/controlapi/...
```
Expect the existing call-site error from Task 18 — Task 21 fixes.

---

## Task 20: Update overridePresentKeys to return byMatchCount

**Files:**
- `control/controlapi/attribute_overrides.go` (modify)

**Steps:**

1. The current `overridePresentKeys` signature (line 141) returns `(byExecutor, byNode []string)`. Add a third return:

   ```go
   func overridePresentKeys(overrides map[string]any) (byExecutor, byNode []string, byMatchCount int) {
       // ... existing logic for byExecutor / byNode unchanged ...
       if raw, ok := overrides["by_match"]; ok {
           if list, ok := raw.([]any); ok {
               byMatchCount = len(list)
           }
       }
       return byExecutor, byNode, byMatchCount
   }
   ```

2. Update every call site of `overridePresentKeys` to accept the third return value. The current sweep (`grep -rn "overridePresentKeys(" .` excluding vendor/) yields exactly two call sites — both in `control/controlapi/instances.go`:

   - `control/controlapi/instances.go:358` — inside the `instance.attribute_overrides_attached` Info log emission (post-create success).
   - `control/controlapi/instances.go:374` — inside the `instance.attribute_overrides_replaced_by_idempotent_match` Warn log emission (idempotent-replace path).

   At each site, accept `byMatchCount` and add a `"by_match_count", byMatchCount` key-value pair to the structured log line. Example:

   ```go
   byExecutor, byNode, byMatchCount := overridePresentKeys(body.AttributeOverrides)
   deps.Logger.Info("instance.attribute_overrides_attached",
       "instance_id", respOut.InstanceID,
       "by_executor", byExecutor,
       "by_node", byNode,
       "by_match_count", byMatchCount,
   )
   ```

   Apply the equivalent change at both sites.

**Verification:**
```
go build ./...
```
Every call site compiles.

---

## Task 21: Update the call site in instances.go

**Files:**
- `control/controlapi/instances.go` (modify)

**Steps:**

1. The call at line 239:

   ```go
   if vErr := validateAttributeOverrides(body.AttributeOverrides, row.Spec.Nodes, deps.Executors); vErr != nil {
       return vErr
   }
   ```

   Update to:

   ```go
   if vErr := validateAttributeOverrides(body.AttributeOverrides, row.Spec.Nodes, row.Spec.Graphs, deps.Executors); vErr != nil {
       return vErr
   }
   ```

2. After the `validateAttributeOverrides` call succeeds, compute the initial match-counts array. Find the call to `provisionInstanceTx` (line 273) and pass through the initial counts via the `provisionArgs` struct (Task 22 extends that struct):

   ```go
   var initialMatchCounts []int64
   if raw, ok := body.AttributeOverrides["by_match"]; ok {
       if list, ok := raw.([]any); ok {
           initialMatchCounts = make([]int64, len(list))
       }
   }
   provisioned, err := provisionInstanceTx(ctx, deps, tx, row, provisionArgs{
       InstanceKey:                   body.InstanceKey,
       Params:                        params,
       AttributeOverrides:            body.AttributeOverrides,
       AttributeOverridesMatchCounts: initialMatchCounts,
       FrameDeliveryMode:             deliveryMode,
   })
   ```

**Verification:**
```
go build ./control/controlapi/...
```

---

## Task 22: Plumb AttributeOverridesMatchCounts through provisionInstanceTx

**Files:**
- `control/controlapi/instances.go` (modify) — extend `provisionArgs` + `provisionInstanceTx`

**Steps:**

1. Locate `provisionArgs` (around line 669 per grep). Add the new field:

   ```go
   type provisionArgs struct {
       // ... existing fields ...
       AttributeOverridesMatchCounts []int64
       // ... existing fields ...
   }
   ```

2. Locate `provisionInstanceTx` (around line 690). Find where it builds the `InstanceCreateInput` and pass the new field through:

   ```go
   instances.Instances().Create(ctx, persistence.InstanceCreateInput{
       // ... existing fields ...
       AttributeOverridesMatchCounts: args.AttributeOverridesMatchCounts,
       // ... existing fields ...
   }, tx)
   ```

**Verification:**
```
go build ./control/controlapi/...
```

---

## Task 23: Surface the new column in instanceItem (GET /instances/{id} response)

**Files:**
- `control/controlapi/instances.go` (modify)

**Steps:**

1. `instanceItem` is at line 123. Add a new field:

   ```go
   type instanceItem struct {
       // ... existing fields ...
       AttributeOverridesMatchCounts []int64 `json:"attribute_overrides_match_counts,omitempty"`
       // ... existing fields ...
   }
   ```

2. `toInstanceItem` (line 134) populates the item. Add:

   ```go
   if len(r.AttributeOverridesMatchCounts) > 0 {
       out.AttributeOverridesMatchCounts = r.AttributeOverridesMatchCounts
   }
   ```

**Verification:**
```
go build ./control/controlapi/...
go test ./control/controlapi/... -count=1
```

---

## Task 24: Write validator tests for the by_match shape

**Files:**
- `control/controlapi/attribute_overrides_test.go` (modify)

**Steps:**

1. Extend the existing `TestValidateAttributeOverrides` table-driven test with cases that exercise the by_match grammar. Add cases for:

   - `valid by_match: single entry with node_type + child_key matcher and an overlay`
   - `valid by_match: empty matcher {} accepted`
   - `valid by_match: [] accepted (empty list)`
   - `by_match not an array → 400`
   - `by_match entry has extra top-level key (e.g. notes) → 400`
   - `by_match entry missing matcher → matcher: {} is implied (valid)`
   - `by_match entry missing overlay → 400`
   - `unknown matcher key (e.g. node_name) → 400 naming the key`
   - `ordinal-shaped matcher key (dispatch_index, nth_child, partition_index, seq) → 400 redirecting to child_key / attrs`
   - `matcher.node_type referencing an unknown node → 400`
   - `matcher.executor referencing an unknown executor → 400`
   - `matcher.executor referencing a declared-but-unused executor → 400`
   - `matcher.graph "main" accepted on a flat-Nodes template (empty Graphs)`
   - `matcher.graph "other" rejected on a flat-Nodes template (empty Graphs) with the "no declared sub-graphs" message`
   - `matcher.graph referencing a declared sub-graph → accepted`
   - `matcher.graph referencing an unknown name (template has sub-graphs) → 400`
   - `matcher.attrs with a primitive value (string / number / bool) → accepted`
   - `matcher.attrs with a non-primitive value (object/array) → 400`

2. Each case follows the existing pattern (`input`, `wantErr`, `errContains`). Update the test's `templateGraphs` setup so cases that need declared graphs have them; the existing tests pass `nodes` only — the new tests need a `graphs []spec.GraphSpec` value to thread.

**Verification:**
```
go test ./control/controlapi/... -run TestValidateAttributeOverrides -count=1 -v
```
All subcases pass.

---

## Task 25: Mutate concept:attribute design doc

**Files:**
- `.ok-planner/design/concepts/attribute.md` (modify)

**Steps:**

1. Open `.ok-planner/design/concepts/attribute.md`. The `## Invariants` section ends around line 54. Append a new bullet (after the existing `## Invariants` list, before `## Aliases and historical names`):

   ```markdown
   - A fifth override layer (L5) extends the four-layer merge: `instance.attribute_overrides.by_match` is an ordered list of `{matcher, overlay}` entries. The matcher predicate is equality-only over a fixed key set (`node_type`, `executor`, `graph`, `child_key`, `attrs.<path>`); evaluated against the dispatch context at runtime; missing keys are wildcards; AND across present keys. Each matching entry's overlay folds on top via `DeepMergeJSON` in declaration order — later entries win. Empty matcher (`{}`) matches every dispatch. The matcher reads from the post-L4 merged bag (overrides applied through L4 are visible to the matcher). Ordinal-shaped matcher keys (`dispatch_index`, `nth_child`, `partition_index`, `seq`) and expression-shaped values are rejected at registration. Enforced at `code:control/controlapi/attribute_overrides.go::validateAttributeOverrides` and `code:runtime/attribute_overrides.go::applyAttributeOverrides`.
   ```

2. After the existing `## Static-default properties` section (line 65), before `## Notes` (line 73), append a new section:

   ```markdown
   ## Matcher overlay (by_match)

   A third routing dimension on `attribute_overrides`, alongside the static `by_executor` (L3) and `by_node` (L4) maps. `by_match` is an ordered list of `{matcher, overlay}` entries where the matcher is a content-keyed predicate over dispatch-time identity — solving the problem that static routes can't differentiate among children of a fan-out node that share node type and executor.

   The matcher grammar is intentionally small: equality only, over a fixed key set. `child_key` is the recommended anchor for fan-out routing (the producer-emitted per-sub-scope identifier from `concept:fan-out`, stable across dispatch reorderings); `attrs.<path>` covers non-fan-out differentiation. Ordinal-style addressing (any "third call" / "index N" semantics) is rejected at registration: matchers address partitions by identity, never by execution order.

   Override values are static — no substitution applied. The matcher reads from the post-L3+L4 bag, meaning earlier-layer overrides are visible to the matcher's `attrs.<path>` comparisons.

   Per-entry match counters persist on `col:rimsky_instances.attribute_overrides_match_counts`. The supervisor increments after the merge returns, in a short dedicated transaction. Operators and tests read the counter via `GET /instances/{id}` and assert on which entries fired. Entries that never match show 0 at instance terminal — the "silent miss becomes loud miss" discipline that makes matcher-overlay testing safe against producer key-scheme changes.
   ```

3. Append a new entry at the bottom of `## Notes` (after the existing 2026-05-21 entry):

   ```markdown
   - 2026-05-21 — Matcher overlay (L5 `by_match`) added per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. Equality-only matcher grammar over `{node_type, executor, graph, child_key, attrs.<path>}`. Per-entry match counter persisted on `attribute_overrides_match_counts`.
   ```

**Verification:**
```
grep -n "Matcher overlay (by_match)" .ok-planner/design/concepts/attribute.md
grep -n "fifth override layer (L5)" .ok-planner/design/concepts/attribute.md
```
Both greps must return a line number; the section was correctly inserted.

---

## Task 26: Mutate concept:instance design doc

**Files:**
- `.ok-planner/design/concepts/instance.md` (modify)

**Steps:**

1. Open `.ok-planner/design/concepts/instance.md`. Replace the `attribute_overrides` validation invariant at line 29:

   FROM:
   ```
   - `attribute_overrides` validation inspects only routing keys (`by_executor`/`by_node` plus executor/node names); fragment values are never inspected (preserves structural-inertness for attribute values).
   ```

   TO:
   ```
   - `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via `col:rimsky_instances.attribute_overrides_match_counts`.
   ```

2. Update the `## Boundaries` "Owns" line at line 23:

   FROM:
   ```
   Owns: the per-deployment runtime state, params, attribute_overrides, the binding to a template hash.
   ```

   TO:
   ```
   Owns: the per-deployment runtime state, params, attribute_overrides (including `by_match` matcher overlays and the per-entry match-counter column), the binding to a template hash.
   ```

3. Append to `## Notes` (currently ends at line 41):

   ```markdown

   2026-05-21 — Matcher overlay (`by_match`) added to `attribute_overrides` per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. New column `col:rimsky_instances.attribute_overrides_match_counts` (JSONB array of int64, indexed by `by_match` entry position). Incremented synchronously by the supervisor at match time; readable via `GET /instances/{id}`.
   ```

**Verification:**
```
grep -n "by_match" .ok-planner/design/concepts/instance.md
grep -n "attribute_overrides_match_counts" .ok-planner/design/concepts/instance.md
```

---

## Task 27: Mutate concept:inertness design doc

**Files:**
- `.ok-planner/design/concepts/inertness.md` (modify)

**Steps:**

1. Open `.ok-planner/design/concepts/inertness.md`. Replace the structural-inertness bullet at line 24:

   FROM:
   ```
   - **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) but does NOT inspect values to make decisions. Applies to: attribute values, named-event payloads, message payloads, `Error.payload`. Rimsky reads them only at substitution leaves and event-ledger writes; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, pattern-matches, attaches to traces, or includes them in error messages.
   ```

   TO:
   ```
   - **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, named-event payloads, message payloads, `Error.payload`. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, attaches to traces, or includes them in error messages.
   ```

2. Append a new sanctioned read site bullet to the list at lines 42-47:

   ```markdown
   - `evaluateMatcher` (`code:runtime/attribute_overrides.go`, the matcher-evaluator helper called from `applyAttributeOverrides`) — applies to attribute values only. Reads the resolved post-L4 attribute bag to evaluate `attrs.<path>` equality predicates from `attribute_overrides.by_match[].matcher`. The read is primitive-equality only; no traversal beyond the named path; values not logged, not formatted, not included in error messages. Sanctioned by `concept:attribute`'s L5 matcher-overlay invariant.
   ```

3. Append to `## Notes` (currently ends at line 63):

   ```markdown
   - 2026-05-21 — Matcher overlay added per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. New sanctioned read site (`evaluateMatcher`) reads resolved attribute values for equality matching. Structural-inertness bullet at line 24 tightened to explicitly allow sanctioned-site reads while preserving the general "no value-driven decisions" discipline.
   ```

**Verification:**
```
grep -n "evaluateMatcher" .ok-planner/design/concepts/inertness.md
grep -n "precisely-enumerated sanctioned read sites" .ok-planner/design/concepts/inertness.md
```

---

## Task 28: Update CHANGELOG

**Files:**
- `CHANGELOG.md` (modify)

**Steps:**

1. Open `CHANGELOG.md`. Find the `## Unreleased` section heading. Append a new bullet under it (alongside whatever entries the userdata-collapse plan already wrote there):

   ```markdown
   - **Matcher overlay for attribute_overrides.** `col:rimsky_instances.attribute_overrides` gains a third routing dimension `by_match` — an ordered list of `{matcher, overlay}` entries keyed by a dispatch-time predicate (`node_type`, `executor`, `graph`, `child_key`, `attrs.<path>`). Equality-only grammar; ordinal addressing rejected. Recommended anchor for per-child fan-out routing is `child_key`. Per-entry match counter persists on new column `attribute_overrides_match_counts` for unused-entry observability. Enables consumer tests to script per-(partition, iter, …) executor stubs against a single real template, without forking template variants per child. Structural-inertness discipline (`concept:inertness`) gains a new sanctioned read site at the matcher evaluator — narrowly enumerated, primitive-equality only. Depends on the userdata-collapse work (`attribute_overrides` rename, post-collapse merge layering). See `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`.
   ```

**Verification:**
```
grep -n "Matcher overlay for attribute_overrides" CHANGELOG.md
```

---

## Task 29: Documentation sweep — find userdata/attribute_overrides docs that need updating

**Files:**
- `docs/concepts/*.md` (read; modify if `attribute_overrides` is documented)
- `docs/protocols/executor.md` (read; modify if instance-create body shape is documented)
- `docs/agents/llms.txt` (read; modify if relevant)
- `docs/humans/landing.md` (read; modify if relevant)
- `docs/glossary.md` (read; modify if `attribute_overrides` has an entry)
- `README.md` (read; modify if relevant)

**Steps:**

1. Run a sweep to find every doc that references `attribute_overrides`:

   ```
   grep -rln "attribute_overrides" docs/ README.md
   ```

2. For each hit, read the file. If it documents the shape of `attribute_overrides` (e.g., lists `by_executor` and `by_node` as the top-level keys), extend the documentation to mention `by_match` as a third top-level key with a one-paragraph summary and a link back to `concept:attribute`'s "Matcher overlay (by_match)" section.

3. If a doc lists override-related routes (`POST /instances`, `GET /instances/{id}`), update the response-shape documentation to include `attribute_overrides_match_counts` as an optional field.

4. Do NOT add docs where none existed. The sweep is to keep existing docs in sync — not to expand documentation surface.

**Verification:**
```
grep -rln "attribute_overrides" docs/ README.md | xargs -I{} grep -l "by_match" {}
```
For every file that documents `attribute_overrides` shape, `by_match` should now appear.

---

## Task 30: Scenario test — fan-out partition routing

**Files:**
- `test/scenarios/attribute_overrides_match_overlay_e2e_test.go` (new)

**Steps:**

1. Create a new scenario test that exercises the L5 by_match path end-to-end through the harness. Use the existing `test/scenarios/attribute_overrides_e2e_test.go` (post-collapse work landed it) as the structural cousin to mirror.

2. The scenario shape:

   - Register a template with a fan-out node `fan` that emits three children with `child_key`s `"a"`, `"b"`, `"c"`.
   - Create an instance with:
     ```jsonc
     {
       "attribute_overrides": {
         "by_match": [
           { "matcher": { "node_type": "fan-child", "child_key": "a" },
             "overlay": { "tag": "for-a" } },
           { "matcher": { "node_type": "fan-child", "child_key": "b" },
             "overlay": { "tag": "for-b" } },
           { "matcher": { "node_type": "fan-child", "child_key": "c" },
             "overlay": { "tag": "for-c" } }
         ]
       }
     }
     ```
   - Run the instance to terminal.
   - Assert each child saw the correct `tag` value in its ExecuteRequest attribute bag.
   - Read the instance via persistence; assert `AttributeOverridesMatchCounts == [1, 1, 1]`.

3. If the stub-mode executor needs additional helpers to capture per-child attributes, mirror the existing scenario patterns — do not invent new harness machinery.

**Verification:**
```
go test ./test/scenarios/... -run AttributeOverridesMatchOverlay -count=1
```

---

## Task 31: Scenario test — sub-graph routing

**Files:**
- `test/scenarios/attribute_overrides_match_overlay_subgraph_e2e_test.go` (new)

**Steps:**

1. Create a scenario test with a template that has a `main` graph and a `worker` sub-graph; both contain a node-type called (e.g.) `pass`.

2. Instance creates with:
   ```jsonc
   {
     "attribute_overrides": {
       "by_match": [
         { "matcher": { "node_type": "pass", "graph": "main" },
           "overlay": { "where": "outer" } },
         { "matcher": { "node_type": "pass", "graph": "worker" },
           "overlay": { "where": "inner" } }
       ]
     }
   }
   ```

3. Assert main-graph `pass` dispatches see `where=outer`; sub-graph internal `pass` dispatches see `where=inner`.

4. For the entry-absorbed dispatch case: the calling node (in the outer graph) that delegates to the sub-graph reports `graph = "main"` per the spec's entry-absorbed disposition. Verify a matcher targeting `graph: "worker"` does NOT apply to the calling node's own dispatch — only to internal nodes of the sub-graph.

**Verification:**
```
go test ./test/scenarios/... -run AttributeOverridesMatchOverlaySubgraph -count=1
```

---

## Task 32: Scenario test — declaration-order specificity

**Files:**
- `test/scenarios/attribute_overrides_match_overlay_order_e2e_test.go` (new)

**Steps:**

1. Two `by_match` entries that both match the same dispatch; their overlays touch overlapping attribute paths. Verify the later entry wins on conflicts, and non-conflicting paths from both apply.

**Verification:**
```
go test ./test/scenarios/... -run AttributeOverridesMatchOverlayOrder -count=1
```

---

## Task 33: Scenario test — unused-entry observability

**Files:**
- `test/scenarios/attribute_overrides_match_overlay_unused_e2e_test.go` (new)

**Steps:**

1. Create an instance with five `by_match` entries; only two match any dispatch during the instance's run (e.g., two match real `child_key` values that fire; three target a node_type that doesn't dispatch).

2. After the instance reaches terminal, fetch via persistence and assert `AttributeOverridesMatchCounts` has nonzero positions only at the indices that fired.

**Verification:**
```
go test ./test/scenarios/... -run AttributeOverridesMatchOverlayUnused -count=1
```

---

## Task 34: Scenario test — validation rejection

**Files:**
- `test/scenarios/attribute_overrides_match_overlay_rejection_e2e_test.go` (new)

**Steps:**

1. Issue `POST /instances` with a request body containing a `by_match` entry whose matcher has `dispatch_index: 2`.

2. Assert the response is HTTP 400 with a message that names `dispatch_index` and redirects to `child_key` or `attrs.<path>`.

3. Repeat for: `partition_index`, `nth_child`, `seq`, an unknown matcher key (`node_name`), and a non-primitive `attrs.<path>` value.

**Verification:**
```
go test ./test/scenarios/... -run AttributeOverridesMatchOverlayRejection -count=1
```

---

## Task 35: Race coverage

**Files:**
- (verification-only — no code changes)

**Steps:**

1. Run the runtime + persistence packages with `-race` to confirm the counter-increment path has no race-detector hits:

   ```
   go test -race -count=3 ./foundation/persistence/postgres/...
   go test -race -count=3 ./foundation/persistence/sqlite/...
   go test -race -count=3 ./runtime/...
   ```

2. All must pass with no race warnings.

**Verification:**
```
go test -race -count=3 ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... ./runtime/...
```

---

## Task 36: Full build + test + lint sweep

**Files:**
- (verification-only — no code changes)

**Steps:**

1. Run the canonical `make` targets per the repo's CLAUDE.md "After Code Changes" guidance:

   ```
   go build ./...
   go test ./...
   make lint
   ```

2. Address any failures by going back to the relevant task.

**Verification:**
```
go build ./... && go test ./... && make lint
```
All must succeed.

---

## Manual checks after completion

(None — every verification in this plan is automatable via `go test` / `make`. The matcher overlay is rimsky-internal; no UI to inspect, no externally-visible behavior change beyond the documented `POST /instances` body extension and the new `GET /instances/{id}` response field, both covered by the scenario tests.)
