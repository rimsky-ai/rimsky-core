// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package clitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const maxFakePageSize = 500

var BreakpointHitsDefaultPageSize = 100

type Server struct {
	*httptest.Server
	State *InMemoryState

	failMu   sync.Mutex
	failNext map[string]*FailureSpec

	authMu       sync.Mutex
	requiredKey  string
	seenBearers  []string
	unauthorized int

	getInstanceHits atomic.Int64
	listEventsHits  atomic.Int64

	ListInstancesDefaultPageSize int
	ListNodesDefaultPageSize     int
}

func (s *Server) GetInstanceHitCount() int64 { return s.getInstanceHits.Load() }

func (s *Server) ListEventsHitCount() int64 { return s.listEventsHits.Load() }

type FailureSpec struct {
	Status int
	Body   any
	Times  int
}

func NewServer(t testing.TB) *Server {
	t.Helper()
	srv := &Server{
		State:    NewInMemoryState(),
		failNext: map[string]*FailureSpec{},
	}
	r := chi.NewRouter()
	r.Use(srv.requireBearer)
	srv.registerRoutes(r)
	srv.Server = httptest.NewServer(r)
	return srv
}

func (s *Server) RequireBearer(key string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	s.requiredKey = key
}

func (s *Server) SeenBearers() []string {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return append([]string(nil), s.seenBearers...)
}

func (s *Server) UnauthorizedCount() int {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.unauthorized
}

func (s *Server) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.authMu.Lock()
		s.seenBearers = append(s.seenBearers, presented)
		required := s.requiredKey
		refused := required != "" && presented != required
		if refused {
			s.unauthorized++
		}
		s.authMu.Unlock()
		if refused {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	r.Post("/v1/instances/{idOrKey}/messages", s.handleCreateInstanceMessage)
	r.Get("/v1/instances/{idOrKey}/messages", s.handleListInstanceMessages)
	r.Get("/v1/instances/{idOrKey}/frames", s.handleListInstanceFrames)

	r.Get("/v1/nodes/{id}", s.handleGetNode)
	r.Post("/v1/nodes/{id}/reset", s.handleResetNode)

	r.Get("/v1/events", s.handleListEvents)
	r.Get("/v1/audit", s.handleListAudit)
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
	// @decision: template-identity-deployment-canonical
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  ok,
		"validation_errors":   errs,
		"validation_warnings": warns,
		"template_hash":       hashSpec(body.Spec),
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

// @decision: compose-driver-sends-empty-message-after-create
func (s *Server) handleListInstanceMessages(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	if r.URL.Query().Get("pending") == "true" {
		s.State.TakeInstanceActivityStep(inst.ID)
	}
	pendingCount, _ := s.State.InstanceActivity(inst.ID)
	messages := []map[string]any{}
	if r.URL.Query().Get("pending") == "true" {
		for i := 0; i < pendingCount; i++ {
			messages = append(messages, map[string]any{
				"id": fmt.Sprintf("pending-%d", i), "instance_id": inst.ID, "type": "test/pending",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) handleListInstanceFrames(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	_, runningCount := s.State.InstanceActivity(inst.ID)
	frames := []map[string]any{}
	if r.URL.Query().Get("state") == "running" {
		for i := 0; i < runningCount; i++ {
			frames = append(frames, map[string]any{
				"frame_id": fmt.Sprintf("frame-%d", i), "instance_id": inst.ID, "state": "running",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"frames": frames})
}

func (s *Server) handleCreateInstanceMessage(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/v1/instances/"+idOrKey+"/messages") {
		return
	}
	inst := s.State.FindInstance(idOrKey)
	if inst == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "instance not found"})
		return
	}
	var body struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad body"})
		return
	}
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Idempotency-Key header is required"})
		return
	}
	msgID, fresh := s.State.RecordInstanceMessage(inst.ID, idemKey)
	status := http.StatusCreated
	if !fresh {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"message_id": msgID})
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/instances") {
		return
	}
	hash := r.URL.Query().Get("template_hash")
	all := s.State.ListInstances(hash, "")

	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		for i, inst := range all {
			if inst.ID == cursor {
				start = i + 1
				break
			}
		}
	}

	limit := s.ListInstancesDefaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	end := len(all)
	nextCursor := ""
	if limit > 0 && start+limit < len(all) {
		end = start + limit
		nextCursor = all[end-1].ID
	}

	out := []map[string]any{}
	for _, inst := range all[start:end] {
		out = append(out, instanceToWire(inst))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instances":   out,
		"next_cursor": nextCursor,
	})
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	s.getInstanceHits.Add(1)
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
	all := s.State.ListNodes(inst.ID)
	if tag := r.URL.Query().Get("tag"); tag != "" {
		kept := all[:0]
		for _, n := range all {
			for _, t := range n.Tags {
				if t == tag {
					kept = append(kept, n)
					break
				}
			}
		}
		all = kept
	}

	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		for i, n := range all {
			if n.ID == cursor {
				start = i + 1
				break
			}
		}
	}

	limit := s.ListNodesDefaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	end := len(all)
	nextCursor := ""
	if limit > 0 && start+limit < len(all) {
		end = start + limit
		nextCursor = all[end-1].ID
	}

	out := make([]cli.Node, 0, end-start)
	out = append(out, all[start:end]...)
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out, "next_cursor": nextCursor})
}

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
	limit, ok := parseFakeLimit(w, q, BreakpointHitsDefaultPageSize)
	if !ok {
		return
	}
	var afterSeq int64
	if raw := q.Get("cursor"); raw != "" {
		seq, err := persistence.DecodeKeyCursor(raw)
		parsed, convErr := strconv.ParseInt(seq, 10, 64)
		if err != nil || convErr != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "breakpoint-hits: bad cursor"})
			return
		}
		afterSeq = parsed
	}
	hits := s.State.BreakpointHitsFor(inst.ID, afterSeq, limit)
	nextCursor := ""
	if len(hits) > limit {
		hits = hits[:limit]
		if seq, ok := hits[len(hits)-1]["seq"].(int64); ok {
			nextCursor = persistence.EncodeKeyCursor(strconv.FormatInt(seq, 10))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":        hits,
		"next_cursor": nextCursor,
	})
}

