// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type scopeOnlyPersist struct {
	scopes persistence.RunScopeTable
}

func (p *scopeOnlyPersist) Templates() persistence.TemplateTable       { return nil }
func (p *scopeOnlyPersist) TemplateTags() persistence.TemplateTagTable { return nil }
func (p *scopeOnlyPersist) Instances() persistence.InstanceTable       { return nil }
func (p *scopeOnlyPersist) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return nil
}
func (p *scopeOnlyPersist) Nodes() persistence.NodeTable                              { return nil }
func (p *scopeOnlyPersist) ClaimHandles() persistence.ClaimHandleTable                { return nil }
func (p *scopeOnlyPersist) NodeAttributes() persistence.NodeAttributeTable            { return nil }
func (p *scopeOnlyPersist) ClaimHolders() persistence.ClaimHolderTable                { return nil }
func (p *scopeOnlyPersist) Events() persistence.EventTable                            { return nil }
func (p *scopeOnlyPersist) Supervisors() persistence.SupervisorTable                  { return nil }
func (p *scopeOnlyPersist) Frames() persistence.FrameTable                            { return nil }
func (p *scopeOnlyPersist) BlobOrphans() persistence.BlobOrphanTable                  { return nil }
func (p *scopeOnlyPersist) WaitSet() persistence.WaitSetTable                         { return nil }
func (p *scopeOnlyPersist) Messages() persistence.MessagesTable                       { return nil }
func (p *scopeOnlyPersist) MessageIdempotencies() persistence.MessageIdempotencyTable { return nil }
func (p *scopeOnlyPersist) Lineage() persistence.LineageTable                         { return nil }
func (p *scopeOnlyPersist) PublisherSubscriptions() persistence.PublisherSubscriptionsTable {
	return nil
}
func (p *scopeOnlyPersist) NodeRunTree() persistence.RunTreeTable       { return nil }
func (p *scopeOnlyPersist) RunScopes() persistence.RunScopeTable        { return p.scopes }
func (p *scopeOnlyPersist) APIKeys() persistence.APIKeyTable            { return nil }
func (p *scopeOnlyPersist) DeploymentCA() persistence.DeploymentCATable { return nil }
func (p *scopeOnlyPersist) Breakpoints() persistence.BreakpointTable    { return nil }
func (p *scopeOnlyPersist) BreakpointHits() persistence.BreakpointHitTable {
	return nil
}

func (p *scopeOnlyPersist) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

type messagesPersist struct {
	scopeOnlyPersist
	msgs persistence.MessagesTable
}

func (p *messagesPersist) Messages() persistence.MessagesTable { return p.msgs }

