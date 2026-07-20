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

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type fakeWaitSetTable struct {
	byFrame    []persistence.WaitSetRow
	byReceiver []persistence.WaitSetRow

	gotFrameID     shared.UUID
	gotReceiverID  shared.UUID
	receiverCalled bool
	frameCalled    bool
}

func (f *fakeWaitSetTable) Insert(context.Context, persistence.WaitSetRow, persistence.Tx) error {
	return nil
}

func (f *fakeWaitSetTable) MarkDrainedBySender(context.Context, shared.UUID, shared.UUID, persistence.Tx) error {
	return nil
}

func (f *fakeWaitSetTable) ListForReceiver(_ context.Context, frameID, receiverNodeRunID shared.UUID, _ persistence.Tx) ([]persistence.WaitSetRow, error) {
	f.receiverCalled = true
	f.gotFrameID = frameID
	f.gotReceiverID = receiverNodeRunID
	return f.byReceiver, nil
}

func (f *fakeWaitSetTable) ListForFrame(_ context.Context, frameID shared.UUID, _ persistence.Tx) ([]persistence.WaitSetRow, error) {
	f.frameCalled = true
	f.gotFrameID = frameID
	return f.byFrame, nil
}

func (f *fakeWaitSetTable) ListSenderNodesForReceiver(context.Context, shared.UUID, shared.UUID, persistence.Tx) ([]shared.UUID, error) {
	return nil, nil
}

func (f *fakeWaitSetTable) HasRowForSenderRun(context.Context, shared.UUID, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}

func (f *fakeWaitSetTable) ListPendingReceiversForDrainedSender(context.Context, shared.UUID, shared.UUID, persistence.Tx) ([]shared.UUID, error) {
	return nil, nil
}

func (f *fakeWaitSetTable) HasUndrainedRowsForReceiver(context.Context, shared.UUID, shared.UUID, persistence.Tx) (bool, error) {
	return false, nil
}

type waitSetStore struct {
	noopStore
	table *fakeWaitSetTable
}

func (s waitSetStore) WaitSet() persistence.WaitSetTable { return s.table }

