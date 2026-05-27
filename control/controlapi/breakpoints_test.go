// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// breakpoints_test.go — HTTP-level integration tests for the
// instance-debugger breakpoint surface per spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md §4.
//
// Tests exercise the four endpoints end-to-end against the pgtest
// harness. The resume-validation domain logic is tested separately in
// runtime/breakpoint_resume_test.go; here we focus on the transport
// layer (parsing, defaulting, validation gates, sentinel-to-status
// translation, idempotent replay).
//
// @concept: breakpoint

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// seedBPInstance boots a template + instance and returns (templateID, instanceID).
func seedBPInstance(t *testing.T, h *harness, suffix string) (string, string) {
	t.Helper()
	tplBody := validTemplateBody("bp-" + suffix)
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	status, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	status, out = h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "bp-ck-" + suffix,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	return tplID, instID
}

// TestBreakpoint_CreateListDelete drives the basic CRUD shape.
func TestBreakpoint_CreateListDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, instID := seedBPInstance(t, h, uuid.NewString())

	// Create with explicit pause mode and the default overflow_policy
	// (empty body → block_dispatch).
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "root"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)
	require.NotEmpty(t, bpID)
	require.Equal(t, "pause", out["mode"])
	require.Equal(t, "block_dispatch", out["overflow_policy"])
	require.Equal(t, "before_dispatch", out["checkpoint"])

	// List → 1 row.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/instances/%s/breakpoints", instID), nil)
	require.Equal(t, http.StatusOK, status)
	bps, _ := out["breakpoints"].([]any)
	require.Len(t, bps, 1)

	// Delete → 204.
	status, _ = h.httpJSON(t, "DELETE", fmt.Sprintf("/instances/%s/breakpoints/%s", instID, bpID), nil)
	require.Equal(t, http.StatusNoContent, status)

	// List → empty.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/instances/%s/breakpoints", instID), nil)
	require.Equal(t, http.StatusOK, status)
	bps, _ = out["breakpoints"].([]any)
	require.Len(t, bps, 0)
}

// TestBreakpoint_CreateDefaultsForNotifyOnly checks the notify_only
// path resolves overflow_policy to drop_oldest by default per spec §4.8.
func TestBreakpoint_CreateDefaultsForNotifyOnly(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"mode":       "notify_only",
	})
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, "notify_only", out["mode"])
	require.Equal(t, "drop_oldest", out["overflow_policy"])
}

// TestBreakpoint_CreateRejectsIllegalCombinations covers the spec §4.8
// rejection branches.
func TestBreakpoint_CreateRejectsIllegalCombinations(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	cases := []struct {
		name           string
		mode           string
		overflowPolicy string
	}{
		{"pause+drop_oldest", "pause", "drop_oldest"},
		{"notify_only+block_dispatch", "notify_only", "block_dispatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
				"checkpoint":      "before_dispatch",
				"mode":            c.mode,
				"overflow_policy": c.overflowPolicy,
			})
			require.Equal(t, http.StatusBadRequest, status)
		})
	}
}

// TestBreakpoint_CreateRejectsSignalTypeOnBeforeDispatch covers the
// spec §4.5 rule that signal_type is only valid on after_terminal.
func TestBreakpoint_CreateRejectsSignalTypeOnBeforeDispatch(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	signalType := "terminal/error/*"
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint":  "before_dispatch",
		"signal_type": signalType,
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestBreakpoint_CreateAcceptsTrailingWildcardSignal verifies the
// signal package's ValidateSubscriptionType admits trailing-*.
func TestBreakpoint_CreateAcceptsTrailingWildcardSignal(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint":  "after_terminal",
		"signal_type": "terminal/error/*",
		"mode":        "notify_only",
	})
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, "terminal/error/*", out["signal_type"])
}

// TestBreakpoint_CreateRejectsUnknownNodeType verifies matcher
// validation routes through the foundation/matcher package's
// ValidationRefs and the controlapi error translator (400 with
// matcher.ErrInvalid).
func TestBreakpoint_CreateRejectsUnknownNodeType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "not-a-real-node"},
	})
	require.Equal(t, http.StatusBadRequest, status)
}

// TestBreakpoint_DeleteNotFound returns 404 when the breakpoint id
// doesn't exist.
func TestBreakpoint_DeleteNotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	bogus := uuid.NewString()
	status, _ := h.httpJSON(t, "DELETE", fmt.Sprintf("/instances/%s/breakpoints/%s", instID, bogus), nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestBreakpoint_ResumeHitHappyPath drives the resume route. We seed a
// hit directly via the persistence layer (the supervisor-side
// checkpoint write lands in Pass 5; for now this test exercises the
// HTTP transport's parsing + sentinel translation against a hit row
// that was inserted out-of-band).
func TestBreakpoint_ResumeHitHappyPath(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()
	_, instID := seedBPInstance(t, h, uuid.NewString())

	// Create a breakpoint via HTTP.
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpIDStr, _ := out["breakpoint_id"].(string)
	bpID, err := uuid.Parse(bpIDStr)
	require.NoError(t, err)

	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	// Seed a hit directly so the resume handler has something to act on.
	var hitID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := h.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID,
			InstanceID:   shared.UUID(instUUID),
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			Snapshot: map[string]any{
				"dispatch_context": map[string]any{
					"merged_attributes": map[string]any{},
				},
			},
		}, tx)
		if err != nil {
			return err
		}
		hitID = id
		return nil
	}))

	// First resume call returns first_resume=true.
	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": hitID.String(),
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])
	require.Equal(t, true, out["first_resume"])

	// Replay returns first_resume=false (idempotent).
	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": hitID.String(),
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])
	require.Equal(t, false, out["first_resume"])
}

// TestBreakpoint_ResumeMissingHitID returns 404 when the hit id is a
// well-formed UUID that doesn't exist (or doesn't belong to the named
// breakpoint).
func TestBreakpoint_ResumeMissingHitID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)

	bogusHit := uuid.NewString()
	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": bogusHit,
	})
	require.Equal(t, http.StatusNotFound, status)
}

// TestBreakpoint_ResumeBadHitID rejects malformed hit_id with 400.
func TestBreakpoint_ResumeBadHitID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": "not-a-uuid",
	})
	require.Equal(t, http.StatusBadRequest, status)
}
