// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
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
	// @constraint: TemplateID is the content hash.
	TemplateID string   `json:"template_id"`
	Tags       []string `json:"tags,omitempty"`
	// ValidationWarnings carries the merged advisory set (static
	// validator + Validation-mix-in pipeline) so warnings the system
	// computed reach the registrant even when registration succeeds
	// (TD-merge-validator-warnings).
	ValidationWarnings []runtime.ValidationFinding `json:"validation_warnings,omitempty"`
}

type templateListItem struct {
	// @constraint: ID is the content hash.
	ID           string   `json:"id"`
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
	r.Post("/templates", gate(deps, "template:register", handleDeployTemplate(deps)))
	// @constraint: static segment registered alongside the others; chi resolves the
	// static `/templates/validate` ahead of the `/templates/{id}` wildcard.
	r.Post("/templates/validate", gate(deps, "template:validate", handleValidateTemplate(deps)))
	r.Get("/templates", gate(deps, "template:read", handleListTemplates(deps)))
	r.Get("/templates/{id}", gate(deps, "template:read", handleGetTemplate(deps)))
	r.Delete("/templates/{id}", gate(deps, "template:deregister", handleDeleteTemplate(deps)))
	r.Post("/templates/{id}/deploy", gate(deps, "template:deploy", handleDeployTemplateState(deps)))
	r.Post("/templates/{id}/undeploy", gate(deps, "template:undeploy", handleUndeployTemplateState(deps)))
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
//
// Names in spec.LateBindServices bypass the registration-time existence
// and expected_attributes_schema cross-checks: a late-bound service is
// not declared in rimsky.yml and has no discovery-cache entry yet, so
// the StoreDeclared / ExecutorDeclared / ExecutorExpectedAttributesSchema
// hooks short-circuit to "declared, no schema cross-check" for those
// names. The spawned binary's Capabilities supplies the real schema at
// dispatch; the proxy enforces it there. Per spec
// 2026-05-24-host-agent-and-proxy-design.md.
func validatorHooksFor(deps AppDeps, spec node.TemplateSpec) node.RegistryHooks {
	isLateBind := func(name string) bool {
		for _, ls := range spec.LateBindServices {
			if ls == name {
				return true
			}
		}
		return false
	}
	hooks := node.RegistryHooks{
		// @constraint: stamp the operator-set registration-time reference-validation
		// mode onto the hooks so the registration path AND the
		// POST /templates/validate path (both route through
		// validatorHooksFor) share one operator-chosen strictness. The
		// zero value is node.RefValidateAll — strict by default.
		RefValidationMode: deps.RefValidationMode,
	}
	if deps.Stores != nil {
		hooks.StoreDeclared = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			_, ok := deps.Stores.Get(name)
			return ok
		}
	}
	if deps.StoreDeclaredErrorClasses != nil {
		hooks.StoreDeclaredErrorClasses = func(name string) ([]string, bool) {
			if isLateBind(name) {
				// @constraint: late-bound producer: no discovery-cache entry yet; it
				// contributes no vocabulary at registration. Keys only it
				// could attribute surface as advisory warnings, never
				// rejections.
				return nil, false
			}
			return deps.StoreDeclaredErrorClasses(name)
		}
	}
	hooks.NamedLockDeclared = func(name string) bool {
		_, ok := deps.NamedLocks.Get(name)
		return ok
	}
	if deps.Executors != nil {
		hooks.ExecutorDeclared = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			_, ok := deps.Executors[name]
			return ok
		}
	}
	if deps.ExecutorCapabilities != nil {
		hooks.ExecutorDeclaredEvents = func(name string) ([]string, bool) {
			events, _, _, ok := deps.ExecutorCapabilities(name)
			return events, ok
		}
		hooks.ExecutorDeclaredErrorClasses = func(name string) ([]string, bool) {
			_, classes, _, ok := deps.ExecutorCapabilities(name)
			return classes, ok
		}
		hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
			if isLateBind(name) {
				// @constraint: declared, no schema cross-check — the spawned binary's
				// Capabilities supplies the schema at dispatch.
				return nil, true
			}
			_, _, schema, ok := deps.ExecutorCapabilities(name)
			return schema, ok
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
		t0 := time.Now()
		log := slog.With("path", "/templates")
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
		tValidate := time.Now()
		res := node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))
		log.Debug("register.validate.done", "elapsed_ms", time.Since(tValidate).Milliseconds())
		if !res.Ok() {
			errs := make([]map[string]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				errs = append(errs, map[string]string{"path": e.Path, "msg": e.Msg})
			}
			// @constraint: parity with the pipeline rejection below: the warnings
			// the validator computed alongside the errors ride the
			// rejection body too, so an operator fixing the errors sees
			// the advisories in the same pass.
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":               shared.ErrTemplateValidation.Error(),
				"validation_errors":   errs,
				"validation_warnings": staticWarningsToFindings(res.Warnings),
			})
			return
		}
		// @constraint: static-validator advisories merge into the same
		// validation_warnings stream as the pipeline findings below
		// (TD-merge-validator-warnings): a warning the validator
		// computed must reach the response, and warnings_as_errors
		// must trip on it exactly like a pipeline warning.
		staticWarnings := staticWarningsToFindings(res.Warnings)
		// @constraint: default-fill frame-resolution fields (FrameTimeoutMs == 0 →
		// FrameTimeoutDefaultMs). Validator is pure; the boundary handler
		// applies defaults after validation passes so the persisted spec
		// carries the resolved value.
		node.ApplyFrameResolutionDefaults(&spec)

		tHash := time.Now()
		hash, err := canonical.CanonicalSpecHash(spec)
		log.Debug("register.hash.done", "elapsed_ms", time.Since(tHash).Milliseconds())
		if err != nil {
			writeError(w, err)
			return
		}

		// @constraint: F9: Validation pipeline. After canonicalization + static-
		// check pass, fire `Validate` RPCs to advertising services.
		// Errors at this step reject the registration; warnings fail
		// only when `?warnings_as_errors=true` is set on the request.
		// Spec §Protocol surfaces / Validation / Pipeline integration.
		warningsAsErrors := req.URL.Query().Get("warnings_as_errors") == "true"
		tValidatePipeline := time.Now()
		var execSchemaLookup runtime.ExpectedAttributesSchemaLookup
		if deps.ExecutorCapabilities != nil {
			execSchemaLookup = func(executor string) ([]byte, bool) {
				_, _, schema, ok := deps.ExecutorCapabilities(executor)
				if !ok || len(schema) == 0 {
					return nil, false
				}
				return schema, true
			}
		}
		outcome, vErr := runtime.RunValidationPipeline(
			req.Context(), deps.Validators, spec, hash, deps.UnreachableValidatorPolicy, execSchemaLookup,
		)
		log.Debug("register.validate_pipeline.done",
			"elapsed_ms", time.Since(tValidatePipeline).Milliseconds(),
			"errors", len(outcome.Errors),
			"warnings", len(outcome.Warnings),
			"err", vErr)
		if vErr != nil {
			writeError(w, vErr)
			return
		}
		// @constraint: merged advisory set: static-validator warnings first, then
		// the pipeline's. Both feed the warnings_as_errors rejection
		// gate and every response that carries validation_warnings.
		mergedWarnings := append(staticWarnings, outcome.Warnings...)
		if len(outcome.Errors) > 0 || (warningsAsErrors && len(mergedWarnings) > 0) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":               "template validation pipeline rejected the registration",
				"validation_errors":   outcome.Errors,
				"validation_warnings": mergedWarnings,
				"warnings_as_errors":  warningsAsErrors,
			})
			return
		}

		// @constraint: dry-run: validation (static + Validation mix-in pipeline)
		// has run faithfully against the registry; skip only the DB
		// inserts. Per spec section "Synthetic response shape /
		// template:register".
		if WriteDryRunResponse(w, req, "would_have_registered", map[string]any{
			"template_hash":       hash,
			"tag":                 tag,
			"source":              source,
			"validation_warnings": mergedWarnings,
		}) {
			return
		}

		// @constraint: idempotent re-register: if a row with this hash already exists,
		// short-circuit per spec §1.5 step 1. If a tag was supplied, upsert
		// it pointing at the existing row.
		tGetByHash := time.Now()
		var existing *persistence.TemplateRow
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Templates().GetByHash(ctx, hash, tx)
			existing = r
			return err
		})
		log.Debug("register.getbyhash.done", "elapsed_ms", time.Since(tGetByHash).Milliseconds())
		if err != nil {
			writeError(w, err)
			return
		}
		if existing != nil {
			if tag != "" {
				if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
					return deps.Persist.TemplateTags().Upsert(ctx, tag, hash, tx)
				}); err != nil {
					writeError(w, err)
					return
				}
			}
			tags := tagsForTemplate(req.Context(), deps, hash)
			writeJSON(w, http.StatusOK, templateRegisterResponse{
				TemplateID:         hash,
				Tags:               tags,
				ValidationWarnings: mergedWarnings,
			})
			return
		}

		// @constraint: fire OnTemplateRegistered to every store referenced by the spec.
		// Per spec §5.4 the fan-out runs synchronously before the row is
		// inserted; on partial failure we surface a 5xx and leave the
		// caller to retry. Carry the JCS-canonical spec bytes so
		// subscribers see exactly what rimsky hashed.
		canonBytes, err := canonical.CanonicalSpecBytes(spec)
		if err != nil {
			writeError(w, err)
			return
		}
		tFanOut := time.Now()
		peers, perStore, ferr := FanOutTemplateEvent(req.Context(), deps, EventTemplateRegistered, hash, spec, TemplatePayload{Spec: canonBytes}, nil)
		log.Debug("register.fanout.done", "elapsed_ms", time.Since(tFanOut).Milliseconds(), "peers", len(peers), "err", ferr)
		if ferr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "template lifecycle fan-out failed",
				"details": perStore,
			})
			return
		}

		tTx := time.Now()
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
		log.Debug("register.tx.done", "elapsed_ms", time.Since(tTx).Milliseconds(), "total_ms", time.Since(t0).Milliseconds(), "err", err)
		if err != nil {
			writeError(w, err)
			return
		}
		tags := tagsForTemplate(req.Context(), deps, hash)
		writeJSON(w, http.StatusCreated, templateRegisterResponse{
			TemplateID:         hash,
			Tags:               tags,
			ValidationWarnings: mergedWarnings,
		})
	}
}

