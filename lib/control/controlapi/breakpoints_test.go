// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func seedBPInstance(t *testing.T, h *harness, suffix string) (string, string) {
	t.Helper()
	tplBody := validTemplateBody("bp-" + suffix)
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	status, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "bp-ck-" + suffix,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	return tplID, instID
}

func TestBreakpoint_CreateListDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "root"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)
	require.NotEmpty(t, bpID)
	require.Equal(t, "pause", out["mode"])
	require.Equal(t, "block_dispatch", out["overflow_policy"])
	require.Equal(t, "before_dispatch", out["checkpoint"])

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), nil)
	require.Equal(t, http.StatusOK, status)
	bps, _ := out["breakpoints"].([]any)
	require.Len(t, bps, 1)

	status, _ = h.httpJSON(t, "DELETE", fmt.Sprintf("/v1/instances/%s/breakpoints/%s", instID, bpID), nil)
	require.Equal(t, http.StatusNoContent, status)

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), nil)
	require.Equal(t, http.StatusOK, status)
	bps, _ = out["breakpoints"].([]any)
	require.Len(t, bps, 0)
}

func TestBreakpoint_CreateDefaultsForNotifyOnly(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"mode":       "notify_only",
	})
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, "notify_only", out["mode"])
	require.Equal(t, "drop_oldest", out["overflow_policy"])
}

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
			status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
				"checkpoint":      "before_dispatch",
				"mode":            c.mode,
				"overflow_policy": c.overflowPolicy,
			})
			require.Equal(t, http.StatusBadRequest, status)
		})
	}
}

func TestBreakpoint_CreateRejectsSignalTypeOnBeforeDispatch(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	signalType := "terminal/error/*"
	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint":  "before_dispatch",
		"signal_type": signalType,
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestBreakpoint_CreateAcceptsTrailingWildcardSignal(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint":  "after_terminal",
		"signal_type": "terminal/error/*",
		"mode":        "notify_only",
	})
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, "terminal/error/*", out["signal_type"])
}

func TestBreakpoint_CreateRejectsRetiredEventTypePath(t *testing.T) {
	t.Parallel()
	cases := []string{"event/discovered", "event/*"}
	for _, sig := range cases {
		sig := sig
		t.Run(sig, func(t *testing.T) {
			t.Parallel()
			h, teardown := newHarness(t)
			t.Cleanup(teardown)
			_, instID := seedBPInstance(t, h, uuid.NewString())

			status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
				"checkpoint":  "after_terminal",
				"signal_type": sig,
				"mode":        "notify_only",
			})
			require.Equal(t, http.StatusBadRequest, status,
				"signal_type=%q must reject — event/<name> retired under TD-collapse-named-event-to-tags", sig)
		})
	}
}

func TestBreakpoint_CreateRejectsUnknownNodeType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, _ := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "not-a-real-node"},
	})
	require.Equal(t, http.StatusBadRequest, status)
}

func TestBreakpoint_DeleteNotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	bogus := uuid.NewString()
	status, _ := h.httpJSON(t, "DELETE", fmt.Sprintf("/v1/instances/%s/breakpoints/%s", instID, bogus), nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestBreakpoint_DeleteWrongInstance404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instA := seedBPInstance(t, h, uuid.NewString())
	_, instB := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instA), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)
	require.NotEmpty(t, bpID)

	status, _ = h.httpJSON(t, "DELETE", fmt.Sprintf("/v1/instances/%s/breakpoints/%s", instB, bpID), nil)
	require.Equal(t, http.StatusNotFound, status,
		"a breakpoint that exists but belongs to a different instance must 404, not succeed")

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoints", instA), nil)
	require.Equal(t, http.StatusOK, status)
	bps, _ := out["breakpoints"].([]any)
	require.Len(t, bps, 1, "the breakpoint must survive the cross-instance delete attempt")
}

