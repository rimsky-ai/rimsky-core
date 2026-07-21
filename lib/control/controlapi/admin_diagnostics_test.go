// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
func (noopStore) Nodes() persistence.NodeTable                                   { return noopNodes{} }
func (noopStore) ClaimHandles() persistence.ClaimHandleTable                     { return nil }
func (noopStore) NodeAttributes() persistence.NodeAttributeTable                 { return nil }
func (noopStore) ClaimHolders() persistence.ClaimHolderTable                     { return nil }
func (noopStore) Events() persistence.EventTable                                 { return fakeEvents{} }
func (noopStore) Supervisors() persistence.SupervisorTable                       { return nil }
func (noopStore) Frames() persistence.FrameTable                                 { return nil }
func (noopStore) BlobOrphans() persistence.BlobOrphanTable                       { return nil }
func (noopStore) WaitSet() persistence.WaitSetTable                              { return nil }
func (noopStore) Messages() persistence.MessageTable                             { return nil }
func (noopStore) MessageIdempotencies() persistence.MessageIdempotencyTable      { return nil }
func (noopStore) Lineage() persistence.LineageTable                              { return nil }
func (noopStore) PublisherSubscriptions() persistence.PublisherSubscriptionTable { return nil }
func (noopStore) NodeRunTree() persistence.NodeRunTreeTable                      { return nil }
func (noopStore) RunScopes() persistence.RunScopeTable                           { return nil }
func (noopStore) APIKeys() persistence.APIKeyTable                               { return fakeAPIKeys{} }
func (noopStore) DeploymentCA() persistence.DeploymentCATable                    { return nil }
func (noopStore) Breakpoints() persistence.BreakpointTable                       { return nil }
func (noopStore) BreakpointHits() persistence.BreakpointHitTable                 { return nil }

func (noopStore) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, &noopTx{})
}

type fakeEvents struct{}

func (fakeEvents) Append(context.Context, persistence.EventAppendInput, persistence.Tx) error {
	return nil
}
func (fakeEvents) List(context.Context, persistence.EventListFilter, persistence.ListPagination, persistence.Tx) (persistence.EventListResult, error) {
	return persistence.EventListResult{}, nil
}
func (fakeEvents) LastTerminalByNodes(context.Context, []shared.UUID, persistence.Tx) (map[shared.UUID]persistence.EventRow, error) {
	return nil, nil
}
func (fakeEvents) CountAttributeOverrideMatchesByIndex(context.Context, shared.UUID, persistence.Tx) (map[int64]int64, error) {
	return nil, nil
}
func (fakeEvents) DeleteOlderThan(context.Context, time.Time) (int, error) {
	return 0, nil
}

type fakeAPIKeys struct{}

func (fakeAPIKeys) TxOptional() bool { return true }

