// templates.go — POST /templates, GET /templates, GET /templates/:id,
// DELETE /templates/:id.
//
// The deploy handler decodes JSON request bodies straight into
// node.TemplateSpec (the in-memory representation, json-tagged) and
// runs node.ValidateTemplate against the per-process store registry
// (AppDeps.Stores). Concurrency-tag / owns-resources / reads-resources
// fields were retired in the stores redesign (spec §11.3); the JSON
// shape mirrors the current template shape: stores, locks, attributes,
// quality_rules. Per the 2026-04-30 stores cleanup, claim_resolutions
// is gone — store disposition is governed by per-store config, not by
// the template.
package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
	"github.com/fallguy/rimsky/modeling/template/canonical"
)

// readAllBody reads the request body in full.
func readAllBody(req *http.Request) ([]byte, error) {
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

// bytesReader wraps body bytes in a *bytes.Reader for json.NewDecoder.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// templateRegisterRequest is the per-spec §1.5 register-template body.
// The wrapped shape `{spec: {...}, tag, source}` is the only accepted
// form; the legacy bare-spec body was retired alongside the control-
// plane v1 cutover.
type templateRegisterRequest struct {
	Spec   *node.TemplateSpec `json:"spec,omitempty"`
	Tag    string             `json:"tag,omitempty"`
	Source string             `json:"source,omitempty"`
}

type templateRegisterResponse struct {
	TemplateID string   `json:"template_id"` // content hash
	Tags       []string `json:"tags,omitempty"`
}

type templateListItem struct {
	ID           string   `json:"id"` // content hash
	State        string   `json:"state"`
	RegisteredAt string   `json:"registered_at"`
	Source       string   `json:"source"`
	Tags         []string `json:"tags,omitempty"`
}

type templateGetResponse struct {
	ID           string            `json:"id"`
	State        string            `json:"state"`
	RegisteredAt string            `json:"registered_at"`
	Source       string            `json:"source"`
	Tags         []string          `json:"tags,omitempty"`
	Spec         node.TemplateSpec `json:"spec"`
}

// registerTemplatesRoutes wires the /templates group.
func registerTemplatesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/templates", handleDeployTemplate(deps))
	r.Get("/templates", handleListTemplates(deps))
	r.Get("/templates/{id}", handleGetTemplate(deps))
	r.Delete("/templates/{id}", handleDeleteTemplate(deps))
	r.Post("/templates/{id}/deploy", handleDeployTemplateState(deps))
	r.Post("/templates/{id}/undeploy", handleUndeployTemplateState(deps))
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
	if deps.Executors != nil {
		hooks.ExecutorDeclared = func(name string) bool {
			_, ok := deps.Executors[name]
			return ok
		}
	}
	return hooks
}

// handleDeployTemplate is POST /templates: register a template (insert
// a new content-addressed row, optionally attach a tag, fire the
// OnTemplateRegistered fan-out). The handler accepts the wrapped body
// shape (`{spec: {...}, tag, source}`) only; legacy bare-spec bodies
// are rejected by decodeRegisterRequest.
func handleDeployTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, err := readAllBody(req)
		if err != nil {
			badRequest(w, "read body: "+err.Error())
			return
		}
		specBody, tag, source, err := decodeRegisterRequest(raw)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		if tag != "" && !validTag(tag) {
			badRequest(w, "invalid tag identifier")
			return
		}

		spec := *specBody
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

		hash, err := canonical.CanonicalSpecHash(spec)
		if err != nil {
			writeError(w, err)
			return
		}

		// Idempotent re-register: if a row with this hash already exists,
		// short-circuit per spec §1.5 step 1. If a tag was supplied, upsert
		// it pointing at the existing row.
		existing, err := deps.Persist.Templates().GetByHash(req.Context(), hash, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if existing != nil {
			if tag != "" {
				if err := deps.Persist.TemplateTags().Upsert(req.Context(), tag, hash, nil); err != nil {
					writeError(w, err)
					return
				}
			}
			tags := tagsForTemplate(req.Context(), deps, hash)
			writeJSON(w, http.StatusOK, templateRegisterResponse{
				TemplateID: hash,
				Tags:       tags,
			})
			return
		}

		// Fire OnTemplateRegistered to every store referenced by the spec.
		// Per spec §5.4 the fan-out runs synchronously before the row is
		// inserted; on partial failure we surface a 5xx and leave the
		// caller to retry.
		if _, perStore, err := FanOutTemplateEvent(req.Context(), deps, EventTemplateRegistered, hash, spec); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "template lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}

		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if err := deps.Persist.Templates().Insert(ctx, persistence.TemplateInsertInput{
				ID:     hash,
				Spec:   spec,
				State:  persistence.TemplateStateRegistered,
				Source: source,
			}, tx); err != nil {
				return err
			}
			if tag != "" {
				return deps.Persist.TemplateTags().Upsert(ctx, tag, hash, tx)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		tags := tagsForTemplate(req.Context(), deps, hash)
		writeJSON(w, http.StatusCreated, templateRegisterResponse{
			TemplateID: hash,
			Tags:       tags,
		})
	}
}

