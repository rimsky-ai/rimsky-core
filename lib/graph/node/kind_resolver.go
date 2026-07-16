// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: node
type KindAliasMap struct {
	mu      sync.RWMutex
	aliases map[string]string
}

func NewKindAliasMap() *KindAliasMap {
	return &KindAliasMap{aliases: map[string]string{}}
}

func (m *KindAliasMap) Register(kind, executorAlias string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.aliases[kind]; exists {
		return fmt.Errorf("KindAliasMap: duplicate registration for kind %q (existing alias %q)", kind, existing)
	}
	m.aliases[kind] = executorAlias
	return nil
}

func (m *KindAliasMap) Resolve(kind string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	alias, ok := m.aliases[kind]
	return alias, ok
}

// @concept: node
func CanonicalizeKindSugar(tspec *TemplateSpec, aliases *KindAliasMap) {
	if tspec == nil || aliases == nil {
		return
	}
	for i := range tspec.Nodes {
		n := &tspec.Nodes[i]
		if n.Kind == "" {
			continue
		}
		if alias, ok := aliases.Resolve(n.Kind); ok {
			n.Executor = alias
			n.Kind = ""
		}
	}
}

// @concept: message-sender-node
// @concept: node
func CanonicalizeSendMessageSugar(tspec *TemplateSpec, aliases *KindAliasMap) error {
	if tspec == nil || aliases == nil {
		return nil
	}
	alias, registered := aliases.Resolve(spec.SendMessageKindName)
	for i := range tspec.Nodes {
		n := &tspec.Nodes[i]
		if n.SendsMessage == "" {
			continue
		}
		if n.Executor != "" {
			continue
		}
		if !registered {
			return fmt.Errorf(
				"CanonicalizeSendMessageSugar: nodes[%d] (type %q) declares sends_message but builtin kind %q has no registered executor alias",
				i, n.Type, spec.SendMessageKindName)
		}
		n.Executor = alias
	}
	return nil
}

// @concept: fan-out
func CanonicalizeAggregationPolicyDefault(tspec *TemplateSpec) {
	if tspec == nil {
		return
	}
	for i := range tspec.Nodes {
		n := &tspec.Nodes[i]
		if n.FanOut == nil {
			continue
		}
		if n.FanOut.ErrorPolicy.Kind == "" {
			n.FanOut.ErrorPolicy.Kind = spec.AggregationKindStrict
		}
	}
}
