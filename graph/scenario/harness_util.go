// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenario

import (
	"bytes"
	"io"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/graph/node"
)

// bytesReader wraps a byte slice as an io.Reader for http.Post bodies.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// parseUUIDStr is a thin shim around uuid.Parse kept alongside the harness
// for readability in DeployTemplate / CreateInstance.
func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// ClaimRef returns a NodeStoreRef declaring a read-only claim against
// the named store with the given selector. Convenience wrapper for the
// common case in scenario tests.
func ClaimRef(storeName, selector string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: "r"}
}

// WriteClaimRef returns a NodeStoreRef declaring a read-write claim
// against the named store with the given selector.
func WriteClaimRef(storeName, selector string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: "rw"}
}

// AliasedClaimRef returns a NodeStoreRef with an explicit alias. Used
// when a node holds multiple claims against the same store (the alias
// disambiguates substitution paths).
func AliasedClaimRef(storeName, selector, intent, alias string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Selector: selector, Intent: intent, Alias: alias}
}

// MutexLock returns a NodeLockRef for a process-wide mutex (limit
// configured operator-side per spec §6.1).
func MutexLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

// CountingLock returns a NodeLockRef. Limit is operator-configured per
// spec §6.1; templates reference by name only.
func CountingLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name}
}

// Inherit returns an InheritEntry referencing an upstream-acquirer
// claim alias. Used alongside scenario.WithInherits.
func Inherit(alias string) node.InheritEntry {
	return node.InheritEntry{Claim: alias}
}
