// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// mcp_resources.go — the in-control-api MCP `resources` skin, paired
// with the JSON-RPC dispatcher in control/controlapi/mcp/server.go.
//
// This is the ONLY code site that parses the `rimsky://...` URI scheme
// (per spec .ok-planner/specs/2026-05-24-instance-debugger-design.md
// §6.2). The layers above (the JSON-RPC dispatcher) and below (the
// breakpoint-hits persistence accessor) stay URI-agnostic. Adding a
// future SSE/webhook adapter is "write a new Layer 3 file"; nothing
// downstream has to change.
//
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

// Resource-read pagination bounds per spec §6.2 / §6.4.
const (
	resourceReadDefaultLimit = 100
	resourceReadMaxLimit     = 500
)

// MIME type advertised in resources/list entries and resources/read
// envelopes per spec §6.4.
const breakpointHitsMimeType = "application/x-rimsky-breakpoint-hits+json"

// breakpointResourceCatalog implements mcp.ResourceCatalog for the
// `rimsky://instances/{id}/breakpoint-hits` and
// `rimsky://breakpoints/{bp_id}/hits` URI families. It gates each read
// against `breakpoint:read` on the resolved instance using the same
// identity the catalog uses for tool filtering.
type breakpointResourceCatalog struct {
	deps AppDeps
}

// newBreakpointResourceCatalog wires the resource catalog from the
// AppDeps bundle. Returned as the mcp.ResourceCatalog interface so the
// in-process server can stay loosely coupled to controlapi.
func newBreakpointResourceCatalog(deps AppDeps) mcp.ResourceCatalog {
	return &breakpointResourceCatalog{deps: deps}
}

