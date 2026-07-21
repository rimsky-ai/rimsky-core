// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent-proxy

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const (
	errClassBindingNotFound       = "binding_not_found"
	errClassHostAgentNotConnected = "host_agent_not_connected"
	errClassSpawnFailed           = "spawn_failed"
	errClassHostAgentDisconnected = "host_agent_disconnected"
	errClassExecutorCrashed       = "executor_crashed"
	errClassContractMismatch      = "contract_mismatch"
)

const serviceNameHeader = peer.ServiceNameMetadataKey

const anonymousRoutingIdentity = hostagent.AnonymousRoutingIdentity

func resolveOwnerRoutingKey(ownerAPIKeyID string) string {
	if ownerAPIKeyID == "" {
		return anonymousRoutingIdentity
	}
	return ownerAPIKeyID
}

type resolveError struct {
	class string
	msg   string
}

func (e *resolveError) Error() string { return fmt.Sprintf("%s: %s", e.class, e.msg) }

type resolved struct {
	agent   *agentConnection
	spawnID string
	scopeID string
}

func serviceNameFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	vals := md.Get(serviceNameHeader)
	if len(vals) == 0 || vals[0] == "" {
		return "", false
	}
	return vals[0], true
}

type instanceFetcher func(ctx context.Context, instanceID string) (*instanceCacheEntry, bool, error)

// @concept: host-agent-proxy
func resolveAndSpawn(
	ctx context.Context,
	state *proxyState,
	fetch instanceFetcher,
	expectedProtocols []string,
	instanceID string,
	runScopeID string,
	originalCallback string,
	spawnTimeout time.Duration,
) (*resolved, *resolveError) {
	name, ok := serviceNameFromContext(ctx)
	if !ok {
		return nil, &resolveError{class: errClassBindingNotFound, msg: "missing x-rimsky-service-name header"}
	}

	entry, ok := state.lookupInstance(instanceID)
	if !ok {
		fetched, found, err := fetch(ctx, instanceID)
		if err != nil {
			return nil, &resolveError{class: errClassBindingNotFound, msg: fmt.Sprintf("instance fetch failed: %v", err)}
		}
		if !found {
			return nil, &resolveError{class: errClassBindingNotFound, msg: fmt.Sprintf("instance %q not found", instanceID)}
		}
		state.cacheInstance(instanceID, fetched.serviceBindings, fetched.ownerAPIKeyID, fetched.params)
		entry = fetched
	}

	routingKey := resolveOwnerRoutingKey(entry.ownerAPIKeyID)
	agent, ok := state.lookupAgent(routingKey)
	if !ok {
		return nil, &resolveError{class: errClassHostAgentNotConnected, msg: fmt.Sprintf("no agent connected for owner %q", routingKey)}
	}

	binding, ok := entry.serviceBindings[name]
	if !ok {
		return nil, &resolveError{class: errClassBindingNotFound, msg: fmt.Sprintf("binding %q not in service_bindings", name)}
	}

	scopeID := runScopeID
	if scopeID == "" {
		scopeID = instanceID
	}
	if spawnID, ok := state.lookupSpawnByRunScopeBinding(scopeID, name); ok {
		return &resolved{agent: agent, spawnID: spawnID, scopeID: scopeID}, nil
	}

	spawnKey := scopeID + "\x00" + name
	v, err, _ := state.spawnGroup.Do(spawnKey, func() (any, error) {
		if spawnID, ok := state.lookupSpawnByRunScopeBinding(scopeID, name); ok {
			return spawnID, nil
		}
		spawnID, capabilities, rerr := spawnChild(agent, binding, entry, name, scopeID, expectedProtocols, spawnTimeout)
		if rerr != nil {
			return nil, rerr
		}
		state.recordSpawn(spawnID, agent.apiKeyID, scopeID, name, capabilities, originalCallback)
		return spawnID, nil
	})
	if err != nil {
		if rerr, ok := err.(*resolveError); ok {
			return nil, rerr
		}
		return nil, &resolveError{class: errClassSpawnFailed, msg: err.Error()}
	}
	return &resolved{agent: agent, spawnID: v.(string), scopeID: scopeID}, nil
}

