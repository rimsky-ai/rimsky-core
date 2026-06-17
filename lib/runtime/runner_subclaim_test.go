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
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// TestAcquireSubClaims_UnsupportedSplitErrors confirms a producer that
// doesn't advertise SupportsSplitScope returns the typed sentinel and
// the wrapping fmt.Errorf preserves the message context.
func TestAcquireSubClaims_UnsupportedSplitErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	reg := locks.NewRegistry()
	store := storetest.NewFake("ds-store", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		// @deliberate: SupportsSplitScope unset → fake's SplitScope returns
		// ErrSplitScopeUnsupported by default.
	})
	reg.Add("ds-store", store)

	clk := newTickClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		Clock:         clk,
		SupervisorID:  "sup-U",
	}
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "ds-store",
		HolderSupervisorID:  "sup-U",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
	})
	require.Error(t, err)
}

// TestAcquireSubClaims_UnknownProducerErrors confirms an unregistered
// producer name surfaces a typed error early.
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
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "missing-store",
		HolderSupervisorID:  "sup-X",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
	})
	require.Error(t, err)
}

// TestAcquireSubClaims_EmptyPartitionKeyRejected confirms a producer
// SplitScope descriptor carrying an empty partition_key aborts the
// whole acquisition: the empty key is the delegation discriminator
// (the settlement walk skips closing empty-key scopes, and the
// per-partition uniqueness index exempts them), so a producer-returned
// empty key would silently leak its partition RunScope open.
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
	_, err := runtime.AcquireSubClaims(ctx, args, nil, runtime.AcquireSubClaimsInput{
		ParentClaimHandleID: shared.UUID(uuid.New()),
		ProducerName:        "ds-store",
		HolderSupervisorID:  "sup-EK",
		InstanceID:          shared.UUID{},
		LivenessInterval:    30 * time.Second,
	})
	require.ErrorContains(t, err, "empty partition_key")
}

// tickClock is a minimal Clock fixture for the sub-claim tests.
type tickClock struct{ t time.Time }

