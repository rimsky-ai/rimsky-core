// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state.go — in-memory state for the fake control-api.
//
// Mirrors the contracts that the live control-api enforces:
//   - Tag delete on a hash with no other tags allows template delete.
//   - Template delete refused when state is `deployed`.
//   - Template delete refused when any non-terminal instance binds.
//   - Instance delete refused when terminated_at IS NULL.
//   - Instance create with same instance_key against same template_hash
//     collides on the unique key (returns existing row).
//   - Tag create with hash-shape input is rejected.
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

// InMemoryState backs the fake control-api. Methods are concurrency-safe.
// templates is keyed by hash; tags is keyed tag → hash; instances is
// keyed by id; nodes is keyed instance_id → node_id → node.
type InMemoryState struct {
	mu        sync.Mutex
	templates map[string]*storedTemplate
	tags      map[string]string
	instances map[string]*storedInstance
	events    []cli.Event
	nodes     map[string]map[string]*cli.Node
	nextEvent int64

	// breakpointHits holds pending breakpoint-hit rows keyed by instance
	// ID. Each hit is the flat wire shape the live route returns (seq,
	// hit_id, …, plus snapshot keys). nextHitSeq assigns seq monotonically
	// across all instances, mirroring the live ledger's per-instance seq
	// being a strictly increasing cursor.
	breakpointHits map[string][]map[string]any
	nextHitSeq     int64
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

// NewInMemoryState constructs an empty state.
func NewInMemoryState() *InMemoryState {
	return &InMemoryState{
		templates:      map[string]*storedTemplate{},
		tags:           map[string]string{},
		instances:      map[string]*storedInstance{},
		nodes:          map[string]map[string]*cli.Node{},
		breakpointHits: map[string][]map[string]any{},
	}
}

// hashSpec produces the canonical content hash for a spec map, matching
// the live control-api's CanonicalSpecHash. Falls back to a SHA-256 of
// the raw JSON if the spec cannot be coerced into TemplateSpec — that
// fallback is only used by tests that pass partial / illustrative
// specs and don't care about bit-for-bit hash equality.
//
// json.Marshal on a map[string]any only fails when the map contains a
// non-marshallable value (channels, funcs, complex). Tests pass plain
// JSON-shaped data; if that invariant ever breaks, panic loudly rather
// than silently emitting a malformed sha256-… token that the
// manifest-validation regex (^sha256-[0-9a-f]{64}$) would later reject.
func hashSpec(spec map[string]any) string {
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("clitest.hashSpec: json.Marshal failed on spec %#v: %v", spec, err))
	}
	var ts node.TemplateSpec
	if uerr := json.Unmarshal(raw, &ts); uerr == nil {
		node.ApplyFrameResolutionDefaults(&ts)
		if h, herr := canonical.CanonicalSpecHash(ts); herr == nil {
			return h
		}
	}
	sum := sha256.Sum256(raw)
	return "sha256-" + hex.EncodeToString(sum[:])
}

// RegisterTemplate inserts (or no-ops on existing hash) a template and
// optionally upserts a tag pointing at it.
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

// LookupRef resolves a tag-or-hash to a stored hash, returning empty
// string when unknown.
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

// GetTemplate returns the stored template by hash. Returns a value copy
// (and ok=false when not found) so callers can read fields without
// holding the state mutex.
func (s *InMemoryState) GetTemplate(hash string) (storedTemplate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.templates[hash]
	if !ok {
		return storedTemplate{}, false
	}
	return *t, true
}

// ListTemplates returns templates sorted by hash. Returns value copies
// so callers can read fields without holding the state mutex.
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

// ListTags returns tags sorted by name.
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

// SetTagHash directly upserts a tag. Used by tests setting up state.
func (s *InMemoryState) SetTagHash(tag, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[tag] = hash
}

// CountTagsForHash returns the number of tags pointing at hash.
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

// DeleteTag removes a tag. Returns true if it existed.
func (s *InMemoryState) DeleteTag(tag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tags[tag]; !ok {
		return false
	}
	delete(s.tags, tag)
	return true
}

// SetTemplateState updates a template's state ("registered","deployed","undeployed").
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

// CountActiveInstances returns the number of non-terminal instances bound to hash.
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

// DeleteTemplate removes a template + its tags. No-op if missing.
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

