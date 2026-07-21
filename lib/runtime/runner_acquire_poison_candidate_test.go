// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func insertDeployedTemplateInternal(ctx context.Context, t *testing.T, sb persistence.Tables, spec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(spec.Name + ":" + spec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: spec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := sb.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	return *row
}

func seedStaleCandidateInternal(
	ctx context.Context, t *testing.T, sb persistence.Tables, q persistence.Queue,
	tmplHash string, nodeType string,
) (nodeID, frameID, nodeRunID, runScopeID, instanceID shared.UUID) {
	t.Helper()
	ck := "ck-" + uuid.NewString()
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		if _, err := sb.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmplHash, InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		n, err := sb.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID, NodeType: nodeType, Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nodeID = n.ID
		return nil
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := sb.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := sb.Frames().InsertRunningFrame(ctx, instID, msgID, mainScopeID, tx)
		frameID = fid
		return err
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID: nodeID, ExecutorName: "stub", RequiredClaimProducers: []string{},
			EnqueuedAt: time.Now().Add(-time.Second), FrameID: frameID, RunScopeID: mainScopeID,
		}, tx)
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"}, AcceptedClaimProducers: []string{}, Limit: 16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				nodeRunID = c.NodeRunID
				return nil
			}
		}
		t.Fatalf("seedStaleCandidateInternal: candidate not surfaced for node %s", nodeID)
		return nil
	}))
	return nodeID, frameID, nodeRunID, mainScopeID, instID
}

func runStateInternal(ctx context.Context, t *testing.T, sb persistence.Tables, nodeRunID shared.UUID) string {
	t.Helper()
	var state string
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := sb.Nodes().GetRunForGate(ctx, tx, nodeRunID)
		if err != nil {
			return err
		}
		if row == nil {
			t.Fatalf("runStateInternal: run %s not found", nodeRunID)
		}
		state = string(row.State)
		return nil
	}))
	return state
}

// @concept: frame
// @concept: error-policy
func TestTryAcquire_NullFrameIDCandidateReturnsSentinelWithFullContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "nil-frame-id-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "leaf", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, _, _ := seedStaleCandidateInternal(ctx, t, backend, d.Queue(), tmpl.ID, "leaf")

	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID, FrameID: shared.UUID{}}
	args := RunArgs{Persist: backend, Logger: shared.SilentLogger{}}

	var acq acquisition
	var ok bool
	var acquireErr error
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		acq, ok, acquireErr = tryAcquire(ctx, args, tx, cand, 5*time.Second)
		return nil
	}))
	if !errors.Is(acquireErr, errAcquireNilFrameID) {
		t.Fatalf("expected errAcquireNilFrameID sentinel; got ok=%v err=%v", ok, acquireErr)
	}
	if acq.NodeDef == nil {
		t.Fatal("expected NodeDef to be resolved so the poison row can be routed through error policy")
	}
	if acq.NodeID != nodeID || acq.NodeRunID != nodeRunID {
		t.Fatalf("acquisition context incomplete: NodeID=%v NodeRunID=%v", acq.NodeID, acq.NodeRunID)
	}
}

// @concept: frame
// @concept: error-policy
func TestHandleAcquireNilFrameID_TerminalizesPoisonRowInsteadOfLoopingForever(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "nil-frame-id-terminalize-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "leaf", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "leaf")

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "stale" {
		t.Fatalf("precondition: expected a freshly-enqueued candidate to be 'stale', got %q", got)
	}

	nodeDef := &node.TemplateNodeDef{Type: "leaf", Executor: "stub"}
	acq := acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "leaf",
		NodeDef: nodeDef, RunScopeID: runScopeID,
	}
	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, SupervisorID: "sup-nil-frame",
		Clock: shared.SystemClock{},
	}

	decision := handleAcquireNilFrameID(ctx, args, acq, cand)
	if decision != nil && decision.IsRetry() {
		t.Fatalf("expected a give-up resolution (no configured retry policy for a poison row), got retry decision: %+v", decision)
	}

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "failed" {
		t.Fatalf("expected the poisoned candidate to settle to 'failed', got %q", got)
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"}, AcceptedClaimProducers: []string{}, Limit: 16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeRunID == nodeRunID {
				t.Fatalf("poisoned candidate %s is still selectable after handleAcquireNilFrameID; "+
					"it must be retired, not re-offered every sweep", nodeRunID)
			}
		}
		return nil
	}))
}