func newTickClock(start time.Time) *tickClock { return &tickClock{t: start} }
func (c *tickClock) Now() time.Time           { return c.t }
func (c *tickClock) Sleep(ctx context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

// fakeDataProcessingClient records BeginCandidate / CommitCandidate /
// AbandonCandidate calls so the integration test below can assert the
// runtime dispatched the right verbs with the right `candidate_handle`
// bytes. Each verb returns canned bytes so the runtime's downstream
// persistence writes (VersionID, candidate_handle round-tripping) get
// exercised end-to-end.
type fakeDataProcessingClient struct {
	mu     sync.Mutex
	name   string
	begins []runtime.BeginCandidateInput
	// beginHandleFunc returns the candidate_handle bytes BeginCandidate
	// should hand back per call. Indexed by call number (0-based).
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

// ListVersions non-fan-out enumeration verbs aren't exercised by this test;
// return empty results to keep the interface satisfied.
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

// fakeDataProcessingRegistry is a single-entry registry over a fake client.
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

// TestSubClaim_BeginThenCommitFlowsThroughRuntime drives the rimsky
// runtime dispatch all the way through `AcquireSubClaims` (which fires
// `BeginCandidate` per sub-scope) and `ResolveClaimHandleTerminal`
// (which fires `CommitCandidate` per sub-claim's terminal). The fake
// DataProcessingRegistry records every call so the assertions can pin
// that the registry-lookup + argument-threading at both call sites is
// load-bearing.
func TestSubClaim_BeginThenCommitFlowsThroughRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	const storeName = "fan-out-store"
	// @deliberate: Producer fake — advertises SplitScope so AcquireSubClaims can call
	// it; SplitClaimScopeFunc returns two sub-claim-scopes.
	reg := locks.NewRegistry()
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		// @deliberate: Two sub-scopes; bytes are inert in rimsky.
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: []byte(`{"p":"alpha"}`)},
				{PartitionKey: "beta", ClaimScopeData: []byte(`{"p":"beta"}`)},
			},
		}, nil
	}
	reg.Add(storeName, store)

	// @deliberate: Fake DataProcessingClient + Registry that records every call.
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

	// @deliberate: Insert a minimal template / instance / node row chain so the
	// sub-claim INSERTs have a valid FK chain. The fan-out node-row is
	// enough for the AcquireSubClaims path; the per-leaf run rows
	// aren't needed for this test (we drive ResolveClaimHandleTerminal
	// directly with the sub-claim ids).
	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-runtime-dispatch", Version: "1",
	})
	ck := "ck-fan"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))

	// @deliberate: Seed a frame + parent run row so the parent claim handle's
	// `node_run_id` FK chain resolves. The sub-claim INSERTs in
	// AcquireSubClaims also key on this run id.
	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	// @deliberate: Seed a parent claim handle (the fan-out root).
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
			NodeRunID:          &parentRunID,
		}, tx)
	}))

	// @deliberate: Drive AcquireSubClaims — registry-lookup + per-sub-claim
	// BeginCandidate threading is now exercised against a real
	// persistence.Tx and the fake DataProcessingClient.
	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, args, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ParentClaimScope:    parentScope,
			ProducerName:        storeName,
			NodeRunID:           parentRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  "sup-FAN",
			InstanceID:          shared.UUID{},
			LivenessInterval:    30 * time.Second,
			ParentIsHeld:        false,
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2, "two sub-scopes → two sub-claims")

	// @deliberate: BeginCandidate must have fired twice with the per-sub-claim id +
	// the per-partition descriptor bytes.
	begins := dpClient.Begins()
	require.Len(t, begins, 2, "BeginCandidate must fire per sub-scope")
	require.Equal(t, storeName, begins[0].ProducerName)
	require.Equal(t, storeName, begins[1].ProducerName)
	// @deliberate: Sub-claim handle ids must match the persisted sub-claim ids.
	beginIDs := map[string]bool{begins[0].ClaimHandleID: true, begins[1].ClaimHandleID: true}
	require.True(t, beginIDs[subClaims[0].ClaimHandleID.String()],
		"BeginCandidate.ClaimHandleID must match the sub-claim row id")
	require.True(t, beginIDs[subClaims[1].ClaimHandleID.String()],
		"BeginCandidate.ClaimHandleID must match the sub-claim row id")

	// @deliberate: Each returned SubClaim must carry the producer's candidate_handle
	// bytes — the runtime persists them on the row's
	// `producer_candidate_handle` column.
	for i, sc := range subClaims {
		require.NotEmpty(t, sc.ProducerCandidateHandle,
			"sub-claim[%d] must carry producer_candidate_handle bytes", i)
	}

	// @deliberate: Drive ResolveClaimHandleTerminal on sub-claim[0] with
	// AggregateCommit — the runtime must dispatch CommitCandidate first,
	// then the standard ClaimProducer.Commit verb.
	candidateHandle0 := subClaims[0].ProducerCandidateHandle
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID:       subClaims[0].ClaimHandleID,
			SupervisorID:        "sup-FAN",
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.AggregateCommit,
			Producer:            store,
			Scope:               []byte(`{"p":"alpha"}`),
			Address:             []byte{},
			Lifetime:            "subgraph",
			CandidateHandle:     candidateHandle0,
			ProducerName:        storeName,
			ParentClaimHandleID: &parentClaimID,
		})
	}))

	// @deliberate: Drive ResolveClaimHandleTerminal on sub-claim[1] with
	// AggregateAbandon — the runtime must dispatch AbandonCandidate.
	candidateHandle1 := subClaims[1].ProducerCandidateHandle
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.ResolveClaimHandleTerminal(ctx, args, tx, runtime.TerminalDecision{
			ClaimHandleID:       subClaims[1].ClaimHandleID,
			SupervisorID:        "sup-FAN",
			Source:              runtime.ActiveTerminal,
			Outcome:             runtime.AggregateAbandon,
			Producer:            store,
			Scope:               []byte(`{"p":"beta"}`),
			Address:             []byte{},
			Lifetime:            "subgraph",
			CandidateHandle:     candidateHandle1,
			ProducerName:        storeName,
			ParentClaimHandleID: &parentClaimID,
		})
	}))

	// @deliberate: CommitCandidate must have fired exactly once with sub-claim[0]'s
	// id + candidate_handle.
	commits := dpClient.Commits()
	require.Len(t, commits, 1, "CommitCandidate must fire on AggregateCommit")
	require.Equal(t, subClaims[0].ClaimHandleID.String(), commits[0].ClaimHandleID,
		"CommitCandidate.ClaimHandleID must match the sub-claim row id")
	require.Equal(t, candidateHandle0, commits[0].CandidateHandle,
		"CommitCandidate.CandidateHandle must round-trip the bytes BeginCandidate handed back")

	// @deliberate: AbandonCandidate must have fired exactly once with sub-claim[1]'s
	// id + candidate_handle.
	abandons := dpClient.Abandons()
	require.Len(t, abandons, 1, "AbandonCandidate must fire on AggregateAbandon")
	require.Equal(t, subClaims[1].ClaimHandleID.String(), abandons[0].ClaimHandleID,
		"AbandonCandidate.ClaimHandleID must match the sub-claim row id")
	require.Equal(t, candidateHandle1, abandons[0].CandidateHandle,
		"AbandonCandidate.CandidateHandle must round-trip the bytes BeginCandidate handed back")

	// @deliberate: Recursive claim-tree resolution assertion (fix 8 from cycle 3): once
	// the last sub-claim resolves, `ResolveClaimHandleTerminal`'s non-durable
	// Delete branch walks `SettleChildren`, which fires the parent's
	// terminal verb against the standard `ClaimProducer` surface. Because
	// the last-resolved sub-claim seeded `AggregateAbandon`, the parent
	// inherits that seed and Abandon must fire on the parent ClaimID. A
	// regression reverting fix 8 (the recursive walk in `auto_terminal.go`)
	// would leave no parent-id call in the storetest fake's Calls() slice.
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
		"recursive claim-tree resolution must fire Abandon on the parent ClaimID after the last sub-claim resolves (seedOutcome=AggregateAbandon)")
	require.False(t, parentCommitSeen,
		"parent must NOT Commit when the last-resolved sub-claim seeded AggregateAbandon")

	// @deliberate: And the parent claim_handle row must be promoted to state=abandoned
	// by the recursive resolution walk (Promote-not-delete; preserved
	// past terminal for forensics / retention).
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

