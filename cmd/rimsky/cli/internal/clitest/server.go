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
	r.Get("/health", s.handleHealth)

	r.Post("/templates", s.handleRegisterTemplate)
	r.Get("/templates", s.handleListTemplates)
	r.Get("/templates/{id}", s.handleGetTemplate)
	r.Post("/templates/{id}/deploy", s.handleDeployTemplate)
	r.Post("/templates/{id}/undeploy", s.handleUndeployTemplate)
	r.Delete("/templates/{id}", s.handleDeleteTemplate)

	r.Post("/tags", s.handleCreateTag)
	r.Get("/tags", s.handleListTags)
	r.Put("/tags/{tag}", s.handleMoveTag)
	r.Delete("/tags/{tag}", s.handleDeleteTag)

	r.Post("/instances", s.handleCreateInstance)
	r.Get("/instances", s.handleListInstances)
	r.Get("/instances/{idOrKey}", s.handleGetInstance)
	r.Delete("/instances/{idOrKey}", s.handleDeleteInstance)
	r.Get("/instances/{idOrKey}/nodes", s.handleListInstanceNodes)

	r.Get("/nodes/{id}", s.handleGetNode)
	r.Post("/nodes/{id}/invalidate", s.handleInvalidateNode)
	r.Post("/nodes/{id}/reset", s.handleResetNode)

	r.Get("/events", s.handleListEvents)

	r.Post("/admin/scheduled-nodes/{node_id}/force-fire", s.handleAdminForceFire)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/health") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"supervisors": []any{},
		"node_counts": map[string]int{},
	})
}

func (s *Server) handleRegisterTemplate(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/templates") {
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

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/templates") {
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
	if s.maybeFail(w, r, "/templates/"+ref) {
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
	if s.maybeFail(w, r, "/templates/"+ref+"/deploy") {
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
	if s.maybeFail(w, r, "/templates/"+ref+"/undeploy") {
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
	if s.maybeFail(w, r, "/templates/"+ref) {
		return
	}
	isTagForm := !looksLikeHashFake(ref)
	hash := s.State.LookupRef(ref)
	if hash == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	if isTagForm {
		// Tag-form: count remaining tags pointing at the same hash.
		if n := s.State.CountTagsForHash(hash); n > 1 {
			s.State.DeleteTag(ref)
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "tag_only": true})
			return
		}
		// last tag → fall through to template delete.
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
	if s.maybeFail(w, r, "/tags") {
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
	if s.maybeFail(w, r, "/tags") {
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
	if s.maybeFail(w, r, "/tags/"+tag) {
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
	if s.maybeFail(w, r, "/tags/"+tag) {
		return
	}
	if !s.State.DeleteTag(tag) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tag not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/instances") {
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
	if s.maybeFail(w, r, "/instances") {
		return
	}
	hash := r.URL.Query().Get("template_hash")
	// instance_key is intentionally NOT honored here: the real
	// control-api's /instances endpoint filters only on template_hash
	// and active. Keeping the fake aligned with the real surface
	// avoids tests passing against the fake while breaking against the
	// real server.
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
	if s.maybeFail(w, r, "/instances/"+idOrKey) {
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
	if s.maybeFail(w, r, "/instances/"+idOrKey) {
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

func (s *Server) handleListInstanceNodes(w http.ResponseWriter, r *http.Request) {
	idOrKey := chi.URLParam(r, "idOrKey")
	if s.maybeFail(w, r, "/instances/"+idOrKey+"/nodes") {
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

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/nodes/"+id) {
		return
	}
	n, ok := s.State.GetNode(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleInvalidateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/nodes/"+id+"/invalidate") {
		return
	}
	if _, ok := s.State.GetNode(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleResetNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.maybeFail(w, r, "/nodes/"+id+"/reset") {
		return
	}
	if _, ok := s.State.GetNode(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	if s.maybeFail(w, r, "/events") {
		return
	}
	q := r.URL.Query()
	instanceID := q.Get("instance_id")
	cursor := q.Get("cursor")
	var after int64
	if cursor != "" {
		v, err := strconv.ParseInt(cursor, 10, 64)
		if err == nil {
			after = v
		}
	}
	events := s.State.EventsFor(instanceID, after)
	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	// Mirror the live control-api (`foundation/persistence/postgres/events.go`):
	// next_cursor is set only when the page is full (len == limit). A
	// partial page returns next_cursor="" so clients know to wait
	// rather than continue paging.
	nextCursor := ""
	full := len(events) >= limit
	if len(events) > limit {
		events = events[:limit]
	}
	if full && len(events) > 0 {
		nextCursor = fmt.Sprintf("%d", events[len(events)-1].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":      events,
		"next_cursor": nextCursor,
	})
}

func (s *Server) handleAdminForceFire(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "node_id")
	if s.maybeFail(w, r, "/admin/scheduled-nodes/"+id+"/force-fire") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// validTagFake mirrors the control-api's tag regex.
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
