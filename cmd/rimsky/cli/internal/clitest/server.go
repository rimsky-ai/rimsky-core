// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package clitest provides an httptest-backed fake control-api for
// CLI tests. It lives under internal/ because it imports rimsky's
// canonical-hash and template-spec packages to mirror the production
// hashing predicate exactly — that coupling is sanctioned because
// clitest exists to validate the CLI against rimsky internals, not as
// a public test fixture. Only packages under control/cli/ may import it.
//
// Usage:
//
//	srv := clitest.NewServer(t)
//	defer srv.Close()
//	srv.State.RegisterTemplate(spec, "tag", "")
//	client := cli.NewClient(srv.URL)
//
// FailNext lets a test inject failures keyed by "METHOD path" — when
// matched, the handler returns the configured status+body before
// touching state. Times decrements on each match; 0 means once.
package clitest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// Server wraps an httptest.Server with state and failure injection.
type Server struct {
	*httptest.Server
	State *InMemoryState

	failMu   sync.Mutex
	failNext map[string]*FailureSpec
}

// FailureSpec configures a one-shot failure injection.
type FailureSpec struct {
	Status int
	Body   any
	// Times: how many subsequent calls fail; 0 means once.
	Times int
}

// NewServer constructs a Server bound to a fresh in-memory state.
func NewServer(t testing.TB) *Server {
	t.Helper()
	srv := &Server{
		State:    NewInMemoryState(),
		failNext: map[string]*FailureSpec{},
	}
	r := chi.NewRouter()
	srv.registerRoutes(r)
	srv.Server = httptest.NewServer(r)
	return srv
}

// SetFailure configures a failure for the next call to "METHOD path"
// (path may be a chi-style template). Times <= 0 means once.
func (s *Server) SetFailure(method, path string, spec FailureSpec) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	s.failNext[method+" "+path] = &spec
}

func (s *Server) maybeFail(w http.ResponseWriter, r *http.Request, route string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	key := r.Method + " " + route
	spec, ok := s.failNext[key]
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(spec.Status)
	if spec.Body != nil {
		_ = json.NewEncoder(w).Encode(spec.Body)
	}
	if spec.Times <= 1 {
		delete(s.failNext, key)
	} else {
		spec.Times--
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (s *Server) registerRoutes(r chi.Router) {
	r.Get("/v1/health", s.handleHealth)

	r.Post("/v1/templates", s.handleRegisterTemplate)
	r.Post("/v1/templates/validate", s.handleValidateTemplate)
	r.Get("/v1/templates", s.handleListTemplates)
	r.Get("/v1/templates/{id}", s.handleGetTemplate)
	r.Post("/v1/templates/{id}/deploy", s.handleDeployTemplate)
	r.Post("/v1/templates/{id}/undeploy", s.handleUndeployTemplate)
	r.Delete("/v1/templates/{id}", s.handleDeleteTemplate)

	r.Post("/v1/tags", s.handleCreateTag)
	r.Get("/v1/tags", s.handleListTags)
	r.Put("/v1/tags/{tag}", s.handleMoveTag)
	r.Delete("/v1/tags/{tag}", s.handleDeleteTag)

	r.Post("/v1/instances", s.handleCreateInstance)
	r.Get("/v1/instances", s.handleListInstances)
	r.Get("/v1/instances/{idOrKey}", s.handleGetInstance)
	r.Delete("/v1/instances/{idOrKey}", s.handleDeleteInstance)
	r.Post("/v1/instances/{idOrKey}/terminate", s.handleTerminateInstance)
	r.Get("/v1/instances/{idOrKey}/nodes", s.handleListInstanceNodes)
	r.Get("/v1/instances/{idOrKey}/breakpoint-hits", s.handleListBreakpointHits)

	r.Get("/v1/nodes/{id}", s.handleGetNode)
	r.Post("/v1/nodes/{id}/reset", s.handleResetNode)

	r.Get("/v1/events", s.handleListEvents)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/health") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"supervisors": []any{},
		"node_counts": map[string]int{},
	})
}

