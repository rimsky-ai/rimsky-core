// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package observability is the read-only observability surface mounted
// on rimsky-control-api at /v1/observability/*. Exposes a curated view
// over rimsky_* tables plus a handshake-driven discovery cache for the
// per-peer observability endpoints declared in rimsky.yml.
//
// Per docs/specs/2026-05-02-dashboard-and-observability-design.md.
//
// Import rules: this package may import foundation/persistence/, foundation/locks/
// for shared types. It MUST NOT import control/config/, foundation/persistence/
// postgres/, foundation/persistence/sqlite/, graph/scheduler/, runtime/,
// or control/controlapi/. (Avoids a cycle through config.StartControlAPI,
// which composes RunHandshake + Routes itself.)
package observability

import (
	"sync"
	"time"
)

// Reachability is the result of probing a peer's observability endpoint.
type Reachability string

const (
	ReachabilityReachable   Reachability = "reachable"
	ReachabilityUnreachable Reachability = "unreachable"
	ReachabilityDegraded    Reachability = "degraded"
)

// CustomUI mirrors the proto message of the same name. The proto
// reuses one `dispatch_url_template` field name across both peer kinds
// (executors substitute against {dispatch_id, instance_id, node_type};
// stores substitute against {claim_id, producer_name}), so there's no
// separate claim_url_template — see spec §2.2 / §3.2.
type CustomUI struct {
	URL                 string `json:"ui_url"`
	EmbedMode           string `json:"embed_mode"`
	DispatchURLTemplate string `json:"dispatch_url_template,omitempty"`
}

// AdminViewParam mirrors the proto AdminViewParam.
type AdminViewParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// AdminViewDecl mirrors the proto AdminViewDecl.
type AdminViewDecl struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Params      []AdminViewParam `json:"params,omitempty"`
}

// ObservabilityCapabilities mirrors the union of executor and store
// capability messages — every field is optional and rendered as
// "supported" / "not supported" by the dashboard.
type ObservabilityCapabilities struct {
	SupportsTraceGet              bool            `json:"supports_trace_get,omitempty"`
	SupportsTraceStream           bool            `json:"supports_trace_stream,omitempty"`
	SupportsClaimGet              bool            `json:"supports_claim_get,omitempty"`
	SupportsClaimStream           bool            `json:"supports_claim_stream,omitempty"`
	SupportsListClaims            bool            `json:"supports_list_claims,omitempty"`
	RetentionAfterTerminalSeconds uint64          `json:"retention_after_terminal_seconds"`
	CustomUI                      *CustomUI       `json:"custom_ui,omitempty"`
	AdminViews                    []AdminViewDecl `json:"admin_views,omitempty"`
	// HTTPBridgeURL is the absolute base URL of the peer's HTTP+JSON
	// observability bridge. When non-empty, the dashboard uses this
	// (instead of falling back to the dispatch endpoint) for browser-
	// friendly fetch/SSE access. When empty, the peer exposes only
	// the gRPC surface and the dashboard's HTTP proxy is unavailable.
	HTTPBridgeURL string `json:"http_bridge_url,omitempty"`

	// UserdataSchema, when non-empty, is a JSON Schema (RFC 8259 +
	// draft 2020-12) advertised by an executor to describe its accepted
	// userdata shape. Rimsky validates incoming template userdata against
	// this schema at template registration and at dispatch (post-merge,
	// post-substitution). Empty means "no schema; accept any userdata."
	// Plumbed from ObservabilityCapabilities.userdata_schema (proto v1).
	UserdataSchema []byte `json:"userdata_schema,omitempty"`

	// DeclaredEvents is the set of event names this executor may emit
	// via the non-terminal NamedEvent wire type. Rimsky validates that
	// any on_event handlers in templates referencing this executor name
	// an event in declared_events. Empty means "executor does not emit
	// events." Plumbed from ObservabilityCapabilities.declared_events.
	DeclaredEvents []string `json:"declared_events,omitempty"`
}

// PeerEntry is the cached result of one observability handshake.
type PeerEntry struct {
	Name                  string `json:"name"`
	Endpoint              string `json:"endpoint"`
	ObservabilityEndpoint string `json:"observability_endpoint"`
	// HTTPBridgeURL is the dashboard-visible HTTP base URL for the
	// peer's observability bridge. Promoted out of Capabilities so the
	// dashboard's discovery cache can read it without inspecting the
	// optional Capabilities sub-tree.
	HTTPBridgeURL string                     `json:"http_bridge_url,omitempty"`
	Reachability  Reachability               `json:"reachability_status"`
	Capabilities  *ObservabilityCapabilities `json:"observability_capabilities,omitempty"`
	LastProbedAt  time.Time                  `json:"last_probed_at"`
	LastError     string                     `json:"last_error,omitempty"`
}

// Discovery is a thread-safe cache mapping peer name → observability
// capabilities + reachability, populated by the startup handshake and
// kept fresh by RefreshLoop.
type Discovery struct {
	mu        sync.RWMutex
	executors map[string]PeerEntry
	stores    map[string]PeerEntry
	prober    Prober
}

// NewDiscovery constructs an empty Discovery using prober for probes.
// Pass NewGRPCProber() for the real implementation; tests may inject a
// fake.
func NewDiscovery(prober Prober) *Discovery {
	return &Discovery{
		executors: map[string]PeerEntry{},
		stores:    map[string]PeerEntry{},
		prober:    prober,
	}
}

// SetExecutor stores or updates one executor's probe result.
func (d *Discovery) SetExecutor(entry PeerEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.executors[entry.Name] = entry
}

// SetStore stores or updates one store's probe result.
func (d *Discovery) SetStore(entry PeerEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stores[entry.Name] = entry
}

// GetExecutor returns the cached entry for an executor, if any.
func (d *Discovery) GetExecutor(name string) (PeerEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.executors[name]
	return e, ok
}

// GetStore returns the cached entry for a store, if any.
func (d *Discovery) GetStore(name string) (PeerEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.stores[name]
	return e, ok
}

// ListExecutors returns a snapshot of all cached executor entries.
func (d *Discovery) ListExecutors() []PeerEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]PeerEntry, 0, len(d.executors))
	for _, e := range d.executors {
		out = append(out, e)
	}
	return out
}

// ListStores returns a snapshot of all cached store entries.
func (d *Discovery) ListStores() []PeerEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]PeerEntry, 0, len(d.stores))
	for _, e := range d.stores {
		out = append(out, e)
	}
	return out
}
