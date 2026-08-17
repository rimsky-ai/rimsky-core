// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @concept: sub-graph
type declaredNode struct {
	def      *TemplateNodeDef
	specPath string
}

func declaredNodes(tspec *TemplateSpec) []declaredNode {
	out := make([]declaredNode, 0, len(tspec.Nodes))
	for i := range tspec.Nodes {
		out = append(out, declaredNode{
			def:      &tspec.Nodes[i],
			specPath: fmt.Sprintf("nodes[%d]", i),
		})
	}
	for g := range tspec.Graphs {
		for i := range tspec.Graphs[g].Nodes {
			out = append(out, declaredNode{
				def:      &tspec.Graphs[g].Nodes[i],
				specPath: fmt.Sprintf("graphs[%q].nodes[%d]", tspec.Graphs[g].Name, i),
			})
		}
	}
	return out
}

// @concept: node
// @decision: kind-sugar-resolver
func CanonicalizeKindSugar(tspec *TemplateSpec, aliases *KindAliasMap) {
	if tspec == nil || aliases == nil {
		return
	}
	for _, dn := range declaredNodes(tspec) {
		n := dn.def
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
// @decision: send-as-node-kind
func CanonicalizeSendMessageSugar(tspec *TemplateSpec, aliases *KindAliasMap) error {
	if tspec == nil || aliases == nil {
		return nil
	}
	alias, registered := aliases.Resolve(spec.SendMessageKindName)
	for _, dn := range declaredNodes(tspec) {
		n := dn.def
		if n.SendsMessage == "" {
			continue
		}
		if n.Executor != "" {
			continue
		}
		if !registered {
			return fmt.Errorf(
				"CanonicalizeSendMessageSugar: %s (type %q) declares sends_message but builtin kind %q has no registered executor alias",
				dn.specPath, n.Type, spec.SendMessageKindName)
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
	for _, dn := range declaredNodes(tspec) {
		n := dn.def
		if n.FanOut == nil {
			continue
		}
		if n.FanOut.ErrorPolicy.Kind == "" {
			n.FanOut.ErrorPolicy.Kind = spec.AggregationKindStrict
		}
	}
}
