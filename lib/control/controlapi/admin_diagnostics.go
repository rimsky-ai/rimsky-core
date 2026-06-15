// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// admin_diagnostics.go implements the read-only admin diagnostics
// endpoints (plan G1, G2):
//
//   - GET /admin/diagnostics/held-frames  — frames with at least one
//     parked node (the platform-level "held" notion: a frame's
//     downstream work is awaiting external action).
//   - GET /admin/diagnostics/parked-nodes  — every currently-parked
//     node, optional filter ?reason=<name>.
//
// The admin-invalidate POST retired with the 2026-06-14
// message-schema-layer reshape (operators who want to invalidate post
// a typed message via `POST /instances/{id}/messages` with a
// template-declared `messages:` type; ad-hoc force-stale lives at the
// gated `POST /debug/override` endpoint).
//
// Auth: standard admin perimeter (deps.Auth middleware applies). The
// endpoints are read-only.
package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// isKnownParkReasonFilter returns true when v matches the snake_case
// projection of any ParkReason enum value. Empty input is rejected by
// the caller; this helper only validates non-empty values.
func isKnownParkReasonFilter(v string) bool {
	upper := "PARK_REASON_" + strings.ToUpper(v)
	_, ok := genv1.ParkReason_value[upper]
	return ok
}

// knownParkReasonFilters returns the sorted set of snake_case
// ParkReason values usable as the `?reason=` filter — the closed
// two-value set per the post-collapse ParkReason invariant
// (proto:executor.proto::ParkReason).
func knownParkReasonFilters() []string {
	out := make([]string, 0, len(genv1.ParkReason_name))
	for _, name := range genv1.ParkReason_name {
		const prefix = "PARK_REASON_"
		if strings.HasPrefix(name, prefix) {
			out = append(out, strings.ToLower(name[len(prefix):]))
		}
	}
	sort.Strings(out)
	return out
}

// registerAdminDiagnosticsRoutes wires the admin diagnostics endpoints.
//
// F7 — `GET /diagnostics/parked?reason=` is the spec-named operator
// surface (§Parked-state taxonomy / Control-api filter); it shares
// the handler with the older `/admin/diagnostics/parked-nodes` path
// so both shapes are valid and exhibit identical behaviour.
func registerAdminDiagnosticsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/admin/diagnostics/held-frames", gate(deps, "diagnostics:read", handleAdminHeldFrames(deps)))
	r.Get("/admin/diagnostics/parked-nodes", gate(deps, "parked-node:read", handleAdminParkedNodes(deps)))
	r.Get("/diagnostics/parked", gate(deps, "parked-node:read", handleAdminParkedNodes(deps)))
	r.Get("/admin/diagnostics/wait-sets", gate(deps, "waitset:read", handleAdminWaitSets(deps)))
}

// HeldFramesResponse is the body of GET /admin/diagnostics/held-frames.
//
// FramesWithoutFrameID surfaces parked rows whose node_run lacks a
// frame_id (typically pure-cascade-adjacent or legacy rows that registered
// before the frame model). They cannot be bucketed under a real held
// frame — the endpoint reports them out-of-band so operators can see they
// exist without polluting the by-frame bucket with synthetic
// empty-string keys.
type HeldFramesResponse struct {
	Frames               []HeldFrameEntry  `json:"frames"`
	FramesWithoutFrameID []ParkedNodeEntry `json:"frames_without_frame_id,omitempty"`
}

// HeldFrameEntry summarises one frame that has at least one parked node.
type HeldFrameEntry struct {
	FrameID    string         `json:"frame_id"`
	InstanceID string         `json:"instance_id"`
	NodeIDs    []string       `json:"node_ids"`
	HeldSince  time.Time      `json:"held_since"`
	NodeStates []NodeStateRow `json:"node_states"`
}

