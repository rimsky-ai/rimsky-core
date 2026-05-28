// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state.go — the proxy's in-memory state machine: connected agents,
// lazily-spawned child processes, the spawn-dedup index, and the
// instance binding cache. All state is lost on restart and rebuilt as
// agents reconnect and instance state is consulted (via the
// LifecycleSubscriber subscription or the GET /instances/{id} fallback).
//
// @concept: host-agent-proxy

package main

import (
	"sync"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// proxyState holds all mutable proxy state behind a single RWMutex.
// The maps are guarded by mu; the per-agent pending-ack maps are guarded
// by the agentConnection's own mutex (see agentConnection).
type proxyState struct {
	mu sync.RWMutex

	agents           map[string]*agentConnection    // api_key_id → connection
	spawns           map[string]*spawnState         // spawn_id → metadata
	runScopeBindings map[runScopeBindingKey]string  // (scope_id, binding_name) → spawn_id
	instances        map[string]*instanceCacheEntry // instance_id → cached binding catalog + owner + params
	claimRoutes      map[string]claimRoute          // claim_id → (api_key_id, spawn_id) for Commit/Abandon/Release
}

// claimRoute records which spawned producer holds a given claim so the
// follow-on lifecycle RPCs (Commit/Abandon/Release) — which carry only a
// claim_id, not an instance_id — can route back to the same child.
type claimRoute struct {
	apiKeyID string
	spawnID  string
}

// newProxyState returns an empty proxyState with all maps allocated.
func newProxyState() *proxyState {
	return &proxyState{
		agents:           map[string]*agentConnection{},
		spawns:           map[string]*spawnState{},
		runScopeBindings: map[runScopeBindingKey]string{},
		instances:        map[string]*instanceCacheEntry{},
		claimRoutes:      map[string]claimRoute{},
	}
}

// agentConnection is the proxy-side handle to one connected dev-machine
// agent. sendCh is drained by the agent_server's writer goroutine and
// written to the bidi stream; closed exactly once when the connection is
// dropped or displaced. The pending-ack maps correlate request frames
// (Spawn/Reap/DispatchFrame) with their inbound responses.
type agentConnection struct {
	apiKeyID             string
	agentLabel           string
	localCallbackBaseURL string

	sendCh    chan *genv1.ServerFrame
	closeOnce sync.Once
	closed    chan struct{}

	// pendingMu guards all three pending maps below.
	pendingMu      sync.Mutex
	pendingSpawn   map[string]chan *genv1.SpawnAck      // spawn_id → ack channel
	pendingReap    map[string]chan *genv1.Reaped        // spawn_id → reaped channel
	pendingStreams map[string]chan *genv1.DispatchFrame // stream_id → dispatch-frame channel
}

func newAgentConnection(apiKeyID, label, localCallbackBaseURL string) *agentConnection {
	return &agentConnection{
		apiKeyID:             apiKeyID,
		agentLabel:           label,
		localCallbackBaseURL: localCallbackBaseURL,
		sendCh:               make(chan *genv1.ServerFrame, 64),
		closed:               make(chan struct{}),
		pendingSpawn:         map[string]chan *genv1.SpawnAck{},
		pendingReap:          map[string]chan *genv1.Reaped{},
		pendingStreams:       map[string]chan *genv1.DispatchFrame{},
	}
}

// send enqueues a frame to the agent, returning false if the connection
// has been closed (the writer goroutine has stopped draining sendCh).
func (a *agentConnection) send(frame *genv1.ServerFrame) bool {
	select {
	case <-a.closed:
		return false
	default:
	}
	select {
	case a.sendCh <- frame:
		return true
	case <-a.closed:
		return false
	}
}

// close marks the connection closed exactly once and closes the closed
// channel. The sendCh is intentionally NOT closed here — the writer
// goroutine selects on a.closed to stop, so we avoid a send-on-closed-
// channel panic from a racing producer.
func (a *agentConnection) close() {
	a.closeOnce.Do(func() { close(a.closed) })
}

// registerSpawnPending registers an ack channel for an in-flight Spawn.
func (a *agentConnection) registerSpawnPending(spawnID string) chan *genv1.SpawnAck {
	ch := make(chan *genv1.SpawnAck, 1)
	a.pendingMu.Lock()
	a.pendingSpawn[spawnID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *agentConnection) clearSpawnPending(spawnID string) {
	a.pendingMu.Lock()
	delete(a.pendingSpawn, spawnID)
	a.pendingMu.Unlock()
}

func (a *agentConnection) deliverSpawnAck(ack *genv1.SpawnAck) {
	a.pendingMu.Lock()
	ch, ok := a.pendingSpawn[ack.GetSpawnId()]
	a.pendingMu.Unlock()
	if ok {
		select {
		case ch <- ack:
		default:
		}
	}
}

func (a *agentConnection) registerReapPending(spawnID string) chan *genv1.Reaped {
	ch := make(chan *genv1.Reaped, 1)
	a.pendingMu.Lock()
	a.pendingReap[spawnID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *agentConnection) clearReapPending(spawnID string) {
	a.pendingMu.Lock()
	delete(a.pendingReap, spawnID)
	a.pendingMu.Unlock()
}

func (a *agentConnection) deliverReaped(r *genv1.Reaped) {
	a.pendingMu.Lock()
	ch, ok := a.pendingReap[r.GetSpawnId()]
	a.pendingMu.Unlock()
	if ok {
		select {
		case ch <- r:
		default:
		}
	}
}

// registerStream opens a per-dispatch response channel keyed by stream_id.
func (a *agentConnection) registerStream(streamID string) chan *genv1.DispatchFrame {
	ch := make(chan *genv1.DispatchFrame, 16)
	a.pendingMu.Lock()
	a.pendingStreams[streamID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *agentConnection) clearStream(streamID string) {
	a.pendingMu.Lock()
	delete(a.pendingStreams, streamID)
	a.pendingMu.Unlock()
}

// deliverDispatch routes an inbound response frame to its stream channel.
// The send happens under pendingMu so it cannot race closeAllStreams: once
// closeAllStreams has deleted+closed a channel under the same lock, the
// lookup here misses and the frame is dropped (the reader has already been
// notified via the closed channel / a.closed). The send is non-blocking
// against a.closed so a full buffer on a torn-down connection can't wedge
// the lock; the channel buffer (16) absorbs the common case.
func (a *agentConnection) deliverDispatch(frame *genv1.DispatchFrame) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	ch, ok := a.pendingStreams[frame.GetStreamId()]
	if !ok {
		return
	}
	select {
	case ch <- frame:
	case <-a.closed:
	}
}

// closeAllStreams closes every open per-dispatch response channel. Called
// when the agent connection drops so in-flight dispatch readers observe a
// closed channel and synthesize a host_agent_disconnected terminal. Holds
// pendingMu across the delete+close so a concurrent deliverDispatch (also
// under pendingMu) never sends on a closed channel.
func (a *agentConnection) closeAllStreams() {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	for id, ch := range a.pendingStreams {
		delete(a.pendingStreams, id)
		close(ch)
	}
}

// spawnState records one lazily-spawned child process. originalCallback
// holds the un-rewritten supervisor callback URL so a LocalHttpForward
// from the child can be un-rewritten back to the supervisor.
type spawnState struct {
	spawnID          string
	agentAPIKeyID    string
	scopeID          string // the dispatch-observable scope (instance id in v1)
	bindingName      string
	capabilities     map[string][]byte
	originalCallback string
}

// runScopeBindingKey indexes spawns by (scope, binding-name) for dedup:
// one spawned process per (binding-name, scope).
type runScopeBindingKey struct {
	scopeID     string
	bindingName string
}

// instanceCacheEntry is the proxy's cached view of an instance's
// late-bound service catalog plus the owner identity and params blob,
// populated via OnInstanceCreated or the GET /instances/{id} fallback.
type instanceCacheEntry struct {
	serviceBindings map[string]bindingSpec
	ownerAPIKeyID   string
	params          map[string]any
	lastUpdated     time.Time
}

// bindingSpec is the v1 binding shape: a path the agent exec()s. Other
// fields (args, env, per-binding cwd) are additive; unknown JSON fields
// are ignored on parse.
type bindingSpec struct {
	Path string `json:"path"`
}

// registerAgent installs a new connection for apiKeyID, displacing any
// prior connection for the same key. Returns the new connection, the
// displaced prior connection (nil if none), and a displacedPrior flag.
// The caller is responsible for closing the displaced connection's
// sendCh-draining goroutine via prior.close().
//
// On displacement, the prior connection's spawns are dropped here under
// the lock. The freshly-created new connection owns no spawns yet, so
// every spawn currently keyed to apiKeyID belongs to the prior connection
// — leaving them would let a dispatch resolve a prior spawn-id and route
// it to the new agent (which has no such child), yielding a spurious
// executor_crashed. The prior connection's reconnect-recovery SIGKILLs the
// now-orphaned children on the dev machine.
func (s *proxyState) registerAgent(apiKeyID, label, localCallbackBaseURL string) (conn *agentConnection, prior *agentConnection, displacedPrior bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior = s.agents[apiKeyID]
	displacedPrior = prior != nil
	conn = newAgentConnection(apiKeyID, label, localCallbackBaseURL)
	s.agents[apiKeyID] = conn
	if displacedPrior {
		s.dropSpawnsForAPIKeyLocked(apiKeyID)
	}
	return conn, prior, displacedPrior
}

// dropSpawnsForAPIKeyLocked removes every spawn (and its dedup + claim-route
// index entries) owned by apiKeyID. Caller must hold mu.
func (s *proxyState) dropSpawnsForAPIKeyLocked(apiKeyID string) {
	var dropped []string
	for spawnID, sp := range s.spawns {
		if sp.agentAPIKeyID == apiKeyID {
			dropped = append(dropped, spawnID)
			delete(s.spawns, spawnID)
			delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
		}
	}
	s.purgeClaimRoutesLocked(dropped)
}

// dropAgent removes the connection for apiKeyID only if it is still the
// currently-registered one (a displaced prior must not evict its
// successor). Returns the spawn-ids that were associated with the
// dropped connection, which the caller drops from the spawn index.
func (s *proxyState) dropAgent(apiKeyID string, conn *agentConnection) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.agents[apiKeyID]; ok && cur == conn {
		delete(s.agents, apiKeyID)
	}
	var dropped []string
	for spawnID, sp := range s.spawns {
		if sp.agentAPIKeyID == apiKeyID {
			dropped = append(dropped, spawnID)
			delete(s.spawns, spawnID)
			delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
		}
	}
	s.purgeClaimRoutesLocked(dropped)
	return dropped
}

// purgeClaimRoutesLocked removes every claim route whose spawn appears in
// the dropped set. Caller must hold mu.
func (s *proxyState) purgeClaimRoutesLocked(droppedSpawns []string) {
	if len(droppedSpawns) == 0 {
		return
	}
	gone := make(map[string]struct{}, len(droppedSpawns))
	for _, id := range droppedSpawns {
		gone[id] = struct{}{}
	}
	for claimID, route := range s.claimRoutes {
		if _, ok := gone[route.spawnID]; ok {
			delete(s.claimRoutes, claimID)
		}
	}
}

// lookupAgent returns the currently-registered connection for apiKeyID.
func (s *proxyState) lookupAgent(apiKeyID string) (*agentConnection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, ok := s.agents[apiKeyID]
	return conn, ok
}

// recordSpawn registers a spawned child and its dedup index entry.
func (s *proxyState) recordSpawn(spawnID, agentAPIKeyID, scopeID, bindingName string, capabilities map[string][]byte, originalCallback string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spawns[spawnID] = &spawnState{
		spawnID:          spawnID,
		agentAPIKeyID:    agentAPIKeyID,
		scopeID:          scopeID,
		bindingName:      bindingName,
		capabilities:     capabilities,
		originalCallback: originalCallback,
	}
	s.runScopeBindings[runScopeBindingKey{scopeID: scopeID, bindingName: bindingName}] = spawnID
}

// lookupSpawn returns the spawn metadata for a spawn-id.
func (s *proxyState) lookupSpawn(spawnID string) (*spawnState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp, ok := s.spawns[spawnID]
	return sp, ok
}

// lookupSpawnByRunScopeBinding returns the dedup spawn-id for a
// (scope, binding-name) pair, if any.
func (s *proxyState) lookupSpawnByRunScopeBinding(scopeID, bindingName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	spawnID, ok := s.runScopeBindings[runScopeBindingKey{scopeID: scopeID, bindingName: bindingName}]
	return spawnID, ok
}

// dropSpawnsForRunScope removes every spawn keyed to scopeID and returns
// snapshots of the dropped spawns (so the caller can issue Reap frames to
// the owning agents before the rows are gone from state).
func (s *proxyState) dropSpawnsForRunScope(scopeID string) []spawnState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []spawnState
	var droppedIDs []string
	for spawnID, sp := range s.spawns {
		if sp.scopeID == scopeID {
			dropped = append(dropped, *sp)
			droppedIDs = append(droppedIDs, spawnID)
			delete(s.spawns, spawnID)
			delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
		}
	}
	s.purgeClaimRoutesLocked(droppedIDs)
	return dropped
}

// dropSpawn removes a single spawn (used when an inner dispatch stream
// terminates with a crash so the next dispatch forces a fresh spawn).
func (s *proxyState) dropSpawn(spawnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spawns[spawnID]
	if !ok {
		return
	}
	delete(s.spawns, spawnID)
	delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
	s.purgeClaimRoutesLocked([]string{spawnID})
}

// cacheInstance stores the binding catalog, owner, and params for an
// instance. Overwrites any prior entry (lifecycle events are the
// freshness source).
func (s *proxyState) cacheInstance(instanceID string, serviceBindings map[string]bindingSpec, ownerAPIKeyID string, params map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[instanceID] = &instanceCacheEntry{
		serviceBindings: serviceBindings,
		ownerAPIKeyID:   ownerAPIKeyID,
		params:          params,
		lastUpdated:     time.Now(),
	}
}

// lookupInstance returns the cached entry for an instance, if present.
func (s *proxyState) lookupInstance(instanceID string) (*instanceCacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.instances[instanceID]
	return entry, ok
}

// recordClaimRoute remembers which (agent, spawn) holds a claim so the
// follow-on Commit/Abandon/Release RPCs can route back. No-op for an
// empty claim-id.
func (s *proxyState) recordClaimRoute(claimID, apiKeyID, spawnID string) {
	if claimID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimRoutes[claimID] = claimRoute{apiKeyID: apiKeyID, spawnID: spawnID}
}

// lookupClaimRoute returns the (agent, spawn) holding a claim.
func (s *proxyState) lookupClaimRoute(claimID string) (claimRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.claimRoutes[claimID]
	return r, ok
}

// dropClaimRoute forgets a claim's routing entry (after Release).
func (s *proxyState) dropClaimRoute(claimID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.claimRoutes, claimID)
}
