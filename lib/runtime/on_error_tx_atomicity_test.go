// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// on_error_tx_atomicity_test.go pins
// @blessed-invariant: State-machine writes for a single run must be
// tx-atomic. Specifically, OnError's retry path must perform the
// (state-write + remove-from-queue + enqueue-new-dispatch) sequence
// inside a single transaction. Per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
//
// A pre-fix regression where the retry path opened a fresh
// `sb.Transaction` for each of `UpdateState` and the
// `RemoveForNodeInTx + EnqueueInTx` pair would leave the row stranded
// if the second commit failed (state advanced but no replacement
// enqueued). The fix collapsed those into one tx; this test counts
// each `Tx` pointer threaded through the queue mutators on the retry
// path and asserts the trio share one tx.

package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// txTrackingQueue wraps a persistence.Queue and records the Tx pointer
// passed to each retry-path mutator. The test inspects the trail after
// OnError returns to verify the trio (UpdateState, RemoveForNodeInTx,
// EnqueueInTx) share a single Tx.
//
// We only need to instrument RemoveForNodeInTx and EnqueueInTx here;
// UpdateState is captured via txTrackingNodes below.
type txTrackingQueue struct {
	persistence.Queue
	mu             sync.Mutex
	removeForNodes []persistence.Tx
	enqueueInTxs   []persistence.Tx
}

func (q *txTrackingQueue) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expected string, tx persistence.Tx) error {
	q.mu.Lock()
	q.removeForNodes = append(q.removeForNodes, tx)
	q.mu.Unlock()
	return q.Queue.RemoveForNodeInTx(ctx, nodeID, runScopeID, expected, tx)
}

func (q *txTrackingQueue) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	q.mu.Lock()
	q.enqueueInTxs = append(q.enqueueInTxs, tx)
	q.mu.Unlock()
	return q.Queue.EnqueueInTx(ctx, req, tx)
}

// txTrackingNodes wraps a persistence.NodeTable and records the Tx
// pointer passed to UpdateState (the retry path's state write).
type txTrackingNodes struct {
	persistence.NodeTable
	mu              sync.Mutex
	updateStateTxs  []persistence.Tx
	updateStateArgs []cascade.NodeState
}

func (n *txTrackingNodes) UpdateState(ctx context.Context, id shared.UUID, runScopeID shared.UUID, state cascade.NodeState, reason cascade.TransitionReason, settlingSignalType *string, tx persistence.Tx) error {
	n.mu.Lock()
	n.updateStateTxs = append(n.updateStateTxs, tx)
	n.updateStateArgs = append(n.updateStateArgs, state)
	n.mu.Unlock()
	return n.NodeTable.UpdateState(ctx, id, runScopeID, state, reason, settlingSignalType, tx)
}

// nodeTrackingTables wraps persistence.Tables to route Nodes() through
// txTrackingNodes. Other accessors delegate to inner.
type nodeTrackingTables struct {
	inner persistence.Tables
	nodes *txTrackingNodes
}

func (w *nodeTrackingTables) Templates() persistence.TemplateTable { return w.inner.Templates() }
func (w *nodeTrackingTables) TemplateTags() persistence.TemplateTagTable {
	return w.inner.TemplateTags()
}
func (w *nodeTrackingTables) Instances() persistence.InstanceTable { return w.inner.Instances() }
func (w *nodeTrackingTables) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return w.inner.LifecycleIdempotency()
}
func (w *nodeTrackingTables) Nodes() persistence.NodeTable { return w.nodes }
func (w *nodeTrackingTables) ClaimHandles() persistence.ClaimHandleTable {
	return w.inner.ClaimHandles()
}
func (w *nodeTrackingTables) NodeAttributes() persistence.NodeAttributeTable {
	return w.inner.NodeAttributes()
}
func (w *nodeTrackingTables) ClaimHolders() persistence.ClaimHolderTable {
	return w.inner.ClaimHolders()
}
func (w *nodeTrackingTables) Events() persistence.EventTable           { return w.inner.Events() }
func (w *nodeTrackingTables) Supervisors() persistence.SupervisorTable { return w.inner.Supervisors() }
func (w *nodeTrackingTables) Frames() persistence.FrameTable           { return w.inner.Frames() }
func (w *nodeTrackingTables) BlobOrphans() persistence.BlobOrphanTable { return w.inner.BlobOrphans() }
func (w *nodeTrackingTables) NodeEvents() persistence.NodeEventTable   { return w.inner.NodeEvents() }
func (w *nodeTrackingTables) WaitSet() persistence.WaitSetTable        { return w.inner.WaitSet() }
func (w *nodeTrackingTables) Messages() persistence.MessagesTable      { return w.inner.Messages() }
func (w *nodeTrackingTables) MessageIdempotencies() persistence.MessageIdempotencyTable {
	return w.inner.MessageIdempotencies()
}
func (w *nodeTrackingTables) Lineage() persistence.LineageTable { return w.inner.Lineage() }
func (w *nodeTrackingTables) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return w.inner.PublisherSubscriptions()
}
func (w *nodeTrackingTables) RunTree() persistence.RunTreeTable    { return w.inner.RunTree() }
func (w *nodeTrackingTables) RunScopes() persistence.RunScopeTable { return w.inner.RunScopes() }
func (w *nodeTrackingTables) APIKeys() persistence.APIKeyTable     { return w.inner.APIKeys() }
func (w *nodeTrackingTables) Breakpoints() persistence.BreakpointTable {
	return w.inner.Breakpoints()
}
func (w *nodeTrackingTables) BreakpointHits() persistence.BreakpointHitTable {
	return w.inner.BreakpointHits()
}

