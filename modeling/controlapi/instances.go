// instances.go — POST /instances, GET /instances, GET /instances/:id_or_key,
// DELETE /instances/:id_or_key. Includes the instance-factory logic that
// provisions instance + nodes + schedules from a template.
//
// Provisioning flow (post-control-plane v1):
//  1. Lock the template row FOR UPDATE; reject if state ≠ 'deployed'.
//     Idempotently resolve a pre-existing (template_hash, instance_key)
//     row by returning its instance_id (spec §2.2).
//  2. Validate instance_key uniqueness via InstanceStore.Create. The
//     params map is stored verbatim on rimsky_instances.params; both
//     single-brace `{params.x}` (instantiation) and double-brace
//     `{{params.x}}` (dispatch) consumers re-read this row, so there is
//     no per-instance baked node config to apply substitutions to
//     (spec §10.1).
//  3. Allocate node UUIDs up-front so dependencies[] can be rewritten
//     from node-type names to node IDs.
//  4. Create one node row per template node.
//  5. For schedule nodes, compute the next cron fire time and register.
//  6. For root nodes (no deps), enqueue the first frame so the
//     scheduler can advance them.
//  7. After commit, fire OnInstanceCreated against every store named
//     in the template's spec (spec §5.4 / §5.5).
//
// Resources / concurrency-tags from the previous shape were retired in
// the redesign (spec §11.3); their replacements (stores, locks) live
// entirely on the template and are read by the supervisor at dispatch
// time, not baked here.
package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/frame"
	nodepkg "github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scheduler"
	"github.com/fallguy/rimsky/modeling/shared"
)

