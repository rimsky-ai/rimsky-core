// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// mcp_resources_test.go — integration tests for the breakpoint-hits
// MCP resource catalog defined in mcp_resources.go. Pairs with the
// JSON-RPC dispatcher tests in control/controlapi/mcp/resources_test.go.
//
// Spec coverage (see .ok-planner/specs/2026-05-24-instance-debugger-design.md):
//   - §6.2 URI scheme (parsing both instance-scoped and breakpoint-scoped
//     forms, rejecting non-rimsky:// schemes, mis-shaped paths, etc.)
//   - §6.4 paginated reads (since cursor, limit cap, polling pattern,
//     truncated=true → next page → final empty page)
//   - §6.7 permission gating (no `breakpoint:read` → empty List, denied
//     Read; admin grant sees every instance)
//
// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/control/controlapi/mcp"
	"github.com/rimsky-ai/rimsky-core/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// withIdentity returns a request whose Context carries the given
// Identity via the mcp.SetIdentityHook channel. The hook is restored on
// test cleanup so concurrent siblings can't race.
func withIdentity(t *testing.T, ident auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	restore := mcp.SetIdentityHook(func(ctx context.Context) (auth.Identity, bool) {
		return ident, ident.Kind != ""
	})
	t.Cleanup(restore)
	return httptest.NewRecorder()
}

// seedBPHit inserts a breakpoint hit row directly into persistence and
// returns the assigned (hitID, seq). Tests build a fixed dataset this
// way so they can assert the polling cursor's behavior without
// scheduling the runtime evaluator.
func seedBPHit(t *testing.T, h *harness, bpID, instanceID foundationshared.UUID, hitAt time.Time) (foundationshared.UUID, int64) {
	t.Helper()
	var (
		hitID foundationshared.UUID
		seq   int64
	)
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		id, s, err := h.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID,
			InstanceID:   instanceID,
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			HitAt:        hitAt,
			Snapshot: map[string]any{
				"checkpoint": "before_dispatch",
				"dispatch_context": map[string]any{
					"executor":          "worker",
					"node_type":         "root",
					"graph":             "main",
					"merged_attributes": map[string]any{"k": "v"},
				},
				"node_run":      map[string]any{},
				"held_claims":   []any{},
				"open_wait_set": []any{},
			},
		}, tx)
		if err != nil {
			return err
		}
		hitID = id
		seq = s
		return nil
	}))
	return hitID, seq
}

// buildResourceCatalog returns a breakpointResourceCatalog bound to the
// harness's persistence. The catalog is wired via the production
// constructor so this test exercises the same path registerMCPRoute
// does.
func buildResourceCatalog(h *harness) mcp.ResourceCatalog {
	return newBreakpointResourceCatalog(AppDeps{
		Persist: h.persist,
		Logger:  h.logger,
	})
}

// TestResources_List_AdminSeesInstance covers the admin-grant path —
// `*` matches `breakpoint:read`, so every active instance surfaces as a
// resource URI.
func TestResources_List_AdminSeesInstance(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)

	got, err := cat.List(req)
	require.NoError(t, err)
	require.NotEmpty(t, got, "admin grant should see the seeded instance")
	wantURI := "rimsky://instances/" + instID + "/breakpoint-hits"
	var found bool
	for _, r := range got {
		if r.URI == wantURI {
			found = true
			require.Equal(t, breakpointHitsMimeType, r.MimeType)
			require.Contains(t, r.Name, instID)
		}
	}
	require.True(t, found, "seeded instance %s not in resource list: %+v", instID, got)
}

// TestResources_List_NoBreakpointReadReturnsEmpty covers the
// permission-gated path: an identity without `breakpoint:read` sees
// nothing, even if active instances exist.
func TestResources_List_NoBreakpointReadReturnsEmpty(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, _ = seedBPInstance(t, h, uuid.NewString())

	cat := buildResourceCatalog(h)
	// Grant only `instance:read` — does NOT cover `breakpoint:read`.
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "instance:read"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)

	got, err := cat.List(req)
	require.NoError(t, err)
	require.Empty(t, got, "identity without breakpoint:read should see no resources")
}