// @concept: fan-out
// @concept: error-policy
func TestAcquireFanOutIfDeclared_PartitionRequestSubstitutionFailureReturnsSentinel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	scopeID := shared.UUID(uuid.New())
	scopes := &staticScopeTable{}
	_ = scopes.Create(ctx, nil, persistence.RunScopeRow{ID: scopeID, GraphName: "main"})

	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-root",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{{messages.invalidate.partition_request_override}}`,
			ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
		},
	}
	acquiredLocks := []AcquiredLock{{
		Alias:       "data",
		Spec:        claimproducer.ClaimSpec{ProducerName: "store", Intent: "rw"},
		ClaimResult: claimproducer.ClaimResult{ClaimScope: json.RawMessage(`"parent-scope"`)},
	}}

	out := &acquisition{InstanceID: instanceID, RunScopeID: scopeID}
	cand := persistence.Candidate{
		FrameID: frameID, NodeID: shared.UUID(uuid.New()), NodeRunID: shared.UUID(uuid.New()),
	}
	args := RunArgs{
		Persist: &messagesPersist{scopeOnlyPersist: scopeOnlyPersist{scopes: scopes}, msgs: newFakeMessages()},
		Logger:  shared.SilentLogger{},
	}

	err := acquireFanOutIfDeclared(ctx, args, nil, instanceID, out, cand, nodeDef, acquiredLocks, 30*time.Second)
	if !errors.Is(err, errAcquireFanOutSubstitutionFailed) {
		t.Fatalf("expected errAcquireFanOutSubstitutionFailed sentinel; got %v", err)
	}
	if out.FanOutSubstitutionErr == "" {
		t.Fatal("expected FanOutSubstitutionErr to carry the underlying substitution failure for the error-policy payload")
	}
}

// @concept: fan-out
// @concept: error-policy
func TestHandleAcquireFanOutSubstitutionFailed_TerminalizesRowInsteadOfHotLooping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()
	q := d.Queue()

	tmpl := insertDeployedTemplateInternal(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-substitution-failed-tmpl", Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "fan-leaf", Executor: "stub"}},
	})
	nodeID, _, nodeRunID, runScopeID, instanceID := seedStaleCandidateInternal(ctx, t, backend, q, tmpl.ID, "fan-leaf")

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "stale" {
		t.Fatalf("precondition: expected a freshly-enqueued candidate to be 'stale', got %q", got)
	}

	nodeDef := &node.TemplateNodeDef{
		Type: "fan-leaf", Executor: "stub",
		FanOut: &node.FanOutSpec{Claim: "data", PartitionRequest: `{{messages.invalidate.partition_request_override}}`},
	}
	acq := acquisition{
		NodeID: nodeID, NodeRunID: nodeRunID, InstanceID: instanceID, NodeType: "fan-leaf",
		NodeDef: nodeDef, RunScopeID: runScopeID, FanOutSubstitutionErr: "partition_request substitution: no message delivered",
	}
	cand := persistence.Candidate{NodeID: nodeID, NodeRunID: nodeRunID}
	args := RunArgs{
		Persist: backend, Queue: q, Logger: shared.SilentLogger{}, SupervisorID: "sup-fanout-substitution",
		Clock: shared.SystemClock{},
	}

	decision := handleAcquireFanOutSubstitutionFailed(ctx, args, acq, cand)
	if decision != nil && decision.IsRetry() {
		t.Fatalf("expected a give-up resolution (no configured retry policy for this authoring error), got retry decision: %+v", decision)
	}

	if got := runStateInternal(ctx, t, backend, nodeRunID); got != "failed" {
		t.Fatalf("expected the fan-out substitution failure to settle the run to 'failed', got %q", got)
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"}, AcceptedClaimProducers: []string{}, Limit: 16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeRunID == nodeRunID {
				t.Fatalf("candidate %s with a persistent fan-out partition_request substitution failure is "+
					"still selectable; it must terminalize instead of hot-looping every sweep", nodeRunID)
			}
		}
		return nil
	}))
}
