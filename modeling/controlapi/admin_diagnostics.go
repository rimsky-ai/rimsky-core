// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// admin_diagnostics.go implements three diagnostic endpoints (plan G1,
// G2, G3):
//
//   - GET  /admin/diagnostics/held-frames  — frames with at least one
//     parked node (the platform-level "held" notion: a frame's
//     downstream work is awaiting external action).
//   - GET  /admin/diagnostics/parked-nodes  — every currently-parked
//     node, optional filter ?reason=<name>.
//   - POST /admin/instances/{instance}/nodes/{node_id}/invalidate
//     — admin-triggered invalidate / parked-resume.
//
// Auth: standard admin perimeter (deps.Auth middleware applies). The
// endpoints are read-only except the explicit invalidate POST.
package controlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// registerAdminDiagnosticsRoutes wires the three new admin endpoints.
func registerAdminDiagnosticsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/admin/diagnostics/held-frames", handleAdminHeldFrames(deps))
	r.Get("/admin/diagnostics/parked-nodes", handleAdminParkedNodes(deps))
	r.Post("/admin/instances/{instance}/nodes/{node_id}/invalidate", handleAdminInvalidateNode(deps))
}

// HeldFramesResponse is the body of GET /admin/diagnostics/held-frames.
//
// FramesWithoutFrameID surfaces parked rows whose worker_request lacks a
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
	NodeID string `json:"node_id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
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
}

// handleAdminHeldFrames lists every frame whose state is 'running' AND
// has at least one parked node. The query joins
// rimsky_worker_request (phase='parked') against rimsky_frames via
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
			// Group by frame_id. Rows without a frame_id can't be
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
					NodeID: p.NodeID, State: "parked", Reason: p.Reason,
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

// handleAdminParkedNodes returns every parked worker_request row.
// Optional ?reason=<name> filter; empty reason value is treated as
// "no filter."
func handleAdminParkedNodes(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		reasonFilter := req.URL.Query().Get("reason")
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

// parkedDiagnosticRow is the internal shape used by both diagnostic
// endpoints. Held in this file so we don't add a public read-projection
// to the persistence interface for an admin-only view.
type parkedDiagnosticRow struct {
	InstanceID string
	NodeID     string
	FrameID    string
	ParkedAt   time.Time
	ResumeAt   time.Time
	Reason     string
}

// listParkedDiagnostic queries the read projection directly via the
// shared persistence Tx. The queries are dialect-aware via the
// persistence Driver's diagnosticParkedReader hook (see helper below).
func listParkedDiagnostic(ctx context.Context, tx persistence.Tx, deps AppDeps, reasonFilter string) ([]parkedDiagnosticRow, error) {
	if r := deps.DiagnosticReader; r != nil {
		return r.ListParkedNodes(ctx, tx, reasonFilter)
	}
	// No reader wired — return empty rather than 500. The router will
	// expose an empty list, which is the correct behavior for tests
	// that don't exercise the diagnostic surface.
	return nil, nil
}

// DiagnosticReader is the operator-supplied accessor for parked-node
// diagnostics. The persistence layer drivers ship a default impl in
// modeling/observability/diagnostics.go (or postgres/sqlite-specific
// reader file) — controlapi only needs the interface so it can stay
// driver-agnostic.
type DiagnosticReader interface {
	ListParkedNodes(ctx context.Context, tx persistence.Tx, reasonFilter string) ([]parkedDiagnosticRow, error)
}

// handleAdminInvalidateNode is POST /admin/instances/{instance}/nodes/{node_id}/invalidate.
//
// Dispatches by node state:
//   - parked → wake (set phase='active', mark ready for dispatch with
//     resume_reason='external_invalidate'). Implementation is
//     stubbed: at this layer we mark the row pending and let the
//     foundation runner build the ResumeContext on next dispatch.
//   - fresh  → standard invalidate (state → stale; cascade engine handles).
//   - running | failed → 409 Conflict.
//
// The unified-handler shape (per plan G3) is delegated to the wired
// InvalidateHandler on AppDeps. When unset, returns 503.
func handleAdminInvalidateNode(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		instanceID, err := uuid.Parse(chi.URLParam(req, "instance"))
		if err != nil {
			badRequest(w, "invalid instance")
			return
		}
		nodeID, err := uuid.Parse(chi.URLParam(req, "node_id"))
		if err != nil {
			badRequest(w, "invalid node_id")
			return
		}
		if deps.InvalidateHandler == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "no invalidate handler wired on this control-api process",
			})
			return
		}
		result, err := deps.InvalidateHandler.InvalidateNode(req.Context(), instanceID.String(), nodeID.String())
		if err != nil {
			if errors.Is(err, ErrInvalidateConflict) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// InvalidateHandler is the unified surface used by admin invalidates,
// on_event handler-emitted invalidates, and (forward-compat) any other
// invalidate source. The control-api layer holds an interface so it
// doesn't need to import the foundation runner.
type InvalidateHandler interface {
	InvalidateNode(ctx context.Context, instanceID, nodeID string) (any, error)
}

// ErrInvalidateConflict is the sentinel returned by InvalidateHandler
// implementations when the node is in a state that does not accept
// invalidates (running | failed). The HTTP layer maps this to 409.
var ErrInvalidateConflict = errors.New("admin invalidate is valid only for parked or fresh states")