func (s *Server) handleRegisterTemplate(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/templates") {
		return
	}
	var body struct {
		Spec   map[string]any `json:"spec"`
		Tag    string         `json:"tag"`
		Source string         `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	if body.Spec == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing spec"})
		return
	}
	if body.Tag != "" && !validTagFake(body.Tag) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid tag identifier"})
		return
	}
	hash, isNew := s.State.RegisterTemplate(body.Spec, body.Tag, body.Source)
	tags := []string{}
	for _, t := range s.State.ListTags() {
		if t.TemplateID == hash {
			tags = append(tags, t.Tag)
		}
	}
	status := http.StatusOK
	if isNew {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"template_id": hash,
		"tags":        tags,
	})
}

// handleValidateTemplate mirrors POST /templates/validate: always HTTP
// 200 with {ok, validation_errors, validation_warnings}, and persists
// nothing (it never touches s.State). This fake does not run the real
// validation pipeline; the verdict is derived deterministically from
// the spec so tests can drive the not-ok path — any node referencing the
// sentinel executor "drift-executor" yields an error, and any node
// referencing "warn-executor" yields a warning. `?warnings_as_errors=true`
// folds warnings into the ok verdict, mirroring the live handler.
func (s *Server) handleValidateTemplate(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/templates/validate") {
		return
	}
	var body struct {
		Spec map[string]any `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	if body.Spec == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing spec"})
		return
	}
	errs := []map[string]string{}
	warns := []map[string]string{}
	if nodes, ok := body.Spec["nodes"].([]any); ok {
		for i, raw := range nodes {
			n, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch n["executor"] {
			case "drift-executor":
				errs = append(errs, map[string]string{
					"path": fmt.Sprintf("nodes[%d].executor", i),
					"msg":  "undeclared executor \"drift-executor\"",
				})
			case "warn-executor":
				warns = append(warns, map[string]string{
					"path": fmt.Sprintf("nodes[%d].executor", i),
					"msg":  "executor \"warn-executor\" is unreachable in the discovery cache",
				})
			}
		}
	}
	warningsAsErrors := r.URL.Query().Get("warnings_as_errors") == "true"
	ok := len(errs) == 0 && (!warningsAsErrors || len(warns) == 0)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  ok,
		"validation_errors":   errs,
		"validation_warnings": warns,
	})
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/templates") {
		return
	}
	stateFilter := r.URL.Query().Get("state")
	out := []map[string]any{}
	for _, t := range s.State.ListTemplates() {
		if stateFilter != "" && t.State != stateFilter {
			continue
		}
		tags := []string{}
		for _, tg := range s.State.ListTags() {
			if tg.TemplateID == t.Hash {
				tags = append(tags, tg.Tag)
			}
		}
		out = append(out, map[string]any{
			"id":            t.Hash,
			"state":         t.State,
			"registered_at": t.RegisteredAt.Format(time.RFC3339),
			"source":        t.Source,
			"tags":          tags,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"templates":   out,
		"next_cursor": "",
	})
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/templates/"+ref) {
		return
	}
	hash := s.State.LookupRef(ref)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	t, ok := s.State.GetTemplate(hash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	tags := []string{}
	for _, tg := range s.State.ListTags() {
		if tg.TemplateID == hash {
			tags = append(tags, tg.Tag)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            t.Hash,
		"state":         t.State,
		"registered_at": t.RegisteredAt.Format(time.RFC3339),
		"source":        t.Source,
		"tags":          tags,
		"spec":          t.Spec,
	})
}

func (s *Server) handleDeployTemplate(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/templates/"+ref+"/deploy") {
		return
	}
	hash := s.State.LookupRef(ref)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	t, ok := s.State.GetTemplate(hash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if t.State == "deployed" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "deployed", "no_op": true})
		return
	}
	s.State.SetTemplateState(hash, "deployed")
	writeJSON(w, http.StatusOK, map[string]any{"state": "deployed"})
}

func (s *Server) handleUndeployTemplate(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/templates/"+ref+"/undeploy") {
		return
	}
	hash := s.State.LookupRef(ref)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	t, ok := s.State.GetTemplate(hash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if t.State == "undeployed" || t.State == "registered" {
		writeJSON(w, http.StatusOK, map[string]any{"state": "undeployed", "no_op": true})
		return
	}
	if active := s.State.CountActiveInstances(hash); active > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "template has active instances",
			"active_count": active,
		})
		return
	}
	s.State.SetTemplateState(hash, "undeployed")
	writeJSON(w, http.StatusOK, map[string]any{"state": "undeployed"})
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/templates/"+ref) {
		return
	}
	isTagForm := !looksLikeHashFake(ref)
	hash := s.State.LookupRef(ref)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if isTagForm {
		// @deliberate: tag-form delete removes only the tag while other tags still point at the hash; the underlying template is removed only when this was the last tag.
		if n := s.State.CountTagsForHash(hash); n > 1 {
			s.State.DeleteTag(ref)
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "tag_only": true})
			return
		}
	}
	t, ok := s.State.GetTemplate(hash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if t.State == "deployed" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "template is in 'deployed' state; undeploy first"})
		return
	}
	if active := s.State.CountActiveInstances(hash); active > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "template has active instances", "active_count": active})
		return
	}
	s.State.DeleteTemplate(hash)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/tags") {
		return
	}
	var body struct {
		Tag      string `json:"tag"`
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	if !validTagFake(body.Tag) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid tag identifier"})
		return
	}
	hash := s.State.LookupRef(body.Template)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if existing := s.State.LookupRef(body.Tag); existing != "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "tag already exists"})
		return
	}
	s.State.SetTagHash(body.Tag, hash)
	writeJSON(w, http.StatusCreated, map[string]any{"tag": body.Tag, "template_id": hash})
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/tags") {
		return
	}
	tags := s.State.ListTags()
	writeJSON(w, http.StatusOK, map[string]any{
		"tags":        tags,
		"next_cursor": "",
	})
}

