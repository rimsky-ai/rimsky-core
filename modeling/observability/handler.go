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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// Deps bundles the dependencies the observability HTTP handlers need.
// The persistence layer is consumed via the typed Store/Queue
// interfaces; no driver-specific subpackages are imported here. The
// peer specs reflect the rimsky.yml `executors:` and `stores:` blocks
// projected via PeerSpec — keeps the package free of core/config.
type Deps struct {
	Store     persistence.Store
	Queue     persistence.Queue
	Driver    persistence.Driver
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
	r.Get("/schedules", handleListSchedules(deps))

	r.Get("/frames", handleListFrames(deps))
	r.Get("/frames/{id}", handleGetFrame(deps))
	r.Get("/nodes/{instance_id}/{node_type}", handleGetNode(deps))
	r.Get("/dispatches", handleListDispatches(deps))
	r.Get("/dispatches/{id}", handleGetDispatch(deps))

	r.Get("/lock-holders", handleListLockHolders(deps))
	r.Get("/lock-holders/{id}", handleGetLockHolder(deps))

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

// ---- Topology ----

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
		lifecycle, err := deps.Store.LifecycleIdempotency().ListByStore(r.Context(), name, nil)
		if err != nil {
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

// ---- Templates / instances / schedules ----

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
		res, err := deps.Store.Templates().List(r.Context(), filter, pag, nil)
		if err != nil {
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
		row, err := deps.Store.Templates().GetByHash(r.Context(), hash, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "template not found")
			return
		}
		tags, err := deps.Store.TemplateTags().ListByTemplate(r.Context(), hash, nil)
		if err != nil {
			internalErr(w, err)
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
		res, err := deps.Store.Instances().List(r.Context(), filter, pag, nil)
		if err != nil {
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
		row, err := deps.Store.Instances().Get(r.Context(), id, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "instance not found")
			return
		}
		nodes, err := deps.Store.Nodes().ListByInstance(r.Context(), id, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		template, _ := deps.Store.Templates().GetByHash(r.Context(), row.TemplateHash, nil)
		graph := computeCascadeGraph(r.Context(), deps, *row, nodes, template)
		writeJSON(w, http.StatusOK, map[string]any{
			"instance":      row,
			"cascade_graph": graph,
		})
	}
}

func handleListSchedules(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.ScheduleListFilter{}
		if v := r.URL.Query().Get("node_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				badRequest(w, "invalid node_id")
				return
			}
			filter.NodeID = &id
		}
		res, err := deps.Store.Schedules().ListForObservability(r.Context(), filter, pag, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schedules":   res.Rows,
			"next_cursor": res.NextCursor,
		})
	}
}

// ---- Runtime state ----

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
		res, err := deps.Store.Frames().ListForObservability(r.Context(), filter, pag, nil)
		if err != nil {
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
		row, err := deps.Store.Frames().GetForObservability(r.Context(), id, nil)
		if err != nil {
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
		nodes, err := deps.Store.Nodes().ListByInstance(r.Context(), id, nil)
		if err != nil {
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
		holdings, err := deps.Store.LockHolders().ListByHolderNode(r.Context(), match.ID, nil)
		if err != nil {
			holdings = nil
		}
		eventRes, err := deps.Store.Events().List(r.Context(), persistence.EventListFilter{NodeID: &match.ID}, persistence.ListPagination{Limit: 50}, nil)
		if err != nil {
			eventRes = persistence.EventListResult{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"node":     match,
			"events":   eventRes.Events,
			"holdings": holdings,
		})
	}
}

func handleListDispatches(deps Deps) http.HandlerFunc {
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
				"id":                row.ID,
				"node_id":           row.NodeID,
				"executor_name":     row.ExecutorName,
				"state":             state,
				"claimed_by":        row.ClaimedBy,
				"enqueued_at":       row.EnqueuedAt,
				"claimed_at":        row.ClaimedAt,
				"last_heartbeat_at": row.LastHeartbeatAt,
				"frame_id":          row.FrameID,
				"required_stores":   row.RequiredStores,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dispatches":  out,
			"next_cursor": res.NextCursor,
		})
	}
}

func handleGetDispatch(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid dispatch id")
			return
		}
		// Direct point-lookup; avoids scanning the live dispatch table.
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
		// Look up matching lock-holder (if any) so the dashboard can
		// follow the dispatch → claim_id link. Direct (frame_id, node_id)
		// lookup avoids the full holder-list scan.
		var claimID *shared.UUID
		if holder, err := deps.Store.LockHolders().GetByFrameAndNode(r.Context(), match.NodeID, match.FrameID, nil); err == nil && holder != nil {
			claimID = &holder.ID
		}
		// Also surface instance_id and node_type so the dashboard can
		// resolve the executor's `dispatch_url_template` substitution
		// markers ({dispatch_id}, {instance_id}, {node_type}) per
		// spec §2.2 on the dispatch-detail page.
		var instanceID *shared.UUID
		var nodeType string
		if nodeRow, err := deps.Store.Nodes().Get(r.Context(), match.NodeID, nil); err == nil && nodeRow != nil {
			id := nodeRow.InstanceID
			instanceID = &id
			nodeType = nodeRow.NodeType
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                match.ID,
			"node_id":           match.NodeID,
			"instance_id":       instanceID,
			"node_type":         nodeType,
			"executor_name":     match.ExecutorName,
			"state":             state,
			"claimed_by":        match.ClaimedBy,
			"claimed_at":        match.ClaimedAt,
			"last_heartbeat_at": match.LastHeartbeatAt,
			"enqueued_at":       match.EnqueuedAt,
			"frame_id":          match.FrameID,
			"claim_id":          claimID,
		})
	}
}

// ---- Lock holders ----

func handleListLockHolders(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pag, err := parsePagination(r)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		filter := persistence.LockHolderListFilter{
			StoreName:        r.URL.Query().Get("store_name"),
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
		res, err := deps.Store.LockHolders().ListForObservability(r.Context(), filter, pag, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"lock_holders": res.Rows,
			"next_cursor":  res.NextCursor,
		})
	}
}

func handleGetLockHolder(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid lock-holder id")
			return
		}
		row, err := deps.Store.LockHolders().Get(r.Context(), id, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		if row == nil {
			notFound(w, "lock-holder not found")
			return
		}
		holders, err := deps.Store.ClaimHolders().ListByLockHolderID(r.Context(), id, nil)
		if err != nil {
			holders = nil
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"lock_holder":   row,
			"claim_holders": holders,
		})
	}
}

// ---- Events ----

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
		res, err := deps.Store.Events().List(r.Context(), filter, pag, nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"events":      res.Events,
			"next_cursor": res.NextCursor,
		})
	}
}

// ---- System ----

func handleSystemHealth(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sups, err := deps.Store.Supervisors().List(r.Context(), nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		// Postgres connectivity probe (spec §1.2.6). Driver may be nil
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
		counts, err := deps.Store.Nodes().CountByState(r.Context(), nil)
		if err != nil {
			internalErr(w, err)
			return
		}
		nodeCounts := map[string]int{
			string(shared.NodeStateFresh):   counts[shared.NodeStateFresh],
			string(shared.NodeStateStale):   counts[shared.NodeStateStale],
			string(shared.NodeStateRunning): counts[shared.NodeStateRunning],
			string(shared.NodeStateFailed):  counts[shared.NodeStateFailed],
		}
		active, terminated, err := deps.Store.Instances().CountByActive(r.Context(), nil)
		if err != nil {
			internalErr(w, err)
			return
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
			"dispatches_claimed":   dispatchClaimed,
			"dispatches_pending":   dispatchPending,
		})
	}
}