type createInstanceRequest struct {
	Template    string         `json:"template"` // tag or hash; per spec §2.2.
	InstanceKey *string        `json:"instance_key,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

type createInstanceResponse struct {
	InstanceID   string  `json:"instance_id"`
	TemplateHash string  `json:"template_hash"`
	InstanceKey  *string `json:"instance_key,omitempty"`
	NodeCount    int     `json:"node_count"`
}

type instanceItem struct {
	ID           string         `json:"id"`
	TemplateHash string         `json:"template_hash"`
	InstanceKey  *string        `json:"instance_key,omitempty"`
	Params       map[string]any `json:"params"`
	CreatedAt    time.Time      `json:"created_at"`
	TerminatedAt *time.Time     `json:"terminated_at,omitempty"`
}

func toInstanceItem(r persistence.InstanceRow, redact []string) instanceItem {
	return instanceItem{
		ID:           r.ID.String(),
		TemplateHash: r.TemplateHash,
		InstanceKey:  r.InstanceKey,
		Params:       ApplyParamsRedact(r.Params, redact),
		CreatedAt:    r.CreatedAt,
		TerminatedAt: r.TerminatedAt,
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
		// Empty-string instance_key is treated as absent (spec §2.2 — the
		// nullable column is the absence sentinel; an empty string would
		// participate in the unique index and break further inserts with
		// no key).
		if body.InstanceKey != nil && *body.InstanceKey == "" {
			body.InstanceKey = nil
		}
		if strings.TrimSpace(body.Template) == "" {
			badRequest(w, "template is required (tag or hash)")
			return
		}
		hash, err := resolveTagOrHash(req.Context(), deps, body.Template)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		params := body.Params
		if params == nil {
			params = map[string]any{}
		}

		// Spec §2.2 acquisition: lock the template row FOR UPDATE,
		// validate state == 'deployed', then idempotently resolve the
		// (template_hash, instance_key) collision to return the existing
		// row. Capture the locked spec for the post-commit fan-out so we
		// don't have to re-read.
		var (
			tplSpec    nodepkg.TemplateSpec
			respOut    createInstanceResponse
			existedKey bool
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			row, err := deps.Persist.Templates().LockForUpdate(ctx, hash, tx)
			if err != nil {
				return err
			}
			if row == nil {
				return shared.ErrTemplateNotFound
			}
			if row.State != persistence.TemplateStateDeployed {
				return shared.Wrap(shared.ErrTemplateValidation,
					"instance creation requires template state 'deployed'",
					map[string]any{"template_hash": hash, "state": string(row.State)})
			}
			tplSpec = row.Spec
			// Idempotent resolution on (template_hash, instance_key).
			if body.InstanceKey != nil {
				existing, err := deps.Persist.Instances().GetByInstanceKey(ctx, hash, *body.InstanceKey, tx)
				if err != nil {
					return err
				}
				if existing != nil {
					existedKey = true
					respOut = createInstanceResponse{
						InstanceID:   existing.ID.String(),
						TemplateHash: existing.TemplateHash,
						InstanceKey:  existing.InstanceKey,
						NodeCount:    len(row.Spec.Nodes),
					}
					return nil
				}
			}
			provisioned, err := provisionInstanceTx(ctx, deps, tx, row, body.InstanceKey, params)
			if err != nil {
				return err
			}
			respOut = provisioned
			return nil
		})
		if err != nil {
			if errors.Is(err, shared.ErrTemplateNotFound) {
				notFoundResp(w, shared.ErrTemplateNotFound.Error())
				return
			}
			if errors.Is(err, shared.ErrTemplateValidation) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		// Spec §2.2 / §5.4 mandate firing OnInstanceCreated on every call,
		// including idempotent re-creates: the fan-out helper is already
		// progress-preserving (skip-if-already-at-target via the
		// rimsky_lifecycle_idempotency bookkeeping), so a partial-failure on
		// the original creation will be resumed here.
		if _, perStore, err := FanOutInstanceEvent(req.Context(), deps,
			EventInstanceCreated, hash, respOut.InstanceID, tplSpec); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "instance lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}
		status := http.StatusCreated
		if existedKey {
			status = http.StatusOK
		}
		writeJSON(w, status, respOut)
	}
}

func handleListInstances(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		filter := persistence.InstanceListFilter{
			TemplateHash: q.Get("template_hash"),
		}
		if v := q.Get("active"); v != "" {
			b := v == "1" || v == "true"
			filter.Active = &b
		}
		pag := persistence.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		page, err := deps.Persist.Instances().List(req.Context(), filter, pag, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		// Redact per-template — look up each row's template to grab its
		// params_redact slice.
		items := make([]instanceItem, 0, len(page.Rows))
		redactCache := map[string][]string{}
		for _, r := range page.Rows {
			redact, ok := redactCache[r.TemplateHash]
			if !ok {
				tpl, err := deps.Persist.Templates().GetByHash(req.Context(), r.TemplateHash, nil)
				if err == nil && tpl != nil {
					redact = tpl.Spec.ParamsRedact
				}
				redactCache[r.TemplateHash] = redact
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
		tpl, _ := deps.Persist.Templates().GetByHash(req.Context(), inst.TemplateHash, nil)
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
		// Spec §2.4: instance deletion is only sanctioned after the
		// instance has reached terminal state (terminated_at IS NOT NULL).
		// The terminator worker fires OnInstanceTerminated as soon as the
		// row terminates; a DELETE on an active instance would bypass that
		// trigger and risks firing the lifecycle event against a still-
		// running instance.
		if inst.TerminatedAt == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "instance is not in terminal state; wait for terminated_at to be set",
			})
			return
		}
		// Spec §1.6 / §5.5: fire OnInstanceTerminated to every store
		// referenced by the instance's template before deleting the row.
		// FanOutInstanceEvent deletes per-store lifecycle rows on
		// success and surfaces partial failures so the operator can
		// retry; surviving lifecycle rows are picked up by the
		// terminator if the instance row remains. We only proceed with
		// the row delete after fan-out fully succeeds.
		tpl, err := deps.Persist.Templates().GetByHash(req.Context(), inst.TemplateHash, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if tpl != nil {
			if _, perStore, err := FanOutInstanceEvent(req.Context(), deps,
				EventInstanceTerminated, inst.TemplateHash, inst.ID.String(), tpl.Spec); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":   "instance lifecycle fan-out failed",
					"details": perStore,
				})
				return
			}
		} else {
			// Template gone (e.g. force-deleted): fall back to fanning
			// out via the recorded lifecycle rows so any per-instance
			// state in stores is still settled before we drop the row.
			if err := fanOutInstanceTerminatedFromLifecycleRows(req.Context(), deps,
				inst.TemplateHash, inst.ID.String()); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error": err.Error(),
				})
				return
			}
		}
		// Defensive: per spec §1.6 any remaining lifecycle rows for
		// scope='instance' on this id are deleted with the instance.
		if err := deps.Persist.LifecycleIdempotency().DeleteByScope(req.Context(),
			persistence.LifecycleIdempotencyScopeInstance, inst.ID.String(), nil); err != nil {
			writeError(w, err)
			return
		}
		if err := deps.Persist.Instances().Delete(req.Context(), inst.ID, nil); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

// fanOutInstanceTerminatedFromLifecycleRows fires OnInstanceTerminated
// against the stores recorded in rimsky_lifecycle_idempotency for the given
// instance, deleting each row on success. Used as a fallback path when
// the bound template row is gone (e.g. force-deleted) and we cannot
// recover the spec's referenced-stores list.
//
// Unknown-store rows (the lifecycle row references a store no longer
// configured on this process) are skipped with a warning and the row is
// deleted regardless — the alternative is to wedge instance deletion
// permanently on a configuration drift the operator may have intended.
// Mirrors the terminator's fanOutFromLifecycleRows behavior so the two
// callers stay consistent.
func fanOutInstanceTerminatedFromLifecycleRows(
	ctx context.Context,
	deps AppDeps,
	templateHash, instanceID string,
) error {
	rows, err := deps.Persist.LifecycleIdempotency().ListByScope(ctx,
		persistence.LifecycleIdempotencyScopeInstance, instanceID, nil)
	if err != nil {
		return fmt.Errorf("lifecycle row list: %w", err)
	}
	if deps.LifecycleSubs == nil {
		return errors.New("lifecycle subscriber registry not initialized")
	}
	for _, r := range rows {
		s, ok := deps.LifecycleSubs.Get(r.StoreRegistrationName)
		if !ok {
			deps.Logger.Warn("instance_delete.unknown_lifecycle_subscriber",
				"instance_id", instanceID,
				"template_hash", templateHash,
				"peer_name", r.StoreRegistrationName)
			if err := deps.Persist.LifecycleIdempotency().Delete(ctx,
				r.StoreRegistrationName,
				persistence.LifecycleIdempotencyScopeInstance,
				instanceID, nil); err != nil {
				return fmt.Errorf("delete lifecycle row %q: %w", r.StoreRegistrationName, err)
			}
			continue
		}
		if err := s.OnInstanceTerminated(ctx, templateHash, instanceID); err != nil {
			return fmt.Errorf("peer %q OnInstanceTerminated: %w", r.StoreRegistrationName, err)
		}
		if err := deps.Persist.LifecycleIdempotency().Delete(ctx,
			r.StoreRegistrationName,
			persistence.LifecycleIdempotencyScopeInstance,
			instanceID, nil); err != nil {
			return fmt.Errorf("delete lifecycle row %q: %w", r.StoreRegistrationName, err)
		}
	}
	return nil
}

// provisionInstanceTx is the instance-factory routine. Runs the create
// sequence inside the supplied tx (the same tx that locked the template
// row FOR UPDATE per spec §2.2) so the entire instance + nodes +
// schedules + initial frames are atomic with the deployed-state check.
//
// Per the stores redesign:
//   - rimsky_instances.params is stored verbatim. Both `{params.x}`
//     (instantiation, single-brace) and `{{params.x}}` (dispatch,
//     double-brace) consumers re-read this row, so there is no
//     instantiation-time substitution to apply here (spec §10.1).
//   - Concurrency tags and owned/read resources are gone (spec §11.3);
//     stores/locks live on the template and are resolved at dispatch.
func provisionInstanceTx(
	ctx context.Context,
	deps AppDeps,
	tx persistence.Tx,
	tpl *persistence.TemplateRow,
	instanceKey *string,
	params map[string]any,
) (createInstanceResponse, error) {
	// Create instance row (fails with ErrInstanceKeyConflict if duplicate).
	inst, err := deps.Persist.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           uuid.New(),
		TemplateHash: tpl.ID,
		InstanceKey:  instanceKey,
		Params:       params,
	}, tx)
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
		if _, err := deps.Persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:           nodeID,
			InstanceID:   inst.ID,
			NodeType:     def.Type,
			Executor:     def.Executor,
			ScheduleCron: def.Schedule,
			Dependencies: depUUIDs,
		}, tx); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: create node %q: %w", def.Type, err)
		}

		// Register schedule if declared.
		if def.Schedule != "" {
			next, err := scheduler.NextFireAt(def.Schedule, deps.Clock.Now())
			if err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: invalid cron on node %q: %w", def.Type, err)
			}
			if err := deps.Persist.Schedules().Register(ctx, persistence.ScheduleRegisterInput{
				NodeID:     nodeID,
				CronExpr:   def.Schedule,
				NextFireAt: next,
			}, tx); err != nil {
				return createInstanceResponse{}, fmt.Errorf("instance-factory: register schedule on node %q: %w", def.Type, err)
			}
		}
	}

	// Phase 2: enqueue an initial frame for each root node (no deps),
	// reusing the caller's tx so the frame inserts are atomic with the
	// instance+node creation above.
	for _, def := range tpl.Spec.Nodes {
		if len(def.Dependencies) != 0 {
			continue
		}
		nodeID := nodeIDs[def.Type]
		if _, err := frame.EnqueueOrCoalesce(ctx, deps.Persist, tx, inst.ID, nodeID); err != nil {
			return createInstanceResponse{}, fmt.Errorf("instance-factory: enqueue root node %q: %w", def.Type, err)
		}
	}

	return createInstanceResponse{
		InstanceID:   inst.ID.String(),
		TemplateHash: inst.TemplateHash,
		InstanceKey:  inst.InstanceKey,
		NodeCount:    len(tpl.Spec.Nodes),
	}, nil
}
