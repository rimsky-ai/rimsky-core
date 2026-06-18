// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"sync"
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
func CanonicalizeKindSugar(spec *TemplateSpec, aliases *KindAliasMap) {
	if spec == nil || aliases == nil {
		return
	}
	for i := range spec.Nodes {
		n := &spec.Nodes[i]
		if n.Kind == "" {
			continue
		}
		if alias, ok := aliases.Resolve(n.Kind); ok {
			n.Executor = alias
			n.Kind = ""
		}
	}
}
