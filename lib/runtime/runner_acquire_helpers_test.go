// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Same-package unit tests for the runner_acquire helpers. The
// production-bug regression pin lives here as a deterministic,
// harness-free check that the recursion-guard in
// `acquireFanOutIfDeclared` short-circuits non-root acquisitions.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// scopeOnlyPersist is a minimal persistence.Tables surface that only
// honors RunScopes(). Every other accessor returns nil. Used by the
// child-run-short-circuit tests below so that
// `acquireFanOutIfDeclared`'s `args.Persist.RunScopes().GetByID(...)`
// resolves without touching any other infrastructure.
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
func (p *scopeOnlyPersist) RunTree() persistence.RunTreeTable        { return nil }
func (p *scopeOnlyPersist) RunScopes() persistence.RunScopeTable     { return p.scopes }
func (p *scopeOnlyPersist) APIKeys() persistence.APIKeyTable         { return nil }
func (p *scopeOnlyPersist) Breakpoints() persistence.BreakpointTable { return nil }
func (p *scopeOnlyPersist) BreakpointHits() persistence.BreakpointHitTable {
	return nil
}

func (p *scopeOnlyPersist) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

// messagesPersist extends scopeOnlyPersist with a live Messages() table,
// used by the partition_request-substitution tests so the by-frame
// trigger-message lookup resolves against an in-memory fake.
type messagesPersist struct {
	scopeOnlyPersist
	msgs persistence.MessagesTable
}

func (p *messagesPersist) Messages() persistence.MessagesTable { return p.msgs }

// TestSubstituteFanOutPartitionRequest_OverrideBindsFromTriggerMessage is
// the regression pin for the silent partition-request override drop. It
// asserts the SUBSTITUTED partition_request bytes carry the triggering
// message's `partition_request_override` — i.e. the override changes the
// partitions that reach SplitScope — and that the absence of a trigger
// message falls back to the template default (the `|`-fallback), never a
// panic or a silent wrong-partition run.
//
// Without runtime substitution the literal template bytes flow verbatim,
// so the `{{trigger.message.payload…}}` directive never resolves and the
// override is silently lost (the `|`-fallback always fires).
//
// @concept: fan-out
func TestSubstituteFanOutPartitionRequest_OverrideBindsFromTriggerMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @deliberate: The canonical fan-out partition_request: pull the override off the
	// trigger message, else fall back to the template default. The
	// fallback literal grammar admits a quoted string (here "all", the
	// "process every partition" request a producer understands by
	// default); the override that rides the message can be any JSON value
	// (here a structured partition_keys object).
	const directive = `{{trigger.message.payload.partition_request_override | "all"}}`

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

	// @deliberate: (1) Override present → substituted bytes carry the override's
	// partition keys, NOT the template default.
	msgs := newFakeMessages()
	deliverMessage(msgs, `{"partition_request_override":{"partition_keys":["region-x","region-y"]}}`)
	got, err := substituteFanOutPartitionRequest(ctx, newArgs(msgs), nil, frameID, out, directive)
	if err != nil {
		t.Fatalf("substitute (override present): %v", err)
	}
	var override struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	// @deliberate: If the silent-drop bug were present, the directive would never
	// resolve and the bytes would be the literal fallback "all" — which
	// is not valid JSON for this struct, so the unmarshal would fail.
	if err := json.Unmarshal(got, &override); err != nil {
		t.Fatalf("override did not reach SplitScope (template default fired?): %v (bytes=%s)", err, got)
	}
	if len(override.PartitionKeys) != 2 || override.PartitionKeys[0] != "region-x" || override.PartitionKeys[1] != "region-y" {
		t.Fatalf("override did not reach SplitScope: got partition_keys=%v, want [region-x region-y]", override.PartitionKeys)
	}

	// @deliberate: (2) No trigger message in the frame → the `|`-fallback fires and the
	// template default ("all") is used (no panic, no error). The
	// quoted-string literal resolves to the Go string and flows through
	// as raw bytes.
	gotDefault, err := substituteFanOutPartitionRequest(ctx, newArgs(newFakeMessages()), nil, frameID, out, directive)
	if err != nil {
		t.Fatalf("substitute (no trigger): %v", err)
	}
	if string(gotDefault) != "all" {
		t.Fatalf("fallback default not used: got %q want %q", gotDefault, "all")
	}

	// @deliberate: (3) A literal partition_request (no directive, e.g. "all") flows
	// through as its raw bytes verbatim — preserving the producer's
	// existing interpretation.
	literal, err := substituteFanOutPartitionRequest(ctx, newArgs(newFakeMessages()), nil, frameID, out, "all")
	if err != nil {
		t.Fatalf("substitute (literal): %v", err)
	}
	if string(literal) != "all" {
		t.Fatalf("literal partition_request mangled: got %q want %q", literal, "all")
	}
}

