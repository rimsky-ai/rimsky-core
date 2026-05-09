// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
)

// InlineBackend is the degenerate BlobBackend used when Backend="inline".
// All values are written into the existing inline column on
// rimsky_node_attributes / rimsky_worker_request / rimsky_node_events
// rather than spilled.
//
// The attribute write path checks
//
//	if len(value) <= cfg.SpillThresholdBytes || backend.Name() == "inline" {
//	    write inline; never call backend.Write
//	}
//
// so InlineBackend.Write should never be reached in practice. The
// methods are implemented here defensively: Write returns an explicit
// error so a misuse fails loudly instead of silently writing nothing,
// while Read/ReadRange/Delete behave as the "no handles exist" no-ops
// they morally are.
type InlineBackend struct{}

// Compile-time interface check.
var _ BlobBackend = InlineBackend{}

// errInlineNotSpillable is returned by InlineBackend.Write to surface
// caller bugs. The attribute write path is supposed to short-circuit on
// Name()=="inline" before invoking Write; if this error fires, the
// caller's threshold check has a bug.
var errInlineNotSpillable = errors.New("blob: inline backend does not produce handles (caller's spill check should have short-circuited)")

// Write returns errInlineNotSpillable. Inline values never become
// handles; the attribute write path never reaches this method when
// Name()=="inline".
func (InlineBackend) Write(_ context.Context, _ BlobKey, _ []byte) (Handle, error) {
	return "", errInlineNotSpillable
}

// Read returns ErrBlobNotFound. There are no handles to look up under
// the inline backend.
func (InlineBackend) Read(_ context.Context, _ Handle) ([]byte, error) {
	return nil, ErrBlobNotFound
}

// ReadRange returns ErrBlobNotFound for the same reason as Read.
func (InlineBackend) ReadRange(_ context.Context, _ Handle, _, _ int64) ([]byte, error) {
	return nil, ErrBlobNotFound
}

// Delete is a no-op (idempotent: deleting an absent handle returns nil).
func (InlineBackend) Delete(_ context.Context, _ Handle) error {
	return nil
}

// Name returns "inline".
func (InlineBackend) Name() string { return "inline" }
