// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// inTx runs fn inside a fresh short Tables.Transaction. Read-only
// observability handlers all share this shape: a single read or a small
// fan-out followed by JSON serialization. Wrapping each persistence
// call in its own short tx keeps each handler invocation simple under
// option C (every Table method requires an explicit tx).
func inTx(ctx context.Context, tables persistence.Tables, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return tables.Transaction(ctx, fn)
}

// Deps bundles the dependencies the observability HTTP handlers need.
// The persistence layer is consumed via the typed Tables/Queue
// interfaces; no driver-specific subpackages are imported here. The
// peer specs reflect the rimsky.yml `executors:` and `claim_producers:`
// blocks projected via PeerSpec — keeps the package free of
// control/config.
type Deps struct {
	Tables    persistence.Tables
	Queue     persistence.Queue
	Driver    persistence.Database
	Executors []PeerSpec
	Stores    []PeerSpec
	Discovery *Discovery
}

// Routes mounts the read-only observability endpoints under the parent
// chi router. Per spec §1.2.
func Routes(r chi.Router, deps Deps) {
	r.Get("/stores", handleListStores(deps))
	r.Get("/stores/{name}", handleGetStore(deps))
	r.Get("/executors", handleListExecutors(deps))
	r.Get("/executors/{name}", handleGetExecutor(deps))

	r.Get("/templates", handleListTemplates(deps))
	r.Get("/templates/{hash}", handleGetTemplate(deps))
	r.Get("/instances", handleListInstances(deps))
	r.Get("/instances/{id}", handleGetInstance(deps))
	// @constraint: (/schedules retired by the 2026-05-15 plan B10 / D7 / E16
	// schedule-retirement cascade; cron firing is owned by
	// sensors/sensor-cron/.)

	r.Get("/frames", handleListFrames(deps))
	r.Get("/frames/{id}", handleGetFrame(deps))
	r.Get("/nodes/{instance_id}/{node_type}", handleGetNode(deps))
	r.Get("/node-runs", handleListNodeRuns(deps))
	r.Get("/node-runs/{id}", handleGetNodeRun(deps))

	r.Get("/lock-holders", handleListLockHolders(deps))
	r.Get("/lock-holders/{id}", handleGetClaimHandle(deps))

	r.Get("/events", handleListEvents(deps))
	r.Get("/system/health", handleSystemHealth(deps))
	r.Get("/system/summary", handleSystemSummary(deps))
}

const (
	defaultLimit = 50
	maxLimit     = 500
)

func parsePagination(r *http.Request) (persistence.ListPagination, error) {
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return persistence.ListPagination{}, errors.New("invalid limit")
		}
		if n > maxLimit {
			n = maxLimit
		}
		if n > 0 {
			limit = n
		}
	}
	return persistence.ListPagination{Limit: limit, Cursor: r.URL.Query().Get("cursor")}, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusBadRequest, "bad_request", msg)
}

func notFound(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusNotFound, "not_found", msg)
}

func internalErr(w http.ResponseWriter, err error) {
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

type peerListResponse struct {
	Stores    []PeerEntry `json:"stores,omitempty"`
	Executors []PeerEntry `json:"executors,omitempty"`
}

func handleListStores(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := []PeerEntry{}
		for _, e := range deps.Stores {
			cached, ok := deps.Discovery.GetStore(e.Name)
			if !ok {
				cached = PeerEntry{
					Name:                  e.Name,
					Endpoint:              e.Endpoint,
					ObservabilityEndpoint: chooseObsEndpoint(e.ObservabilityEndpoint, e.Endpoint),
					Reachability:          ReachabilityUnreachable,
				}
			}
			entries = append(entries, cached)
		}
		writeJSON(w, http.StatusOK, peerListResponse{Stores: entries})
	}
}

