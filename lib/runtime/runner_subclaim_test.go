// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestAcquireSubClaims_UnsupportedSplitErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	reg := locks.NewRegistry()
	store := storetest.NewFake("ds-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add("ds-store", store)

	clk := newTickClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-U",
	}
	args = withSyncVerbFlush(args)
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "ds-store",
		HolderSupervisorID:  "sup-U",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
		ParentIntent:        "rw",
	})
	require.Error(t, err)
}

func TestAcquireSubClaims_UnknownProducerErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	reg := locks.NewRegistry()
	clk := newTickClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-X",
	}
	args = withSyncVerbFlush(args)
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "missing-store",
		HolderSupervisorID:  "sup-X",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
		ParentIntent:        "rw",
	})
	require.Error(t, err)
}

func TestAcquireSubClaims_EmptyPartitionKeyRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	reg := locks.NewRegistry()
	store := storetest.NewFake("ds-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "", ClaimScopeData: []byte(`{"p":"unkeyed"}`)},
			},
		}, nil
	}
	reg.Add("ds-store", store)

	clk := newTickClock(time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-EK",
	}
	args = withSyncVerbFlush(args)
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "ds-store",
		HolderSupervisorID:  "sup-EK",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
		ParentIntent:        "rw",
	})
	require.ErrorContains(t, err, "empty partition_key")
}

type tickClock struct{ t time.Time }

func newTickClock(start time.Time) *tickClock { return &tickClock{t: start} }
func (c *tickClock) Now() time.Time           { return c.t }
func (c *tickClock) Sleep(ctx context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

type fakeDataProcessingClient struct {
	mu              sync.Mutex
	name            string
	begins          []runtime.BeginCandidateInput
	beginHandleFunc func(callIdx int, in runtime.BeginCandidateInput) []byte

	commits   []runtime.CommitCandidateInput
	abandons  []runtime.AbandonCandidateInput
	versionFn func(in runtime.CommitCandidateInput) string
}

func newFakeDataProcessingClient(name string) *fakeDataProcessingClient {
	return &fakeDataProcessingClient{name: name}
}

func (f *fakeDataProcessingClient) Name() string { return f.name }

func (f *fakeDataProcessingClient) BeginCandidate(_ context.Context, in runtime.BeginCandidateInput) (runtime.BeginCandidateOutput, error) {
	f.mu.Lock()
	idx := len(f.begins)
	f.begins = append(f.begins, in)
	fn := f.beginHandleFunc
	f.mu.Unlock()
	if fn == nil {
		return runtime.BeginCandidateOutput{CandidateHandle: []byte("handle-" + in.ClaimHandleID)}, nil
	}
	return runtime.BeginCandidateOutput{CandidateHandle: fn(idx, in)}, nil
}

func (f *fakeDataProcessingClient) CommitCandidate(_ context.Context, in runtime.CommitCandidateInput) (runtime.CommitCandidateOutput, error) {
	f.mu.Lock()
	f.commits = append(f.commits, in)
	fn := f.versionFn
	f.mu.Unlock()
	vid := "version-" + in.ClaimHandleID
	if fn != nil {
		vid = fn(in)
	}
	return runtime.CommitCandidateOutput{VersionID: vid}, nil
}

func (f *fakeDataProcessingClient) AbandonCandidate(_ context.Context, in runtime.AbandonCandidateInput) error {
	f.mu.Lock()
	f.abandons = append(f.abandons, in)
	f.mu.Unlock()
	return nil
}

func (f *fakeDataProcessingClient) ListVersions(_ context.Context, _ runtime.ListVersionsInput) (runtime.ListVersionsOutput, error) {
	return runtime.ListVersionsOutput{}, nil
}
func (f *fakeDataProcessingClient) ListPartitions(_ context.Context, _ runtime.ListPartitionsInput) (runtime.ListPartitionsOutput, error) {
	return runtime.ListPartitionsOutput{}, nil
}
func (f *fakeDataProcessingClient) GetVersionSchema(_ context.Context, _ runtime.GetVersionSchemaInput) (runtime.GetVersionSchemaOutput, error) {
	return runtime.GetVersionSchemaOutput{}, nil
}

func (f *fakeDataProcessingClient) Begins() []runtime.BeginCandidateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.BeginCandidateInput, len(f.begins))
	copy(out, f.begins)
	return out
}
func (f *fakeDataProcessingClient) Commits() []runtime.CommitCandidateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.CommitCandidateInput, len(f.commits))
	copy(out, f.commits)
	return out
}
func (f *fakeDataProcessingClient) Abandons() []runtime.AbandonCandidateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.AbandonCandidateInput, len(f.abandons))
	copy(out, f.abandons)
	return out
}

