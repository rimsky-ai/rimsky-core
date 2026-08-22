// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package clitest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/template/canonical"
)

type InMemoryState struct {
	mu           sync.Mutex
	templates    map[string]*storedTemplate
	tags         map[string]string
	instances    map[string]*storedInstance
	events       []cli.Event
	nodes        map[string]map[string]*cli.Node
	nextEvent    int64
	nextInstance int64

	breakpointHits map[string][]map[string]any
	nextHitSeq     int64

	messageIdem map[string]string
	nextMessage int64

	pendingMessages map[string]int
	runningFrames   map[string]int
	activityScript  map[string][]InstanceActivity
	activityReads   map[string]int
}

type storedTemplate struct {
	Hash         string
	State        string
	Source       string
	Spec         map[string]any
	RegisteredAt time.Time
}

type storedInstance struct {
	ID           string
	TemplateHash string
	InstanceKey  *string
	Params       map[string]any
	CreatedAt    time.Time
	TerminatedAt *time.Time
}

func NewInMemoryState() *InMemoryState {
	return &InMemoryState{
		templates:      map[string]*storedTemplate{},
		tags:           map[string]string{},
		instances:      map[string]*storedInstance{},
		nodes:          map[string]map[string]*cli.Node{},
		breakpointHits: map[string][]map[string]any{},
		messageIdem:    map[string]string{},
	}
}

// @decision: compose-driver-sends-empty-message-after-create
func (s *InMemoryState) RecordInstanceMessage(instanceID, idempotencyKey string) (messageID string, fresh bool) {
	if idempotencyKey == "" {
		panic("clitest.RecordInstanceMessage: empty idempotencyKey — boundary gate bypassed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idemBucket := instanceID + "\x00" + idempotencyKey
	if existing, ok := s.messageIdem[idemBucket]; ok {
		return existing, false
	}
	s.nextMessage++
	id := fmt.Sprintf("msg-%d", s.nextMessage)
	s.messageIdem[idemBucket] = id
	return id, true
}

func hashSpec(spec map[string]any) string {
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("clitest.hashSpec: json.Marshal failed on spec %#v: %v", spec, err))
	}
	var ts node.TemplateSpec
	if uerr := json.Unmarshal(raw, &ts); uerr == nil {
		node.ApplyFrameResolutionDefaults(&ts)
		node.CanonicalizeAggregationPolicyDefault(&ts)
		if h, herr := canonical.CanonicalSpecHash(ts); herr == nil {
			return h
		}
	}
	sum := sha256.Sum256(raw)
	return "sha256-" + hex.EncodeToString(sum[:])
}

func (s *InMemoryState) RegisterTemplate(spec map[string]any, tag, source string) (hash string, isNew bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash = hashSpec(spec)
	if _, ok := s.templates[hash]; !ok {
		s.templates[hash] = &storedTemplate{
			Hash:         hash,
			State:        "registered",
			Source:       source,
			Spec:         spec,
			RegisteredAt: time.Now().UTC(),
		}
		isNew = true
	}
	if tag != "" {
		s.tags[tag] = hash
	}
	return hash, isNew
}

func (s *InMemoryState) LookupRef(ref string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupRefLocked(ref)
}

func (s *InMemoryState) lookupRefLocked(ref string) string {
	if _, ok := s.templates[ref]; ok {
		return ref
	}
	if h, ok := s.tags[ref]; ok {
		return h
	}
	return ""
}

func (s *InMemoryState) GetTemplate(hash string) (storedTemplate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[hash]
	if !ok {
		return storedTemplate{}, false
	}
	return *t, true
}

func (s *InMemoryState) ListTemplates() []storedTemplate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storedTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out
}

