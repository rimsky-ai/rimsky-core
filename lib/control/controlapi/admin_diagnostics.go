// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func registerAdminDiagnosticsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/admin/diagnostics/held-frames", gate(deps, "diagnostics:read", handleAdminHeldFrames(deps)))
	r.Get("/admin/diagnostics/parked-nodes", gate(deps, "parked-node:read", handleAdminParkedNodes(deps)))
	r.Get("/admin/diagnostics/wait-sets", gate(deps, "waitset:read", handleAdminWaitSets(deps)))
}

type HeldFramesResponse struct {
	Frames               []HeldFrameEntry  `json:"frames"`
	FramesWithoutFrameID []ParkedNodeEntry `json:"frames_without_frame_id,omitempty"`
}

type HeldFrameEntry struct {
	FrameID    string         `json:"frame_id"`
	InstanceID string         `json:"instance_id"`
	NodeIDs    []string       `json:"node_ids"`
	HeldSince  time.Time      `json:"held_since"`
	NodeStates []NodeStateRow `json:"node_states"`
}

type NodeStateRow struct {
	NodeID string `json:"node_id"`
	State  string `json:"state"`
}

type ParkedNodesResponse struct {
	ParkedNodes []ParkedNodeEntry `json:"parked_nodes"`
}

type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
}

func handleAdminHeldFrames(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out := HeldFramesResponse{Frames: []HeldFrameEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, tx, deps)
			if err != nil {
				return err
			}
			groups := map[string]*HeldFrameEntry{}
			for _, p := range parked {
				if p.FrameID == "" {
					entry := ParkedNodeEntry{
						InstanceID: p.InstanceID,
						NodeID:     p.NodeID,
						ParkedAt:   p.ParkedAt,
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
					NodeID: p.NodeID, State: "parked",
				})
			}
			for _, g := range groups {
				out.Frames = append(out.Frames, *g)
			}
			sort.Slice(out.Frames, func(i, j int) bool {
				if !out.Frames[i].HeldSince.Equal(out.Frames[j].HeldSince) {
					return out.Frames[i].HeldSince.Before(out.Frames[j].HeldSince)
				}
				return out.Frames[i].FrameID < out.Frames[j].FrameID
			})
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// @concept: parked-state
func handleAdminParkedNodes(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		out := ParkedNodesResponse{ParkedNodes: []ParkedNodeEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, tx, deps)
			if err != nil {
				return err
			}
			for _, p := range parked {
				entry := ParkedNodeEntry{
					InstanceID: p.InstanceID,
					NodeID:     p.NodeID,
					ParkedAt:   p.ParkedAt,
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

func listParkedDiagnostic(ctx context.Context, tx persistence.Tx, deps AppDeps) ([]persistence.ParkedDiagnosticRow, error) {
	if deps.Queue == nil {
		return nil, nil
	}
	return deps.Queue.ListParkedDiagnostic(ctx, tx)
}