type fakeDataProcessingRegistry struct {
	clients map[string]runtime.DataProcessingClient
}

func newFakeDataProcessingRegistry(c *fakeDataProcessingClient) *fakeDataProcessingRegistry {
	return &fakeDataProcessingRegistry{clients: map[string]runtime.DataProcessingClient{c.Name(): c}}
}
func (r *fakeDataProcessingRegistry) Get(name string) (runtime.DataProcessingClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

func TestAcquireSubClaims_BeginCandidateIdempotencyKeyIsRunAndPartitionDerivedStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "idem-key-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
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
	reg.Add(storeName, store)

	dpClient := newFakeDataProcessingClient(storeName)
	dpReg := newFakeDataProcessingRegistry(dpClient)

	clk := newTickClock(time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:        backend,
		ClaimHandles:   backend.ClaimHandles(),
		StoreRegistry:  reg,
		DataProcessors: dpReg,
		Logger:         shared.SilentLogger{},
		Clock:          clk,
		SupervisorID:   "sup-IDEM",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-idempotency-key", Version: "1",
	})
	ck := "ck-idem"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		parentNode = p
		return err
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope-idem"`)
	intent := "rw"
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: parentClaimID, LockKind: persistence.LockKindScope,
			ProducerName: &producerName, ClaimScopeData: parentScope, Address: json.RawMessage(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-IDEM", HolderNodeID: parentNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			NodeRunID: &parentNodeRunID,
		}, tx)
	}))

	acquireOnce := func() {
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
				ParentClaimHandleID: parentClaimID,
				ProducerName:        storeName,
				NodeRunID:           parentNodeRunID,
				HolderNodeID:        parentNode.ID,
				HolderSupervisorID:  "sup-IDEM",
				InstanceID:          inst.ID,
				LivenessInterval:    30 * time.Second,
				ParentIntent:        string(claimproducer.IntentReadWrite),
			})
			return err
		}))
	}
	acquireOnce()
	acquireOnce()

	begins := dpClient.Begins()
	require.Len(t, begins, 2, "two acquisition attempts against the same node run must each call BeginCandidate")
	require.Equal(t, begins[0].IdempotencyKey, begins[1].IdempotencyKey,
		"BeginCandidate.IdempotencyKey must be stable (run_id+partition_key derived) across separate "+
			"acquisition attempts for the same node run and partition, so the producer's idempotency can engage")
	require.NotEqual(t, begins[0].ClaimHandleID, begins[1].ClaimHandleID,
		"each attempt must still mint a fresh claim_handle row id — only the idempotency key is stable")
	require.NotEqual(t, begins[0].IdempotencyKey, begins[0].ClaimHandleID,
		"the idempotency key must not be the same randomness as the claim_handle id")
}

