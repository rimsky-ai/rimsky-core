// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

package main

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type proxyState struct {
	mu sync.RWMutex

	daemons          map[string]*daemonConnection
	spawns           map[string]*spawnState
	runScopeBindings map[runScopeBindingKey]string
	instances        map[string]*instanceCacheEntry
	claimRoutes      map[string]claimRoute

	spawnGroup singleflight.Group
}

type claimRoute struct {
	routingIdentity string
	spawnID         string
}

func newProxyState() *proxyState {
	return &proxyState{
		daemons:          map[string]*daemonConnection{},
		spawns:           map[string]*spawnState{},
		runScopeBindings: map[runScopeBindingKey]string{},
		instances:        map[string]*instanceCacheEntry{},
		claimRoutes:      map[string]claimRoute{},
	}
}

type daemonConnection struct {
	routingIdentity      string
	daemonLabel          string
	localCallbackBaseURL string

	sendCh    chan *genv1.ServerFrame
	closeOnce sync.Once
	closed    chan struct{}

	pendingMu      sync.Mutex
	pendingSpawn   map[string]chan *genv1.SpawnAck
	pendingReap    map[string]chan *genv1.Reaped
	pendingStreams map[string]chan *genv1.DispatchFrame
}

func newDaemonConnection(routingIdentity, label, localCallbackBaseURL string) *daemonConnection {
	return &daemonConnection{
		routingIdentity:      routingIdentity,
		daemonLabel:          label,
		localCallbackBaseURL: localCallbackBaseURL,
		sendCh:               make(chan *genv1.ServerFrame, 64),
		closed:               make(chan struct{}),
		pendingSpawn:         map[string]chan *genv1.SpawnAck{},
		pendingReap:          map[string]chan *genv1.Reaped{},
		pendingStreams:       map[string]chan *genv1.DispatchFrame{},
	}
}

