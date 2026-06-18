// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

func readAllBody(req *http.Request) ([]byte, error) {
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

type templateRegisterRequest struct {
	Spec   *node.TemplateSpec `json:"spec,omitempty"`
	Tag    string             `json:"tag,omitempty"`
	Source string             `json:"source,omitempty"`
}

type templateRegisterResponse struct {
	TemplateID         string                      `json:"template_id"`
	Tags               []string                    `json:"tags,omitempty"`
	ValidationWarnings []runtime.ValidationFinding `json:"validation_warnings,omitempty"`
}

type templateListItem struct {
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

func registerTemplatesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/templates", gate(deps, "template:register", handleDeployTemplate(deps)))
	r.Post("/templates/validate", gate(deps, "template:validate", handleValidateTemplate(deps)))
	r.Get("/templates", gate(deps, "template:read", handleListTemplates(deps)))
	r.Get("/templates/{id}", gate(deps, "template:read", handleGetTemplate(deps)))
	r.Delete("/templates/{id}", gate(deps, "template:deregister", handleDeleteTemplate(deps)))
	r.Post("/templates/{id}/deploy", gate(deps, "template:deploy", handleDeployTemplateState(deps)))
	r.Post("/templates/{id}/undeploy", gate(deps, "template:undeploy", handleUndeployTemplateState(deps)))
}

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
		RefValidationMode: deps.RefValidationMode,
		KindAliases:       deps.KindAliases,
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
				return nil, false
			}
			return deps.StoreDeclaredErrorClasses(name)
		}
	}
	hooks.NamedLockDeclared = func(name string) bool {
		_, ok := deps.NamedLocks.Get(name)
		return ok
	}
	inprocAlias := func(name string) bool {
		return name == loop_counter.ExecutorAlias
	}

	if deps.Executors != nil {
		hooks.ExecutorDeclared = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			if inprocAlias(name) {
				return true
			}
			_, ok := deps.Executors[name]
			return ok
		}
	} else if deps.KindAliases != nil {
		hooks.ExecutorDeclared = func(name string) bool {
			return inprocAlias(name)
		}
	}
	if deps.ExecutorCapabilities != nil {
		hooks.ExecutorDeclaredTags = func(name string) ([]string, bool) {
			if name == loop_counter.ExecutorAlias {
				return loop_counter.DeclaredTags(), true
			}
			tags, _, _, ok := deps.ExecutorCapabilities(name)
			return tags, ok
		}
		hooks.ExecutorDeclaredErrorClasses = func(name string) ([]string, bool) {
			if inprocAlias(name) {
				return nil, true
			}
			_, classes, _, ok := deps.ExecutorCapabilities(name)
			return classes, ok
		}
		hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
			if isLateBind(name) {
				return nil, true
			}
			if name == loop_counter.ExecutorAlias {
				return loop_counter.SchemaBytes(), true
			}
			_, _, schema, ok := deps.ExecutorCapabilities(name)
			return schema, ok
		}
	} else if deps.KindAliases != nil {
		hooks.ExecutorDeclaredTags = func(name string) ([]string, bool) {
			if name == loop_counter.ExecutorAlias {
				return loop_counter.DeclaredTags(), true
			}
			return nil, false
		}
		hooks.ExecutorDeclaredErrorClasses = func(name string) ([]string, bool) {
			if inprocAlias(name) {
				return nil, true
			}
			return nil, false
		}
		hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
			if name == loop_counter.ExecutorAlias {
				return loop_counter.SchemaBytes(), true
			}
			return nil, false
		}
	}
	return hooks
}

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
			entries := make([]map[string]any, 0, len(res.Errors)+len(res.StructuredErrors))
			for _, e := range res.Errors {
				entries = append(entries, map[string]any{"path": e.Path, "msg": e.Msg})
			}
			entries = append(entries, res.StructuredErrors...)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":               shared.ErrTemplateValidation.Error(),
				"validation_errors":   entries,
				"validation_warnings": staticWarningsToFindings(res.Warnings),
			})
			return
		}
		staticWarnings := staticWarningsToFindings(res.Warnings)
		node.ApplyFrameResolutionDefaults(&spec)
		node.CanonicalizeKindSugar(&spec, deps.KindAliases)

		tHash := time.Now()
		hash, err := canonical.CanonicalSpecHash(spec)
		log.Debug("register.hash.done", "elapsed_ms", time.Since(tHash).Milliseconds())
		if err != nil {
			writeError(w, err)
			return
		}

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

		if WriteDryRunResponse(w, req, "would_have_registered", map[string]any{
			"template_hash":       hash,
			"tag":                 tag,
			"source":              source,
			"validation_warnings": mergedWarnings,
		}) {
			return
		}

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

func handleValidateTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, err := readAllBody(req)
		if err != nil {
			badRequest(w, "read body: "+err.Error())
			return
		}
		specBody, _, _, err := decodeRegisterRequest(raw)
		if err != nil {
			badRequest(w, err.Error())
			return
		}

		spec := *specBody
		res := node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))

		validationErrors := make([]map[string]any, 0, len(res.Errors)+len(res.StructuredErrors))
		for _, e := range res.Errors {
			validationErrors = append(validationErrors, map[string]any{"path": e.Path, "msg": e.Msg})
		}
		validationErrors = append(validationErrors, res.StructuredErrors...)

		node.ApplyFrameResolutionDefaults(&spec)
		node.CanonicalizeKindSugar(&spec, deps.KindAliases)

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

		for _, e := range outcome.Errors {
			validationErrors = append(validationErrors, findingToProjectionAny(e))
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

func findingToProjectionAny(f runtime.ValidationFinding) map[string]any {
	p := findingToProjection(f)
	return map[string]any{"path": p["path"], "msg": p["msg"]}
}

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

func handleDeleteTemplate(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idOrTag := chi.URLParam(req, "id")
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