func TestSubClaim_BeginThenCommitFlowsThroughRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "fan-out-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`)},
			},
		}, nil
	}
	reg.Add(storeName, store)

	dpClient := newFakeDataProcessingClient(storeName)
	dpReg := newFakeDataProcessingRegistry(dpClient)

	clk := newTickClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:        backend,
		ClaimHandles:   backend.ClaimHandles(),
		StoreRegistry:  reg,
		DataProcessors: dpReg,
		Logger:         shared.SilentLogger{},
		Clock:          clk,
		SupervisorID:   "sup-FAN",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-runtime-dispatch", Version: "1",
	})
	ck := "ck-fan"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope"`)
	parentAddr := json.RawMessage(`"parent-addr"`)
	intent := "rw"
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Address:            parentAddr,
			Intent:             &intent,
			HolderSupervisorID: "sup-FAN",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-FAN",
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIsHeld:        false,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2, "two sub-scopes → two sub-claims")

	begins := dpClient.Begins()
	require.Len(t, begins, 2, "BeginCandidate must fire per sub-scope")
	require.Equal(t, storeName, begins[0].ProducerName)
	require.Equal(t, storeName, begins[1].ProducerName)
	beginIDs := map[string]bool{begins[0].ClaimHandleID: true, begins[1].ClaimHandleID: true}
	require.True(t, beginIDs[subClaims[0].ClaimHandleID.String()],
		"BeginCandidate.ClaimHandleID must match the sub-claim row id")
	require.True(t, beginIDs[subClaims[1].ClaimHandleID.String()],
		"BeginCandidate.ClaimHandleID must match the sub-claim row id")

	for i, sc := range subClaims {
		require.NotEmpty(t, sc.ProducerCandidateHandle,
			"sub-claim[%d] must carry producer_candidate_handle bytes", i)
	}

	candidateHandle0 := subClaims[0].ProducerCandidateHandle
	var post0 func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID:       subClaims[0].ClaimHandleID,
			SupervisorID:        "sup-FAN",
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.OutcomeCommit,
			Producer:            store,
			Scope:               []byte(`{"p":"alpha"}`),
			Address:             []byte{},
			Lifetime:            "subgraph",
			CandidateHandle:     candidateHandle0,
			ProducerName:        storeName,
			ParentClaimHandleID: &parentClaimID,
		})
		post0 = pc
		return err
	}))
	if post0 != nil {
		post0(ctx)
	}

	candidateHandle1 := subClaims[1].ProducerCandidateHandle
	var post1 func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID:       subClaims[1].ClaimHandleID,
			SupervisorID:        "sup-FAN",
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.OutcomeAbandon,
			Producer:            store,
			Scope:               []byte(`{"p":"beta"}`),
			Address:             []byte{},
			Lifetime:            "subgraph",
			CandidateHandle:     candidateHandle1,
			ProducerName:        storeName,
			ParentClaimHandleID: &parentClaimID,
		})
		post1 = pc
		return err
	}))
	if post1 != nil {
		post1(ctx)
	}

	commits := dpClient.Commits()
	require.Len(t, commits, 1, "CommitCandidate must fire on OutcomeCommit")
	require.Equal(t, subClaims[0].ClaimHandleID.String(), commits[0].ClaimHandleID,
		"CommitCandidate.ClaimHandleID must match the sub-claim row id")
	require.Equal(t, candidateHandle0, commits[0].CandidateHandle,
		"CommitCandidate.CandidateHandle must round-trip the bytes BeginCandidate handed back")

	abandons := dpClient.Abandons()
	require.Len(t, abandons, 1, "AbandonCandidate must fire on OutcomeAbandon")
	require.Equal(t, subClaims[1].ClaimHandleID.String(), abandons[0].ClaimHandleID,
		"AbandonCandidate.ClaimHandleID must match the sub-claim row id")
	require.Equal(t, candidateHandle1, abandons[0].CandidateHandle,
		"AbandonCandidate.CandidateHandle must round-trip the bytes BeginCandidate handed back")

	parentClaimIDStr := parentClaimID.String()
	var parentAbandonSeen, parentCommitSeen bool
	for _, c := range store.Calls() {
		if string(c.ClaimID) != parentClaimIDStr {
			continue
		}
		switch c.Verb {
		case "abandon":
			parentAbandonSeen = true
		case "commit":
			parentCommitSeen = true
		}
	}
	require.True(t, parentAbandonSeen,
		"recursive claim-tree resolution must fire Abandon on the parent ClaimID after the last sub-claim resolves (seedOutcome=OutcomeAbandon)")
	require.False(t, parentCommitSeen,
		"parent must NOT Commit when the last-resolved sub-claim seeded OutcomeAbandon")

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentClaimID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow,
		"parent claim_handle row must be preserved past terminal (Promote-not-delete)")
	require.Equal(t, spec.ClaimHandleStateAbandoned, parentRow.State,
		"parent claim_handle row must be promoted to state=abandoned by the recursive resolution walk")
}