// TestSubClaim_CrossSupervisorSettlementResolvesParent pins the
// ≥2-supervisor fan-out settlement: the parent claim handle is acquired
// (and held) by supervisor ONE, the leaf runs are claimed by supervisor
// TWO (whose acquisition restamps the sub-claim rows onto itself — here
// simulated via the same `ReassignHolderSupervisor` CAS the acquisition
// path calls), and both leaves resolve under TWO. The chain must NOT
// stall: each child row promotes under TWO, the per-outcome counter
// bumps land on the parent under its ACTUAL holder, and the last
// child's settlement takes over the parent handle and fires the
// parent's aggregate Commit — instead of the pre-fix shape where the
// producer saw the children's Commits while the parent stayed active
// until the orphan reaper recorded a spurious Abandon.
func TestSubClaim_CrossSupervisorSettlementResolvesParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
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
	argsTwo := argsOne
	argsTwo.SupervisorID = supTwo

	tmplRow := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "fanout-cross-supervisor", Version: "1",
	})
	ck := "ck-xsup"
	var inst persistence.InstanceRow
	var parentNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmplRow.ID, &ck)
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "fanout", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		parentNode = p
		return nil
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, parentNode.ID)
	parentRunID := seedRunForNode(ctx, t, backend, d.Queue(), parentNode.ID, frameID)

	// @deliberate: Parent claim handle held by supervisor ONE.
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
			NodeRunID:          &parentRunID,
		}, tx)
	}))

	// @deliberate: AcquireSubClaims under supervisor ONE — the rows are stamped with
	// ONE's holder id, exactly the shape the leaf acquisition later
	// finds.
	var subClaims []runtime.SubClaim
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := runtime.AcquireSubClaims(ctx, argsOne, tx, runtime.AcquireSubClaimsInput{
			ParentClaimHandleID: parentClaimID,
			ParentClaimScope:    parentScope,
			ProducerName:        storeName,
			NodeRunID:           parentRunID,
			HolderNodeID:        parentNode.ID,
			HolderSupervisorID:  supOne,
			InstanceID:          shared.UUID{},
			LivenessInterval:    30 * time.Second,
		})
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 2)

	// @deliberate: Supervisor TWO claims the leaves: the acquisition path's restamp
	// (runner_acquire.go::restampLinkedSubClaimHolders) CAS-moves each
	// sub-claim's holder ONE → TWO. Exercised here through the same
	// persistence primitive the acquisition calls.
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, sc := range subClaims {
			if err := backend.ClaimHandles().ReassignHolderSupervisor(ctx, sc.ClaimHandleID, supOne, supTwo, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	// @deliberate: Both leaves resolve with Commit under supervisor TWO.
	for _, sc := range subClaims {
		sc := sc
		require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return runtime.ResolveClaimHandleTerminal(ctx, argsTwo, tx, runtime.TerminalDecision{
				ClaimHandleID:       sc.ClaimHandleID,
				SupervisorID:        supTwo,
				Source:              runtime.ActiveTerminal,
				Outcome:             runtime.AggregateCommit,
				Producer:            store,
				Scope:               []byte(sc.ClaimScope),
				Address:             []byte{},
				Lifetime:            "subgraph",
				ProducerName:        storeName,
				ParentClaimHandleID: &parentClaimID,
			})
		}))
	}

	// @deliberate: Every child row promoted to committed (no silent claimant-guard
	// no-op leaving an active child behind).
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

	// @deliberate: The parent settled: counters reflect BOTH children (the bump is
	// guarded on the parent's actual holder, not the child's
	// supervisor), the settlement takeover moved the handle, and the
	// aggregate Commit fired the producer verb on the parent ClaimID.
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