// handleListTemplates is GET /templates: paginated list of registry rows.
func handleListTemplates(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		state := req.URL.Query().Get("state")
		cursor := req.URL.Query().Get("cursor")
		limit := parseLimit(req, 100)
		page, err := deps.Persist.Templates().List(req.Context(),
			persistence.TemplateListFilter{State: persistence.TemplateState(state)},
			persistence.ListPagination{Limit: limit, Cursor: cursor},
			nil,
		)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]templateListItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, templateListItem{
				ID:           r.ID,
				State:        string(r.State),
				RegisteredAt: r.RegisteredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
				Source:       r.Source,
				Tags:         tagsForTemplate(req.Context(), deps, r.ID),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"templates":   items,
			"next_cursor": page.NextCursor,
		})
	}
}

// handleGetTemplate is GET /templates/{tag_or_hash}.
func handleGetTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		hash, err := resolveTagOrHash(req.Context(), deps, chi.URLParam(req, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		row, err := deps.Persist.Templates().GetByHash(req.Context(), hash, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		writeJSON(w, http.StatusOK, templateGetResponse{
			ID:           row.ID,
			State:        string(row.State),
			RegisteredAt: row.RegisteredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Source:       row.Source,
			Tags:         tagsForTemplate(req.Context(), deps, row.ID),
			Spec:         row.Spec,
		})
	}
}

// handleDeleteTemplate is DELETE /templates/{tag_or_hash}: see spec §1.5.
// Tag-form: deletes the tag. If other tags still point at this template,
// the template is preserved. Last-tag (or direct-hash) form: refuse if
// state is deployed or instances are active; fire deregister fan-out;
// delete row.
func handleDeleteTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idOrTag := chi.URLParam(req, "id")
		// Resolve to (hash, isTagForm).
		isTag := !looksLikeHash(idOrTag)
		hash, err := resolveTagOrHash(req.Context(), deps, idOrTag)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}

		if isTag {
			// Tag-form: count remaining tags pointing at the same hash.
			n, err := deps.Persist.TemplateTags().CountByTemplate(req.Context(), hash, nil)
			if err != nil {
				writeError(w, err)
				return
			}
			if n > 1 {
				if _, err := deps.Persist.TemplateTags().Delete(req.Context(), idOrTag, nil); err != nil {
					writeError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "tag_only": true})
				return
			}
			// last tag → fall through to template deregister.
		}

		row, err := deps.Persist.Templates().GetByHash(req.Context(), hash, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}
		if row.State == persistence.TemplateStateDeployed {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "template is in 'deployed' state; undeploy first",
			})
			return
		}
		active, err := deps.Persist.Instances().CountActiveByTemplate(req.Context(), hash, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if active > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":        "template has active instances",
				"active_count": active,
			})
			return
		}
		if _, perStore, err := FanOutTemplateEvent(req.Context(), deps, EventTemplateDeregistered, hash, row.Spec); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "template lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			if isTag {
				if _, err := deps.Persist.TemplateTags().Delete(ctx, idOrTag, tx); err != nil {
					return err
				}
			} else {
				// Direct-hash form: drop all tags pointing at this row.
				tags, err := deps.Persist.TemplateTags().ListByTemplate(ctx, hash, tx)
				if err != nil {
					return err
				}
				for _, t := range tags {
					if _, err := deps.Persist.TemplateTags().Delete(ctx, t.Tag, tx); err != nil {
						return err
					}
				}
			}
			return deps.Persist.Templates().DeleteByHash(ctx, hash, tx)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