// TestSubstituteFanOutPartitionRequest_StrictDirectiveRefusesWithoutTrigger
// pins the "refuse, don't guess" path: a STRICT directive (no
// `|`-fallback) with no trigger message bound returns ErrMissingSource
// rather than silently substituting wrong/empty partition bytes. The
// caller (acquireFanOutIfDeclared) rolls back the acquisition on that
// error so the run never proceeds with a mis-resolved partition request.
func TestSubstituteFanOutPartitionRequest_StrictDirectiveRefusesWithoutTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frameID := shared.UUID(uuid.New())
	out := &acquisition{InstanceID: shared.UUID(uuid.New())}
	args := RunArgs{Logger: shared.SilentLogger{}, Persist: &messagesPersist{msgs: newFakeMessages()}}

	_, err := substituteFanOutPartitionRequest(ctx, args, nil, frameID, out, `{{trigger.message.payload.partition_request_override}}`)
	if err == nil {
		t.Fatal("expected ErrMissingSource for a strict directive with no trigger message; got nil")
	}
	if !attributes.IsMissingSource(err) {
		t.Fatalf("expected ErrMissingSource, got %v", err)
	}
}

// staticScopeTable is a RunScopeTable that returns a single canned row
// for GetByID. The other accessors are unused by these tests.
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

