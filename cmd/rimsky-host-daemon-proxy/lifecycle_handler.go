// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type lifecycleHandler struct {
	genv1.UnimplementedLifecycleSubscriberServer
	state       *proxyState
	reapTimeout time.Duration
}

func newLifecycleHandler(state *proxyState, cfg Config) *lifecycleHandler {
	return &lifecycleHandler{state: state, reapTimeout: cfg.ReapTimeout}
}

func (h *lifecycleHandler) OnInstanceCreated(_ context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	bindings := parseServiceBindings(req.GetServiceBindings())
	params := parseParams(req.GetParams())
	h.state.cacheInstance(req.GetInstanceId(), bindings, req.GetTargetRoutingIdentity(), params)
	slog.Debug("PROXY.INSTANCEBINDINGS.CACHED",
		"instance_id", req.GetInstanceId(),
		"binding_count", len(bindings),
		"target_routing_identity_present", req.GetTargetRoutingIdentity() != "")
	return &genv1.LifecycleAck{}, nil
}

const reapGraceSeconds = 30

func (h *lifecycleHandler) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	scopeID := req.GetRunScopeId()
	if scopeID == "" {
		scopeID = req.GetInstanceId()
	}
	dropped := h.state.dropSpawnsForRunScope(scopeID)
	var wg sync.WaitGroup
	wg.Add(len(dropped))
	for i := range dropped {
		go func(sp spawnState) {
			defer wg.Done()
			h.reap(sp)
		}(dropped[i])
	}
	wg.Wait()
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) reap(sp spawnState) {
	daemon, ok := h.state.lookupDaemon(sp.daemonRoutingIdentity)
	if !ok {
		return
	}
	ackCh := daemon.registerReapPending(sp.spawnID)
	defer daemon.clearReapPending(sp.spawnID)

	if !daemon.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_Reap{Reap: &genv1.Reap{
		SpawnId:             sp.spawnID,
		SigtermGraceSeconds: reapGraceSeconds,
	}}}) {
		return
	}
	select {
	case <-ackCh:
	case <-time.After(h.reapTimeout):
		slog.Warn("PROXY.REAPACK.TIMEDOUT", "spawn_id", sp.spawnID)
	case <-daemon.closed:
	}
}

func (h *lifecycleHandler) OnTemplateRegistered(_ context.Context, _ *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) OnTemplateDeployed(_ context.Context, _ *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) OnTemplateUndeployed(_ context.Context, _ *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) OnTemplateDeregistered(_ context.Context, _ *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	h.state.dropInstance(req.GetInstanceId())
	return &genv1.LifecycleAck{}, nil
}
