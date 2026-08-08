// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
)

var errTagMoveForbidden = errors.New("tag already points at another template; moving it requires tag:set permission on that tag")

func callerCanSetTag(req *http.Request, tag string) bool {
	ident, ok := IdentityFromContextOK(req.Context())
	if !ok {
		return false
	}
	return auth.CheckGrant(ident.Permissions, "tag:set", map[string]string{"template_tag": tag}).Allowed
}

func upsertTagIfNotConflicting(ctx context.Context, deps AppDeps, req *http.Request, tag, hash string, tx persistence.Tx) error {
	existing, err := deps.Persist.TemplateTags().Get(ctx, tag, tx)
	if err != nil {
		return err
	}
	if existing != nil && existing.TemplateID != hash && !callerCanSetTag(req, tag) {
		return errTagMoveForbidden
	}
	return deps.Persist.TemplateTags().Upsert(ctx, tag, hash, tx)
}

func readAllBody(req *http.Request) ([]byte, error) {
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

type templateRegisterRequest struct {
	Spec   *node.TemplateSpec `json:"spec,omitempty"`
	Tag    string             `json:"tag,omitempty"`
	Source string             `json:"source,omitempty"`
}

type templateRegisterResponse struct {
	TemplateID         string              `json:"template_id"`
	Tags               []string            `json:"tags,omitempty"`
	ValidationWarnings []map[string]string `json:"validation_warnings,omitempty"`
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
	r.Post("/templates", gate(deps, "template:register", handleRegisterTemplate(deps)))
	r.Post("/templates/validate", gate(deps, "template:validate", handleValidateTemplate(deps)))
	r.Get("/templates", gate(deps, "template:read", handleListTemplates(deps)))
	r.Get("/templates/{id}", gate(deps, "template:read", handleGetTemplate(deps)))
	r.Delete("/templates/{id}", gate(deps, "template:deregister", handleDeleteTemplate(deps)))
	r.Post("/templates/{id}/deploy", gate(deps, "template:deploy", handleDeployTemplateState(deps)))
	r.Post("/templates/{id}/undeploy", gate(deps, "template:undeploy", handleUndeployTemplateState(deps)))
}

// @concept: service-address-book
func serviceDeclaredInAddressBook(persist persistence.Tables, kind, name string) (bool, error) {
	if persist == nil {
		return false, nil
	}
	var row *persistence.ServiceAddressRow
	if err := persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := persist.ServiceAddressBook().Get(ctx, kind, name, tx)
		row = r
		return err
	}); err != nil {
		return false, fmt.Errorf("service-address-book lookup for %s %q failed: %w", kind, name, err)
	}
	return row != nil, nil
}

type hookInfraErrors struct {
	errs []error
}

func (h *hookInfraErrors) record(err error) {
	h.errs = append(h.errs, err)
}

func (h *hookInfraErrors) first() error {
	if len(h.errs) == 0 {
		return nil
	}
	return h.errs[0]
}

