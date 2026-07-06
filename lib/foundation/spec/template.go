// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package spec

import "encoding/json"

type TemplateSpec struct {
	Name           string            `yaml:"name" json:"name"`
	Version        string            `yaml:"version" json:"version"`
	Description    string            `yaml:"description,omitempty" json:"description,omitempty"`
	FrameTimeoutMs int64             `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
	Nodes          []TemplateNodeDef `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	Graphs         []GraphSpec       `yaml:"graphs,omitempty" json:"graphs,omitempty"`
	Publishers     []PublisherSpec   `yaml:"publishers,omitempty" json:"publishers,omitempty"`
	ParamsSchema   map[string]any    `yaml:"params_schema,omitempty" json:"params_schema,omitempty"`
	ParamsRedact   []string          `yaml:"params_redact,omitempty" json:"params_redact,omitempty"`

	LateBindServices []string `yaml:"late_bind_services,omitempty" json:"late_bind_services,omitempty"`

	// @concept: attribute
	Defaults *TemplateDefaults `yaml:"defaults,omitempty" json:"defaults,omitempty"`

	// @concept: message-schema
	Messages []MessageSchema `yaml:"messages,omitempty" json:"messages,omitempty"`

	// @concept: instance
	MessageQueueMode string `yaml:"message_queue_mode,omitempty" json:"message_queue_mode,omitempty"`
}

// @concept: message-schema
type MessageSchema struct {
	Type       string          `yaml:"type" json:"type"`
	BodySchema json.RawMessage `yaml:"body_schema,omitempty" json:"body_schema,omitempty"`
}

type TemplateDefaults struct {
	Attributes *TemplateAttributeDefaults `yaml:"attributes,omitempty" json:"attributes,omitempty"`
}

// @concept: attribute
type TemplateAttributeDefaults struct {
	ByExecutor map[string]map[string]any `yaml:"by_executor,omitempty" json:"by_executor,omitempty"`
}

// @concept: frame
const (
	FrameTimeoutDefaultMs = int64(600000)
	FrameTimeoutMinMs     = int64(60000)
)

type TemplateNodeDef struct {
	Type string `yaml:"type" json:"type"`
	// @concept: node
	Kind        string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Executor    string `yaml:"executor,omitempty" json:"executor,omitempty"`

	// @concept: node
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	ClaimProducers []NodeClaimProducerRef     `yaml:"claim_producers,omitempty" json:"claim_producers,omitempty"`
	Locks          []NodeLockRef              `yaml:"locks,omitempty" json:"locks,omitempty"`
	Attributes     *NodeAttributesDef         `yaml:"attributes,omitempty" json:"attributes,omitempty"`
	ErrorTypes     map[string]ErrorTypePolicy `yaml:"error_types,omitempty" json:"error_types,omitempty"`

	//	@concept: node-subscription
	//	@concept: cascade
	Subscribes []SubscriptionEntry `yaml:"subscribes,omitempty" json:"subscribes,omitempty"`

	MaxParkDuration string `yaml:"max_park_duration,omitempty" json:"max_park_duration,omitempty"`

	MaxRetries *int `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`

	RetryBackoff *RetryBackoffConfig `yaml:"retry_backoff,omitempty" json:"retry_backoff,omitempty"`

	// @decision: three-dispatch-deadlines
	SyncRPCDeadline string `yaml:"sync_rpc_deadline,omitempty" json:"sync_rpc_deadline,omitempty"`

	// @decision: three-dispatch-deadlines
	MaxQuietPeriod string `yaml:"max_quiet_period,omitempty" json:"max_quiet_period,omitempty"`

	// @decision: three-dispatch-deadlines
	MaxRuntime string `yaml:"max_runtime,omitempty" json:"max_runtime,omitempty"`

	Delegate string `yaml:"delegate,omitempty" json:"delegate,omitempty"`

	// @concept: message-sender-node
	SendsMessage string `yaml:"sends_message,omitempty" json:"sends_message,omitempty"`

	Holds map[string]HoldsBinding `yaml:"holds,omitempty" json:"holds,omitempty"`

	FanOut *FanOutSpec `yaml:"fan_out,omitempty" json:"fan_out,omitempty"`

	IsSubgraphEntryAbsorbed bool `yaml:"is_subgraph_entry_absorbed,omitempty" json:"is_subgraph_entry_absorbed,omitempty"`

	IsSubgraphExit bool `yaml:"is_subgraph_exit,omitempty" json:"is_subgraph_exit,omitempty"`

	// @concept: cascade
	// @decision: mode-default-most-recent
	CascadeMode string `yaml:"cascade_mode,omitempty" json:"cascade_mode,omitempty"`
}

const (
	SelfTarget = "self"
)

// @concept: claim
type NodeClaimProducerRef struct {
	Name     string          `yaml:"name" json:"name"`
	Selector string          `yaml:"selector" json:"selector"`
	Intent   string          `yaml:"intent" json:"intent"`
	Alias    string          `yaml:"alias,omitempty" json:"alias,omitempty"`
	Lifetime string          `yaml:"lifetime,omitempty" json:"lifetime,omitempty"`
	Data     json.RawMessage `yaml:"data,omitempty" json:"data,omitempty"`
}

func (s NodeClaimProducerRef) AliasOf() string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Name
}

type NodeLockRef struct {
	Name string `yaml:"name" json:"name"`
}

type NodeAttributesDef struct {
	Schema map[string]any `yaml:"schema,omitempty" json:"schema,omitempty"`
}