func (s *Server) handleMoveTag(w http.ResponseWriter, r *http.Request) {
	tag := chi.URLParam(r, "tag")
	if s.maybeFail(w, r, "/v1/tags/"+tag) {
		return
	}
	if existing := s.State.LookupRef(tag); existing == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tag not found"})
		return
	}
	var body struct {
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	hash := s.State.LookupRef(body.Template)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	s.State.SetTagHash(tag, hash)
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "template_id": hash})
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	tag := chi.URLParam(r, "tag")
	if s.maybeFail(w, r, "/v1/tags/"+tag) {
		return
	}
	if !s.State.DeleteTag(tag) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tag not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/instances") {
		return
	}
	var body struct {
		Template    string         `json:"template"`
		InstanceKey *string        `json:"instance_key,omitempty"`
		Params      map[string]any `json:"params,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	if body.InstanceKey != nil && *body.InstanceKey == "" {
		body.InstanceKey = nil
	}
	hash := s.State.LookupRef(body.Template)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	t, ok := s.State.GetTemplate(hash)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if t.State != "deployed" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "template not deployed"})
		return
	}
	inst, existed, err := s.State.CreateInstance(hash, body.InstanceKey, body.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	resp := map[string]any{
		"instance_id":   inst.ID,
		"template_hash": inst.TemplateHash,
		"node_count":    nodeCountForSpec(t.Spec),
	}
	if inst.InstanceKey != nil {
		resp["instance_key"] = *inst.InstanceKey
	}
	writeJSON(w, status, resp)
}

func nodeCountForSpec(spec map[string]any) int {
	if nodes, ok := spec["nodes"].([]any); ok {
		return len(nodes)
	}
	return 0
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/instances") {
		return
	}
	hash := r.URL.Query().Get("template_hash")
	// @constraint: the real control-api's /instances endpoint filters only on template_hash and active, so this fake must also ignore instance_key — otherwise tests pass here while breaking against the real server.
	out := []map[string]any{}
	for _, inst := range s.State.ListInstances(hash, "") {
		out = append(out, instanceToWire(inst))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instances":   out,
		"next_cursor": "",
	})
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey) {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	writeJSON(w, http.StatusOK, instanceToWire(inst))
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey) {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	if inst.TerminatedAt == nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "instance is not in terminal state; wait for terminated_at to be set",
		})
		return
	}
	s.State.DeleteInstance(inst.ID)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleTerminateInstance mirrors POST /instances/{idOrKey}/terminate: it
// marks the instance terminal (sets terminated_at) and returns the updated
// instance projection with 200. Idempotent — an already-terminal instance
// returns its current projection unchanged. The optional `{reason}` body is
// accepted and ignored (the real handler records it as an audit event;
// the fake holds no event log for terminate). This mirrors the live
// control-api force-terminate surface so CLI tests don't pass against the
// fake while breaking against the real server.
func (s *Server) handleTerminateInstance(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey+"/terminate") {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	if inst.TerminatedAt == nil {
		s.State.SetInstanceTerminated(inst.ID, nil)
		inst = s.State.FindInstance(idOrKey)
	}
	writeJSON(w, http.StatusOK, instanceToWire(inst))
}

func (s *Server) handleListInstanceNodes(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey+"/nodes") {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	out := s.State.ListNodes(inst.ID)
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out, "next_cursor": ""})
}

// handleListBreakpointHits mirrors GET /instances/{idOrKey}/breakpoint-hits:
// the read-only twin of the MCP `rimsky://instances/{id}/breakpoint-hits`
// resource. Returns the live route's {hits, next_since, truncated} shape,
// fetching limit+1 rows so `truncated` reflects a row beyond the requested
// page. since defaults to 0; limit defaults to 100 and is capped at 500,
// matching resourceReadDefaultLimit / resourceReadMaxLimit on the real
// route so CLI tests don't pass against the fake while breaking against
// the real server.
func (s *Server) handleListBreakpointHits(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey+"/breakpoint-hits") {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	q := r.URL.Query()
	var since int64
	if v := q.Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			since = n
		}
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	hits := s.State.BreakpointHitsFor(inst.ID, since, limit)
	truncated := len(hits) > limit
	if truncated {
		hits = hits[:limit]
	}
	nextSince := since
	if len(hits) > 0 {
		if seq, ok := hits[len(hits)-1]["seq"].(int64); ok {
			nextSince = seq
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":       hits,
		"next_since": nextSince,
		"truncated":  truncated,
	})
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/nodes/"+id) {
		return
	}
	n, ok := s.State.GetNode(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleResetNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/v1/nodes/"+id+"/reset") {
		return
	}
	if _, ok := s.State.GetNode(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/events") {
		return
	}
	q := r.URL.Query()
	instanceID := q.Get("instance_id")
	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	// @constraint: cursor decode must match foundation/persistence/{sqlite,postgres}/events.go::decodeEventCursor (base64 JSON {"o":<occurred>,"i":<id>}); a non-base64 cursor (e.g. the CLI's old numeric fmt.Sprintf("%d", lastSeenID) token) must be rejected with `events.list: bad cursor` so a CLI test can't silently pass against a token the live server would 500 on.
	cursor := q.Get("cursor")
	var (
		cursorOccurred time.Time
		cursorID       int64
		hasCursor      bool
	)
	if cursor != "" {
		oc, id, err := decodeEventCursorFake(cursor)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "events.list: bad cursor: " + err.Error(),
			})
			return
		}
		cursorOccurred, cursorID, hasCursor = oc, id, true
	}

	events := s.State.EventsPage(instanceID, cursorOccurred, cursorID, hasCursor, limit)

	// @constraint: next_cursor must mirror foundation/persistence/{sqlite,postgres}/events.go — opaque keyset of the last (oldest) row on the page, set only when the page is full (len == limit); a partial page returns "" so clients wait rather than page on past the tail.
	nextCursor := ""
	if len(events) == limit && len(events) > 0 {
		last := events[len(events)-1]
		nextCursor = encodeEventCursorFake(eventOccurredAt(last), last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":      events,
		"next_cursor": nextCursor,
	})
}