func TestBreakpoint_ResumeHitWrongBreakpoint404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := shared.UUID(uuid.MustParse(instID))

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpAStr, _ := out["breakpoint_id"].(string)
	bpA, err := uuid.Parse(bpAStr)
	require.NoError(t, err)

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpBStr, _ := out["breakpoint_id"].(string)
	require.NotEmpty(t, bpBStr)

	var hitID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := h.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: shared.UUID(bpA),
			InstanceID:   instUUID,
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

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpBStr), map[string]any{
		"hit_id": hitID.String(),
	})
	require.Equal(t, http.StatusNotFound, status,
		"a hit belonging to a different breakpoint must 404, not resume")
}

func TestBreakpoint_ResumeHitHappyPath(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpIDStr, _ := out["breakpoint_id"].(string)
	bpID, err := uuid.Parse(bpIDStr)
	require.NoError(t, err)

	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

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

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": hitID.String(),
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])
	require.Equal(t, true, out["first_resume"])

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": hitID.String(),
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])
	require.Equal(t, false, out["first_resume"])
}

func TestBreakpoint_ListHits_HTTPMirrorsMCPResource(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := shared.UUID(uuid.MustParse(instID))

	bpID := createBreakpointForRead(t, h, instID)
	base := time.Now().UTC().Add(-3 * time.Minute)
	_, seq1 := seedBPHit(t, h, bpID, instUUID, base)
	_, seq2 := seedBPHit(t, h, bpID, instUUID, base.Add(time.Minute))
	_, seq3 := seedBPHit(t, h, bpID, instUUID, base.Add(2*time.Minute))
	require.Less(t, seq1, seq2)
	require.Less(t, seq2, seq3)

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoint-hits", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	hits, _ := out["hits"].([]any)
	require.Len(t, hits, 3)
	require.EqualValues(t, seq3, int64(out["next_since"].(float64)))
	require.Equal(t, false, out["truncated"])

	first, _ := hits[0].(map[string]any)
	require.EqualValues(t, seq1, int64(first["seq"].(float64)))
	require.NotEmpty(t, first["hit_id"])
	require.Equal(t, instID, first["instance_id"])
	require.Equal(t, "before_dispatch", first["checkpoint"])
	require.NotNil(t, first["dispatch_context"])

	cat := buildResourceCatalog(h)
	mcpReq := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*:read"}}})
	contents, rpcErr := cat.Read(mcpReq, fmt.Sprintf("rimsky://instances/%s/breakpoint-hits", instID))
	require.Nil(t, rpcErr, "mcp read failed: %+v", rpcErr)
	var mcpBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &mcpBody))
	httpJSONBytes, err := json.Marshal(out)
	require.NoError(t, err)
	var httpBody map[string]any
	require.NoError(t, json.Unmarshal(httpJSONBytes, &httpBody))
	require.Equal(t, mcpBody, httpBody, "HTTP route and MCP resource must return identical breakpoint-hits payloads")

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoint-hits?since=%d", instID, seq1), nil)
	require.Equal(t, http.StatusOK, status, out)
	hits, _ = out["hits"].([]any)
	require.Len(t, hits, 2)
	require.EqualValues(t, seq3, int64(out["next_since"].(float64)))
	require.Equal(t, false, out["truncated"])

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoint-hits?limit=2", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	hits, _ = out["hits"].([]any)
	require.Len(t, hits, 2)
	require.EqualValues(t, seq2, int64(out["next_since"].(float64)))
	require.Equal(t, true, out["truncated"])

	status, _ = h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoint-hits?since=-1", instID), nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestBreakpoint_ListHits_InstanceNotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "GET", fmt.Sprintf("/v1/instances/%s/breakpoint-hits", uuid.NewString()), nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestBreakpoint_ResumeMissingHitID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)

	bogusHit := uuid.NewString()
	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": bogusHit,
	})
	require.Equal(t, http.StatusNotFound, status)
}

func TestBreakpoint_ResumeBadHitID(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, http.StatusCreated, status, out)
	bpID, _ := out["breakpoint_id"].(string)

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints/%s/resume", instID, bpID), map[string]any{
		"hit_id": "not-a-uuid",
	})
	require.Equal(t, http.StatusBadRequest, status)
}
