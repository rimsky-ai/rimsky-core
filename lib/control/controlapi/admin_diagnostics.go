// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"fmt"
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
	r.Get("/admin/diagnostics/producer-outbox", gate(deps, "diagnostics:read", handleAdminProducerOutbox(deps)))
	// @decision: service-delivery-stall-signal
	r.Get("/admin/diagnostics/lifecycle-outbox", gate(deps, "diagnostics:read", handleAdminLifecycleOutbox(deps)))
}

// @decision: service-delivery-stall-signal
type LifecycleOutboxResponse struct {
	Services   []LifecycleOutboxService `json:"services"`
	NextCursor string                   `json:"next_cursor"`
}

// @decision: service-delivery-stall-signal
type LifecycleOutboxService struct {
	Service          string                 `json:"service"`
	Depth            int                    `json:"depth"`
	OldestStagedAt   time.Time              `json:"oldest_staged_at"`
	OldestAgeSeconds float64                `json:"oldest_age_seconds"`
	Entries          []LifecycleOutboxEntry `json:"entries"`
}

// @decision: service-delivery-stall-signal
type LifecycleOutboxEntry struct {
	Seq           int64     `json:"seq"`
	ScopeKind     string    `json:"scope_kind"`
	ScopeID       string    `json:"scope_id"`
	Event         string    `json:"event"`
	StagedAt      time.Time `json:"staged_at"`
	AgeSeconds    float64   `json:"age_seconds"`
	AttemptCount  int       `json:"attempt_count"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     string    `json:"last_error,omitempty"`
}

// @decision: service-delivery-stall-signal
// @concept: lifecycle-subscriber
func handleAdminLifecycleOutbox(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit, limErr := parseLimit(req, 100)
		if limErr != nil {
			badRequest(w, limErr.Error())
			return
		}
		out := LifecycleOutboxResponse{Services: []LifecycleOutboxService{}}
		now := deps.Clock.Now()
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			summary, err := deps.Persist.LifecycleOutbox().PendingSummaryByService(ctx, tx)
			if err != nil {
				return err
			}
			for _, s := range summary {
				rows, err := deps.Persist.LifecycleOutbox().ListPendingForService(ctx, s.Service,
					persistence.DefaultServiceOutboxPageSize, tx)
				if err != nil {
					return err
				}
				entry := LifecycleOutboxService{
					Service:          s.Service,
					Depth:            s.PendingCount,
					OldestStagedAt:   s.OldestPendingAt,
					OldestAgeSeconds: now.Sub(s.OldestPendingAt).Seconds(),
					Entries:          []LifecycleOutboxEntry{},
				}
				for _, row := range rows {
					entry.Entries = append(entry.Entries, LifecycleOutboxEntry{
						Seq:           row.Seq,
						ScopeKind:     string(row.ScopeKind),
						ScopeID:       row.ScopeID,
						Event:         row.Event,
						StagedAt:      row.StagedAt,
						AgeSeconds:    now.Sub(row.StagedAt).Seconds(),
						AttemptCount:  row.AttemptCount,
						NextAttemptAt: row.NextAttemptAt,
						LastError:     row.LastError,
					})
				}
				out.Services = append(out.Services, entry)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		page, nextCursor, pageErr := persistence.PageByKey(out.Services, req.URL.Query().Get("cursor"), limit,
			func(s LifecycleOutboxService) string { return s.Service })
		if pageErr != nil {
			writeError(w, pageErr)
			return
		}
		out.Services = page
		out.NextCursor = nextCursor
		writeJSON(w, http.StatusOK, out)
	}
}

func producerOutboxKey(seq int64) string {
	return fmt.Sprintf("%020d", seq)
}

type producerVerbOutboxProvider interface {
	ProducerVerbOutbox() persistence.ProducerVerbOutboxTable
}

type ProducerOutboxResponse struct {
	Depth            int                   `json:"depth"`
	OldestEnqueuedAt *time.Time            `json:"oldest_enqueued_at,omitempty"`
	OldestAgeSeconds *float64              `json:"oldest_age_seconds,omitempty"`
	Entries          []ProducerOutboxEntry `json:"entries"`
	NextCursor       string                `json:"next_cursor"`
}

type ProducerOutboxEntry struct {
	Seq           int64     `json:"seq"`
	ProducerName  string    `json:"producer_name"`
	Verb          string    `json:"verb"`
	ClaimHandleID string    `json:"claim_handle_id"`
	InstanceID    *string   `json:"instance_id,omitempty"`
	AttemptCount  int       `json:"attempt_count"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	LastError     string    `json:"last_error,omitempty"`
	EnqueuedAt    time.Time `json:"enqueued_at"`
}

