// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"errors"
)

type BlobBackend interface {
	Write(ctx context.Context, key BlobKey, bytes []byte) (Handle, error)

	Read(ctx context.Context, handle Handle) ([]byte, error)

	ReadRange(ctx context.Context, handle Handle, offset, length int64) ([]byte, error)

	Delete(ctx context.Context, handle Handle) error

	Name() string
}

type BlobKey struct {
	NodeID        string
	AttributeName string
	Hint          string
}

type Handle string

var ErrBlobNotFound = errors.New("blob: handle not found")