func validatorHooksFor(deps AppDeps, spec node.TemplateSpec) (node.RegistryHooks, *hookInfraErrors) {
	infra := &hookInfraErrors{}
	isLateBind := func(name string) bool {
		for _, ls := range spec.LateBindServices {
			if ls == name {
				return true
			}
		}
		return false
	}
	hooks := node.RegistryHooks{
		KindAliases: deps.KindAliases,
	}
	if deps.ClaimProducers != nil {
		// @concept: service-address-book
		hooks.StoreDeclared = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			if _, ok := deps.ClaimProducers.Get(name); ok {
				return true
			}
			declared, err := serviceDeclaredInAddressBook(deps.Persist, persistence.ServiceKindClaimProducer, name)
			if err != nil {
				infra.record(err)
				return false
			}
			return declared
		}
		hooks.ClaimProducerAdvertisesSplitScope = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			p, ok, err := deps.ClaimProducers.ResolveWithContext(context.Background(), name, "", nil)
			if err != nil {
				infra.record(fmt.Errorf("resolving claim producer %q: %w", name, err))
				return false
			}
			if !ok {
				return false
			}
			caps, err := p.Capabilities(context.Background())
			if err != nil {
				infra.record(fmt.Errorf("claim producer %q capabilities: %w", name, err))
				return false
			}
			return caps.SupportsSplitScope
		}
	}
	if deps.ClaimProducerDeclaredErrorClasses != nil {
		hooks.ClaimProducerDeclaredErrorClasses = func(name string) ([]string, bool) {
			if isLateBind(name) {
				return nil, false
			}
			return deps.ClaimProducerDeclaredErrorClasses(name)
		}
	}
	if deps.Publishers != nil {
		// @concept: publisher
		hooks.PublisherDeclaredKinds = func(name string) ([]string, bool) {
			if isLateBind(name) {
				return nil, false
			}
			client, ok := deps.Publishers.Get(name)
			if !ok {
				return nil, false
			}
			kinds, err := client.SupportedKinds(context.Background())
			if err != nil {
				infra.record(fmt.Errorf("publisher %q capabilities: %w", name, err))
				return nil, false
			}
			return kinds, true
		}
	}
	hooks.NamedLockDeclared = func(name string) bool {
		_, ok := deps.NamedLocks.Get(name)
		return ok
	}
	if deps.Executors != nil {
		// @concept: service-address-book
		hooks.ExecutorDeclared = func(name string) bool {
			if isLateBind(name) {
				return true
			}
			if builtin.IsBuiltinAlias(name) {
				return true
			}
			if _, ok := deps.Executors[name]; ok {
				return true
			}
			declared, err := serviceDeclaredInAddressBook(deps.Persist, persistence.ServiceKindExecutor, name)
			if err != nil {
				infra.record(err)
				return false
			}
			return declared
		}
	} else if deps.KindAliases != nil {
		hooks.ExecutorDeclared = func(name string) bool {
			return builtin.IsBuiltinAlias(name)
		}
	}
	if deps.ExecutorCapabilities != nil {
		hooks.ExecutorDeclaredTags = func(name string) ([]string, bool) {
			if tags, ok := builtin.DeclaredTagsFor(name); ok {
				return tags, true
			}
			tags, _, _, ok := deps.ExecutorCapabilities(name)
			return tags, ok
		}
		hooks.ExecutorDeclaredErrorClasses = func(name string) ([]string, bool) {
			if classes, ok := builtin.DeclaredErrorClassesFor(name); ok {
				return classes, true
			}
			_, classes, _, ok := deps.ExecutorCapabilities(name)
			return classes, ok
		}
		hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
			if isLateBind(name) {
				return nil, true
			}
			if schema, ok := builtin.SchemaFor(name); ok {
				return schema, true
			}
			_, _, schema, ok := deps.ExecutorCapabilities(name)
			return schema, ok
		}
	} else if deps.KindAliases != nil {
		hooks.ExecutorDeclaredTags = func(name string) ([]string, bool) {
			return builtin.DeclaredTagsFor(name)
		}
		hooks.ExecutorDeclaredErrorClasses = builtin.DeclaredErrorClassesFor
		hooks.ExecutorExpectedAttributesSchema = func(name string) ([]byte, bool) {
			return builtin.SchemaFor(name)
		}
	}
	return hooks, infra
}

