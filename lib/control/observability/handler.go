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

func inTx(ctx context.Context, tables persistence.Tables, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return tables.Transaction(ctx, fn)
}

type Deps struct {
	Tables         persistence.Tables
	Queue          persistence.Queue
	Driver         persistence.Database
	Executors      []PeerSpec
	ClaimProducers []PeerSpec
	Discovery      *Discovery
}

func Routes(r chi.Router, deps Deps) {
	r.Get("/claim-producers", handleListClaimProducers(deps))
	r.Get("/claim-producers/{name}", handleGetClaimProducer(deps))
	r.Get("/executors", handleListExecutors(deps))
	r.Get("/executors/{name}", handleGetExecutor(deps))

	r.Get("/templates", handleListTemplates(deps))
	r.Get("/templates/{hash}", handleGetTemplate(deps))
	r.Get("/instances", handleListInstances(deps))
	r.Get("/instances/{id}", handleGetInstance(deps))

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

func handleListClaimProducers(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := []PeerEntry{}
		seen := map[string]bool{}
		for _, e := range deps.ClaimProducers {
			cached, ok := deps.Discovery.GetClaimProducer(e.Name)
			if !ok {
				cached = unreachablePeerEntry(e)
			}
			entries = append(entries, cached)
			seen[e.Name] = true
		}
		for _, cached := range deps.Discovery.ListClaimProducers() {
			if seen[cached.Name] {
				continue
			}
			entries = append(entries, cached)
		}
		writeJSON(w, http.StatusOK, map[string]any{"claim_producers": entries})
	}
}

func handleGetClaimProducer(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		cached, ok := deps.Discovery.GetClaimProducer(name)
		if !ok {
			spec, declared := findPeerSpec(deps.ClaimProducers, name)
			if !declared {
				notFound(w, "unknown claim producer")
				return
			}
			cached = unreachablePeerEntry(spec)
		}
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
		seen := map[string]bool{}
		for _, e := range deps.Executors {
			cached, ok := deps.Discovery.GetExecutor(e.Name)
			if !ok {
				cached = unreachablePeerEntry(e)
			}
			entries = append(entries, cached)
			seen[e.Name] = true
		}
		for _, cached := range deps.Discovery.ListExecutors() {
			if seen[cached.Name] {
				continue
			}
			entries = append(entries, cached)
		}
		writeJSON(w, http.StatusOK, map[string]any{"executors": entries})
	}
}

func handleGetExecutor(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		cached, ok := deps.Discovery.GetExecutor(name)
		if !ok {
			spec, declared := findPeerSpec(deps.Executors, name)
			if !declared {
				notFound(w, "unknown executor")
				return
			}
			cached = unreachablePeerEntry(spec)
		}
		writeJSON(w, http.StatusOK, map[string]any{"peer": cached})
	}
}

func findPeerSpec(peers []PeerSpec, name string) (PeerSpec, bool) {
	for _, p := range peers {
		if p.Name == name {
			return p, true
		}
	}
	return PeerSpec{}, false
}

