// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lifecycle_handler.go — the proxy's LifecycleSubscriber consumer role.
// Two methods do real work: OnInstanceCreated populates the binding
// cache (service_bindings + owner + params), and OnRunScopeTerminal
// drives reap (drop the scope's spawns, send Reap frames, await Reaped).
// The other five methods are no-op LifecycleAck returns — the proxy does
// not care about template lifecycle.
//
// @concept: host-agent-proxy

package main

import (
	"context"
	"log/slog"
	"time"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// lifecycleHandler implements genv1.LifecycleSubscriberServer.
type lifecycleHandler struct {
	genv1.UnimplementedLifecycleSubscriberServer
	state       *proxyState
	reapTimeout time.Duration
}

func newLifecycleHandler(state *proxyState, cfg Config) *lifecycleHandler {
	return &lifecycleHandler{state: state, reapTimeout: cfg.ReapTimeout}
}

// OnInstanceCreated caches the instance's late-bound binding catalog.
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

// reapGraceSeconds is the SIGTERM grace the proxy asks the agent to honor
// before escalating to SIGKILL on a reaped child.
const reapGraceSeconds = 30

// OnRunScopeTerminal reaps every spawn keyed to the terminating scope.
// The proxy keys lazily-spawned children by instance id (its v1
// dispatch-observable scope — see resolveAndSpawn's scopeID), so the reap
// matches on instance_id, not run_scope_id. A pre-field caller that left
// instance_id empty falls back to run_scope_id so the reap still has a
// chance to match the (degenerate) main-scope-equals-instance case.
func (h *lifecycleHandler) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	scopeID := req.GetInstanceId()
	if scopeID == "" {
		scopeID = req.GetRunScopeId()
	}
	dropped := h.state.dropSpawnsForRunScope(scopeID)
	for i := range dropped {
		h.reap(dropped[i])
	}
	return &genv1.LifecycleAck{}, nil
}

// reap sends a Reap frame to the owning agent and awaits the Reaped ack
// (bounded by reapTimeout). The spawn snapshot is taken before the row is
// dropped, so the owning agent is reachable here.
func (h *lifecycleHandler) reap(sp spawnState) {
	agent, ok := h.state.lookupAgent(sp.agentAPIKeyID)
	if !ok {
		// Owner disconnected; the agent's reconnect-recovery SIGKILLs the
		// orphaned child. Nothing more to do here.
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

// OnTemplateRegistered is a no-op ack.
func (h *lifecycleHandler) OnTemplateRegistered(_ context.Context, _ *genv1.OnTemplateRegisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

// OnTemplateDeployed is a no-op ack.
func (h *lifecycleHandler) OnTemplateDeployed(_ context.Context, _ *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

// OnTemplateUndeployed is a no-op ack.
func (h *lifecycleHandler) OnTemplateUndeployed(_ context.Context, _ *genv1.OnTemplateUndeployedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

// OnTemplateDeregistered is a no-op ack.
func (h *lifecycleHandler) OnTemplateDeregistered(_ context.Context, _ *genv1.OnTemplateDeregisteredRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}

// OnInstanceTerminated is a no-op ack (reap is driven by
// OnRunScopeTerminal, which control-api fires before this event).
func (h *lifecycleHandler) OnInstanceTerminated(_ context.Context, _ *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	return &genv1.LifecycleAck{}, nil
}
