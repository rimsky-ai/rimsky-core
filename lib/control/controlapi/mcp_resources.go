// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: control-api
// @concept: breakpoint

package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	resourceReadDefaultLimit = 100
	resourceReadMaxLimit     = 500
)

const breakpointHitsMimeType = "application/x-rimsky-breakpoint-hits+json"

type breakpointResourceCatalog struct {
	deps AppDeps
}

func newBreakpointResourceCatalog(deps AppDeps) mcp.ResourceCatalog {
	return &breakpointResourceCatalog{deps: deps}
}

func (c *breakpointResourceCatalog) List(r *http.Request) ([]mcp.Resource, error) {
	ident, _ := IdentityFromContextOK(r.Context())
	if !auth.HasAnyGrant(ident.Permissions, "breakpoint:read") {
		return []mcp.Resource{}, nil
	}
	out := []mcp.Resource{}
	cursor := ""
	active := true
	for {
		var page persistence.PaginatedListResult[persistence.InstanceRow]
		err := c.deps.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := c.deps.Persist.Instances().List(ctx, persistence.InstanceListFilter{Active: &active}, persistence.ListPagination{
				Limit:  200,
				Cursor: cursor,
			}, tx)
			page = p
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("instances.list: %w", err)
		}
		for _, inst := range page.Rows {
			id := inst.ID.String()
			out = append(out, mcp.Resource{
				URI:      "rimsky://instances/" + id + "/breakpoint-hits",
				Name:     "Breakpoint hits for instance " + id,
				MimeType: breakpointHitsMimeType,
				Description: "Breakpoint hits for instance " + id + ". Read with ?limit=<n> and ?cursor=<next_cursor>." +
					" A single breakpoint's hits are also readable directly at rimsky://breakpoints/{breakpoint_id}/hits.",
			})
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *breakpointResourceCatalog) Read(r *http.Request, rawURI string) (*mcp.ResourceContents, *mcp.Error) {
	parsed, rpcErr := parseBreakpointHitsURI(rawURI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	ident, _ := IdentityFromContextOK(r.Context())
	if !auth.CheckGrant(ident.Permissions, "breakpoint:read", nil).Allowed {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "permission denied: breakpoint:read"}
	}

	var (
		hits    []persistence.BreakpointHitRow
		hitsErr error
	)
	fetchLimit := parsed.limit + 1
	switch parsed.kind {
	case bpHitsScopeInstance:
		hitsErr = c.deps.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := c.deps.Persist.Instances().Get(ctx, parsed.id, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return foundationshared.ErrInstanceNotFound
			}
			hits, err = c.deps.Persist.BreakpointHits().ListSinceForInstance(ctx, parsed.id, parsed.afterSeq, fetchLimit, tx)
			return err
		})
	case bpHitsScopeBreakpoint:
		hitsErr = c.deps.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
			bp, err := c.deps.Persist.Breakpoints().Get(ctx, parsed.id, tx)
			if err != nil {
				return err
			}
			if bp == nil {
				return foundationshared.ErrBreakpointNotFound
			}
			hits, err = c.deps.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, parsed.id, parsed.afterSeq, fetchLimit, tx)
			return err
		})
	}
	if hitsErr != nil {
		if errors.Is(hitsErr, foundationshared.ErrBreakpointNotFound) || errors.Is(hitsErr, foundationshared.ErrInstanceNotFound) {
			return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: hitsErr.Error()}
		}
		return nil, &mcp.Error{Code: mcp.CodeInternalError, Message: hitsErr.Error()}
	}

	nextCursor := ""
	if len(hits) > parsed.limit {
		hits = hits[:parsed.limit]
		nextCursor = encodeSeqCursor(hits[len(hits)-1].Seq)
	}

	items := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		items = append(items, hitToWireShape(h))
	}
	bodyBytes, err := json.Marshal(map[string]any{
		"hits":        items,
		"next_cursor": nextCursor,
	})
	if err != nil {
		return nil, &mcp.Error{Code: mcp.CodeInternalError, Message: "marshal hits: " + err.Error()}
	}
	return &mcp.ResourceContents{
		URI:      rawURI,
		MimeType: breakpointHitsMimeType,
		Text:     string(bodyBytes),
	}, nil
}

func hitToWireShape(h persistence.BreakpointHitRow) map[string]any {
	out := map[string]any{
		"seq":           h.Seq,
		"hit_id":        h.ID.String(),
		"breakpoint_id": h.BreakpointID.String(),
		"instance_id":   h.InstanceID.String(),
		"checkpoint":    string(h.Checkpoint),
		"mode":          string(h.Mode),
		"hit_at":        h.HitAt,
	}
	if h.NodeRunID != nil {
		out["node_run_id"] = h.NodeRunID.String()
	}
	if h.FrameID != nil {
		out["frame_id"] = h.FrameID.String()
	}
	for k, v := range h.Snapshot {
		if _, taken := out[k]; taken {
			continue
		}
		out[k] = v
	}
	return out
}

type parsedBreakpointHitsURI struct {
	kind     bpHitsURIKind
	id       foundationshared.UUID
	afterSeq int64
	limit    int
}

type bpHitsURIKind int

const (
	bpHitsScopeInstance bpHitsURIKind = iota + 1
	bpHitsScopeBreakpoint
)

func parseBreakpointHitsURI(raw string) (*parsedBreakpointHitsURI, *mcp.Error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "uri parse: " + err.Error()}
	}
	if u.Scheme != "rimsky" {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "uri scheme must be rimsky:// (got " + u.Scheme + "://)"}
	}
	pathPart := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(strings.TrimSuffix(u.Host+"/"+pathPart, "/"), "/")
	if len(parts) != 3 {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "uri must be rimsky://instances/{uuid}/breakpoint-hits or rimsky://breakpoints/{uuid}/hits (got " + raw + ")"}
	}
	var kind bpHitsURIKind
	switch {
	case parts[0] == "instances" && parts[2] == "breakpoint-hits":
		kind = bpHitsScopeInstance
	case parts[0] == "breakpoints" && parts[2] == "hits":
		kind = bpHitsScopeBreakpoint
	default:
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "unknown uri shape: " + raw}
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "id segment must be a UUID: " + err.Error()}
	}
	limit, rpcErr := parseResourceLimit(u.Query())
	if rpcErr != nil {
		return nil, rpcErr
	}
	afterSeq := int64(0)
	if raw := u.Query().Get("cursor"); raw != "" {
		seq, err := decodeSeqCursor(raw)
		if err != nil {
			return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "cursor is not one this server minted (got " + raw + ")"}
		}
		afterSeq = seq
	}
	return &parsedBreakpointHitsURI{
		kind:     kind,
		id:       id,
		afterSeq: afterSeq,
		limit:    limit,
	}, nil
}

func parseResourceLimit(q url.Values) (int, *mcp.Error) {
	v := q.Get("limit")
	if v == "" {
		return resourceReadDefaultLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "limit must be a positive integer (got " + v + ")"}
	}
	if n > resourceReadMaxLimit {
		return resourceReadMaxLimit, nil
	}
	return n, nil
}
