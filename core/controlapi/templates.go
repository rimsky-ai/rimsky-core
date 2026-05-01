// templates.go — POST /templates, GET /templates, GET /templates/:id,
// DELETE /templates/:id.
//
// The deploy handler converts the JSON request shape into node.TemplateSpec
// (the in-memory representation) and runs node.ValidateTemplate against the
// per-process store registry (AppDeps.Stores). Concurrency-tag /
// owns-resources / reads-resources fields were retired in the stores
// redesign (spec §11.3); the JSON shape mirrors the current template
// shape: stores, locks, attributes, quality_rules. Per the 2026-04-30
// stores cleanup, claim_resolutions is gone — store disposition is
// governed by per-store config, not by the template.
package controlapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/qualityrule"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// templateDeployRequest matches the JSON form of node.TemplateSpec. Fields use
// JSON-lower-snake-case names by convention.
type templateDeployRequest struct {
	Name            string                `json:"name"`
	Version         string                `json:"version"`
	Description     string                `json:"description,omitempty"`
	FrameResolution string                `json:"frame_resolution"`
	FrameTimeoutMs  int64                 `json:"frame_timeout_ms,omitempty"`
	Nodes           []templateNodeDefJSON `json:"nodes"`
	ParamsSchema    map[string]any        `json:"params_schema,omitempty"`
	ParamsRedact    []string              `json:"params_redact,omitempty"`
}

type templateNodeDefJSON struct {
	Type         string                         `json:"type"`
	Description  string                         `json:"description,omitempty"`
	Executor     string                         `json:"executor,omitempty"`
	Userdata     map[string]any                 `json:"userdata,omitempty"`
	Schedule     string                         `json:"schedule,omitempty"`
	Dependencies []string                       `json:"dependencies,omitempty"`
	Stores       []nodeStoreRefJSON             `json:"stores,omitempty"`
	Locks        []nodeLockRefJSON              `json:"locks,omitempty"`
	Inherits     []inheritEntryJSON             `json:"inherits,omitempty"`
	Attributes   *nodeAttributesDefJSON         `json:"attributes,omitempty"`
	QualityRules []qualityRuleJSON              `json:"quality_rules,omitempty"`
	ErrorTypes   map[string]errorTypePolicyJSON `json:"error_types,omitempty"`
}

type nodeStoreRefJSON struct {
	Name     string `json:"name"`
	Selector string `json:"selector"`
	Intent   string `json:"intent"`
	Alias    string `json:"alias,omitempty"`
}

type nodeLockRefJSON struct {
	Name string `json:"name"`
}

type inheritEntryJSON struct {
	Claim string `json:"claim"`
}

type nodeAttributesDefJSON struct {
	Schema map[string]any `json:"schema,omitempty"`
}

type qualityRuleJSON struct {
	Type     string         `json:"type"`
	Config   map[string]any `json:"config,omitempty"`
	Severity string         `json:"severity,omitempty"`
}

type errorTypePolicyJSON struct {
	Policy []policyActionJSON `json:"policy"`
}

type policyActionJSON struct {
	Action         string   `json:"action"`
	Count          int      `json:"count,omitempty"`
	Backoff        string   `json:"backoff,omitempty"`
	Jitter         string   `json:"jitter,omitempty"`
	BaseDelayMs    int      `json:"base_delay_ms,omitempty"`
	MaxDelayMs     int      `json:"max_delay_ms,omitempty"`
	Targets        []string `json:"targets,omitempty"`
	ReasonTemplate string   `json:"reason_template,omitempty"`
}

// toTemplateSpec converts the JSON form to the domain node.TemplateSpec.
// Pure mapping; no validation here — the deploy handler runs
// node.ValidateTemplate after this returns.
func (r *templateDeployRequest) toTemplateSpec() node.TemplateSpec {
	spec := node.TemplateSpec{
		Name:            r.Name,
		Version:         r.Version,
		Description:     r.Description,
		FrameResolution: r.FrameResolution,
		FrameTimeoutMs:  r.FrameTimeoutMs,
		ParamsSchema:    r.ParamsSchema,
		ParamsRedact:    r.ParamsRedact,
	}
	for _, n := range r.Nodes {
		def := node.TemplateNodeDef{
			Type:         n.Type,
			Description:  n.Description,
			Executor:     n.Executor,
			Userdata:     n.Userdata,
			Schedule:     n.Schedule,
			Dependencies: n.Dependencies,
		}
		for _, s := range n.Stores {
			def.Stores = append(def.Stores, node.NodeStoreRef{
				Name:     s.Name,
				Selector: s.Selector,
				Intent:   s.Intent,
				Alias:    s.Alias,
			})
		}
		for _, l := range n.Locks {
			def.Locks = append(def.Locks, node.NodeLockRef{Name: l.Name})
		}
		for _, ie := range n.Inherits {
			def.Inherits = append(def.Inherits, node.InheritEntry{Claim: ie.Claim})
		}
		if n.Attributes != nil {
			def.Attributes = node.NodeAttributesDef{Schema: n.Attributes.Schema}
		}
		for _, qr := range n.QualityRules {
			def.QualityRules = append(def.QualityRules, qualityrule.Spec{
				Type:     qr.Type,
				Config:   qr.Config,
				Severity: shared.Severity(qr.Severity),
			})
		}
		if len(n.ErrorTypes) > 0 {
			def.ErrorTypes = map[string]node.ErrorTypePolicy{}
			for cls, etp := range n.ErrorTypes {
				policy := node.ErrorTypePolicy{}
				for _, a := range etp.Policy {
					policy.Policy = append(policy.Policy, node.PolicyAction{
						Action:         a.Action,
						Count:          a.Count,
						Backoff:        shared.BackoffKind(a.Backoff),
						Jitter:         shared.JitterKind(a.Jitter),
						BaseDelayMs:    a.BaseDelayMs,
						MaxDelayMs:     a.MaxDelayMs,
						Targets:        a.Targets,
						ReasonTemplate: a.ReasonTemplate,
					})
				}
				def.ErrorTypes[cls] = policy
			}
		}
		spec.Nodes = append(spec.Nodes, def)
	}
	return spec
}