// @concept: fan-out
// @story: typed-message-substitution
func TestSubstituteFanOutPartitionRequest_OverrideBindsFromMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const directive = `{{messages.invalidate.partition_request_override | "all"}}`

	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	deliverMessage := func(msgs *fakeMessagesTable, payload string) {
		t.Helper()
		id := shared.UUID(uuid.New())
		if err := msgs.Insert(ctx, nil, persistence.EnqueueMessageRequest{
			ID:         id,
			InstanceID: instanceID,
			Type:       "invalidate",
			Sender:     "operator",
			SenderKind: "operator",
			Payload:    json.RawMessage(payload),
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if ok, err := msgs.MarkDelivered(ctx, nil, id, frameID, time.Now().UTC()); err != nil || !ok {
			t.Fatalf("MarkDelivered: ok=%v err=%v", ok, err)
		}
	}

	newArgs := func(msgs persistence.MessagesTable) RunArgs {
		return RunArgs{
			Logger:  shared.SilentLogger{},
			Persist: &messagesPersist{msgs: msgs},
		}
	}
	out := &acquisition{InstanceID: instanceID}

	msgs := newFakeMessages()
	deliverMessage(msgs, `{"partition_request_override":{"partition_keys":["region-x","region-y"]}}`)
	got, err := substituteFanOutPartitionRequest(ctx, newArgs(msgs), nil, frameID, out, nil, directive)
	if err != nil {
		t.Fatalf("substitute (override present): %v", err)
	}
	var override struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(got, &override); err != nil {
		t.Fatalf("override did not reach SplitScope (template default fired?): %v (bytes=%s)", err, got)
	}
	if len(override.PartitionKeys) != 2 || override.PartitionKeys[0] != "region-x" || override.PartitionKeys[1] != "region-y" {
		t.Fatalf("override did not reach SplitScope: got partition_keys=%v, want [region-x region-y]", override.PartitionKeys)
	}

	gotDefault, err := substituteFanOutPartitionRequest(ctx, newArgs(newFakeMessages()), nil, frameID, out, nil, directive)
	if err != nil {
		t.Fatalf("substitute (no message): %v", err)
	}
	if string(gotDefault) != "all" {
		t.Fatalf("fallback default not used: got %q want %q", gotDefault, "all")
	}

	literal, err := substituteFanOutPartitionRequest(ctx, newArgs(newFakeMessages()), nil, frameID, out, nil, "all")
	if err != nil {
		t.Fatalf("substitute (literal): %v", err)
	}
	if string(literal) != "all" {
		t.Fatalf("literal partition_request mangled: got %q want %q", literal, "all")
	}
}

// @concept: fan-out
// @story: typed-message-substitution
func TestSubstituteFanOutPartitionRequest_OverrideBindsFromMessage_NonZeroReceiver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const directive = `{{messages.invalidate.partition_request_override | "all"}}`

	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	receiverNodeRunID := shared.UUID(uuid.New())

	msgs := newFakeMessages()
	id := shared.UUID(uuid.New())
	if err := msgs.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID:         id,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "operator",
		SenderKind: "operator",
		Payload:    json.RawMessage(`{"partition_request_override":{"partition_keys":["region-x","region-y"]}}`),
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ok, err := msgs.MarkDelivered(ctx, nil, id, frameID, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("MarkDelivered: ok=%v err=%v", ok, err)
	}

	wait := &fakeWaitSet{drained: map[shared.UUID][]persistence.WaitSetRow{}}
	runTree := &fakeRunTreeDeps{rows: map[shared.UUID]*persistence.NodeRunTreeRow{}}
	nodes := &fakeNodesDeps{rows: map[shared.UUID]*persistence.NodeRow{}}
	attrs := &fakeNodeAttrs{rows: map[shared.UUID]*persistence.NodeAttributesRow{}}
	persist := &depsCapablePersist{
		messagesPersist: messagesPersist{msgs: msgs},
		waitSet:         wait,
		runTree:         runTree,
		nodes:           nodes,
		nodeAttrs:       attrs,
	}
	args := RunArgs{Logger: shared.SilentLogger{}, Persist: persist}
	out := &acquisition{
		InstanceID: instanceID,
		NodeRunID:  receiverNodeRunID,
		FrameID:    frameID,
	}

	got, err := substituteFanOutPartitionRequest(ctx, args, nil, frameID, out, nil, directive)
	if err != nil {
		t.Fatalf("substitute (production-path, non-zero receiverNodeRunID): %v", err)
	}
	var override struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(got, &override); err != nil {
		t.Fatalf("override did not bind through BuildAttributeDeps' message seeding (production path): %v (bytes=%s)", err, got)
	}
	if len(override.PartitionKeys) != 2 || override.PartitionKeys[0] != "region-x" || override.PartitionKeys[1] != "region-y" {
		t.Fatalf("production-path message override did not reach SplitScope: got partition_keys=%v, want [region-x region-y]", override.PartitionKeys)
	}
}

// @concept: fan-out
// @story: typed-message-substitution
func TestSubstituteFanOutPartitionRequest_StrictDirectiveRefusesWithoutMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	out := &acquisition{InstanceID: shared.UUID(uuid.New())}
	args := RunArgs{Logger: shared.SilentLogger{}, Persist: &messagesPersist{msgs: newFakeMessages()}}

	_, err := substituteFanOutPartitionRequest(ctx, args, nil, frameID, out, nil, `{{messages.invalidate.partition_request_override}}`)
	if err == nil {
		t.Fatal("expected ErrMissingSource for a strict directive with no delivered message; got nil")
	}
	if !attributes.IsMissingSource(err) {
		t.Fatalf("expected ErrMissingSource, got %v", err)
	}
}

type staticScopeTable struct {
	rows map[shared.UUID]*persistence.RunScopeRow
}

func (s *staticScopeTable) Create(_ context.Context, _ persistence.Tx, row persistence.RunScopeRow) error {
	if s.rows == nil {
		s.rows = make(map[shared.UUID]*persistence.RunScopeRow)
	}
	s.rows[row.ID] = &row
	return nil
}

func (s *staticScopeTable) GetByID(_ context.Context, _ persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	if r, ok := s.rows[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, nil
}

func (s *staticScopeTable) GetFanoutPartition(_ context.Context, _ persistence.Tx, _ shared.UUID, _ string) (*persistence.RunScopeRow, error) {
	return nil, nil
}
func (s *staticScopeTable) Close(_ context.Context, _ persistence.Tx, _ shared.UUID) error {
	return nil
}
func (s *staticScopeTable) ListChildScopes(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}
func (s *staticScopeTable) ListParentChain(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}
func (s *staticScopeTable) ListTreeDeepestFirst(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}

// @concept: fan-out
func TestAcquireFanOutIfDeclared_ChildRunsSkipSplitScope(t *testing.T) {
	t.Parallel()
	parentRun := shared.UUID{1, 2, 3}
	scopeID := shared.UUID{9, 9, 9}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:              scopeID,
		ParentNodeRunID: &parentRun,
		PartitionKey:    "a",
		GraphName:       "main",
	})
	out := &acquisition{RunScopeID: scopeID}
	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-leaf",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{"partition_keys": ["a", "b", "c"]}`,
			ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
		},
	}
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		shared.UUID{},
		out,
		persistence.Candidate{},
		nodeDef,
		nil,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("expected nil error for child-run short-circuit; got %v", err)
	}
	if out.SubClaims != nil {
		t.Errorf("expected no SubClaims populated on child runs; got %d entries",
			len(out.SubClaims))
	}
}

