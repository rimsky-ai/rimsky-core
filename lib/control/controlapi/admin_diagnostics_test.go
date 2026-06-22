// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type noopStore struct{}

type noopTx struct{ persistence.TxMarker }

func (noopStore) Templates() persistence.TemplateTable       { return nil }
func (noopStore) TemplateTags() persistence.TemplateTagTable { return nil }
func (noopStore) Instances() persistence.InstanceTable       { return nil }
func (noopStore) LifecycleIdempotency() persistence.LifecycleIdempotencyTable {
	return nil
}
func (noopStore) Nodes() persistence.NodeTable                                    { return noopNodes{} }
func (noopStore) ClaimHandles() persistence.ClaimHandleTable                      { return nil }
func (noopStore) NodeAttributes() persistence.NodeAttributeTable                  { return nil }
func (noopStore) ClaimHolders() persistence.ClaimHolderTable                      { return nil }
func (noopStore) Events() persistence.EventTable                                  { return nil }
func (noopStore) Supervisors() persistence.SupervisorTable                        { return nil }
func (noopStore) Frames() persistence.FrameTable                                  { return nil }
func (noopStore) BlobOrphans() persistence.BlobOrphanTable                        { return nil }
func (noopStore) WaitSet() persistence.WaitSetTable                               { return nil }
func (noopStore) Messages() persistence.MessagesTable                             { return nil }
func (noopStore) MessageIdempotencies() persistence.MessageIdempotencyTable       { return nil }
func (noopStore) Lineage() persistence.LineageTable                               { return nil }
func (noopStore) PublisherSubscriptions() persistence.PublisherSubscriptionsTable { return nil }
func (noopStore) RunTree() persistence.RunTreeTable                               { return nil }
func (noopStore) RunScopes() persistence.RunScopeTable                            { return nil }
func (noopStore) APIKeys() persistence.APIKeyTable                                { return nil }
func (noopStore) Breakpoints() persistence.BreakpointTable                        { return nil }
func (noopStore) BreakpointHits() persistence.BreakpointHitTable                  { return nil }

func (noopStore) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, &noopTx{})
}

type noopNodes struct{}

