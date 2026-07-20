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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: terminal-resolution
func TestTryAcquire_TransientConflictBailAbandonsAlreadyOpenedLocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	tables := d.Tables()
	queue := d.Queue()

	const (
		producerFree     = "prod-a-free"
		producerConflict = "prod-b-conflict"
		supMe            = "sup-acquirer"
		supOther         = "sup-incumbent"
		nodeType         = "multi-claim"
	)

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	incumbentNodeID := shared.UUID(uuid.New())

	tmplSpec := tmplspec.TemplateSpec{
		Name: "bail-fixture", Version: "1",
		Nodes: []tmplspec.TemplateNodeDef{{
			Type: nodeType, Executor: "stub",
			ClaimProducers: []tmplspec.NodeClaimProducerRef{
				{Name: producerFree, Selector: "a", Intent: "rw"},
				{Name: producerConflict, Selector: "b", Intent: "rw"},
			},
		}},
	}

	var frameID shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmplSpec, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: nodeType, Executor: "stub",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: incumbentNodeID, InstanceID: instanceID, NodeType: "incumbent", Executor: "stub",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		conflictScope := []byte(`"b"`)
		incumbentIntent := "rw"
		producerConflictCopy := producerConflict
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     shared.UUID(uuid.New()),
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producerConflictCopy,
			ClaimScopeData:         conflictScope,
			Intent:                 &incumbentIntent,
			HolderSupervisorID:     supOther,
			HolderNodeID:           incumbentNodeID,
			ExpiresAt:              time.Now().Add(10 * time.Minute),
			Lifetime:               tmplspec.ClaimLifetimeSubgraph,
			RealizedWriteSemantics: string(claimproducer.WriteSemanticsSync),
		}, tx)
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var cand persistence.Candidate
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := queue.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID: nodeID, ExecutorName: "stub", RequiredClaimProducers: []string{},
			EnqueuedAt: time.Now().Add(-1 * time.Second), FrameID: frameID, RunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"stub"}, AcceptedClaimProducers: []string{}, Limit: 16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				cand = c
				return nil
			}
		}
		t.Fatalf("candidate for node not surfaced")
		return nil
	}); err != nil {
		t.Fatalf("enqueue/select: %v", err)
	}

	fakeFree := storetest.NewFake(producerFree, claimproducer.Capabilities{})
	fakeConflict := storetest.NewFake(producerConflict, claimproducer.Capabilities{})
	reg := locks.NewRegistry()
	reg.Add(producerFree, fakeFree)
	reg.Add(producerConflict, fakeConflict)

	args := RunArgs{
		Persist:        tables,
		Queue:          queue,
		AdvisoryLocker: d.AdvisoryLocker(),
		ClaimHandles:   tables.ClaimHandles(),
		StoreRegistry:  reg,
		Clock:          shared.SystemClock{},
		Logger:         shared.SilentLogger{},
		SupervisorID:   supMe,
	}

	acq, ok, err := tryAcquireWithTx(ctx, args, cand, 5*time.Second)
	if err != nil {
		t.Fatalf("tryAcquireWithTx returned error: %v", err)
	}
	if ok {
		t.Fatalf("acquisition must fail on the transient conflict, got ok=true (acq=%+v)", acq)
	}
	if _, err := FlushProducerVerbOutbox(ctx, args); err != nil {
		t.Fatalf("flush producer verb outbox: %v", err)
	}

	countVerb := func(f *storetest.Fake, verb string) int {
		n := 0
		for _, c := range f.Calls() {
			if c.Verb == verb {
				n++
			}
		}
		return n
	}

	if got := countVerb(fakeFree, "open"); got != 1 {
		t.Fatalf("the free producer's Open must have fired exactly once, got %d", got)
	}
	if got := countVerb(fakeFree, "abandon"); got != 1 {
		t.Fatalf("every Opened lock must be Abandoned on the bail path; free producer abandon count = %d, want 1", got)
	}
	if got := countVerb(fakeConflict, "open"); got != 0 {
		t.Fatalf("the conflicting producer bails before Open; its Open count = %d, want 0", got)
	}

	var opened, abandoned claimproducer.ClaimID
	for _, c := range fakeFree.Calls() {
		switch c.Verb {
		case "open":
			opened = c.ClaimID
		case "abandon":
			abandoned = c.ClaimID
		}
	}
	if opened != abandoned {
		t.Fatalf("abandon must target the same claim id as open: open=%s abandon=%s", opened, abandoned)
	}

	var leaked []persistence.ClaimHandleRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, lerr := tables.ClaimHandles().ListByProducerClaimScope(ctx, producerFree, tx)
		leaked = rows
		return lerr
	}); err != nil {
		t.Fatalf("ListByProducerClaimScope: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("the bailed acquisition's claim-handle rows must roll back, found %d leaked", len(leaked))
	}
}