// eventCursorFake mirrors the live persistence layer's cursor struct
// (foundation/persistence/{sqlite,postgres}/events.go) byte-for-byte: the
// keyset position is base64(JSON {"o":<occurred>,"i":<id>}). The fake
// re-implements it (rather than importing the unexported persistence type)
// so the CLI is driven against the real wire shape.
type eventCursorFake struct {
	O time.Time `json:"o"`
	I int64     `json:"i"`
}

func encodeEventCursorFake(occurred time.Time, id int64) string {
	b, _ := json.Marshal(eventCursorFake{O: occurred, I: id})
	return base64.StdEncoding.EncodeToString(b)
}

func decodeEventCursorFake(s string) (time.Time, int64, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, err
	}
	var c eventCursorFake
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, 0, err
	}
	return c.O, c.I, nil
}

func instanceToWire(inst *storedInstance) map[string]any {
	m := map[string]any{
		"id":            inst.ID,
		"template_hash": inst.TemplateHash,
		"params":        inst.Params,
		"created_at":    inst.CreatedAt.Format(time.RFC3339),
	}
	if inst.InstanceKey != nil {
		m["instance_key"] = *inst.InstanceKey
	}
	if inst.TerminatedAt != nil {
		m["terminated_at"] = inst.TerminatedAt.Format(time.RFC3339)
	}
	return m
}

// @constraint: tagFakeRe / hashFakeRe / hashFakeStr must mirror the real control-api's tag-vs-hash regexes (lib/control/api template handlers); any drift here lets CLI tests accept refs the live server would reject (or vice versa).
var (
	tagFakeRe   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$`)
	hashFakeRe  = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
	hashFakeStr = "sha256-"
)

func validTagFake(s string) bool {
	if !tagFakeRe.MatchString(s) {
		return false
	}
	if looksLikeHashFake(s) {
		return false
	}
	return true
}

func looksLikeHashFake(s string) bool {
	if !strings.HasPrefix(s, hashFakeStr) {
		return false
	}
	return hashFakeRe.MatchString(s)
}
