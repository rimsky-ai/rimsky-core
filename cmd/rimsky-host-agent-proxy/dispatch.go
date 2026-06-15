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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @constraint: error-class vocabulary surfaced by the proxy (slots into
// the existing executor-Error / claim-producer-error class spaces).
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

// anonymousRoutingIdentity is the well-known routing key the proxy resolves an
// anonymous-owner instance against. An instance created in anonymous mode
// persists with an empty owner api-key, so it cannot match an owner-keyed
// agent; an agent running against an anonymous-mode deployment registers under
// this same sentinel (see hostagent.AnonymousRoutingIdentity — the two MUST
// stay in lockstep) so the dispatch still resolves to a connected agent rather
// than dead-ending on host_agent_not_connected. @concept: host-agent-proxy
const anonymousRoutingIdentity = "anonymous"

// resolveOwnerRoutingKey maps an instance's persisted owner api-key to the
// routing key the proxy looks an agent up under. An empty owner (an instance
// created in anonymous mode) routes to the anonymous sentinel; an authenticated
// owner routes to itself, unchanged.
func resolveOwnerRoutingKey(ownerAPIKeyID string) string {
	if ownerAPIKeyID == "" {
		return anonymousRoutingIdentity
	}
	return ownerAPIKeyID
}

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
// scopeID is the dispatch-observable spawn-isolation scope: the inbound
// run_scope_id when present, so two concurrent run-scopes of one instance
// get distinct late-bound children (one spawn per (run_scope, binding)).
// It falls back to instanceID only when run_scope_id is empty — the
// degenerate / pre-field caller, where the main run-scope is
// one-per-instance and instance keying is the historical happy path.
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

	// @constraint: an empty owner is an anonymous-mode instance — resolve
	// the agent under the anonymous routing identity instead of
	// short-circuiting. host_agent_not_connected is still returned, but
	// only when no agent (owner-keyed OR anonymous) is connected — the
	// guard discriminates "no anonymous agent" from "owner empty".
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

	// @deliberate: lazy spawn — no cached spawn matched the cache key.
	spawnID, rerr := spawnChild(agent, binding, entry, name, scopeID, expectedProtocols, spawnTimeout)
	if rerr != nil {
		return nil, rerr
	}
	state.recordSpawn(spawnID, agent.apiKeyID, scopeID, name, nil, originalCallback)
	return &resolved{agent: agent, spawnID: spawnID, scopeID: scopeID}, nil
}

// resolveAndSpawnByService resolves a dispatch for one of the fronted
// protocols whose request message may not carry an instance_id (publisher
// carries it, but validation and data-processing do not). When instanceID
// is non-empty it delegates to the instance-keyed resolveAndSpawn. When it
// is empty it resolves the binding by service name: the
// x-rimsky-service-name header names the late-bound service, and the single
// cached instance carrying that binding supplies the owner + agent + path +
// spawn scope. This keeps the proxy a transparent forwarder for every
// protocol it fronts, including the instance-id-less ones.
//
// @concept: host-agent-proxy
func resolveAndSpawnByService(
	ctx context.Context,
	state *proxyState,
	fetch instanceFetcher,
	expectedProtocols []string,
	instanceID string,
	runScopeID string,
	spawnTimeout time.Duration,
) (*resolved, *resolveError) {
	if instanceID != "" {
		return resolveAndSpawn(ctx, state, fetch, expectedProtocols, instanceID, runScopeID, "", spawnTimeout)
	}

	name, ok := serviceNameFromContext(ctx)
	if !ok {
		return nil, &resolveError{class: errClassBindingNotFound, msg: "missing x-rimsky-service-name header"}
	}
	resolvedInstanceID, entry, ok := state.lookupInstanceByBinding(name)
	if !ok {
		return nil, &resolveError{class: errClassBindingNotFound, msg: fmt.Sprintf("no cached instance binds service %q", name)}
	}
	// @constraint: anonymous-mode instance (empty owner) resolves under
	// the anonymous routing identity; host_agent_not_connected only when
	// no agent is connected under the resolved key.
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
		scopeID = resolvedInstanceID
	}
	if spawnID, ok := state.lookupSpawnByRunScopeBinding(scopeID, name); ok {
		return &resolved{agent: agent, spawnID: spawnID, scopeID: scopeID}, nil
	}
	spawnID, rerr := spawnChild(agent, binding, entry, name, scopeID, expectedProtocols, spawnTimeout)
	if rerr != nil {
		return nil, rerr
	}
	state.recordSpawn(spawnID, agent.apiKeyID, scopeID, name, nil, "")
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

	// @constraint: per-binding cwd overrides the instance-level
	// params.cwd only when set; otherwise the agent falls back to the
	// instance-level cwd carried on the Spawn frame.
	cwd, _ := entry.params["cwd"].(string)

	// @constraint: a per-binding timeout bounds BOTH the agent's
	// readiness wait (carried in ReadyTimeoutSeconds) and the proxy's
	// own SpawnAck wait below — so a no-bind child fails inside the
	// binding-specified budget rather than the proxy's larger configured
	// default. Absent (<=0) → the proxy's configured spawn timeout governs.
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
	case <-time.After(effectiveTimeout):
		return "", &resolveError{class: errClassSpawnFailed, msg: "spawn ack timed out"}
	case <-agent.closed:
		return "", &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected during spawn"}
	}
}

