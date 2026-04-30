// instances.go — POST /instances, GET /instances, GET /instances/:id_or_key,
// DELETE /instances/:id_or_key. Includes the instance-factory logic that
// provisions instance + nodes + schedules from a template.
//
// Provisioning flow (post-stores-redesign):
//  1. Validate consumer_key uniqueness via InstanceStore.Create. The params
//     map is stored verbatim on rimsky_instances.params; both single-brace
//     `{params.x}` (instantiation) and double-brace `{{params.x}}` (dispatch)
//     consumers re-read this row, so there is no per-instance baked node
//     config to apply substitutions to (spec §10.1).
//  2. Allocate node UUIDs up-front so dependencies[] can be rewritten from
//     node-type names to node IDs.
//  3. Create one node row per template node.
//  4. For schedule nodes, compute the next cron fire time and register.
//  5. For root executor nodes (no deps, has executor), enqueue the first
//     dispatch row with required_stores denormalised from the template
//     so the supervisor-pool predicate (spec §6.2) can filter.
//
// Resources / concurrency-tags from the previous shape were retired in
// the redesign (spec §11.3); their replacements (stores, locks) live
// entirely on the template and are read by the supervisor at dispatch
// time, not baked here.
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

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/scheduler"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
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

// provisionInstance is the instance-factory routine. Runs the create
// sequence in single-shot calls (no transaction wrapper) — the instance
// row is created first, then nodes/schedules/dispatch rows are appended;
// callers can DELETE the instance to roll back on partial failure.
//
// Per the stores redesign:
//   - rimsky_instances.params is stored verbatim. Both `{params.x}`
//     (instantiation, single-brace) and `{{params.x}}` (dispatch,
//     double-brace) consumers re-read this row, so there is no
//     instantiation-time substitution to apply here (spec §10.1).
//   - Concurrency tags and owned/read resources are gone (spec §11.3);
//     stores/locks live on the template and are resolved at dispatch.
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

	// Phase 1: create nodes (Create defaults to 'fresh' per migration 002 +
	// spec §3.1) + register schedules. Phase 2 enqueues an initial frame
	// for each root.
	for _, def := range tpl.Spec.Nodes {
		nodeID := nodeIDs[def.Type]

		// Map dependency node-types to UUIDs.
		depUUIDs := make([]shared.UUID, 0, len(def.Dependencies))
		for _, depType := range def.Dependencies {
			depID, ok := nodeIDs[depType]
			if !ok {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: unknown dependency %q referenced by node %q", depType, def.Type)
			}
			depUUIDs = append(depUUIDs, depID)
		}

		// Create node row.
		if _, err := deps.Storage.Nodes().Create(ctx, storage.NodeCreateInput{
			ID:           nodeID,
			InstanceID:   inst.ID,
			NodeType:     def.Type,
			Executor:     def.Executor,
			ScheduleCron: def.Schedule,
			Dependencies: depUUIDs,
		}, nil); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
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
	}

	// Phase 2: enqueue an initial frame for each root node (no deps).
	// Both executor-backed and pure-cascade roots are covered.
	for _, def := range tpl.Spec.Nodes {
		if len(def.Dependencies) != 0 {
			continue
		}
		nodeID := nodeIDs[def.Type]
		if err := deps.Storage.Transaction(ctx, func(ctx context.Context, stx storage.Tx) error {
			pgT, err := pgstorage.PgxTxFromStorage(stx)
			if err != nil {
				return err
			}
			_, err = frame.EnqueueOrCoalesce(ctx, pgT, inst.ID, nodeID)
			return err
		}); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: enqueue root node %q: %w", def.Type, err)
		}
	}

	return createInstanceResponse{
		InstanceID:  inst.ID.String(),
		ConsumerKey: inst.ConsumerKey,
		NodeCount:   len(tpl.Spec.Nodes),
	}, nil
}