// List enumerates the instances the requesting identity has
// `breakpoint:read` permission for, advertising one instance-scoped
// resource URI per accessible instance. Per spec §6.2 the
// breakpoint-scoped URI shape is constructed by the agent after the
// create call returns its `breakpoint_id` and is not enumerated.
func (c *breakpointResourceCatalog) List(r *http.Request) ([]mcp.Resource, error) {
	ident, _ := IdentityFromContextOK(r.Context())
	// `breakpoint:read` covers both URI families per spec §6.7. If the
	// requesting identity can't read any breakpoint at all (no `*:read`,
	// no `breakpoint:*`, no `breakpoint:read`), return an empty list.
	if !auth.CheckGrant(ident.Permissions, "breakpoint:read", nil).Allowed {
		return []mcp.Resource{}, nil
	}
	// Enumerate active instances. The list is paginated by persistence;
	// drain every page so the catalog's enumeration is exhaustive
	// (resources/list is expected to be small per spec — bounded by the
	// number of live debuggable instances). Terminated instances are
	// filtered out — their breakpoints and hits cascade-deleted with
	// the instance, so advertising URIs for them would point at empty
	// resources.
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
				URI:         "rimsky://instances/" + id + "/breakpoint-hits",
				Name:        "Breakpoint hits for instance " + id,
				MimeType:    breakpointHitsMimeType,
				Description: "Breakpoint hits for instance " + id + ". Read with ?since=<seq> and ?limit=<n>.",
			})
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// Read parses the URI, gates against `breakpoint:read`, calls the
// appropriate ListSince* accessor, and shapes the response per spec
// §6.4.
func (c *breakpointResourceCatalog) Read(r *http.Request, rawURI string) (*mcp.ResourceContents, *mcp.Error) {
	parsed, rpcErr := parseBreakpointHitsURI(rawURI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	ident, _ := IdentityFromContextOK(r.Context())
	if !auth.CheckGrant(ident.Permissions, "breakpoint:read", nil).Allowed {
		// Mirror the HTTP gateByAction shape — denials are 403 with
		// `permission_denied`; map to JSON-RPC -32603 with the same
		// message so the agent has a clear signal.
		return nil, &mcp.Error{Code: mcp.CodeInternalError, Message: "permission denied: breakpoint:read"}
	}

	var (
		hits    []persistence.BreakpointHitRow
		hitsErr error
	)
	// Fetch limit+1 so we can report `truncated` only when there's
	// actually a hit beyond the requested page, instead of speculating
	// every time the page size happens to be exactly `limit`.
	fetchLimit := parsed.limit + 1
	switch parsed.kind {
	case bpHitsScopeInstance:
		// Confirm the instance exists; otherwise return a clear 404-ish
		// signal (CodeInvalidParams below) instead of leaking through
		// whatever the storage layer would surface for an unknown id.
		hitsErr = c.deps.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := c.deps.Persist.Instances().Get(ctx, parsed.id, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return foundationshared.ErrInstanceNotFound
			}
			hits, err = c.deps.Persist.BreakpointHits().ListSinceForInstance(ctx, parsed.id, parsed.since, fetchLimit, tx)
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
			hits, err = c.deps.Persist.BreakpointHits().ListSinceForBreakpoint(ctx, parsed.id, parsed.since, fetchLimit, tx)
			return err
		})
	}
	if hitsErr != nil {
		// Not-found sentinels map to CodeInvalidParams (the URI named a
		// non-existent instance / breakpoint). Anything else is internal.
		if errors.Is(hitsErr, foundationshared.ErrBreakpointNotFound) || errors.Is(hitsErr, foundationshared.ErrInstanceNotFound) {
			return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: hitsErr.Error()}
		}
		return nil, &mcp.Error{Code: mcp.CodeInternalError, Message: hitsErr.Error()}
	}

	// Trim the probe row before serialization; `truncated` reflects
	// whether we observed at least one row beyond the requested page,
	// not whether the page happened to hit exactly `limit`.
	truncated := len(hits) > parsed.limit
	if truncated {
		hits = hits[:parsed.limit]
	}

	// Marshal hits per spec §4.6. The snapshot stored on the hit row is
	// the canonical payload; the per-row envelope adds the
	// row-identity fields (seq, hit_id, breakpoint_id, instance_id,
	// node_run_id, frame_id, checkpoint, mode, hit_at).
	items := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		items = append(items, hitToWireShape(h))
	}
	nextSince := parsed.since
	if len(hits) > 0 {
		nextSince = hits[len(hits)-1].Seq
	}
	bodyBytes, err := json.Marshal(map[string]any{
		"hits":       items,
		"next_since": nextSince,
		"truncated":  truncated,
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

// hitToWireShape produces the per-hit envelope wrapping snapshot per
// spec §4.6. Row-identity fields (seq, hit_id, ...) sit alongside the
// snapshot map's contents so polling clients see one flat object per
// hit.
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
	// The snapshot map contains dispatch_context, node_run, held_claims,
	// open_wait_set, optionally terminal_signal and effective_schema.
	// Surface its top-level keys directly so callers can read them
	// without an extra unwrap.
	for k, v := range h.Snapshot {
		// Don't let snapshot keys shadow the row-identity fields above.
		if _, taken := out[k]; taken {
			continue
		}
		out[k] = v
	}
	return out
}

// parsedBreakpointHitsURI is the typed parse result returned by
// parseBreakpointHitsURI. Internal to this file.
type parsedBreakpointHitsURI struct {
	kind  bpHitsURIKind
	id    foundationshared.UUID
	since int64
	limit int
}

type bpHitsURIKind int

const (
	bpHitsScopeInstance bpHitsURIKind = iota + 1
	bpHitsScopeBreakpoint
)

// parseBreakpointHitsURI parses one of the two canonical URI shapes:
//
//	rimsky://instances/{uuid}/breakpoint-hits[?since=<seq>&limit=<n>]
//	rimsky://breakpoints/{uuid}/hits[?since=<seq>&limit=<n>]
//
// Returns CodeInvalidParams for anything else. The query parsing is
// permissive: since defaults to 0; limit defaults to
// resourceReadDefaultLimit and is capped at resourceReadMaxLimit.
func parseBreakpointHitsURI(raw string) (*parsedBreakpointHitsURI, *mcp.Error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "uri parse: " + err.Error()}
	}
	if u.Scheme != "rimsky" {
		return nil, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "uri scheme must be rimsky:// (got " + u.Scheme + "://)"}
	}
	// Combine host + path so both `rimsky://instances/{uuid}/...` and
	// `rimsky://breakpoints/{uuid}/...` parse the same way regardless of
	// whether url.Parse decided to put the leading segment in Host or
	// Path. Different stdlib versions can vary here.
	pathPart := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(strings.TrimSuffix(u.Host+"/"+pathPart, "/"), "/")
	// parts must be one of:
	//   ["instances",   "<uuid>", "breakpoint-hits"]
	//   ["breakpoints", "<uuid>", "hits"]
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
	since, limit, rpcErr := parseSinceLimit(u.Query())
	if rpcErr != nil {
		return nil, rpcErr
	}
	return &parsedBreakpointHitsURI{
		kind:  kind,
		id:    id,
		since: since,
		limit: limit,
	}, nil
}

// parseSinceLimit pulls `since` and `limit` from the URI's query
// parameters, applying defaults and bounds per spec §6.2.
func parseSinceLimit(q url.Values) (int64, int, *mcp.Error) {
	since := int64(0)
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "since must be a non-negative integer (got " + v + ")"}
		}
		since = n
	}
	limit := resourceReadDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, 0, &mcp.Error{Code: mcp.CodeInvalidParams, Message: "limit must be a positive integer (got " + v + ")"}
		}
		if n > resourceReadMaxLimit {
			n = resourceReadMaxLimit
		}
		limit = n
	}
	return since, limit, nil
}
