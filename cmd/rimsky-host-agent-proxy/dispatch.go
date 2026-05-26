// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// dispatch.go — shared spawn-lifecycle machinery used by the
// supervisor-facing Executor and ClaimProducer handlers. Both protocols
// resolve a dispatch to (owner → agent connection → binding → spawn),
// lazily spawning the named child on first use for a given
// (scope, binding-name) and reusing it thereafter. The only difference
// between the two protocols is which inner RPC is tunneled; everything
// up to and including the spawn is shared here.
//
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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// Error-class vocabulary surfaced by the proxy (slots into the existing
// executor-Error / claim-producer-error class spaces).
const (
	errClassBindingNotFound       = "binding_not_found"
	errClassHostAgentNotConnected = "host_agent_not_connected"
	errClassSpawnFailed           = "spawn_failed"
	errClassHostAgentDisconnected = "host_agent_disconnected"
	errClassExecutorCrashed       = "executor_crashed"
)

// serviceNameHeader is the per-call gRPC metadata key the supervisor's
// client-side interceptor stamps with the resolved-for service name.
const serviceNameHeader = "x-rimsky-service-name"

// resolveError carries a proxy resolution/spawn failure and the
// error_class the supervisor-facing handler should surface.
type resolveError struct {
	class string
	msg   string
}

func (e *resolveError) Error() string { return fmt.Sprintf("%s: %s", e.class, e.msg) }

// resolved is the outcome of resolving a dispatch to a live spawned
// child: the owning agent connection and the spawn-id to dispatch into.
type resolved struct {
	agent   *agentConnection
	spawnID string
	scopeID string
}

// serviceNameFromContext extracts the x-rimsky-service-name header.
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

// instanceFetcher fetches an instance's binding catalog from control-api
// on a cache miss. Injected so tests can stub the HTTP fallback.
type instanceFetcher func(ctx context.Context, instanceID string) (*instanceCacheEntry, bool, error)

// resolveAndSpawn performs the shared dispatch-resolution flow:
//  1. extract the service name from gRPC metadata,
//  2. resolve the instance (cache → fetcher fallback),
//  3. resolve the owner's agent connection,
//  4. resolve the binding,
//  5. dedup-or-lazily-spawn the child for (scope, binding-name).
//
// scopeID is the dispatch-observable scope (the instance id in v1 — the
// wire protocols carry instance_id but not run_scope_id; the main
// run-scope is one-per-instance, the common case).
func resolveAndSpawn(
	ctx context.Context,
	state *proxyState,
	fetch instanceFetcher,
	expectedProtocols []string,
	instanceID string,
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

	if entry.ownerAPIKeyID == "" {
		return nil, &resolveError{class: errClassHostAgentNotConnected, msg: "instance has no owner api-key (anonymous-mode)"}
	}

	agent, ok := state.lookupAgent(entry.ownerAPIKeyID)
	if !ok {
		return nil, &resolveError{class: errClassHostAgentNotConnected, msg: fmt.Sprintf("no agent connected for owner %q", entry.ownerAPIKeyID)}
	}

	binding, ok := entry.serviceBindings[name]
	if !ok {
		return nil, &resolveError{class: errClassBindingNotFound, msg: fmt.Sprintf("binding %q not in service_bindings", name)}
	}

	scopeID := instanceID
	if spawnID, ok := state.lookupSpawnByRunScopeBinding(scopeID, name); ok {
		return &resolved{agent: agent, spawnID: spawnID, scopeID: scopeID}, nil
	}

	// Lazy spawn.
	spawnID, rerr := spawnChild(agent, binding, entry, name, scopeID, expectedProtocols, spawnTimeout)
	if rerr != nil {
		return nil, rerr
	}
	state.recordSpawn(spawnID, agent.apiKeyID, scopeID, name, nil, originalCallback)
	return &resolved{agent: agent, spawnID: spawnID, scopeID: scopeID}, nil
}

// spawnChild issues a Spawn frame and awaits the SpawnAck.
func spawnChild(
	agent *agentConnection,
	binding bindingSpec,
	entry *instanceCacheEntry,
	bindingName, scopeID string,
	expectedProtocols []string,
	timeout time.Duration,
) (string, *resolveError) {
	spawnID := uuid.NewString()
	ackCh := agent.registerSpawnPending(spawnID)
	defer agent.clearSpawnPending(spawnID)

	cwd, _ := entry.params["cwd"].(string)
	readyTimeout := int32(timeout / time.Second)
	if !agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_Spawn{Spawn: &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: binding.Path},
		Cwd:                 cwd,
		RunScopeId:          scopeID,
		ExpectedProtocols:   expectedProtocols,
		ReadyTimeoutSeconds: readyTimeout,
	}}}) {
		return "", &resolveError{class: errClassHostAgentDisconnected, msg: "agent connection closed before spawn"}
	}

	select {
	case ack := <-ackCh:
		if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
			msg := "spawn failed"
			if e := ack.GetError(); e != nil {
				msg = e.GetMessage()
			}
			return "", &resolveError{class: errClassSpawnFailed, msg: msg}
		}
		return spawnID, nil
	case <-time.After(timeout):
		return "", &resolveError{class: errClassSpawnFailed, msg: "spawn ack timed out"}
	case <-agent.closed:
		return "", &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected during spawn"}
	}
}

// rewriteCallbackURL replaces the host:port of original with the agent's
// local-callback base URL, preserving the path/query. Returns original
// unchanged when either URL is empty or unparsable (the rewrite is
// best-effort: a malformed callback can't be tunneled, but the dispatch
// should still proceed and surface whatever the child reports).
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

// newControlAPIFetcher returns an instanceFetcher backed by control-api's
// GET /instances/{id} endpoint. baseURL must have no trailing slash.
func newControlAPIFetcher(client *http.Client, baseURL, token string) instanceFetcher {
	return func(ctx context.Context, instanceID string) (*instanceCacheEntry, bool, error) {
		if baseURL == "" {
			return nil, false, nil
		}
		reqURL := baseURL + "/instances/" + url.PathEscape(instanceID)
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
			return nil, false, fmt.Errorf("control-api GET /instances/%s: status %d", instanceID, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false, err
		}
		return parseInstanceResponse(body)
	}
}

// instanceJSON is the subset of GET /instances/{id} the proxy reads.
type instanceJSON struct {
	ServiceBindings map[string]bindingSpec `json:"service_bindings"`
	CreatedByAPIKey string                 `json:"created_by_api_key_id"`
	OwnerAPIKeyID   string                 `json:"owner_api_key_id"`
	Params          json.RawMessage        `json:"params"`
}

// parseInstanceResponse decodes a GET /instances/{id} body into a cache
// entry. Accepts either created_by_api_key_id or owner_api_key_id as the
// owner field (control-api exposes the former; the lifecycle payload uses
// the latter).
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

// parseServiceBindings decodes the opaque service_bindings JSONB blob.
func parseServiceBindings(raw []byte) map[string]bindingSpec {
	out := map[string]bindingSpec{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// parseParams decodes the opaque params JSONB blob.
func parseParams(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// nowUnixMs is the current wall-clock time in unix milliseconds.
func nowUnixMs() int64 { return time.Now().UnixMilli() }
