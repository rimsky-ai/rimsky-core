// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestNodeAttributesSpillRoundtrip(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	nodeID, runID := seedFixtureNodeAndRun(t, rawDB)

	small := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, small, tx)
	}); err != nil {
		t.Fatalf("Upsert small: %v", err)
	}
	verifyNoSpill(t, rawDB, runID)

	got := readData(t, store, runID)
	if got["k"] != "v" {
		t.Fatalf("Get small: got %v, want k=v", got)
	}

	bigVal := strings.Repeat("x", 500)
	large := map[string]any{"big": bigVal, "tag": "first"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, large, tx)
	}); err != nil {
		t.Fatalf("Upsert large: %v", err)
	}
	firstHandle := verifySpill(t, rawDB, runID, mem.Name())

	got = readData(t, store, runID)
	if got["big"] != bigVal {
		t.Fatalf("Get large: big mismatch")
	}
	if got["tag"] != "first" {
		t.Fatalf("Get large: tag=%v, want first", got["tag"])
	}

	large2 := map[string]any{"big": bigVal, "tag": "second"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, large2, tx)
	}); err != nil {
		t.Fatalf("Upsert large2: %v", err)
	}
	secondHandle := verifySpill(t, rawDB, runID, mem.Name())
	if firstHandle == secondHandle {
		t.Fatalf("expected new handle on overwrite; both = %q", firstHandle)
	}

	orphRows, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	foundFirst := false
	for _, r := range orphRows {
		if r.Handle == firstHandle {
			foundFirst = true
			if r.Backend != mem.Name() {
				t.Fatalf("orphan row backend: got %q, want %q", r.Backend, mem.Name())
			}
		}
	}
	if !foundFirst {
		t.Fatalf("first handle %q not found in orphans (got %d rows)", firstHandle, len(orphRows))
	}

	tiny := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, tiny, tx)
	}); err != nil {
		t.Fatalf("Upsert tiny: %v", err)
	}
	verifyNoSpill(t, rawDB, runID)

	orphRows, err = orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore (post-downgrade): %v", err)
	}
	foundSecond := false
	for _, r := range orphRows {
		if r.Handle == secondHandle {
			foundSecond = true
		}
	}
	if !foundSecond {
		t.Fatalf("second handle not found in orphans on downgrade (got %d rows)", len(orphRows))
	}
}

func TestNodeAttributesMergeDeltaSpill(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	nodeID, runID := seedFixtureNodeAndRun(t, rawDB)

	bigVal := strings.Repeat("y", 500)
	initial := map[string]any{"big": bigVal, "phase": "a"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, initial, tx)
	}); err != nil {
		t.Fatalf("Upsert initial: %v", err)
	}
	_ = verifySpill(t, rawDB, runID, mem.Name())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.MergeDelta(ctx, runID, map[string]any{"phase": "b"}, tx)
	}); err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	got := readData(t, store, runID)
	if got["big"] != bigVal {
		t.Fatalf("MergeDelta: big lost")
	}
	if got["phase"] != "b" {
		t.Fatalf("MergeDelta: phase=%v, want b", got["phase"])
	}
	_ = verifySpill(t, rawDB, runID, mem.Name())
}

func TestSnapshotBagCarriesForwardSpilledBlobWithoutAliasing(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	nodeID, priorRunID := seedFixtureNodeAndRun(t, rawDB)
	scopeID := runScopeOf(t, rawDB, priorRunID)

	bigVal := strings.Repeat("z", 500)
	priorBag := map[string]any{"big": bigVal, "tag": "prior"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, priorRunID, nodeID, priorBag, tx)
	}); err != nil {
		t.Fatalf("Upsert prior spilled: %v", err)
	}
	priorHandle := verifySpill(t, rawDB, priorRunID, mem.Name())

	newRunID := seedSecondRun(t, rawDB, nodeID, priorRunID, scopeID, 2)
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.SnapshotBagForNewRun(ctx, tx, newRunID, nodeID, scopeID)
	}); err != nil {
		t.Fatalf("SnapshotBagForNewRun: %v", err)
	}

	newHandle := verifySpill(t, rawDB, newRunID, mem.Name())
	if newHandle == priorHandle {
		t.Fatalf("carry-forward aliased the blob handle across runs: both = %q", newHandle)
	}

	snap, err := readDispatchBag(t, store, newRunID)
	if err != nil {
		t.Fatalf("GetDispatchInputBag(new): %v", err)
	}
	if snap["big"] != bigVal || snap["tag"] != "prior" {
		t.Fatalf("carried dispatch_input_bag lost spilled content: got %+v", snap)
	}

	newBag := map[string]any{"big": strings.Repeat("q", 500), "tag": "new"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, newRunID, nodeID, newBag, tx)
	}); err != nil {
		t.Fatalf("Upsert on new run: %v", err)
	}

	orphRows, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	for _, r := range orphRows {
		if r.Handle == priorHandle {
			t.Fatalf("Upsert on new run enrolled the prior run's still-referenced blob %q as an orphan", priorHandle)
		}
		if err := mem.Delete(ctx, persistence.Handle(r.Handle)); err != nil {
			t.Fatalf("reap orphan %q: %v", r.Handle, err)
		}
	}

	got := readData(t, store, priorRunID)
	if got["big"] != bigVal || got["tag"] != "prior" {
		t.Fatalf("prior run's spilled value was destroyed by new run's Upsert+reap: got %+v", got)
	}
}