// NodeStateRow is the per-node summary inside a HeldFrameEntry.
type NodeStateRow struct {
	NodeID     string `json:"node_id"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	ReasonNote string `json:"reason_note,omitempty"`
}

// ParkedNodesResponse is the body of GET /admin/diagnostics/parked-nodes.
type ParkedNodesResponse struct {
	ParkedNodes []ParkedNodeEntry `json:"parked_nodes"`
}

// ParkedNodeEntry summarises one currently-parked node.
type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ReasonNote string     `json:"reason_note,omitempty"`
}

// handleAdminHeldFrames lists every frame whose state is 'running' AND
// has at least one parked node. The query joins
// rimsky_node_runs (phase='parked') against rimsky_frames via
// frame_id; rows that lack a frame_id are excluded silently (defensive
// against pure-cascade nodes whose dispatch never registered a frame).
func handleAdminHeldFrames(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out := HeldFramesResponse{Frames: []HeldFrameEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, tx, deps, "")
			if err != nil {
				return err
			}
			// @constraint: group by frame_id. Rows without a frame_id can't be
			// represented as a held frame (the endpoint is documented
			// as "frames currently in held state") — report them in
			// a separate FramesWithoutFrameID list so the by-frame
			// bucket is not contaminated with a synthetic
			// empty-string key whose InstanceID/HeldSince would
			// arbitrarily reflect whichever orphan row was seen
			// first.
			groups := map[string]*HeldFrameEntry{}
			for _, p := range parked {
				if p.FrameID == "" {
					entry := ParkedNodeEntry{
						InstanceID: p.InstanceID,
						NodeID:     p.NodeID,
						ParkedAt:   p.ParkedAt,
						Reason:     p.Reason,
						ReasonNote: p.ReasonNote,
					}
					if !p.ResumeAt.IsZero() {
						ra := p.ResumeAt
						entry.ResumeAt = &ra
					}
					out.FramesWithoutFrameID = append(out.FramesWithoutFrameID, entry)
					continue
				}
				key := p.FrameID
				g, ok := groups[key]
				if !ok {
					g = &HeldFrameEntry{
						FrameID:    p.FrameID,
						InstanceID: p.InstanceID,
						HeldSince:  p.ParkedAt,
					}
					groups[key] = g
				}
				if p.ParkedAt.Before(g.HeldSince) {
					g.HeldSince = p.ParkedAt
				}
				g.NodeIDs = append(g.NodeIDs, p.NodeID)
				g.NodeStates = append(g.NodeStates, NodeStateRow{
					NodeID: p.NodeID, State: "parked", Reason: p.Reason, ReasonNote: p.ReasonNote,
				})
			}
			for _, g := range groups {
				out.Frames = append(out.Frames, *g)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleAdminParkedNodes returns every parked node_run row.
// Optional ?reason=<snake_case> filter; empty reason value is treated
// as "no filter." Per the 2026-05-14 ParkReason-typed cycle, the
// `reason` query param is validated against the typed enum's
// snake_case projection; unknown values return HTTP 400 with the
// allowed set.
//
//	@concept: parked-state
func handleAdminParkedNodes(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		reasonFilter := req.URL.Query().Get("reason")
		if reasonFilter != "" && !isKnownParkReasonFilter(reasonFilter) {
			badRequest(w, fmt.Sprintf(
				"unknown park reason %q (allowed: %s)",
				reasonFilter, strings.Join(knownParkReasonFilters(), ", ")))
			return
		}
		out := ParkedNodesResponse{ParkedNodes: []ParkedNodeEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, tx, deps, reasonFilter)
			if err != nil {
				return err
			}
			for _, p := range parked {
				entry := ParkedNodeEntry{
					InstanceID: p.InstanceID,
					NodeID:     p.NodeID,
					ParkedAt:   p.ParkedAt,
					Reason:     p.Reason,
					ReasonNote: p.ReasonNote,
				}
				if !p.ResumeAt.IsZero() {
					ra := p.ResumeAt
					entry.ResumeAt = &ra
				}
				out.ParkedNodes = append(out.ParkedNodes, entry)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// listParkedDiagnostic queries the read projection directly via the
// persistence.Queue accessor. The query joins rimsky_nodes for
// instance_id so the endpoint can group by frame without a second read.
func listParkedDiagnostic(ctx context.Context, tx persistence.Tx, deps AppDeps, reasonFilter string) ([]persistence.ParkedDiagnosticRow, error) {
	if deps.Queue == nil {
		return nil, nil
	}
	return deps.Queue.ListParkedDiagnostic(ctx, tx, reasonFilter)
}