func spawnChild(
	agent *agentConnection,
	binding bindingSpec,
	entry *instanceCacheEntry,
	bindingName, scopeID string,
	expectedProtocols []string,
	timeout time.Duration,
) (string, map[string][]byte, *resolveError) {
	spawnID := uuid.NewString()
	ackCh := agent.registerSpawnPending(spawnID)
	defer agent.clearSpawnPending(spawnID)

	cwd, _ := entry.params["cwd"].(string)

	effectiveTimeout := timeout
	if binding.TimeoutSeconds > 0 {
		effectiveTimeout = time.Duration(binding.TimeoutSeconds) * time.Second
	}
	readyTimeout := int32(effectiveTimeout / time.Second)
	if !agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_Spawn{Spawn: &genv1.Spawn{
		SpawnId: spawnID,
		Binding: &genv1.Binding{
			Path: binding.Path,
			Args: binding.Args,
			Env:  binding.Env,
			Cwd:  binding.Cwd,
		},
		Cwd:                 cwd,
		RunScopeId:          scopeID,
		ExpectedProtocols:   expectedProtocols,
		ReadyTimeoutSeconds: readyTimeout,
	}}}) {
		return "", nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent connection closed before spawn"}
	}

	select {
	case ack := <-ackCh:
		if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
			msg := "spawn failed"
			if e := ack.GetError(); e != nil {
				msg = e.GetMessage()
			}
			return "", nil, &resolveError{class: errClassSpawnFailed, msg: msg}
		}
		return spawnID, ack.GetCapabilities(), nil
	case <-time.After(effectiveTimeout):
		return "", nil, &resolveError{class: errClassSpawnFailed, msg: "spawn ack timed out"}
	case <-agent.closed:
		return "", nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected during spawn"}
	}
}

func sendDispatchFrame(agent *agentConnection, spawnID, protocol, streamID string, payload []byte, verb *genv1.DispatchFrame_ClaimProducerVerb) bool {
	frame := &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocol,
		Payload:  payload,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}
	if verb != nil {
		frame.ClaimProducerVerb = *verb
	}
	return agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: frame}})
}

func rewriteCallbackURL(original, agentBase string) string {
	if original == "" || agentBase == "" {
		return original
	}
	orig, err := url.Parse(original)
	if err != nil {
		return original
	}
	base, err := url.Parse(agentBase)
	if err != nil || base.Host == "" {
		return original
	}
	orig.Scheme = base.Scheme
	orig.Host = base.Host
	return orig.String()
}

func newControlAPIFetcher(client *http.Client, baseURL, token string) instanceFetcher {
	return func(ctx context.Context, instanceID string) (*instanceCacheEntry, bool, error) {
		if baseURL == "" {
			return nil, false, nil
		}
		reqURL := baseURL + "/v1/instances/" + url.PathEscape(instanceID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, false, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, false, nil
		}
		if resp.StatusCode != http.StatusOK {
			return nil, false, fmt.Errorf("control-api GET /v1/instances/%s: status %d", instanceID, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false, err
		}
		return parseInstanceResponse(body)
	}
}

type instanceJSON struct {
	ServiceBindings map[string]bindingSpec `json:"service_bindings"`
	CreatedByAPIKey string                 `json:"created_by_api_key_id"`
	OwnerAPIKeyID   string                 `json:"owner_api_key_id"`
	Params          json.RawMessage        `json:"params"`
}

func parseInstanceResponse(body []byte) (*instanceCacheEntry, bool, error) {
	var ij instanceJSON
	if err := json.Unmarshal(body, &ij); err != nil {
		return nil, false, err
	}
	owner := ij.OwnerAPIKeyID
	if owner == "" {
		owner = ij.CreatedByAPIKey
	}
	params := map[string]any{}
	if len(ij.Params) > 0 {
		_ = json.Unmarshal(ij.Params, &params)
	}
	bindings := ij.ServiceBindings
	if bindings == nil {
		bindings = map[string]bindingSpec{}
	}
	return &instanceCacheEntry{
		serviceBindings: bindings,
		ownerAPIKeyID:   owner,
		params:          params,
	}, true, nil
}

func parseServiceBindings(raw []byte) map[string]bindingSpec {
	out := map[string]bindingSpec{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func parseParams(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func nowUnixMs() int64 { return time.Now().UnixMilli() }