func TestNodeAttributesBackendMismatchFallsBackToInlineData(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	nodeID, runID := seedFixtureNodeAndRun(t, rawDB)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, map[string]any{"k": "v"}, tx)
	}); err != nil {
		t.Fatalf("Upsert seed: %v", err)
	}

	if _, err := rawDB.ExecContext(ctx,
		`UPDATE rimsky_node_attributes
		    SET data                 = ?,
		        value_handle         = ?,
		        value_handle_backend = ?
		  WHERE node_run_id = ?`,
		`{"inline_survivor":"yes"}`, "handle-under-other-backend", "other-backend", runID.String(),
	); err != nil {
		t.Fatalf("seed backend mismatch: %v", err)
	}

	got := readData(t, store, runID)
	if got["inline_survivor"] != "yes" {
		t.Fatalf("GetByRun with mismatched value_handle_backend = %+v; want inline data column fallback", got)
	}
}

func runScopeOf(t *testing.T, rawDB *sql.DB, runID uuid.UUID) uuid.UUID {
	t.Helper()
	var scope string
	if err := rawDB.QueryRowContext(context.Background(),
		`SELECT run_scope_id FROM rimsky_node_runs WHERE id = ?`, runID.String(),
	).Scan(&scope); err != nil {
		t.Fatalf("runScopeOf: %v", err)
	}
	id, err := uuid.Parse(scope)
	if err != nil {
		t.Fatalf("runScopeOf parse: %v", err)
	}
	return id
}

func seedSecondRun(t *testing.T, rawDB *sql.DB, nodeID, priorRunID, scopeID uuid.UUID, sequence int) uuid.UUID {
	t.Helper()
	var frameID string
	if err := rawDB.QueryRowContext(context.Background(),
		`SELECT frame_id FROM rimsky_node_runs WHERE id = ?`, priorRunID.String(),
	).Scan(&frameID); err != nil {
		t.Fatalf("seedSecondRun: read frame: %v", err)
	}
	newRunID := uuid.New()
	if _, err := rawDB.ExecContext(context.Background(),
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'stale', 'cascade', ?, ?, ?)`,
		newRunID.String(), nodeID.String(), sequence, frameID, scopeID.String(),
	); err != nil {
		t.Fatalf("seedSecondRun: insert run: %v", err)
	}
	return newRunID
}

func readDispatchBag(t *testing.T, store persistence.Tables, runID uuid.UUID) (map[string]any, error) {
	t.Helper()
	var out map[string]any
	err := store.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		bag, err := store.NodeAttributes().GetDispatchInputBag(ctx, tx, runID)
		out = bag
		return err
	})
	return out, err
}

func verifyNoSpill(t *testing.T, rawDB *sql.DB, runID uuid.UUID) {
	t.Helper()
	h, _ := readSpillHandle(t, rawDB, runID)
	if h != "" {
		t.Fatalf("expected no spill handle for %s, got %q", runID, h)
	}
}

func verifySpill(t *testing.T, rawDB *sql.DB, runID uuid.UUID, wantBackend string) string {
	t.Helper()
	h, b := readSpillHandle(t, rawDB, runID)
	if h == "" {
		t.Fatalf("expected spill handle for %s, got empty", runID)
	}
	if b != wantBackend {
		t.Fatalf("expected spill backend %q, got %q", wantBackend, b)
	}
	return h
}

func seedFixtureNodeAndRun(t *testing.T, rawDB *sql.DB) (uuid.UUID, uuid.UUID) {
	t.Helper()
	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	nodeID := uuid.New()
	frameID := uuid.New().String()
	runID := uuid.New()
	ctx := context.Background()
	_, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	)
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
	scopeID := uuid.New().String()
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash) VALUES (?, ?)`,
		instanceID, templateID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID,
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
		nodeID.String(), instanceID,
	)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at)
		 VALUES (?, ?, ?, ?,
		         datetime('now'))`,
		frameID, instanceID, msgID, scopeID,
	)
	if err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'stale', 'cascade', 1, ?, ?)`,
		runID.String(), nodeID.String(), frameID, scopeID,
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return nodeID, runID
}

func readSpillHandle(t *testing.T, rawDB *sql.DB, runID uuid.UUID) (string, string) {
	t.Helper()
	row := rawDB.QueryRowContext(context.Background(),
		`SELECT value_handle, value_handle_backend FROM rimsky_node_attributes WHERE node_run_id = ?`,
		runID.String(),
	)
	var h, b sql.NullString
	if err := row.Scan(&h, &b); err != nil {
		if err == sql.ErrNoRows {
			return "", ""
		}
		t.Fatalf("readSpillHandle scan: %v", err)
	}
	hs := ""
	bs := ""
	if h.Valid {
		hs = h.String
	}
	if b.Valid {
		bs = b.String
	}
	return hs, bs
}

func readData(t *testing.T, store persistence.Tables, runID uuid.UUID) map[string]any {
	t.Helper()
	var out *persistence.NodeAttributesRow
	if err := store.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		out = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out == nil {
		t.Fatalf("Get returned nil")
	}
	return out.Data
}