func unreachablePeerEntry(spec PeerSpec) PeerEntry {
	return PeerEntry{
		Name:                  spec.Name,
		Endpoint:              spec.Endpoint,
		ObservabilityEndpoint: chooseObsEndpoint(spec.ObservabilityEndpoint, spec.Endpoint),
		Reachability:          ReachabilityUnreachable,
	}
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
		var graph []CascadeNode
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			r2, err := deps.Tables.Instances().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			row = r2
			if row == nil {
				return nil
			}
			nodes, err := deps.Tables.Nodes().ListByInstance(ctx, id, tx)
			if err != nil {
				return err
			}
			template, err := deps.Tables.Templates().GetByHash(ctx, row.TemplateHash, tx)
			if err != nil {
				return err
			}
			g, err := computeCascadeGraph(ctx, deps, tx, nodes, template)
			if err != nil {
				return err
			}
			graph = g
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "instance not found")
			return
		}
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
		if err := persistence.ApplyFrameStateQueryParam(&filter, r.URL.Query().Get("state")); err != nil {
			badRequest(w, err.Error())
			return
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

		var (
			match     *persistence.NodeRow
			holdings  []persistence.ClaimHandleRow
			eventRes  persistence.EventListResult
			latestBag map[string]any
			summary   persistence.NodeRunSummary
		)
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			nodes, err := deps.Tables.Nodes().ListByInstance(ctx, id, tx)
			if err != nil {
				return err
			}
			for i := range nodes {
				if nodes[i].NodeType == nodeType {
					m := nodes[i]
					match = &m
					break
				}
			}
			if match == nil {
				return nil
			}
			h, err := deps.Tables.ClaimHandles().ListByHolderNode(ctx, match.ID, tx)
			if err != nil {
				return err
			}
			holdings = h
			e, err := deps.Tables.Events().List(ctx, persistence.EventListFilter{NodeID: &match.ID}, persistence.ListPagination{Limit: 50}, tx)
			if err != nil {
				return err
			}
			eventRes = e
			latest, err := deps.Tables.Nodes().GetLatestRunForNode(ctx, tx, match.ID)
			if err != nil {
				return err
			}
			if latest != nil {
				attrs, err := deps.Tables.NodeAttributes().GetLatestByNode(ctx, match.ID, latest.RunScopeID, tx)
				if err != nil {
					return err
				}
				if attrs != nil {
					latestBag = attrs.Data
				}
			}
			s, err := deps.Tables.Nodes().GetRunSummary(ctx, match.ID, tx)
			if err != nil {
				return err
			}
			summary = s
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		if match == nil {
			notFound(w, "node not found")
			return
		}
		if latestBag == nil {
			latestBag = map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node":              match,
			"run_summary":       summary,
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
		stateFilter := r.URL.Query().Get("state")
		switch stateFilter {
		case "", "pending", "claimed":
		default:
			badRequest(w, "invalid state (pending or claimed required)")
			return
		}
		filter := persistence.DispatchListFilter{
			State:        stateFilter,
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
			out = append(out, map[string]any{
				"id":                       row.ID,
				"node_id":                  row.NodeID,
				"executor_name":            row.ExecutorName,
				"state":                    row.State,
				"claimed_by":               row.ClaimedBy,
				"enqueued_at":              row.EnqueuedAt,
				"claimed_at":               row.ClaimedAt,
				"last_progress_at":         row.LastProgressAt,
				"frame_id":                 row.FrameID,
				"required_claim_producers": row.RequiredClaimProducers,
				"async_ack_id":             row.AsyncAckID,
				"tags":                     row.Tags,
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
		match, err := deps.Queue.GetByID(r.Context(), id)
		if err != nil {
			internalErr(w, err)
			return
		}
		if match == nil {
			notFound(w, "dispatch not found in the live queue (this view only covers pending/stale/running/held/parked runs; a settled run is still readable via the lineage surface)")
			return
		}
		var claimID *shared.UUID
		var instanceID *shared.UUID
		var nodeType string
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			holder, err := deps.Tables.ClaimHandles().GetByFrameAndNode(ctx, match.NodeID, match.FrameID, tx)
			if err != nil {
				return err
			}
			if holder != nil {
				claimID = &holder.ID
			}
			nodeRow, err := deps.Tables.Nodes().Get(ctx, match.NodeID, tx)
			if err != nil {
				return err
			}
			if nodeRow != nil {
				id := nodeRow.InstanceID
				instanceID = &id
				nodeType = nodeRow.NodeType
			}
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                       match.ID,
			"node_id":                  match.NodeID,
			"instance_id":              instanceID,
			"node_type":                nodeType,
			"executor_name":            match.ExecutorName,
			"state":                    match.State,
			"claimed_by":               match.ClaimedBy,
			"claimed_at":               match.ClaimedAt,
			"last_progress_at":         match.LastProgressAt,
			"enqueued_at":              match.EnqueuedAt,
			"frame_id":                 match.FrameID,
			"required_claim_producers": match.RequiredClaimProducers,
			"claim_id":                 claimID,
			"async_ack_id":             match.AsyncAckID,
			"tags":                     match.Tags,
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
		filter := persistence.ClaimHandleListFilter{
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
		dbStatus := "unknown"
		if deps.Driver != nil {
			pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := deps.Driver.Ping(pingCtx)
			cancel()
			if err == nil {
				dbStatus = "ok"
			} else {
				dbStatus = "degraded: " + err.Error()
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"control_api_status": "ok",
			"database_status":    dbStatus,
			"supervisors":        sups,
			"executors":          deps.Discovery.ListExecutors(),
			"claim_producers":    deps.Discovery.ListClaimProducers(),
		})
	}
}

func handleSystemSummary(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var counts map[cascade.NodeState]int
		var active, terminated, nodesTotal, nodesWithRuns int
		if err := inTx(r.Context(), deps.Tables, func(ctx context.Context, tx persistence.Tx) error {
			c, err := deps.Tables.Nodes().CountByState(ctx, tx)
			if err != nil {
				return err
			}
			counts = c
			a, t, err := deps.Tables.Instances().CountByActive(ctx, tx)
			if err != nil {
				return err
			}
			active, terminated = a, t
			n, err := deps.Tables.Nodes().CountAllNodes(ctx, tx)
			if err != nil {
				return err
			}
			nodesTotal = n
			wr, err := deps.Tables.Nodes().CountDistinctNodesWithRuns(ctx, tx)
			if err != nil {
				return err
			}
			nodesWithRuns = wr
			return nil
		}); err != nil {
			internalErr(w, err)
			return
		}
		runsTotal := counts[cascade.NodeStateFresh] + counts[cascade.NodeStateStale] +
			counts[cascade.NodeStateRunning] + counts[cascade.NodeStatePending] +
			counts[cascade.NodeStateHeld] + counts[cascade.NodeStateParked] +
			counts[cascade.NodeStateFailed]
		nodesWithoutRuns := nodesTotal - nodesWithRuns
		if nodesWithoutRuns < 0 {
			nodesWithoutRuns = 0
		}
		nodeRunsByState := map[string]int{
			string(cascade.NodeStateFresh):   counts[cascade.NodeStateFresh],
			string(cascade.NodeStatePending): counts[cascade.NodeStatePending],
			string(cascade.NodeStateStale):   counts[cascade.NodeStateStale],
			string(cascade.NodeStateRunning): counts[cascade.NodeStateRunning],
			string(cascade.NodeStateHeld):    counts[cascade.NodeStateHeld],
			string(cascade.NodeStateParked):  counts[cascade.NodeStateParked],
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
			"node_runs_by_state":   nodeRunsByState,
			"nodes_total":          nodesTotal,
			"nodes_without_runs":   nodesWithoutRuns,
			"node_runs_total":      runsTotal,
			"instances_active":     active,
			"instances_terminated": terminated,
			"node_runs_claimed":    dispatchClaimed,
			"node_runs_pending":    dispatchPending,
		})
	}
}
