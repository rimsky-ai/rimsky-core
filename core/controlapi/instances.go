// instances.go — POST /instances, GET /instances, GET /instances/:id_or_key,
// DELETE /instances/:id_or_key. Includes the instance-factory logic that
// provisions instance + nodes + resources + schedules from a template.
//
// Provisioning flow (spec §5.8):
//  1. Validate consumer_key uniqueness via InstanceStore.Create.
//  2. Allocate node UUIDs up-front so dependencies[] can be rewritten from
//     node-type names to node IDs.
//  3. For each node, resolve {instance_id} / {consumer_key} / {params.<key>}
//     placeholders against the UNREDACTED params (redaction only applies at
//     egress). Create owned resources and instantiate them via their
//     registered resource.Factory.
//  4. For schedule nodes, compute the next cron fire time and register.
//  5. For root executor nodes (no deps, has executor), enqueue the first
//     dispatch row so the supervisor pool can pick them up.
package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type createInstanceRequest struct {
	TemplateID  string         `json:"template_id"`
	ConsumerKey string         `json:"consumer_key"`
	Params      map[string]any `json:"params,omitempty"`
}

type createInstanceResponse struct {
	InstanceID  string `json:"instance_id"`
	ConsumerKey string `json:"consumer_key"`
	NodeCount   int    `json:"node_count"`
}

type instanceItem struct {
	ID          string         `json:"id"`
	TemplateID  string         `json:"template_id"`
	ConsumerKey string         `json:"consumer_key"`
	Params      map[string]any `json:"params"`
	CreatedAt   time.Time      `json:"created_at"`
}

func toInstanceItem(r storage.InstanceRow, redact []string) instanceItem {
	return instanceItem{
		ID:          r.ID.String(),
		TemplateID:  r.TemplateID.String(),
		ConsumerKey: r.ConsumerKey,
		Params:      ApplyParamsRedact(r.Params, redact),
		CreatedAt:   r.CreatedAt,
	}
}

// registerInstancesRoutes wires the /instances group.
func registerInstancesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances", handleCreateInstance(deps))
	r.Get("/instances", handleListInstances(deps))
	r.Get("/instances/{idOrKey}", handleGetInstance(deps))
	r.Delete("/instances/{idOrKey}", handleDeleteInstance(deps))
}

func handleCreateInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body createInstanceRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if strings.TrimSpace(body.ConsumerKey) == "" {
			badRequest(w, "consumer_key is required")
			return
		}
		tplID, err := uuid.Parse(body.TemplateID)
		if err != nil {
			badRequest(w, "invalid template_id")
			return
		}
		tpl, err := deps.Storage.Templates().Get(req.Context(), tplID, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if tpl == nil {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		params := body.Params
		if params == nil {
			params = map[string]any{}
		}

		out, err := provisionInstance(req.Context(), deps, tpl, body.ConsumerKey, params)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	}
}

func handleListInstances(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		filter := storage.InstanceListFilter{
			ConsumerKey: q.Get("consumer_key"),
		}
		if s := q.Get("template_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				badRequest(w, "invalid template_id")
				return
			}
			filter.TemplateID = id
		}
		pag := storage.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		page, err := deps.Storage.Instances().List(req.Context(), filter, pag, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		// Redact per-template — look up each row's template to grab its
		// params_redact slice.
		items := make([]instanceItem, 0, len(page.Rows))
		redactCache := map[shared.UUID][]string{}
		for _, r := range page.Rows {
			redact, ok := redactCache[r.TemplateID]
			if !ok {
				tpl, err := deps.Storage.Templates().Get(req.Context(), r.TemplateID, nil)
				if err == nil && tpl != nil {
					redact = tpl.Spec.ParamsRedact
				}
				redactCache[r.TemplateID] = redact
			}
			items = append(items, toInstanceItem(r, redact))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instances":   items,
			"next_cursor": page.NextCursor,
		})
	}
}

func handleGetInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		tpl, _ := deps.Storage.Templates().Get(req.Context(), inst.TemplateID, nil)
		var redact []string
		if tpl != nil {
			redact = tpl.Spec.ParamsRedact
		}
		writeJSON(w, http.StatusOK, toInstanceItem(*inst, redact))
	}
}