// CreateInstance inserts a new instance. Returns the existing row if a
// matching (hash, key) already exists.
//
// Caller (the HTTP handler) is responsible for the "template must be in
// deployed state" precondition: it issues a 409 before reaching this
// method (see server.go::handleCreateInstance). We re-check
// "template-exists" here because the server resolved the ref to a
// hash earlier and a template-deletion race would invalidate that
// hash by the time this method runs.
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
	id := fmt.Sprintf("inst-%d", len(s.instances)+1)
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

// SetInstanceTerminated marks an instance terminal at the given time.
// If t is nil, sets to now. Used by tests.
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

// IsTerminated reports whether the instance's terminated_at is set.
// Returns false when the instance is unknown. Used by tests asserting
// terminal state without reaching into the unexported storedInstance.
func (s *InMemoryState) IsTerminated(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return false
	}
	return inst.TerminatedAt != nil
}

// AddNode injects a node row for the given instance.
func (s *InMemoryState) AddNode(instanceID string, node cli.Node) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodes[instanceID] == nil {
		s.nodes[instanceID] = map[string]*cli.Node{}
	}
	cp := node
	s.nodes[instanceID][node.ID] = &cp
}

// AddEvent appends an event to the log; assigns ID monotonically.
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

// FindInstance resolves id-or-key to a stored instance. Returns a value
// copy so callers can read fields without holding the state mutex.
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

// DeleteInstance removes an instance. No-op if missing.
func (s *InMemoryState) DeleteInstance(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, id)
	delete(s.nodes, id)
}

// ListInstances returns instances filtered by hash and key, sorted by ID.
// Returns value copies so callers can read fields without holding the
// state mutex.
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

// MarkFirstActiveTerminated sets terminated_at on the first non-terminal
// instance encountered (deterministic order by ID). Returns the id, or
// empty when no active instance exists. Used by tests that drive
// `--no-keep` style flows without external concurrency.
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

// ListNodes returns the nodes for an instance, sorted by ID. Returns
// value copies so callers can read fields without holding the state
// mutex.
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

// GetNode returns a node by ID across all instances. Returns a value
// copy (and ok=false when not found) so callers can read fields
// without holding the state mutex.
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

// SetNodeState updates a node's state.
func (s *InMemoryState) SetNodeState(id, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.nodes {
		if n, ok := m[id]; ok {
			n.State = state
			return
		}
	}
}

// AddBreakpointHit appends a pending breakpoint hit for an instance,
// assigning seq monotonically. The supplied fields are merged into the
// flat wire envelope the live route returns; identity fields (seq,
// instance_id) are filled in here. Used by tests seeding hits.
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

// BreakpointHitsFor returns the pending hits for an instance with seq >
// since, capped at limit+1 rows so the caller can compute `truncated`
// (mirrors the live route's fetch-+1 discipline). Returns value copies.
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

// EventsPage mirrors the live control-api's keyset-paginated read of the
// event log: results are ordered newest-first ((occurred_at, id) DESC) and
// `cursor` is an opaque keyset position — the page returned is strictly
// OLDER than the cursor ((occurred_at, id) < (cursor.occurred, cursor.id)).
// This is byte-for-byte the contract the real persistence layer enforces
// (foundation/persistence/{sqlite,postgres}/events.go); the fake mirrors it
// so CLI tests can't pass against a numeric-cursor shortcut the live server
// rejects.
//
// cursorOccurred/cursorID are the decoded keyset; pass the zero time and
// 0 for the first (uncursored) page. limit bounds the page; the caller
// (the HTTP handler) is responsible for emitting next_cursor only when the
// page is full, matching the live server.
func (s *InMemoryState) EventsPage(instanceID string, cursorOccurred time.Time, cursorID int64, hasCursor bool, limit int) []cli.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := []cli.Event{}
	for _, e := range s.events {
		if instanceID != "" && e.InstanceID != instanceID {
			continue
		}
		filtered = append(filtered, e)
	}
	// @constraint: newest-first (occurred_at, id) DESC matches the live ORDER BY.
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
			// @constraint: keyset predicate — strictly older than the cursor position.
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

// eventOccurredAt parses an event's RFC3339 occurred_at string back to a
// time for keyset comparison. A row whose timestamp fails to parse sorts as
// the zero time (it never wins an ordering comparison) — the seeding helpers
// always emit RFC3339, so this is defensive, not a live path.
func eventOccurredAt(e cli.Event) time.Time {
	t, err := time.Parse(time.RFC3339, e.OccurredAt)
	if err != nil {
		return time.Time{}
	}
	return t
}