func TestSubClaim_CrossSupervisorSettlementResolvesParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "xsup-store"
	const supOne = "sup-ONE"
	const supTwo = "sup-TWO"

	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`)},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC))
	argsOne := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  supOne,
	}
	argsOne = withSyncVerbFlush(argsOne)
	argsTwo := argsOne
	argsTwo.SupervisorID = supTwo
	argsTwo = withSyncVerbFlush(argsTwo)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-cross-supervisor", Version: "1",
	})
	ck := "ck-xsup"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope"`)
	intent := "rw"
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Address:            json.RawMessage(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: supOne,
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, argsOne, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  supOne,
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, sc := range subClaims {
			if err := backend.ClaimHandles().ReassignHolderSupervisor(ctx, sc.ClaimHandleID, supOne, supTwo, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	for _, sc := range subClaims {
		sc := sc
		var post func(context.Context)
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			pc, err := runtime.ResolveClaimHandleTerminal(ctx, argsTwo, tx, runtime.TerminalDecision{
				ClaimHandleID:       sc.ClaimHandleID,
				SupervisorID:        supTwo,
				Source:              runtime.ActiveTerminal,
				Outcome:             runtime.OutcomeCommit,
				Producer:            store,
				Scope:               []byte(sc.ClaimScope),
				Address:             []byte{},
				Lifetime:            "subgraph",
				ProducerName:        storeName,
				ParentClaimHandleID: &parentClaimID,
			})
			post = pc
			return err
		}))
		if post != nil {
			post(ctx)
		}
	}

	for _, sc := range subClaims {
		var row *persistence.ClaimHandleRow
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := backend.ClaimHandles().Get(ctx, sc.ClaimHandleID, tx)
			row = r
			return err
		}))
		require.NotNil(t, row)
		require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
			"sub-claim %s must promote under the supervisor that drives the leaf", sc.ClaimHandleID)
	}

	var parentRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, parentClaimID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow)
	require.Equal(t, 2, parentRow.CommittedChildrenCount,
		"both children's outcomes must land on the parent's counters across supervisors")
	require.Equal(t, spec.ClaimHandleStateCommitted, parentRow.State,
		"the last child's settlement must take over and settle the parent — not defer to a holder that is never re-driven")
	require.Nil(t, parentRow.HolderSupervisorID,
		"Promote must null the holder after the takeover settlement")
	parentCommits := 0
	for _, c := range store.Calls() {
		if string(c.ClaimID) == parentClaimID.String() && c.Verb == "commit" {
			parentCommits++
		}
	}
	require.Equal(t, 1, parentCommits,
		"the parent's aggregate Commit must fire exactly once under cross-supervisor settlement")
}

func TestAcquireSubClaims_InheritsParentReadOnlyIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "ro-intent-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`)},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-RO",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-intent-inheritance", Version: "1",
	})
	ck := "ck-ro"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope-ro"`)
	parentIntent := string(claimproducer.IntentRead)
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Intent:             &parentIntent,
			HolderSupervisorID: "sup-RO",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-RO",
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIsHeld:        false,
			ParentIntent:        string(claimproducer.IntentRead),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2, "two sub-scopes → two sub-claims")

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, sc := range subClaims {
			row, err := backend.ClaimHandles().Get(ctx, sc.ClaimHandleID, tx)
			if err != nil {
				return err
			}
			require.NotNil(t, row, "sub-claim row must be persisted")
			require.NotNil(t, row.Intent, "sub-claim row must carry intent")
			require.Equal(t, string(claimproducer.IntentRead), *row.Intent,
				"sub-claim must inherit parent read-only intent — not be hardcoded to rw")
		}
		return nil
	}))
}

func TestAcquireSubClaims_InheritsParentReadWriteIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "rw-intent-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`)},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-RW",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-intent-inheritance-rw", Version: "1",
	})
	ck := "ck-rw"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope-rw"`)
	parentIntent := string(claimproducer.IntentReadWrite)
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Intent:             &parentIntent,
			HolderSupervisorID: "sup-RW",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-RW",
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIsHeld:        false,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, sc := range subClaims {
			row, err := backend.ClaimHandles().Get(ctx, sc.ClaimHandleID, tx)
			if err != nil {
				return err
			}
			require.NotNil(t, row, "sub-claim row must be persisted")
			require.NotNil(t, row.Intent, "sub-claim row must carry intent")
			require.Equal(t, string(claimproducer.IntentReadWrite), *row.Intent,
				"sub-claim must inherit parent read-write intent")
		}
		return nil
	}))
}

func TestAcquireSubClaims_PersistsAddressAndPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "addr-payload-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	wantAddrAlpha := []byte(`{"folder":"/q/alpha"}`)
	wantPayloadAlpha := []byte(`{"items":["a","b"]}`)
	wantAddrBeta := []byte(`{"folder":"/q/beta"}`)
	wantPayloadBeta := []byte(`{"items":["c"]}`)
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`), Address: wantAddrAlpha, Payload: wantPayloadAlpha},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`), Address: wantAddrBeta, Payload: wantPayloadBeta},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-AP",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-addr-payload-persist", Version: "1",
	})
	ck := "ck-ap"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"parent-scope-ap"`)
	parentIntent := string(claimproducer.IntentReadWrite)
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Intent:             &parentIntent,
			HolderSupervisorID: "sup-AP",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-AP",
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIsHeld:        false,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2)

	byKey := map[string]struct {
		addr, payload []byte
	}{
		"alpha": {wantAddrAlpha, wantPayloadAlpha},
		"beta":  {wantAddrBeta, wantPayloadBeta},
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, sc := range subClaims {
			row, err := backend.ClaimHandles().Get(ctx, sc.ClaimHandleID, tx)
			if err != nil {
				return err
			}
			require.NotNil(t, row, "sub-claim row must be persisted")
			want := byKey[sc.PartitionKey]
			require.JSONEq(t, string(want.addr), string(row.Address),
				"sub-claim row Address must JSON-equal the SplitScope descriptor Address (partition_key=%s)", sc.PartitionKey)
			require.JSONEq(t, string(want.payload), string(row.Payload),
				"sub-claim row Payload must JSON-equal the SplitScope descriptor Payload (partition_key=%s)", sc.PartitionKey)
		}
		return nil
	}))
}

func TestAcquireSubClaims_RejectsNonJSONAddress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const storeName = "non-json-addr-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`), Address: []byte("not-json")},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-AJ",
	}
	args = withSyncVerbFlush(args)
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        storeName,
		HolderSupervisorID:  "sup-AJ",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
		ParentIntent:        "rw",
	})
	require.ErrorContains(t, err, "non-JSON address bytes")
}

