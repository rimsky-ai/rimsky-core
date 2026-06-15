// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"
	"sync"
)

// KindAliasMap resolves a template node's `kind:` value to the executor
// identity registered for it. Populated at supervisor startup alongside
// the InProcessRegistry — every inproc handler with a kind sugar gets
// both a registry entry AND a kind-alias entry, so authors can write
// either `kind: loop_counter` or `executor: <its-alias>` and both
// resolve to the same handler. Unknown kinds are rejected at
// registration with the same error class as unknown executors.
//
// @concept: node
type KindAliasMap struct {
	mu      sync.RWMutex
	aliases map[string]string
}

func NewKindAliasMap() *KindAliasMap {
	return &KindAliasMap{aliases: map[string]string{}}
}

// Register adds a kind → executor-alias entry. Returns an error on
// duplicate registration so a misconfigured second seed (e.g. two
// builtin executors accidentally claiming the same kind name)
// surfaces as a startup error rather than silently overwriting the
// prior mapping. Mirrors `executor.InProcessRegistry.Register`'s
// contract — the two surfaces are seeded together with the same
// constants, so their error contracts match.
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

// CanonicalizeKindSugar walks every node in spec and, for any node that
// declares Kind != "", looks up the registered executor alias via the
// supplied map and substitutes Executor = <alias>, Kind = "". After this
// step the spec is in normal form — downstream registration code never
// needs to know about kind sugar.
//
// MUST be called only after ValidateTemplate has returned Ok() and the
// validator's validateKindDeclaration step has confirmed that every
// Kind value resolves and no node declares both Kind and Executor.
//
// Idempotent: a spec whose nodes already have empty Kind passes
// through unchanged. nil-safe on spec and aliases.
//
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