func handleDeleteInstance(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		if err := deps.Storage.Instances().Delete(req.Context(), inst.ID, nil); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

// provisionInstance is the instance-factory routine (spec §5.8). Runs the
// full create sequence in a single transaction where possible; resource
// creation inside factories may use its own transactional boundary, so we
// create the instance row first and let downstream failures bubble up — the
// caller can DELETE the instance to roll back.
func provisionInstance(
	ctx context.Context,
	deps AppDeps,
	tpl *storage.TemplateRow,
	consumerKey string,
	params map[string]any,
) (createInstanceResponse, error) {
	// Create instance row (fails with ErrConsumerKeyConflict if duplicate).
	inst, err := deps.Storage.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID:  tpl.ID,
		ConsumerKey: consumerKey,
		Params:      params,
	}, nil)
	if err != nil {
		return createInstanceResponse{}, err
	}

	// Allocate one UUID per node up-front so dependencies[] can be rewritten.
	nodeIDs := make(map[string]shared.UUID, len(tpl.Spec.Nodes))
	for _, def := range tpl.Spec.Nodes {
		nodeIDs[def.Type] = uuid.New()
	}

	for _, def := range tpl.Spec.Nodes {
		nodeID := nodeIDs[def.Type]

		// Resolve placeholders.
		resolvedTags := make([]string, 0, len(def.ConcurrencyTags))
		for _, tag := range def.ConcurrencyTags {
			resolvedTags = append(resolvedTags, resolvePlaceholders(tag, inst.ID, consumerKey, params))
		}

		// Map dependency node-types to UUIDs.
		deps_uuids := make([]shared.UUID, 0, len(def.Dependencies))
		for _, depType := range def.Dependencies {
			depID, ok := nodeIDs[depType]
			if !ok {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: unknown dependency %q referenced by node %q", depType, def.Type)
			}
			deps_uuids = append(deps_uuids, depID)
		}

		// Create node row.
		if _, err := deps.Storage.Nodes().Create(ctx, storage.NodeCreateInput{
			ID:              nodeID,
			InstanceID:      inst.ID,
			NodeType:        def.Type,
			Executor:        def.Executor,
			ScheduleCron:    def.Schedule,
			Dependencies:    deps_uuids,
			ConcurrencyTags: resolvedTags,
		}, nil); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
		}

		// Create owned resources.
		for _, rdef := range def.OwnsResources {
			resolvedPath := make([]string, 0, len(rdef.Path))
			for _, seg := range rdef.Path {
				resolvedPath = append(resolvedPath, resolvePlaceholders(seg, inst.ID, consumerKey, params))
			}
			keep := 2
			if rdef.Retention != nil && rdef.Retention.KeepVersions > 0 {
				keep = rdef.Retention.KeepVersions
			}
			rrow, err := deps.Storage.Resources().Create(ctx, storage.ResourceCreateInput{
				ResourcePath: resolvedPath,
				OwnerNodeID:  nodeID,
				KeepVersions: keep,
			}, nil)
			if err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: create resource %v: %w", resolvedPath, err)
			}
			// Instantiate via factory (consistency check that the registered
			// impl can honour the config). We do not retain the returned
			// resource.Resource — the supervisor re-creates it at execution
			// time from the stored config.
			factory, ok := deps.ResourceFactories.Get(rdef.Implementation)
			if !ok {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: unknown resource implementation %q", rdef.Implementation)
			}
			cfg := resolveConfigPlaceholders(rdef.Config, inst.ID, consumerKey, params)
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["_resource_id"] = rrow.ID.String()
			cfg["_path"] = resolvedPath
			cfg["_owner_node_id"] = nodeID.String()
			if _, err := factory.Create(cfg, nil, nil); err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: resource factory %q: %w", rdef.Implementation, err)
			}
		}

		// Register schedule if declared.
		if def.Schedule != "" {
			next, err := scheduler.NextFireAt(def.Schedule, deps.Clock.Now())
			if err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: invalid cron on node %q: %w", def.Type, err)
			}
			if err := deps.Storage.Schedules().Register(ctx, storage.ScheduleRegisterInput{
				NodeID:     nodeID,
				CronExpr:   def.Schedule,
				NextFireAt: next,
			}, nil); err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: register schedule on node %q: %w", def.Type, err)
			}
		}

		// Root executor nodes (no deps, has executor) are ready immediately.
		if def.Executor != "" && len(def.Dependencies) == 0 {
			if err := deps.Queue.Enqueue(ctx, queue.DispatchRequest{
				NodeID:          nodeID,
				ExecutorName:    def.Executor,
				ConcurrencyTags: resolvedTags,
				EnqueuedAt:      deps.Clock.Now(),
			}); err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: enqueue root node %q: %w", def.Type, err)
			}
		}
	}

	return createInstanceResponse{
		InstanceID:  inst.ID.String(),
		ConsumerKey: inst.ConsumerKey,
		NodeCount:   len(tpl.Spec.Nodes),
	}, nil
}

// resolvePlaceholders substitutes {instance_id}, {consumer_key}, and
// {params.<key>} per spec §5.8. Unknown placeholders are left untouched (the
// template validator rejects bad placeholders before deploy, so this path is
// best-effort at runtime).
func resolvePlaceholders(s string, instanceID shared.UUID, consumerKey string, params map[string]any) string {
	s = strings.ReplaceAll(s, "{instance_id}", instanceID.String())
	s = strings.ReplaceAll(s, "{consumer_key}", consumerKey)
	for k, v := range params {
		token := "{params." + k + "}"
		if !strings.Contains(s, token) {
			continue
		}
		s = strings.ReplaceAll(s, token, fmt.Sprintf("%v", v))
	}
	return s
}

// resolveConfigPlaceholders deep-walks a config map, replacing placeholders in
// string leaves. Non-string leaves pass through unchanged.
func resolveConfigPlaceholders(v any, instanceID shared.UUID, consumerKey string, params map[string]any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return walkConfig(m, instanceID, consumerKey, params).(map[string]any)
}

func walkConfig(v any, instanceID shared.UUID, consumerKey string, params map[string]any) any {
	switch t := v.(type) {
	case string:
		return resolvePlaceholders(t, instanceID, consumerKey, params)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = walkConfig(vv, instanceID, consumerKey, params)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = walkConfig(vv, instanceID, consumerKey, params)
		}
		return out
	default:
		return v
	}
}