func (fakeAPIKeys) Insert(context.Context, persistence.APIKey, persistence.Tx) error { return nil }
func (fakeAPIKeys) GetByID(context.Context, shared.UUID, persistence.Tx) (persistence.APIKey, bool, error) {
	return persistence.APIKey{}, false, nil
}
func (fakeAPIKeys) GetByName(context.Context, string, persistence.Tx) (persistence.APIKey, bool, error) {
	return persistence.APIKey{}, false, nil
}
func (fakeAPIKeys) GetByHash(context.Context, []byte, persistence.Tx) (persistence.APIKey, bool, error) {
	return persistence.APIKey{}, false, nil
}
func (fakeAPIKeys) List(context.Context, bool, string, persistence.Tx) ([]persistence.APIKey, error) {
	return nil, nil
}
func (fakeAPIKeys) ActiveCount(context.Context, time.Time, persistence.Tx) (int, error) {
	return 0, nil
}
func (fakeAPIKeys) MarkRevoked(context.Context, shared.UUID, time.Time, persistence.Tx) (bool, bool, error) {
	return false, false, nil
}
func (fakeAPIKeys) RevokeIfNotLast(context.Context, shared.UUID, time.Time, bool, persistence.Tx) (persistence.RevokeResult, error) {
	return persistence.RevokeResultNotFound, nil
}
func (fakeAPIKeys) SetRevokeAt(context.Context, shared.UUID, time.Time, persistence.Tx) (bool, error) {
	return false, nil
}
func (fakeAPIKeys) SweepRotationGrace(context.Context, time.Time, persistence.Tx) ([]persistence.APIKey, error) {
	return nil, nil
}
func (fakeAPIKeys) UpdateLastUsed(context.Context, shared.UUID, time.Time, persistence.Tx) error {
	return nil
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
func (noopNodes) ListByInstancePagedFiltered(context.Context, shared.UUID, persistence.ListPagination, persistence.NodeListFilter, persistence.Tx) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	return persistence.PaginatedListResult[persistence.NodeRow]{}, nil
}
func (noopNodes) ListReadyForDispatch(context.Context, persistence.Tx) ([]persistence.NodeRow, error) {
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
func (noopNodes) UpdateState(context.Context, shared.UUID, cascade.NodeState, cascade.TransitionReason, *string, persistence.Tx) error {
	return nil
}
func (noopNodes) UpdateError(context.Context, shared.UUID, spec.EvaluatorState, persistence.Tx) error {
	return nil
}
func (noopNodes) UpdateHeartbeat(context.Context, shared.UUID, shared.UUID, time.Time, string, persistence.Tx) error {
	return nil
}
func (noopNodes) ResetFailedTerminalSettlingSignalType(context.Context, shared.UUID, shared.UUID, persistence.Tx) error {
	return nil
}
func (noopNodes) GetFailedTerminalRunScopeID(context.Context, shared.UUID, persistence.Tx) (*shared.UUID, error) {
	return nil, nil
}
func (noopNodes) HasRunForNodeInFrame(context.Context, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}
func (noopNodes) HasAdvancedSiblingInScope(context.Context, persistence.Tx, shared.UUID, shared.UUID, shared.UUID) (bool, error) {
	return false, nil
}
func (noopNodes) ListPendingSiblingRunsInScope(context.Context, persistence.Tx, shared.UUID) ([]shared.UUID, error) {
	return nil, nil
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
func (noopNodes) GetRunSummaryForNodes(context.Context, []shared.UUID, persistence.Tx) (map[shared.UUID]persistence.NodeRunSummary, error) {
	return nil, nil
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
func (noopNodes) GetLatestRunForNodes(context.Context, persistence.Tx, []shared.UUID) (map[shared.UUID]persistence.NodeRunLatest, error) {
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
func (noopNodes) HasLaterCascadePending(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (bool, error) {
	return false, nil
}
func (noopNodes) ListPendingRunsInScopeForNodes(context.Context, persistence.Tx, shared.UUID, []shared.UUID) ([]shared.UUID, error) {
	return nil, nil
}
func (noopNodes) GetPriorCascadeQueuedNotClaimed(context.Context, persistence.Tx, shared.UUID, shared.UUID, int64) (*persistence.NodeRunForGate, error) {
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
func (noopNodes) SetRunRequiredStores(context.Context, persistence.Tx, shared.UUID, []string) (bool, error) {
	return false, nil
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

func (f *fakeDiagnosticQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	return f.rows, nil
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
func (f *fakeDiagnosticQueue) ForceComplete(context.Context, shared.UUID) error {
	return nil
}
func (f *fakeDiagnosticQueue) RemoveForNodeInTx(context.Context, shared.UUID, shared.UUID, string, persistence.Tx) error {
	return nil
}
func (f *fakeDiagnosticQueue) ForceRemoveForNode(context.Context, shared.UUID, shared.UUID) error {
	return nil
}
func (f *fakeDiagnosticQueue) ForceRemoveForNodeInTx(context.Context, shared.UUID, shared.UUID, persistence.Tx) error {
	return nil
}
func (f *fakeDiagnosticQueue) ListOrphanedClaims(context.Context) ([]persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) StampPriorDispatchInTx(context.Context, persistence.Tx, shared.UUID, shared.UUID, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) ReleaseClaimWithDisposition(context.Context, shared.UUID, string, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) ReleaseClaim(context.Context, shared.UUID, string) error {
	return nil
}
func (f *fakeDiagnosticQueue) ForceReleaseClaim(context.Context, shared.UUID) error {
	return nil
}
func (f *fakeDiagnosticQueue) GetClaimedBy(context.Context, shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{}, nil
}
func (f *fakeDiagnosticQueue) GetDispatchNode(context.Context, shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{}, nil
}
func (f *fakeDiagnosticQueue) GetDispatchNodeInTx(context.Context, persistence.Tx, shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{}, nil
}
func (f *fakeDiagnosticQueue) RefreshHeartbeat(context.Context, string) error { return nil }
func (f *fakeDiagnosticQueue) ListLive(context.Context, persistence.DispatchListFilter, persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	return persistence.PaginatedListResult[persistence.DispatchRow]{}, nil
}
func (f *fakeDiagnosticQueue) CountLive(context.Context, persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *fakeDiagnosticQueue) CountParked(context.Context) (int, error) {
	return 0, nil
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
func (f *fakeDiagnosticQueue) GetParkedByNode(context.Context, persistence.Tx, shared.UUID, shared.UUID) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeDiagnosticQueue) ResumeParkedInTx(context.Context, persistence.Tx, shared.UUID) (bool, error) {
	return false, nil
}
func (f *fakeDiagnosticQueue) UpdateDispatchTuningInTx(context.Context, persistence.Tx, shared.UUID, *int) error {
	return nil
}
func (f *fakeDiagnosticQueue) BumpLastProgressAt(context.Context, persistence.Tx, shared.UUID, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeDiagnosticQueue) RegisterAsyncAck(context.Context, persistence.Tx, shared.UUID, string, time.Time, *int, *int, string) error {
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

type fakeOutboxStore struct {
	noopStore
	rows []persistence.ProducerVerbOutboxRow
}

func (s fakeOutboxStore) ProducerVerbOutbox() persistence.ProducerVerbOutboxTable {
	return fakeOutboxTable{rows: s.rows}
}

type fakeOutboxTable struct {
	rows []persistence.ProducerVerbOutboxRow
}

func (f fakeOutboxTable) Enqueue(context.Context, persistence.ProducerVerbOutboxInsertInput, persistence.Tx) error {
	return nil
}
func (f fakeOutboxTable) ListAll(context.Context, persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	return f.rows, nil
}
func (f fakeOutboxTable) ListByProducer(context.Context, string, persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	return nil, nil
}
func (f fakeOutboxTable) RecordAttempt(context.Context, int64, time.Time, string, persistence.Tx) error {
	return nil
}
func (f fakeOutboxTable) Delete(context.Context, int64, persistence.Tx) error { return nil }
func (f fakeOutboxTable) CountByProducer(context.Context, persistence.Tx) (map[string]int, error) {
	return nil, nil
}

func outboxDiagnosticsDeps(store persistence.Tables, clock shared.Clock) AppDeps {
	return AppDeps{
		Persist: store,
		Queue:   &fakeDiagnosticQueue{},
		Logger:  shared.SilentLogger{},
		Clock:   clock,
		AuthState: &AuthState{
			Tables:   noopStore{},
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
	}
}

func TestAdminProducerOutbox_ReportsDepthOldestAgeAndEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	instanceID := shared.UUID{0xAA}
	rows := []persistence.ProducerVerbOutboxRow{
		{
			Seq:           1,
			ClaimHandleID: shared.UUID{0x01},
			ProducerName:  "store-a",
			Verb:          persistence.ProducerVerbCommit,
			InstanceID:    &instanceID,
			AttemptCount:  3,
			NextAttemptAt: now.Add(30 * time.Second),
			LastError:     "dial tcp: connection refused",
			EnqueuedAt:    now.Add(-90 * time.Second),
		},
		{
			Seq:           2,
			ClaimHandleID: shared.UUID{0x02},
			ProducerName:  "store-b",
			Verb:          persistence.ProducerVerbAbandon,
			AttemptCount:  0,
			NextAttemptAt: now,
			EnqueuedAt:    now.Add(-10 * time.Second),
		},
	}
	deps := outboxDiagnosticsDeps(fakeOutboxStore{rows: rows}, shared.NewControllableClock(now))
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/producer-outbox")
	if err != nil {
		t.Fatalf("GET producer-outbox: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got ProducerOutboxResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Depth != 2 || len(got.Entries) != 2 {
		t.Fatalf("want depth=2 with 2 entries, got depth=%d entries=%d", got.Depth, len(got.Entries))
	}
	if got.OldestEnqueuedAt == nil || !got.OldestEnqueuedAt.Equal(now.Add(-90*time.Second)) {
		t.Fatalf("oldest_enqueued_at: want %v, got %v", now.Add(-90*time.Second), got.OldestEnqueuedAt)
	}
	if got.OldestAgeSeconds == nil || *got.OldestAgeSeconds != 90 {
		t.Fatalf("oldest_age_seconds: want 90, got %v", got.OldestAgeSeconds)
	}
	first := got.Entries[0]
	if first.Seq != 1 || first.ProducerName != "store-a" || first.Verb != "commit" ||
		first.AttemptCount != 3 || first.LastError != "dial tcp: connection refused" {
		t.Fatalf("entry[0] fields drifted: %+v", first)
	}
	if first.InstanceID == nil || *first.InstanceID != instanceID.String() {
		t.Fatalf("entry[0].instance_id: want %s, got %v", instanceID.String(), first.InstanceID)
	}
	if got.Entries[1].InstanceID != nil {
		t.Fatalf("entry[1].instance_id should be omitted, got %v", *got.Entries[1].InstanceID)
	}
}

func TestAdminProducerOutbox_EmptyOutbox(t *testing.T) {
	t.Parallel()
	deps := outboxDiagnosticsDeps(fakeOutboxStore{}, shared.SystemClock{})
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/producer-outbox")
	if err != nil {
		t.Fatalf("GET producer-outbox: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got ProducerOutboxResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Depth != 0 || len(got.Entries) != 0 {
		t.Fatalf("want empty outbox, got depth=%d entries=%d", got.Depth, len(got.Entries))
	}
	if got.OldestEnqueuedAt != nil || got.OldestAgeSeconds != nil {
		t.Fatalf("oldest fields must be omitted on an empty outbox, got %v / %v",
			got.OldestEnqueuedAt, got.OldestAgeSeconds)
	}
}

func TestAdminProducerOutbox_StoreWithoutOutboxFailsLoudly(t *testing.T) {
	t.Parallel()
	deps := outboxDiagnosticsDeps(noopStore{}, shared.SystemClock{})
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/producer-outbox")
	if err != nil {
		t.Fatalf("GET producer-outbox: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a store without a producer-verb outbox must not report success")
	}
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
			FrameID:    "33333333-3333-3333-3333-333333333333",
		},
		{
			InstanceID: "11111111-1111-1111-1111-111111111111",
			NodeID:     "44444444-4444-4444-4444-444444444444",
			ParkedAt:   now,
		},
	}
	deps := AppDeps{
		Persist: noopStore{},
		Queue:   &fakeDiagnosticQueue{rows: rows},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
		AuthState: &AuthState{
			Tables:   noopStore{},
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
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

	raw, err := json.Marshal(got.ParkedNodes[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, retired := range []string{"reason", "reason_note"} {
		if strings.Contains(string(raw), `"`+retired+`"`) {
			t.Fatalf("parked-nodes entry must not carry the retired %q field: %s", retired, raw)
		}
	}
}

func TestAdminHeldFrames_GroupsByFrame(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []persistence.ParkedDiagnosticRow{
		{
			InstanceID: "i1", NodeID: "n1", FrameID: "f1",
			ParkedAt: now.Add(-time.Hour),
		},
		{
			InstanceID: "i1", NodeID: "n2", FrameID: "f1",
			ParkedAt: now.Add(-time.Minute),
		},
		{
			InstanceID: "i2", NodeID: "n3", FrameID: "f2",
			ParkedAt: now,
		},
	}
	deps := AppDeps{
		Persist: noopStore{},
		Queue:   &fakeDiagnosticQueue{rows: rows},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
		AuthState: &AuthState{
			Tables:   noopStore{},
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
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