// forwardProxyUnary tunnels a serialized unary request to the spawned child
// over a fresh dispatch stream and awaits exactly one response frame, for
// the publisher / validation / data-processing protocols. The rpc_method
// names which child RPC the agent must invoke — it rides the wire because
// these protocols expose multiple unary RPCs whose request messages are
// distinct types, so the agent cannot infer the target RPC from the payload
// shape (the generic analogue of claim_producer_verb).
//
// @concept: host-agent-proxy
func forwardProxyUnary(ctx context.Context, agent *agentConnection, spawnID, protocol, rpcMethod string, payload []byte, timeout time.Duration) ([]byte, *resolveError) {
	streamID := uuid.NewString()
	respCh := agent.registerStream(streamID)
	defer agent.clearStream(streamID)

	if !agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:   spawnID,
		Protocol:  protocol,
		Payload:   payload,
		StreamId:  streamID,
		Kind:      genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		RpcMethod: rpcMethod,
	}}}) {
		return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected before dispatch"}
	}

	select {
	case frame, ok := <-respCh:
		if !ok {
			return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected mid-call"}
		}
		if frame.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
			return nil, &resolveError{class: errClassExecutorCrashed, msg: "spawned " + protocol + " cancelled the call"}
		}
		return frame.GetPayload(), nil
	case <-time.After(timeout):
		return nil, &resolveError{class: errClassExecutorCrashed, msg: protocol + " call timed out"}
	case <-ctx.Done():
		return nil, &resolveError{class: errClassExecutorCrashed, msg: "caller context cancelled: " + ctx.Err().Error()}
	case <-agent.closed:
		return nil, &resolveError{class: errClassHostAgentDisconnected, msg: "agent disconnected mid-call"}
	}
}

// proxyStatus maps a resolveError to a gRPC status carrying the error_class
// in a google.rpc.ErrorInfo detail (mirrors claimProducerStatus, the shape
// the supervisor-facing clients decode). Missing-binding-style faults use
// FailedPrecondition; all other proxy-side faults use Internal.
func proxyStatus(rerr *resolveError) error {
	return claimProducerStatus(rerr)
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
// GET /v1/instances/{id} endpoint. baseURL must have no trailing slash.
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

// instanceJSON is the subset of GET /v1/instances/{id} the proxy reads.
type instanceJSON struct {
	ServiceBindings map[string]bindingSpec `json:"service_bindings"`
	CreatedByAPIKey string                 `json:"created_by_api_key_id"`
	OwnerAPIKeyID   string                 `json:"owner_api_key_id"`
	Params          json.RawMessage        `json:"params"`
}

// parseInstanceResponse decodes a GET /v1/instances/{id} body into a cache
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
