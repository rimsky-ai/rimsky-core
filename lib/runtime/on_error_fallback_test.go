// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func seedFallbackNode(t *testing.T, ctx context.Context, suffix string) (persistence.Tables, persistence.Queue, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
	return seedFallbackNodeWithErrorTypes(t, ctx, suffix, map[string]spec.ErrorTypePolicy{
		"acquire/unavailable": {
			Policy: []spec.PolicyAction{{Action: "retry", Count: 3}},
		},
	})
}

func seedFallbackNodeWithErrorTypes(
	t *testing.T, ctx context.Context, suffix string,
	errorTypes map[string]spec.ErrorTypePolicy,
) (persistence.Tables, persistence.Queue, shared.UUID, shared.UUID, shared.UUID) {
	t.Helper()
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

	templateHash := "sha256-on-error-fallback-" + suffix
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

	tmplSpec := spec.TemplateSpec{
		Name:           "on-error-fallback-test-" + suffix,
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{
				Type:       "worker",
				Executor:   "test-executor",
				ErrorTypes: errorTypes,
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
		msgID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := store.Frames().InsertFrame(ctx, instanceID, msgID, 600000, tx)
		if err != nil {
			return err
		}
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
	return store, q, nodeID, instanceID, mainRunScopeID
}

func nodeState(t *testing.T, ctx context.Context, store persistence.Tables, nodeID shared.UUID) cascade.NodeState {
	t.Helper()
	var st cascade.NodeState
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		n, err := store.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		st = n.State
		return nil
	}); err != nil {
		t.Fatalf("read node: %v", err)
	}
	return st
}

func TestOnError_AcquireFallback_ProducerClassRoutesToGenericRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, q, nodeID, instanceID, runScopeID := seedFallbackNode(t, ctx, "retry")

	if err := OnError(ctx, OnErrorArgs{
		Persist:             store,
		Queue:               q,
		Clock:               shared.SystemClock{},
		Logger:              shared.SilentLogger{},
		NodeID:              nodeID,
		InstanceID:          instanceID,
		RunScopeID:          runScopeID,
		SupervisorID:        "sup-A",
		ErrorClass:          "pg/claim_unavailable",
		PolicyFallbackClass: "acquire/unavailable",
		Payload:             map[string]any{},
	}); err != nil {
		t.Fatalf("OnError: %v", err)
	}

	if got := nodeState(t, ctx, store, nodeID); got != cascade.NodeStateStale {
		t.Fatalf("node state = %s, want %s (retry route via acquire/unavailable fallback)",
			got, cascade.NodeStateStale)
	}
}

func TestOnError_NoFallback_ProducerClassGivesUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, q, nodeID, instanceID, runScopeID := seedFallbackNode(t, ctx, "giveup")

	if err := OnError(ctx, OnErrorArgs{
		Persist:      store,
		Queue:        q,
		Clock:        shared.SystemClock{},
		Logger:       shared.SilentLogger{},
		NodeID:       nodeID,
		InstanceID:   instanceID,
		RunScopeID:   runScopeID,
		SupervisorID: "sup-A",
		ErrorClass:   "pg/claim_unavailable",
		Payload:      map[string]any{},
	}); err != nil {
		t.Fatalf("OnError: %v", err)
	}

	if got := nodeState(t, ctx, store, nodeID); got != cascade.NodeStateFailed {
		t.Fatalf("node state = %s, want %s (no fallback → unknown-class give_up default)",
			got, cascade.NodeStateFailed)
	}
}

func TestOnError_ExactKeyWinsOverFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, q, nodeID, instanceID, runScopeID := seedFallbackNodeWithErrorTypes(t, ctx, "exactwins",
		map[string]spec.ErrorTypePolicy{
			"pg/claim_unavailable": {
				Policy: []spec.PolicyAction{{Action: "give_up"}},
			},
			"acquire/unavailable": {
				Policy: []spec.PolicyAction{{Action: "retry", Count: 3}},
			},
		})

	if err := OnError(ctx, OnErrorArgs{
		Persist:             store,
		Queue:               q,
		Clock:               shared.SystemClock{},
		Logger:              shared.SilentLogger{},
		NodeID:              nodeID,
		InstanceID:          instanceID,
		RunScopeID:          runScopeID,
		SupervisorID:        "sup-A",
		ErrorClass:          "pg/claim_unavailable",
		PolicyFallbackClass: "acquire/unavailable",
		Payload:             map[string]any{},
	}); err != nil {
		t.Fatalf("OnError: %v", err)
	}

	if got := nodeState(t, ctx, store, nodeID); got != cascade.NodeStateFailed {
		t.Fatalf("node state = %s, want %s (exact-key give_up must win over the fallback retry)",
			got, cascade.NodeStateFailed)
	}
}
