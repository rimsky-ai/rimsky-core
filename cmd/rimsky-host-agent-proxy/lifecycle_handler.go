// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent-proxy

package main

import (
	"context"
	"log/slog"
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
	h.state.cacheInstance(req.GetInstanceId(), bindings, req.GetOwnerApiKeyId(), params)
	slog.Debug("cached instance bindings",
		"instance_id", req.GetInstanceId(),
		"binding_count", len(bindings),
		"owner_present", req.GetOwnerApiKeyId() != "")
	return &genv1.LifecycleAck{}, nil
}

const reapGraceSeconds = 30

func (h *lifecycleHandler) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	scopeID := req.GetRunScopeId()
	if scopeID == "" {
		scopeID = req.GetInstanceId()
	}
	dropped := h.state.dropSpawnsForRunScope(scopeID)
	for i := range dropped {
		h.reap(dropped[i])
	}
	return &genv1.LifecycleAck{}, nil
}

func (h *lifecycleHandler) reap(sp spawnState) {
	agent, ok := h.state.lookupAgent(sp.agentAPIKeyID)
	if !ok {
		return
	}
	ackCh := agent.registerReapPending(sp.spawnID)
	defer agent.clearReapPending(sp.spawnID)

	if !agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_Reap{Reap: &genv1.Reap{
		SpawnId:             sp.spawnID,
		SigtermGraceSeconds: reapGraceSeconds,
	}}}) {
		return
	}
	select {
	case <-ackCh:
	case <-time.After(h.reapTimeout):
		slog.Warn("reap ack timed out", "spawn_id", sp.spawnID)
	case <-agent.closed:
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

func (h *lifecycleHandler) OnInstanceTerminated(_ context.Context, _ *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