func TestAcquireSubClaims_RejectsNonJSONPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const storeName = "non-json-payload-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`), Payload: []byte("not-json")},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-PJ",
	}
	args = withSyncVerbFlush(args)
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        storeName,
		HolderSupervisorID:  "sup-PJ",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
		ParentIntent:        "rw",
	})
	require.ErrorContains(t, err, "non-JSON payload bytes")
}

// @story: sub-claim-payload-substitution
// @story: fs-fanout-list-array
func TestReuseLinkedSubClaim_ChildRunAttachesWithoutReOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "linked-subclaim-store"
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	wantPayload := []byte(`{"v":7}`)
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`"parent/_list/alpha"`), Payload: wantPayload},
			},
		}, nil
	}
	reg.Add(storeName, store)

	clk := newTickClock(time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-LSC",
	}
	args = withSyncVerbFlush(args)

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-linked-subclaim-reuse", Version: "1",
	})
	ck := "ck-lsc"
	var mainScopeID shared.UUID
	var inst persistence.InstanceRow
	var parentNode, childNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		mainScopeID = ms
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		childNode = c
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)
	parentNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)
	childNodeRunID := seedRunForNode(ctx, t, backend, d.Queue(), childNode.ID, frameID)

	parentClaimID := shared.UUID(uuid.New())
	parentScope := json.RawMessage(`"@queue"`)
	parentIntent := string(claimproducer.IntentReadWrite)
	producerName := storeName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentClaimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     parentScope,
			Intent:             &parentIntent,
			HolderSupervisorID: "sup-LSC",
			HolderNodeID:       parentNode.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			NodeRunID:          &parentNodeRunID,
		}, tx)
	}))

	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ProducerName:        storeName,
			NodeRunID:           parentNodeRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-LSC",
			InstanceID:          inst.ID,
			LivenessInterval:    30 * time.Second,
			ParentIntent:        string(claimproducer.IntentReadWrite),
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 1)

	claimSpec := claimproducer.ClaimSpec{
		ProducerName: storeName,
		Alias:        "fs_queue",
		Selector:     "@queue",
		Intent:       claimproducer.IntentReadWrite,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		al, reused, err := runtime.ReuseLinkedSubClaimForTest(
			ctx, args, tx, claimSpec, persistence.Candidate{NodeRunID: parentNodeRunID},
			store, nil, 30*time.Second)
		if err != nil {
			return err
		}
		require.False(t, reused,
			"the parent's own run must NOT attach to a sub-claim whose parent claim lives in the same run: got %+v", al)
		return nil
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().UpdateNodeRunID(ctx, subClaims[0].ClaimHandleID, childNodeRunID, args.SupervisorID, tx)
	}))

	var al runtime.AcquiredLock
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, reused, err := runtime.ReuseLinkedSubClaimForTest(
			ctx, args, tx, claimSpec, persistence.Candidate{NodeRunID: childNodeRunID},
			store, nil, 30*time.Second)
		if err != nil {
			return err
		}
		require.True(t, reused, "a fan-out child run must attach to its linked sub-claim")
		al = got
		return nil
	}))
	require.Equal(t, subClaims[0].ClaimHandleID, al.ClaimHandleID,
		"the attached lock must be the synthesized sub-claim, not a fresh Open")
	require.JSONEq(t, string(wantPayload), string(al.ClaimResult.Payload),
		"the attached lock must carry the per-sub-claim payload for {{claim.<alias>.payload}} substitution")
	require.Equal(t, `"parent/_list/alpha"`, string(al.ClaimResult.ClaimScope))

	for _, c := range store.Calls() {
		require.NotEqual(t, "open", c.Verb,
			"child attachment must not dial producer Open (no double-acquire per partition)")
	}
}