// handleDeployTemplateState is POST /templates/{tag_or_hash}/deploy.
//
// The transaction stays open across the fan-out: we hold the
// FOR UPDATE row lock from LockForUpdate through the per-store
// OnTemplateDeployed dispatches and the final UpdateState. Two
// concurrent deploys serialize on the row lock; the second one
// observes state='deployed' on entry and short-circuits as a no-op.
// Pre-v1 acceptable cost: fan-out is in-process gRPC and bounded by
// the spec's per-store call budget; the alternative (CAS-style
// UpdateState with a re-checked prior state) would still need a row
// lock to avoid lost-update on the lifecycle bookkeeping rows.
func handleDeployTemplateState(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		hash, err := resolveTagOrHash(req.Context(), deps, chi.URLParam(req, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}

		var (
			outState      string
			noOp          bool
			fanOutErr     error
			fanOutDetails map[string]error
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			row, err := deps.Persist.Templates().LockForUpdate(ctx, hash, tx)
			if err != nil {
				return err
			}
			if row == nil {
				return shared.ErrTemplateNotFound
			}
			if row.State == persistence.TemplateStateDeployed {
				outState = "deployed"
				noOp = true
				return nil
			}
			if row.State != persistence.TemplateStateRegistered && row.State != persistence.TemplateStateUndeployed {
				return shared.Wrap(shared.ErrTemplateValidation,
					"template not deployable from state "+string(row.State),
					map[string]any{"template_hash": hash, "state": string(row.State)})
			}
			if _, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateDeployed, hash, row.Spec); ferr != nil {
				fanOutErr = ferr
				fanOutDetails = perStore
				return ferr
			}
			if err := deps.Persist.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
				return err
			}
			outState = "deployed"
			return nil
		})
		if err != nil {
			if fanOutErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":   "template lifecycle fan-out failed",
					"details": fanOutDetails,
				})
				return
			}
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
		resp := map[string]any{"state": outState}
		if noOp {
			resp["no_op"] = true
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleUndeployTemplateState is POST /templates/{tag_or_hash}/undeploy.
//
// Same transactional structure as handleDeployTemplateState — the row
// lock is held across the fan-out so two concurrent undeploys serialize
// and the lifecycle rows are written exactly once.
func handleUndeployTemplateState(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		hash, err := resolveTagOrHash(req.Context(), deps, chi.URLParam(req, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}

		var (
			outState      string
			noOp          bool
			activeCount   int
			fanOutErr     error
			fanOutDetails map[string]error
		)
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			row, err := deps.Persist.Templates().LockForUpdate(ctx, hash, tx)
			if err != nil {
				return err
			}
			if row == nil {
				return shared.ErrTemplateNotFound
			}
			if row.State == persistence.TemplateStateUndeployed {
				outState = "undeployed"
				noOp = true
				return nil
			}
			if row.State != persistence.TemplateStateDeployed {
				return shared.Wrap(shared.ErrTemplateValidation,
					"template not undeployable from state "+string(row.State),
					map[string]any{"template_hash": hash, "state": string(row.State)})
			}
			active, err := deps.Persist.Instances().CountActiveByTemplate(ctx, hash, tx)
			if err != nil {
				return err
			}
			if active > 0 {
				activeCount = active
				return shared.Wrap(shared.ErrTemplateValidation,
					"template has active instances",
					map[string]any{"template_hash": hash, "active_count": active})
			}
			if _, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateUndeployed, hash, row.Spec); ferr != nil {
				fanOutErr = ferr
				fanOutDetails = perStore
				return ferr
			}
			if err := deps.Persist.Templates().UpdateState(ctx, hash, persistence.TemplateStateUndeployed, tx); err != nil {
				return err
			}
			outState = "undeployed"
			return nil
		})
		if err != nil {
			if fanOutErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":   "template lifecycle fan-out failed",
					"details": fanOutDetails,
				})
				return
			}
			if errors.Is(err, shared.ErrTemplateNotFound) {
				notFoundResp(w, shared.ErrTemplateNotFound.Error())
				return
			}
			if errors.Is(err, shared.ErrTemplateValidation) {
				body := map[string]any{"error": err.Error()}
				if activeCount > 0 {
					body["active_count"] = activeCount
				}
				writeJSON(w, http.StatusConflict, body)
				return
			}
			writeError(w, err)
			return
		}
		resp := map[string]any{"state": outState}
		if noOp {
			resp["no_op"] = true
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// decodeRegisterRequest decodes the wrapped POST /templates body shape
// `{spec: {...}, tag, source}`. The legacy bare-spec shape was removed
// alongside the control-plane v1 cutover; bodies missing the "spec" key
// are rejected.
func decodeRegisterRequest(body []byte) (specOut *node.TemplateSpec, tag, source string, err error) {
	var wrap templateRegisterRequest
	dec := json.NewDecoder(bytesReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wrap); err != nil {
		return nil, "", "", fmt.Errorf("invalid JSON body: %w", err)
	}
	if wrap.Spec == nil {
		return nil, "", "", fmt.Errorf("invalid JSON body: missing required field \"spec\"")
	}
	return wrap.Spec, wrap.Tag, wrap.Source, nil
}

// resolveTagOrHash maps a tag or content hash to a hash. Returns the
// empty string when the input is unrecognized or absent.
func resolveTagOrHash(ctx context.Context, deps AppDeps, value string) (string, error) {
	if looksLikeHash(value) {
		return value, nil
	}
	row, err := deps.Persist.TemplateTags().Get(ctx, value, nil)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	return row.TemplateID, nil
}

// looksLikeHash reports whether s is the canonical "sha256-<64-hex>"
// shape. Cheap-string check; the canonical-hash function is the actual
// source of truth (`canonical.CanonicalSpecHash`).
func looksLikeHash(s string) bool {
	if !strings.HasPrefix(s, "sha256-") {
		return false
	}
	suffix := s[len("sha256-"):]
	if len(suffix) != 64 {
		return false
	}
	for _, c := range suffix {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return false
		}
	}
	return true
}

// tagsForTemplate returns the sorted list of tag strings that point at
// templateHash. Best-effort: a query failure produces a nil slice.
func tagsForTemplate(ctx context.Context, deps AppDeps, templateHash string) []string {
	rows, err := deps.Persist.TemplateTags().ListByTemplate(ctx, templateHash, nil)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Tag)
	}
	return out
}
