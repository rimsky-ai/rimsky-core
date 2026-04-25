// templates.go — POST /templates, GET /templates, GET /templates/:id,
// DELETE /templates/:id.
package controlapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

// templateDeployRequest matches the JSON form of node.TemplateSpec. Fields use
// JSON-lower-snake-case names by convention.
type templateDeployRequest struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description,omitempty"`
	Nodes        []templateNodeDefJSON  `json:"nodes"`
	ParamsSchema map[string]any         `json:"params_schema,omitempty"`
	ParamsRedact []string               `json:"params_redact,omitempty"`
}

type templateNodeDefJSON struct {
	Type            string                           `json:"type"`
	Description     string                           `json:"description,omitempty"`
	Executor        string                           `json:"executor,omitempty"`
	Userdata        map[string]any                   `json:"userdata,omitempty"`
	Schedule        string                           `json:"schedule,omitempty"`
	Dependencies    []string                         `json:"dependencies,omitempty"`
	ConcurrencyTags []string                         `json:"concurrency_tags,omitempty"`
	OwnsResources   []resourceDefJSON                `json:"owns_resources,omitempty"`
	ReadsResources  []readResourceDefJSON            `json:"reads_resources,omitempty"`
	ErrorTypes      map[string]errorTypePolicyJSON   `json:"error_types,omitempty"`
}

type resourceDefJSON struct {
	Path           []string       `json:"path"`
	Implementation string         `json:"implementation"`
	Config         map[string]any `json:"config,omitempty"`
	Retention      *retentionJSON `json:"retention,omitempty"`
	QualityRules   []any          `json:"quality_rules,omitempty"`
}

type retentionJSON struct {
	KeepVersions int `json:"keep_versions"`
}

type readResourceDefJSON struct {
	Path []string `json:"path"`
	Via  string   `json:"via"`
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
	RestoreVersion string   `json:"restore_version,omitempty"`
	ReasonTemplate string   `json:"reason_template,omitempty"`
}

// toTemplateSpec converts the JSON form to the domain node.TemplateSpec.
// Quality rules are passed through as raw JSON into the resource def's
// QualityRules slice; the node.TemplateSpec uses qualityrule.Spec which the
// JSON here matches structurally — we re-marshal and decode to convert.
func (r *templateDeployRequest) toTemplateSpec() (node.TemplateSpec, error) {
	spec := node.TemplateSpec{
		Name:         r.Name,
		Version:      r.Version,
		Description:  r.Description,
		ParamsSchema: r.ParamsSchema,
		ParamsRedact: r.ParamsRedact,
	}
	for _, n := range r.Nodes {
		def := node.TemplateNodeDef{
			Type:            n.Type,
			Description:     n.Description,
			Executor:        n.Executor,
			Userdata:        n.Userdata,
			Schedule:        n.Schedule,
			Dependencies:    n.Dependencies,
			ConcurrencyTags: n.ConcurrencyTags,
		}
		for _, rd := range n.OwnsResources {
			rdef := node.ResourceDef{
				Path:           rd.Path,
				Implementation: rd.Implementation,
				Config:         rd.Config,
			}
			if rd.Retention != nil {
				rdef.Retention = &node.Retention{KeepVersions: rd.Retention.KeepVersions}
			}
			// quality_rules is accepted but not decoded into qualityrule.Spec
			// here — v1 templates keep them opaque at the API layer.
			_ = rd.QualityRules
			def.OwnsResources = append(def.OwnsResources, rdef)
		}
		for _, rr := range n.ReadsResources {
			def.ReadsResources = append(def.ReadsResources, node.ReadResourceDef{
				Path: rr.Path,
				Via:  shared.AccessKind(rr.Via),
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
						RestoreVersion: a.RestoreVersion,
						ReasonTemplate: a.ReasonTemplate,
					})
				}
				def.ErrorTypes[cls] = policy
			}
		}
		spec.Nodes = append(spec.Nodes, def)
	}
	return spec, nil
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

func handleDeployTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body templateDeployRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		spec, err := body.toTemplateSpec()
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		res := node.ValidateTemplate(&spec, func(name string) bool {
			_, ok := deps.ResourceFactories.Get(name)
			return ok
		})
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