// handleValidateTemplate is POST /templates/validate: run the full
// registration validation pipeline (static `node.ValidateTemplate` +
// the Validation-protocol mix-in pipeline) against the live registry,
// but stop before canonicalization-for-persistence and any DB insert.
// It always returns HTTP 200 when validation ran; the `ok` flag and the
// findings carry the verdict. Non-2xx is reserved for request-level
// failures (a malformed body → 400). Per spec
// 2026-05-28-quality-of-life-features.
func handleValidateTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, err := readAllBody(req)
		if err != nil {
			badRequest(w, "read body: "+err.Error())
			return
		}
		// @constraint: tag and source are accepted-but-ignored on the lint path: they
		// only affect persistence, which validate-only never reaches.
		specBody, _, _, err := decodeRegisterRequest(raw)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		spec := *specBody
		res := node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))

		// @constraint: project static findings into the unified {path, msg} shape.
		validationErrors := make([]map[string]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			validationErrors = append(validationErrors, map[string]string{"path": e.Path, "msg": e.Msg})
		}

		// @constraint: default-fill frame-resolution fields before the pipeline, exactly
		// as register does, so the spec the validators see matches what
		// would be persisted.
		node.ApplyFrameResolutionDefaults(&spec)

		// @constraint: CanonicalSpecHash is computed purely to feed the validation
		// pipeline (validation input, not persistence). A hash error is a
		// request-level failure.
		hash, err := canonical.CanonicalSpecHash(spec)
		if err != nil {
			writeError(w, err)
			return
		}

		var execSchemaLookup runtime.ExpectedAttributesSchemaLookup
		if deps.ExecutorCapabilities != nil {
			execSchemaLookup = func(executor string) ([]byte, bool) {
				_, _, schema, ok := deps.ExecutorCapabilities(executor)
				if !ok || len(schema) == 0 {
					return nil, false
				}
				return schema, true
			}
		}
		outcome, vErr := runtime.RunValidationPipeline(
			req.Context(), deps.Validators, spec, hash, deps.UnreachableValidatorPolicy, execSchemaLookup,
		)
		if vErr != nil {
			writeError(w, vErr)
			return
		}

		// @constraint: merge the pipeline findings (ValidationFinding) into the unified
		// {path, msg} projection. Static-validator advisories lead the
		// warnings array (TD-merge-validator-warnings): a warning the
		// static validator computed must reach the response and count
		// toward the warnings_as_errors verdict below.
		for _, e := range outcome.Errors {
			validationErrors = append(validationErrors, findingToProjection(e))
		}
		validationWarnings := make([]map[string]string, 0, len(res.Warnings)+len(outcome.Warnings))
		for _, wn := range res.Warnings {
			validationWarnings = append(validationWarnings, map[string]string{"path": wn.Path, "msg": wn.Msg})
		}
		for _, wn := range outcome.Warnings {
			validationWarnings = append(validationWarnings, findingToProjection(wn))
		}

		warningsAsErrors := req.URL.Query().Get("warnings_as_errors") == "true"
		ok := len(validationErrors) == 0 && (!warningsAsErrors || len(validationWarnings) == 0)

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                  ok,
			"validation_errors":   validationErrors,
			"validation_warnings": validationWarnings,
		})
	}
}