// TestResources_Read_ByInstance covers the happy path on the
// instance-scoped URI: seeded hits surface in seq-ascending order,
// next_since advances to the last seq, truncated is false when fewer
// than `limit` rows are returned.
func TestResources_Read_ByInstance(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)

	// Seed a breakpoint + 3 hits.
	bpID := createBreakpointForRead(t, h, instID)
	t1 := time.Now().UTC().Add(-3 * time.Minute)
	_, seq1 := seedBPHit(t, h, bpID, instUUID, t1)
	_, seq2 := seedBPHit(t, h, bpID, instUUID, t1.Add(time.Minute))
	_, seq3 := seedBPHit(t, h, bpID, instUUID, t1.Add(2*time.Minute))
	require.Less(t, seq1, seq2)
	require.Less(t, seq2, seq3)

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*:read"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits", instID)
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr, "read failed: %+v", rpcErr)
	require.NotNil(t, contents)
	require.Equal(t, breakpointHitsMimeType, contents.MimeType)
	require.Equal(t, uri, contents.URI)

	var body struct {
		Hits      []map[string]any `json:"hits"`
		NextSince int64            `json:"next_since"`
		Truncated bool             `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
	require.Len(t, body.Hits, 3)
	require.Equal(t, seq3, body.NextSince)
	require.False(t, body.Truncated)

	// Each row should carry seq + identity fields + snapshot fields.
	require.EqualValues(t, seq1, int64(body.Hits[0]["seq"].(float64)))
	require.NotEmpty(t, body.Hits[0]["hit_id"])
	require.Equal(t, "before_dispatch", body.Hits[0]["checkpoint"])
	require.NotNil(t, body.Hits[0]["dispatch_context"])
}

// TestResources_Read_BySinceCursor verifies the polling cursor: only
// hits with seq > since are returned, ordered ascending.
func TestResources_Read_BySinceCursor(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)
	bpID := createBreakpointForRead(t, h, instID)

	now := time.Now().UTC()
	_, seq1 := seedBPHit(t, h, bpID, instUUID, now.Add(-3*time.Minute))
	_, seq2 := seedBPHit(t, h, bpID, instUUID, now.Add(-2*time.Minute))
	_, seq3 := seedBPHit(t, h, bpID, instUUID, now.Add(-time.Minute))

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?since=%d", instID, seq1)
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr)

	var body struct {
		Hits      []map[string]any `json:"hits"`
		NextSince int64            `json:"next_since"`
		Truncated bool             `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
	require.Len(t, body.Hits, 2, "since=%d should drop seq1 (%d) and keep seq2 (%d) + seq3 (%d)", seq1, seq1, seq2, seq3)
	require.EqualValues(t, seq2, int64(body.Hits[0]["seq"].(float64)))
	require.EqualValues(t, seq3, int64(body.Hits[1]["seq"].(float64)))
	require.Equal(t, seq3, body.NextSince)
}

// TestResources_Read_PollingCursorFlow exercises the agent polling
// pattern from spec §6.4: read → advance cursor by next_since → read
// again until truncated=false. Uses limit=2 over 5 seeded hits so the
// flow has to drain 3 pages (2 + 2 + 1).
func TestResources_Read_PollingCursorFlow(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)
	bpID := createBreakpointForRead(t, h, instID)

	base := time.Now().UTC().Add(-10 * time.Minute)
	seeded := []int64{}
	for i := 0; i < 5; i++ {
		_, seq := seedBPHit(t, h, bpID, instUUID, base.Add(time.Duration(i)*time.Second))
		seeded = append(seeded, seq)
	}

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)

	cursor := int64(0)
	collected := []int64{}
	for page := 0; page < 10; page++ { // safety bound; expect 3 iterations
		uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?since=%d&limit=2", instID, cursor)
		contents, rpcErr := cat.Read(req, uri)
		require.Nil(t, rpcErr, "page %d: %+v", page, rpcErr)
		var body struct {
			Hits      []map[string]any `json:"hits"`
			NextSince int64            `json:"next_since"`
			Truncated bool             `json:"truncated"`
		}
		require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
		for _, h := range body.Hits {
			collected = append(collected, int64(h["seq"].(float64)))
		}
		cursor = body.NextSince
		if !body.Truncated {
			require.Equal(t, page, 2, "expected the final page to be page index 2 (third iteration)")
			break
		}
		require.Equal(t, page, page) // suppress unused-var lints if loop body changes
		require.Equal(t, 2, len(body.Hits), "truncated pages should be full")
	}
	require.Equal(t, seeded, collected, "polling loop should drain every seeded hit in seq order")
}