func (noopNodes) Create(context.Context, persistence.NodeCreateInput, persistence.Tx) (persistence.NodeRow, error) {
	return persistence.NodeRow{}, nil
}
func (noopNodes) Get(_ context.Context, id shared.UUID, _ persistence.Tx) (*persistence.NodeRow, error) {
	return &persistence.NodeRow{ID: id}, nil
}
func (noopNodes) ListByInstance(context.Context, shared.UUID, persistence.Tx) ([]persistence.NodeRow, error) {
	return nil, nil
}
func (noopNodes) ListByInstancePaged(context.Context, shared.UUID, persistence.ListPagination, persistence.Tx) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	return persistence.PaginatedListResult[persistence.NodeRow]{}, nil
}
func (noopNodes) ListByInstancePagedFiltered(context.Context, shared.UUID, persistence.ListPagination, persistence.NodeListFilter, persistence.Tx) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	return persistence.PaginatedListResult[persistence.NodeRow]{}, nil
}
func (noopNodes) ListReadyForDispatch(context.Context, persistence.Tx) ([]persistence.NodeRow, error) {
	return nil, nil
}
func (noopNodes) ListRunning(context.Context, persistence.Tx) ([]persistence.NodeRow, error) {
	return nil, nil
}
func (noopNodes) CountRunningForSupervisor(context.Context, string, persistence.Tx) (int, error) {
	return 0, nil
}
func (noopNodes) CountAllNodes(context.Context, persistence.Tx) (int, error) {
	return 0, nil
}
func (noopNodes) CountDistinctNodesWithRuns(context.Context, persistence.Tx) (int, error) {
	return 0, nil
}
func (noopNodes) ListPureCascadeReady(context.Context, persistence.Tx) ([]persistence.PureCascadeReadyRow, error) {
	return nil, nil
}
func (noopNodes) CountByState(context.Context, persistence.Tx) (map[cascade.NodeState]int, error) {
	return nil, nil
}
func (noopNodes) UpdateState(context.Context, shared.UUID, shared.UUID, cascade.NodeState, cascade.TransitionReason, *string, persistence.Tx) error {
	return nil
}
func (noopNodes) UpdateError(context.Context, shared.UUID, spec.EvaluatorState, persistence.Tx) error {
	return nil
}
func (noopNodes) UpdateHeartbeat(context.Context, shared.UUID, shared.UUID, time.Time, string, persistence.Tx) error {
	return nil
}
func (noopNodes) SetFrameID(context.Context, shared.UUID, *shared.UUID, persistence.Tx) error {
	return nil
}
func (noopNodes) ClearSettlingSignalType(context.Context, shared.UUID, shared.UUID, persistence.Tx) error {
	return nil
}
func (noopNodes) ResetFailedTerminalSettlingSignalType(context.Context, shared.UUID, shared.UUID, persistence.Tx) error {
	return nil
}
func (noopNodes) GetFailedTerminalRunScopeID(context.Context, shared.UUID, persistence.Tx) (*shared.UUID, error) {
	return nil, nil
}
func (noopNodes) DeleteByInstance(context.Context, shared.UUID, persistence.Tx) error { return nil }
func (noopNodes) HasRunForNodeInFrame(context.Context, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (noopNodes) GetRunByDispatchIDForUpdate(context.Context, shared.UUID, persistence.Tx) (*persistence.NodeRunForCallback, error) {
	return nil, nil
}
func (noopNodes) GetCascadeMode(context.Context, shared.UUID, persistence.Tx) (cascade.CascadeMode, error) {
	return cascade.CascadeModeMostRecent, nil
}
func (noopNodes) GetRunSummary(context.Context, shared.UUID, persistence.Tx) (persistence.NodeRunSummary, error) {
	return persistence.NodeRunSummary{}, nil
}
func (noopNodes) FindLatestCascadePending(context.Context, persistence.Tx, shared.UUID, shared.UUID, shared.UUID) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (noopNodes) CreateCascadePending(context.Context, persistence.Tx, shared.UUID, shared.UUID, shared.UUID) (shared.UUID, error) {
	return shared.UUID{}, nil
}
func (noopNodes) LockReceiverCascade(context.Context, persistence.Tx, shared.UUID, shared.UUID, shared.UUID) error {
	return nil
}
func (noopNodes) GetLatestRunForNode(context.Context, persistence.Tx, shared.UUID) (*persistence.NodeRunLatest, error) {
	return nil, nil
}
func (noopNodes) GetLatestRunInScope(context.Context, persistence.Tx, shared.UUID, shared.UUID) (*persistence.NodeRunLatest, error) {
	return nil, nil
}
func (noopNodes) ListRunsForInstanceByStates(context.Context, persistence.Tx, shared.UUID, []cascade.NodeState) ([]persistence.NodeRunLatest, error) {
	return nil, nil
}
func (noopNodes) GetRunForGate(context.Context, persistence.Tx, shared.UUID) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (noopNodes) GetPriorRunBySequence(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (noopNodes) DeletePriorCascadeStales(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (int, error) {
	return 0, nil
}
func (noopNodes) GetPriorCascadeStaleNotClaimed(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (noopNodes) GetMostRecentSettledRun(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (*persistence.NodeRunForGate, error) {
	return nil, nil
}
func (noopNodes) TransitionPendingToStale(context.Context, persistence.Tx, shared.UUID, time.Time) error {
	return nil
}
func (noopNodes) DropPendingRun(context.Context, persistence.Tx, shared.UUID) error {
	return nil
}
func (noopNodes) CreateNonCascadeStale(context.Context, persistence.Tx, persistence.NonCascadeStaleInput) (shared.UUID, error) {
	return shared.UUID{}, nil
}
func (noopNodes) UpdateRunEvaluatorState(context.Context, shared.UUID, spec.EvaluatorState, persistence.Tx) error {
	return nil
}
func (noopNodes) GetRunEvaluatorState(context.Context, shared.UUID, persistence.Tx) (spec.EvaluatorState, error) {
	return spec.EvaluatorState{}, nil
}

type fakeDiagnosticQueue struct {
	rows []persistence.ParkedDiagnosticRow
}

func (f *fakeDiagnosticQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx, reasonFilter string) ([]persistence.ParkedDiagnosticRow, error) {
	if reasonFilter == "" {
		return f.rows, nil
	}
	var out []persistence.ParkedDiagnosticRow
	for _, r := range f.rows {
		if r.Reason == reasonFilter {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDiagnosticQueue) Enqueue(context.Context, persistence.DispatchRequest) error {
	return nil
}
func (f *fakeDiagnosticQueue) EnqueueInTx(context.Context, persistence.DispatchRequest, persistence.Tx) error {
	return nil
}
func (f *fakeDiagnosticQueue) SelectCandidates(context.Context, persistence.Tx, persistence.SelectCandidatesRequest) ([]persistence.Candidate, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) ClaimDispatchRow(context.Context, persistence.Tx, shared.UUID, string) (bool, error) {
	return false, nil
}
func (f *fakeDiagnosticQueue) PromoteClaimedToRunning(context.Context, persistence.Tx, shared.UUID, string) (bool, error) {
	return false, nil
}
func (f *fakeDiagnosticQueue) Complete(context.Context, shared.UUID, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) RemoveForNode(context.Context, shared.UUID, shared.UUID, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) RemoveForNodeInTx(context.Context, shared.UUID, shared.UUID, string, persistence.Tx) error {
	return nil
}
func (f *fakeDiagnosticQueue) ListOrphanedClaims(context.Context) ([]persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) ReleaseClaim(context.Context, shared.UUID, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) GetClaimedBy(context.Context, shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{}, nil
}
func (f *fakeDiagnosticQueue) GetDispatchNode(context.Context, shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{}, nil
}
func (f *fakeDiagnosticQueue) RefreshHeartbeat(context.Context, string) error { return nil }
func (f *fakeDiagnosticQueue) ListLive(context.Context, persistence.DispatchListFilter, persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	return persistence.PaginatedListResult[persistence.DispatchRow]{}, nil
}
func (f *fakeDiagnosticQueue) CountLive(context.Context, persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *fakeDiagnosticQueue) CountParkedByReason(context.Context) (map[string]int, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) GetByID(context.Context, shared.UUID) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) GetInFlightRunForNode(context.Context, persistence.Tx, shared.UUID, shared.UUID) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeDiagnosticQueue) GetMostRecentRunForNodeInScope(context.Context, persistence.Tx, shared.UUID, shared.UUID) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeDiagnosticQueue) ListInFlightRunStates(context.Context, persistence.Tx, []shared.UUID, shared.UUID, shared.UUID) (map[shared.UUID][]string, error) {
	return map[shared.UUID][]string{}, nil
}
func (f *fakeDiagnosticQueue) ParkActiveInTx(context.Context, persistence.Tx, persistence.ParkActiveInput) error {
	return nil
}
func (f *fakeDiagnosticQueue) ListParkedReadyForResume(context.Context, time.Time, int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) ListParkedOverdue(context.Context, time.Time, int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) GetParkedByNode(context.Context, shared.UUID, shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) ResumeParkedInTx(context.Context, persistence.Tx, shared.UUID) (bool, error) {
	return false, nil
}
func (f *fakeDiagnosticQueue) RebindRunFrameInTx(context.Context, persistence.Tx, shared.UUID, shared.UUID) error {
	return nil
}
func (f *fakeDiagnosticQueue) GetRetryNoProgress(context.Context, shared.UUID) (int, *int, error) {
	return 0, nil, nil
}
func (f *fakeDiagnosticQueue) SetRetryNoProgressForRunInTx(context.Context, persistence.Tx, shared.UUID, int) error {
	return nil
}
func (f *fakeDiagnosticQueue) UpdateDispatchTuningInTx(context.Context, persistence.Tx, shared.UUID, *int, *int) error {
	return nil
}
func (f *fakeDiagnosticQueue) BumpLastProgressAt(context.Context, persistence.Tx, shared.UUID, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeDiagnosticQueue) RegisterAsyncAck(context.Context, persistence.Tx, shared.UUID, string, time.Time, *int, *int) error {
	return nil
}
func (f *fakeDiagnosticQueue) LookupRunByAsyncAckID(context.Context, persistence.Tx, string) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) LoadScratchInTx(context.Context, persistence.Tx, shared.UUID) ([]byte, string, string, error) {
	return nil, "", "", nil
}
func (f *fakeDiagnosticQueue) WriteScratchInTx(context.Context, persistence.Tx, shared.UUID, []byte, string, string) error {
	return nil
}

func TestAdminParkedNodes_ReturnsEntries(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []persistence.ParkedDiagnosticRow{
		{
			InstanceID: "11111111-1111-1111-1111-111111111111",
			NodeID:     "22222222-2222-2222-2222-222222222222",
			ParkedAt:   now,
			ResumeAt:   now.Add(time.Hour),
			Reason:     "snooze",
			ReasonNote: "rate-limit; resume at +1h",
			FrameID:    "33333333-3333-3333-3333-333333333333",
		},
		{
			InstanceID: "11111111-1111-1111-1111-111111111111",
			NodeID:     "44444444-4444-4444-4444-444444444444",
			ParkedAt:   now,
			Reason:     "await_callback",
		},
	}
	deps := AppDeps{
		Persist: noopStore{},
		Queue:   &fakeDiagnosticQueue{rows: rows},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
	}
	app := NewApp(deps)
	srv := httptest.NewServer(app)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/parked-nodes")
	if err != nil {
		t.Fatalf("GET parked-nodes: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got ParkedNodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.ParkedNodes) != 2 {
		t.Fatalf("want 2 parked rows, got %d", len(got.ParkedNodes))
	}

	resp2, err := http.Get(srv.URL + "/v1/admin/diagnostics/parked-nodes?reason=snooze")
	if err != nil {
		t.Fatalf("GET parked-nodes filtered: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var got2 ParkedNodesResponse
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got2.ParkedNodes) != 1 {
		t.Fatalf("filter want 1, got %d", len(got2.ParkedNodes))
	}
	if got2.ParkedNodes[0].Reason != "snooze" {
		t.Fatalf("filter mismatch: %+v", got2.ParkedNodes)
	}
}

func TestAdminHeldFrames_GroupsByFrame(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []persistence.ParkedDiagnosticRow{
		{
			InstanceID: "i1", NodeID: "n1", FrameID: "f1",
			ParkedAt: now.Add(-time.Hour), Reason: "rate_limit",
		},
		{
			InstanceID: "i1", NodeID: "n2", FrameID: "f1",
			ParkedAt: now.Add(-time.Minute), Reason: "human_review",
		},
		{
			InstanceID: "i2", NodeID: "n3", FrameID: "f2",
			ParkedAt: now, Reason: "rate_limit",
		},
	}
	deps := AppDeps{
		Persist: noopStore{},
		Queue:   &fakeDiagnosticQueue{rows: rows},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
	}
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/held-frames")
	if err != nil {
		t.Fatalf("GET held-frames: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got HeldFramesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Frames) != 2 {
		t.Fatalf("want 2 frames, got %d (%+v)", len(got.Frames), got.Frames)
	}
}