func newWaitSetTestApp(table *fakeWaitSetTable) *httptest.Server {
	deps := AppDeps{
		Persist: waitSetStore{table: table},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
		AuthState: &AuthState{
			Tables:   waitSetStore{table: table},
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
	}
	return httptest.NewServer(NewApp(deps))
}

func TestAdminWaitSets_MissingFrameParam(t *testing.T) {
	t.Parallel()
	srv := newWaitSetTestApp(&fakeWaitSetTable{})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/wait-sets")
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminWaitSets_InvalidFrameUUID(t *testing.T) {
	t.Parallel()
	srv := newWaitSetTestApp(&fakeWaitSetTable{})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/wait-sets?frame=not-a-uuid")
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminWaitSets_InvalidReceiverRunUUID(t *testing.T) {
	t.Parallel()
	srv := newWaitSetTestApp(&fakeWaitSetTable{})
	defer srv.Close()

	frameID := uuid.New().String()
	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/wait-sets?frame=" + frameID + "&receiver_run=not-a-uuid")
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAdminWaitSets_ListForFrame_WhenNoReceiverGiven(t *testing.T) {
	t.Parallel()
	frameID := uuid.New()
	senderID := uuid.New()
	receiverID := uuid.New()
	table := &fakeWaitSetTable{
		byFrame: []persistence.WaitSetRow{
			{
				FrameID:           frameID,
				ReceiverNodeRunID: receiverID,
				SenderNodeRunID:   senderID,
				TopicKind:         "attribute",
				TopicFilter:       json.RawMessage(`{"path":"x"}`),
			},
		},
	}
	srv := newWaitSetTestApp(table)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/wait-sets?frame=" + frameID.String())
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !table.frameCalled || table.receiverCalled {
		t.Fatalf("want ListForFrame called and ListForReceiver not called, got frameCalled=%v receiverCalled=%v", table.frameCalled, table.receiverCalled)
	}

	var got WaitSetResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.WaitSet) != 1 {
		t.Fatalf("want 1 wait-set entry, got %d", len(got.WaitSet))
	}
	if got.WaitSet[0].TopicKind != "attribute" {
		t.Fatalf("topic_kind = %q, want attribute", got.WaitSet[0].TopicKind)
	}
	filterMap, ok := got.WaitSet[0].TopicFilter.(map[string]any)
	if !ok || filterMap["path"] != "x" {
		t.Fatalf("topic_filter not decoded correctly: %+v", got.WaitSet[0].TopicFilter)
	}
}

func TestAdminWaitSets_ListForReceiver_WhenReceiverGiven(t *testing.T) {
	t.Parallel()
	frameID := uuid.New()
	receiverID := uuid.New()
	senderID := uuid.New()
	table := &fakeWaitSetTable{
		byReceiver: []persistence.WaitSetRow{
			{
				FrameID:           frameID,
				ReceiverNodeRunID: receiverID,
				SenderNodeRunID:   senderID,
				TopicKind:         "signal",
			},
		},
		byFrame: []persistence.WaitSetRow{
			{FrameID: frameID, ReceiverNodeRunID: uuid.New(), SenderNodeRunID: uuid.New(), TopicKind: "unrelated"},
		},
	}
	srv := newWaitSetTestApp(table)
	defer srv.Close()

	url := srv.URL + "/v1/admin/diagnostics/wait-sets?frame=" + frameID.String() + "&receiver_run=" + receiverID.String()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !table.receiverCalled || table.frameCalled {
		t.Fatalf("want ListForReceiver called and ListForFrame not called, got receiverCalled=%v frameCalled=%v", table.receiverCalled, table.frameCalled)
	}
	if table.gotReceiverID != receiverID {
		t.Fatalf("gotReceiverID = %v, want %v", table.gotReceiverID, receiverID)
	}

	var got WaitSetResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.WaitSet) != 1 {
		t.Fatalf("want 1 wait-set entry, got %d", len(got.WaitSet))
	}
	if got.WaitSet[0].TopicKind != "signal" {
		t.Fatalf("topic_kind = %q, want signal", got.WaitSet[0].TopicKind)
	}
	if got.WaitSet[0].TopicFilter != nil {
		t.Fatalf("topic_filter should be omitted when absent, got %+v", got.WaitSet[0].TopicFilter)
	}
}

func TestAdminWaitSets_MalformedTopicFilterLogsDebugAndYieldsNull(t *testing.T) {
	t.Parallel()
	frameID := uuid.New()
	table := &fakeWaitSetTable{
		byFrame: []persistence.WaitSetRow{
			{
				FrameID:           frameID,
				ReceiverNodeRunID: uuid.New(),
				SenderNodeRunID:   uuid.New(),
				TopicKind:         "attribute",
				TopicFilter:       json.RawMessage(`not-json`),
			},
		},
	}
	logger := shared.NewCapturingLogger()
	deps := AppDeps{
		Persist: waitSetStore{table: table},
		Logger:  logger,
		Clock:   shared.SystemClock{},
		AuthState: &AuthState{
			Tables:   waitSetStore{table: table},
			Registry: BuildV1Registry(),
			Clock:    shared.SystemClock{},
			Logger:   shared.SilentLogger{},
		},
	}
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/admin/diagnostics/wait-sets?frame=" + frameID.String())
	if err != nil {
		t.Fatalf("GET wait-sets: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got WaitSetResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.WaitSet[0].TopicFilter != nil {
		t.Fatalf("topic_filter should be nil for malformed JSON, got %+v", got.WaitSet[0].TopicFilter)
	}

	found := false
	for _, rec := range logger.Records() {
		if rec.Msg == "admin.wait_set.topic_filter_decode_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a debug log for the malformed topic_filter decode, got records: %+v", logger.Records())
	}
}