func TestAcquireFanOutIfDeclared_RootRunWithoutMatchingAliasIsNoOp(t *testing.T) {
	t.Parallel()
	scopeID := shared.UUID{8, 8, 8}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:        scopeID,
		GraphName: "main",
	})
	out := &acquisition{RunScopeID: scopeID}
	nodeDef := &node.TemplateNodeDef{
		Type: "fan-root",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `{"partition_keys": ["a"]}`,
			ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
		},
	}
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		shared.UUID{},
		out,
		persistence.Candidate{},
		nodeDef,
		nil,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("expected nil error for root run with no matching alias; got %v", err)
	}
	if out.SubClaims != nil {
		t.Errorf("expected no SubClaims; got %d entries", len(out.SubClaims))
	}
}

func TestAcquireFanOutIfDeclared_NoFanOutSpecIsNoOp(t *testing.T) {
	t.Parallel()
	rootScopeID := shared.UUID{1}
	childScopeID := shared.UUID{2}
	parentRun := shared.UUID{3}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{ID: rootScopeID, GraphName: "main"})
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID: childScopeID, ParentNodeRunID: &parentRun, PartitionKey: "a", GraphName: "main",
	})
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	for _, scopeID := range []shared.UUID{rootScopeID, childScopeID} {
		out := &acquisition{RunScopeID: scopeID}
		nodeDef := &node.TemplateNodeDef{
			Type: "plain-node", Executor: "stub",
		}
		err := acquireFanOutIfDeclared(
			context.Background(),
			args,
			(persistence.Tx)(nil),
			shared.UUID{},
			out,
			persistence.Candidate{},
			nodeDef,
			nil,
			30*time.Second,
		)
		if err != nil {
			t.Fatalf("scope=%v: expected nil error; got %v", scopeID, err)
		}
	}
}

// @concept: fan-out
func TestAcquireFanOutIfDeclared_ForwardsSubstitutedOverrideToSplitScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const storeName = "fan-out-store"
	const directive = `{{messages.invalidate.partition_request_override | "all"}}`
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	rootScopeID := shared.UUID(uuid.New())

	scopes := &staticScopeTable{}
	_ = scopes.Create(ctx, nil, persistence.RunScopeRow{ID: rootScopeID, GraphName: "main"})

	msgs := newFakeMessages()
	msgID := shared.UUID(uuid.New())
	if err := msgs.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID:         msgID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "operator",
		SenderKind: "operator",
		Payload:    json.RawMessage(`{"partition_request_override":{"partition_keys":["region-x","region-y"]}}`),
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if ok, err := msgs.MarkDelivered(ctx, nil, msgID, frameID, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("MarkDelivered: ok=%v err=%v", ok, err)
	}

	errStop := errors.New("stop-after-capture")
	var captured []byte
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		captured = append([]byte(nil), req.PartitionRequest...)
		return claimproducer.SplitClaimScopeResponse{}, errStop
	}
	reg := locks.NewRegistry()
	reg.Add(storeName, store)

	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-root",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: directive,
		},
	}
	acquiredLocks := []AcquiredLock{{
		Alias:         "data",
		Spec:          claimproducer.ClaimSpec{ProducerName: storeName, Intent: "rw"},
		ClaimHandleID: shared.UUID(uuid.New()),
		ClaimResult:   claimproducer.ClaimResult{ClaimScope: json.RawMessage(`"parent-scope"`)},
	}}

	out := &acquisition{InstanceID: instanceID, RunScopeID: rootScopeID}
	persist := &messagesPersist{
		scopeOnlyPersist: scopeOnlyPersist{scopes: scopes},
		msgs:             msgs,
	}
	args := RunArgs{
		Persist:       persist,
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-FAN",
	}
	cand := persistence.Candidate{
		FrameID:   frameID,
		NodeID:    shared.UUID(uuid.New()),
		NodeRunID: shared.UUID(uuid.New()),
	}

	err := acquireFanOutIfDeclared(ctx, args, nil, instanceID, out, cand, nodeDef, acquiredLocks, 30*time.Second)

	if !errors.Is(err, errStop) {
		t.Fatalf("expected the sentinel short-circuit error from SplitScope, got %v", err)
	}
	if captured == nil {
		t.Fatal("SplitScope was never reached — acquireFanOutIfDeclared did not forward to AcquireSubClaims")
	}
	if string(captured) == directive {
		t.Fatalf("forwarded the literal template directive verbatim (the original silent bug): %s", captured)
	}
	if string(captured) == "all" {
		t.Fatalf("forwarded the template default — the override did not bind: %s", captured)
	}
	var got struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("forwarded bytes are not the override JSON: %v (bytes=%s)", err, captured)
	}
	if len(got.PartitionKeys) != 2 || got.PartitionKeys[0] != "region-x" || got.PartitionKeys[1] != "region-y" {
		t.Fatalf("forwarded override partitions = %v, want [region-x region-y]", got.PartitionKeys)
	}
}

