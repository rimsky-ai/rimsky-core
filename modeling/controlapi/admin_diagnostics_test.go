// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// noopStore is a minimal persistence.Store impl used by admin-diagnostics
// tests that exercise only the route layer (no real DB read). Every
// per-feature accessor returns nil; only Transaction is exercised, and
// it just runs fn with a sentinel Tx.
type noopStore struct{}

type noopTx struct{ persistence.TxMarker }

func (noopStore) Templates() persistence.TemplateStore        { return nil }
func (noopStore) TemplateTags() persistence.TemplateTagsStore { return nil }
func (noopStore) Instances() persistence.InstanceStore        { return nil }
func (noopStore) LifecycleIdempotency() persistence.LifecycleIdempotencyStore {
	return nil
}
func (noopStore) Nodes() persistence.NodeStore                    { return nil }
func (noopStore) LockHolders() persistence.LockHoldersStore       { return nil }
func (noopStore) NodeAttributes() persistence.NodeAttributesStore { return nil }
func (noopStore) ClaimHolders() persistence.ClaimHoldersStore     { return nil }
func (noopStore) Events() persistence.EventStore                  { return nil }
func (noopStore) Schedules() persistence.ScheduleStore            { return nil }
func (noopStore) Supervisors() persistence.SupervisorStore        { return nil }
func (noopStore) Frames() persistence.FrameStore                  { return nil }
func (noopStore) BlobOrphans() persistence.BlobOrphansStore       { return nil }
func (noopStore) NodeEvents() persistence.NodeEventsStore         { return nil }

func (noopStore) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, &noopTx{})
}

// fakeDiagnosticReader returns the supplied rows verbatim.
type fakeDiagnosticReader struct {
	rows []parkedDiagnosticRow
}

func (f *fakeDiagnosticReader) ListParkedNodes(_ context.Context, _ persistence.Tx, reasonFilter string) ([]parkedDiagnosticRow, error) {
	if reasonFilter == "" {
		return f.rows, nil
	}
	var out []parkedDiagnosticRow
	for _, r := range f.rows {
		if r.Reason == reasonFilter {
			out = append(out, r)
		}
	}
	return out, nil
}

// fakeInvalidateHandler returns the configured shape and / or err.
type fakeInvalidateHandler struct {
	result any
	err    error
}

func (f *fakeInvalidateHandler) InvalidateNode(_ context.Context, _, _ string) (any, error) {
	return f.result, f.err
}

func TestAdminParkedNodes_ReturnsEntries(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []parkedDiagnosticRow{
		{
			InstanceID: "11111111-1111-1111-1111-111111111111",
			NodeID:     "22222222-2222-2222-2222-222222222222",
			ParkedAt:   now,
			ResumeAt:   now.Add(time.Hour),
			Reason:     "rate_limit",
			FrameID:    "33333333-3333-3333-3333-333333333333",
		},
		{
			InstanceID: "11111111-1111-1111-1111-111111111111",
			NodeID:     "44444444-4444-4444-4444-444444444444",
			ParkedAt:   now,
			Reason:     "human_review",
		},
	}
	deps := AppDeps{
		Persist:          noopStore{},
		Logger:           shared.SilentLogger{},
		Clock:            shared.SystemClock{},
		DiagnosticReader: &fakeDiagnosticReader{rows: rows},
	}
	app := NewApp(deps)
	srv := httptest.NewServer(app)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/diagnostics/parked-nodes")
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

	// With reason filter.
	resp2, err := http.Get(srv.URL + "/admin/diagnostics/parked-nodes?reason=rate_limit")
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
	if got2.ParkedNodes[0].Reason != "rate_limit" {
		t.Fatalf("filter mismatch: %+v", got2.ParkedNodes)
	}
}

func TestAdminHeldFrames_GroupsByFrame(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []parkedDiagnosticRow{
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
		Persist:          noopStore{},
		Logger:           shared.SilentLogger{},
		Clock:            shared.SystemClock{},
		DiagnosticReader: &fakeDiagnosticReader{rows: rows},
	}
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/diagnostics/held-frames")
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

func TestAdminInvalidateNode_NoHandler503(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		Persist: noopStore{},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
	}
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/admin/instances/11111111-1111-1111-1111-111111111111/nodes/22222222-2222-2222-2222-222222222222/invalidate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST invalidate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", resp.StatusCode)
	}
}

func TestAdminInvalidateNode_Conflict409(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		Persist: noopStore{},
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
	}
	deps.InvalidateHandler = &fakeInvalidateHandler{err: ErrInvalidateConflict}
	srv := httptest.NewServer(NewApp(deps))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/admin/instances/11111111-1111-1111-1111-111111111111/nodes/22222222-2222-2222-2222-222222222222/invalidate",
		"application/json", nil)
	if err != nil {
		t.Fatalf("POST invalidate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "parked or fresh") {
		t.Fatalf("body should describe conflict: %s", body)
	}
}

// Unused imports buster: errors stays present via test code paths.
var _ = errors.Is
