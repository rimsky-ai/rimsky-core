// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"sync"
	"time"
)

type Reachability string

const (
	ReachabilityReachable   Reachability = "reachable"
	ReachabilityUnreachable Reachability = "unreachable"
)

type CustomUI struct {
	URL                 string `json:"ui_url"`
	EmbedMode           string `json:"embed_mode"`
	DispatchURLTemplate string `json:"dispatch_url_template,omitempty"`
}

type AdminViewParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

type AdminViewDecl struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Params      []AdminViewParam `json:"params,omitempty"`
}

type ObservabilityCapabilities struct {
	SupportsTraceGet              bool            `json:"supports_trace_get,omitempty"`
	SupportsTraceStream           bool            `json:"supports_trace_stream,omitempty"`
	SupportsClaimGet              bool            `json:"supports_claim_get,omitempty"`
	SupportsClaimStream           bool            `json:"supports_claim_stream,omitempty"`
	SupportsListClaims            bool            `json:"supports_list_claims,omitempty"`
	RetentionAfterTerminalSeconds uint64          `json:"retention_after_terminal_seconds"`
	CustomUI                      *CustomUI       `json:"custom_ui,omitempty"`
	AdminViews                    []AdminViewDecl `json:"admin_views,omitempty"`
	HTTPBridgeURL                 string          `json:"http_bridge_url,omitempty"`

	ExpectedAttributesSchema []byte `json:"expected_attributes_schema,omitempty"`

	DeclaredTags []string `json:"declared_tags,omitempty"`

	// @concept: signal
	DeclaredErrorClasses []string `json:"declared_error_classes,omitempty"`
}

type PeerEntry struct {
	Name                  string                     `json:"name"`
	Endpoint              string                     `json:"endpoint"`
	ObservabilityEndpoint string                     `json:"observability_endpoint"`
	HTTPBridgeURL         string                     `json:"http_bridge_url,omitempty"`
	Reachability          Reachability               `json:"reachability_status"`
	Capabilities          *ObservabilityCapabilities `json:"observability_capabilities,omitempty"`
	LastProbedAt          time.Time                  `json:"last_probed_at"`
	LastError             string                     `json:"last_error,omitempty"`
	TLS                   string                     `json:"tls,omitempty"`
	Static                bool                       `json:"static,omitempty"`
}

// @concept: discovery-cache
type Discovery struct {
	mu             sync.RWMutex
	executors      map[string]PeerEntry
	claimProducers map[string]PeerEntry
	prober         Prober
}

func NewDiscovery(prober Prober) *Discovery {
	return &Discovery{
		executors:      map[string]PeerEntry{},
		claimProducers: map[string]PeerEntry{},
		prober:         prober,
	}
}

func (d *Discovery) SetExecutor(entry PeerEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.executors[entry.Name] = entry
}

func (d *Discovery) SetClaimProducer(entry PeerEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.claimProducers[entry.Name] = entry
}

func (d *Discovery) GetExecutor(name string) (PeerEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.executors[name]
	return e, ok
}

func (d *Discovery) GetClaimProducer(name string) (PeerEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.claimProducers[name]
	return e, ok
}

func (d *Discovery) ListExecutors() []PeerEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]PeerEntry, 0, len(d.executors))
	for _, e := range d.executors {
		out = append(out, e)
	}
	return out
}

func (d *Discovery) ListClaimProducers() []PeerEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]PeerEntry, 0, len(d.claimProducers))
	for _, e := range d.claimProducers {
		out = append(out, e)
	}
	return out
}