func (s *InMemoryState) ListTags() []cli.Tag {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]cli.Tag, 0, len(s.tags))
	for tag, hash := range s.tags {
		out = append(out, cli.Tag{Tag: tag, TemplateID: hash, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func (s *InMemoryState) SetTagHash(tag, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[tag] = hash
}

func (s *InMemoryState) CountTagsForHash(hash string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.tags {
		if h == hash {
			n++
		}
	}
	return n
}

func (s *InMemoryState) DeleteTag(tag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tags[tag]; !ok {
		return false
	}
	delete(s.tags, tag)
	return true
}

func (s *InMemoryState) SetTemplateState(hash, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[hash]
	if !ok {
		return false
	}
	t.State = state
	return true
}

func (s *InMemoryState) CountActiveInstances(hash string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, inst := range s.instances {
		if inst.TemplateHash == hash && inst.TerminatedAt == nil {
			n++
		}
	}
	return n
}

func (s *InMemoryState) DeleteTemplate(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.templates, hash)
	for tag, h := range s.tags {
		if h == hash {
			delete(s.tags, tag)
		}
	}
}

func (s *InMemoryState) CreateInstance(hash string, key *string, params map[string]any) (inst *storedInstance, existed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[hash]; !ok {
		return nil, false, fmt.Errorf("template not found")
	}
	if key != nil {
		for _, ex := range s.instances {
			if ex.TemplateHash == hash && ex.InstanceKey != nil && *ex.InstanceKey == *key {
				cp := *ex
				return &cp, true, nil
			}
		}
	}
	s.nextInstance++
	id := fmt.Sprintf("inst-%d", s.nextInstance)
	row := &storedInstance{
		ID:           id,
		TemplateHash: hash,
		InstanceKey:  key,
		Params:       params,
		CreatedAt:    time.Now().UTC(),
	}
	s.instances[id] = row
	s.nodes[id] = map[string]*cli.Node{}
	cp := *row
	return &cp, false, nil
}

func (s *InMemoryState) SetInstanceActivity(id string, pendingMessages, runningFrames int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingMessages == nil {
		s.pendingMessages = map[string]int{}
	}
	if s.runningFrames == nil {
		s.runningFrames = map[string]int{}
	}
	s.pendingMessages[id] = pendingMessages
	s.runningFrames[id] = runningFrames
}

type InstanceActivity struct {
	PendingMessages int
	RunningFrames   int
}

func (s *InMemoryState) SetInstanceActivityScript(id string, steps ...InstanceActivity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activityScript == nil {
		s.activityScript = map[string][]InstanceActivity{}
	}
	s.activityScript[id] = steps
}

func (s *InMemoryState) InstanceActivityReads(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activityReads[id]
}

func (s *InMemoryState) TakeInstanceActivityStep(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activityReads == nil {
		s.activityReads = map[string]int{}
	}
	s.activityReads[id]++
	script := s.activityScript[id]
	if len(script) == 0 {
		return
	}
	if len(script) > 1 {
		s.activityScript[id] = script[1:]
	}
	if s.pendingMessages == nil {
		s.pendingMessages = map[string]int{}
	}
	if s.runningFrames == nil {
		s.runningFrames = map[string]int{}
	}
	s.pendingMessages[id] = script[0].PendingMessages
	s.runningFrames[id] = script[0].RunningFrames
}

func (s *InMemoryState) InstanceActivity(id string) (pendingMessages, runningFrames int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingMessages[id], s.runningFrames[id]
}

func (s *InMemoryState) SetInstanceTerminated(id string, t *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return
	}
	if t == nil {
		now := time.Now().UTC()
		t = &now
	}
	inst.TerminatedAt = t
}

func (s *InMemoryState) IsTerminated(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return false
	}
	return inst.TerminatedAt != nil
}

func (s *InMemoryState) AddNode(instanceID string, node cli.Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodes[instanceID] == nil {
		s.nodes[instanceID] = map[string]*cli.Node{}
	}
	cp := node
	s.nodes[instanceID][node.ID] = &cp
}