func (a *daemonConnection) send(frame *genv1.ServerFrame) bool {
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

func (a *daemonConnection) close() {
	a.closeOnce.Do(func() { close(a.closed) })
}

func (a *daemonConnection) registerSpawnPending(spawnID string) chan *genv1.SpawnAck {
	ch := make(chan *genv1.SpawnAck, 1)
	a.pendingMu.Lock()
	a.pendingSpawn[spawnID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *daemonConnection) clearSpawnPending(spawnID string) {
	a.pendingMu.Lock()
	delete(a.pendingSpawn, spawnID)
	a.pendingMu.Unlock()
}

func (a *daemonConnection) deliverSpawnAck(ack *genv1.SpawnAck) {
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

func (a *daemonConnection) registerReapPending(spawnID string) chan *genv1.Reaped {
	ch := make(chan *genv1.Reaped, 1)
	a.pendingMu.Lock()
	a.pendingReap[spawnID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *daemonConnection) clearReapPending(spawnID string) {
	a.pendingMu.Lock()
	delete(a.pendingReap, spawnID)
	a.pendingMu.Unlock()
}

func (a *daemonConnection) deliverReaped(r *genv1.Reaped) {
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

func (a *daemonConnection) registerStream(streamID string) chan *genv1.DispatchFrame {
	ch := make(chan *genv1.DispatchFrame, 16)
	a.pendingMu.Lock()
	a.pendingStreams[streamID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *daemonConnection) clearStream(streamID string) {
	a.pendingMu.Lock()
	delete(a.pendingStreams, streamID)
	a.pendingMu.Unlock()
}

func (a *daemonConnection) deliverDispatch(frame *genv1.DispatchFrame) {
	a.pendingMu.Lock()
	ch, ok := a.pendingStreams[frame.GetStreamId()]
	a.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- frame:
	default:
	}
}

func (a *daemonConnection) closeAllStreams() {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	for id, ch := range a.pendingStreams {
		delete(a.pendingStreams, id)
		close(ch)
	}
}

type spawnState struct {
	spawnID               string
	daemonRoutingIdentity string
	scopeID               string
	bindingName           string
	capabilities          map[string][]byte
	originalCallback      string
}

type runScopeBindingKey struct {
	scopeID     string
	bindingName string
}

type instanceCacheEntry struct {
	serviceBindings       map[string]bindingSpec
	targetRoutingIdentity string
	params                map[string]any
	lastUpdated           time.Time
}

type bindingSpec struct {
	Path           string            `json:"path"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// @concept: host-daemon-proxy
type registrationMode int

const (
	registerDisplacePrior registrationMode = iota
	registerRejectOnCollision
)

// @concept: host-daemon-proxy
// @concept: anonymous-mode
func (s *proxyState) registerDaemon(routingIdentity, label, localCallbackBaseURL string, mode registrationMode) (conn *daemonConnection, prior *daemonConnection, collided bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior = s.daemons[routingIdentity]
	if prior != nil && mode == registerRejectOnCollision {
		return nil, prior, true
	}
	conn = newDaemonConnection(routingIdentity, label, localCallbackBaseURL)
	s.daemons[routingIdentity] = conn
	if prior != nil {
		s.dropSpawnsForRoutingIdentityLocked(routingIdentity)
	}
	return conn, prior, false
}

func (s *proxyState) dropSpawnsForRoutingIdentityLocked(routingIdentity string) {
	var dropped []string
	for spawnID, sp := range s.spawns {
		if sp.daemonRoutingIdentity == routingIdentity {
			dropped = append(dropped, spawnID)
			delete(s.spawns, spawnID)
			delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
		}
	}
	s.purgeClaimRoutesLocked(dropped)
}

func (s *proxyState) dropDaemon(routingIdentity string, conn *daemonConnection) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.daemons[routingIdentity]; ok && cur == conn {
		delete(s.daemons, routingIdentity)
	}
	var dropped []string
	for spawnID, sp := range s.spawns {
		if sp.daemonRoutingIdentity == routingIdentity {
			dropped = append(dropped, spawnID)
			delete(s.spawns, spawnID)
			delete(s.runScopeBindings, runScopeBindingKey{scopeID: sp.scopeID, bindingName: sp.bindingName})
		}
	}
	s.purgeClaimRoutesLocked(dropped)
	return dropped
}

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

func (s *proxyState) lookupDaemon(routingIdentity string) (*daemonConnection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, ok := s.daemons[routingIdentity]
	return conn, ok
}

func (s *proxyState) recordSpawn(spawnID, daemonRoutingIdentity, scopeID, bindingName string, capabilities map[string][]byte, originalCallback string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spawns[spawnID] = &spawnState{
		spawnID:               spawnID,
		daemonRoutingIdentity: daemonRoutingIdentity,
		scopeID:               scopeID,
		bindingName:           bindingName,
		capabilities:          capabilities,
		originalCallback:      originalCallback,
	}
	s.runScopeBindings[runScopeBindingKey{scopeID: scopeID, bindingName: bindingName}] = spawnID
}

func (s *proxyState) lookupSpawn(spawnID string) (*spawnState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sp, ok := s.spawns[spawnID]
	return sp, ok
}

func (s *proxyState) lookupSpawnByRunScopeBinding(scopeID, bindingName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	spawnID, ok := s.runScopeBindings[runScopeBindingKey{scopeID: scopeID, bindingName: bindingName}]
	return spawnID, ok
}

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

func (s *proxyState) cacheInstance(instanceID string, serviceBindings map[string]bindingSpec, targetRoutingIdentity string, params map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[instanceID] = &instanceCacheEntry{
		serviceBindings:       serviceBindings,
		targetRoutingIdentity: targetRoutingIdentity,
		params:                params,
		lastUpdated:           time.Now(),
	}
}

func (s *proxyState) lookupInstance(instanceID string) (*instanceCacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.instances[instanceID]
	return entry, ok
}

func (s *proxyState) dropInstance(instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, instanceID)
}

func (s *proxyState) recordClaimRoute(claimID, routingIdentity, spawnID string) {
	if claimID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimRoutes[claimID] = claimRoute{routingIdentity: routingIdentity, spawnID: spawnID}
}

func (s *proxyState) lookupClaimRoute(claimID string) (claimRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.claimRoutes[claimID]
	return r, ok
}
