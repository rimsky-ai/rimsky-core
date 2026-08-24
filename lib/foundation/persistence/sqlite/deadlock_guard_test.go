// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: executor
// @concept: orphan-reaper

package sqlite_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func seedDispatchInstance(t *testing.T, ctx context.Context, d persistence.Database) (shared.UUID, shared.UUID) {
	t.Helper()
	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	scopeID := uuid.New()
	frameID := uuid.New()
	nodeID := uuid.New()
	runID := shared.UUID(uuid.New())
	msgID := uuid.New().String()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, target_routing_identity) VALUES (?, ?, 'test-daemon')`,
		instanceID.String(), templateID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID.String(), instanceID.String(),
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID.String(),
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, root_run_scope_id, started_at)
		 VALUES (?, ?, ?, ?, datetime('now'))`,
		frameID.String(), instanceID.String(), msgID, scopeID.String(),
	); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
		nodeID.String(), instanceID.String(),
	); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_claim_producers, enqueued_at, state, creation_reason, sequence, frame_id,
		    run_scope_id, claimed_by, claimed_at, last_progress_at)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'running', 'cascade', 1, ?, ?,
		         'sup-test', datetime('now'), datetime('now'))`,
		runID.String(), nodeID.String(), frameID.String(), scopeID.String(),
	); err != nil {
		t.Fatalf("seed in-flight run: %v", err)
	}
	return runID, scopeID
}

// @concept: executor
func TestQueue_BumpLastProgressAt_NoDeadlockUnderContention(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		concurrency int
		bumpsEach   int
	}{
		{name: "low_contention", concurrency: 4, bumpsEach: 16},
		{name: "moderate_contention", concurrency: 16, bumpsEach: 32},
		{name: "high_contention", concurrency: 32, bumpsEach: 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			d := openSQLite(t)
			runID, _ := seedDispatchInstance(t, ctx, d)
			store := d.Tables()

			var wg sync.WaitGroup
			var bumps atomic.Int64
			wg.Add(tc.concurrency)

			for g := 0; g < tc.concurrency; g++ {
				go func() {
					defer wg.Done()
					for i := 0; i < tc.bumpsEach; i++ {
						err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
							_, berr := d.Queue().BumpLastProgressAt(ctx, runID, time.Now(), tx)
							return berr
						})
						if err != nil {
							t.Errorf("BumpLastProgressAt: %v", err)
							return
						}
						bumps.Add(1)
					}
				}()
			}
			wg.Wait()
			expected := int64(tc.concurrency * tc.bumpsEach)
			if got := bumps.Load(); got != expected {
				t.Fatalf("bumps completed=%d want=%d (some goroutines deadlocked or errored)", got, expected)
			}
		})
	}
}

// @concept: orphan-reaper
func TestQueue_BumpAndSweepConcurrent_NoDeadlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLite(t)
	runID, _ := seedDispatchInstance(t, ctx, d)
	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	if _, err := rawDB.ExecContext(ctx,
		`UPDATE rimsky_node_runs SET async_ack_id = 'ack-sweep' WHERE id = ?`,
		runID.String(),
	); err != nil {
		t.Fatalf("set async_ack_id: %v", err)
	}
	store := d.Tables()

	const (
		bumpGoroutines  = 8
		sweepGoroutines = 4
		bumpsEach       = 32
		sweepsEach      = 32
	)
	var wg sync.WaitGroup
	wg.Add(bumpGoroutines + sweepGoroutines)

	for g := 0; g < bumpGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < bumpsEach; i++ {
				err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
					_, berr := d.Queue().BumpLastProgressAt(ctx, runID, time.Now(), tx)
					return berr
				})
				if err != nil {
					t.Errorf("bump: %v", err)
					return
				}
			}
		}()
	}

	go func() {
		wg2 := sync.WaitGroup{}
		wg2.Add(sweepGoroutines)
		for g := 0; g < sweepGoroutines; g++ {
			go func() {
				defer wg2.Done()
				defer wg.Done()
				for i := 0; i < sweepsEach; i++ {
					if _, err := d.Queue().ListOrphanedClaims(ctx); err != nil {
						t.Errorf("sweep ListOrphanedClaims: %v", err)
						return
					}
				}
			}()
		}
		wg2.Wait()
	}()

	wg.Wait()
}

func TestQueue_PoolWidthDoesNotStarveLockFreeRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLite(t)
	runID, _ := seedDispatchInstance(t, ctx, d)
	store := d.Tables()

	bumperStarted := make(chan struct{})
	release := make(chan struct{})
	bumperErr := make(chan error, 1)
	go func() {
		bumperErr <- store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if _, berr := d.Queue().BumpLastProgressAt(ctx, runID, time.Now(), tx); berr != nil {
				return berr
			}
			close(bumperStarted)
			<-release
			return nil
		})
	}()
	<-bumperStarted

	_, err := d.Queue().ListLive(ctx, persistence.DispatchListFilter{}, persistence.ListPagination{Limit: 10})
	close(release)
	if err != nil {
		t.Fatalf("ListLive starved by held bumper tx: %v "+
			"(wide pool guarantee: a held writer conn must NOT block lock-free reads)", err)
	}
	if err := <-bumperErr; err != nil {
		t.Fatalf("bumper tx: %v", err)
	}
}

// @decision: async-callback-persistent-registry
func TestQueue_RegisterAsyncAckAndLookupRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLite(t)
	runID, _ := seedDispatchInstance(t, ctx, d)
	store := d.Tables()

	const ackID = "ack-roundtrip"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Queue().RegisterAsyncAck(ctx, runID, ackID, time.Now(), nil, nil, "", "", tx)
	}); err != nil {
		t.Fatalf("RegisterAsyncAck: %v", err)
	}

	var got *persistence.DispatchRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, lerr := d.Queue().LookupRunByAsyncAckID(ctx, ackID, tx)
		got = row
		return lerr
	}); err != nil {
		t.Fatalf("LookupRunByAsyncAckID: %v", err)
	}
	if got == nil {
		t.Fatal("LookupRunByAsyncAckID returned nil for a just-registered ack id; the persistent registry must survive in-process")
	}
	if got.ID != runID {
		t.Fatalf("LookupRunByAsyncAckID returned run id %s, want %s", got.ID, runID)
	}
}