func (s *InMemoryState) AddEvent(e cli.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextEvent++
	e.ID = s.nextEvent
	if e.OccurredAt == "" {
		e.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.events = append(s.events, e)
}

func (s *InMemoryState) FindInstance(idOrKey string) *storedInstance {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[idOrKey]; ok {
		cp := *inst
		return &cp
	}
	for _, inst := range s.instances {
		if inst.InstanceKey != nil && *inst.InstanceKey == idOrKey {
			cp := *inst
			return &cp
		}
	}
	return nil
}

func (s *InMemoryState) DeleteInstance(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, id)
	delete(s.nodes, id)
}

func (s *InMemoryState) ListInstances(hash, key string) []*storedInstance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*storedInstance{}
	for _, inst := range s.instances {
		if hash != "" && inst.TemplateHash != hash {
			continue
		}
		if key != "" {
			if inst.InstanceKey == nil || *inst.InstanceKey != key {
				continue
			}
		}
		cp := *inst
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *InMemoryState) MarkFirstActiveTerminated() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.instances))
	for id := range s.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		inst := s.instances[id]
		if inst.TerminatedAt == nil {
			now := time.Now().UTC()
			inst.TerminatedAt = &now
			return id
		}
	}
	return ""
}

func (s *InMemoryState) ListNodes(instanceID string) []cli.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.nodes[instanceID]
	out := make([]cli.Node, 0, len(in))
	for _, n := range in {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *InMemoryState) GetNode(id string) (cli.Node, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.nodes {
		if n, ok := m[id]; ok {
			return *n, true
		}
	}
	return cli.Node{}, false
}

// @concept: node
func (s *InMemoryState) SetNodeRunSummary(id string, summary cli.NodeRunSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.nodes {
		if n, ok := m[id]; ok {
			sc := summary
			n.RunSummary = &sc
			return
		}
	}
}

func (s *InMemoryState) AddBreakpointHit(instanceID string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextHitSeq++
	hit := map[string]any{
		"seq":         s.nextHitSeq,
		"instance_id": instanceID,
	}
	for k, v := range fields {
		hit[k] = v
	}
	if _, ok := hit["hit_id"]; !ok {
		hit["hit_id"] = fmt.Sprintf("hit-%d", s.nextHitSeq)
	}
	s.breakpointHits[instanceID] = append(s.breakpointHits[instanceID], hit)
}

func (s *InMemoryState) BreakpointHitsFor(instanceID string, since int64, limit int) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, h := range s.breakpointHits[instanceID] {
		seq, _ := h["seq"].(int64)
		if seq <= since {
			continue
		}
		cp := map[string]any{}
		for k, v := range h {
			cp[k] = v
		}
		out = append(out, cp)
		if limit > 0 && len(out) >= limit+1 {
			break
		}
	}
	return out
}

func (s *InMemoryState) EventsPage(instanceID string, since, until *time.Time, kind string, cursorOccurred time.Time, cursorID int64, hasCursor bool, limit int) []cli.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := []cli.Event{}
	for _, e := range s.events {
		if instanceID != "" && e.InstanceID != instanceID {
			continue
		}
		if kind != "" && e.Kind != kind {
			continue
		}
		occurred := eventOccurredAt(e)
		if since != nil && occurred.Before(*since) {
			continue
		}
		if until != nil && occurred.After(*until) {
			continue
		}
		filtered = append(filtered, e)
	}
	sort.Slice(filtered, func(i, j int) bool {
		oi := eventOccurredAt(filtered[i])
		oj := eventOccurredAt(filtered[j])
		if !oi.Equal(oj) {
			return oi.After(oj)
		}
		return filtered[i].ID > filtered[j].ID
	})
	out := []cli.Event{}
	for _, e := range filtered {
		if hasCursor {
			oe := eventOccurredAt(e)
			if !(oe.Before(cursorOccurred) || (oe.Equal(cursorOccurred) && e.ID < cursorID)) {
				continue
			}
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func eventOccurredAt(e cli.Event) time.Time {
	t, err := time.Parse(time.RFC3339, e.OccurredAt)
	if err != nil {
		return time.Time{}
	}
	return t
}