// staticWarningsToFindings projects the static validator's advisory
// warnings (node.ValidationWarning, {Path, Msg}) into the pipeline's
// ValidationFinding shape so both warning kinds travel through one
// validation_warnings array on the register surface. ServiceName/Role
// name the in-process static validator as the origin — static warnings
// always carry a Path, so findingToProjection never falls back to them.
func staticWarningsToFindings(warnings []node.ValidationWarning) []runtime.ValidationFinding {
	out := make([]runtime.ValidationFinding, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, runtime.ValidationFinding{
			ServiceName: "rimsky",
			Role:        "static",
			Path:        w.Path,
			Message:     w.Msg,
		})
	}
	return out
}

// findingToProjection flattens a pipeline ValidationFinding into the
// unified {path, msg} shape the lint response and CLI consume. When the
// finding carries no path, fall back to the originating service/role so
// the operator still sees where it came from.
func findingToProjection(f runtime.ValidationFinding) map[string]string {
	path := f.Path
	if path == "" {
		path = f.ServiceName
		if f.Role != "" {
			path = f.ServiceName + " (" + f.Role + ")"
		}
	}
	return map[string]string{"path": path, "msg": f.Message}
}

// handleListTemplates is GET /templates: paginated list of registry rows.
func handleListTemplates(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		state := req.URL.Query().Get("state")
		cursor := req.URL.Query().Get("cursor")
		limit := parseLimit(req, 100)
		var page persistence.PaginatedListResult[persistence.TemplateRow]
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Templates().List(ctx,
				persistence.TemplateListFilter{State: persistence.TemplateState(state)},
				persistence.ListPagination{Limit: limit, Cursor: cursor},
				tx,
			)
			page = p
			return err
		}); err != nil {
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
		var row *persistence.TemplateRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Templates().GetByHash(ctx, hash, tx)
			row = r
			return err
		}); err != nil {
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
		// @constraint: resolve to (hash, isTagForm).
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
			var n int
			if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
				v, err := deps.Persist.TemplateTags().CountByTemplate(ctx, hash, tx)
				n = v
				return err
			}); err != nil {
				writeError(w, err)
				return
			}
			if n > 1 {
				// @constraint: tag-only deletion validation has passed. Honor
				// dry-run by skipping the DELETE.
				if WriteDryRunResponse(w, req, "would_have_deregistered", map[string]any{
					"template_hash": hash,
					"is_tag_form":   true,
					"tag_only":      true,
				}) {
					return
				}
				if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
					_, err := deps.Persist.TemplateTags().Delete(ctx, idOrTag, tx)
					return err
				}); err != nil {
					writeError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "tag_only": true})
				return
			}
			// @constraint: last tag → fall through to template deregister.
		}

		var row *persistence.TemplateRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Templates().GetByHash(ctx, hash, tx)
			row = r
			return err
		}); err != nil {
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
		var active int
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			v, err := deps.Persist.Instances().CountActiveByTemplate(ctx, hash, tx)
			active = v
			return err
		}); err != nil {
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
		// @constraint: all validation has passed (template found, state not
		// 'deployed', no active instances). Dry-run skips fan-out +
		// delete; the synthetic envelope is now an honest precursor.
		if WriteDryRunResponse(w, req, "would_have_deregistered", map[string]any{
			"template_hash": hash,
			"is_tag_form":   isTag,
		}) {
			return
		}
		if _, perStore, err := FanOutTemplateEvent(req.Context(), deps, EventTemplateDeregistered, hash, row.Spec, TemplatePayload{}, nil); err != nil {
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
				// @constraint: direct-hash form: drop all tags pointing at this row.
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
		t0 := time.Now()
		log := slog.With("path", "/templates/deploy", "request_id", req.Header.Get("X-Request-Id"))
		hash, err := resolveTagOrHash(req.Context(), deps, chi.URLParam(req, "id"))
		log.Debug("deploy.resolve.done", "elapsed_ms", time.Since(t0).Milliseconds(), "err", err)
		if err != nil {
			writeError(w, err)
			return
		}
		if hash == "" {
			notFoundResp(w, shared.ErrTemplateNotFound.Error())
			return
		}

		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		var (
			outState      string
			noOp          bool
			fanOutErr     error
			fanOutDetails map[string]error
		)
		txStart := time.Now()
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			tBegin := time.Now()
			log.Debug("deploy.tx.begin", "elapsed_ms", time.Since(txStart).Milliseconds())
			row, err := deps.Persist.Templates().LockForUpdate(ctx, hash, tx)
			log.Debug("deploy.lockforupdate.done", "elapsed_ms", time.Since(tBegin).Milliseconds(), "err", err)
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
			// @constraint: dry-run: every state validation has passed (template
			// found, state in {registered, undeployed}). Skip fan-out
			// and the UPDATE; the synthetic envelope below is now an
			// honest precursor to a real deploy.
			if isDryRun {
				return errDryRunOK
			}
			tListTags := time.Now()
			tagRows, err := deps.Persist.TemplateTags().ListByTemplate(ctx, hash, tx)
			log.Debug("deploy.listtags.done", "elapsed_ms", time.Since(tListTags).Milliseconds(), "tags", len(tagRows), "err", err)
			if err != nil {
				return err
			}
			tags := make([]string, 0, len(tagRows))
			for _, t := range tagRows {
				tags = append(tags, t.Tag)
			}
			tFanOut := time.Now()
			peers, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateDeployed, hash, row.Spec, TemplatePayload{Tags: tags}, tx)
			log.Debug("deploy.fanout.done", "elapsed_ms", time.Since(tFanOut).Milliseconds(), "peers", len(peers), "err", ferr)
			if ferr != nil {
				fanOutErr = ferr
				fanOutDetails = perStore
				return ferr
			}
			tUpdate := time.Now()
			if err := deps.Persist.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
				log.Debug("deploy.updatestate.err", "elapsed_ms", time.Since(tUpdate).Milliseconds(), "err", err)
				return err
			}
			log.Debug("deploy.updatestate.done", "elapsed_ms", time.Since(tUpdate).Milliseconds())
			outState = "deployed"
			return nil
		})
		log.Debug("deploy.tx.done", "elapsed_ms", time.Since(txStart).Milliseconds(), "err", err)
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_deployed", map[string]any{
				"template_hash": hash,
			})
			return
		}
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

		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
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
			// @constraint: dry-run: every validation has passed. Skip fan-out and
			// the UPDATE; the synthetic envelope is now an honest
			// precursor.
			if isDryRun {
				return errDryRunOK
			}
			if _, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateUndeployed, hash, row.Spec, TemplatePayload{}, tx); ferr != nil {
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
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_undeployed", map[string]any{
				"template_hash": hash,
			})
			return
		}
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
	var row *persistence.TemplateTagRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.TemplateTags().Get(ctx, value, tx)
		row = r
		return err
	}); err != nil {
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
	var rows []persistence.TemplateTagRow
	if err := deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := deps.Persist.TemplateTags().ListByTemplate(ctx, templateHash, tx)
		rows = r
		return err
	}); err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Tag)
	}
	return out
}