func handleRegisterTemplate(deps AppDeps) http.HandlerFunc {
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
		if tag != "" && strings.HasPrefix(tag, composeReservedPrefix) && !isComposeOrigin(req) {
			badRequest(w, "tag uses reserved prefix \"compose:\" (managed by the compose command)")
			return
		}

		spec := *specBody
		hooks, infra := validatorHooksFor(deps, spec)
		res := node.ValidateTemplate(&spec, hooks)
		if err := infra.first(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "template validation could not consult the service registry (transient infrastructure fault, not a validation failure): " + err.Error(),
			})
			return
		}
		if !res.Ok() {
			entries := make([]map[string]any, 0, len(res.Errors)+len(res.StructuredErrors))
			for _, e := range res.Errors {
				entries = append(entries, map[string]any{"path": e.Path, "msg": e.Msg})
			}
			entries = append(entries, res.StructuredErrors...)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":               shared.ErrTemplateValidation.Error(),
				"validation_errors":   entries,
				"validation_warnings": findingsToProjection(staticWarningsToFindings(res.Warnings)),
			})
			return
		}
		staticWarnings := staticWarningsToFindings(res.Warnings)
		node.ApplyFrameResolutionDefaults(&spec)
		node.CanonicalizeKindSugar(&spec, deps.KindAliases)
		if err := node.CanonicalizeSendMessageSugar(&spec, deps.KindAliases); err != nil {
			badRequest(w, err.Error())
			return
		}
		node.CanonicalizeAggregationPolicyDefault(&spec)

		hash, err := canonical.CanonicalSpecHash(spec)
		if err != nil {
			writeError(w, err)
			return
		}

		warningsAsErrors := req.URL.Query().Get("warnings_as_errors") == "true"
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
		mergedWarnings := append(staticWarnings, outcome.Warnings...)
		if len(outcome.Errors) > 0 || (warningsAsErrors && len(mergedWarnings) > 0) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":               "template validation pipeline rejected the registration",
				"validation_errors":   findingsToProjection(outcome.Errors),
				"validation_warnings": findingsToProjection(mergedWarnings),
				"warnings_as_errors":  warningsAsErrors,
			})
			return
		}

		var existing *persistence.TemplateRow
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.Templates().GetByHash(ctx, hash, tx)
			existing = r
			return err
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if existing != nil {
			if WriteDryRunResponse(w, req, "would_have_registered", map[string]any{
				"template_hash":       hash,
				"tag":                 tag,
				"source":              source,
				"validation_warnings": findingsToProjection(mergedWarnings),
				"already_registered":  true,
			}) {
				return
			}
			if tag != "" {
				err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
					return upsertTagIfNotConflicting(ctx, deps, req, tag, hash, tx)
				})
				if errors.Is(err, errTagMoveForbidden) {
					writeJSON(w, http.StatusForbidden, map[string]any{"error": errTagMoveForbidden.Error()})
					return
				}
				if err != nil {
					writeError(w, err)
					return
				}
			}
			tags := tagsForTemplate(req.Context(), deps, hash)
			writeJSON(w, http.StatusOK, templateRegisterResponse{
				TemplateID:         hash,
				Tags:               tags,
				ValidationWarnings: findingsToProjection(mergedWarnings),
			})
			return
		}

		if WriteDryRunResponse(w, req, "would_have_registered", map[string]any{
			"template_hash":       hash,
			"tag":                 tag,
			"source":              source,
			"validation_warnings": findingsToProjection(mergedWarnings),
		}) {
			return
		}

		canonBytes, err := canonical.CanonicalSpecBytes(spec)
		if err != nil {
			writeError(w, err)
			return
		}

		var fanOutErr error
		var fanOutDetails map[string]error
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
				if err := upsertTagIfNotConflicting(ctx, deps, req, tag, hash, tx); err != nil {
					return err
				}
			}
			_, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateRegistered, hash, spec, TemplatePayload{Spec: canonBytes}, tx)
			if ferr != nil {
				fanOutErr = ferr
				fanOutDetails = perStore
				return ferr
			}
			return nil
		})
		if errors.Is(err, errTagMoveForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errTagMoveForbidden.Error()})
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
			writeError(w, err)
			return
		}
		tags := tagsForTemplate(req.Context(), deps, hash)
		writeJSON(w, http.StatusCreated, templateRegisterResponse{
			TemplateID:         hash,
			Tags:               tags,
			ValidationWarnings: findingsToProjection(mergedWarnings),
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
		hooks, infra := validatorHooksFor(deps, spec)
		res := node.ValidateTemplate(&spec, hooks)
		if err := infra.first(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "template validation could not consult the service registry (transient infrastructure fault, not a validation failure): " + err.Error(),
			})
			return
		}

		validationErrors := make([]map[string]any, 0, len(res.Errors)+len(res.StructuredErrors))
		for _, e := range res.Errors {
			validationErrors = append(validationErrors, map[string]any{"path": e.Path, "msg": e.Msg})
		}
		validationErrors = append(validationErrors, res.StructuredErrors...)

		validationWarnings := make([]map[string]string, 0, len(res.Warnings))
		for _, wn := range res.Warnings {
			validationWarnings = append(validationWarnings, map[string]string{"path": wn.Path, "msg": wn.Msg})
		}

		if !res.Ok() {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                  false,
				"validation_errors":   validationErrors,
				"validation_warnings": validationWarnings,
			})
			return
		}

		node.ApplyFrameResolutionDefaults(&spec)
		node.CanonicalizeKindSugar(&spec, deps.KindAliases)
		if err := node.CanonicalizeSendMessageSugar(&spec, deps.KindAliases); err != nil {
			validationErrors = append(validationErrors, map[string]any{"path": "nodes", "msg": err.Error()})
		}
		node.CanonicalizeAggregationPolicyDefault(&spec)

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
	out := map[string]string{"path": path, "msg": f.Message}
	if f.Class != "" {
		out["class"] = f.Class
	}
	return out
}

func findingToProjectionAny(f runtime.ValidationFinding) map[string]any {
	p := findingToProjection(f)
	out := map[string]any{"path": p["path"], "msg": p["msg"]}
	if c, ok := p["class"]; ok {
		out["class"] = c
	}
	return out
}

func findingsToProjection(fs []runtime.ValidationFinding) []map[string]string {
	out := make([]map[string]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, findingToProjection(f))
	}
	return out
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
				RegisteredAt: r.RegisteredAt.UTC().Format(time.RFC3339),
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
			RegisteredAt: row.RegisteredAt.UTC().Format(time.RFC3339),
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
		var fanOutErr error
		var fanOutDetails map[string]error
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			_, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateDeregistered, hash, row.Spec, TemplatePayload{}, tx)
			if ferr != nil {
				fanOutErr = ferr
				fanOutDetails = perStore
				return ferr
			}
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
			if fanOutErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":   "template lifecycle fan-out failed",
					"details": fanOutDetails,
				})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

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

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
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
			if isDryRun {
				return errDryRunOK
			}
			tagRows, err := deps.Persist.TemplateTags().ListByTemplate(ctx, hash, tx)
			if err != nil {
				return err
			}
			tags := make([]string, 0, len(tagRows))
			for _, t := range tagRows {
				tags = append(tags, t.Tag)
			}
			_, perStore, ferr := FanOutTemplateEvent(ctx, deps, EventTemplateDeployed, hash, row.Spec, TemplatePayload{Tags: tags}, tx)
			if ferr != nil {
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

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
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
	dec := json.NewDecoder(bytes.NewReader(body))
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
	suffix := strings.ToLower(s[len("sha256-"):])
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
		if deps.Logger != nil {
			deps.Logger.Warn("tagsForTemplate: list tags failed; presenting template as untagged",
				"template_hash", templateHash,
				"error", err.Error())
		}
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Tag)
	}
	return out
}