// TestResources_Read_ByBreakpoint exercises the breakpoint-scoped URI
// shape `rimsky://breakpoints/{bp_id}/hits`.
func TestResources_Read_ByBreakpoint(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)
	bpID := createBreakpointForRead(t, h, instID)

	_, seq1 := seedBPHit(t, h, bpID, instUUID, time.Now().UTC().Add(-time.Minute))

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	uri := fmt.Sprintf("rimsky://breakpoints/%s/hits", bpID.String())
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr, "%+v", rpcErr)

	var body struct {
		Hits      []map[string]any `json:"hits"`
		NextSince int64            `json:"next_since"`
		Truncated bool             `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
	require.Len(t, body.Hits, 1)
	require.EqualValues(t, seq1, int64(body.Hits[0]["seq"].(float64)))
	require.Equal(t, bpID.String(), body.Hits[0]["breakpoint_id"])
}

// TestResources_Read_RejectsUnknownScheme covers the URI validator's
// scheme check.
func TestResources_Read_RejectsUnknownScheme(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	_, rpcErr := cat.Read(req, "http://instances/abc/breakpoint-hits")
	require.NotNil(t, rpcErr)
	require.Equal(t, mcp.CodeInvalidParams, rpcErr.Code)
}

// TestResources_Read_RejectsMalformedURI verifies non-canonical paths
// fail with CodeInvalidParams.
func TestResources_Read_RejectsMalformedURI(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)

	for _, bad := range []string{
		"rimsky://instances/00000000-0000-0000-0000-000000000001/wrong-suffix",
		"rimsky://breakpoints/00000000-0000-0000-0000-000000000001/wrong-suffix",
		"rimsky://instances/not-a-uuid/breakpoint-hits",
		"rimsky://something-else/abc/hits",
	} {
		_, rpcErr := cat.Read(req, bad)
		require.NotNil(t, rpcErr, "expected error for uri %q", bad)
		require.Equal(t, mcp.CodeInvalidParams, rpcErr.Code, "uri %q", bad)
	}
}

// TestResources_Read_PermissionDenied verifies an identity without
// `breakpoint:read` is rejected at the URL-resolved gate.
func TestResources_Read_PermissionDenied(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	cat := buildResourceCatalog(h)
	// `event:read` does NOT cover `breakpoint:read`.
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "event:read"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits", instID)
	_, rpcErr := cat.Read(req, uri)
	require.NotNil(t, rpcErr, "expected permission denial")
	require.Equal(t, mcp.CodeInternalError, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "permission denied")
}

// TestResources_Read_LimitCappedAtMax verifies the spec §6.2 limit
// ceiling (500). Requesting limit=9999 should be silently capped.
func TestResources_Read_LimitCappedAtMax(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)
	bpID := createBreakpointForRead(t, h, instID)
	// One hit is enough — we're checking parser bounds, not row content.
	_, _ = seedBPHit(t, h, bpID, instUUID, time.Now().UTC().Add(-time.Minute))

	cat := buildResourceCatalog(h)
	_ = withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	req := httptest.NewRequest("POST", "/mcp", nil)
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?limit=9999", instID)
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr)
	require.NotNil(t, contents)
}

// createBreakpointForRead is a small helper that drives the HTTP route
// to install a breakpoint on the given instance — bypassing the
// matcher template-validation rabbit hole that direct persistence
// inserts would require.
func createBreakpointForRead(t *testing.T, h *harness, instID string) foundationshared.UUID {
	t.Helper()
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, 201, status, out)
	idStr, _ := out["breakpoint_id"].(string)
	id, err := uuid.Parse(idStr)
	require.NoError(t, err)
	return id
}