func (w *nodeTrackingTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return w.inner.Transaction(ctx, fn)
}

// TestOnErrorTxAtomicity verifies the retry path performs (state-write
// + remove-from-queue + enqueue-new-dispatch) inside a single tx.
//
// The test instruments NodeTable.UpdateState, Queue.RemoveForNodeInTx,
// and Queue.EnqueueInTx; each records the tx pointer it was invoked
// with. After OnError returns, the test asserts the three retry-path
// writes share exactly one tx pointer (the same Tx value).
//
// Regression coverage: if the retry path's state write or the
// remove+enqueue gets split into a separate `sb.Transaction(...)`
// block, the tx pointers will differ and the assertion trips.
func TestOnErrorTxAtomicity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dir := t.TempDir()
	cfg := persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	}
	d, err := persistence.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := d.Tables()
	q := d.Queue()

	templateHash := "sha256-on-error-tx-atomicity"
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())
	var frameID shared.UUID

	tmplSpec := spec.TemplateSpec{
		Name:                "on-error-tx-test",
		Version:             "1",
		FrameResolutionMode: spec.FrameResolutionSerialQueue,
		FrameTimeoutMs:      600000,
		Nodes: []spec.TemplateNodeDef{
			{
				Type:     "worker",
				Executor: "test-executor",
				ErrorTypes: map[string]spec.ErrorTypePolicy{
					"test_error": {
						Policy: []spec.PolicyAction{{Action: "retry", Count: 3}},
					},
				},
			},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplSpec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   "worker",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		fid, err := store.Frames().EnqueueSerialFrame(ctx, instanceID, nodeID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if _, err := store.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:       nodeID,
			ExecutorName: "test-executor",
			EnqueuedAt:   time.Now().Add(-time.Second),
			FrameID:      frameID,
			RunScopeID:   mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			Limit:             16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				if _, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, "sup-A"); err != nil {
					return err
				}
				break
			}
		}
		if err := store.Nodes().SetFrameID(ctx, nodeID, &frameID, tx); err != nil {
			return err
		}
		return store.Nodes().UpdateState(ctx, nodeID, mainRunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Wrap nodes + queue for tx tracking. The wrapped Tables is what
	// OnError sees.
	trackedNodes := &txTrackingNodes{NodeTable: store.Nodes()}
	trackedQueue := &txTrackingQueue{Queue: q}
	tracked := &nodeTrackingTables{inner: store, nodes: trackedNodes}

	if err := OnError(ctx, OnErrorArgs{
		Persist:      tracked,
		Queue:        trackedQueue,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		NodeID:       nodeID,
		InstanceID:   instanceID,
		RunScopeID:   mainRunScopeID,
		SupervisorID: "sup-A",
		ErrorClass:   "test_error",
		Payload:      map[string]any{},
	}); err != nil {
		t.Fatalf("OnError: %v", err)
	}

	// Find the retry path's UpdateState — the call writing
	// NodeStateStale (the only `policy_retry` state write). The seed
	// step earlier issued an UpdateState(NodeStateRunning); skip those.
	var retryStateTx persistence.Tx
	found := false
	for i, st := range trackedNodes.updateStateArgs {
		if st == cascade.NodeStateStale {
			retryStateTx = trackedNodes.updateStateTxs[i]
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("retry path UpdateState(stale) not observed; calls: %v", trackedNodes.updateStateArgs)
	}

	if len(trackedQueue.removeForNodes) == 0 {
		t.Fatalf("retry path did not call Queue.RemoveForNodeInTx")
	}
	// The first RemoveForNodeInTx after seed must share the retry state tx.
	if trackedQueue.removeForNodes[0] != retryStateTx {
		t.Fatalf("retry path opened a new tx between UpdateState and RemoveForNodeInTx: "+
			"state tx=%p, remove tx=%p — must be the same tx",
			retryStateTx, trackedQueue.removeForNodes[0])
	}

	if len(trackedQueue.enqueueInTxs) == 0 {
		t.Fatalf("retry path did not call Queue.EnqueueInTx")
	}
	if trackedQueue.enqueueInTxs[0] != retryStateTx {
		t.Fatalf("retry path opened a new tx between UpdateState/Remove and EnqueueInTx: "+
			"state tx=%p, enqueue tx=%p — must be the same tx",
			retryStateTx, trackedQueue.enqueueInTxs[0])
	}
}