// @concept: fan-out
func TestAcquireFanOutIfDeclared_SubgraphDelegationScopeStillFansOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const storeName = "delegation-scope-store"
	instanceID := shared.UUID(uuid.New())
	parentRun := shared.UUID(uuid.New())
	delegationScopeID := shared.UUID(uuid.New())

	scopes := &staticScopeTable{}
	_ = scopes.Create(ctx, nil, persistence.RunScopeRow{
		ID: delegationScopeID, ParentNodeRunID: &parentRun, PartitionKey: "", GraphName: "staging",
	})

	errStop := errors.New("stop-after-capture")
	store := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{}, errStop
	}
	reg := locks.NewRegistry()
	reg.Add(storeName, store)

	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-in-subgraph",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `"all"`,
		},
	}
	acquiredLocks := []AcquiredLock{{
		Alias:         "data",
		Spec:          claimproducer.ClaimSpec{ProducerName: storeName, Intent: "rw"},
		ClaimHandleID: shared.UUID(uuid.New()),
		ClaimResult:   claimproducer.ClaimResult{ClaimScope: json.RawMessage(`"parent-scope"`)},
	}}

	out := &acquisition{InstanceID: instanceID, RunScopeID: delegationScopeID}
	args := RunArgs{
		Persist:       &scopeOnlyPersist{scopes: scopes},
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-FAN-DELEGATE",
	}
	cand := persistence.Candidate{
		FrameID:   shared.UUID(uuid.New()),
		NodeID:    shared.UUID(uuid.New()),
		NodeRunID: shared.UUID(uuid.New()),
	}

	err := acquireFanOutIfDeclared(ctx, args, nil, instanceID, out, cand, nodeDef, acquiredLocks, 30*time.Second)
	if !errors.Is(err, errStop) {
		t.Fatalf("expected fan-out to reach SplitScope from a sub-graph delegation scope "+
			"(ParentNodeRunID set, PartitionKey empty) — the recursion guard must only suppress "+
			"re-fan-out of fan-out PARTITION scopes, not every non-root scope; got err=%v", err)
	}
}

// @concept: fan-out
type erroringScopeTable struct {
	staticScopeTable
	err error
}

func (s *erroringScopeTable) GetByID(_ context.Context, _ persistence.Tx, _ shared.UUID) (*persistence.RunScopeRow, error) {
	return nil, s.err
}

func TestAcquireFanOutIfDeclared_RunScopeLookupErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("run-scope lookup: transient db error")
	scopes := &erroringScopeTable{err: wantErr}
	out := &acquisition{RunScopeID: shared.UUID(uuid.New())}
	nodeDef := &node.TemplateNodeDef{
		Type:     "fan-leaf",
		Executor: "stub",
		FanOut: &node.FanOutSpec{
			Claim:            "data",
			PartitionRequest: `"all"`,
		},
	}
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(), args, (persistence.Tx)(nil), shared.UUID{}, out,
		persistence.Candidate{}, nodeDef, nil, 30*time.Second,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected acquireFanOutIfDeclared to propagate the RunScopes.GetByID error instead of "+
			"silently falling through and treating the run as a fan-out root; got %v", err)
	}
}