func handleGetStore(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !peerExists(deps.Stores, name) {
			notFound(w, "unknown store")
			return
		}
		cached, _ := deps.Discovery.GetStore(name)
		var lifecycle []persistence.LifecycleIdempotencyRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			rows, err := deps.Tables.LifecycleIdempotency().ListByStore(ctx, name, tx)
			lifecycle = rows
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		resp := map[string]any{
			"peer":      cached,
			"lifecycle": lifecycle,
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleListExecutors(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := []PeerEntry{}
		for _, e := range deps.Executors {
			cached, ok := deps.Discovery.GetExecutor(e.Name)
			if !ok {
				cached = PeerEntry{
					Name:                  e.Name,
					Endpoint:              e.Endpoint,
					ObservabilityEndpoint: chooseObsEndpoint(e.ObservabilityEndpoint, e.Endpoint),
					Reachability:          ReachabilityUnreachable,
				}
			}
			entries = append(entries, cached)
		}
		writeJSON(w, http.StatusOK, peerListResponse{Executors: entries})
	}
}

func handleGetExecutor(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if !peerExists(deps.Executors, name) {
			notFound(w, "unknown executor")
			return
		}
		cached, _ := deps.Discovery.GetExecutor(name)
		writeJSON(w, http.StatusOK, map[string]any{"peer": cached})
	}
}

func peerExists(peers []PeerSpec, name string) bool {
	for _, p := range peers {
		if p.Name == name {
			return true
		}
	}
	return false
}

func handleListTemplates(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.TemplateListFilter{}
		if s := r.URL.Query().Get("state"); s != "" {
			filter.State = persistence.TemplateState(s)
		}
		if t := r.URL.Query().Get("tag"); t != "" {
			filter.Tag = t
		}
		var res persistence.PaginatedListResult[persistence.TemplateRow]
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Templates().List(ctx, filter, pag, tx)
			res = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"templates":   res.Rows,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleGetTemplate(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := chi.URLParam(r, "hash")
		var row *persistence.TemplateRow
		var tags []persistence.TemplateTagRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Templates().GetByHash(ctx, hash, tx)
			if err != nil {
				return err
			}
			row = r2
			if row == nil {
				return nil
			}
			t, err := deps.Tables.TemplateTags().ListByTemplate(ctx, hash, tx)
			tags = t
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "template not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"template": row,
			"tags":     tags,
		})
	}
}

func handleListInstances(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.InstanceListFilter{
			TemplateHash: r.URL.Query().Get("template_hash"),
		}
		if v := r.URL.Query().Get("active"); v != "" {
			b := v == "1" || v == "true"
			filter.Active = &b
		}
		var res persistence.PaginatedListResult[persistence.InstanceRow]
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Instances().List(ctx, filter, pag, tx)
			res = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instances":   res.Rows,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleGetInstance(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		var row *persistence.InstanceRow
		var nodes []persistence.NodeRow
		var template *persistence.TemplateRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Instances().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r2
			if row == nil {
				return nil
			}
			ns, err := deps.Tables.Nodes().ListByInstance(ctx, id, tx)
			if err != nil {
				return err
			}
			nodes = ns
			t, _ := deps.Tables.Templates().GetByHash(ctx, row.TemplateHash, tx)
			template = t
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "instance not found")
			return
		}
		graph := computeCascadeGraph(r.Context(), deps, *row, nodes, template)
		writeJSON(w, http.StatusOK, map[string]any{
			"instance":      row,
			"cascade_graph": graph,
		})
	}
}

func handleListFrames(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.FrameListFilter{}
		if v := r.URL.Query().Get("instance_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid instance_id")
				return
			}
			filter.InstanceID = &id
		}
		if s := r.URL.Query().Get("state"); s != "" {
			filter.State = persistence.FrameState(s)
		}
		var res persistence.PaginatedListResult[persistence.FrameRow]
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Frames().ListForObservability(ctx, filter, pag, tx)
			res = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"frames":      res.Rows,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleGetFrame(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid frame id")
			return
		}
		var row *persistence.FrameRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Frames().GetForObservability(ctx, id, tx)
			row = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "frame not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"frame": row})
	}
}