// @concept: terminal-resolution
func handleAdminProducerOutbox(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		provider, ok := deps.Persist.(producerVerbOutboxProvider)
		if !ok {
			writeError(w, fmt.Errorf("store %T does not expose the producer-verb outbox", deps.Persist))
			return
		}
		limit, limErr := parseLimit(req, persistence.DefaultServiceOutboxPageSize)
		if limErr != nil {
			badRequest(w, limErr.Error())
			return
		}
		cursor := req.URL.Query().Get("cursor")
		after := ""
		if cursor != "" {
			key, curErr := persistence.DecodeKeyCursor(cursor)
			if curErr != nil {
				writeError(w, persistence.ErrInvalidCursor)
				return
			}
			after = key
		}
		out := ProducerOutboxResponse{Entries: []ProducerOutboxEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			rows, err := provider.ProducerVerbOutbox().ListAll(ctx, tx)
			if err != nil {
				return err
			}
			out.Depth = len(rows)
			for _, row := range rows {
				if out.OldestEnqueuedAt == nil || row.EnqueuedAt.Before(*out.OldestEnqueuedAt) {
					enqueuedAt := row.EnqueuedAt
					out.OldestEnqueuedAt = &enqueuedAt
				}
				if producerOutboxKey(row.Seq) <= after || len(out.Entries) > limit {
					continue
				}
				entry := ProducerOutboxEntry{
					Seq:           row.Seq,
					ProducerName:  row.ProducerName,
					Verb:          string(row.Verb),
					ClaimHandleID: row.ClaimHandleID.String(),
					AttemptCount:  row.AttemptCount,
					NextAttemptAt: row.NextAttemptAt,
					LastError:     row.LastError,
					EnqueuedAt:    row.EnqueuedAt,
				}
				if row.InstanceID != nil {
					iid := row.InstanceID.String()
					entry.InstanceID = &iid
				}
				out.Entries = append(out.Entries, entry)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if out.OldestEnqueuedAt != nil {
			age := deps.Clock.Now().Sub(*out.OldestEnqueuedAt).Seconds()
			out.OldestAgeSeconds = &age
		}
		page, nextCursor, pageErr := persistence.PageByKey(out.Entries, cursor, limit,
			func(e ProducerOutboxEntry) string { return producerOutboxKey(e.Seq) })
		if pageErr != nil {
			writeError(w, pageErr)
			return
		}
		out.Entries = page
		out.NextCursor = nextCursor
		writeJSON(w, http.StatusOK, out)
	}
}

type HeldFramesResponse struct {
	Frames     []HeldFrameEntry `json:"frames"`
	NextCursor string           `json:"next_cursor"`
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
	NextCursor  string            `json:"next_cursor"`
}

type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
}

func handleAdminHeldFrames(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit, limErr := parseLimit(req, 100)
		if limErr != nil {
			badRequest(w, limErr.Error())
			return
		}
		out := HeldFramesResponse{Frames: []HeldFrameEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, deps, tx)
			if err != nil {
				return err
			}
			groups := map[string]*HeldFrameEntry{}
			for _, p := range parked {
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
		page, nextCursor, pageErr := persistence.PageByKey(out.Frames, req.URL.Query().Get("cursor"), limit,
			func(f HeldFrameEntry) string {
				return persistence.SortableTimeKey(f.HeldSince) + "|" + f.FrameID
			})
		if pageErr != nil {
			writeError(w, pageErr)
			return
		}
		out.Frames = page
		out.NextCursor = nextCursor
		writeJSON(w, http.StatusOK, out)
	}
}

// @concept: parked-state
func handleAdminParkedNodes(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit, limErr := parseLimit(req, 100)
		if limErr != nil {
			badRequest(w, limErr.Error())
			return
		}
		out := ParkedNodesResponse{ParkedNodes: []ParkedNodeEntry{}}
		err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			parked, err := listParkedDiagnostic(ctx, deps, tx)
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
		page, nextCursor, pageErr := persistence.PageByKey(out.ParkedNodes, req.URL.Query().Get("cursor"), limit,
			func(p ParkedNodeEntry) string {
				return persistence.SortableTimeKey(p.ParkedAt) + "|" + p.NodeID
			})
		if pageErr != nil {
			writeError(w, pageErr)
			return
		}
		out.ParkedNodes = page
		out.NextCursor = nextCursor
		writeJSON(w, http.StatusOK, out)
	}
}

func listParkedDiagnostic(ctx context.Context, deps AppDeps, tx persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	if deps.Queue == nil {
		return nil, nil
	}
	return deps.Queue.ListParkedDiagnostic(ctx, tx)
}
