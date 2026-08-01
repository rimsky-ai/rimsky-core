// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"context"
	"errors"
)

type InlineBackend struct{}

var _ BlobBackend = InlineBackend{}

var errInlineNotSpillable = errors.New("blob: inline backend does not produce handles (caller's spill check should have short-circuited)")

func (InlineBackend) Write(_ context.Context, _ BlobKey, _ []byte) (Handle, error) {
	return "", errInlineNotSpillable
}

func (InlineBackend) Read(_ context.Context, _ Handle) ([]byte, error) {
	return nil, ErrBlobNotFound
}

func (InlineBackend) ReadRange(_ context.Context, _ Handle, _, _ int64) ([]byte, error) {
	return nil, ErrBlobNotFound
}

func (InlineBackend) Delete(_ context.Context, _ Handle) error {
	return nil
}

func (InlineBackend) Name() string { return "inline" }
