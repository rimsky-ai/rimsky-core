// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
)

// BlobBackend stores attribute values, parked-state payloads, and
// named-event payloads that exceed the inline-spill threshold.
//
// Implementations are typically out-of-process (Postgres LOBs, filesystem
// on a shared volume); the in-process "memory" backend is dev-only and
// is rejected at startup outside the single-process mode
// (RIMSKY_PROCESS_ROLE=unified; see ValidateBlobConfig).
//
// The Driver constructs exactly one BlobBackend at startup based on
// BlobConfig.Backend; multiple concurrent backends are not supported in
// v1. Future versions may relax this when a deployment legitimately
// needs to migrate between backends.
//
// @blessed-invariant 21: Blob content is inert in Rimsky. Bytes are read
// only via the persistence-layer fetch on attribute read and via walkPath
// substitution. Rimsky never logs, formats with %v, validates beyond
// schema gates, transforms, normalizes, hashes, indexes, pattern-matches,
// attaches to traces, or includes blob bytes in error messages.
type BlobBackend interface {
	// Write persists bytes and returns an opaque handle. Implementations
	// SHOULD use the BlobKey hint to namespace storage (filesystem
	// derives a path component; pg-largeobject ignores) but the handle
	// itself remains backend-opaque from the caller's perspective.
	Write(ctx context.Context, key BlobKey, bytes []byte) (Handle, error)

	// Read returns the bytes referenced by handle. Returns
	// ErrBlobNotFound when the handle is unknown.
	Read(ctx context.Context, handle Handle) ([]byte, error)

	// ReadRange returns a byte range. Backends that do not support
	// native range reads MAY fall back to full Read + slice.
	// Returns ErrBlobNotFound when the handle is unknown.
	// Returns io.ErrUnexpectedEOF if offset+length exceeds blob size.
	ReadRange(ctx context.Context, handle Handle, offset, length int64) ([]byte, error)

	// Delete removes the blob. Idempotent: deleting an absent handle
	// returns nil.
	Delete(ctx context.Context, handle Handle) error

	// Name returns the backend's identifier as documented in BlobConfig
	// ("inline" | "pg-largeobject" | "filesystem" | "memory"). Used by
	// the attribute write path to record value_handle_backend so the
	// read path can route the fetch.
	Name() string
}

// BlobKey is a write-side hint for content addressing or namespacing.
// Implementations may ignore it (memory) or use it for path derivation
// (filesystem) or future content-hash deduplication. Both fields are
// optional — empty strings are valid for callers that do not have node
// or attribute context (e.g. parked-payload writes are keyed by
// node-run id rather than node id).
type BlobKey struct {
	NodeID        string
	AttributeName string
	// Hint is a free-form discriminator the caller may use to tag the
	// blob when neither NodeID nor AttributeName apply (e.g. parked
	// payloads, named-event payloads). Backends that derive paths use
	// it; backends that ignore key data ignore it.
	Hint string
}

// Handle is a backend-opaque identifier for a stored blob. Format is
// backend-internal; callers MUST treat it as an opaque string. By
// convention each backend prefixes the handle with its name to keep
// handles self-describing across mixed-backend deployments (future
// migration support):
//
//   - inline:        "inline:<n>"   (degenerate; never produced)
//   - pg-largeobject: "pglo:<oid>"
//   - filesystem:    "fs:<relpath>"
//   - memory:        "mem:<n>"
type Handle string

// ErrBlobNotFound is returned by Read/ReadRange/Delete when the handle
// is unknown. Callers MAY use errors.Is to detect this and surface
// "missing blob" warnings; the in-band semantics (vs an infrastructural
// error) are: ErrBlobNotFound is "the blob was deleted or never written"
// — non-error for Delete, recoverable for Read/ReadRange.
var ErrBlobNotFound = errors.New("blob: handle not found")
