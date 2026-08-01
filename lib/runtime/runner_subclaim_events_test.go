// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type noopDataProcessingClient struct{ name string }

func (c noopDataProcessingClient) Name() string { return c.name }
func (c noopDataProcessingClient) BeginCandidate(_ context.Context, in BeginCandidateInput) (BeginCandidateOutput, error) {
	return BeginCandidateOutput{CandidateHandle: []byte("handle-" + in.ClaimHandleID)}, nil
}
func (c noopDataProcessingClient) CommitCandidate(_ context.Context, in CommitCandidateInput) (CommitCandidateOutput, error) {
	return CommitCandidateOutput{}, nil
}
func (c noopDataProcessingClient) AbandonCandidate(_ context.Context, _ AbandonCandidateInput) error {
	return nil
}
func (c noopDataProcessingClient) ListVersions(_ context.Context, _ ListVersionsInput) (ListVersionsOutput, error) {
	return ListVersionsOutput{}, nil
}
func (c noopDataProcessingClient) ListPartitions(_ context.Context, _ ListPartitionsInput) (ListPartitionsOutput, error) {
	return ListPartitionsOutput{}, nil
}
func (c noopDataProcessingClient) GetVersionSchema(_ context.Context, _ GetVersionSchemaInput) (GetVersionSchemaOutput, error) {
	return GetVersionSchemaOutput{}, nil
}

type singleClientRegistry struct{ c DataProcessingClient }

func (r singleClientRegistry) Get(name string) (DataProcessingClient, bool) {
	if r.c.Name() != name {
		return nil, false
	}
	return r.c, true
}

func TestAcquireSubClaims_EventsCarryInstanceAndNodeAttribution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "subclaim-events.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tables := d.Tables()
	q := d.Queue()

	const producerName = "subclaim-events-producer"
	reg := locks.NewRegistry()
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
			},
		}, nil
	}
	reg.Add(producerName, store)

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	parentNodeID := shared.UUID(uuid.New())
	var parentNodeRunID shared.UUID

	tmpl := spec.TemplateSpec{
		Name:    "subclaim-event-attribution",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "fanout", Executor: "test-executor"},
		},
	}

	var frameID shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: parentNodeID, InstanceID: instanceID, NodeType: "fanout", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 parentNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             mainScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == parentNodeID {
				parentNodeRunID = c.NodeRunID
			}
		}
		if parentNodeRunID == (shared.UUID{}) {
			t.Fatalf("candidate not surfaced for %s", parentNodeID)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope"`)
	intent := "rw"
	producerNameRef := producerName
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerNameRef,
			ClaimScopeData:     parentScope,
			Intent:             &intent,
			HolderSupervisorID: "sup-subclaim-events",
			HolderNodeID:       parentNodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed parent claim handle: %v", err)
	}

	args := RunArgs{
		Persist:               tables,
		ClaimHandles:          tables.ClaimHandles(),
		ClaimProducerRegistry: reg,
		DataProcessors:        singleClientRegistry{c: noopDataProcessingClient{name: producerName}},
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          "sup-subclaim-events",
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := AcquireSubClaims(ctx, args, AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        producerName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNodeID,
			HolderSupervisorID:  "sup-subclaim-events",
			InstanceID:          instanceID,
			LivenessInterval:    30 * time.Second,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("AcquireSubClaims: %v", err)
	}

	var res persistence.EventListResult
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = tables.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
		}, persistence.ListPagination{Limit: 64}, tx)
		return lerr
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}

	var sawBeginCandidate, sawAcquired bool
	for _, ev := range res.Events {
		switch ev.KindRaw {
		case "subclaim.begin_candidate":
			sawBeginCandidate = true
			if ev.InstanceID == nil || *ev.InstanceID != instanceID {
				t.Errorf("subclaim.begin_candidate: InstanceID = %v; want %s", ev.InstanceID, instanceID)
			}
			if ev.NodeID == nil || *ev.NodeID != parentNodeID {
				t.Errorf("subclaim.begin_candidate: NodeID = %v; want %s", ev.NodeID, parentNodeID)
			}
		case "subclaim.acquired":
			sawAcquired = true
			if ev.InstanceID == nil || *ev.InstanceID != instanceID {
				t.Errorf("subclaim.acquired: InstanceID = %v; want %s", ev.InstanceID, instanceID)
			}
			if ev.NodeID == nil || *ev.NodeID != parentNodeID {
				t.Errorf("subclaim.acquired: NodeID = %v; want %s", ev.NodeID, parentNodeID)
			}
		}
	}
	if !sawBeginCandidate {
		t.Fatalf("expected a subclaim.begin_candidate event; got kinds=%v", eventKinds(res.Events))
	}
	if !sawAcquired {
		t.Fatalf("expected a subclaim.acquired event; got kinds=%v", eventKinds(res.Events))
	}
}

func eventKinds(rows []persistence.EventRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.KindRaw
	}
	return out
}
