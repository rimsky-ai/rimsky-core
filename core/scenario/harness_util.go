package scenario

import (
	"bytes"
	"io"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/store"
)

// bytesReader wraps a byte slice as an io.Reader for http.Post bodies.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// parseUUIDStr is a thin shim around uuid.Parse kept alongside the harness
// for readability in DeployTemplate / CreateInstance.
func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }

// ClaimRef returns a NodeStoreRef declaring claim-and-forget against the
// named store. Hold defaults to false; use ClaimAndHoldRef for held claims.
//
// Convenience wrapper so scenario tests don't have to spell out the struct
// literal for the common case.
func ClaimRef(storeName string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Claim: true}
}

// ClaimAndHoldRef returns a NodeStoreRef declaring claim-and-hold. The
// terminal node responsible for resolving the held claim must list a
// matching ClaimResolutionRef in its ClaimResolutions.
func ClaimAndHoldRef(storeName string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Claim: true, Hold: true}
}

// RegionRef returns a NodeStoreRef declaring a write-region acquisition
// against the named store. Pass distinct region tokens for distinct regions
// and equal tokens for overlapping ones (the stub-filesystem store treats
// region tokens as opaque set-equality checks).
func RegionRef(storeName string, write ...string) node.NodeStoreRef {
	return node.NodeStoreRef{Name: storeName, Write: write}
}

// MutexLock returns a NodeLockRef for a process-wide mutex (limit=1).
func MutexLock(name string) node.NodeLockRef {
	return node.NodeLockRef{Name: name, Mode: store.LockModeMutex}
}

// CountingLock returns a NodeLockRef for a counting semaphore with the given
// limit. Limit must be >= 1; the validator rejects 0.
func CountingLock(name string, limit int) node.NodeLockRef {
	return node.NodeLockRef{Name: name, Mode: store.LockModeCounting, Limit: limit}
}

// ResolveClaim returns a ClaimResolutionRef declaring this node resolves a
// held claim originally taken by `sourceNode` against `storeName`. OnCommit
// and OnGiveUp default to empty (use store defaults); pass non-empty
// strings to override the per-resolution disposition.
func ResolveClaim(sourceNode, storeName string) node.ClaimResolutionRef {
	return node.ClaimResolutionRef{Source: sourceNode, Store: storeName}
}