func parseFakeLimit(w http.ResponseWriter, q url.Values, fallback int) (int, bool) {
	raw := q.Get("limit")
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "limit must be a positive integer"})
		return 0, false
	}
	if n > maxFakePageSize {
		n = maxFakePageSize
	}
	return n, true
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
	s.listEventsHits.Add(1)
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

	var since, until *time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid since (RFC3339 required)"})
			return
		}
		since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid until (RFC3339 required)"})
			return
		}
		until = &t
	}

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

	kind := q.Get("kind")
	events := s.State.EventsPage(instanceID, since, until, kind, cursorOccurred, cursorID, hasCursor, limit)

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

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/v1/audit") {
		return
	}
	q := r.URL.Query()
	var since, until *time.Time
	for name, into := range map[string]**time.Time{"since": &since, "until": &until} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid " + name + " (RFC3339 required)"})
			return
		}
		*into = &t
	}
	rows := s.State.EventsPage("", since, until, q.Get("kind"), time.Time{}, 0, false, 100)
	writeJSON(w, http.StatusOK, map[string]any{"audit": rows, "next_cursor": ""})
}

type eventCursorFake struct {
	O time.Time `json:"o"`
	I int64     `json:"i"`
}

func encodeEventCursorFake(occurred time.Time, id int64) string {
	return persistence.EncodeCursor(eventCursorFake{O: occurred, I: id})
}

func decodeEventCursorFake(s string) (time.Time, int64, error) {
	var c eventCursorFake
	if err := persistence.DecodeCursor(s, &c); err != nil {
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