// @concept: fan-out
// @decision: substitution-grammar-closed
// @story: typed-message-substitution
func TestSubstituteFanOutPartitionRequest_BindsFromNodeAttribute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	runScopeID := shared.UUID(uuid.New())
	receiverNodeRunID := shared.UUID(uuid.New())
	receiverNodeID := shared.UUID(uuid.New())
	senderNodeRunID := shared.UUID(uuid.New())
	senderNodeID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	const (
		receiverType = "consumer"
		upstreamType = "prefilter"
	)
	itemsValue := []any{
		map[string]any{"key": "a", "payload": map[string]any{"v": float64(1)}},
		map[string]any{"key": "b", "payload": map[string]any{"v": float64(2)}},
	}

	templateHash := "tmpl-" + uuid.New().String()
	tmplSpec := spec.TemplateSpec{
		Name: "fanout-substitute-test", Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: upstreamType, Executor: "stub"},
			{Type: receiverType, Executor: "stub", Subscribes: []spec.SubscriptionEntry{
				{Node: upstreamType, Type: "attribute/items/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
			}},
		},
	}

	nodes := &fakeNodesDeps{
		rows: map[shared.UUID]*persistence.NodeRow{
			receiverNodeID: {ID: receiverNodeID, InstanceID: instanceID, NodeType: receiverType},
			senderNodeID:   {ID: senderNodeID, InstanceID: instanceID, NodeType: upstreamType},
		},
		gateRows: map[shared.UUID]*persistence.NodeRunForGate{
			receiverNodeRunID: {NodeRunID: receiverNodeRunID, NodeID: receiverNodeID, RunScopeID: runScopeID, FrameID: frameID},
		},
		freshByNodeInScope: map[freshKey]*persistence.NodeRunForGate{
			{nodeID: senderNodeID, runScopeID: runScopeID}: {
				NodeRunID: senderNodeRunID, NodeID: senderNodeID, RunScopeID: runScopeID, FrameID: frameID,
				State: cascade.NodeStateFresh,
			},
		},
		byInstance: map[shared.UUID][]persistence.NodeRow{
			instanceID: {
				{ID: receiverNodeID, InstanceID: instanceID, NodeType: receiverType},
				{ID: senderNodeID, InstanceID: instanceID, NodeType: upstreamType},
			},
		},
	}
	attrs := &fakeNodeAttrs{
		rows: map[shared.UUID]*persistence.NodeAttributesRow{
			senderNodeRunID: {NodeRunID: senderNodeRunID, NodeID: senderNodeID, Data: map[string]any{
				"items": itemsValue,
			}},
		},
	}
	instances := &fakeInstancesDeps{
		rows: map[shared.UUID]*persistence.InstanceRow{
			instanceID: {ID: instanceID, TemplateHash: templateHash},
		},
	}
	templates := &fakeTemplatesDeps{
		rows: map[string]*persistence.TemplateRow{
			templateHash: {ID: templateHash, Spec: tmplSpec, State: persistence.TemplateStateDeployed},
		},
	}

	persist := &depsCapablePersist{
		messagesPersist: messagesPersist{msgs: newFakeMessages()},
		waitSet:         &fakeWaitSet{drained: map[shared.UUID][]persistence.WaitSetRow{}},
		runTree:         &fakeRunTreeDeps{rows: map[shared.UUID]*persistence.NodeRunTreeRow{}},
		nodes:           nodes,
		nodeAttrs:       attrs,
		instances:       instances,
		templates:       templates,
	}
	args := RunArgs{
		Logger:  shared.SilentLogger{},
		Persist: persist,
	}

	out := &acquisition{
		NodeRunID:    receiverNodeRunID,
		FrameID:      frameID,
		InstanceID:   instanceID,
		TemplateHash: templateHash,
	}

	directive := "{{nodes." + upstreamType + ".attribute.items}}"
	got, err := substituteFanOutPartitionRequest(ctx, args, nil, frameID, out, nil, directive)
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	wantBytes, _ := json.Marshal(itemsValue)
	if string(got) != string(wantBytes) {
		t.Fatalf("nodes.<X>.attribute.items did not resolve through Deps: got %s want %s", got, wantBytes)
	}
}

type fakeWaitSet struct {
	drained map[shared.UUID][]persistence.WaitSetRow
}

func (f *fakeWaitSet) Insert(_ context.Context, _ persistence.WaitSetRow, _ persistence.Tx) error {
	return nil
}
func (f *fakeWaitSet) MarkDrainedBySender(_ context.Context, _, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeWaitSet) ListForReceiver(_ context.Context, _, _ shared.UUID, _ persistence.Tx) ([]persistence.WaitSetRow, error) {
	return nil, nil
}
func (f *fakeWaitSet) ListForFrame(_ context.Context, _ shared.UUID, _ persistence.Tx) ([]persistence.WaitSetRow, error) {
	return nil, nil
}
func (f *fakeWaitSet) ListDrainedAttributeRowsForReceiver(_ context.Context, _, receiverNodeRunID shared.UUID, _ persistence.Tx) ([]persistence.WaitSetRow, error) {
	return f.drained[receiverNodeRunID], nil
}
func (f *fakeWaitSet) ListSenderNodesForReceiver(_ context.Context, _, _ shared.UUID, _ persistence.Tx) ([]shared.UUID, error) {
	return nil, nil
}
func (f *fakeWaitSet) HasRowForSenderRun(_ context.Context, _, _, _ shared.UUID, _ persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeWaitSet) ListPendingReceiversForDrainedSender(_ context.Context, _, _ shared.UUID, _ persistence.Tx) ([]shared.UUID, error) {
	return nil, nil
}
func (f *fakeWaitSet) HasUndrainedRowsForReceiver(_ context.Context, _, _ shared.UUID, _ persistence.Tx) (bool, error) {
	return false, nil
}

type fakeRunTreeDeps struct {
	rows map[shared.UUID]*persistence.NodeRunTreeRow
}

func (f *fakeRunTreeDeps) CreateRootNodeRun(_ context.Context, _ persistence.Tx, _ persistence.CreateRootNodeRunInput) error {
	return nil
}
func (f *fakeRunTreeDeps) CreateChildNodeRun(_ context.Context, _ persistence.Tx, _ persistence.CreateChildNodeRunInput) error {
	return nil
}
func (f *fakeRunTreeDeps) GetByID(_ context.Context, _ persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
	r, ok := f.rows[runID]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeRunTreeDeps) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
	return f.GetByID(ctx, tx, runID)
}
func (f *fakeRunTreeDeps) ListChildren(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]persistence.NodeRunTreeRow, error) {
	return nil, nil
}
func (f *fakeRunTreeDeps) UpdateStateAndOutcome(_ context.Context, _ persistence.Tx, _ shared.UUID, _ cascade.NodeState, _ *string, _ bool) error {
	return nil
}
func (f *fakeRunTreeDeps) UpdateAggregationPolicy(_ context.Context, _ persistence.Tx, _ shared.UUID, _ spec.AggregationPolicy) error {
	return nil
}