// TestAcquireFanOutIfDeclared_ChildRunsSkipSplitScope is the regression
// pin for the fan-out recursion bug: children of a fan-out parent
// re-use the parent's `node_id` (per
// `runtime/fanout_dispatch.go::dispatchFanOutChildren`'s
// single child-run spec) and therefore
// inherit the same `nodeDef.FanOut`. Without the run-scope-has-parent
// guard, every child re-fires `SplitScope` and creates grand-children:
// a fan-out of 3 partition keys produces 3 → 9 → 27 → … runs without
// bound.
//
// The test invokes `acquireFanOutIfDeclared` directly with the
// child-run shape (acquisition.RunScopeID resolves to a RunScopeRow with
// a non-nil ParentRunID) plus a `nodeDef` that declares fan_out. The
// guard MUST return nil without consulting the store registry —
// `args.StoreRegistry` is left intentionally nil so any call into the
// registry would panic, surfacing a regression as an unmistakable
// failure.
//
// @concept: fan-out
func TestAcquireFanOutIfDeclared_ChildRunsSkipSplitScope(t *testing.T) {
	t.Parallel()
	parentRun := shared.UUID{1, 2, 3}
	scopeID := shared.UUID{9, 9, 9}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:           scopeID,
		ParentRunID:  &parentRun, // @deliberate: <-- this marks the scope as a fan-out partition
		PartitionKey: "a",
		GraphName:    "main",
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
	// @deliberate: Args.StoreRegistry intentionally nil — if the guard fails to
	// short-circuit, the helper will reach into the registry to call
	// SplitScope and crash. That crash is the regression signal.
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		shared.UUID{},
		out,
		persistence.Candidate{},
		nodeDef,
		nil, // @deliberate: acquiredLocks
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

// TestAcquireFanOutIfDeclared_RootRunWithoutMatchingAliasIsNoOp covers
// the complementary path: a root run (RunScopeID resolves to a scope
// with nil ParentRunID) whose nodeDef declares fan_out but whose
// `acquiredLocks` does NOT contain the referenced alias. The helper
// returns nil (best-effort safe) so the caller proceeds without
// sub-claims. Pinned here as a guard against the recursion-guard
// accidentally catching root runs.
func TestAcquireFanOutIfDeclared_RootRunWithoutMatchingAliasIsNoOp(t *testing.T) {
	t.Parallel()
	scopeID := shared.UUID{8, 8, 8}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID:        scopeID,
		GraphName: "main",
		// @deliberate: ParentRunID nil — main RunScope, root run.
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
	// @deliberate: No acquiredLocks → no parent claim to split → helper bails with
	// nil (validator catches this case at template-deploy time; the
	// runtime path is best-effort safe).
	args := RunArgs{Persist: &scopeOnlyPersist{scopes: scopes}}
	err := acquireFanOutIfDeclared(
		context.Background(),
		args,
		(persistence.Tx)(nil),
		shared.UUID{},
		out,
		persistence.Candidate{},
		nodeDef,
		nil, // @deliberate: acquiredLocks — no parent claim
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("expected nil error for root run with no matching alias; got %v", err)
	}
	if out.SubClaims != nil {
		t.Errorf("expected no SubClaims; got %d entries", len(out.SubClaims))
	}
}

// TestAcquireFanOutIfDeclared_NoFanOutSpecIsNoOp pins the baseline
// "node does not declare fan_out" short-circuit. The helper returns
// nil immediately regardless of RunScope shape.
func TestAcquireFanOutIfDeclared_NoFanOutSpecIsNoOp(t *testing.T) {
	t.Parallel()
	// @deliberate: Two scopes: one root, one child — both must short-circuit.
	rootScopeID := shared.UUID{1}
	childScopeID := shared.UUID{2}
	parentRun := shared.UUID{3}
	scopes := &staticScopeTable{}
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{ID: rootScopeID, GraphName: "main"})
	_ = scopes.Create(context.Background(), nil, persistence.RunScopeRow{
		ID: childScopeID, ParentRunID: &parentRun, PartitionKey: "a", GraphName: "main",
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

// TestAcquireFanOutIfDeclared_ForwardsSubstitutedOverrideToSplitScope
// pins the FORWARDING seam between substitution and acquisition: the
// substituted partition_request (carrying the trigger-message override)
// must be the bytes acquireFanOutIfDeclared hands to AcquireSubClaims →
// ClaimProducer.SplitScope — NOT the literal template directive. The
// companion TestSubstituteFanOutPartitionRequest_* tests prove the
// substitution itself binds the override; this one proves the caller
// actually forwards that result. Reintroducing the original silent bug
// (passing []byte(nodeDef.FanOut.PartitionRequest) verbatim) would make
// the captured bytes the unresolved directive and fail this test — which
// the substitution-only tests would NOT catch.
//
// The fake producer's SplitScope captures the request and returns a
// sentinel error to short-circuit before any sub-claim persistence runs;
// the forwarded bytes are the whole subject under test.
//
// @concept: fan-out
func TestAcquireFanOutIfDeclared_ForwardsSubstitutedOverrideToSplitScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const storeName = "fan-out-store"
	const directive = `{{trigger.message.payload.partition_request_override | "all"}}`
	frameID := shared.UUID(uuid.New())
	instanceID := shared.UUID(uuid.New())
	rootScopeID := shared.UUID(uuid.New())

	// @deliberate: Root run scope (nil ParentRunID) so the recursion guard does not
	// short-circuit — this is the run that splits.
	scopes := &staticScopeTable{}
	_ = scopes.Create(ctx, nil, persistence.RunScopeRow{ID: rootScopeID, GraphName: "main"})

	// @deliberate: Deliver an invalidate message to the frame carrying the
	// partition_request override, exactly as a real operator request would.
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

	// @deliberate: Fake producer: capture the PartitionRequest SplitScope receives,
	// then short-circuit with a sentinel so no sub-claim persistence runs.
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
			// @deliberate: Empty ErrorPolicy → AcquireSubClaims skips the
			// SetAggregationPolicy branch (no ClaimHandles fake needed);
			// SplitScope is reached and captured before it regardless.
		},
	}
	acquiredLocks := []AcquiredLock{{
		Alias:         "data",
		Spec:          claimproducer.ClaimSpec{ProducerName: storeName},
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
		FrameID:    frameID,
		NodeID:     shared.UUID(uuid.New()),
		DispatchID: shared.UUID(uuid.New()),
	}

	err := acquireFanOutIfDeclared(ctx, args, nil, instanceID, out, cand, nodeDef, acquiredLocks, 30*time.Second)

	// @deliberate: The sentinel short-circuit is expected; what matters is WHAT reached
	// SplitScope.
	if !errors.Is(err, errStop) {
		t.Fatalf("expected the sentinel short-circuit error from SplitScope, got %v", err)
	}
	if captured == nil {
		t.Fatal("SplitScope was never reached — acquireFanOutIfDeclared did not forward to AcquireSubClaims")
	}
	// @deliberate: The forwarded bytes must be the SUBSTITUTED override — not the
	// literal directive (the original bug) and not the template default.
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