type templateDeployResponse struct {
	TemplateID string `json:"template_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

type templateListItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	DeployedAt string `json:"deployed_at"`
}

type templateGetResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	DeployedAt string            `json:"deployed_at"`
	Spec       node.TemplateSpec `json:"spec"`
}

// registerTemplatesRoutes wires the /templates group.
func registerTemplatesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/templates", handleDeployTemplate(deps))
	r.Get("/templates", handleListTemplates(deps))
	r.Get("/templates/{id}", handleGetTemplate(deps))
	r.Delete("/templates/{id}", handleDeleteTemplate(deps))
}

// validatorHooksFor builds the registry-dependent lookups consumed by
// node.ValidateTemplate. Nil-safe on deps.Stores.
//
// The pick-policy hook from v2 is gone (per the v3 inertness cleanup):
// rimsky no longer recognises pick-policy selectors. Substrate is the
// only entity with that knowledge.
//
// NamedLockDeclared is wired unconditionally so missing names always
// fail validation — an empty `named_locks:` block is still a valid
// (empty) declaration, and templates referencing any name when none
// are declared must be rejected.
func validatorHooksFor(deps AppDeps) node.RegistryHooks {
	hooks := node.RegistryHooks{}
	if deps.Stores != nil {
		hooks.StoreDeclared = func(name string) bool {
			_, ok := deps.Stores.Get(name)
			return ok
		}
	}
	hooks.NamedLockDeclared = func(name string) bool {
		_, ok := deps.NamedLocks.Get(name)
		return ok
	}
	return hooks
}

func handleDeployTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body templateDeployRequest
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		spec := body.toTemplateSpec()
		res := node.ValidateTemplate(&spec, validatorHooksFor(deps))
		if !res.Ok() {
			errs := make([]map[string]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				errs = append(errs, map[string]string{"path": e.Path, "msg": e.Msg})
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":             shared.ErrTemplateValidation.Error(),
				"validation_errors": errs,
			})
			return
		}
		// Default-fill frame-resolution fields (FrameTimeoutMs == 0 →
		// FrameTimeoutDefaultMs). Validator is pure; the boundary handler
		// applies defaults after validation passes so the persisted spec
		// carries the resolved value.
		node.ApplyFrameResolutionDefaults(&spec)
		sum, err := deps.Storage.Templates().Deploy(req.Context(), spec, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, templateDeployResponse{
			TemplateID: sum.ID.String(),
			Name:       sum.Name,
			Version:    sum.Version,
		})
	}
}

func handleListTemplates(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		name := req.URL.Query().Get("name")
		cursor := req.URL.Query().Get("cursor")
		limit := parseLimit(req, 100)
		page, err := deps.Storage.Templates().List(req.Context(),
			storage.TemplateListFilter{Name: name},
			storage.ListPagination{Limit: limit, Cursor: cursor},
			nil,
		)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]templateListItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, templateListItem{
				ID:         r.ID.String(),
				Name:       r.Name,
				Version:    r.Version,
				DeployedAt: r.DeployedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"templates":   items,
			"next_cursor": page.NextCursor,
		})
	}
}

func handleGetTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		row, err := deps.Storage.Templates().Get(req.Context(), id, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		writeJSON(w, http.StatusOK, templateGetResponse{
			ID:         row.ID.String(),
			Name:       row.Name,
			Version:    row.Version,
			DeployedAt: row.DeployedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Spec:       row.Spec,
		})
	}
}

func handleDeleteTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		if err := deps.Storage.Templates().Delete(req.Context(), id, nil); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}