func handleGetNode(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "instance_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		nodeType := chi.URLParam(r, "node_type")
		var nodes []persistence.NodeRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			rows, err := deps.Tables.Nodes().ListByInstance(ctx, id, tx)
			nodes = rows
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		var match *persistence.NodeRow
		for i := range nodes {
			if nodes[i].NodeType == nodeType {
				match = &nodes[i]
				break
			}
		}
		if match == nil {
			notFound(w, "node not found")
			return
		}
		var holdings []persistence.ClaimHandleRow
		_ = inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			h, err := deps.Tables.ClaimHandles().ListByHolderNode(ctx, match.ID, tx)
			holdings = h
			return err
		})
		var eventRes persistence.EventListResult
		_ = inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			e, err := deps.Tables.Events().List(ctx, persistence.EventListFilter{NodeID: &match.ID}, persistence.ListPagination{Limit: 50}, tx)
			eventRes = e
			return err
		})
		// latestBag is the node's most-recent resolved attribute bag — its
		// forensic last-attribute snapshot, the Data map of the row
		// NodeAttributes().GetLatestByNode returns for (node, main run
		// scope). nil → the node has never executed; the key is then an
		// empty object so the surface is stable across executed/unexecuted
		// nodes (the test treats absent and empty-object identically).
		var latestBag map[string]any
		_ = inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Tables.Instances().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return nil
			}
			attrs, err := deps.Tables.NodeAttributes().GetLatestByNode(ctx, match.ID, inst.MainRunScopeID, tx)
			if err != nil {
				return err
			}
			if attrs != nil {
				latestBag = attrs.Data
			}
			return nil
		})
		if latestBag == nil {
			latestBag = map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node":              match,
			"events":            eventRes.Events,
			"holdings":          holdings,
			"latest_attributes": latestBag,
		})
	}
}

func handleListNodeRuns(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.DispatchListFilter{
			State:        r.URL.Query().Get("state"),
			ExecutorName: r.URL.Query().Get("executor_name"),
		}
		if v := r.URL.Query().Get("instance_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid instance_id")
				return
			}
			filter.InstanceID = &id
		}
		res, err := deps.Queue.ListLive(r.Context(), filter, pag)
		if err != nil {
			internalErr(w, err)
			return
		}
		out := make([]map[string]any, 0, len(res.Rows))
		for _, row := range res.Rows {
			state := "pending"
			if row.ClaimedBy != nil {
				state = "claimed"
			}
			out = append(out, map[string]any{
				"id":               row.ID,
				"node_id":          row.NodeID,
				"executor_name":    row.ExecutorName,
				"state":            state,
				"claimed_by":       row.ClaimedBy,
				"enqueued_at":      row.EnqueuedAt,
				"claimed_at":       row.ClaimedAt,
				"last_progress_at": row.LastProgressAt,
				"frame_id":         row.FrameID,
				"required_stores":  row.RequiredStores,
				"async_ack_id":     row.AsyncAckID,
				"tags":             row.Tags,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node_runs":   out,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleGetNodeRun(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid dispatch id")
			return
		}
		// @constraint: direct point-lookup; avoids scanning the live dispatch table.
		match, err := deps.Queue.GetByID(r.Context(), id)
		if err != nil {
			internalErr(w, err)
			return
		}
		if match == nil {
			notFound(w, "dispatch not found (terminal-deleted)")
			return
		}
		state := "pending"
		if match.ClaimedBy != nil {
			state = "claimed"
		}
		// @deliberate: direct (frame_id, node_id) lookup of the matching
		// lock-holder avoids the full holder-list scan; surfaces the
		// dispatch → claim_id link for the dashboard.
		var claimID *shared.UUID
		var instanceID *shared.UUID
		var nodeType string
		_ = inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			if holder, err := deps.Tables.ClaimHandles().GetByFrameAndNode(ctx, match.NodeID, match.FrameID, tx); err == nil && holder != nil {
				claimID = &holder.ID
			}
			// @constraint: also surface instance_id and node_type so the dashboard can
			// resolve the executor's `dispatch_url_template` substitution
			// markers ({dispatch_id}, {instance_id}, {node_type}) per
			// spec §2.2 on the dispatch-detail page.
			if nodeRow, err := deps.Tables.Nodes().Get(ctx, match.NodeID, tx); err == nil && nodeRow != nil {
				id := nodeRow.InstanceID
				instanceID = &id
				nodeType = nodeRow.NodeType
			}
			return nil
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               match.ID,
			"node_id":          match.NodeID,
			"instance_id":      instanceID,
			"node_type":        nodeType,
			"executor_name":    match.ExecutorName,
			"state":            state,
			"claimed_by":       match.ClaimedBy,
			"claimed_at":       match.ClaimedAt,
			"last_progress_at": match.LastProgressAt,
			"enqueued_at":      match.EnqueuedAt,
			"frame_id":         match.FrameID,
			"claim_id":         claimID,
			"async_ack_id":     match.AsyncAckID,
			"tags":             match.Tags,
		})
	}
}

