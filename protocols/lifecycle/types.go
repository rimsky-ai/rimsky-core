// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package lifecycle

import "encoding/json"

// OnTemplateRegisteredRequest fires when a template is first registered
// (its content-hashed spec is persisted but not yet deployed under any
// movable tag).
type OnTemplateRegisteredRequest struct {
	TemplateHash string
	Spec         json.RawMessage
}

// OnTemplateDeployedRequest fires when one or more tags are pointed at a
// template hash. Tags is the set of tags newly attached.
type OnTemplateDeployedRequest struct {
	TemplateHash string
	Tags         []string
}

// OnTemplateUndeployedRequest fires when the last tag is removed from a
// template hash (the template is no longer reachable by tag, but its
// hashed spec persists).
type OnTemplateUndeployedRequest struct {
	TemplateHash string
}

// OnTemplateDeregisteredRequest fires when a template hash is fully
// deleted (no tags, no instances).
type OnTemplateDeregisteredRequest struct {
	TemplateHash string
}

// OnInstanceCreatedRequest fires when a new instance is created from a
// template hash.
type OnInstanceCreatedRequest struct {
	InstanceID   string
	TemplateHash string
	InstanceKey  string
	Params       json.RawMessage

	// ServiceBindings carries the per-instance late-bound service catalog
	// (opaque JSON bytes). Empty when the instance has no late-bound services.
	// Consumed by the host-agent-proxy to populate its binding cache.
	ServiceBindings json.RawMessage

	// OwnerAPIKeyID is the api-key whose authenticated request created the
	// instance. Empty string for anonymous-mode-created instances. Consumed
	// by the host-agent-proxy to route dispatches to the right user's agent.
	OwnerAPIKeyID string
}

// OnInstanceTerminatedRequest fires when an instance reaches the
// terminated state (rimsky_instances.terminated_at is set).
type OnInstanceTerminatedRequest struct {
	InstanceID         string
	TemplateHash       string
	TerminatedAtUnixMs int64
}

// OnRunScopeTerminalRequest fires when a run-scope reaches terminal state.
// Fired from the rimsky-side process that owns the state transition
// (control-api for main scopes; the supervisor for sub-graph and
// fanout-partition scopes). Consumed by the host-agent-proxy to drive
// spawn reaping.
//
// RunScopeID is a string (UUID hex form) for consistency with the other
// On*Request structs in this file, which all use string UUIDs.
type OnRunScopeTerminalRequest struct {
	RunScopeID     string
	TerminalReason string
	// InstanceID is the owning instance of the terminating run-scope. The
	// host-agent-proxy keys spawned children by instance id (its v1
	// dispatch-observable scope), so it reaps on InstanceID, not RunScopeID.
	InstanceID string
}