type freshKey struct {
	nodeID, runScopeID shared.UUID
}

type fakeNodesDeps struct {
	rows               map[shared.UUID]*persistence.NodeRow
	gateRows           map[shared.UUID]*persistence.NodeRunForGate
	freshByNodeInScope map[freshKey]*persistence.NodeRunForGate
	byInstance         map[shared.UUID][]persistence.NodeRow
}

func (f *fakeNodesDeps) Create(_ context.Context, _ persistence.NodeCreateInput, _ persistence.Tx) (persistence.NodeRow, error) {
	return persistence.NodeRow{}, nil
}
func (f *fakeNodesDeps) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.NodeRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeNodesDeps) ListByInstance(_ context.Context, instanceID shared.UUID, _ persistence.Tx) ([]persistence.NodeRow, error) {
	if rows, ok := f.byInstance[instanceID]; ok {
		out := make([]persistence.NodeRow, len(rows))
		copy(out, rows)
		return out, nil
	}
	return nil, nil
}
func (f *fakeNodesDeps) ListByInstancePagedFiltered(_ context.Context, _ shared.UUID, _ persistence.ListPagination, _ persistence.NodeListFilter, _ persistence.Tx) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	return persistence.PaginatedListResult[persistence.NodeRow]{}, nil
}
func (f *fakeNodesDeps) ListReadyForDispatch(_ context.Context, _ persistence.Tx) ([]persistence.NodeRow, error) {
	return nil, nil
}
func (f *fakeNodesDeps) CountRunningForSupervisor(_ context.Context, _ string, _ persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeNodesDeps) CountAllNodes(_ context.Context, _ persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeNodesDeps) CountDistinctNodesWithRuns(_ context.Context, _ persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeNodesDeps) ListPureCascadeReady(_ context.Context, _ persistence.Tx) ([]persistence.PureCascadeReadyRow, error) {
	return nil, nil
}
func (f *fakeNodesDeps) CountByState(_ context.Context, _ persistence.Tx) (map[cascade.NodeState]int, error) {
	return nil, nil
}
func (f *fakeNodesDeps) UpdateState(_ context.Context, _ shared.UUID, _ cascade.NodeState, _ cascade.TransitionReason, _ *string, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodesDeps) UpdateError(_ context.Context, _ shared.UUID, _ spec.EvaluatorState, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodesDeps) ResetFailedTerminalSettlingSignalType(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodesDeps) GetFailedTerminalRunScopeID(_ context.Context, _ shared.UUID, _ persistence.Tx) (*shared.UUID, error) {
	return nil, nil
}
func (f *fakeNodesDeps) HasRunForNodeInFrame(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeNodesDeps) HasAdvancedSiblingInScope(_ context.Context, _ persistence.Tx, _, _, _ shared.UUID) (bool, error) {
	return false, nil
}
func (f *fakeNodesDeps) ListPendingSiblingRunsInScope(_ context.Context, _ persistence.Tx, _ shared.UUID) ([]shared.UUID, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetRunByDispatchIDForUpdate(_ context.Context, _ shared.UUID, _ persistence.Tx) (*persistence.NodeRunForCallback, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetCascadeMode(_ context.Context, _ shared.UUID, _ persistence.Tx) (cascade.CascadeMode, error) {
	return cascade.CascadeModeMostRecent, nil
}
func (f *fakeNodesDeps) GetRunSummary(_ context.Context, _ shared.UUID, _ persistence.Tx) (persistence.NodeRunSummary, error) {
	return persistence.NodeRunSummary{}, nil
}
func (f *fakeNodesDeps) GetRunSummaryForNodes(_ context.Context, _ []shared.UUID, _ persistence.Tx) (map[shared.UUID]persistence.NodeRunSummary, error) {
	return nil, nil
}
func (f *fakeNodesDeps) FindLatestCascadePending(_ context.Context, _ persistence.Tx, _, _, _ shared.UUID) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (f *fakeNodesDeps) CreateCascadePending(_ context.Context, _ persistence.Tx, _, _, _ shared.UUID) (shared.UUID, error) {
	return shared.UUID{}, nil
}
func (f *fakeNodesDeps) LockReceiverCascade(_ context.Context, _ persistence.Tx, _, _, _ shared.UUID) error {
	return nil
}
func (f *fakeNodesDeps) GetLatestRunForNode(_ context.Context, _ persistence.Tx, _ shared.UUID) (*persistence.NodeRunLatest, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetLatestRunForNodes(_ context.Context, _ persistence.Tx, _ []shared.UUID) (map[shared.UUID]persistence.NodeRunLatest, error) {
	return nil, nil
}
func (f *fakeNodesDeps) ListRunsForInstanceByStates(_ context.Context, _ persistence.Tx, _ shared.UUID, _ []cascade.NodeState) ([]persistence.NodeRunLatest, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetRunForGate(_ context.Context, _ persistence.Tx, runID shared.UUID) (*persistence.NodeRunForGate, error) {
	r, ok := f.gateRows[runID]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeNodesDeps) GetPriorRunBySequence(_ context.Context, _ persistence.Tx, _, _ shared.UUID, _ int64) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (f *fakeNodesDeps) DeletePriorCascadeStales(_ context.Context, _ persistence.Tx, _, _ shared.UUID, _ int64) (int, error) {
	return 0, nil
}
func (f *fakeNodesDeps) HasLaterCascadePending(_ context.Context, _ persistence.Tx, _, _ shared.UUID, _ int64) (bool, error) {
	return false, nil
}
func (f *fakeNodesDeps) ListPendingRunsInScopeForNodes(_ context.Context, _ persistence.Tx, _ shared.UUID, _ []shared.UUID) ([]shared.UUID, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetPriorCascadeStaleNotClaimed(_ context.Context, _ persistence.Tx, _, _ shared.UUID, _ int64) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (f *fakeNodesDeps) GetMostRecentSettledRun(_ context.Context, _ persistence.Tx, nodeID, runScopeID shared.UUID, _ int64) (*persistence.NodeRunForGate, error) {
	r, ok := f.freshByNodeInScope[freshKey{nodeID: nodeID, runScopeID: runScopeID}]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeNodesDeps) TransitionPendingToStale(_ context.Context, _ persistence.Tx, _ shared.UUID, _ time.Time) error {
	return nil
}
func (f *fakeNodesDeps) DropPendingRun(_ context.Context, _ persistence.Tx, _ shared.UUID) error {
	return nil
}
func (f *fakeNodesDeps) SetRunRequiredStores(_ context.Context, _ persistence.Tx, _ shared.UUID, _ []string) (bool, error) {
	return false, nil
}
func (f *fakeNodesDeps) CreateNonCascadeStale(_ context.Context, _ persistence.Tx, _ persistence.NonCascadeStaleInput) (shared.UUID, error) {
	return shared.UUID{}, nil
}
func (f *fakeNodesDeps) UpdateRunEvaluatorState(_ context.Context, _ shared.UUID, _ spec.EvaluatorState, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodesDeps) GetRunEvaluatorState(_ context.Context, _ shared.UUID, _ persistence.Tx) (spec.EvaluatorState, error) {
	return spec.EvaluatorState{}, nil
}

type fakeNodeAttrs struct {
	rows map[shared.UUID]*persistence.NodeAttributesRow
}

func (f *fakeNodeAttrs) GetByRun(_ context.Context, runID shared.UUID, _ persistence.Tx) (*persistence.NodeAttributesRow, error) {
	r, ok := f.rows[runID]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeNodeAttrs) GetLatestByNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) (*persistence.NodeAttributesRow, error) {
	return nil, nil
}
func (f *fakeNodeAttrs) Upsert(_ context.Context, _, _ shared.UUID, _ map[string]any, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodeAttrs) MergeDelta(_ context.Context, _ shared.UUID, _ map[string]any, _ persistence.Tx) error {
	return nil
}
func (f *fakeNodeAttrs) SetDispatchInputBag(_ context.Context, _ persistence.Tx, _, _ shared.UUID, _ map[string]any) error {
	return nil
}
func (f *fakeNodeAttrs) GetDispatchInputBag(_ context.Context, _ persistence.Tx, _ shared.UUID) (map[string]any, error) {
	return nil, nil
}
func (f *fakeNodeAttrs) SnapshotBagForNewRun(_ context.Context, _ persistence.Tx, _, _, _ shared.UUID) error {
	return nil
}
func (f *fakeNodeAttrs) GetPriorRunData(_ context.Context, _ persistence.Tx, _ shared.UUID) (map[string]any, error) {
	return map[string]any{}, nil
}

type depsCapablePersist struct {
	messagesPersist
	waitSet   persistence.WaitSetTable
	runTree   persistence.RunTreeTable
	nodes     persistence.NodeTable
	nodeAttrs persistence.NodeAttributeTable
	instances persistence.InstanceTable
	templates persistence.TemplateTable
}

func (p *depsCapablePersist) WaitSet() persistence.WaitSetTable              { return p.waitSet }
func (p *depsCapablePersist) NodeRunTree() persistence.RunTreeTable          { return p.runTree }
func (p *depsCapablePersist) Nodes() persistence.NodeTable                   { return p.nodes }
func (p *depsCapablePersist) NodeAttributes() persistence.NodeAttributeTable { return p.nodeAttrs }
func (p *depsCapablePersist) Instances() persistence.InstanceTable           { return p.instances }
func (p *depsCapablePersist) Templates() persistence.TemplateTable           { return p.templates }

// @concept: fan-out
// @decision: substitution-grammar-closed
func TestSubstituteFanOutPartitionRequest_BindsFromAcquiredClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	acquiredLocks := []AcquiredLock{{
		Alias: "data",
		Spec:  claimproducer.ClaimSpec{ProducerName: "store"},
		ClaimResult: claimproducer.ClaimResult{
			Payload: json.RawMessage(`{"items":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}}]}`),
		},
	}}

	out := &acquisition{InstanceID: instanceID}
	args := RunArgs{
		Logger:  shared.SilentLogger{},
		Persist: &messagesPersist{msgs: newFakeMessages()},
	}

	got, err := substituteFanOutPartitionRequest(
		ctx, args, nil, frameID, out, acquiredLocks,
		`{"list":{{claim.data.payload.items}}}`,
	)
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	var parsed struct {
		List []struct {
			Key     string          `json:"key"`
			Payload json.RawMessage `json:"payload"`
		} `json:"list"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("unmarshal substituted bytes: %v (bytes=%s)", err, got)
	}
	if len(parsed.List) != 2 || parsed.List[0].Key != "a" || parsed.List[1].Key != "b" {
		t.Fatalf("substituted list = %v, want [{a ...} {b ...}]", parsed.List)
	}
}

// @concept: fan-out
func TestSubstituteFanOutPartitionRequest_BindsFromHeldClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())

	out := &acquisition{
		InstanceID: instanceID,
		HeldClaims: map[string]claimproducer.ClaimResult{
			"inherited": {
				Payload: json.RawMessage(`{"items":[{"key":"x","payload":{}}]}`),
			},
		},
	}
	args := RunArgs{
		Logger:  shared.SilentLogger{},
		Persist: &messagesPersist{msgs: newFakeMessages()},
	}

	got, err := substituteFanOutPartitionRequest(
		ctx, args, nil, frameID, out, nil,
		`{"list":{{claim.inherited.payload.items}}}`,
	)
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	var parsed struct {
		List []struct {
			Key string `json:"key"`
		} `json:"list"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("unmarshal substituted bytes: %v (bytes=%s)", err, got)
	}
	if len(parsed.List) != 1 || parsed.List[0].Key != "x" {
		t.Fatalf("substituted list = %v, want [{x}]", parsed.List)
	}
}

type fakeInstancesDeps struct {
	rows map[shared.UUID]*persistence.InstanceRow
}

func (f *fakeInstancesDeps) Create(_ context.Context, _ persistence.InstanceCreateInput, _ persistence.Tx) (persistence.InstanceRow, error) {
	return persistence.InstanceRow{}, nil
}
func (f *fakeInstancesDeps) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.InstanceRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeInstancesDeps) GetByInstanceKey(_ context.Context, _ string, _ string, _ persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesDeps) FindAnyByInstanceKey(_ context.Context, _ string, _ persistence.Tx) (*persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesDeps) List(_ context.Context, _ persistence.InstanceListFilter, _ persistence.ListPagination, _ persistence.Tx) (persistence.PaginatedListResult[persistence.InstanceRow], error) {
	return persistence.PaginatedListResult[persistence.InstanceRow]{}, nil
}
func (f *fakeInstancesDeps) Delete(_ context.Context, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeInstancesDeps) MarkTerminated(_ context.Context, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeInstancesDeps) CountActiveByTemplate(_ context.Context, _ string, _ persistence.Tx) (int, error) {
	return 0, nil
}
func (f *fakeInstancesDeps) ListTerminatedWithLifecycleRows(_ context.Context, _ int, _ persistence.Tx) ([]persistence.InstanceRow, error) {
	return nil, nil
}
func (f *fakeInstancesDeps) CountByActive(_ context.Context, _ persistence.Tx) (int, int, error) {
	return 0, 0, nil
}
func (f *fakeInstancesDeps) SetPaused(_ context.Context, _ shared.UUID, _ bool, _ persistence.Tx) (bool, error) {
	return false, nil
}

type fakeTemplatesDeps struct {
	rows map[string]*persistence.TemplateRow
}

func (f *fakeTemplatesDeps) Insert(_ context.Context, _ persistence.TemplateInsertInput, _ persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesDeps) GetByHash(_ context.Context, hash string, _ persistence.Tx) (*persistence.TemplateRow, error) {
	r, ok := f.rows[hash]
	if !ok {
		return nil, nil
	}
	c := *r
	return &c, nil
}
func (f *fakeTemplatesDeps) List(_ context.Context, _ persistence.TemplateListFilter, _ persistence.ListPagination, _ persistence.Tx) (persistence.PaginatedListResult[persistence.TemplateRow], error) {
	return persistence.PaginatedListResult[persistence.TemplateRow]{}, nil
}
func (f *fakeTemplatesDeps) UpdateState(_ context.Context, _ string, _ persistence.TemplateState, _ persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesDeps) DeleteByHash(_ context.Context, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeTemplatesDeps) LockForUpdate(_ context.Context, _ string, _ persistence.Tx) (*persistence.TemplateRow, error) {
	return nil, nil
}