func handleListLockHolders(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.LockHolderListFilter{
			ProducerName:     r.URL.Query().Get("producer_name"),
			HolderSupervisor: r.URL.Query().Get("holder_supervisor_id"),
			NodeType:         r.URL.Query().Get("node_type"),
		}
		if v := r.URL.Query().Get("holder_node_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid holder_node_id")
				return
			}
			filter.HolderNodeID = &id
		}
		if v := r.URL.Query().Get("instance_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid instance_id")
				return
			}
			filter.InstanceID = &id
		}
		var res persistence.PaginatedListResult[persistence.ClaimHandleRow]
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.ClaimHandles().ListForObservability(ctx, filter, pag, tx)
			res = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"lock_holders": res.Rows,
			"next_cursor":  res.NextCursor,
		})
	}
}

func handleGetClaimHandle(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid lock-holder id")
			return
		}
		var row *persistence.ClaimHandleRow
		var holders []persistence.ClaimHolderRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.ClaimHandles().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r2
			if row == nil {
				return nil
			}
			h, err := deps.Tables.ClaimHolders().ListByClaimHandleID(ctx, id, tx)
			if err == nil {
				holders = h
			}
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "lock-holder not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"lock_holder":   row,
			"claim_holders": holders,
		})
	}
}

func handleListEvents(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.EventListFilter{
			Kind: r.URL.Query().Get("kind"),
		}
		if v := r.URL.Query().Get("kind_in"); v != "" {
			parts := strings.Split(v, ",")
			for i, p := range parts {
				parts[i] = strings.TrimSpace(p)
			}
			filter.KindIn = parts
		}
		if v := r.URL.Query().Get("since"); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				badRequest(w, "invalid since timestamp (want RFC3339)")
				return
			}
			filter.Since = &t
		}
		if v := r.URL.Query().Get("instance_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid instance_id")
				return
			}
			filter.InstanceID = &id
		}
		if v := r.URL.Query().Get("node_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid node_id")
				return
			}
			filter.NodeID = &id
		}
		var res persistence.EventListResult
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Events().List(ctx, filter, pag, tx)
			res = r2
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"events":      res.Events,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleSystemHealth(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sups []persistence.SupervisorRow
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			s, err := deps.Tables.Supervisors().List(ctx, tx)
			sups = s
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		// @constraint: Postgres connectivity probe (spec §1.2.6). Driver may be nil
		// in some test fixtures — surface as "unknown" in that case.
		pgStatus := "unknown"
		if deps.Driver != nil {
			pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := deps.Driver.Ping(pingCtx)
			cancel()
			if err == nil {
				pgStatus = "ok"
			} else {
				pgStatus = "degraded: " + err.Error()
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"control_api_status": "ok",
			"postgres_status":    pgStatus,
			"supervisors":        sups,
			"executors":          deps.Discovery.ListExecutors(),
			"stores":             deps.Discovery.ListStores(),
		})
	}
}

func handleSystemSummary(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var counts map[cascade.NodeState]int
		var active, terminated int
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			c, err := deps.Tables.Nodes().CountByState(ctx, tx)
			if err != nil {
				return err
			}
			counts = c
			a, t, err := deps.Tables.Instances().CountByActive(ctx, tx)
			active, terminated = a, t
			return err
		}); err != nil {
			internalErr(w, err)
			return
		}
		nodeCounts := map[string]int{
			string(cascade.NodeStateFresh):   counts[cascade.NodeStateFresh],
			string(cascade.NodeStateStale):   counts[cascade.NodeStateStale],
			string(cascade.NodeStateRunning): counts[cascade.NodeStateRunning],
			string(cascade.NodeStateFailed):  counts[cascade.NodeStateFailed],
		}
		dispatchClaimed, err := deps.Queue.CountLive(r.Context(), persistence.DispatchListFilter{State: "claimed"})
		if err != nil {
			internalErr(w, err)
			return
		}
		dispatchPending, err := deps.Queue.CountLive(r.Context(), persistence.DispatchListFilter{State: "pending"})
		if err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node_counts":          nodeCounts,
			"instances_active":     active,
			"instances_terminated": terminated,
			"node_runs_claimed":    dispatchClaimed,
			"node_runs_pending":    dispatchPending,
		})
	}
}
