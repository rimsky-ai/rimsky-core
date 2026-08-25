// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func withIdentity(t *testing.T, ident auth.Identity) *http.Request {
	t.Helper()
	ctx := context.WithValue(context.Background(), ctxKeyIdentity{}, ident)
	return httptest.NewRequest("POST", "/v1/mcp", nil).WithContext(ctx)
}

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

func buildResourceCatalog(h *harness) mcp.ResourceCatalog {
	return newBreakpointResourceCatalog(AppDeps{
		Persist: h.persist,
		Logger:  h.logger,
	})
}

func TestResources_List_AdminSeesInstance(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())

	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})

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

func TestResources_List_NoBreakpointReadReturnsEmpty(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, _ = seedBPInstance(t, h, uuid.NewString())

	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "instance:read"}}})

	got, err := cat.List(req)
	require.NoError(t, err)
	require.Empty(t, got, "identity without breakpoint:read should see no resources")
}

func TestResources_Read_ByInstance(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)

	bpID := createBreakpointForRead(t, h, instID)
	t1 := time.Now().UTC().Add(-3 * time.Minute)
	_, seq1 := seedBPHit(t, h, bpID, instUUID, t1)
	_, seq2 := seedBPHit(t, h, bpID, instUUID, t1.Add(time.Minute))
	_, seq3 := seedBPHit(t, h, bpID, instUUID, t1.Add(2*time.Minute))
	require.Less(t, seq1, seq2)
	require.Less(t, seq2, seq3)

	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*:read"}}})
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits", instID)
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr, "read failed: %+v", rpcErr)
	require.NotNil(t, contents)
	require.Equal(t, breakpointHitsMimeType, contents.MimeType)
	require.Equal(t, uri, contents.URI)

	var body struct {
		Hits       []map[string]any `json:"hits"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
	require.Len(t, body.Hits, 3)
	require.Equal(t, "", body.NextCursor)
	require.EqualValues(t, seq3, int64(body.Hits[2]["seq"].(float64)))

	require.EqualValues(t, seq1, int64(body.Hits[0]["seq"].(float64)))
	require.NotEmpty(t, body.Hits[0]["hit_id"])
	require.Equal(t, "before_dispatch", body.Hits[0]["checkpoint"])
	require.NotNil(t, body.Hits[0]["dispatch_context"])
}

func TestResources_Read_ByCursor(t *testing.T) {
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
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	firstPage, rpcErr := cat.Read(req, fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?limit=1", instID))
	require.Nil(t, rpcErr)

	var body struct {
		Hits       []map[string]any `json:"hits"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(firstPage.Text), &body))
	require.Len(t, body.Hits, 1)
	require.EqualValues(t, seq1, int64(body.Hits[0]["seq"].(float64)))
	require.NotEmpty(t, body.NextCursor, "a truncated page names the cursor that reaches the rest")

	rest, rpcErr := cat.Read(req, fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?cursor=%s",
		instID, url.QueryEscape(body.NextCursor)))
	require.Nil(t, rpcErr)
	require.NoError(t, json.Unmarshal([]byte(rest.Text), &body))
	require.Len(t, body.Hits, 2, "the cursor reaches the hits after seq1 (%d): seq2 (%d) and seq3 (%d)", seq1, seq2, seq3)
	require.EqualValues(t, seq2, int64(body.Hits[0]["seq"].(float64)))
	require.EqualValues(t, seq3, int64(body.Hits[1]["seq"].(float64)))
	require.Equal(t, "", body.NextCursor)
}

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
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})

	cursor := ""
	collected := []int64{}
	for page := 0; page < 10; page++ {
		uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits?limit=2", instID)
		if cursor != "" {
			uri += "&cursor=" + url.QueryEscape(cursor)
		}
		contents, rpcErr := cat.Read(req, uri)
		require.Nil(t, rpcErr, "page %d: %+v", page, rpcErr)
		var body struct {
			Hits       []map[string]any `json:"hits"`
			NextCursor string           `json:"next_cursor"`
		}
		require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
		for _, h := range body.Hits {
			collected = append(collected, int64(h["seq"].(float64)))
		}
		if body.NextCursor == "" {
			require.Equal(t, 2, page, "expected the final page to be page index 2 (third iteration)")
			break
		}
		require.Less(t, page, 2, "a non-final page must come before the final page index")
		require.Equal(t, 2, len(body.Hits), "a page that names a next cursor is full")
		cursor = body.NextCursor
	}
	require.Equal(t, seeded, collected, "polling loop should drain every seeded hit in seq order")
}

func TestResources_Read_ByBreakpoint(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	instUUID := uuid.MustParse(instID)
	bpID := createBreakpointForRead(t, h, instID)

	_, seq1 := seedBPHit(t, h, bpID, instUUID, time.Now().UTC().Add(-time.Minute))

	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	uri := fmt.Sprintf("rimsky://breakpoints/%s/hits", bpID.String())
	contents, rpcErr := cat.Read(req, uri)
	require.Nil(t, rpcErr, "%+v", rpcErr)

	var body struct {
		Hits       []map[string]any `json:"hits"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal([]byte(contents.Text), &body))
	require.Len(t, body.Hits, 1)
	require.EqualValues(t, seq1, int64(body.Hits[0]["seq"].(float64)))
	require.Equal(t, bpID.String(), body.Hits[0]["breakpoint_id"])
}

func TestResources_Read_RejectsUnknownScheme(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})
	_, rpcErr := cat.Read(req, "http://instances/abc/breakpoint-hits")
	require.NotNil(t, rpcErr)
	require.Equal(t, mcp.CodeInvalidParams, rpcErr.Code)
}

func TestResources_Read_RejectsMalformedURI(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "*"}}})

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

func TestResources_Read_PermissionDenied(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	_, instID := seedBPInstance(t, h, uuid.NewString())
	cat := buildResourceCatalog(h)
	req := withIdentity(t, auth.Identity{Kind: auth.IdentityAPIKey, Permissions: auth.Grant{{Action: "event:read"}}})
	uri := fmt.Sprintf("rimsky://instances/%s/breakpoint-hits", instID)
	_, rpcErr := cat.Read(req, uri)
	require.NotNil(t, rpcErr, "expected permission denial")
	require.Equal(t, mcp.CodeInvalidParams, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "permission denied")
}

func TestResources_Read_LimitCappedAtMax(t *testing.T) {
	t.Parallel()
	limit, rpcErr := parseResourceLimit(url.Values{"limit": []string{"9999"}})
	require.Nil(t, rpcErr)
	require.Equal(t, resourceReadMaxLimit, limit,
		"a limit above the ceiling clamps to it rather than reaching the store")

	limit, rpcErr = parseResourceLimit(url.Values{"limit": []string{"7"}})
	require.Nil(t, rpcErr)
	require.Equal(t, 7, limit, "a limit under the ceiling is honored as given")

	limit, rpcErr = parseResourceLimit(url.Values{})
	require.Nil(t, rpcErr)
	require.Equal(t, resourceReadDefaultLimit, limit, "an absent limit takes the default")
}

func createBreakpointForRead(t *testing.T, h *harness, instID string) foundationshared.UUID {
	t.Helper()
	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/breakpoints", instID), map[string]any{
		"checkpoint": "before_dispatch",
	})
	require.Equal(t, 201, status, out)
	idStr, _ := out["breakpoint_id"].(string)
	id, err := uuid.Parse(idStr)
	require.NoError(t, err)
	return id
}
